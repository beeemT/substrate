# 10 - Foreman Lifecycle Ownership

<!-- docs:last-integrated-commit 5f40bd72111dbaec6c4ea02625679580f6d96c0a -->
<!-- docs:status-updated 2026-05-01 -->
<!-- docs:migration-status NOT_STARTED -->
<!-- docs:naming-collision Note: orchestrator.ReviewFollowup (new) collides with views.ReviewFollowupModel (existing TUI overlay). Rename the orchestrator type to avoid confusion. -->

The Foreman is the persistent harness session that handles unresolved questions during implementation. This document covers the ownership boundary between orchestrator and TUI.

> **Status:** Migration pending. None of the interfaces, orchestrator methods, or event types described in this document have been implemented. The TUI still directly owns Foreman lifecycle. See §1 (Current State) and §7 (Migration Steps) for the planned cutover.

For Foreman semantics (two-tier resolution, answer timeout, recovery) see `05-orchestration.md §4`. For the TUI interaction model for questions see `06-tui-design.md`.

---

## 1. Current State

The `Foreman` type lives in `internal/orchestrator/` but the TUI holds a direct `*orchestrator.Foreman` pointer and calls it at seven lifecycle sites:

```
TUI                           Foreman
──────────────────────────────────────────────────────
StartForemanCmd()            Start(planID, followUpContext)
StopForemanCmd()             Stop()
restartForemanForTask()       Start()/Stop()
teardownAllPipelines()        Stop()
AnswerQuestionCmd()           ResolveEscalated(questionID, answer)
SkipQuestionCmd()            ResolveEscalated(questionID, "")
App.foremanPlanID (field)     tracks active plan
```

`ImplementationService` holds `foreman *Foreman` but **never calls it** — the field is forwarded only to `QuestionRouter`. The service that runs the implementation pipeline does not own the Foreman lifecycle.

### Consequences

1. **Racing lifecycle** — TUI and orchestrator can both influence Foreman state. TUI starts/stops at points that may not align with implementation wave boundaries.
2. **Orphaned field** — `ImplementationService.foreman` is a dead reference; the service cannot coordinate Foreman lifecycle with its own wave execution.
3. **TUI state duplication** — `foremanPlanID string` in `App` tracks what plan the Foreman is running for, duplicating knowledge the orchestrator already holds.
4. **Follow-up boundary violation** — Follow-up sessions are driven by the TUI calling `restartForemanForTask()`, breaking the `ImplementationService` boundary.
5. **Test coupling** — TUI handlers that test Foreman interaction must construct or mock `*Foreman` directly.

---

## 1b. Current State Verification (as of 2026-05-01)

Confirmed at these exact locations:

### TUI holds `*orchestrator.Foreman` directly
- `internal/tui/views/services.go:38` — `Foreman *orchestrator.Foreman`
- `internal/tui/views/cmds.go:1225` — `StartForemanCmd(foreman *orchestrator.Foreman, ...)`
- `internal/tui/views/cmds.go:1283` — `StopForemanCmd(foreman *orchestrator.Foreman)`
- `internal/tui/views/app.go:1666–1669` — `StartForemanCmd` dispatched on `PlanApprovedMsg`
- `internal/tui/views/app.go:1714–1719` — `StopForemanCmd` dispatched on `FollowUpDoneMsg`
- `internal/tui/views/app.go:1858–1863` — `StopForemanCmd` on `WorkItemUpdatedMsg`
- `internal/tui/views/app.go:1900–1920` — `StartForemanCmd` on `PlanReImplementsMsg` / `PlanRetryImplementsMsg`
- `internal/tui/views/app.go:2295–2301` — `StopForemanCmd` on `ImplementationCompleteMsg`
- `internal/tui/views/app.go:3047–3111` — `restartForemanForTask()` and `teardownAllPipelines()`
- `internal/tui/views/app.go:3336–3339` — Sidebar reads `SessionID()`, `LastPlanID()`, `LastSessionID()`

### Interfaces do not exist
`ForemanLifecycle`, `ForemanReadOnly`, `ForemanAnswerer` are not present anywhere in the codebase.

### Events do not exist
`EventForemanStarted`, `EventForemanStopped` are not present in `internal/domain/event.go`.

### `ImplementationService.foreman` is the orphaned field
- `internal/orchestrator/implementation.go` — `foreman *Foreman` field; never called in that file
- `internal/orchestrator/question_router.go` — `foreman *Foreman` field; used for restart-on-failure

### `ReviewFollowup` does not exist
No `ReviewFollowup` type in `internal/orchestrator/`. The TUI overlay named `ReviewFollowupModel` is unrelated (UI component for review comments, not lifecycle ownership).

