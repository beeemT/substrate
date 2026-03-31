-- Allow multiple plans per work item by superseding old plans instead of deleting them.
-- The inline UNIQUE on plans.work_item_id is replaced with a partial unique index
-- that only constrains non-superseded plans.
PRAGMA foreign_keys = OFF;

CREATE TABLE plans_v2 (
    id                TEXT PRIMARY KEY,
    work_item_id      TEXT NOT NULL REFERENCES work_items(id),
    orchestrator_plan TEXT NOT NULL,
    status            TEXT NOT NULL CHECK (status IN (
                          'draft','pending_review','approved','rejected','superseded')),
    version           INTEGER NOT NULL DEFAULT 1,
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    faq               TEXT NOT NULL DEFAULT '[]'
);

INSERT INTO plans_v2 (id, work_item_id, orchestrator_plan, status, version, created_at, updated_at, faq)
SELECT id, work_item_id, orchestrator_plan, status, version, created_at, updated_at, faq FROM plans;

DROP TABLE plans;
ALTER TABLE plans_v2 RENAME TO plans;

-- At most one non-superseded plan per work item.
CREATE UNIQUE INDEX idx_plans_active_work_item ON plans(work_item_id) WHERE status != 'superseded';
CREATE INDEX idx_plans_work_item ON plans(work_item_id);

-- Link planning sessions to the plan they produced.
ALTER TABLE agent_sessions ADD COLUMN plan_id TEXT REFERENCES plans(id) ON DELETE SET NULL;
CREATE INDEX idx_sessions_plan ON agent_sessions(plan_id);

PRAGMA foreign_keys = ON;
