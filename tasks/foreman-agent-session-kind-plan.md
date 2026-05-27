# Foreman Agent Session Kind Plan

## Problem

Accepting an implementation plan starts the Foreman and shows a success toast, but the Foreman does not reliably appear in the Tasks sidebar. Today the Foreman is tracked as an in-memory runtime object and as `foreman.started` / `foreman.stopped` events, while normal child agent sessions are persisted in `agent_sessions` and loaded into the sidebar from the database.

The current `agent_sessions.phase` name is also misleading. The field is used as the persisted classification of an agent session (`planning`, `implementation`, `review`, `manual`), not strictly as a temporal phase. Foreman should be one of those persisted classifications.

## Current Behavior

Normal planning, implementation, review, and manual sessions are persisted as `domain.AgentSession` rows. Each row has a `phase` value, lifecycle status, IDs, timestamps, and log path derivable from the session ID.

Foreman differs today:

1. `Foreman.Start()` starts an `adapter.AgentSession` with `adapter.SessionModeForeman`.
2. The live handle is stored on `Foreman.session`.
3. `ImplementationService.BeginForeman()` registers the `*Foreman` in `SessionRegistry` by work item ID.
4. The Foreman emits `foreman.started` / `foreman.stopped` events.
5. No `domain.AgentSession` row is created for the Foreman.

That makes the registry the source of runtime reachability, but leaves no durable agent-session row for UI/history. The TUI compensates with a virtual `foremanSessions` map and plan-matching logic; that is fragile and should go away.

## Target Model

Rename persisted agent-session classification from `phase` to `kind`.

```go
type AgentSessionKind string

const (
    AgentSessionKindPlanning       AgentSessionKind = "planning"
    AgentSessionKindImplementation AgentSessionKind = "implementation"
    AgentSessionKindReview         AgentSessionKind = "review"
    AgentSessionKindManual         AgentSessionKind = "manual"
    AgentSessionKindForeman        AgentSessionKind = "foreman"
)

type AgentSession struct {
    Kind AgentSessionKind
}
```

Foreman becomes a first-class persisted `domain.AgentSession`:

```go
domain.AgentSession{
    ID:             foremanSessionID,
    WorkItemID:     plan.WorkItemID,
    WorkspaceID:    workItem.WorkspaceID,
    Kind:           domain.AgentSessionKindForeman,
    PlanID:         plan.ID,
    SubPlanID:      "",
    RepositoryName: "Foreman",
    WorktreePath:   "",
    HarnessName:    foremanHarness.Name(),
    Status:         domain.AgentSessionRunning,
}
```

The harness runtime still uses:

```go
adapter.SessionModeForeman
```

The `foreman.started` / `foreman.stopped` events use `domain.ForemanEventPayload`:

```go
type ForemanEventPayload struct {
    WorkItemID    string  // owning work item
    PlanID        string  // current plan ID for this foreman lifecycle
    SessionID     string  // current (live) session ID while running
    LastPlanID    string  // set on stop; the plan ID when session ended
    LastSessionID string  // set on stop; the session ID that ended
}
```

Meaning:

- `AgentSession.Kind` describes what the persisted child session is.
- `adapter.SessionMode` describes how the harness process is launched.
- `SessionRegistry` remains runtime reachability, not UI/history truth.

## Persistence Plan

Add a migration that rebuilds `agent_sessions`:

- rename current `phase` column to `kind`
- expand the CHECK constraint to include `foreman`
- copy existing rows with `kind = phase`
- preserve all existing indexes and foreign keys

Current valid values become:

```sql
CHECK (kind IN ('planning','implementation','review','manual','foreman'))
```

Update repository/domain mapping:

- `domain.AgentSession.Phase` -> `domain.AgentSession.Kind`
- `domain.AgentSessionPhase` -> `domain.AgentSessionKind`
- sqlite row field `Phase` -> `Kind`
- SQL INSERT/UPDATE/SELECT references `kind`
- event payload fields should emit `kind`, not `phase`, where the field refers to persisted agent-session classification

### Call Sites to Update

Rename all references from `Phase`/`AgentSessionPhase` to `Kind`/`AgentSessionKind` across:

| File | Change |
|------|---------|
| `internal/domain/session.go` | Type declaration and struct field |
| `internal/domain/question.go` | `Question.Stage` field type |
| `internal/repository/sqlite/session.go` | `toDomain()`, `rowFromAgentSession()`, `Get()`, `ListByWorkItemID()` |
| `internal/repository/sqlite/question.go` | `toDomain()`, `rowFromQuestion()` (DB column `stage` stays; domain field maps to `Kind`) |
| `internal/service/session.go` | `Resume()`, `Create()`, `marshalAgentSessionPayload()` JSON field |
| `internal/service/question.go` | any `AgentSessionPhase` references |
| `internal/orchestrator/question_router.go` | switch cases on `AgentSessionPhase`; add `AgentSessionKindForeman` case that returns an error |
| `internal/orchestrator/manual.go` | `forwardEvents()` passes `AgentSessionKindManual` |
| `internal/orchestrator/foreman.go` | any `AgentSessionPhase` references |
| `internal/tui/views/` | any sidebar or session rendering that reads `Phase` |

> **Note:** `Question.Stage` field name may stay as-is (`Stage`) — it describes routing terminology ("what stage asked this question?"), not the persisted column name. Rename only the type, not the field, unless UX copy is updated too.

