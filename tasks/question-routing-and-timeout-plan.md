# Question Routing, Timeout, and Intentional Interruption Architecture Fix

## Problem Statement

Two related bugs in the planning session question flow:

### Bug 1: Questions not visible when viewing a different work item
When a planning agent calls `ask_foreman`, the question is persisted and the agent session transitions to `waiting_for_answer`, but the TUI does not notify the operator if they are viewing a different work item.

### Bug 2: Planning session becomes interrupted instead of staying answerable
After navigating to the session with the question, the session can show as `interrupted` instead of `waiting_for_answer`. The user can only restart/resume planning, not answer the question. Current cause:

1. Planning wraps the whole harness session in `context.WithTimeout(ctx, s.cfg.SessionTimeout)`.
2. The 30-minute timeout cancels the same context used by `waitForPlanningTurn` and the harness.
3. `waitForPlanningTurn` returns `ctx.Err()`.
4. `planRun` treats cancellation as an interruption and transitions the session to `interrupted`.

This happens regardless of whether the agent is hung, actively making progress, or blocked on a human question.

## Architecture

### Core distinction

Timeout-driven interruption is wrong. Operator-requested interruption is correct and must be resumable.

Planning sessions should not have a fixed lifetime timeout. They end only through:

- `completed` — done event / clean process completion
- `failed` — error event / harness crash / unsuccessful process exit
- `waiting_for_answer` — agent asked a human question and is blocked
- `interrupted` — operator explicitly stops the session

`interrupted` remains a valid state. It must be used for intentional stop paths:

1. User confirms app quit while agent sessions are running.
2. User focuses an agent session and invokes a new Interrupt action.

Both stop paths must preserve enough state for resume via:

- pressing Resume in the interrupted-session UI
- starting/resuming with a manual prompt

### Harness death detection

No progress monitor is needed for this fix. Existing mechanisms already detect harness death:

1. Event channel closure: harness/SSE/bridge stream closes, so `events` closes.
2. `waitForClosedPlanningSession(session)` calls `session.Wait(context.Background())` for definitive exit status.

```go
case evt, ok := <-events:
    if !ok {
        return waitForClosedPlanningSession(session)
    }
```

A future hung-agent detector can be added separately, but it must be an explicit operator-visible interrupt path, not a hidden session timeout.

---

## Changes

### 1. TUI: toast notification for cross-session questions

**File**: `internal/tui/views/app.go`

**Problem**: `QuestionRaisedMsg` updates local state/sidebar but does not notify the operator when they are viewing another work item.

**Fix**: Always show an informational toast for questions. Only refresh content when the current work item is the question's work item.

```go
case QuestionRaisedMsg:
    a.upsertQuestion(msg.SessionID, msg.Question)
    a.rebuildSidebar()

    stageLabel := "Question"
    if msg.Question.Stage == domain.AgentSessionPhasePlanning {
        stageLabel = "Planning question"
    }
    a.toasts.AddToast(
        fmt.Sprintf("%s: %s", stageLabel, summarizeQuestionText(msg.Question.Content, 60)),
        components.ToastInfo,
    )

    if a.currentWorkItemID == msg.WorkItemID {
        cmds = append(cmds, a.updateContentFromState())
    }
    return a, tea.Batch(cmds...)
```

Add or reuse a small helper that trims whitespace, collapses newlines, and truncates by runes without splitting UTF-8.

**Acceptance criteria**:

- Toast appears for every raised question regardless of current view.
- Toast includes a <= 60-rune question preview.
- Toast level is `ToastInfo`.
- Content refresh is limited to the affected work item.

---

### 2. Remove planning `SessionTimeout`

**Files**:

- `internal/orchestrator/planning.go`
- `internal/orchestrator/planning_test.go`
- `internal/config/config.go`

#### 2.1 Remove `SessionTimeout` from `PlanningConfig`

```go
// PlanningConfig controls the planning pipeline.
type PlanningConfig struct {
    MaxParseRetries int
}
```

`DefaultPlanningConfig` should only set `MaxParseRetries`. Existing YAML has no `plan.session_timeout` field today, so no config migration is required unless an uncommitted/local config contains historical fields; unknown YAML fields are already ignored by the current loader.

