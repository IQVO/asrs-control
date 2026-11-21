---
id: tools
title: MCP tools
sidebar_label: MCP tools
sidebar_position: 1
description: "The five read-only MCP tools of asrs-control, each declaring authority L1, plus the storage-job resource template and the review prompt."
---

# MCP tools

`cmd/mcp` serves a read-only [Model Context Protocol](https://modelcontextprotocol.io) server over **Streamable HTTP
only**, stateless ([ADR 0005](/docs/adr/0005-read-only-mcp-server-and-a2a-agent)). It is unauthenticated like the rest
of the fleet: the in-cluster ClusterIP boundary is the access control.

| | |
| --- | --- |
| Listen address | `MCP_ADDR`, default `:8090` |
| Endpoint | `/mcp` (also mounted at `/`) |
| Probe | `GET /healthz` returns `200 {"status":"ok"}` |
| Needs | `DATABASE_URL` and `ASRS_LAYOUT_FILE`; no Kafka, no MQTT, no write seam |
| Deployed by | the chart behind `mcp.enabled` (default off), Service `<release>-mcp:8090` |
| Gateway | `/api/mcp/asrs-control` on Kong, rewritten to `/mcp` (`mcp.route.enabled`, default off) |

Source: `cmd/mcp/main.go`, `cmd/mcp/router.go`, `internal/adapters/inbound/mcp/` (`server.go`, `tools.go`,
`payloads.go`, `resources.go`, `prompts.go`, `errors.go`). The full advertised registry (descriptions, input and output
schemas) is pinned in `internal/adapters/inbound/mcp/testdata/tool_registry.golden.json`.

## Tools

Every tool carries `readOnlyHint: true`, `idempotentHint: true`, `openWorldHint: false` and
`_meta["com.warehouse/authority"] = "L1"`. Arguments are camelCase like the REST path parameters; results use the REST
response fields (snake_case). No rack position is ever returned.

| Tool | Arguments | Result |
| --- | --- | --- |
| `list_asrs` | none | `asrs_ids` of the layout file |
| `get_asrs_state` | `asrsId` | `aisles` (unit, state, busy, open jobs, positions), `ports` (with `presented_container_id`), `recommended_input_port_id`, `capacity` |
| `get_asrs_capacity` | `asrsId` | units total/available, positions total/free, open jobs |
| `get_storage_job` | `asrsId`, `storageJobId` | the job: kind, container, port, reference, status, reason, plc_reason, accepted_at, ended_at |
| `list_storage_jobs` | `asrsId`, optional `status`, `cursor`, `limit` (1..100, default 20) | `items` newest first and `nextCursor` (absent on the last page); reshuffles never listed |

Errors are `isError` results whose text starts with the REST slug: `asrs-not-found`, `storage-job-not-found`,
`invalid-parameter` (a malformed cursor, an unknown status, a limit out of range); anything else is a generic
`internal-error` (the cause is logged, never returned).

## Resource and prompt

- Resource template `warehouse://asrs-control/asrs/{asrsId}/storage-jobs/{storageJobId}`: the job JSON of
  `get_storage_job`, without a tool call.
- Prompt `review_storage_job` (`asrsId`, `storageJobId`): the operator SOP. It reads the job and its aisle, interprets
  the status and the failure reason, and says when to escalate to the WCS operator (a job ACKNOWLEDGED far longer than
  its aisle's others, a HELD unit, repeated failures with one reason).

## Guarantees and their tests

| Guarantee | Test |
| --- | --- |
| exactly these five tools, no write verb | `TestToolSurface`, `TestWriteVerbSensorFailsOnWriteNames` |
| read-only, closed-world, L1 | `TestEveryToolIsReadOnlyClosedWorldAndL1`, `TestEveryMCPToolDeclaresAuthority` |
| stateless (no session id) | `TestInitializeIssuesNoSessionID`, `TestToolsListOverHTTPWithoutASession` |
| a resource and a prompt | `TestMCPServerOffersResourceAndPrompt`, `surface_test.go` |
| adapter isolation and read-only wiring | `TestMCPAdapterDependencyRule`, `TestAgentSurfacesAreReadOnly` |
| Postgres-backed reads that write nothing | `cmd/mcp/main_integration_test.go` |
