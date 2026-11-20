---
name: how-to-test
description: Write or review tests and diagnose a failing coverage, mutation, bdd or integration CI job: the four test layers, the 90% gate, gremlins threshold semantics, the testcontainers rule. Use when adding tests, killing a surviving mutant, or fixing a red check.
---

# How to test

Use when writing or reviewing tests here, or diagnosing a red `test`, `bdd`, `integration`,
`mutation-fast` or `arch-test` job. Passing `go test` is the weakest of the signals below; mutation
testing exists because green tests can assert nothing.

## The layers

1. **Unit** (`make test`): aggregates (`internal/domain/**`), the scheduler
   (`internal/domain/scheduling/next_test.go`), use cases against the in-memory doubles
   (`internal/adapters/outbound/memory`, wired by `internal/application/usecases/harness_test.go`),
   the telegram codec and retransmitter (`internal/adapters/telegram`), the MQTT link with a fake
   publisher (`internal/adapters/mqtt/link_test.go`), the HTTP adapter (`handlers_test.go`), and the
   golden CloudEvents JSON (`internal/adapters/outbound/events/golden_test.go`). Never a network or
   database call.
2. **Coverage** (`make coverage`, 90% on domain + application): proves lines ran, nothing more.
3. **Mutation** (`make mutation`, gremlins, `.gremlins.yaml`): proves the tests assert.
4. **BDD** (`make bdd`): `features/asrs_control.feature` through the real Service with the
   in-memory doubles and a scripted PLC (`features/steps_test.go`).
5. **Integration** (`make integration`, `//go:build integration`, Docker): Postgres repositories
   and the unit of work (`internal/adapters/outbound/postgres/*_integration_test.go`,
   `internal/testing/pgusecases`), the MQTT link against Mosquitto
   (`internal/adapters/mqtt/link_integration_test.go`), the relay against Kafka, the idempotency
   replay classes against Postgres, and the end-to-end test against the emulator image
   (`cmd/api/e2e_emulator_integration_test.go`, needs `EQUIPMENT_EMULATOR_IMAGE`).

## Domain-specific traps

- **Time.** Two clocks: asrs-control's (`ports.Clock`, the `*_at` fields) and the PLC's telegram
  timestamp (`plc_timestamp`, simulated in the emulator). Assert both, with distinct values, or a
  mutant swapping them survives.
- **Duplicates.** Every up-telegram test needs its duplicate twin: the same `telegramId` must
  return the same ACK id and change nothing (`uplink_test.go`, the BDD "duplicate JOB_DONE").
- **One outcome.** For a failure path assert the job's status AND that exactly one
  `StorageJobFailed` was appended, with its reason.
- **Depth rules.** Use a double-deep layout and distinct levels/slots so a wrong front/back or cost
  ordering shows (`internal/domain/storagemap/assign_test.go`).
- **Retransmit timing.** The retransmitter runs on real time; tests inject the wall clock
  (`newLink`), never sleep for 2 s.

## Mutation testing

gremlins fails when the measured value is `<=` the threshold. Quoted thresholds (`"95"`) never
gate: write numbers. Measure after `go clean -testcache` (a warm cache reports spurious TIMED OUT
mutants). Boundary guards need the boundary value itself; zero-valued fixtures hide arithmetic
mutants; do not pin an unspecified tie-break just to kill a `<`/`<=` mutant, document it instead.
To diagnose `mutation-fast`, compare the survivor set with `origin/develop`, not just the score.

## Integration tests: testcontainers only

Every Postgres or Kafka integration test starts its own container (`internal/testing/pgtest`,
`internal/testing/kafkatest`); never `os.Getenv("DATABASE_URL")` + `t.Skip`, never
`localhost:9092`. `TestKafkaIntegrationTestsUseTestcontainers` and
`TestPostgresIntegrationTestsUseTestcontainers` enforce it. CI's `integration` job provisions no
services.

## Verify before opening the PR

```bash
make check-all     # check + coverage + arch-test + bdd
make integration   # Docker
make mutation      # changed packages
```