#### 2.2 Use a non-deadline session context

Replace the session timeout context:

```go
sessionCtx, sessionCancel := context.WithTimeout(ctx, s.cfg.SessionTimeout)
defer sessionCancel()
```

with:

```go
// Preserve parent values for tracing/log correlation, but remove parent
// cancellation and deadlines. The harness session is cancelled only by explicit
// orchestration cleanup: normal return, confirmed quit, focused interrupt, or
// retry restart.
sessionCtx, sessionCancel := context.WithCancel(context.WithoutCancel(ctx))
defer sessionCancel()
```

Important: `context.WithoutCancel(ctx)` returns a context with no `Done`, no `Err`, and no deadline while still delegating values to `ctx`. It is intentionally wrapped with `context.WithCancel` so the orchestrator can stop the harness explicitly.

---

### 3. Keep question waiting alive until answer or explicit interrupt

**File**: `internal/orchestrator/planning.go`

With `SessionTimeout` removed, question waiting should not be modeled as context cancellation. A planning question is an intermediate event, not a terminal turn result.

Required `waitForPlanningTurn` behavior:

1. On `question` event, `handlePlanningTurnEvent` routes the question and transitions the session to `waiting_for_answer`.
2. `waitForPlanningTurn` keeps waiting on the same harness event stream.
3. The session remains registered so `registry.SendAnswer()` can deliver the answer to the blocked harness.
4. When the answer is sent, `ResumeFromAnswer` transitions the session back to `running` and the harness eventually emits `done` or `error`.
5. Only `done`, clean stream closure, error event, harness crash, or explicit operator interruption ends the wait.

Do not make `waiting_for_answer` return `nil` from `waitForPlanningTurn`. Returning would let `runPlanningWithCorrectionLoop` treat an incomplete/missing draft as final or start correction retries. The correct fix is to remove timeout-driven cancellation so the waiting loop remains alive.

Keep the existing “prefer closed clean session over cancelled context” behavior for races, but do not special-case `waiting_for_answer` on cancellation. Cancellation should now mean intentional interruption/shutdown, and the shared interrupt helper owns the durable transition to `interrupted`.

---

### 4. Intentional interruption and resumability

**Files**:

- `internal/tui/views/app.go`
- `internal/tui/views/msgs.go`
- `internal/tui/views/cmds.go`
- `internal/orchestrator/session_registry.go`
- `internal/orchestrator/planning.go`
- `internal/orchestrator/implementation.go`
- `internal/orchestrator/resume.go`
- `internal/orchestrator/manual.go`
- `internal/service/session.go`

#### 4.1 Confirmed app quit interrupts running sessions

Current TUI already shows a quit confirmation modal when agent sessions are active. Change the confirmed path from “kill only” semantics to “interrupt and abort” semantics.

On `QuitConfirmedMsg`:

1. Find all agent sessions in `running` or `waiting_for_answer`.
2. For each registered harness session:
   - read `ResumeInfo()` before abort when available
   - persist it via `AgentSessionService.UpdateResumeInfo`
   - durably transition the agent session to `interrupted`
   - call `SessionRegistry.AbortAndDeregister`
3. Cancel pipeline contexts and stop Foreman/runtime resources.
4. Exit the app.

The quit modal copy should say sessions will be interrupted and resumable, not killed.

Example copy:

> 2 agent sessions are running. Quit will interrupt them so they can be resumed later. Quit anyway?

#### 4.2 Add focused Interrupt action

Add a TUI action available when an agent session is focused and its status is `running` or `waiting_for_answer`.

Behavior:

1. Show confirmation modal: “Interrupt agent session?”
2. On confirmation, call the same interrupt-and-abort helper used by quit.
3. If the focused node represents a work item/product session rather than a concrete agent session, interrupt all running/waiting child agent sessions for that work item.
4. Rebuild sidebar/content and show success/error toast.

Do not use `Steer`; steering interrupts a streaming turn with a message and keeps the session alive. This action stops the harness process and persists `interrupted` state.

#### 4.3 Shared interrupt helper

Introduce one helper path so quit and focused interrupt cannot diverge. The helper should:

