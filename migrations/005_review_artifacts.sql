-- GitHub pull requests (provider-native data)
CREATE TABLE IF NOT EXISTS github_pull_requests (
    id          TEXT PRIMARY KEY,
    owner       TEXT NOT NULL,
    repo        TEXT NOT NULL,
    number      INTEGER NOT NULL,
    state       TEXT NOT NULL DEFAULT '',
    draft       INTEGER NOT NULL DEFAULT 0,
    head_branch TEXT NOT NULL DEFAULT '',
    html_url    TEXT NOT NULL DEFAULT '',
    merged_at   TEXT,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE UNIQUE INDEX idx_github_pull_requests_owner_repo_number ON github_pull_requests(owner, repo, number);

-- GitLab merge requests (provider-native data)
CREATE TABLE IF NOT EXISTS gitlab_merge_requests (
    id            TEXT PRIMARY KEY,
    project_path  TEXT NOT NULL,
    iid           INTEGER NOT NULL,
    state         TEXT NOT NULL DEFAULT '',
    draft         INTEGER NOT NULL DEFAULT 0,
    source_branch TEXT NOT NULL DEFAULT '',
    web_url       TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE UNIQUE INDEX idx_gitlab_merge_requests_project_path_iid ON gitlab_merge_requests(project_path, iid);

-- Generic link table: session <-> review artifact
CREATE TABLE IF NOT EXISTS session_review_artifacts (
    id                   TEXT PRIMARY KEY,
    workspace_id         TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    work_item_id         TEXT NOT NULL REFERENCES work_items(id) ON DELETE CASCADE,
    provider             TEXT NOT NULL CHECK (provider IN ('github', 'gitlab')),
    provider_artifact_id TEXT NOT NULL,
    created_at           TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at           TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE UNIQUE INDEX idx_session_review_artifacts_dedup ON session_review_artifacts(workspace_id, work_item_id, provider, provider_artifact_id);
CREATE INDEX idx_session_review_artifacts_work_item ON session_review_artifacts(work_item_id);
CREATE INDEX idx_session_review_artifacts_workspace ON session_review_artifacts(workspace_id);

-- ---------------------------------------------------------------------------
-- Backfill from system_events
-- ---------------------------------------------------------------------------

-- GitHub PRs: pick the latest event per (owner, repo, number) so we get the
-- most recent state, then INSERT OR IGNORE (UNIQUE index deduplicates).
INSERT OR IGNORE INTO github_pull_requests (id, owner, repo, number, state, draft, head_branch, html_url, created_at, updated_at)
SELECT
    e.id,
    SUBSTR(json_extract(e.payload, '$.artifact.repo_name'), 1, INSTR(json_extract(e.payload, '$.artifact.repo_name'), '/') - 1),
    SUBSTR(json_extract(e.payload, '$.artifact.repo_name'), INSTR(json_extract(e.payload, '$.artifact.repo_name'), '/') + 1),
    CAST(SUBSTR(json_extract(e.payload, '$.artifact.ref'), 2) AS INTEGER),
    COALESCE(json_extract(e.payload, '$.artifact.state'), ''),
    COALESCE(json_extract(e.payload, '$.artifact.draft'), 0),
    COALESCE(json_extract(e.payload, '$.artifact.branch'), ''),
    COALESCE(json_extract(e.payload, '$.artifact.url'), ''),
    e.created_at,
    COALESCE(json_extract(e.payload, '$.artifact.updated_at'), e.created_at)
FROM system_events e
WHERE e.event_type = 'review.artifact_recorded'
  AND json_extract(e.payload, '$.artifact.provider') = 'github'
  AND json_extract(e.payload, '$.artifact.ref') LIKE '#%'
  AND INSTR(json_extract(e.payload, '$.artifact.repo_name'), '/') > 0
ORDER BY e.created_at DESC;

-- GitLab MRs: same strategy, refs like '!17'.
INSERT OR IGNORE INTO gitlab_merge_requests (id, project_path, iid, state, draft, source_branch, web_url, created_at, updated_at)
SELECT
    e.id,
    json_extract(e.payload, '$.artifact.repo_name'),
    CAST(SUBSTR(json_extract(e.payload, '$.artifact.ref'), 2) AS INTEGER),
    COALESCE(json_extract(e.payload, '$.artifact.state'), ''),
    COALESCE(json_extract(e.payload, '$.artifact.draft'), 0),
    COALESCE(json_extract(e.payload, '$.artifact.branch'), ''),
    COALESCE(json_extract(e.payload, '$.artifact.url'), ''),
    e.created_at,
    COALESCE(json_extract(e.payload, '$.artifact.updated_at'), e.created_at)
FROM system_events e
WHERE e.event_type = 'review.artifact_recorded'
  AND json_extract(e.payload, '$.artifact.provider') = 'gitlab'
  AND json_extract(e.payload, '$.artifact.ref') LIKE '!%'
ORDER BY e.created_at DESC;

-- Link table: GitHub artifacts
-- Use a subquery to find the canonical PR row ID (the Upsert above may have
-- picked a different event's ID than this one).  Also guard against deleted
-- workspaces / work_items to prevent FK violations (INSERT OR IGNORE only
-- suppresses UNIQUE / NOT NULL / CHECK, not FK errors).
INSERT OR IGNORE INTO session_review_artifacts (id, workspace_id, work_item_id, provider, provider_artifact_id, created_at, updated_at)
SELECT
    e.id || ':link',
    e.workspace_id,
    json_extract(e.payload, '$.work_item_id'),
    'github',
    (SELECT gp.id FROM github_pull_requests gp
     WHERE gp.owner = SUBSTR(json_extract(e.payload, '$.artifact.repo_name'), 1, INSTR(json_extract(e.payload, '$.artifact.repo_name'), '/') - 1)
       AND gp.repo  = SUBSTR(json_extract(e.payload, '$.artifact.repo_name'), INSTR(json_extract(e.payload, '$.artifact.repo_name'), '/') + 1)
       AND gp.number = CAST(SUBSTR(json_extract(e.payload, '$.artifact.ref'), 2) AS INTEGER)),
    e.created_at,
    COALESCE(json_extract(e.payload, '$.artifact.updated_at'), e.created_at)
FROM system_events e
WHERE e.event_type = 'review.artifact_recorded'
  AND json_extract(e.payload, '$.artifact.provider') = 'github'
  AND json_extract(e.payload, '$.artifact.ref') LIKE '#%'
  AND INSTR(json_extract(e.payload, '$.artifact.repo_name'), '/') > 0
  AND e.workspace_id IS NOT NULL
  AND json_extract(e.payload, '$.work_item_id') IS NOT NULL
  AND e.workspace_id IN (SELECT id FROM workspaces)
  AND json_extract(e.payload, '$.work_item_id') IN (SELECT id FROM work_items)
ORDER BY e.created_at DESC;

-- Link table: GitLab artifacts
INSERT OR IGNORE INTO session_review_artifacts (id, workspace_id, work_item_id, provider, provider_artifact_id, created_at, updated_at)
SELECT
    e.id || ':link',
    e.workspace_id,
    json_extract(e.payload, '$.work_item_id'),
    'gitlab',
    (SELECT gm.id FROM gitlab_merge_requests gm
     WHERE gm.project_path = json_extract(e.payload, '$.artifact.repo_name')
       AND gm.iid = CAST(SUBSTR(json_extract(e.payload, '$.artifact.ref'), 2) AS INTEGER)),
    e.created_at,
    COALESCE(json_extract(e.payload, '$.artifact.updated_at'), e.created_at)
FROM system_events e
WHERE e.event_type = 'review.artifact_recorded'
  AND json_extract(e.payload, '$.artifact.provider') = 'gitlab'
  AND json_extract(e.payload, '$.artifact.ref') LIKE '!%'
  AND e.workspace_id IS NOT NULL
  AND json_extract(e.payload, '$.work_item_id') IS NOT NULL
  AND e.workspace_id IN (SELECT id FROM workspaces)
  AND json_extract(e.payload, '$.work_item_id') IN (SELECT id FROM work_items)
ORDER BY e.created_at DESC;
