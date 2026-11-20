//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/IQVO/asrs-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/asrs-control/internal/application/outbox"
	"github.com/IQVO/asrs-control/internal/testing/pgtest"
)

func TestOutboxRowsCommitWithTheUnitOfWorkAndDrainInOrder(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	repo, uow := postgres.NewOutboxRepo(pool), postgres.NewUnitOfWork(pool)
	msg := func(id string) outbox.Message {
		return outbox.Message{EventID: id, Topic: "warehouse.asrs-control.events", EventType: "t", Subject: "s", Key: []byte("s"),
			DataSchema: "urn", Value: []byte(`{}`), Headers: []outbox.Header{{Key: "content-type", Value: "application/cloudevents+json; charset=UTF-8"}}}
	}
	_ = uow.Do(ctx, func(ctx context.Context) error {
		_ = repo.Insert(ctx, msg("rolled-back"))
		return errors.New("roll back")
	})
	if err := uow.Do(ctx, func(ctx context.Context) error { return repo.Insert(ctx, msg("e1"), msg("e2")) }); err != nil {
		t.Fatal(err)
	}
	var sent []string
	n, err := repo.Drain(ctx, 10, 20, func(_ context.Context, m outbox.Message) error {
		sent = append(sent, m.EventID)
		if m.Headers[0].Key != "content-type" {
			t.Errorf("headers lost: %+v", m.Headers)
		}
		return nil
	})
	if err != nil || n != 2 || len(sent) != 2 || sent[0] != "e1" || sent[1] != "e2" {
		t.Fatalf("Drain = %d %v %v", n, sent, err)
	}
	_ = repo.Insert(ctx, msg("e3"))
	_, err = repo.Drain(ctx, 10, 1, func(context.Context, outbox.Message) error { return errors.New("broker down") })
	var sf *outbox.SendFailureError
	if !errors.As(err, &sf) || sf.Attempts != 1 {
		t.Fatalf("failed pass = %v", err)
	}
	if n, _ := repo.Drain(ctx, 10, 1, func(context.Context, outbox.Message) error { return nil }); n != 0 {
		t.Fatalf("a row at the attempt cap was claimed again (%d)", n)
	}
}

func TestAfterCommitHooksRunAfterTheCommitAndNeverAfterARollback(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	uow := postgres.NewUnitOfWork(pool)
	committed := func() int {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM processed_telegrams`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	inbox := postgres.NewTelegramInbox(pool)
	var seen []int
	err := uow.Do(ctx, func(ctx context.Context) error {
		if _, _, err := inbox.Claim(ctx, "ASRS1", "u1", "a1"); err != nil {
			return err
		}
		uow.AfterCommit(ctx, func() { seen = append(seen, committed()) })
		return uow.Do(ctx, func(ctx context.Context) error { // joined: its hook waits for the outer commit too
			uow.AfterCommit(ctx, func() { seen = append(seen, -1) })
			return nil
		})
	})
	if err != nil || len(seen) != 2 || seen[0] != 1 || seen[1] != -1 {
		t.Fatalf("hooks = %v (%v), want [1 -1]: run in order, after the row was committed", seen, err)
	}
	seen = nil
	_ = uow.Do(ctx, func(ctx context.Context) error {
		uow.AfterCommit(ctx, func() { seen = append(seen, 99) })
		return errors.New("roll back")
	})
	if len(seen) != 0 {
		t.Fatalf("a rolled-back unit of work ran its hooks: %v", seen)
	}
	uow.AfterCommit(ctx, func() { seen = append(seen, 7) })
	if len(seen) != 1 {
		t.Fatal("outside a unit of work a hook runs at once")
	}
}
