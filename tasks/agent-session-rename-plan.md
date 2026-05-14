# Agent Session Rename Plan

## Goal

Perform the terminology cutover from `domain.Task` / `TaskService` to `domain.AgentSession` / `AgentSessionService` before the event-handler refactor. The current naming is misleading because `TaskPlan` is a sub-plan while `Task` is an agent session; this has repeatedly caused design and implementation confusion.

## Scope

Rename code symbols and tests without changing behavior:

- `domain.Task` → `domain.AgentSession`
- `domain.TaskStatus` → `domain.AgentSessionStatus`
- `domain.TaskPhase` → `domain.AgentSessionPhase`
- `TaskService` → `AgentSessionService`
- task repository symbols that store agent sessions should be renamed consistently where practical.

Do not rename `domain.TaskPlan`; it remains the persisted sub-plan model.

## Rules

1. This is a pure naming cutover. Do not change event semantics, payload shapes, adapter behavior, or TUI dispatch behavior in this plan.
2. Keep database table and migration names stable unless a rename is required by generated code or repository interfaces. Avoid persistence migrations for this terminology-only change.
3. Search the full codebase for old user-facing and internal names before declaring the cutover complete.
4. Keep compatibility aliases only if needed to stage a safe compile; remove them before completing this plan unless an external API requires them.
5. Update tests, mocks, generated mocks, comments, and helper names together so later event work does not need to reason through mixed terminology.

## Files to Modify

Likely areas:

- `internal/domain/session.go`
- `internal/service/session.go`
- `internal/repository/interfaces.go`
- `internal/repository/sqlite/session.go`
- `internal/orchestrator/*`
- `internal/tui/views/*`
- tests and mocks under `internal/**` and `test/**`

## Verification

1. `go test ./internal/domain/`
2. `go test ./internal/service/`
3. `go test ./internal/orchestrator/`
4. `go test ./internal/tui/views/`
5. `go test ./internal/repository/...`
6. `go test ./test/e2e/`
7. `go build ./...`
