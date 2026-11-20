package memory

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/IQVO/asrs-control/internal/application/idempotency"
	"github.com/IQVO/asrs-control/internal/application/telegrams"
)

type Telegrams struct{ s *Store }

func NewTelegrams(s *Store) *Telegrams { return &Telegrams{s: s} }

func (r *Telegrams) Claim(_ context.Context, asrsID, telegramID, ackID string) (string, bool, error) {
	first, claimed := ackID, true
	r.s.with(func(t *tables) {
		k := telegramKey{asrsID, telegramID}
		if prev, ok := t.telegrams[k]; ok {
			first, claimed = prev, false
			return
		}
		t.telegrams[k] = ackID
	})
	return first, claimed, nil
}

func (r *Telegrams) Purge(context.Context, time.Time) (int64, error) { return 0, nil }

// Events is the in-memory EventSink: it records the domain events in the unit of work.
type Events struct{ s *Store }

func NewEvents(s *Store) *Events { return &Events{s: s} }

func (e *Events) Append(_ context.Context, events ...any) error {
	e.s.with(func(t *tables) { t.events = append(t.events, events...) })
	return nil
}

// All returns every committed event in order.
func (e *Events) All() []any {
	var out []any
	e.s.with(func(t *tables) { out = append(out, t.events...) })
	return out
}

// Idempotency is the in-memory ports.IdempotencyStore.
type Idempotency struct{ s *Store }

func NewIdempotency(s *Store) *Idempotency { return &Idempotency{s: s} }

func (r *Idempotency) Lookup(_ context.Context, key string, notBefore time.Time) (idempotency.IdempotencyRecord, bool, error) {
	var rec idempotency.IdempotencyRecord
	var ok bool
	r.s.with(func(t *tables) { rec, ok = t.idem[key] })
	if !ok || rec.CreatedAt.Before(notBefore) {
		return idempotency.IdempotencyRecord{}, false, nil
	}
	return rec, true, nil
}

func (r *Idempotency) Save(_ context.Context, rec idempotency.IdempotencyRecord, notBefore time.Time) error {
	r.s.with(func(t *tables) {
		if prev, ok := t.idem[rec.Key]; ok && !prev.CreatedAt.Before(notBefore) {
			return
		}
		t.idem[rec.Key] = rec
	})
	return nil
}

func (r *Idempotency) Purge(_ context.Context, before time.Time) (int64, error) {
	var n int64
	r.s.with(func(t *tables) {
		for k, rec := range t.idem {
			if rec.CreatedAt.Before(before) {
				delete(t.idem, k)
				n++
			}
		}
	})
	return n, nil
}

// Rows is 1 when key has a stored answer, else 0.
func (r *Idempotency) Rows(key string) int {
	n := 0
	r.s.with(func(t *tables) {
		if _, ok := t.idem[key]; ok {
			n = 1
		}
	})
	return n
}

// Clock is a settable clock.
type Clock struct {
	mu sync.Mutex
	t  time.Time
}

func NewClock(t time.Time) *Clock { return &Clock{t: t} }

func (c *Clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }

func (c *Clock) Set(t time.Time) { c.mu.Lock(); defer c.mu.Unlock(); c.t = t }

func (c *Clock) Advance(d time.Duration) { c.mu.Lock(); defer c.mu.Unlock(); c.t = c.t.Add(d) }

// IDs mints prefix1, prefix2, ...
type IDs struct {
	mu     sync.Mutex
	prefix string
	n      int
}

func NewIDs(prefix string) *IDs { return &IDs{prefix: prefix} }

func (i *IDs) NewID() string {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.n++
	return i.prefix + strconv.Itoa(i.n)
}

// Downlink records the commands sent to each AS/RS.
type Downlink struct {
	mu   sync.Mutex
	sent map[string][]telegrams.Command
}

func NewDownlink() *Downlink { return &Downlink{sent: map[string][]telegrams.Command{}} }

func (d *Downlink) Send(_ context.Context, asrsID string, cmds ...telegrams.Command) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sent[asrsID] = append(d.sent[asrsID], cmds...)
}

func (d *Downlink) Sent(asrsID string) []telegrams.Command {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]telegrams.Command(nil), d.sent[asrsID]...)
}

// Last is the last command sent to asrsID (the zero Command when none).
func (d *Downlink) Last(asrsID string) telegrams.Command {
	s := d.Sent(asrsID)
	if len(s) == 0 {
		return telegrams.Command{}
	}
	return s[len(s)-1]
}

func (d *Downlink) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sent = map[string][]telegrams.Command{}
}
