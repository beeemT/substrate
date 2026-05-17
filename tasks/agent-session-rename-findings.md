# Agent Session Rename Findings

## Summary

The renaming from `domain.Task` → `domain.AgentSession` is **largely complete** at the domain/service/repository layers. The remaining work is confined to the TUI message type field names.

**Important:** The TUI's "Tasks" view / "Tasks" pane is a **product-level grouping concept** — agent sessions together with virtual nodes (Source Details, Artifacts, Foreman session). The `taskSidebar*` functions, `LoadTasksCmd`, `Task()` service provider method, `sidebarPaneTasks`, and `"Task"` labels are all product terminology and **should NOT be renamed**. Only the message type field `Task domain.AgentSession` is a domain-level remnant that should be renamed.

## Completed Renames ✓

### Domain Layer
- `domain.Task` → `domain.AgentSession`
- `domain.TaskStatus` → `domain.AgentSessionStatus`
- `domain.TaskPhase` → `domain.AgentSessionPhase`
- Table `agent_sessions` unchanged (no migration needed)

### Service Layer
- `TaskService` → `AgentSessionService`

### Repository Layer
- `AgentSessionRepository` interface (already correctly named)
- `NewAgentSessionRepo` (already correctly named)

### TUI Product Layer (no change needed)
- `LoadTasksCmd`, `LoadTasksForSessionCmd`, `ReconcileOrphanedTasksCmd` — product concept names
- `Task() *service.AgentSessionService` — product accessor for the agent sessions service
- `Services.Task` field — product struct field
- `taskSidebar*` functions and constants — product view/UI concept names
- `"Task"` labels in sidebar — product terminology

## Actual Remaining Work

### 1. Message type field names

All message types that carry an agent session embed it with field name `Task`:

| File | Message Type | Field |
|------|-------------|-------|
| `msgs.go` | `SessionStartedMsg` | `Task domain.AgentSession` |
| `msgs.go` | `SessionUpdatedMsg` | `Task domain.AgentSession` |
| `msgs.go` | `TaskStartedMsg` | `Task domain.AgentSession` |
| `msgs.go` | `TaskUpdatedMsg` | `Task domain.AgentSession` |
| `msgs.go` | `SessionResumedMsg` | `Task domain.AgentSession` |
| `msgs.go` | `FollowUpFromReviewMsg` | `Task domain.AgentSession` |

**Rename:** `Task domain.AgentSession` → `AgentSession domain.AgentSession`

This is the only domain-level remnant left. All call sites (App.Update handlers, event_consumer.go) need the field access updated from `msg.Task` to `msg.AgentSession`.

### 2. Event consumer Task field references

`internal/tui/views/event_consumer.go` — all `p.Session` assignments to `Task:` fields.

### 3. App.Update handler field references

`internal/tui/views/app.go` — all `msg.Task` accesses in `TaskStartedMsg`/`TaskUpdatedMsg`/`SessionStartedMsg`/`SessionUpdatedMsg` handlers.

### 4. Tests

- `event_consumer_task_test.go` — `typed.Task.ID` etc.
- `event_session_test.go` — service setup (`Task:`)
- `app_history_test.go` — service setup (`Task:`)
- `overview_test.go` — assertion checking for `"Task Session"` prefix (already correct — the test verifies "Session" without "Task" prefix, confirming the product label is clean)

### 5. Orchestrator prompt headings (optional)

| File | Line | Content |
|------|------|---------|
| `implementation.go` | 980 | `# Task` → `# Session` |
| `review.go` | 305 | `## Task` → `## Review` |

These are user-facing prompt headings. The implementation prompt could read `# Session` or `# Implementation`; the review prompt could read `# Review` or `## Review Session`.

## Files Requiring Changes

1. `internal/tui/views/msgs.go` — field `Task` → `AgentSession`
2. `internal/tui/views/event_consumer.go` — `Task:` field assignments
3. `internal/tui/views/app.go` — `msg.Task` → `msg.AgentSession`
4. `internal/tui/views/event_consumer_task_test.go` — `typed.Task` → `typed.AgentSession`
5. `internal/tui/views/event_session_test.go` — `Task:` field
6. `internal/tui/views/app_history_test.go` — `Task:` field
7. `internal/orchestrator/implementation.go` — prompt heading (optional)
8. `internal/orchestrator/review.go` — prompt heading (optional)

## Documentation Notes

The docs reference the old `Task` domain model. However, since `domain.Task` was renamed to `domain.AgentSession`, the docs now describe a type that no longer exists. They need updating to reflect `domain.AgentSession`.

This is low priority — the docs are accurate about the architecture but use stale type names.

## Verification Commands
```bash
go test ./internal/domain/
go test ./internal/service/
go test ./internal/orchestrator/
go test ./internal/tui/views/
go test ./internal/repository/...
go build ./...
```
