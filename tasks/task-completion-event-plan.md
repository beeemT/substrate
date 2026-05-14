# Task Completion Event Architecture

## Prerequisite

Execute these plans first, in order:

1. `tasks/agent-session-rename-plan.md` — complete the misleading `domain.Task` → `domain.AgentSession` terminology cutover before behavioral event work.
2. `tasks/event-handler-refactor-plan.md`:
   - service-owned lifecycle event emission for work items, agent sessions, plans, and sub-plans;
   - `EventPlanGenerated` emitted by `PlanService.CreatePlanAtomic`, not the planning orchestrator;
   - `EventAgentSessionResumed` emitted by `AgentSessionService.Transition()` or a service-owned resume/follow-up mutator, never by the orchestrator;
   - no TUI/orchestrator duplicate `EventWorkItemCompleted` emitters remain.

## Domain Model

```text
Session (work item)          — external ticket / aggregate work item
├── Plan                     — current approved implementation plan
│   └── TaskPlan (sub-plan)  — one repo's implementation unit
│       ├── Status: pending | in_progress | completed | failed
│       └── Content: repo-specific implementation plan
└── AgentSession             — one harness invocation
    ├── Phase: planning | implementation | review
    └── Status: pending | running | waiting_for_answer | completed | interrupted | failed
```

## Corrected Core Principle

- `EventAgentSessionCompleted` = one harness invocation finished. Not meaningful for PR/MR readiness.
- `EventSubPlanCompleted` = the repo's harness/review work is complete in the local worktree. It is **not** a PR/MR-ready signal because final commit/push happens later.
- `EventSubPlanPRReady` = the repo branch has been finalized and pushed; adapters may create or mark the PR/MR ready.
- `EventWorkItemCompleted` = the entire work item completed. It is only for work-item/TUI/tracker state concerns.

## Service Ownership

`PlanService` owns plan/sub-plan event emission. The orchestrator may compute context and call service methods, but it must not publish plan/sub-plan lifecycle events directly.

Use two service-owned signals:

1. `TransitionSubPlan(...)` emits state events (`subplan.started`, `subplan.completed`, `subplan.failed`).
2. `MarkSubPlanPRReady(...)` emits `subplan.pr_ready` after final commit/push succeeds for that sub-plan's repo.

`MarkSubPlanPRReady` is intentionally separate from `TransitionSubPlan`: PR readiness is a repo-lifecycle fact, not the same fact as implementation completion. It does not require a new persisted sub-plan status; readiness is materialized by the repo lifecycle adapters through stored review artifacts / tracker records. Therefore this method must be safe to retry: adapters must locate existing PR/MR artifacts by branch/review coordinates and update/create idempotently.

## Event Types

**`internal/domain/event.go`**:

```go
// Sub-plan state events.
EventSubPlanStarted   EventType = "subplan.started"
EventSubPlanCompleted EventType = "subplan.completed"
EventSubPlanFailed    EventType = "subplan.failed"

// Repo lifecycle event consumed by GitHub/glab lifecycle adapters.
EventSubPlanPRReady EventType = "subplan.pr_ready"
```

Remove `EventSubPlanStatusChanged` after all callers/subscribers are migrated.

## Payloads

### `subPlanEventPayload`

Used by `EventSubPlanStarted`, `EventSubPlanCompleted`, and `EventSubPlanFailed`.

```go
type subPlanEventPayload struct {
    WorkItemID string                `json:"work_item_id"`
    WorkspaceID string               `json:"workspace_id,omitempty"`
    PlanID     string                `json:"plan_id"`
    SubPlanID  string                `json:"sub_plan_id"`
    SubPlan    domain.TaskPlan       `json:"sub_plan"`
    Status     domain.TaskPlanStatus `json:"status"`
}
```

Rules:

- `PlanID` must never be empty.
- `SubPlan` is included so the TUI can upsert even if it has not loaded the plan yet.
- `SystemEvent.WorkspaceID` must be the real workspace ID, not the work item ID.

### `subPlanPRReadyPayload`

Used by `EventSubPlanPRReady`.

```go
type subPlanPRReadyPayload struct {
    WorkItemID     string                    `json:"work_item_id"`
    WorkspaceID    string                    `json:"workspace_id,omitempty"`
    PlanID         string                    `json:"plan_id"`
    SubPlanID      string                    `json:"sub_plan_id"`
    Repository     string                    `json:"repository"`
    Branch         string                    `json:"branch"`
    WorktreePath   string                    `json:"worktree_path,omitempty"`
    WorkItemTitle  string                    `json:"work_item_title,omitempty"`
    SubPlanContent string                    `json:"sub_plan_content,omitempty"`
    TrackerRefs    []domain.TrackerReference `json:"tracker_refs,omitempty"`
    Review         domain.ReviewRef          `json:"review"`
}
```

