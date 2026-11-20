// Package ports are the application's outbound ports: repositories, the unit of work, the
// clock, ids, the event sink (the outbox writer) and the PLC downlink.
package ports

import (
	"context"
	"errors"
	"time"

	"github.com/IQVO/asrs-control/internal/domain/capacity"
	"github.com/IQVO/asrs-control/internal/domain/equipment"
	"github.com/IQVO/asrs-control/internal/domain/layout"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
	"github.com/IQVO/asrs-control/internal/domain/storagemap"
)

// ErrJobNotFound is answered by StorageJobs for an unknown job or telegram.
var ErrJobNotFound = errors.New("storage job not found")

type Clock interface{ Now() time.Time }

type IDs interface{ NewID() string }

// UnitOfWork runs fn atomically: every repository call made with the ctx fn receives commits
// together or rolls back together. Do joins an outer unit of work carried by ctx (the
// Idempotency-Key middleware opens one around every POST).
//
// AfterCommit registers fn to run after the OUTERMOST unit of work carried by ctx committed,
// in registration order; the hooks are dropped when it rolls back or panics. Outside a unit of
// work fn runs at once. The use cases send PLC commands only from such a hook, so a command is
// never published for state that was not committed (and never before its ACK can find it).
type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
	AfterCommit(ctx context.Context, fn func())
}

// StorageMaps persists one StorageMap per (AS/RS, aisle). Lock takes the aisle's row lock for
// the unit of work: every write to an aisle holds it (ADR 0002).
type StorageMaps interface {
	Ensure(ctx context.Context, l layout.Layout) error
	Lock(ctx context.Context, l layout.Layout, aisle int) (*storagemap.Map, error)
	Get(ctx context.Context, l layout.Layout, aisle int) (*storagemap.Map, error)
	Save(ctx context.Context, asrsID string, m *storagemap.Map) error
	Find(ctx context.Context, asrsID, containerID string) (aisle int, found bool, err error)
}

// StorageJobs persists jobs. Insert assigns the job's Seq (acceptance order). Open lists an
// aisle's open jobs (internal ones included) in ascending Seq, that is acceptance, order:
// scheduling.Next trusts that order. HasOpen and CountOpen see external jobs only.
type StorageJobs interface {
	Insert(ctx context.Context, j *storagejob.Job) error
	Update(ctx context.Context, j *storagejob.Job) error
	Get(ctx context.Context, jobID string) (*storagejob.Job, error)
	ByTelegram(ctx context.Context, asrsID, telegramID string) (*storagejob.Job, error)
	Open(ctx context.Context, asrsID string, aisle int) ([]*storagejob.Job, error)
	OldestAwaiting(ctx context.Context, asrsID, portID string) (*storagejob.Job, error)
	HasOpen(ctx context.Context, asrsID, containerID string) (bool, error)
	CountOpen(ctx context.Context, asrsID string) (int, error)
	CountRetrievals(ctx context.Context, asrsID, containerID string, since time.Time) (int, error)
	InFlightCommands(ctx context.Context, asrsID string) ([]*storagejob.Job, error)
}

// Units persists one EquipmentUnit per aisle; Lock is taken after the aisle's map lock.
type Units interface {
	Ensure(ctx context.Context, l layout.Layout, at time.Time) error
	Lock(ctx context.Context, asrsID string, aisle int) (*equipment.Unit, error)
	List(ctx context.Context, asrsID string) ([]*equipment.Unit, error)
	Save(ctx context.Context, u *equipment.Unit) error
}

// Capacities holds the last published capacity snapshot per AS/RS.
type Capacities interface {
	Lock(ctx context.Context, asrsID string) (capacity.Snapshot, bool, error)
	Save(ctx context.Context, s capacity.Snapshot) error
}

// EventSink writes domain events to the transactional outbox, in the caller's unit of work.
type EventSink interface {
	Append(ctx context.Context, events ...any) error
}
