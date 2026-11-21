package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	ahttp "github.com/IQVO/asrs-control/internal/adapters/inbound/http"
	"github.com/IQVO/asrs-control/internal/adapters/outbound/memory"
	"github.com/IQVO/asrs-control/internal/application/idempotency"
	"github.com/IQVO/asrs-control/internal/application/ports"
)

var inflightT0 = time.Date(2026, 11, 21, 8, 0, 0, 0, time.UTC)

// blindStore stands for the key Lookup an in-flight duplicate made BEFORE the original
// committed: while blind > 0 a Lookup reports "not found" (and counts down); every later one
// reads the store. failAfter > 0 makes every Lookup after the first failAfter ones fail, the way
// a database that dropped between the request and the refusal's re-read does.
type blindStore struct {
	ports.IdempotencyStore
	mu        sync.Mutex
	blind     int
	failAfter int
	lookups   int
}

func (s *blindStore) Lookup(ctx context.Context, key string, notBefore time.Time) (idempotency.IdempotencyRecord, bool, error) {
	s.mu.Lock()
	s.lookups++
	n, blind := s.lookups, s.blind > 0
	if blind {
		s.blind--
	}
	s.mu.Unlock()
	if s.failAfter > 0 && n > s.failAfter {
		return idempotency.IdempotencyRecord{}, false, errors.New("idempotency store unreachable")
	}
	if blind {
		return idempotency.IdempotencyRecord{}, false, nil
	}
	return s.IdempotencyStore.Lookup(ctx, key, notBefore)
}

type inflightRig struct {
	store *blindStore
	clock *memory.Clock
	h     *fakeHandler
	mw    http.Handler
	logs  *bytes.Buffer
}

func newInflightRig(script ...answerFn) *inflightRig {
	db := memory.NewStore()
	r := &inflightRig{store: &blindStore{IdempotencyStore: memory.NewIdempotency(db)}, clock: memory.NewClock(inflightT0),
		h: &fakeHandler{script: script}, logs: &bytes.Buffer{}}
	m := ahttp.NewIdempotency(r.store, memory.NewUnitOfWork(db), r.clock)
	m.Logger = slog.New(slog.NewJSONHandler(r.logs, nil))
	r.mw = m.Middleware(r.h)
	return r
}

func accepted(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_, _ = io.WriteString(w, `{"storage_job_id":"sj-1"}`)
}

func refusingWith(slug string) answerFn {
	return func(w http.ResponseWriter, r *http.Request) { ahttp.Refuse(w, r, slug) }
}

func codeOf(t *testing.T, body string) string {
	t.Helper()
	var p map[string]any
	_ = json.Unmarshal([]byte(body), &p)
	code, _ := p["code"].(string)
	return code
}

// carry (fleet audit 2026-11-21, defect 1 / P3-R15b): a duplicate that passed the key Lookup
// while the original was in flight (the blind Lookup stands for it), waited on the aisle lock and
// then decided a RETRYABLE refusal (container-busy: the original's job is open now) answers the
// stored 202 as a replay, not its own 422. A 5xx it reaches the same way replays too, and a copy
// with another body answers 409 idempotency-key-reused. The Postgres twin is
// internal/testing/pgusecases TestAnInFlightDuplicateStoreRequestReplaysTheOriginalOverPostgres.
func TestAnInFlightDuplicateThatDecidesAnUnstoredRefusalReplaysTheStoredAnswer(t *testing.T) {
	r := newInflightRig(accepted, refusingWith("container-busy"))
	if first := post(t, r.mw, "k-dup", `{"n":1}`); first.Code != http.StatusAccepted || replayed(first) {
		t.Fatalf("first = %d replayed=%v %s, want a fresh 202", first.Code, replayed(first), first.Body)
	}
	r.store.blind = 1
	dup := post(t, r.mw, "k-dup", `{"n":1}`)
	if dup.Code != http.StatusAccepted || !replayed(dup) || dup.Body.String() != `{"storage_job_id":"sj-1"}` {
		t.Fatalf("in-flight duplicate = %d replayed=%v %s, want the first 202 replayed", dup.Code, replayed(dup), dup.Body)
	}
	r.h.script = append(r.h.script, refusingWith("dependency-unavailable"))
	r.store.blind = 1
	if five := post(t, r.mw, "k-dup", `{"n":1}`); five.Code != http.StatusAccepted || !replayed(five) {
		t.Fatalf("in-flight duplicate answering a 5xx = %d replayed=%v %s, want the first 202 replayed", five.Code, replayed(five), five.Body)
	}
	r.store.blind = 1
	other := post(t, r.mw, "k-dup", `{"n":2}`)
	if other.Code != http.StatusConflict || codeOf(t, other.Body.String()) != keyReusedSlug {
		t.Fatalf("in-flight copy with another body = %d %s, want 409 %s", other.Code, other.Body, keyReusedSlug)
	}
	if r.h.calls != 4 {
		t.Fatalf("handler calls = %d, want 4 (every blind copy ran its use case)", r.h.calls)
	}
}

