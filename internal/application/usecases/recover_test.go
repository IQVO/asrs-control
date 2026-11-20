package usecases_test

import (
	"testing"
	"time"

	"github.com/IQVO/asrs-control/internal/adapters/outbound/memory"
	"github.com/IQVO/asrs-control/internal/application/telegrams"
	"github.com/IQVO/asrs-control/internal/application/usecases"
	"github.com/IQVO/asrs-control/internal/domain/layout"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
	"github.com/IQVO/asrs-control/internal/domain/storagemap"
)

// restart builds a second Service over the same store, as a process restart would.
func restart(t *testing.T, h *harness, l layout.Layout) (*usecases.Service, *memory.Downlink) {
	t.Helper()
	down := memory.NewDownlink()
	svc, err := usecases.New(usecases.Deps{Layouts: []layout.Layout{l}, UoW: memory.NewUnitOfWork(h.db),
		Maps: memory.NewStorageMaps(h.db), Jobs: h.jobs, Units: memory.NewUnits(h.db), Capacity: memory.NewCapacities(h.db),
		Telegrams: memory.NewTelegrams(h.db), Events: h.events, Downlink: down, Clock: h.clock, IDs: memory.NewIDs("re-"),
		Velocity: usecases.VelocityPolicy{Window: time.Hour, FastRetrievals: 2}})
	if err != nil {
		t.Fatal(err)
	}
	return svc, down
}

func TestRecoverResendsUnacknowledgedCommandsWithTheirTelegramIDs(t *testing.T) {
	l := twoAisles()
	l.Aisles = 3
	l.Ports = append(l.Ports, layout.Port{ID: "IN3", Aisle: 3, Kind: layout.PortInput}, layout.Port{ID: "OUT3", Aisle: 3, Kind: layout.PortOutput})
	h := newHarness(t, l)
	a := h.store("A", "IN1") // aisle 1: dispatched, never acknowledged
	b := h.store("B", "IN2") // aisle 2: never acknowledged, then a cancel requested
	if _, err := h.svc.Cancel(h.ctx, "ASRS1", b.ID()); err != nil {
		t.Fatal(err)
	}
	bCancel := h.job(b.ID()).CancelTelegramID()
	c := h.store("C", "IN3") // aisle 3: acknowledged, then a cancel requested
	if err := h.svc.HandleAck(h.ctx, "ASRS1", c.TelegramID(), true, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Cancel(h.ctx, "ASRS1", c.ID()); err != nil {
		t.Fatal(err)
	}
	cCancel := h.job(c.ID()).CancelTelegramID()
	svc, down := restart(t, h, l)
	if err := svc.Recover(h.ctx, "ASRS1"); err != nil {
		t.Fatal(err)
	}
	sent := down.Sent("ASRS1")
	want := []struct {
		typ telegrams.CommandType
		tid string
	}{{telegrams.CmdStore, a.TelegramID()}, {telegrams.CmdStore, b.TelegramID()}, {telegrams.CmdCancel, bCancel}, {telegrams.CmdCancel, cCancel}, {telegrams.CmdStateRequest, ""}}
	if len(sent) != len(want) {
		t.Fatalf("sent %d commands, want %d: %+v", len(sent), len(want), sent)
	}
	for i, w := range want {
		if sent[i].Type != w.typ || (w.tid != "" && sent[i].TelegramID != w.tid) || sent[i].TelegramID == "" {
			t.Errorf("command %d = %+v, want %s with telegram id %q", i, sent[i], w.typ, w.tid)
		}
	}
}

func TestRecoverDispatchesAQueuedJobOnAnIdleAisle(t *testing.T) {
	h := newHarness(t, tinyLayout())
	maps := memory.NewStorageMaps(h.db)
	m, _ := maps.Get(h.ctx, tinyLayout(), 1)
	p, _ := m.ReserveStore("sj-x", "X", storagemap.Slow)
	_ = maps.Save(h.ctx, "ASRS1", m)
	j, _ := storagejob.NewStore(storagejob.Spec{ID: "sj-x", AsrsID: "ASRS1", Aisle: 1, ContainerID: "X", PortID: "IN1", To: p, At: t0})
	_ = h.jobs.Insert(h.ctx, j)
	svc, down := restart(t, h, tinyLayout())
	if err := svc.Recover(h.ctx, "ASRS1"); err != nil {
		t.Fatal(err)
	}
	if got := h.job("sj-x"); got.Status() != storagejob.Dispatched {
		t.Fatalf("queued job after recovery = %s", got.Status())
	}
	if last := down.Last("ASRS1"); last.Type != telegrams.CmdStore || last.JobID != "sj-x" {
		t.Fatalf("last command = %+v", last)
	}
	if err := svc.Recover(h.ctx, "NOPE"); err == nil {
		t.Fatal("Recover of an unknown AS/RS succeeded")
	}
}

func TestRecoverTwiceResendsButChangesNothingAndEmitsNoEvents(t *testing.T) {
	h := newHarness(t, twoAisles())
	a := h.store("A", "IN1")
	svc, down := restart(t, h, twoAisles())
	if err := svc.Recover(h.ctx, "ASRS1"); err != nil {
		t.Fatal(err)
	}
	events, first := len(h.events.All()), len(down.Sent("ASRS1"))
	if err := svc.Recover(h.ctx, "ASRS1"); err != nil {
		t.Fatal(err)
	}
	if got := len(down.Sent("ASRS1")); got != 2*first || first < 2 {
		t.Fatalf("sent %d then %d commands, want a full resend", first, got)
	}
	if len(h.events.All()) != events {
		t.Fatalf("the second Recover emitted events: %d -> %d", events, len(h.events.All()))
	}
	if j := h.job(a.ID()); j.Status() != storagejob.Dispatched || j.TelegramID() != a.TelegramID() {
		t.Fatalf("the second Recover changed the job: %s %s", j.Status(), j.TelegramID())
	}
}
