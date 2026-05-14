# Event Handler Refactor Plan

## Goal

Remove duplicate TUI event handling while preserving the domain event contract:

1. Domain/service events are emitted by the owning service transition method (or existing timestamp-preserving service mutator where the aggregate already has one), not by TUI commands or orchestrator helper functions.
2. TUI `DomainEventMsg` dispatch has one authoritative path per event.
3. Events that carry their full entity are handled by typed messages and local upserts.
4. Events from external systems that cannot carry the mutated entity may still trigger targeted DB reloads.

## Corrected Architecture Decisions

### 1. Service-owned emission is the default

Centralize lifecycle event emission in the owning service. Execute `tasks/agent-session-rename-plan.md` first so the names below are already canonical before this behavioral refactor starts.

| Aggregate | Owning service | Emission location |
|---|---|---|
| Work item (`domain.Session`) | `SessionService` | `Transition`, `Archive`, `Unarchive` |
| Agent session (`domain.AgentSession`) | `AgentSessionService` | `Transition`, `Start`, `Complete`, `Interrupt`, `FollowUpRestart`, resume/follow-up service methods |
| Plan and sub-plan | `PlanService` | `CreatePlanAtomic`, `TransitionPlan`, `ApprovePlan`, `RejectPlan`, `ApplyReviewedPlanOutput`, `TransitionSubPlan` |
| Review cycle | `ReviewService` or review orchestrator until service method owns creation | Creation/status transition path |

Callers may pass context options into service methods when the event needs data outside the aggregate, but the service chooses the event type and publishes the event. Do not reintroduce TUI/orchestrator duplicate event helpers.

`EventPlanGenerated` must move out of `PlanningService.emitPlanGeneratedEvent` and into `PlanService.CreatePlanAtomic`, because `CreatePlanAtomic` owns the atomic creation of the plan and sub-plans. The service-owned event must include the created `Plan`, created `SubPlans`, real workspace ID, and top-level `work_item_id`.

### 2. `SystemEvent.WorkspaceID` must be a real workspace ID

Several current plan-service emissions use `plan.WorkItemID` as `SystemEvent.WorkspaceID`. That is wrong: the TUI drops events whose `WorkspaceID` does not match `runtimeCtx.WorkspaceID`.

When `PlanService` or `TransitionSubPlan` emits an event, it must load the work item through the transaction (`res.Sessions.Get(ctx, plan.WorkItemID)`) and set:

```go
WorkspaceID: workItem.WorkspaceID
```

The JSON payload must still include top-level `work_item_id`; TUI extractors read the payload, not `SystemEvent.WorkspaceID`.

### 3. Preserve adapter fields when consolidating events

`EventPlanApproved` currently has two emissions:

1. `PlanService.ApprovePlan()` emits a minimal service event.
2. `ApprovePlanCmd.emitPlanApproved()` emits an enriched adapter event with `external_id`, `external_ids`, `comment_body`, and `repo_comment_scopes`.

The refactor must make this a single service-owned emission without losing adapter fields. Use service options, for example:

```go
type PlanApprovalEventContext struct {
    ExternalID        string
    ExternalIDs       []string
    CommentBody       string
    RepoCommentScopes map[string]string
}

type PlanOption func(*planEventExtra)

func WithPlanApprovalEventContext(ctx PlanApprovalEventContext) PlanOption { ... }

func (s *PlanService) ApprovePlan(ctx context.Context, id string, opts ...PlanOption) error
```

`ApprovePlanCmd` may build adapter-specific context from config and work-item metadata, but it must pass that context to `PlanService.ApprovePlan`; it must not publish its own `EventPlanApproved`.

### 4. Do not add a persisted `domain.Session.Branch` just for events

The previous plan proposed a `Branch` field on `domain.Session`. That creates unnecessary persistence/migration work and still does not solve PR/MR finalization cleanly.

Final split after `task-completion-event-plan.md` completes:

- `EventWorkItemCompleted` remains service-owned and work-item-level. It carries work-item/tracker fields only:
  - `work_item_id`
  - `workspace_id`
  - `external_id`
  - `source_item_ids`
  - `external_ids` for prefixed tracker IDs
  - `session` full work item
- PR/MR readiness is **not** a work-item completion concern. It is handled by the dedicated `EventSubPlanPRReady` in `task-completion-event-plan.md`.

Because `task-completion-event-plan.md` is implemented directly after this plan, do not preserve a compatibility completion path. Remove TUI/orchestrator `emitWorkItemCompleted` helpers during the direct cutover to PR-ready events; no persisted `branch` or `review` field is required on `domain.Session`.

### 5. Agent session events carry the full entity

`EventAgentSessionStarted`, `Completed`, `Failed`, `Interrupted`, and `Resumed` use the full agent-session payload. Typed TUI handlers can upsert directly.

