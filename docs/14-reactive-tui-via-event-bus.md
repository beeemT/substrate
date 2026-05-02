# 14. Reactive TUI via Event Bus

## Status

Post-gap-analysis. Assumes orchestrator publishes all state transitions on the bus (covered by `event_bus_refactor.md`). This document covers only the TUI-side consumption layer.

## Goal

Replace the 2-second poll cycle with a fully event-driven TUI. The TUI subscribes to the bus and reacts to events. No polling for work item, session, plan, question, or review state.

---

## Background: What the TUI Polls Today

`PollTickMsg` fires every 2 seconds from `cmds.go:43`:

```go
func PollTickCmd() tea.Cmd {
    return tea.Tick(pollInterval, func(t time.Time) tea.Msg {
        return PollTickMsg(t)
    })
}
```

The `PollTickMsg` handler in `app.go:1026` issues concurrent DB queries:

```go
case PollTickMsg:
    a.toasts.Prune()
    if a.svcs.WorkspaceID != "" {
        cmds = append(cmds,
            LoadSessionsCmd(a.svcs.Session, a.svcs.WorkspaceID),
            LoadTasksCmd(a.svcs.Task, a.svcs.WorkspaceID),
            LoadLiveInstancesCmd(a.svcs.Instance, a.svcs.WorkspaceID),
        )
    }
    if a.activeOverlay == overlaySessionSearch {
        cmds = append(cmds, a.runSessionSearch(false))
    }
    cmds = append(cmds, PollTickCmd())
    return a, tea.Batch(cmds...)
```

`SessionsLoadedMsg` triggers plan pre-loads; `TasksLoadedMsg` triggers question and review loads. The entire workspace is reloaded every 2 seconds.

---

## Architecture

```
Orchestrator service/transition
    → eventBus.Publish()
        → Bus dispatch (buffered channel send)
            → TUI EventConsumer goroutine reads sub.C
                → app.Send(toMsg(evt))
                    → Bubble Tea serializes through App.Update()
                        → Cache upserted, sidebar rebuilt, content refreshed
```

**Key invariants:**
- One consumer goroutine per TUI. No locking needed on App state — `app.Send()` serializes through Bubble Tea's event loop.
- Events carry the affected entity's data (for creates/updates) or IDs only (for deletes/answers). The TUI applies upserts.
- `PollTickMsg` remains active only for toast pruning. All state-loading commands are removed from the poll handler.

---

## File Map

| File | Change |
|---|---|
| `internal/event/bus.go` | Increase subscriber buffer from 100 → 500 |
| `internal/event/payload.go` | **NEW** — shared payload helper functions |
| `internal/tui/views/msgs.go` | Add `DomainEventMsg`, `SessionLoadedMsg`, `TasksForSessionLoadedMsg`, `PlanForSessionLoadedMsg`, typed event messages |
| `internal/tui/views/cmds.go` | Add targeted load commands; remove `emitPlanApproved` (moved to service layer by `event_bus_refactor.md`) |
| `internal/tui/views/event_consumer.go` | **NEW** — `EventConsumer`, `SubscribeToBus`, `toMsg`, `Stop` |
| `internal/tui/views/app.go` | Add `busSub *event.Subscriber` field; wire subscription in `Init()`; add `DomainEventMsg` handler; remove state-loading from `PollTickMsg`; add targeted load handlers; update completion message handlers |
| `internal/tui/views/settings_service.go` | Add `WithDropHandler` to bus creation; wire `bus.Close()` in `ServiceManager.Close()` |

---

## Phase 1 — Infrastructure

### 1.1 Increase subscriber buffer

**File:** `internal/event/bus.go`, line 168.

```go
// Change:
C: make(chan domain.SystemEvent, 100),

// To:
C: make(chan domain.SystemEvent, 500),
```

Memory: 500 × ~256 bytes = ~128KB per subscriber. At 10 events/second burst, ~50 seconds of headroom.

---

### 1.2 New event payload helpers

**File:** `internal/event/payload.go` (create this file).

```go
package event

import (
    "encoding/json"

    "github.com/beeemT/substrate/internal/domain"
)

func WorkItemStatePayload(workItemID, workspaceID string, session domain.Session) string {
    m := map[string]any{
        "work_item_id": workItemID,
        "workspace_id": workspaceID,
        "session":      session,
    }
    b, _ := json.Marshal(m)
    return string(b)
}

func WorkItemIngestedPayload(workspaceID string, session domain.Session) string {
    m := map[string]any{
        "workspace_id": workspaceID,
        "session":      session,
    }
    b, _ := json.Marshal(m)
    return string(b)
}

func SessionPayload(session domain.Task) string {
    m := map[string]any{
        "session_id":   session.ID,
        "work_item_id": session.WorkItemID,
        "workspace_id": session.WorkspaceID,
        "phase":        string(session.Phase),
    }
    b, _ := json.Marshal(m)
    return string(b)
}

func QuestionAnsweredPayload(sessionID, questionID string) string {
    m := map[string]any{
        "session_id":  sessionID,
        "question_id": questionID,
    }
    b, _ := json.Marshal(m)
    return string(b)
}

func ExtractWorkItemID(payload string) string {
    var m map[string]any
    if err := json.Unmarshal([]byte(payload), &m); err != nil {
        return ""
    }
    if id, ok := m["work_item_id"].(string); ok {
        return id
    }
    return ""
}

func ExtractSessionID(payload string) string {
    var m map[string]any
    if err := json.Unmarshal([]byte(payload), &m); err != nil {
        return ""
    }
    if id, ok := m["session_id"].(string); ok {
        return id
    }
    return ""
}
```

---

### 1.3 New message types

**File:** `internal/tui/views/msgs.go`

Add after the existing `SessionsLoadedMsg` definition (around line 20):

