//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IQVO/asrs-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/asrs-control/internal/application/outbox"
	"github.com/IQVO/asrs-control/internal/testing/pgtest"
)

// Backlog feeds the asrs_control.outbox.backlog and .lag gauges (ADR 0004): every unpublished
// row counts (an attempt-capped one included) and the lag is the age of the oldest of them.
func TestOutboxBacklogCountsUnpublishedRowsAndAgesTheOldest(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	repo := postgres.NewOutboxRepo(pool)
	rows, lag, err := repo.Backlog(ctx)
	if err != nil || rows != 0 || lag != 0 {
		t.Fatalf("empty outbox: Backlog = %d %v %v, want 0 0 <nil>", rows, lag, err)
	}
	msg := func(id string) outbox.Message {
		return outbox.Message{EventID: id, Topic: "warehouse.asrs-control.events", EventType: "t", Subject: "s", Key: []byte("s"),
			DataSchema: "urn", Value: []byte(`{}`)}
	}
	if err := repo.Insert(ctx, msg("e1"), msg("e2"), msg("e3")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE outbox_events SET created_at = now() - interval '90 seconds' WHERE event_id = 'e1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Drain(ctx, 1, 1, func(context.Context, outbox.Message) error { return errors.New("broker down") }); err == nil {
		t.Fatal("the failing pass returned no error")
	}
	rows, lag, err = repo.Backlog(ctx)
	if err != nil || rows != 3 {
		t.Fatalf("Backlog rows = %d (%v), want 3: an attempt-capped row is still backlog", rows, err)
	}
	if lag < 89*time.Second || lag > 120*time.Second {
		t.Fatalf("Backlog lag = %v, want about 90s (the oldest unpublished row)", lag)
	}
	if _, err := pool.Exec(ctx, `UPDATE outbox_events SET published_at = now()`); err != nil {
		t.Fatal(err)
	}
	if rows, lag, err = repo.Backlog(ctx); err != nil || rows != 0 || lag != 0 {
		t.Fatalf("drained outbox: Backlog = %d %v %v, want 0 0 <nil>", rows, lag, err)
	}
}
