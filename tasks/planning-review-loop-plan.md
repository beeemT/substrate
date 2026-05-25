# Implementation Plan: Planning Review Loop

## Context

Today Substrate has two different review concepts:

1. **Human plan review**: the planning agent produces a plan, `PlanService.SubmitForReview` moves it from `draft` to `pending_review`, and the work item enters `plan_review` until the operator approves, edits, requests changes, or rejects it.
2. **Agent implementation review**: after each implementation run, `ImplementationService.reviewLoop` runs a review harness session, records `ReviewCycle` / `Critique` rows, and optionally loops implementation with critique feedback until the review passes, escalates, or fails.

This change adds an **agent planning review loop** between a valid planning draft and human plan review. The loop should mirror the implementation review loop's behavior and settings shape, but it must not remove the human approval gate. A passing agent plan review only means the plan is ready for the operator's existing `plan_review` UI.

## Research Evidence

### Existing implementation review loop

- `internal/orchestrator/implementation.go:525` — `ImplementationService.reviewLoop` is the outer implement -> review -> reimplement loop.
- `internal/orchestrator/review.go:69` — `ReviewPipeline.ReviewSession` creates a `ReviewCycle`, starts a review harness session, parses critiques, and returns `ReviewResult`.
- `internal/orchestrator/review.go:196` — `startReviewAgent` creates a separate `AgentSession` with `Phase: review`, registers it for steering, waits for `done`, then reads the harness output from the session log.
- `internal/orchestrator/review.go:390` — `makeDecision` applies `review.pass_threshold`.
- `internal/orchestrator/implementation.go:540` and `:548` — the outer loop uses `review.auto_feedback_loop` and `review.max_cycles` as the total-cycle guard.
- `internal/config/config.go:113` — `ReviewConfig` has `pass_threshold`, `max_cycles`, `timeout`, and `auto_feedback_loop`; defaults are `minor_ok`, `3`, `1h`, and `true` at `config.go:537-548`.
- `internal/domain/review.go` — `ReviewCycle` is keyed by `AgentSessionID`; `Critique` supports `FilePath`, optional `LineNumber`, `Description`, optional `Suggestion`, `Severity`, and status.

### Existing planning flow

- `internal/orchestrator/planning.go:159` — `Plan` transitions the work item to `planning`, finds any active plan to replace, then calls `planRun`.
- `internal/orchestrator/planning.go:182` — `PlanWithFeedback` captures the current plan, rejects it, transitions the work item back to `planning`, then calls `planRun` with revision feedback and prior resume info.
- `internal/orchestrator/planning.go:429` — `planRun` discovers repos, creates a planning `AgentSession`, runs the parse-correction loop, persists the plan, sets `agent_sessions.plan_id`, submits the plan, moves the work item to `plan_review`, then completes the planning session.
- `internal/orchestrator/planning.go:620` — `runPlanningWithCorrectionLoop` already loops on malformed draft output using `plan.max_parse_retries`; this is separate from semantic agent review.
- `internal/service/plan.go:259` — `SubmitForReview` is the `draft -> pending_review` transition and emits `plan.submitted`.
- `internal/service/work_item.go:471` — `SubmitPlanForReview` is the `planning -> plan_review` work-item transition.
- `internal/domain/session.go:21-22` — `AgentSession` already has both `SubPlanID` and `PlanID`, which is the right persistence hook for plan-level review sessions.

### TUI / settings / persistence surfaces

- `internal/tui/views/service_manager.go:303` wires a single `ReviewPipeline` only into `ImplementationService` today.
- `internal/tui/views/settings_service.go:627-642` exposes `plan.max_parse_retries` and implementation `review.*` settings, but no planning-review settings.
- `internal/service/session.go:236` currently requires `SubPlanID` for both implementation and review phases. Planning review sessions will need `Phase: review` with `PlanID` and no `SubPlanID`.
- `migrations/007_plan_supersede.sql` and current SQLite session rows already include nullable `agent_sessions.plan_id`; no new column is needed for plan review sessions.
- `internal/domain/event.go:95-100` already has review lifecycle events (`review.started`, `review.completed`, `review.critiques_found`, `reimplementation.started`, `review_cycle.status_changed`). The generic review events can be reused if payloads include enough identifiers for the TUI to distinguish plan-level and sub-plan-level review; `reimplementation.started` remains implementation-specific.

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

`ReviewPipeline.ReviewSession(ctx, req ReviewRequest)` becomes the new core and runs exactly one review cycle. It must not load `TaskPlan`/`Plan` itself, and it must not enforce `max_cycles` or `auto_feedback_loop`; those guards live in the implementation and planning outer loops. Keep a compatibility wrapper for implementation call sites if useful:

```go
func (p *ReviewPipeline) ReviewImplementationSession(ctx context.Context, agentSession domain.AgentSession) (*ReviewResult, error)
```

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

### 3. Model planning review sessions as review-phase sessions scoped to a plan candidate

A planning review harness invocation is a review session, so its `AgentSession.Phase` should be `domain.AgentSessionPhaseReview`.

Differences from implementation review sessions:

- `PlanID` set when a persisted plan exists; for pre-persistence candidate review it may be empty unless the plan candidate is persisted first.
- `SubPlanID`, `RepositoryName`, and `WorktreePath` may be empty.
- `WorkItemID` and `WorkspaceID` must always be set.

Update `AgentSessionService` validation so:

- implementation sessions continue to require `SubPlanID`. Current service validation does **not** require `RepositoryName` or `WorktreePath`; add those requirements only as an intentional behavior change after auditing all implementation-session creators and updating tests.
- review sessions require either:
  - `SubPlanID` for implementation review, or
  - a planning-reviewed session/work-item scope for pre-persistence planning review. `PlanID` should be set when reviewing an already-persisted plan, but must not be mandatory for raw candidate review.

Prefer setting `PlanID` whenever a persisted plan exists. For the recommended raw-candidate path, correlate through the reviewed planning session ID plus `work_item_id`; do not add schema just to satisfy validation.

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

### 6. Planning outer loop behavior

Add a `PlanningReviewLoop` or private `PlanningService.reviewPlanningCandidate` path:

```go
for cycle := 1; ; cycle++ {
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
        persist candidate and submit for human review with escalation context
        return
    }

    req = req.withRevisionFeedback(buildPlanningCritiqueFeedback(reviewResult.Critiques, candidate.RawContent))
    req.currentPlanText = candidate.RawContent
    req.priorSessionID = candidate.Session.ID
    req.priorResumeInfo = candidate.Session.ResumeInfo
    req.replacePlanID = originalReplacePlanID
}
```

Important details:

- Each automatic revision should create a new planning `AgentSession`, matching `PlanWithFeedback` and implementation re-runs.
- Preserve native resume when available, exactly like `PlanWithFeedback` and `runPlanningWithCorrectionLoop` already do.
- Mark each planning attempt session completed immediately after it produces a parse-valid candidate and native resume info has been durably stored, before starting that candidate's review session. If the agent review requests revision, the completed planning session remains audit history and the next automatic revision creates a fresh planning session. Final persistence must still call `SetPlanID` on the planning session that produced the selected plan, even if that session is already completed. The final work-item state stays `planning` until escalation/pass submits a candidate for human review.
- On context cancellation, preserve current interruption behavior: mark the active planning session interrupted using durable cleanup and do not fake a plan-review state.

### 7. Event payloads and TUI correlation

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

### 8. Persistence and migrations

Likely no schema migration is required for the core loop:

- `review_cycles.agent_session_id` can point to the planning session being reviewed.
- `critiques.review_cycle_id` remains unchanged.
- `agent_sessions.plan_id` already exists and can link plan-level review sessions to persisted plans.

Potential migration only if the TUI needs durable review scope without joining through `agent_sessions`:

```sql
ALTER TABLE review_cycles ADD COLUMN scope TEXT NOT NULL DEFAULT 'implementation';
```

Do not add this unless needed. The cheaper and less invasive approach is to infer scope from the reviewed `AgentSession.Phase` and whether `SubPlanID` or `PlanID` is set.

## Implementation Phases

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
6. Fix the settings description default for `review.auto_feedback_loop` if touched; current code default is `true`, while the field description currently says `false`.

Acceptance:

- Config load tests prove defaults and explicit false are preserved for both `review.auto_feedback_loop` and `planning_review.auto_feedback_loop`.
- Invalid `planning_review.pass_threshold` and `planning_review.max_cycles < 1` fail validation.
- If timeout validation is added, invalid `planning_review.timeout` and `review.timeout` fail consistently; otherwise tests document the current fallback-to-default behavior.
- Settings save/load tests cover editing `planning_review.*` fields.

### Phase 2 — Review pipeline generalization

Files:

- `internal/orchestrator/review.go`
- `internal/orchestrator/review_test.go`
- `internal/orchestrator/phase9_test.go`
- `internal/service/session.go`
- `internal/service/session_test.go`

Tasks:

1. Introduce `ReviewRequest` and `ReviewSubjectKind`; pass the existing `config.ReviewConfig` through the request when the one-shot runner needs pass-threshold or timeout settings.
2. Move implementation-specific prompt loading (`GetSubPlan`, `GetPlan`, `buildReviewPrompt`) out of the core review-session runner.
3. Keep `ReviewPipeline` responsible for persistence of `ReviewCycle`, review agent session lifecycle, event emission, per-request timeout, parsing, and pass-threshold decision logic.
4. Allow review-phase `AgentSession` rows that are plan-scoped rather than sub-plan-scoped.
5. Add `review_scope`, `plan_id`, and `review_agent_session_id` to emitted review payloads while retaining existing `agent_session_id` meaning: the reviewed session.
6. Remove `max_cycles` enforcement from the one-shot review runner; implementation and planning loops own total-cycle limits.

Acceptance:

- Existing implementation review tests pass unchanged or with minimal wrapper updates.
- New unit tests prove a planning-scope review request can create a review session without `SubPlanID` and without calling `GetSubPlan`.
- Event payload tests prove old implementation fields remain present and new scope fields are present.
- Tests prove `reimplementation.started` is not emitted for planning-scope critiques, while `review.critiques_found` still is.
- Tests prove max-cycle escalation is handled by the outer loop, not by `ReviewSession`.

### Phase 3 — Planning candidate split

Files:

- `internal/orchestrator/planning.go`
- `internal/orchestrator/planning_test.go`

Tasks:

1. Split `planRun` into candidate generation and candidate persistence.
2. Preserve every existing failure transition and durable cleanup path.
3. Preserve parse-correction loop behavior and tests.
4. Preserve `SetPlanID` on the planning session that produced the persisted plan.
5. Ensure replaced-plan handling still supersedes exactly one active prior plan.

Acceptance:

- Existing planning tests for parse retries, native resume, rejected-plan replacement, approved-plan replacement, and `PlanGenerated` event pass.
- New tests prove candidate generation alone does not create plan rows.
- New tests prove final persistence still transitions plan to `pending_review`, work item to `plan_review`, and completes the planning session.

### Phase 4 — Planning review loop

Files:

- `internal/orchestrator/planning.go`
- `internal/orchestrator/planning_review.go` (new, if it keeps `planning.go` smaller)
- `internal/orchestrator/template.go` or a new prompt file if prompts are separated
- `internal/orchestrator/planning_test.go`

Tasks:

1. Add planning review prompt builder.
2. Add planning critique feedback builder.
3. Inject `ReviewPipeline` and planning-review settings into `PlanningService` from `service_manager.go`.
4. Run planning review after a parse-valid candidate and before human `plan_review`.
5. On pass, persist candidate and submit to human review.
6. On critiques with auto-loop enabled, start a fresh planning attempt with critique feedback and prior resume data.
7. On critiques with auto-loop disabled or max cycles exceeded, persist best candidate and submit to human review with critique context.
8. On review harness error, fail planning unless a parse-valid candidate has already been produced and the error is explicitly classified as escalation; log all handled errors with `slog`.

Acceptance:

- Test: no critiques -> one planning session, one review session, one pending-review plan, work item `plan_review` and not `approved`.
- Test: major critique + auto loop true -> second planning session receives critique feedback; final plan comes from second candidate.
- Test: major critique + auto loop false -> first candidate persists; review cycle/critiques are recorded; work item enters `plan_review`.
- Test: max cycles -> best candidate persists and human plan review is required.
- Test: review harness failure before any persisted candidate -> planning fails and work item rolls back according to existing planning failure behavior.
- Test: context cancellation still interrupts the active planning session and does not submit a plan.
- Test: the final work item remains in `planning` until the selected candidate is persisted and submitted to human `plan_review`.

### Phase 5 — TUI review visibility

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

Acceptance:

- TUI event consumer tests cover planning review events and older implementation payloads without `review_scope`.
- Overview render tests include normal plan review and plan review with planning-review critiques.
- Width/height tests cover narrow terminal rendering for any new callout, per TUI rules.

### Phase 6 — Documentation and cleanup

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

## Open Decisions

1. Whether to persist candidate plans before agent review or review raw candidates before persistence. Recommendation: review raw candidates before final persistence to avoid plan-row churn.
2. Whether to add `review_cycles.scope`. Recommendation: avoid unless TUI queries prove too expensive or ambiguous.
3. Whether planning review failures with a valid draft should escalate to human review or fail planning. Recommendation: fail on infrastructure/harness errors before a complete reviewed cycle; escalate only for semantic critiques and max-cycle exhaustion.
