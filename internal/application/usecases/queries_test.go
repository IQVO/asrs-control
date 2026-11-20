package usecases_test

import (
	"errors"
	"testing"

	"github.com/IQVO/asrs-control/internal/application/telegrams"
	"github.com/IQVO/asrs-control/internal/application/usecases"
	"github.com/IQVO/asrs-control/internal/domain/equipment"
	"github.com/IQVO/asrs-control/internal/domain/layout"
)

func TestJobReadsExternalJobsOnly(t *testing.T) {
	h := newHarness(t, tinyLayout())
	h.stored("A", "IN1")
	h.stored("B", "IN1")
	h.stored("C", "IN1")
	r := h.retrieve("A")
	move := h.last()
	if got, err := h.svc.Job(h.ctx, "ASRS1", r.ID()); err != nil || got.ID() != r.ID() {
		t.Fatalf("Job(retrieve) = %v, %v", got, err)
	}
	if _, err := h.svc.Job(h.ctx, "ASRS1", move.JobID); !errors.Is(err, usecases.ErrJobNotFound) {
		t.Fatalf("Job(reshuffle) = %v, want ErrJobNotFound", err)
	}
	if _, err := h.svc.Cancel(h.ctx, "ASRS1", move.JobID); !errors.Is(err, usecases.ErrJobNotFound) {
		t.Fatalf("Cancel(reshuffle) = %v, want ErrJobNotFound", err)
	}
	if _, err := h.svc.Job(h.ctx, "OTHER", r.ID()); !errors.Is(err, usecases.ErrUnknownAsrs) {
		t.Fatalf("Job of another AS/RS = %v", err)
	}
}

func TestAsrsViewShowsAislesPortsCapacityAndTheBalancedInputPort(t *testing.T) {
	h := newHarness(t, twoAisles())
	h.store("A", "IN1") // aisle 1: one open job
	v, err := h.svc.Asrs(h.ctx, "ASRS1")
	if err != nil {
		t.Fatal(err)
	}
	checkAisles(t, v)
	checkPortsAndCapacity(t, v)
	h.up(telegrams.UpTelegram{Type: telegrams.UpUnitState, UnitID: "A2", State: "HELD"})
	v, _ = h.svc.Asrs(h.ctx, "ASRS1")
	if v.RecommendedInputPortID != "IN1" {
		t.Fatalf("recommended with aisle 2 held = %q, want IN1", v.RecommendedInputPortID)
	}
	h.up(telegrams.UpTelegram{Type: telegrams.UpUnitState, UnitID: "A1", State: "HELD"})
	v, _ = h.svc.Asrs(h.ctx, "ASRS1")
	if v.RecommendedInputPortID != "" || v.Capacity.UnitsAvailable != 0 {
		t.Fatalf("all held: recommended %q, capacity %+v", v.RecommendedInputPortID, v.Capacity)
	}
	if _, err := h.svc.Asrs(h.ctx, "NOPE"); !errors.Is(err, usecases.ErrUnknownAsrs) {
		t.Fatalf("Asrs(NOPE) = %v", err)
	}
}

func checkAisles(t *testing.T, v usecases.AsrsView) {
	t.Helper()
	if v.AsrsID != "ASRS1" || len(v.Aisles) != 2 || len(v.Ports) != 4 {
		t.Fatalf("view = %+v", v)
	}
	a1, a2 := v.Aisles[0], v.Aisles[1]
	if a1.Aisle != 1 || a1.UnitID != "A1" || !a1.Busy || a1.OpenJobs != 1 || a1.PositionsTotal != 4 || a1.PositionsFree != 3 || a1.State != equipment.Idle {
		t.Fatalf("aisle 1 = %+v", a1)
	}
	if a2.Busy || a2.OpenJobs != 0 || a2.PositionsFree != 4 {
		t.Fatalf("aisle 2 = %+v", a2)
	}
}

func checkPortsAndCapacity(t *testing.T, v usecases.AsrsView) {
	t.Helper()
	if v.RecommendedInputPortID != "IN2" {
		t.Fatalf("recommended = %q, want IN2 (aisle 2 has fewer open jobs)", v.RecommendedInputPortID)
	}
	if v.Capacity.UnitsTotal != 2 || v.Capacity.PositionsTotal != 8 || v.Capacity.PositionsFree != 7 || v.Capacity.OpenJobs != 1 {
		t.Fatalf("capacity = %+v", v.Capacity)
	}
	if p := v.Ports[1]; p.PortID != "OUT1" || p.Kind != layout.PortOutput || p.PresentedContainerID != "" {
		t.Fatalf("OUT1 = %+v", p)
	}
}

func TestTiesGoToTheAisleWithMoreRoomThenTheLowerAisle(t *testing.T) {
	h := newHarness(t, twoAisles())
	if v, _ := h.svc.Asrs(h.ctx, "ASRS1"); v.RecommendedInputPortID != "IN1" {
		t.Fatalf("empty AS/RS recommends %q, want IN1", v.RecommendedInputPortID)
	}
	h.stored("A", "IN1") // aisle 1 has less room, both have 0 open jobs
	if v, _ := h.svc.Asrs(h.ctx, "ASRS1"); v.RecommendedInputPortID != "IN2" {
		t.Fatalf("recommended = %q, want IN2 (more free positions)", v.RecommendedInputPortID)
	}
	r := h.retrieve("A")
	h.ackLast()
	h.done(r.ID())
	v, _ := h.svc.Asrs(h.ctx, "ASRS1")
	if v.Ports[1].PresentedContainerID != "A" {
		t.Fatalf("OUT1 presents %q, want A", v.Ports[1].PresentedContainerID)
	}
}
