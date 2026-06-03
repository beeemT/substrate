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
- Legacy kind-blind resume/follow-up paths and direct continuation wrappers have been removed; graph entry points now own continuation.

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

### 1. Add graph intent types — DONE

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

Status:

- Done: `AgentGraphTrigger`, `ContinuationContext`, `AgentGraphIntent`, and `AgentGraphRunResult` exist in `internal/orchestrator/implementation.go`.

### 2. Centralize graph leaf selection — DONE

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

Status:

- Done: domain graph helpers exist as `LeafAgentSessions`, `FindLeafAgentSessionByID`, `IsLeafAgentSessionID`, `RetryableAgentSessionLeaves`, and `ResumableAgentSessionLeaves`.
- Done: domain tests cover current leaf selection and retry/resume eligibility.
- Done: `internal/tui/views/overview.go` action-card building now uses the domain retryable/resumable leaf helpers instead of reconstructing failed/interrupted leaf eligibility.
- Done: `internal/tui/views/app.go` focused retry checks now validate selected sessions through `FindLeafAgentSessionByID` and only allow implementation/review failed leaves.
- Done: focused and bulk TUI graph paths delegate eligibility to domain/orchestrator graph helpers.

### 3. Introduce an AgentRunSupervisor

Create `internal/orchestrator/agent_run_supervisor.go`.

Responsibilities:

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

Start should return after successful harness start. The supervisor should run completion asynchronously, then call the continuation callback. TUI never waits on harness completion directly. The supervisor may be implemented as one instance per harness/kind or with a harness selector, but the ServiceManager/constructor cutover must preserve today's separate implementation, review, foreman, and manual harness ownership. Foreman is not graph-supervised.

Status:

- Done: `internal/orchestrator/agent_run_supervisor.go` exists and owns harness start, event forwarding, registry lifecycle, wait, resume-info persistence, durable complete/fail/interrupt transitions, and completion callbacks.
- Done: implementation graph runs use `AgentRunSupervisor` for harness lifecycle.

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


Status:

- Done: `ImplementationService.StartImplementationGraphRun` exists and validates source leaf/kind/status/work-item/sub-plan state for implementation resume, retry, failed follow-up, and completed follow-up.
- Done: implementation graph children are parented to the superseded implementation leaf and call `ContinueImplementationGraph` after clean harness completion.
- Done: implementation graph child execution uses `AgentRunSupervisor`, and graph continuation rows are created atomically with the implementation session completion transition.

### 5. Replace legacy continuation wrappers with graph-aware continuation — DONE

Legacy direct-continuation wrappers have been removed; callers use:

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

`ContinueImplementationGraph` must be durably fail-closed: if review, sub-plan transition, aggregate work-item transition, or finalization fails after the agent session completed, record a continuation failure tied to the completed session before returning. A completed implementation session with no durable continuation result is not an allowed terminal state.

Status:

- Done: `ContinueImplementationGraph(ctx, ContinuationContext)` is implemented.
- Done: compatibility wrappers `ContinueAfterImplSession` and `ContinueAfterImplSessionWithReviewParent` were deleted after TUI/orchestrator callsites moved to graph entry points.
- Done: continuation failures during review/sub-plan/work-item/finalization are recorded durably and still emit the existing continuation-failed event.

### 5a. Make continuation state first-class — DONE

Do not rely only on `agent_session.continuation_failed` events or log entries. Add durable continuation state so the system can distinguish:

- continuation not started
- continuation running
- continuation completed
- continuation failed with retryable error
- continuation intentionally skipped with reason
- continuation superseded by a graph child

Recommended schema:

```go
type AgentSessionContinuationStatus string

const (
    AgentSessionContinuationPending   AgentSessionContinuationStatus = "pending"
    AgentSessionContinuationRunning   AgentSessionContinuationStatus = "running"
    AgentSessionContinuationCompleted AgentSessionContinuationStatus = "completed"
    AgentSessionContinuationFailed    AgentSessionContinuationStatus = "failed"
    AgentSessionContinuationSkipped   AgentSessionContinuationStatus = "skipped"
    AgentSessionContinuationSuperseded AgentSessionContinuationStatus = "superseded"
)

type AgentSessionContinuation struct {
    ID             string
    AgentSessionID string
    WorkItemID     string
    SubPlanID      string
    Kind           string
    Status         AgentSessionContinuationStatus
    Attempt        int
    LastError      string
    StartedAt      *time.Time
    CompletedAt    *time.Time
    CreatedAt      time.Time
    UpdatedAt      time.Time
}
```

