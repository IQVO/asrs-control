package usecases_test

import (
	"errors"
	"testing"

	"github.com/IQVO/asrs-control/internal/application/usecases"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
)

func ids(p usecases.JobPage) []string {
	out := make([]string, 0, len(p.Jobs))
	for _, j := range p.Jobs {
		out = append(out, j.ID())
	}
	return out
}

func TestListJobsPagesExternalJobsNewestFirst(t *testing.T) {
	h := newHarness(t, tinyLayout())
	a := h.stored("A", "IN1")
	b := h.stored("B", "IN1")
	c := h.stored("C", "IN1") // blocks A: retrieving A reshuffles C first (an internal job)
	r := h.retrieve("A")

	first, err := h.svc.ListJobs(h.ctx, usecases.ListJobsRequest{AsrsID: "ASRS1", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(first); len(got) != 2 || got[0] != r.ID() || got[1] != c.ID() || first.NextBeforeSeq == 0 {
		t.Fatalf("page 1 = %v next %d, want [%s %s] and a next page", got, first.NextBeforeSeq, r.ID(), c.ID())
	}
	second, err := h.svc.ListJobs(h.ctx, usecases.ListJobsRequest{AsrsID: "ASRS1", Limit: 2, BeforeSeq: first.NextBeforeSeq})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(second); len(got) != 2 || got[0] != b.ID() || got[1] != a.ID() || second.NextBeforeSeq != 0 {
		t.Fatalf("page 2 = %v next %d, want [%s %s] and no next page (reshuffles never listed)", got, second.NextBeforeSeq, b.ID(), a.ID())
	}
}

func TestListJobsFiltersByStatus(t *testing.T) {
	h := newHarness(t, tinyLayout())
	done := h.stored("A", "IN1")
	open := h.store("B", "IN1")
	page, err := h.svc.ListJobs(h.ctx, usecases.ListJobsRequest{AsrsID: "ASRS1", Status: storagejob.Done})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(page); len(got) != 1 || got[0] != done.ID() {
		t.Fatalf("DONE jobs = %v, want [%s]", got, done.ID())
	}
	page, _ = h.svc.ListJobs(h.ctx, usecases.ListJobsRequest{AsrsID: "ASRS1", Status: storagejob.Dispatched})
	if got := ids(page); len(got) != 1 || got[0] != open.ID() {
		t.Fatalf("DISPATCHED jobs = %v, want [%s]", got, open.ID())
	}
}

func TestListJobsDefaultsAndBoundsTheLimit(t *testing.T) {
	h := newHarness(t, twoAisles())
	h.store("A", "IN1")
	h.store("B", "IN2")
	h.store("C", "IN2")
	page, err := h.svc.ListJobs(h.ctx, usecases.ListJobsRequest{AsrsID: "ASRS1"})
	if err != nil || len(page.Jobs) != 3 || page.NextBeforeSeq != 0 {
		t.Fatalf("default page = %d jobs next %d (%v), want all 3 and no next page", len(page.Jobs), page.NextBeforeSeq, err)
	}
	for _, limit := range []int{-1, usecases.MaxJobPageLimit + 1} {
		if _, err := h.svc.ListJobs(h.ctx, usecases.ListJobsRequest{AsrsID: "ASRS1", Limit: limit}); !errors.Is(err, usecases.ErrInvalidRequest) {
			t.Errorf("limit %d: %v, want ErrInvalidRequest", limit, err)
		}
	}
	if _, err := h.svc.ListJobs(h.ctx, usecases.ListJobsRequest{AsrsID: "ASRS1", Limit: usecases.MaxJobPageLimit}); err != nil {
		t.Errorf("limit %d is the maximum and must be accepted: %v", usecases.MaxJobPageLimit, err)
	}
}

func TestListJobsRefusesAnUnknownAsrsStatusOrCursor(t *testing.T) {
	h := newHarness(t, tinyLayout())
	cases := []struct {
		name string
		r    usecases.ListJobsRequest
		want error
	}{
		{"unknown AS/RS", usecases.ListJobsRequest{AsrsID: "NOPE"}, usecases.ErrUnknownAsrs},
		{"unknown status", usecases.ListJobsRequest{AsrsID: "ASRS1", Status: "LOST"}, usecases.ErrInvalidRequest},
		{"negative cursor", usecases.ListJobsRequest{AsrsID: "ASRS1", BeforeSeq: -1}, usecases.ErrInvalidRequest},
	}
	for _, c := range cases {
		if _, err := h.svc.ListJobs(h.ctx, c.r); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
}

func TestAsrsIDsAndCapacityAreReadable(t *testing.T) {
	h := newHarness(t, twoAisles())
	if got := h.svc.AsrsIDs(); len(got) != 1 || got[0] != "ASRS1" {
		t.Fatalf("AsrsIDs = %v", got)
	}
	h.store("A", "IN1")
	snap, err := h.svc.Capacity(h.ctx, "ASRS1")
	if err != nil {
		t.Fatal(err)
	}
	if snap.AsrsID != "ASRS1" || snap.UnitsTotal != 2 || snap.PositionsTotal != 8 || snap.OpenJobs != 1 || snap.PositionsFree != 7 {
		t.Fatalf("capacity = %+v", snap)
	}
	if _, err := h.svc.Capacity(h.ctx, "NOPE"); !errors.Is(err, usecases.ErrUnknownAsrs) {
		t.Fatalf("unknown AS/RS capacity: %v", err)
	}
}
