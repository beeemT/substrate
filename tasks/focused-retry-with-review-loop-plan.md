# Implementation Plan: Focused Retry With Full Review Loop

## Context

Today, retrying a failed agent session from the TUI (`r` key on a focused
failed session) only re-runs the implementation harness. It does **not** enter
the review loop, does **not** transition the sub-plan back to in_progress, and
does **not** transition the work item state. The new session ends up
`completed` while the sub-plan stays `failed` and the work item stays
`failed` — the TUI shows a green task on top of a frozen lifecycle.

Bulk retry (`RetryFailedCmd`) works correctly because it goes through
`ImplementationService.Implement(planID)`, which owns the full pipeline. The
focused retry path bypasses all of that.

The user wants focused retry of a single failed agent session to:

1. Run a new implementation session resuming the failed conversation.
2. Run the full review loop for that sub-plan.
3. Transition the sub-plan and work item according to the outcome.

While doing this, we noticed several model-level workarounds that should be
fixed instead of accommodated. The fixes simplify the retry path and remove
existing latent bugs.

## Research Evidence

### Current focused-retry call chain

- `internal/tui/views/app.go:3006` — `r` keypress on a focused failed session
  → `RetrySessionCmd`.
- `internal/tui/views/cmds.go:1326-1340` — `RetrySessionCmd` →
  `Resumption.FollowUpFailedSession(task, "", instanceID)` →
  `Resumption.WaitAndComplete`. Returns `FollowUpSessionCompleteMsg`.
- `internal/orchestrator/resume.go:301-356` — `Resumption.FollowUpFailedSession`
  builds a system prompt from the failed session's last log lines + feedback,
  calls `AgentSessionService.FollowUpFailed`, starts the harness with
  `ResumeFromSessionID = failedSession.ID`. Touches no sub-plan or work-item
  state.
- `internal/service/session.go:547` — `AgentSessionService.FollowUpFailed`
  inserts a new pending `AgentSession`, transitions it to `running`, emits
  `EventAgentSessionResumed`. Hardcodes `Kind: AgentSessionKindImplementation`
  (line 553).
- `internal/orchestrator/resume.go:483-518` — `Resumption.WaitAndComplete`
  forwards events, waits for the harness, then transitions the new session to
  `completed` / `failed` / `interrupted`.

### Where the review loop lives

- `internal/orchestrator/implementation.go:532-550` — `reviewLoop` is invoked
  from `executeSubPlan`, which is only called from `Implement(planID)` via
  `executeWave`. The post-loop sub-plan/work-item state writes
  (`persistSubPlanStatus`, `state.CompleteSubPlan/FailSubPlan`, the post-wave
  `CompleteWorkItem`/`SubmitForReview`/`FailWorkItem` switch at lines 374-403)
  are private to `ImplementationService` and reachable only through
  `Implement`.

### Bulk retry path for comparison

- `internal/tui/views/cmds.go:1006` — `RetryFailedCmd` calls
  `workItemSvc.RetryFailedWorkItem(ctx, workItemID)` (Failed → Implementing)
  then `implSvc.Implement(ctx, planID)`.
- `internal/orchestrator/implementation.go:281-285` — `Implement` is
  idempotent on work-item state (only calls `StartImplementation` if not
  already `Implementing`).
- `internal/orchestrator/implementation.go:318-322` — Implement resets
  `SubPlanFailed`/`SubPlanInProgress` → `SubPlanPending` so `BuildWaves`
  picks them up.
- `internal/orchestrator/waves.go:26` — `BuildWaves` filters
  `status != SubPlanCompleted`.

### Model bugs found during research

#### Sub-plan state transitions (`internal/service/plan.go:140-146`)

```go
SubPlanFailed: {SubPlanPending}, // Allow retry
```

Forces a `Failed → Pending → InProgress` two-hop. Retry semantically is one
event ("we're trying again"), not two. The intermediate `Pending` state has
no behavioral meaning during a retry — it only exists because the transition
table is too restrictive.

