CREATE TABLE IF NOT EXISTS agent_session_continuations (
    id TEXT PRIMARY KEY,
    agent_session_id TEXT NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
    work_item_id TEXT NOT NULL REFERENCES work_items(id) ON DELETE CASCADE,
    sub_plan_id TEXT REFERENCES sub_plans(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'running', 'completed', 'failed', 'skipped', 'superseded')),
    attempt INTEGER NOT NULL DEFAULT 1 CHECK (attempt > 0),
    last_error TEXT NOT NULL DEFAULT '',
    started_at TEXT,
    completed_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (agent_session_id, kind, attempt)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_session_continuations_active
    ON agent_session_continuations(agent_session_id, kind)
    WHERE status IN ('pending', 'running', 'failed');

CREATE INDEX IF NOT EXISTS idx_agent_session_continuations_recoverable
    ON agent_session_continuations(work_item_id, status, updated_at)
    WHERE status IN ('pending', 'running', 'failed');

CREATE INDEX IF NOT EXISTS idx_agent_session_continuations_session
    ON agent_session_continuations(agent_session_id);
