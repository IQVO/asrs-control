package storagejob_test

import (
	"errors"
	"testing"
	"time"

	"github.com/IQVO/asrs-control/internal/domain/storagejob"
)

var t1 = t0.Add(time.Minute)
var plc = t0.Add(10 * time.Minute) // simulated time runs ahead at SIM_SPEED > 1

func dispatched(t *testing.T, j *storagejob.Job) *storagejob.Job {
	t.Helper()
	if j.Kind() == storagejob.Retrieve {
		if err := j.Locate(at(1, 2, 2)); err != nil {
			t.Fatal(err)
		}
	}
	if err := j.Dispatch("tg-" + j.ID()); err != nil {
		t.Fatal(err)
	}
	return j
}

func TestDispatchNeedsAQueuedJobATelegramAndARetrieveSource(t *testing.T) {
	r := mustRetrieve(t)
	if err := r.Dispatch("tg"); !errors.Is(err, storagejob.ErrInvalidTransition) {
		t.Fatalf("dispatch of an unlocated retrieve = %v", err)
	}
	s := mustStore(t)
	if err := s.Dispatch(""); !errors.Is(err, storagejob.ErrInvalidTransition) {
		t.Fatalf("dispatch without telegram = %v", err)
	}
	if err := s.Locate(at(1, 1, 1)); !errors.Is(err, storagejob.ErrInvalidTransition) {
		t.Fatalf("Locate on a store = %v", err)
	}
	dispatched(t, s)
	if s.Status() != storagejob.Dispatched || s.TelegramID() != "tg-sj-1" || !s.InFlight() {
		t.Fatalf("store = %+v", s.State())
	}
	if err := s.Dispatch("tg2"); !errors.Is(err, storagejob.ErrInvalidTransition) {
		t.Fatalf("second dispatch = %v", err)
	}
	r = dispatched(t, mustRetrieve(t))
	if r.From() != at(1, 2, 2) {
		t.Fatalf("retrieve source = %s", r.From())
	}
	if err := r.Locate(at(1, 1, 1)); !errors.Is(err, storagejob.ErrInvalidTransition) {
		t.Fatalf("Locate after dispatch = %v", err)
	}
}

func TestAcknowledgeIsIdempotentAndKeepsACancelRequest(t *testing.T) {
	s := dispatched(t, mustStore(t))
	if err := s.Acknowledge(); err != nil || s.Status() != storagejob.Acknowledged {
		t.Fatalf("Acknowledge = %v, %s", err, s.Status())
	}
	if err := s.Acknowledge(); err != nil {
		t.Fatalf("second Acknowledge = %v", err)
	}
	if _, _, err := s.RequestCancel("tc", t1); err != nil {
		t.Fatal(err)
	}
	if err := s.Acknowledge(); err != nil || s.Status() != storagejob.CancelRequested || !s.Acked() {
		t.Fatalf("Acknowledge during a cancel = %v, %s acked=%v", err, s.Status(), s.Acked())
	}
	u := dispatched(t, mustRetrieve(t))
	_, _, _ = u.RequestCancel("tc", t1)
	if err := u.Acknowledge(); err != nil || u.Status() != storagejob.CancelRequested || !u.Acked() {
		t.Fatalf("the command's ACK arriving during a cancel = %v %s acked=%v", err, u.Status(), u.Acked())
	}
	if err := mustStore(t).Acknowledge(); !errors.Is(err, storagejob.ErrInvalidTransition) {
		t.Fatalf("Acknowledge of a queued job = %v", err)
	}
}

func TestCompleteMotionOfAStoreRaisesContainerStored(t *testing.T) {
	s := dispatched(t, mustStore(t))
	evs, err := s.CompleteMotion(t1, plc)
	if err != nil {
		t.Fatal(err)
	}
	want := storagejob.ContainerStored{JobID: "sj-1", AsrsID: "ASRS1", ContainerID: "C1", PortID: "IN1", Reference: "leg-9", At: t1, PLCTime: plc, AcceptedAt: t0}
	if len(evs) != 1 || evs[0] != want {
		t.Fatalf("events = %+v, want %+v", evs, want)
	}
	if s.Status() != storagejob.Done || s.Open() || !s.EndedAt().Equal(t1) {
		t.Fatalf("store = %+v", s.State())
	}
	if _, err := s.CompleteMotion(t1, plc); !errors.Is(err, storagejob.ErrInvalidTransition) {
		t.Fatalf("a second JOB_DONE = %v", err)
	}
}

