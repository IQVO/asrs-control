//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/IQVO/asrs-control/internal/testing/kafkatest"
	"github.com/IQVO/asrs-control/internal/testing/pgtest"
)

const world = `robots: []
asrs:
  - id: ASRS1
    aisles: 1
    levels: 1
    slots: 2
    doubleDeep: true
    baseSeconds: 1
    perLevelSeconds: 0.2
    perSlotSeconds: 0.2
    ports:
      - {id: IN1, aisle: 1, kind: INPUT, nodeId: ASRS1-IN1}
      - {id: OUT1, aisle: 1, kind: OUTPUT, nodeId: ASRS1-OUT1}
`

type e2e struct {
	t        *testing.T
	api, emu string
	n        int
}

// stack starts Mosquitto and the emulator on one network and asrs-control in-process.
func stack(t *testing.T) *e2e {
	t.Helper()
	ctx := context.Background()
	image := os.Getenv("EQUIPMENT_EMULATOR_IMAGE")
	if image == "" {
		image = "equipment-emulator:e2e"
	}
	nw, err := network.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = nw.Remove(ctx) })
	mq, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{Started: true, ContainerRequest: testcontainers.ContainerRequest{
		Image: "eclipse-mosquitto:2.0.20", ExposedPorts: []string{"1883/tcp"}, Cmd: []string{"mosquitto", "-c", "/mosquitto-no-auth.conf"},
		Networks: []string{nw.Name}, NetworkAliases: map[string][]string{nw.Name: {"mosquitto"}}, WaitingFor: wait.ForListeningPort("1883/tcp")}})
	if err != nil {
		t.Fatalf("start mosquitto: %v", err)
	}
	t.Cleanup(func() { _ = mq.Terminate(ctx) })
	emu, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{Started: true, ContainerRequest: testcontainers.ContainerRequest{
		Image: image, ExposedPorts: []string{"8080/tcp"}, Networks: []string{nw.Name},
		Env:        map[string]string{"MQTT_URL": "tcp://mosquitto:1883", "SIM_SPEED": "10", "WORLD_FILE": "/tmp/world.yaml"},
		Files:      []testcontainers.ContainerFile{{Reader: strings.NewReader(world), ContainerFilePath: "/tmp/world.yaml", FileMode: 0o644}},
		WaitingFor: wait.ForHTTP("/readyz").WithPort("8080/tcp").WithStartupTimeout(60 * time.Second)}})
	if err != nil {
		t.Fatalf("start the emulator image %q: %v. Build it first: docker build -t equipment-emulator:e2e <equipment-emulator checkout at fa60024>", image, err)
	}
	t.Cleanup(func() { _ = emu.Terminate(ctx) })
	mqURL, err := mq.PortEndpoint(ctx, "1883/tcp", "tcp")
	if err != nil {
		t.Fatalf("mosquitto endpoint: %v", err)
	}
	emuURL, err := emu.PortEndpoint(ctx, "8080/tcp", "http")
	if err != nil {
		t.Fatalf("emulator endpoint: %v", err)
	}
	return &e2e{t: t, api: startAsrsControl(t, mqURL), emu: emuURL}
}