### `foremanPlanID` is present
- `internal/tui/views/app.go:197` — `foremanPlanID string` field on `App`

### `BeginForeman`/`EndForeman`/`FollowUp` do not exist
No lifecycle methods on `ImplementationService`.

### Summary of required work
The migration is entirely unstarted. All five phases need to be implemented from scratch.

---

## 2. Non-Goals

- Do not change the `Foreman`'s internal behavior (question routing, FAQ appending, escalation logic, session restart).
- Do not change the `QuestionRouter` routing logic.
- Do not change the `Bus` event flow or which events are persisted.
- Do not change the `event.Bus` semantics (pre-hook vs regular events).

---

## 3. Target State

### Principle

The orchestrator owns Foreman lifecycle. The TUI calls orchestrator operations; the orchestrator calls Foreman. The TUI learns Foreman state from DB polls and read-only accessor methods.

### New orchestrator interfaces

Three interfaces isolate the three concerns: lifecycle control, read-only state, and question resolution.

#### `ForemanLifecycle`

```go
// ForemanLifecycle abstracts Foreman for use by orchestrators that need to
// control its lifecycle. Implemented by *Foreman.
type ForemanLifecycle interface {
    Start(ctx context.Context, planID string, followUpContext string) error
    Stop(ctx context.Context) error
    IsRunning() bool
    LastSessionID() string
    LastPlanID() string
}
```

Implemented by `*Foreman` with trivial adapter methods.

#### `ForemanReadOnly`

```go
// ForemanReadOnly exposes read-only access to the Foreman for UI components
// that need to render session state (sidebar, log display).
type ForemanReadOnly interface {
    SessionID() string
    IsRunning() bool
    LastSessionID() string
    LastPlanID() string
}
```

Implemented by `*Foreman` (same methods as `ForemanLifecycle`, minus `Start`/`Stop`).

#### `ForemanAnswerer`

```go
// ForemanAnswerer resolves escalated questions through the Foreman.
// Separated from lifecycle so the TUI can call ResolveEscalated
// without owning lifecycle.
type ForemanAnswerer interface {
    ResolveEscalated(ctx context.Context, questionID, answer string) error
    SendUserMessage(ctx context.Context, questionID, text string) (newProposal string, uncertain bool, err error)
}
```

`QuestionRouter` holds `ForemanLifecycle` so it can restart the Foreman on failure. `ImplementationService` injects the concrete `*Foreman`.

### `ImplementationService` gains lifecycle methods

```go
// BeginForeman starts the Foreman for the given plan.
// Called internally at the start of Implement(). Safe to call idempotently.
func (s *ImplementationService) BeginForeman(ctx context.Context, planID string) error

// EndForeman stops the Foreman. Called when implementation completes or is abandoned.
func (s *ImplementationService) EndForeman(ctx context.Context) error

// FollowUp restarts the Foreman with follow-up context.
// Wraps: Stop() if running → Start(planID, feedback).
func (s *ImplementationService) FollowUp(ctx context.Context, taskID string, feedback string) error
```

`Implement()`, `RetryFailed()`, and `FinalizeWorkItem()` call `BeginForeman()` internally at the appropriate point. `EndForeman()` is called when the implementation run reaches a terminal state or when the session is abandoned.

`BeginForeman()` and `EndForeman()` publish `EventForemanStarted` and `EventForemanStopped` respectively, so adapters and other listeners can track Foreman lifecycle without the TUI tracking it imperatively.

### New event types

```go
// Foreman lifecycle events
EventForemanStarted  EventType = "foreman.started"
EventForemanStopped  EventType = "foreman.stopped"
```

These are regular (post-persistence) events. `Foreman.Start()` publishes `EventForemanStarted` after starting the session. `Foreman.Stop()` publishes `EventForemanStopped` before returning. The `planID` is included in the payload so subscribers can correlate with the active plan.

These events replace the need for `App.foremanPlanID` as an imperative tracking field.

### `ReviewFollowup` orchestrator

A new orchestrator type owns the follow-up session lifecycle:

```go
// ReviewFollowup orchestrates a follow-up agent session and the associated
// Foreman lifecycle. It is the single owner of Foreman Start/Stop for
// follow-up sessions.
type ReviewFollowup struct {
    harness    adapter.AgentHarness
    foreman    ForemanLifecycle
    sessionSvc *service.TaskService
    planSvc    *service.PlanService
    registry   *SessionRegistry
    eventBus   *event.Bus
}

func NewReviewFollowup(...) *ReviewFollowup
func (r *ReviewFollowup) FollowUp(ctx context.Context, taskID, feedback string) error
func (r *ReviewFollowup) FollowUpFailed(ctx context.Context, taskID, feedback string) error
```

