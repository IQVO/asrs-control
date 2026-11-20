---
id: agent
title: A2A agent
sidebar_label: A2A agent
sidebar_position: 2
description: "The read-only Agent2Agent agent of asrs-control: the Agent Card, the job_status and asrs_capacity skills at authority L1, and the in-band problems."
---

# A2A agent

`cmd/a2a` serves a read-only [Agent2Agent](https://a2a-protocol.org) agent over JSON-RPC 2.0
([ADR 0005](/docs/adr/0005-read-only-mcp-server-and-a2a-agent)). A2A is the delegation channel between agents: it
answers from asrs-control's own state through its own use cases, and facts still travel only as CloudEvents on Kafka.

| | |
| --- | --- |
| Listen address | `A2A_ADDR`, default `:8093` |
| JSON-RPC endpoint | `/` and `/a2a` (`SendMessage`; header `A2A-Version` as the SDK sends it) |
| Agent Card | `GET /.well-known/agent-card.json`, advertising `A2A_AGENT_URL` (required, absolute http(s) URL) |
| Probe | `GET /healthz` |
| Needs | `DATABASE_URL`, `ASRS_LAYOUT_FILE`, `A2A_AGENT_URL`; no Kafka, no MQTT, no write seam, no auth |
| Deployed by | the chart behind `a2a.enabled` (default off), Service `<release>-a2a:8093`; `A2A_AGENT_URL` defaults to that Service's DNS name |

## Skills

A request is the first message part, as a data part (or a text part holding the same JSON):
`{"skill": "<id>", "arguments": {...}}`. Unknown argument names are refused.

| Skill | Tags | Arguments | Answer `data` |
| --- | --- | --- | --- |
| `job_status` | `asrs`, `storage-job`, `read`, `authority:L1` | `asrsId`, `storageJobId` | `storageJob`: id, kind, container, port, status, reason, plcReason, acceptedAt, endedAt |
| `asrs_capacity` | `asrs`, `capacity`, `read`, `authority:L1` | `asrsId` | `capacity`: units total/available, positions total/free, open jobs |

Every answer is ONE agent message with a data part (the envelope `{"skill", "data" | "problem"}`) and a text part (a
one-line summary), bound to the caller's contextId. Rejections are answered in-band as RFC 7807-shaped problems:
`asrs-not-found` and `storage-job-not-found` (404, the REST slugs), `invalid-argument`, `unknown-skill` and
`malformed-skill-request` (400), `internal-error` (500, the cause only in the logs). Each skill run is an
`a2a.skill <id>` span.

Source: `cmd/a2a/`, `internal/adapters/inbound/a2a/` (`server.go`, `skills.go`, `mapping.go`). Tests:
`internal/adapters/inbound/a2a/skills_test.go` (the real JSON-RPC wire over in-memory repositories),
`cmd/a2a/main_test.go` (router, card, boot config) and `cmd/a2a/main_integration_test.go` (Postgres-backed, writes
nothing); `TestA2AAdapterDependencyRule`, `TestA2ASkillsDeclareAuthority` and `TestAgentSurfacesAreReadOnly` guard the
shape.
