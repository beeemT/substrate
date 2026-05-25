# 10 - Foreman Lifecycle Ownership

<!-- docs:last-integrated-commit 5f40bd72111dbaec6c4ea02625679580f6d96c0a -->
<!-- docs:status-updated 2026-05-25 -->
<!-- docs:migration-status COMPLETED -->

> **Status:** Migration complete. All interfaces, orchestrator methods, and event types have been implemented. The TUI no longer directly owns Foreman lifecycle.

For Foreman semantics (two-tier resolution, answer timeout, recovery) see `05-orchestration.md §4`. For the TUI interaction model for questions see `06-tui-design.md`.

---

## 1. Summary

The Foreman lifecycle is now fully owned by the orchestrator layer. The TUI calls orchestrator operations; the orchestrator calls Foreman. The TUI learns Foreman state from DB polls and read-only accessor methods.

### Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                           TUI (Views)                                │
│  - Reads foreman state via SessionRegistry.ForemanReadOnly()        │
│  - Uses AnswerRouter.Answer/Skip for human answers                 │
│  - Uses ReviewFollowup for follow-up operations                    │
└─────────────────────────────────┬───────────────────────────────────┘
                                  │
                                  ▼
┌─────────────────────────────────────────────────────────────────────┐
│                     Orchestrator Layer                               │
│                                                                     │
│  ┌──────────────────────────────────────────────────────────────┐  │
│  │ AnswerRouter (stateless)                                      │  │
│  │  - Answer(questionID, answer, answeredBy)                     │  │
│  │  - Skip(questionID)                                          │  │
│  │  - RefineAnswer(questionID, text)                             │  │
│  │  Uses: SessionRegistry, QuestionSvc, SessionSvc, EventBus      │  │
│  └──────────────────────────────────────────────────────────────┘  │
│                                                                     │
│  ┌──────────────────────────────────────────────────────────────┐  │
│  │ SessionRegistry (interface)                                   │  │
│  │  - Register/Deregister (sessions)                            │  │
│  │  - SendMessage/Steer/SendAnswer                               │  │
│  │  - RegisterForeman/GetForeman (per work item)                │  │
│  └──────────────────────────────────────────────────────────────┘  │
│                                                                     │
│  ┌──────────────────────────────────────────────────────────────┐  │
│  │ QuestionRouter                                               │  │
│  │  - Route(stage, event, sessionID)                             │  │
│  │  - Uses ForemanLifecycle for impl/review questions           │  │
│  └──────────────────────────────────────────────────────────────┘  │
│                                                                     │
│  ┌──────────────────────────────────────────────────────────────┐  │
│  │ ImplementationService                                        │  │
│  │  - BeginForeman(workItemID, planID)                         │  │
│  │  - EndForeman(workItemID)                                   │  │
│  │  - RestartForemanWithPlan(workItemID, planID)               │  │
│  └──────────────────────────────────────────────────────────────┘  │
│                                                                     │
│  ┌──────────────────────────────────────────────────────────────┐  │
│  │ ReviewFollowup                                               │  │
│  │  - FollowUp(workItemID, feedback)                           │  │
│  │  - FollowUpFailed(workItemID, feedback)                     │  │
│  └──────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────┘
                                  │
                                  ▼
┌─────────────────────────────────────────────────────────────────────┐
│                      Foreman Implementation                         │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │ *Foreman (satisfies ForemanLifecycle)                        │   │
│  │  - Start, Stop, IsRunning                                    │   │
│  │  - ResolveEscalated, RefineAnswer                            │   │
│  │  - SessionID, LastSessionID, LastPlanID                      │   │
│  └──────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 2. Implemented Interfaces

### `ForemanLifecycle`

```go
// ForemanLifecycle abstracts Foreman for use by orchestrators that need to
// control its lifecycle. Implemented by *Foreman.
type ForemanLifecycle interface {
    Start(ctx context.Context, planID string, followUpContext string) error
    Stop(ctx context.Context) error
    IsRunning() bool
}
```

Defined in `internal/orchestrator/interfaces.go`.

### `SessionRegistry`

```go
// SessionRegistry abstracts session and foreman registration.
// The concrete implementation is *sessionRegistry; consumers use this interface.
type SessionRegistry interface {
    // Session management
    Register(sessionID string, session adapter.AgentSession)
    Deregister(sessionID string)
    SendMessage(ctx context.Context, sessionID string, msg string) error
    Steer(ctx context.Context, sessionID string, msg string) error
    SendAnswer(ctx context.Context, sessionID string, answer string) error
    IsRunning(sessionID string) bool
    Registered(sessionID string) (adapter.AgentSession, bool)
    AbortAndDeregister(ctx context.Context, sessionID string)

    // Foreman management (per work item)
    RegisterForeman(workItemID string, foreman *Foreman)
    GetForeman(workItemID string) *Foreman
    DeregisterForeman(workItemID string)
}
```

Defined in `internal/orchestrator/interfaces.go`. Concrete implementation in `internal/orchestrator/session_registry.go`.

### `AnswerRouter`

```go
// AnswerRouter routes human answers and skips back to the correct handler
// based on question stage. It delegates to SessionRegistry and *Foreman
// based on the question's phase, looking up the foreman dynamically per question.
type AnswerRouter interface {
    Answer(ctx context.Context, questionID, answer, answeredBy string) error
    Skip(ctx context.Context, questionID string) error
    RefineAnswer(ctx context.Context, questionID, text string) (newProposal string, uncertain bool, err error)
}
```

Implementation in `internal/orchestrator/answer_router.go`.

---

## 3. Domain Events

```go
// Foreman lifecycle events
EventForemanStarted EventType = "foreman.started"
EventForemanStopped EventType = "foreman.stopped"
```

