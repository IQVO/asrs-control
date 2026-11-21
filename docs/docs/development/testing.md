---
id: testing
title: Testing
sidebar_label: Testing
sidebar_position: 1
description: "The test layers of asrs-control, the make targets, the CI jobs that run them and the quality gates."
---

# Testing

| Layer | Where | Run | What it proves |
| --- | --- | --- | --- |
| Unit | `internal/domain/**/*_test.go`, `internal/application/usecases/*_test.go`, adapter tests | `make test` | aggregates, the scheduler, use cases against `internal/adapters/outbound/memory` doubles, the telegram codec and retransmitter, the HTTP adapter |
| Golden | `internal/adapters/outbound/events/golden_test.go`, `internal/adapters/kafka/cloudevents/cloudevents_test.go` | `make test` | the exact CloudEvents JSON of every published type and the `content-type` header |
| BDD | `features/asrs_control.feature` (godog, `features/*_test.go`) | `make bdd` | ten scenarios: stores reported once, depth rule D2, reshuffle publishes nothing, port order, HELD fails queued jobs, duplicate JOB_DONE, refused command, unanswered command, cancel at once, refused cancel |
| Integration | `//go:build integration`: `internal/adapters/outbound/postgres/*_integration_test.go`, `internal/testing/pgusecases`, `internal/adapters/mqtt/link_integration_test.go`, `internal/adapters/outbound/kafka/relay_sink_latency_integration_test.go`, `internal/adapters/inbound/http/idempotency_replay_classes_test.go` | `make integration` (Docker) | real Postgres, Kafka and Mosquitto through testcontainers |
| End to end | `cmd/api/e2e_emulator_integration_test.go` | `make integration` with `EQUIPMENT_EMULATOR_IMAGE` (`docker build -t equipment-emulator:e2e <equipment-emulator checkout>`) | the whole service against the emulator's PLC |
| Architecture | `internal/architecture/` | `make arch-test` | layer direction and the fleet fitness rules |
| Mutation | gremlins, `.gremlins.yaml` | `make mutation` (changed packages), `make mutation-full` | the tests assert, not merely execute |
| Chart | `charts/asrs-control/tests/render_test.sh`, `test_service_selectors.py`, `test_kong_config.py` | `bash charts/asrs-control/tests/render_test.sh`, `python3 charts/asrs-control/tests/test_kong_config.py` | the env ConfigMap is mounted, every env var each binary reads is rendered, the render gates fire, each Service selects its own Deployment, the Kong routes and plugins follow the fleet contract (default off, annotated, no credential plugin, cors headers) |
| Contracts | Spectral, `asyncapi_breaking.py`, oasdiff | `spectral lint ...`, CI `spec-breaking` | the specs lint and no PR breaks a consumer |

Integration tests start their own containers (testcontainers-go); none reads `DATABASE_URL` or `KAFKA_BROKERS` to
skip itself (`TestKafkaIntegrationTestsUseTestcontainers`, `TestPostgresIntegrationTestsUseTestcontainers`).

## Local gate

```bash
make check-fast   # fmt-check vet arch-test
make check        # fmt-check vet build lint test (the pre-push hook)
make check-all    # check + coverage + arch-test + bdd
make integration  # needs Docker
```

`lefthook install` once per clone: pre-commit runs fmt-check, vet and lint; pre-push runs `make check`. Never bypass
the hooks.

## CI (`.github/workflows/ci.yml`)

lint, guide-lint, complexity, file-length, test, bdd, integration, mutation-fast, mutation (scheduled), drift
(scheduled), api-lint, vuln, docs-api-drift, helm-lint, chart-tests, arch-test, spec-breaking, trivy-scan,
docker-publish and release (main only). `.github/workflows/docs.yml` builds this site on every PR that touches `docs/`
or `apis/`, and publishes it from `main`.

## Kits

The fleet kits this repository carries, as plain Markdown next to this site:
[idempotency replay classes](https://github.com/IQVO/asrs-control/blob/develop/docs/idempotency-replay-classes.md),
[contract testing](https://github.com/IQVO/asrs-control/blob/develop/docs/contract-testing.md),
[spec-breaking kit](https://github.com/IQVO/asrs-control/blob/develop/docs/spec-breaking-kit.md) and the
[before-first-release checklist](https://github.com/IQVO/asrs-control/blob/develop/docs/before-first-release.md).
