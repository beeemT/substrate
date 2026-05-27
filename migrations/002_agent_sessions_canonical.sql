PRAGMA foreign_keys = OFF;

CREATE TABLE agent_sessions_v2 (
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
    updated_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

INSERT INTO agent_sessions_v2 (
    id,
    work_item_id,
    sub_plan_id,
    workspace_id,
    phase,
    repository_name,
    harness_name,
    worktree_path,
    pid,
    status,
    exit_code,
    started_at,
    shutdown_at,
    completed_at,
    created_at,
    owner_instance_id,
    updated_at
)
SELECT
    s.id,
    p.work_item_id,
    s.sub_plan_id,
    s.workspace_id,
    'implementation',
    s.repository_name,
    s.harness_name,
    s.worktree_dir,
    s.pid,
    s.status,
    s.exit_code,
    s.started_at,
    s.shutdown_at,
    s.completed_at,
    s.created_at,
    s.owner_instance_id,
    s.updated_at
FROM agent_sessions s
JOIN sub_plans sp ON sp.id = s.sub_plan_id
JOIN plans p ON p.id = sp.plan_id;

DROP TABLE agent_sessions;
ALTER TABLE agent_sessions_v2 RENAME TO agent_sessions;

CREATE INDEX idx_sessions_work_item ON agent_sessions(work_item_id);
CREATE INDEX idx_sessions_sub_plan ON agent_sessions(sub_plan_id);
CREATE INDEX idx_sessions_workspace ON agent_sessions(workspace_id);
CREATE INDEX idx_sessions_owner_instance ON agent_sessions(owner_instance_id);
CREATE INDEX idx_sessions_status ON agent_sessions(status);
CREATE INDEX idx_sessions_phase ON agent_sessions(phase);

PRAGMA foreign_keys = ON;
