package usecases_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/IQVO/asrs-control/internal/adapters/outbound/memory"
	"github.com/IQVO/asrs-control/internal/application/telegrams"
	"github.com/IQVO/asrs-control/internal/application/usecases"
	"github.com/IQVO/asrs-control/internal/domain/layout"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
)

var t0 = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

// tinyLayout is the e2e layout: 1 aisle, 1 level, 2 slots, double-deep (4 positions).
func tinyLayout() layout.Layout {
	return layout.Layout{ID: "ASRS1", Aisles: 1, Levels: 1, Slots: 2, DoubleDeep: true, Cost: layout.Cost{PerLevel: 1, PerSlot: 1},
		Ports: []layout.Port{{ID: "IN1", Aisle: 1, Kind: layout.PortInput}, {ID: "OUT1", Aisle: 1, Kind: layout.PortOutput}}}
}

// twoAisles has IN1/OUT1 in aisle 1 and IN2/OUT2 in aisle 2.
func twoAisles() layout.Layout {
	l := tinyLayout()
	l.Aisles = 2
	l.Ports = append(l.Ports, layout.Port{ID: "IN2", Aisle: 2, Kind: layout.PortInput}, layout.Port{ID: "OUT2", Aisle: 2, Kind: layout.PortOutput})
	return l
}

type harness struct {
	t      *testing.T
	ctx    context.Context
	db     *memory.Store
	events *memory.Events
	jobs   *memory.StorageJobs
	clock  *memory.Clock
	down   *memory.Downlink
	svc    *usecases.Service
}

func newHarness(t *testing.T, l layout.Layout) *harness {
	t.Helper()
	st := memory.NewStore()
	h := &harness{t: t, ctx: context.Background(), db: st, events: memory.NewEvents(st), jobs: memory.NewStorageJobs(st),
		clock: memory.NewClock(t0), down: memory.NewDownlink()}
	svc, err := usecases.New(usecases.Deps{Layouts: []layout.Layout{l}, UoW: memory.NewUnitOfWork(st),
		Maps: memory.NewStorageMaps(st), Jobs: h.jobs, Units: memory.NewUnits(st), Capacity: memory.NewCapacities(st),
		Telegrams: memory.NewTelegrams(st), Events: h.events, Downlink: h.down, Clock: h.clock, IDs: memory.NewIDs("id-"),
		Velocity: usecases.VelocityPolicy{Window: 24 * time.Hour, FastRetrievals: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Prepare(h.ctx); err != nil {
		t.Fatal(err)
	}
	h.svc = svc
	return h
}

func (h *harness) store(container, port string) *storagejob.Job {
	h.t.Helper()
	j, err := h.svc.RequestStore(h.ctx, usecases.StoreRequest{AsrsID: "ASRS1", ContainerID: container, PortID: port})
	if err != nil {
		h.t.Fatalf("RequestStore(%s): %v", container, err)
	}
	return j
}

func (h *harness) retrieve(container string) *storagejob.Job {
	h.t.Helper()
	j, err := h.svc.RequestRetrieve(h.ctx, usecases.RetrieveRequest{AsrsID: "ASRS1", ContainerID: container})
	if err != nil {
		h.t.Fatalf("RequestRetrieve(%s): %v", container, err)
	}
	return j
}

func (h *harness) job(id string) *storagejob.Job {
	h.t.Helper()
	j, err := h.jobs.Get(h.ctx, id)
	if err != nil {
		h.t.Fatalf("job %s: %v", id, err)
	}
	return j
}

// last is the last command sent to the PLC.
func (h *harness) last() telegrams.Command { return h.down.Last("ASRS1") }

// ackLast accepts the last command.
func (h *harness) ackLast() {
	h.t.Helper()
	if err := h.svc.HandleAck(h.ctx, "ASRS1", h.last().TelegramID, true, ""); err != nil {
		h.t.Fatal(err)
	}
}

var upSeq int

// up delivers one up telegram with a fresh telegram id and returns the ACK id.
func (h *harness) up(t telegrams.UpTelegram) string {
	h.t.Helper()
	upSeq++
	if t.TelegramID == "" {
		t.TelegramID = "up-" + strconv.Itoa(upSeq)
	}
	if t.Timestamp.IsZero() {
		t.Timestamp = h.clock.Now().Add(time.Hour)
	}
	ack, err := h.svc.HandleUp(h.ctx, "ASRS1", t)
	if err != nil {
		h.t.Fatalf("HandleUp(%s): %v", t.Type, err)
	}
	return ack
}

func (h *harness) done(jobID string) {
	h.up(telegrams.UpTelegram{Type: telegrams.UpJobDone, JobID: jobID})
}

func (h *harness) arrive(port, container string) {
	h.up(telegrams.UpTelegram{Type: telegrams.UpPortArrival, PortID: port, ContainerID: container})
}

// stored runs a store to DONE through the PLC handshake.
func (h *harness) stored(container, port string) *storagejob.Job {
	h.t.Helper()
	j := h.store(container, port)
	h.ackLast()
	h.done(j.ID())
	return h.job(j.ID())
}

// retrieved runs a retrieve to DONE (arrival read correctly) and confirms the pickup.
func (h *harness) retrieved(container string) *storagejob.Job {
	h.t.Helper()
	j := h.retrieve(container)
	for h.last().Type == telegrams.CmdMove { // reshuffles first
		h.ackLast()
		h.done(h.last().JobID)
	}
	h.ackLast()
	h.done(j.ID())
	h.arrive(j.PortID(), container)
	if err := h.svc.ConfirmPickup(h.ctx, "ASRS1", j.PortID(), container); err != nil {
		h.t.Fatal(err)
	}
	return h.job(j.ID())
}

func eventsOf[T any](h *harness) []T {
	var out []T
	for _, e := range h.events.All() {
		if v, ok := e.(T); ok {
			out = append(out, v)
		}
	}
	return out
}