```go
// DomainEventMsg bridges the event.Bus to the bubbletea update loop.
// It is produced by the EventConsumer goroutine and handled in App.Update().
type DomainEventMsg struct {
    Event domain.SystemEvent
}

// Targeted load messages — returned by DomainEventMsg handler to load specific entities.
type SessionLoadedMsg struct {
    WorkItem domain.Session
}

type TasksForSessionLoadedMsg struct {
    WorkItemID string
    Sessions   []domain.Task
}

type PlanForSessionLoadedMsg struct {
    WorkItemID string
    Plan       *domain.Plan
    SubPlans   []domain.TaskPlan
}
```

Add these existing types to `msgs.go` (they do not exist yet — add them):

```go
// Work item lifecycle
type WorkItemIngestedMsg struct {
    WorkspaceID string
    Session    domain.Session
}

type WorkItemUpdatedMsg struct {
    Session domain.Session
}

// Plan lifecycle
type PlanGeneratedMsg struct {
    Plan       *domain.Plan
    SubPlans   []domain.TaskPlan
    WorkItemID string
}

type PlanUpdatedMsg struct {
    WorkItemID string
    Plan       *domain.Plan
}

// Agent session lifecycle
type SessionStartedMsg struct {
    Task domain.Task
}

type SessionUpdatedMsg struct {
    Task domain.Task
}

// Questions
type QuestionRaisedMsg struct {
    SessionID string
    Question domain.Question
}

type QuestionAnsweredMsg struct {
    SessionID  string
    QuestionID string
}

// Reviews — the event triggers a targeted DB load; the event carries only the session ID.
type ReviewStartedMsg           struct{ SessionID string }
type ReviewCompletedMsg         struct{ SessionID string }
type CritiquesFoundMsg          struct{ SessionID string }
type ReimplementationStartedMsg struct{ SessionID string }

// Adapter
type AdapterErrorEventMsg struct {
    Adapter   string
    EventType string
    Err       error
}

// PR merged
type PRMergedMsg struct{ SessionID string }
```

---

### 1.4 New targeted load commands

**File:** `internal/tui/views/cmds.go`

Add after the existing `LoadTasksCmd` (around line 79):

```go
// LoadSessionCmd fetches a single work item from the DB.
func LoadSessionCmd(svc *service.SessionService, workItemID string) tea.Cmd {
    return func() tea.Msg {
        item, err := svc.Get(context.Background(), workItemID)
        if err != nil {
            return ErrMsg{Err: err}
        }
        return SessionLoadedMsg{WorkItem: item}
    }
}

// LoadTasksForSessionCmd fetches all agent tasks for a work item.
func LoadTasksForSessionCmd(svc *service.TaskService, workItemID string) tea.Cmd {
    return func() tea.Msg {
        sessions, err := svc.ListByWorkItemID(context.Background(), workItemID)
        if err != nil {
            return ErrMsg{Err: err}
        }
        return TasksForSessionLoadedMsg{WorkItemID: workItemID, Sessions: sessions}
    }
}

// LoadPlanForSessionCmd fetches the plan and sub-plans for a work item.
func LoadPlanForSessionCmd(planSvc *service.PlanService, workItemID string) tea.Cmd {
    return func() tea.Msg {
        plan, err := planSvc.GetByWorkItemID(context.Background(), workItemID)
        if err != nil {
            return ErrMsg{Err: err}
        }
        var subPlans []domain.TaskPlan
        if plan != nil {
            subPlans, err = planSvc.ListSubPlansByPlanID(context.Background(), plan.ID)
            if err != nil {
                return ErrMsg{Err: err}
            }
        }
        return PlanForSessionLoadedMsg{WorkItemID: workItemID, Plan: plan, SubPlans: subPlans}
    }
}
```

**Verify** `planSvc.GetByWorkItemID` and `planSvc.ListSubPlansByPlanID` exist. If not, add them to `PlanService` or use existing methods. If `PlanService` does not have `GetByWorkItemID`, use the existing `PlanService.Get()` by first looking up the plan ID from the work item's cached plan.

---

### 1.5 Change questions cache to nested map

**File:** `internal/tui/views/app.go`

The `questions` field on `App` (line ~133) changes from:
```go
questions map[string][]domain.Question  // sessionID → questions slice
```
To:
```go
questions map[string]map[string]domain.Question  // sessionID → questionID → Question
```

Initialize in `NewApp` (around line 224):
```go
questions: make(map[string]map[string]domain.Question),
```

Update `QuestionsLoadedMsg` handler (around line 1131) to populate the nested map:
```go
case QuestionsLoadedMsg:
    if a.questions[msg.SessionID] == nil {
        a.questions[msg.SessionID] = make(map[string]domain.Question)
    }
    for _, q := range msg.Questions {
        a.questions[msg.SessionID][q.ID] = q
    }
    a.rebuildSidebar()
    a.refreshSessionSearchEntriesFromLocalState()
    if a.currentWorkItemID != "" {
        cmds = append(cmds, a.updateContentFromState())
    }
    return a, tea.Batch(cmds...)
```

---

### 1.6 New EventConsumer

**File:** `internal/tui/views/event_consumer.go` (create this file):

