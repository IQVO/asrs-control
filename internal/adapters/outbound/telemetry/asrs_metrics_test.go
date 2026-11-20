package telemetry_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/IQVO/asrs-control/internal/adapters/outbound/telemetry"
	"github.com/IQVO/asrs-control/internal/application/ports"
)

var _ ports.JobMetrics = (*telemetry.Metrics)(nil)

func newMetrics(t *testing.T) (*telemetry.Metrics, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	m, err := telemetry.NewMetrics(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	return m, reader
}

func collect(t *testing.T, r *sdkmetric.ManualReader) map[string]metricdata.Aggregation {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := r.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	out := map[string]metricdata.Aggregation{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			out[m.Name] = m.Data
		}
	}
	return out
}

// sumOf returns the int64 sum of the data point whose attributes are exactly want.
func sumOf(t *testing.T, data metricdata.Aggregation, want ...attribute.KeyValue) int64 {
	t.Helper()
	sum, ok := data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("aggregation %T is not an int64 sum", data)
	}
	set := attribute.NewSet(want...)
	for _, dp := range sum.DataPoints {
		if dp.Attributes.Equals(&set) {
			return dp.Value
		}
	}
	t.Fatalf("no data point with attributes %v in %+v", want, sum.DataPoints)
	return 0
}

func TestMetricsCountJobsByKindOutcomeAndReason(t *testing.T) {
	m, r := newMetrics(t)
	ctx := context.Background()
	m.JobAccepted(ctx, "STORE")
	m.JobAccepted(ctx, "STORE")
	m.JobAccepted(ctx, "RETRIEVE")
	m.JobEnded(ctx, "STORE", "completed", "", 30*time.Second)
	m.JobEnded(ctx, "RETRIEVE", "failed", "EQUIPMENT_FAULT", 2*time.Second)
	m.JobEnded(ctx, "STORE", "failed", "CANCELLED", time.Second)

	got := collect(t, r)
	kind := attribute.Key("job_kind")
	if n := sumOf(t, got["asrs_control.storage_jobs.accepted"], kind.String("STORE")); n != 2 {
		t.Errorf("accepted STORE = %d, want 2", n)
	}
	if n := sumOf(t, got["asrs_control.storage_jobs.accepted"], kind.String("RETRIEVE")); n != 1 {
		t.Errorf("accepted RETRIEVE = %d, want 1", n)
	}
	if n := sumOf(t, got["asrs_control.storage_jobs.completed"], kind.String("STORE")); n != 1 {
		t.Errorf("completed STORE = %d, want 1", n)
	}
	reason := attribute.Key("reason")
	if n := sumOf(t, got["asrs_control.storage_jobs.failed"], kind.String("RETRIEVE"), reason.String("EQUIPMENT_FAULT")); n != 1 {
		t.Errorf("failed RETRIEVE EQUIPMENT_FAULT = %d, want 1", n)
	}
	if n := sumOf(t, got["asrs_control.storage_jobs.failed"], kind.String("STORE"), reason.String("CANCELLED")); n != 1 {
		t.Errorf("failed STORE CANCELLED = %d, want 1", n)
	}
	hist, ok := got["asrs_control.storage_job.duration"].(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("duration is %T, want a float64 histogram", got["asrs_control.storage_job.duration"])
	}
	var count uint64
	var total float64
	for _, dp := range hist.DataPoints {
		count += dp.Count
		total += dp.Sum
	}
	if count != 3 || total != 33 {
		t.Errorf("duration count/sum = %d/%v, want 3/33 seconds", count, total)
	}
}

func TestMetricsCountTelegramRetransmitsByType(t *testing.T) {
	m, r := newMetrics(t)
	m.TelegramRetransmitted(context.Background(), "ASRS1", "STORE")
	m.TelegramRetransmitted(context.Background(), "ASRS1", "STORE")
	got := collect(t, r)
	n := sumOf(t, got["asrs_control.telegrams.retransmitted"], attribute.String("asrs_id", "ASRS1"), attribute.String("telegram_type", "STORE"))
	if n != 2 {
		t.Errorf("retransmits = %d, want 2", n)
	}
}

func TestMetricsObserveTheOutboxBacklogAndLag(t *testing.T) {
	m, r := newMetrics(t)
	reg, err := m.ObserveOutbox(func(context.Context) (int64, time.Duration, error) { return 7, 90 * time.Second, nil })
	if err != nil {
		t.Fatalf("ObserveOutbox: %v", err)
	}
	defer func() { _ = reg.Unregister() }()
	got := collect(t, r)
	backlog, ok := got["asrs_control.outbox.backlog"].(metricdata.Gauge[int64])
	if !ok || len(backlog.DataPoints) != 1 || backlog.DataPoints[0].Value != 7 {
		t.Errorf("backlog = %+v, want one point of 7", got["asrs_control.outbox.backlog"])
	}
	lag, ok := got["asrs_control.outbox.lag"].(metricdata.Gauge[float64])
	if !ok || len(lag.DataPoints) != 1 || lag.DataPoints[0].Value != 90 {
		t.Errorf("lag = %+v, want one point of 90 seconds", got["asrs_control.outbox.lag"])
	}
}

func TestMetricsObserveNothingWhenTheOutboxReadFails(t *testing.T) {
	m, r := newMetrics(t)
	reg, err := m.ObserveOutbox(func(context.Context) (int64, time.Duration, error) { return 0, 0, errors.New("db down") })
	if err != nil {
		t.Fatalf("ObserveOutbox: %v", err)
	}
	defer func() { _ = reg.Unregister() }()
	got := collect(t, r)
	if g, ok := got["asrs_control.outbox.backlog"].(metricdata.Gauge[int64]); ok && len(g.DataPoints) > 0 {
		t.Errorf("a failed read must observe no backlog point, got %+v", g.DataPoints)
	}
}

func TestNilMetricsAreANoOp(t *testing.T) {
	var m *telemetry.Metrics
	m.JobAccepted(context.Background(), "STORE")
	m.JobEnded(context.Background(), "STORE", "completed", "", time.Second)
	m.TelegramRetransmitted(context.Background(), "ASRS1", "STORE")
}
