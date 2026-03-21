# Review Pipeline Restructure — Orchestrator-Owned Per-Repo Lifecycle

## Problem

The review pipeline has three structural defects:

1. **Lockstep waves**: All repos in a wave must finish implementation before any enter review. Repos should be reviewed individually as each completes.

2. **Dead review loop**: `ReviewSession()` returns `NeedsReimpl: true`, but `RunReviewSessionCmd` drops everything except the review session ID. The TUI never dispatches reimplementation. The auto-fix loop is broken.

3. **TUI as orchestrator**: The TUI dispatches `RunReviewSessionCmd` per session after `ImplementationCompleteMsg`. Review is a domain concern — it should live in the orchestrator, not the UI layer.

Additionally:
- Review timeout is hardcoded at 5 minutes (should be ~1 hour, configurable)
- No config toggle for automatic vs manual review-reimpl cycling

## Design

### Core Principle

The orchestrator owns the full per-repo lifecycle: **implement → review → reimpl → re-review → ... → pass/escalate/fail**. The TUI observes state via events and only intervenes when human input is required (escalation, override-accept).

### Per-Repo Lifecycle (within a wave)

```
executeSubPlan completes implementation session
    │
    ├─ Failed → mark failed, return
    │
    └─ Completed → enter review loop:
         │
         ReviewSession()
         │
         ├─ Passed → done, sub-plan passed
         │
         ├─ Escalated (max cycles) → mark escalated, return
         │
         ├─ NeedsReimpl + AutoFeedbackLoop=false → mark needs-human, return
         │
         └─ NeedsReimpl + AutoFeedbackLoop=true:
              │
              Re-run implementation session (same sub-plan, same worktree)
              with critique feedback in prompt
              │
              └─ Loop back to ReviewSession()
```

### Wave Execution (unchanged ordering, independent per-repo)

Waves still gate when repos *start* (dependency ordering). But within a wave, each repo runs its full implement+review cycle independently in the errgroup. The wave completes when all repos in it have reached a terminal state (passed, escalated, or failed).

### Work Item State Machine

The work item stays in `implementing` for the entire lifecycle. It transitions to:
- `completed` — all repos passed review
- `reviewing` — at least one repo escalated (needs human decision)
- `failed` — at least one repo hard-failed

The `implementing → reviewing` transition now means "human attention needed" (escalation), not "review is running." This is a semantic shift: review runs automatically within `implementing`.

### New Config Fields

```go
type ReviewConfig struct {
    PassThreshold    PassThreshold `yaml:"pass_threshold"`     // default: minor_ok
    MaxCycles        *int          `yaml:"max_cycles"`         // default: 3
    Timeout          *string       `yaml:"timeout"`            // default: "1h"
    AutoFeedbackLoop *bool         `yaml:"auto_feedback_loop"` // default: true
}
```

### Review Timeout

Replace hardcoded `5*time.Minute` in `startReviewAgent` with parsed `ReviewConfig.Timeout`. Default 1 hour.

## Implementation Phases

### Phase 1 — Config Changes

**Files**: `internal/config/config.go`

- Add `Timeout *string` and `AutoFeedbackLoop *bool` to `ReviewConfig`
- Add defaults in `applyDefaults`: `Timeout = "1h"`, `AutoFeedbackLoop = true`
- Add `ReviewTimeout() time.Duration` helper method on `ReviewConfig`

### Phase 2 — Review Timeout + Config Wiring

**Files**: `internal/orchestrator/review.go`

- `ReviewPipeline` stores parsed review timeout from config
- `startReviewAgent`: replace `5*time.Minute` with `p.reviewTimeout`
- `NewReviewPipeline`: parse timeout from `cfg.Review.Timeout`

### Phase 3 — Per-Repo Review Loop in Orchestrator

**Files**: `internal/orchestrator/implementation.go`, `internal/orchestrator/review.go`

New method on `ImplementationService`:

```go
func (s *ImplementationService) reviewLoop(
    ctx context.Context,
    session domain.Task,
    subPlan domain.TaskPlan,
    critiqueFeedback string,
) (*ReviewResult, error)
```

This runs the implement-review cycle for a single repo:
1. Call `ReviewPipeline.ReviewSession(ctx, session)`
2. If passed or escalated → return
3. If `NeedsReimpl` and `AutoFeedbackLoop`:
   a. Build reimpl prompt with critique feedback
   b. Re-run implementation session (same worktree, same sub-plan, fresh Task row)
   c. Loop back to step 1
4. If `NeedsReimpl` and not `AutoFeedbackLoop` → return (needs human)

`ImplementationService` gains a `reviewPipeline *ReviewPipeline` field.

Modify `executeSubPlan`: after implementation completes successfully, call `reviewLoop` inline. The sub-plan only reaches "completed" status after review passes.

### Phase 4 — Wave Completion + Work Item Transitions

**Files**: `internal/orchestrator/implementation.go`

Modify `Implement()`:
- Remove `SubmitForReview()` call at the end
- After all waves: if all repos passed → `CompleteWorkItem`. If any escalated → `SubmitForReview` (human needed). If any failed → `FailWorkItem`.
- `ImplementResult` gains `ReviewResults map[string]*ReviewResult` keyed by sub-plan ID

### Phase 5 — TUI Simplification

**Files**: `internal/tui/views/app.go`, `internal/tui/views/cmds.go`, `internal/tui/views/msgs.go`

- Remove `RunReviewSessionCmd` (review now runs in orchestrator)
- `ImplementationCompleteMsg` handler: remove review dispatch loop. Implementation complete now means the full lifecycle (impl+review) is done.
- Keep `OverrideAcceptMsg`/`OverrideAcceptCmd` for human escalation handling
- Keep `ReimplementMsg` for manual re-trigger when `AutoFeedbackLoop=false`
- `ReviewCompleteMsg` becomes unused — remove or repurpose for event-driven updates

### Phase 6 — Tests

- Config: test `ReviewTimeout()` parsing and defaults
- Review: test timeout uses config value
- Implementation: test `reviewLoop` with mock ReviewPipeline (pass, reimpl, escalate, max-cycles)
- Integration: test wave completes with per-repo review
- TUI: verify `ImplementationCompleteMsg` no longer fires review commands

## Risks

1. **Review inside errgroup**: Long-running review+reimpl loops inside the wave errgroup mean a single repo can hold up the entire wave. This is acceptable — wave ordering is intentional, and repos within a wave are independent. A stuck repo times out after `ReviewConfig.Timeout`.

2. **Reimplementation within `implementing` state**: The work item stays in `implementing` while review runs. TUI progress display needs to reflect per-repo review status via events, not work-item state transitions. The existing `EventReviewStarted`/`EventReviewCompleted` events already emit per-session — the TUI can use these.

3. **`SubmitForReview` semantic shift**: Currently means "review is starting." After this change, means "review found issues needing human decision." Any code checking for `reviewing` state to determine "review in progress" needs updating.