`EventAgentSessionResumed` must be emitted by `AgentSessionService` from `Transition()` or a service-owned resume/follow-up mutator, never by the orchestrator. If resume handling creates a replacement agent session and needs `old_session_id` / `new_session_id`, the orchestrator passes that context into the service method; the service still chooses `EventAgentSessionResumed` and publishes a payload containing the full new agent session plus top-level IDs.

### 6. Remove generic `EventAgentSessionStatusChanged`

`AgentSessionService.Transition()` currently emits `EventAgentSessionStatusChanged` for all transitions. Replace it with semantic events:

```go
func agentSessionEventType(from, to domain.AgentSessionStatus) domain.EventType {
    switch to {
    case domain.AgentSessionRunning:
        if from == domain.AgentSessionInterrupted || from == domain.AgentSessionWaitingForAnswer {
            return domain.EventAgentSessionResumed
        }
    }
    return ""
}
```

Keep specialized mutators where they preserve timestamps:

- `Start()` emits `EventAgentSessionStarted`.
- `Complete()` emits `EventAgentSessionCompleted`.
- `Interrupt()` emits `EventAgentSessionInterrupted`.
- `FollowUpRestart()` emits new `EventAgentSessionFollowUp`.
- Remove unused `Resume()` if it has no production callers.

### 7. Plan events carry full plan and sub-plans

`EventPlanGenerated`, `EventPlanSubmitted`, `EventPlanApproved`, `EventPlanRejected`, and `EventPlanRevised` must include:

```go
type planEventPayload struct {
    WorkItemID string            `json:"work_item_id"`
    PlanID     string            `json:"plan_id,omitempty"`
    Plan       *domain.Plan      `json:"plan,omitempty"`
    SubPlans   []domain.TaskPlan `json:"sub_plans,omitempty"`

    // Adapter fields for plan approval. Empty for other plan events.
    ExternalID        string            `json:"external_id,omitempty"`
    ExternalIDs       []string          `json:"external_ids,omitempty"`
    CommentBody       string            `json:"comment_body,omitempty"`
    RepoCommentScopes map[string]string `json:"repo_comment_scopes,omitempty"`
}
```

`decodePlanGenerated` and `decodePlanUpdated` both need `SubPlans`. `PlanUpdatedMsg` must carry `SubPlans` and update `a.subPlans[msg.Plan.ID]` when `msg.Plan != nil`.

### 8. Rename `EventPlanSubmittedForReview` to `EventPlanSubmitted`

There is no AI review at this stage; the user approves/rejects. Rename:

```go
EventPlanSubmitted EventType = "plan.submitted"
```

Update all call sites, registry entries, tests, and subscriptions.

### 9. Remove `EventImplementationStarted`

`EventWorkItemImplementing` is the state transition signal. Remove `EventImplementationStarted`, its emission, TUI decoder/message/handler, and update E2E tests to assert `EventWorkItemImplementing`.

### 10. `EventReviewStarted` should carry the review cycle

Include `domain.ReviewCycle` in the payload so TUI can upsert the new cycle without an immediate reload.

### 11. TUI PR/MR events must use one path, not two

`EventPRReviewStateChanged` and `EventPRMerged` already have typed decoders and typed handlers that call `LoadSessionCmd` / `LoadAgentSessionsForSessionCmd`. Do **not** also handle them in the raw `DomainEventMsg` switch.

After this refactor the raw switch should be removed entirely unless an event deliberately has no typed handler. For PR/MR events, keep the typed handlers.

## Implementation Phases

### Phase 0 — Agent-session naming cutover

Execute `tasks/agent-session-rename-plan.md` before this refactor. The event-handler refactor assumes `domain.AgentSession`, `domain.AgentSessionStatus`, `domain.AgentSessionPhase`, and `AgentSessionService` are already the canonical names. Do not mix behavior changes into that rename.

### Phase 1 — Event constants

1. Add `EventAgentSessionFollowUp`.
2. Rename `EventPlanSubmittedForReview` to `EventPlanSubmitted`.
3. Remove `EventImplementationStarted`.

### Phase 2 — Service event payloads

1. Update `marshalWorkItemPayload` to include adapter-safe tracker fields:

```go
type workItemPayload struct {
    WorkItemID    string         `json:"work_item_id"`
    WorkspaceID   string         `json:"workspace_id,omitempty"`
    ExternalID    string         `json:"external_id,omitempty"`
    SourceItemIDs []string       `json:"source_item_ids,omitempty"`
    ExternalIDs   []string       `json:"external_ids,omitempty"`
    Session       domain.Session `json:"session,omitempty"`
}
```

2. Remove `internal/tui/views/cmds.go:emitWorkItemCompleted`. `OverrideAcceptCmd` must call `CompleteWorkItem()` only and rely on the service-owned `EventWorkItemCompleted`.
3. Remove `internal/orchestrator/implementation.go:emitWorkItemCompleted`; PR/MR finalization moves to `EventSubPlanPRReady` in `task-completion-event-plan.md`.
4. Update `PlanService.ApprovePlan` to accept event-context options and emit the single enriched `EventPlanApproved`.
5. Move `EventPlanGenerated` emission into `PlanService.CreatePlanAtomic`; include the full plan, sub-plans, top-level `work_item_id`, and real `WorkspaceID`.
6. Ensure all plan/sub-plan service events, including `EventPlanSuperseded`, set `SystemEvent.WorkspaceID` to the real workspace ID.

