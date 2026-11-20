---
id: events
title: Events and telegrams (AsyncAPI)
sidebar_label: Events and telegrams
sidebar_position: 3
description: "The AsyncAPI contract of asrs-control: the five CloudEvents on Kafka and the telegram protocol v1 channels to the PLC."
---

# Events and telegrams (AsyncAPI)

`apis/asyncapi.yaml` (AsyncAPI 2.6.0) holds both asynchronous interfaces. Operations are written from asrs-control's
side: `subscribe` = asrs-control publishes, `publish` = asrs-control receives. The spec is linted by Spectral
(`.spectral.asyncapi.yaml`) and every PR that changes it runs the `spec-breaking` gate (`scripts/harness/asyncapi_breaking.py`).
Raw spec: [apis/asyncapi.yaml](https://github.com/IQVO/asrs-control/blob/develop/apis/asyncapi.yaml).

## Kafka (server `production`)

| Channel | Operation | Messages |
| --- | --- | --- |
| `warehouse.asrs-control.events` | `receiveAsrsControlEvents` (asrs-control publishes) | the five types below, CloudEvents 1.0 structured mode |
| `warehouse.asrs-control.analytics` | `receiveAsrsControlAnalytics` (asrs-control publishes) | the same occurrences (same `id`, `type`, `data`), `dataschema` segment `analytics` |

| `type` | `subject` | Consumers |
| --- | --- | --- |
| `com.warehouse.wes.asrs-control.storagejob.ContainerStored` | storage job id | material-flow-control (designed) |
| `com.warehouse.wes.asrs-control.storagejob.ContainerPresentedAtPort` | storage job id | material-flow-control |
| `com.warehouse.wes.asrs-control.storagejob.StorageJobFailed` | storage job id | material-flow-control; exception-management (`EQUIPMENT_FAULT`) |
| `com.warehouse.wes.asrs-control.equipmentunit.EquipmentStateChanged` | `<asrsId>/<unitId>` | material-flow-control |
| `com.warehouse.wes.asrs-control.asrs.AsrsCapacityChanged` | AS/RS id | material-flow-control (capacity loop) |

Header `content-type: application/cloudevents+json; charset=UTF-8`, `source` `/warehouse/asrs-control`, `dataschema`
`urn:warehouse:asrs-control:<events|analytics>:<EventName>:v1`, Kafka key = `subject`. Fields: [Domain events](/docs/ddd/domain-events).
asrs-control consumes no Kafka topic.

## MQTT (server `plc-link`): telegram protocol v1

Flat JSON (`application/json`, not CloudEvents), QoS 1, not retained, timestamps `2006-01-02T15:04:05.000Z`. The
equipment-emulator's `apis/asyncapi.yaml` describes the same channels from the PLC's side.

| Channel | Operation | Telegrams |
| --- | --- | --- |
| `asrs/v1/{asrsId}/down` | `sendDownTelegram` (asrs-control publishes) | STORE, RETRIEVE, MOVE (reshuffle), CANCEL, STATE_REQUEST, ACK |
| `asrs/v1/{asrsId}/up` | `receiveUpTelegram` (asrs-control receives) | ACK, JOB_DONE, JOB_FAILED, UNIT_STATE, PORT_ARRIVAL |

| Rule | Behaviour |
| --- | --- |
| Handshake | Every non-ACK telegram is ACKed. asrs-control ACKs an up telegram only after its effect committed; one it cannot decode but that carries a `telegramId` is ACKed `accepted: false`, `reason: MALFORMED`. |
| Retransmit | An unacknowledged command or STATE_REQUEST is resent with the same `telegramId` after 2 s real time, at most 3 times; then a command fails its job `EQUIPMENT_UNREACHABLE` and a CANCEL is reverted. |
| Dedup | Up telegrams are claimed in `processed_telegrams` with the ACK id; a duplicate gets the identical ACK and changes nothing, also after a restart. |
| Ordering | paho with ordered delivery, one worker per link. |
| NOT_READY | A transient refusal: the telegram is tracked again and resent, never a job outcome. |
| Refusal | Any other refused ACK of a command fails the job `COMMAND_REFUSED` with the code as `plc_reason`. |
| JOB_FAILED codes | `CONTAINER_NOT_AT_PORT`, `PORT_OCCUPIED` keep their name; `CONTAINER_NOT_AT_POSITION`, `POSITION_OCCUPIED`, `POSITION_BLOCKED` become `STORAGE_MAP_MISMATCH`; anything else `EQUIPMENT_FAULT`. |
| One job per aisle | The next command of an aisle goes only after the previous one's JOB_DONE or JOB_FAILED. |

Rack positions travel only on this link; they never reach REST or Kafka (invariant 6).
