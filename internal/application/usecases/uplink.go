package usecases

import (
	"context"
	"errors"
	"time"

	"github.com/IQVO/asrs-control/internal/application/ports"
	"github.com/IQVO/asrs-control/internal/application/telegrams"
	"github.com/IQVO/asrs-control/internal/domain/equipment"
	"github.com/IQVO/asrs-control/internal/domain/layout"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
)

// HandleUp applies one PLC up telegram exactly once: it is claimed with its ACK id in the
// effect's unit of work, so a retransmission gets the same ACK and changes nothing. The link
// sends the returned ACK only after this returned nil (ADR 0002).
func (s *Service) HandleUp(ctx context.Context, asrsID string, t telegrams.UpTelegram) (string, error) {
	l, err := s.Layout(asrsID)
	if err != nil {
		return "", err
	}
	n, ok, err := s.aisleOf(ctx, l, t)
	if err != nil {
		return "", err
	}
	ackID := s.d.IDs.NewID()
	if !ok {
		s.d.Log.WarnContext(ctx, "up telegram matches nothing in this AS/RS; acknowledged and ignored",
			"asrs", sanitizeLog(l.ID), "type", sanitizeLog(t.Type), "telegramId", sanitizeLog(t.TelegramID))
		return s.claimOnly(ctx, l.ID, t.TelegramID, ackID)
	}
	err = s.inAisle(ctx, l, n, func(ctx context.Context, a *aisle) error {
		first, claimed, err := s.d.Telegrams.Claim(ctx, l.ID, t.TelegramID, ackID)
		if err != nil {
			return err
		}
		if !claimed {
			ackID, a.quiet = first, true
			return nil
		}
		return a.apply(ctx, t)
	})
	return ackID, err
}

func (s *Service) claimOnly(ctx context.Context, asrsID, telegramID, ackID string) (string, error) {
	err := s.d.UoW.Do(ctx, func(ctx context.Context) error {
		first, _, err := s.d.Telegrams.Claim(ctx, asrsID, telegramID, ackID)
		ackID = first
		return err
	})
	return ackID, err
}

// aisleOf finds the aisle an up telegram is about.
func (s *Service) aisleOf(ctx context.Context, l layout.Layout, t telegrams.UpTelegram) (int, bool, error) {
	switch t.Type {
	case telegrams.UpJobDone, telegrams.UpJobFailed:
		j, err := s.d.Jobs.Get(ctx, t.JobID)
		if errors.Is(err, ports.ErrJobNotFound) {
			return 0, false, nil
		}
		if err != nil {
			return 0, false, err
		}
		return j.Aisle(), j.AsrsID() == l.ID, nil
	case telegrams.UpUnitState:
		n, ok := l.AisleOfUnit(t.UnitID)
		return n, ok, nil
	case telegrams.UpPortArrival:
		p, ok := l.Port(t.PortID)
		return p.Aisle, ok && p.Kind == layout.PortOutput, nil
	default:
		return 0, false, nil
	}
}

func (a *aisle) apply(ctx context.Context, t telegrams.UpTelegram) error {
	switch t.Type {
	case telegrams.UpJobDone:
		return a.jobDone(ctx, t)
	case telegrams.UpJobFailed:
		return a.jobFailed(ctx, t)
	case telegrams.UpUnitState:
		return a.unitState(ctx, t)
	default:
		return a.portArrival(ctx, t)
	}
}

func (a *aisle) jobDone(ctx context.Context, t telegrams.UpTelegram) error {
	j, err := a.s.d.Jobs.Get(ctx, t.JobID)
	if err != nil {
		return err
	}
	if !j.Open() || !j.InFlight() { // a duplicate or late JOB_DONE (ended job), or a stray one
		a.s.d.Log.WarnContext(ctx, "JOB_DONE for a job that is not with the PLC; ignored", "job", j.ID(), "status", sanitizeLog(string(j.Status())))
		return nil
	}
	if err := a.applyMotion(ctx, j); err != nil {
		a.s.d.Log.ErrorContext(ctx, "the storage map disagrees with the PLC's JOB_DONE", "job", j.ID(), "error", sanitizeErr(err))
		return a.fail(ctx, j, storagejob.Failure{Reason: storagejob.ReasonStorageMapMismatch, At: a.now, PLCTime: t.Timestamp})
	}
	evs, err := j.CompleteMotion(a.now, t.Timestamp)
	if err != nil {
		return err
	}
	a.u.Release(j.ID())
	a.events = append(a.events, evs...)
	return a.s.d.Jobs.Update(ctx, j)
}

