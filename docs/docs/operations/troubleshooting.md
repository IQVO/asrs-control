---
id: troubleshooting
title: Troubleshooting
sidebar_label: Troubleshooting
sidebar_position: 4
description: "Boot errors, refusals and PLC-link symptoms of asrs-control and what to do about them."
---

# Troubleshooting

| Symptom | Cause | Fix |
| --- | --- | --- |
| Boot: `DATABASE_URL is required: asrs-control runs on real Postgres, Kafka and MQTT with a layout file only` (or the same for `KAFKA_BROKERS`, `MQTT_URL`, `ASRS_LAYOUT_FILE`) | a required variable is empty | set it; there is no in-memory mode at runtime |
| Boot: `invalid AS/RS layout ...` | the layout file is invalid (ids, aisles, ports per aisle) | fix the file; every aisle needs its ports, ids match `[A-Za-z0-9_-]{1,32}` |
| Boot: `migrations: ...` | the migration connection failed or the schema is dirty | check `MIGRATIONS_DATABASE_URL`; a direct (not pooled) connection is needed |
| Boot fails after a layout change | the new layout lost the position of a stored container | restore the layout or retrieve the containers first |
| Helm: `replicaCount must be 1` | more than one replica requested | keep one; the scheduler is single-writer |
| `/readyz` 503 naming `mqtt` | the MQTT link is disconnected | check the broker and `MQTT_URL`; paho reconnects and resubscribes on its own |
| `422 wrong-port-kind` | a STORE to an OUTPUT port, or a RETRIEVE/pickup at an INPUT port | use the right port kind |
| `422 port-unreachable` | the retrieve's output port is not in the container's aisle | omit `port_id` (the aisle's first output port is used) or pick a port of that aisle |
| `422 container-already-stored` / `container-busy` | the container is in the rack, or has an open job | read its job; wait or cancel it |
| `409 port-not-presenting` on a pickup | the port does not hold that container (yet) | wait for `ContainerPresentedAtPort`; check the container id |
| `409 job-not-cancellable` | a retrieve on its way to the port (AWAITING_ARRIVAL) | wait for the presentation |
| Jobs fail `COMMAND_REFUSED` | the PLC refused the command; `plc_reason` has its code (for example `UNKNOWN_PORT` when the PLC's world and the layout differ) | align the PLC configuration and the layout file |
| Jobs fail `STORAGE_MAP_MISMATCH` | the PLC's rack and the storage map disagree | reconcile the rack; the position is not quarantined automatically |
| Retrieves wait forever at one port | a presented container's pickup was never confirmed | `POST /asrs/<id>/ports/<port>/pickups` |
| `Idempotency-Replayed: true` and an old answer | the same `Idempotency-Key` was reused within 24 h | use a new key per intended operation |
