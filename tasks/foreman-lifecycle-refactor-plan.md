# Foreman Lifecycle Refactor Plan

**Status:** Not Started  
**Target:** Fix foreman lifecycle ownership - move from TUI to orchestrator layer  
**Created:** 2026-05-22

## Problem Statement

The Foreman lifecycle is currently distributed across TUI and services, which violates the architectural principle that the orchestrator layer should own session lifecycle.

### Current Problems

1. **TUI owns lifecycle**: TUI (`app.go`) directly calls `StartForemanCmd`, `StopForemanCmd`, `restartForemanForTask`
2. **Duplicate tracking**: `App.foremanPlanID` duplicates plan tracking already in orchestrator
3. **Two foreman instances**: The codebase has two separate foreman instances with different lifecycles:
   - `Services.Foreman` (service_manager.go:314-316): Single global instance, never started/stopped by orchestrator, used only by TUI for `AnswerQuestionCmd`/`SkipQuestionCmd`
   - `ImplementationService.foreman`: Created fresh per `Implement()` call for per-session question routing
4. **No proper abstraction**: TUI holds concrete `*orchestrator.Foreman` pointer
5. **Per-session foreman in ImplementationService**: Creates fresh foreman per `Implement()` call; foreman lifecycle is not externally managed
6. **Replan doesn't update foreman**: When plan changes, foreman retains stale context

### Current Call Sites in TUI

| Location | Operation | Trigger |
|----------|-----------|---------|
| `app.go:1968-1969` | Start Foreman | `PlanApprovedMsg` |
| `app.go:2018-2019` | Stop Foreman | `FollowUpSessionCompleteMsg` |
| `app.go:1658-1660` | Stop Foreman | `WorkItemCompletedMsg` |
| `app.go:2172-2174` | Stop Foreman | `DeleteSessionMsg` |
| `app.go:2247-2248` | Start Foreman | `ReimplementMsg` |
| `app.go:2263-2264` | Start Foreman | `RetryFailedMsg` |
| `app.go:1999,2008` | Restart Foreman | `FollowUpSessionMsg`, `FollowUpFailedSessionMsg` |
| `app.go:3538-3542` | Stop Foreman | `teardownAllPipelines` (app shutdown) |
| `cmds.go:1244-1258` | Start Foreman | `StartForemanCmd` function |
| `cmds.go:1301-1315` | Stop Foreman | `StopForemanCmd` function |

---

## Target Architecture

### Principles

1. **Orchestrator owns lifecycle**: All Foreman Start/Stop calls move to `internal/orchestrator/`
2. **One foreman per work item**: Each work item implementing simultaneously has its own foreman instance
3. **Foreman survives follow-ups**: Foreman is tied to work item lifecycle, not individual implementation runs
4. **Replan updates context**: After replanning, foreman is restarted with new plan/FAQ context
5. **Interface abstraction**: Foreman is behind interfaces; no concrete pointers in TUI

### Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────────┐
│                           TUI (Views)                                │
│  - Reads foreman state via ForemanReadOnly (SessionRegistry)       │
│  - Uses AnswerRouter.Answer/Skip for human answers                 │
│  - Uses ReviewFollowup for follow-up operations                    │
└─────────────────────────────────┬───────────────────────────────────┘
                                  │
                                  ▼
