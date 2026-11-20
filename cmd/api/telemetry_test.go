package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

// The telemetry environment (ADR 0004): OTEL_* names from the OTel spec plus the fleet's
// SERVICE_VERSION. An unset endpoint means telemetry is off.
func TestTelemetryConfigDefaultsLeaveTelemetryOff(t *testing.T) {
	c, err := loadConfig(env(full()))
	if err != nil {
		t.Fatal(err)
	}
	if c.ServiceName != "asrs-control" || c.ServiceVersion != "" || c.OTLPEndpoint != "" {
		t.Fatalf("telemetry defaults = %q / %q / %q, want asrs-control and both empty", c.ServiceName, c.ServiceVersion, c.OTLPEndpoint)
	}
}

func TestTelemetryConfigFromTheEnvironment(t *testing.T) {
	m := full()
	m["OTEL_SERVICE_NAME"], m["SERVICE_VERSION"], m["OTEL_EXPORTER_OTLP_ENDPOINT"] = "asrs-control-staging", "v1.2.3", "otel-collector:4317"
	c, err := loadConfig(env(m))
	if err != nil {
		t.Fatal(err)
	}
	if c.ServiceName != "asrs-control-staging" || c.ServiceVersion != "v1.2.3" || c.OTLPEndpoint != "otel-collector:4317" {
		t.Fatalf("telemetry config = %q / %q / %q", c.ServiceName, c.ServiceVersion, c.OTLPEndpoint)
	}
}

// Every record written under a span carries trace_id and span_id: the composition root's logger
// is the trace-correlating one.
func TestTheLoggerStampsTraceAndSpanIDs(t *testing.T) {
	var buf bytes.Buffer
	log := newLoggerTo(&buf, "info")
	tid, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	sid, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled}))
	log.InfoContext(ctx, "job accepted")
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatal(err)
	}
	if line["trace_id"] != "4bf92f3577b34da6a3ce929d0e0e4736" || line["span_id"] != "00f067aa0ba902b7" {
		t.Fatalf("log line = %v", line)
	}
}

func TestStartTelemetryWithoutAnEndpointIsANoOp(t *testing.T) {
	var buf bytes.Buffer
	flush, err := startTelemetry(newLoggerTo(&buf, "info"), config{ServiceName: "asrs-control"})
	if err != nil {
		t.Fatal(err)
	}
	flush()
	if !bytes.Contains(buf.Bytes(), []byte("telemetry disabled")) {
		t.Fatalf("boot log = %s, want the disabled notice", buf.String())
	}
}

func TestJobMetricsRegisterOnTheGlobalProvider(t *testing.T) {
	if m := newMetrics(newLoggerTo(&bytes.Buffer{}, "info")); m == nil {
		t.Fatal("the instruments did not register")
	}
}
