CREATE TABLE IF NOT EXISTS workspaces (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    root_path    TEXT NOT NULL,
    status       TEXT NOT NULL CHECK (status IN ('creating','ready','archived','error')),
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS work_items (
    id              TEXT PRIMARY KEY,
    workspace_id    TEXT NOT NULL REFERENCES workspaces(id),
    external_id     TEXT,
    source          TEXT NOT NULL,
    source_scope    TEXT,
    title           TEXT NOT NULL,
    description     TEXT,
    assignee_id     TEXT,
    state           TEXT NOT NULL CHECK (state IN (
                        'ingested','planning','plan_review','approved',
                        'implementing','reviewing','completed','failed')),
    labels          TEXT,  -- JSON array
    source_item_ids TEXT,  -- JSON array
    metadata        TEXT,  -- JSON object
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX idx_work_items_state ON work_items(state);
CREATE INDEX idx_work_items_source ON work_items(source);
CREATE INDEX idx_work_items_workspace ON work_items(workspace_id);
CREATE UNIQUE INDEX idx_work_items_external_id ON work_items(workspace_id, external_id) WHERE external_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS plans (
    id                TEXT PRIMARY KEY,
    work_item_id      TEXT NOT NULL UNIQUE REFERENCES work_items(id),
    orchestrator_plan TEXT NOT NULL,
    status            TEXT NOT NULL CHECK (status IN (
                          'draft','pending_review','approved','rejected')),
    version           INTEGER NOT NULL DEFAULT 1,
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX idx_plans_work_item ON plans(work_item_id);

CREATE TABLE IF NOT EXISTS sub_plans (
    id          TEXT PRIMARY KEY,
    plan_id     TEXT NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    repo_name   TEXT NOT NULL,
    content     TEXT NOT NULL,
    exec_order  INTEGER NOT NULL DEFAULT 0,
    status      TEXT NOT NULL CHECK (status IN ('pending','in_progress','completed','failed')),
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(plan_id, repo_name)
);
CREATE INDEX idx_sub_plans_plan ON sub_plans(plan_id);

CREATE TABLE IF NOT EXISTS agent_sessions (
    id                TEXT PRIMARY KEY,
    sub_plan_id       TEXT NOT NULL REFERENCES sub_plans(id),
    workspace_id      TEXT NOT NULL REFERENCES workspaces(id),
    repository_name   TEXT NOT NULL,
    harness_name      TEXT NOT NULL,
    worktree_dir      TEXT NOT NULL,
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
CREATE INDEX idx_sessions_sub_plan ON agent_sessions(sub_plan_id);
CREATE INDEX idx_sessions_status ON agent_sessions(status);

CREATE TABLE IF NOT EXISTS review_cycles (
    id               TEXT PRIMARY KEY,
    agent_session_id TEXT NOT NULL REFERENCES agent_sessions(id),
    cycle_number     INTEGER NOT NULL DEFAULT 1,
    reviewer_harness TEXT NOT NULL,
    status           TEXT NOT NULL CHECK (status IN (
                         'reviewing','critiques_found','reimplementing','passed','failed')),
    summary          TEXT,
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(agent_session_id, cycle_number)
);
CREATE INDEX idx_reviews_session ON review_cycles(agent_session_id);

CREATE TABLE IF NOT EXISTS critiques (
    id              TEXT PRIMARY KEY,
    review_cycle_id TEXT NOT NULL REFERENCES review_cycles(id) ON DELETE CASCADE,
    file_path       TEXT,
    line_number     INTEGER,
    severity        TEXT NOT NULL CHECK (severity IN ('critical','major','minor','nit')),
    description     TEXT NOT NULL,
    suggestion      TEXT,
    status          TEXT NOT NULL CHECK (status IN ('open','resolved')) DEFAULT 'open',
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX idx_critiques_review ON critiques(review_cycle_id);

CREATE TABLE IF NOT EXISTS questions (
    id               TEXT PRIMARY KEY,
    agent_session_id TEXT NOT NULL REFERENCES agent_sessions(id),
    content          TEXT NOT NULL,
    context          TEXT,
    answer           TEXT,
    answered_by      TEXT CHECK (answered_by IN ('foreman','human')),
    status           TEXT NOT NULL CHECK (status IN ('pending','answered','escalated')) DEFAULT 'pending',
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    answered_at      TEXT
);
CREATE INDEX idx_questions_session ON questions(agent_session_id);
CREATE INDEX idx_questions_status ON questions(status);

CREATE TABLE IF NOT EXISTS documentation_sources (
    id              TEXT PRIMARY KEY,
    workspace_id    TEXT NOT NULL REFERENCES workspaces(id),
    source_type     TEXT NOT NULL CHECK (source_type IN ('repo_embedded','dedicated_repo')),
    path            TEXT,
    repo_url        TEXT,
    repository_name TEXT,
    branch          TEXT,
    description     TEXT,
    last_synced     TEXT,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX idx_docs_workspace ON documentation_sources(workspace_id);

CREATE TABLE IF NOT EXISTS system_events (
    id           TEXT PRIMARY KEY,
    event_type   TEXT NOT NULL,
    workspace_id TEXT REFERENCES workspaces(id),
    payload      TEXT NOT NULL,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX idx_events_type ON system_events(event_type);
CREATE INDEX idx_events_workspace ON system_events(workspace_id);
CREATE INDEX idx_events_created ON system_events(created_at);

CREATE TABLE IF NOT EXISTS substrate_instances (
    id              TEXT PRIMARY KEY,
    workspace_id    TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    pid             INTEGER NOT NULL,
    hostname        TEXT NOT NULL,
    last_heartbeat  TEXT NOT NULL,
    started_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX idx_instances_workspace ON substrate_instances(workspace_id);