#### Sub-plan status enum (`internal/domain/plan.go:53-59`)

Only four values: `pending`, `in_progress`, `completed`, `failed`.
Escalations from `reviewLoop` are persisted as `SubPlanFailed`
(implementation.go:516-518). The work-item evaluation then has to consult the
in-memory `SubPlanOutcome` map to distinguish escalated from failed. That
map only exists for the duration of one `Implement` call, so any out-of-band
retry path loses the distinction.

#### Hardcoded `Kind` in `FollowUpFailed` (`internal/service/session.go:553`)

`Kind: AgentSessionKindImplementation` is set regardless of the failed
session's kind. Same hardcoding at line 52 for `Resume`. `retryableFocusedSessionID`
allows retry of any non-foreman failed session, so retrying a failed `review`
session today rewrites it as an implementation session.

`Resumption.forwardEvents` (resume.go:467) and `questionFromEvent`
(question_router.go:72) hardcode the same constant when routing.
`QuestionRouter.Route` (question_router.go:38) already accepts both
`Implementation` and `Review`, so kind-aware routing is a one-line change.

#### Foreman lifecycle tied to message events (`internal/tui/views/app.go:2097`)

`EndForeman` fires on `FollowUpSessionCompleteMsg` regardless of work-item
state. This contradicts `docs/10-foreman-lifecycle.md` §3 ("The Foreman
session is stopped when implementation completes and restarted when follow-up
feedback is provided.") and is a latent bug for the feedback follow-up path:
`ReviewFollowup.FollowUpFailed` (review_followup.go:88) already does Stop +
Start with feedback context, then the TUI redundantly stops the new foreman
when `FollowUpSessionCompleteMsg` arrives.

The other `EndForeman` triggers are correct:

1. `SessionStateChangedMsg` with `state == SessionCompleted` (app.go:1720).
2. `DeleteSessionMsg` (app.go:2249).

### Reviewing → Implementing re-entry path

- `RequestReimplementation` (`internal/service/work_item.go:506-531`) — has
  zero production callers; only invoked from tests.
- The real production path is `ReimplementMsg` (`app.go:2316-2326`), bound to
  the `r` shortcut in the reviewing overlay. It calls
  `RunImplementationCmd` + `RestartForemanWithPlanOrchestratedCmd`.
- `Implement` itself does the `Reviewing → Implementing` work-item transition
  via `StartImplementation` (allowed by the transition table at
  work_item.go:36).

This means tightening foreman scope to "alive only during Implementing" is
safe: every production path that re-enters Implementing already calls
`RestartForemanWithPlan` or equivalent.

## Product Behavior

### Target flow

```text
focused retry pressed on a failed agent session
  -> Resumption.FollowUpFailedSession runs the new session
        (resumes prior conversation, kind preserved from failed session)
  -> Resumption.WaitAndComplete completes the new agent session row
  -> ImplementationService.ContinueAfterImplSession picks up:
        ensure work item is Implementing
        ensure sub-plan is InProgress (now reachable directly from Failed)
        run reviewLoop with the new completed session
        persist sub-plan outcome (Completed | Failed | Escalated)
        re-derive work-item state by listing all sub-plans:
          any Escalated  -> SubmitForReview
          any Failed     -> FailWorkItem
          all Completed  -> finalizeCompletedWorkItem (small terminal action)
```

### Non-goals

- Do not change the user-visible retry UX (still `r` key, still scoped to one
  failed session).
- Do not change the behavior of bulk retry (`RetryFailedCmd`).
- Do not rewrite `Implement`'s wave orchestration. The change reuses
  `reviewLoop` but adds a new entry point for the single-session case.

## Design Decisions

### 1. Fix the model first; retry path follows naturally

Five model adjustments that simplify the retry path and remove latent bugs:

#### M1. Direct `SubPlanFailed → SubPlanInProgress` transition

```go
// internal/service/plan.go:140-146
var validSubPlanTransitions = map[domain.TaskPlanStatus][]domain.TaskPlanStatus{
    SubPlanPending:    {SubPlanInProgress},
    SubPlanInProgress: {SubPlanCompleted, SubPlanFailed, SubPlanEscalated, SubPlanPending},
    SubPlanCompleted:  {},
    SubPlanFailed:     {SubPlanInProgress, SubPlanPending}, // direct retry; Pending kept for wave reset
    SubPlanEscalated:  {SubPlanInProgress, SubPlanPending}, // human-resumed retry
}
```

Removes the artificial Pending-bounce in retry paths. Keeps Pending available
for `Implement`'s wave reset (which still uses it as the canonical "needs to
be picked up" state).

#### M2. Add `SubPlanEscalated` status

`internal/domain/plan.go:53-59`:

```go
SubPlanEscalated TaskPlanStatus = "escalated"
```

Migration: new file recreating `sub_plans` with the expanded `CHECK`
constraint, following the pattern of migrations 016 / 017 / 019 for
`agent_sessions`.

`internal/orchestrator/waves.go:26` — also exclude `Escalated` from wave
scheduling. Escalation is a human-paused state.

`internal/orchestrator/implementation.go:516-518` — change
`persistSubPlanStatus(..., SubPlanFailed)` for escalated outcomes to
`SubPlanEscalated`. The post-wave switch can then derive `hasEscalated` /
`hasFailed` purely from a DB read of sub-plan statuses, removing the
dependency on the in-memory `result.ReviewResults` map. Same logic works for
the new focused-retry method.

`internal/tui/views/overview.go:1413-1424` — add `humanTaskPlanStatus` case
for `SubPlanEscalated` (e.g. "Needs review"). Default fallback prevents a
crash if missed.

`internal/domain/event.go` — add `EventSubPlanEscalated`. Reuse
`decodeSubPlanEvent` in the TUI consumer (it already carries the full
`TaskPlan` + `Status`).

#### M3. Preserve `Kind` in `FollowUpFailed`

`internal/service/session.go:553` (and `:52` for `Resume`):

```go
Kind: failed.Kind, // was: AgentSessionKindImplementation
```

`internal/orchestrator/resume.go:467` — replace hardcoded
`AgentSessionKindImplementation` with the session's actual kind (read once at
the top of `forwardEvents` from the registry-resolved session, or pass via
struct field).

