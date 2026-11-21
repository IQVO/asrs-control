package scheduling_test

import (
	"testing"
	"time"

	"github.com/IQVO/asrs-control/internal/domain/equipment"
	"github.com/IQVO/asrs-control/internal/domain/layout"
	"github.com/IQVO/asrs-control/internal/domain/scheduling"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
	"github.com/IQVO/asrs-control/internal/domain/storagemap"
)

var t0 = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

func tiny() layout.Layout {
	return layout.Layout{ID: "ASRS1", Aisles: 1, Levels: 1, Slots: 2, DoubleDeep: true, Cost: layout.Cost{PerLevel: 1, PerSlot: 1},
		Ports: []layout.Port{{ID: "IN1", Aisle: 1, Kind: layout.PortInput}, {ID: "OUT1", Aisle: 1, Kind: layout.PortOutput}, {ID: "OUT9", Aisle: 1, Kind: layout.PortOutput}}}
}

func at(level, slot, depth int) layout.Position {
	return layout.Position{Aisle: 1, Level: level, Slot: slot, Depth: depth}
}

func job(id string, k storagejob.Kind, st storagejob.Status, container, port string, seq int64) *storagejob.Job {
	s := storagejob.State{ID: id, AsrsID: "ASRS1", Aisle: 1, Kind: k, ContainerID: container, PortID: port, Status: st, Seq: seq, AcceptedAt: t0}
	if k == storagejob.Store {
		s.To = at(1, 1, 1)
	}
	if k == storagejob.Reshuffle {
		s.ParentID, s.From, s.To, s.PortID = "p", at(1, 2, 1), at(1, 1, 1), ""
	}
	return storagejob.Rehydrate(s)
}

func put(t *testing.T, m *storagemap.Map, c string) layout.Position {
	t.Helper()
	p, err := m.ReserveStore("s-"+c, c, storagemap.Slow)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.CompleteStore("s-"+c, c, p); err != nil {
		t.Fatal(err)
	}
	return p
}

func idle() *equipment.Unit { return equipment.New("ASRS1", "A1", 1, t0) }

func expect(t *testing.T, d scheduling.Decision, a scheduling.Action, jobID string) {
	t.Helper()
	got := ""
	if d.Job != nil {
		got = d.Job.ID()
	}
	if d.Action != a || got != jobID {
		t.Fatalf("Next = %s %q, want %s %q", d.Action, got, a, jobID)
	}
}

func TestNothingRunsOnABusyOrHeldUnit(t *testing.T) {
	m := storagemap.New(tiny(), 1)
	open := []*storagejob.Job{job("j1", storagejob.Store, storagejob.Queued, "C1", "IN1", 1)}
	busy := idle()
	_ = busy.Assign("j0")
	expect(t, scheduling.Next(m, busy, open), scheduling.None, "")
	held := idle()
	held.Report(equipment.Held, t0, t0)
	expect(t, scheduling.Next(m, held, open), scheduling.None, "")
	expect(t, scheduling.Next(m, idle(), nil), scheduling.None, "")
}

func TestTheOldestQueuedStoreDispatches(t *testing.T) {
	m := storagemap.New(tiny(), 1)
	open := []*storagejob.Job{
		job("j0", storagejob.Store, storagejob.Done, "C0", "IN1", 1),
		job("j1", storagejob.Store, storagejob.Queued, "C1", "IN1", 2),
		job("j2", storagejob.Store, storagejob.Queued, "C2", "IN1", 3),
	}
	expect(t, scheduling.Next(m, idle(), open), scheduling.Dispatch, "j1")
}

func TestAQueuedReshuffleGoesFirst(t *testing.T) {
	m := storagemap.New(tiny(), 1)
	open := []*storagejob.Job{
		job("j1", storagejob.Store, storagejob.Queued, "C1", "IN1", 1),
		job("r1", storagejob.Reshuffle, storagejob.Queued, "C9", "", 5),
	}
	expect(t, scheduling.Next(m, idle(), open), scheduling.Dispatch, "r1")
}

