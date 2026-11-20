//go:build integration

package postgres_test

import (
	"context"
	"strings"
	"testing"

	"github.com/IQVO/asrs-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/asrs-control/internal/domain/layout"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
	"github.com/IQVO/asrs-control/internal/testing/pgtest"
)

func jobIDs(js []*storagejob.Job) []string {
	out := make([]string, 0, len(js))
	for _, j := range js {
		out = append(out, j.ID())
	}
	return out
}

// ListExternal feeds list_storage_jobs (ADR 0005): external jobs only, newest first by seq (ids
// sort differently on purpose), keyset by seq, an optional status filter, one AS/RS only.
func TestListExternalPagesExternalJobsNewestFirst(t *testing.T) {
	ctx := context.Background()
	jobs := postgres.NewStorageJobRepo(pgtest.Pool(t))
	insert := func(j *storagejob.Job, err error) *storagejob.Job {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		if err := jobs.Insert(ctx, j); err != nil {
			t.Fatal(err)
		}
		return j
	}
	to := layout.Position{Aisle: 1, Level: 1, Slot: 1, Depth: 1}
	insert(storagejob.NewStore(storagejob.Spec{ID: "sj-9", AsrsID: "ASRS1", Aisle: 1, ContainerID: "C9", PortID: "IN1", To: to, At: t0}))
	insert(storagejob.NewReshuffle(storagejob.Spec{ID: "sj-r", AsrsID: "ASRS1", Aisle: 1, ContainerID: "CR", ParentID: "sj-1",
		From: to, To: layout.Position{Aisle: 1, Level: 1, Slot: 2, Depth: 1}, At: t0}))
	done := insert(storagejob.NewRetrieve(storagejob.Spec{ID: "sj-1", AsrsID: "ASRS1", Aisle: 1, ContainerID: "C1", PortID: "OUT1", At: t0}))
	insert(storagejob.NewStore(storagejob.Spec{ID: "sj-5", AsrsID: "ASRS1", Aisle: 1, ContainerID: "C5", PortID: "IN1", To: to, At: t0}))
	insert(storagejob.NewStore(storagejob.Spec{ID: "sj-x", AsrsID: "ASRS2", Aisle: 1, ContainerID: "CX", PortID: "IN1", To: to, At: t0}))
	if _, err := done.Fail(storagejob.Failure{Reason: storagejob.ReasonEquipmentFault, At: t0}); err != nil {
		t.Fatal(err)
	}
	if err := jobs.Update(ctx, done); err != nil {
		t.Fatal(err)
	}

	all := wantJobs(t, "newest first, no reshuffle, no other AS/RS", "sj-5,sj-1,sj-9")(jobs.ListExternal(ctx, "ASRS1", "", 0, 10))
	wantJobs(t, "the page after sj-5", "sj-1")(jobs.ListExternal(ctx, "ASRS1", "", all[0].Seq(), 1))
	wantJobs(t, "FAILED", "sj-1")(jobs.ListExternal(ctx, "ASRS1", storagejob.Failed, 0, 10))
}

// wantJobs asserts a ListExternal answer is exactly the comma-separated ids, and returns it.
func wantJobs(t *testing.T, what, want string) func([]*storagejob.Job, error) []*storagejob.Job {
	t.Helper()
	return func(js []*storagejob.Job, err error) []*storagejob.Job {
		t.Helper()
		if got := strings.Join(jobIDs(js), ","); err != nil || got != want {
			t.Fatalf("%s = %s (%v), want %s", what, got, err, want)
		}
		return js
	}
}
