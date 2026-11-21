package usecases

import (
	"context"
	"errors"
	"fmt"

	"github.com/IQVO/asrs-control/internal/application/telegrams"
	"github.com/IQVO/asrs-control/internal/domain/layout"
	"github.com/IQVO/asrs-control/internal/domain/scheduling"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
	"github.com/IQVO/asrs-control/internal/domain/storagemap"
)

// maxDecisions bounds one scheduling pass; every non-final decision ends one job.
const maxDecisions = 256

// schedule asks the scheduler until it dispatches a job or has nothing to do.
func (a *aisle) schedule(ctx context.Context) error {
	for range maxDecisions {
		open, err := a.s.d.Jobs.Open(ctx, a.l.ID, a.m.Aisle())
		if err != nil {
			return err
		}
		d := scheduling.Next(a.m, a.u, open)
		switch d.Action {
		case scheduling.None:
			return nil
		case scheduling.Dispatch:
			return a.dispatch(ctx, d.Job)
		case scheduling.Reshuffle:
			if err := a.reshuffle(ctx, d.Job, d.Blocked); err != nil {
				return err
			}
		case scheduling.Fail:
			if err := a.fail(ctx, d.Job, storagejob.Failure{Reason: d.Reason, At: a.now}); err != nil {
				return err
			}
		}
		if a.u.Busy() {
			return nil
		}
	}
	return fmt.Errorf("usecases: the scheduler of %s aisle %d did not settle", a.l.ID, a.m.Aisle())
}

func (a *aisle) dispatch(ctx context.Context, j *storagejob.Job) error {
	if j.Kind() == storagejob.Retrieve {
		from, _ := a.m.Position(j.ContainerID())
		if err := j.Locate(from); err != nil {
			return err
		}
	}
	if err := j.Dispatch(a.s.d.IDs.NewID()); err != nil {
		return err
	}
	if err := a.u.Assign(j.ID()); err != nil {
		return err
	}
	if err := a.s.d.Jobs.Update(ctx, j); err != nil {
		return err
	}
	a.cmds = append(a.cmds, commandFor(j))
	return nil
}

// reshuffle moves the container blocking the parent retrieve (invisible outside, invariant 6).
func (a *aisle) reshuffle(ctx context.Context, parent *storagejob.Job, blocked layout.Position) error {
	id := a.s.newJobID()
	container, from, to, err := a.m.PlanReshuffle(id, blocked, storagemap.Fast)
	if errors.Is(err, storagemap.ErrNoFreePosition) || errors.Is(err, storagemap.ErrContainerBusy) {
		return a.fail(ctx, parent, storagejob.Failure{Reason: storagejob.ReasonNoReshufflePosition, At: a.now})
	}
	if err != nil {
		return err
	}
	j, err := storagejob.NewReshuffle(storagejob.Spec{ID: id, AsrsID: a.l.ID, Aisle: a.m.Aisle(), ContainerID: container,
		ParentID: parent.ID(), From: from, To: to, At: a.now})
	if err != nil {
		return err
	}
	if err := a.s.d.Jobs.Insert(ctx, j); err != nil {
		return err
	}
	return a.dispatch(ctx, j)
}

// fail ends j, releases what it held and, for a reshuffle, fails its retrieve.
func (a *aisle) fail(ctx context.Context, j *storagejob.Job, f storagejob.Failure) error {
	evs, err := j.Fail(f)
	if err != nil {
		return err
	}
	a.release(j)
	a.events = append(a.events, evs...)
	if err := a.s.d.Jobs.Update(ctx, j); err != nil {
		return err
	}
	if j.Kind() != storagejob.Reshuffle {
		return nil
	}
	parent, err := a.s.d.Jobs.Get(ctx, j.ParentID())
	if err != nil || !parent.Open() {
		return err
	}
	return a.fail(ctx, parent, storagejob.Failure{Reason: storagejob.ReasonReshuffleFailed, At: f.At, PLCTime: f.PLCTime})
}

// release frees the map holds and the unit of a job that ended without its motion.
func (a *aisle) release(j *storagejob.Job) {
	switch j.Kind() {
	case storagejob.Store:
		a.m.ReleaseStore(j.ID(), j.To())
	case storagejob.Retrieve:
		a.m.ReleaseRetrieve(j.ID(), j.ContainerID())
	default:
		a.m.ReleaseMove(j.ID(), j.ContainerID(), j.To())
	}
	a.u.Release(j.ID())
}

func commandFor(j *storagejob.Job) telegrams.Command {
	c := telegrams.Command{TelegramID: j.TelegramID(), JobID: j.ID(), ContainerID: j.ContainerID()}
	from, to := j.From(), j.To()
	switch j.Kind() {
	case storagejob.Store:
		c.Type, c.PortID, c.To = telegrams.CmdStore, j.PortID(), &to
	case storagejob.Retrieve:
		c.Type, c.PortID, c.From = telegrams.CmdRetrieve, j.PortID(), &from
	default:
		c.Type, c.From, c.To = telegrams.CmdMove, &from, &to
	}
	return c
}

func cancelCommand(j *storagejob.Job) telegrams.Command {
	return telegrams.Command{TelegramID: j.CancelTelegramID(), Type: telegrams.CmdCancel, JobID: j.ID()}
}
