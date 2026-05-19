-- Remove stale review artifact links whose owning work item has been deleted.
-- These rows should normally be removed by the ON DELETE CASCADE constraint, but
-- older databases may contain orphans from runs before foreign-key enforcement.
DELETE FROM session_review_artifacts
WHERE NOT EXISTS (
    SELECT 1
    FROM work_items
    WHERE work_items.id = session_review_artifacts.work_item_id
);
