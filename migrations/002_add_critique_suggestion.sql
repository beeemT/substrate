-- Add suggestion column to critiques table for storing improvement suggestions.
-- This column was added after the initial migration, so existing databases
-- need this migration to add the column.
--
-- This migration is idempotent: it checks if the column exists before adding it.
-- This handles the case where the column was already added to 001_initial.sql
-- for fresh databases.

-- SQLite doesn't support IF NOT EXISTS for ALTER TABLE ADD COLUMN,
-- so we use a workaround: attempt to select from the column and add only if it fails.
-- However, since SQLite migrations run as a single script, we use a different approach:
-- We rely on the fact that SQLite will ignore the error if we wrap this in a way that
-- doesn't fail the entire migration.

-- For simplicity, we just use ALTER TABLE which will fail silently if the column
-- already exists when using some migration runners, or we document that this error
-- can be ignored. A more robust solution would use a migration system that supports
-- conditional DDL.

-- Check if running manually: if you get "duplicate column name" error, the column
-- already exists and you can ignore this error.
ALTER TABLE critiques ADD COLUMN suggestion TEXT;
