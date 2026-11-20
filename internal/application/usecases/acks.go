package usecases

import (
	"context"
	"errors"

	"github.com/IQVO/asrs-control/internal/application/ports"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
)

// onTelegramJob runs fn, in its aisle's unit of work, on the job whose command or CANCEL
// telegram is telegramID (read again under the aisle lock). An unknown telegram is nothing to do.
func (s *Service) onTelegramJob(ctx context.Context, asrsID, telegramID string,
	fn func(ctx context.Context, a *aisle, j *storagejob.Job) error) error {
	l, err := s.Layout(asrsID)
	if err != nil || telegramID == "" {
		return err
	}
	pre, err := s.d.Jobs.ByTelegram(ctx, l.ID, telegramID)
	if errors.Is(err, ports.ErrJobNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.inAisle(ctx, l, pre.Aisle(), func(ctx context.Context, a *aisle) error {
		j, err := s.d.Jobs.Get(ctx, pre.ID())
		if err != nil {
			return err
		}
		return fn(ctx, a, j)
	})
}

// HandleAck applies the PLC's ACK of a command or of a CANCEL (never a NOT_READY: the link
// resends those).
func (s *Service) HandleAck(ctx context.Context, asrsID, ackOf string, accepted bool, reason string) error {
	return s.onTelegramJob(ctx, asrsID, ackOf, func(ctx context.Context, a *aisle, j *storagejob.Job) error {
		switch ackOf {
		case j.CancelTelegramID():
			return a.cancelAcked(ctx, j, accepted)
		case j.TelegramID():
			return a.commandAcked(ctx, j, accepted, reason)
		default:
			return nil
		}
	})
}

// commandAcked records the command's ACK. It also lands while a CANCEL is pending (the job stays
// CANCEL_REQUESTED but remembers the ACK, so a refused CANCEL returns to ACKNOWLEDGED).
func (a *aisle) commandAcked(ctx context.Context, j *storagejob.Job, accepted bool, reason string) error {
	if !j.Open() || !j.InFlight() || (accepted && j.Acked()) {
		return nil // a late or repeated ACK
	}
	if !accepted {
		return a.commandUnanswered(ctx, j, storagejob.ReasonCommandRefused, reason)
	}
	if err := j.Acknowledge(); err != nil {
		return err
	}
	return a.s.d.Jobs.Update(ctx, j)
}

func (a *aisle) cancelAcked(ctx context.Context, j *storagejob.Job, accepted bool) error {
	if !j.Open() || j.Status() != storagejob.CancelRequested {
		return nil
	}
	if !accepted {
		return a.cancelUnanswered(ctx, j)
	}
	evs, err := j.ConfirmCancel(a.now)
	if err != nil {
		return err
	}
	a.release(j)
	a.events = append(a.events, evs...)
	return a.s.d.Jobs.Update(ctx, j)
}

// commandUnanswered is the one job outcome of a command the PLC refused (HandleAck) or never
// answered (HandleExpired): the job fails with reason and frees the unit.
func (a *aisle) commandUnanswered(ctx context.Context, j *storagejob.Job, reason storagejob.Reason, plcReason string) error {
	if !j.Open() || !j.InFlight() {
		return nil
	}
	return a.fail(ctx, j, storagejob.Failure{Reason: reason, PLCReason: plcReason, At: a.now})
}

// cancelUnanswered is the one outcome of a CANCEL the PLC refused or never answered: the job goes
// on (RevertCancel picks ACKNOWLEDGED or DISPATCHED from whether the command was acknowledged).
func (a *aisle) cancelUnanswered(ctx context.Context, j *storagejob.Job) error {
	if err := j.RevertCancel(); err != nil {
		return err
	}
	return a.s.d.Jobs.Update(ctx, j)
}

// HandleExpired applies a down telegram the PLC never acknowledged after 3 retransmissions: an
// unanswered command fails its job EQUIPMENT_UNREACHABLE (also while a CANCEL is pending, so the
// aisle is never left waiting on a command nobody answers); an unanswered CANCEL is reverted.
func (s *Service) HandleExpired(ctx context.Context, asrsID, telegramID string) error {
	return s.onTelegramJob(ctx, asrsID, telegramID, func(ctx context.Context, a *aisle, j *storagejob.Job) error {
		switch {
		case !j.Open():
			return nil
		case j.CancelTelegramID() == telegramID:
			return a.cancelAcked(ctx, j, false)
		case j.TelegramID() == telegramID && !j.Acked():
			return a.commandUnanswered(ctx, j, storagejob.ReasonEquipmentUnreachable, "")
		default:
			return nil
		}
	})
}
