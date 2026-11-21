---
id: overview
title: API overview
slug: /api-reference
sidebar_position: 1
description: "The interfaces of asrs-control (REST, Kafka events, the MQTT telegram link), the endpoint table and the contract conventions."
---

# API overview

:::info Implemented by cmd/api
`cmd/api` serves the REST routes and the probes (`internal/adapters/inbound/http`) on `ADDR` (default `:8080`), runs one
MQTT link per AS/RS and the outbox relay. The contracts are `apis/openapi.yaml` (OpenAPI 3.1.0, version 1.0.0, 7
operations) and `apis/asyncapi.yaml` (AsyncAPI 2.6.0, version 1.0.0: 2 Kafka channels and 2 MQTT channels).
:::

| Interface | Source of truth | Notes |
| --- | --- | --- |
| REST | `apis/openapi.yaml` | The pages under *REST* are generated from the spec with `npm run gen-api-docs`; do not edit them by hand (the `docs-api-drift` CI job fails on a difference). Caller: material-flow-control. |
| Events | `apis/asyncapi.yaml`, Kafka channels | Five published CloudEvents types, none consumed. Summarised in the [event and telegram catalogue](/docs/api-reference/events); every field on [Domain events](/docs/ddd/domain-events). |
| PLC link | `apis/asyncapi.yaml`, MQTT channels | Telegram protocol v1 (IQVO/equipment-emulator `docs/telegram-protocol.md`), asrs-control's side ([ADR 0002](/docs/adr/0002-telegram-conformance-and-aisle-scheduling)). |
| MCP, A2A | none | Not built yet (a later fleet-standard wave). |

## Endpoints

| Tag | Endpoint | Operation id | Success |
| --- | --- | --- | --- |
| storage-jobs | `POST /asrs/{asrsId}/storage-jobs` | `createStorageJob` | `202`, the job (STORE reserves its position now) |
| storage-jobs | `GET /asrs/{asrsId}/storage-jobs/{storageJobId}` | `getStorageJob` | `200`, the job |
| storage-jobs | `POST /asrs/{asrsId}/storage-jobs/{storageJobId}/cancellation` | `cancelStorageJob` | `200` cancelled at once, `202` a CANCEL went to the PLC |
| asrs | `GET /asrs/{asrsId}` | `getAsrs` | `200`, aisles, ports, recommended input port, capacity |
| asrs | `POST /asrs/{asrsId}/ports/{portId}/pickups` | `confirmPortPickup` | `200`, the port is free |
| health | `GET /healthz` | `getLiveness` | `200` |
| health | `GET /readyz` | `getReadiness` | `200` ready; `503` draining or Postgres, Kafka or an MQTT link down |

A store or retrieve is accepted or refused at once; its progress is reported only by events. No rack position appears in
any body.

## Idempotency

Every POST requires `Idempotency-Key` (1-128 characters; missing or longer is `400 idempotency-key-required`). A retry
with the same key and body gets the stored answer with `Idempotency-Replayed: true`; the same key with another body is
`409 idempotency-key-reused`. Only 2xx and FINAL refusals are stored, for 24 hours; RETRYABLE refusals release the key.
A copy that arrives while the first request is still in flight waits for it, and before it answers a RETRYABLE (or
5xx) refusal of its own it re-reads the key: when the first request's answer was stored meanwhile, that answer is
replayed instead (another body: `409 idempotency-key-reused`).

## Errors

RFC 9457 `application/problem+json` with a stable `code`, type `https://errors.asrs-control.warehouse-systems.dev/<code>`
(`internal/adapters/inbound/http/errors.go`).

| Status | Code | Class |
| --- | --- | --- |
| 400 | `bad-request`, `invalid-parameter`, `idempotency-key-required` | FINAL |
| 404 | `asrs-not-found`, `storage-job-not-found` | FINAL |
| 409 | `idempotency-key-reused` | FINAL |
| 422 | `unknown-port`, `wrong-port-kind` | FINAL (configuration) |
| 422 | `unknown-container`, `container-busy`, `container-already-stored`, `storage-full`, `aisle-unavailable`, `port-unreachable` | RETRYABLE (state) |
| 409 | `port-not-presenting`, `job-not-cancellable`, `job-ended` | RETRYABLE |
| 500, 503 | `internal-error`, `dependency-unavailable` | RETRYABLE |

No authentication, by fleet decision (2026-09-11); `TestNoAuthMiddlewareReintroduced` keeps it so.
