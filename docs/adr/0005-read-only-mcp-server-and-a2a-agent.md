# 5. A read-only MCP server and a read-only A2A agent

Status: Accepted (2026-11-20). Applies the fleet's agentic standard (Agentic Standard v2: authority levels on every
MCP tool and A2A skill, stateless MCP, a resource and a prompt per server; the MCP and A2A inbound-adapter patterns of
the cohort repos, customer-returns ADRs 0006, 0009 and 0011) to asrs-control.

## Context

asrs-control answered only REST (material-flow-control) and MQTT (the PLC). An agent such as warehouse-ops-agent
had no way to ask where a storage job stands or how full an AS/RS is, except by calling the REST interface that
material-flow-control depends on. The fleet gives every backend context two more inbound adapters over the same use
cases: an MCP server (port 8090) and an A2A agent (port 8093), both unauthenticated (fleet decision 2026-09-11: the
in-cluster ClusterIP boundary is the access control).

## Decision

**Both surfaces are READ-ONLY.** Requesting a store or a retrieve, cancelling a job and confirming a pickup stay on REST:
they need the `Idempotency-Key`, the aisle unit of work, the PLC downlink after commit and the outbox, and they are
material-flow-control's interface (WCS context map relationship 5). An agent proposing a job would compete with the
material-flow scheduler, which no ADR here allows. Every tool and skill is therefore authority **L1** (reads facts,
changes nothing).

**MCP server** (`cmd/mcp`, `internal/adapters/inbound/mcp`): the official MCP Go SDK, Streamable HTTP only, stateless
(`StreamableHTTPOptions{Stateless: true}`: no `Mcp-Session-Id`), at `/mcp` and `/`, `GET /healthz` open.

| Tool | Reads |
| --- | --- |
| `list_asrs` | the AS/RS ids of the layout file |
| `get_asrs_state` | aisles (unit state, busy, open jobs, free positions), ports with the presented container, the recommended input port, capacity |
| `get_asrs_capacity` | the capacity snapshot (what `AsrsCapacityChanged` publishes) |
| `get_storage_job` | one STORE or RETRIEVE job (status, reason, PLC reason, times) |
| `list_storage_jobs` | an AS/RS's jobs, newest first, optional status filter, keyset-paged by the job sequence (`cursor`/`nextCursor`, 1..100, default 20) |

Each tool is annotated `readOnlyHint`, `idempotentHint`, `openWorldHint: false` and declares
`_meta["com.warehouse/authority"] = "L1"`. One resource template,
`warehouse://asrs-control/asrs/{asrsId}/storage-jobs/{storageJobId}`, and one prompt, `review_storage_job` (the operator
SOP: read the job and its aisle, interpret status and reason, escalate stuck or repeated failures). Tool results carry the
REST response shapes (snake_case); errors carry the REST slugs (`asrs-not-found`, `storage-job-not-found`,
`invalid-parameter`) as `isError` results. The registry is pinned by `testdata/tool_registry.golden.json`.

**A2A agent** (`cmd/a2a`, `internal/adapters/inbound/a2a`): a2a-go v2, JSON-RPC 2.0 at `/` and `/a2a`, the Agent Card at
`/.well-known/agent-card.json` advertising `A2A_AGENT_URL`, a message-only agent (no task lifecycle), no security scheme.
Two goal-shaped skills tagged `read` and `authority:L1`: `job_status` (`asrsId`, `storageJobId`) and `asrs_capacity`
(`asrsId`). Arguments are decoded strictly (unknown fields refused); rejections are answered in-band as RFC 7807-shaped
problems with the REST slugs, never as transport errors. A2A is delegation only: facts still travel as CloudEvents on
Kafka (ADR 0003), and the agent contains no outbound A2A client.

**Hexagonal wiring.** Both adapters depend only on the application and domain layers and are imported by nothing but
their `cmd/` (`TestMCPAdapterDependencyRule`, `TestA2AAdapterDependencyRule`). Each holds a `Reads` interface over
`*usecases.Service`, whose method set is pinned by `TestAgentSurfacesAreReadOnly`, an allowlist sensor over the adapter and
its composition root: only read use cases and the read repositories may be referenced, and no Kafka, outbox, event sink,
MQTT link, telegram codec, memory double, sibling inbound adapter or A2A client may be imported. Since the fleet audit
of 2026-11-21 the sensor resolves each qualifier through the file's imports (an alias cannot launder a write seam, and
a dot or aliased import of `usecases`, `ports` or `postgres` is refused), refuses a write method or raw-SQL call on ANY
value (repository and pool writes, `Query`/`QueryRow`/`Exec`/`Begin`/`Acquire`, and the write use cases of
`*usecases.Service`) and refuses a raw driver import (`jackc/pgx`, `database/sql`):
`agent_readonly_methods_fitness_test.go`, bitten by `agent_readonly_hardening_test.go`. The application gains
three reads for these surfaces: `ListJobs` (over the new `StorageJobs.ListExternal`, external jobs only, newest first),
`AsrsIDs` and `Capacity`. The composition roots load the same layout file as `cmd/api`, run the idempotent migrations
and wire the use cases with the Postgres map, job and unit repositories only: no unit of work, outbox, inbox, downlink or
id generator, so neither binary can write, and neither talks to the PLC.

**Telemetry and packaging.** Both binaries follow ADR 0004 (`OTEL_SERVICE_NAME` defaults `asrs-control-mcp` and
`asrs-control-a2a`; the A2A skills open `a2a.skill <id>` spans). The Dockerfile already builds every `cmd/*`; the
chart gains `mcp.*` and `a2a.*` (Deployments running `/app/mcp` and `/app/a2a`, Services on 8090 and 8093, both
`enabled: false` by default; warehouse-infra switches them on). The gateway routes (`/api/mcp/asrs-control`,
`/api/a2a/asrs-control`) are a separate chart change.

## Consequences

- An agent can read every AS/RS, its capacity and its jobs without touching material-flow-control's REST interface.
- Adding a write tool or skill is a new ADR here: it must be at least L3, go through the same use case as REST and its
  unit of work, and the read-only sensors and the tool golden fail until it is deliberately admitted.
- `list_storage_jobs` reads `storage_jobs` by `asrs_id` ordered by `seq` without a dedicated index; the table is bounded
  by the throughput of a few aisles, and an index is a migration to add when it is not.
