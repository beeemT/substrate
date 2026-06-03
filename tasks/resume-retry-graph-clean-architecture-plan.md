# Resume and Retry Graph Architecture Plan

## Goal

Replace the current resume/retry/follow-up patchwork with one graph-driven agent-session progression model.

The desired model is:

```text
TUI action
  -> orchestration intent
    -> graph leaf validation
      -> agent run supervisor
        -> durable terminal agent-session state
          -> kind-specific continuation
            -> sub-plan status
              -> work-item aggregate state / finalization
                -> events
```

This plan intentionally permits larger changes. The current bugs are not isolated missing checks; they come from duplicated state machines and incomplete caller-owned continuations.

## Current Evidence

- `domain.LeafAgentSessions` already defines the best abstraction for current state: a session is current if it has no graph child, with legacy fallback for unlinked historical rows.
- `tasks/agent-session-graph-plan.md` established edge semantics: implementation -> review -> reimplementation -> review, failed/interrupted -> retry/resume, completed -> follow-up.
- `internal/orchestrator/resume.go:483` `WaitAndComplete` only terminal-transitions an agent session. It does not advance review/sub-plan/work-item state.
- `internal/orchestrator/implementation.go:762` `ContinueAfterImplSession` is the closest existing continuation, but it is only composed by focused retry in `internal/tui/views/cmds.go:1370`.
- `internal/tui/views/cmds.go:883` bulk resume starts replacement sessions but does not own post-completion pipeline continuation.
- `internal/tui/views/cmds.go:1334`, `:1351`, and `:1370` block Bubble Tea commands on long-running harness/review work, violating `internal/tui/AGENTS.md`.
- Review retry loses graph semantics: `RetrySessionCmd` retries a failed review by calling `ContinueAfterImplSession(parentImplID)`, so the replacement review is parented to the implementation instead of the failed review leaf.

## Target Domain Model

### Graph Terms

- **Attempt**: one agent session node in the graph.
- **Leaf**: current terminal/current node for a `(work_item_id, sub_plan_id, kind, repository)` branch. Existing `domain.LeafAgentSessions` is the source of truth.
- **Superseded leaf**: the failed/interrupted/completed node that triggered a retry/resume/follow-up. New work must point `ParentAgentSessionID` to this node.
- **Continuation**: work performed after an agent session exits cleanly: review, reimplementation, sub-plan transition, work-item aggregate state, finalization.

### Invariants

1. User-triggered resume/retry/follow-up may only target graph leaves, except explicit historical read-only actions.
2. Every replacement agent session must have `ParentAgentSessionID = sourceLeaf.ID`.
3. Review sessions created while retrying a failed review must have `ParentAgentSessionID = failedReview.ID`, not the reviewed implementation ID.
4. Implementation session completion is not complete from the product perspective until continuation has run.
5. TUI never blocks on harness/review execution. It dispatches an intent and observes durable events.
6. Services enforce primitive per-entity transitions; orchestrators enforce cross-entity graph progression.
7. Resume/retry candidate selection lives in one domain/orchestrator helper, not in overview, TUI commands, and implementation retry code separately.

## New Architecture

### 1. Add graph intent types

Create `internal/orchestrator/agent_graph.go` or `implementation_graph.go`.

```go
type AgentGraphTrigger string

const (
    AgentGraphTriggerResumeInterrupted AgentGraphTrigger = "resume_interrupted"
    AgentGraphTriggerRetryFailed       AgentGraphTrigger = "retry_failed"
    AgentGraphTriggerFollowUpCompleted AgentGraphTrigger = "follow_up_completed"
    AgentGraphTriggerFollowUpFailed    AgentGraphTrigger = "follow_up_failed"
    AgentGraphTriggerAutoReimpl        AgentGraphTrigger = "auto_reimpl"
)

type AgentGraphIntent struct {
    SourceSessionID   string
    WorkItemID        string
    SubPlanID         string
    Trigger           AgentGraphTrigger
    Feedback          string
    CurrentInstanceID string
}

type AgentGraphRunResult struct {
    SourceSession domain.AgentSession
    NewSession    domain.AgentSession
    Trigger       AgentGraphTrigger
}
```

The intent should be created by TUI/service callers but validated by the orchestrator using current DB state.

### 2. Centralize graph leaf selection

Add a helper near domain or service, e.g.:

```go
type AgentSessionGraph struct {
    Sessions []domain.AgentSession
    Leaves   []domain.AgentSession
}

func CurrentAgentSessionLeaves(sessions []domain.AgentSession) []domain.AgentSession
func FindLeafByID(sessions []domain.AgentSession, id string) (domain.AgentSession, bool)
func RetryableLeavesForWorkItem(sessions []domain.AgentSession) []domain.AgentSession
func ResumableLeavesForWorkItem(sessions []domain.AgentSession) []domain.AgentSession
```

Rules:

- Exclude manual sessions from automated resume/retry.
- Planning sessions are returned separately for planning-specific resume.
- Implementation and review leaves are graph-managed.
- Failed/interrupted non-leaf sessions are historical and cannot be retried/resumed from UI.

Replace duplicate logic in:

- `internal/tui/views/overview.go` action-card building.
- `internal/tui/views/cmds.go:883` `ResumeAllSessionsForWorkItemCmd`.
- `internal/tui/views/app.go:3436` focused retry checks.
- `internal/orchestrator/implementation.go` retry reset eligibility where it currently partially reconstructs leaf behavior.

### 3. Introduce an AgentRunSupervisor

Create `internal/orchestrator/agent_run_supervisor.go`.

Responsibilities currently duplicated across `runImplementation`, `Resumption.WaitAndComplete`, and review code:

- own the single consumer of each harness event channel
- forward text/progress/question events from that single consumer
- detect terminal review events (`done` / `error`) without double-consuming events
- route questions using the actual session kind
- compact/resume/send initial message as required
- wait for harness completion when the harness contract requires `Wait`
- convert terminal result into durable `AgentSessionService.Complete/Fail/Interrupt`
- persist `ResumeInfo`
- emit/log failures consistently

Sketch:

```go
type AgentRunSupervisor struct {
    harnesses  AgentHarnessSelector
    sessionSvc *service.AgentSessionService
    questions  *QuestionRouter
    registry   *SessionRegistry
}

type AgentHarnessSelector interface {
    HarnessFor(kind domain.AgentSessionKind) adapter.AgentHarness
}

type AgentRunRequest struct {
    Session domain.AgentSession
    Opts    adapter.SessionOpts
    OnCompleted func(context.Context, domain.AgentSession) error
    OnFailed    func(context.Context, domain.AgentSession, error) error
    OnInterrupted func(context.Context, domain.AgentSession) error
}

func (s *AgentRunSupervisor) Start(ctx context.Context, req AgentRunRequest) (domain.AgentSession, error)
```

Start should return after successful harness start. The supervisor should run completion asynchronously, then call the continuation callback. TUI never calls `WaitAndComplete` directly. The supervisor may be implemented as one instance per harness/kind or with a harness selector, but the ServiceManager/constructor cutover must preserve today's separate implementation, review, resume, foreman, and manual harness ownership. Foreman is not graph-supervised.

### 4. Make ImplementationService own implementation graph progression

Add:

```go
func (s *ImplementationService) StartImplementationGraphRun(ctx context.Context, intent AgentGraphIntent) (AgentGraphRunResult, error)
```

This is the single entry point for:

- interrupted implementation resume
- failed implementation retry
- failed implementation follow-up with feedback
- completed implementation follow-up if product semantics mean “apply requested changes and re-review”
- auto reimplementation after critique

It should:

1. Load all sessions for the work item.
2. Validate `intent.SourceSessionID` is a graph leaf.
3. Validate `source.Kind == implementation`.
4. Validate source status matches trigger:
   - interrupted -> resume
   - failed -> retry/follow-up failed
   - completed -> follow-up completed
5. Ensure work item is in an implementation-capable state:
   - `failed` -> `RetryFailedWorkItem`
   - `reviewing` -> `StartImplementation`
   - `implementing` -> ok
   - completed follow-up should transition via the intended product route, not ad hoc row restart.
6. Ensure sub-plan status is `in_progress` for single-session continuation:
   - failed/escalated -> in_progress
   - pending -> in_progress
   - completed follow-up -> in_progress if follow-up changes code
7. Create a child implementation session with `ParentAgentSessionID = source.ID`.
8. Start harness through `AgentRunSupervisor`.
9. On completed implementation session, call unified continuation.

### 5. Replace `ContinueAfterImplSession` with graph-aware continuation

