---
id: ubiquitous-language
title: Ubiquitous language
sidebar_label: Ubiquitous language
sidebar_position: 1
description: "The terms of the asrs-control model and the Go names that carry them."
---

# Ubiquitous language

The terms are the context page's ([asrs-control in the WCS design](https://iqvo.github.io/warehouse-docs/wcs/asrs-control), warehouse-docs); the code uses them as written.
`.claude/rules/domain-model.md` is the same list for agents.

| Term | Meaning | In code |
| --- | --- | --- |
| **AS/RS** | One automated storage and retrieval system, as the layout file describes it. To the WMS it is one location. | `layout.Layout` (`id`, e.g. `ASRS1`) |
| **Aisle**, **Level**, **Slot**, **Depth** | The rack structure. Depth 1 is the front, 2 the back of a double-deep rack. | `layout.Position{Aisle, Level, Slot, Depth}` |
| **RackPosition** | One storage position for one container, inside this context only. Not a `LocationCode`, never published. | `layout.Position` (`A1-L2-S10-D1`) |
| **Port** | An INPUT or OUTPUT point of one aisle, the handoff point with material-flow-control. | `layout.Port`, `layout.PortKind` |
| **StorageMap** | Which container is in which position of one aisle, the reservations, the containers leaving or moving, and the container waiting on each output port. | `storagemap.Map` |
| **StorageJob** | One STORE or RETRIEVE requested by material-flow-control, or one internal RESHUFFLE. | `storagejob.Job`, `storagejob.Kind` |
| **Reshuffle** | An internal MOVE of a front container that blocks the back position a retrieve needs. Invisible outside. | `storagejob.Reshuffle`, `storagemap.PlanReshuffle` |
| **Reservation** | A position held for a store from acceptance to its outcome, so it is never assigned twice. | `storagemap.ReserveStore` |
| **Velocity** | FAST or SLOW, from this context's own count of DONE retrieves of the container in a window. | `storagemap.Velocity`, `usecases.VelocityPolicy` |
| **Depth rules D1, D2** | D1: a back position is assignable only while its front is clear. D2: a front is not assigned over a back that a store is on its way to, or whose container is leaving or moving. | `storagemap.assignable` |
| **EquipmentUnit** | The combined shuttle-and-lift unit of one aisle, unit id `A<aisle>`, with its PackML state. | `equipment.Unit` |
| **PackML state** | IDLE, EXECUTE or HELD (protocol v1 sends no other). HELD makes an aisle unavailable. | `equipment.State` |
| **Presentation** | A retrieved container arriving at its output port, read by the port's scanner (READ, NO_READ, MISMATCH). | `storagejob.Present`, `ScanResult` |
| **Pickup** | material-flow-control taking a presented container off an output port; the port is then free. | `usecases.Service.ConfirmPickup` |
| **Telegram** | A message to or from the PLC with a handshake: ACK, 2 s retransmit, dedup by `telegramId`. | `internal/adapters/telegram` |
| **Capacity** | Units available, positions total and free, open jobs of one AS/RS. | `capacity.Snapshot` |

## Job statuses

| Status | Meaning |
| --- | --- |
| `QUEUED` | Accepted, waiting for its aisle's unit |
| `DISPATCHED` | Its command telegram went to the PLC |
| `ACKNOWLEDGED` | The PLC accepted the command |
| `CANCEL_REQUESTED` | A CANCEL telegram went to the PLC; its answer decides |
| `AWAITING_ARRIVAL` | A retrieve whose motion is done, waiting for PORT_ARRIVAL |
| `DONE`, `FAILED`, `CANCELLED` | The one outcome (invariant 5) |

## Failure reasons

`CANCELLED`, `EQUIPMENT_FAULT`, `EQUIPMENT_UNREACHABLE`, `COMMAND_REFUSED`, `CONTAINER_NOT_AT_PORT`,
`STORAGE_MAP_MISMATCH`, `PORT_OCCUPIED`, `RESHUFFLE_FAILED`, `NO_RESHUFFLE_POSITION` (`storagejob.Reason`). The PLC's
bare code is kept as `plc_reason`.