`internal/orchestrator/question_router.go:72` — same; take stage from the
session.

`QuestionRouter.Route` already accepts both `Implementation` and `Review`,
so no routing change needed.

Note: also fixes the same bug in the `Resume` (interrupted-session) path,
which is out of scope for the user's request but is the same model bug. Bundle
to avoid leaving it half-fixed.

#### M4. Foreman lifecycle scoped to work-item state

`internal/tui/views/app.go`:

- **Remove** the `EndForeman` call from `FollowUpSessionCompleteMsg`
  (line 2097). The feedback path's foreman is owned by
  `ReviewFollowup.FollowUp` (Stop + Start). The retry path keeps the foreman
  alive for the review loop's potential auto-reimpl.
- **Add** `EndForeman` calls in the `SessionStateChangedMsg` handler for
  `state == SessionFailed` and `state == SessionArchived`. Optional: also
  `SessionReviewing` (review sessions don't talk to foreman; safe to end on
  entry to Reviewing — re-entry via `ReimplementMsg` already restarts via
  `RestartForemanWithPlanOrchestratedCmd`).

Verified safe: every production path into Implementing
(`PlanApprovedMsg`, `ReimplementMsg`, `RetryFailedCmd`, the new focused-retry)
already pairs with `BeginForeman` or `RestartForemanWithPlan`.

#### M5. Drop `executeSubPlan`'s crash-recovery review-retry branch

