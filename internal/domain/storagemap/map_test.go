package storagemap_test

import (
	"errors"
	"testing"

	"github.com/IQVO/asrs-control/internal/domain/layout"
	"github.com/IQVO/asrs-control/internal/domain/storagemap"
)

// tiny is 1 aisle, 1 level, 2 slots, double-deep: 4 positions, the e2e layout.
func tiny() layout.Layout {
	return layout.Layout{ID: "ASRS1", Aisles: 1, Levels: 1, Slots: 2, DoubleDeep: true, Cost: layout.Cost{PerLevel: 1, PerSlot: 1},
		Ports: []layout.Port{{ID: "IN1", Aisle: 1, Kind: layout.PortInput}, {ID: "OUT1", Aisle: 1, Kind: layout.PortOutput}}}
}

// grid is 1 aisle, 2 levels, 3 slots, double-deep: 12 positions.
func grid(cost layout.Cost) layout.Layout {
	l := tiny()
	l.Levels, l.Slots, l.Cost = 2, 3, cost
	return l
}

func at(level, slot, depth int) layout.Position {
	return layout.Position{Aisle: 1, Level: level, Slot: slot, Depth: depth}
}

// stored puts container c into the rack through a completed store job.
func stored(t *testing.T, m *storagemap.Map, job, c string, v storagemap.Velocity) layout.Position {
	t.Helper()
	p, err := m.ReserveStore(job, c, v)
	if err != nil {
		t.Fatalf("ReserveStore(%s): %v", c, err)
	}
	if err := m.CompleteStore(job, c, p); err != nil {
		t.Fatalf("CompleteStore(%s): %v", c, err)
	}
	return p
}

func TestANewMapIsEmptyWithItsOutputPortsFree(t *testing.T) {
	m := storagemap.New(tiny(), 1)
	if m.Aisle() != 1 || m.Occupied() != 0 || m.Free() != 4 {
		t.Fatalf("aisle=%d occupied=%d free=%d, want 1/0/4", m.Aisle(), m.Occupied(), m.Free())
	}
	if !m.PortFree("OUT1") || m.PortFree("IN1") || m.PortFree("NOPE") {
		t.Error("only OUT1 is an output port of aisle 1")
	}
	if _, ok := m.Position("C1"); ok {
		t.Error("an empty map knows a container")
	}
}

func TestFreeCountsOccupiedAndReservedPositions(t *testing.T) {
	m := storagemap.New(tiny(), 1)
	stored(t, m, "j1", "C1", storagemap.Slow)
	if _, err := m.ReserveStore("j2", "C2", storagemap.Slow); err != nil {
		t.Fatal(err)
	}
	if m.Occupied() != 1 || m.Free() != 2 {
		t.Fatalf("occupied=%d free=%d, want 1/2", m.Occupied(), m.Free())
	}
}

func TestBlockerIsTheFrontContainerOfABackPosition(t *testing.T) {
	m := storagemap.New(tiny(), 1)
	stored(t, m, "j1", "A", storagemap.Slow) // A1-L1-S2-D2
	if c, blocked := m.Blocker(at(1, 2, 2)); blocked || c != "" {
		t.Fatalf("empty front blocks: %q", c)
	}
	stored(t, m, "j2", "B", storagemap.Slow) // A1-L1-S1-D2
	stored(t, m, "j3", "C", storagemap.Slow) // A1-L1-S2-D1, front of A
	if c, blocked := m.Blocker(at(1, 2, 2)); !blocked || c != "C" {
		t.Fatalf("Blocker(S2D2) = %q, %v, want C, true", c, blocked)
	}
	if _, blocked := m.Blocker(at(1, 2, 1)); blocked {
		t.Error("a front position is never blocked")
	}
}

