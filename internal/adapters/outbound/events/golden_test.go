package events_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/IQVO/asrs-control/internal/adapters/outbound/events"
	"github.com/IQVO/asrs-control/internal/application/outbox"
	"github.com/IQVO/asrs-control/internal/domain/capacity"
	"github.com/IQVO/asrs-control/internal/domain/equipment"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
)

type recorder struct{ msgs []outbox.Message }

func (r *recorder) Insert(_ context.Context, msgs ...outbox.Message) error {
	r.msgs = append(r.msgs, msgs...)
	return nil
}

var (
	at  = time.Date(2026, 11, 20, 8, 0, 1, 0, time.UTC)
	plc = time.Date(2026, 11, 20, 9, 30, 0, 500_000_000, time.UTC)
)

func encode(t *testing.T, e any) []outbox.Message {
	t.Helper()
	rec := &recorder{}
	if err := events.NewSink(rec, func() string { return "11111111-2222-4333-8444-555555555555" }).Append(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if len(rec.msgs) != 2 {
		t.Fatalf("%T: %d messages, want 2 (events + analytics)", e, len(rec.msgs))
	}
	return rec.msgs
}

func asMap(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func check(t *testing.T, e any, typ, subject, name, data string) {
	t.Helper()
	msgs := encode(t, e)
	for i, stream := range []string{"events", "analytics"} {
		m := msgs[i]
		topic := "warehouse.asrs-control." + stream
		schema := "urn:warehouse:asrs-control:" + stream + ":" + name + ":v1"
		if m.Topic != topic || m.EventType != typ || m.Subject != subject || string(m.Key) != subject || m.DataSchema != schema ||
			m.EventID != "11111111-2222-4333-8444-555555555555" {
			t.Fatalf("%s row = %+v", stream, m)
		}
		if len(m.Headers) != 1 || m.Headers[0].Key != "content-type" || m.Headers[0].Value != "application/cloudevents+json; charset=UTF-8" {
			t.Fatalf("%s headers = %+v", stream, m.Headers)
		}
		want := asMap(t, []byte(`{"specversion":"1.0","id":"11111111-2222-4333-8444-555555555555","source":"/warehouse/asrs-control",
			"type":"`+typ+`","subject":"`+subject+`","time":"2026-11-20T08:00:01Z","datacontenttype":"application/json",
			"dataschema":"`+schema+`","data":`+data+`}`))
		if got := asMap(t, m.Value); !reflect.DeepEqual(got, want) {
			gb, _ := json.Marshal(got)
			wb, _ := json.Marshal(want)
			t.Fatalf("%s %s:\n got %s\nwant %s", stream, name, gb, wb)
		}
	}
}

func TestContainerStoredGolden(t *testing.T) {
	check(t, storagejob.ContainerStored{JobID: "sj-1", AsrsID: "ASRS1", ContainerID: "C1", PortID: "IN1", Reference: "leg-9", At: at, PLCTime: plc},
		"com.warehouse.wes.asrs-control.storagejob.ContainerStored", "sj-1", "ContainerStored",
		`{"storage_job_id":"sj-1","asrs_id":"ASRS1","container_id":"C1","port_id":"IN1","reference":"leg-9","stored_at":"2026-11-20T08:00:01Z","plc_timestamp":"2026-11-20T09:30:00.5Z"}`)
}

func TestContainerPresentedAtPortGolden(t *testing.T) {
	check(t, storagejob.ContainerPresentedAtPort{JobID: "sj-2", AsrsID: "ASRS1", ContainerID: "C1", PortID: "OUT1", Scan: storagejob.ScanNoRead, At: at, PLCTime: plc},
		"com.warehouse.wes.asrs-control.storagejob.ContainerPresentedAtPort", "sj-2", "ContainerPresentedAtPort",
		`{"storage_job_id":"sj-2","asrs_id":"ASRS1","container_id":"C1","port_id":"OUT1","scan_result":"NO_READ","presented_at":"2026-11-20T08:00:01Z","plc_timestamp":"2026-11-20T09:30:00.5Z"}`)
}

func TestStorageJobFailedGoldenWithoutAPLCTime(t *testing.T) {
	check(t, storagejob.StorageJobFailed{JobID: "sj-3", AsrsID: "ASRS1", Kind: storagejob.Retrieve, ContainerID: "C1", PortID: "OUT1", Reason: storagejob.ReasonCancelled, At: at},
		"com.warehouse.wes.asrs-control.storagejob.StorageJobFailed", "sj-3", "StorageJobFailed",
		`{"storage_job_id":"sj-3","asrs_id":"ASRS1","job_kind":"RETRIEVE","container_id":"C1","port_id":"OUT1","reason":"CANCELLED","failed_at":"2026-11-20T08:00:01Z"}`)
}

func TestStorageJobFailedGoldenFromThePLC(t *testing.T) {
	check(t, storagejob.StorageJobFailed{JobID: "sj-4", AsrsID: "ASRS1", Kind: storagejob.Store, ContainerID: "C1", PortID: "IN1", Reason: storagejob.ReasonContainerNotAtPort,
		PLCReason: "CONTAINER_NOT_AT_PORT", At: at, PLCTime: plc},
		"com.warehouse.wes.asrs-control.storagejob.StorageJobFailed", "sj-4", "StorageJobFailed",
		`{"storage_job_id":"sj-4","asrs_id":"ASRS1","job_kind":"STORE","container_id":"C1","port_id":"IN1","reason":"CONTAINER_NOT_AT_PORT","plc_reason":"CONTAINER_NOT_AT_PORT","failed_at":"2026-11-20T08:00:01Z","plc_timestamp":"2026-11-20T09:30:00.5Z"}`)
}

func TestEquipmentStateChangedGolden(t *testing.T) {
	check(t, equipment.EquipmentStateChanged{AsrsID: "ASRS1", UnitID: "A1", Aisle: 1, State: equipment.Held, Previous: equipment.Execute, At: at, PLCTime: plc},
		"com.warehouse.wes.asrs-control.equipmentunit.EquipmentStateChanged", "ASRS1/A1", "EquipmentStateChanged",
		`{"asrs_id":"ASRS1","unit_id":"A1","aisle":1,"state":"HELD","previous_state":"EXECUTE","changed_at":"2026-11-20T08:00:01Z","plc_timestamp":"2026-11-20T09:30:00.5Z"}`)
}

func TestAsrsCapacityChangedGolden(t *testing.T) {
	check(t, capacity.AsrsCapacityChanged{Snapshot: capacity.Snapshot{AsrsID: "ASRS1", UnitsTotal: 2, UnitsAvailable: 1, PositionsTotal: 1280, PositionsFree: 1200, OpenJobs: 3}, At: at},
		"com.warehouse.wes.asrs-control.asrs.AsrsCapacityChanged", "ASRS1", "AsrsCapacityChanged",
		`{"asrs_id":"ASRS1","units_total":2,"units_available":1,"positions_total":1280,"positions_free":1200,"open_jobs":3,"measured_at":"2026-11-20T08:00:01Z"}`)
}

func TestAnUnknownEventIsAnErrorAndNothingIsWritten(t *testing.T) {
	rec := &recorder{}
	err := events.NewSink(rec, func() string { return "x" }).Append(context.Background(), storagejob.ContainerStored{JobID: "sj", AsrsID: "A", At: at}, "not an event")
	if err == nil || len(rec.msgs) != 0 {
		t.Fatalf("Append = %v with %d messages, want an error and nothing written", err, len(rec.msgs))
	}
	if err := events.NewSink(rec, func() string { return "x" }).Append(context.Background()); err != nil || len(rec.msgs) != 0 {
		t.Fatal("an empty Append wrote something")
	}
}
