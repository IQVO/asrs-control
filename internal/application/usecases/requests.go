package usecases

import (
	"context"
	"errors"

	"github.com/IQVO/asrs-control/internal/application/ports"
	"github.com/IQVO/asrs-control/internal/domain/layout"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
	"github.com/IQVO/asrs-control/internal/domain/storagemap"
)

// StoreRequest stores the container waiting on an input port.
type StoreRequest struct{ AsrsID, ContainerID, PortID, Reference string }

// RetrieveRequest presents a stored container at an output port (its aisle's first by default).
type RetrieveRequest struct{ AsrsID, ContainerID, PortID, Reference string }

// RequestStore accepts a store, reserving its position at once (D4).
func (s *Service) RequestStore(ctx context.Context, r StoreRequest) (*storagejob.Job, error) {
	l, err := s.Layout(r.AsrsID)
	if err != nil {
		return nil, err
	}
	if r.ContainerID == "" {
		return nil, ErrInvalidRequest
	}
	port, err := portOf(l, r.PortID, layout.PortInput)
	if err != nil {
		return nil, err
	}
	v, err := s.admitStore(ctx, l, r.ContainerID)
	if err != nil {
		return nil, err
	}
	id := s.newJobID()
	err = s.inAisle(ctx, l, port.Aisle, func(ctx context.Context, a *aisle) error {
		if !a.u.Available() {
			return ErrAisleUnavailable
		}
		to, err := a.m.ReserveStore(id, r.ContainerID, v)
		switch {
		case errors.Is(err, storagemap.ErrNoFreePosition):
			return ErrStorageFull
		case errors.Is(err, storagemap.ErrContainerInRack):
			return ErrContainerAlreadyStored
		case errors.Is(err, storagemap.ErrContainerBusy):
			return ErrContainerBusy
		case err != nil:
			return err
		}
		j, err := storagejob.NewStore(storagejob.Spec{ID: id, AsrsID: l.ID, Aisle: port.Aisle, ContainerID: r.ContainerID,
			PortID: port.ID, Reference: r.Reference, To: to, At: a.now})
		if err != nil {
			return err
		}
		return insertExternal(ctx, s.d.Jobs, j)
	})
	if err != nil {
		return nil, err
	}
	return s.d.Jobs.Get(ctx, id)
}

// insertExternal stores a new external job; the repository's per-container uniqueness of open
// external jobs (P2-R24) is what closes the race between aisles, and it rolls the unit of work back.
func insertExternal(ctx context.Context, jobs ports.StorageJobs, j *storagejob.Job) error {
	if err := jobs.Insert(ctx, j); errors.Is(err, ports.ErrOpenJobExists) {
		return ErrContainerBusy
	} else if err != nil {
		return err
	}
	return nil
}

// admitStore refuses a container already in the AS/RS or with an open job, and classifies it.
func (s *Service) admitStore(ctx context.Context, l layout.Layout, container string) (storagemap.Velocity, error) {
	if _, found, err := s.d.Maps.Find(ctx, l.ID, container); err != nil || found {
		if err == nil {
			err = ErrContainerAlreadyStored
		}
		return "", err
	}
	if busy, err := s.d.Jobs.HasOpen(ctx, l.ID, container); err != nil || busy {
		if err == nil {
			err = ErrContainerBusy
		}
		return "", err
	}
	since := s.d.Clock.Now().UTC().Add(-s.d.Velocity.Window)
	n, err := s.d.Jobs.CountRetrievals(ctx, l.ID, container, since)
	if err != nil {
		return "", err
	}
	return s.d.Velocity.classify(n), nil
}

// RequestRetrieve accepts a retrieve of a stored container.
func (s *Service) RequestRetrieve(ctx context.Context, r RetrieveRequest) (*storagejob.Job, error) {
	l, err := s.Layout(r.AsrsID)
	if err != nil {
		return nil, err
	}
	if r.ContainerID == "" {
		return nil, ErrInvalidRequest
	}
	n, found, err := s.d.Maps.Find(ctx, l.ID, r.ContainerID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrUnknownContainer
	}
	port, err := retrievePort(l, n, r.PortID)
	if err != nil {
		return nil, err
	}
	id := s.newJobID()
	err = s.inAisle(ctx, l, n, func(ctx context.Context, a *aisle) error {
		if !a.u.Available() {
			return ErrAisleUnavailable
		}
		switch err := a.m.ReserveRetrieve(id, r.ContainerID); {
		case errors.Is(err, storagemap.ErrContainerNotInRack):
			return ErrUnknownContainer
		case errors.Is(err, storagemap.ErrContainerBusy):
			return ErrContainerBusy
		case err != nil:
			return err
		}
		j, err := storagejob.NewRetrieve(storagejob.Spec{ID: id, AsrsID: l.ID, Aisle: n, ContainerID: r.ContainerID,
			PortID: port.ID, Reference: r.Reference, At: a.now})
		if err != nil {
			return err
		}
		return insertExternal(ctx, s.d.Jobs, j)
	})
	if err != nil {
		return nil, err
	}
	return s.d.Jobs.Get(ctx, id)
}