┌─────────────────────────────────────────────────────────────────────┐
│                     Orchestrator Layer                               │
│                                                                     │
│  ┌──────────────────────────────────────────────────────────────┐  │
│  │ AnswerRouter (stateless)                                     │  │
│  │  - Answer(questionID, answer, answeredBy)                    │  │
│  │  - Skip(questionID)                                         │  │
│  │  - RefineAnswer(questionID, text)                            │  │
│  │  Uses: SessionRegistry, QuestionSvc, SessionSvc, EventBus      │  │
│  └──────────────────────────────────────────────────────────────┘  │
│                                                                     │
│  ┌──────────────────────────────────────────────────────────────┐  │
│  │ SessionRegistry (interface)                                    │  │
│  │  - Register/Deregister (sessions)                             │  │
│  │  - SendMessage/Steer/SendAnswer                              │  │
│  │  - RegisterForeman/GetForeman (per work item)                │  │
│  │  - ForemanReadOnly(workItemID)                              │  │
│  └──────────────────────────────────────────────────────────────┘  │
│                                                                     │
│  ┌──────────────────────────────────────────────────────────────┐  │
│  │ QuestionRouter                                               │  │
│  │  - Route(stage, event, sessionID)                            │  │
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
│  │ *Foreman (satisfies multiple interfaces)                      │   │
│  │  - ForemanLifecycle: Start, Stop, IsRunning                  │   │
│  │  - ForemanHandler: ResolveEscalated, RefineAnswer           │   │
│  │  - ForemanReadOnly: SessionID, LastSessionID, LastPlanID    │   │
│  └──────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────┘
```

---

## Design Decisions

| Decision | Choice | Rationale |
|----------|--------|----------|
| Foreman scope | Work item scoped | One foreman per work item, survives follow-ups |
| Foreman start | On plan approval, before Implement() | Current docs approach |
| Foreman stop | When work item completes | All repos done = stop |
| Replan handling | Restart foreman with new plan | Ensures FAQ and sub-plan context is current |
| Multiplexing | Multiple foremen, one per work item | Enforced via SessionRegistry map |
| Interface location | `internal/orchestrator/` package | Next to Foreman implementation |
| Foreman storage | In SessionRegistry | "At the end the foreman is just a special type of agent session" |

---

## Implementation Phases

### Phase 1: Interfaces and Events

#### 1.1 Create Interface Definitions

**File:** `internal/orchestrator/interfaces.go`

```go
package orchestrator

// ============================================================================
// Foreman Interfaces
// ============================================================================

// ForemanLifecycle abstracts Foreman for use by orchestrators that need to
// control its lifecycle. Implemented by *Foreman.
type ForemanLifecycle interface {
    Start(ctx context.Context, planID string, followUpContext string) error
    Stop(ctx context.Context) error
    IsRunning() bool
}

// ForemanReadOnly exposes read-only access to the Foreman for UI components
// that need to render session state (sidebar, log display).
type ForemanReadOnly interface {
    SessionID() string
    IsRunning() bool
    LastSessionID() string
    LastPlanID() string
}

// ForemanHandler abstracts the foreman-side of question answering.
// Used by AnswerRouter to send answers/messages to the correct foreman.
type ForemanHandler interface {
    ResolveEscalated(ctx context.Context, questionID, answer string) error
    // RefineAnswer sends human follow-up text to get a revised answer proposal.
    // The escalation remains open; human must still call ResolveEscalated to finalize.
    RefineAnswer(ctx context.Context, questionID, text string) (newProposal string, uncertain bool, err error)
}

// ============================================================================
// Session Registry Interface
// ============================================================================

// SessionRegistry abstracts session and foreman registration.
// The concrete implementation is *SessionRegistry; consumers use this interface.
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
    ForemanReadOnly(workItemID string) ForemanReadOnly
}

// Verify *SessionRegistry satisfies SessionRegistry interface.
var _ SessionRegistry = (*SessionRegistry)(nil)  // NOTE: This will be (*sessionRegistry) after rename

// ============================================================================
// Answer Router Interface
// ============================================================================

// AnswerRouter routes human answers and skips back to the correct handler
// based on question phase. It delegates to SessionRegistry and ForemanHandler
// based on the question's stage, looking up the foreman dynamically per question.
type AnswerRouter interface {
    // Answer routes an answer based on the question's phase.
    // Publishes EventAgentQuestionAnswered on success.
    Answer(ctx context.Context, questionID, answer, answeredBy string) error

    // Skip routes a skip for a question based on its phase.
    // Publishes EventAgentQuestionAnswered on success.
    Skip(ctx context.Context, questionID string) error

    // RefineAnswer sends human follow-up text to get a revised answer proposal.
    // Returns the updated proposal so the UI can refresh.
    RefineAnswer(ctx context.Context, questionID, text string) (newProposal string, uncertain bool, err error)
}

// ============================================================================
// Compile-Time Interface Checks
// ============================================================================

var _ ForemanLifecycle = (*Foreman)(nil)
var _ ForemanReadOnly = (*Foreman)(nil)
var _ ForemanHandler = (*Foreman)(nil)
var _ SessionRegistry = (*sessionRegistry)(nil)
```

#### 1.2 Add Domain Events

**File:** `internal/domain/event.go`

```go
// ForemanEventPayload is the payload for foreman lifecycle events.
type ForemanEventPayload struct {
    WorkItemID string
    PlanID     string
    SessionID  string
}

