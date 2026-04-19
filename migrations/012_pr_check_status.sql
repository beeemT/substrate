CREATE TABLE IF NOT EXISTS github_pr_checks (
    id          TEXT PRIMARY KEY,
    pr_id       TEXT NOT NULL REFERENCES github_pull_requests(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    status      TEXT NOT NULL,
    conclusion  TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(pr_id, name)
);

CREATE TABLE IF NOT EXISTS gitlab_mr_checks (
    id          TEXT PRIMARY KEY,
    mr_id       TEXT NOT NULL REFERENCES gitlab_merge_requests(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    status      TEXT NOT NULL,
    conclusion  TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(mr_id, name)
);