### Phase 3 — TUI typed handlers and switch removal

1. Keep `eventConsumer.toMsg()` as the dispatch mechanism.
2. Enrich all payloads before removing raw switch behavior; do not remove a DB-loading switch case while its typed decoder can still produce an empty or partial entity. `WorkItemUpdatedMsg` must never upsert an empty `domain.Session`; either all work-item events carry `session`, or the decoder marks the payload as incomplete and the handler performs a targeted load.
3. Add tests that every TUI-subscribed event has a registry decoder and an `App.Update` handler, except events deliberately documented as no-op.
4. Remove raw switch cases that duplicate typed handlers.
5. `WorkItemUpdatedMsg`:
   - upsert `msg.Session` only when it is non-empty
   - rebuild sidebar/search
   - run completion side effects when `msg.Session.State == domain.SessionCompleted` (`cancelPipeline`, completion toast, foreman stop)
   - only issue DB loads if the payload lacks data required by the visible panel
6. `PlanGeneratedMsg` / `PlanUpdatedMsg`:
   - upsert `Plan`
   - upsert `SubPlans`
7. Agent-session messages:
   - upsert directly from full payload
8. PR/MR typed messages:
   - keep targeted DB reloads because they originate from external API state and do not carry the full work item.

### Phase 4 — Dead code and tests

Remove:

- `extractSessionID` and `TestExtractSessionID`
- `EventImplementationStarted` tests and assertions

Keep:

- `extractWorkItemID` and `TestExtractWorkItemID`; typed decoders and some fallback paths still require top-level `work_item_id`.

## Adapter Fields

### Required top-level fields

Tracker adapters require top-level `external_id` for single-issue compatibility. Multi-issue handling needs a list field.

Use both:

- `external_id`: existing single-issue path / fallback
- `external_ids`: provider-prefixed all-issue list for GitHub/GitLab/Linear state updates
- `source_item_ids`: raw work-item source IDs when needed by UI/domain code

Update all tracker adapters to iterate `external_ids` first, falling back to `external_id`.

## Files to Modify

| File | Changes |
|---|---|
| `internal/domain/event.go` | Add semantic events; remove `EventImplementationStarted`; rename plan submitted event |
| `internal/domain/session.go` | Already renamed by `tasks/agent-session-rename-plan.md`; update only event-related references left by this plan |
| `internal/service/work_item.go` | Enrich `marshalWorkItemPayload`; keep `EventWorkItemCompleted` service-owned |
| `internal/service/session.go` | Already renamed by `tasks/agent-session-rename-plan.md`; semantic agent-session events from transition/mutators |
| `internal/service/plan.go` | Single enriched plan events; `EventPlanGenerated` from `CreatePlanAtomic`; correct `WorkspaceID` |
| `internal/orchestrator/planning.go` | Stop publishing `EventPlanGenerated` directly; call `PlanService.CreatePlanAtomic` and rely on its event |
| `internal/orchestrator/implementation.go` | Remove implementation-started event and duplicate work-item completion event |
| `internal/orchestrator/resume.go` | Stop publishing resumption events directly; call service-owned resume/transition method |
| `internal/orchestrator/review.go` | Review started payload includes cycle |
| `internal/tui/views/event_consumer.go` | Update decoders; remove implementation-started decoder |
| `internal/tui/views/app.go` | Typed handlers own updates; remove duplicate raw switch handling |
| `internal/tui/views/cmds.go` | Remove completion event publishing; pass plan approval context to services |
| `internal/tui/views/msgs.go` | Add fields/messages for updated payloads |
| `internal/tui/views/service_manager.go` | Remove duplicate event handling paths after typed handlers are complete |
| `internal/adapter/github/adapter.go` | Tracker state uses `external_ids` |
| `internal/adapter/gitlab/adapter.go` | Tracker state uses `external_ids` |
| `internal/adapter/glab/adapter.go` | No event-handler-plan changes; PR/MR readiness moves in task-completion plan |
| `internal/adapter/linear/adapter.go` | Tracker state uses `external_ids` |

## Verification

1. `go test ./internal/domain/`
2. `go test ./internal/service/`
3. `go test ./internal/orchestrator/`
4. `go test ./internal/tui/views/`
5. `go test ./internal/adapter/github/`
6. `go test ./internal/adapter/glab/`
7. `go test ./internal/adapter/gitlab/`
8. `go test ./internal/adapter/linear/`
9. `go test ./test/e2e/`
10. `go build ./...`
11. Manual: approve a plan, observe one `plan.approved`, one work-item state transition event per transition, no duplicate TUI loads for the same event.
