package usecases_test

import (
	"errors"
	"testing"

	"github.com/IQVO/asrs-control/internal/application/telegrams"
	"github.com/IQVO/asrs-control/internal/application/usecases"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
)

func TestCancellingAQueuedJobEndsItAndReleasesItsPosition(t *testing.T) {
	h := newHarness(t, tinyLayout())
	h.store("A", "IN1")
	q := h.store("B", "IN1")
	j, err := h.svc.Cancel(h.ctx, "ASRS1", q.ID())
	if err != nil || j.Status() != storagejob.Cancelled {
		t.Fatalf("Cancel = %v, %s", err, j.Status())
	}
	fs := eventsOf[storagejob.StorageJobFailed](h)
	if len(fs) != 1 || fs[0].JobID != q.ID() || fs[0].Reason != storagejob.ReasonCancelled || !fs[0].PLCTime.IsZero() {
		t.Fatalf("failures = %+v", fs)
	}
	if len(h.down.Sent("ASRS1")) != 1 {
		t.Fatal("cancelling a queued job sent a telegram")
	}
	h.store("B", "IN1") // the released position is assignable again
}

func TestCancellingADispatchedJobSendsOneCancel(t *testing.T) {
	h := newHarness(t, tinyLayout())
	s := h.store("A", "IN1")
	h.ackLast()
	j, err := h.svc.Cancel(h.ctx, "ASRS1", s.ID())
	if err != nil || j.Status() != storagejob.CancelRequested {
		t.Fatalf("Cancel = %v, %s", err, j.Status())
	}
	c := h.last()
	if c.Type != telegrams.CmdCancel || c.JobID != s.ID() || c.TelegramID != j.CancelTelegramID() {
		t.Fatalf("cancel command = %+v", c)
	}
	if _, err := h.svc.Cancel(h.ctx, "ASRS1", s.ID()); err != nil {
		t.Fatal(err)
	}
	if n := len(h.down.Sent("ASRS1")); n != 2 {
		t.Fatalf("a repeated cancellation sent another CANCEL: %d commands", n)
	}
}

func TestCancelRefusals(t *testing.T) {
	h := newHarness(t, tinyLayout())
	done := h.stored("A", "IN1")
	if _, err := h.svc.Cancel(h.ctx, "ASRS1", "sj-nope"); !errors.Is(err, usecases.ErrJobNotFound) {
		t.Fatalf("unknown job = %v", err)
	}
	if _, err := h.svc.Cancel(h.ctx, "OTHER", done.ID()); !errors.Is(err, usecases.ErrUnknownAsrs) {
		t.Fatalf("unknown asrs = %v", err)
	}
	if _, err := h.svc.Cancel(h.ctx, "ASRS1", done.ID()); !errors.Is(err, storagejob.ErrJobEnded) {
		t.Fatalf("ended job = %v", err)
	}
	r := h.retrieve("A")
	h.ackLast()
	h.done(r.ID())
	if _, err := h.svc.Cancel(h.ctx, "ASRS1", r.ID()); !errors.Is(err, storagejob.ErrNotCancellable) {
		t.Fatalf("job on its way to the port = %v", err)
	}
}

func TestPickupFreesThePortForTheNextRetrieveInOrder(t *testing.T) {
	h := newHarness(t, tinyLayout())
	h.stored("A", "IN1")
	h.stored("B", "IN1")
	r1 := h.retrieve("A")
	r2 := h.retrieve("B")
	h.ackLast()
	h.done(r1.ID())
	if h.job(r2.ID()).Status() != storagejob.Queued {
		t.Fatal("the second retrieve overtook the first one's arrival")
	}
	h.arrive("OUT1", "A")
	if h.job(r2.ID()).Status() != storagejob.Queued {
		t.Fatal("the second retrieve went before the port was cleared")
	}
	if err := h.svc.ConfirmPickup(h.ctx, "ASRS1", "OUT1", "B"); !errors.Is(err, usecases.ErrPortNotPresenting) {
		t.Fatalf("pickup of the wrong container = %v", err)
	}
	if err := h.svc.ConfirmPickup(h.ctx, "ASRS1", "OUT1", "A"); err != nil {
		t.Fatal(err)
	}
	if h.job(r2.ID()).Status() != storagejob.Dispatched {
		t.Fatal("the second retrieve was not dispatched after the pickup")
	}
	presented := eventsOf[storagejob.ContainerPresentedAtPort](h)
	if len(presented) != 1 || presented[0].ContainerID != "A" || presented[0].Scan != storagejob.ScanRead {
		t.Fatalf("presented = %+v", presented)
	}
}

func TestPickupRefusals(t *testing.T) {
	h := newHarness(t, tinyLayout())
	for _, c := range []struct {
		asrs, port, container string
		want                  error
	}{
		{"X", "OUT1", "A", usecases.ErrUnknownAsrs},
		{"ASRS1", "OUT9", "A", usecases.ErrUnknownPort},
		{"ASRS1", "IN1", "A", usecases.ErrWrongPortKind},
		{"ASRS1", "OUT1", "", usecases.ErrInvalidRequest},
		{"ASRS1", "OUT1", "A", usecases.ErrPortNotPresenting},
	} {
		if err := h.svc.ConfirmPickup(h.ctx, c.asrs, c.port, c.container); !errors.Is(err, c.want) {
			t.Errorf("ConfirmPickup(%s,%s,%q) = %v, want %v", c.asrs, c.port, c.container, err, c.want)
		}
	}
}
