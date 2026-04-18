CREATE TABLE IF NOT EXISTS github_pr_reviews (
    id              TEXT PRIMARY KEY,
    pr_id           TEXT NOT NULL REFERENCES github_pull_requests(id) ON DELETE CASCADE,
    reviewer_login  TEXT NOT NULL,
    state           TEXT NOT NULL,
    submitted_at    TEXT NOT NULL,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE UNIQUE INDEX idx_github_pr_reviews_pr_reviewer ON github_pr_reviews(pr_id, reviewer_login);

CREATE TABLE IF NOT EXISTS gitlab_mr_reviews (
    id              TEXT PRIMARY KEY,
    mr_id           TEXT NOT NULL REFERENCES gitlab_merge_requests(id) ON DELETE CASCADE,
    reviewer_login  TEXT NOT NULL,
    state           TEXT NOT NULL,
    submitted_at    TEXT NOT NULL,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE UNIQUE INDEX idx_gitlab_mr_reviews_mr_reviewer ON gitlab_mr_reviews(mr_id, reviewer_login);
