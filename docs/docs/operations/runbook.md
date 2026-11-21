---
id: runbook
title: Runbook
sidebar_label: Runbook
sidebar_position: 2
description: "Deploying, checking and operating asrs-control: probes, the PLC link, the outbox, stuck jobs and restarts."
---

# Runbook

## Deploy

```bash
helm upgrade --install asrs-control charts/asrs-control -n warehouse-systems \
  --set database.existingSecret=asrs-control-db --set kafka.brokers=kafka:9092 \
  --set image.tag=<version>
```

Exactly one replica, `Recreate`: a rollout stops the old pod before the new one starts, so two schedulers never drive one
AS/RS. Expect a short gap in which the PLC retransmits; boot recovery resends what was in flight.

asrs-control is not registered in warehouse-infra yet and has no release (no `main` branch): the commands above are
for a manual install.

## Health

| Check | Meaning |
| --- | --- |
| `GET /healthz` = 200 | the process serves HTTP |
| `GET /readyz` = 200 | not draining, and Postgres (ping), Kafka (TCP dial of the first broker) and every MQTT link are up; checks are cached for 3 s |
| `GET /readyz` = 503 | draining after SIGTERM, or a dependency is down; the body names the failing dependency |
| `GET /asrs/<id>` | per aisle: unit state, busy, open jobs, free positions; ports with the presented container; capacity |

## Common operations

### A job seems stuck

1. `GET /asrs/<asrsId>/storage-jobs/<id>`: note `status`.
2. QUEUED: the aisle's unit is busy with an earlier job, or (retrieve) its output port still holds a container whose pickup
   was never confirmed. `GET /asrs/<asrsId>` shows `busy` and `presented_container_id`. Confirm the pickup if the
   container was taken: `POST /asrs/<asrsId>/ports/<portId>/pickups`.
3. DISPATCHED for more than about 8 s: the PLC never acknowledged; the retransmitter will fail it
   `EQUIPMENT_UNREACHABLE`. Check the MQTT link (`/readyz`) and the PLC.
4. ACKNOWLEDGED for a long time: the PLC is executing, or its JOB_DONE was lost while asrs-control was down longer than
   the PLC's retransmit window (known limit of protocol v1, ADR 0002). Check the PLC's own state (with the emulator:
   `GET /asrs/<asrsId>` on its admin API). Cancel the job if the PLC no longer holds it.
5. CANCEL_REQUESTED: the PLC's answer to the CANCEL decides; an unanswered CANCEL is reverted after its retransmits.

### An aisle refuses work (`422 aisle-unavailable`)

The unit reported HELD. Its queued jobs already failed with `EQUIPMENT_FAULT` (exception-management opens cases). Clear
the fault at the PLC; the next UNIT_STATE (IDLE or EXECUTE) makes the aisle available again.

### `422 storage-full`

The aisle of the requested input port has no assignable position (occupied or reserved, depth rules included). Use the
`recommended_input_port_id` of `GET /asrs/<asrsId>`.

### Events are not arriving

Rows wait in `outbox_events` until published:

```sql
SELECT count(*), max(attempts), min(created_at) FROM outbox_events WHERE published_at IS NULL;
SELECT id, event_type, attempts, last_error FROM outbox_events WHERE published_at IS NULL ORDER BY id LIMIT 20;
```

A row that reached `OUTBOX_MAX_ATTEMPTS` is no longer retried; fix the cause, then reset its `attempts`.

### Restart

Safe at any time: up telegrams are deduplicated through `processed_telegrams`, down commands are resent with their
original telegram ids by boot recovery, and the idempotency store replays answered POSTs for 24 hours.

## Changing the layout

Edit `layout:` in the values (or the file), then roll out. Boot refuses a layout that drops the position of a stored
container: retrieve those containers first.

## Data

`processed_telegrams` and `idempotency_keys` are purged hourly (24 h retention). The storage map is the source of truth
for rack contents; never edit `storage_maps.snapshot` while the service runs.