Historical migrations may retain `phase` because they describe schema history. Current code should use `kind`.

## Foreman Lifecycle

### No Foreman Ever Started

Do not show a Foreman group in the Tasks sidebar.

### Start

`Foreman.Start()` should:

1. Load the plan.
2. Load the owning work item to get `WorkspaceID`.
3. If this Foreman already has a persisted Foreman session row, reuse that row ID; otherwise allocate a new session ID and create a pending `AgentSessionKindForeman` row.
4. Start the harness with the same session ID and `adapter.SessionModeForeman`.
5. Transition the persisted row to `running` via `AgentSessionService.Start`.
6. Store the live adapter handle on `Foreman.session`.
7. Publish normal agent-session lifecycle events through the service layer.
8. Keep `RegisterForeman(workItemID, foreman)` for question routing.

If harness launch fails after the row exists, mark the row `failed`.

### Running

The Tasks sidebar should show the Foreman from persisted `agent_sessions` state:

```text
Foreman
  Foreman session abc123   Running
```

Selecting it should:

- open `<sessionsDir>/abc123.log`
- show `Running · Foreman`
- tail the live log
- show the active spinner

### Normal Stop

When implementation completes and the Foreman is no longer needed, transition the persisted row to `completed`.

```text
Foreman
  Foreman session abc123   Completed
```

The log remains selectable and readable.

### Cancellation / App Shutdown / Archive

If the Foreman is stopped because the owning work item or app pipeline is interrupted before normal completion, transition the row to `interrupted`.

### Harness Failure

If the Foreman harness process exits unexpectedly, transition the row to `failed`.

This requires a watcher around the Foreman adapter session so the persisted lifecycle cannot remain `running` after the process dies.

### Follow-up / Restart

Follow-up and Foreman restart should **not create a new row**.

The existing Foreman `AgentSessionKindForeman` row represents the Foreman for that work item/plan lifecycle. On restart:

1. Preserve the same persisted `AgentSession.ID`.
2. Abort/stop the current harness process if one exists.
3. Relaunch the Foreman harness with the **same `SessionID`** and updated context.
4. Keep or transition the row back to `running` as needed.
5. Continue writing to the same session log path.

This gives the operator one continuous Foreman session in the sidebar while still allowing the runtime process to be relaunched with updated plan/FAQ/follow-up context.

> **⚠️ Both `Start()` and `restartSession()` must reuse the persisted session ID.**
> Today `restartSession()` at `orchestrator/foreman.go:~395` calls `domain.NewID()`.
> After this change, both must read from the stored `AgentSession.ID` and pass it to the harness.

## Sidebar Rules

The Tasks sidebar should be built from persisted `domain.AgentSession` rows.

Foreman grouping:

```go
case domain.AgentSessionKindForeman:
    group = "Foreman"
    title = "Foreman session " + shortSessionID(agentSession.ID)
    modeLabel = "Foreman"
```

The Foreman row status comes from `AgentSession.Status`, not TUI-local event cache.

Remove or shrink TUI-only Foreman state:

- `App.foremanSessions` — field at `internal/tui/views/app.go:~152` keyed by `workItemID`
- `foremanSessionState` — struct at `internal/tui/views/msgs.go:~134`; can be deleted once no longer referenced
- virtual `taskSidebarForemanID`
- `runningForPlan`
- Foreman-specific sidebar visibility checks based on plan/event matching

Prefer normal `showTaskContent` for Foreman if it can render the Foreman log with the correct title/meta. If a special renderer remains, it should consume the persisted `domain.AgentSession`, not virtual event state.

## Event Model

UI visibility should depend on normal agent-session lifecycle events:

- `agent_session.started`
- `agent_session.completed`
- `agent_session.failed`
- `agent_session.interrupted`
- `agent_session.resumed` if restart uses a running transition from an interrupted state

`foreman.started` / `foreman.stopped` may remain as semantic orchestration events, but they should not be the source of sidebar truth.

The TUI should reload/upsert the persisted Foreman session through the same paths as other agent sessions.

## Question Routing

Question routing currently uses `AgentSessionPhase` values as a stage discriminator. During the rename:

- change the type to `AgentSessionKind`
- decide whether the field name `Question.Stage` remains acceptable as routing terminology
- route `planning` questions to the planner
- route `implementation` and `review` questions through Foreman/sub-agent answer flow
- route `manual` questions through manual session handling
- **`foreman` questions must be rejected.** Add a `case AgentSessionKindForeman:` branch in `QuestionRouter.Route()` that returns an error. A foreman sub-agent asking foreman routing is a misconfiguration; the error makes it visible rather than silent.

## Verification Plan

Add focused tests for:

1. Migration preserves existing agent sessions and renames `phase` to `kind`.
2. Foreman start creates or reuses one `AgentSessionKindForeman` row.
3. Foreman appears in the Tasks sidebar from persisted session state after reload.
4. Foreman running/completed/interrupted/failed states render correctly.
5. Follow-up/restart reuses the same Foreman row, same session ID, and same log path.
6. `restartSession()` (harness failure recovery) reuses the persisted session ID, not `domain.NewID()`.
7. `foreman` kind questions are rejected by `QuestionRouter.Route()`.
8. Normal implementation/review/manual/planning behavior still groups and routes correctly after `phase` -> `kind` rename.

Run the affected package tests plus full repository verification after implementation.
