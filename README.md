# asrs-control

`asrs-control` is the AS/RS controller bounded context of the warehouse-systems WCS tier: it owns where each
container sits inside the automated storage and retrieval system (rack positions per aisle), assigns storage
positions by velocity zone with double-deep depth rules, schedules store and retrieve jobs one at a time per aisle
unit, presents retrieved containers at output ports in request order, reshuffles blocking containers, and tracks
PackML equipment state and capacity. It does not own container contents or the stock ledger (inventory-storage),
which container moves when (fulfillment-execution and wes-work-planning, through material-flow-control), transport
beyond its ports (material-flow-control, amr-fleet-control) or the AS/RS as a building location (facility-layout).
Classification: Generic, built behind the subsystem-controller interface so a vendor controller could replace it.

## Disclaimer

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company, and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.

## Documentation

The documentation site is built from `docs/` (Docusaurus) and published to
[iqvo.github.io/asrs-control](https://iqvo.github.io/asrs-control/) from `main` by `.github/workflows/docs.yml`
(pull requests only build it). The strategic design is the warehouse-docs WCS section:
[asrs-control](https://iqvo.github.io/warehouse-docs/wcs/asrs-control).

| Page | What it holds |
| --- | --- |
| `docs/docs/overview/` | introduction, architecture, quickstart |
| `docs/docs/operations/` | configuration, runbook, observability, troubleshooting |
| `docs/docs/development/testing.md` | test layers, make targets, CI jobs |
| `docs/docs/ddd/` | ubiquitous language, use cases, classification and the ddd-crew artifact pack |
| `docs/docs/api-reference/` | API overview, the REST reference generated from `apis/openapi.yaml`, the event and telegram catalogue |
| `docs/docs/ecosystem/integration.md` | neighbours, topics, broker, what is not wired yet |
| `docs/adr/` | ADR 0001 (the context), 0002 (telegram conformance, the layout file, one job per aisle unit), 0003 (CloudEvents and the type catalogue), served on the site under `/docs/adr` |
| `apis/openapi.yaml` | the REST subsystem-controller interface |
| `apis/asyncapi.yaml` | the five published events (Kafka) and the telegram protocol v1 channels to the PLC (MQTT) |
| `config/layout.example.yaml` | the layout file format |
| `SPEC.md` | the long description of what is built and what is planned |

## Architecture

Hexagonal, enforced by `internal/architecture/architecture_test.go` (arch-go):

    cmd/api                      composition root
    internal/domain/             layout, storagemap, storagejob, equipment, capacity, scheduling
    internal/application/        ports, usecases (one Service, every write in one aisle unit of work), outbox
    internal/adapters/inbound/http   REST
    internal/adapters/mqtt + telegram  the PLC link (telegram protocol v1)
    internal/adapters/outbound/  postgres, events, outbox relay, kafka, memory (test doubles), system

## Domain events / Integration

Publishes CloudEvents 1.0 (`com.warehouse.wes.asrs-control.<entity>.<EventName>`) to
`warehouse.asrs-control.events` and `warehouse.asrs-control.analytics`, for example
`com.warehouse.wes.asrs-control.storagejob.ContainerPresentedAtPort`. Five types: ContainerStored,
ContainerPresentedAtPort, StorageJobFailed (storagejob), EquipmentStateChanged (equipmentunit), AsrsCapacityChanged
(asrs). Consumes nothing from Kafka. Speaks AS/RS telegram protocol v1 (IQVO/equipment-emulator
`docs/telegram-protocol.md`) on `asrs/v1/<asrsId>/down` and `/up`, described in `apis/asyncapi.yaml` (server
`plc-link`).

## Running it

`cmd/api` (HTTP on `:8080`):

| Variable | Default | Meaning |
| --- | --- | --- |
| `DATABASE_URL` | required | Postgres DSN (migrations run at boot) |
| `MIGRATIONS_DATABASE_URL` | `DATABASE_URL` | direct connection for golang-migrate |
| `KAFKA_BROKERS` | required | comma-separated brokers |
| `MQTT_URL` | required | the broker of the PLC link |
| `ASRS_LAYOUT_FILE` | required | the layout YAML (ADR 0002) |
| `MQTT_CLIENT_ID` | `asrs-control` | client id prefix (`<prefix>-<asrsId>`) |
| `ADDR` | `:8080` | listen address |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `OUTBOX_INTERVAL_MS` / `OUTBOX_BATCH` / `OUTBOX_MAX_ATTEMPTS` | `250` / `100` / `20` | relay tuning |
| `VELOCITY_WINDOW_HOURS` / `VELOCITY_FAST_RETRIEVALS` | `168` / `3` | FAST/SLOW classification |

## Observability

JSON logs on stdout (`log/slog`). OpenTelemetry traces and metrics are not wired yet (ADR 0001).

## API

| Route | Purpose |
| --- | --- |
| `GET /asrs/{asrsId}` | aisles, ports, recommended input port, capacity |
| `POST /asrs/{asrsId}/storage-jobs` | request a STORE or a RETRIEVE (202) |
| `GET /asrs/{asrsId}/storage-jobs/{storageJobId}` | read a job |
| `POST /asrs/{asrsId}/storage-jobs/{storageJobId}/cancellation` | cancel (200 cancelled, 202 asked the PLC) |
| `POST /asrs/{asrsId}/ports/{portId}/pickups` | confirm a container left an output port |

Every POST takes `Idempotency-Key`; 2xx and FINAL refusals replay for 24 hours with `Idempotency-Replayed: true`,
RETRYABLE refusals release the key (the class of every problem type is listed in `apis/openapi.yaml`). Errors are
`application/problem+json` with a `code`. No authentication (fleet rule).

## Helm chart

`charts/asrs-control` (lint fixtures `ci/default-values.yaml` and `ci/all-components-values.yaml`): one Deployment (component `api`, exactly one replica, Recreate), a Service, a ConfigMap with
the environment and `layout.yaml`, and a Secret for `DATABASE_URL`. `database.url` (or `database.existingSecret`)
and `kafka.brokers` are mandatory.

## Local development

`make check-fast`, `make check`, `make check-all`, `make integration` (Docker; the e2e test also needs
`docker build -t equipment-emulator:e2e <equipment-emulator checkout>`), `make mutation`.

## Tests

Unit and BDD (`features/asrs_control.feature`, `make bdd`), integration (`//go:build integration`: Postgres
repositories, the MQTT link against Mosquitto, the end-to-end test against the emulator). CI jobs: lint, guide-lint,
complexity, file-length, test, bdd, integration, mutation-fast, mutation, drift, api-lint, vuln, docs-api-drift,
helm-lint, chart-tests, arch-test, spec-breaking, trivy-scan, docker-publish, release; `.github/workflows/docs.yml`
builds the documentation site.

## Architecture Decision Records

3 ADRs, indexed in `docs/adr/index.md`.

## License

See `LICENSE`.