// Foreman lifecycle events
const (
    EventForemanStarted EventType = "foreman.started"
    EventForemanStopped EventType = "foreman.stopped"
)
```

#### 1.3 Update Foreman to Publish Events

**File:** `internal/orchestrator/foreman.go`

Modify `Start()` to publish `EventForemanStarted` after starting the session.

Modify `Stop()` to publish `EventForemanStopped` before returning.

**Important:** Also drain and close any open entries in `f.escalatedChs` during `Stop()` to prevent goroutine leaks. In-flight answer channels must be closed so waiting goroutines in `routeImplementation` unblock.

---

### Phase 2: SessionRegistry Interface and Enhancement

#### 2.1 Refactor Concrete Implementation

**File:** `internal/orchestrator/session_registry.go`

Rename the exported `SessionRegistry` struct to unexported `sessionRegistry` to match the interface:

```go
// sessionRegistry is the concrete implementation of SessionRegistry.
type sessionRegistry struct {
    mu       sync.RWMutex
    sessions map[string]adapter.AgentSession
    foremen  map[string]*Foreman  // workItemID → foreman
}

// Verify sessionRegistry satisfies SessionRegistry interface.
var _ SessionRegistry = (*sessionRegistry)(nil)

func NewSessionRegistry() *sessionRegistry {
    return &sessionRegistry{
        sessions: make(map[string]adapter.AgentSession),
        foremen:  make(map[string]*Foreman),
    }
}

// Cast to interface for consumers.
func NewSessionRegistryInterface() SessionRegistry {
    return NewSessionRegistry()
}
```

#### 2.2 Add Foreman Tracking

Add foreman tracking map keyed by workItemID to `sessionRegistry`:

```go
// RegisterForeman registers a foreman instance for a work item.
func (r *sessionRegistry) RegisterForeman(workItemID string, foreman *Foreman) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.foremen[workItemID] = foreman
}

// GetForeman returns the foreman for a work item, or nil if none exists.
func (r *sessionRegistry) GetForeman(workItemID string) *Foreman {
    r.mu.RLock()
    defer r.mu.RUnlock()
    return r.foremen[workItemID]
}

// ForemanReadOnly returns a read-only view of the foreman for a work item.
func (r *sessionRegistry) ForemanReadOnly(workItemID string) ForemanReadOnly {
    return r.GetForeman(workItemID)
}

// DeregisterForeman removes the foreman for a work item.
func (r *sessionRegistry) DeregisterForeman(workItemID string) {
    r.mu.Lock()
    defer r.mu.Unlock()
    delete(r.foremen, workItemID)
}
```

#### 2.3 Create AnswerRouter Implementation

**File:** `internal/orchestrator/answer_router.go`

```go
package orchestrator

