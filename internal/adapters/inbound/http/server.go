// Package http is asrs-control's REST adapter: the subsystem-controller interface
// material-flow-control calls (WCS context map relationship 5), problem+json errors with replay
// classes, the Idempotency-Key middleware on every POST and the probes. No auth middleware
// (fleet decision 2026-09-11, TestNoAuthMiddlewareReintroduced).
package http

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/IQVO/asrs-control/internal/application/usecases"
)

// Server holds what the handlers need. Idempotency nil leaves the POSTs unwrapped (handler unit
// tests only; cmd/ always wires it). Readiness nil means always ready.
type Server struct {
	Service     *usecases.Service
	Idempotency func(http.Handler) http.Handler
	Readiness   *Readiness
}

// NewRouter builds every route of apis/openapi.yaml.
func NewRouter(s *Server, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(recoverPanic)
	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", s.handleReadyz)
	r.Get("/asrs/{asrsId}", s.handleGetAsrs)
	r.Get("/asrs/{asrsId}/storage-jobs/{storageJobId}", s.handleGetJob)
	r.Group(func(r chi.Router) {
		if s.Idempotency != nil {
			r.Use(s.Idempotency)
		}
		r.Post("/asrs/{asrsId}/storage-jobs", s.handleCreateJob)
		r.Post("/asrs/{asrsId}/storage-jobs/{storageJobId}/cancellation", s.handleCancel)
		r.Post("/asrs/{asrsId}/ports/{portId}/pickups", s.handlePickup)
	})
	logger.Debug("router built")
	return r
}
