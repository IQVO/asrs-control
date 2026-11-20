# SPEC.md — asrs-control

The agent entry point is `CLAUDE.md` (`AGENTS.md` is a symlink to it). This file is the longer description of what
asrs-control is and does, as built on `develop`. The wire contracts are `apis/openapi.yaml` (REST) and
`apis/asyncapi.yaml` (Kafka events and the MQTT telegram link); the decisions are `docs/adr/0001`-`0003`. When this file
and a contract or an ADR disagree, the contract or ADR wins and this file is fixed. The documentation site
(`docs/`, published to https://iqvo.github.io/asrs-control/ from `main`) has the long form of every section.

## Role

asrs-control is the AS/RS controller bounded context of the warehouse-systems WCS tier (warehouse-docs
`docs/wcs/asrs-control.md` on `main`, published at https://iqvo.github.io/warehouse-docs/wcs/asrs-control; ADR 0001). Classification **Generic**: AS/RS controllers ship with the
hardware, so this one is built behind the **subsystem-controller interface** that a vendor controller could implement
instead. CloudEvents segment `wes`. It is step 2 of the WCS build order, after the equipment emulator.

It **owns** where each container sits inside the rack (rack positions per aisle), storage position assignment by velocity
zone with double-deep depth rules, the queue and sequencing of store and retrieve jobs per aisle, port presentation order,
reshuffles, the PackML state of each aisle's shuttle-and-lift unit, and capacity facts.

It **does not own** container contents or the stock ledger (inventory-storage), which container moves and when
(fulfillment-execution and wes-work-planning, through material-flow-control), transport beyond its ports
(material-flow-control, amr-fleet-control), or the AS/RS as a location in the building (facility-layout). To the WMS the
whole AS/RS is one logical location.

## Guarantees (invariants 1-6 of the context page)

1. One container per rack position, and every stored container is in exactly one position.
2. A reserved position is never assigned.
3. An aisle's shuttle-and-lift unit runs one job at a time.
4. Retrieved containers reach each output port in the requested sequence.
5. Every accepted job ends in exactly one of DONE, FAILED or CANCELLED, with a reason.
6. Internal moves (reshuffles) are invisible outside: no rack position, reshuffle or internal move is published.

## Process

One binary, `cmd/api`: the REST interface, one MQTT link per AS/RS, the transactional-outbox relay, an hourly purger and
the probes. It runs on real Postgres, Kafka and MQTT only (no in-memory runtime; `runtime_no_memory_fitness_test.go`).

| Variable | Default | Meaning |
| --- | --- | --- |
| `DATABASE_URL` | required | Postgres DSN; migrations run at boot |
| `MIGRATIONS_DATABASE_URL` | `DATABASE_URL` | direct connection for golang-migrate |
| `KAFKA_BROKERS` | required | comma-separated brokers |
| `MQTT_URL` | required | broker of the PLC link |
| `ASRS_LAYOUT_FILE` | required | the layout YAML (ADR 0002) |
| `MQTT_CLIENT_ID` | `asrs-control` | client id prefix (`<prefix>-<asrsId>`) |
| `ADDR` | `:8080` | listen address |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `OUTBOX_INTERVAL_MS` / `OUTBOX_BATCH` / `OUTBOX_MAX_ATTEMPTS` | `250` / `100` / `20` | relay tuning |
| `VELOCITY_WINDOW_HOURS` / `VELOCITY_FAST_RETRIEVALS` | `168` / `3` | FAST/SLOW classification |

Boot: load and validate the layout, migrate, create every aisle's map and unit, connect and start the links, recover each
AS/RS (resend unacknowledged commands and pending CANCELs with their telegram ids, STATE_REQUEST, schedule every aisle),
start the relay, serve HTTP. Boot refuses a layout that no longer contains a stored container's position. SIGTERM flips
`/readyz` to not ready, then drains HTTP for at most 10 s.

**Layout file.** Per AS/RS: `id`, `aisles`, `levels`, `slots`, `doubleDeep`, `ports` (`id`, `aisle`, `kind`
INPUT/OUTPUT), `cost` (`perLevel`, `perSlot`). The field names are the emulator world's; emulator-only fields are accepted
and ignored (`config/layout.example.yaml`).

## Domain model

| Aggregate | Package | Protects |
| --- | --- | --- |
| `StorageMap` (one per aisle) | `internal/domain/storagemap` | invariants 1, 2, 6; depth rules D1 (a back position only while its front is clear) and D2 (no front over a back that a store is heading to, or whose container is leaving or moving); which container waits on each output port |
| `StorageJob` | `internal/domain/storagejob` | invariants 4 (with the scheduler) and 5; kinds STORE, RETRIEVE, RESHUFFLE; statuses QUEUED, DISPATCHED, ACKNOWLEDGED, CANCEL_REQUESTED, AWAITING_ARRIVAL, DONE, FAILED, CANCELLED |
| `EquipmentUnit` | `internal/domain/equipment` | invariant 3; PackML state IDLE, EXECUTE, HELD (protocol v1 sends no other) |

`scheduling.Next` (pure) picks an aisle's next action: a queued reshuffle first, else the oldest queued job whose output
port is free (a retrieve whose container is blocked gets a reshuffle; one whose container is missing fails
STORAGE_MAP_MISMATCH). `capacity.Snapshot` is units total/available, positions total/free and open jobs per AS/RS.

**Position assignment.** A store's position is reserved at acceptance. FAST containers (at least
`VELOCITY_FAST_RETRIEVALS` DONE retrieves in `VELOCITY_WINDOW_HOURS`, from this context's own history) prefer the front
and the cheapest travel cost; SLOW ones the back (double-deep) and the dearest; ties break on the lower level and slot.

**Consistency.** Every write runs in one Postgres unit of work around one aisle: it locks the aisle's map row and then its
unit row, so the map, the unit and the aisle's jobs change serially (ADR 0002 decision 3). A unique index allows one open
STORE/RETRIEVE per (AS/RS, container). Events are written to the outbox in the same transaction; PLC commands are sent
only after the outermost unit of work committed.

## Interface 1: REST (`apis/openapi.yaml`)

Caller: material-flow-control (WCS context map relationship 5, Customer/Supplier with asrs-control as the supplier).

| Route | Purpose |
| --- | --- |
| `GET /asrs/{asrsId}` | aisles (unit state, busy, open jobs, positions), ports (presented container), recommended input port, capacity |
| `POST /asrs/{asrsId}/storage-jobs` | request a STORE (input port required) or a RETRIEVE (output port optional): `202` or `422` at once |
| `GET /asrs/{asrsId}/storage-jobs/{storageJobId}` | read an external job |
| `POST /asrs/{asrsId}/storage-jobs/{storageJobId}/cancellation` | `200` cancelled at once (QUEUED), `202` a CANCEL went to the PLC |
| `POST /asrs/{asrsId}/ports/{portId}/pickups` | confirm a container left an output port (no telegram reports it) |
| `GET /healthz`, `GET /readyz` | liveness; readiness checks Postgres, Kafka and every MQTT link |

Every POST requires `Idempotency-Key`; 2xx and FINAL refusals replay for 24 hours with `Idempotency-Replayed: true`,
RETRYABLE refusals release the key. Errors are RFC 9457 `application/problem+json` with a `code`; the replay class of
each problem type is in `internal/adapters/inbound/http/errors.go` and the spec. No authentication (fleet rule). No rack
position appears in any body.

## Interface 2: events (`apis/asyncapi.yaml`, Kafka; ADR 0003)

CloudEvents 1.0 structured mode, published only through the transactional outbox, every event on both
`warehouse.asrs-control.events` and `warehouse.asrs-control.analytics` (same `id`, `type`, `data`; `dataschema` segment
differs). `type` = `com.warehouse.wes.asrs-control.<entity>.<EventName>`, `source` `/warehouse/asrs-control`, key =
`subject`.

| `type` | Raised when |
| --- | --- |
| `...storagejob.ContainerStored` | a STORE's JOB_DONE |
| `...storagejob.ContainerPresentedAtPort` | a RETRIEVE's PORT_ARRIVAL (scan READ, NO_READ, MISMATCH) |
| `...storagejob.StorageJobFailed` | an external job fails or is cancelled (reasons CANCELLED, EQUIPMENT_FAULT, EQUIPMENT_UNREACHABLE, COMMAND_REFUSED, CONTAINER_NOT_AT_PORT, STORAGE_MAP_MISMATCH, PORT_OCCUPIED, RESHUFFLE_FAILED, NO_RESHUFFLE_POSITION) |
| `...equipmentunit.EquipmentStateChanged` | a UNIT_STATE changes a unit's state |
| `...asrs.AsrsCapacityChanged` | the capacity snapshot changes |

Consumers: material-flow-control (all five, designed), exception-management (`StorageJobFailed` with `EQUIPMENT_FAULT`).
asrs-control consumes no Kafka topic and calls no sibling service.

## Interface 3: the PLC link (`apis/asyncapi.yaml`, MQTT; ADR 0002)

asrs-control is Conformist to AS/RS telegram protocol v1 (IQVO/equipment-emulator `docs/telegram-protocol.md`): flat
JSON, QoS 1, not retained, on `asrs/v1/<asrsId>/down` (STORE, RETRIEVE, MOVE, CANCEL, STATE_REQUEST, ACK) and
`asrs/v1/<asrsId>/up` (ACK, JOB_DONE, JOB_FAILED, UNIT_STATE, PORT_ARRIVAL). Its own codec and retransmitter live in
`internal/adapters/telegram`, the client and ordered worker in `internal/adapters/mqtt`.

- Every non-ACK up telegram is ACKed after its effect committed; it is claimed in `processed_telegrams` with the ACK id
  in that transaction, so a duplicate gets the identical ACK, also after a restart. An undecodable telegram with a
  `telegramId` is ACKed `accepted:false, reason:MALFORMED`.
- Down telegrams leave from a `UnitOfWork.AfterCommit` hook and are retransmitted with the same id after 2 s real time,
  at most 3 times. An expired command fails its job EQUIPMENT_UNREACHABLE; an expired or refused CANCEL is reverted. A
  refused command fails COMMAND_REFUSED. `NOT_READY` is transient and resent.
- The PLC gets the next job of an aisle only after the previous one's JOB_DONE or JOB_FAILED.
- HELD fails the aisle's queued jobs with EQUIPMENT_FAULT and refuses new ones (`422 aisle-unavailable`).

## Persistence

Postgres, migration `0001_asrs_control_schema`: `storage_maps` (JSONB snapshot per aisle, the aisle lock),
`storage_jobs`, `equipment_units`, `asrs_capacity`, `processed_telegrams`, `outbox_events`, `idempotency_keys`. The
idempotency keys and the telegram inbox are purged hourly (24 h).

## Packaging

`Dockerfile` (Go build stage, Alpine runtime, user 1000) and `charts/asrs-control`: one Deployment (component `api`,
exactly one replica, `Recreate`), a Service, a ConfigMap with the environment and `layout.yaml`, a Secret for
`DATABASE_URL`. The chart refuses to render without a database source, Kafka brokers or the MQTT URL, with more than one
replica, or with a grace period below 15 s.

## Quality evidence

Unit tests, golden CloudEvents tests, godog BDD (`features/asrs_control.feature`, 10 scenarios), testcontainers
integration tests (Postgres, Kafka, Mosquitto), an end-to-end test against the equipment-emulator image, arch-go and the
fleet fitness tests (`internal/architecture/`), gremlins mutation testing, Spectral and the spec-breaking gate on both
specs, chart render and selector tests. CI: `.github/workflows/ci.yml`; the docs site: `.github/workflows/docs.yml`.

## Known limits (ADR 0002)

- Protocol v1 has no job-state query: a JOB_DONE the PLC gave up retransmitting while asrs-control was down longer than
  about 8 s leaves that job ACKNOWLEDGED.
- A CANCEL the PLC accepted whose ACK and all retransmits are lost leaves the job waiting for an outcome (deliberately not
  auto-failed).
- A JOB_FAILED for POSITION_OCCUPIED / CONTAINER_NOT_AT_POSITION fails the job STORAGE_MAP_MISMATCH; the position is not
  quarantined.

## Planned, not built

- An MCP server and an A2A adapter, with the fleet agent authority levels.
- Kong/Gateway API routes in the chart, registration in warehouse-infra, a first release (no `main` yet).
- Measured mutation thresholds in `.gremlins.yaml`.
- A reconciliation of ACKNOWLEDGED jobs (UNIT_STATE / aisle watchdog, or a protocol v2 job-state query).
- material-flow-control itself (WCS plan 04), the main consumer of the interface.

## Non-goals

- No rack position, reshuffle or internal move on REST or Kafka.
- No knowledge of container contents, SKUs or the stock ledger.
- No transport outside the AS/RS ports, no building locations.
- No authentication on REST (fleet decision 2026-09-11).
- More than one replica: the aisle scheduler is single-writer.