Persistence requirements:

- Unique active continuation per `agent_session_id` and continuation `kind`.
- Create `pending` continuation before or in the same transaction as marking an implementation session completed.
- Transition to `running` before review/sub-plan/work-item/finalization work starts.
- Transition to `completed` only after durable sub-plan/work-item/finalization writes finish.
- Transition to `failed` with `LastError` when continuation cannot complete.
- Startup recovery must find `pending`/stale `running` continuations and resume or expose retry.
- UI should render “agent completed; continuation failed/pending” distinctly from agent-session failure.

Events remain useful for notification (`agent_session.continuation_failed`), but the continuation table is the source of truth.


Status:

- Done: domain model, migration, repository, and service primitives exist.
- Done: implementation graph continuation uses pending/running/completed/failed states.
- Done: implementation graph completion creates the pending continuation in the same transaction as marking the implementation session completed.
- Done: startup recovery resumes pending/running implementation continuations from `ServiceManager` rebuild/startup via `ImplementationService.RecoverContinuationsForWorkspace`; failed continuation state is exposed in completed implementation session metadata for TUI visibility.

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

Status:

- Done: `ImplementationService.RetryReviewLeaf` validates failed/interrupted review leaves, walks ancestors to the nearest completed implementation session, and invokes graph continuation with `FirstReviewParentID = source review leaf`.
- Done: focused tests cover ancestor walking through a review retry chain and rejecting historical non-leaf review retries.

### 7. Split resume by kind

Remove kind-blind resume behavior formerly implemented by `internal/orchestrator/resume.go`.

Desired routing:

| Source kind | Source status | Route |
|---|---|---|
| planning | interrupted | `PlanningService.ResumeInterruptedPlanning` |
| implementation | interrupted | `ImplementationService.StartImplementationGraphRun(resume)` |
| implementation | failed | `ImplementationService.StartImplementationGraphRun(retry/follow-up)` |
| review | interrupted/failed | `ImplementationService.RetryReviewLeaf` or review resume equivalent |
| foreman | interrupted/failed | foreman-specific restart/recovery only |
| manual | any | no automated resume/retry |

Status: Done — `internal/orchestrator/resume.go` and its legacy tests were deleted after production paths moved to kind-specific graph/planning/manual orchestrators.

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

### 9. Make TUI actions async and event-driven — DONE

Change long-running commands:

- `ResumeAllSessionsForWorkItemCmd`
- `FollowUpSessionCmd` — DONE for implementation graph dispatch
- `FollowUpFailedSessionCmd` — DONE for implementation/review graph dispatch
- `RetrySessionCmd` — DONE for implementation/review graph dispatch
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

Status:

- Done: focused `FollowUpSessionCmd`, `FollowUpFailedSessionCmd`, and `RetrySessionCmd` now return dispatch acknowledgements immediately and run implementation/review graph continuations asynchronously.
- Done: async errors from these focused commands are logged and sent back through `program.Send` as `ErrMsg`.
- Done: `ResumeAllSessionsForWorkItemCmd` uses the work-item graph entry point asynchronously.

### 10. Centralize bulk resume/retry orchestration — DONE

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

Status:

- Done: `ImplementationService.ResumeRetryLeavesForWorkItem` exists, computes resumable/retryable graph leaves once, dispatches implementation and review leaves through graph entry points, routes interrupted planning leaves to planning resume when planning orchestration is wired, restarts Foreman once for graph-managed work, and returns skipped reasons for manual/unsupported leaves.
- Done: `ResumeAllSessionsForWorkItemCmd` dispatches this graph entry point off the Bubble Tea command path when implementation orchestration is wired.

### 13. Continuation persistence — DONE

Add a migration and repository/service layer for `agent_session_continuations`.

Files:

- `migrations/023_agent_session_continuations.sql`
- `internal/domain/session_continuation.go`
- `internal/repository/interfaces.go`
- `internal/repository/sqlite/session_continuation.go`
- `internal/service/session_continuation.go`

The service should provide idempotent primitives:

```go
CreatePending(ctx, agentSessionID, kind)
Start(ctx, continuationID)
Complete(ctx, continuationID)
Fail(ctx, continuationID, err)
ListRecoverable(ctx, workspaceID)
```

The graph orchestrator should use these primitives instead of inferring continuation state from `agent_sessions`, `review_cycles`, `sub_plans`, and `work_items`.

