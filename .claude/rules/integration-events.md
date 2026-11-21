---
paths:
  - "internal/adapters/**/kafka/**"
  - "internal/adapters/outbound/events/**"
  - "apis/asyncapi*"
---

# Cross-service integration events (Kafka)

This service PUBLISHES only, through the transactional outbox, every event to BOTH
`warehouse.asrs-control.events` (integration) and `warehouse.asrs-control.analytics` (same `id` and `type`,
`dataschema` stream segment `analytics`). It consumes no Kafka topic.

## Events: CloudEvents 1.0 is MANDATORY

Every Kafka message this service produces or consumes (integration
`warehouse.<ctx>.events` AND analytics `warehouse.<ctx>.analytics`) is a
CloudEvents 1.0 event in structured content mode. This is a hard fleet rule,
not a preference — there is nothing to "choose" here:

- No flat envelope (`event_id`/`event_type`/`occurred_at`), no dual-write,
  no dual-read, no envelope toggle env var (`EVENT_ENVELOPE_MODE` is gone
  fleet-wide). `internal/architecture/fitness_test.go`'s
  `TestNoEventEnvelopeToggleOrFlatEnvelope` fails CI on any of them.
- Build/validate/(un)marshal with `github.com/cloudevents/sdk-go/v2/event`
  (latest v2) via ONE helper package,
  `internal/adapters/kafka/cloudevents/` (source `/warehouse/asrs-control`, `wes` segment;
  it was generated from the fleet template and only its per-repo constants differ). The
  only producing caller is `internal/adapters/outbound/events` (`Sink.Append`, payloads in
  `encode.go`), which writes both outbox rows. Transport stays `segmentio/kafka-go` (no sdk-go
  protocol/client packages, no hand-rolled CloudEvent structs).
- Every produced message carries the Kafka header
  `content-type: application/cloudevents+json; charset=UTF-8`
  (`cloudevents.ContentTypeHeader()`), next to the W3C trace headers
  (`traceparent`/`tracestate` stay in headers, never duplicated into
  extension attributes). Message key = aggregate id, `kafkago.Hash{}`
  balancer.
- Required attributes: `specversion=1.0`; `id` (UUID v4 minted ONCE per
  domain event and persisted with the outbox row, so redelivery carries
  the same id); `source=/warehouse/<repo>`; `type`; `subject` (aggregate
  instance id, never empty); `time` (domain occurred-at, UTC);
  `datacontenttype=application/json`;
  `dataschema=urn:warehouse:<repo>:<events|analytics>:<EventName>:v<N>`.
  No custom extension attributes without an ADR.
- `type` = `com.warehouse.<subdomain>.<bounded-context>.<entity>.<EventName>`
  (this repo: `com.warehouse.wes.asrs-control`). The SAME `type` names the
  occurrence on both the integration and the analytics topic; `dataschema`
  names the payload shape. Breaking payload change => new `.v2` type + new
  dataschema version, never mutate an existing one.
- Consumers decode with `cloudevents.Decode` (validates), dispatch on the
  FULL `type` string (never a short name or suffix match), ignore unknown
  types, read `time`/`subject` from attributes and the payload via
  `DataAs`, dedupe on `id`, and DLQ/skip — WARN log + commit past, never
  crash, never block the partition, never fall back to parsing a legacy
  shape — anything that fails CloudEvents validation.
- Tests: a golden exact-JSON test per published `type` (all attributes +
  the `content-type` header) in `internal/adapters/outbound/events/golden_test.go`; a
  legacy-flat-message-rejected test per consumer (none yet); Kafka integration tests via
  testcontainers only (`internal/testing/kafkatest`).
- The MQTT telegram channels in `apis/asyncapi.yaml` are NOT CloudEvents: flat JSON of
  telegram protocol v1 (ADR 0002), the only place a rack position may appear.

Full standard, subdomain table and the fleet's cross-service type
catalogue: warehouse-docs docs/strategic-design/event-standard-cloudevents.md.
This repo records it as ADR 0003 (`docs/adr/0003-cloudevents-envelope-and-type-catalogue.md`),
the type catalogue `TestEventCatalogueMatchesContract` checks `apis/asyncapi.yaml` against.

### Published types

| `type` | topic(s) | `subject` | `dataschema` |
| --- | --- | --- | --- |
| `com.warehouse.wes.asrs-control.storagejob.ContainerStored` | events + analytics | storage job id | `urn:warehouse:asrs-control:events:ContainerStored:v1` |
| `com.warehouse.wes.asrs-control.storagejob.ContainerPresentedAtPort` | events + analytics | storage job id | `urn:warehouse:asrs-control:events:ContainerPresentedAtPort:v1` |
| `com.warehouse.wes.asrs-control.storagejob.StorageJobFailed` | events + analytics | storage job id | `urn:warehouse:asrs-control:events:StorageJobFailed:v1` |
| `com.warehouse.wes.asrs-control.equipmentunit.EquipmentStateChanged` | events + analytics | `<asrsId>/<unitId>` | `urn:warehouse:asrs-control:events:EquipmentStateChanged:v1` |
| `com.warehouse.wes.asrs-control.asrs.AsrsCapacityChanged` | events + analytics | AS/RS id | `urn:warehouse:asrs-control:events:AsrsCapacityChanged:v1` |

### Consumed types

None.

## Consumer group id

Not applicable: no consumer.