func startAsrsControl(t *testing.T, mqURL string) string {
	t.Helper()
	layoutFile := filepath.Join(t.TempDir(), "layout.yaml")
	if err := os.WriteFile(layoutFile, []byte(strings.Replace(world, "robots: []\n", "", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	db := pgtest.URL(t)
	// The broker is booted once per test binary by kafkatest, backed by
	// github.com/testcontainers/testcontainers-go/modules/kafka — this test
	// never reads a broker address from the environment, it wires the one it
	// started into the real app config (TestKafkaIntegrationTestsUseTestcontainers).
	brokers := kafkatest.Brokers(t)
	// The fleet convention (outbound-shipping's kafkatest): auto-creation is OFF and the
	// topics exist, one partition each with a leader, BEFORE the app's relay first writes.
	kafkatest.CreateTopic(t, "warehouse.asrs-control.events")
	kafkatest.CreateTopic(t, "warehouse.asrs-control.analytics")
	vars := map[string]string{"KAFKA_BROKERS": strings.Join(brokers, ","), "MQTT_URL": mqURL, "ASRS_LAYOUT_FILE": layoutFile,
		"OUTBOX_INTERVAL_MS": "100"}
	cfg, err := loadConfig(func(k string) string { return vars[k] })
	if err == nil {
		t.Fatal("loadConfig accepted a configuration without a database")
	}
	cfg.DatabaseURL, cfg.MigrationsURL = db, db
	ctx, cancel := context.WithCancel(context.Background())
	a, err := wire(ctx, cfg, slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.run(ctx, ln) }()
	t.Cleanup(func() { cancel(); <-done; a.close() })
	return "http://" + ln.Addr().String()
}

func (e *e2e) call(method, url, body, key string) (int, map[string]any, http.Header) {
	e.t.Helper()
	req, _ := http.NewRequest(method, url, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	if method == http.MethodPost && strings.HasPrefix(url, e.api) {
		if key == "" {
			e.n++
			key = "e2e-" + strconv.Itoa(e.n)
		}
		req.Header.Set("Idempotency-Key", key)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return res.StatusCode, m, res.Header
}

// waitJob polls the job until it reaches status (or fails the test).
func (e *e2e) waitJob(id, status string) map[string]any {
	e.t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	var last map[string]any
	for time.Now().Before(deadline) {
		_, last, _ = e.call(http.MethodGet, e.api+"/asrs/ASRS1/storage-jobs/"+id, "", "")
		if last["status"] == status {
			return last
		}
		time.Sleep(200 * time.Millisecond)
	}
	e.t.Fatalf("job %s never reached %s; last %v", id, status, last)
	return nil
}

func (e *e2e) store(c string) string {
	e.t.Helper()
	if code, m, _ := e.call(http.MethodPost, e.emu+"/asrs/ASRS1/ports/IN1/container", `{"containerId":"`+c+`"}`, ""); code != http.StatusNoContent {
		e.t.Fatalf("emulator refused %s at IN1: %d %v", c, code, m)
	}
	code, m, _ := e.call(http.MethodPost, e.api+"/asrs/ASRS1/storage-jobs", `{"kind":"STORE","container_id":"`+c+`","port_id":"IN1"}`, "")
	if code != http.StatusAccepted {
		e.t.Fatalf("store %s = %d %v", c, code, m)
	}
	id := m["storage_job_id"].(string)
	e.waitJob(id, "DONE")
	return id
}

func (e *e2e) emulatorView() map[string]any {
	_, v, _ := e.call(http.MethodGet, e.emu+"/asrs/ASRS1", "", "")
	return v
}

func TestStoreReshuffleRetrieveAndFaultsAgainstTheEmulator(t *testing.T) {
	e := stack(t)
	e.store("A")
	e.store("B")
	e.store("C")
	slots := e.emulatorView()["slots"].(map[string]any)
	if slots["A1-L1-S2-D2"] != "A" || slots["A1-L1-S1-D2"] != "B" || slots["A1-L1-S2-D1"] != "C" {
		t.Fatalf("emulator rack = %v, want A S2-D2, B S1-D2, C S2-D1", slots)
	}

	// The replay of the first store answers from the store, it does not run again.
	if code, _, h := e.call(http.MethodPost, e.api+"/asrs/ASRS1/storage-jobs", `{"kind":"STORE","container_id":"A","port_id":"IN1"}`, "e2e-1"); code != http.StatusAccepted || h.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay = %d %q", code, h.Get("Idempotency-Replayed"))
	}

	// Retrieving A moves C first (invisible), then presents A at OUT1.
	code, m, _ := e.call(http.MethodPost, e.api+"/asrs/ASRS1/storage-jobs", `{"kind":"RETRIEVE","container_id":"A","reference":"leg-1"}`, "")
	if code != http.StatusAccepted || m["port_id"] != "OUT1" {
		t.Fatalf("retrieve A = %d %v", code, m)
	}
	retrieveA := m["storage_job_id"].(string)
	e.waitJob(retrieveA, "DONE")
	v := e.emulatorView()
	if v["ports"].(map[string]any)["OUT1"] != "A" || v["slots"].(map[string]any)["A1-L1-S1-D1"] != "C" {
		t.Fatalf("emulator after the retrieve = %v, want A on OUT1 and C moved to S1-D1", v)
	}

	// material-flow-control confirms the pickup in asrs-control; physically A is still on OUT1, so the next
	// retrieve's PLC precheck fails with PORT_OCCUPIED, which ends the job FAILED with that reason.
	if code, m, _ := e.call(http.MethodPost, e.api+"/asrs/ASRS1/ports/OUT1/pickups", `{"container_id":"A"}`, ""); code != http.StatusOK {
		t.Fatalf("pickup = %d %v", code, m)
	}
	_, m, _ = e.call(http.MethodPost, e.api+"/asrs/ASRS1/storage-jobs", `{"kind":"RETRIEVE","container_id":"B"}`, "")
	failed := e.waitJob(m["storage_job_id"].(string), "FAILED")
	if failed["reason"] != "PORT_OCCUPIED" || failed["plc_reason"] != "PORT_OCCUPIED" {
		t.Fatalf("retrieve B = %v, want FAILED PORT_OCCUPIED", failed)
	}

	// A held unit refuses new work at once.
	if code, m, _ := e.call(http.MethodPost, e.emu+"/faults", `{"kind":"ASRS_UNIT","target":"ASRS1","aisle":1,"active":true}`, ""); code != http.StatusNoContent {
		t.Fatalf("fault = %d %v", code, m)
	}
	deadline := time.Now().Add(20 * time.Second)
	var lastState any
	for time.Now().Before(deadline) {
		_, view, _ := e.call(http.MethodGet, e.api+"/asrs/ASRS1", "", "")
		lastState = view["aisles"].([]any)[0].(map[string]any)["state"]
		if lastState == "HELD" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if lastState != "HELD" {
		t.Fatalf("aisle 1 never reached HELD within 20s; last state = %v", lastState)
	}
	if code, m, _ := e.call(http.MethodPost, e.api+"/asrs/ASRS1/storage-jobs", `{"kind":"RETRIEVE","container_id":"C"}`, ""); code != http.StatusUnprocessableEntity || m["code"] != "aisle-unavailable" {
		t.Fatalf("retrieve on a held aisle = %d %v", code, m)
	}

	assertEvents(t, retrieveA)
}

// assertEvents reads the integration topic: three stores, A presented and read, B failed, a HELD state,
// capacity facts, every message a CloudEvent with the content-type header, and nothing about the reshuffle.
func assertEvents(t *testing.T, retrieveA string) {
	t.Helper()
	held := func(ms []kafkatest.Message) bool {
		for _, m := range ms {
			if strings.Contains(string(m.Value), `"state":"HELD"`) {
				return true
			}
		}
		return false
	}
	msgs := kafkatest.ReadAll(t, "warehouse.asrs-control.events", held, 60*time.Second)
	count := map[string]int{}
	for _, m := range msgs {
		if m.Headers["content-type"] != "application/cloudevents+json; charset=UTF-8" {
			t.Fatalf("message without the CloudEvents content-type: %v", m.Headers)
		}
		var ce struct {
			Type, Subject, Source string
			Data                  map[string]any
		}
		if err := json.Unmarshal(m.Value, &ce); err != nil || ce.Source != "/warehouse/asrs-control" || m.Key != ce.Subject {
			t.Fatalf("not a CloudEvent of asrs-control: %s (%v)", m.Value, err)
		}
		name := ce.Type[strings.LastIndex(ce.Type, ".")+1:]
		count[name]++
		if name == "ContainerPresentedAtPort" && (ce.Subject != retrieveA || ce.Data["scan_result"] != "READ" || ce.Data["reference"] != "leg-1") {
			t.Fatalf("presentation = %v", ce)
		}
		if name == "ContainerStored" && ce.Data["plc_timestamp"] == nil {
			t.Fatalf("a PLC-driven fact without plc_timestamp: %v", ce)
		}
		for _, leak := range []string{`"level"`, `"slot"`, `"depth"`, "RESHUFFLE"} {
			if strings.Contains(string(m.Value), leak) {
				t.Fatalf("an event leaks %s: %s", leak, m.Value)
			}
		}
	}
	if count["ContainerStored"] != 3 || count["ContainerPresentedAtPort"] != 1 || count["StorageJobFailed"] != 1 ||
		count["EquipmentStateChanged"] < 1 || count["AsrsCapacityChanged"] < 1 {
		t.Fatalf("event counts = %v", count)
	}
	fmt.Fprintf(os.Stderr, "e2e events: %v\n", count)
}
