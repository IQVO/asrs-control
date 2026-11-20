package capacity_test

import (
	"testing"
	"time"

	"github.com/IQVO/asrs-control/internal/domain/capacity"
)

func TestChangedOnlyWhenTheSnapshotDiffers(t *testing.T) {
	at := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	s := capacity.Snapshot{AsrsID: "ASRS1", UnitsTotal: 2, UnitsAvailable: 2, PositionsTotal: 8, PositionsFree: 8}
	if ev, changed := capacity.Changed(capacity.Snapshot{}, false, s, at); !changed || ev.Snapshot != s || !ev.At.Equal(at) {
		t.Fatalf("first snapshot = %+v, %v", ev, changed)
	}
	if _, changed := capacity.Changed(s, true, s, at); changed {
		t.Fatal("an equal snapshot is no change")
	}
	for _, mut := range []func(*capacity.Snapshot){
		func(n *capacity.Snapshot) { n.UnitsAvailable = 1 },
		func(n *capacity.Snapshot) { n.PositionsFree = 7 },
		func(n *capacity.Snapshot) { n.OpenJobs = 1 },
	} {
		n := s
		mut(&n)
		if _, changed := capacity.Changed(s, true, n, at); !changed {
			t.Errorf("%+v -> %+v reported no change", s, n)
		}
	}
}
