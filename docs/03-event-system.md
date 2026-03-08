# 03 - Event System

The event system is Substrate's integration backbone. Every meaningful state transition emits an event. Work item adapters subscribe to mutate external trackers (Linear, GitLab, GitHub), repo lifecycle adapters subscribe to create or advance GitLab merge requests and GitHub pull requests, and harness-driven orchestration publishes session/review/question events. The bus is in-process, channel-based, and persisted to SQLite for audit and replay.
See `02-layered-architecture.md` for where the event bus sits in the service layer. See `04-adapters.md` for the adapter and harness implementations that consume these events, and `06-tui-design.md` for how the TUI subscribes to events for reactive updates.
---
## 1. System Events Catalog

Every event embeds `BaseEvent` for tracing and persistence:
```go
type EventType string

type BaseEvent struct {
    ID        string    // ULID
    Type      EventType
    Timestamp time.Time
    Workspace string    // workspace ID (ULID from .substrate-workspace), empty for global events; used as workspace_id FK in system_events
}

type SystemEvent interface {
    Base() BaseEvent
}
```

| Event | Payload | Trigger |
|---|---|---|
| `WorkItemIngested` | `WorkItem` | Work item matches filter |
| `WorkspaceCreated` | `Workspace` | Workspace initialized |
| `PlanningStarted` | `WorkItem` | Workspace ready, planner invoked |
| `PlanGenerated` | `Plan` | Planning agent produces plans |
| `PlanSubmittedForReview` | `Plan` | Plan ready for human review |
| `PlanApproved` | `Plan` | Human accepts plan |
| `ImplementationStarted` | `WorkItem`, `Plan` | Plan approved, worktrees being created |
| `PlanRejected` | `Plan`, `Reason string` | Human rejects with feedback |
| `PlanRevised` | `Plan`, `Feedback string` | Human requests changes; agent produces revised plan |
| `WorktreeCreating` | `Workspace`, `RepositoryName`, `Branch` | Pre-hook before git-work checkout |
| `WorktreeCreated` | `Workspace`, `RepositoryName`, `Branch`, `WorktreePath`, `WorkItemTitle` | Post-hook after checkout |
| `WorktreeRemoved` | `Workspace`, `RepositoryName`, `Branch` | Worktree removed via git-work rm |
| `AgentSessionStarted` | `AgentSession` | Agent harness spawned |
| `AgentSessionCompleted` | `AgentSession`, `Result` | Agent exits 0 |
| `AgentSessionFailed` | `AgentSession`, `Error string` | Agent exits non-zero or timeout |
| `AgentSessionInterrupted` | `AgentSession` | Startup reconciliation detects dead PID for running session |
| `AgentSessionResumed` | `AgentSession` | Human chooses to resume an interrupted session |
| `AgentQuestionRaised` | `Question` | Agent cannot resolve from context |
| `AgentQuestionAnswered` | `Question` | Foreman or human answers |
| `ReviewStarted` | `ReviewCycle` | Review agent begins |
| `ReviewCompleted` | `ReviewCycle` | Review passes, no critiques |
| `CritiquesFound` | `ReviewCycle`, `[]Critique` | Review produces critiques |
| `ReimplementationStarted` | `AgentSession`, `[]Critique` | Re-impl session spawned |
| `WorkItemCompleted` | `WorkItem` | All repos pass review |
| `WorkItemFailed` | `WorkItem`, `Error string` | Unrecoverable error in any phase |

### Event Type Constants and Representative Structs

