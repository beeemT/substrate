> **Implementation instruction (read this first)**
>
> Before starting any work on this plan, the implementing agent MUST explicitly raise and resolve every item in the **Open Questions** section at the bottom of this document. Each open question lists a recommended default; surface that default to the user, ask whether to proceed with the recommendation or pick a different option, and resolve ALL open questions in writing (in the implementation issue or PR description) before writing any code.
>
> This plan is a load-bearing architectural change with several mutually-incompatible off-ramps: raw-candidate vs. persisted-plan review, best-candidate criteria on max-cycle escalation, resume policy after a mid-loop crash, and operator escape semantics. Choosing later in code review is expensive — choose now, in conversation with the user, and only then start implementing.
>
> If a question cannot be resolved with available context, the implementing agent MUST stop and ask the user via the `ask` tool rather than guessing. Do not begin implementation while any open question is unresolved.

# Implementation Plan: Planning Review Loop

## Context

Today Substrate has two different review concepts:

1. **Human plan review**: the planning agent produces a plan, `PlanService.SubmitForReview` moves it from `draft` to `pending_review`, and the work item enters `plan_review` until the operator approves, edits, requests changes, or rejects it.
2. **Agent implementation review**: after each implementation run, `ImplementationService.reviewLoop` runs a review harness session, records `ReviewCycle` / `Critique` rows, and optionally loops implementation with critique feedback until the review passes, escalates, or fails.

This change adds an **agent planning review loop** between a valid planning draft and human plan review. The loop should mirror the implementation review loop's behavior and settings shape, but it must not remove the human approval gate. A passing agent plan review only means the plan is ready for the operator's existing `plan_review` UI.

## Research Evidence

### Existing implementation review loop (current line numbers)

- `internal/orchestrator/implementation.go:741` — `ImplementationService.reviewLoop` is the outer implement -> review -> reimplement loop.
- `internal/orchestrator/implementation.go:769` and `:778` — the outer loop reads `review.auto_feedback_loop` (line 769) and uses `review.max_cycles` as the total-cycle guard (line 778, pre-review; line 819, post-review).
- `internal/orchestrator/implementation.go:778` is a pre-iteration crash-recovery guard for resumed loops that are already over budget; preserve this shape in the planning outer loop.
- `internal/orchestrator/review.go:72` — `ReviewPipeline.ReviewSession` is the one-shot core that creates a `ReviewCycle`, starts a review harness session, parses critiques, and returns `ReviewResult`. Note: today this also enforces a per-session `max_cycles` check (`internal/orchestrator/review.go:108-121`); this enforcement must be removed when generalizing the runner.
- `internal/orchestrator/review.go:247` — `startReviewAgent` creates a separate `AgentSession` with `Kind: review`, registers it for steering, waits for `done`, then reads the harness output from the session log. **This is a direct `harness.StartSession` call and does NOT use `AgentRunSupervisor`; the new planning review must fix this rather than replicate the gap.**
- `internal/orchestrator/review.go:441` — `makeDecision` applies `review.pass_threshold`.
- `internal/orchestrator/agent_run_supervisor.go` — `AgentRunSupervisor` is the graph-era harness lifecycle owner introduced by the agent-session-graph work. The new planning review should run through it.
- `internal/orchestrator/implementation.go:1115-1206` — `RetryReviewLeaf` and `nearestCompletedImplementationAncestor` walk ancestors to find the reviewed session. There is no equivalent for planning review yet; Phase 4 must add it.
- `internal/config/config.go:114` — `ReviewConfig` has `pass_threshold`, `max_cycles`, `timeout`, and `auto_feedback_loop`; defaults are `minor_ok`, `3`, `1h`, and `true` at `config.go:541-549`.
- `internal/domain/review.go` — `ReviewCycle` is keyed by `AgentSessionID`; `Critique` supports `FilePath`, optional `LineNumber`, `Description`, optional `Suggestion`, `Severity`, and status.
- `internal/domain/session.go:140-143` — `AgentSessionKind` is the field formerly called `AgentSessionPhase`; migration 019 renamed the column from `phase` to `kind` and added the `foreman` value. The current code validates by `Kind`, not `Phase`.

### Existing planning flow (current line numbers)

- `internal/orchestrator/planning.go:161` — `Plan` transitions the work item to `planning`, finds any active plan to replace, then calls `planRun`.
- `internal/orchestrator/planning.go:184` — `PlanWithFeedback` captures the current plan, rejects it, transitions the work item back to `planning`, then calls `planRun` with revision feedback and prior resume info. **Bug to fix in Phase 2:** `PlanWithFeedback` does not set `planRunRequest.parentSessionID`, which violates the graph invariant that every replacement session links to its superseded leaf. The candidate-split work must correct this.
- `internal/orchestrator/planning.go:210` — `ResumeInterruptedPlanning` correctly sets `parentSessionID = interrupted.ID`. This is the shape to mirror in the auto-review loop.
- `internal/orchestrator/planning.go:432` — `planRun` discovers repos, creates a planning `AgentSession`, runs the parse-correction loop, persists the plan, sets `agent_sessions.plan_id`, submits the plan, moves the work item to `plan_review`, then completes the planning session. Lines 432-521 + 543-558 are candidate generation; 559-596 are persistence.
- `internal/orchestrator/planning.go:622` — `runPlanningWithCorrectionLoop` already loops on malformed draft output using `plan.max_parse_retries`; this is separate from semantic agent review.
- `internal/orchestrator/planning.go:680` — the planning session is started via a direct `harness.StartSession` call, not via `AgentRunSupervisor`. Phase 3 should migrate this to the supervisor for consistency with the graph-era abstractions.
- `internal/service/plan.go:260` — `SubmitForReview` is the `draft -> pending_review` transition and emits `plan.submitted`.
- `internal/service/plan.go:613` — `CreatePlanAtomic` emits `EventPlanGenerated` when a new plan row is created. The new auto-review loop emits this exactly once, at the end.
- `internal/service/work_item.go:472` — `SubmitPlanForReview` is the `planning -> plan_review` work-item transition.
- `internal/domain/session.go:21-22` — `AgentSession` already has both `SubPlanID` and `PlanID`, which is the right persistence hook for plan-level review sessions.

### TUI / settings / persistence surfaces (current line numbers)

