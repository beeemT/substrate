-- Rename agent_sessions.phase to kind and expand CHECK constraint to include 'foreman'.
--
-- Migration 017 added 'manual' to the phase CHECK constraint. This migration rebuilds
-- the table to rename phase → kind and include 'foreman' for the Foreman's
-- persisted agent session.
--
-- Idempotency: The _018_skip_guard prevents re-running after success.
-- Manual re-runs will lose data. To skip, first run: DELETE FROM _018_skip_guard WHERE id = 'done';

-- Guard table: creates idempotently, insert fails if already migrated (causes rollback).
CREATE TABLE IF NOT EXISTS _018_skip_guard (id TEXT PRIMARY KEY);
INSERT INTO _018_skip_guard VALUES ('done');

-- Create new agent_sessions table with kind column (renamed from phase) and updated CHECK.
CREATE TABLE agent_sessions_new (
    id                TEXT PRIMARY KEY,
    work_item_id      TEXT NOT NULL,
    workspace_id      TEXT NOT NULL,
    sub_plan_id       TEXT,
    plan_id           TEXT,
    repository_name   TEXT,
    harness_name      TEXT NOT NULL,
    worktree_path     TEXT,
    pid               INTEGER,
    status            TEXT NOT NULL,
    exit_code         INTEGER,
    started_at        TEXT,
    shutdown_at       TEXT,
    completed_at      TEXT,
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    owner_instance_id TEXT,
    updated_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    resume_info       TEXT,
    kind              TEXT NOT NULL CHECK (kind IN ('planning','implementation','review','manual','foreman'))
);

-- Copy all data from the current agent_sessions table.
-- The source table already has a 'kind' column (migrations 001-017 were updated to use 'kind').
INSERT INTO agent_sessions_new (
    id, work_item_id, workspace_id, sub_plan_id, plan_id,
    repository_name, harness_name, worktree_path, pid, status,
    exit_code, started_at, shutdown_at, completed_at, created_at,
    owner_instance_id, updated_at, resume_info, kind
)
SELECT
    id, work_item_id, workspace_id, sub_plan_id, plan_id,
    repository_name, harness_name, worktree_path, pid, status,
    exit_code, started_at, shutdown_at, completed_at, created_at,
    owner_instance_id, updated_at, resume_info, kind
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
CREATE INDEX idx_sessions_kind ON agent_sessions(kind);
CREATE INDEX idx_sessions_plan ON agent_sessions(plan_id);
