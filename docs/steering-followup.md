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

## Level 1 — Whole-Session Differential Re-Planning

### Work Item State Machine

Transition added: `completed → planning`. After this, the existing pipeline runs:
`planning → plan_review → approved → implementing → reviewing → completed`

### Planning Round Versioning

Each sub-plan carries a `PlanningRound int` field, set from `Plan.Version` when
created or modified during `ApplyReviewedPlanOutput`. This tracks which repos were
touched in each re-planning wave:
```
Initial (round 0): repo-a(0), repo-b(0), repo-c(0)
Follow-up 1 (round 1): repo-a(1), repo-b(0), repo-c(0)    — only repo-a changed
Follow-up 2 (round 2): repo-a(2), repo-b(2), repo-c(0)    — repo-a + repo-b changed
```

DB: Migration `004_sub_plan_planning_round.sql`:
```sql
ALTER TABLE sub_plans ADD COLUMN planning_round INTEGER NOT NULL DEFAULT 0;
```

### PlanningService.FollowUpPlan()

```go
func (s *PlanningService) FollowUpPlan(ctx context.Context, workItemID, feedback string) (*domain.PlanningResult, error)
```

1. Validates work item is `completed`
2. Fetches current approved plan + sub-plans
3. Builds `RepoResultSummary` per repo (status + last 50 lines of session log)
4. Renders `followUpPromptTemplate` with: user feedback, repo results, current plan
5. Transitions work item `completed → planning`
6. Calls `planRun()` with rendered follow-up context
7. Returns to `plan_review` for user approval before re-implementation

### Sub-Plan Reconciliation

`ApplyReviewedPlanOutput` now:
- Sets `PlanningRound = plan.Version` on created/updated sub-plans
- Resets `completed → pending` when sub-plan content changes (signals re-execution)
- Leaves unchanged sub-plans untouched (status + PlanningRound preserved)

### Differential Implementation

`BuildWaves` filters out `completed` sub-plans before grouping into waves.
Only `pending` (new or reset) sub-plans enter execution. First-time implementation
is unaffected (all sub-plans start as pending).

### Worktree Reuse + Lifecycle Adapters

When `ensureWorktree` finds an existing branch, it emits `EventWorktreeReused`
instead of creating a new worktree. Adapters handle this:
- **glab**: `onWorktreeReused` updates MR description via `glab mr update --description`
- **github**: `onWorktreeReused` updates PR body via GitHub API PATCH

Both use the same `WorktreeCreatedPayload` struct (includes updated `SubPlan` content).
This also fixes Level 2 resume: previously, reused worktrees didn't trigger adapter
updates for MR/PR descriptions.

### TUI Entry Point

- `CompletedModel` overlay: `f` keybind opens feedback text input
- Enter submits `FollowUpPlanMsg{WorkItemID, Feedback}` → `FollowUpPlanCmd`
- Result transitions work item to `plan_review`; existing plan review flow takes over
- Success/error toasts via `FollowUpPlanResultMsg`