- `internal/tui/views/service_manager.go:310-313` wires a single `ReviewPipeline` only into `ImplementationService` today. The new planning review must inject the same pipeline into `PlanningService`.
- `internal/tui/views/service_manager.go:317` — `NewImplementationService` already accepts a `*ReviewPipeline` parameter; the same wiring exists for planning.
- `internal/tui/views/settings_service.go:970-1004` exposes `plan.max_parse_retries` and `review.*` settings setters; `planning_review.*` setters are added in Phase 1.
- `internal/tui/views/settings_service.go:1196-1204` exposes the descriptions and default-value strings for `plan.*` and `review.*`. **Bug to fix in Phase 1:** `review.auto_feedback_loop` description default currently says `"false"` while the actual code default at `config.go:548` is `true`. `planning_review.auto_feedback_loop` description default must match its real default (`"true"`).
- `internal/service/session.go:240-258` — `AgentSessionService.Create` currently requires `SubPlanID` for both implementation and review kinds. Planning review sessions need `Kind: review` with `PlanID` and no `SubPlanID` (or neither set, for raw-candidate review).
- `internal/service/session_continuation.go` — `AgentSessionContinuationService` is the source of truth for kind-specific post-session work. The new auto-review loop does NOT need a new continuation kind: it is fully in-process and in-memory until the candidate is persisted, so a continuation would be ceremony without a beneficiary.
- `migrations/019_agent_session_kind.sql` — renamed `agent_sessions.phase` to `agent_sessions.kind` and added `foreman` to the CHECK constraint. `migrations/007_plan_supersede.sql` added the nullable `agent_sessions.plan_id` column. No new migration is required for the core loop.
- `internal/domain/event.go:89-93` — review lifecycle events (`review.started`, `review.completed`, `review.critiques_found`, `reimplementation.started`, `review_cycle.status_changed`). `reimplementation.started` is implementation-specific; planning replans must NOT emit it. The TUI consumes review events in `internal/tui/views/event_consumer.go:78-82` and renders them via the existing typed message cases in `internal/tui/views/msgs.go:147-161`.
- `internal/tui/views/event_consumer.go:319-353` — `decodeReviewStarted`, `decodeReviewCompleted`, `decodeCritiquesFound` currently extract `SessionID` from `agent_session_id` (the reviewed session). Phase 6 must extend these decoders to also read `review_scope`, `plan_id`, and `review_agent_session_id` without breaking older payloads.

## Product Behavior

### Target flow

```text
work item ingested
  -> planning
      planning attempt produces a parse-valid draft
      agent planning review starts
        -> pass: persist draft, submit for human plan_review
        -> critiques + auto loop enabled: start a new planning attempt with critique feedback, then review again
        -> critiques + auto loop disabled: persist draft, submit for human plan_review with critique context
        -> max cycles exceeded: persist the best parse-valid draft, submit for human plan_review with escalation context
        -> review harness failure: fail planning unless a valid draft exists and policy says to escalate to human
  -> plan_review
      existing human approve / edit / request changes / reject flow remains authoritative
  -> approved
  -> implementing
```

### Non-goals

- Do not auto-approve plans. Human approval remains required before implementation.
- Do not replace parse correction. `plan.max_parse_retries` still repairs malformed drafts before semantic review runs.
- Do not create PR/MR review artifacts for planning review. Planning review uses `ReviewCycle` / `Critique`, not `session_review_artifacts`.
- Do not rename product-level TUI "Tasks" concepts.
- Do not wire Foreman question routing into planning review sessions. Planning review does not interact with the foreman; the review harness prompt is non-interactive. The next maintainer must not be tempted to wire `BeginForeman` into the planning review path.
- Do not introduce a new `AgentSessionContinuation` kind for planning review. The loop is fully in-process and in-memory until the candidate is persisted; a continuation would be ceremony without a beneficiary.
- Do not auto-resume the auto-review loop on `ServiceManager` startup. Recovery follows the existing `RecoverContinuationsForWorkspace` pattern: operator-initiated only.

## Design Decisions

### 1. Extract a reusable review-session runner, not a single giant generic loop

Generalize the common part of review: starting a review harness session, creating a `ReviewCycle`, parsing structured critiques, applying pass-threshold decision logic, emitting review events, timeout handling, session registry registration, and log reading.

Keep the outer loops domain-specific:

- Implementation loop knows how to re-run implementation in a worktree and mark sub-plans complete/failed/escalated.
- Planning loop knows how to re-run planning, preserve current draft text, persist only the selected plan, and move the root work item to `plan_review`.

This avoids an abstraction that has to understand both `TaskPlan` lifecycle and `Plan` lifecycle.

Recommended shape:

```go
type ReviewSubjectKind string

const (
    ReviewSubjectImplementation ReviewSubjectKind = "implementation"
    ReviewSubjectPlanning       ReviewSubjectKind = "planning"
)

type ReviewRequest struct {
    Kind            ReviewSubjectKind
    ReviewedSession domain.AgentSession // implementation or planning session being reviewed
    WorkspaceID     string
    PlanID          string
    SubPlanID       string
    RepositoryName  string
    WorktreePath    string
    SystemPrompt    string
    UserPrompt      string
    // ReviewSession is one-shot. It may read pass_threshold and timeout from Settings,
    // but max_cycles and auto_feedback_loop belong to the domain-specific outer loops.
    Settings        config.ReviewConfig
}
```

`ReviewPipeline.ReviewSession(ctx, req ReviewRequest)` becomes the new core and runs exactly one review cycle. It must not load `TaskPlan`/`Plan` itself, and it must not enforce `max_cycles` or `auto_feedback_loop`; those guards live in the implementation and planning outer loops. **Note:** The existing `ReviewSession` implementation at `internal/orchestrator/review.go:79-93` currently enforces `max_cycles` — this enforcement must be actively removed from the extracted one-shot runner. Keep a compatibility wrapper for implementation call sites if useful:

```go
func (p *ReviewPipeline) ReviewImplementationSession(ctx context.Context, agentSession domain.AgentSession) (*ReviewResult, error)
```

**Harness lifecycle MUST go through `AgentRunSupervisor`.** The existing `startReviewAgent` (`internal/orchestrator/review.go:247`) calls `harness.StartSession` directly and uses an ad-hoc `done` channel. The agent-session-graph work introduced `AgentRunSupervisor` (`internal/orchestrator/agent_run_supervisor.go`) as the graph-era harness lifecycle owner, and the new planning review must not stack on the direct-harness pattern. Phase 2 migrates `startReviewAgent` to the supervisor as part of the runner extraction. The supervisor's `Start(ctx, AgentRunRequest)` returns after harness start; the runner registers the `OnCompleted` callback to run `makeDecision` and emit events. The supervisor's `OnFailed` callback transitions the review cycle to `failed` (replacing the current `defer` block in `ReviewSessionWithParent` at `internal/orchestrator/review.go:165-178`).