// answerRouter implements AnswerRouter. It is stateless and delegates
// to SessionRegistry and ForemanHandler based on question stage.
type answerRouter struct {
    registry       SessionRegistry  // Interface, not concrete type
    questionSvc    *service.QuestionService
    sessionSvc    *service.AgentSessionService
    eventBus       event.Publisher
    foremanHandler ForemanHandler   // nil if no harness
}
```go
func NewAnswerRouter(
    registry SessionRegistry,
    questionSvc *service.QuestionService,
    sessionSvc *service.AgentSessionService,
    eventBus event.Publisher,
) *answerRouter {
    return &answerRouter{
        registry:    registry,
        questionSvc: questionSvc,
        sessionSvc: sessionSvc,
        eventBus:   eventBus,
    }
}

// getForemanHandler looks up the correct foreman for a question dynamically.
// It finds the workItemID from the question's session, then gets the foreman from registry.
func (r *answerRouter) getForemanHandler(ctx context.Context, questionID string) (ForemanHandler, error) {
    q, err := r.questionSvc.Get(ctx, questionID)
    if err != nil {
        return nil, fmt.Errorf("get question: %w", err)
    }

    session, err := r.sessionSvc.Get(ctx, q.AgentSessionID)
    if err != nil {
        return nil, fmt.Errorf("get session: %w", err)
    }

    foreman := r.registry.GetForeman(session.WorkItemID)
    if foreman == nil {
        return nil, nil  // No foreman for this work item
    }
    return foreman, nil
}

// Answer routes an answer based on the question's phase.
// Internal helper that does the actual routing; publishes event on success.
func (r *answerRouter) Answer(ctx context.Context, questionID, answer, answeredBy string) error {
    q, err := r.questionSvc.Get(ctx, questionID)
    if err != nil {
        return fmt.Errorf("get question: %w", err)
    }

    switch q.Stage {
    case domain.AgentSessionPhasePlanning:
        return r.answerPlanningQuestion(ctx, q, answer, answeredBy)
    case domain.AgentSessionPhaseImplementation, domain.AgentSessionPhaseReview, "":
        return r.answerImplementationQuestion(ctx, q, answer, answeredBy)
    case domain.AgentSessionPhaseManual:
        return r.answerManualQuestion(ctx, q, answer, answeredBy)
    default:
        return fmt.Errorf("unsupported stage: %q", q.Stage)
    }
}

func (r *answerRouter) answerPlanningQuestion(ctx context.Context, q domain.Question, answer, answeredBy string) error {
    if err := r.registry.SendAnswer(ctx, q.AgentSessionID, answer); err != nil && !errors.Is(err, ErrSessionNotRunning) {
        return fmt.Errorf("send planning answer: %w", err)
    }
    if err := r.questionSvc.Answer(ctx, q.ID, answer, answeredBy); err != nil {
        return fmt.Errorf("persist answer: %w", err)
    }
    if err := r.sessionSvc.ResumeFromAnswer(ctx, q.AgentSessionID); err != nil {
        slog.Warn("failed to resume planning session", "error", err, "session_id", q.AgentSessionID)
    }
    return r.publishAnswered(ctx, q.ID, q.AgentSessionID)
}

func (r *answerRouter) answerImplementationQuestion(ctx context.Context, q domain.Question, answer, answeredBy string) error {
    // Look up the foreman dynamically for this question's work item
    foremanHandler, err := r.getForemanHandler(ctx, q.ID)
    if err != nil {
        return fmt.Errorf("get foreman handler: %w", err)
    }

    // Try foreman escalation first
    if foremanHandler != nil {
        err := foremanHandler.ResolveEscalated(ctx, q.ID, answer)
        if err == nil {
            return nil  // Escalation handled; event already published by foreman
        }
        if !errors.Is(err, ErrQuestionNotEscalated) {
            return fmt.Errorf("resolve escalated: %w", err)
        }
        // Fall through to non-escalated path
    }

    // Non-escalated fallback: resume session and persist answer
    if q.AgentSessionID != "" {
        if err := r.sessionSvc.ResumeFromAnswer(ctx, q.AgentSessionID); err != nil {
            slog.Warn("failed to resume impl session", "error", err, "session_id", q.AgentSessionID)
        }
    }
    if err := r.questionSvc.Answer(ctx, q.ID, answer, answeredBy); err != nil {
        return fmt.Errorf("persist answer: %w", err)
    }
    return r.publishAnswered(ctx, q.ID, q.AgentSessionID)
}

func (r *answerRouter) answerManualQuestion(ctx context.Context, q domain.Question, answer, answeredBy string) error {
    if err := r.registry.SendAnswer(ctx, q.AgentSessionID, answer); err != nil && !errors.Is(err, ErrSessionNotRunning) {
        return fmt.Errorf("send manual answer: %w", err)
    }
    if err := r.sessionSvc.ResumeFromAnswer(ctx, q.AgentSessionID); err != nil {
        slog.Warn("failed to resume manual session", "error", err, "session_id", q.AgentSessionID)
    }
    if err := r.questionSvc.Answer(ctx, q.ID, answer, answeredBy); err != nil {
        return fmt.Errorf("persist answer: %w", err)
    }
    return r.publishAnswered(ctx, q.ID, q.AgentSessionID)
}

// Skip routes a skip based on the question's phase.
func (r *answerRouter) Skip(ctx context.Context, questionID string) error {
    return r.Answer(ctx, questionID, "", "human")
}

// RefineAnswer sends human follow-up text to get a revised answer proposal.
// The escalation remains open; human must still call ResolveEscalated to finalize.
func (r *answerRouter) RefineAnswer(ctx context.Context, questionID, text string) (string, bool, error) {
    foremanHandler, err := r.getForemanHandler(ctx, questionID)
    if err != nil {
        return "", false, fmt.Errorf("get foreman handler: %w", err)
    }
    if foremanHandler == nil {
        return "", false, fmt.Errorf("no foreman available for question")
    }
    return foremanHandler.RefineAnswer(ctx, questionID, text)
}

func (r *answerRouter) publishAnswered(ctx context.Context, questionID, sessionID string) error {
    data, err := json.Marshal(map[string]string{"id": questionID, "agent_session_id": sessionID})
    if err != nil {
        return fmt.Errorf("marshal answered event: %w", err)
    }
    return r.eventBus.Publish(ctx, domain.SystemEvent{
        ID:          domain.NewID(),
        EventType:   string(domain.EventAgentQuestionAnswered),
        WorkspaceID: "",
        Payload:     string(data),
        CreatedAt:   time.Now(),
    })
}
```