```go
func interruptAgentSession(ctx context.Context, session domain.AgentSession) error {
    // If registered, capture ResumeInfo before aborting.
    // Persist ResumeInfo when non-empty.
    // Transition running/waiting_for_answer -> interrupted durably.
    // AbortAndDeregister registered harness.
}
```

Use a detached/durable cleanup context for DB writes so interruption still persists during app shutdown.

#### 4.4 Resume paths

Both app-quit interruptions and focused interruptions must resume through both existing operator paths:

1. **Pressing Resume** from the interrupted-session UI.
   - Existing implementation-session resume should continue to create a replacement session via `Resumption.ResumeSession`.
   - Manual sessions should continue through `ManualSessionService.ResumeManualSession`.
   - Planning sessions need an explicit resume path if they can be interrupted: start planning with `ResumeFromSessionID`/`ResumeInfo` from the interrupted planning session and current draft/path context.

2. **Manual prompt resume**.
   - When the operator supplies a prompt while resuming an interrupted session, pass it as the first resumed user message if native resume is available.
   - If native resume info is unavailable, include the prompt plus log-tail context in the fallback resume prompt.

Do not mark confirmed app quit as completed or failed. It is an intentional interruption and should remain available in the interrupted-session UI.

---

### 5. Answer delivery mechanism

Answer delivery remains unchanged:

1. User answers in TUI.
2. `registry.SendAnswer(sessionID, answer)` calls the registered harness `SendAnswer`.
3. Harness delivers the answer to the blocked agent process.
4. Agent continues and eventually emits `done` or `error`.

Verify implementations:

| Harness | SendAnswer behavior |
|---------|---------------------|
| OpenCode | HTTP reply to the question endpoint |
| Claude Agent Bridge | BCP message via stdio |
| OMP Bridge | BCP message via stdio |
| OpenCode MCP | BCP message via stdio |
| Codex CLI | returns `adapter.ErrSendAnswerNotSupported` |

If a harness cannot answer questions, `SendAnswer` must return `adapter.ErrSendAnswerNotSupported` and the TUI must surface that as an actionable error.

---

## Acceptance Criteria

### Toast notification

- [ ] Toast appears when a question is raised regardless of current work item view.
- [ ] Toast includes truncated question text.
- [ ] Toast level is `ToastInfo`.

### Planning timeout removal

- [ ] `PlanningConfig.SessionTimeout` is removed.
- [ ] Planning no longer wraps harness sessions in `context.WithTimeout`.
- [ ] Planning sessions do not become `interrupted` because of elapsed wall-clock time.

### Question waiting

- [ ] Planning question transitions session to `waiting_for_answer`.
- [ ] `waitForPlanningTurn` remains alive while the session is `waiting_for_answer`.
- [ ] `runPlanningWithCorrectionLoop` does not read/parse a draft or start correction retries while waiting for answer.
- [ ] User can answer after navigating to the session.
- [ ] Answer delivery resumes the same registered harness session and allows the same wait loop to complete on `done`.

### Intentional interruption

- [ ] Confirmed app quit interrupts all `running` and `waiting_for_answer` agent sessions before exit.
- [ ] Focused Interrupt action interrupts the selected running/waiting agent session, or all running/waiting children when focused on a work item node.
- [ ] Interrupt captures and persists non-empty `ResumeInfo` before aborting the harness.
- [ ] Interrupt durably transitions sessions to `interrupted`.
- [ ] Interrupt aborts and deregisters harness sessions.
- [ ] Quit modal copy says sessions will be interrupted and resumable, not killed.

### Resume

- [ ] Sessions interrupted by app quit can be resumed by pressing Resume.
- [ ] Sessions interrupted by focused Interrupt can be resumed by pressing Resume.
- [ ] Both interruption sources can be resumed with an operator-supplied manual prompt.
- [ ] Native resume receives `ResumeFromSessionID` and `ResumeInfo` when available.
- [ ] Fallback resume includes log-tail context when native resume info is unavailable.

### Harness death detection

- [ ] Harness crash is detected via event channel closure.
- [ ] `session.Wait()` is called for definitive exit status.
- [ ] Harness crash transitions session to `failed`, not `interrupted`.

---

## Files to Change

