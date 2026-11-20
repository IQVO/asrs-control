// Command api is asrs-control: the REST subsystem-controller interface, one MQTT link per
// AS/RS (telegram protocol v1), the transactional-outbox relay and the probes. It runs on real
// Postgres, Kafka and MQTT only; a missing variable is a boot error naming it (CLAUDE.md lists
// the environment).
package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	if err := runMain(); err != nil {
		slog.Error("asrs-control exited with error", "error", err)
		os.Exit(1)
	}
}

func runMain() error {
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		return err
	}
	log := newLogger(cfg.LogLevel)
	slog.SetDefault(log)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	a, err := wire(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer a.close()
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return err
	}
	return a.run(ctx, ln)
}

func newLogger(level string) *slog.Logger {
	lv := slog.LevelInfo
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "warn", "warning":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lv}))
}
