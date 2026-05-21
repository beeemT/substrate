# Plan: Resume All Agent Sessions

## Overview

When the user is focused on one work item and presses `r` on an interrupted session, only one agent session is resumed. The fix changes `r` to batch-resume **all** non-superseded, non-planning-phase interrupted agent sessions under that work item.

The `WorkItemID` is the right input — not `OldSessionID`. Abandon remains single-session (user selects which one to abandon).

## Changes

### 1. `internal/tui/views/msgs.go`

Replace `OldSessionID` with `WorkItemID`. Drop `SubPlanID` — derivable from each session and unnecessary as a parameter.

```go
// ResumeSessionMsg fires when the user presses [r] on an interrupted session.
type ResumeSessionMsg struct {
    WorkItemID string
}
```

### 2. `internal/tui/views/cmds.go`

Replace `ResumeSessionCmd` with `ResumeAllSessionsForWorkItemCmd`. It takes `WorkItemID`, enumerates all interrupted sessions under it, filters superseded/planning-phase ones, and resumes each.

```go
// ResumeAllSessionsForWorkItemCmd resumes all non-superseded, non-planning-phase
// interrupted sessions for a work item. Planning-phase interruptions are handled
// by RestartPlanningCmd instead.
func ResumeAllSessionsForWorkItemCmd(
    ctx context.Context,
    workItemSvc *service.SessionService,
    planningSvc *orchestrator.PlanningService,
    resumption *orchestrator.Resumption,
    sessionSvc *service.AgentSessionService,
    workItemID string,
    instanceID string,
) tea.Cmd {
    return func() tea.Msg {
        sessions, err := sessionSvc.ListByWorkItemID(ctx, workItemID)
        if err != nil {
            return ErrMsg{Err: err}
        }

        // Build supersession map using the same logic as the overview action card builder
        // (overview.go:1518-1548).
        activeSubPlans := make(map[string]bool)
        hasPlanningActive := false
        for _, s := range sessions {
            if s.Status == domain.AgentSessionRunning || s.Status == domain.AgentSessionPending ||
                s.Status == domain.AgentSessionCompleted || s.Status == domain.AgentSessionWaitingForAnswer {
                if s.Phase == domain.AgentSessionPhasePlanning {
                    hasPlanningActive = true
                } else if s.SubPlanID != "" {
                    activeSubPlans[s.SubPlanID] = true
                }
            }
        }

        var toResume []*domain.AgentSession
        var planningInterrupted *domain.AgentSession
        for _, s := range sessions {
            if s.Status != domain.AgentSessionInterrupted {
                continue
            }
            if s.Phase == domain.AgentSessionPhasePlanning {
                planningInterrupted = &s
                continue
            }
            if s.SubPlanID != "" && activeSubPlans[s.SubPlanID] {
                continue
            }
            toResume = append(toResume, &s)
        }

        // Planning interrupted → restart planning.
        if planningInterrupted != nil {
            if err := workItemSvc.RollbackPlanningInterrupt(ctx, workItemID); err != nil {
                return ErrMsg{Err: err}
            }
            return RestartPlanningCmd(ctx, workItemSvc, planningSvc, sessionSvc, workItemID)()
        }

        if len(toResume) == 0 {
            return SessionResumedMsg{WorkItemID: workItemID, Message: "No resumable tasks"}
        }

        succeeded, failed := 0, 0
        for _, s := range toResume {
            _, err := resumption.ResumeSession(ctx, *s, instanceID)
            if err != nil {
                slog.Warn("resume all: failed to resume session",
                    "agent_session_id", s.ID, "error", err)
                failed++
            } else {
                succeeded++
            }
        }

        msg := "Resumed 1 task"
        if succeeded != 1 {
            msg = fmt.Sprintf("Resumed %d tasks", succeeded)
        }
        return SessionResumedMsg{WorkItemID: workItemID, Message: msg}
    }
}
```

Delete `ResumeSessionCmd` (the old single-session command).

### 3. `internal/tui/views/app.go`

Simplify the `ResumeSessionMsg` handler — no branching needed, always batch-resume.

```go
case ResumeSessionMsg:
    if a.provider.Resumption() != nil {
        cmds = append(cmds, ResumeAllSessionsForWorkItemCmd(
            context.Background(),
            a.provider.Session(),
            a.provider.Planning(),
            a.provider.Resumption(),
            a.provider.Task(),
            msg.WorkItemID,
            a.runtimeCtx.InstanceID,
        ))
    } else {
        a.toasts.AddToast("Resume not available (no resumption service)", components.ToastError)
    }
    return a, tea.Batch(cmds...)
```

> **Context:** The original `ResumeSessionCmd` used `pipelineCtxForTask(msg.OldSessionID)` to get a cancellable context. The batch command uses `context.Background()` — acceptable because resume is fire-and-forget; each resumed harness goroutine manages its own lifecycle.

### 4. `internal/tui/views/overview.go`

Update the `r` handler for `overviewActionInterrupted` to use `WorkItemID` instead of extracting one `OldSessionID`.

Around line 499–518:

```go
case overviewActionInterrupted:
    if action.Session != nil && action.CanAct {
        if action.Session.Phase == domain.AgentSessionPhasePlanning {
            wID := action.Session.WorkItemID
            return m, func() tea.Msg { return RestartPlanMsg{WorkItemID: wID} }
        }
        // Resume all interrupted sessions for this work item.
        return m, func() tea.Msg {
            return ResumeSessionMsg{WorkItemID: m.data.WorkItemID}
        }
    }
```

Update the keybind hints label (around line 830–841). Count resumable action cards to show "Resume all (N)" when there are multiple:

```go
case overviewActionInterrupted:
    hints := []KeybindHint{{Key: "i", Label: "Inspect"}}
    if action.CanAct {
        resumableCount := 0
        for _, card := range m.data.Actions {
            if card.Kind == overviewActionInterrupted && card.Session != nil &&
                card.Session.Phase != domain.AgentSessionPhasePlanning {
                resumableCount++
            }
        }
        resumeLabel := "Resume all"
        if resumableCount > 1 {
            resumeLabel = fmt.Sprintf("Resume all (%d)", resumableCount)
        }
        hints = append([]KeybindHint{{Key: "r", Label: resumeLabel}, {Key: "a", Label: "Abandon"}}, hints...)
    }
    return hints
```

> **Note:** Loop variable is `card`, not `a`, to avoid shadowing the `action` parameter.

### 5. `internal/tui/views/interrupted_view.go`

Update `Update()` — pressing `r` now resumes all for the work item.

```go
case "r":
    if m.isPlanningPhase {
        wID := m.workItemID
        return m, func() tea.Msg { return RestartPlanMsg{WorkItemID: wID} }
    }
    wID := m.workItemID
    return m, func() tea.Msg {
        return ResumeSessionMsg{WorkItemID: wID}
    }
```

Drop `sID` and `spID` locals — no longer needed.

Update `KeybindHints()`:

```go
if m.canAct {
    if m.isPlanningPhase {
        return []KeybindHint{
            {Key: "r", Label: "Restart planning"},
            {Key: "a", Label: "Abandon"},
        }
    }
    return []KeybindHint{
        {Key: "r", Label: "Resume all"},
        {Key: "a", Label: "Abandon"},
    }
}
```

### 6. `internal/tui/views/overlay_help.go`

```go
{"Interrupted", []entry{
    {"r", "Resume all"},
    {"a", "Abandon session"},
}},
```

### 7. Abandon path (unchanged)

The `ConfirmAbandonMsg` path at `interrupted_view.go` sends `ConfirmAbandonMsg{SessionID: m.sessionID}` — single session, correct. The abandon action card in the overview (`overview.go:486-491`) already uses `action.Session.ID`, not batch. No changes needed.

## Response Handling

`SessionResumedMsg.AgentSession` will be nil after batch resume — only `WorkItemID` and `Message` are set. This is fine because `EventAgentSessionResumed` (emitted per session by `AgentSessionService.Resume`) triggers `LoadTasksForSessionCmd` which refreshes the UI. No downstream changes needed.

## Behavioral Summary

| Entry point | Key | Behavior |
|---|---|---|
| Overview: interrupted action card | `r` | Resume all non-superseded, non-planning interrupted sessions for this work item |
| Overview: planning interrupted card | `r` | Restart planning (unchanged) |
| Overlay: non-planning | `r` | Resume all non-superseded interrupted sessions for this work item |
| Overlay: planning | `r` | Restart planning (unchanged) |
| Abandon | `a` | Confirm → abandon selected session only (unchanged) |

## Supersession Filter

Same logic as `buildActionCards` (overview.go:1518-1548):
- An interrupted implementation session is **skipped** if the current instance already owns an active (running/pending/completed/waiting) replacement for its sub-plan.
- Interrupted sessions whose active replacement is owned by a different live instance are **resumed** (the other instance may have abandoned).
- `hasPlanningActive` tracks whether the current instance owns an active planning session — used to supersede stale interrupted planning sessions from a dead instance.

## Tests to Add

### New tests for `ResumeAllSessionsForWorkItemCmd`

1. **`TestResumeAllSessionsForWorkItemCmd_ResumesAll`** — two interrupted implementation sessions; verifies both are resumed.
2. **`TestResumeAllSessionsForWorkItemCmd_SkipsSuperseded`** — session A interrupted for sub-plan X; session B running for same sub-plan X (active replacement); verifies A is skipped.
3. **`TestResumeAllSessionsForWorkItemCmd_PlanningTriggersRestart`** — one planning-phase + one implementation-phase interrupted; verifies planning restart is triggered, implementation not touched.
4. **`TestResumeAllSessionsForWorkItemCmd_ReportsCorrectCount`** — three resumable; verifies message says "Resumed 3 tasks".
5. **`TestResumeAllSessionsForWorkItemCmd_NoResumableTasks`** — all sessions are superseded or non-interrupted; verifies "No resumable tasks" message.
6. **`TestResumeAllSessionsForWorkItemCmd_PartialFailure`** — three resumable sessions; two fail; verifies message says "Resumed 1 task" and failed counter is incremented.

### New tests for keybind hints

7. **`TestOverviewInterruptedActionCard_FiresResumeSessionMsgWithWorkItemID`** — verify `r` on the action card fires `ResumeSessionMsg{WorkItemID: "wi-1"}` (not `OldSessionID`).
8. **`TestInterruptedOverlayUpdate_FiresResumeSessionMsgWithWorkItemID`** — verify `r` fires `ResumeSessionMsg{WorkItemID: m.workItemID}`.
9. **`TestInterruptedOverlayKeybindHints_ShowsResumeAll`** — verify hint label is `"Resume all"`.
10. **`TestOverviewInterruptedKeybindHints_Pluralizes`** — two resumable interrupted cards; verifies hint label is `"Resume all (2)"`.

### Tests to update

11. **`TestOverviewInterruptedImplementationDispatchesResumeSessionMsg`** (in `overview_test.go:1292-1334`) — currently asserts on `OldSessionID` and `SubPlanID` fields. Rewrite to assert on `WorkItemID` field instead:
    ```go
    resumeMsg := msg.(ResumeSessionMsg)
    if resumeMsg.WorkItemID != "wi-1" {
        t.Fatalf("ResumeSessionMsg.WorkItemID = %q, want %q", resumeMsg.WorkItemID, "wi-1")
    }
    ```
