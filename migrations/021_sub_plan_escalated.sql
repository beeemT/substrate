-- Add 'escalated' to sub_plans status CHECK constraint.
--
-- SQLite does not support ALTER TABLE ... ALTER CONSTRAINT, so we must
-- recreate the table with the expanded CHECK.

-- Guard table: creates idempotently, insert fails if already migrated.
CREATE TABLE IF NOT EXISTS _021_skip_guard (id TEXT PRIMARY KEY);
INSERT INTO _021_skip_guard VALUES ('done');

-- Create new sub_plans table with expanded CHECK constraint.
CREATE TABLE sub_plans_new (
    id          TEXT PRIMARY KEY,
    plan_id     TEXT NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    repo_name   TEXT NOT NULL,
    content     TEXT NOT NULL,
    exec_order  INTEGER NOT NULL DEFAULT 0,
    status      TEXT NOT NULL CHECK (status IN ('pending','in_progress','completed','failed','escalated')),
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(plan_id, repo_name)
);

-- Copy all data from the current sub_plans table.
INSERT INTO sub_plans_new (id, plan_id, repo_name, content, exec_order, status, created_at, updated_at)
    SELECT id, plan_id, repo_name, content, exec_order, status, created_at, updated_at
    FROM sub_plans;

-- Replace old table with new one.
DROP TABLE sub_plans;
ALTER TABLE sub_plans_new RENAME TO sub_plans;

-- Restore indexes.
CREATE INDEX idx_sub_plans_plan ON sub_plans(plan_id);
