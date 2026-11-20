package equipment_test

import (
	"errors"
	"testing"
	"time"

	"github.com/IQVO/asrs-control/internal/domain/equipment"
)

var t0 = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

func TestParseStateAcceptsTheThreeProtocolStates(t *testing.T) {
	for _, s := range []string{"IDLE", "EXECUTE", "HELD"} {
		if got, err := equipment.ParseState(s); err != nil || string(got) != s {
			t.Errorf("ParseState(%s) = %s, %v", s, got, err)
		}
	}
	for _, s := range []string{"", "idle", "SUSPENDED", "STOPPED", "ABORTED"} {
		if _, err := equipment.ParseState(s); !errors.Is(err, equipment.ErrUnknownState) {
			t.Errorf("ParseState(%q) = %v, want ErrUnknownState", s, err)
		}
	}
}

func TestANewUnitIsIdleFreeAndAvailable(t *testing.T) {
	u := equipment.New("ASRS1", "A2", 2, t0)
	if u.AsrsID() != "ASRS1" || u.ID() != "A2" || u.Aisle() != 2 || u.State() != equipment.Idle || u.Busy() || !u.Available() {
		t.Fatalf("unit = %+v", u.Snapshot())
	}
}

func TestOneJobAtATime(t *testing.T) {
	u := equipment.New("ASRS1", "A1", 1, t0)
	if err := u.Assign("j1"); err != nil || !u.Busy() || u.CurrentJob() != "j1" {
		t.Fatalf("Assign = %v busy=%v", err, u.Busy())
	}
	if err := u.Assign("j2"); !errors.Is(err, equipment.ErrUnitBusy) {
		t.Fatalf("second Assign = %v, want ErrUnitBusy", err)
	}
	if u.Release("j2") || !u.Busy() {
		t.Fatal("releasing another job freed the unit")
	}
	if !u.Release("j1") || u.Busy() {
		t.Fatal("Release(j1) did not free the unit")
	}
	if u.Release("") {
		t.Fatal("Release of nothing reported a release")
	}
	if err := u.Assign(""); !errors.Is(err, equipment.ErrUnitBusy) {
		t.Fatalf("Assign of an empty job id = %v", err)
	}
}

func TestReportRaisesAnEventOnlyOnChange(t *testing.T) {
	u := equipment.New("ASRS1", "A1", 1, t0)
	if _, changed := u.Report(equipment.Idle, t0, t0); changed {
		t.Fatal("IDLE -> IDLE is no change")
	}
	at, plc := t0.Add(time.Second), t0.Add(time.Hour)
	ev, changed := u.Report(equipment.Held, at, plc)
	want := equipment.EquipmentStateChanged{AsrsID: "ASRS1", UnitID: "A1", Aisle: 1, State: equipment.Held, Previous: equipment.Idle, At: at, PLCTime: plc}
	if !changed || ev != want {
		t.Fatalf("Report = %+v, %v, want %+v", ev, changed, want)
	}
	if u.Available() || !u.Snapshot().Since.Equal(at) {
		t.Fatal("a HELD unit is not available")
	}
	back := equipment.Rehydrate(u.Snapshot())
	if back.Snapshot() != u.Snapshot() {
		t.Fatal("snapshot round trip")
	}
}
