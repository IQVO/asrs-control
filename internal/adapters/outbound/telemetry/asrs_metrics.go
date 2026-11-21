package telemetry

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/IQVO/asrs-control/internal/application/ports"
)

// meterName scopes asrs-control's own instruments, distinct from the ones otelchi and the
// runtime collector register.
const meterName = "github.com/IQVO/asrs-control"

// Instrument names (ADR 0004). A name is a public contract with every dashboard built on it:
// renaming one or its attributes needs a new ADR.
const (
	jobsAcceptedName  = "asrs_control.storage_jobs.accepted"
	jobsCompletedName = "asrs_control.storage_jobs.completed"
	jobsFailedName    = "asrs_control.storage_jobs.failed"
	jobDurationName   = "asrs_control.storage_job.duration"
	retransmitsName   = "asrs_control.telegrams.retransmitted"
	outboxBacklogName = "asrs_control.outbox.backlog"
	outboxLagName     = "asrs_control.outbox.lag"
)

// outcomeFailed is the ports.JobMetrics outcome of a failed or cancelled job.
const outcomeFailed = "failed"

// outboxReadTimeout bounds one backlog query of a metric collection.
const outboxReadTimeout = 2 * time.Second

const (
	kindKey    = attribute.Key("job_kind")
	outcomeKey = attribute.Key("outcome")
	reasonKey  = attribute.Key("reason")
	asrsKey    = attribute.Key("asrs_id")
	typeKey    = attribute.Key("telegram_type")
)

// OutboxBacklogReader reads the unpublished outbox rows and the age of the oldest one
// (postgres.OutboxRepo.Backlog).
type OutboxBacklogReader func(ctx context.Context) (rows int64, oldest time.Duration, err error)

// Metrics is asrs-control's Tier 2 instrument set: ports.JobMetrics for the storage jobs, the
// telegram retransmit counter the MQTT link calls, and the outbox backlog gauges. A nil
// *Metrics records nothing.
type Metrics struct {
	meter     metric.Meter
	accepted  metric.Int64Counter
	completed metric.Int64Counter
	failed    metric.Int64Counter
	duration  metric.Float64Histogram
	retx      metric.Int64Counter
}

var _ ports.JobMetrics = (*Metrics)(nil)

// NewMetrics registers the instruments on provider (otel.GetMeterProvider() in cmd/api, after
// Setup). It fails only on an invalid instrument definition, a programming error.
func NewMetrics(provider metric.MeterProvider) (*Metrics, error) {
	m := &Metrics{meter: provider.Meter(meterName)}
	var errs [5]error
	m.accepted, errs[0] = m.meter.Int64Counter(jobsAcceptedName, metric.WithUnit("{job}"),
		metric.WithDescription("External storage jobs (STORE, RETRIEVE) accepted, by job_kind."))
	m.completed, errs[1] = m.meter.Int64Counter(jobsCompletedName, metric.WithUnit("{job}"),
		metric.WithDescription("External storage jobs completed (a store's JOB_DONE, a retrieve's PORT_ARRIVAL), by job_kind."))
	m.failed, errs[2] = m.meter.Int64Counter(jobsFailedName, metric.WithUnit("{job}"),
		metric.WithDescription("External storage jobs failed or cancelled, by job_kind and reason (the StorageJobFailed reason)."))
	m.duration, errs[3] = m.meter.Float64Histogram(jobDurationName, metric.WithUnit("s"),
		metric.WithDescription("Acceptance to end of an external storage job, by job_kind and outcome (completed, failed)."),
		metric.WithExplicitBucketBoundaries(1, 5, 10, 30, 60, 120, 300, 600, 1800, 3600))
	m.retx, errs[4] = m.meter.Int64Counter(retransmitsName, metric.WithUnit("{telegram}"),
		metric.WithDescription("Down telegrams resent because the PLC did not acknowledge them within 2 s, by asrs_id and telegram_type."))
	if err := errors.Join(errs[:]...); err != nil {
		return nil, err
	}
	return m, nil
}

// JobAccepted implements ports.JobMetrics.
func (m *Metrics) JobAccepted(ctx context.Context, kind string) {
	if m == nil {
		return
	}
	m.accepted.Add(ctx, 1, metric.WithAttributes(kindKey.String(kind)))
}

// JobEnded implements ports.JobMetrics.
func (m *Metrics) JobEnded(ctx context.Context, kind, outcome, reason string, took time.Duration) {
	if m == nil {
		return
	}
	if outcome == outcomeFailed {
		m.failed.Add(ctx, 1, metric.WithAttributes(kindKey.String(kind), reasonKey.String(reason)))
	} else {
		m.completed.Add(ctx, 1, metric.WithAttributes(kindKey.String(kind)))
	}
	m.duration.Record(ctx, took.Seconds(), metric.WithAttributes(kindKey.String(kind), outcomeKey.String(outcome)))
}

// TelegramRetransmitted counts one resend of a down telegram (the MQTT link's retransmitter).
func (m *Metrics) TelegramRetransmitted(ctx context.Context, asrsID, telegramType string) {
	if m == nil {
		return
	}
	m.retx.Add(ctx, 1, metric.WithAttributes(asrsKey.String(asrsID), typeKey.String(telegramType)))
}

// ObserveOutbox registers the outbox gauges: asrs_control.outbox.backlog (unpublished rows,
// attempt-capped ones included) and asrs_control.outbox.lag (seconds since the oldest
// unpublished row was written; 0 when the outbox is drained). read runs once per collection; a
// failed read observes nothing, so a database outage shows as a gap, never as a zero backlog.
func (m *Metrics) ObserveOutbox(read OutboxBacklogReader) (metric.Registration, error) {
	backlog, err := m.meter.Int64ObservableGauge(outboxBacklogName, metric.WithUnit("{row}"),
		metric.WithDescription("Unpublished transactional-outbox rows (rows the relay excluded at the attempt cap included)."))
	if err != nil {
		return nil, err
	}
	lag, err := m.meter.Float64ObservableGauge(outboxLagName, metric.WithUnit("s"),
		metric.WithDescription("Age of the oldest unpublished transactional-outbox row; 0 when the outbox is drained."))
	if err != nil {
		return nil, err
	}
	return m.meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		rctx, cancel := context.WithTimeout(ctx, outboxReadTimeout)
		defer cancel()
		rows, oldest, err := read(rctx)
		if err != nil {
			return nil //nolint:nilerr // a failed read is a gap in the series, not an SDK error
		}
		o.ObserveInt64(backlog, rows)
		o.ObserveFloat64(lag, oldest.Seconds())
		return nil
	}, backlog, lag)
}
