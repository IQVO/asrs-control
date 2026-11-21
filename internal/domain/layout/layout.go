// Package layout is the physical structure of one AS/RS as asrs-control knows it:
// aisles, levels, slots, rack depth, ports and the travel-cost weights used to rank
// positions. It is configuration (ADR 0002), validated once at boot.
package layout

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ErrInvalidLayout wraps every reason a layout is refused.
var ErrInvalidLayout = errors.New("invalid AS/RS layout")

// idPattern bounds AS/RS and port ids: they are MQTT topic segments and REST path segments.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

// PortKind is INPUT (a container is stored from it) or OUTPUT (a container is presented at it).
type PortKind string

const (
	PortInput  PortKind = "INPUT"
	PortOutput PortKind = "OUTPUT"
)

// Port is one input or output point of one aisle.
type Port struct {
	ID    string
	Aisle int
	Kind  PortKind
}

// Position is one rack position. Depth 1 is the front, 2 the back (double-deep racks only).
type Position struct{ Aisle, Level, Slot, Depth int }

func (p Position) String() string {
	return fmt.Sprintf("A%d-L%d-S%d-D%d", p.Aisle, p.Level, p.Slot, p.Depth)
}

// Front is the front position of p's slot.
func (p Position) Front() Position { p.Depth = 1; return p }

// Back is the back position of p's slot.
func (p Position) Back() Position { p.Depth = 2; return p }

// IsZero reports whether p is the zero value (no position).
func (p Position) IsZero() bool { return p == (Position{}) }

// Cost weighs a position's travel time: PerLevel*level + PerSlot*slot.
type Cost struct{ PerLevel, PerSlot float64 }

// Layout is one AS/RS.
type Layout struct {
	ID                    string
	Aisles, Levels, Slots int
	DoubleDeep            bool
	Ports                 []Port
	Cost                  Cost
}

// Validate refuses a layout asrs-control cannot run.
func (l Layout) Validate() error {
	if !idPattern.MatchString(l.ID) {
		return fmt.Errorf("%w: id %q must be 1 to 32 letters, digits, '-' or '_'", ErrInvalidLayout, l.ID)
	}
	if l.Aisles < 1 || l.Levels < 1 || l.Slots < 1 {
		return fmt.Errorf("%w: %s: aisles, levels and slots must be at least 1", ErrInvalidLayout, l.ID)
	}
	if l.Cost.PerLevel < 0 || l.Cost.PerSlot < 0 {
		return fmt.Errorf("%w: %s: cost weights must not be negative", ErrInvalidLayout, l.ID)
	}
	return l.validatePorts()
}

// aisleKind is one aisle's ports of one kind.
type aisleKind struct {
	aisle int
	kind  PortKind
}

func (l Layout) validatePorts() error {
	seen := map[string]bool{}
	// Keyed by aisle AND kind: an if/else on the kind filling an in-set and an out-set made
	// its negation an equivalent mutant (the both-kinds check below is symmetric). MUTATION.md.
	has := map[aisleKind]bool{}
	for _, p := range l.Ports {
		// An if-chain, not a tagless switch: Go's cover profile does not instrument `case`
		// expressions, so gremlins would report their mutants NOT COVERED (MUTATION.md).
		if !idPattern.MatchString(p.ID) {
			return fmt.Errorf("%w: %s: port id %q must be 1 to 32 letters, digits, '-' or '_'", ErrInvalidLayout, l.ID, p.ID)
		}
		if seen[p.ID] {
			return fmt.Errorf("%w: %s: port %s is declared twice", ErrInvalidLayout, l.ID, p.ID)
		}
		if p.Aisle < 1 || p.Aisle > l.Aisles {
			return fmt.Errorf("%w: %s: port %s is outside the aisles", ErrInvalidLayout, l.ID, p.ID)
		}
		if p.Kind != PortInput && p.Kind != PortOutput {
			return fmt.Errorf("%w: %s: port %s has kind %q, want INPUT or OUTPUT", ErrInvalidLayout, l.ID, p.ID, p.Kind)
		}
		seen[p.ID] = true
		has[aisleKind{p.Aisle, p.Kind}] = true
	}
	for a := 1; a <= l.Aisles; a++ {
		if !has[aisleKind{a, PortInput}] || !has[aisleKind{a, PortOutput}] {
			return fmt.Errorf("%w: %s: aisle %d needs at least one INPUT and one OUTPUT port", ErrInvalidLayout, l.ID, a)
		}
	}
	return nil
}

// MaxDepth is 2 for a double-deep rack, else 1.
func (l Layout) MaxDepth() int {
	if l.DoubleDeep {
		return 2
	}
	return 1
}

// Contains reports whether p is a position of this AS/RS.
func (l Layout) Contains(p Position) bool {
	return p.Aisle >= 1 && p.Aisle <= l.Aisles && p.Level >= 1 && p.Level <= l.Levels &&
		p.Slot >= 1 && p.Slot <= l.Slots && p.Depth >= 1 && p.Depth <= l.MaxDepth()
}

// Port returns the port with id.
func (l Layout) Port(id string) (Port, bool) {
	for _, p := range l.Ports {
		if p.ID == id {
			return p, true
		}
	}
	return Port{}, false
}

// InputPorts are the aisle's INPUT ports in declaration order.
func (l Layout) InputPorts(aisle int) []Port { return l.portsOf(aisle, PortInput) }

// OutputPorts are the aisle's OUTPUT ports in declaration order.
func (l Layout) OutputPorts(aisle int) []Port { return l.portsOf(aisle, PortOutput) }

func (l Layout) portsOf(aisle int, kind PortKind) []Port {
	var out []Port
	for _, p := range l.Ports {
		if p.Aisle == aisle && p.Kind == kind {
			out = append(out, p)
		}
	}
	return out
}

// AislePositions lists every position of one aisle: level by level, slot by slot, front first.
func (l Layout) AislePositions(aisle int) []Position {
	out := make([]Position, 0, l.PositionsPerAisle())
	for lv := 1; lv <= l.Levels; lv++ {
		for s := 1; s <= l.Slots; s++ {
			for d := 1; d <= l.MaxDepth(); d++ {
				out = append(out, Position{Aisle: aisle, Level: lv, Slot: s, Depth: d})
			}
		}
	}
	return out
}

// PositionsPerAisle is the number of rack positions in one aisle.
func (l Layout) PositionsPerAisle() int { return l.Levels * l.Slots * l.MaxDepth() }

// TotalPositions is the number of rack positions in the AS/RS.
func (l Layout) TotalPositions() int { return l.Aisles * l.PositionsPerAisle() }

// CostOf is the travel cost of p.
func (l Layout) CostOf(p Position) float64 {
	return l.Cost.PerLevel*float64(p.Level) + l.Cost.PerSlot*float64(p.Slot)
}

// UnitID is the telegram unit id of an aisle's shuttle-and-lift unit ("A1").
func (l Layout) UnitID(aisle int) string { return "A" + strconv.Itoa(aisle) }

// AisleOfUnit parses a telegram unit id back to its aisle. The number is parsed as 32-bit:
// an aisle is persisted as int32, so a larger id is refused here rather than wrapped later.
func (l Layout) AisleOfUnit(unitID string) (int, bool) {
	rest, ok := strings.CutPrefix(unitID, "A")
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseInt(rest, 10, 32)
	if err != nil || v < 1 || v > int64(l.Aisles) {
		return 0, false
	}
	return int(v), true
}