### 2. Add planning-review settings as a sibling to implementation review settings

Do not silently reuse `review.*` for planning. Operators may want implementation review strictness to differ from plan review strictness, and changing `review.max_cycles` today should not unexpectedly change planning behavior.

Add a config section that mirrors the existing fields:

```yaml
planning_review:
  pass_threshold: minor_ok
  max_cycles: 3
  timeout: 1h
  auto_feedback_loop: true
```

Implementation detail:

```go
type Config struct {
    Plan           PlanConfig   `yaml:"plan"`
    Review         ReviewConfig `yaml:"review"`
    PlanningReview ReviewConfig `yaml:"planning_review"`
    // ...
}
```

Use the existing `ReviewConfig` type for both `review.*` and `planning_review.*`; it already has the exact fields needed. Do not introduce a second config type unless implementation proves the two sections need to diverge. Keep existing `review.*` YAML and Go call sites compatible. Add a generic timeout helper on `ReviewConfig`, for example `TimeoutDuration(defaultValue time.Duration) time.Duration`, and keep `ReviewTimeout()` as a compatibility wrapper for implementation review call sites.

Defaults for `planning_review.*` should match `review.*`: `minor_ok`, `3`, `1h`, `true`.

**Settings description default fix.** `internal/tui/views/settings_service.go:1203-1204` currently reports `"false"` as the default for `review.auto_feedback_loop`, while the actual code default at `internal/config/config.go:548` is `true`. Phase 1 must correct this for both `review.auto_feedback_loop` and the new `planning_review.auto_feedback_loop` — operators reading the field description must see the real default, not a stale one.

### 3. Model planning review sessions as review-kind sessions scoped to a plan candidate

A planning review harness invocation is a review session, so its `AgentSession.Kind` is `domain.AgentSessionKindReview` (formerly `AgentSessionPhaseReview`; migration 019 renamed the column from `phase` to `kind`).

Differences from implementation review sessions:

- `PlanID` set when a persisted plan exists; for pre-persistence candidate review it is empty.
- `SubPlanID`, `RepositoryName`, and `WorktreePath` are empty.
- `WorkItemID` and `WorkspaceID` must always be set.
- `ParentAgentSessionID` is the reviewed planning session's ID (the agent-session-graph edge from prior planning to its review).

Reshape `AgentSessionService.Create` validation in `internal/service/session.go:240-258`:

- `Kind = Implementation` continues to require `SubPlanID` (unchanged).
- `Kind = Review` requires **at least one** of `SubPlanID` (implementation review) or `PlanID` (reviewing a persisted plan). For raw-candidate planning review both may be empty; the graph link is `WorkItemID` + `ParentAgentSessionID` pointing at the planning session being reviewed.
- Reject review sessions with all of `SubPlanID`, `PlanID`, and `WorkItemID` empty (no scope).

Prefer setting `PlanID` whenever a persisted plan exists. For the recommended raw-candidate path, correlate through the reviewed planning session ID plus `work_item_id`; do not add schema just to satisfy validation.

Tests for the validation reshape (added in Phase 2):

- Review session with `SubPlanID="sp-1"`, no `PlanID`, no `WorkItemID` -> rejected (current behavior preserved for implementation review).
- Review session with `SubPlanID="sp-1"`, `WorkItemID="wi-1"` -> accepted (implementation review).
- Review session with `PlanID="plan-1"`, no `SubPlanID`, `WorkItemID="wi-1"` -> accepted (planning review of a persisted plan).
- Review session with no `SubPlanID`, no `PlanID`, `WorkItemID="wi-1"` -> accepted (raw-candidate planning review).
- Review session with no `SubPlanID`, no `PlanID`, no `WorkItemID` -> rejected.

### 4. Prefer reviewing persisted candidate plans, but avoid plan-row churn if it complicates state

Two implementation strategies are viable:

#### Recommended: split planning into candidate generation and final persistence

Refactor `planRun` into:

1. `runPlanningAttempt(ctx, req) -> planningCandidate`
   - creates and runs one planning `AgentSession`
   - applies parse correction
   - returns raw valid plan text, parsed output, planning session, warnings, repos, workspace/work item context
   - does **not** persist plan rows
2. `persistPlanningCandidate(ctx, candidate, replacePlanID) -> PlanningResult`
   - wraps the existing `buildAndPersistPlan`, `SetPlanID`, `SubmitForReview`, and `SubmitPlanForReview` sequence
   - does not normally complete the planning session, because `runPlanningAttempt` completes it as soon as the candidate is parse-valid and resume info is durable

Planning review then loops over candidates before final persistence. This avoids creating rejected/superseded plan rows for every automatic review revision.

Hazard: review sessions cannot set `PlanID` before persistence. The review events and cycles must therefore be queryable by `ReviewedSession.ID` and `WorkItemID`. This already matches `ReviewCycle.AgentSessionID`.

#### Alternative: persist each candidate, review persisted plan, supersede on revision

This makes every review session have `PlanID`, but it creates more plan rows and more state transitions. It also risks noisy TUI updates if the work item enters `plan_review` before agent review is complete.

Use this only if `PlanID` is required for TUI drilldown or event correlation. If chosen, keep the work item in `planning` until the final candidate is ready for human review.

