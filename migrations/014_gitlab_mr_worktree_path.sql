ALTER TABLE gitlab_merge_requests ADD COLUMN worktree_path TEXT NOT NULL DEFAULT '';

UPDATE gitlab_merge_requests
SET worktree_path = COALESCE((
    SELECT json_extract(e.payload, '$.artifact.worktree_path')
    FROM system_events e
    WHERE e.event_type = 'review.artifact_recorded'
      AND e.workspace_id IN (
          SELECT s.workspace_id
          FROM session_review_artifacts s
          WHERE s.provider = 'gitlab'
            AND s.provider_artifact_id = gitlab_merge_requests.id
      )
      AND json_extract(e.payload, '$.artifact.provider') = 'gitlab'
      AND json_extract(e.payload, '$.artifact.repo_name') = gitlab_merge_requests.project_path
      AND CAST(SUBSTR(json_extract(e.payload, '$.artifact.ref'), 2) AS INTEGER) = gitlab_merge_requests.iid
      AND COALESCE(json_extract(e.payload, '$.artifact.worktree_path'), '') != ''
    ORDER BY e.created_at DESC
    LIMIT 1
), '')
WHERE worktree_path = '';
