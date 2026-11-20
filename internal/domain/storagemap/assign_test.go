package storagemap_test

import (
	"errors"
	"testing"

	"github.com/IQVO/asrs-control/internal/domain/layout"
	"github.com/IQVO/asrs-control/internal/domain/storagemap"
)

func assign(t *testing.T, m *storagemap.Map, v storagemap.Velocity) layout.Position {
	t.Helper()
	p, err := m.Assign(v, nil)
	if err != nil {
		t.Fatalf("Assign(%s): %v", v, err)
	}
	return p
}

func TestSlowTakesTheDearestBackAndFastTheCheapestFront(t *testing.T) {
	m := storagemap.New(grid(layout.Cost{PerLevel: 1, PerSlot: 1}), 1)
	if p := assign(t, m, storagemap.Slow); p != at(2, 3, 2) {
		t.Errorf("Slow = %s, want A1-L2-S3-D2", p)
	}
	if p := assign(t, m, storagemap.Fast); p != at(1, 1, 1) {
		t.Errorf("Fast = %s, want A1-L1-S1-D1", p)
	}
}

func TestCostWeightsDecideTheOrder(t *testing.T) {
	// A level costs 10, a slot 1: L1-S3 (13) is cheaper than L2-S1 (21).
	m := storagemap.New(grid(layout.Cost{PerLevel: 10, PerSlot: 1}), 1)
	stored(t, m, "j1", "X", storagemap.Fast) // L1-S1-D1
	stored(t, m, "j2", "Y", storagemap.Fast) // L1-S2-D1
	if p := assign(t, m, storagemap.Fast); p != at(1, 3, 1) {
		t.Errorf("third Fast = %s, want A1-L1-S3-D1 (cost 13 < 21)", p)
	}
	if p := assign(t, m, storagemap.Slow); p != at(2, 3, 2) {
		t.Errorf("Slow = %s, want A1-L2-S3-D2 (cost 23)", p)
	}
}

func TestZeroCostFallsBackToLowestLevelThenSlot(t *testing.T) {
	m := storagemap.New(grid(layout.Cost{}), 1)
	if p := assign(t, m, storagemap.Slow); p != at(1, 1, 2) {
		t.Errorf("Slow = %s, want A1-L1-S1-D2", p)
	}
	if p := assign(t, m, storagemap.Fast); p != at(1, 1, 1) {
		t.Errorf("Fast = %s, want A1-L1-S1-D1", p)
	}
}

func TestSingleDeepSlowUsesFrontPositions(t *testing.T) {
	l := grid(layout.Cost{PerLevel: 1, PerSlot: 1})
	l.DoubleDeep = false
	m := storagemap.New(l, 1)
	if p := assign(t, m, storagemap.Slow); p != at(2, 3, 1) {
		t.Errorf("Slow on single-deep = %s, want A1-L2-S3-D1", p)
	}
	if m.Free() != 6 {
		t.Errorf("Free = %d, want 6", m.Free())
	}
}

func TestD1ABackIsNotAssignedBehindAnOccupiedOrReservedFront(t *testing.T) {
	m := storagemap.New(tiny(), 1)
	stored(t, m, "j1", "F", storagemap.Fast) // S1-D1 (cheapest front)
	if p := assign(t, m, storagemap.Slow); p != at(1, 2, 2) {
		t.Fatalf("Slow = %s, want A1-L1-S2-D2 (S1-D2 is behind F)", p)
	}
	if _, err := m.ReserveStore("j2", "G", storagemap.Fast); err != nil { // reserves S2-D1
		t.Fatal(err)
	}
	if _, err := m.Assign(storagemap.Slow, func(p layout.Position) bool { return p.Depth == 1 }); !errors.Is(err, storagemap.ErrNoFreePosition) {
		t.Fatalf("a back behind an occupied or reserved front was assignable: %v", err)
	}
}

func TestD2AFrontIsNotAssignedBeforeAReservedBack(t *testing.T) {
	m := storagemap.New(tiny(), 1)
	if _, err := m.ReserveStore("j1", "A", storagemap.Slow); err != nil { // S2-D2
		t.Fatal(err)
	}
	if _, err := m.ReserveStore("j2", "B", storagemap.Slow); err != nil { // S1-D2
		t.Fatal(err)
	}
	if _, err := m.ReserveStore("j3", "C", storagemap.Fast); !errors.Is(err, storagemap.ErrNoFreePosition) {
		t.Fatalf("a front before a reserved back was assigned: %v", err)
	}
	if m.Free() != 2 {
		t.Errorf("Free = %d, want 2 (positions exist but none is assignable)", m.Free())
	}
}

