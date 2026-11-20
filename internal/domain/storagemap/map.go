// Package storagemap is the StorageMap aggregate, one per aisle (asrs-control context page,
// "Candidate aggregates"). It owns which container sits in which rack position, which
// positions are reserved for a job in flight, which containers are leaving (a retrieve) or
// moving (a reshuffle), and which container waits on each output port of the aisle. It
// protects invariants 1, 2 and 6: one container per position and one position per container,
// a reserved position is never assigned, internal moves change only this map.
package storagemap

import (
	"errors"
	"fmt"
	"maps"

	"github.com/IQVO/asrs-control/internal/domain/layout"
)

// Velocity is how often a container is retrieved; it picks the preferred depth and cost.
type Velocity string

const (
	Fast Velocity = "FAST"
	Slow Velocity = "SLOW"
)

var (
	ErrContainerInRack    = errors.New("storagemap: container is already in the rack")
	ErrContainerNotInRack = errors.New("storagemap: container is not in this aisle")
	ErrContainerBusy      = errors.New("storagemap: container already has a job in progress")
	ErrNoFreePosition     = errors.New("storagemap: no assignable rack position in the aisle")
	ErrNotReserved        = errors.New("storagemap: position is not reserved by this job")
	ErrNotAtPosition      = errors.New("storagemap: container is not at that position")
	ErrUnknownPort        = errors.New("storagemap: not an output port of this aisle")
	ErrNotPresenting      = errors.New("storagemap: output port does not hold that container")
	ErrNotBlocked         = errors.New("storagemap: position is not blocked")
	ErrCorruptSnapshot    = errors.New("storagemap: snapshot is inconsistent with the layout")
)

// Map is the StorageMap of one aisle.
type Map struct {
	l          layout.Layout
	aisle      int
	slots      map[layout.Position]string // position -> container
	where      map[string]layout.Position // container -> position
	reserved   map[layout.Position]string // position -> job id
	reservedBy map[layout.Position]string // position -> container the reservation is for
	retrieving map[string]string          // container -> retrieve job id
	moving     map[string]string          // container -> reshuffle job id
	presented  map[string]string          // output port -> container ("" = free)
}

// New is the empty map of one aisle.
func New(l layout.Layout, aisle int) *Map {
	m := &Map{l: l, aisle: aisle,
		slots: map[layout.Position]string{}, where: map[string]layout.Position{},
		reserved: map[layout.Position]string{}, reservedBy: map[layout.Position]string{}, retrieving: map[string]string{},
		moving: map[string]string{}, presented: map[string]string{}}
	for _, p := range l.OutputPorts(aisle) {
		m.presented[p.ID] = ""
	}
	return m
}

// Snapshot is the persisted state of a map.
type Snapshot struct {
	Aisle    int
	Occupied map[layout.Position]string
	Reserved map[layout.Position]string
	// ReservedContainer is the container each reserved position is held for.
	ReservedContainer map[layout.Position]string
	Retrieving        map[string]string
	Moving            map[string]string
	Presented         map[string]string
}

// Snapshot copies the map's state.
func (m *Map) Snapshot() Snapshot {
	return Snapshot{Aisle: m.aisle, Occupied: maps.Clone(m.slots), Reserved: maps.Clone(m.reserved),
		ReservedContainer: maps.Clone(m.reservedBy),
		Retrieving:        maps.Clone(m.retrieving), Moving: maps.Clone(m.moving), Presented: maps.Clone(m.presented)}
}

