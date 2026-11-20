# 3. CloudEvents 1.0 as the mandatory event envelope, and the type catalogue

Status: Accepted (2026-11-20). Must exist before `apis/asyncapi.yaml`: `TestEventCatalogueMatchesContract`
fails a contract type this catalogue does not list.

## Context

Every Kafka message in the fleet is a CloudEvents 1.0 event in structured mode (warehouse-docs
`docs/strategic-design/event-standard-cloudevents.md`, fleet rule `.claude/rules/fleet/cloudevents.md`).
The WCS contexts publish with the `wes` segment on their own topics, each with an `.analytics` topic (WCS seam
decision 5).

## Decision

- `specversion` `1.0`; `id` a UUID v4 minted once per occurrence and persisted with both outbox rows; `source`
  `/warehouse/asrs-control`; `subject` the aggregate instance (storage job id, `<asrsId>/<unitId>`, or the AS/RS
  id), also the Kafka key; `time` asrs-control's clock when the fact was recorded, UTC; `datacontenttype`
  `application/json`; `dataschema` `urn:warehouse:asrs-control:<events|analytics>:<EventName>:v1`.
- Header `content-type: application/cloudevents+json; charset=UTF-8`. Built only through
  `internal/adapters/kafka/cloudevents`. Published only through the transactional outbox.
- Every event goes to `warehouse.asrs-control.events` AND `warehouse.asrs-control.analytics` (same `id`, same
  `type`, same `data`). The analytics stream has no consumer yet; it is the fleet's analytics data product slot.
- `data` is snake_case. `*_at` fields are asrs-control's clock. `plc_timestamp` is the PLC telegram's timestamp,
  which is simulated time in the emulator (ahead of wall time at `SIM_SPEED` > 1); it is omitted when the fact did
  not come from a telegram. No rack position, reshuffle or internal move is ever published (invariant 6, seam
  decision 2).

### Published types

| `type` | `subject` | consumers |
| --- | --- | --- |
| `com.warehouse.wes.asrs-control.storagejob.ContainerStored` | storage job id | material-flow-control (designed, plan 04) |
| `com.warehouse.wes.asrs-control.storagejob.ContainerPresentedAtPort` | storage job id | material-flow-control |
| `com.warehouse.wes.asrs-control.storagejob.StorageJobFailed` | storage job id | material-flow-control; exception-management (reason `EQUIPMENT_FAULT`) |
| `com.warehouse.wes.asrs-control.equipmentunit.EquipmentStateChanged` | `<asrsId>/<unitId>` | material-flow-control |
| `com.warehouse.wes.asrs-control.asrs.AsrsCapacityChanged` | AS/RS id | material-flow-control (capacity loop) |

`StorageJobFailed.reason` is one of `CANCELLED`, `EQUIPMENT_FAULT`, `EQUIPMENT_UNREACHABLE`, `COMMAND_REFUSED`,
`CONTAINER_NOT_AT_PORT`, `STORAGE_MAP_MISMATCH`, `PORT_OCCUPIED`, `RESHUFFLE_FAILED`, `NO_RESHUFFLE_POSITION`;
`plc_reason` carries the PLC's bare code when there is one.

### Consumed types

None.

## Consequences

- The warehouse-docs Event Standard's subdomain table gains the row `asrs-control | wes | asrs-control |
  /warehouse/asrs-control` when the repository lands on `develop` (a warehouse-docs change, out of this plan).
