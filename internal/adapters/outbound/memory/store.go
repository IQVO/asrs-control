// Package memory holds in-memory TEST DOUBLES of the application ports. cmd/ never imports it
// (TestRuntimeBinaryHasNoInMemoryAdapters). One Store holds every table; a UnitOfWork
// serialises units of work and restores the whole store when fn fails, so the doubles have
// the same commit-or-rollback semantics as Postgres.
package memory

import (
	"context"
	"maps"
	"sync"

	"github.com/IQVO/asrs-control/internal/application/idempotency"
	"github.com/IQVO/asrs-control/internal/domain/capacity"
	"github.com/IQVO/asrs-control/internal/domain/equipment"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
	"github.com/IQVO/asrs-control/internal/domain/storagemap"
)

type aisleKey struct {
	asrs  string
	aisle int
}

type telegramKey struct{ asrs, id string }

type tables struct {
	maps      map[aisleKey]storagemap.Snapshot
	jobs      map[string]storagejob.State
	seq       int64
	units     map[aisleKey]equipment.UnitState
	capacity  map[string]capacity.Snapshot
	telegrams map[telegramKey]string
	events    []any
	idem      map[string]idempotency.IdempotencyRecord
	tags      map[string]int
}

// Store is the in-memory database.
type Store struct {
	tx sync.Mutex // serialises units of work
	mu sync.Mutex // guards t
	t  tables
}

func NewStore() *Store {
	return &Store{t: tables{maps: map[aisleKey]storagemap.Snapshot{}, jobs: map[string]storagejob.State{},
		units: map[aisleKey]equipment.UnitState{}, capacity: map[string]capacity.Snapshot{},
		telegrams: map[telegramKey]string{}, idem: map[string]idempotency.IdempotencyRecord{}, tags: map[string]int{}}}
}

func (s *Store) with(fn func(t *tables)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.t)
}

// cloneSnapshot deep-copies every map of the snapshot, ReservedContainer included.
func cloneSnapshot(m storagemap.Snapshot) storagemap.Snapshot {
	return storagemap.Snapshot{Aisle: m.Aisle, Occupied: maps.Clone(m.Occupied), Reserved: maps.Clone(m.Reserved),
		ReservedContainer: maps.Clone(m.ReservedContainer),
		Retrieving:        maps.Clone(m.Retrieving), Moving: maps.Clone(m.Moving), Presented: maps.Clone(m.Presented)}
}

func (s *Store) save() tables {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := tables{maps: map[aisleKey]storagemap.Snapshot{}, jobs: maps.Clone(s.t.jobs), seq: s.t.seq,
		units: maps.Clone(s.t.units), capacity: maps.Clone(s.t.capacity), telegrams: maps.Clone(s.t.telegrams),
		events: append([]any(nil), s.t.events...), idem: maps.Clone(s.t.idem), tags: maps.Clone(s.t.tags)}
	for k, v := range s.t.maps {
		c.maps[k] = cloneSnapshot(v)
	}
	return c
}

func (s *Store) restore(t tables) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.t = t
}

// Tag is a scratch write inside the caller's unit of work (replay-class test adapter).
func (s *Store) Tag(_ context.Context, tag string) error {
	s.with(func(t *tables) { t.tags[tag]++ })
	return nil
}

// Tagged counts committed Tag writes.
func (s *Store) Tagged(tag string) int {
	n := 0
	s.with(func(t *tables) { n = t.tags[tag] })
	return n
}

type txKey struct{}

// txState is the outermost unit of work's after-commit hooks.
type txState struct{ hooks []func() }

// UnitOfWork is the in-memory ports.UnitOfWork.
type UnitOfWork struct{ s *Store }

func NewUnitOfWork(s *Store) *UnitOfWork { return &UnitOfWork{s: s} }

// Do runs fn alone; a failing or panicking fn restores the store as it was and drops the
// after-commit hooks; a successful outermost Do runs them, in order, after it released the store.
func (u *UnitOfWork) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, joined := ctx.Value(txKey{}).(*txState); joined {
		return fn(ctx)
	}
	st := &txState{}
	if err := u.run(ctx, st, fn); err != nil {
		return err
	}
	for _, h := range st.hooks {
		h()
	}
	return nil
}

func (u *UnitOfWork) run(ctx context.Context, st *txState, fn func(ctx context.Context) error) (err error) {
	u.s.tx.Lock()
	defer u.s.tx.Unlock()
	saved := u.s.save()
	defer func() {
		if r := recover(); r != nil {
			u.s.restore(saved)
			panic(r)
		}
		if err != nil {
			u.s.restore(saved)
		}
	}()
	return fn(context.WithValue(ctx, txKey{}, st))
}

// AfterCommit implements ports.UnitOfWork.
func (u *UnitOfWork) AfterCommit(ctx context.Context, fn func()) {
	if st, ok := ctx.Value(txKey{}).(*txState); ok {
		st.hooks = append(st.hooks, fn)
		return
	}
	fn()
}