**Best-candidate selection on max-cycle escalation.** When the auto-loop exits with `cycle >= settings.MaxCycles` (or any other "no more iterations" reason), it must persist *some* candidate so the operator gets a `plan_review` to look at. The candidate-selection rule is a separate decision (see Open Question #2) and the recommended default is **the latest candidate whose critiques are all ≤ the configured pass threshold's "ignore" level** — that is, the most recent candidate that the review harness would have passed if the loop had stopped one iteration earlier. If no candidate meets that bar, fall back to the most recent candidate (the one the agent just critiqued) and surface the critique context in the human `plan_review` callout. The selection function takes the list of `(cycleIndex, planningSessionID, critiques)` tuples produced by the loop and returns the chosen `planningCandidate` for `persistPlanningCandidate`.

### 5. Planning review prompt and critique feedback

Add a planning-specific review prompt builder. It should evaluate the full plan document against:

- work item title/description/source context
- workspace `AGENTS.md`
- discovered repo set and execution groups
- parser/validator expectations that passed syntactically but may still be semantically weak
- completeness of each sub-plan: goal, scope, concrete changes, validation, risks, cross-repo ordering
- likely implementation ambiguity, missing dependency ordering, unsafe assumptions, and untestable validation

Output format should reuse the existing critique block format so `parseCritiques` can stay generic:

```text
NO_CRITIQUES
```

or:

```text
CRITIQUE
File: plan
Severity: critical | major | minor | nit
Description: <what is wrong and what a revised plan must change>
END_CRITIQUE
```

Add `buildPlanningCritiqueFeedback(critiques []domain.Critique, rawPlan string) string`, separate from implementation's `buildCritiqueFeedback`, because planning feedback should refer to plan sections and expected revisions, not code files.

**No foreman for planning review.** The planning review harness prompt must be non-interactive: the harness reviews the plan, outputs `CRITIQUE` blocks or `NO_CRITIQUES`, and exits. Do not wire foreman question routing into the review session; do not enable the question router for planning review sessions. The plan reviewer's job is to evaluate, not to consult. If the operator wants to override an agent's review verdict, the existing `plan_review` UI (and `Request changes` action) is the surface for that.

### 6. Planning outer loop behavior

Add a `PlanningReviewLoop` or private `PlanningService.reviewPlanningCandidate` path:

```go
// Pre-iteration guard mirrors implementation.go:778. Fires on entry for
// resumed loops that are already over budget, and after each review
// result before starting the next attempt. The per-session max_cycles
// check inside the one-shot review runner is removed; the planning
// outer loop owns total-cycle limits.
if cycle > settings.MaxCycles {
    return persistBestCandidateAndEscalate(...)
}

for {
    candidate := runPlanningAttempt(ctx, req)

    reviewResult := planningReview.ReviewCandidate(ctx, candidate, cycle)
    if reviewResult.Passed {
        return persistPlanningCandidate(ctx, candidate, req.replacePlanID)
    }

    if reviewResult.Escalated || !settings.AutoFeedbackLoop || !reviewResult.NeedsReimpl {
        result := persistPlanningCandidate(ctx, candidate, req.replacePlanID)
        result.ReviewEscalated = true // add field only if UI/domain needs it
        return result
    }

    if cycle >= settings.MaxCycles {
        return persistBestCandidateAndEscalate(...)
    }

    req = req.withRevisionFeedback(buildPlanningCritiqueFeedback(reviewResult.Critiques, candidate.RawContent))
    req.currentPlanText = candidate.RawContent
    // Graph edge: the new planning attempt's parent is the prior planning
    // attempt (the superseded leaf), NOT the prior review session.
    req.parentSessionID = candidate.Session.ID
    req.priorSessionID = candidate.Session.ID
    req.priorResumeInfo = candidate.Session.ResumeInfo
    req.replacePlanID = originalReplacePlanID
    cycle++
}
```

Important details:

- **Pre-iteration guard** (mirroring `internal/orchestrator/implementation.go:778`): the planning outer loop checks `cycle > settings.MaxCycles` on entry as well as after each review result. This handles the crash-recovery case where a resumed loop is already over budget before starting another attempt. The per-session `max_cycles` check inside the one-shot review runner (`internal/orchestrator/review.go:108-121`) is removed in Phase 2.
- **Graph edge for the new planning attempt**: `req.parentSessionID = candidate.Session.ID` (the prior planning attempt), NOT the prior review session's ID. The prior review is a child of the prior planning; the new attempt is a sibling child of the prior planning. `LeafAgentSessions` groups by `(Kind, SubPlanID, RepositoryName)` (`internal/domain/session.go:48`), so prior planning, prior review, and the new planning all get distinct leaf slots. This is the same shape as `implementation -> failed review -> replacement review` but with `planning` as the parent kind.
- Each automatic revision creates a new planning `AgentSession`, matching `PlanWithFeedback` and implementation re-runs.
- Preserve native resume when available, exactly like `PlanWithFeedback` and `runPlanningWithCorrectionLoop` already do.
- Mark each planning attempt session completed immediately after it produces a parse-valid candidate and native resume info has been durably stored, before starting that candidate's review session. If the agent review requests revision, the completed planning session remains audit history and the next automatic revision creates a fresh planning session. Final persistence must still call `SetPlanID` on the planning session that produced the selected plan, even if that session is already completed. The selected session is identified explicitly: `persistPlanningCandidate` takes the chosen `candidate.Session.ID` and updates the `agent_sessions.plan_id` column on that row, regardless of whether it was the most recent attempt (e.g. when the best-candidate rule picked an earlier attempt). The final work-item state stays `planning` until escalation/pass submits a candidate for human review.
- **Work-item state machine**: the new loop never transitions the work item to `plan_review` mid-loop. `EventPlanGenerated`, `EventPlanSubmitted`, and `EventPlanStatusChanged` are emitted exactly once, at the end, from `persistPlanningCandidate`. Tests must assert that no plan events fire before the final candidate is persisted — otherwise the TUI's plan view flickers on every iteration.
- **Crash-recovery policy**: the new loop is NOT auto-resumed on `ServiceManager` startup, matching the existing `RecoverContinuationsForWorkspace` policy. The TUI must offer explicit "resume planning loop" / "skip to human review" commands (Phase 6). On resume, the loop re-enters at the pre-iteration guard and re-creates the next planning attempt with `parentSessionID = prior.Session.ID`.
- On context cancellation, preserve current interruption behavior: mark the active planning session interrupted using durable cleanup and do not fake a plan-review state. Context cancellation propagates to the active review harness session through the supervisor and durably marks the review cycle `failed`.

### 7. Planning review retry path

The graph plan's `ImplementationService.RetryReviewLeaf` (`internal/orchestrator/implementation.go:1115-1206`) handles interrupted/failed implementation review leaves by walking ancestors via `nearestCompletedImplementationAncestor` and re-issuing the review with the failed review as the new review's graph parent. There is no equivalent for planning review. **Without this, a planning review harness that fails or is interrupted leaves a dead leaf that no bulk-resume command can recover.**

Add a new entry point on `ImplementationService` (or a new planning-owned sibling):

```go
// RetryPlanningReviewLeaf re-issues a planning review harness session from a
// failed or interrupted planning review leaf while preserving the graph edge
// from the planning attempt to the replacement review. Walks parent of the
// review leaf to the planning session (Kind=planning) and validates the
// planning session is still the source of truth for the work item.
func (s *ImplementationService) RetryPlanningReviewLeaf(ctx context.Context, intent AgentGraphIntent) (AgentGraphRunResult, error)
```

The retry path is a graph entry point and dispatches async. The TUI's `ResumeRetryLeavesForWorkItem` (`internal/orchestrator/implementation.go:852-945`) routes review leaves to either `RetryReviewLeaf` (when the reviewed session is an implementation) or `RetryPlanningReviewLeaf` (when the reviewed session is a planning) based on the leaf's `Kind` after walking `ParentAgentSessionID`.

If the prior planning session is also `interrupted`/`failed`, the retry path must surface that as a separate retry target (handled by the existing `ResumeInterruptedPlanning` in `internal/orchestrator/planning.go:210`). The two retry paths compose: first resume the planning attempt, then the next review leaf retry finds the resumed attempt as its parent.

Acceptance for the retry path (Phase 5):

- `RetryPlanningReviewLeaf` succeeds when the leaf is a planning review with a completed planning parent.
- `RetryPlanningReviewLeaf` returns a clear error when the leaf's parent is not a planning session.
- `ResumeRetryLeavesForWorkItem` correctly dispatches: implementation leaves to `RetryReviewLeaf`, planning review leaves to `RetryPlanningReviewLeaf`, planning leaves to `ResumeInterruptedPlanning`.
- The new replacement review session has `ParentAgentSessionID = failedReview.ID` (same shape as `RetryReviewLeaf`).

### 8. Event payloads and TUI correlation

Reuse generic review events, but extend payloads to include scope. Emit `review.started`, `review.completed`, and `review.critiques_found` for both planning and implementation scopes. Emit `reimplementation.started` only for implementation scope; do not use that event name for planning replans.

```json
{
  "work_item_id": "...",
  "agent_session_id": "reviewed planning session id",
  "review_agent_session_id": "actual review harness session id",
  "plan_id": "optional persisted plan id",
  "sub_plan_id": "optional implementation sub-plan id",
  "review_scope": "planning",
  "cycle_number": 1,
  "cycle": {...}
}
```

For implementation review, emit `review_scope: "implementation"` and preserve existing fields. Preserve the current meaning of `agent_session_id`: it is the reviewed session ID used to load `ReviewCycle` rows. `review_agent_session_id` is additive metadata for the actual harness session and must not replace `agent_session_id` in existing reload paths.

TUI behavior:

- Existing `plan_review` overview remains the human action surface.
- If planning review escalated or auto-loop is disabled, show a non-blocking callout in the plan review action card: "Agent plan review found issues; inspect critiques before approving." Do not block the existing approve/edit/request-changes actions.
- Planning/review child sessions should appear in the Tasks sidebar in chronological order. Do not rename `taskSidebar*`, `LoadTasks*`, or other product Tasks-view symbols.
- If review events are handled in `event_consumer.go`, decode `review_scope`, `plan_id`, and `review_agent_session_id` without breaking older implementation payloads.
- **Operator escape valve**: the TUI must offer a "Submit current best to human review" command visible while a planning review loop is in progress. The command calls `persistPlanningCandidate` immediately with the most recent candidate (whatever the agent just critiqued, or the latest passing one), terminating the loop and moving the work item to `plan_review`. This is the operator escape hatch from a loop that has been running too long. The command is only meaningful when the work item is in `planning` state and the loop has produced at least one parse-valid candidate.

### 9. Persistence and migrations

Likely no schema migration is required for the core loop:

- `review_cycles.agent_session_id` can point to the planning session being reviewed.
- `critiques.review_cycle_id` remains unchanged.
- `agent_sessions.plan_id` already exists and can link plan-level review sessions to persisted plans.

Potential migration only if the TUI needs durable review scope without joining through `agent_sessions`:

```sql
ALTER TABLE review_cycles ADD COLUMN scope TEXT NOT NULL DEFAULT 'implementation';
```

Do not add this unless needed. The cheaper and less invasive approach is to infer scope from the reviewed `AgentSession.Kind` and whether `SubPlanID` or `PlanID` is set.


## Implementation Phases

The phases are ordered so each phase's preconditions are in place before the next. Phase 2 is intentionally ahead of Phase 3: the candidate split is the larger refactor and lays the groundwork the pipeline generalization and the new loop both need. Phase 4 (planning review retry path) is its own phase because it is a new graph entry point that depends on the new review pipeline from Phase 3.

### Phase 1 — Config and settings

Files:

- `internal/config/config.go`
- `internal/config/config_test.go`
- `internal/tui/views/settings_service.go`
- `internal/tui/views/settings_page_test.go` if settings rendering expectations need updates

Tasks:

1. Add `Config.PlanningReview ReviewConfig` with YAML key `planning_review`; reuse the existing `ReviewConfig` type rather than adding a parallel config struct.
2. Keep existing `Config.Review` YAML and Go call sites compatible.
3. Apply defaults for `planning_review`: `pass_threshold=minor_ok`, `max_cycles=3`, `timeout=1h`, `auto_feedback_loop=true`.
4. Validate `planning_review.pass_threshold` and `planning_review.max_cycles` with the same rules as `review.*`.
5. Add Settings UI section "Planning Review" with the four mirrored fields.
6. Fix the settings description default for `review.auto_feedback_loop` (currently `"false"` at `settings_service.go:1203-1204`, real default is `true` at `config.go:548`) and the new `planning_review.auto_feedback_loop` description default so both report `"true"`.

Acceptance:

- Config load tests prove defaults and explicit false are preserved for both `review.auto_feedback_loop` and `planning_review.auto_feedback_loop`.
- Invalid `planning_review.pass_threshold` and `planning_review.max_cycles < 1` fail validation.
- If timeout validation is added, invalid `planning_review.timeout` and `review.timeout` fail consistently; otherwise tests document the current fallback-to-default behavior.
- Settings save/load tests cover editing `planning_review.*` fields.
- Settings description-default test asserts the displayed default string for `review.auto_feedback_loop` and `planning_review.auto_feedback_loop` is `"true"`.

### Phase 2 — Planning candidate split

Files:

- `internal/orchestrator/planning.go`
- `internal/orchestrator/planning_test.go`
- `internal/orchestrator/agent_run_supervisor.go` (no behavior change; verify the planning session transition continues to call `sessionSvc.Complete` correctly)

Tasks:

1. Split `planRun` (`internal/orchestrator/planning.go:432`) into `runPlanningAttempt(ctx, req) -> planningCandidate` and `persistPlanningCandidate(ctx, candidate, replacePlanID) -> PlanningResult`. The split point: candidate generation is lines 432-521 + 543-558; persistence is lines 559-596.
2. `runPlanningAttempt` runs the parse-correction loop, marks the planning session `completed` once a parse-valid candidate + durable resume info is available, and returns a `planningCandidate` value with raw content, parsed output, planning session, warnings, repos, workspace, and work-item context. It does NOT call `SubmitForReview`, `SubmitPlanForReview`, `SetPlanID`, or `buildAndPersistPlan`.
3. `persistPlanningCandidate` wraps the existing `buildAndPersistPlan`, `SetPlanID`, `SubmitForReview`, and `SubmitPlanForReview` sequence. It does NOT normally complete the planning session (that already happened in `runPlanningAttempt`); the only path that completes the planning session here is when the operator escape valve or a crash-recovery path persists an already-completed candidate.
4. **Graph fix for `PlanWithFeedback`**: update `planRunRequest` to also carry `parentSessionID`. `PlanWithFeedback` (`planning.go:184`) must set `parentSessionID = priorSessionID` so the new plan session is linked to the prior planning session in the agent-session graph. This restores the graph invariant that every replacement session links to its superseded leaf.
5. Preserve every existing failure transition and durable cleanup path (parse-correction loop exhaustion, plan persistence error, `EventPlanFailed` emission, `revertWorkItemToIngested`).
6. Preserve the parse-correction loop behavior and existing tests for `parse retries`, `native resume`, `rejected-plan replacement`, `approved-plan replacement`, and `PlanGenerated` event.
7. Ensure replaced-plan handling still supersedes exactly one active prior plan via `CreatePlanAtomic`.
8. The migration of the planning harness to `AgentRunSupervisor` is deferred to Phase 3 to keep this phase focused on the candidate/persistence seam. Phase 3 migrates both `startReviewAgent` and the planning session harness call in the same supervisor sweep.

Acceptance:

- Existing planning tests pass unchanged.
- New tests prove `runPlanningAttempt` alone does not create plan rows, does not emit `EventPlanGenerated` / `EventPlanSubmitted` / `EventPlanStatusChanged`, and does not transition the work item.
- New tests prove `persistPlanningCandidate` still transitions plan to `pending_review`, work item to `plan_review`, calls `SetPlanID` on the provided candidate's planning session, and emits the three plan events exactly once.
- New test proves `PlanWithFeedback` sets `parentSessionID` on the new planning session (graph edge preserved across human-feedback replans).

### Phase 3 — Review pipeline generalization

Files:

- `internal/orchestrator/review.go`
- `internal/orchestrator/review_test.go`
- `internal/orchestrator/phase9_test.go`
- `internal/orchestrator/agent_run_supervisor.go`
- `internal/orchestrator/planning.go` (planning harness supervisor migration)
- `internal/service/session.go`
- `internal/service/session_test.go`

Tasks:

1. Introduce `ReviewRequest` and `ReviewSubjectKind`; pass the existing `config.ReviewConfig` through the request when the one-shot runner needs pass-threshold or timeout settings.
2. Move implementation-specific prompt loading (`GetSubPlan`, `GetPlan`, `buildReviewPrompt`) out of the core review-session runner.
3. Keep `ReviewPipeline` responsible for persistence of `ReviewCycle`, review agent session lifecycle, event emission, per-request timeout, parsing, and pass-threshold decision logic.
4. **Migrate `startReviewAgent` to `AgentRunSupervisor`**. The supervisor's `Start` returns after harness start; the runner registers the `OnCompleted` callback to run `makeDecision` and emit events. The supervisor's `OnFailed` callback transitions the review cycle to `failed`, replacing the current `defer` block in `ReviewSessionWithParent` (`internal/orchestrator/review.go:165-178`). The new `runReviewAgent` (or equivalent) returns the new review agent session ID and `domain.AgentSession` so the caller can record the cycle and emit events.
5. **Migrate the planning harness call to `AgentRunSupervisor`** at the same time. The current direct call in `runPlanningWithCorrectionLoop` (`internal/orchestrator/planning.go:680`) is migrated together with `startReviewAgent` so both phases land on the same supervisor-based harness lifecycle.
6. Allow review-kind `AgentSession` rows that are plan-scoped rather than sub-plan-scoped. Reshape `AgentSessionService.Create` validation per Design Decision #3 (the explicit test matrix of five cases).
7. Add `review_scope`, `plan_id`, and `review_agent_session_id` to emitted review payloads while retaining existing `agent_session_id` meaning: the reviewed session. Existing `decodeReviewStarted` / `decodeReviewCompleted` / `decodeCritiquesFound` (`internal/tui/views/event_consumer.go:319-353`) must continue to work for older payloads without these fields.
8. Remove `max_cycles` enforcement from the one-shot review runner (`internal/orchestrator/review.go:108-121`); implementation and planning loops own total-cycle limits. Add a regression test that proves a single `ReviewSession` call returns without enforcing the cap.

Acceptance:

- Existing implementation review tests pass unchanged or with minimal wrapper updates.
- New unit tests prove a planning-scope review request can create a review session without `SubPlanID` and without calling `GetSubPlan`.
- Validation test matrix (Design Decision #3) passes all five cases.
- Event payload tests prove old implementation fields remain present and new scope fields are present.
- Tests prove `reimplementation.started` is not emitted for planning-scope critiques, while `review.critiques_found` still is.
- Tests prove max-cycle escalation is handled by the outer loop, not by `ReviewSession`.
- Tests prove `startReviewAgent` now uses the supervisor (a stub harness registered via the supervisor's `Start` is sufficient).

### Phase 4 — Planning review retry path

Files:

- `internal/orchestrator/implementation.go` (or a new `internal/orchestrator/planning_review_retry.go` if it keeps the implementation file smaller)
- `internal/orchestrator/planning.go` (the planning-side helpers)
- `internal/orchestrator/implementation_test.go` and/or `planning_test.go`

Tasks:

1. Add `RetryPlanningReviewLeaf(ctx, intent AgentGraphIntent) (AgentGraphRunResult, error)` to `ImplementationService`. The function walks the failed/interrupted review leaf's `ParentAgentSessionID` to the planning session, validates the planning session is `Kind=planning` and `WorkItemID` matches the intent, and re-issues the review with `ParentAgentSessionID = failedReview.ID`.
2. Update `ResumeRetryLeavesForWorkItem` (`internal/orchestrator/implementation.go:852-945`) to dispatch review leaves to either `RetryReviewLeaf` (when the reviewed session is `Kind=implementation`) or `RetryPlanningReviewLeaf` (when the reviewed session is `Kind=planning`). The dispatch key is `reviewLeaf.ParentAgentSessionID`'s target kind.
3. If the prior planning session is also `interrupted` or `failed`, surface that as a separate retry target. `ResumeInterruptedPlanning` (`internal/orchestrator/planning.go:210`) handles the planning session side; do not duplicate it. The two paths compose: first resume the planning attempt, then the review leaf retry finds the resumed attempt.
4. Test that a planning review that fails leaves a leaf surfaced by `ResumableAgentSessionLeaves` / `RetryableAgentSessionLeaves` and is recovered by `RetryPlanningReviewLeaf` rather than the implementation-only `RetryReviewLeaf`.

Acceptance:

- `RetryPlanningReviewLeaf` succeeds when the leaf is a planning review with a completed planning parent.
- `RetryPlanningReviewLeaf` returns a clear error when the leaf's parent is not a planning session.
- `ResumeRetryLeavesForWorkItem` correctly dispatches: implementation leaves to `RetryReviewLeaf`, planning review leaves to `RetryPlanningReviewLeaf`, planning leaves to `ResumeInterruptedPlanning`.
- The new replacement review session has `ParentAgentSessionID = failedReview.ID` (same shape as `RetryReviewLeaf`).

### Phase 5 — Planning review loop

Files:

- `internal/orchestrator/planning.go`
- `internal/orchestrator/planning_review.go` (new, if it keeps `planning.go` smaller)
- `internal/orchestrator/template.go` or a new prompt file if prompts are separated
- `internal/orchestrator/planning_test.go`
- `internal/orchestrator/agent_run_supervisor.go` (no change expected; verify the supervisor's `Start` handles a planning review session request)
- `internal/tui/views/service_manager.go` (inject the `*ReviewPipeline` into `PlanningService`)

Tasks:

1. Add planning review prompt builder.
2. Add `buildPlanningCritiqueFeedback(critiques []domain.Critique, rawPlan string) string` separate from `buildCritiqueFeedback`.
3. Inject `ReviewPipeline` and planning-review settings into `PlanningService` from `service_manager.go`. The pipeline is the same instance already wired into `ImplementationService`; do not create a second one.
4. Implement the planning outer loop per Design Decision #6: pre-iteration guard, `parentSessionID` = prior planning attempt, max-cycle ownership, candidate selection rule from Design Decision #4.
5. On pass, persist candidate and submit to human review.
6. On critiques with auto-loop enabled, start a fresh planning attempt with critique feedback, `parentSessionID = priorPlanningAttempt.ID`, and prior resume data.
7. On critiques with auto-loop disabled or max cycles exceeded, persist the best candidate (per Design Decision #4) and submit to human review with critique context.
8. On review harness error, fail planning unless a parse-valid candidate has already been produced and the error is explicitly classified as escalation; log all handled errors with `slog`.
9. **Operator escape valve**: expose `SubmitCurrentBestCandidateToHumanReview(ctx, workItemID)` on `PlanningService` so the TUI's "Submit current best to human review" command (Phase 6) can call it. The function persists the most recent candidate immediately, terminating any in-progress loop and moving the work item to `plan_review`. No-op if the work item is not in `planning` state.

Acceptance:

- Test: no critiques -> one planning session, one review session, one pending-review plan, work item `plan_review` and not `approved`. No `EventPlanGenerated` / `EventPlanSubmitted` / `EventPlanStatusChanged` emitted before the final candidate is persisted.
- Test: major critique + auto loop true -> second planning session receives critique feedback; second planning session has `parentSessionID = firstPlanning.ID`; final plan comes from second candidate.
- Test: major critique + auto loop false -> first candidate persists; review cycle/critiques are recorded; work item enters `plan_review`.
- Test: max cycles -> best candidate (per Design Decision #4) persists and human plan review is required; the persisted plan is linked via `SetPlanID` to the chosen candidate's planning session, not necessarily the most recent one.
- Test: pre-iteration guard fires when a resumed loop enters with `cycle > settings.MaxCycles` (simulate by setting `MaxCycles=1` and providing a loop state with cycle 2).
- Test: review harness failure before any persisted candidate -> planning fails and work item rolls back according to existing planning failure behavior.
- Test: context cancellation still interrupts the active planning session and does not submit a plan.
- Test: the final work item remains in `planning` until the selected candidate is persisted and submitted to human `plan_review`.
- Test: `SubmitCurrentBestCandidateToHumanReview` is a no-op when work item is not in `planning`, persists immediately when in `planning` with at least one parse-valid candidate, and emits exactly one set of plan events.

### Phase 6 — TUI review visibility

Files:

- `internal/tui/views/event_consumer.go`
- `internal/tui/views/msgs.go`
- `internal/tui/views/app.go`
- `internal/tui/views/overview.go`
- `internal/tui/views/plan_review.go` if the plan review overlay shows critique callouts
- `internal/tui/AGENTS.md` must be read before editing these files

Tasks:

1. Decode new review event fields while preserving existing implementation-review decoding and preserving reviewed-session-ID reload behavior for `LoadReviewsCmd`.
2. Update in-memory overview/action data so `review_scope=planning` critiques can be surfaced on the plan review card.
3. Show planning-review escalation/caution copy only when critiques exist or the review loop escalated.
4. Keep existing plan approve/edit/request-changes/reject actions unchanged.
5. Ensure Tasks sidebar ordering remains planning session -> planning review session(s) -> plan review virtual node/repo nodes, without renaming product Tasks symbols.
6. **Operator escape valve command**: add a TUI command "Submit current best to human review" that calls `PlanningService.SubmitCurrentBestCandidateToHumanReview`. The command is available in the plan action card while the work item is in `planning` state and the loop has produced at least one parse-valid candidate; it is hidden otherwise. The command shows a confirmation prompt because it is destructive (terminates the in-progress loop and moves the work item to `plan_review`).

Acceptance:

- TUI event consumer tests cover planning review events and older implementation payloads without `review_scope`.
- Overview render tests include normal plan review and plan review with planning-review critiques.
- Width/height tests cover narrow terminal rendering for any new callout, per TUI rules.
- The escape-valve command is present and enabled only when the precondition is met; selecting it prompts for confirmation and the action is recorded in the work item history.

### Phase 7 — Documentation and cleanup

Files:

- `docs/05-orchestration.md`
- `docs/06-tui-design.md`
- `docs/03-event-system.md` if event payloads change
- `docs/01-domain-model.md` if review session scoping changes materially

Tasks:

1. Update orchestration docs to distinguish:
   - parse correction loop
   - agent planning review loop
   - human plan review loop
   - implementation review loop
2. Document `planning_review.*` settings.
3. Correct any stale review-loop doc that says unparseable implementation review output gets correction retries; current code treats it as no critiques.
4. Document review event payload additions.
5. Document the operator escape valve and crash-recovery policy.

Acceptance:

- Docs match actual state names and event names from `internal/domain`.
- No docs imply planning review auto-approves plans.


## Verification Plan

Run targeted tests by phase, then combined package tests:

```bash
go test ./internal/config -run 'Test.*Review|TestLoadInvalidPassThreshold'
go test ./internal/service -run 'TestAgentSession|TestReview|TestPlanService'
go test ./internal/orchestrator -run 'TestReviewSession|TestReviewLoop|TestRunPlanningWithCorrectionLoop|TestPlan_'
go test ./internal/tui/views -run 'TestSettingsPage|TestPlanReview|TestOverview|TestEventConsumer|TestSidebar'
```

Before merging the full change, run:

```bash
go test ./internal/config ./internal/service ./internal/orchestrator ./internal/tui/views
```

Manual scenario to verify from the TUI:

1. Create a work item that generates a syntactically valid but semantically incomplete plan using a scripted/fake harness.
2. Confirm planning review records critiques and auto-replans when `planning_review.auto_feedback_loop=true`.
3. Confirm the final plan still lands in human `plan_review`, not `approved`.
4. Set `planning_review.auto_feedback_loop=false` and confirm the first critiqued draft is shown to the operator with critique context.

## Risks and Mitigations

| Risk | Mitigation |
|---|---|
| Generic review abstraction becomes too broad | Generalize only the single review-session runner; keep implementation and planning outer loops separate. |
| Planning review changes existing plan persistence semantics | Split candidate generation from persistence and keep existing persistence sequence covered by tests. |
| Existing review TUI treats every review as repo/sub-plan review | Add `review_scope` to events and infer from session scope when absent. |
| Agent review critiques block human review indefinitely | Keep max-cycle ownership in the planning outer loop; never block on critiques after max cycles or when auto-loop is disabled; submit to human `plan_review` with critique context. |
| Settings surprise operators | Use separate `planning_review.*` settings instead of reusing `review.*`. |
| Review session validation rejects plan-scoped review sessions | Update `AgentSessionService` validation with explicit review-session scope rules and tests. |
| Plan review loop accidentally auto-approves | Tests must assert final state is `plan_review`, not `approved`, after agent review passes. |
| Mid-loop crash strands the work item in `planning` with no operator escape | TUI exposes "Submit current best to human review" command visible during the loop; crash recovery is operator-initiated, not automatic; `RetryPlanningReviewLeaf` recovers interrupted/failed review leaves. |
| Loop runs unbounded (e.g. `max_cycles=3` and per-cycle 1h harness timeout) with no operator escape | Pre-iteration guard at the planning outer loop mirrors the implementation loop's crash-recovery check; max-cycle ownership lives in the outer loop, never the one-shot runner. The TUI escape-valve command is always available while a parse-valid candidate exists. |
| Foreman question routing gets wired into planning review by mistake | Explicit non-goal in the plan; planning review harness prompt is non-interactive. Reviewing `BeginForeman` callers should be part of Phase 5's review checklist. |
| `EventPlanGenerated` flickers on every iteration and confuses the TUI | Tests assert that no plan events fire before the final candidate is persisted; the work item stays in `planning` for the entire loop. |
| Bulk resume path routes planning review leaves through `RetryReviewLeaf`, which fails because there is no implementation ancestor | Phase 4 adds `RetryPlanningReviewLeaf` and dispatches by reviewed-session `Kind`. |

## Open Questions

Each open question must be raised with the user and resolved in writing (in the implementation issue or PR description) before implementation begins. The recommended default is given for each so the agent can make progress once the user picks an answer, but the user is the authority.

1. **Candidate review strategy**: review raw candidates before persistence, or persist each candidate and supersede on revision?
   - **Recommended**: review raw candidates before final persistence to avoid plan-row churn. The TUI's plan view stays empty during the loop; plan events fire exactly once at the end.
   - **Alternative**: persist each candidate, review persisted plan, supersede on revision. More plan rows, but every review session has `PlanID` set.
2. **Best-candidate selection on max-cycle escalation**: which candidate should the loop persist when `cycle >= settings.MaxCycles` and we must submit to human review?
   - **Recommended**: the latest candidate whose critiques are all ≤ the configured pass threshold's "ignore" level (the most recent candidate the agent would have passed if the loop had stopped one iteration earlier). Fall back to the most recent candidate if no candidate meets that bar, and surface the critique context in the human `plan_review` callout.
   - **Alternatives**: always-latest (predictable, but human sees the plan the agent just critiqued); lowest-severity (best quality, but may be a much earlier candidate that doesn't reflect the agent's most recent thinking).
3. **`review_cycles.scope` schema column**: add a `scope` column to `review_cycles` for fast TUI queries, or infer scope from the reviewed session's `Kind` and `PlanID`?
   - **Recommended**: do not add. `agent_sessions.kind` and `agent_sessions.plan_id` are sufficient to disambiguate; the TUI can join through `agent_sessions` for scope-aware rendering.
4. **Planning review failure semantics**: when the review harness itself fails (infrastructure error), should planning fail or escalate to human review with the un-reviewed draft?
   - **Recommended**: infrastructure errors (harness crash, timeout) fail planning through the existing `EventPlanFailed` path; semantic critiques and max-cycle exhaustion escalate. This mirrors the implementation review behavior — `makeDecision` only escalates on semantic pass/fail, not on harness errors.
5. **Resume policy for an interrupted auto-review loop**: should the auto-review loop auto-resume on `ServiceManager` startup after a mid-loop crash, or be operator-initiated?
   - **Recommended**: operator-initiated only, matching the existing `RecoverContinuationsForWorkspace` policy. The TUI offers "Resume planning loop" and "Skip to human review" commands; auto-resume is intentionally not wired. The TUI must surface any non-`SessionPlanning` state as "recovery needed" if the loop was interrupted.
6. **Operator escape valve semantics**: when an operator force-submits the current best candidate mid-loop, what is the source of "best"?
   - **Recommended**: the most recent parse-valid candidate. If the loop is mid-review of a candidate, the candidate is the one currently being reviewed (not its successor). The escape valve is destructive (terminates the in-progress loop) and prompts for confirmation in the TUI.
7. **Foreman interaction for planning review**: should planning review harness sessions be wired to Foreman question routing?
   - **Recommended**: no. Planning review does not interact with Foreman. This is a non-goal documented in the plan, not a real question — included here so the next maintainer sees the explicit decision.
