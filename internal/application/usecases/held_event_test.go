package usecases_test

import (
	"testing"

	"github.com/IQVO/asrs-control/internal/application/telegrams"
	"github.com/IQVO/asrs-control/internal/domain/capacity"
	"github.com/IQVO/asrs-control/internal/domain/equipment"
)

// TestHeldUnitStateWithNoOpenJobStillPublishesTheEvent reproduces the e2e's fault tail exactly:
// the last job already ended, the unit is IDLE, then UNIT_STATE(HELD) arrives. The state change
// must still publish EquipmentStateChanged and the capacity fact — the first CI run of the e2e
// lost exactly these two events while the unit row did turn HELD.
func TestHeldUnitStateWithNoOpenJobStillPublishesTheEvent(t *testing.T) {
	h := newHarness(t, tinyLayout())
	h.stored("A", "IN1")
	h.retrieved("A")

	ack := h.up(telegrams.UpTelegram{Type: telegrams.UpUnitState, UnitID: "A1", State: "HELD"})
	if ack == "" {
		t.Fatal("UNIT_STATE(HELD) was not acknowledged")
	}

	var held *equipment.EquipmentStateChanged
	states := eventsOf[equipment.EquipmentStateChanged](h)
	for _, e := range states {
		if e.State == equipment.Held {
			held = &e
		}
	}
	if held == nil {
		t.Fatalf("no HELD EquipmentStateChanged among %d state events %+v", len(states), states)
	}
	if held.Previous != equipment.Idle {
		t.Fatalf("HELD came from %s, want IDLE", held.Previous)
	}
	if caps := eventsOf[capacity.AsrsCapacityChanged](h); len(caps) == 0 {
		t.Fatal("the unit turning unavailable published no capacity fact")
	}

	// A duplicate HELD telegram (the emulator reports on every tick) is quiet: no second event.
	before := len(states)
	h.up(telegrams.UpTelegram{Type: telegrams.UpUnitState, UnitID: "A1", State: "HELD"})
	if after := len(eventsOf[equipment.EquipmentStateChanged](h)); after != before {
		t.Fatalf("a repeated HELD raised another event: %d -> %d", before, after)
	}
}