```go
package views

import (
    "encoding/json"
    "log/slog"

    tea "github.com/charmbracelet/bubbletea"

    "github.com/beeemT/substrate/internal/domain"
    "github.com/beeemT/substrate/internal/event"
)

// EventConsumer bridges the event.Bus to Bubble Tea's update loop.
// It runs a single goroutine that reads from the bus subscriber channel and
// forwards events to the app as tea.Msg values via app.Send().
type EventConsumer struct {
    app       *App
    sub       *event.Subscriber
    stopCh    chan struct{}
    stoppedCh chan struct{}
}

// SubscribeToBus subscribes the TUI to the event bus and starts the consumer goroutine.
// Returns an error if the bus subscription fails. The TUI must exit on error.
func (app *App) SubscribeToBus(bus *event.Bus) (*EventConsumer, error) {
    topics := []string{
        // Work item lifecycle
        string(domain.EventWorkItemIngested),
        string(domain.EventWorkItemPlanning),
        string(domain.EventWorkItemPlanReview),
        string(domain.EventWorkItemApproved),
        string(domain.EventWorkItemImplementing),
        string(domain.EventWorkItemReviewing),
        string(domain.EventWorkItemCompleted),
        string(domain.EventWorkItemFailed),
        string(domain.EventWorkItemMerged),
        // Plan lifecycle
        string(domain.EventPlanGenerated),
        string(domain.EventPlanSubmittedForReview),
        string(domain.EventPlanApproved),
        string(domain.EventPlanRejected),
        string(domain.EventPlanRevised),
        string(domain.EventPlanFailed),
        // Agent session lifecycle
        string(domain.EventAgentSessionStarted),
        string(domain.EventAgentSessionCompleted),
        string(domain.EventAgentSessionFailed),
        string(domain.EventAgentSessionInterrupted),
        string(domain.EventAgentSessionResumed),
        // Questions
        string(domain.EventAgentQuestionRaised),
        string(domain.EventAgentQuestionAnswered),
        // Reviews
        string(domain.EventReviewStarted),
        string(domain.EventReviewCompleted),
        string(domain.EventCritiquesFound),
        string(domain.EventReimplementationStarted),
        // Adapters
        string(domain.EventAdapterError),
        string(domain.EventPRMerged),
    }

    sub, err := bus.Subscribe("tui:"+app.svcs.WorkspaceID, topics...)
    if err != nil {
        return nil, err
    }

    ec := &EventConsumer{
        app:       app,
        sub:       sub,
        stopCh:    make(chan struct{}),
        stoppedCh: make(chan struct{}),
    }

    go ec.run()

    return ec, nil
}

// run reads from the subscriber channel and forwards events to the app.
// It exits when stopCh is closed or the subscriber channel is closed.
func (ec *EventConsumer) run() {
    defer close(ec.stoppedCh)
    for {
        select {
        case <-ec.stopCh:
            return
        case evt, ok := <-ec.sub.C:
            if !ok {
                return
            }
            ec.app.Send(ec.toMsg(evt))
        }
    }
}

// Stop signals the consumer goroutine to exit and waits for it to finish.
func (ec *EventConsumer) Stop() {
    close(ec.stopCh)
    <-ec.stoppedCh
}

// toMsg converts a domain.SystemEvent to a tea.Msg for the update loop.
func (ec *EventConsumer) toMsg(evt domain.SystemEvent) tea.Msg {
    switch domain.EventType(evt.EventType) {
    case domain.EventWorkItemIngested:
        var p struct {
            WorkspaceID string         `json:"workspace_id"`
            Session     domain.Session `json:"session"`
        }
        if err := json.Unmarshal([]byte(evt.Payload), &p); err != nil {
            slog.Warn("failed to decode EventWorkItemIngested payload", "error", err)
            return nil
        }
        return WorkItemIngestedMsg{WorkspaceID: p.WorkspaceID, Session: p.Session}

    case domain.EventWorkItemPlanning,
         domain.EventWorkItemPlanReview,
         domain.EventWorkItemApproved,
         domain.EventWorkItemImplementing,
         domain.EventWorkItemReviewing,
         domain.EventWorkItemCompleted,
         domain.EventWorkItemFailed,
         domain.EventWorkItemMerged:
        var p struct {
            WorkItemID  string         `json:"work_item_id"`
            WorkspaceID string         `json:"workspace_id"`
            Session     domain.Session `json:"session"`
        }
        if err := json.Unmarshal([]byte(evt.Payload), &p); err != nil {
            slog.Warn("failed to decode work item state event payload", "error", err, "type", evt.EventType)
            return nil
        }
        return WorkItemUpdatedMsg{Session: p.Session}

    case domain.EventPlanGenerated:
        var p struct {
            WorkItemID string             `json:"work_item_id"`
            Plan       *domain.Plan       `json:"plan"`
            SubPlans   []domain.TaskPlan `json:"sub_plans"`
        }
        if err := json.Unmarshal([]byte(evt.Payload), &p); err != nil {
            slog.Warn("failed to decode EventPlanGenerated payload", "error", err)
            return nil
        }
        return PlanGeneratedMsg{WorkItemID: p.WorkItemID, Plan: p.Plan, SubPlans: p.SubPlans}

    case domain.EventPlanSubmittedForReview,
         domain.EventPlanApproved,
         domain.EventPlanRejected,
         domain.EventPlanRevised,
         domain.EventPlanFailed:
        var p struct {
            WorkItemID string       `json:"work_item_id"`
            Plan       *domain.Plan `json:"plan"`
        }
        if err := json.Unmarshal([]byte(evt.Payload), &p); err != nil {
            slog.Warn("failed to decode plan event payload", "error", err, "type", evt.EventType)
            return nil
        }
        return PlanUpdatedMsg{WorkItemID: p.WorkItemID, Plan: p.Plan}

    case domain.EventAgentSessionStarted:
        var p struct {
            Session domain.Task `json:"session"`
        }
        if err := json.Unmarshal([]byte(evt.Payload), &p); err != nil {
            slog.Warn("failed to decode EventAgentSessionStarted payload", "error", err)
            return nil
        }
        return SessionStartedMsg{Task: p.Session}

    case domain.EventAgentSessionCompleted,
         domain.EventAgentSessionFailed,
         domain.EventAgentSessionInterrupted,
         domain.EventAgentSessionResumed:
        var p struct {
            Session domain.Task `json:"session"`
        }
        if err := json.Unmarshal([]byte(evt.Payload), &p); err != nil {
            slog.Warn("failed to decode agent session event payload", "error", err, "type", evt.EventType)
            return nil
        }
        return SessionUpdatedMsg{Task: p.Session}

    case domain.EventAgentQuestionRaised:
        var p struct {
            SessionID string           `json:"session_id"`
            Question  domain.Question `json:"question"`
        }
        if err := json.Unmarshal([]byte(evt.Payload), &p); err != nil {
            slog.Warn("failed to decode EventAgentQuestionRaised payload", "error", err)
            return nil
        }
        return QuestionRaisedMsg{SessionID: p.SessionID, Question: p.Question}

    case domain.EventAgentQuestionAnswered:
        var p struct {
            SessionID  string `json:"session_id"`
            QuestionID string `json:"question_id"`
        }
        if err := json.Unmarshal([]byte(evt.Payload), &p); err != nil {
            slog.Warn("failed to decode EventAgentQuestionAnswered payload", "error", err)
            return nil
        }
        return QuestionAnsweredMsg{SessionID: p.SessionID, QuestionID: p.QuestionID}

    case domain.EventReviewStarted,
         domain.EventReviewCompleted,
         domain.EventCritiquesFound,
         domain.EventReimplementationStarted:
        var p struct {
            SessionID string `json:"session_id"`
        }
        if err := json.Unmarshal([]byte(evt.Payload), &p); err != nil {
            slog.Warn("failed to decode review event payload", "error", err, "type", evt.EventType)
            return nil
        }
        switch domain.EventType(evt.EventType) {
        case domain.EventReviewStarted:
            return ReviewStartedMsg{SessionID: p.SessionID}
        case domain.EventReviewCompleted:
            return ReviewCompletedMsg{SessionID: p.SessionID}
        case domain.EventCritiquesFound:
            return CritiquesFoundMsg{SessionID: p.SessionID}
        case domain.EventReimplementationStarted:
            return ReimplementationStartedMsg{SessionID: p.SessionID}
        }

    case domain.EventAdapterError:
        var p struct {
            Adapter   string `json:"adapter"`
            EventType string `json:"event_type"`
            Err       string `json:"error"`
        }
        if err := json.Unmarshal([]byte(evt.Payload), &p); err != nil {
            slog.Warn("failed to decode EventAdapterError payload", "error", err)
            return nil
        }
        return AdapterErrorEventMsg{Adapter: p.Adapter, EventType: p.EventType, Err: errors.New(p.Err)}

    case domain.EventPRMerged:
        var p struct {
            SessionID string `json:"session_id"`
        }
        if err := json.Unmarshal([]byte(evt.Payload), &p); err != nil {
            slog.Warn("failed to decode EventPRMerged payload", "error", err)
            return nil
        }
        return PRMergedMsg{SessionID: p.SessionID}

    default:
        slog.Debug("unhandled bus event in TUI", "type", evt.EventType)
        return nil
    }
}
```

