-- Fix agent_sessions.work_item_id FK to reference work_items instead of work_items_old.
--
-- Migration 015 renamed work_items to work_items_old and work_items_new to work_items,
-- but did not update the FK constraint in agent_sessions, leaving it pointing to the
-- wrong table (work_items_old instead of work_items). This caused all new session
-- creations to fail with FOREIGN KEY constraint failed.
--
-- SQLite does not support ALTER TABLE to modify FK constraints, so we recreate the table.
--
-- Idempotency: The _016_skip_guard prevents re-running after success.
-- ⚠️  Manual re-runs will lose data. To skip, first run: DELETE FROM _016_skip_guard WHERE id = 'done';

-- Guard table: creates idempotently, insert fails if already migrated (causes rollback).
CREATE TABLE IF NOT EXISTS _016_skip_guard (id TEXT PRIMARY KEY);
INSERT INTO _016_skip_guard VALUES ('done');

-- Create new agent_sessions table with correct FK reference to work_items.
-- Using explicit column list to match the current schema.
CREATE TABLE agent_sessions_new (
    id                TEXT PRIMARY KEY,
    work_item_id      TEXT NOT NULL REFERENCES work_items(id) ON DELETE CASCADE,
    sub_plan_id       TEXT REFERENCES sub_plans(id) ON DELETE SET NULL,
    workspace_id      TEXT NOT NULL REFERENCES workspaces(id),
    phase             TEXT NOT NULL CHECK (phase IN ('planning','implementation','review')),
    repository_name   TEXT,
    harness_name      TEXT NOT NULL,
    worktree_path     TEXT,
    pid               INTEGER,
    status            TEXT NOT NULL CHECK (status IN (
                          'pending','running','waiting_for_answer','completed','failed','interrupted')),
    exit_code         INTEGER,
    started_at        TEXT,
    shutdown_at       TEXT,
    completed_at      TEXT,
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    owner_instance_id TEXT REFERENCES substrate_instances(id) ON DELETE SET NULL,
    updated_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    resume_info       TEXT,
    plan_id           TEXT REFERENCES plans(id) ON DELETE SET NULL
);

-- Copy all data from the current agent_sessions table.
INSERT INTO agent_sessions_new (
    id, work_item_id, sub_plan_id, workspace_id, phase, repository_name,
    harness_name, worktree_path, pid, status, exit_code, started_at,
    shutdown_at, completed_at, created_at, owner_instance_id, updated_at,
    resume_info, plan_id
)
SELECT
    id, work_item_id, sub_plan_id, workspace_id, phase, repository_name,
    harness_name, worktree_path, pid, status, exit_code, started_at,
    shutdown_at, completed_at, created_at, owner_instance_id, updated_at,
    resume_info, plan_id
FROM agent_sessions;

-- Replace old table with new one.
DROP TABLE agent_sessions;
ALTER TABLE agent_sessions_new RENAME TO agent_sessions;

-- Restore all indexes from the original schema.
CREATE INDEX idx_sessions_work_item ON agent_sessions(work_item_id);
CREATE INDEX idx_sessions_sub_plan ON agent_sessions(sub_plan_id);
CREATE INDEX idx_sessions_workspace ON agent_sessions(workspace_id);
CREATE INDEX idx_sessions_owner_instance ON agent_sessions(owner_instance_id);
CREATE INDEX idx_sessions_status ON agent_sessions(status);
CREATE INDEX idx_sessions_phase ON agent_sessions(phase);
CREATE INDEX idx_sessions_plan ON agent_sessions(plan_id);
