-- Add previous_state column to work_items to track the state a session transitioned from
-- (used for unarchive to restore the pre-archive state).
-- SQLite does not support ALTER TABLE ADD COLUMN IF NOT EXISTS; this migration must
-- only be run once. It is safe to re-run on the same database only if the column
-- already exists (SQLite returns a "duplicate column name" error on re-run).
ALTER TABLE work_items ADD COLUMN previous_state TEXT;
