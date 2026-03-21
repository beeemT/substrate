# 10 - Live Planning Agent Session Plan
<!-- docs:last-integrated-commit 48e8fb10f7d7c33874e8cfeb903f05cbd983d6ce -->

## Status

Implemented. Planning runs are first-class `agent_sessions` that appear in the task sidebar and stream live planner activity in-session. The feature shipped as described in this plan.

---

## 1. Decision summary

### Agreed product decisions

- Any child harness run, whether it is planning or repo execution, is an **agent session**.
- Planning must appear in the same durable model as other child sessions.
- This project is **pre-v1**, so the implementation should do a **clean cutover** rather than preserve backwards-compatibility shims.
- The planning transcript must show meaningful **live input and output**, not only coarse progress markers.

### Consequences

This is not only a TUI tweak.

The implementation requires a coordinated cutover across:

1. `domain.Task` / `agent_sessions` shape
2. planning orchestration persistence
3. session event / log format
4. TUI sidebar selection and session-log rendering
5. tests and fixtures that currently assume implementation-only child sessions or the current progress-only transcript

---

## 2. Current gap

Today `PlanningService` creates a real planning harness session with a real `sessionID`, real session directory, and real log-producing run, but it does **not** create a `domain.Task` / `agent_sessions` row.

As a result:

- planning is visible only as session-state copy and lifecycle events
- the task sidebar does not know about the planning run
- the operator cannot drill into planning as a live child session
- the transcript fidelity is limited by the current bridge/log format

Relevant code paths:

- planning orchestration: `internal/orchestrator/planning.go`
- implementation session lifecycle pattern: `internal/orchestrator/implementation.go`
- task/session persistence: `internal/repository/sqlite/session.go`
- task sidebar and content switching: `internal/tui/views/app.go`
- session log rendering and tailing: `internal/tui/views/planning_view.go`, `internal/tui/views/cmds.go`
- Oh My Pi bridge event mapping: `bridge/omp-bridge.ts`

---

## 3. Target end state

### Session model

All child harness runs are stored as `agent_sessions` with one canonical model.

That includes at least:

- planning sessions
- implementation sessions
- review sessions

A planning run is not a synthetic UI row and not an event-only artifact. It is a durable child session.

### TUI behavior

When a root session is in `planning`:

- the task sidebar includes a `Planning` child session entry
- entering task drill-down defaults to that planning session
- the main content pane shows the live planning transcript
- the transcript tails while the planning session is running

After planning completes:

- the planning child session remains visible as historical context
- subsequent plan revisions create additional planning child sessions rather than mutating the old run into the new one

### Transcript model

There is one canonical session log/event shape used by session rendering.

That canonical shape must be rich enough to distinguish at least:

- user/prompt input
- assistant/planner output
- tool invocation
- tool result/output
- question/interrupt/failure/completion markers

No dual old/new runtime parsing path should remain after the cutover.

---

## 4. Architecture changes

### 4.1 Domain and schema cutover

Generalize `domain.Task` so it models any child agent session, not only repo/sub-plan execution.

Required direction:

- make `SubPlanID` optional instead of universally required
- add `WorkItemID` directly to the session/task model
- add a canonical discriminator such as `Phase` / `Kind` for at least `planning`, `implementation`, and `review`
- stop assuming repository-specific fields are always populated

Schema changes to `agent_sessions` should follow that model directly.

Required direction:

- `work_item_id` becomes required
- `sub_plan_id` becomes nullable
- a session phase/kind column is added
- fields that do not apply to planning are allowed to be empty/null as appropriate

Because this is pre-v1, do not preserve the old shape in parallel.

### 4.2 Planning orchestration persistence

`PlanningService` should follow the same durable session lifecycle as implementation runs:

1. create a planning task/session row as soon as the planning `sessionID` exists
2. transition it to running before `harness.StartSession`
3. stream/persist the planning session log/events under that session identity
4. mark it completed on successful planning
5. mark it failed on unrecoverable planning failure

For `PlanWithFeedback`, each revision planning run creates a **new** planning session row.

### 4.3 Session event/log cutover

Replace the current progress-oriented session-log event shape with a richer canonical format.

Required direction:

- bridge emits structured entries for prompt/input, assistant text, tool start, tool output/result, and lifecycle markers
- log consumers (`normalizeSessionLogLine`, `SessionLogModel`, and any other session renderers) are updated to the new format in the same change
- tests/fixtures/producers are rewritten to match the new format

Do not keep runtime dual parsing for old and new shapes.

### 4.4 TUI wiring

Use the existing task/session rendering path rather than building a planning-only surface.

Narrowest implementation seam:

- add planning child sessions to `taskSidebarEntries` in `internal/tui/views/app.go`
- preserve and default selection via `selectedTaskSessionID`, `rebuildSidebar`, and `enterTaskSidebar`
- let `showTaskContent`, `SessionLogModel`, and `TailSessionLogCmd` handle live rendering
- remove the special `SessionPlanning` placeholder content branch that currently shows static copy instead of selecting the planning child session

---

## 5. Concrete TODO

### Phase 1 — Domain and storage cutover

- [x] Update `internal/domain/session.go` so child sessions are modeled generically rather than as implementation-only task runs.
- [x] Add/update the session kind/phase enum and use it in the domain model.
- [x] Add a migration that changes `agent_sessions` to the new canonical shape.
- [x] Update `internal/repository/interfaces.go` if repository contracts need the new fields.
- [x] Rewrite `internal/repository/sqlite/session.go` mappings and queries to the new schema.
- [x] Rewrite `SearchHistory` SQL so work items with planning-only child sessions are fully supported.
- [x] Update repository/service tests that currently assume all sessions have a `sub_plan_id`.