---

### Phase 3: ImplementationService Lifecycle Methods

#### 3.1 Add Lifecycle Methods

**File:** `internal/orchestrator/implementation.go`

```go
// BeginForeman starts a foreman for the work item, tied to the plan.
// Called when implementation starts (from TUI after plan approval, before Implement()).
// Creates a fresh *Foreman instance registered in SessionRegistry.
func (s *ImplementationService) BeginForeman(ctx context.Context, workItemID, planID string) error {
    s.foremanMu.Lock()
    defer s.foremanMu.Unlock()

    // Check if foreman already exists for this work item
    if existing := s.registry.GetForeman(workItemID); existing != nil {
        if existing.IsRunning() {
            // Already running for this work item - no-op
            return nil
        }
    }

    // Create new foreman instance
    foreman := NewForeman(s.cfg, s.foremanHarness, s.planSvc, s.questionSvc, s.sessionSvc, s.eventBus)
    
    // Start the foreman
    if err := foreman.Start(ctx, planID, ""); err != nil {
        return fmt.Errorf("start foreman: %w", err)
    }

    // Register in session registry
    s.registry.RegisterForeman(workItemID, foreman)

    // Update question router with new foreman
    s.questionRouter = NewQuestionRouter(s.questionSvc, s.sessionSvc, s.registry, foreman, s.eventBus)

    return nil
}

// EndForeman stops the foreman for the work item.
// Called when implementation completes or is abandoned.
func (s *ImplementationService) EndForeman(ctx context.Context, workItemID string) error {
    s.foremanMu.Lock()
    defer s.foremanMu.Unlock()

    foreman := s.registry.GetForeman(workItemID)
    if foreman == nil {
        return nil
    }

    // Stop with durable context to ensure completion
    stopCtx, stopCancel := durableCleanupContext(ctx)
    defer stopCancel()

    if err := foreman.Stop(stopCtx); err != nil {
        slog.Warn("failed to stop foreman", "error", err, "work_item_id", workItemID)
        // Continue with deregistration even on error
    }

    // Deregister from session registry
    s.registry.DeregisterForeman(workItemID)

    // Clear question router's foreman reference
    s.questionRouter = NewQuestionRouter(s.questionSvc, s.sessionSvc, s.registry, nil, s.eventBus)

    return nil
}

// RestartForemanWithPlan stops and starts the foreman with the new plan.
// Called after replanning to update foreman's context with new plan/FAQ.
func (s *ImplementationService) RestartForemanWithPlan(ctx context.Context, workItemID, planID string) error {
    // Get existing foreman to preserve any in-flight escalated questions
    // (though ideally escalation should be resolved before replanning)
    
    // End existing foreman
    if err := s.EndForeman(ctx, workItemID); err != nil {
        slog.Warn("error ending foreman before restart", "error", err)
    }

    // Begin new foreman with fresh context
    return s.BeginForeman(ctx, workItemID, planID)
}
```

#### 3.2 Refactor Foreman Field Lifecycle

**File:** `internal/orchestrator/implementation.go`

The existing `foreman *Foreman` field is **not dead code** — it's used in `Implement()` to create per-session foremen. However, its lifecycle should be refactored:

- **Before**: Foreman created directly in `Implement()`
- **After**: Foreman lifecycle managed via `BeginForeman()`/`EndForeman()` methods that register with SessionRegistry

Remove the field from the struct after `BeginForeman()`/`EndForeman()` methods are added. The foreman is now owned by SessionRegistry, not by ImplementationService directly.

---

### Phase 4: ReviewFollowup Orchestrator

#### 4.1 Create ReviewFollowup

**File:** `internal/orchestrator/review_followup.go`