Defined in `internal/domain/event.go`. Published by `Foreman.Start()` and `Foreman.Stop()` respectively.

---

## 4. ImplementationService Lifecycle Methods

```go
// BeginForeman starts a foreman for the work item, tied to the plan.
// Called when implementation starts (from TUI after plan approval, before Implement()).
// Creates a fresh *Foreman instance registered in SessionRegistry.
func (s *ImplementationService) BeginForeman(ctx context.Context, workItemID, planID string) error

// EndForeman stops the foreman for the work item.
// Called when implementation completes or is abandoned.
func (s *ImplementationService) EndForeman(ctx context.Context, workItemID string) error

// RestartForemanWithPlan stops and starts the foreman with the new plan.
// Called after replanning to update foreman's context with new plan/FAQ.
func (s *ImplementationService) RestartForemanWithPlan(ctx context.Context, workItemID, planID string) error
```

Defined in `internal/orchestrator/implementation.go`.

---

## 5. ReviewFollowup Orchestrator

```go
// ReviewFollowup orchestrates a follow-up agent session and the associated
// Foreman lifecycle. It is the single owner of Foreman Start/Stop for
// follow-up sessions.
type ReviewFollowup struct {
    harness     adapter.AgentHarness
    registry    SessionRegistry
    planSvc     *service.PlanService
    questionSvc *service.QuestionService
    sessionSvc  *service.AgentSessionService
    eventBus    event.Publisher
    cfg         *config.Config
}

// FollowUp restarts the foreman with follow-up context.
func (r *ReviewFollowup) FollowUp(ctx context.Context, workItemID, feedback string) error

// FollowUpFailed handles the failed follow-up case.
func (r *ReviewFollowup) FollowUpFailed(ctx context.Context, workItemID, feedback string) error
```

Defined in `internal/orchestrator/review_followup.go`.

---

## 6. TUI Changes

### Services struct

```go
// internal/tui/views/services.go
type Services struct {
    // ... other fields ...
    AnswerRouter   orchestrator.AnswerRouter       // nil if no harness
    ReviewFollowup *orchestrator.ReviewFollowup   // nil if no harness
    SessionRegistry orchestrator.SessionRegistry
    // ...
}
```

### Orchestrated Commands

```go
// internal/tui/views/cmds.go

// BeginForemanOrchestratedCmd calls ImplementationService.BeginForeman.
func BeginForemanOrchestratedCmd(impl *orchestrator.ImplementationService, workItemID, planID string) tea.Cmd

// EndForemanOrchestratedCmd calls ImplementationService.EndForeman.
func EndForemanOrchestratedCmd(impl *orchestrator.ImplementationService, workItemID string) tea.Cmd

// RestartForemanWithPlanOrchestratedCmd calls ImplementationService.RestartForemanWithPlan.
func RestartForemanWithPlanOrchestratedCmd(impl *orchestrator.ImplementationService, workItemID, planID string) tea.Cmd

// FollowUpOrchestratedCmd calls ReviewFollowup.FollowUp.
func FollowUpOrchestratedCmd(rf *orchestrator.ReviewFollowup, workItemID, feedback string) tea.Cmd

// FollowUpFailedOrchestratedCmd calls ReviewFollowup.FollowUpFailed.
func FollowUpFailedOrchestratedCmd(rf *orchestrator.ReviewFollowup, workItemID, feedback string) tea.Cmd

// AnswerQuestionCmd delivers a human answer through AnswerRouter.
func AnswerQuestionCmd(router orchestrator.AnswerRouter, questionID, answer, answeredBy string) tea.Cmd

// SkipQuestionCmd marks a question as skipped through AnswerRouter.
func SkipQuestionCmd(router orchestrator.AnswerRouter, questionID string) tea.Cmd
```

### Removed from TUI

- `foremanPlanID` field from App struct
- `StartForemanCmd`, `StopForemanCmd` (direct foreman access)
- `restartForemanForTask()` method
- `*orchestrator.Foreman` pointer from Services struct

---

## 7. Escalation Channel Cleanup

`Foreman.Stop()` drains and closes any open entries in `escalatedChs` to prevent goroutine leaks for questions that were escalated to humans but not yet resolved when the foreman is stopped.

---

## 8. Acceptance Criteria Verification

| Criterion | Status |
|-----------|--------|
| `*orchestrator.Foreman` not referenced directly from `internal/tui/` | ✅ Verified |
| `App` has no `foremanPlanID` field | ✅ Verified |
| All Foreman Start/Stop calls are in `internal/orchestrator/` | ✅ Verified |
| `ImplementationService.BeginForeman`/`EndForeman`/`RestartForemanWithPlan` | ✅ Implemented |
| `ReviewFollowup` owns follow-up Foreman lifecycle | ✅ Implemented |
| Multiple simultaneous foremen work (one per work item) | ✅ Via SessionRegistry map |
| Replanning restarts foreman with new plan context | ✅ Via RestartForemanWithPlan |
| `EventForemanStarted` and `EventForemanStopped` are published | ✅ Implemented |
| `SessionRegistry` interface used throughout orchestrator | ✅ Verified |
| `AnswerRouter` correctly routes answers | ✅ Implemented |
| `escalatedChs` drained and closed on `Foreman.Stop()` | ✅ Implemented |
| `go build ./...` passes | ✅ Verified |
| `go test ./internal/orchestrator/...` passes | ✅ 251 tests pass |

---

## 9. Naming Note

`orchestrator.ReviewFollowup` (orchestrator type) and `views.ReviewFollowupModel` (TUI overlay) share a root name but live in different packages, so there is no actual collision.
