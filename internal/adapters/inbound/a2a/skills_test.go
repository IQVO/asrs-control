package a2a_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"

	inbounda2a "github.com/IQVO/asrs-control/internal/adapters/inbound/a2a"
	"github.com/IQVO/asrs-control/internal/adapters/outbound/memory"
	"github.com/IQVO/asrs-control/internal/application/usecases"
	"github.com/IQVO/asrs-control/internal/domain/layout"
)

// rig drives the real JSON-RPC wire over httptest: card -> client -> executor -> the real read
// use cases over in-memory repositories (seeded through the write use cases, as REST would).
type rig struct {
	t      *testing.T
	svc    *usecases.Service
	client *a2aclient.Client
}

func newRig(t *testing.T) *rig {
	t.Helper()
	st := memory.NewStore()
	l := layout.Layout{ID: "ASRS1", Aisles: 2, Levels: 1, Slots: 2, DoubleDeep: true, Cost: layout.Cost{PerLevel: 1, PerSlot: 1},
		Ports: []layout.Port{{ID: "IN1", Aisle: 1, Kind: layout.PortInput}, {ID: "OUT1", Aisle: 1, Kind: layout.PortOutput},
			{ID: "IN2", Aisle: 2, Kind: layout.PortInput}, {ID: "OUT2", Aisle: 2, Kind: layout.PortOutput}}}
	svc, err := usecases.New(usecases.Deps{Layouts: []layout.Layout{l}, UoW: memory.NewUnitOfWork(st), Maps: memory.NewStorageMaps(st),
		Jobs: memory.NewStorageJobs(st), Units: memory.NewUnits(st), Capacity: memory.NewCapacities(st), Telegrams: memory.NewTelegrams(st),
		Events: memory.NewEvents(st), Downlink: memory.NewDownlink(), Clock: memory.NewClock(time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)),
		IDs: memory.NewIDs("id-"), Velocity: usecases.VelocityPolicy{Window: time.Hour, FastRetrievals: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(inbounda2a.Handler(inbounda2a.NewExecutor(inbounda2a.Deps{Reads: svc})))
	t.Cleanup(srv.Close)
	client, err := a2aclient.NewFromCard(context.Background(), inbounda2a.Card(inbounda2a.Config{AgentURL: srv.URL, Version: "test"}))
	if err != nil {
		t.Fatal(err)
	}
	return &rig{t: t, svc: svc, client: client}
}

// send delivers part and returns the envelope JSON and the text summary of the one reply.
func (r *rig) send(part *a2a.Part) (map[string]any, string) {
	r.t.Helper()
	resp, err := r.client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, part)})
	if err != nil {
		r.t.Fatalf("SendMessage: %v", err)
	}
	msg, ok := resp.(*a2a.Message)
	if !ok || len(msg.Parts) != 2 {
		r.t.Fatalf("reply = %T %+v, want one message with a data and a text part", resp, resp)
	}
	raw, _ := json.Marshal(msg.Parts[0].Content.(a2a.Data).Value)
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		r.t.Fatal(err)
	}
	return env, string(msg.Parts[1].Content.(a2a.Text))
}

func skill(name string, args map[string]any) *a2a.Part {
	return a2a.NewDataPart(map[string]any{"skill": name, "arguments": args})
}

func problemType(env map[string]any) string {
	p, _ := env["problem"].(map[string]any)
	s, _ := p["type"].(string)
	return s
}

func TestJobStatusAnswersTheRecordedJob(t *testing.T) {
	r := newRig(t)
	j, err := r.svc.RequestStore(context.Background(), usecases.StoreRequest{AsrsID: "ASRS1", ContainerID: "C1", PortID: "IN1"})
	if err != nil {
		t.Fatal(err)
	}
	env, text := r.send(skill("job_status", map[string]any{"asrsId": "ASRS1", "storageJobId": j.ID()}))
	data, _ := env["data"].(map[string]any)
	job, _ := data["storageJob"].(map[string]any)
	if env["skill"] != "job_status" || job["storageJobId"] != j.ID() || job["status"] != "DISPATCHED" || job["kind"] != "STORE" {
		t.Fatalf("envelope = %v", env)
	}
	if !strings.Contains(text, j.ID()) || !strings.Contains(text, "DISPATCHED") {
		t.Fatalf("summary = %q", text)
	}
}

func TestAsrsCapacityAnswersTheSnapshot(t *testing.T) {
	r := newRig(t)
	env, text := r.send(skill("asrs_capacity", map[string]any{"asrsId": "ASRS1"}))
	data, _ := env["data"].(map[string]any)
	c, _ := data["capacity"].(map[string]any)
	if c["asrsId"] != "ASRS1" || c["unitsTotal"] != float64(2) || c["positionsTotal"] != float64(8) || c["openJobs"] != float64(0) {
		t.Fatalf("capacity = %v", env)
	}
	if !strings.Contains(text, "8/8 positions free") {
		t.Fatalf("summary = %q", text)
	}
}

func TestTextPartsCarryTheSameRequest(t *testing.T) {
	env, _ := newRig(t).send(a2a.NewTextPart(`{"skill":"asrs_capacity","arguments":{"asrsId":"ASRS1"}}`))
	if env["data"] == nil {
		t.Fatalf("a text part holding the skill JSON must be answered: %v", env)
	}
}

func TestProblemsAreAnsweredInBand(t *testing.T) {
	r := newRig(t)
	cases := map[string]struct {
		part *a2a.Part
		want string
	}{
		"unknown AS/RS":    {skill("asrs_capacity", map[string]any{"asrsId": "NOPE"}), "asrs-not-found"},
		"unknown job":      {skill("job_status", map[string]any{"asrsId": "ASRS1", "storageJobId": "sj-none"}), "storage-job-not-found"},
		"missing argument": {skill("job_status", map[string]any{"asrsId": "ASRS1"}), "invalid-argument"},
		"unknown argument": {skill("asrs_capacity", map[string]any{"asrsId": "ASRS1", "aisle": 1}), "invalid-argument"},
		"unknown skill":    {skill("cancel_storage_job", map[string]any{}), "unknown-skill"},
		"no skill":         {a2a.NewDataPart(map[string]any{"arguments": map[string]any{}}), "malformed-skill-request"},
		"not JSON":         {a2a.NewTextPart("where is my container?"), "malformed-skill-request"},
	}
	for name, c := range cases {
		if env, _ := r.send(c.part); problemType(env) != c.want || env["data"] != nil {
			t.Errorf("%s: envelope %v, want only the %s problem", name, env, c.want)
		}
	}
}

func TestTheCardOffersTwoReadSkillsAtL1AndNoSecurity(t *testing.T) {
	card := inbounda2a.Card(inbounda2a.Config{AgentURL: "http://asrs-control-a2a:8093/", Version: "v1"})
	if card.Name != "asrs-control" || len(card.Skills) != 2 || card.SecuritySchemes != nil || card.Capabilities.Streaming ||
		card.SupportedInterfaces[0].URL != "http://asrs-control-a2a:8093/" {
		t.Fatalf("card = %+v", card)
	}
	for _, s := range card.Skills {
		tags := strings.Join(s.Tags, ",")
		if !strings.Contains(tags, "authority:L1") || !strings.Contains(tags, "read") || strings.Contains(tags, "write") {
			t.Errorf("skill %s tags %v, want read and authority:L1", s.ID, s.Tags)
		}
	}
}
