# Steering & Follow-Up Prompts for Agent Sessions

## Overview

Two levels of interactive follow-up for agent sessions in the Substrate TUI:

- **Level 2 (Single-Repo)**: Steer a running agent mid-stream, or restart a completed repo session with follow-up feedback. Operates on individual `Task` rows.
- **Level 1 (Whole-Session)**: Re-plan a completed work item with differential sub-plans. Operates on the `Session` (work item) and produces new/updated sub-plans for only the repos that need changes.

---

## Level 2 — Single-Repo Steering & Follow-Up

### Bridge Protocol Changes (`bridge/omp-bridge.ts`)

| Command | Behavior |
|---------|----------|
| `{"type":"steer","text":"..."}` | Calls `session.prompt(text, { streamingBehavior: "steer" })` — interrupts active streaming turn |
| `{"type":"prompt","text":"..."}` | Unchanged — initial prompt |
| `{"type":"message","text":"..."}` | Unchanged — post-turn message (foreman, critique) |

**Resume support**: Read `SUBSTRATE_RESUME_SESSION_FILE` env var. When set, use `SessionManager.open(filePath)` instead of `SessionManager.create(worktreePath)`.

**Session metadata**: After `createAgentSession()`, emit `session_meta` event:
```json
{"type":"session_meta","omp_session_id":"...","omp_session_file":"..."}
```

### Adapter Interface Changes

```go
// Added to AgentSession interface
Steer(ctx context.Context, msg string) error

// New sentinel error
var ErrSteerNotSupported = errors.New("steer not supported by this harness")

// Added to SessionOpts
ResumeSessionFile string

// Added to HarnessCapabilities
SupportsNativeResume bool
```

### DB Schema

Migration `003_omp_session_meta.sql`:
```sql
ALTER TABLE agent_sessions ADD COLUMN omp_session_file TEXT;
ALTER TABLE agent_sessions ADD COLUMN omp_session_id TEXT;
```

Domain `Task` gains `OmpSessionFile string` and `OmpSessionID string`.

### Service Layer

Task state machine: `completed → running` transition added.

New methods on `TaskService`:
- `FollowUpRestart(ctx, id)` — clears `CompletedAt`, transitions `completed → running`
- `UpdateOmpSessionFile(ctx, id, file, ompID)` — stores OMP session metadata after completion

### Orchestrator

- `SessionRegistry.Steer(ctx, sessionID, msg)` — delegates to `session.Steer()`
- `implementation.go`: After `completeSessionDurably`, captures OMP session file via type assertion on `harnessSession`
- `Resumption.FollowUpSession(ctx, completedTask, feedback, instanceID)`:
  - Validates task is completed
  - Builds follow-up system prompt from sub-plan + last log lines + user feedback
  - Calls `FollowUpRestart()` to transition `completed → running`
  - Starts harness session with `ResumeSessionFile` set (native OMP resume)
  - Registers in `SessionRegistry` for steering

### TUI

- `SteerSessionCmd` calls `registry.Steer()` instead of `registry.SendMessage()`
- New messages: `FollowUpSessionMsg{TaskID, Feedback}`, `FollowUpSessionSentMsg{TaskID}`
- New command: `FollowUpSessionCmd` — calls `resumption.FollowUpSession()`
- `implementing_view.go`:
  - `p` key activates for both running and completed repos
  - Enter routes to `SteerSessionMsg` for running, `FollowUpSessionMsg` for completed
  - Keybind hints: "Prompt agent" for running, "Follow up" for completed
- `app.go`: Handles `FollowUpSessionMsg` → `FollowUpSessionCmd`, `FollowUpSessionSentMsg` → toast

---

## Level 1 — Whole-Session Differential Re-Planning (Future)

### Concept

After all repos complete, the user can provide session-level feedback. This triggers a new planning pass that produces sub-plans only for repos needing changes, then implements only those.

### Work Item State Machine

Add transition: `completed → planning` (currently terminal).

### PlanningService

New method: `FollowUpPlan(ctx, workItemID, feedback)`:
1. Transitions work item `completed → planning`
2. Captures current plan text and per-repo completion summaries
3. Builds differential planning prompt:
   - Original plan context
   - Per-repo results (last N lines from each session log)
   - User feedback
   - Instruction: "Only produce sub-plans for repositories that require changes"
4. Runs planning session with revision template
5. Returns differential plan for review

### TUI Entry Point

- `p` key on a completed work item in the session log view (or sidebar)
- Opens text input for session-level feedback
- Routes to `FollowUpPlanMsg` → `FollowUpPlanCmd`

### Implementation Flow

After differential plan approval:
1. Only new/modified sub-plans trigger worktree creation and agent sessions
2. Existing completed sub-plans are preserved (no re-execution)
3. Review pipeline runs on the delta set

### Dependencies

- Level 2 must ship first (task state machine, resume infrastructure)
- Requires `revisionPromptTemplate` (already exists in planning.go) to support differential context
- May need sub-plan versioning or iteration tracking to avoid ID collisions
