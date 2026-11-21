package storagejob_test

import (
	"errors"
	"testing"

	"github.com/IQVO/asrs-control/internal/domain/storagejob"
)

func cancelledWant(j *storagejob.Job) storagejob.StorageJobFailed {
	return storagejob.StorageJobFailed{JobID: j.ID(), AsrsID: j.AsrsID(), Kind: j.Kind(), ContainerID: j.ContainerID(),
		PortID: j.PortID(), Reference: j.Reference(), Reason: storagejob.ReasonCancelled, At: t1, AcceptedAt: t0}
}

func TestCancelOutcomeEventsCarryTheWholeFailure(t *testing.T) {
	q := mustStore(t)
	_, evs, err := q.RequestCancel("tc", t1)
	if err != nil || len(evs) != 1 || evs[0] != cancelledWant(q) {
		t.Fatalf("queued cancel = %+v, %v; want %+v", evs, err, cancelledWant(q))
	}
	if !q.EndedAt().Equal(t1) {
		t.Fatalf("EndedAt = %s", q.EndedAt())
	}
	d := dispatched(t, mustRetrieve(t))
	if _, _, err := d.RequestCancel("tc", t0); err != nil {
		t.Fatal(err)
	}
	evs, err = d.ConfirmCancel(t1)
	if err != nil || len(evs) != 1 || evs[0] != cancelledWant(d) {
		t.Fatalf("ConfirmCancel = %+v, %v; want %+v", evs, err, cancelledWant(d))
	}
}

func TestJobDoneDuringCancelRaisesTheWholeContainerStored(t *testing.T) {
	s := dispatched(t, mustStore(t))
	_, _, _ = s.RequestCancel("tc", t0)
	evs, err := s.CompleteMotion(t1, plc)
	want := storagejob.ContainerStored{JobID: "sj-1", AsrsID: "ASRS1", ContainerID: "C1", PortID: "IN1", Reference: "leg-9", At: t1, PLCTime: plc, AcceptedAt: t0}
	if err != nil || len(evs) != 1 || evs[0] != want || s.Status() != storagejob.Done {
		t.Fatalf("got %+v, %v, %s; want %+v", evs, err, s.Status(), want)
	}
}

func TestFailFromEveryOpenStatusEmitsOneFailedEvent(t *testing.T) {
	f := storagejob.Failure{Reason: storagejob.ReasonEquipmentFault, PLCReason: "X", At: t1, PLCTime: plc}
	prep := map[string]func(*testing.T) *storagejob.Job{
		"acknowledged": func(t *testing.T) *storagejob.Job {
			j := dispatched(t, mustStore(t))
			_ = j.Acknowledge()
			return j
		},
		"cancel requested": func(t *testing.T) *storagejob.Job {
			j := dispatched(t, mustStore(t))
			_, _, _ = j.RequestCancel("tc", t0)
			return j
		},
		"awaiting arrival": func(t *testing.T) *storagejob.Job {
			j := dispatched(t, mustRetrieve(t))
			_, _ = j.CompleteMotion(t0, plc)
			return j
		},
	}
	for name, p := range prep {
		t.Run(name, func(t *testing.T) {
			j := p(t)
			evs, err := j.Fail(f)
			want := storagejob.StorageJobFailed{JobID: j.ID(), AsrsID: "ASRS1", Kind: j.Kind(), ContainerID: "C1", PortID: j.PortID(),
				Reference: j.Reference(), Reason: f.Reason, PLCReason: "X", At: t1, PLCTime: plc, AcceptedAt: t0}
			if err != nil || len(evs) != 1 || evs[0] != want || j.Status() != storagejob.Failed {
				t.Fatalf("got %+v, %v, %s; want %+v", evs, err, j.Status(), want)
			}
		})
	}
}

func TestEndedJobsRejectEveryLateTelegram(t *testing.T) {
	done := dispatched(t, mustRetrieve(t))
	_, _ = done.CompleteMotion(t1, plc)
	_, _ = done.Present("C1", t1, plc)
	cancelled := dispatched(t, mustStore(t))
	_, _, _ = cancelled.RequestCancel("tc", t0)
	_, _ = cancelled.ConfirmCancel(t0)
	failed := dispatched(t, mustStore(t))
	_, _ = failed.Fail(storagejob.Failure{Reason: storagejob.ReasonEquipmentFault, At: t0})
	for name, j := range map[string]*storagejob.Job{"done": done, "cancelled": cancelled, "failed": failed} {
		t.Run(name, func(t *testing.T) { assertRejectsLateTelegrams(t, j) })
	}
}

func assertRejectsLateTelegrams(t *testing.T, j *storagejob.Job) {
	t.Helper()
	if err := j.Acknowledge(); !errors.Is(err, storagejob.ErrInvalidTransition) {
		t.Errorf("Acknowledge = %v", err)
	}
	if err := j.RevertCancel(); !errors.Is(err, storagejob.ErrInvalidTransition) {
		t.Errorf("RevertCancel = %v", err)
	}
	if evs, err := j.ConfirmCancel(t1); !errors.Is(err, storagejob.ErrInvalidTransition) || evs != nil {
		t.Errorf("ConfirmCancel = %+v, %v", evs, err)
	}
	if evs, err := j.CompleteMotion(t1, plc); !errors.Is(err, storagejob.ErrInvalidTransition) || evs != nil {
		t.Errorf("CompleteMotion = %+v, %v", evs, err)
	}
	if evs, err := j.Present("C1", t1, plc); !errors.Is(err, storagejob.ErrInvalidTransition) || evs != nil {
		t.Errorf("Present = %+v, %v", evs, err)
	}
	if evs, err := j.Fail(storagejob.Failure{Reason: storagejob.ReasonEquipmentFault, At: t1}); !errors.Is(err, storagejob.ErrJobEnded) || evs != nil {
		t.Errorf("Fail = %+v, %v", evs, err)
	}
}
