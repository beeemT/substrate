# Agent Session Graph Plan

## Goal

Make agent-session ordering and labels graph-based instead of relying on flat historical status scans. Each subplan should derive its current label from the leaf nodes of its agent-session graph.

## Model

Add a first-class edge to `agent_sessions`:

```go
ParentAgentSessionID string
```

SQLite column:

```sql
parent_agent_session_id TEXT REFERENCES agent_sessions(id) ON DELETE SET NULL
```

Index:

```sql
CREATE INDEX idx_sessions_parent_agent_session
  ON agent_sessions(parent_agent_session_id);
```

Direction:

```text
parent -> child
```

The child row stores `parent_agent_session_id`.

Use `parent_agent_session_id` rather than `supersedes_agent_session_id` because the graph represents both supersession and normal continuation:

- implementation -> review
- review -> reimplementation
- failed/interrupted session -> retry/resume
- completed session -> follow-up

A leaf is any session with no child sessions.

## Edge Creation Rules

For implementation/review sessions inside a subplan:

| Scenario | Parent |
|---|---|
| Initial implementation | empty |
| Review created for implementation | implementation session ID |
| Reimplementation created from review critique | review session ID |
| Review created for reimplementation | reimplementation session ID |
| Retry failed implementation/review | failed session ID |
| Resume interrupted implementation/review | interrupted session ID |
| Follow-up failed/completed session | original session ID |

Planning sessions can use the same field later, but the first cut should scope graph-derived labels to implementation/review subplan sessions.

## Migration

Add migration:

```text
migrations/020_agent_session_parent.sql
```

Requirements:

- Follow `migrations/AGENTS.md` idempotency rules.
- Add `parent_agent_session_id` only if missing.
- Add index only if missing.
- Backfill conservatively.

Backfill rule:

For each `(work_item_id, sub_plan_id)` implementation/review sequence ordered by:

```sql
created_at ASC, id ASC
```

set:

```text
session[0].parent = NULL
session[n].parent = session[n-1].id
```

Do not infer edges across subplans.

## Domain and Repository Changes

Update:

- `internal/domain/session.go`
  - add `ParentAgentSessionID string` to `domain.AgentSession`.

- `internal/repository/sqlite/session.go`
  - `sessionRow`
  - `toDomain`
  - `rowFromAgentSession`
  - `INSERT`
  - `UPDATE`

- repository tests
  - create/get/list/update round-trip preserves `ParentAgentSessionID`.

## Service and Orchestrator Changes

Set parent IDs at creation time:

- `AgentSessionService.Resume`
  - `ParentAgentSessionID = interrupted.ID`

- `AgentSessionService.FollowUpFailed`
  - `ParentAgentSessionID = failed.ID`

- completed-session follow-up path
  - parent should be the original completed session ID when a new row is created.

- `ImplementationService.runImplementation`
  - accept explicit parent context, e.g. `parentSessionID string`.
  - first implementation: empty.
  - retry failed implementation: failed implementation ID.
  - reimplementation after review: review session ID.

- `ReviewPipeline.startReviewAgent`
  - `ParentAgentSessionID = implementation session ID being reviewed`.

Do not guess parent inside low-level create helpers if the caller has the lifecycle context.

## TUI Leaf Logic

Add shared helper near task-sidebar/session helpers:

```go
func leafAgentSessions(sessions []domain.AgentSession) []domain.AgentSession
```

Algorithm:

1. Build `hasChild[parentID] = true` for every non-empty `ParentAgentSessionID`.
2. Leaf = session whose `ID` is not in `hasChild`.
3. For legacy rows with no graph edges, fall back to today's transitive approximation:
   - group by `(sub_plan_id, repository_name)`
   - newest by `CreatedAt`, then `UpdatedAt`, then `ID` is the leaf.

Use leaves for:

- `sidebarEntryFromWorkItem`
  - `HasInterrupted` only from leaves.
  - `HasOpenQuestion` only from leaf waiting sessions with open questions.

- `localSessionSearchEntry`
  - same status projection.

- `latestTaskForSubPlan`
  - latest/waiting/interrupted should be derived from subplan leaves.

- `buildOverviewActions`
  - replace current superseded-session heuristic with leaf logic.

## Label Rules

Per subplan leaf set:

| Leaf status | Meaning |
|---|---|
| `waiting_for_answer` | Waiting for answer |
| `pending` / `running` | Active |
| `interrupted` | Interrupted |
| `failed` | Failed |
| `completed` | Completed |

Work-item display:

1. Any leaf waiting with open question -> `Waiting for answer`.
2. Else any leaf interrupted -> `Interrupted`.
3. Else use work-item state label (`Implementing`, `Under review`, etc.).

A historical interrupted/failed session must not affect labels if it has a child.

## Tests

Add coverage for:

1. Work-item retry projection
   - old interrupted/failed session
   - new running child session
   - work item state `implementing`
   - sidebar subtitle is `Implementing`, not `Interrupted`.

2. Review leaf
   - implementation completed
   - review running child
   - leaf is review.

3. Reimplementation chain
   - implementation completed -> review completed/critique -> reimplementation running
   - leaf is reimplementation.

4. Legacy fallback
   - no parent IDs
   - same repo sessions ordered by created time
   - newest active session suppresses older interrupted label.

5. Repository round-trip
   - parent ID persists through create/get/list/update.

6. Migration
   - column exists.
   - index exists.
   - backfill links sessions in created order for the same subplan.
   - different subplans are not linked.

## Cutover

1. Add column and backfill.
2. Persist parent ID for all newly created agent sessions.
3. Switch TUI label/status logic to graph leaves.
4. Keep legacy fallback for rows without graph edges.
5. Remove flat historical interrupted checks where leaf logic replaces them.
