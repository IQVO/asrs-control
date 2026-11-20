---
name: how-to-add-a-rest-endpoint
description: Add or change a REST endpoint in this service in the fleet's hexagonal order (domain invariant, use case, port, HTTP adapter, apis/openapi.yaml, generated docs, godog scenario). Use when touching internal/adapters/inbound/http, apis/openapi.yaml, or exposing a use case over HTTP.
---

# How to add a REST endpoint

Follow this order: domain first, adapter last. A handler written before the rule it relies on
validates nothing and gets bypassed. The caller of this API is material-flow-control (the
subsystem-controller interface, ADR 0001): keep it shaped for any AS/RS controller, and never put
a rack position in a body (invariant 6).

The worked example is `POST /asrs/{asrsId}/ports/{portId}/pickups`:
`internal/domain/storagemap/motion.go` (`Map.Pickup`) ->
`internal/application/usecases/commands.go` (`Service.ConfirmPickup`) ->
`internal/adapters/inbound/http/handlers.go` (`handlePickup`), routed in `server.go`.
The read-side example is `GET /asrs/{asrsId}` (`queries.go` `Service.Asrs` -> `handleGetAsrs`).

## 1. Domain first

Does the rule already exist in `internal/domain/<storagemap|storagejob|equipment|scheduling|layout>/`?
A new rule goes into the aggregate with a named `Err...` variable and a table-driven test in the
same package. Pure Go: no I/O; `now`, ids and telegram ids are passed in. Re-run the mutation gate
for that package (`gremlins unleash ./internal/domain/<pkg>`).

## 2. Application: a Service method

Add a method to `usecases.Service` (one file per concern: `requests.go`, `commands.go`,
`queries.go`). A write resolves the layout (`s.Layout`), validates ports with `portOf`, then runs
inside `s.inAisle(ctx, l, aisle, func(ctx, a *aisle) error {...})`: that locks the aisle's map and
unit, runs the scheduler after your change, saves, appends the events and hands PLC commands to
the Downlink after commit. Never call `Downlink.Send` yourself. Map domain errors to the
`usecases.Err...` refusals in `service.go`. A new repository method goes into
`internal/application/ports/ports.go` (interfaces only) and is implemented in BOTH
`internal/adapters/outbound/postgres/` and `internal/adapters/outbound/memory/`, with the Postgres side
covered by `repos_integration_test.go`. Unit-test the method in
`internal/application/usecases/*_test.go` (the harness in `harness_test.go`): success AND every
refusal.

## 3. Adapter: the handler

In `internal/adapters/inbound/http/`:

- the request/response DTOs go in `dto.go` (snake_case JSON; domain types carry no tags);
- the handler goes in `handlers.go`: read path params with `params`, decode the body, call the
  Service, answer with the helpers in `response.go`;
- register the route in `server.go` `NewRouter`: a POST goes inside the `r.Group` that carries
  `s.Idempotency` (every POST needs `Idempotency-Key`), a GET outside it;
- a new refusal gets a row in `errors.go` `problemCatalogue` with its status, kebab-case slug and
  replay class (FINAL: stored and replayed; RETRYABLE: the key is released), and a case in
  `handlers_test.go` / `errors_test.go`. `idempotency_replay_classes_test.go` checks every problem
  type's class against the store.

## 4. Contract: apis/openapi.yaml

Describe the operation, its `Idempotency-Key` parameter (POST), the success body and every problem
it can return; list a new problem type in the info description's FINAL or RETRYABLE list. Lint and
regenerate the site's REST reference (the `docs-api-drift` CI job fails on a difference):

```bash
spectral lint apis/openapi.yaml --ruleset .spectral.yaml --fail-severity=warn
cd docs && npm ci && npm run clean-api-docs asrs-control && npm run gen-api-docs asrs-control
```

A removed or narrowed field is a breaking change: the `spec-breaking` job (oasdiff) fails it.

## 5. Behaviour: a godog scenario

If the endpoint changes behaviour material-flow-control relies on, add a scenario to
`features/asrs_control.feature` with its steps in `features/steps_test.go` (`make bdd`).

## 6. Verify before opening the PR

```bash
make check       # fmt-check vet build lint test (the pre-push hook)
make check-all   # + coverage, arch-test, bdd
```
