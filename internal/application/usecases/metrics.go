package usecases

import (
	"context"
	"time"

	"github.com/IQVO/asrs-control/internal/domain/storagejob"
)

// Outcomes of an ended external job on ports.JobMetrics.
const (
	OutcomeCompleted = "completed"
	OutcomeFailed    = "failed"
)

// jobEnd is one external job that ended in a unit of work.
type jobEnd struct {
	kind    storagejob.Kind
	outcome string
	reason  storagejob.Reason
	took    time.Duration
}

// endsOf reads the ended external jobs off the domain events of a unit of work: a store ends
// with ContainerStored, a retrieve with ContainerPresentedAtPort, and every failure or
// cancellation of an external job with StorageJobFailed. Reshuffles publish none of them.
func endsOf(events []any) []jobEnd {
	var out []jobEnd
	for _, e := range events {
		switch ev := e.(type) {
		case storagejob.ContainerStored:
			out = append(out, jobEnd{storagejob.Store, OutcomeCompleted, "", ev.At.Sub(ev.AcceptedAt)})
		case storagejob.ContainerPresentedAtPort:
			out = append(out, jobEnd{storagejob.Retrieve, OutcomeCompleted, "", ev.At.Sub(ev.AcceptedAt)})
		case storagejob.StorageJobFailed:
			out = append(out, jobEnd{ev.Kind, OutcomeFailed, ev.Reason, ev.At.Sub(ev.AcceptedAt)})
		}
	}
	return out
}

// observeAfterCommit reports the aisle's accepted and ended external jobs to Deps.Metrics from
// an AfterCommit hook: only for the OUTERMOST unit of work that committed, never for a rolled-back
// one. base is the caller's context (the unit of work's ctx must not outlive it).
func (s *Service) observeAfterCommit(ctx, base context.Context, a *aisle) {
	m := s.d.Metrics
	if m == nil {
		return
	}
	accepted, ends := a.accepted, endsOf(a.events)
	if len(accepted) == 0 && len(ends) == 0 {
		return
	}
	s.d.UoW.AfterCommit(ctx, func() {
		for _, k := range accepted {
			m.JobAccepted(base, string(k))
		}
		for _, e := range ends {
			m.JobEnded(base, string(e.kind), e.outcome, string(e.reason), e.took)
		}
	})
}
