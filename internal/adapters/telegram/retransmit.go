package telegram

import (
	"sync"
	"time"
)

type pending struct {
	t        Telegram
	next     time.Time
	attempts int
	expired  bool
}

// Retransmitter tracks down telegrams until they are acknowledged (protocol rule 2: resend
// after interval, at most max times). An expiry is reported once and stays tracked until the
// link's worker Forgets it, so an ACK handled before the expiry still finds it.
type Retransmitter struct {
	mu       sync.Mutex
	interval time.Duration
	max      int
	items    map[string]*pending
}

func NewRetransmitter(interval time.Duration, max int) *Retransmitter {
	return &Retransmitter{interval: interval, max: max, items: map[string]*pending{}}
}

// Track (re)starts tracking t.
func (r *Retransmitter) Track(t Telegram, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[t.ID] = &pending{t: t, next: now.Add(r.interval)}
}

// Ack stops tracking id and returns the telegram it tracked.
func (r *Retransmitter) Ack(id string) (Telegram, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.items[id]
	if !ok {
		return Telegram{}, false
	}
	delete(r.items, id)
	return p.t, true
}

// Forget removes an expired telegram; it reports whether one was still tracked.
func (r *Retransmitter) Forget(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.items[id]
	delete(r.items, id)
	return ok
}

// Due returns the telegrams to resend now and those that just ran out of attempts.
func (r *Retransmitter) Due(now time.Time) (resend, expired []Telegram) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.items {
		if p.expired || now.Before(p.next) {
			continue
		}
		if p.attempts >= r.max {
			p.expired = true
			expired = append(expired, p.t)
			continue
		}
		p.attempts++
		p.next = now.Add(r.interval)
		resend = append(resend, p.t)
	}
	return resend, expired
}
