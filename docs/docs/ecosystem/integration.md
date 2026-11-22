---
id: integration
title: Integration
sidebar_label: Integration
sidebar_position: 1
description: "How asrs-control fits the warehouse-systems fleet: neighbours, topics, the broker, the emulator and what is not wired yet."
---

# Integration

| Neighbour | Direction | Contract | State on `develop` |
| --- | --- | --- | --- |
| material-flow-control | calls asrs-control's REST; consumes all five events | `apis/openapi.yaml`, `apis/asyncapi.yaml` | built (material-flow-control v0.1.0; its asyncapi declares all five types) |
| AS/RS PLC | MQTT both ways | telegram protocol v1 (`asrs/v1/<asrsId>/down`, `/up`) | equipment-emulator is built; `cmd/api/e2e_emulator_integration_test.go` runs asrs-control against its image |
| exception-management | consumes `StorageJobFailed` (`EQUIPMENT_FAULT`) | `apis/asyncapi.yaml` | published; not consumed yet |
| analytics | `warehouse.asrs-control.analytics` | same five events | no consumer yet (the data product slot) |

asrs-control calls no sibling service and consumes no Kafka topic. It never sees SKUs, quantities or the building
location of the AS/RS.

## Infrastructure

| Dependency | How it is reached | Ownership |
| --- | --- | --- |
| Postgres | `DATABASE_URL` (pooled), `MIGRATIONS_DATABASE_URL` (direct, for golang-migrate) | owned by the context |
| Kafka | `KAFKA_BROKERS`; topics `warehouse.asrs-control.events`, `warehouse.asrs-control.analytics` | the fleet's single broker |
| MQTT | `MQTT_URL`, client id `<MQTT_CLIENT_ID>-<asrsId>` | the fleet's Mosquitto (the chart defaults to `mosquitto.warehouse-systems.svc.cluster.local:1883`) |
| Layout | `ASRS_LAYOUT_FILE` (the chart renders `values.yaml` `layout:` into the ConfigMap) | configuration |

## The event standard

asrs-control's row of the warehouse-docs Event Standard subdomain table is `asrs-control | wes | asrs-control |
/warehouse/asrs-control` ([ADR 0003](/docs/adr/0003-cloudevents-envelope-and-type-catalogue)).

## Not wired yet

- Registration in warehouse-infra (Terraform apps, the `helm-values/asrs-control.yaml` that switches on the Gateway
  routes, the KongPlugins and the mcp/a2a components) and a release: no `main` branch yet. The chart side exists, off by
  default: `/api/asrs-control`, `/api/mcp/asrs-control` and `/api/a2a/asrs-control`
  ([Configuration](/docs/operations/configuration#gateway-kong), [MCP tools](/docs/mcp/tools),
  [A2A agent](/docs/a2a/agent)).