The TUI should not know active sub-plan maps, leaf supersession, or kind-specific orchestration rules.

Status:

- Done: all listed files exist.
- Done: service primitives exist with idempotent `CreatePending`, transition validation, `LastError` preservation, and recoverable queries.
- Done: graph continuation uses the service primitives instead of event-only inference.

### 11. Service-layer cleanup — DONE

#### `AgentSessionService.CreateResumeChild` — DONE

The interrupted-session primitive is named for what it does: create a running
child from an interrupted source leaf. It is not full orchestration.

Status:

- Done: `AgentSessionService.CreateResumeChild` accepts a source ID, reloads the source inside the transaction, validates the source is still a graph leaf, preserves kind, sets `ParentAgentSessionID`, creates a running child, and emits old/new IDs.

### `migrations/023_agent_session_continuations.sql`

- Add `agent_session_continuations` table with foreign key to `agent_sessions`.
- Store status, kind, attempt, last_error, timestamps.
- Add uniqueness/indexes for active continuation lookup and startup recovery.
- Update repository structs and `db` tags together.

### `internal/domain/session_continuation.go`

- Define continuation statuses and transition rules.
- Keep continuation state separate from `AgentSessionStatus`; do not overload agent-session completion.

### `internal/service/session_continuation.go`

- Own continuation lifecycle transitions.
- Preserve error chains in `LastError` text and log all transition failures.
- Provide recoverable continuation queries for startup/rebuild flows.


#### `AgentSessionService.CreateRetryChild` — DONE

The failed-session primitive is named for what it does: create a running child
from a failed source leaf. It is not full orchestration.

Status:

- Done: `AgentSessionService.CreateRetryChild` accepts a source ID, reloads the source inside the transaction, validates graph-leaf status, preserves kind, creates an append-only child, and emits old/new IDs.

#### `RestartCompletedManualSession`

The old generic follow-up restart primitive was renamed to the manual-only `RestartCompletedManualSession`. Graph-managed implementation/review follow-up creates append-only child nodes.

### 12. Event model additions

Ensure all graph progression writes durable events:

- `agent_session.resumed` for child creation from interrupted source.
- `agent_session.follow_up` for child creation from completed/failed source with feedback, but payload must include old and new session IDs.
- `subplan.started`, `subplan.completed`, `subplan.failed`, `subplan.escalated` as applicable.
- `work_item.implementing/reviewing/completed/failed` already exists; use only after durable aggregate state changes.


TUI event decoding must preserve graph link metadata. Update `internal/tui/views/event_consumer.go`, `msgs.go`, and handlers/tests so `agent_session.resumed` and `agent_session.follow_up` messages carry both old/source session ID and new session ID. Do not decode only the new session and discard the edge; UI pending state and graph leaf refreshes must be able to clear or reload the superseded source deterministically.
Avoid UI-only completion messages for lifecycle state.

Status:

- Done: append-only child event payloads now include canonical `source_session_id` plus legacy `old_session_id`, top-level `agent_session_id`, and the full new session.
- Done: TUI event decoders preserve `source_session_id`/new session IDs for resumed and follow-up messages, with tests covering canonical graph edge metadata.
- Done: failed/completed feedback child creation now has a dedicated `CreateFollowUpChild` primitive that emits `agent_session.follow_up` with source/new IDs; legacy retry without feedback still emits `agent_session.resumed`.

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

- Replace or wrap legacy resume/follow-up row mutation primitives with append-only child creation primitives.
- Reload source sessions inside transactions instead of trusting caller-supplied structs.
- Preserve kind by default.
- Add transactional graph-leaf validation; do not allow historical non-leaf sources to create sibling retries.
- Emit old/new IDs consistently.

### `internal/orchestrator/agent_run_supervisor.go` (new)

- Consolidate harness lifecycle logic from:
  - `ImplementationService.runImplementation`
  - legacy resumption wait loops
  - `ReviewPipeline.startReviewAgent` where practical
- Own terminal agent-session transitions and `ResumeInfo` persistence.

### `internal/orchestrator/implementation.go`

- Add graph entry points for implementation resume/retry/follow-up.
- Replace direct continuation wrappers with graph-aware continuation.
- Pass `FirstReviewParentID` into review loop when retrying failed review leaves.
- Remove stale compensating branches once graph continuation is idempotent.

### `internal/orchestrator/review.go`

- Ensure review creation always accepts explicit parent context:
  - normal review parent = implementation session
  - retry/resume review parent = failed/interrupted review leaf
