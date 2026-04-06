CREATE TABLE IF NOT EXISTS new_session_filters (
    id            TEXT PRIMARY KEY,
    workspace_id  TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    provider      TEXT NOT NULL,
    criteria_json TEXT NOT NULL,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(workspace_id, provider, name)
);
CREATE INDEX idx_new_session_filters_workspace ON new_session_filters(workspace_id);
CREATE INDEX idx_new_session_filters_workspace_provider ON new_session_filters(workspace_id, provider);

CREATE TABLE IF NOT EXISTS new_session_filter_locks (
    filter_id         TEXT PRIMARY KEY REFERENCES new_session_filters(id) ON DELETE CASCADE,
    instance_id       TEXT NOT NULL,
    lease_expires_at  TEXT NOT NULL,
    acquired_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX idx_new_session_filter_locks_instance ON new_session_filter_locks(instance_id);
CREATE INDEX idx_new_session_filter_locks_expires ON new_session_filter_locks(lease_expires_at);
