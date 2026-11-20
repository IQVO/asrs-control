---
id: bounded-context-canvas
title: Bounded Context Canvas
sidebar_label: Bounded Context Canvas
sidebar_position: 2
description: "The ddd-crew Bounded Context Canvas of asrs-control."
---

# Bounded Context Canvas

Following the ddd-crew [Bounded Context Canvas](https://github.com/ddd-crew/bounded-context-canvas).

## Name and purpose

**asrs-control.** Store and retrieve containers in the automated storage and retrieval system at the throughput the WCS
needs, and own where each container sits inside the rack. To the WMS the AS/RS is one logical location.

## Strategic classification

| Domain | Business model | Evolution |
| --- | --- | --- |
| Generic | Compliance with the equipment (a controller every AS/RS needs) | Commodity: replaceable by a vendor controller |

## Domain roles

**Execution context** (it executes commands against equipment) and **gateway** (it translates between the fleet's
REST/Kafka world and the PLC's telegram protocol).

## Inbound communication

| Collaborator | Message | Channel |
| --- | --- | --- |
| material-flow-control | request a STORE or RETRIEVE, cancel a job, confirm a port pickup, read job and AS/RS state | REST (`apis/openapi.yaml`) |
| PLC (equipment-emulator) | ACK, JOB_DONE, JOB_FAILED, UNIT_STATE, PORT_ARRIVAL | MQTT `asrs/v1/<asrsId>/up` |

## Outbound communication

| Collaborator | Message | Channel |
| --- | --- | --- |
| PLC | STORE, RETRIEVE, MOVE, CANCEL, STATE_REQUEST, ACK | MQTT `asrs/v1/<asrsId>/down` |
| material-flow-control | ContainerStored, ContainerPresentedAtPort, StorageJobFailed, EquipmentStateChanged, AsrsCapacityChanged | Kafka `warehouse.asrs-control.events` |
| exception-management | StorageJobFailed (reason EQUIPMENT_FAULT opens a case) | Kafka `warehouse.asrs-control.events` |
| analytics (no consumer yet) | the same five events | Kafka `warehouse.asrs-control.analytics` |

## Ubiquitous language

AS/RS, Aisle, Level, Slot, Depth, RackPosition, Port, StorageMap, StorageJob, Reshuffle, Reservation, Velocity,
EquipmentUnit, PackML state, Presentation, Pickup, Telegram, Capacity. See [Ubiquitous language](/docs/ddd/ubiquitous-language).

## Business decisions

- Positions are reserved at acceptance; a full aisle refuses a store (`422 storage-full`).
- FAST containers go to the front and the cheapest positions, SLOW ones to the back and the dearest (double-deep).
- One job per aisle unit at a time; the queue is asrs-control's, not the PLC's.
- Retrieves to one port are presented in acceptance order; the next waits until the port is picked up.
- A reshuffle is chosen at dispatch, when the retrieve's container is blocked.
- HELD fails the aisle's queued jobs with EQUIPMENT_FAULT and refuses new ones.
- A command the PLC never acknowledges fails EQUIPMENT_UNREACHABLE; a refused one COMMAND_REFUSED.

## Assumptions and open questions

- Protocol v1 has no job-state query: a JOB_DONE the PLC gave up retransmitting while asrs-control was down longer than
  about 8 s leaves the job ACKNOWLEDGED ([ADR 0002](/docs/adr/0002-telegram-conformance-and-aisle-scheduling), known limits).
- A map/rack disagreement fails the job STORAGE_MAP_MISMATCH; the position is not quarantined.

## Verification metrics

Every accepted job ends in one outcome (`features/asrs_control.feature`); duplicates are applied once; mutation and
coverage gates on the domain and application layers ([Testing](/docs/development/testing)).
