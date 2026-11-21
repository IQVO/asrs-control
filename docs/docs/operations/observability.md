---
id: observability
title: Observability
sidebar_label: Observability
sidebar_position: 3
description: "The fleet OpenTelemetry standard in asrs-control: traces across REST, the MQTT telegram link and the outbox, the metrics, trace-correlated JSON logs and the probes."
---

# Observability

asrs-control follows the fleet OpenTelemetry standard
([ADR 0004](/docs/adr/0004-adopt-the-fleet-opentelemetry-standard)): traces and metrics over OTLP/gRPC to the Collector,
JSON logs on stdout carrying the trace id. With `OTEL_EXPORTER_OTLP_ENDPOINT` empty, telemetry is **off**: nothing is
installed and nothing is dialled; the chart renders the endpoint while `otel.enabled` (default on, the fleet Collector).
Export never blocks: an unreachable Collector costs telemetry, not availability.

## Traces

| Span | Kind | Where | Attributes |
| --- | --- | --- | --- |
| the chi route pattern, e.g. `/asrs/{asrsId}/storage-jobs` | server | every REST request (otelchi) | HTTP semconv |
| `asrs/v1/<asrsId>/down publish` | producer | every down telegram: command, ACK, retransmit | `asrs.id`, `asrs.telegram.id`, `asrs.telegram.type`, `asrs.telegram.retransmit`, `messaging.system=mqtt` |
| `asrs/v1/<asrsId>/up process` | consumer | every up-topic message | `asrs.id`, `asrs.telegram.id`, `asrs.telegram.type`; Error status when malformed or not applied |
| `warehouse.asrs-control.events publish`, `warehouse.asrs-control.analytics publish` | producer | every outbox row the relay publishes | `messaging.system=kafka`, `messaging.message.id` (the CloudEvents id), `cloudevents.event_type` |

How they join: a REST call's server span is the parent of the command telegram it sends (after commit); an up telegram's
process span is the parent of its ACK; every outbox row stores the W3C `traceparent` of the unit of work that wrote it, so
its publish span continues the request's or the telegram's trace, and the Kafka message carries the publish span's
`traceparent` to consumers. Telegram spans carry ids and types only, never a container, position or payload. MQTT v3.1.1
has no headers: the trace stops at the broker (the PLC is not traced).

## Metrics

| Series | Kind | Labels | Use |
| --- | --- | --- | --- |
| `http.server.request.duration` | histogram | route, method, status | REST RED |
| Go runtime (`go.*`, `process.runtime.*`) | gauges | none | goroutines, GC, heap |
| `asrs_control.storage_jobs.accepted` | counter | `job_kind` | external jobs (STORE, RETRIEVE) accepted |
| `asrs_control.storage_jobs.completed` | counter | `job_kind` | store JOB_DONE, retrieve PORT_ARRIVAL |
| `asrs_control.storage_jobs.failed` | counter | `job_kind`, `reason` | `EQUIPMENT_FAULT` opens an exception case; a rising `EQUIPMENT_UNREACHABLE` means the PLC link is dropping ACKs; `CANCELLED` counts cancellations |
| `asrs_control.storage_job.duration` | histogram (s) | `job_kind`, `outcome` | acceptance to end |
| `asrs_control.telegrams.retransmitted` | counter | `asrs_id`, `telegram_type` | resends after 2 s without an ACK: a rising rate precedes `EQUIPMENT_UNREACHABLE` |
| `asrs_control.outbox.backlog` | gauge | none | unpublished outbox rows (attempt-capped included) |
| `asrs_control.outbox.lag` | gauge (s) | none | age of the oldest unpublished row; a growing lag means Kafka is not taking the relay's writes |

The job counters count only what committed (a refusal, a rollback or an `Idempotency-Key` replay counts nothing) and never
an internal reshuffle. In Prometheus the dots become underscores and counters get `_total` (for example
`asrs_control_storage_jobs_failed_total`). Dashboards and alert rules live in warehouse-infra.

## Logs

JSON on stdout through `log/slog`, level from `LOG_LEVEL`. Records written under a span carry `trace_id` and `span_id`;
every REST request writes one `http request` line (method, path, status, duration, request id). Messages worth alerting on:

| Message | Level | Meaning |
| --- | --- | --- |
| `telegram never acknowledged` | WARN | a down telegram expired after 3 retransmits (its job fails `EQUIPMENT_UNREACHABLE`) |
| `up telegram not applied; not acknowledged, the PLC retransmits it` | ERROR | an up telegram's transaction failed |
| `ACK not applied; the command is resent so the PLC answers again` | ERROR | applying an ACK failed |
| `malformed up telegram` | WARN | a telegram failed to decode (ACKed MALFORMED when it had an id) |
| `the storage map disagrees with the PLC's JOB_DONE` | ERROR | the job fails `STORAGE_MAP_MISMATCH` |
| `presented over a container whose pickup was never confirmed` | WARN | a missing pickup confirmation |
| `mqtt publish failed; the retransmitter retries` | WARN | the broker was unreachable for a publish |
| `idempotency purge failed`, `telegram inbox purge failed` | WARN | the hourly purge failed |
| `telemetry disabled: OTEL_EXPORTER_OTLP_ENDPOINT is not set` | INFO | boot without a Collector |

## Other signals

| Signal | Where |
| --- | --- |
| Readiness and the failing dependency | `GET /readyz` |
| Capacity, unit states, open jobs | `GET /asrs/<asrsId>`, and the `AsrsCapacityChanged` / `EquipmentStateChanged` events |
| Job outcomes and reasons | `StorageJobFailed.reason` on `warehouse.asrs-control.events` (and `.analytics`) |
| Outbox rows themselves | `outbox_events` with `published_at IS NULL` ([Runbook](/docs/operations/runbook#events-are-not-arriving)) |