// carry (fleet audit 2026-11-21): the re-read honours the 24 h replay window. A stored answer
// exactly 24 h old is still replayed; one a microsecond older is outside the window, so the
// duplicate's own RETRYABLE refusal goes out (and the key stays released).
func TestTheReReadBeforeAnUnstoredRefusalHonoursThe24HourWindow(t *testing.T) {
	r := newInflightRig(accepted, refusingWith("container-busy"))
	if first := post(t, r.mw, "k-win", `{"n":1}`); first.Code != http.StatusAccepted {
		t.Fatalf("first = %d %s", first.Code, first.Body)
	}
	r.clock.Advance(ahttp.IdempotencyWindow)
	r.store.blind = 1
	if edge := post(t, r.mw, "k-win", `{"n":1}`); edge.Code != http.StatusAccepted || !replayed(edge) {
		t.Fatalf("duplicate at exactly 24 h = %d replayed=%v %s, want the stored 202 replayed", edge.Code, replayed(edge), edge.Body)
	}
	r.clock.Advance(time.Microsecond)
	r.store.blind = 1
	late := post(t, r.mw, "k-win", `{"n":1}`)
	if late.Code != http.StatusUnprocessableEntity || replayed(late) || codeOf(t, late.Body.String()) != "container-busy" {
		t.Fatalf("duplicate past 24 h = %d replayed=%v %s, want its own 422 container-busy", late.Code, replayed(late), late.Body)
	}
}

// carry (fleet audit 2026-11-21, mfc f4b80e1): a failed re-read before an unstored refusal is
// logged at WARN, and the request's own refusal still goes out (the key stays released).
func TestAFailedReReadBeforeAnUnstoredRefusalIsLoggedAndTheRefusalAnswered(t *testing.T) {
	r := newInflightRig(refusingWith("container-busy"))
	r.store.failAfter = 1
	rec := post(t, r.mw, "k-warn", `{"n":1}`)
	if rec.Code != http.StatusUnprocessableEntity || replayed(rec) || codeOf(t, rec.Body.String()) != "container-busy" {
		t.Fatalf("answer = %d replayed=%v %s, want the request's own 422 container-busy", rec.Code, replayed(rec), rec.Body)
	}
	if r.store.lookups != 2 {
		t.Fatalf("lookups = %d, want the first Lookup and the re-read", r.store.lookups)
	}
	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(r.logs.Bytes()), &entry); err != nil {
		t.Fatalf("log = %q, want one JSON line: %v", r.logs.String(), err)
	}
	msg, _ := entry["error"].(string)
	if entry["level"] != "WARN" || entry["msg"] != "idempotency: re-read before an unstored refusal failed" ||
		!strings.Contains(msg, "idempotency store unreachable") {
		t.Fatalf("log entry = %v", entry)
	}
}
