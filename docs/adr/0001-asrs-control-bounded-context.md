# 1. asrs-control is a bounded context of the warehouse fleet

Status: Accepted (2026-11-20). Implements the WCS strategic design (warehouse-docs `docs/wcs/asrs-control.md`).

## Context

The fleet has no WCS. `fulfillment-execution` keeps an empty `EquipmentCommandPort` for it (its ADR-0015). The WCS
design splits the tier into `material-flow-control` (Supporting) over two subsystem controllers, `amr-fleet-control`
and `asrs-control` (both Generic), and builds asrs-control second because it is the smaller model and proves the
subsystem-controller interface (implementation handoff, build order step 2).

## Decision

Create `asrs-control`, Generic, CloudEvents segment `wes`, repository `IQVO/asrs-control`.

- Owns: rack positions per aisle (`StorageMap`), storage position assignment by velocity zone with double-deep
  depth rules, `StorageJob` scheduling and port presentation order, reshuffles, PackML equipment state
  (`EquipmentUnit`) and capacity facts. Invariants 1-6 of the context page are its guarantees.
- Does not own: container contents and the stock ledger (inventory-storage), which container moves and when
  (fulfillment-execution, wes-work-planning, through material-flow-control), transport beyond its ports
  (material-flow-control, amr-fleet-control), the AS/RS as one building location (facility-layout).

| Neighbour | Relationship | Edge |
| --- | --- | --- |
| material-flow-control | Customer/Supplier, asrs-control is the supplier of the subsystem-controller interface (relationship 5) | REST: request a store or retrieve job, cancel a job, confirm a port pickup, read job and AS/RS state. Kafka: the five facts of ADR 0003 |
| AS/RS PLC (emulated by IQVO/equipment-emulator) | Conformist to telegram protocol v1 (relationship 7) | MQTT `asrs/v1/<asrsId>/down` and `/up` (ADR 0002) |
| exception-management | Conformist on their side (relationship 8) | consumes `StorageJobFailed` (reason `EQUIPMENT_FAULT` opens a case) |

No call to any other context; no Kafka consumer. No rack position, reshuffle or internal move leaves the context
(seam decision 2, invariant 6). The layout of each AS/RS is configuration, not a sibling's data (ADR 0002).

Deferred: the fleet OpenTelemetry standard (outbound-shipping ADR 0008) is adopted in a follow-up; this version
logs JSON with `log/slog` only. Registration in warehouse-infra is a later plan.

## Consequences

- material-flow-control can be built against a real subsystem controller (plan 04).
- One more service and one more MQTT client of the broker. One replica only (the aisle scheduler is single-writer).
