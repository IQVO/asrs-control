package postgres

import (
	"context"
	"testing"

	"github.com/IQVO/asrs-control/internal/domain/layout"
	"github.com/IQVO/asrs-control/internal/domain/storagemap"
)

func TestAfterCommitOutsideAUnitOfWorkRunsAtOnce(t *testing.T) {
	ran := 0
	NewUnitOfWork(nil).AfterCommit(context.Background(), func() { ran++ })
	if ran != 1 {
		t.Fatalf("hook ran %d times, want 1", ran)
	}
}

func TestAfterCommitInsideAnOpenUnitOfWorkWaits(t *testing.T) {
	h := &hooks{}
	ctx := context.WithValue(context.Background(), hooksKey{}, h)
	ran := 0
	NewUnitOfWork(nil).AfterCommit(ctx, func() { ran++ })
	NewUnitOfWork(nil).AfterCommit(ctx, func() { ran++ })
	if ran != 0 || len(h.fns) != 2 {
		t.Fatalf("ran %d, queued %d; want 0 and 2", ran, len(h.fns))
	}
}

func TestSnapshotEncodingKeepsTheReservedContainer(t *testing.T) {
	l := layout.Layout{ID: "ASRS1", Aisles: 1, Levels: 1, Slots: 2, DoubleDeep: true, Cost: layout.Cost{PerLevel: 1, PerSlot: 1},
		Ports: []layout.Port{{ID: "IN1", Aisle: 1, Kind: layout.PortInput}, {ID: "OUT1", Aisle: 1, Kind: layout.PortOutput}}}
	m := storagemap.New(l, 1)
	pos, err := m.ReserveStore("sj-1", "C1", storagemap.Slow)
	if err != nil {
		t.Fatal(err)
	}
	b, containers, err := encodeSnapshot(m.Snapshot())
	if err != nil || len(containers) != 0 {
		t.Fatalf("encode: %v %v", containers, err)
	}
	s, err := decodeSnapshot(1, b)
	if err != nil {
		t.Fatal(err)
	}
	if s.ReservedContainer[pos] != "C1" || s.Reserved[pos] != "sj-1" {
		t.Fatalf("decoded %+v", s)
	}
	if _, err := storagemap.Rehydrate(l, s); err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}
}

func TestPositionArrays(t *testing.T) {
	if posArray(layout.Position{}) != nil || posFrom(nil) != (layout.Position{}) {
		t.Fatal("the zero position is NULL")
	}
	p := layout.Position{Aisle: 2, Level: 3, Slot: 4, Depth: 1}
	if posFrom(posArray(p)) != p {
		t.Fatal("position round trip")
	}
}
