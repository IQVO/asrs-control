package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	ahttp "github.com/IQVO/asrs-control/internal/adapters/inbound/http"
	"github.com/IQVO/asrs-control/internal/adapters/outbound/memory"
	"github.com/IQVO/asrs-control/internal/application/usecases"
	"github.com/IQVO/asrs-control/internal/domain/layout"
)

type api struct {
	t    *testing.T
	h    http.Handler
	svc  *usecases.Service
	down *memory.Downlink
	n    int
}

func newAPI(t *testing.T) *api {
	t.Helper()
	l := layout.Layout{ID: "ASRS1", Aisles: 1, Levels: 1, Slots: 2, DoubleDeep: true, Cost: layout.Cost{PerLevel: 1, PerSlot: 1},
		Ports: []layout.Port{{ID: "IN1", Aisle: 1, Kind: layout.PortInput}, {ID: "OUT1", Aisle: 1, Kind: layout.PortOutput}}}
	st := memory.NewStore()
	clock := memory.NewClock(time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC))
	down := memory.NewDownlink()
	uow := memory.NewUnitOfWork(st)
	svc, err := usecases.New(usecases.Deps{Layouts: []layout.Layout{l}, UoW: uow, Maps: memory.NewStorageMaps(st),
		Jobs: memory.NewStorageJobs(st), Units: memory.NewUnits(st), Capacity: memory.NewCapacities(st), Telegrams: memory.NewTelegrams(st),
		Events: memory.NewEvents(st), Downlink: down, Clock: clock, IDs: memory.NewIDs("id-"), Velocity: usecases.VelocityPolicy{Window: time.Hour, FastRetrievals: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	idem := ahttp.NewIdempotency(memory.NewIdempotency(st), uow, clock)
	return &api{t: t, svc: svc, down: down, h: ahttp.NewRouter(&ahttp.Server{Service: svc, Idempotency: idem.Middleware}, nil)}
}

func (a *api) do(method, path, body, key string) *httptest.ResponseRecorder {
	a.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if method == http.MethodPost {
		if key == "" {
			a.n++
			key = "k-" + strconv.Itoa(a.n)
		}
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	return m
}

func TestStoreIsAcceptedWithALocationAndNoRackPosition(t *testing.T) {
	a := newAPI(t)
	rec := a.do(http.MethodPost, "/asrs/ASRS1/storage-jobs", `{"kind":"STORE","container_id":"C1","port_id":"IN1","reference":"leg-9"}`, "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("store = %d %s", rec.Code, rec.Body.String())
	}
	body := decode(t, rec)
	id, _ := body["storage_job_id"].(string)
	if rec.Header().Get("Location") != "/asrs/ASRS1/storage-jobs/"+id || body["status"] != "DISPATCHED" || body["kind"] != "STORE" ||
		body["reference"] != "leg-9" || body["accepted_at"] != "2026-11-20T08:00:00Z" {
		t.Fatalf("answer = %s %v", rec.Header().Get("Location"), body)
	}
	for _, field := range []string{"to", "from", "position", "level", "slot"} {
		if _, leaked := body[field]; leaked {
			t.Errorf("the response leaks %q", field)
		}
	}
	got := a.do(http.MethodGet, "/asrs/ASRS1/storage-jobs/"+id, "", "")
	if got.Code != http.StatusOK || decode(t, got)["storage_job_id"] != id {
		t.Fatalf("GET job = %d %s", got.Code, got.Body.String())
	}
}

func TestARetryWithTheSameKeyIsReplayed(t *testing.T) {
	a := newAPI(t)
	first := a.do(http.MethodPost, "/asrs/ASRS1/storage-jobs", `{"kind":"STORE","container_id":"C1","port_id":"IN1"}`, "same")
	again := a.do(http.MethodPost, "/asrs/ASRS1/storage-jobs", `{"kind":"STORE","container_id":"C1","port_id":"IN1"}`, "same")
	if again.Code != http.StatusAccepted || again.Header().Get("Idempotency-Replayed") != "true" || again.Body.String() != first.Body.String() {
		t.Fatalf("replay = %d %q %s", again.Code, again.Header().Get("Idempotency-Replayed"), again.Body.String())
	}
	if n := len(a.down.Sent("ASRS1")); n != 1 {
		t.Fatalf("the replay dispatched again: %d commands", n)
	}
	other := a.do(http.MethodPost, "/asrs/ASRS1/storage-jobs", `{"kind":"STORE","container_id":"C2","port_id":"IN1"}`, "same")
	if other.Code != http.StatusConflict || decode(t, other)["code"] != "idempotency-key-reused" {
		t.Fatalf("reuse = %d %s", other.Code, other.Body.String())
	}
	missing := httptest.NewRecorder()
	a.h.ServeHTTP(missing, httptest.NewRequest(http.MethodPost, "/asrs/ASRS1/storage-jobs", strings.NewReader(`{}`)))
	if missing.Code != http.StatusBadRequest || decode(t, missing)["code"] != "idempotency-key-required" {
		t.Fatalf("no key = %d %s", missing.Code, missing.Body.String())
	}
}

func TestARetryableRefusalReleasesItsKey(t *testing.T) {
	a := newAPI(t)
	refused := a.do(http.MethodPost, "/asrs/ASRS1/storage-jobs", `{"kind":"RETRIEVE","container_id":"C1"}`, "r1")
	if refused.Code != http.StatusUnprocessableEntity || decode(t, refused)["code"] != "unknown-container" {
		t.Fatalf("retrieve of a missing container = %d %s", refused.Code, refused.Body.String())
	}
	if again := a.do(http.MethodPost, "/asrs/ASRS1/storage-jobs", `{"kind":"RETRIEVE","container_id":"C1"}`, "r1"); again.Header().Get("Idempotency-Replayed") == "true" {
		t.Fatal("a RETRYABLE refusal was replayed")
	}
}

func TestValidationAndNotFoundProblems(t *testing.T) {
	a := newAPI(t)
	cases := []struct {
		method, path, body string
		status             int
		code               string
	}{
		{http.MethodPost, "/asrs/ASRS1/storage-jobs", `{"kind":"STORE"`, http.StatusBadRequest, "bad-request"},
		{http.MethodPost, "/asrs/ASRS1/storage-jobs", `{"kind":"STORE","container_id":"C1","port_id":"IN1","extra":1}`, http.StatusBadRequest, "bad-request"},
		{http.MethodPost, "/asrs/ASRS1/storage-jobs", `{"kind":"MOVE","container_id":"C1"}`, http.StatusBadRequest, "invalid-parameter"},
		{http.MethodPost, "/asrs/ASRS1/storage-jobs", `{"kind":"STORE","container_id":"C1"}`, http.StatusBadRequest, "invalid-parameter"},
		{http.MethodPost, "/asrs/ASRS1/storage-jobs", `{"kind":"STORE","container_id":"C\u00001","port_id":"IN1"}`, http.StatusBadRequest, "invalid-parameter"},
		{http.MethodPost, "/asrs/ASRS1/storage-jobs", `{"kind":"STORE","container_id":"C1","port_id":"IN1","reference":"a\nb"}`, http.StatusBadRequest, "invalid-parameter"},
		{http.MethodPost, "/asrs/NOPE/storage-jobs", `{"kind":"STORE","container_id":"C1","port_id":"IN1"}`, http.StatusNotFound, "asrs-not-found"},
		{http.MethodPost, "/asrs/ASRS1/storage-jobs", `{"kind":"STORE","container_id":"C1","port_id":"OUT1"}`, http.StatusUnprocessableEntity, "wrong-port-kind"},
		{http.MethodGet, "/asrs/ASRS1/storage-jobs/sj-nope", "", http.StatusNotFound, "storage-job-not-found"},
		{http.MethodGet, "/asrs/NOPE", "", http.StatusNotFound, "asrs-not-found"},
		{http.MethodPost, "/asrs/ASRS1/storage-jobs/sj-nope/cancellation", "", http.StatusNotFound, "storage-job-not-found"},
		{http.MethodPost, "/asrs/ASRS1/ports/OUT1/pickups", `{"container_id":"C1"}`, http.StatusConflict, "port-not-presenting"},
		{http.MethodPost, "/asrs/ASRS1/ports/OUT1/pickups", `{}`, http.StatusBadRequest, "invalid-parameter"},
	}
	for _, c := range cases {
		rec := a.do(c.method, c.path, c.body, "")
		if rec.Code != c.status || rec.Header().Get("Content-Type") != "application/problem+json" || decode(t, rec)["code"] != c.code {
			t.Errorf("%s %s %s = %d %s, want %d %s", c.method, c.path, c.body, rec.Code, rec.Body.String(), c.status, c.code)
		}
	}
}

func TestCancellationAnswers200WhenDoneAnd202WhenThePLCDecides(t *testing.T) {
	a := newAPI(t)
	first := decode(t, a.do(http.MethodPost, "/asrs/ASRS1/storage-jobs", `{"kind":"STORE","container_id":"C1","port_id":"IN1"}`, ""))
	second := decode(t, a.do(http.MethodPost, "/asrs/ASRS1/storage-jobs", `{"kind":"STORE","container_id":"C2","port_id":"IN1"}`, ""))
	queued := a.do(http.MethodPost, "/asrs/ASRS1/storage-jobs/"+second["storage_job_id"].(string)+"/cancellation", "", "")
	if queued.Code != http.StatusOK || decode(t, queued)["status"] != "CANCELLED" || decode(t, queued)["reason"] != "CANCELLED" {
		t.Fatalf("cancel queued = %d %s", queued.Code, queued.Body.String())
	}
	inPLC := a.do(http.MethodPost, "/asrs/ASRS1/storage-jobs/"+first["storage_job_id"].(string)+"/cancellation", "", "")
	if inPLC.Code != http.StatusAccepted || decode(t, inPLC)["status"] != "CANCEL_REQUESTED" {
		t.Fatalf("cancel dispatched = %d %s", inPLC.Code, inPLC.Body.String())
	}
}

func TestTheAsrsViewHasAislesPortsAndCapacity(t *testing.T) {
	a := newAPI(t)
	rec := a.do(http.MethodGet, "/asrs/ASRS1", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET asrs = %d", rec.Code)
	}
	b := decode(t, rec)
	ports := b["ports"].([]any)
	out1 := ports[1].(map[string]any)
	if len(b["aisles"].([]any)) != 1 || len(ports) != 2 || out1["port_id"] != "OUT1" || out1["presented_container_id"] != nil ||
		b["recommended_input_port_id"] != "IN1" || b["capacity"].(map[string]any)["positions_total"] != 4.0 {
		t.Fatalf("view = %v", b)
	}
}

func TestAPanicIsA500Problem(t *testing.T) {
	boom := ahttp.NewReadiness(time.Nanosecond, time.Second, ahttp.Dependency{Name: "x", Check: func(context.Context) error { panic("boom") }})
	h := ahttp.NewRouter(&ahttp.Server{Readiness: boom}, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "internal-error") {
		t.Fatalf("panic = %d %s", rec.Code, rec.Body.String())
	}
}
