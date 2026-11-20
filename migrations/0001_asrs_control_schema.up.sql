-- storage_maps: one StorageMap aggregate per (AS/RS, aisle). snapshot is the aggregate's state
-- (occupied, reserved, reserved_for, retrieving, moving, presented); containers lists the
-- containers in the rack for Find. The row is the aisle lock: every write takes
-- SELECT ... FOR UPDATE (ADR 0002).
CREATE TABLE storage_maps (
    asrs_id    TEXT COLLATE "C" NOT NULL,
    aisle      INTEGER     NOT NULL CHECK (aisle >= 1),
    snapshot   JSONB       NOT NULL,
    containers TEXT[]      NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (asrs_id, aisle)
);
CREATE INDEX idx_storage_maps_containers ON storage_maps USING GIN (containers);

-- storage_jobs: the StorageJob aggregate. seq is the acceptance order (invariant 4).
-- Positions are 4-element arrays {aisle, level, slot, depth}, NULL when none.
CREATE TABLE storage_jobs (
    job_id             TEXT COLLATE "C" PRIMARY KEY,
    seq                BIGSERIAL   NOT NULL UNIQUE,
    asrs_id            TEXT COLLATE "C" NOT NULL,
    aisle              INTEGER     NOT NULL CHECK (aisle >= 1),
    kind               TEXT        NOT NULL CHECK (kind IN ('STORE', 'RETRIEVE', 'RESHUFFLE')),
    container_id       TEXT COLLATE "C" NOT NULL,
    port_id            TEXT COLLATE "C" NOT NULL DEFAULT '',
    reference          TEXT        NOT NULL DEFAULT '',
    parent_job_id      TEXT COLLATE "C" NOT NULL DEFAULT '',
    from_pos           INTEGER[]   CHECK (from_pos IS NULL OR cardinality(from_pos) = 4),
    to_pos             INTEGER[]   CHECK (to_pos IS NULL OR cardinality(to_pos) = 4),
    status             TEXT        NOT NULL CHECK (status IN ('QUEUED', 'DISPATCHED', 'ACKNOWLEDGED', 'CANCEL_REQUESTED', 'AWAITING_ARRIVAL', 'DONE', 'FAILED', 'CANCELLED')),
    reason             TEXT        NOT NULL DEFAULT '',
    plc_reason         TEXT        NOT NULL DEFAULT '',
    telegram_id        TEXT COLLATE "C" NOT NULL DEFAULT '',
    cancel_telegram_id TEXT COLLATE "C" NOT NULL DEFAULT '',
    acked              BOOLEAN     NOT NULL DEFAULT false, -- the PLC accepted the command telegram
    accepted_at        TIMESTAMPTZ NOT NULL,
    ended_at           TIMESTAMPTZ
);
CREATE INDEX idx_storage_jobs_open ON storage_jobs (asrs_id, aisle, seq)
    WHERE status NOT IN ('DONE', 'FAILED', 'CANCELLED');
CREATE INDEX idx_storage_jobs_telegram ON storage_jobs (asrs_id, telegram_id) WHERE telegram_id <> '';
CREATE INDEX idx_storage_jobs_cancel_telegram ON storage_jobs (asrs_id, cancel_telegram_id) WHERE cancel_telegram_id <> '';
CREATE INDEX idx_storage_jobs_container ON storage_jobs (asrs_id, container_id, kind, status);
-- At most one open external job per container of an AS/RS (ports.ErrOpenJobExists). The two
-- aisles' map locks differ, so this index is what serialises two concurrent requests.
CREATE UNIQUE INDEX uq_storage_jobs_open_external ON storage_jobs (asrs_id, container_id)
    WHERE kind <> 'RESHUFFLE' AND status NOT IN ('DONE', 'FAILED', 'CANCELLED');

-- equipment_units: one EquipmentUnit per aisle (PackML state, the one job in the PLC).
CREATE TABLE equipment_units (
    asrs_id     TEXT COLLATE "C" NOT NULL,
    aisle       INTEGER     NOT NULL CHECK (aisle >= 1),
    unit_id     TEXT COLLATE "C" NOT NULL,
    state       TEXT        NOT NULL CHECK (state IN ('IDLE', 'EXECUTE', 'HELD')),
    current_job TEXT COLLATE "C" NOT NULL DEFAULT '',
    since       TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (asrs_id, aisle)
);

-- asrs_capacity: the last published AsrsCapacityChanged snapshot per AS/RS.
CREATE TABLE asrs_capacity (
    asrs_id         TEXT COLLATE "C" PRIMARY KEY,
    units_total     INTEGER NOT NULL,
    units_available INTEGER NOT NULL,
    positions_total INTEGER NOT NULL,
    positions_free  INTEGER NOT NULL,
    open_jobs       INTEGER NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- processed_telegrams: the up-telegram inbox (protocol rule 3). Claimed in the effect's
-- transaction with the ACK id, so a duplicate gets the identical ACK, also after a restart.
CREATE TABLE processed_telegrams (
    asrs_id         TEXT COLLATE "C" NOT NULL,
    telegram_id     TEXT COLLATE "C" NOT NULL,
    ack_telegram_id TEXT COLLATE "C" NOT NULL,
    processed_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (asrs_id, telegram_id)
);
CREATE INDEX idx_processed_telegrams_at ON processed_telegrams (processed_at);

-- outbox_events: the fleet's transactional outbox shape.
CREATE TABLE outbox_events (
    id           BIGSERIAL   PRIMARY KEY,
    event_id     TEXT        NOT NULL,
    topic        TEXT        NOT NULL,
    event_type   TEXT        NOT NULL,
    subject      TEXT        NOT NULL,
    key          BYTEA,
    dataschema   TEXT        NOT NULL,
    value        BYTEA       NOT NULL,
    headers      JSONB       NOT NULL DEFAULT '[]',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    attempts     INTEGER     NOT NULL DEFAULT 0,
    last_error   TEXT,
    CONSTRAINT outbox_events_event_id_topic_key UNIQUE (event_id, topic)
);
CREATE INDEX idx_outbox_events_unpublished ON outbox_events (id) WHERE published_at IS NULL;

-- idempotency_keys: stored answers of POSTs (2xx and FINAL 4xx only; never RETRYABLE, never 5xx).
CREATE TABLE idempotency_keys (
    key          TEXT PRIMARY KEY,
    fingerprint  TEXT        NOT NULL,
    status       INTEGER     NOT NULL CHECK (status >= 200 AND status < 500),
    content_type TEXT        NOT NULL DEFAULT '',
    body         BYTEA       NOT NULL DEFAULT ''::bytea,
    created_at   TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_idempotency_keys_created_at ON idempotency_keys (created_at);
