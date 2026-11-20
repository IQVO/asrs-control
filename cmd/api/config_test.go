package main

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func full() map[string]string {
	return map[string]string{"DATABASE_URL": "postgres://db", "KAFKA_BROKERS": " k1:9092 , k2:9092 ,", "MQTT_URL": "tcp://mq:1883", "ASRS_LAYOUT_FILE": "/etc/asrs-control/layout.yaml"}
}

func TestEveryMandatoryVariableIsNamedWhenMissing(t *testing.T) {
	_, err := loadConfig(env(map[string]string{}))
	if err == nil {
		t.Fatal("an empty environment booted")
	}
	for _, v := range []string{"DATABASE_URL", "KAFKA_BROKERS", "MQTT_URL", "ASRS_LAYOUT_FILE"} {
		if !strings.Contains(err.Error(), v) {
			t.Errorf("the boot error does not name %s: %v", v, err)
		}
	}
}

func TestDefaults(t *testing.T) {
	c, err := loadConfig(env(full()))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Brokers) != 2 || c.Brokers[1] != "k2:9092" || c.Addr != ":8080" || c.LogLevel != "info" || c.MQTTClientID != "asrs-control" ||
		c.MigrationsURL != "postgres://db" || c.OutboxInterval != 250*time.Millisecond || c.OutboxBatch != 100 || c.OutboxMaxAttempts != 20 ||
		c.Velocity.Window != 168*time.Hour || c.Velocity.FastRetrievals != 3 {
		t.Fatalf("defaults = %+v", c)
	}
}

func TestOverridesAndBadNumbers(t *testing.T) {
	m := full()
	m["MIGRATIONS_DATABASE_URL"], m["ADDR"], m["MQTT_CLIENT_ID"] = "postgres://direct", ":9090", "acx"
	m["OUTBOX_MAX_ATTEMPTS"], m["OUTBOX_BATCH"], m["VELOCITY_WINDOW_HOURS"], m["VELOCITY_FAST_RETRIEVALS"] = "0", "abc", "24", "-1"
	c, err := loadConfig(env(m))
	if err != nil {
		t.Fatal(err)
	}
	if c.MigrationsURL != "postgres://direct" || c.Addr != ":9090" || c.MQTTClientID != "acx" || c.OutboxMaxAttempts != 0 ||
		c.OutboxBatch != 100 || c.Velocity.Window != 24*time.Hour || c.Velocity.FastRetrievals != 3 {
		t.Fatalf("overrides = %+v", c)
	}
}

func TestLoggerLevels(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		level   string
		lowest  slog.Level // the lowest level that is enabled
		blocked slog.Level // the level just below it, which must be off
	}{
		{"debug", slog.LevelDebug, slog.LevelDebug - 4},
		{"info", slog.LevelInfo, slog.LevelDebug},
		{"warn", slog.LevelWarn, slog.LevelInfo},
		{"warning", slog.LevelWarn, slog.LevelInfo},
		{"error", slog.LevelError, slog.LevelWarn},
		{"bogus", slog.LevelInfo, slog.LevelDebug},
	} {
		l := newLogger(c.level)
		if !l.Enabled(ctx, c.lowest) || l.Enabled(ctx, c.blocked) {
			t.Errorf("LOG_LEVEL=%s: enabled(%v)=%v enabled(%v)=%v, want true/false", c.level, c.lowest, l.Enabled(ctx, c.lowest), c.blocked, l.Enabled(ctx, c.blocked))
		}
	}
}
