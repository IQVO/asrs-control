# 4. Adopt the fleet OpenTelemetry standard

Status: Accepted (2026-11-20). Supersedes the "Deferred" sentence of [ADR 0001](0001-asrs-control-bounded-context.md)
about the fleet OpenTelemetry standard; ADR 0001 is otherwise unchanged (ADR bodies are immutable, so ADR 0001 only
carries a one-line pointer to this record under its Status).

## Context

ADR 0001 shipped asrs-control with JSON `log/slog` only and deferred the fleet OpenTelemetry standard (outbound-shipping
ADR 0008; the shape also used by customer-returns): no tracer or meter provider, no exporter, no `OTEL_*` variable, a plain
chi router with no access log, and outbox rows without a trace context. The fleet pipeline (pods, OTLP/gRPC to the
Collector, then Jaeger and the Prometheus exporter) saw nothing of the context. asrs-control has three inputs a trace must
cross: the REST subsystem-controller interface, the MQTT telegram link to the PLC, and the transactional outbox to Kafka.

## Decision

**Tier 1, identical to the fleet.**

- `internal/adapters/outbound/telemetry` owns `Setup` (OTLP/gRPC trace and metric exporters, the W3C trace-context and
  baggage propagator, Go runtime metrics) and `WithTraceContext` (the slog handler that stamps `trace_id`/`span_id` on records
  written under a span). `cmd/api/telemetry.go` starts it before `wire` builds the router and flushes it on shutdown (5 s).
- **An empty OTLP endpoint means telemetry is off**: `Setup` installs nothing and never dials (outbound-shipping ADR 0008).
  With an endpoint, export is non-blocking: a Collector that is down costs telemetry, never availability.
- `service.name` is `OTEL_SERVICE_NAME` (default `asrs-control`), `service.version` is `SERVICE_VERSION` and
  `deployment.environment.name` is `ENVIRONMENT`.
- REST middleware order: `RequestID`, `otelchi.Middleware` (a server span named after the chi route pattern),
  `otelchimetric` (`http.server.request.duration`), `requestlog.RequestLogger` (the access log, inside the span), then the
  existing problem+json panic recoverer. The access log lives in `internal/adapters/inbound/requestlog`, so a later MCP or
  A2A binary can share it without importing the REST adapter.

**Spans of the context.**

- **MQTT telegrams**: one span per telegram. A down telegram (command, ACK, retransmit) is a producer span
  `asrs/v1/<asrsId>/down publish`; an up-topic message is a consumer span `asrs/v1/<asrsId>/up process`. Attributes are
  `asrs.id`, `asrs.telegram.id`, `asrs.telegram.type`, `asrs.telegram.retransmit` and the messaging semconv
  (`messaging.system=mqtt`); never a container, position or any other payload field. A command's span is a child of the
  request that committed it (the use case hands its context to the downlink after commit); an ACK's span is a child of the
  telegram it answers; a malformed or unapplied telegram marks its span as an error. MQTT v3.1.1 has no headers, so the
  trace stops at the broker: the PLC (the emulator) is not traced.
- **Outbox**: `events.Sink` injects the W3C trace context of the unit of work into each row's `headers`
  (none when telemetry is off, so the goldens stay byte-identical; no migration, the column existed). The relay's
  `RelaySink` opens a producer span `<topic> publish` per row, parented on the stored context, and re-injects the publish
  span into the Kafka headers, so a consumer continues from the publish. asrs-control has no Kafka consumer, so there is
  no `<topic> process` span in this repository.

**Tier 2 instruments** (`internal/adapters/outbound/telemetry/asrs_metrics.go`, meter `github.com/IQVO/asrs-control`):

| Instrument | Kind | Attributes | Meaning |
| --- | --- | --- | --- |
| `asrs_control.storage_jobs.accepted` | counter `{job}` | `job_kind` | external jobs (STORE, RETRIEVE) accepted |
| `asrs_control.storage_jobs.completed` | counter `{job}` | `job_kind` | a store's JOB_DONE, a retrieve's PORT_ARRIVAL |
| `asrs_control.storage_jobs.failed` | counter `{job}` | `job_kind`, `reason` | failed or cancelled; `reason` is the `StorageJobFailed` reason (bounded catalogue, `CANCELLED` included) |
| `asrs_control.storage_job.duration` | histogram `s` | `job_kind`, `outcome` | acceptance to end on asrs-control's clock |
| `asrs_control.telegrams.retransmitted` | counter `{telegram}` | `asrs_id`, `telegram_type` | down telegrams resent after 2 s without an ACK |
| `asrs_control.outbox.backlog` | gauge `{row}` | none | unpublished outbox rows, attempt-capped ones included |
| `asrs_control.outbox.lag` | gauge `s` | none | age of the oldest unpublished row (0 when drained) |

The job counters go through a new port, `ports.JobMetrics`, that the use cases call from an `AfterCommit` hook: only the
outermost unit of work that committed counts, so a refusal, a rollback or an `Idempotency-Key` replay counts nothing.
Ended jobs are read off the domain events of the unit of work (`ContainerStored`, `ContainerPresentedAtPort`,
`StorageJobFailed`), which now carry the job's `AcceptedAt` for the duration (not encoded on the wire; the CloudEvents
payloads are unchanged). Internal reshuffles publish none of these events and are never counted. The retransmit counter is
an option of the MQTT link (`mqtt.WithRetransmitCounter`); the outbox gauges read `OutboxRepo.Backlog` once per collection
and observe nothing when the read fails (a gap, never a false zero).

**Chart.** `otel.{enabled,endpoint,serviceName}`, `environment` and `serviceVersion` (empty falls back to `image.tag`, then
`appVersion`). `ENVIRONMENT` and `SERVICE_VERSION` are always rendered; `OTEL_EXPORTER_OTLP_ENDPOINT` and
`OTEL_SERVICE_NAME` only while `otel.enabled` and the endpoint is set. Each is an explicit Deployment env entry and a key of
the ConfigMap; `extraEnv` refuses the four names. `render_test.sh` scans `cmd/api` and the telemetry adapter for every env
name the binary reads and fails when the chart does not render one.

## Consequences

- Every REST request produces a server span and `http.server.request.duration` labelled with the route pattern, and an
  access-log line with `trace_id`/`span_id`; every telegram produces one span; every outbox row a publish span whose trace
  starts at the request or telegram that wrote it.
- Dashboards and alert rules belong to warehouse-infra and are not part of this change; the series names above are the
  contract they build on. Renaming an instrument or an attribute needs a new ADR.
- New direct dependencies: the OpenTelemetry SDK, the OTLP/gRPC exporters, `otelchi` and the runtime instrumentation, at the
  versions customer-returns pins.