func TestSnapshotRoundTrips(t *testing.T) {
	m := storagemap.New(tiny(), 1)
	stored(t, m, "j1", "A", storagemap.Slow)
	stored(t, m, "j2", "B", storagemap.Slow)
	if err := m.ReserveRetrieve("j3", "A"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ReserveStore("j4", "D", storagemap.Fast); err != nil {
		t.Fatal(err)
	}
	s := m.Snapshot()
	back, err := storagemap.Rehydrate(tiny(), s)
	if err != nil {
		t.Fatal(err)
	}
	got := back.Snapshot()
	if len(got.Occupied) != 2 || len(got.Reserved) != 1 || got.Retrieving["A"] != "j3" || got.Presented["OUT1"] != "" {
		t.Fatalf("round trip lost state: %+v", got)
	}
	s.Occupied[at(1, 1, 1)] = "Z" // the snapshot is a copy
	if _, ok := m.Position("Z"); ok {
		t.Error("Snapshot shares its maps with the aggregate")
	}
}

// A SLOW store reserves a back behind a free front (rule D1 allows it): the snapshot of that
// state must rehydrate, with the reservation intact.
func TestRehydrateKeepsAReservedBackBehindAFreeFront(t *testing.T) {
	m := storagemap.New(tiny(), 1)
	p, err := m.ReserveStore("j1", "C", storagemap.Slow)
	if err != nil || p != at(1, 2, 2) {
		t.Fatalf("ReserveStore = %s, %v; want A1-L1-S2-D2", p, err)
	}
	back, err := storagemap.Rehydrate(tiny(), m.Snapshot())
	if err != nil {
		t.Fatalf("Rehydrate of a reserved back behind a free front = %v, want nil", err)
	}
	if got := back.Snapshot(); got.Reserved[at(1, 2, 2)] != "j1" || got.ReservedContainer[at(1, 2, 2)] != "C" || back.Free() != 3 {
		t.Fatalf("rehydrated reservation = %+v, free %d; want j1/C on A1-L1-S2-D2, free 3", got, back.Free())
	}
}

func reserve(s *storagemap.Snapshot, p layout.Position, job, container string) {
	if s.ReservedContainer == nil {
		s.ReservedContainer = map[layout.Position]string{}
	}
	s.Reserved[p] = job
	s.ReservedContainer[p] = container
}

func TestRehydrateRefusesAContainerOnTwoPorts(t *testing.T) {
	l := tiny()
	l.Ports = append(l.Ports, layout.Port{ID: "OUT2", Aisle: 1, Kind: layout.PortOutput})
	s := storagemap.Snapshot{Aisle: 1, Occupied: map[layout.Position]string{}, Reserved: map[layout.Position]string{},
		Retrieving: map[string]string{}, Moving: map[string]string{}, Presented: map[string]string{"OUT1": "C", "OUT2": "C"}}
	if _, err := storagemap.Rehydrate(l, s); !errors.Is(err, storagemap.ErrCorruptSnapshot) {
		t.Fatalf("Rehydrate = %v, want ErrCorruptSnapshot", err)
	}
}

func TestRehydrateRefusesAnInconsistentSnapshot(t *testing.T) {
	base := func() storagemap.Snapshot {
		return storagemap.Snapshot{Aisle: 1, Occupied: map[layout.Position]string{}, Reserved: map[layout.Position]string{},
			Retrieving: map[string]string{}, Moving: map[string]string{}, Presented: map[string]string{"OUT1": ""}}
	}
	cases := map[string]func(*storagemap.Snapshot){
		"aisle zero":                   func(s *storagemap.Snapshot) { s.Aisle = 0 },
		"aisle beyond the layout":      func(s *storagemap.Snapshot) { s.Aisle = 2 },
		"position outside the rack":    func(s *storagemap.Snapshot) { s.Occupied[at(1, 3, 1)] = "C" },
		"position of another aisle":    func(s *storagemap.Snapshot) { s.Occupied[layout.Position{Aisle: 2, Level: 1, Slot: 1, Depth: 1}] = "C" },
		"empty container":              func(s *storagemap.Snapshot) { s.Occupied[at(1, 1, 1)] = "" },
		"container in two positions":   func(s *storagemap.Snapshot) { s.Occupied[at(1, 1, 1)] = "C"; s.Occupied[at(1, 1, 2)] = "C" },
		"reservation of an occupied":   func(s *storagemap.Snapshot) { s.Occupied[at(1, 1, 1)] = "C"; s.Reserved[at(1, 1, 1)] = "j" },
		"reservation outside the rack": func(s *storagemap.Snapshot) { s.Reserved[at(1, 1, 3)] = "j" },
		"reservation without a job":    func(s *storagemap.Snapshot) { s.Reserved[at(1, 1, 1)] = "" },
		"retrieve of a missing box":    func(s *storagemap.Snapshot) { s.Retrieving["C"] = "j" },
		"move of a missing box":        func(s *storagemap.Snapshot) { s.Moving["C"] = "j" },
		"retrieve without a job":       func(s *storagemap.Snapshot) { s.Occupied[at(1, 1, 1)] = "C"; s.Retrieving["C"] = "" },
		"presentation at an input":     func(s *storagemap.Snapshot) { s.Presented["IN1"] = "C" },
		"reserved back behind an occupied front": func(s *storagemap.Snapshot) {
			s.Occupied[at(1, 1, 1)] = "C"
			reserve(s, at(1, 1, 2), "j", "D")
		},
		"reserved back behind a reserved front": func(s *storagemap.Snapshot) {
			reserve(s, at(1, 1, 1), "j", "C")
			reserve(s, at(1, 1, 2), "k", "D")
		},
		"reserved front before a leaving container": func(s *storagemap.Snapshot) {
			s.Occupied[at(1, 1, 2)] = "C"
			s.Retrieving["C"] = "j"
			reserve(s, at(1, 1, 1), "k", "D")
		},
		"reservation without a container": func(s *storagemap.Snapshot) { s.Reserved[at(1, 1, 1)] = "j" },
		"container in the rack and on a port": func(s *storagemap.Snapshot) {
			s.Occupied[at(1, 1, 1)] = "C"
			s.Presented["OUT1"] = "C"
		},
		"moving container at the back": func(s *storagemap.Snapshot) {
			s.Occupied[at(1, 1, 2)] = "C"
			s.Moving["C"] = "j"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := base()
			mutate(&s)
			if _, err := storagemap.Rehydrate(tiny(), s); !errors.Is(err, storagemap.ErrCorruptSnapshot) {
				t.Fatalf("Rehydrate = %v, want ErrCorruptSnapshot", err)
			}
		})
	}
}