```go
const (
    EventWorkItemIngested       EventType = "work_item.ingested"
    EventWorkspaceCreated       EventType = "workspace.created"
    EventPlanningStarted         EventType = "work_item.planning_started"
    EventPlanGenerated          EventType = "plan.generated"
    EventPlanSubmittedForReview  EventType = "plan.submitted_for_review"
    EventPlanApproved           EventType = "plan.approved"
    EventImplementationStarted   EventType = "work_item.implementation_started"
    EventPlanRejected           EventType = "plan.rejected"
    EventPlanRevised             EventType = "plan.revised"
    EventWorktreeCreating       EventType = "worktree.creating"
    EventWorktreeCreated        EventType = "worktree.created"
    EventWorktreeRemoved        EventType = "worktree.removed"
    EventAgentSessionStarted    EventType = "agent_session.started"
    EventAgentSessionCompleted  EventType = "agent_session.completed"
    EventAgentSessionFailed     EventType = "agent_session.failed"
    EventAgentSessionInterrupted EventType = "agent_session.interrupted"
    EventAgentSessionResumed     EventType = "agent_session.resumed"
    EventAgentQuestionRaised    EventType = "agent_question.raised"
    EventAgentQuestionAnswered  EventType = "agent_question.answered"
    EventReviewStarted          EventType = "review.started"
    EventReviewCompleted        EventType = "review.completed"
    EventCritiquesFound         EventType = "review.critiques_found"
    EventReimplementationStarted EventType = "reimplementation.started"
    EventWorkItemCompleted      EventType = "work_item.completed"
    EventWorkItemFailed          EventType = "work_item.failed"
)

// Representative struct. All events follow this pattern: embed BaseEvent,
// carry payload fields from the catalog table, implement Base().

type PlanApprovedEvent struct {
    BaseEvent
    Plan Plan
}

func (e PlanApprovedEvent) Base() BaseEvent { return e.BaseEvent }
```

## 2. Event Bus

```go
type EventHandler func(ctx context.Context, event SystemEvent) error

type Subscription interface { Unsubscribe() }

type EventBus interface {
    // Pre-hook events: synchronous, error aborts. All others: async fan-out.
    Publish(ctx context.Context, event SystemEvent) error
    Subscribe(handler EventHandler) Subscription
    SubscribeType(eventType EventType, handler EventHandler) Subscription
    Close() error
}
```

### Implementation

The bus is in-process, no external broker. Each subscriber gets a buffered channel (cap 256). SQLite persistence is the first step in `Publish`, so events survive crashes. Pre-hook events run handlers synchronously; post-hook events fan out to channels asynchronously.