This replaces the TUI's `restartForemanForTask()` method and the combination of `FollowUpSessionCmd` / `FollowUpFailedSessionCmd`. The TUI calls one orchestrator method; the orchestrator handles Stop + Start internally.

### TUI changes

#### `Services` struct

```go
// Before
Foreman *orchestrator.Foreman

// After
ForemanReadOnly orchestrator.ForemanReadOnly  // nil if no harness
ReviewFollowup  *orchestrator.ReviewFollowup  // nil if no harness
```

`ForemanReadOnly` is the read-only view for sidebar rendering. `ReviewFollowup` is the entry point for follow-up actions.

#### `App` struct

```go
// Remove: foremanPlanID string
// Remove: all svcs.Foreman != nil guards for lifecycle calls
// Keep: svcs.ForemanReadOnly.SessionID() reads for sidebar rendering
```

#### `cmds.go`

- Remove `StartForemanCmd`, `StopForemanCmd`
- `AnswerQuestionCmd` and `SkipQuestionCmd` take `ForemanAnswerer` instead of `*Foreman`
- New cmd constructors that delegate to orchestrator:

```go
// Ends Foreman and returns nil (TUI learns state change via poll)
func EndForemanCmd(svc *orchestrator.ImplementationService) tea.Cmd

// Calls ReviewFollowup.FollowUp
func FollowUpOrchestratedCmd(ctx context.Context, rf *orchestrator.ReviewFollowup, taskID, feedback string) tea.Cmd

// Calls ReviewFollowup.FollowUpFailed
func FollowUpFailedOrchestratedCmd(ctx context.Context, rf *orchestrator.ReviewFollowup, taskID, feedback string) tea.Cmd
```

`BeginForeman()` is called internally by `ImplementationService.Implement()`, so no TUI-side cmd is needed for starting.

#### Sidebar rendering

Sidebar reads `svcs.ForemanReadOnly.SessionID()` and `svcs.ForemanReadOnly.LastPlanID()` to render the Foreman group header. No lifecycle calls.

---

## 4. Current Call Sites (before migration)

### TUI (`internal/tui/views/`)

| File | Symbol | Foreman method called |
|---|---|---|
| `cmds.go` | `StartForemanCmd` | `Start(ctx, planID, followUpContext)` |
| `cmds.go` | `StopForemanCmd` | `Stop(ctx)` |
| `cmds.go` | `AnswerQuestionCmd` | `ResolveEscalated(ctx, questionID, answer)` |
| `cmds.go` | `SkipQuestionCmd` | `ResolveEscalated(ctx, questionID, "")` |
| `app.go` | `restartForemanForTask` | `IsRunning()`, `Stop(ctx)`, `Start(ctx, planID, feedback)` |
| `app.go` | `teardownAllPipelines` | `Stop(ctx)` |

### Orchestrator (`internal/orchestrator/`)

| File | Field / Symbol | Note |
|---|---|---|
| `implementation.go` | `foreman *Foreman` field | Never called; forwarded to `QuestionRouter` |
| `question_router.go` | `foreman *Foreman` field | Used for restart on failure |

---

## 5. Files to Change

| File | Change |
|---|---|
| `internal/domain/event.go` | Add `EventForemanStarted`, `EventForemanStopped` |
| `internal/orchestrator/foreman.go` | Publish events in `Start()`/`Stop()`; add interface implementations |
| `internal/orchestrator/implementation.go` | Add `BeginForeman`, `EndForeman`, `FollowUp`; change `foreman` field type to `ForemanLifecycle` |
| `internal/orchestrator/review_followup.go` | New file — `ReviewFollowup` type |
| `internal/orchestrator/question_router.go` | Hold `ForemanLifecycle` interface instead of `*Foreman` |
| `internal/tui/views/services.go` | `Foreman *orchestrator.Foreman` → `ForemanReadOnly orchestrator.ForemanReadOnly`; add `ReviewFollowup` |
| `internal/tui/views/cmds.go` | Remove `StartForemanCmd`, `StopForemanCmd`; add orchestrator delegation cmds; update answerer signatures |
| `internal/tui/views/app.go` | Remove `foremanPlanID`; update all lifecycle call sites; update sidebar reads |
| `internal/tui/views/settings_service.go` | Wire `ForemanReadOnly`, `ReviewFollowup` into `Services` |

---

## 6. Acceptance Criteria

