//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IQVO/asrs-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/asrs-control/internal/application/idempotency"
	"github.com/IQVO/asrs-control/internal/application/ports"
	"github.com/IQVO/asrs-control/internal/domain/capacity"
	"github.com/IQVO/asrs-control/internal/domain/equipment"
	"github.com/IQVO/asrs-control/internal/domain/layout"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
	"github.com/IQVO/asrs-control/internal/domain/storagemap"
	"github.com/IQVO/asrs-control/internal/testing/pgtest"
)

var t0 = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

func tiny() layout.Layout {
	return layout.Layout{ID: "ASRS1", Aisles: 2, Levels: 1, Slots: 2, DoubleDeep: true, Cost: layout.Cost{PerLevel: 1, PerSlot: 1},
		Ports: []layout.Port{{ID: "IN1", Aisle: 1, Kind: layout.PortInput}, {ID: "OUT1", Aisle: 1, Kind: layout.PortOutput},
			{ID: "IN2", Aisle: 2, Kind: layout.PortInput}, {ID: "OUT2", Aisle: 2, Kind: layout.PortOutput}}}
}

func TestStorageMapsPersistLockAndFind(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	maps, uow := postgres.NewStorageMapRepo(pool), postgres.NewUnitOfWork(pool)
	if err := maps.Ensure(ctx, tiny()); err != nil {
		t.Fatal(err)
	}
	if err := maps.Ensure(ctx, tiny()); err != nil {
		t.Fatalf("Ensure is not idempotent: %v", err)
	}
	err := uow.Do(ctx, func(ctx context.Context) error {
		m, err := maps.Lock(ctx, tiny(), 2)
		if err != nil {
			return err
		}
		p, _ := m.ReserveStore("sj-1", "C1", storagemap.Slow)
		_ = m.CompleteStore("sj-1", "C1", p)
		_ = m.ReserveRetrieve("sj-2", "C1")
		return maps.Save(ctx, "ASRS1", m)
	})
	if err != nil {
		t.Fatal(err)
	}
	m, err := maps.Get(ctx, tiny(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := m.Position("C1"); !ok || p != (layout.Position{Aisle: 2, Level: 1, Slot: 2, Depth: 2}) || !m.PortFree("OUT2") {
		t.Fatalf("C1 at %s (%v)", p, ok)
	}
	if err := m.ReserveRetrieve("sj-3", "C1"); !errors.Is(err, storagemap.ErrContainerBusy) {
		t.Fatalf("the retrieve hold was not persisted: %v", err)
	}
	if aisle, found, err := maps.Find(ctx, "ASRS1", "C1"); err != nil || !found || aisle != 2 {
		t.Fatalf("Find = %d %v %v", aisle, found, err)
	}
	if _, found, _ := maps.Find(ctx, "ASRS1", "nope"); found {
		t.Fatal("Find found a ghost")
	}
	shrunk := tiny()
	shrunk.Aisles, shrunk.Ports = 1, shrunk.Ports[:2]
	if err := maps.Ensure(ctx, shrunk); err == nil {
		t.Fatal("Ensure accepted a layout that dropped an aisle holding a container")
	}
}

func TestStorageJobsRoundTripAndQueries(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	jobs := postgres.NewStorageJobRepo(pool)
	to := layout.Position{Aisle: 1, Level: 1, Slot: 2, Depth: 2}
	s, _ := storagejob.NewStore(storagejob.Spec{ID: "sj-1", AsrsID: "ASRS1", Aisle: 1, ContainerID: "C1", PortID: "IN1", Reference: "leg", To: to, At: t0})
	r, _ := storagejob.NewRetrieve(storagejob.Spec{ID: "sj-2", AsrsID: "ASRS1", Aisle: 1, ContainerID: "C2", PortID: "OUT1", At: t0})
	for _, j := range []*storagejob.Job{s, r} {
		if err := jobs.Insert(ctx, j); err != nil {
			t.Fatal(err)
		}
	}
	if s.Seq() == 0 || r.Seq() <= s.Seq() {
		t.Fatalf("seq %d / %d", s.Seq(), r.Seq())
	}
	_ = s.Dispatch("tg-1")
	_, _, _ = s.RequestCancel("tc-1", t0) // CANCEL_REQUESTED, command not acknowledged
	_ = s.Acknowledge()                   // the command's ACK during the cancel: acked = true
	if err := jobs.Update(ctx, s); err != nil {
		t.Fatal(err)
	}
	got, err := jobs.Get(ctx, "sj-1")
	if err != nil || got.State() != s.State() {
		t.Fatalf("round trip:\n got %+v\nwant %+v (%v)", got.State(), s.State(), err)
	}
	if j, err := jobs.ByTelegram(ctx, "ASRS1", "tg-1"); err != nil || j.ID() != "sj-1" {
		t.Fatalf("ByTelegram = %v %v", j, err)
	}
	if _, err := jobs.ByTelegram(ctx, "ASRS1", ""); !errors.Is(err, ports.ErrJobNotFound) {
		t.Fatalf("ByTelegram(\"\") = %v", err)
	}
	if open, _ := jobs.Open(ctx, "ASRS1", 1); len(open) != 2 || open[0].ID() != "sj-1" {
		t.Fatalf("Open = %v", open)
	}
	if fl, _ := jobs.InFlightCommands(ctx, "ASRS1"); len(fl) != 1 || fl[0].ID() != "sj-1" {
		t.Fatalf("InFlightCommands = %v", fl)
	}
	retrieveLifecycle(ctx, t, jobs, r, to)
}

// retrieveLifecycle takes the retrieve r (already inserted) through awaiting arrival and a
// presentation, checking the queries that follow its status.
func retrieveLifecycle(ctx context.Context, t *testing.T, jobs *postgres.StorageJobRepo, r *storagejob.Job, to layout.Position) {
	t.Helper()
	_ = r.Locate(to)
	_ = r.Dispatch("tg-2")
	_, _ = r.CompleteMotion(t0, t0)
	_ = jobs.Update(ctx, r)
	if a, err := jobs.OldestAwaiting(ctx, "ASRS1", "OUT1"); err != nil || a.ID() != "sj-2" {
		t.Fatalf("OldestAwaiting = %v %v", a, err)
	}
	_, _ = r.Present("C2", t0.Add(time.Hour), t0)
	_ = jobs.Update(ctx, r)
	if n, _ := jobs.CountRetrievals(ctx, "ASRS1", "C2", t0); n != 1 {
		t.Fatalf("CountRetrievals = %d", n)
	}
	if n, _ := jobs.CountRetrievals(ctx, "ASRS1", "C2", t0.Add(2*time.Hour)); n != 0 {
		t.Fatalf("CountRetrievals after the window = %d", n)
	}
	if n, _ := jobs.CountOpen(ctx, "ASRS1"); n != 1 {
		t.Fatalf("CountOpen = %d", n)
	}
	if ok, _ := jobs.HasOpen(ctx, "ASRS1", "C1"); !ok {
		t.Fatal("HasOpen(C1) = false")
	}
	ghost, _ := storagejob.NewRetrieve(storagejob.Spec{ID: "sj-x", AsrsID: "ASRS1", Aisle: 1, ContainerID: "X", PortID: "OUT1", At: t0})
	if err := jobs.Update(ctx, ghost); !errors.Is(err, ports.ErrJobNotFound) {
		t.Fatalf("Update(ghost) = %v", err)
	}
}

func TestUnitsAndCapacity(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	units, caps := postgres.NewUnitRepo(pool), postgres.NewCapacityRepo(pool)
	if err := units.Ensure(ctx, tiny(), t0); err != nil {
		t.Fatal(err)
	}
	u, err := units.Lock(ctx, "ASRS1", 2)
	if err != nil || u.ID() != "A2" || u.State() != equipment.Idle {
		t.Fatalf("unit = %+v %v", u, err)
	}
	u.Report(equipment.Held, t0, t0)
	_ = u.Assign("sj-1")
	_ = units.Save(ctx, u)
	if all, _ := units.List(ctx, "ASRS1"); len(all) != 2 || all[1].State() != equipment.Held || all[1].CurrentJob() != "sj-1" {
		t.Fatalf("List = %+v", all)
	}
	if _, known, _ := caps.Lock(ctx, "ASRS1"); known {
		t.Fatal("a capacity snapshot exists before the first save")
	}
	snap := capacity.Snapshot{AsrsID: "ASRS1", UnitsTotal: 2, UnitsAvailable: 1, PositionsTotal: 8, PositionsFree: 7, OpenJobs: 1}
	_ = caps.Save(ctx, snap)
	_ = caps.Save(ctx, snap)
	if got, known, _ := caps.Lock(ctx, "ASRS1"); !known || got != snap {
		t.Fatalf("capacity = %+v %v", got, known)
	}
}

func TestTelegramInboxKeepsTheFirstAck(t *testing.T) {
	ctx := context.Background()
	inbox := postgres.NewTelegramInbox(pgtest.Pool(t))
	if ack, claimed, _ := inbox.Claim(ctx, "ASRS1", "u1", "a1"); !claimed || ack != "a1" {
		t.Fatalf("first claim = %s %v", ack, claimed)
	}
	if ack, claimed, _ := inbox.Claim(ctx, "ASRS1", "u1", "a2"); claimed || ack != "a1" {
		t.Fatalf("duplicate claim = %s %v, want the first ACK id", ack, claimed)
	}
	if n, err := inbox.Purge(ctx, time.Now().Add(time.Hour)); err != nil || n != 1 {
		t.Fatalf("inbox Purge = %d %v", n, err)
	}
}

func TestIdempotencyStoreWindowAndPurge(t *testing.T) {
	ctx := context.Background()
	idem := postgres.NewIdempotencyStore(pgtest.Pool(t))
	rec := idempotency.IdempotencyRecord{Key: "k1", Fingerprint: "f", Status: 202, ContentType: "application/json", Body: []byte("{}"), CreatedAt: t0}
	_ = idem.Save(ctx, rec, t0.Add(-24*time.Hour))
	if got, ok, _ := idem.Lookup(ctx, "k1", t0.Add(-24*time.Hour)); !ok || got.Status != 202 {
		t.Fatalf("Lookup = %+v %v", got, ok)
	}
	if _, ok, _ := idem.Lookup(ctx, "k1", t0.Add(time.Second)); ok {
		t.Fatal("an answer older than the window was replayed")
	}
	newer := rec
	newer.Fingerprint, newer.CreatedAt = "g", t0.Add(25*time.Hour)
	_ = idem.Save(ctx, newer, t0.Add(time.Hour)) // the old row is outside the window: replaced
	if got, _, _ := idem.Lookup(ctx, "k1", t0); got.Fingerprint != "g" {
		t.Fatalf("an expired row was not replaced: %+v", got)
	}
	if n, _ := idem.Purge(ctx, t0.Add(26*time.Hour)); n != 1 {
		t.Fatalf("Purge = %d", n)
	}
}

// A reserved position keeps the container it is held for across Save and Get (Rehydrate refuses a
// reserved position without it).
func TestStorageMapPersistsTheReservedContainer(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	maps := postgres.NewStorageMapRepo(pool)
	if err := maps.Ensure(ctx, tiny()); err != nil {
		t.Fatal(err)
	}
	m, err := maps.Get(ctx, tiny(), 1)
	if err != nil {
		t.Fatal(err)
	}
	pos, err := m.ReserveStore("sj-1", "C1", storagemap.Slow)
	if err != nil {
		t.Fatal(err)
	}
	want := m.Snapshot()
	if want.ReservedContainer[pos] != "C1" {
		t.Fatalf("the map does not record the reservation's container: %+v", want.ReservedContainer)
	}
	if err := maps.Save(ctx, "ASRS1", m); err != nil {
		t.Fatal(err)
	}
	got, err := maps.Get(ctx, tiny(), 1)
	if err != nil {
		t.Fatalf("a map with a reservation does not rehydrate: %v", err)
	}
	if gs := got.Snapshot(); gs.ReservedContainer[pos] != "C1" || gs.Reserved[pos] != "sj-1" {
		t.Fatalf("reservation lost: %+v", gs)
	}
	if err := got.CompleteStore("sj-1", "C1", pos); err != nil {
		t.Fatalf("the reloaded reservation cannot complete: %v", err)
	}
}

func TestOpenAndInFlightJobsAreInAcceptanceOrder(t *testing.T) {
	ctx := context.Background()
	jobs := postgres.NewStorageJobRepo(pgtest.Pool(t))
	// Insert in an order whose ids sort differently from seq, so only ORDER BY seq passes.
	for _, id := range []string{"sj-9", "sj-1", "sj-5"} {
		r, _ := storagejob.NewRetrieve(storagejob.Spec{ID: id, AsrsID: "ASRS1", Aisle: 1, ContainerID: "C-" + id, PortID: "OUT1", At: t0})
		if err := jobs.Insert(ctx, r); err != nil {
			t.Fatal(err)
		}
		_ = r.Locate(layout.Position{Aisle: 1, Level: 1, Slot: 1, Depth: 1})
		_ = r.Dispatch("tg-" + id)
		if err := jobs.Update(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	for name, list := range map[string]func() ([]*storagejob.Job, error){
		"Open":             func() ([]*storagejob.Job, error) { return jobs.Open(ctx, "ASRS1", 1) },
		"InFlightCommands": func() ([]*storagejob.Job, error) { return jobs.InFlightCommands(ctx, "ASRS1") },
	} {
		got, err := list()
		if err != nil || len(got) != 3 || got[0].ID() != "sj-9" || got[1].ID() != "sj-1" || got[2].ID() != "sj-5" {
			t.Fatalf("%s = %v (%v), want sj-9, sj-1, sj-5", name, got, err)
		}
	}
}

func TestInsertRefusesASecondOpenExternalJobButNotAReshuffleOrAClosedOne(t *testing.T) {
	ctx := context.Background()
	jobs := postgres.NewStorageJobRepo(pgtest.Pool(t))
	to := layout.Position{Aisle: 1, Level: 1, Slot: 1, Depth: 1}
	store := func(id, container string) *storagejob.Job {
		j, err := storagejob.NewStore(storagejob.Spec{ID: id, AsrsID: "ASRS1", Aisle: 1, ContainerID: container, PortID: "IN1", To: to, At: t0})
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	first := store("sj-1", "C1")
	if err := jobs.Insert(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := jobs.Insert(ctx, store("sj-2", "C1")); !errors.Is(err, ports.ErrOpenJobExists) {
		t.Fatalf("second open job = %v, want ErrOpenJobExists", err)
	}
	if err := jobs.Insert(ctx, store("sj-3", "C2")); err != nil {
		t.Fatalf("another container: %v", err)
	}
	_, _ = first.Fail(storagejob.Failure{Reason: storagejob.ReasonNoReshufflePosition, At: t0})
	if err := jobs.Update(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := jobs.Insert(ctx, store("sj-4", "C1")); err != nil {
		t.Fatalf("the first job is closed, C1 is free again: %v", err)
	}
}
