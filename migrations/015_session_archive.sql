-- Add previous_state column to work_items to track the state a session transitioned from
-- (used for unarchive to restore the pre-archive state).
--
-- Also updates the state CHECK constraint to include 'archived' (new state added by this
-- feature) and 'merged' (was missing from migration 001).
--
-- SQLite does not support DROP CONSTRAINT or ALTER COLUMN, so updating the CHECK
-- constraint requires table recreation. We use explicit column lists (not SELECT *)
-- to match the exact column set present at the time this migration runs.
--
-- Note: the migration runner wraps this file in a transaction; do NOT add BEGIN/COMMIT.
PRAGMA foreign_keys = OFF;

-- Create new table with previous_state column and updated CHECK constraint.
CREATE TABLE work_items_new (
    id              TEXT PRIMARY KEY,
    workspace_id    TEXT NOT NULL REFERENCES workspaces(id),
    external_id     TEXT,
    source          TEXT NOT NULL,
    source_scope    TEXT,
    title           TEXT NOT NULL,
    description     TEXT,
    assignee_id     TEXT,
    state           TEXT NOT NULL,
    labels          TEXT,
    source_item_ids TEXT,
    metadata        TEXT,
    extra_context   TEXT,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    previous_state  TEXT,
    CHECK (state IN ('ingested','planning','plan_review','approved','implementing','reviewing','completed','failed','archived','merged'))
);

-- Copy data from the current work_items table.
-- Explicit column list is required because the table schema has been extended by
-- earlier migrations (e.g., extra_context from 010). Using * would break if the
-- column count doesn't match.
INSERT INTO work_items_new (
    id, workspace_id, external_id, source, source_scope, title, description,
    assignee_id, state, labels, source_item_ids, metadata, extra_context,
    created_at, updated_at, previous_state
) SELECT
    id, workspace_id, external_id, source, source_scope, title, description,
    assignee_id, state, labels, source_item_ids, metadata, extra_context,
    created_at, updated_at, NULL
FROM work_items;

-- Drop old table and rename new one. Foreign keys are OFF so this won't fail
-- even though plans.work_item_id references work_items.
DROP TABLE work_items;
ALTER TABLE work_items_new RENAME TO work_items;

-- Restore indexes from the original schema (001).
CREATE INDEX idx_work_items_state ON work_items(state);
CREATE INDEX idx_work_items_source ON work_items(source);
CREATE INDEX idx_work_items_workspace ON work_items(workspace_id);
CREATE UNIQUE INDEX idx_work_items_external_id ON work_items(workspace_id, external_id) WHERE external_id IS NOT NULL;

PRAGMA foreign_keys = ON;