- [ ] `Foreman` is not referenced directly from `internal/tui/`
- [ ] `App` has no `foremanPlanID` field
- [ ] All Foreman Start/Stop calls are in `internal/orchestrator/`
- [ ] `ImplementationService` calls `BeginForeman`/`EndForeman` around its implementation runs
- [ ] `ReviewFollowup` owns follow-up Foreman lifecycle
- [ ] `AnswerQuestionCmd` and `SkipQuestionCmd` use `ForemanAnswerer` (not `*Foreman`)
- [ ] Sidebar renders Foreman session correctly via `ForemanReadOnly`
- [ ] `EventForemanStarted` and `EventForemanStopped` are published
- [ ] `go build ./...` passes
- [ ] `go test ./internal/orchestrator/...` passes
- [ ] Manual smoke test: approve plan → Foreman starts → implementation runs → Foreman stops → follow-up → Foreman restarts

---

## 7. Migration Steps

### Phase 1 — Add interfaces, methods, and events (no behavioral change)

1. Add `ForemanLifecycle`, `ForemanReadOnly`, and `ForemanAnswerer` interfaces to `internal/orchestrator/`.
2. Verify `*Foreman` satisfies all three interfaces (it will, with no code changes).
3. Add `EventForemanStarted` and `EventForemanStopped` to `internal/domain/event.go`.
4. Update `Foreman.Start()` and `Foreman.Stop()` to publish these events.
5. Add `BeginForeman`, `EndForeman`, and `FollowUp` to `ImplementationService` with **no-op implementations** that call the existing `foreman.Start()`/`Stop()` directly. This preserves existing behavior while establishing the new call sites.
6. Add `orchestrator.ReviewFollowup` with `FollowUp`/`FollowUpFailed` methods that call the existing TUI restart logic (Stop + Start). This establishes the new type without changing behavior.
7. Wire `ReviewFollowup` into `Services`.

**Verification**: `go build ./...` passes. All existing tests pass. No TUI behavior changes.

### Phase 2 — Migrate lifecycle calls from TUI to orchestrator

1. Remove `StartForemanCmd`, `StopForemanCmd` from `cmds.go`.
2. Update `AnswerQuestionCmd`, `SkipQuestionCmd` to take `ForemanAnswerer` instead of `*Foreman`.
3. Update all TUI `Update()` cases: replace `StartForemanCmd(svcs.Foreman, ...)` with `BeginForemanCmd(svcs.Implementation, ...)` (only if not already called internally by `Implement()`), `StopForemanCmd(svcs.Foreman)` with `EndForemanCmd(svcs.Implementation)`.
4. Replace `a.restartForemanForTask(...)` calls with `FollowUpOrchestratedCmd(svcs.ReviewFollowup, ...)`.
5. Replace `a.restartForemanFailedForTask(...)` calls with `FollowUpFailedOrchestratedCmd(svcs.ReviewFollowup, ...)`.
6. Update `teardownAllPipelines()` to call `EndForemanCmd` instead of `svcs.Foreman.Stop()` directly.
7. Remove `App.foremanPlanID` field and all guards `if svcs.Foreman != nil { ... StartForemanCmd ... }`.
8. Remove `restartForemanForTask()` and `restartForemanFailedForTask()` from `App`.

**Verification**: `go build ./...`. Run the app through plan approval → implementation → review → follow-up. Foreman session appears in sidebar and is correctly started/stopped by the orchestrator.

### Phase 3 — Remove `Foreman` from `Services`, use `ForemanReadOnly`

1. Remove `Foreman *orchestrator.Foreman` from `Services`.
2. Add `ForemanReadOnly orchestrator.ForemanReadOnly` to `Services`.
3. In `settings_service.go`, when wiring: `Services.ForemanReadOnly = foreman` (where `foreman` is the concrete `*orchestrator.Foreman`).
4. Update all `svcs.Foreman != nil` guards in the TUI to `svcs.ForemanReadOnly != nil`.
5. Update `app.go` sidebar block to use `svcs.ForemanReadOnly`.

**Verification**: `go build ./...`. Run the app. Sidebar renders Foreman session log correctly.

### Phase 4 — Inject `ForemanLifecycle` interface into `ImplementationService` and `QuestionRouter`

1. Change `NewImplementationService` signature: `foreman *Foreman` → `foreman ForemanLifecycle`.
2. Update all call sites in `settings_service.go`.
3. Update `QuestionRouter` to hold `ForemanLifecycle` instead of `*Foreman`.

**Verification**: `go test ./internal/orchestrator/...`

### Phase 5 — Wire event publishing into `BeginForeman`/`EndForeman`/`FollowUp`

1. `BeginForeman()` publishes `EventForemanStarted`.
2. `EndForeman()` publishes `EventForemanStopped`.
3. `FollowUp()` publishes `EventForemanStopped` then `EventForemanStarted` in sequence.

This ensures adapters that need to react to Foreman lifecycle have a bus signal.