func TestCompleteMotionOfAReshuffleIsSilent(t *testing.T) {
	m := dispatched(t, mustReshuffle(t))
	evs, err := m.CompleteMotion(t1, plc)
	if err != nil || len(evs) != 0 || m.Status() != storagejob.Done {
		t.Fatalf("reshuffle done = %+v, %v, %s", evs, err, m.Status())
	}
}

func TestARetrieveWaitsForItsArrivalAndScansIt(t *testing.T) {
	for _, c := range []struct {
		scanned string
		want    storagejob.ScanResult
	}{{"C1", storagejob.ScanRead}, {"", storagejob.ScanNoRead}, {"C7", storagejob.ScanMismatch}} {
		r := dispatched(t, mustRetrieve(t))
		if _, err := r.Present(c.scanned, t1, plc); !errors.Is(err, storagejob.ErrInvalidTransition) {
			t.Fatalf("Present before JOB_DONE = %v", err)
		}
		evs, err := r.CompleteMotion(t1, plc)
		if err != nil || len(evs) != 0 || r.Status() != storagejob.AwaitingArrival || !r.Open() || r.InFlight() {
			t.Fatalf("after JOB_DONE: %+v, %v, %s", evs, err, r.Status())
		}
		evs, err = r.Present(c.scanned, t1, plc)
		if err != nil {
			t.Fatal(err)
		}
		want := storagejob.ContainerPresentedAtPort{JobID: "sj-2", AsrsID: "ASRS1", ContainerID: "C1", PortID: "OUT1", Scan: c.want, ScannedContainerID: c.scanned, At: t1, PLCTime: plc, AcceptedAt: t0}
		if len(evs) != 1 || evs[0] != want || r.Status() != storagejob.Done {
			t.Fatalf("scan %q: events %+v, want %+v", c.scanned, evs, want)
		}
	}
}

func TestFailEndsAnOpenJobOnceAndOnlyExternalJobsPublish(t *testing.T) {
	f := storagejob.Failure{Reason: storagejob.ReasonPortOccupied, PLCReason: "PORT_OCCUPIED", At: t1, PLCTime: plc}
	r := dispatched(t, mustRetrieve(t))
	evs, err := r.Fail(f)
	if err != nil {
		t.Fatal(err)
	}
	want := storagejob.StorageJobFailed{JobID: "sj-2", AsrsID: "ASRS1", Kind: storagejob.Retrieve, ContainerID: "C1", PortID: "OUT1",
		Reason: storagejob.ReasonPortOccupied, PLCReason: "PORT_OCCUPIED", At: t1, PLCTime: plc, AcceptedAt: t0}
	if len(evs) != 1 || evs[0] != want {
		t.Fatalf("events = %+v, want %+v", evs, want)
	}
	if r.Status() != storagejob.Failed || r.Reason() != storagejob.ReasonPortOccupied || r.PLCReason() != "PORT_OCCUPIED" {
		t.Fatalf("job = %+v", r.State())
	}
	if _, err := r.Fail(f); !errors.Is(err, storagejob.ErrJobEnded) {
		t.Fatalf("second Fail = %v", err)
	}
	if evs, err := mustReshuffle(t).Fail(f); err != nil || len(evs) != 0 {
		t.Fatalf("reshuffle Fail = %+v, %v (must be silent)", evs, err)
	}
	q := mustStore(t)
	if _, err := q.Fail(f); err != nil || q.Status() != storagejob.Failed {
		t.Fatalf("a queued job fails too: %v %s", err, q.Status())
	}
}