Rules:

- `Review` is required for routed repo lifecycle adapters and GitHub fork/base/head handling.
- `Branch` is required for both GitHub and glab PR/MR lookup.
- `Repository` should match the repo lifecycle adapter's existing conventions (`owner/repo` for GitHub; GitLab project path for glab/GitLab).

## Plan Service Changes

### `TransitionSubPlan`

```go
func (s *PlanService) TransitionSubPlan(ctx context.Context, id string, to domain.TaskPlanStatus) error
```

Responsibilities:

1. Load sub-plan.
2. Validate status transition.
3. Update sub-plan status and `UpdatedAt`.
4. Load plan and work item in the same transaction.
5. Emit the semantic event based on destination status:
   - `SubPlanInProgress` → `EventSubPlanStarted`
   - `SubPlanCompleted` → `EventSubPlanCompleted`
   - `SubPlanFailed` → `EventSubPlanFailed`
   - `SubPlanPending` → no event unless a future UI explicitly needs retry/reset events

Pseudocode:

```go
func (s *PlanService) TransitionSubPlan(ctx context.Context, id string, to domain.TaskPlanStatus) error {
    var sp domain.TaskPlan
    var workItem domain.Session
    err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
        current, err := res.SubPlans.Get(ctx, id)
        if err != nil { return newNotFoundError("sub-plan", id) }
        if !canTransitionSubPlan(current.Status, to) { ... }

        current.Status = to
        current.UpdatedAt = time.Now()
        if err := res.SubPlans.Update(ctx, current); err != nil { return err }

        plan, err := res.Plans.Get(ctx, current.PlanID)
        if err != nil { return fmt.Errorf("get plan for sub-plan event: %w", err) }
        wi, err := res.Sessions.Get(ctx, plan.WorkItemID)
        if err != nil { return fmt.Errorf("get work item for sub-plan event: %w", err) }

        current.PlanID = plan.ID
        sp = current
        workItem = wi
        return nil
    })
    if err != nil { return err }

    evtType := subPlanEventType(to)
    if evtType == "" { return nil }

    Emit(s.eventBus, domain.SystemEvent{
        ID:          domain.NewID(),
        EventType:   string(evtType),
        WorkspaceID: workItem.WorkspaceID,
        Payload: marshalJSONOrEmpty(string(evtType), subPlanEventPayload{
            WorkItemID:  workItem.ID,
            WorkspaceID: workItem.WorkspaceID,
            PlanID:      sp.PlanID,
            SubPlanID:   sp.ID,
            SubPlan:     sp,
            Status:      sp.Status,
        }),
        CreatedAt: time.Now(),
    })
    return nil
}
```

### `MarkSubPlanPRReady`

Add a service-owned event method for the dedicated PR/MR readiness signal:

```go
type SubPlanPRReadyContext struct {
    Repository     string
    Branch         string
    WorktreePath   string
    WorkItemTitle  string
    SubPlanContent string
    TrackerRefs    []domain.TrackerReference
    Review         domain.ReviewRef
}

func (s *PlanService) MarkSubPlanPRReady(ctx context.Context, subPlanID string, ready SubPlanPRReadyContext) error
```

Responsibilities:

1. Load sub-plan, plan, and work item in one transaction.
2. Validate that the sub-plan status is `SubPlanCompleted`. If not, return an invalid transition/input error.
3. Validate generic readiness context only: `Repository` and `Branch` must be non-empty, and `Review` must contain enough provider/repo signal for routed lifecycle adapters. Do not encode GitHub/glab-specific coordinate rules in `PlanService`; adapters validate platform-specific requirements.
4. Emit `EventSubPlanPRReady` with real `SystemEvent.WorkspaceID`.

This keeps event emission in a service-owned method while allowing the orchestrator to supply finalization-specific context that only it has after commit/push. The method is event-only: it does not mutate sub-plan status or persist a separate readiness state.

## Orchestrator Changes

### Sub-plan state changes

`executeSubPlan` continues to call `PlanService.TransitionSubPlan` when implementation/review status changes:

```go
s.persistSubPlanStatus(ctx, &subPlan, domain.SubPlanInProgress)
s.persistSubPlanStatus(ctx, &subPlan, domain.SubPlanCompleted)
s.persistSubPlanStatus(ctx, &subPlan, domain.SubPlanFailed)
```