```go
type subscriber struct {
    id      string
    types   map[EventType]bool // nil = all types
    ch      chan SystemEvent
    handler EventHandler
    cancel  context.CancelFunc
}

func (s *subscriber) matches(e SystemEvent) bool {
    return s.types == nil || s.types[e.Base().Type]
}
func (s *subscriber) Unsubscribe() { s.cancel() }
type channelEventBus struct {
    mu           sync.RWMutex
    subscribers  map[string]*subscriber
    preHookSubs  []*subscriber // ordered; used for deterministic pre-hook execution
    repo         EventRepository
    preHookTypes map[EventType]bool
    wg           sync.WaitGroup
    onDrop       func(subscriberID string, event SystemEvent) // nil = log only; set by TUI to enqueue a warning toast
}

type BusOption func(*channelEventBus)

func WithDropHandler(fn func(subscriberID string, event SystemEvent)) BusOption {
    return func(b *channelEventBus) { b.onDrop = fn }
}

func NewEventBus(repo EventRepository, opts ...BusOption) EventBus {
    b := &channelEventBus{
        subscribers:  make(map[string]*subscriber),
        repo:         repo,
        preHookTypes: map[EventType]bool{EventWorktreeCreating: true},
    }
    for _, o := range opts { o(b) }
    return b
}

func (b *channelEventBus) Publish(ctx context.Context, event SystemEvent) error {
    b.mu.RLock()
    defer b.mu.RUnlock()
    if b.preHookTypes[event.Base().Type] {
        // Pre-hooks run BEFORE persistence. If a pre-hook rejects, nothing is written to SQLite.
        // Pre-hooks MUST be idempotent: if the process crashes after a pre-hook passes but
        // before the event is persisted, the operation will be retried on restart and the
        // pre-hook will run again with the same inputs.
        for _, sub := range b.preHookSubs {
            if sub.matches(event) {
                if err := sub.handler(ctx, event); err != nil {
                    return fmt.Errorf("pre-hook %s rejected: %w", sub.id, err)
                }
            }
        }
        // All pre-hooks passed; persist now for audit and crash-recovery.
        if err := b.repo.Save(ctx, event); err != nil {
            return fmt.Errorf("persisting event: %w", err)
        }
        return nil
    }

    // Non-pre-hook: persist first, then async fan-out.
    if err := b.repo.Save(ctx, event); err != nil {
        return fmt.Errorf("persisting event: %w", err)
    }
    for _, sub := range b.subscribers {
        if sub.matches(event) {
            select {
            case sub.ch <- event:
            default:
                slog.Warn("event dropped", "subscriber", sub.id, "event", event.Base().Type)
                if b.onDrop != nil {
                    b.onDrop(sub.id, event)
                }
            }
        }
    }
    return nil
}
func (b *channelEventBus) subscribe(types map[EventType]bool, handler EventHandler) Subscription {
    ctx, cancel := context.WithCancel(context.Background())
    sub := &subscriber{
        id: ulid.Make().String(), types: types,
        ch: make(chan SystemEvent, 256), handler: handler, cancel: cancel,
    }
    b.mu.Lock()
    b.subscribers[sub.id] = sub
    if sub.types != nil {
        for t := range sub.types {
            if b.preHookTypes[t] {
                b.preHookSubs = append(b.preHookSubs, sub)
                break
            }
        }
    }
    b.mu.Unlock()
    b.wg.Add(1)
    go func() {
        defer b.wg.Done()
        defer func() {
            b.mu.Lock()
            delete(b.subscribers, sub.id)
            b.mu.Unlock()
        }()
        for {
            select {
            case <-ctx.Done(): return
            case evt := <-sub.ch:
                func() {
                    defer func() {
                        if r := recover(); r != nil {
                            slog.Error("handler panic recovered", "sub", sub.id, "panic", r)
                        }
                    }()
                    hCtx, hCancel := context.WithTimeout(ctx, 30*time.Second)
                    defer hCancel()
                    if err := handler(hCtx, evt); err != nil {
                        slog.Error("handler failed", "sub", sub.id, "err", err)
                    }
                }()
            }
        }
    }()
    return sub
}
func (b *channelEventBus) SubscribeType(t EventType, h EventHandler) Subscription {
    return b.subscribe(map[EventType]bool{t: true}, h)
}
func (b *channelEventBus) Subscribe(h EventHandler) Subscription { return b.subscribe(nil, h) }
func (b *channelEventBus) Close() error {
    b.mu.Lock()
    for _, sub := range b.subscribers { sub.cancel() }
    b.mu.Unlock()
    b.wg.Wait()
    return nil
}
```

### SQLite Persistence

Events stored in `system_events` for audit and replay (see `02-layered-architecture.md`). JSON-serialized via type registry for replay deserialization.

```go
type EventRepository interface {
    Save(ctx context.Context, event SystemEvent) error
    List(ctx context.Context, filter EventFilter) ([]PersistedEvent, error)
    ListSince(ctx context.Context, after string) ([]PersistedEvent, error)
}
```

```sql
CREATE TABLE system_events (
    id TEXT PRIMARY KEY, type TEXT NOT NULL, workspace_id TEXT REFERENCES workspaces(id),
    payload TEXT NOT NULL, created_at TEXT NOT NULL
);
CREATE INDEX idx_events_type ON system_events(type);
CREATE INDEX idx_events_workspace ON system_events(workspace_id);
```
---
## 3. Hook Mechanism

Adapters register event interest at startup via `EventBus.SubscribeType`. The orchestration layer publishes events; the bus routes them to registered handlers. Work item adapters and repo lifecycle adapters are wired independently so tracker mutation and repository-host lifecycle automation can evolve without sharing a forced abstraction.

**Pre-hooks** (e.g., `WorktreeCreating`): synchronous, block caller, error aborts operation, uses caller's context deadline. **Post-hooks** (all others): asynchronous, 30s timeout per handler, errors logged only. Panics recovered via `defer recover()`.

