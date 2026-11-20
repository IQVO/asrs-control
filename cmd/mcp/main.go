// Command mcp is asrs-control's read-only MCP server (ADR 0005): it wires the layout file and the
// Postgres read repositories into the SAME read use cases the REST adapter serves, hands them to
// the inbound MCP adapter and serves MCP over Streamable HTTP only (no stdio, no SSE), stateless.
// It is a second, independent deployable beside cmd/api.
//
// READ-ONLY: no unit of work, no outbox, no telegram inbox, no MQTT link and no Kafka are wired
// here, so this binary has no seam to write through and never talks to the PLC. There is NO auth
// of any kind (fleet decision 2026-09-11): access control is the in-cluster ClusterIP boundary.
//
// Environment:
//
//	MCP_ADDR                     listen address (default :8090)
//	DATABASE_URL                 REQUIRED. the Postgres DSN cmd/api owns
//	MIGRATIONS_DATABASE_URL      direct DSN for the boot migrations (default DATABASE_URL)
//	ASRS_LAYOUT_FILE             REQUIRED. the layout YAML (ADR 0002), the same file cmd/api reads
//	LOG_LEVEL                    debug | info | warn | error (default info)
//	OTEL_SERVICE_NAME            service.name (default asrs-control-mcp)
//	OTEL_EXPORTER_OTLP_ENDPOINT  Collector OTLP/gRPC endpoint; empty = telemetry off (ADR 0004)
//	SERVICE_VERSION              service.version (default dev)
//	ENVIRONMENT                  deployment.environment.name (read by the telemetry adapter)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	inboundmcp "github.com/IQVO/asrs-control/internal/adapters/inbound/mcp"
	"github.com/IQVO/asrs-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/asrs-control/internal/adapters/outbound/system"
	"github.com/IQVO/asrs-control/internal/application/usecases"
	layoutfile "github.com/IQVO/asrs-control/internal/config"
)

// shutdownTimeout bounds the HTTP drain.
const shutdownTimeout = 10 * time.Second

type config struct {
	Addr, DatabaseURL, MigrationsURL, LayoutFile, LogLevel string
	ServiceName, ServiceVersion, OTLPEndpoint              string
}

func loadConfig(getenv func(string) string) (config, error) {
	or := func(k, def string) string {
		if v := getenv(k); v != "" {
			return v
		}
		return def
	}
	c := config{Addr: or("MCP_ADDR", ":8090"), DatabaseURL: getenv("DATABASE_URL"), MigrationsURL: postgres.MigrationsURL(getenv),
		LayoutFile: getenv("ASRS_LAYOUT_FILE"), LogLevel: or("LOG_LEVEL", "info"), ServiceName: or("OTEL_SERVICE_NAME", "asrs-control-mcp"),
		ServiceVersion: getenv("SERVICE_VERSION"), OTLPEndpoint: getenv("OTEL_EXPORTER_OTLP_ENDPOINT")}
	var problems []error
	if c.DatabaseURL == "" {
		problems = append(problems, errors.New("DATABASE_URL is required: the MCP server reads asrs-control's Postgres"))
	}
	if c.LayoutFile == "" {
		problems = append(problems, errors.New("ASRS_LAYOUT_FILE is required: the MCP server reads the same layout as cmd/api"))
	}
	return c, errors.Join(problems...)
}

func main() {
	if err := run(); err != nil {
		slog.Error("asrs-control mcp exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		return err
	}
	log := newLogger(os.Stdout, cfg.LogLevel)
	slog.SetDefault(log)
	flush, err := startTelemetry(log, cfg.ServiceName, cfg.ServiceVersion, cfg.OTLPEndpoint)
	if err != nil {
		return err
	}
	defer flush()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	reads, closeReads, err := buildReads(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeReads()
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: newRouter(inboundmcp.Handler(inboundmcp.NewServer(inboundmcp.Deps{Reads: reads})), log, cfg.ServiceName),
		ReadHeaderTimeout: 5 * time.Second}
	log.Info("asrs-control mcp listening (Streamable HTTP)", "addr", ln.Addr().String())
	return serve(ctx, srv, ln)
}

// buildReads loads the layout, runs the idempotent migrations (so this binary may boot before
// cmd/api; golang-migrate's advisory lock makes concurrent starts safe) and wires the read use
// cases over the Postgres read repositories. READ-ONLY wiring: no unit of work, outbox, inbox,
// downlink or id generator is constructed, so nothing here can write.
func buildReads(ctx context.Context, cfg config) (*usecases.Service, func(), error) {
	layouts, err := layoutfile.LoadLayouts(cfg.LayoutFile)
	if err != nil {
		return nil, nil, err
	}
	if err := postgres.RunMigrations(cfg.MigrationsURL); err != nil {
		return nil, nil, fmt.Errorf("migrations: %w", err)
	}
	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("postgres: %w", err)
	}
	svc, err := usecases.New(usecases.Deps{Layouts: layouts, Maps: postgres.NewStorageMapRepo(pool), Jobs: postgres.NewStorageJobRepo(pool),
		Units: postgres.NewUnitRepo(pool), Clock: system.Clock{}})
	if err != nil {
		pool.Close()
		return nil, nil, err
	}
	return svc, pool.Close, nil
}

// serve runs srv on ln until ctx ends, then drains it.
func serve(ctx context.Context, srv *http.Server, ln net.Listener) error {
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	drain, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return srv.Shutdown(drain)
}
