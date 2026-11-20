package usecases

import (
	"context"
	"time"

	"github.com/IQVO/asrs-control/internal/application/telegrams"
	"github.com/IQVO/asrs-control/internal/domain/capacity"
	"github.com/IQVO/asrs-control/internal/domain/equipment"
	"github.com/IQVO/asrs-control/internal/domain/layout"
	"github.com/IQVO/asrs-control/internal/domain/storagemap"
)

// aisle is one aisle's state inside one unit of work. The map and the unit are locked for the
// transaction, so every job change of the aisle is serialised.
type aisle struct {
	s      *Service
	l      layout.Layout
	m      *storagemap.Map
	u      *equipment.Unit
	now    time.Time
	events []any
	cmds   []telegrams.Command
	quiet  bool // a duplicate telegram: nothing changed, nothing to save
}

// inAisle runs fn in one unit of work on aisle n, then the scheduler, then saves. The commands it
// produced are handed to the Downlink from an AfterCommit hook: after the OUTERMOST unit of work
// committed (the Idempotency-Key middleware's, for a REST call), never for a rolled-back one.
func (s *Service) inAisle(ctx context.Context, l layout.Layout, n int, fn func(ctx context.Context, a *aisle) error) error {
	// The hook sends with the caller's context, captured before Do: the transaction's ctx must
	// not be reused once the unit of work is over (P2-R21).
	base := context.WithoutCancel(ctx)
	return s.d.UoW.Do(ctx, func(ctx context.Context) error {
		a, err := s.openAisle(ctx, l, n)
		if err != nil {
			return err
		}
		if err := fn(ctx, a); err != nil {
			return err
		}
		if a.quiet {
			return nil
		}
		if err := a.schedule(ctx); err != nil {
			return err
		}
		if err := a.save(ctx); err != nil {
			return err
		}
		if len(a.cmds) > 0 {
			cmds := a.cmds
			s.d.UoW.AfterCommit(ctx, func() { s.d.Downlink.Send(base, l.ID, cmds...) })
		}
		return nil
	})
}

func (s *Service) openAisle(ctx context.Context, l layout.Layout, n int) (*aisle, error) {
	m, err := s.d.Maps.Lock(ctx, l, n)
	if err != nil {
		return nil, err
	}
	u, err := s.d.Units.Lock(ctx, l.ID, n)
	if err != nil {
		return nil, err
	}
	return &aisle{s: s, l: l, m: m, u: u, now: s.d.Clock.Now().UTC()}, nil
}

func (a *aisle) save(ctx context.Context) error {
	if err := a.s.d.Maps.Save(ctx, a.l.ID, a.m); err != nil {
		return err
	}
	if err := a.s.d.Units.Save(ctx, a.u); err != nil {
		return err
	}
	if err := a.refreshCapacity(ctx); err != nil {
		return err
	}
	if len(a.events) == 0 {
		return nil
	}
	return a.s.d.Events.Append(ctx, a.events...)
}

// refreshCapacity publishes AsrsCapacityChanged when the snapshot moved. The capacity row lock
// serialises it across aisles, so the stored snapshot is the last committed one.
func (a *aisle) refreshCapacity(ctx context.Context) error {
	prev, known, err := a.s.d.Capacity.Lock(ctx, a.l.ID)
	if err != nil {
		return err
	}
	next, err := a.s.capacity(ctx, a.l)
	if err != nil {
		return err
	}
	ev, changed := capacity.Changed(prev, known, next, a.now)
	if !changed {
		return nil
	}
	if err := a.s.d.Capacity.Save(ctx, next); err != nil {
		return err
	}
	a.events = append(a.events, ev)
	return nil
}

// capacity reads the AS/RS's current capacity (inside a unit of work it sees its own writes).
func (s *Service) capacity(ctx context.Context, l layout.Layout) (capacity.Snapshot, error) {
	snap := capacity.Snapshot{AsrsID: l.ID, UnitsTotal: l.Aisles, PositionsTotal: l.TotalPositions()}
	units, err := s.d.Units.List(ctx, l.ID)
	if err != nil {
		return snap, err
	}
	for _, u := range units {
		if u.Available() {
			snap.UnitsAvailable++
		}
	}
	for n := 1; n <= l.Aisles; n++ {
		m, err := s.d.Maps.Get(ctx, l, n)
		if err != nil {
			return snap, err
		}
		snap.PositionsFree += m.Free()
	}
	snap.OpenJobs, err = s.d.Jobs.CountOpen(ctx, l.ID)
	return snap, err
}