```go
for _, adapter := range workItemAdapters {
    bus.SubscribeType(EventPlanApproved, adapter.OnEvent)
    bus.SubscribeType(EventWorkItemCompleted, adapter.OnEvent)
}

for _, adapter := range repoLifecycleAdapters {
    bus.SubscribeType(EventWorktreeCreated, adapter.OnEvent)
    bus.SubscribeType(EventWorkItemCompleted, adapter.OnEvent)
}
```
---
## 4. Work Item Adapter Interface

```go
type TrackerState string

const (
    TrackerStateTodo       TrackerState = "todo"
    TrackerStateInProgress TrackerState = "in_progress"
    TrackerStateInReview   TrackerState = "in_review"
    TrackerStateDone       TrackerState = "done"
)

type WorkItemFilter struct {
    ExternalIDs []string      // filter by specific external IDs (e.g. "LIN-FOO-123")
    ProjectIDs  []string
    States      []TrackerState
    Labels      []string
    AssigneeID  string
}

type WorkItemAdapter interface {
    Name() string
    Capabilities() AdapterCapabilities

    // Interactive: browse + select for new session creation
    ListSelectable(ctx context.Context, opts ListOpts) (*ListResult, error)
    Resolve(ctx context.Context, selection Selection) (WorkItem, error)

    // Reactive: auto-assignment watching
    Watch(ctx context.Context, filter WorkItemFilter) (<-chan WorkItemEvent, error)

    // External tracker mutations
    Fetch(ctx context.Context, externalID string) (WorkItem, error)
    UpdateState(ctx context.Context, externalID string, state TrackerState) error
    AddComment(ctx context.Context, externalID string, body string) error

    // System event hooks
    OnEvent(ctx context.Context, event SystemEvent) error
}
```

The adapter methods serve distinct roles:

- `Capabilities()` tells the TUI which session creation flow to present. Adapters with `CanBrowse: true` get the interactive search-and-select flow; those without (Manual) get a freeform input form.
- `ListSelectable` + `Resolve` is the interactive selection path: the TUI calls `ListSelectable` to populate a searchable list, then `Resolve` to aggregate selections into a `WorkItem`.
- `Watch` is the reactive auto-assignment path, independent of interactive selection.
- Adapters that lack browsing return `ErrNotSupported` from `ListSelectable`.
- `OnEvent` dispatches via type switch. Each adapter maps substrate events into provider-native tracker mutations — for example Linear state transitions, scoped GitLab issue updates, or GitHub issue state/comment calls.
- The same shared interface intentionally covers adapters with different browse semantics. Concrete provider behavior and browse-filter semantics live in `04-adapters.md`.
See `01-domain-model.md` for the full type definitions of `AdapterCapabilities`, `ListOpts`, `ListResult`, `Selection`, and `SelectableItem`.

```go
func (a *LinearAdapter) Capabilities() AdapterCapabilities {
    return AdapterCapabilities{
        CanWatch:     true,
        CanBrowse:    true,
        CanMutate:    true,
        BrowseScopes: []SelectionScope{ScopeIssues, ScopeProjects, ScopeInitiatives},
    }
}

func (a *LinearAdapter) OnEvent(ctx context.Context, event SystemEvent) error {
    switch e := event.(type) {
    case PlanApprovedEvent:
        if err := a.UpdateState(ctx, e.Plan.WorkItemID, TrackerStateInProgress); err != nil {
            return err
        }
        return a.AddComment(ctx, e.Plan.WorkItemID,
            fmt.Sprintf("Plan approved. Starting across %d repos.", len(e.Plan.SubPlans)))
    case WorkItemCompletedEvent:
        return a.UpdateState(ctx, e.WorkItem.ExternalID, TrackerStateDone)
    default:
        return nil
    }
}
```

---

## 5. Repo Lifecycle Adapter Interface

```go
type RepoLifecycleAdapter interface {
    Name() string
    OnEvent(ctx context.Context, event SystemEvent) error
}
```

Deliberately narrow. Repo lifecycle actions are event reactions, not imperative calls.

### Repo Lifecycle Adapter Event Handling

