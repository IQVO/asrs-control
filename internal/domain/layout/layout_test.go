package layout_test

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/IQVO/asrs-control/internal/domain/layout"
)

func sample() layout.Layout {
	return layout.Layout{ID: "ASRS1", Aisles: 2, Levels: 3, Slots: 4, DoubleDeep: true,
		Cost: layout.Cost{PerLevel: 1.5, PerSlot: 0.2},
		Ports: []layout.Port{
			{ID: "IN1", Aisle: 1, Kind: layout.PortInput}, {ID: "OUT1", Aisle: 1, Kind: layout.PortOutput},
			{ID: "IN2", Aisle: 2, Kind: layout.PortInput}, {ID: "OUT2", Aisle: 2, Kind: layout.PortOutput},
		}}
}

func TestValidLayoutPasses(t *testing.T) {
	if err := sample().Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestBoundaryValuesAreAccepted(t *testing.T) {
	l := sample()
	l.ID = strings.Repeat("A", 32)
	l.Cost = layout.Cost{}
	l.Aisles, l.Levels, l.Slots = 1, 1, 1
	l.Ports = l.Ports[:2]
	if err := l.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil for 32-char id, zero cost, 1x1x1", err)
	}
}

func TestValidateRefuses(t *testing.T) {
	cases := map[string]func(*layout.Layout){
		"empty id":                     func(l *layout.Layout) { l.ID = "" },
		"id with a slash":              func(l *layout.Layout) { l.ID = "AS/RS" },
		"id of 33 characters":          func(l *layout.Layout) { l.ID = strings.Repeat("A", 33) },
		"zero aisles":                  func(l *layout.Layout) { l.Aisles = 0 },
		"zero levels":                  func(l *layout.Layout) { l.Levels = 0 },
		"zero slots":                   func(l *layout.Layout) { l.Slots = 0 },
		"negative level cost":          func(l *layout.Layout) { l.Cost.PerLevel = -0.1 },
		"negative slot cost":           func(l *layout.Layout) { l.Cost.PerSlot = -0.1 },
		"port without id":              func(l *layout.Layout) { l.Ports[0].ID = "" },
		"port id with a plus":          func(l *layout.Layout) { l.Ports[0].ID = "IN+1" },
		"duplicate port":               func(l *layout.Layout) { l.Ports[1].ID = "IN1" },
		"port aisle zero":              func(l *layout.Layout) { l.Ports[0].Aisle = 0 },
		"port beyond the last aisle":   func(l *layout.Layout) { l.Ports[3].Aisle = 3 },
		"unknown port kind":            func(l *layout.Layout) { l.Ports[0].Kind = "SIDE" },
		"aisle without an output port": func(l *layout.Layout) { l.Ports = l.Ports[:3] },
		"aisle without an input port":  func(l *layout.Layout) { l.Ports = []layout.Port{l.Ports[0], l.Ports[1], l.Ports[3]} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			l := sample()
			l.Ports = append([]layout.Port(nil), l.Ports...)
			mutate(&l)
			if err := l.Validate(); !errors.Is(err, layout.ErrInvalidLayout) {
				t.Fatalf("Validate() = %v, want ErrInvalidLayout", err)
			}
		})
	}
}

func TestContainsIsInclusiveAtEveryBound(t *testing.T) {
	l := sample()
	for _, p := range []layout.Position{{1, 1, 1, 1}, {2, 3, 4, 2}} {
		if !l.Contains(p) {
			t.Errorf("Contains(%s) = false, want true", p)
		}
	}
	for _, p := range []layout.Position{{0, 1, 1, 1}, {3, 1, 1, 1}, {1, 0, 1, 1}, {1, 4, 1, 1}, {1, 1, 0, 1}, {1, 1, 5, 1}, {1, 1, 1, 0}, {1, 1, 1, 3}} {
		if l.Contains(p) {
			t.Errorf("Contains(%s) = true, want false", p)
		}
	}
	single := sample()
	single.DoubleDeep = false
	if single.Contains(layout.Position{Aisle: 1, Level: 1, Slot: 1, Depth: 2}) || !single.Contains(layout.Position{Aisle: 1, Level: 1, Slot: 1, Depth: 1}) {
		t.Error("a single-deep rack has depth 1 only")
	}
	if single.MaxDepth() != 1 || l.MaxDepth() != 2 {
		t.Errorf("MaxDepth = %d/%d, want 1/2", single.MaxDepth(), l.MaxDepth())
	}
}

func TestAislePositionsAreOrderedLevelSlotDepth(t *testing.T) {
	l := sample()
	ps := l.AislePositions(2)
	if len(ps) != 24 || l.PositionsPerAisle() != 24 || l.TotalPositions() != 48 {
		t.Fatalf("len=%d perAisle=%d total=%d, want 24/24/48", len(ps), l.PositionsPerAisle(), l.TotalPositions())
	}
	want := map[int]layout.Position{0: {2, 1, 1, 1}, 1: {2, 1, 1, 2}, 2: {2, 1, 2, 1}, 8: {2, 2, 1, 1}, 23: {2, 3, 4, 2}}
	for i, p := range want {
		if ps[i] != p {
			t.Errorf("AislePositions(2)[%d] = %s, want %s", i, ps[i], p)
		}
	}
}

func TestCostOfWeighsLevelAndSlot(t *testing.T) {
	got := sample().CostOf(layout.Position{Aisle: 1, Level: 2, Slot: 3, Depth: 1})
	if math.Abs(got-3.6) > 1e-9 {
		t.Fatalf("CostOf = %v, want 3.6 (1.5*2 + 0.2*3)", got)
	}
}

func TestPortLookups(t *testing.T) {
	l := sample()
	if p, ok := l.Port("OUT2"); !ok || p.Aisle != 2 || p.Kind != layout.PortOutput {
		t.Errorf("Port(OUT2) = %+v, %v", p, ok)
	}
	if _, ok := l.Port("NOPE"); ok {
		t.Error("Port(NOPE) found")
	}
	if out := l.OutputPorts(1); len(out) != 1 || out[0].ID != "OUT1" {
		t.Errorf("OutputPorts(1) = %+v", out)
	}
	if in := l.InputPorts(2); len(in) != 1 || in[0].ID != "IN2" {
		t.Errorf("InputPorts(2) = %+v", in)
	}
}

func TestUnitIDsRoundTrip(t *testing.T) {
	l := sample()
	if l.UnitID(2) != "A2" {
		t.Errorf("UnitID(2) = %q", l.UnitID(2))
	}
	if n, ok := l.AisleOfUnit("A2"); !ok || n != 2 {
		t.Errorf("AisleOfUnit(A2) = %d, %v", n, ok)
	}
	for _, id := range []string{"A0", "A3", "B1", "A", "1", "A1x"} {
		if _, ok := l.AisleOfUnit(id); ok {
			t.Errorf("AisleOfUnit(%q) accepted", id)
		}
	}
}

func TestPositionHelpers(t *testing.T) {
	p := layout.Position{Aisle: 1, Level: 2, Slot: 3, Depth: 2}
	if p.String() != "A1-L2-S3-D2" || p.Front().Depth != 1 || p.Front().Back() != p {
		t.Errorf("String/Front/Back wrong: %s %s", p, p.Front())
	}
	if p.IsZero() || !(layout.Position{}).IsZero() {
		t.Error("IsZero wrong")
	}
}