`internal/orchestrator/implementation.go:478-503` — the special case for "if
last session was a review session, retry review only" is a workaround for
state ambiguity. With M2 in place and the new `ContinueAfterImplSession`
entry point, crash recovery can be expressed as a single rule: if a sub-plan
is `InProgress` and the latest session for it is a completed implementation
session with no successor review session, the next step is review.

This unification can be done after M2 lands; not strictly required for the
focused-retry feature.

#### M6. Filter cycle counting to terminal statuses

`internal/orchestrator/review.go:70-93` — `ReviewSession` currently counts
every `ReviewCycle` row when computing `cycleNumber = len(cycles) + 1` and
gating against `max_cycles`. Stale cycles in `ReviewCycleReviewing` or
`ReviewCycleReimplementing` (left behind by harness crashes that never
reached `makeDecision`) count toward the max, which means a few crashed
review attempts can immediately escalate the next legitimate retry.

Change cycle counting to consider only cycles in terminal-decision states:

```go
// Terminal review cycle statuses are those that reached a decision.
func isTerminalReviewCycle(s domain.ReviewCycleStatus) bool {
    switch s {
    case domain.ReviewCyclePassed,
         domain.ReviewCycleCritiquesFound,
         domain.ReviewCycleFailed:
        return true
    }
    return false
}

terminal := 0
for _, c := range cycles {
    if isTerminalReviewCycle(c.Status) {
        terminal++
    }
}
cycleNumber := terminal + 1
```

Stale `Reviewing`/`Reimplementing` cycles remain in the DB as audit trail
but do not consume the budget.

### 2. New entry point: `ImplementationService.ContinueAfterImplSession`

```go
// ContinueAfterImplSession resumes the per-sub-plan pipeline starting from a
// completed implementation session. Used by focused retry and (eventually)
// crash recovery. Caller has already produced a completed AgentSession row
// (kind=implementation) with a SubPlanID.
func (s *ImplementationService) ContinueAfterImplSession(
    ctx context.Context,
    completedSessionID string,
) error
```

Steps:

1. Load session, sub-plan, plan, work item, workspace.
2. Validate: session.Status == Completed, session.Kind == Implementation,
   session.SubPlanID != "".
3. If work item is `Failed`, `RetryFailedWorkItem` (Failed → Implementing).
   Otherwise leave as-is.
4. If sub-plan is `Failed` or `Escalated`, transition directly to
   `InProgress` (now allowed by M1).
