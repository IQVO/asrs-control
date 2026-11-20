package main

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/IQVO/asrs-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/asrs-control/internal/application/usecases"
)

type config struct {
	DatabaseURL, MigrationsURL                        string
	Brokers                                           []string
	MQTTURL, MQTTClientID, LayoutFile, Addr, LogLevel string
	OutboxInterval                                    time.Duration
	OutboxBatch, OutboxMaxAttempts                    int
	Velocity                                          usecases.VelocityPolicy
	// Telemetry (ADR 0004): an empty OTLPEndpoint means telemetry is off.
	ServiceName, ServiceVersion, OTLPEndpoint string
}

func loadConfig(getenv func(string) string) (config, error) {
	or := func(k, def string) string {
		if v := getenv(k); v != "" {
			return v
		}
		return def
	}
	atLeast := func(k string, def, min int) int {
		if n, err := strconv.Atoi(getenv(k)); err == nil && n >= min {
			return n
		}
		return def
	}
	c := config{DatabaseURL: getenv("DATABASE_URL"), MigrationsURL: postgres.MigrationsURL(getenv), MQTTURL: getenv("MQTT_URL"),
		LayoutFile: getenv("ASRS_LAYOUT_FILE"), MQTTClientID: or("MQTT_CLIENT_ID", "asrs-control"), Addr: or("ADDR", ":8080"),
		LogLevel: or("LOG_LEVEL", "info"), OutboxInterval: time.Duration(atLeast("OUTBOX_INTERVAL_MS", 250, 1)) * time.Millisecond,
		OutboxBatch: atLeast("OUTBOX_BATCH", 100, 1), OutboxMaxAttempts: atLeast("OUTBOX_MAX_ATTEMPTS", 20, 0),
		Velocity: usecases.VelocityPolicy{Window: time.Duration(atLeast("VELOCITY_WINDOW_HOURS", 168, 1)) * time.Hour,
			FastRetrievals: atLeast("VELOCITY_FAST_RETRIEVALS", 3, 1)},
		ServiceName: or("OTEL_SERVICE_NAME", "asrs-control"), ServiceVersion: getenv("SERVICE_VERSION"),
		OTLPEndpoint: getenv("OTEL_EXPORTER_OTLP_ENDPOINT")}
	for _, b := range strings.Split(getenv("KAFKA_BROKERS"), ",") {
		if b = strings.TrimSpace(b); b != "" {
			c.Brokers = append(c.Brokers, b)
		}
	}
	var problems []error
	for _, req := range []struct{ name, value string }{{"DATABASE_URL", c.DatabaseURL}, {"KAFKA_BROKERS", strings.Join(c.Brokers, ",")},
		{"MQTT_URL", c.MQTTURL}, {"ASRS_LAYOUT_FILE", c.LayoutFile}} {
		if req.value == "" {
			problems = append(problems, errors.New(req.name+" is required: asrs-control runs on real Postgres, Kafka and MQTT with a layout file only"))
		}
	}
	return c, errors.Join(problems...)
}

// kafkaCheck is the /readyz dependency for the brokers: a TCP dial of the first one.
func kafkaCheck(brokers []string) func(context.Context) error {
	return func(ctx context.Context) error {
		d := net.Dialer{Timeout: 2 * time.Second}
		conn, err := d.DialContext(ctx, "tcp", brokers[0])
		if err != nil {
			return err
		}
		return conn.Close()
	}
}
