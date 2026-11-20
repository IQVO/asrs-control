package memory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IQVO/asrs-control/internal/adapters/outbound/memory"
	"github.com/IQVO/asrs-control/internal/application/ports"
	"github.com/IQVO/asrs-control/internal/application/telegrams"
	"github.com/IQVO/asrs-control/internal/domain/layout"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
	"github.com/IQVO/asrs-control/internal/domain/storagemap"
)

var t0 = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

func tiny() layout.Layout {
	return layout.Layout{ID: "ASRS1", Aisles: 1, Levels: 1, Slots: 2, DoubleDeep: true, Cost: layout.Cost{PerLevel: 1, PerSlot: 1},
		Ports: []layout.Port{{ID: "IN1", Aisle: 1, Kind: layout.PortInput}, {ID: "OUT1", Aisle: 1, Kind: layout.PortOutput}}}
}

func TestAFailedUnitOfWorkRollsEverythingBack(t *testing.T) {
	ctx := context.Background()
	st := memory.NewStore()
	maps, jobs, events := memory.NewStorageMaps(st), memory.NewStorageJobs(st), memory.NewEvents(st)
	if err := maps.Ensure(ctx, tiny()); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	err := memory.NewUnitOfWork(st).Do(ctx, func(ctx context.Context) error {
		m, err := maps.Lock(ctx, tiny(), 1)
		if err != nil {
			return err
		}
		p, _ := m.ReserveStore("sj-1", "C1", storagemap.Slow)
		_ = maps.Save(ctx, "ASRS1", m)
		j, _ := storagejob.NewStore(storagejob.Spec{ID: "sj-1", AsrsID: "ASRS1", Aisle: 1, ContainerID: "C1", PortID: "IN1", To: p, At: t0})
		_ = jobs.Insert(ctx, j)
		_ = events.Append(ctx, "an event")
		_ = st.Tag(ctx, "x")
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Do = %v", err)
	}
	m, _ := maps.Get(ctx, tiny(), 1)
	if _, err := jobs.Get(ctx, "sj-1"); !errors.Is(err, ports.ErrJobNotFound) || m.Free() != 4 || len(events.All()) != 0 || st.Tagged("x") != 0 {
		t.Fatalf("rollback incomplete: job err %v, free %d, events %d, tags %d", err, m.Free(), len(events.All()), st.Tagged("x"))
	}
}

func TestACommittedReservationSurvivesTheSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := memory.NewStore()
	maps := memory.NewStorageMaps(st)
	_ = maps.Ensure(ctx, tiny())
	m, _ := maps.Lock(ctx, tiny(), 1)
	if _, err := m.ReserveStore("sj-1", "C1", storagemap.Slow); err != nil {
		t.Fatal(err)
	}
	if err := maps.Save(ctx, "ASRS1", m); err != nil {
		t.Fatal(err)
	}
	back, err := maps.Get(ctx, tiny(), 1)
	if err != nil {
		t.Fatalf("Get after Save = %v", err)
	}
	if back.Free() != 3 {
		t.Fatalf("Free = %d, want 3", back.Free())
	}
}

func TestJobsKeepAcceptanceOrderAndFilters(t *testing.T) {
	ctx := context.Background()
	jobs := memory.NewStorageJobs(memory.NewStore())
	for i, c := range []string{"A", "B"} {
		j, _ := storagejob.NewRetrieve(storagejob.Spec{ID: "sj-" + c, AsrsID: "ASRS1", Aisle: 1, ContainerID: c, PortID: "OUT1", At: t0})
		if err := jobs.Insert(ctx, j); err != nil || j.Seq() != int64(i+1) {
			t.Fatalf("Insert = %v seq %d", err, j.Seq())
		}
	}
	open, _ := jobs.Open(ctx, "ASRS1", 1)
	if len(open) != 2 || open[0].ID() != "sj-A" || open[1].ID() != "sj-B" {
		t.Fatalf("Open = %v", open)
	}
	if n, _ := jobs.CountOpen(ctx, "ASRS1"); n != 2 {
		t.Fatalf("CountOpen = %d", n)
	}
	if ok, _ := jobs.HasOpen(ctx, "ASRS1", "B"); !ok {
		t.Fatal("HasOpen(B) = false")
	}
	if _, err := jobs.OldestAwaiting(ctx, "ASRS1", "OUT1"); !errors.Is(err, ports.ErrJobNotFound) {
		t.Fatalf("OldestAwaiting = %v", err)
	}
	ghost, _ := storagejob.NewRetrieve(storagejob.Spec{ID: "sj-Z", AsrsID: "ASRS1", Aisle: 1, ContainerID: "Z", PortID: "OUT1", At: t0})
	if err := jobs.Update(ctx, ghost); !errors.Is(err, ports.ErrJobNotFound) {
		t.Fatalf("Update of an unknown job = %v", err)
	}
}

