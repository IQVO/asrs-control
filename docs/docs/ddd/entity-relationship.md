---
id: entity-relationship
title: Entity relationship
sidebar_label: Entity relationship
sidebar_position: 8
description: "The Postgres schema of asrs-control (migration 0001)."
---

# Entity relationship

One migration, `migrations/0001_asrs_control_schema.up.sql`, run at boot by golang-migrate.

```mermaid
erDiagram
  storage_maps {
    text asrs_id PK
    int aisle PK
    jsonb snapshot
    text_array containers
    timestamptz updated_at
  }
  storage_jobs {
    text job_id PK
    bigserial seq UK
    text asrs_id
    int aisle
    text kind
    text container_id
    text port_id
    text reference
    text parent_job_id
    int_array from_pos
    int_array to_pos
    text status
    text reason
    text plc_reason
    text telegram_id
    text cancel_telegram_id
    bool acked
    timestamptz accepted_at
    timestamptz ended_at
  }
  equipment_units {
    text asrs_id PK
    int aisle PK
    text unit_id
    text state
    text current_job
    timestamptz since
  }
  asrs_capacity {
    text asrs_id PK
    int units_total
    int units_available
    int positions_total
    int positions_free
    int open_jobs
  }
  processed_telegrams {
    text asrs_id PK
    text telegram_id PK
    text ack_telegram_id
    timestamptz processed_at
  }
  outbox_events {
    bigserial id PK
    text event_id
    text topic
    text event_type
    text subject
    bytea value
    jsonb headers
    timestamptz published_at
    int attempts
  }
  idempotency_keys {
    text key PK
    text fingerprint
    int status
    bytea body
    timestamptz created_at
  }
  storage_maps ||--o{ storage_jobs : "aisle's jobs"
  equipment_units ||--o| storage_jobs : "current_job"
  storage_jobs ||--o{ storage_jobs : "parent_job_id (reshuffle)"
```

| Table | Notes |
| --- | --- |
| `storage_maps` | One row per (AS/RS, aisle); the map is a JSONB snapshot, `containers` a GIN-indexed array for "which aisle holds C". The row is the aisle lock. |
| `storage_jobs` | `seq` is the acceptance order the scheduler trusts. `uq_storage_jobs_open_external` allows one open STORE/RETRIEVE per (AS/RS, container); it closes the race between aisles. |
| `equipment_units` | One per aisle, locked after the map. |
| `asrs_capacity` | The last published snapshot; its row lock serialises capacity changes across aisles. |
| `processed_telegrams` | The up-telegram inbox: (AS/RS, telegram id) → the ACK id sent. Purged after 24 h. |
| `outbox_events` | Two rows per event (events and analytics), same `event_id`. |
| `idempotency_keys` | Stored 2xx and FINAL answers, 24 h window, purged hourly. |
