---
id: aggregate-design-canvas
title: Aggregate Design Canvas
sidebar_label: Aggregate Design Canvas
sidebar_position: 4
description: "The three aggregates of asrs-control on the ddd-crew Aggregate Design Canvas."
---

# Aggregate Design Canvas

Following the ddd-crew [Aggregate Design Canvas](https://github.com/ddd-crew/aggregate-design-canvas). The three
aggregates of one aisle are always changed together in one aisle unit of work, under the aisle's row locks.

## StorageMap (`internal/domain/storagemap`, one per aisle)

| | |
| --- | --- |
| Responsibility | Which container is in which position; reservations; containers leaving (retrieve) or moving (reshuffle); the container on each output port |
| Invariants | 1 (one container per position, one position per container), 2 (a reserved position is never assigned), 6 (internal moves change only the map); depth rules D1 and D2 |
| Commands | `ReserveStore`, `ReserveRetrieve`, `PlanReshuffle`, `CompleteStore`, `CompleteRetrieve`, `CompleteMove`, `Pickup`, `Release*`, `Assign` |
| Created by | `Prepare` at boot (empty), rehydrated from the `storage_maps.snapshot` JSONB |
| Events | none (it publishes nothing by design) |
| Throughput | every job of the aisle; the row lock serialises them |
| Size | levels × slots × depth positions (1280 for the example layout's two aisles together) |

## StorageJob (`internal/domain/storagejob`)

| | |
| --- | --- |
| Responsibility | One STORE, RETRIEVE or RESHUFFLE, its status and its one outcome |
| Invariants | 4 with the scheduler (port order by `Seq`), 5 (exactly one outcome), only external jobs publish |
| Commands | `NewStore`, `NewRetrieve`, `NewReshuffle`, `Locate`, `Dispatch`, `Acknowledge`, `CompleteMotion`, `Present`, `Fail`, `RequestCancel`, `ConfirmCancel`, `RevertCancel` |
| Events | `ContainerStored`, `ContainerPresentedAtPort`, `StorageJobFailed` |
| States | QUEUED → DISPATCHED → ACKNOWLEDGED → DONE (a retrieve via AWAITING_ARRIVAL); CANCEL_REQUESTED → CANCELLED or back; any open → FAILED |

## EquipmentUnit (`internal/domain/equipment`)

| | |
| --- | --- |
| Responsibility | The aisle's shuttle-and-lift unit: its PackML state and its one current job |
| Invariants | 3 (one job at a time); HELD means not available |
| Commands | `Assign`, `Release`, `Report` |
| Events | `EquipmentStateChanged` |

`capacity.Snapshot` is a derived value (no identity of its own beyond the AS/RS); `AsrsCapacityChanged` is raised when it
moves. `scheduling.Next` is a pure domain service over the three aggregates.
