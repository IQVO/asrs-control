---
paths:
  - "internal/adapters/inbound/http/**"
  - "apis/openapi*.yaml"
  - "apis/openapi/**"
---

# REST API (inbound adapter)

Routes (apis/openapi.yaml), each to one `usecases.Service` method:

- `GET /healthz` -> liveness; `GET /readyz` -> Postgres, Kafka and every MQTT link
- `GET /asrs/{asrsId}` -> `Asrs` (aisles, units, ports, recommended input port, capacity)
- `POST /asrs/{asrsId}/storage-jobs` -> `RequestStore` (kind STORE) / `RequestRetrieve` (kind RETRIEVE); 202
- `GET /asrs/{asrsId}/storage-jobs/{storageJobId}` -> `Job`
- `POST /asrs/{asrsId}/storage-jobs/{storageJobId}/cancellation` -> `Cancel`; 200 when cancelled at once, 202 when
  a CANCEL telegram went to the PLC
- `POST /asrs/{asrsId}/ports/{portId}/pickups` -> `ConfirmPickup`; 200

## Conventions

- Errors: RFC 9457 `application/problem+json` with `code`; every problem type declares a replay class in
  `errors.go` (FINAL stored and replayed, RETRYABLE never stored). Store/retrieve refusals that depend on state are
  422 RETRYABLE (unknown-container, container-busy, container-already-stored, storage-full, aisle-unavailable,
  port-unreachable); 422 unknown-port and wrong-port-kind are FINAL (configuration); 409 port-not-presenting,
  job-not-cancellable and job-ended are RETRYABLE; 400 and 404 are FINAL.
- Every POST requires `Idempotency-Key` (1-128 chars); replays carry `Idempotency-Replayed: true`; 24 h window.
- Unauthenticated by fleet decision (2026-09-11); `TestNoAuthMiddlewareReintroduced` keeps it so.
- Bodies are snake_case. No rack position appears in any body.