### Phase 2 — Planning persistence and event flow

- [x] Inject session/task persistence into `PlanningService`.
- [x] Create a planning `agent_session` row when `PlanningService` creates the planning `sessionID`.
- [x] Transition the planning session through pending -> running -> completed/failed.
- [x] Reuse/extract implementation-style harness event forwarding where that produces the correct shared behavior.
- [x] Ensure revision planning (`PlanWithFeedback`) creates a distinct planning child session.
- [x] Keep the existing planning lifecycle audit events (`work_item.planning`, `plan.generated`, `plan.failed`) only where they still serve a distinct audit purpose.

### Phase 3 — Canonical transcript cutover

- [x] Replace the current bridge session-log output with the richer canonical event format in `bridge/omp-bridge.ts`.
- [x] Update Go-side session log normalization to the new format.
- [x] Update session log rendering so tool invocation and tool output are visibly distinct and readable.
- [x] Remove old-format assumptions from tests, fixtures, and renderers rather than preserving fallback parsing.

### Phase 4 — TUI surfacing

- [x] Add planning child sessions into task sidebar construction.
- [x] Default a `planning` root session to the planning child session when entering task drill-down.
- [x] Replace the static `"Planning has started..."` pane with real session-log rendering.
- [x] Keep historical planning sessions selectable and readable after planning completes.
- [x] Ensure transcript rendering still fits within existing width/height constraints.

### Phase 5 — Verification and cleanup

- [x] Remove any obsolete assumptions, helpers, or comments that describe planning as non-session state.
- [x] Update docs/tests/fixtures that describe the old implementation-only `agent_sessions` model.
- [x] Run focused tests for every touched subsystem.
- [x] Confirm there are no remaining runtime compatibility shims for the old model or old transcript shape.

---

## 6. Autonomous validation I can perform

These are the validations I can execute myself during implementation without needing additional product input.

### Storage / service validation

- run repository and service tests covering `agent_sessions`, session lifecycle transitions, and history queries
- add tests for planning-only child sessions and revision planning sessions
- verify that planning rows are persisted with the correct phase/kind and work-item linkage

### Orchestration validation

- add tests around planning success/failure/revision flows so planning session rows end in the correct terminal state
- verify that planning harness runs emit and persist session log entries under the planning `sessionID`

### TUI validation

- add/extend tests that confirm:
  - task sidebar includes a planning session row
  - planning drill-down defaults to that row
  - planning content uses session log rendering rather than placeholder copy
  - historical planning child sessions remain selectable
- run focused `go test` for `./internal/tui/views` suites, especially the existing width/height constraint tests

### Transcript validation

- add/extend tests for session log normalization so richer tool input/output events render deterministically
- verify width/height constraints still hold with denser transcript content
- verify the new canonical format is used by both planning and implementation session rendering paths

### Suggested concrete commands during implementation

These are the likely focused validation commands, subject to the exact touched packages:

- `go test ./internal/repository/sqlite ./internal/service`
- `go test ./internal/orchestrator/...`
- `go test ./internal/tui/views`
- targeted package tests added for bridge/log parsing if that logic is covered from Go-side consumers

If bridge-specific tests are JavaScript/TypeScript-based in this repo, add and run only those targeted tests as well.

---

## 7. Acceptance criteria

> **All acceptance criteria below are met**, with one intentional product deviation noted at item 4.

The change is done when all of the following are true:

1. A planning run creates a durable `agent_sessions` row using the same canonical child-session model as implementation/review runs.
2. `agent_sessions` no longer assumes every child session belongs to a sub-plan.
3. While a root session is in `planning`, entering task drill-down shows a `Planning` child session row.
4. ~~Entering task drill-down during active planning defaults to that planning child session rather than overview.~~ **Intentionally not implemented.** The product decision was to default to the overview rather than auto-selecting the planning child session. The planning session is visible and selectable in the sidebar, but the operator explicitly chooses to drill into it. This avoids surprising navigation and keeps the overview as the stable default entry point.
5. Selecting that row shows the live planning transcript instead of placeholder copy.
6. The live planning transcript shows meaningful input/output distinctions, including tool invocation and tool output/result visibility.
7. After planning completes, the planning child session remains visible as historical context.
8. Requesting plan changes creates an additional planning child session rather than overwriting the previous one.
9. Session history/search works for work items whose only child sessions are planning runs.
10. No runtime compatibility shim remains for the old task/session shape or the old progress-only transcript shape.
11. All affected focused tests pass.
12. Updated TUI layout tests continue to satisfy width and height constraints, including narrow cases.

---

## 8. Risks and watch-outs

- `agent_sessions` queries are currently sub-plan-centric; search/history SQL is the highest-risk persistence area.
- Planning sessions do not naturally have the same repository/worktree metadata as implementation sessions; nullable/optional fields must be chosen deliberately.
- Revision planning introduces multiple planning child sessions per work item; ordering and selection rules must stay deterministic.
- Richer transcript events can increase vertical space pressure in the TUI; layout-fit tests are required, not optional.

---

## 9. Explicitly rejected alternative

### Synthetic planning sidebar row backed only by lifecycle events

Rejected because it would:

- create a second class of task-like entry outside the canonical `agent_sessions` model
- duplicate selection/history logic in the TUI
- leave planning as a special-case subsystem instead of a normal child session
- make future history, analytics, and review/session features harder to reason about

The correct design is one durable agent-session model for all child runs.