// Open must come back in Seq order whatever order the jobs were inserted under in map iteration;
// many jobs make a map-order result fail with near certainty.
func TestOpenListsManyJobsInSeqOrder(t *testing.T) {
	ctx := context.Background()
	jobs := memory.NewStorageJobs(memory.NewStore())
	const n = 40
	for i := 0; i < n; i++ {
		id := "sj-" + string(rune('a'+i%26)) + string(rune('A'+i/26))
		j, _ := storagejob.NewRetrieve(storagejob.Spec{ID: id, AsrsID: "ASRS1", Aisle: 1, ContainerID: id, PortID: "OUT1", At: t0})
		if err := jobs.Insert(ctx, j); err != nil {
			t.Fatal(err)
		}
	}
	open, _ := jobs.Open(ctx, "ASRS1", 1)
	if len(open) != n {
		t.Fatalf("Open = %d jobs, want %d", len(open), n)
	}
	for i, j := range open {
		if j.Seq() != int64(i+1) {
			t.Fatalf("open[%d].Seq = %d, want %d", i, j.Seq(), i+1)
		}
	}
}

func TestAfterCommitHooksRunOnlyAfterTheOutermostCommit(t *testing.T) {
	ctx := context.Background()
	st := memory.NewStore()
	uow := memory.NewUnitOfWork(st)
	var ran []string
	err := uow.Do(ctx, func(ctx context.Context) error {
		uow.AfterCommit(ctx, func() { ran = append(ran, "outer") })
		if err := uow.Do(ctx, func(ctx context.Context) error {
			uow.AfterCommit(ctx, func() { ran = append(ran, "joined") })
			return nil
		}); err != nil {
			return err
		}
		if len(ran) != 0 {
			t.Fatal("a hook ran before the outermost commit")
		}
		return nil
	})
	if err != nil || len(ran) != 2 || ran[0] != "outer" || ran[1] != "joined" {
		t.Fatalf("hooks after commit = %v (%v), want [outer joined]", ran, err)
	}
	ran = nil
	_ = uow.Do(ctx, func(ctx context.Context) error {
		uow.AfterCommit(ctx, func() { ran = append(ran, "rolled back") })
		return errors.New("roll back")
	})
	func() {
		defer func() { _ = recover() }()
		_ = uow.Do(ctx, func(ctx context.Context) error {
			uow.AfterCommit(ctx, func() { ran = append(ran, "panicked") })
			panic("boom")
		})
	}()
	if len(ran) != 0 {
		t.Fatalf("hooks of a rolled-back unit of work ran: %v", ran)
	}
	uow.AfterCommit(ctx, func() { ran = append(ran, "no unit of work") })
	if len(ran) != 1 {
		t.Fatal("outside a unit of work a hook runs at once")
	}
}

func TestDoublesBehave(t *testing.T) {
	ids := memory.NewIDs("id-")
	if ids.NewID() != "id-1" || ids.NewID() != "id-2" {
		t.Fatal("ids are not sequential")
	}
	c := memory.NewClock(t0)
	c.Advance(time.Minute)
	if !c.Now().Equal(t0.Add(time.Minute)) {
		t.Fatal("Advance")
	}
	d := memory.NewDownlink()
	d.Send(context.Background(), "ASRS1", telegrams.Command{TelegramID: "t1", Type: telegrams.CmdStateRequest})
	if len(d.Sent("ASRS1")) != 1 || d.Last("ASRS1").TelegramID != "t1" {
		t.Fatal("Downlink did not record")
	}
	d.Reset()
	if len(d.Sent("ASRS1")) != 0 {
		t.Fatal("Reset")
	}
}