`persistSubPlanStatus` does not need branch/review/tracker context. State events carry the full `TaskPlan` from the service.

### PR-ready emission after final push

`EventSubPlanPRReady` must be emitted only after the repo branch has been finalized and pushed.

Refactor finalization from all-repo batch to per-sub-plan success reporting:

1. `commitAndPushRepos` should return structured per-sub-plan success/failure information, not just one joined error. Key results by `SubPlanID`, not only repository name, because the emitted PR-ready event is sub-plan scoped.
2. Use an explicit result contract:

```go
type RepoFinalizationResult struct {
    SubPlanID    string
    Repository   string
    WorktreePath string
    Branch       string
    Review       domain.ReviewRef
    Err          error
}
```

3. Each success result must include the data needed to emit PR-ready without rediscovery ambiguity: `SubPlanID`, `Repository`, `WorktreePath`, `Branch`, `Review`, and nil `Err`.
4. For each sub-plan whose commit/push succeeds, call:

```go
err := s.planSvc.MarkSubPlanPRReady(ctx, subPlan.ID, service.SubPlanPRReadyContext{
    Repository:     result.Repository,
    Branch:         result.Branch,
    WorktreePath:   result.WorktreePath,
    WorkItemTitle:  workItem.Title,
    SubPlanContent: subPlan.Content,
    TrackerRefs:    trackerRefsFromMetadata(workItem.Metadata),
    Review:         result.Review,
})
```

5. If any sub-plan fails finalization, do not emit PR-ready for that sub-plan.
6. If `MarkSubPlanPRReady` returns an error, treat finalization as failed and do not complete the work item. PR-ready publication is part of the completion contract, not a best-effort side effect.
7. Work-item completion happens only after all required sub-plan finalizations succeed and all corresponding PR-ready events are published.

Do not move `ResolveReviewContextWithBranch` into `persistSubPlanStatus`; that was the wrong data-flow. It belongs in finalization/PR-ready context assembly, after the branch/worktree/repo are known and before `MarkSubPlanPRReady`.

## TUI Changes

### Decode sub-plan state events

Register:

```go
domain.EventSubPlanStarted:   decodeSubPlanEvent,
domain.EventSubPlanCompleted: decodeSubPlanEvent,
domain.EventSubPlanFailed:    decodeSubPlanEvent,
```

Message:

```go
type SubPlanStatusChangedMsg struct {
    WorkItemID string
    PlanID     string
    SubPlan    domain.TaskPlan
    Status     domain.TaskPlanStatus
}
```

Handler:

```go
case SubPlanStatusChangedMsg:
    if msg.SubPlan.ID != "" {
        planID := msg.PlanID
        if planID == "" {
            planID = msg.SubPlan.PlanID
        }
        if planID != "" {
            a.upsertSubPlan(planID, msg.SubPlan)
        } else if msg.WorkItemID != "" {
            cmds = append(cmds, LoadPlanForSessionCmd(a.provider.Plan(), msg.WorkItemID))
        }
    }
    a.rebuildSidebar()
    a.refreshSessionSearchEntriesFromLocalState()
    if a.currentWorkItemID == msg.WorkItemID {
        cmds = append(cmds, a.updateContentFromState())
    }
    return a, tea.Batch(cmds...)
```

Add an `upsertSubPlan(planID string, sp domain.TaskPlan)` helper instead of scanning all `a.subPlans` maps by ID.

### PR-ready events

The TUI does not need to handle `EventSubPlanPRReady` unless the UI wants to show a PR-ready toast. Repo lifecycle adapters are the primary consumers.

## Adapter Changes

### Service manager subscriptions

Repo lifecycle adapters should subscribe to:

- `EventWorktreeCreated`
- `EventWorktreeReused`
- `EventSubPlanPRReady`
- `EventPRMerged`
- `EventPlanApproved` for description sync where currently used

They should stop using `EventWorkItemCompleted` for PR/MR finalization.

Work-item tracker adapters should keep:

- `EventPlanApproved`
- `EventWorkItemCompleted`
- `EventPRMerged` where currently used

### Routed repo lifecycle adapter

`repoLifecycleEventPlatform` must recognize `EventSubPlanPRReady` from its `review` payload. The payload includes `ReviewRef`, so the existing review-based platform routing should work once subscriptions include the new event.

### GitHub lifecycle adapter

Replace `onWorkItemCompleted` PR finalization with `onSubPlanPRReady`:

1. Decode `subPlanPRReadyPayload`.
2. Call `resolveForkBase(ctx, &p.Review)`.
3. Derive:
   - `baseOwner`, `baseRepo` from `p.Review.BaseRepo`
   - `headOwner` from `p.Review.HeadRepo`
   - `baseBranch` from `p.Review.BaseBranch`, defaulting to `main` as existing code does
4. Find PR by branch using existing `findOpenPullByBranch(ctx, baseOwner, baseRepo, baseBranch, headOwner, p.Branch)`.
5. If no PR exists, create a non-draft PR with:
   - title = `WorkItemTitle` or branch
   - head = `headOwner + ":" + Branch`
   - base = `baseBranch`
   - body = sub-plan content + tracker footer
6. If PR exists, patch `draft=false`.
7. Persist the review artifact / GitHub PR record exactly like current completion handling.

Do not call `findOpenPullByBranch` with empty head owner or empty base branch.

### glab lifecycle adapter

Replace `EventWorkItemCompleted` MR finalization with `EventSubPlanPRReady`.

Use payload `Review`, `Repository`, and `Branch` to locate the project/MR. Keep existing fallback logic that uses persisted MR artifacts where appropriate.

### Tracker adapters

Tracker state transitions remain work-item-level:

- Plan approval → in progress
- Work item completion → done / in review depending adapter config

Update GitHub, GitLab, and Linear tracker handlers to iterate `external_ids` first, with `external_id` fallback.

## Ordering

Safe implementation order:

1. Add `EventSubPlanStarted`, `EventSubPlanCompleted`, `EventSubPlanFailed`, and `EventSubPlanPRReady`.
2. `PlanService.TransitionSubPlan` semantic state events with full `TaskPlan` payload and correct `WorkspaceID`.
3. TUI sub-plan decoders/handlers.
4. `PlanService.MarkSubPlanPRReady` and payload.
5. Orchestrator finalization returns per-sub-plan push results and calls `MarkSubPlanPRReady` after successful push.
6. GitHub/glab lifecycle adapters consume `EventSubPlanPRReady` idempotently, using stored artifacts / tracker records when present.
7. Remove lifecycle adapter handling of `EventWorkItemCompleted`.
8. Remove old `EventSubPlanStatusChanged` and stale tests.

## Files to Modify

| File | Changes |
|---|---|
| `internal/domain/event.go` | Add `EventSubPlanStarted`, `EventSubPlanCompleted`, `EventSubPlanFailed`, `EventSubPlanPRReady`; remove old generic event after migration |
| `internal/service/plan.go` | Semantic `TransitionSubPlan`; `MarkSubPlanPRReady`; correct `WorkspaceID`; full payload structs |
| `internal/orchestrator/implementation.go` | Return per-sub-plan finalization results; call service transition/event methods; emit PR-ready only after successful push; remove duplicate completion helper |
| `internal/tui/views/event_consumer.go` | Add sub-plan event decoders |
| `internal/tui/views/msgs.go` | Add `SubPlanStatusChangedMsg` with full `TaskPlan` |
| `internal/tui/views/app.go` | Add `upsertSubPlan`; handle sub-plan state messages robustly |
| `internal/tui/views/service_manager.go` | Subscribe lifecycle adapters to `EventSubPlanPRReady` and stop sending them `EventWorkItemCompleted` |
| `internal/tui/views/cmds.go` | Remove completion helper; `OverrideAcceptCmd` calls service completion only |
| `internal/app/wire.go` | Route PR-ready lifecycle events via `review` payload |
| `internal/adapter/github/adapter.go` | Replace work-item completion PR finalization with PR-ready handler |
| `internal/adapter/glab/adapter.go` | Replace work-item completion MR finalization with PR-ready handler |
| `internal/adapter/gitlab/adapter.go` | Keep tracker behavior; update multi-ID state handling if needed |
| `internal/adapter/linear/adapter.go` | Update multi-ID state handling |
| `internal/app/adapter_bus_test.go` | Update adapter subscription/routing expectations |

## Verification

1. `go test ./internal/domain/`
2. `go test ./internal/service/`
3. `go test ./internal/orchestrator/`
4. `go test ./internal/tui/views/`
5. `go test ./internal/adapter/github/`
6. `go test ./internal/adapter/glab/`
7. `go test ./internal/adapter/gitlab/`
8. `go test ./internal/adapter/linear/`
9. `go test ./internal/app/`
10. `go build ./...`
11. Manual: implement a two-repo plan and verify each PR/MR becomes ready only after that repo's branch has been pushed, not when the harness session merely completes.
