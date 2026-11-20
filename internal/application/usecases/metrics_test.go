package usecases_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/IQVO/asrs-control/internal/application/telegrams"
	"github.com/IQVO/asrs-control/internal/application/usecases"
)

// ended is one JobEnded call.
type ended struct {
	kind, outcome, reason string
	took                  time.Duration
}

// fakeMetrics records what the use cases report through ports.JobMetrics.
type fakeMetrics struct {
	mu       sync.Mutex
	accepted []string
	ended    []ended
}

func (f *fakeMetrics) JobAccepted(_ context.Context, kind string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accepted = append(f.accepted, kind)
}

func (f *fakeMetrics) JobEnded(_ context.Context, kind, outcome, reason string, took time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ended = append(f.ended, ended{kind, outcome, reason, took})
}

func (f *fakeMetrics) assert(t *testing.T, accepted []string, end []ended) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if !reflect.DeepEqual(f.accepted, accepted) {
		t.Errorf("accepted = %v, want %v", f.accepted, accepted)
	}
	if !reflect.DeepEqual(f.ended, end) {
		t.Errorf("ended = %+v, want %+v", f.ended, end)
	}
}

func TestMetricsCountAnAcceptedStoreAndItsCompletionWithTheDuration(t *testing.T) {
	m := &fakeMetrics{}
	h := newHarnessWith(t, tinyLayout(), m)
	j := h.store("C1", "IN1")
	m.assert(t, []string{"STORE"}, nil)
	h.ackLast()
	h.clock.Advance(42 * time.Second)
	h.done(j.ID())
	m.assert(t, []string{"STORE"}, []ended{{"STORE", "completed", "", 42 * time.Second}})
}

func TestMetricsCountARetrieveCompletedAtItsPortArrival(t *testing.T) {
	m := &fakeMetrics{}
	h := newHarnessWith(t, tinyLayout(), m)
	h.clock.Advance(time.Second)
	h.stored("C1", "IN1")
	j := h.retrieve("C1")
	h.ackLast()
	h.clock.Advance(10 * time.Second)
	h.done(j.ID())
	m.assert(t, []string{"STORE", "RETRIEVE"}, []ended{{"STORE", "completed", "", 0}})
	h.clock.Advance(5 * time.Second)
	h.arrive(j.PortID(), "C1")
	m.assert(t, []string{"STORE", "RETRIEVE"}, []ended{{"STORE", "completed", "", 0}, {"RETRIEVE", "completed", "", 15 * time.Second}})
}

func TestMetricsCountAFailedJobByItsReason(t *testing.T) {
	m := &fakeMetrics{}
	h := newHarnessWith(t, tinyLayout(), m)
	j := h.store("C1", "IN1")
	h.ackLast()
	h.clock.Advance(3 * time.Second)
	h.up(telegrams.UpTelegram{Type: telegrams.UpJobFailed, JobID: j.ID(), Reason: "PORT_OCCUPIED"})
	m.assert(t, []string{"STORE"}, []ended{{"STORE", "failed", "PORT_OCCUPIED", 3 * time.Second}})
}

func TestMetricsCountACancelledJobAsFailedWithReasonCancelled(t *testing.T) {
	m := &fakeMetrics{}
	h := newHarnessWith(t, tinyLayout(), m)
	h.store("C1", "IN1") // in flight: the second store queues behind it
	q := h.store("C2", "IN1")
	h.clock.Advance(time.Second)
	if _, err := h.svc.Cancel(h.ctx, "ASRS1", q.ID()); err != nil {
		t.Fatal(err)
	}
	m.assert(t, []string{"STORE", "STORE"}, []ended{{"STORE", "failed", "CANCELLED", time.Second}})
}

func TestMetricsCountNothingForARefusedOrRolledBackRequest(t *testing.T) {
	m := &fakeMetrics{}
	h := newHarnessWith(t, tinyLayout(), m)
	if _, err := h.svc.RequestStore(h.ctx, usecases.StoreRequest{AsrsID: "ASRS1", ContainerID: "C1", PortID: "OUT1"}); err == nil {
		t.Fatal("a store on an OUTPUT port was accepted")
	}
	for _, c := range []string{"C1", "C2", "C3", "C4"} {
		h.stored(c, "IN1")
	}
	_, err := h.svc.RequestStore(h.ctx, usecases.StoreRequest{AsrsID: "ASRS1", ContainerID: "C5", PortID: "IN1"})
	if !errors.Is(err, usecases.ErrStorageFull) {
		t.Fatalf("fifth store: %v, want ErrStorageFull (the unit of work rolls back)", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.accepted) != 4 || len(m.ended) != 4 {
		t.Errorf("accepted %d, ended %d; want 4 and 4 (refusals count nothing)", len(m.accepted), len(m.ended))
	}
}

func TestMetricsNeverCountAnInternalReshuffle(t *testing.T) {
	m := &fakeMetrics{}
	h := newHarnessWith(t, tinyLayout(), m)
	h.stored("A", "IN1") // S2-D2
	h.stored("B", "IN1") // S1-D2
	h.stored("C", "IN1") // S2-D1 blocks A
	m.mu.Lock()
	m.accepted, m.ended = nil, nil
	m.mu.Unlock()
	r := h.retrieve("A")
	if move := h.last(); move.Type != telegrams.CmdMove {
		t.Fatalf("expected a reshuffle first, got %+v", move)
	}
	h.ackLast()
	h.done(h.last().JobID) // the reshuffle ends: not an external job
	h.ackLast()
	h.done(r.ID())
	h.arrive(r.PortID(), "A")
	m.assert(t, []string{"RETRIEVE"}, []ended{{"RETRIEVE", "completed", "", 0}})
}
