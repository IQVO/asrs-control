---
name: how-to-add-an-integration-event
description: Publish or consume a cross-service Kafka event: CloudEvents 1.0 type naming, AsyncAPI, transactional outbox, consumer-group rules. Use when touching internal/adapters kafka or outbox code, a publisher/consumer, or apis/asyncapi*.yaml.
---

# How to add an integration event (publish and consume)

asrs-control publishes five CloudEvents types and consumes none (ADR 0003). A new event, or a
first consumer, is a contract change for material-flow-control and exception-management: an
ADR 0003 amendment (a new ADR) lists the new type, or `TestEventCatalogueMatchesContract` fails.

## Publishing a new event

### 1. Is it a fact outside the rack?

Invariant 6: no rack position, reshuffle or internal move is ever published. A new event must be
a fact material-flow-control (or another neighbour) needs about the AS/RS as one location: a job
outcome, an equipment state, a capacity. If it carries a `layout.Position`, it does not belong on
Kafka.

### 2. The domain event

Add the struct next to its aggregate (`internal/domain/storagejob/events.go`,
`internal/domain/equipment/unit.go`, `internal/domain/capacity/capacity.go`) and return it from the
aggregate method that raises it. The use case appends it to `a.events` inside `inAisle`; `save`
writes it through `ports.EventSink`.

### 3. The encoder

In `internal/adapters/outbound/events/encode.go` add the snake_case payload struct and a case in
`describe` returning the `occurrence`: entity segment (`storagejob`, `equipmentunit`, `asrs`), the
PascalCase name, the `subject` (also the Kafka key) and the time. `*_at` is asrs-control's clock;
a fact from a telegram also sets `plc_timestamp` (`plcTime(...)`). `Sink.Append`
(`sink.go`) mints one CloudEvents id and writes two outbox rows, `warehouse.asrs-control.events`
and `warehouse.asrs-control.analytics`, through `internal/adapters/kafka/cloudevents` (never a
hand-rolled envelope). The relay publishes them; nothing else writes to Kafka.

`type` = `com.warehouse.wes.asrs-control.<entity>.<EventName>`, `dataschema`
`urn:warehouse:asrs-control:<events|analytics>:<EventName>:v1`. A breaking payload change is a
new `:v2` dataschema, never a mutation of v1.

### 4. Contract and catalogue

- `apis/asyncapi.yaml`: a message under `components.messages` (and its analytics twin when the
  examples differ), a payload schema built on `CloudEventEnvelope` with the `type` const, an
  example per message (Spectral `asyncapi-message-examples` is an error), and a `oneOf` entry on
  both Kafka channels. Keep the MQTT telegram channels untouched.
- A new ADR amending ADR 0003's type table (ADR bodies are immutable).
- `.claude/rules/integration-events.md` "Published types" table and the site's
  `docs/docs/ddd/domain-events.md` and `docs/docs/api-reference/events.md`.
- `spectral lint apis/asyncapi.yaml --ruleset .spectral.asyncapi.yaml --fail-severity=warn` and
  `python3 scripts/harness/asyncapi_breaking.py <base> apis/asyncapi.yaml`.

### 5. Test

Add the type to `internal/adapters/outbound/events/golden_test.go` (exact JSON of every
attribute and the `content-type` header), the aggregate test that raises it, and a use-case test.
A delivery test against a real broker uses testcontainers (`internal/testing/kafkatest`); a
skip-gated `KAFKA_BROKERS` test fails `TestKafkaIntegrationTestsUseTestcontainers`.

## Consuming an event (none today)

asrs-control calls no sibling and consumes nothing (ADR 0001). A first consumer is an
architectural decision: write the ADR first. Then the fleet rules apply:

- Know only the producer's topic, full `type` strings and payload shape, never its Go types.
- Decode with `cloudevents.Decode`, dispatch on the FULL `type`, ignore unknown types, dedupe on
  `id`, skip (WARN, commit past) anything that fails validation; never parse a legacy flat shape.
- Consumer group: a named, env-configurable group for a single-instance consumer, or a
  per-process-unique group for a cache that replays the topic on every start. Never an inline
  literal (`TestKafkaConsumerGroupNeverHardcodedInline`).
- Pin the producer's spec in `contracts/pinned.json` (see `contracts/pinned.example.json` and `docs/contract-testing.md`).

## Verify before opening the PR

```bash
make check-all
go test ./internal/architecture/ -run TestEventCatalogueMatchesContract -v
```
