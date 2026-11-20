# CLAUDE.md — asrs-control

`asrs-control` is a bounded context of the `warehouse-systems` fleet (GitHub org `IQVO`, Go module
github.com/IQVO/asrs-control, scaffolded from warehouse-harness-template).
It is the **AS/RS controller** of the WCS tier, classification **Generic** (built behind the subsystem-controller
interface so a vendor AS/RS controller could replace it), CloudEvents segment `wes`. It answers "where does each
container sit inside the AS/RS, and which shuttle-and-lift job runs next?". It owns rack positions per aisle
(`StorageMap`), storage position assignment (velocity zone + double-deep depth rules), `StorageJob` scheduling and
port presentation order, reshuffles, PackML equipment state and capacity facts. It does NOT own container contents
or the stock ledger (inventory-storage), which container moves when (fulfillment-execution, wes-work-planning via
material-flow-control), transport beyond its ports (material-flow-control, amr-fleet-control) or the AS/RS as a
building location (facility-layout). Richest aggregate: package `storagejob`. One binary: `cmd/api` (REST on :8080,
one MQTT link per AS/RS, the outbox relay).

## Hard rules (each one cost an incident or is fitness-tested)

1. **Hexagonal dependency rule.** The domain layer (internal/domain) depends on nothing internal but domain;
   the application layer only on domain and application; inbound and outbound adapters never depend on each
   other; only cmd/ wires layers. No JSON tags, SQL or HTTP types in the domain. `make arch-test` enforces it
   (`internal/architecture/architecture_test.go`).
2. **Real infrastructure at runtime.** A cmd/ binary never imports the in-memory adapters (test doubles only;
   the fleet runs on real Postgres and Kafka): `TestRuntimeBinaryHasNoInMemoryAdapters`. No `.go` file at the
   module root (`TestNoGoFilesAtRepoRoot`); the godog runner lives in features/.
3. **No auth layer on REST or MCP** (fleet-wide revert 2026-09-11): never add bearer/JWT/API-key middleware
   (`TestNoAuthMiddlewareReintroduced`). Agent authority is declared per MCP tool / A2A skill instead
   (`.claude/rules/fleet/agent-authority.md`).
4. **No outbound calls to sibling contexts without an ADR.** Cross-context data arrives as events kept in
   local copies (`.claude/rules/fleet/context-boundaries.md`).
