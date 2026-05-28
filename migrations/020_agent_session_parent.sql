-- Add parent_agent_session_id column to agent_sessions to track the graph edge
-- between consecutive sessions for a sub-plan (initial implementation, retry,
-- review, reimplementation, follow-up, ...). Direction: parent -> child; the
-- child stores its parent's ID. A leaf is any session with no children.
--
-- Idempotency: The _020_skip_guard prevents re-running after success.
-- The migration runner (schema_migrations) already records applied migrations,
-- so this guard is a belt-and-suspenders against manual re-execution that would
-- otherwise fail with "duplicate column" on ALTER TABLE ADD COLUMN.
-- Manual re-runs will lose data. To skip, first run: DELETE FROM _020_skip_guard WHERE id = 'done';

CREATE TABLE IF NOT EXISTS _020_skip_guard (id TEXT PRIMARY KEY);
INSERT INTO _020_skip_guard VALUES ('done');

-- Add the column. ON DELETE SET NULL so deleting a parent does not cascade-delete
-- its descendants — the audit trail of attempts/retries should survive parent
-- removal even if the parent row is somehow pruned.
ALTER TABLE agent_sessions
    ADD COLUMN parent_agent_session_id TEXT
        REFERENCES agent_sessions(id) ON DELETE SET NULL;

-- Index so leaf-derivation and "find children of session X" queries stay cheap.
CREATE INDEX idx_sessions_parent_agent_session
    ON agent_sessions(parent_agent_session_id);

-- Backfill: for each (work_item_id, sub_plan_id) implementation/review sequence
-- ordered by created_at ASC, id ASC, set the parent of session[n] to session[n-1].id.
-- This conservatively rebuilds the linear chain of attempts/retries that the new
-- code will use to derive the leaf. Different sub-plans are not linked together.
--
-- We restrict the backfill to implementation/review rows because planning,
-- manual, and foreman sessions either share a sub-plan id with implementation
-- rows (and would corrupt the chain) or have no sub-plan at all.
--
-- We only update rows where parent_agent_session_id IS NULL so any backfill that
-- partially completed previously, or any rows the application already linked,
-- are left untouched.
WITH ordered AS (
    SELECT
        id,
        LAG(id) OVER (
            PARTITION BY work_item_id, sub_plan_id
            ORDER BY created_at ASC, id ASC
        ) AS prev_id
    FROM agent_sessions
    WHERE sub_plan_id IS NOT NULL
      AND kind IN ('implementation', 'review')
)
UPDATE agent_sessions
SET parent_agent_session_id = (
    SELECT prev_id FROM ordered WHERE ordered.id = agent_sessions.id
)
WHERE parent_agent_session_id IS NULL
  AND id IN (SELECT id FROM ordered WHERE prev_id IS NOT NULL);