5. Build a single-entry `worktreePaths` from `session.WorktreePath`
   (reviewLoop only needs the entry for this sub-plan's repo).
6. Run `s.reviewLoop(ctx, session, subPlan, workspace, plan, workItem, worktreePaths)`.
7. Persist sub-plan outcome:
   - Passed → `SubPlanCompleted`
   - Failed → `SubPlanFailed`
   - Escalated → `SubPlanEscalated` (M2)
8. Re-derive work-item state by listing all sub-plans of the plan:
   - any `SubPlanFailed` → `FailWorkItem`
   - else any `SubPlanEscalated` → `SubmitForReview`
   - else all `SubPlanCompleted` → `finalizeCompletedWorkItem` (small action:
     commit residuals, push, emit `SubPlanPRReady`, transition work item to
     `Completed`)
   - else (some still `Pending`/`InProgress`) → leave as `Implementing`

`finalize` stays a small terminal action; never wraps the review loop.

`reviewLoop` is invoked with a fresh `outcome.Cycles = 0`, so the focused
retry receives a new `max_cycles` budget. The persisted-cycle counter inside
`ReviewSession` (M6) ensures only legitimate prior decisions consume the
overall budget; stale crashed cycles do not.

### 3. Retry dispatch by session kind

The retry has different semantics depending on the failed session's kind.
Both routes converge on `ContinueAfterImplSession`.

#### Implementation (and reimplementation) sessions

Resume the failed conversation. The agent had work in progress, accumulated
context, and partial worktree state. `FollowUpFailedSession` resumes via
`ResumeFromSessionID` so the harness picks up the prior turns.

```go
result, err := resumption.FollowUpFailedSession(ctx, task, "", instanceID)
resumption.WaitAndComplete(ctx, result.Session.ID, result.HarnessSession)
implSvc.ContinueAfterImplSession(ctx, result.Session.ID)
```

Reimpl sessions (`Kind == Implementation` with `ParentAgentSessionID` set to
a prior impl/review) flow through the same branch — from the retry's point
of view, they are just implementation sessions.

#### Review sessions

Review sessions are stateless evaluators of an impl session's output.
Resuming the conversation of a failed review agent makes no semantic sense.
Instead, discard the failed review (already preserved in the DB as audit
trail) and run a fresh review of the parent impl session.

Migration 020 (`agent_session_parent`) guarantees every review session has
`ParentAgentSessionID` pointing at the impl session it was reviewing.

```go
parentImplID := task.ParentAgentSessionID
implSvc.ContinueAfterImplSession(ctx, parentImplID)
```

`reviewLoop` will create cycle N+1 (counting only terminal cycles per M6)
and evaluate the impl output fresh.

#### Other kinds

`Foreman` is already excluded by `retryableFocusedSessionID`. `Manual`
and `Planning` sessions are not part of the implement→review pipeline; the
retry path should refuse them with a clear error rather than silently
rewriting them as implementation.

### 4. `RetrySessionCmd` rewiring

`internal/tui/views/cmds.go:1326-1340` — take additional
`*orchestrator.ImplementationService` parameter and dispatch on `task.Kind`:

```go
func RetrySessionCmd(
    ctx context.Context,
    resumption *orchestrator.Resumption,
    taskSvc *service.AgentSessionService,
    implSvc *orchestrator.ImplementationService,
    sessionID, instanceID string,
) tea.Cmd {
    return func() tea.Msg {
        task, err := taskSvc.Get(ctx, sessionID)
        if err != nil { ... }

        var continueFromID string
        switch task.Kind {
        case domain.AgentSessionKindImplementation:
            result, err := resumption.FollowUpFailedSession(ctx, task, "", instanceID)
            if err != nil { ... }
            resumption.WaitAndComplete(ctx, result.Session.ID, result.HarnessSession)
            continueFromID = result.Session.ID
        case domain.AgentSessionKindReview:
            if task.ParentAgentSessionID == "" {
                return ErrMsg{Err: fmt.Errorf("review session %s has no parent impl session", task.ID)}
            }
            continueFromID = task.ParentAgentSessionID
        default:
            return ErrMsg{Err: fmt.Errorf("retry not supported for kind %s", task.Kind)}
        }

        if err := implSvc.ContinueAfterImplSession(ctx, continueFromID); err != nil {
            slog.Warn("continue after retry failed", "error", err,
                "agent_session_id", continueFromID)
        }
        return FollowUpSessionCompleteMsg{WorkItemID: task.WorkItemID}
    }
}
```

`app.go:3006` — call site receives `a.provider.Implementation()`.

Errors from `ContinueAfterImplSession` are logged, not surfaced as toast
errors; the new agent session is already in a terminal DB state and the TUI
refreshes from events.

## Implementation Steps

### Phase A — Model adjustments (independent of feature)

1. Add `SubPlanEscalated` to the domain enum and migration.
2. Update `validSubPlanTransitions` with M1 + M2 edges.
3. Update `BuildWaves` filter to exclude `SubPlanEscalated`.
4. Change `reviewLoop` escalation persistence to `SubPlanEscalated`.
5. Update `Implement`'s post-wave switch to read sub-plan statuses from DB
   (drop dependency on `result.ReviewResults` for the failed/escalated split).
6. Add TUI rendering for `SubPlanEscalated`.
7. Add `EventSubPlanEscalated` event type and consumer entry.
8. Pass `Kind` through `FollowUpFailed` and `Resume`. Update routing in
   `forwardEvents` and `questionFromEvent`.
9. Foreman lifecycle: remove `EndForeman` from
   `FollowUpSessionCompleteMsg`; add it to `SessionStateChangedMsg` for
   `Failed` / `Archived` (and optionally `Reviewing`).
10. Filter cycle counting in `ReviewSession` to terminal-status cycles only
    (M6).

Tests:

- New transition tests for `SubPlanFailed → InProgress` and `Escalated`
  edges.
- `Implement` post-wave evaluation reads from DB; existing escalation tests
  need to seed `SubPlanEscalated` rather than rely on `ReviewResults`.
- Foreman lifecycle test: failing a work item ends the foreman; completing
  ends the foreman; `FollowUpSessionCompleteMsg` no longer ends the foreman.
- Cycle counting: stale `Reviewing`/`Reimplementing` cycles do not consume
  budget; only `Passed`/`CritiquesFound`/`Failed` count.

### Phase B — Focused retry feature

11. Add `ContinueAfterImplSession` to `ImplementationService`.
12. Rewire `RetrySessionCmd` to dispatch on `task.Kind` and call
    `ContinueAfterImplSession` with the appropriate session ID.
13. Update `app.go:3006` call site to pass the implementation service.

Tests:

- Focused retry of a failed impl sub-plan whose review then passes →
  sub-plan completed, work item finalized.
- Focused retry whose review fails → sub-plan failed, work item failed.
- Focused retry whose review escalates → sub-plan escalated, work item
  submitted for review.
- Focused retry of a failed **review** session → `ContinueAfterImplSession`
  receives the parent impl session ID; new review cycle is created and
  evaluates the impl output; failed review session row remains as audit.
- Focused retry on a work item with multiple failed sub-plans only
  finalizes/transitions according to the aggregate state of all sub-plans.
- Re-retry: retry a session that was itself produced by a prior retry
  (impl and review variants).
- Refused-kind retry: invoking on a `Manual` or `Planning` kind session
  returns a clear error and does not create a new session.

### Phase C — Cleanup (optional, after Phase A+B land)

14. Remove `RequestReimplementation` from `SessionService` (dead code; the
    real path uses `StartImplementation` from inside `Implement`). Or keep
    with a comment marking it as a thin semantic wrapper.
15. Drop `executeSubPlan`'s crash-recovery review-retry branch (M5);
    crash recovery routes through `ContinueAfterImplSession`.

## Open Questions

1. **Reviewing → end foreman?** Phase A item 9 lists `SessionReviewing` as
   optional. Argument for: review sessions don't use the foreman. Argument
   against: if escalation is brief and the user immediately reimplements,
   keeping the foreman alive avoids restart cost. Current default
   recommendation: end on Reviewing for consistency, since
   `RestartForemanWithPlan` handles re-entry cleanly.

2. **`ContinueAfterImplSession` errors:** when the review loop itself fails
   (review harness error), should the work item be failed or left as
   implementing? Today `reviewLoop` returns `outcome.Failed = true` on review
   harness errors, which would route through the existing failed branch. OK
   to keep that semantic.

3. **Concurrency:** if the user clicks retry while another retry on the same
   work item is still in flight, what wins? Today the registry tracks
   per-session, not per-work-item. Likely fine since each retry is scoped to
   one session ID, but worth a defensive check at the top of
   `ContinueAfterImplSession` (refuse if work item is already in a non-failed
   non-implementing state from a parallel retry).

4. **Migration ordering:** the `SubPlanEscalated` migration should land
   before any code that writes the new value; sequence Phase A items 1-4
   together in a single PR.

## Referenced Documents

- `docs/05-orchestration.md` — per-repo review loop semantics.
- `docs/10-foreman-lifecycle.md` — orchestrator-owned foreman lifecycle.
- `docs/01-domain-model.md` — work-item and sub-plan state machines.