5. **An ADR comes first** for a new cross-context contract or any auth.
   This repo's own rules: (a) the PLC contract is AS/RS telegram protocol v1 of IQVO/equipment-emulator
   (docs/telegram-protocol.md there; asrs-control's side is in apis/asyncapi.yaml), Conformist: never change the wire shape here; (b) at most ONE job per aisle
   unit is in the PLC at a time, and every write locks its aisle (ADR 0002); (c) paho callbacks only enqueue; one
   worker per MQTT link handles up telegrams and retransmit expiries in order; (d) no rack position, reshuffle or
   internal move leaves this context, in REST or in events; (e) one replica only.
6. **Events: CloudEvents 1.0 is MANDATORY** on every Kafka message produced or consumed (structured mode,
   no flat envelope, no dual-write, no dual-read, no envelope toggle; `TestNoEventEnvelopeToggleOrFlatEnvelope`).
   Build and decode ONLY through `internal/adapters/kafka/cloudevents/` (generated from
   `templates/cloudevents/cloudevents.go.tmpl`); `type` is
   `com.warehouse.wes.asrs-control.<entity>.<EventName>`, with `source` and
   `dataschema` exactly as that helper builds them. A breaking payload change is a new `.v2` type, never a
   mutation. Consumers dispatch on the FULL `type`, dedupe on `id`, and skip/DLQ (never crash on) anything
   that fails validation. Full rule: `.claude/rules/integration-events.md`.
7. **Events leave through the transactional outbox only**: the aggregate save and the encoded CloudEvents
   commit in one transaction; a relay publishes. Never write to Kafka from a handler or a use case.

## Commands

```bash
make check-fast   # fmt-check + vet + arch-test: run before saying "done"
make check        # fmt-check vet build lint test
make check-all    # check + coverage + arch-test + bdd (the full local gate)
make integration  # real Postgres/Kafka via testcontainers (needs Docker)
make mutation     # gremlins on the richest aggregate (thresholds in .gremlins.yaml, measured first)
make guide-lint   # these guides: skills load, references resolve, context budget
make harness-test # unit-test the agent hooks
```

`cmd/api`: `DATABASE_URL`, `KAFKA_BROKERS`, `MQTT_URL` (e.g. `tcp://localhost:1883`) and `ASRS_LAYOUT_FILE`
(e.g. `config/layout.example.yaml`) are required; `MIGRATIONS_DATABASE_URL` (direct connection for the
boot migrations, default `DATABASE_URL`), `ADDR` (default `:8080`), `LOG_LEVEL`, `MQTT_CLIENT_ID`,
`OUTBOX_INTERVAL_MS`, `OUTBOX_BATCH`, `OUTBOX_MAX_ATTEMPTS`, `VELOCITY_WINDOW_HOURS`, `VELOCITY_FAST_RETRIEVALS`
and the telemetry variables `OTEL_EXPORTER_OTLP_ENDPOINT` (empty = telemetry off), `OTEL_SERVICE_NAME`,
`SERVICE_VERSION`, `ENVIRONMENT` (ADR 0004) are optional. Run the emulator beside it for a PLC.

## Where things are written down (read before editing the matching area)

- `.claude/rules/domain-model.md`: ubiquitous language, aggregates, events, use cases.
  `.claude/rules/rest-api.md`: endpoints and error slugs.
  `.claude/rules/integration-events.md`: published/consumed events and the CloudEvents rule.
- `.claude/rules/fleet/`: fleet-wide rules copied from the harness template; never edit them here
  (their canonical copies live in warehouse-docs, agents/fleet).
- `HARNESS.md`: what each sensor runs and why. `INCIDENTS.md`: the fleet incident log (template-managed,
  byte-identical in every repo; add a row in warehouse-harness-template first).
  `.claude/memory/MEMORY.md`: this repo's own durable heuristics.
- `docs/before-first-release.md`: packaging checklist to finish before the first develop -> main release.
  ADRs are in docs/adr (index.md).
- `docs/`: the Docusaurus site (pages in `docs/docs/`, ADRs served in place from `docs/adr`), built by
  `.github/workflows/docs.yml` and published from `main` to https://iqvo.github.io/asrs-control/. After any change to
  `apis/openapi.yaml` run `cd docs && npm ci && npm run clean-api-docs asrs-control && npm run gen-api-docs asrs-control`
  and commit `docs/docs/api-reference/rest/` (the `docs-api-drift` CI job fails otherwise); `npm run build` must pass.
- `apis/asyncapi.yaml` holds BOTH the Kafka CloudEvents channels and the MQTT telegram channels
  (`asrs/v1/{asrsId}/down|up`, server `plc-link`); the telegram channels mirror equipment-emulator's spec.

harness-template: v4

<!-- harness:scoped-rules:start (generated by tools/migrate_v3.py in warehouse-harness-template; do not hand-edit) -->
## Scoped rules and harness

Claude Code loads each rule below automatically when you touch the matching paths. OpenCode and Codex do NOT: read the rule BEFORE editing matching files.

| When touching | Read |
|---|---|
| `internal/adapters/**/kafka/**`, `internal/adapters/outbound/events/**`, `apis/asyncapi*` | `.claude/rules/integration-events.md` |
| `internal/adapters/inbound/http/**`, `apis/openapi*.yaml`, `apis/openapi/**` | `.claude/rules/rest-api.md` |

Hooks (`scripts/harness/hook.py`, wired for Claude Code, Codex and OpenCode) block pushes to develop/main, `--no-verify`, bare `rm -rf`, and edits to generated files, and feed gofmt/vet findings back after each edit. Before saying "done" run `make check-fast`; the full gate is `make check-all`. `HARNESS_OFF=1` disables the hooks when debugging the harness itself.
<!-- harness:scoped-rules:end -->
