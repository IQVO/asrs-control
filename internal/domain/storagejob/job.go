// Package storagejob is the StorageJob aggregate: one store or retrieve of one container
// requested by material-flow-control, or one internal reshuffle. It protects invariants 4
// and 5 of the asrs-control context page with the scheduler: every accepted job ends in
// exactly one of DONE, FAILED or CANCELLED, and only external jobs publish.
package storagejob

import (
	"errors"
	"fmt"
	"time"

	"github.com/IQVO/asrs-control/internal/domain/layout"
)

type Kind string

const (
	Store     Kind = "STORE"
	Retrieve  Kind = "RETRIEVE"
	Reshuffle Kind = "RESHUFFLE"
)

type Status string

const (
	Queued          Status = "QUEUED"
	Dispatched      Status = "DISPATCHED"
	Acknowledged    Status = "ACKNOWLEDGED"
	CancelRequested Status = "CANCEL_REQUESTED"
	AwaitingArrival Status = "AWAITING_ARRIVAL"
	Done            Status = "DONE"
	Failed          Status = "FAILED"
	Cancelled       Status = "CANCELLED"
)

var (
	ErrInvalidJob        = errors.New("storagejob: invalid job")
	ErrInvalidTransition = errors.New("storagejob: not allowed from the job's current status")
	ErrNotCancellable    = errors.New("storagejob: the container is already on its way to the port")
	ErrJobEnded          = errors.New("storagejob: the job has already ended")
)

// Spec is what a new job is created from.
type Spec struct {
	ID, AsrsID                               string
	Aisle                                    int
	ContainerID, PortID, Reference, ParentID string
	From, To                                 layout.Position
	At                                       time.Time
}

// State is the persisted state of a job.
type State struct {
	ID, AsrsID                               string
	Aisle                                    int
	Kind                                     Kind
	ContainerID, PortID, Reference, ParentID string
	From, To                                 layout.Position
	Status                                   Status
	Reason                                   Reason
	PLCReason, TelegramID, CancelTelegramID  string
	Acked                                    bool // the PLC accepted the command telegram
	Seq                                      int64
	AcceptedAt, EndedAt                      time.Time
}

// Job is one StorageJob.
type Job struct{ s State }

func newJob(k Kind, s Spec) (*Job, error) {
	if s.ID == "" || s.AsrsID == "" || s.ContainerID == "" || s.Aisle < 1 || s.At.IsZero() {
		return nil, fmt.Errorf("%w: id, asrs, aisle, container and acceptance time are required", ErrInvalidJob)
	}
	return &Job{s: State{ID: s.ID, AsrsID: s.AsrsID, Aisle: s.Aisle, Kind: k, ContainerID: s.ContainerID,
		PortID: s.PortID, Reference: s.Reference, ParentID: s.ParentID, From: s.From, To: s.To,
		Status: Queued, AcceptedAt: s.At}}, nil
}

// NewStore is a store from an input port into the position reserved for it.
func NewStore(s Spec) (*Job, error) {
	if s.PortID == "" || s.To.IsZero() || s.To.Aisle != s.Aisle {
		return nil, fmt.Errorf("%w: a store needs its input port and a reserved position in its aisle", ErrInvalidJob)
	}
	return newJob(Store, s)
}

// NewRetrieve is a retrieve to an output port; its source is read from the map at dispatch.
func NewRetrieve(s Spec) (*Job, error) {
	if s.PortID == "" || !s.From.IsZero() || !s.To.IsZero() {
		return nil, fmt.Errorf("%w: a retrieve needs its output port and no position", ErrInvalidJob)
	}
	return newJob(Retrieve, s)
}

// NewReshuffle is an internal move of a blocking container for the retrieve ParentID.
func NewReshuffle(s Spec) (*Job, error) {
	if s.ParentID == "" || s.PortID != "" || s.From.IsZero() || s.To.IsZero() || s.From.Aisle != s.Aisle || s.To.Aisle != s.Aisle {
		return nil, fmt.Errorf("%w: a reshuffle needs its parent and two positions of its aisle", ErrInvalidJob)
	}
	return newJob(Reshuffle, s)
}

// Rehydrate rebuilds a job from its persisted state.
func Rehydrate(s State) *Job { return &Job{s: s} }

// State copies the job's state.
func (j *Job) State() State { return j.s }

// AssignSeq records the acceptance order the repository gave the job; only the first call counts.
func (j *Job) AssignSeq(seq int64) {
	if j.s.Seq == 0 {
		j.s.Seq = seq
	}
}

func (j *Job) ID() string               { return j.s.ID }
func (j *Job) AsrsID() string           { return j.s.AsrsID }
func (j *Job) Aisle() int               { return j.s.Aisle }
func (j *Job) Kind() Kind               { return j.s.Kind }
func (j *Job) ContainerID() string      { return j.s.ContainerID }
func (j *Job) PortID() string           { return j.s.PortID }
func (j *Job) Reference() string        { return j.s.Reference }
func (j *Job) ParentID() string         { return j.s.ParentID }
func (j *Job) From() layout.Position    { return j.s.From }
func (j *Job) To() layout.Position      { return j.s.To }
func (j *Job) Status() Status           { return j.s.Status }
func (j *Job) Reason() Reason           { return j.s.Reason }
func (j *Job) PLCReason() string        { return j.s.PLCReason }
func (j *Job) TelegramID() string       { return j.s.TelegramID }
func (j *Job) CancelTelegramID() string { return j.s.CancelTelegramID }
func (j *Job) Acked() bool              { return j.s.Acked }
func (j *Job) Seq() int64               { return j.s.Seq }
func (j *Job) AcceptedAt() time.Time    { return j.s.AcceptedAt }
func (j *Job) EndedAt() time.Time       { return j.s.EndedAt }

// External reports whether material-flow-control requested the job (a reshuffle is internal).
func (j *Job) External() bool { return j.s.Kind != Reshuffle }

// Open reports whether the job has no outcome yet.
func (j *Job) Open() bool {
	return j.s.Status != Done && j.s.Status != Failed && j.s.Status != Cancelled
}

// InFlight reports whether the job's command is with the PLC.
func (j *Job) InFlight() bool {
	return j.s.Status == Dispatched || j.s.Status == Acknowledged || j.s.Status == CancelRequested
}