// Rehydrate rebuilds a map and refuses a snapshot the layout cannot hold.
func Rehydrate(l layout.Layout, s Snapshot) (*Map, error) {
	if s.Aisle < 1 || s.Aisle > l.Aisles {
		return nil, fmt.Errorf("%w: aisle %d is not in %s", ErrCorruptSnapshot, s.Aisle, l.ID)
	}
	m := New(l, s.Aisle)
	if err := m.restoreSlots(s); err != nil {
		return nil, err
	}
	if err := m.restoreJobs(s); err != nil {
		return nil, err
	}
	if err := m.restorePorts(s); err != nil {
		return nil, err
	}
	if err := m.checkConsistent(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Map) restorePorts(s Snapshot) error {
	seen := map[string]bool{}
	for port, c := range s.Presented {
		if _, ok := m.presented[port]; !ok {
			return fmt.Errorf("%w: %s is not an output port of aisle %d", ErrCorruptSnapshot, port, s.Aisle)
		}
		if c != "" {
			if _, in := m.where[c]; in || seen[c] {
				return fmt.Errorf("%w: container %s is in the rack or on two ports", ErrCorruptSnapshot, c)
			}
			seen[c] = true
		}
		m.presented[port] = c
	}
	return nil
}

// checkConsistent refuses states the aggregate cannot produce: depth rules D1 and D2 broken
// by a reservation, and a reshuffle of a container that is not in front.
func (m *Map) checkConsistent() error {
	for p := range m.reserved {
		if p.Depth == 2 && (m.slots[p.Front()] != "" || m.reserved[p.Front()] != "") {
			return fmt.Errorf("%w: %s is reserved behind an occupied or reserved front", ErrCorruptSnapshot, p)
		}
		if p.Depth == 1 && m.l.DoubleDeep {
			b := p.Back()
			if c := m.slots[b]; m.reserved[b] != "" || m.retrieving[c] != "" || m.moving[c] != "" {
				return fmt.Errorf("%w: %s is reserved before a reserved or leaving back", ErrCorruptSnapshot, p)
			}
		}
	}
	for c := range m.moving {
		if m.where[c].Depth != 1 {
			return fmt.Errorf("%w: container %s is moving but not in a front position", ErrCorruptSnapshot, c)
		}
	}
	return nil
}

func (m *Map) restoreSlots(s Snapshot) error {
	for p, c := range s.Occupied {
		if !m.inAisle(p) || c == "" {
			return fmt.Errorf("%w: occupied position %s", ErrCorruptSnapshot, p)
		}
		if _, dup := m.where[c]; dup {
			return fmt.Errorf("%w: container %s is in two positions", ErrCorruptSnapshot, c)
		}
		m.place(c, p)
	}
	for p, j := range s.Reserved {
		if !m.inAisle(p) || j == "" || m.slots[p] != "" || s.ReservedContainer[p] == "" {
			return fmt.Errorf("%w: reserved position %s", ErrCorruptSnapshot, p)
		}
		m.reserved[p], m.reservedBy[p] = j, s.ReservedContainer[p]
	}
	return nil
}

func (m *Map) restoreJobs(s Snapshot) error {
	for _, set := range []struct{ from, to map[string]string }{{s.Retrieving, m.retrieving}, {s.Moving, m.moving}} {
		for c, j := range set.from {
			if _, in := m.where[c]; !in || j == "" {
				return fmt.Errorf("%w: container %s has a job but is not in the rack", ErrCorruptSnapshot, c)
			}
			set.to[c] = j
		}
	}
	return nil
}

// Aisle is the aisle this map belongs to.
func (m *Map) Aisle() int { return m.aisle }

// Position is where container sits, if it is in this aisle's rack.
func (m *Map) Position(container string) (layout.Position, bool) {
	p, ok := m.where[container]
	return p, ok
}

// Occupied is the number of containers in the aisle's rack.
func (m *Map) Occupied() int { return len(m.slots) }

// Free is the number of positions neither occupied nor reserved.
func (m *Map) Free() int { return m.l.PositionsPerAisle() - len(m.slots) - len(m.reserved) }

// Presented is the container waiting on an output port ("" when free).
func (m *Map) Presented(port string) string { return m.presented[port] }

// PortFree reports whether port is an output port of this aisle with no container on it.
func (m *Map) PortFree(port string) bool {
	c, ok := m.presented[port]
	return ok && c == ""
}

// Blocker is the container in front of a back position, if any.
func (m *Map) Blocker(p layout.Position) (string, bool) {
	if p.Depth != 2 {
		return "", false
	}
	c := m.slots[p.Front()]
	return c, c != ""
}

func (m *Map) inAisle(p layout.Position) bool { return p.Aisle == m.aisle && m.l.Contains(p) }

func (m *Map) place(c string, p layout.Position) { m.slots[p], m.where[c] = c, p }

func (m *Map) remove(p layout.Position) {
	delete(m.where, m.slots[p])
	delete(m.slots, p)
}