**Note on imports:** `errors` is needed for the `EventAdapterError` case. Ensure it is included in the file imports.

---

### 1.7 Wire subscription in App

**File:** `internal/tui/views/app.go`

Add to `App` struct (around line 196):
```go
busSub *event.Subscriber
```

In `Init()` (around line 283), after the workspace init check and before returning:

```go
if a.svcs.WorkspaceID != "" {
    a.busSub, err = a.svcs.Bus.Subscribe(
        "tui:"+a.svcs.WorkspaceID,
        string(domain.EventWorkItemIngested),
        string(domain.EventWorkItemPlanning),
        string(domain.EventWorkItemPlanReview),
        string(domain.EventWorkItemApproved),
        string(domain.EventWorkItemImplementing),
        string(domain.EventWorkItemReviewing),
        string(domain.EventWorkItemCompleted),
        string(domain.EventWorkItemFailed),
        string(domain.EventWorkItemMerged),
        string(domain.EventPlanGenerated),
        string(domain.EventPlanSubmittedForReview),
        string(domain.EventPlanApproved),
        string(domain.EventPlanRejected),
        string(domain.EventPlanRevised),
        string(domain.EventPlanFailed),
        string(domain.EventAgentSessionStarted),
        string(domain.EventAgentSessionCompleted),
        string(domain.EventAgentSessionFailed),
        string(domain.EventAgentSessionInterrupted),
        string(domain.EventAgentSessionResumed),
        string(domain.EventAgentQuestionRaised),
        string(domain.EventAgentQuestionAnswered),
        string(domain.EventReviewStarted),
        string(domain.EventReviewCompleted),
        string(domain.EventCritiquesFound),
        string(domain.EventReimplementationStarted),
        string(domain.EventAdapterError),
        string(domain.EventPRMerged),
    )
    if err != nil {
        slog.Error("failed to subscribe TUI to event bus", "error", err)
        return func() tea.Msg { return QuitConfirmedMsg{} }
    }

    // Bridge: forward events from the subscriber channel to the update loop.
    cmds = append(cmds, func() tea.Msg {
        for evt := range a.busSub.C {
            a.Send(DomainEventMsg{Event: evt})
        }
        return nil
    })
}
```