func TestCancellingAQueuedJobEndsItAtOnce(t *testing.T) {
	s := mustStore(t)
	immediate, evs, err := s.RequestCancel("tc", t1)
	if err != nil || !immediate || s.Status() != storagejob.Cancelled || s.Reason() != storagejob.ReasonCancelled {
		t.Fatalf("cancel queued = %v %v %+v", immediate, err, s.State())
	}
	if len(evs) != 1 || evs[0].(storagejob.StorageJobFailed).Reason != storagejob.ReasonCancelled {
		t.Fatalf("events = %+v", evs)
	}
	if _, _, err := s.RequestCancel("tc2", t1); !errors.Is(err, storagejob.ErrJobEnded) {
		t.Fatalf("cancel of a cancelled job = %v", err)
	}
}

func TestCancellingADispatchedJobAsksThePLC(t *testing.T) {
	s := dispatched(t, mustStore(t))
	if _, _, err := s.RequestCancel("", t1); !errors.Is(err, storagejob.ErrInvalidTransition) {
		t.Fatalf("cancel without telegram = %v", err)
	}
	immediate, evs, err := s.RequestCancel("tc", t1)
	if err != nil || immediate || len(evs) != 0 || s.Status() != storagejob.CancelRequested || s.CancelTelegramID() != "tc" || !s.InFlight() {
		t.Fatalf("cancel dispatched = %v %v %+v", immediate, err, s.State())
	}
	if immediate, _, err := s.RequestCancel("tc2", t1); err != nil || immediate || s.CancelTelegramID() != "tc" {
		t.Fatalf("a repeated cancel must keep the first request: %v %v %s", immediate, err, s.CancelTelegramID())
	}
	evs, err = s.ConfirmCancel(t1)
	if err != nil || s.Status() != storagejob.Cancelled || len(evs) != 1 {
		t.Fatalf("ConfirmCancel = %+v %v %s", evs, err, s.Status())
	}
	if _, err := s.ConfirmCancel(t1); !errors.Is(err, storagejob.ErrInvalidTransition) {
		t.Fatalf("second ConfirmCancel = %v", err)
	}
}

func TestARefusedCancelRevertsToAcknowledged(t *testing.T) {
	s := dispatched(t, mustStore(t))
	_ = s.Acknowledge()
	_, _, _ = s.RequestCancel("tc", t1)
	if err := s.RevertCancel(); err != nil || s.Status() != storagejob.Acknowledged || s.CancelTelegramID() != "" {
		t.Fatalf("RevertCancel = %v %+v", err, s.State())
	}
	if err := s.RevertCancel(); !errors.Is(err, storagejob.ErrInvalidTransition) {
		t.Fatalf("RevertCancel twice = %v", err)
	}
}

func TestARefusedCancelOfAnUnacknowledgedCommandRevertsToDispatched(t *testing.T) {
	s := dispatched(t, mustStore(t))
	_, _, _ = s.RequestCancel("tc", t1)
	if err := s.RevertCancel(); err != nil || s.Status() != storagejob.Dispatched || s.Acked() || s.CancelTelegramID() != "" {
		t.Fatalf("RevertCancel = %v %+v, want DISPATCHED (the command is still unanswered)", err, s.State())
	}
	if err := s.Acknowledge(); err != nil || s.Status() != storagejob.Acknowledged {
		t.Fatalf("the late command ACK = %v %s", err, s.Status())
	}
}

func TestAJobOnItsWayToThePortCannotBeCancelled(t *testing.T) {
	r := dispatched(t, mustRetrieve(t))
	_, _ = r.CompleteMotion(t1, plc)
	if _, _, err := r.RequestCancel("tc", t1); !errors.Is(err, storagejob.ErrNotCancellable) {
		t.Fatalf("cancel while awaiting arrival = %v", err)
	}
}

func TestJobDoneDuringACancelRequestCompletesTheJob(t *testing.T) {
	s := dispatched(t, mustStore(t))
	_, _, _ = s.RequestCancel("tc", t1)
	evs, err := s.CompleteMotion(t1, plc)
	if err != nil || len(evs) != 1 || s.Status() != storagejob.Done {
		t.Fatalf("JOB_DONE during a cancel = %+v %v %s", evs, err, s.Status())
	}
}
