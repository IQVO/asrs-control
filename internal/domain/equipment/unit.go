// Package equipment is the EquipmentUnit aggregate: the combined shuttle-and-lift unit of one
// aisle as the PLC reports it (unit id "A<aisle>"). It protects invariant 3, one job at a
// time, and tracks the unit's PackML state. Telegram protocol v1 reports IDLE, EXECUTE and
// HELD; SUSPENDED, STOPPED and ABORTED are not sent by the PLC and are refused.
package equipment

import (
	"errors"
	"fmt"
	"time"
)

type State string

const (
	Idle    State = "IDLE"
	Execute State = "EXECUTE"
	Held    State = "HELD"
)

var (
	ErrUnknownState = errors.New("equipment: unknown unit state")
	ErrUnitBusy     = errors.New("equipment: the unit already runs a job")
)

// ParseState reads a UNIT_STATE telegram's state.
func ParseState(s string) (State, error) {
	switch State(s) {
	case Idle, Execute, Held:
		return State(s), nil
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownState, s)
}

// UnitState is the persisted state of a unit.
type UnitState struct {
	AsrsID, UnitID string
	Aisle          int
	State          State
	CurrentJob     string
	Since          time.Time
}

// Unit is one aisle's shuttle-and-lift unit.
type Unit struct{ s UnitState }

// EquipmentStateChanged is raised when a UNIT_STATE changes a unit's state.
type EquipmentStateChanged struct {
	AsrsID, UnitID  string
	Aisle           int
	State, Previous State
	At, PLCTime     time.Time
}

// New is an IDLE unit with no job.
func New(asrsID, unitID string, aisle int, at time.Time) *Unit {
	return &Unit{s: UnitState{AsrsID: asrsID, UnitID: unitID, Aisle: aisle, State: Idle, Since: at}}
}

// Rehydrate rebuilds a unit from its persisted state.
func Rehydrate(s UnitState) *Unit { return &Unit{s: s} }

func (u *Unit) Snapshot() UnitState { return u.s }
func (u *Unit) AsrsID() string      { return u.s.AsrsID }
func (u *Unit) ID() string          { return u.s.UnitID }
func (u *Unit) Aisle() int          { return u.s.Aisle }
func (u *Unit) State() State        { return u.s.State }
func (u *Unit) CurrentJob() string  { return u.s.CurrentJob }

// Busy reports whether a job is with the PLC for this unit.
func (u *Unit) Busy() bool { return u.s.CurrentJob != "" }

// Available reports whether the unit can take work (it is not HELD).
func (u *Unit) Available() bool { return u.s.State != Held }

// Assign gives the unit its one job.
func (u *Unit) Assign(jobID string) error {
	if u.s.CurrentJob != "" || jobID == "" {
		return ErrUnitBusy
	}
	u.s.CurrentJob = jobID
	return nil
}

// Release frees the unit when jobID is its current job.
func (u *Unit) Release(jobID string) bool {
	if jobID == "" || u.s.CurrentJob != jobID {
		return false
	}
	u.s.CurrentJob = ""
	return true
}

// Report applies a UNIT_STATE; it returns the event when the state changed.
func (u *Unit) Report(s State, at, plcTime time.Time) (EquipmentStateChanged, bool) {
	if s == u.s.State {
		return EquipmentStateChanged{}, false
	}
	ev := EquipmentStateChanged{AsrsID: u.s.AsrsID, UnitID: u.s.UnitID, Aisle: u.s.Aisle, State: s, Previous: u.s.State, At: at, PLCTime: plcTime}
	u.s.State, u.s.Since = s, at
	return ev, true
}
