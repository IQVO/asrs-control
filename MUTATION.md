# Mutation testing: residual mutants

Gate: `.gremlins.yaml` (`efficacy: 99`, `mutant-coverage: 99`, the fleet target), measured per package with
`gremlins unleash ./internal/domain/<pkg>` (what `make mutation` and the `mutation-fast` CI job run: one run per
package under `internal/domain/`), and over the whole domain by `make mutation-full` (the weekly `mutation` job).

Measured 2026-11-20, gremlins v0.6.0, `go clean -testcache` first:

| Package | Killed | Lived | Not covered | Timed out | Efficacy | Mutator coverage |
|---|---|---|---|---|---|---|
| capacity | 1 | 0 | 0 | 0 | 100.00% | 100.00% |
| equipment | 7 | 0 | 0 | 0 | 100.00% | 100.00% |
| layout | 57 | 0 | 0 | 3 | 100.00% | 100.00% |
| scheduling | 5 | 0 | 0 | 0 | 100.00% | 100.00% |
| storagejob | 31 | 0 | 0 | 0 | 100.00% | 100.00% |
| storagemap | 72 | 0 | 0 | 0 | 100.00% | 100.00% |

Aggregate (`make mutation-full`): 173 killed, 0 lived, 0 not covered, 3 timed out, efficacy 100.00%, mutator
coverage 100.00%. Before the ratchet the same run read 163 killed, 9 lived, 8 not covered (efficacy 94.77%,
mutator coverage 95.56%), the gate sat at 93/92 and `mutation-fast` covered `storagejob` only.

## Mutants that cannot be killed

None today. The three TIMED OUT mutants are detected, not survivors: gremlins leaves them out of both ratios.

| Package | File:line | Mutator | Why |
|---|---|---|---|
| layout | layout.go:157:33, :158:30, :159:36 | INCREMENT_DECREMENT | `lv++` / `s++` / `d++` -> `--` in `AislePositions` loops forever and appends without bound, so the test binary hits gremlins' timeout. In CI `ulimit -v 4000000` (both mutation jobs) makes it die "out of memory" first, which gremlins counts as KILLED. |

## Equivalent mutants removed by restructuring (behaviour unchanged)

A mutant no input can tell apart from the original is restructured away rather than argued about. Each change kept
every existing test green, and a test pins the behaviour. Line numbers are those of the code before the change.

| Package | Was | Mutator | Why it was equivalent | Now |
|---|---|---|---|---|
| layout | layout.go:92 `if p.Kind == PortInput { in[a] = true } else { out[a] = true }` | CONDITIONALS_NEGATION | The negation swaps the in-set and the out-set, and the check that follows (`!in[a] \|\| !out[a]`, one error message) is symmetric in them. | Ports are recorded under an `(aisle, kind)` key: there is no kind test to invert. |
| storagemap | assign.go:58, :60 `ca < cb` / `ca > cb` | CONDITIONALS_BOUNDARY | Evaluated only under `if ca != cb`, where `<` and `<=` (`>` and `>=`) give the same answer. | `better` compares cost once; a tie falls to the stable sort. |
| storagemap | assign.go:63, :65 `a.Level < b.Level` / `a.Slot < b.Slot` | CONDITIONALS_BOUNDARY | Same shape: under `a.Level != b.Level`; and two candidates with the same level, slot and depth preference are the same position. | Explicit level/slot keys dropped: `Assign` sorts stably over `AislePositions` (level, slot, front first), so a cost tie keeps the lower level, then the lower slot. `TestFastBreaksACostTieByTheLowerLevel`, `TestSlowBreaksACostTieByTheLowerLevel` and `TestZeroCostFallsBackToLowestLevelThenSlot` pin that order and kill `<` -> `<=` / `>` -> `>=` on the cost comparison. |

## Coverage-profile artefacts removed

Go's cover profile does not instrument the `case` expressions of a tagless `switch`, so gremlins reported their
mutants NOT COVERED although every branch was tested. Both functions are now if-chains with the same logic (own
commit, tests unchanged): `layout.validatePorts` (6 mutants, layout.go:86, :88) and `storagejob.Job.Present`
(2 mutants, transitions.go:105, :107, now `scanOf`). Write new pure classifiers as if-chains for the same reason.

## Survivors killed by tests (2026-11-20)

| Package | Mutant (line before the change) | Test |
|---|---|---|
| layout | layout.go:181 `v < 1` -> `v <= 1` (AisleOfUnit) | `TestAisleOfUnitAcceptsTheFirstAndTheLastAisle` |
| storagemap | assign.go:62 `a.Level != b.Level` negated | `TestFastBreaksACostTieByTheLowerLevel`, `TestSlowBreaksACostTieByTheLowerLevel` |
| storagemap | assign.go:107:60 `q.Level == blocked.Level` negated (PlanReshuffle's same-slot exclusion) | `TestPlanReshuffleMayUseTheSameSlotOnAnotherLevel` |
| storagemap | map.go:124:73 `m.reserved[p.Front()] != ""` negated (checkConsistent) | `TestRehydrateKeepsAReservedBackBehindAFreeFront` |

Each kill was proven by running the package's tests against the mutated file with `go test -overlay`.