```go
package orchestrator

// ReviewFollowup orchestrates a follow-up agent session and the associated
// Foreman lifecycle. It is the single owner of Foreman Start/Stop for
// follow-up sessions.
type ReviewFollowup struct {
    harness     adapter.AgentHarness
    registry    SessionRegistry  // Interface, not concrete type
    planSvc     *service.PlanService
    questionSvc *service.QuestionService
    sessionSvc  *service.AgentSessionService
    eventBus    event.Publisher
    cfg         *config.Config
}

func NewReviewFollowup(
    cfg *config.Config,
    harness adapter.AgentHarness,
    registry SessionRegistry,
    planSvc *service.PlanService,
    questionSvc *service.QuestionService,
    sessionSvc *service.AgentSessionService,
    eventBus event.Publisher,
) *ReviewFollowup {
    return &ReviewFollowup{
        cfg:         cfg,
        harness:     harness,
        registry:    registry,
        planSvc:     planSvc,
        questionSvc: questionSvc,
        sessionSvc:  sessionSvc,
        eventBus:    eventBus,
    }
}

// FollowUp restarts the foreman with follow-up context.
// Gets the current foreman, stops it, starts new one with feedback.
func (r *ReviewFollowup) FollowUp(ctx context.Context, workItemID, feedback string) error {
    // Get plan ID from session service
    workItem, err := r.sessionSvc.GetWorkItem(ctx, workItemID)
    if err != nil {
        return fmt.Errorf("get work item: %w", err)
    }
    
    // Get the plan for this work item
    plan, err := r.planSvc.GetPlanByWorkItemID(ctx, workItemID)
    if err != nil {
        return fmt.Errorf("get plan: %w", err)
    }

    // Get existing foreman to stop it
    existingForeman := r.registry.GetForeman(workItemID)
    if existingForeman != nil && existingForeman.IsRunning() {
        if err := existingForeman.Stop(ctx); err != nil {
            slog.Warn("failed to stop foreman before follow-up", "error", err)
        }
    }

    // Create new foreman
    newForeman := NewForeman(r.cfg, r.harness, r.planSvc, r.questionSvc, r.sessionSvc, r.eventBus)
    
    // Start with follow-up context
    if err := newForeman.Start(ctx, plan.ID, feedback); err != nil {
        return fmt.Errorf("start foreman for follow-up: %w", err)
    }

    // Register new foreman
    r.registry.RegisterForeman(workItemID, newForeman)

    return nil
}

// FollowUpFailed handles the failed follow-up case.
// Similar to FollowUp but may include different context about what failed.
func (r *ReviewFollowup) FollowUpFailed(ctx context.Context, workItemID, feedback string) error {
    // Same as FollowUp but with "Failed: " prefix on feedback
    return r.FollowUp(ctx, workItemID, "Failed: "+feedback)
}
```

---

### Phase 5: Remove TUI Lifecycle Ownership

#### 5.1 Update Services Struct

**File:** `internal/tui/views/services.go`

```go
// Before
Foreman *orchestrator.Foreman

// After
AnswerRouter  orchestrator.AnswerRouter  // nil if no harness (stateless, created in service_manager)
ReviewFollowup *orchestrator.ReviewFollowup // nil if no harness
```

#### 5.2 Update ServiceProvider Interface

**File:** `internal/tui/views/service_provider.go`

```go
// Before
Foreman() *orchestrator.Foreman

// After
AnswerRouter() orchestrator.AnswerRouter
ReviewFollowup() *orchestrator.ReviewFollowup
SessionRegistry() orchestrator.SessionRegistry  // Changed from *orchestrator.SessionRegistry
```

#### 5.3 Update Commands

**File:** `internal/tui/views/cmds.go`

**Remove:**
- `StartForemanCmd`
- `StopForemanCmd`
- `AnswerQuestionCmd` (replaced by direct AnswerRouter usage)
- `SkipQuestionCmd` (replaced by direct AnswerRouter usage)

**Update delegation commands to use AnswerRouter:**
```go
// AnswerQuestionCmd delivers a human answer through AnswerRouter.
func AnswerQuestionCmd(router orchestrator.AnswerRouter, questionID, answer, answeredBy string) tea.Cmd {
    return func() tea.Msg {
        if err := router.Answer(context.Background(), questionID, answer, answeredBy); err != nil {
            return ErrMsg{Err: err}
        }
        return ActionDoneMsg{Message: "Answer submitted"}
    }
}

// SkipQuestionCmd marks a question as skipped through AnswerRouter.
func SkipQuestionCmd(router orchestrator.AnswerRouter, questionID string) tea.Cmd {
    return func() tea.Msg {
        if err := router.Skip(context.Background(), questionID); err != nil {
            return ErrMsg{Err: err}
        }
        return ActionDoneMsg{Message: "Question skipped"}
    }
}

// RefineAnswerCmd sends human follow-up to get a revised answer proposal.
func RefineAnswerCmd(router orchestrator.AnswerRouter, questionID, text string) tea.Cmd {
    return func() tea.Msg {
        newProposal, uncertain, err := router.RefineAnswer(context.Background(), questionID, text)
        if err != nil {
            return ErrMsg{Err: err}
        }
        return ForemanUserMessageResponseMsg{
            QuestionID:   questionID,
            NewProposal:  newProposal,
            Uncertain:    uncertain,
        }
    }
}

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
```