Current `ContinueAfterImplSession(ctx, completedSessionID)` is too narrow. Replace or wrap it with:

```go
type ContinuationContext struct {
    CompletedImplementationID string
    SupersededLeafID          string
    Trigger                   AgentGraphTrigger
    FirstReviewParentID       string
}

func (s *ImplementationService) ContinueImplementationGraph(ctx context.Context, cc ContinuationContext) error
```

Behavior:

- Load completed implementation session.
- Load sub-plan, plan, work item, workspace.
- Run review loop.
- If the continuation is retrying a failed review, pass `FirstReviewParentID = failedReview.ID` so replacement review is correctly parented.
- Persist sub-plan status from the durable review result. These writes must return errors; do not use best-effort helpers that only log and continue.
- Re-derive work-item state from all sub-plans. Aggregate work-item transitions must also return errors to the continuation caller.
- Finalize completed work item if all sub-plans completed. Finalization failures are continuation failures, not warnings hidden behind a completed agent session.

`ContinueImplementationGraph` must be durably fail-closed: if review, sub-plan transition, aggregate work-item transition, or finalization fails after the agent session completed, record a continuation failure tied to the completed session before returning. Acceptable mechanisms are a dedicated `agent_session.continuation_failed` event plus durable error metadata, or a first-class continuation status table. A completed implementation session with no durable continuation result is not an allowed terminal state.

Eventually delete or make private `ContinueAfterImplSession`.

### 6. Make review retry graph-aware

Add review-specific graph entry point:

```go
func (s *ImplementationService) RetryReviewLeaf(ctx context.Context, intent AgentGraphIntent) (AgentGraphRunResult, error)
```

For failed/interrupted review leaf:

1. Validate source is a review graph leaf.
2. Walk the session graph ancestors from the review leaf to the nearest completed implementation session. Do not assume the direct parent is the implementation; replacement reviews will be children of failed/interrupted review leaves.
3. Validate the implementation ancestor is completed and belongs to the same sub-plan/work item.
4. Call graph-aware continuation with:

```go
CompletedImplementationID: parentImpl.ID,
SupersededLeafID:          failedReview.ID,
FirstReviewParentID:       failedReview.ID,
Trigger:                   intent.Trigger,
```

This preserves the edge:

```text
implementation -> failed review -> replacement review
```

not:

```text
implementation -> failed review
implementation -> replacement review
```

### 7. Split resume by kind

Remove kind-blind resume behavior from `Resumption.ResumeSessionWithPrompt`.

Desired routing:

| Source kind | Source status | Route |
|---|---|---|
| planning | interrupted | `PlanningService.ResumeInterruptedPlanning` |
| implementation | interrupted | `ImplementationService.StartImplementationGraphRun(resume)` |
| implementation | failed | `ImplementationService.StartImplementationGraphRun(retry/follow-up)` |
| review | interrupted/failed | `ImplementationService.RetryReviewLeaf` or review resume equivalent |
| foreman | interrupted/failed | foreman-specific restart/recovery only |
| manual | any | no automated resume/retry |

Keep `Resumption` only if it becomes a thin façade that routes to kind-specific orchestrators. Prefer deleting it after cutover if it has no cohesive responsibility.

### 8. Fix completed follow-up semantics

There are currently two contradictory surfaces:

- `completed_view.go` emits `FollowUpPlanMsg` — planning/replan style.
- `action_menu.go` emits `FollowUpSessionMsg{TaskID: currentWorkItemID}` — wrong because handler expects an agent session ID.

Decision:

- If completed-session feedback means “revise the plan / create a new plan cycle,” route only through `FollowUpPlanMsg`.
- If it means “apply more code changes to a completed implementation,” route through `StartImplementationGraphRun(FollowUpCompleted)` and re-review.

Do not keep both under the same UI label. Pick explicit labels, e.g.:

- “Revise plan” -> planning follow-up.
- “Request code changes” -> implementation graph follow-up.

### 9. Make TUI actions async and event-driven

Change long-running commands:

- `ResumeAllSessionsForWorkItemCmd`
- `FollowUpSessionCmd`
- `FollowUpFailedSessionCmd`
- `RetrySessionCmd`
- `RestartPlanningCmd` if it can run agent work synchronously

Pattern:

```go
func RetrySessionCmd(...) tea.Cmd {
    return func() tea.Msg {
        go func() {
            if err := orchestrator.Start...(context.WithoutCancel(ctx), intent); err != nil {
                program.Send(ErrMsg{Err: err})
                return
            }
        }()
        return ActionStartedMsg{...}
    }
}
```

The durable state changes must arrive through event bus:

- `agent_session.resumed`
- `agent_session.completed`
- `agent_session.failed`
- `subplan.completed/failed/escalated`
- `work_item.implementing/reviewing/completed/failed`

Use command return messages only for “dispatch accepted” or immediate validation errors.

### 10. Centralize bulk resume/retry orchestration

Add a work-item-level graph entry point:

```go
func (s *ImplementationService) ResumeRetryLeavesForWorkItem(ctx context.Context, workItemID string, mode ResumeRetryMode, instanceID string) (ResumeRetryDispatchResult, error)
```

It should:

1. Load sessions, plan, sub-plans.
2. Compute graph leaves once.
3. Split by kind/status.
4. Route planning leaves to planning service or return a planning-required result.
5. Dispatch implementation/review graph runs.
6. Start/restart Foreman once per work item, not once per UI branch.
7. Return accepted counts and skipped reasons.

The TUI should not know active sub-plan maps, leaf supersession, or kind-specific orchestration rules.

### 11. Service-layer cleanup

#### `AgentSessionService.Resume`

Current behavior creates a running child from interrupted source and only rejects active children. Keep the primitive, but rename it to make clear it is not full orchestration:

```go
CreateResumeChild(...)
```

It should:

- reload source inside the transaction
- validate source status/kind if parameters include expected values
- validate the source is still a graph leaf under the same transaction. Do not accept “no active child” as a substitute; a source with a terminal child is historical and must not spawn another sibling retry.
- set `ParentAgentSessionID`
- create running child
- emit `agent_session.resumed`

#### `AgentSessionService.FollowUpFailed`

Currently accepts a caller-supplied stale `domain.AgentSession`. Change to accept source ID and reload inside transaction:

```go
CreateRetryChild(ctx, sourceID string, attrs RetryChildAttrs) (domain.AgentSession, error)
```

Validate:

- source exists
- source is still a graph leaf under the same transaction. Do not accept “no active child” as a substitute; stale UI actions against historical nodes must fail.
- source status matches allowed trigger
- kind is preserved unless caller explicitly requests a different kind and that transition is allowed

#### `FollowUpRestart`

This mutates a completed row back to running. That breaks graph append-only semantics for implementation/review sessions. Deprecate/remove it for graph-managed kinds. Preserve manual-session compatibility by either keeping `FollowUpRestart` as a manual-only primitive or replacing `ManualSessionService.FollowUpCompleted` with an explicit manual-only restart path before removing the general method. Completed implementation/review follow-up should create a child node.

### 12. Event model additions

Ensure all graph progression writes durable events:

- `agent_session.resumed` for child creation from interrupted source.
- `agent_session.follow_up` for child creation from completed/failed source with feedback, but payload must include old and new session IDs.
- `subplan.started`, `subplan.completed`, `subplan.failed`, `subplan.escalated` as applicable.
- `work_item.implementing/reviewing/completed/failed` already exists; use only after durable aggregate state changes.


TUI event decoding must preserve graph link metadata. Update `internal/tui/views/event_consumer.go`, `msgs.go`, and handlers/tests so `agent_session.resumed` and `agent_session.follow_up` messages carry both old/source session ID and new session ID. Do not decode only the new session and discard the edge; UI pending state and graph leaf refreshes must be able to clear or reload the superseded source deterministically.
Avoid UI-only completion messages for lifecycle state.

## File-by-File Change Plan

### `internal/domain/session.go`

- Keep `LeafAgentSessions` as core abstraction.
- Add graph helper functions for finding leaves and eligible resume/retry targets.
- Add tests for:
  - failed parent + running child suppresses failed parent
  - interrupted parent + failed child leaf remains retryable
  - review retry chain leaf is replacement review
  - manual sessions excluded

### `internal/service/session.go`

- Replace or wrap `Resume`, `FollowUpFailed`, `FollowUpRestart` with append-only child creation primitives.
- Reload source sessions inside transactions instead of trusting caller-supplied structs.
- Preserve kind by default.
- Add transactional graph-leaf validation; do not allow historical non-leaf sources to create sibling retries.
- Emit old/new IDs consistently.

### `internal/orchestrator/agent_run_supervisor.go` (new)