func TestAnUnblockedRetrieveToAFreePortDispatches(t *testing.T) {
	m := storagemap.New(tiny(), 1)
	put(t, m, "A")
	open := []*storagejob.Job{job("r", storagejob.Retrieve, storagejob.Queued, "A", "OUT1", 1)}
	expect(t, scheduling.Next(m, idle(), open), scheduling.Dispatch, "r")
}

func TestABlockedRetrieveAsksForAReshuffleOfItsPosition(t *testing.T) {
	m := storagemap.New(tiny(), 1)
	pa := put(t, m, "A") // S2-D2
	put(t, m, "B")       // S1-D2
	put(t, m, "C")       // S2-D1 blocks A
	d := scheduling.Next(m, idle(), []*storagejob.Job{job("r", storagejob.Retrieve, storagejob.Queued, "A", "OUT1", 1)})
	expect(t, d, scheduling.Reshuffle, "r")
	if d.Blocked != pa {
		t.Fatalf("Blocked = %s, want %s", d.Blocked, pa)
	}
}

func TestARetrieveOfAnUnknownContainerFails(t *testing.T) {
	m := storagemap.New(tiny(), 1)
	d := scheduling.Next(m, idle(), []*storagejob.Job{job("r", storagejob.Retrieve, storagejob.Queued, "GHOST", "OUT1", 1)})
	expect(t, d, scheduling.Fail, "r")
	if d.Reason != storagejob.ReasonStorageMapMismatch {
		t.Fatalf("Reason = %s", d.Reason)
	}
}

func TestRetrievesToOnePortKeepTheirOrderAndAStoreMayOvertake(t *testing.T) {
	m := storagemap.New(tiny(), 1)
	pa := put(t, m, "A")
	put(t, m, "B")
	_, _ = m.CompleteRetrieve("A", pa, "OUT1") // A waits on OUT1, pickup not confirmed
	open := []*storagejob.Job{
		job("r2", storagejob.Retrieve, storagejob.Queued, "B", "OUT1", 2),
		job("s3", storagejob.Store, storagejob.Queued, "C", "IN1", 3),
	}
	expect(t, scheduling.Next(m, idle(), open), scheduling.Dispatch, "s3")
	expect(t, scheduling.Next(m, idle(), open[:1]), scheduling.None, "")
}

func TestARetrieveWaitsBehindAnEarlierOneAwaitingArrival(t *testing.T) {
	m := storagemap.New(tiny(), 1)
	put(t, m, "B")
	open := []*storagejob.Job{
		job("r1", storagejob.Retrieve, storagejob.AwaitingArrival, "A", "OUT1", 1),
		job("r2", storagejob.Retrieve, storagejob.Queued, "B", "OUT1", 2),
	}
	expect(t, scheduling.Next(m, idle(), open), scheduling.None, "")
}

func TestALaterRetrieveToTheSamePortNeverOvertakesAWaitingOne(t *testing.T) {
	m := storagemap.New(tiny(), 1)
	pa := put(t, m, "A")
	put(t, m, "B")
	_, _ = m.CompleteRetrieve("A", pa, "OUT1")
	put(t, m, "D")
	open := []*storagejob.Job{
		job("r2", storagejob.Retrieve, storagejob.Queued, "B", "OUT1", 2),
		job("r3", storagejob.Retrieve, storagejob.Queued, "D", "OUT9", 3),
		job("r4", storagejob.Retrieve, storagejob.Queued, "D", "OUT1", 4),
	}
	expect(t, scheduling.Next(m, idle(), open), scheduling.Dispatch, "r3")
	expect(t, scheduling.Next(m, idle(), []*storagejob.Job{open[0], open[2]}), scheduling.None, "")
}

func TestAHeldUnitDispatchesAgainOnceReportedIdle(t *testing.T) {
	m := storagemap.New(tiny(), 1)
	open := []*storagejob.Job{job("j1", storagejob.Store, storagejob.Queued, "C1", "IN1", 1)}
	u := idle()
	u.Report(equipment.Held, t0, t0)
	expect(t, scheduling.Next(m, u, open), scheduling.None, "")
	u.Report(equipment.Idle, t0.Add(time.Second), t0)
	expect(t, scheduling.Next(m, u, open), scheduling.Dispatch, "j1")
}