`RepoLifecycleAdapter` remains deliberately narrow: repo-host automation is event-driven, not an imperative service API. Concrete implementations differ by platform and are selected at startup via remote detection.

```go
func (a *GlabAdapter) OnEvent(ctx context.Context, event SystemEvent) error {
    switch e := event.(type) {
    case WorktreeCreatedEvent:
        return a.createDraftMR(ctx, e.RepositoryName, e.Branch, e.WorkItemTitle)
    case WorkItemCompletedEvent:
        return a.markAllReady(ctx, e.WorkItem.Repos)
    default:
        return nil
    }
}

func (a *GithubAdapter) OnEvent(ctx context.Context, event SystemEvent) error {
    switch e := event.(type) {
    case WorktreeCreatedEvent:
        return a.ensureDraftPR(ctx, e.Branch, e.WorkItemTitle)
    case WorkItemCompletedEvent:
        return a.markReady(ctx, e.WorkItem.Repos)
    default:
        return nil
    }
}
```

GitLab lifecycle remains delegated to `glab`, which infers the instance from the worktree remote. GitHub lifecycle uses direct REST calls rather than `gh`. `internal/app/remotedetect` decides which lifecycle adapter(s) to register by inspecting workspace git remotes at startup, preventing GitLab repos from accidentally attempting GitHub PR automation and vice versa.
---

## 6. Event Flow Diagrams

### 6a. Plan Approval to Work Item State Change

```mermaid
sequenceDiagram
    participant Human
    participant TUI
    participant PlanService
    participant EventBus
    participant SQLite
    participant LinearAdapter
    participant LinearAPI
    Human->>TUI: Approve plan
    TUI->>PlanService: ApprovePlan(planID)
    PlanService->>PlanService: Mark plan approved
    PlanService->>EventBus: Publish(PlanApprovedEvent)
    EventBus->>SQLite: Persist event
    EventBus-->>LinearAdapter: PlanApprovedEvent (async)
    LinearAdapter->>LinearAPI: UpdateIssueState("In Progress")
    LinearAdapter->>LinearAPI: CreateComment("Plan approved...")
    Note over EventBus,LinearAdapter: Error in adapter logged,<br/>does not affect plan flow
```
### 6b. Worktree Creation to Repo Host Lifecycle Automation

```mermaid
sequenceDiagram
    participant Orchestrator
    participant EventBus
    participant SQLite
    participant PreHookHandlers
    participant GitWork
    participant LifecycleAdapter
    participant RepoHost
    Orchestrator->>EventBus: Publish(WorktreeCreatingEvent)
    EventBus->>SQLite: Persist event
    EventBus->>PreHookHandlers: Run synchronously
    PreHookHandlers-->>EventBus: OK (no error)
    EventBus-->>Orchestrator: nil (proceed)
    Orchestrator->>GitWork: checkout -b <branch>
    GitWork-->>Orchestrator: worktree path
    Orchestrator->>EventBus: Publish(WorktreeCreatedEvent)
    EventBus->>SQLite: Persist event
    EventBus-->>LifecycleAdapter: WorktreeCreatedEvent (async)
    LifecycleAdapter->>RepoHost: create draft MR/PR
    Note over Orchestrator,GitWork: If pre-hook returns error,<br/>git-work checkout is skipped
```
## Design Decisions

**In-process, not external broker.** Single-user developer machine. Low event volume. Channel bus is zero-ops and trivially testable. `EventBus` interface allows NATS re-impl later.

**Persist events.** (1) Audit trail for debugging. (2) Crash recovery via replay from last checkpoint (see `01-domain-model.md`).

**Single `OnEvent` dispatch.** Adapters register for specific types via `SubscribeType`. Adding event types never changes adapter interfaces.

**Buffer size 256.** Conservative but generous. Dropped-event warnings (`slog.Warn`) signal pathologically slow handlers. When an `onDrop` handler is registered (the TUI does this), a warning toast is enqueued: e.g. `"GitHub PR update skipped — event queue full"` or `"Linear update skipped — event queue full"`. Preferable to unbounded memory growth.
