-- Add a daemon-local monotonic sequence column to system_events.
--
-- The sequence is assigned in the persistence transaction by
-- `SELECT COALESCE(MAX(sequence), 0) + 1`, so concurrent inserts that
-- race for a sequence number are serialized by SQLite's row-level
-- write lock. The bus.Publish path additionally serializes
-- persist+dispatch so subscribers always observe events in sequence
-- order.
--
-- Idempotency: the _024_skip_guard prevents re-running after success.
-- The migration runner (schema_migrations) already records applied
-- migrations, so this guard is belt-and-suspenders against manual
-- re-execution that would otherwise fail with "duplicate column" on
-- ALTER TABLE ADD COLUMN.

CREATE TABLE IF NOT EXISTS _024_skip_guard (id TEXT PRIMARY KEY);
INSERT INTO _024_skip_guard VALUES ('done');

-- Add the sequence column nullable so we can backfill without a
-- constraint violation. The NOT NULL constraint is added by the
-- table rebuild below.
ALTER TABLE system_events ADD COLUMN sequence INTEGER;

-- Backfill existing rows in a strictly monotonic order, using
-- (created_at, id) as a stable tiebreaker for events that share a
-- timestamp.
WITH ordered_events AS (
    SELECT id, ROW_NUMBER() OVER (ORDER BY created_at, id) AS assigned_sequence
    FROM system_events
)
UPDATE system_events
SET sequence = (
    SELECT assigned_sequence
    FROM ordered_events
    WHERE ordered_events.id = system_events.id
);

-- Enforce NOT NULL on sequence by rebuilding the table. SQLite does
-- not support ALTER COLUMN ... SET NOT NULL. system_events has no
-- incoming foreign-key references (it is only consumed by ad-hoc
-- SELECTs), so the rebuild is safe.
CREATE TABLE system_events_new (
    id           TEXT PRIMARY KEY,
    event_type   TEXT NOT NULL,
    workspace_id TEXT REFERENCES workspaces(id),
    payload      TEXT NOT NULL,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    sequence     INTEGER NOT NULL
);
INSERT INTO system_events_new (id, event_type, workspace_id, payload, created_at, sequence)
    SELECT id, event_type, workspace_id, payload, created_at, sequence
    FROM system_events;
DROP TABLE system_events;
ALTER TABLE system_events_new RENAME TO system_events;

-- Restore the original secondary indexes plus the new sequence indexes.
-- The unique index on sequence pairs with the NOT NULL column to
-- enforce both non-null and unique.
CREATE INDEX idx_events_type ON system_events(event_type);
CREATE INDEX idx_events_workspace ON system_events(workspace_id);
CREATE INDEX idx_events_created ON system_events(created_at);
CREATE UNIQUE INDEX idx_events_sequence ON system_events(sequence);
CREATE INDEX idx_events_workspace_sequence ON system_events(workspace_id, sequence);
