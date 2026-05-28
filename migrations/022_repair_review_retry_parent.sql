-- Repair review-retry graph edges created by the review-existing-implementation
-- recovery path before it learned to link the new review session to the stale
-- failed/interrupted leaf it supersedes.
--
-- Bad shape:
--   completed implementation -> old failed/interrupted retry chain (leaf)
--   completed implementation -> new review/reimplementation chain
--
-- Correct shape:
--   completed implementation -> old failed/interrupted retry chain -> new review
--
-- This is idempotent: after the update, the repaired review no longer has a
-- completed implementation as its direct parent and the stale row is no longer
-- childless, so it will not be selected again.
WITH repair_candidates AS (
    SELECT
        review.id AS review_id,
        (
            SELECT stale.id
            FROM agent_sessions AS stale
            WHERE stale.work_item_id = review.work_item_id
              AND stale.sub_plan_id = review.sub_plan_id
              AND stale.repository_name = review.repository_name
              AND stale.kind IN ('implementation', 'review')
              AND stale.status IN ('interrupted', 'failed')
              AND stale.created_at < review.created_at
              AND stale.id <> parent_impl.id
              AND NOT EXISTS (
                  SELECT 1
                  FROM agent_sessions AS child
                  WHERE child.parent_agent_session_id = stale.id
              )
            ORDER BY stale.created_at DESC, stale.updated_at DESC, stale.id DESC
            LIMIT 1
        ) AS new_parent_id
    FROM agent_sessions AS review
    JOIN agent_sessions AS parent_impl
      ON parent_impl.id = review.parent_agent_session_id
    WHERE review.kind = 'review'
      AND parent_impl.kind = 'implementation'
      AND parent_impl.status = 'completed'
)
UPDATE agent_sessions
SET parent_agent_session_id = (
    SELECT new_parent_id
    FROM repair_candidates
    WHERE repair_candidates.review_id = agent_sessions.id
)
WHERE id IN (
    SELECT review_id
    FROM repair_candidates
    WHERE new_parent_id IS NOT NULL
);

-- Also repair review rows that were created after the graph migration but still
-- have no parent. Prefer a stale failed/interrupted leaf when one exists;
-- otherwise link the review to the latest completed implementation it reviews.
WITH null_parent_candidates AS (
    SELECT
        review.id AS review_id,
        COALESCE(
            (
                SELECT stale.id
                FROM agent_sessions AS stale
                WHERE stale.work_item_id = review.work_item_id
                  AND stale.sub_plan_id = review.sub_plan_id
                  AND stale.repository_name = review.repository_name
                  AND stale.kind IN ('implementation', 'review')
                  AND stale.status IN ('interrupted', 'failed')
                  AND stale.created_at < review.created_at
                  AND NOT EXISTS (
                      SELECT 1
                      FROM agent_sessions AS child
                      WHERE child.parent_agent_session_id = stale.id
                  )
                ORDER BY stale.created_at DESC, stale.updated_at DESC, stale.id DESC
                LIMIT 1
            ),
            (
                SELECT impl.id
                FROM agent_sessions AS impl
                WHERE impl.work_item_id = review.work_item_id
                  AND impl.sub_plan_id = review.sub_plan_id
                  AND impl.repository_name = review.repository_name
                  AND impl.kind = 'implementation'
                  AND impl.status = 'completed'
                  AND impl.created_at < review.created_at
                ORDER BY impl.created_at DESC, impl.updated_at DESC, impl.id DESC
                LIMIT 1
            )
        ) AS new_parent_id
    FROM agent_sessions AS review
    WHERE review.kind = 'review'
      AND review.parent_agent_session_id IS NULL
)
UPDATE agent_sessions
SET parent_agent_session_id = (
    SELECT new_parent_id
    FROM null_parent_candidates
    WHERE null_parent_candidates.review_id = agent_sessions.id
)
WHERE id IN (
    SELECT review_id
    FROM null_parent_candidates
    WHERE new_parent_id IS NOT NULL
);