**The `a.Send()` call inside the closure uses the Tea program's Send method, not app.Send().** Since the closure is returned as a `tea.Cmd` and runs in Bubble Tea's event loop, it must use `a.Send()` (the Tea model's Send method, available on `*App` via the `tea.Model` interface) to enqueue messages back into the update loop.

---

### 1.8 Add DomainEventMsg handler to Update()

**File:** `internal/tui/views/app.go`

Add this as the **first case** inside the main switch in `Update()` (before `tea.WindowSizeMsg`):

```go
case DomainEventMsg:
    // Re-schedule the channel reader so the next event is delivered.
    // This must happen before the handler returns so the bridge stays alive.
    cmds = append(cmds, func() tea.Msg {
        for evt := range a.busSub.C {
            a.Send(DomainEventMsg{Event: evt})
        }
        return nil
    })

    evt := msg.Event

    // Defensive: ignore events not for this workspace.
    if evt.WorkspaceID != "" && evt.WorkspaceID != a.svcs.WorkspaceID {
        return a, nil
    }

    switch domain.EventType(evt.EventType) {
    // Work item state changes → targeted load of work item and its tasks
    case domain.EventWorkItemIngested:
        // WorkItemIngestedMsg is handled directly (not via this switch)
        return a, tea.Batch(cmds...)

    case domain.EventWorkItemPlanning,
         domain.EventWorkItemPlanReview,
         domain.EventWorkItemApproved,
         domain.EventWorkItemImplementing,
         domain.EventWorkItemReviewing,
         domain.EventWorkItemCompleted,
         domain.EventWorkItemFailed,
         domain.EventWorkItemMerged:
        workItemID := event.ExtractWorkItemID(evt.Payload)
        if workItemID != "" {
            cmds = append(cmds,
                LoadSessionCmd(a.svcs.Session, workItemID),
                LoadTasksForSessionCmd(a.svcs.Task, workItemID),
            )
        }
        if a.currentWorkItemID != "" {
            cmds = append(cmds, a.updateContentFromState())
        }

    // Session lifecycle → targeted load of tasks
    case domain.EventAgentSessionStarted,
         domain.EventAgentSessionCompleted,
         domain.EventAgentSessionFailed,
         domain.EventAgentSessionInterrupted,
         domain.EventAgentSessionResumed:
        workItemID := event.ExtractWorkItemID(evt.Payload)
        if workItemID != "" {
            cmds = append(cmds, LoadTasksForSessionCmd(a.svcs.Task, workItemID))
        }

    // Plan lifecycle → targeted load of plan
    case domain.EventPlanGenerated,
         domain.EventPlanSubmittedForReview,
         domain.EventPlanApproved,
         domain.EventPlanRejected,
         domain.EventPlanRevised,
         domain.EventPlanFailed:
        workItemID := event.ExtractWorkItemID(evt.Payload)
        if workItemID != "" {
            cmds = append(cmds, LoadPlanForSessionCmd(a.svcs.Plan, workItemID))
        }

    // Question events → handled by typed messages below
    case domain.EventAgentQuestionRaised,
         domain.EventAgentQuestionAnswered,
         domain.EventReviewStarted,
         domain.EventReviewCompleted,
         domain.EventCritiquesFound,
         domain.EventReimplementationStarted,
         domain.EventAdapterError,
         domain.EventPRMerged:
        // Handled by typed message cases below; no action needed here.

    default:
        // Unhandled event types are logged by toMsg() and fall through.
    }

    return a, tea.Batch(cmds...)
```

**Then add the typed message cases** after the `DomainEventMsg` case:

```go
case WorkItemIngestedMsg:
    a.workItems = append(a.workItems, msg.Session)
    a.rebuildSidebar()
    a.refreshSessionSearchEntriesFromLocalState()
    if a.currentWorkItemID == msg.Session.ID {
        cmds = append(cmds, a.updateContentFromState())
    }
    return a, tea.Batch(cmds...)

case WorkItemUpdatedMsg:
    for i := range a.workItems {
        if a.workItems[i].ID == msg.Session.ID {
            a.workItems[i] = msg.Session
            break
        }
    }
    a.rebuildSidebar()
    a.refreshSessionSearchEntriesFromLocalState()
    if a.currentWorkItemID == msg.Session.ID {
        cmds = append(cmds, a.updateContentFromState())
    }
    return a, tea.Batch(cmds...)

case SessionLoadedMsg:
    if idx := slices.IndexFunc(a.workItems, func(w domain.Session) bool {
        return w.ID == msg.WorkItem.ID
    }); idx >= 0 {
        a.workItems[idx] = msg.WorkItem
    } else {
        a.workItems = append(a.workItems, msg.WorkItem)
    }
    a.rebuildSidebar()
    a.refreshSessionSearchEntriesFromLocalState()
    // Cascade: load tasks and plan for this work item
    cmds = append(cmds,
        LoadTasksForSessionCmd(a.svcs.Task, msg.WorkItem.ID),
        LoadPlanForSessionCmd(a.svcs.Plan, msg.WorkItem.ID),
    )
    if a.currentWorkItemID == msg.WorkItem.ID {
        cmds = append(cmds, a.updateContentFromState())
    }
    return a, tea.Batch(cmds...)

case TasksForSessionLoadedMsg:
    // Remove old tasks for this work item, add new ones.
    filtered := make([]domain.Task, 0, len(a.sessions))
    for _, s := range a.sessions {
        if s.WorkItemID != msg.WorkItemID {
            filtered = append(filtered, s)
        }
    }
    a.sessions = append(filtered, msg.Sessions...)
    a.rebuildSidebar()
    a.refreshSessionSearchEntriesFromLocalState()
    // Load questions for any session in WaitingForAnswer state and reviews for any Completed session.
    for _, s := range msg.Sessions {
        if s.Status == domain.AgentSessionWaitingForAnswer {
            cmds = append(cmds, LoadQuestionsCmd(a.svcs.Question, s.ID))
        }
        if s.Status == domain.AgentSessionCompleted {
            cmds = append(cmds, LoadReviewsCmd(a.svcs.Review, s.ID))
        }
    }
    if a.currentWorkItemID == msg.WorkItemID {
        cmds = append(cmds, a.updateContentFromState())
    }
    return a, tea.Batch(cmds...)

case PlanForSessionLoadedMsg:
    if msg.Plan != nil {
        a.plans[msg.WorkItemID] = msg.Plan
        a.subPlans[msg.Plan.ID] = msg.SubPlans
    }
    a.rebuildSidebar()
    a.refreshSessionSearchEntriesFromLocalState()
    if a.currentWorkItemID == msg.WorkItemID {
        cmds = append(cmds, a.updateContentFromState())
    }
    return a, tea.Batch(cmds...)

case PlanGeneratedMsg:
    a.plans[msg.WorkItemID] = msg.Plan
    if msg.Plan != nil {
        a.subPlans[msg.Plan.ID] = msg.SubPlans
    }
    a.rebuildSidebar()
    a.refreshSessionSearchEntriesFromLocalState()
    if a.currentWorkItemID == msg.WorkItemID {
        cmds = append(cmds, a.updateContentFromState())
    }
    return a, tea.Batch(cmds...)

case PlanUpdatedMsg:
    a.plans[msg.WorkItemID] = msg.Plan
    a.rebuildSidebar()
    a.refreshSessionSearchEntriesFromLocalState()
    if a.currentWorkItemID == msg.WorkItemID {
        cmds = append(cmds, a.updateContentFromState())
    }
    return a, tea.Batch(cmds...)

case SessionStartedMsg:
    a.sessions = append(a.sessions, msg.Task)
    a.rebuildSidebar()
    a.refreshSessionSearchEntriesFromLocalState()
    if a.currentWorkItemID != "" {
        cmds = append(cmds, a.updateContentFromState())
    }
    return a, tea.Batch(cmds...)

case SessionUpdatedMsg:
    for i := range a.sessions {
        if a.sessions[i].ID == msg.Task.ID {
            a.sessions[i] = msg.Task
            break
        }
    }
    a.rebuildSidebar()
    a.refreshSessionSearchEntriesFromLocalState()
    if a.currentWorkItemID != "" {
        cmds = append(cmds, a.updateContentFromState())
    }
    return a, tea.Batch(cmds...)

case QuestionRaisedMsg:
    if a.questions[msg.SessionID] == nil {
        a.questions[msg.SessionID] = make(map[string]domain.Question)
    }
    a.questions[msg.SessionID][msg.Question.ID] = msg.Question
    a.rebuildSidebar()
    if a.currentWorkItemID != "" {
        cmds = append(cmds, a.updateContentFromState())
    }
    return a, tea.Batch(cmds...)

case QuestionAnsweredMsg:
    // O(1) removal — sessionID is in the payload, not looked up by iteration.
    if sessionQuestions, ok := a.questions[msg.SessionID]; ok {
        delete(sessionQuestions, msg.QuestionID)
        if len(sessionQuestions) == 0 {
            delete(a.questions, msg.SessionID)
        }
    }
    a.rebuildSidebar()
    if a.currentWorkItemID != "" {
        cmds = append(cmds, a.updateContentFromState())
    }
    return a, tea.Batch(cmds...)

case ReviewStartedMsg, ReviewCompletedMsg, CritiquesFoundMsg, ReimplementationStartedMsg:
    var sessionID string
    switch m := msg.(type) {
    case ReviewStartedMsg:
        sessionID = m.SessionID
    case ReviewCompletedMsg:
        sessionID = m.SessionID
    case CritiquesFoundMsg:
        sessionID = m.SessionID
    case ReimplementationStartedMsg:
        sessionID = m.SessionID
    }
    cmds = append(cmds, LoadReviewsCmd(a.svcs.Review, sessionID))
    if a.currentWorkItemID != "" {
        cmds = append(cmds, a.updateContentFromState())
    }
    return a, tea.Batch(cmds...)

case AdapterErrorEventMsg:
    a.toasts.AddToast(fmt.Sprintf("Adapter error (%s): %v", msg.Adapter, msg.Err), components.ToastWarning)
    return a, nil

case PRMergedMsg:
    if a.currentWorkItemID != "" {
        cmds = append(cmds, a.updateContentFromState())
    }
    return a, tea.Batch(cmds...)
```

**Note on `slices.IndexFunc`:** Requires `slices` import from `slices` standard library package. Add `slices` to imports in `app.go`.

---

### 1.9 Update existing handlers to use targeted loads

**File:** `internal/tui/views/app.go`

Update these existing handlers to replace full-workspace loads with targeted loads:

**`SessionsLoadedMsg`** — change from full workspace load to cascading per work item:

```go
case SessionsLoadedMsg:
    if msg.WorkspaceID != a.svcs.WorkspaceID {
        return a, nil
    }
    a.workItems = msg.Items
    a.rebuildSidebar()
    a.refreshSessionSearchEntriesFromLocalState()
    // Cascade: load tasks and plan for each work item
    for _, wi := range msg.Items {
        cmds = append(cmds,
            LoadTasksForSessionCmd(a.svcs.Task, wi.ID),
            LoadPlanForSessionCmd(a.svcs.Plan, wi.ID),
        )
        // Load questions and reviews for sessions that are already in the right state.
        // Note: we don't know task state here yet — handled by TasksForSessionLoadedMsg cascade.
    }
    cmds = append(cmds, a.updateContentFromState())
    return a, tea.Batch(cmds...)
```

**`TasksLoadedMsg`** — cascade to load questions/reviews by session state:

```go
case TasksLoadedMsg:
    if msg.WorkspaceID != a.svcs.WorkspaceID {
        return a, nil
    }
    a.sessions = msg.Sessions
    a.rebuildSidebar()
    a.refreshSessionSearchEntriesFromLocalState()
    for _, s := range msg.Sessions {
        if s.Status == domain.AgentSessionWaitingForAnswer {
            cmds = append(cmds, LoadQuestionsCmd(a.svcs.Question, s.ID))
        }
        if s.Status == domain.AgentSessionCompleted {
            cmds = append(cmds, LoadReviewsCmd(a.svcs.Review, s.ID))
        }
    }
    cmds = append(cmds, a.updateContentFromState())
    return a, tea.Batch(cmds...)
```

**`PlanningRestartedMsg`** (around line 1200):
```go
case PlanningRestartedMsg:
    cmds = append(cmds,
        LoadSessionCmd(a.svcs.Session, msg.WorkItemID),
        LoadTasksForSessionCmd(a.svcs.Task, msg.WorkItemID),
        LoadPlanForSessionCmd(a.svcs.Plan, msg.WorkItemID),
    )
    return a, tea.Batch(cmds...)
```

**`ImplementationCompleteMsg`** (around line 1798):
```go
case ImplementationCompleteMsg:
    a.cancelPipeline(msg.WorkItemID)
    a.toasts.AddToast("Implementation complete", components.ToastSuccess)
    if a.currentWorkItemID != "" {
        cmds = append(cmds, a.updateContentFromState())
    }
    // Stop the Foreman — implementation work is done.
    if a.svcs.Foreman != nil && a.foremanPlanID != "" {
        cmds = append(cmds, StopForemanCmd(a.svcs.Foreman))
    }
    return a, tea.Batch(cmds...)
```

**`FollowUpPlanResultMsg`** (find in file):
```go
case FollowUpPlanResultMsg:
    cmds = append(cmds,
        LoadSessionCmd(a.svcs.Session, msg.WorkItemID),
        LoadTasksForSessionCmd(a.svcs.Task, msg.WorkItemID),
        LoadPlanForSessionCmd(a.svcs.Plan, msg.WorkItemID),
    )
    return a, tea.Batch(cmds...)
```

**`FollowUpSessionCompleteMsg`** (find in file):
```go
case FollowUpSessionCompleteMsg:
    cmds = append(cmds, LoadTasksForSessionCmd(a.svcs.Task, msg.WorkItemID))
    return a, tea.Batch(cmds...)
```

**`SessionDeletedMsg`** — keep full workspace reload since the session is gone:
```go
case SessionDeletedMsg:
    // ... existing deletion logic ...
    if a.svcs.WorkspaceID != "" {
        cmds = append(cmds,
            LoadSessionsCmd(a.svcs.Session, a.svcs.WorkspaceID),
            LoadTasksCmd(a.svcs.Task, a.svcs.WorkspaceID),
        )
    }
    // ... rest unchanged ...
```

---

### 1.10 Add drop handler to bus

**File:** `internal/tui/views/settings_service.go` (or `ServiceManager.buildServices`)

When creating the bus, add the drop handler:

```go
bus := event.NewBus(event.BusConfig{EventRepo: s.eventRepo}, event.WithDropHandler(
    func(subscriberID string, evt domain.SystemEvent) {
        slog.Warn("event dropped: slow subscriber",
            "subscriber", subscriberID,
            "event_type", evt.EventType,
            "workspace_id", evt.WorkspaceID,
        )
    },
))
```

In `ServiceManager.Close()`, ensure the bus is closed:
```go
func (sm *ServiceManager) Close() {
    sm.mu.Lock()
    defer sm.mu.Unlock()
    if sm.services != nil && sm.services.Bus != nil {
        sm.services.Bus.Close()
    }
}
```

---

### 1.11 Remove state-loading from PollTickMsg

**File:** `internal/tui/views/app.go`, `PollTickMsg` handler (around line 1026).

Remove `LoadSessionsCmd`, `LoadTasksCmd`, `LoadLiveInstancesCmd` from the handler. Keep toast pruning:

```go
case PollTickMsg:
    a.toasts.Prune()
    cmds = append(cmds, PollTickCmd())  // keeps toast pruning alive
    return a, tea.Batch(cmds...)
```

---

### 1.12 Add `slices` import to app.go

The targeted load handlers use `slices.IndexFunc`. Add `"slices"` to the imports in `app.go`.

---

## Phase 2 — Cleanup

### 2.1 Remove AdapterErrors channel

The `AdapterErrors chan AdapterErrorMsg` field in `Services` (`settings_service.go`) and the `WaitForAdapterErrorCmd` in `Init()` are no longer needed. `AdapterErrorEventMsg` replaces them.

**File:** `internal/tui/views/app.go`

Remove from `Init()` (around line 280):
```go
WaitForAdapterErrorCmd(a.svcs.AdapterErrors),
```

**File:** `internal/tui/views/services.go`

Remove `AdapterErrors chan AdapterErrorMsg` from the `Services` struct.

**File:** `internal/tui/views/settings_service.go`

Remove `adapterErrors := make(chan AdapterErrorMsg, 16)` from `buildAdapterSetup` or wherever it is created. Remove `AdapterErrors` from the `Services` construction.

---

### 2.2 Verify teardown closes bus

In `QuitConfirmedMsg` handler (around line 991):

```go
case QuitConfirmedMsg:
    a.teardownAllPipelines()
    if a.busSub != nil {
        a.svcs.Bus.Unsubscribe("tui:" + a.svcs.WorkspaceID)
    }
    return a, a.quitCmd()
```

`ServiceManager.Close()` (called during full shutdown) closes the bus, which closes all subscriber channels including the TUI's. The goroutine in the closure exits when `busSub.C` is closed.

---

## Phase 3 — Verification

### 3.1 Build

Run `go build ./...` after each phase. Address all compilation errors before proceeding.

### 3.2 Unit tests

**`event_consumer_test.go`** (create):

```go
func TestEventConsumer_toMsg(t *testing.T) {
    bus := event.NewBus(event.BusConfig{})
    defer bus.Close()

    app := NewApp(testServices())
    ec, err := app.SubscribeToBus(bus)
    require.NoError(t, err)
    defer ec.Stop()

    tests := []struct {
        name        string
        publish     func()
        expectType  any
    }{
        {
            name: "EventWorkItemIngested",
            publish: func() {
                payload, _ := json.Marshal(map[string]any{
                    "workspace_id": "ws-1",
                    "session":      domain.Session{ID: "s-1", Title: "Test"},
                })
                bus.Publish(context.Background(), domain.SystemEvent{
                    ID: domain.NewID(), EventType: string(domain.EventWorkItemIngested),
                    WorkspaceID: "ws-1", Payload: string(payload), CreatedAt: time.Now(),
                })
            },
            expectType: reflect.TypeOf(WorkItemIngestedMsg{}),
        },
        {
            name: "EventAgentQuestionAnswered",
            publish: func() {
                payload, _ := json.Marshal(map[string]any{
                    "session_id":  "sess-1",
                    "question_id": "q-1",
                })
                bus.Publish(context.Background(), domain.SystemEvent{
                    ID: domain.NewID(), EventType: string(domain.EventAgentQuestionAnswered),
                    WorkspaceID: "ws-1", Payload: string(payload), CreatedAt: time.Now(),
                })
            },
            expectType: reflect.TypeOf(QuestionAnsweredMsg{}),
        },
        {
            name: "unknown event returns nil",
            publish: func() {
                bus.Publish(context.Background(), domain.SystemEvent{
                    ID: domain.NewID(), EventType: "workspace.created",
                    WorkspaceID: "ws-1", Payload: "{}", CreatedAt: time.Now(),
                })
            },
            expectType: nil,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            tt.publish()
            // Collect the message via a test channel.
            // The consumer goroutine calls app.Send() which enqueues into Bubble Tea's loop.
            // For testing, we use a spy that intercepts Send.
        })
    }
}

func TestQuestionAnsweredMsg_removes_from_nested_map(t *testing.T) {
    app := &App{
        questions: map[string]map[string]domain.Question{
            "sess-1": {
                "q-1": {ID: "q-1", Text: "test?"},
                "q-2": {ID: "q-2", Text: "test 2?"},
            },
        },
    }

    updated, _ := app.Update(QuestionAnsweredMsg{SessionID: "sess-1", QuestionID: "q-1"})
    app = updated.(*App)

    _, has := app.questions["sess-1"]["q-1"]
    require.False(t, has, "q-1 should be removed")
    _, has = app.questions["sess-1"]["q-2"]
    require.True(t, has, "q-2 should remain")

    _, has = app.questions["sess-1"]
    require.True(t, has, "sess-1 key should remain (has q-2)")
}

func TestQuestionAnsweredMsg_removes_empty_session(t *testing.T) {
    app := &App{
        questions: map[string]map[string]domain.Question{
            "sess-1": {
                "q-1": {ID: "q-1", Text: "test?"},
            },
        },
    }

    updated, _ := app.Update(QuestionAnsweredMsg{SessionID: "sess-1", QuestionID: "q-1"})
    app = updated.(*App)

    _, has := app.questions["sess-1"]
    require.False(t, has, "sess-1 key should be removed when last question is answered")
}
```

### 3.3 Integration test

**`event_consumer_integration_test.go`** (create):

```go
func TestEventConsumer_end_to_end(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test")
    }

    bus := event.NewBus(event.BusConfig{})
    defer bus.Close()

    app := NewApp(testServicesWithBus(bus))
    ec, err := app.SubscribeToBus(bus)
    require.NoError(t, err)
    defer ec.Stop()

    // Publish EventWorkItemIngested
    payload, _ := json.Marshal(map[string]any{
        "workspace_id": app.svcs.WorkspaceID,
        "session":      domain.Session{ID: "wi-new", Title: "New Item", WorkspaceID: app.svcs.WorkspaceID},
    })
    bus.Publish(context.Background(), domain.SystemEvent{
        ID: domain.NewID(), EventType: string(domain.EventWorkItemIngested),
        WorkspaceID: app.svcs.WorkspaceID, Payload: string(payload), CreatedAt: time.Now(),
    })

    // Allow time for goroutine to process and Send to enqueue.
    time.Sleep(50 * time.Millisecond)

    // Process the enqueued message.
    updated, _ := app.Update(WorkItemIngestedMsg{
        WorkspaceID: app.svcs.WorkspaceID,
        Session:     domain.Session{ID: "wi-new", Title: "New Item", WorkspaceID: app.svcs.WorkspaceID},
    })
    app = updated.(*App)

    found := false
    for _, wi := range app.workItems {
        if wi.ID == "wi-new" {
            found = true
            break
        }
    }
    require.True(t, found, "wi-new should be in workItems after EventWorkItemIngested")
}
```

### 3.4 Manual verification checklist

After running the full test suite:

- [ ] `go build ./...` succeeds with no errors
- [ ] `go test ./internal/tui/views/...` all pass
- [ ] `go test ./internal/event/...` all pass
- [ ] TUI starts, subscribes to bus, no `QuitConfirmedMsg` on startup
- [ ] Trigger a plan approval; verify UI updates within 1 second (not 2)
- [ ] Trigger a question answer; verify question disappears from sidebar without 2s delay
- [ ] Press Ctrl+C; verify clean shutdown with no goroutine leaks (`go test -race`)

---

## Edge Cases

### Multiple TUI instances

Two TUI instances running against the same workspace both subscribe to the bus. Both receive all events. Both update their local state independently. This is safe — no shared in-memory state. If both try to start the same planning pipeline, the orchestrator's state machine handles the conflict.

### Event arrives before the work item exists in cache

If `EventAgentSessionStarted` arrives before `EventWorkItemPlanning` (shouldn't happen in practice, but possible on restart), the TUI appends to `sessions` with no matching work item. `TasksForSessionLoadedMsg` cascade from the `DomainEventMsg` handler loads tasks for the work item ID in the event payload. The existing session will be replaced when `TasksForSessionLoadedMsg` arrives. No crash, no inconsistency.

### JSON decode failure

`toMsg()` returns `nil` on decode failure. The bridge closure returns `nil` as its `tea.Msg`, which is a no-op in the update loop. The event is dropped but the TUI remains functional. The warning is logged.

### Bus subscribe failure

`SubscribeToBus` returns an error → `Init()` returns `QuitConfirmedMsg` → TUI exits. No partial state, no degraded mode. Error is logged.

### Slow consumer / buffer full

The drop handler logs the event. The TUI does not receive a `tea.Msg` for the dropped event. State may be stale until the next matching event or the next full workspace reload (on restart). If drops are frequent, increase the subscriber buffer from 500.

### Restart with open questions

`Init()` loads sessions → `TasksLoadedMsg` handler loads questions for any session in `WaitingForAnswer` state → `QuestionsLoadedMsg` populates the nested map. The reactive path (`QuestionRaisedMsg`) handles new questions from the bus in steady state.

---

## Rollout Summary

| Phase | Steps | Files touched |
|---|---|---|
| 1a Infrastructure | 1.1, 1.2 | `event/bus.go`, new `event/payload.go` |
| 1b Messages | 1.3 | `tui/views/msgs.go` |
| 1c Commands | 1.4 | `tui/views/cmds.go` |
| 1d Cache change | 1.5 | `tui/views/app.go` |
| 1e Consumer | 1.6 | new `tui/views/event_consumer.go` |
| 1f App wiring | 1.7, 1.8, 1.9, 1.11, 1.12 | `tui/views/app.go` |
| 1g Drop handler | 1.10 | `tui/views/settings_service.go` |
| 2 Cleanup | 2.1, 2.2 | `tui/views/app.go`, `tui/views/services.go`, `tui/views/settings_service.go` |
| 3 Verification | 3.1–3.4 | tests, manual |
