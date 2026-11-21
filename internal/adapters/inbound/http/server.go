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
	"github.com/riandyrn/otelchi"
	otelchimetric "github.com/riandyrn/otelchi/metric"

	"github.com/IQVO/asrs-control/internal/adapters/inbound/requestlog"
	"github.com/IQVO/asrs-control/internal/application/usecases"
)

// DefaultServiceName names the server spans and the HTTP metrics when Server.ServiceName is empty.
const DefaultServiceName = "asrs-control"

// Server holds what the handlers need. Idempotency nil leaves the POSTs unwrapped (handler unit
// tests only; cmd/ always wires it). Readiness nil means always ready.
type Server struct {
	Service     *usecases.Service
	Idempotency func(http.Handler) http.Handler
	Readiness   *Readiness
	// ServiceName names the server spans and http.server.request.duration (OTEL_SERVICE_NAME).
	ServiceName string
}

// NewRouter builds every route of apis/openapi.yaml.
//
// Middleware order is the fleet observability standard (ADR 0004): RequestID, then otelchi (the
// server span, named after the chi route pattern), then the request-duration histogram, then the
// access log (inside the span, so its record carries trace_id and span_id), then the panic
// recoverer that writes the problem+json 500. telemetry.Setup must have run before this call:
// otelchimetric binds the global MeterProvider when it is constructed.
func NewRouter(s *Server, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	name := s.ServiceName
	if name == "" {
		name = DefaultServiceName
	}
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(otelchi.Middleware(name, otelchi.WithChiRoutes(r)))
	r.Use(otelchimetric.NewServerRequestDuration(otelchimetric.NewBaseConfig(name)))
	r.Use(requestlog.RequestLogger(logger))
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
