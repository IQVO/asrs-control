---
id: configuration
title: Configuration
sidebar_label: Configuration
sidebar_position: 1
description: "Every environment variable of cmd/api, the layout file format and the Helm values that set them."
---

# Configuration

`cmd/api/config.go` reads the environment once at boot. A missing required variable is a boot error naming it; an
unparsable or out-of-range number falls back to its default.

| Variable | Default | Meaning | Helm value |
| --- | --- | --- | --- |
| `DATABASE_URL` | required | Postgres DSN of the pool | `database.url` (rendered into a Secret) or `database.existingSecret` + `existingSecretKey` |
| `MIGRATIONS_DATABASE_URL` | `DATABASE_URL` | direct connection for golang-migrate (use it when the pool goes through a pooler) | `database.migrationsExistingSecretKey` (optional key of the same Secret) |
| `KAFKA_BROKERS` | required | comma-separated brokers | `kafka.brokers` |
| `MQTT_URL` | required | broker of the PLC link | `mqtt.url` |
| `ASRS_LAYOUT_FILE` | required | the layout YAML | `config.layoutFile` (the file comes from `layout:`) |
| `MQTT_CLIENT_ID` | `asrs-control` | client id prefix; each AS/RS connects as `<prefix>-<asrsId>` | `mqtt.clientId` |
| `ADDR` | `:8080` | HTTP listen address | `config.addr` |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` | `config.logLevel` |
| `OUTBOX_INTERVAL_MS` | `250` (min 1) | relay poll interval | `config.outboxIntervalMs` |
| `OUTBOX_BATCH` | `100` (min 1) | rows per relay pass | `config.outboxBatch` |
| `OUTBOX_MAX_ATTEMPTS` | `20` (min 0) | publish attempts per row before it is parked | `config.outboxMaxAttempts` |
| `VELOCITY_WINDOW_HOURS` | `168` (min 1) | window for counting a container's DONE retrieves | `config.velocityWindowHours` |
| `VELOCITY_FAST_RETRIEVALS` | `3` (min 1) | that many retrieves in the window make a container FAST | `config.velocityFastRetrievals` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | empty: telemetry off | Collector OTLP/gRPC endpoint, `host:port` or URL ([ADR 0004](/docs/adr/0004-adopt-the-fleet-opentelemetry-standard)) | `otel.endpoint`, rendered while `otel.enabled` |
| `OTEL_SERVICE_NAME` | `asrs-control` | `service.name` of spans and metrics | `otel.serviceName`, rendered while `otel.enabled` |
| `SERVICE_VERSION` | `dev` | `service.version` | `serviceVersion` (empty: `image.tag`, then `appVersion`) |
| `ENVIRONMENT` | `local` | `deployment.environment.name` (read by the telemetry adapter) | `environment` |

## The MCP server and the A2A agent

`cmd/mcp` and `cmd/a2a` ([ADR 0005](/docs/adr/0005-read-only-mcp-server-and-a2a-agent)) read `DATABASE_URL`,
`MIGRATIONS_DATABASE_URL`, `ASRS_LAYOUT_FILE`, `LOG_LEVEL` and the telemetry variables exactly as `cmd/api` does, and
nothing of Kafka or MQTT. The chart renders them from the same values; the components are off by default.

| Variable | Binary | Default | Meaning | Chart value |
| --- | --- | --- | --- | --- |
| `MCP_ADDR` | `cmd/mcp` | `:8090` | listen address | `mcp.httpAddr` (component `mcp.enabled`) |
| `A2A_ADDR` | `cmd/a2a` | `:8093` | listen address | `a2a.httpAddr` (component `a2a.enabled`) |
| `A2A_AGENT_URL` | `cmd/a2a` | required | absolute http(s) URL the Agent Card advertises | `a2a.agentUrl`, empty: `http://<release>-a2a.<namespace>.svc.cluster.local:8093/` |
| `OTEL_SERVICE_NAME` | both | `asrs-control-mcp` / `asrs-control-a2a` | `service.name` | `<otel.serviceName>-mcp` / `-a2a` |

## The layout file

[ADR 0002](/docs/adr/0002-telegram-conformance-and-aisle-scheduling) decision 7. One entry per AS/RS, with the emulator
world's field names; the emulator-only fields (`baseSeconds`, `perLevelSeconds`, `perSlotSeconds`, `nodeId`) are accepted
and ignored. `config/layout.example.yaml`:

```yaml
asrs:
  - id: ASRS1            # 1-32 of [A-Za-z0-9_-]: an MQTT topic and REST path segment
    aisles: 2
    levels: 8
    slots: 40
    doubleDeep: true     # depth 1 front, 2 back
    cost: {perLevel: 1.5, perSlot: 0.2}   # travel weights for position ranking (default 1 and 1)
    ports:
      - {id: IN1, aisle: 1, kind: INPUT}
      - {id: OUT1, aisle: 1, kind: OUTPUT}
      - {id: IN2, aisle: 2, kind: INPUT}
      - {id: OUT2, aisle: 2, kind: OUTPUT}
```

Boot refuses an invalid layout, a duplicate AS/RS id, and a layout that no longer contains the position of a container
stored in Postgres.

## Helm chart

`charts/asrs-control`: one Deployment (component `api`), a Service, a ConfigMap (the environment above plus
`layout.yaml`, mounted read-only at the directory of `config.layoutFile`), a Secret for `DATABASE_URL` when
`database.url` is set, and a ServiceAccount. The chart refuses to render without a database source, `kafka.brokers` or
`mqtt.url`, with `replicaCount` other than 1 (the single scheduler, ADR 0002), or with
`terminationGracePeriodSeconds` below 15. The rollout strategy is `Recreate`. `extraEnv` adds variables, except the four
observability names the chart renders itself (it refuses them).
`ci/default-values.yaml` and `ci/all-components-values.yaml` are the lint fixtures.
