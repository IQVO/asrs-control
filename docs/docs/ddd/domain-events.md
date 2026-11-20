---
id: domain-events
title: Domain events
sidebar_label: Domain events
sidebar_position: 10
description: "Every event asrs-control publishes, with its subject, fields and the rule that raises it."
---

# Domain events

Five CloudEvents types, all on both `warehouse.asrs-control.events` and `warehouse.asrs-control.analytics`
([ADR 0003](/docs/adr/0003-cloudevents-envelope-and-type-catalogue)). `data` is snake_case; `*_at` is asrs-control's
clock; `plc_timestamp` is the PLC telegram's (simulated) time, absent when the fact did not come from a telegram.

## ContainerStored

`com.warehouse.wes.asrs-control.storagejob.ContainerStored`, subject the storage job id. Raised by a STORE's JOB_DONE.

| Field | Type | Notes |
| --- | --- | --- |
| `storage_job_id`, `asrs_id`, `container_id` | string | |
| `port_id` | string | the input port |
| `reference` | string, optional | the caller's reference |
| `stored_at` | date-time | |
| `plc_timestamp` | date-time, optional | |

## ContainerPresentedAtPort

`com.warehouse.wes.asrs-control.storagejob.ContainerPresentedAtPort`, subject the storage job id. Raised by a RETRIEVE's
PORT_ARRIVAL; per port, in acceptance order.

| Field | Type | Notes |
| --- | --- | --- |
| `storage_job_id`, `asrs_id`, `container_id`, `port_id` | string | `container_id` is what the job retrieved |
| `reference` | string, optional | |
| `scan_result` | `READ`, `NO_READ`, `MISMATCH` | NO_READ and MISMATCH send the container to clarification (material-flow-control) |
| `scanned_container_id` | string, optional | what the port read; absent on NO_READ |
| `presented_at`, `plc_timestamp` | date-time | |

## StorageJobFailed

`com.warehouse.wes.asrs-control.storagejob.StorageJobFailed`, subject the storage job id. Raised for an external job
(STORE or RETRIEVE) that failed or was cancelled; a reshuffle's failure fails its retrieve with `RESHUFFLE_FAILED`.

| Field | Type | Notes |
| --- | --- | --- |
| `storage_job_id`, `asrs_id`, `container_id`, `port_id` | string | |
| `job_kind` | `STORE`, `RETRIEVE` | |
| `reference` | string, optional | |
| `reason` | enum | `CANCELLED`, `EQUIPMENT_FAULT` (HELD unit, or an unmapped PLC code), `EQUIPMENT_UNREACHABLE` (no ACK), `COMMAND_REFUSED` (refused ACK), `CONTAINER_NOT_AT_PORT`, `STORAGE_MAP_MISMATCH`, `PORT_OCCUPIED`, `RESHUFFLE_FAILED`, `NO_RESHUFFLE_POSITION` |
| `plc_reason` | string, optional | the PLC's bare code |
| `failed_at`, `plc_timestamp` | date-time | |

## EquipmentStateChanged

`com.warehouse.wes.asrs-control.equipmentunit.EquipmentStateChanged`, subject `<asrsId>/<unitId>`. Raised by a
UNIT_STATE that changes the unit's state.

| Field | Type |
| --- | --- |
| `asrs_id`, `unit_id` | string (`unit_id` = `A<aisle>`) |
| `aisle` | integer |
| `state`, `previous_state` | `IDLE`, `EXECUTE`, `HELD` |
| `changed_at`, `plc_timestamp` | date-time |

## AsrsCapacityChanged

`com.warehouse.wes.asrs-control.asrs.AsrsCapacityChanged`, subject the AS/RS id. Raised whenever the snapshot differs
from the last published one (or none was published yet).

| Field | Type |
| --- | --- |
| `asrs_id` | string |
| `units_total`, `units_available`, `positions_total`, `positions_free`, `open_jobs` | integer |
| `measured_at` | date-time |

The exact JSON, attribute by attribute, is pinned by `internal/adapters/outbound/events/golden_test.go`; the schemas and
examples are in `apis/asyncapi.yaml`.