- Prefer one terminal handling path via supervisor, or clearly isolate review-specific event completion while preserving same graph callbacks.

### `internal/orchestrator/resume.go` — REMOVED

- Done: deleted the kind-blind resumption façade, prompt builders, follow-up helpers, and `WaitAndComplete`.

### `internal/tui/views/cmds.go`

- Replace direct orchestration composition with async intent dispatch.
- Remove direct lifecycle-wait and continuation-wrapper calls from TUI commands.
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

## Implementation Progress

- Graph leaf helpers and tests are present in `internal/domain/session.go` and `internal/domain/session_test.go`.
- Append-only resume/retry child creation primitives are now named `CreateResumeChild` and `CreateRetryChild`; both accept source IDs, reload source rows, and validate graph leaves transactionally.
- Durable continuation persistence is present:
  - migration `023_agent_session_continuations.sql`
  - domain model `internal/domain/session_continuation.go`
  - repository interface/resource wiring
  - SQLite repository `internal/repository/sqlite/session_continuation.go`
  - service primitives `internal/service/session_continuation.go`
- `ImplementationService.ContinueImplementationGraph` now creates pending continuation state, marks it running before review/sub-plan/work-item/finalization work, completes it after durable writes, and marks it failed with `LastError` when continuation work returns an error.
- `ImplementationService.RetryReviewLeaf` now provides the review-specific graph entry point and preserves failed-review → replacement-review parentage through `ContinueImplementationGraph`.
- `ImplementationService.StartImplementationGraphRun` now provides the implementation graph entry point for implementation leaves and rejects historical non-leaf implementation retry sources.
- Focused TUI follow-up/failed-follow-up/retry commands now dispatch implementation/review graph work asynchronously.
- Focused TUI graph command async error paths now use the Bubble Tea program sender so dispatch failures surface as `ErrMsg` instead of log-only failures.
- Bulk TUI resume now dispatches `ImplementationService.ResumeRetryLeavesForWorkItem` asynchronously when implementation orchestration is wired, so implementation/review leaves share the graph eligibility and continuation path.
- Overview action-card candidate selection now uses `RetryableAgentSessionLeaves` and `ResumableAgentSessionLeaves`, keeping action cards aligned with graph eligibility.
- Focused session retry now rejects superseded failed sessions and non-graph-managed failed kinds before dispatching `RetrySessionCmd`.
- Graph lifecycle event payloads now preserve canonical source/new session IDs through service emission and TUI decoding for append-only child edges.
- Follow-up child creation now uses `agent_session.follow_up` for explicit feedback-created failed/completed children, while plain retry/resume primitives keep `agent_session.resumed`.
- Startup recovery now scans recoverable implementation continuations for the workspace, resumes pending/running review continuations, and leaves failed continuation rows intact instead of replaying known-failed work.
- `AgentRunSupervisor` now centralizes implementation harness lifecycle and supports atomic completed-session + pending-continuation creation for graph continuations.
- Bulk graph resume now routes interrupted planning leaves through `PlanningService.ResumeInterruptedPlanning` when wired and starts Foreman once before implementation/review recovery.
- Completed implementation session metadata now includes active continuation status so pending/running/failed continuation state is visible separately from agent-session completion.
- Legacy `Resumption` service wiring, `resume.go`, `resumption_test.go`, `resume_test.go`, direct continuation wrappers, and obsolete follow-up completion messages were removed.
- Terminal agent-session helpers were moved out of `implementation.go` into `agent_session_terminal.go`.
- Manual session row reuse now goes through the explicitly manual `RestartCompletedManualSession` primitive.
- Bulk resume dispatch now propagates asynchronous graph dispatch failures through `ErrMsg`, and planning resume preserves the interrupted source session while creating a graph child.

## Migration Strategy

1. Land graph helpers and tests without behavior change.
2. Introduce `AgentRunSupervisor` and use it for new graph entry points.
3. Convert focused implementation retry to graph entry point first; it has the narrowest scope and existing tests.
4. Convert review retry/resume before bulk cutover. Bulk orchestration must not route review leaves through the old kind-blind resume path; until this step is complete, bulk must skip review leaves with durable skipped reasons.
5. Convert bulk resume/retry to graph entry point after both implementation and review leaf routes exist.
6. Convert completed/failed follow-up semantics.
7. Remove old TUI direct lifecycle waits and continuation-wrapper calls.
8. Delete obsolete resumption methods.
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