1. `internal/tui/views/app.go` — question toast, focused interrupt action, quit interruption copy/flow
2. `internal/tui/views/msgs.go` — interrupt request/confirmed/done messages
3. `internal/tui/views/cmds.go` — interrupt command if service call is command-based
4. `internal/orchestrator/session_registry.go` — expose registered session lookup or capture+abort helper if needed
5. `internal/orchestrator/planning.go` — remove timeout, question waiting handling, planning interruption/resume support
6. `internal/orchestrator/planning_test.go` — no-timeout and question-waiting tests
7. `internal/orchestrator/implementation.go` — ensure cancellation path persists resume info before durable interruption
8. `internal/orchestrator/resume.go` — resume behavior for interrupted implementation sessions remains covered; add manual prompt path if missing
9. `internal/orchestrator/manual.go` — verify interrupted manual prompt resume path
10. `internal/config/config.go` — remove `PlanningConfig.SessionTimeout`
11. TUI tests under `internal/tui/views/*_test.go` — toast, quit copy, interrupt action, layout bounds

---

## Testing

### Unit tests

1. `TestWaitForPlanningTurn_StaysAliveWhileWaitingForAnswer`
   - Route a planning question.
   - Verify session status is `waiting_for_answer`.
   - Verify `waitForPlanningTurn` does not return before answer/done/error/interrupt.
   - Send answer, emit `done`, and verify return is nil.

2. `TestRunPlanningWithCorrectionLoop_DoesNotRetryWhileWaitingForAnswer`
   - Harness emits question and no done event.
   - Verify no correction session starts and no draft parse is attempted while blocked.
   - Then answer and emit `done`; verify normal draft parsing resumes.

3. `TestWaitForPlanningTurn_ReturnsErrorOnHarnessCrash`
   - Events channel closes.
   - `session.Wait()` returns an error.
   - Verify returned error includes the wait failure.

4. `TestPlanningService_NoTimeoutDuringProgress`
   - Harness receives a context with no deadline.
   - Long-running/progressing session is not cancelled by planning config.

5. `TestQuitConfirmed_InterruptsAndAbortsRunningSessions`
   - Seed running and waiting sessions.
   - Registered harness returns resume info.
   - Confirm quit.
   - Verify resume info persisted, sessions marked interrupted, harnesses aborted/deregistered.

6. `TestFocusedInterrupt_InterruptsSelectedAgentSession`
   - Focus a running agent session.
   - Confirm interrupt.
   - Verify only the selected session is interrupted and resumable.

7. `TestFocusedInterrupt_WorkItemInterruptsRunningChildren`
   - Focus a work item with multiple child sessions.
   - Confirm interrupt.
   - Verify all running/waiting children are interrupted.

8. `TestInterruptedSession_ResumeWithManualPrompt`
   - Interrupt a session.
   - Resume with operator prompt.
   - Verify prompt is delivered through native resume or fallback resume context.

9. TUI layout tests
   - Confirm modals and toast rendering fit narrow terminal widths/heights per `internal/tui/AGENTS.md`.

### Integration scenario

1. Start planning session.
2. Agent raises question.
3. Verify cross-session toast appears.
4. Verify session status is `waiting_for_answer` and not `interrupted` after waiting.
5. Answer via TUI.
6. Verify `registry.SendAnswer()` delivers to harness.
7. Verify agent resumes and completes.
8. Start another session, confirm app quit, restart app.
9. Verify interrupted session appears resumable.
10. Resume via button and via manual prompt path.

---

## Migration

### Config

- Remove `PlanningConfig.SessionTimeout` and default 30-minute timeout.
- No replacement config is added.

### Existing sessions

- Existing sessions already in `interrupted` remain resumable through existing interrupted-session flows.
- New behavior prevents timeout-created interruptions but preserves intentional interruptions.

---

## Why no progress monitor is needed

The original progress-monitor idea is unnecessary for these bugs:

1. Harness death is already detected by event channel closure and `session.Wait()`.
2. Done/error events are already processed by `handlePlanningTurnEvent`.
3. Questions are represented by persisted `waiting_for_answer` state.
4. Intentional stop is handled by explicit interrupt actions.

A hung-agent detector can be designed later as an operator-visible interrupt feature, not as a hidden wall-clock timeout.
