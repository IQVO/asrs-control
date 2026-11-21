// Package usecases is asrs-control's application layer: one Service whose every write runs in
// one aisle unit of work (ADR 0002).
package usecases

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/IQVO/asrs-control/internal/application/ports"
	"github.com/IQVO/asrs-control/internal/domain/layout"
	"github.com/IQVO/asrs-control/internal/domain/storagemap"
)

// Refusals the REST adapter maps to problem types.
var (
	ErrUnknownAsrs            = errors.New("unknown AS/RS")
	ErrUnknownPort            = errors.New("the port is not a port of this AS/RS")
	ErrWrongPortKind          = errors.New("a store uses an INPUT port, a retrieve and a pickup an OUTPUT port")
	ErrPortUnreachable        = errors.New("the output port is not in the aisle that holds the container")
	ErrUnknownContainer       = errors.New("the container is not stored in this AS/RS")
	ErrContainerAlreadyStored = errors.New("the container is already stored in this AS/RS")
	ErrContainerBusy          = errors.New("the container already has an open storage job")
	ErrStorageFull            = errors.New("the aisle has no assignable rack position")
	ErrAisleUnavailable       = errors.New("the aisle's shuttle-and-lift unit is held")
	ErrPortNotPresenting      = errors.New("the output port does not hold that container")
	ErrInvalidRequest         = errors.New("invalid request")
	ErrJobNotFound            = ports.ErrJobNotFound
)

// VelocityPolicy: FastRetrievals or more DONE retrieves within Window make a container FAST.
type VelocityPolicy struct {
	Window         time.Duration
	FastRetrievals int
}

func (v VelocityPolicy) classify(retrievals int) storagemap.Velocity {
	if v.FastRetrievals > 0 && retrievals >= v.FastRetrievals {
		return storagemap.Fast
	}
	return storagemap.Slow
}

// Deps are the Service's collaborators.
type Deps struct {
	Layouts   []layout.Layout
	UoW       ports.UnitOfWork
	Maps      ports.StorageMaps
	Jobs      ports.StorageJobs
	Units     ports.Units
	Capacity  ports.Capacities
	Telegrams ports.ProcessedTelegrams
	Events    ports.EventSink
	Downlink  ports.Downlink
	Clock     ports.Clock
	IDs       ports.IDs
	Velocity  VelocityPolicy
	Log       *slog.Logger
	// Metrics counts accepted and ended external jobs after commit; nil records nothing.
	Metrics ports.JobMetrics
}

// Service is the application.
type Service struct {
	d       Deps
	layouts map[string]layout.Layout
}

// New validates the layouts.
func New(d Deps) (*Service, error) {
	if len(d.Layouts) == 0 {
		return nil, errors.New("usecases: at least one AS/RS layout is required")
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	s := &Service{d: d, layouts: map[string]layout.Layout{}}
	for _, l := range d.Layouts {
		if err := l.Validate(); err != nil {
			return nil, err
		}
		if _, dup := s.layouts[l.ID]; dup {
			return nil, fmt.Errorf("usecases: AS/RS %s is configured twice", l.ID)
		}
		s.layouts[l.ID] = l
	}
	return s, nil
}

// Prepare creates every aisle's map and unit that does not exist yet (boot).
func (s *Service) Prepare(ctx context.Context) error {
	for _, l := range s.d.Layouts {
		if err := s.d.Maps.Ensure(ctx, l); err != nil {
			return err
		}
		if err := s.d.Units.Ensure(ctx, l, s.d.Clock.Now().UTC()); err != nil {
			return err
		}
	}
	return nil
}

// Layout returns the layout of asrsID.
func (s *Service) Layout(asrsID string) (layout.Layout, error) {
	l, ok := s.layouts[asrsID]
	if !ok {
		return layout.Layout{}, ErrUnknownAsrs
	}
	return l, nil
}

func (s *Service) newJobID() string { return "sj-" + s.d.IDs.NewID() }

func portOf(l layout.Layout, id string, kind layout.PortKind) (layout.Port, error) {
	p, ok := l.Port(id)
	if !ok {
		return layout.Port{}, ErrUnknownPort
	}
	if p.Kind != kind {
		return layout.Port{}, ErrWrongPortKind
	}
	return p, nil
}

// retrievePort is the requested output port, or the aisle's first one.
func retrievePort(l layout.Layout, aisle int, id string) (layout.Port, error) {
	if id == "" {
		return l.OutputPorts(aisle)[0], nil
	}
	p, err := portOf(l, id, layout.PortOutput)
	if err != nil {
		return layout.Port{}, err
	}
	if p.Aisle != aisle {
		return layout.Port{}, ErrPortUnreachable
	}
	return p, nil
}
