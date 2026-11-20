package storagemap_test

import (
	"errors"
	"testing"

	"github.com/IQVO/asrs-control/internal/domain/storagemap"
)

func TestCompleteStorePlacesTheContainerAndClearsTheReservation(t *testing.T) {
	m := storagemap.New(tiny(), 1)
	p, _ := m.ReserveStore("j1", "A", storagemap.Slow)
	if err := m.CompleteStore("other", "A", p); !errors.Is(err, storagemap.ErrNotReserved) {
		t.Fatalf("CompleteStore by another job = %v", err)
	}
	if err := m.CompleteStore("j1", "A", p); err != nil {
		t.Fatal(err)
	}
	if got, ok := m.Position("A"); !ok || got != p || m.Free() != 3 {
		t.Fatalf("A at %s (%v), free %d", got, ok, m.Free())
	}
}

func TestCompleteMoveRelocatesAndFreesTheContainer(t *testing.T) {
	m := storagemap.New(tiny(), 1)
	stored(t, m, "j1", "A", storagemap.Slow)
	stored(t, m, "j2", "B", storagemap.Slow)
	stored(t, m, "j3", "C", storagemap.Slow)
	c, from, to, _ := m.PlanReshuffle("r1", at(1, 2, 2), storagemap.Fast)
	if err := m.CompleteMove("r1", c, to, to); !errors.Is(err, storagemap.ErrNotAtPosition) {
		t.Fatalf("move from the wrong position = %v", err)
	}
	if err := m.CompleteMove("r2", c, from, to); !errors.Is(err, storagemap.ErrNotReserved) {
		t.Fatalf("move by another job = %v", err)
	}
	if err := m.CompleteMove("r1", c, from, to); err != nil {
		t.Fatal(err)
	}
	if p, _ := m.Position("C"); p != to {
		t.Fatalf("C at %s, want %s", p, to)
	}
	if _, blocked := m.Blocker(at(1, 2, 2)); blocked {
		t.Fatal("A is still blocked")
	}
	if _, _, _, err := m.PlanReshuffle("r3", at(1, 1, 2), storagemap.Fast); err != nil {
		t.Fatalf("C sits in front of B and is no longer moving, a new reshuffle must plan: %v", err)
	}
}

func TestCompleteRetrievePresentsAtThePort(t *testing.T) {
	m := storagemap.New(tiny(), 1)
	p := stored(t, m, "j1", "A", storagemap.Slow)
	_ = m.ReserveRetrieve("j2", "A")
	if _, err := m.CompleteRetrieve("A", at(1, 1, 1), "OUT1"); !errors.Is(err, storagemap.ErrNotAtPosition) {
		t.Fatalf("retrieve from the wrong position = %v", err)
	}
	if _, err := m.CompleteRetrieve("A", p, "IN1"); !errors.Is(err, storagemap.ErrUnknownPort) {
		t.Fatalf("retrieve to an input port = %v", err)
	}
	replaced, err := m.CompleteRetrieve("A", p, "OUT1")
	if err != nil || replaced != "" {
		t.Fatalf("CompleteRetrieve = %q, %v", replaced, err)
	}
	if _, in := m.Position("A"); in || m.PortFree("OUT1") || m.Presented("OUT1") != "A" || m.Free() != 4 {
		t.Fatal("A must have left the rack and wait on OUT1")
	}
	if err := m.ReserveRetrieve("j3", "A"); !errors.Is(err, storagemap.ErrContainerNotInRack) {
		t.Fatalf("A is no longer in the rack: %v", err)
	}
}

func TestCompleteRetrieveReportsAContainerItReplaced(t *testing.T) {
	m := storagemap.New(tiny(), 1)
	pa := stored(t, m, "j1", "A", storagemap.Slow)
	pb := stored(t, m, "j2", "B", storagemap.Slow)
	_, _ = m.CompleteRetrieve("A", pa, "OUT1")
	replaced, err := m.CompleteRetrieve("B", pb, "OUT1")
	if err != nil || replaced != "A" {
		t.Fatalf("replaced = %q, %v, want A", replaced, err)
	}
}

func TestPickupFreesThePortOnlyForThePresentedContainer(t *testing.T) {
	m := storagemap.New(tiny(), 1)
	p := stored(t, m, "j1", "A", storagemap.Slow)
	if err := m.Pickup("OUT1", "A"); !errors.Is(err, storagemap.ErrNotPresenting) {
		t.Fatalf("pickup at a free port = %v", err)
	}
	_, _ = m.CompleteRetrieve("A", p, "OUT1")
	if err := m.Pickup("OUT1", "B"); !errors.Is(err, storagemap.ErrNotPresenting) {
		t.Fatalf("pickup of another container = %v", err)
	}
	if err := m.Pickup("IN1", "A"); !errors.Is(err, storagemap.ErrUnknownPort) {
		t.Fatalf("pickup at an input port = %v", err)
	}
	if err := m.Pickup("OUT1", "A"); err != nil || !m.PortFree("OUT1") {
		t.Fatalf("Pickup = %v, free %v", err, m.PortFree("OUT1"))
	}
}

func TestReleasesUndoOnlyTheirOwnJob(t *testing.T) {
	m := storagemap.New(tiny(), 1)
	p, _ := m.ReserveStore("j1", "A", storagemap.Slow)
	m.ReleaseStore("other", p)
	if m.Free() != 3 {
		t.Fatal("another job's release freed the reservation")
	}
	m.ReleaseStore("j1", p)
	if m.Free() != 4 {
		t.Fatal("the reservation is still held")
	}
	stored(t, m, "j2", "B", storagemap.Slow)
	_ = m.ReserveRetrieve("j3", "B")
	m.ReleaseRetrieve("other", "B")
	if err := m.ReserveRetrieve("j4", "B"); !errors.Is(err, storagemap.ErrContainerBusy) {
		t.Fatal("another job's release freed the retrieve")
	}
	m.ReleaseRetrieve("j3", "B")
	if err := m.ReserveRetrieve("j4", "B"); err != nil {
		t.Fatalf("retrieve after release = %v", err)
	}
}

func TestReleaseMoveFreesTheTargetAndTheContainer(t *testing.T) {
	m := storagemap.New(tiny(), 1)
	stored(t, m, "j1", "A", storagemap.Slow)
	stored(t, m, "j2", "B", storagemap.Slow)
	stored(t, m, "j3", "C", storagemap.Slow)
	c, _, to, _ := m.PlanReshuffle("r1", at(1, 2, 2), storagemap.Fast)
	m.ReleaseMove("r1", c, to)
	if m.Free() != 1 {
		t.Fatalf("Free = %d, want 1", m.Free())
	}
	if _, _, _, err := m.PlanReshuffle("r2", at(1, 2, 2), storagemap.Fast); err != nil {
		t.Fatalf("C is still moving after the release: %v", err)
	}
}
