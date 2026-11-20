//go:build integration

// Package pgusecases_test runs the real use cases over the Postgres adapter. It is a test-only
// package under internal/testing because it must import both the application layer and the
// outbound adapter, which the adapters themselves may not do (hexagonal dependency rule).
package pgusecases_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/IQVO/asrs-control/internal/adapters/outbound/memory"
	"github.com/IQVO/asrs-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/asrs-control/internal/application/usecases"
	"github.com/IQVO/asrs-control/internal/domain/layout"
	"github.com/IQVO/asrs-control/internal/testing/pgtest"
)

func twoAisles() layout.Layout {
	return layout.Layout{ID: "ASRS1", Aisles: 2, Levels: 1, Slots: 2, DoubleDeep: true, Cost: layout.Cost{PerLevel: 1, PerSlot: 1},
		Ports: []layout.Port{{ID: "IN1", Aisle: 1, Kind: layout.PortInput}, {ID: "OUT1", Aisle: 1, Kind: layout.PortOutput},
			{ID: "IN2", Aisle: 2, Kind: layout.PortInput}, {ID: "OUT2", Aisle: 2, Kind: layout.PortOutput}}}
}

// Two stores of one container through input ports of different aisles hold different aisle
// locks; the partial unique index on open external jobs is what admits exactly one.
func TestConcurrentStoresOfOneContainerViaTwoAislesAdmitExactlyOne(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	// The event sink is not part of this task (Task 12 encodes CloudEvents into the outbox):
	// the in-memory sink only records the events.
	svc, err := usecases.New(usecases.Deps{Layouts: []layout.Layout{twoAisles()}, UoW: postgres.NewUnitOfWork(pool),
		Maps: postgres.NewStorageMapRepo(pool), Jobs: postgres.NewStorageJobRepo(pool), Units: postgres.NewUnitRepo(pool),
		Capacity: postgres.NewCapacityRepo(pool), Telegrams: postgres.NewTelegramInbox(pool),
		Events: memory.NewEvents(memory.NewStore()), Downlink: memory.NewDownlink(), Clock: memory.NewClock(time.Now()),
		IDs: memory.NewIDs("job-"), Velocity: usecases.VelocityPolicy{Window: 24 * time.Hour, FastRetrievals: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, port := range []string{"IN1", "IN2"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = svc.RequestStore(ctx, usecases.StoreRequest{AsrsID: "ASRS1", ContainerID: "C1", PortID: port})
		}()
	}
	wg.Wait()
	ok, busy := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, usecases.ErrContainerBusy):
			busy++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 || busy != 1 {
		t.Fatalf("results = %v, want exactly one success and one ErrContainerBusy", errs)
	}
}