#### 5.4 Update App Struct

**File:** `internal/tui/views/app.go`

**Remove field:**
```go
// REMOVE
foremanPlanID string // plan ID the Foreman was last started for
```

**Update message handlers:**

| Message | Before | After |
|---------|--------|-------|
| `PlanApprovedMsg` | Start Foreman directly | `BeginForemanOrchestratedCmd` |
| `FollowUpSessionMsg` | FollowUp + restartForemanForTask | `FollowUpOrchestratedCmd` |
| `FollowUpFailedSessionMsg` | FollowUpFailed + restartForemanForTask | `FollowUpFailedOrchestratedCmd` |
| `FollowUpSessionCompleteMsg` | Stop Foreman | `EndForemanOrchestratedCmd` |
| `WorkItemCompletedMsg` | Stop Foreman | `EndForemanOrchestratedCmd` |
| `DeleteSessionMsg` | Stop Foreman if matches | `EndForemanOrchestratedCmd` |
| `ReimplementMsg` | Set foremanPlanID | `BeginForemanOrchestratedCmd` |
| `RetryFailedMsg` | Set foremanPlanID | `BeginForemanOrchestratedCmd` |
| `PlanRequestChangesMsg` | (no action currently) | `RestartForemanWithPlanOrchestratedCmd` |

**Remove methods:**
- `restartForemanForTask()`

**Update `teardownAllPipelines()`:**
- Iterate over all registered foremen and stop them
- Or have SessionRegistry expose `StopAllForemen(ctx)` method

#### 5.5 Update Sidebar Rendering

**File:** `internal/tui/views/app.go`

Update Foreman group in sidebar to use `ForemanReadOnly` from SessionRegistry:

```go
// Get foreman read-only view for the current work item
foremanReadOnly := a.provider.SessionRegistry().ForemanReadOnly(a.currentWorkItemID)
```

---

### Phase 6: QuestionRouter Interface

#### 6.1 Update QuestionRouter

**File:** `internal/orchestrator/question_router.go`

Change from concrete `*Foreman` to interface:

```go
// Before
foreman *Foreman

// After
foreman ForemanLifecycle
```

The `QuestionRouter` needs `ForemanLifecycle` because it calls `restartSession` (via `foreman.Start`) when a question fails.

---

### Phase 7: Service Manager Wiring

#### 7.1 Update Wiring

**File:** `internal/tui/views/service_manager.go`

```go
// 1. Create SessionRegistry (interface, backed by concrete impl)
registry := orchestrator.NewSessionRegistry()  // returns *sessionRegistry
var registryInterface orchestrator.SessionRegistry = registry

// 2. Create AnswerRouter (stateless, uses SessionRegistry interface)
answerRouter := orchestrator.NewAnswerRouter(
    registryInterface,
    questionSvc,
    sessionSvc,
    bus,
)

// 3. Create QuestionRouter with nil foreman handler initially
// Foreman lifecycle managed by ImplementationService; QuestionRouter uses ForemanLifecycle
questionRouter := orchestrator.NewQuestionRouter(questionSvc, sessionSvc, registryInterface, nil, bus)

// 4. ImplementationService receives registry and foremanHarness
implSvc = orchestrator.NewImplementationService(cfg, harnesses.Implementation, gitClient, bus, planSvc, workItemSvc, sessionSvc, workspaceSvc, registryInterface, reviewPipeline, harnesses.Foreman, questionSvc, reviewSvc, hookRegistry)

// 5. Create ReviewFollowup with registry interface
var reviewFollowup *orchestrator.ReviewFollowup
if harnesses.Foreman != nil {
    reviewFollowup = orchestrator.NewReviewFollowup(cfg, harnesses.Foreman, registryInterface, planSvc, questionSvc, sessionSvc, bus)
}

// 6. AnswerRouter looks up foreman dynamically per question
// (no wiring needed; AnswerRouter.getForemanHandler does the lookup)

return &Services{
    // ... other fields ...
    AnswerRouter:   answerRouter,
    ReviewFollowup: reviewFollowup,
    // SessionRegistry is accessed via SessionRegistry() method returning the interface
    // ...
}
```

#### 7.2 Update Other Orchestrators to Use Interface

Update orchestrator services that depend on SessionRegistry:

```go
// PlanningService, ReviewPipeline, Resumption, ManualSessionService
// Change registry parameter from *SessionRegistry to SessionRegistry
```

---

## Files to Change

| File | Change Type | Description |
|------|-------------|-------------|
| `internal/orchestrator/interfaces.go` | **NEW** | Interface definitions (renamed from foreman_interfaces.go) |
| `internal/domain/event.go` | MODIFY | Add `EventForemanStarted`, `EventForemanStopped` |
| `internal/orchestrator/foreman.go` | MODIFY | Publish lifecycle events; drain escalatedChs on Stop |
| `internal/orchestrator/session_registry.go` | MODIFY | Rename to `sessionRegistry`; implement `SessionRegistry` interface |
| `internal/orchestrator/answer_router.go` | **NEW** | `AnswerRouter` implementation |
| `internal/orchestrator/implementation.go` | MODIFY | Add lifecycle methods; refactor foreman field |
| `internal/orchestrator/review_followup.go` | **NEW** | `ReviewFollowup` orchestrator |
| `internal/orchestrator/question_router.go` | MODIFY | Use `ForemanLifecycle` interface |
| `internal/orchestrator/planning.go` | MODIFY | Use `SessionRegistry` interface |
| `internal/orchestrator/review.go` | MODIFY | Use `SessionRegistry` interface |
| `internal/orchestrator/resume.go` | MODIFY | Use `SessionRegistry` interface |
| `internal/orchestrator/manual.go` | MODIFY | Use `SessionRegistry` interface |
| `internal/tui/views/services.go` | MODIFY | Replace `Foreman` with `AnswerRouter` |
| `internal/tui/views/service_provider.go` | MODIFY | Update interface methods; `SessionRegistry()` returns interface |
| `internal/tui/views/test_provider.go` | MODIFY | Update interface methods |
| `internal/tui/views/cmds.go` | MODIFY | Remove lifecycle cmds; update `AnswerQuestionCmd`/`SkipQuestionCmd` signatures |
| `internal/tui/views/app.go` | MODIFY | Remove foremanPlanID, update all call sites |
| `internal/tui/views/service_manager.go` | MODIFY | Wire new types; create AnswerRouter |

---

## Acceptance Criteria

- [ ] `*orchestrator.Foreman` not referenced directly from `internal/tui/`
- [ ] `App` has no `foremanPlanID` field
- [ ] All Foreman Start/Stop calls are in `internal/orchestrator/`
- [ ] `ImplementationService.BeginForeman`/`EndForeman`/`RestartForemanWithPlan` work correctly
- [ ] `ReviewFollowup` owns follow-up Foreman lifecycle
- [ ] Multiple simultaneous foremen work (one per work item)
- [ ] Replanning restarts foreman with new plan context
- [ ] `EventForemanStarted` and `EventForemanStopped` are published
- [ ] `SessionRegistry` interface used throughout orchestrator (no concrete pointers)
- [ ] `AnswerRouter` correctly routes answers to the right handler based on question stage
- [ ] `escalatedChs` drained and closed on `Foreman.Stop()` to prevent goroutine leaks
- [ ] `go build ./...` passes
- [ ] `go test ./internal/orchestrator/...` passes
- [ ] Manual smoke test: approve plan → Foreman starts → implementation runs → Foreman stops → follow-up → Foreman restarts

---

## Open Questions (To Be Resolved During Implementation)

1. ~~Session→WorkItemID lookup~~: **RESOLVED** - Use `questionSvc.Get(questionID).AgentSessionID` → `sessionSvc.Get().WorkItemID`

2. **In-flight escalations during replan**: What happens to questions escalated to humans when the user replans? Should we:
   - Complete all escalations before allowing replan?
   - Let them resolve via the old foreman and have new foreman pick up subsequent questions?
   - Transfer escalation state to new foreman?

3. ~~Foreman session logs~~: **RESOLVED** - `LastSessionID()`/`LastPlanID()` still work per foreman; TUI reads via `ForemanReadOnly(workItemID)`

4. **QuestionRouter restart logic**: When `QuestionRouter` calls `foreman.Start()` to restart after a failure, which workItemID should it use? Need to track this during routing.

5. ~~Fate of Services.Foreman~~: **RESOLVED** - `Services.Foreman` removed; replaced with `AnswerRouter` which is stateless and uses `SessionRegistry` + `ForemanHandler`

6. ~~Who calls `SetForemanHandler`?~~: **RESOLVED** - `AnswerRouter` looks up the foreman dynamically per question via `getForemanHandler()`