func (a *aisle) applyMotion(ctx context.Context, j *storagejob.Job) error {
	switch j.Kind() {
	case storagejob.Store:
		return a.m.CompleteStore(j.ID(), j.ContainerID(), j.To())
	case storagejob.Reshuffle:
		return a.m.CompleteMove(j.ID(), j.ContainerID(), j.From(), j.To())
	default:
		replaced, err := a.m.CompleteRetrieve(j.ContainerID(), j.From(), j.PortID())
		if replaced != "" {
			a.s.d.Log.WarnContext(ctx, "presented over a container whose pickup was never confirmed", "port", sanitizeLog(j.PortID()), "replaced", sanitizeLog(replaced))
		}
		return err
	}
}

func (a *aisle) jobFailed(ctx context.Context, t telegrams.UpTelegram) error {
	j, err := a.s.d.Jobs.Get(ctx, t.JobID)
	if err != nil {
		return err
	}
	if !j.Open() || !j.InFlight() { // ended (duplicate or late), or not with the PLC (stray)
		a.s.d.Log.WarnContext(ctx, "JOB_FAILED for a job that is not with the PLC; ignored", "job", j.ID(), "status", sanitizeLog(string(j.Status())))
		return nil
	}
	return a.fail(ctx, j, storagejob.Failure{Reason: storagejob.ReasonForPLC(t.Reason), PLCReason: t.Reason, At: a.now, PLCTime: t.Timestamp})
}

func (a *aisle) unitState(ctx context.Context, t telegrams.UpTelegram) error {
	st, err := equipment.ParseState(t.State)
	if err != nil {
		a.s.d.Log.WarnContext(ctx, "UNIT_STATE with an unknown state; ignored", "unit", sanitizeLog(t.UnitID), "error", sanitizeErr(err))
		return nil
	}
	ev, changed := a.u.Report(st, a.now, t.Timestamp)
	if !changed {
		return nil
	}
	a.events = append(a.events, ev)
	if st != equipment.Held {
		return nil
	}
	return a.failQueued(ctx, t.Timestamp)
}

// failQueued fails every queued job of a held aisle with EQUIPMENT_FAULT (D8). Each job is read
// again first: failing a reshuffle also fails its retrieve.
func (a *aisle) failQueued(ctx context.Context, plcTime time.Time) error {
	open, err := a.s.d.Jobs.Open(ctx, a.l.ID, a.m.Aisle())
	if err != nil {
		return err
	}
	for _, q := range open {
		if q.Status() != storagejob.Queued {
			continue
		}
		j, err := a.s.d.Jobs.Get(ctx, q.ID())
		if err != nil {
			return err
		}
		if j.Status() != storagejob.Queued {
			continue
		}
		if err := a.fail(ctx, j, storagejob.Failure{Reason: storagejob.ReasonEquipmentFault, At: a.now, PLCTime: plcTime}); err != nil {
			return err
		}
	}
	return nil
}

func (a *aisle) portArrival(ctx context.Context, t telegrams.UpTelegram) error {
	j, err := a.s.d.Jobs.OldestAwaiting(ctx, a.l.ID, t.PortID)
	if errors.Is(err, ports.ErrJobNotFound) {
		a.s.d.Log.WarnContext(ctx, "PORT_ARRIVAL with no retrieve awaiting it; ignored", "port", sanitizeLog(t.PortID))
		return nil
	}
	if err != nil {
		return err
	}
	evs, err := j.Present(t.ContainerID, a.now, t.Timestamp)
	if err != nil {
		return err
	}
	a.events = append(a.events, evs...)
	return a.s.d.Jobs.Update(ctx, j)
}
