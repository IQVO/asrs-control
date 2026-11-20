---
id: use-cases
title: Use cases
sidebar_label: Use cases
sidebar_position: 2
description: "Every use case of asrs-control's Service: who triggers it, what it checks, what it changes and what it publishes."
---

# Use cases

All use cases are methods of one `usecases.Service` (`internal/application/usecases`). Every write runs in one aisle
unit of work ([Architecture](/docs/overview/architecture#the-aisle-unit-of-work)) and ends with the scheduler.

## REST commands (material-flow-control)

| Use case | Route | Checks | Effect | Events |
| --- | --- | --- | --- | --- |
| `RequestStore` | `POST /asrs/{asrsId}/storage-jobs` kind STORE | known AS/RS, `container_id` given, `port_id` an INPUT port; the container is not in the AS/RS and has no open job; the aisle is not HELD; a position is assignable | reserves the best position for the container's velocity (D4) and queues a STORE | `AsrsCapacityChanged` when the snapshot moved |
| `RequestRetrieve` | same route, kind RETRIEVE | the container is stored here and has no open job; `port_id` (optional) an OUTPUT port of the container's aisle, else the aisle's first output port; the aisle is not HELD | marks the container leaving and queues a RETRIEVE | `AsrsCapacityChanged` |
| `Cancel` | `POST .../storage-jobs/{storageJobId}/cancellation` | an external job of this AS/RS that is still open and not AWAITING_ARRIVAL | QUEUED: CANCELLED at once (`200`); DISPATCHED/ACKNOWLEDGED: CANCEL telegram, CANCEL_REQUESTED (`202`) | `StorageJobFailed` reason CANCELLED when cancelled |
| `ConfirmPickup` | `POST /asrs/{asrsId}/ports/{portId}/pickups` | an OUTPUT port that holds exactly that container | frees the port; the next retrieve to it can be dispatched | none directly; the next dispatch may follow |

Refusals map to problem types with replay classes ([API overview](/docs/api-reference#errors)).

## Telegram handling (the PLC link)

| Use case | Trigger | Effect | Events |
| --- | --- | --- | --- |
| `HandleUp` JOB_DONE | the PLC finished a motion | store: position occupied, DONE; reshuffle: map moved, DONE, its retrieve can go; retrieve: the container leaves the map and is recorded on its output port, AWAITING_ARRIVAL. A JOB_DONE the map disagrees with fails the job STORAGE_MAP_MISMATCH; one for a job not with the PLC is ignored | `ContainerStored` (store only) |
| `HandleUp` PORT_ARRIVAL | a container reached an output port | the oldest retrieve awaiting that port is DONE | `ContainerPresentedAtPort` (scan READ, NO_READ or MISMATCH) |
| `HandleUp` JOB_FAILED | the PLC could not start the job | FAILED with the mapped reason, holds released; a failed reshuffle fails its retrieve with RESHUFFLE_FAILED | `StorageJobFailed` (external jobs) |
| `HandleUp` UNIT_STATE | the unit's PackML state | state recorded; HELD fails the aisle's QUEUED jobs with EQUIPMENT_FAULT | `EquipmentStateChanged` on a change |
| `HandleAck` | the PLC's ACK of a command or CANCEL | command accepted: ACKNOWLEDGED; refused: FAILED COMMAND_REFUSED; CANCEL accepted: CANCELLED; refused: reverted | `StorageJobFailed` on failure or cancel |
| `HandleExpired` | no ACK after 3 retransmits | an unacknowledged command fails EQUIPMENT_UNREACHABLE (also with a CANCEL pending); a CANCEL is reverted | `StorageJobFailed` |

Every up telegram is claimed with its ACK id in `processed_telegrams` inside the same transaction, so it is applied once.

## Boot

`Prepare` creates every aisle's map and unit that does not exist yet. `Recover` (per AS/RS, after the link subscribed)
resends every unacknowledged command with its original telegram id, every pending CANCEL, a STATE_REQUEST, and runs the
scheduler on every aisle.

## Queries

| Use case | Route | Answer |
| --- | --- | --- |
| `Asrs` | `GET /asrs/{asrsId}` | aisles (unit, state, busy, open jobs, positions total and free), ports (with the presented container), the recommended input port (least-loaded available aisle), capacity. No rack position. |
| `Job` | `GET /asrs/{asrsId}/storage-jobs/{storageJobId}` | an external job (a reshuffle is not readable) |