- Consolidate harness lifecycle logic from:
  - `ImplementationService.runImplementation`
  - `Resumption.WaitAndComplete`
  - `ReviewPipeline.startReviewAgent` where practical
- Own terminal agent-session transitions and `ResumeInfo` persistence.

### `internal/orchestrator/implementation.go`

- Add graph entry points for implementation resume/retry/follow-up.
- Replace `ContinueAfterImplSession` with graph-aware continuation or make it delegate.
- Pass `FirstReviewParentID` into review loop when retrying failed review leaves.
- Remove stale compensating branches once graph continuation is idempotent.

### `internal/orchestrator/review.go`

- Ensure review creation always accepts explicit parent context:
  - normal review parent = implementation session
  - retry/resume review parent = failed/interrupted review leaf
- Prefer one terminal handling path via supervisor, or clearly isolate review-specific event completion while preserving same graph callbacks.

### `internal/orchestrator/resume.go`

- Shrink to routing façade or delete.
- Remove kind-blind implementation prompt construction for review sessions.
- Remove public `WaitAndComplete` use from TUI paths.

### `internal/tui/views/cmds.go`

- Replace direct orchestration composition with async intent dispatch.
- Remove `WaitAndComplete` and `ContinueAfterImplSession` calls from TUI commands.
- Make command return indicate dispatch accepted, not lifecycle complete.
- Delegate candidate selection to orchestrator.

### `internal/tui/views/app.go`

- Keep UI state in sync through domain events.
- Replace `resumeInFlight` map with event-backed pending dispatch state if still needed.
- Do not clear all in-flight resumes on any `ErrMsg`; clear by work-item/action ID.
- Add nil-provider guards for direct retry path.

### `internal/tui/views/action_menu.go` and `completed_view.go`

- Resolve completed follow-up contradiction.
- Do not emit `FollowUpSessionMsg` with a work-item ID in `TaskID`.
- Split plan follow-up vs code follow-up explicitly if both are desired.

### Tests

Add/adjust tests in layers:

#### Domain graph tests

- leaf selection for implementation/review chains
- legacy fallback
- retryable/resumable leaf selection

#### Service tests

- child creation reloads source and rejects stale/non-leaf source
- kind is preserved
- completed follow-up creates child instead of mutating original row
- transactional leaf validation prevents duplicate sibling resume/retry after any child exists, active or terminal

#### Orchestrator tests

- interrupted implementation resume completes agent and then runs review continuation
- failed implementation retry with feedback runs review continuation
- failed review retry creates replacement review child under failed review
- bulk resume dispatch uses graph leaves only
- failed harness start leaves source leaf retryable and child failed/cleaned consistently

#### TUI tests

- resume/retry commands return dispatch messages without blocking on harness completion
- event bus updates task/sidebar state after graph continuation
- completed follow-up action emits correct message type
- bulk resume candidates are not recomputed in TUI

## Migration Strategy

1. Land graph helpers and tests without behavior change.
2. Introduce `AgentRunSupervisor` and use it for new graph entry points.
3. Convert focused implementation retry to graph entry point first; it has the narrowest scope and existing tests.
4. Convert review retry/resume before bulk cutover. Bulk orchestration must not route review leaves through the old kind-blind resume path; until this step is complete, bulk must skip review leaves with durable skipped reasons.
5. Convert bulk resume/retry to graph entry point after both implementation and review leaf routes exist.
6. Convert completed/failed follow-up semantics.
7. Remove old TUI calls to `WaitAndComplete` and direct `ContinueAfterImplSession`.
8. Delete or make private obsolete `Resumption` methods.
9. Remove compensating stale-state checks that are no longer needed, but keep idempotency guards for crash recovery.

## Acceptance Criteria

- A clean ACP exit from any implementation resume/retry/follow-up always either:
  - runs review and advances sub-plan/work item, or
  - records a durable failure explaining why continuation did not run.
- Retrying a failed review creates graph edge `failed review -> replacement review`.
- Bulk resume and focused retry use the same graph eligibility rules.
- No user-triggered TUI command blocks on harness or review completion.
- Historical failed/interrupted non-leaf sessions never drive current action cards or labels and cannot create new retry/resume children.
- There is no path where implementation agent session is `completed` while its sub-plan remains stale solely because the caller forgot to call continuation or because continuation failure was only logged.