package usecases

import (
	"context"

	"github.com/IQVO/asrs-control/internal/application/telegrams"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
)

// Recover runs at boot after the link subscribed (ADR 0002, D13): it resends every command the
// PLC never acknowledged, with its original telegram id (the PLC answers a duplicate from its
// dedup), then every pending CANCEL, asks for every unit's state and schedules every aisle.
func (s *Service) Recover(ctx context.Context, asrsID string) error {
	l, err := s.Layout(asrsID)
	if err != nil {
		return err
	}
	// The resend is a Downlink send, so it leaves from an AfterCommit hook with a context captured
	// before the unit of work (P2-R21).
	base := context.WithoutCancel(ctx)
	err = s.d.UoW.Do(ctx, func(ctx context.Context) error {
		jobs, err := s.d.Jobs.InFlightCommands(ctx, l.ID)
		if err != nil {
			return err
		}
		cmds := make([]telegrams.Command, 0, len(jobs)+1)
		for _, j := range jobs {
			if !j.Acked() { // the PLC may never have received the command: resend it first
				cmds = append(cmds, commandFor(j))
			}
			if j.Status() == storagejob.CancelRequested {
				cmds = append(cmds, cancelCommand(j))
			}
		}
		cmds = append(cmds, telegrams.Command{TelegramID: s.d.IDs.NewID(), Type: telegrams.CmdStateRequest})
		s.d.UoW.AfterCommit(ctx, func() { s.d.Downlink.Send(base, l.ID, cmds...) })
		return nil
	})
	if err != nil {
		return err
	}
	for n := 1; n <= l.Aisles; n++ {
		if err := s.inAisle(ctx, l, n, func(context.Context, *aisle) error { return nil }); err != nil {
			return err
		}
	}
	return nil
}
