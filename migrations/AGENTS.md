# Migrations

## Idempotency

**Every migration MUST be idempotent.**

SQLite does not support `IF NOT EXISTS` for `ALTER TABLE ADD COLUMN`, `DROP COLUMN`, or
constraint changes (`ADD CONSTRAINT`, `DROP CONSTRAINT`). A migration that re-runs must
either:

1. **Guard before modifying** — check `pragma_table_info` or `pragma_foreign_key_list`
   and skip if the column/constraint already exists.
2. **Fail safely on re-run** — if a guard is not feasible, the migration MUST detect
   prior completion (e.g., via a dedicated `_migrations` tracking table) and return
   early without modifying the schema or data.

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
