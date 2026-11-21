//go:build integration

package pgusecases_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime/pprof"
	"strings"
	"sync"
	"testing"
	"time"

	inboundhttp "github.com/IQVO/asrs-control/internal/adapters/inbound/http"
	"github.com/IQVO/asrs-control/internal/adapters/outbound/memory"
	"github.com/IQVO/asrs-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/asrs-control/internal/application/usecases"
	"github.com/IQVO/asrs-control/internal/domain/layout"
	"github.com/IQVO/asrs-control/internal/testing/pgtest"
)

// gatedEvents is the EventSink of the in-flight probe: the first Append (the original request,
// inside its unit of work, after it took the aisle and unit row locks) parks until released.
type gatedEvents struct {
	*memory.Events
	once    sync.Once
	parked  chan struct{}
	release chan struct{}
}

func (g *gatedEvents) Append(ctx context.Context, events ...any) error {
	first := false
	g.once.Do(func() { first = true })
	if first {
		close(g.parked)
		<-g.release
	}
	return g.Events.Append(ctx, events...)
}

type answer struct {
	code     int
	replayed string
	body     map[string]any
	raw      string
}

func postStore(t *testing.T, h http.Handler, key, body string) answer {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/asrs/ASRS1/storage-jobs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(inboundhttp.IdempotencyKeyHeader, key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	raw, _ := io.ReadAll(rec.Body)
	a := answer{code: rec.Code, replayed: rec.Header().Get(inboundhttp.IdempotencyReplayedHeader), raw: string(raw)}
	_ = json.Unmarshal(raw, &a.body)
	return a
}

// carry (fleet audit 2026-11-21, defect 1 / P3-R15b): an in-flight duplicate replays. The
// original POST holds the aisle row lock (storage_maps FOR UPDATE) and is parked inside its
// unit of work; the same-key/same-body duplicate passes the key Lookup (nothing committed yet),
// queues on that lock, and decides only after the original committed, against a container that
// now has an open job: container-busy, RETRYABLE, never stored. It must answer the original's
// stored 202 with Idempotency-Replayed instead of its own 422.
func TestAnInFlightDuplicateStoreRequestReplaysTheOriginalOverPostgres(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	events := &gatedEvents{Events: memory.NewEvents(memory.NewStore()), parked: make(chan struct{}), release: make(chan struct{})}
	uow := postgres.NewUnitOfWork(pool)
	clock := memory.NewClock(time.Now().UTC())
	svc, err := usecases.New(usecases.Deps{Layouts: []layout.Layout{twoAisles()}, UoW: uow,
		Maps: postgres.NewStorageMapRepo(pool), Jobs: postgres.NewStorageJobRepo(pool), Units: postgres.NewUnitRepo(pool),
		Capacity: postgres.NewCapacityRepo(pool), Telegrams: postgres.NewTelegramInbox(pool),
		Events: events, Downlink: memory.NewDownlink(), Clock: clock,
		IDs: memory.NewIDs("job-"), Velocity: usecases.VelocityPolicy{Window: 24 * time.Hour, FastRetrievals: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	idem := inboundhttp.NewIdempotency(postgres.NewIdempotencyStore(pool), uow, clock)
	h := inboundhttp.NewRouter(&inboundhttp.Server{Service: svc, Idempotency: idem.Middleware}, discard)

	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(events.release) }) }
	t.Cleanup(release) // a failed wait must not leave the original parked on the pool
	const key, body = "k-inflight-1", `{"kind":"STORE","container_id":"C1","port_id":"IN1"}`
	var original, duplicate answer
	var wg sync.WaitGroup
	wg.Go(func() { original = postStore(t, h, key, body) })
	select {
	case <-events.parked:
	case <-time.After(10 * time.Second):
		_ = pprof.Lookup("goroutine").WriteTo(os.Stderr, 2)
		t.Fatal("the original request never reached its event append (aisle lock held)")
	}
	wg.Go(func() { duplicate = postStore(t, h, key, body) })
	waitForLockWaiter(t, func(sql string) int {
		var n int
		if err := pool.QueryRow(ctx, sql).Scan(&n); err != nil {
			t.Errorf("count: %v", err)
		}
		return n
	})
	release()
	wg.Wait()

	if original.code != http.StatusAccepted || original.replayed != "" {
		t.Fatalf("original = %d replayed=%q %s, want a fresh 202", original.code, original.replayed, original.raw)
	}
	if duplicate.code != http.StatusAccepted || duplicate.replayed != "true" ||
		duplicate.body["storage_job_id"] != original.body["storage_job_id"] {
		t.Fatalf("in-flight duplicate = %d replayed=%q %s; want the original's 202 (job %v) replayed, not its own refusal",
			duplicate.code, duplicate.replayed, duplicate.raw, original.body["storage_job_id"])
	}
	var keys, jobs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM idempotency_keys WHERE key = $1`, key).Scan(&keys); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM storage_jobs`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if keys != 1 || jobs != 1 {
		t.Fatalf("key rows = %d, storage jobs = %d, want 1 and 1", keys, jobs)
	}
}

// waitForLockWaiter waits (bounded) until a session of this database waits on a lock: the
// duplicate queued behind the original's aisle row lock.
func waitForLockWaiter(t *testing.T, count func(sql string) int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	got := 0
	for time.Now().Before(deadline) {
		got = count(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`)
		if got >= 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = pprof.Lookup("goroutine").WriteTo(os.Stderr, 2)
	t.Fatalf("%d sessions wait on a lock after 10 s, want the duplicate queued on the aisle lock", got)
}
