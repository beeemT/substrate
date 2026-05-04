# Migrations

## Idempotency

**Every migration MUST be idempotent.**

SQLite does not support `IF NOT EXISTS` for `ALTER TABLE ADD COLUMN`, `DROP COLUMN`, or
constraint changes (`ADD CONSTRAINT`, `DROP CONSTRAINT`). A migration that re-runs must
either:

1. **Guard before modifying** — check `pragma_table_info` or `pragma_foreign_key_list`
   and skip if the column/constraint already exists.
2. **Guard table with UNIQUE constraint** — for table-rebuilding migrations, use a guard
   table with a unique key. On first run, claim the guard with `INSERT INTO guard VALUES ('done')`.
   On re-run, the `INSERT` fails with `UNIQUE constraint failed`, rolling back the transaction
   and preventing data loss. Example:
   ```sql
   CREATE TABLE IF NOT EXISTS _migration_guard (id TEXT PRIMARY KEY);
   INSERT INTO _migration_guard VALUES ('015_my_migration');  -- fails on re-run
   -- ... migration steps ...
   ```

Migrations MUST NOT:
- Drop tables or columns that may contain user data on re-run
- Fail with a SQL error on re-run (wrap guarded sections in `BEGIN`/`COMMIT` so a
  partial re-run can be cleaned up)
- Make assumptions about run order — any migration may be re-run after any other

## Table Rebuids

When a migration requires table recreation (e.g., to modify a `CHECK` constraint),
use **explicit column lists** in the `INSERT ... SELECT` rather than `SELECT *`:

```sql
INSERT INTO new_table (col_a, col_b, col_c)
    SELECT col_a, col_b, col_c FROM old_table;
```

This prevents misalignment if the schema evolves between migration versions.

## CHECK Constraints

State CHECK constraints MUST enumerate **all** valid domain states. Omitting a state
(e.g., `merged`) will cause `INSERT ... SELECT` to fail when copying rows that have
that state value.