func TestD2AFrontIsNotAssignedBeforeALeavingContainer(t *testing.T) {
	m := storagemap.New(tiny(), 1)
	stored(t, m, "j1", "A", storagemap.Slow) // S2-D2
	stored(t, m, "j2", "B", storagemap.Slow) // S1-D2
	if p := assign(t, m, storagemap.Slow); p != at(1, 2, 1) {
		t.Fatalf("Slow before the retrieve = %s, want A1-L1-S2-D1 (dearest front)", p)
	}
	if err := m.ReserveRetrieve("j3", "A"); err != nil {
		t.Fatal(err)
	}
	if p := assign(t, m, storagemap.Slow); p != at(1, 1, 1) {
		t.Fatalf("Slow while A leaves = %s, want A1-L1-S1-D1 (S2-D1 would block A)", p)
	}
	if _, _, _, err := m.PlanReshuffle("j4", at(1, 1, 2), storagemap.Fast); !errors.Is(err, storagemap.ErrNotBlocked) {
		t.Fatalf("PlanReshuffle of an unblocked position = %v", err)
	}
}

func TestAssignHonoursExclude(t *testing.T) {
	m := storagemap.New(tiny(), 1)
	p, err := m.Assign(storagemap.Slow, func(p layout.Position) bool { return p.Slot == 2 })
	if err != nil || p != at(1, 1, 2) {
		t.Fatalf("Assign excluding slot 2 = %s, %v, want A1-L1-S1-D2", p, err)
	}
}

func TestReserveStoreRefusesAContainerAlreadyInTheRack(t *testing.T) {
	m := storagemap.New(tiny(), 1)
	stored(t, m, "j1", "A", storagemap.Slow)
	if _, err := m.ReserveStore("j2", "A", storagemap.Slow); !errors.Is(err, storagemap.ErrContainerInRack) {
		t.Fatalf("ReserveStore of a stored container = %v", err)
	}
}

func TestReserveRetrieveRules(t *testing.T) {
	m := storagemap.New(tiny(), 1)
	if err := m.ReserveRetrieve("j1", "A"); !errors.Is(err, storagemap.ErrContainerNotInRack) {
		t.Fatalf("retrieve of a missing container = %v", err)
	}
	stored(t, m, "j2", "A", storagemap.Slow)
	if err := m.ReserveRetrieve("j3", "A"); err != nil {
		t.Fatal(err)
	}
	if err := m.ReserveRetrieve("j4", "A"); !errors.Is(err, storagemap.ErrContainerBusy) {
		t.Fatalf("a second retrieve = %v, want ErrContainerBusy", err)
	}
}

func TestPlanReshuffleMovesTheBlockerToAnotherSlot(t *testing.T) {
	m := storagemap.New(tiny(), 1)
	stored(t, m, "j1", "A", storagemap.Slow) // S2-D2
	stored(t, m, "j2", "B", storagemap.Slow) // S1-D2
	stored(t, m, "j3", "C", storagemap.Slow) // S2-D1, blocks A
	c, from, to, err := m.PlanReshuffle("r1", at(1, 2, 2), storagemap.Fast)
	if err != nil {
		t.Fatal(err)
	}
	if c != "C" || from != at(1, 2, 1) || to != at(1, 1, 1) {
		t.Fatalf("reshuffle = %s %s -> %s, want C A1-L1-S2-D1 -> A1-L1-S1-D1", c, from, to)
	}
	if _, _, _, err := m.PlanReshuffle("r2", at(1, 2, 2), storagemap.Fast); !errors.Is(err, storagemap.ErrContainerBusy) {
		t.Fatalf("a second reshuffle of C = %v, want ErrContainerBusy", err)
	}
}

func TestPlanReshuffleWithoutRoomFails(t *testing.T) {
	l := tiny()
	l.Slots = 1
	m := storagemap.New(l, 1)
	stored(t, m, "j1", "A", storagemap.Slow) // S1-D2
	stored(t, m, "j2", "B", storagemap.Slow) // S1-D1
	if _, _, _, err := m.PlanReshuffle("r1", at(1, 1, 2), storagemap.Fast); !errors.Is(err, storagemap.ErrNoFreePosition) {
		t.Fatalf("PlanReshuffle with no other slot = %v", err)
	}
}
