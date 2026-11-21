package usecases

import (
	"context"

	"github.com/IQVO/asrs-control/internal/domain/layout"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
)

// Cancel cancels an external job: at once when it is still queued, else through a CANCEL
// telegram whose ACK decides (HandleAck).
func (s *Service) Cancel(ctx context.Context, asrsID, jobID string) (*storagejob.Job, error) {
	l, err := s.Layout(asrsID)
	if err != nil {
		return nil, err
	}
	pre, err := s.externalJob(ctx, l.ID, jobID)
	if err != nil {
		return nil, err
	}
	err = s.inAisle(ctx, l, pre.Aisle(), func(ctx context.Context, a *aisle) error {
		j, err := s.d.Jobs.Get(ctx, jobID)
		if err != nil {
			return err
		}
		tid := s.d.IDs.NewID()
		immediate, evs, err := j.RequestCancel(tid, a.now)
		if err != nil {
			return err
		}
		if immediate {
			a.release(j)
			a.events = append(a.events, evs...)
		} else if j.CancelTelegramID() == tid {
			a.cmds = append(a.cmds, cancelCommand(j))
		}
		return s.d.Jobs.Update(ctx, j)
	})
	if err != nil {
		return nil, err
	}
	return s.d.Jobs.Get(ctx, jobID)
}

// ConfirmPickup frees an output port: material-flow-control took the presented container (D7).
func (s *Service) ConfirmPickup(ctx context.Context, asrsID, portID, containerID string) error {
	l, err := s.Layout(asrsID)
	if err != nil {
		return err
	}
	port, err := portOf(l, portID, layout.PortOutput)
	if err != nil {
		return err
	}
	if containerID == "" {
		return ErrInvalidRequest
	}
	return s.inAisle(ctx, l, port.Aisle, func(_ context.Context, a *aisle) error {
		if err := a.m.Pickup(port.ID, containerID); err != nil {
			return ErrPortNotPresenting
		}
		return nil
	})
}

// externalJob reads a job material-flow-control may see: a reshuffle is not one.
func (s *Service) externalJob(ctx context.Context, asrsID, jobID string) (*storagejob.Job, error) {
	j, err := s.d.Jobs.Get(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if j.AsrsID() != asrsID || !j.External() {
		return nil, ErrJobNotFound
	}
	return j, nil
}
