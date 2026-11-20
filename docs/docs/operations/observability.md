---
id: observability
title: Observability
sidebar_label: Observability
sidebar_position: 3
description: "What asrs-control emits today (JSON logs, probes, the outbox and the events) and what the fleet OpenTelemetry standard will add."
---

# Observability

:::caution OpenTelemetry is not wired yet
[ADR 0001](/docs/adr/0001-asrs-control-bounded-context) defers the fleet OpenTelemetry standard (traces, metrics, the
`traceparent` Kafka header) to a follow-up. Today asrs-control exports **no traces and no metrics**.
:::

## Logs

JSON on stdout through `log/slog` (`cmd/api/main.go` `newLogger`), level from `LOG_LEVEL`. Messages worth alerting on:

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

## Signals available without OpenTelemetry

| Signal | Where |
| --- | --- |
| Readiness and the failing dependency | `GET /readyz` |
| Capacity, unit states, open jobs | `GET /asrs/<asrsId>`, and the `AsrsCapacityChanged` / `EquipmentStateChanged` events |
| Job outcomes and reasons | `StorageJobFailed.reason` on `warehouse.asrs-control.events` (and `.analytics`) |
| Outbox backlog | `outbox_events` rows with `published_at IS NULL` ([Runbook](/docs/operations/runbook#events-are-not-arriving)) |

## Planned

The fleet standard (as in outbound-shipping ADR 0008 and the cohort repos): OTLP traces across HTTP, the use cases, the
outbox relay and Kafka (`traceparent` in headers), RED metrics for HTTP, relay and link metrics, and the trace id in
every log line.
