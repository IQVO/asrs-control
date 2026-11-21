package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	inboundhttp "github.com/IQVO/asrs-control/internal/adapters/inbound/http"
)

// shutdownTimeout bounds the HTTP drain (the chart's grace period is larger).
const shutdownTimeout = 10 * time.Second

// run starts the links, recovers every AS/RS, runs the relay, the purger and HTTP on ln until
// ctx ends, then drains.
func (a *app) run(ctx context.Context, ln net.Listener) error {
	for _, l := range a.links {
		l.Start(ctx)
	}
	for _, l := range a.layouts {
		if err := a.svc.Recover(ctx, l.ID); err != nil {
			return err
		}
	}
	go func() { _ = a.relay.Run(ctx) }()
	go a.purge(ctx)
	go func() {
		<-ctx.Done()
		a.readiness.SetNotReady()
	}()
	errCh := make(chan error, 1)
	go func() {
		if err := a.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	a.log.Info("asrs-control listening", "addr", ln.Addr().String(), "asrs", len(a.layouts))
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	drain, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return a.server.Shutdown(drain)
}

// purge enforces the 24-hour idempotency window and the inbox retention, hourly.
func (a *app) purge(ctx context.Context) {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		before := time.Now().UTC().Add(-inboundhttp.IdempotencyWindow)
		if _, err := a.idem.Purge(ctx, before); err != nil && ctx.Err() == nil {
			a.log.Warn("idempotency purge failed", "error", err)
		}
		if _, err := a.inbox.Purge(ctx, before); err != nil && ctx.Err() == nil {
			a.log.Warn("telegram inbox purge failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
