# Substrate Bug Audit Refresh

Date: 2026-03-10
Scope: orchestration/runtime state handling, mixed-provider wiring, work-item deduplication, and TUI task-sidebar transitions.
Status: investigation only; no fixes applied.

This refresh supersedes older audit notes for the areas listed above. Several previously reported issues in these files have already changed shape, so this file records only the findings re-validated against the current tree.

## Severity legend

- **High**: breaks a core workflow, strands durable state, or hides required operator action.
- **Medium**: real functional breakage or incorrect cross-system behavior in a realistic edge case.
- **Low**: reliability or coverage risk worth fixing before heavy follow-up work.

## Validation snapshot

Commands run during this audit:

- `go test ./internal/app -run 'TestBuildRepoLifecycleAdapters_|TestWireAdapterSubscriptions' -count=1` → passed
- `go test ./internal/tui/views -run 'TestSidebarSourceDetailsSelectionShowsSourceContent|TestAppSidebarEnterTaskSidebarIncludesSourceDetailsEntry' -count=1` → passed
- `go test ./internal/orchestrator -run 'TestResumeSession_StartsNewSessionWithLogContext|TestResumeSession_StartFailureAbortsHarnessAndDeletesSession' -count=1` → passed
- `go test ./internal/service -run 'TestWorkItemService_Create' -count=1` → passed
- `command -v gh` → `/opt/homebrew/bin/gh`

Those passing tests are useful context: the defects below are currently under-covered edge cases rather than already-failing happy paths.

## Findings summary

| ID | Severity | Area | Short description |
|---|---|---|---|
| BR-001 | High | Implementation rollback | Cancellation cleanup reuses the canceled request context, so failed implementation can leave the work item stuck in `implementing` |
| BR-002 | High | Session lifecycle | Sub-plan execution still starts the harness when the session cannot be persisted as `running`, leaving pending phantom sessions |
| BR-003 | High | Resume cleanup | Resume cleanup deletes the new session with a canceled context, so canceled resumes can leave permanent pending sessions |
| BR-004 | High | TUI state transitions | Selecting `Source details` blocks question/interruption/review/completion takeovers in the main content pane |
| BR-005 | Medium | Mixed-provider wiring | Mixed GitHub/GitLab workspaces fan lifecycle events to every repo adapter instead of routing by repo/provider |
| BR-006 | Medium | Work-item deduplication | Source-item dedupe ignores repo/project/group scope for milestones and epics, rejecting valid work items as duplicates |

---

## BR-001 — High — Implementation rollback uses a canceled context

**Impact**

If implementation is canceled after the work item has been moved to `implementing`, best-effort rollback can fail immediately and leave the persisted work item stuck in `implementing` even though the run has already errored out.

**Evidence**

- `internal/orchestrator/implementation.go:150-160` — `Implement` defers `s.workItemSvc.FailWorkItem(ctx, workItem.ID)` when later steps error after `implementingStarted = true`.
- `internal/orchestrator/implementation.go:163-199` — after `StartImplementation`, later steps like `prepareWorktrees(...)` still return fatal errors.
- `internal/orchestrator/implementation.go:245-253` — the non-deferred failure path also uses the original `ctx` for `FailWorkItem`.
- `internal/service/work_item.go:207-221` — `FailWorkItem` starts with repository reads/writes on that same context.

**Why this is a bug**

Cancellation is a normal control path. Once durable state has been changed, rollback/failure transitions cannot depend on the still-live request context that just got canceled.

**Repro / verification idea**

- Start implementation.
- Cancel the caller context after `StartImplementation` succeeds but before `prepareWorktrees` or later cleanup completes.
- Observe `Implement` return an error while the work item remains `implementing` instead of `failed`.

**Fix prep**

- Likely fix file: `internal/orchestrator/implementation.go`.
- Use a short-lived cleanup context for terminal state repair after cancellation.
- Add regression coverage for cancellation after `StartImplementation` and assert the final persisted work-item state is `failed`.

---

## BR-002 — High — Sub-plan execution starts the harness even when the session never becomes `running`

**Impact**

If the session row cannot be transitioned from `pending` to `running`, Substrate still launches the real harness session. A successful run then cannot be persisted as `completed` because `pending -> completed` is illegal, leaving phantom active sessions and inconsistent state between the harness, session table, sub-plan, and work item.

**Evidence**

- `internal/orchestrator/implementation.go:377-380` — `s.sessionSvc.Start(ctx, sessionID)` failure is logged but not treated as fatal.
- `internal/orchestrator/implementation.go:385-399` — `s.harness.StartSession(...)` still runs after that failure.
- `internal/orchestrator/implementation.go:421-425` — success path later calls `s.sessionSvc.Complete(ctx, sessionID)`.
- `internal/service/session.go:107-127` — `Start` is the only path from `pending` to `running`.
- `internal/service/session.go:140-160` — `Complete` requires a `running` session.
- `internal/tui/views/app.go:1550-1555` — `pending` sessions are still counted as active in the UI.
- `internal/orchestrator/instance_manager.go:104-126` — reconciliation only repairs `running` / `waiting_for_answer`, not `pending`.

**Why this is a bug**

Launching an external session without first persisting the authoritative state transition breaks the core invariant that the database reflects which sessions are actually live.

**Repro / verification idea**

- Inject a repository/update error on the first `sessionSvc.Start` call while allowing session creation and later reads.
- Let the harness session finish successfully.
- Observe the session row remain `pending`, the UI still count it as active, and reconcile ignore it.

**Fix prep**

- Likely fix file: `internal/orchestrator/implementation.go`.
- Treat `sessionSvc.Start` failure as fatal before starting the harness, or abort and clean up immediately if a later refactor requires post-start persistence.
- Add a regression test that forces `Start` to fail and asserts no harness session is left alive and no `pending` phantom session remains.

---

## BR-003 — High — Resume cleanup deletes the new session with a canceled context

**Impact**

A canceled resume can leave behind a brand-new `pending` session row. That row is shown as active in the TUI but is never recovered by instance reconciliation, so the operator can be left with a permanent phantom session.

**Evidence**

- `internal/orchestrator/resume.go:121-132` — if `sessionSvc.Start(ctx, newSession.ID)` fails after the harness session already started, cleanup calls `harnessSession.Abort(ctx)` and `sessionSvc.Delete(ctx, newSession.ID)` using the same request context.
- `internal/service/session.go:242-247` — `Delete` begins with `repo.Get(ctx, id)` and depends on a live context.
- `internal/tui/views/app.go:1550-1555` — `pending` sessions count as active.
- `internal/orchestrator/instance_manager.go:104-126` — reconcile does not touch `pending` sessions.
- Existing happy-path resume coverage passes: `go test ./internal/orchestrator -run 'TestResumeSession_StartsNewSessionWithLogContext|TestResumeSession_StartFailureAbortsHarnessAndDeletesSession' -count=1`.

**Why this is a bug**

The cleanup path exists, but cancellation defeats it. That leaves durable UI-visible garbage state that other recovery logic explicitly does not repair.

**Repro / verification idea**

- Start resume.
- Cancel the request context after `harness.StartSession(...)` succeeds but before `sessionSvc.Start(...)` cleanup completes.
- Observe `ResumeSession` return an error and the new session row remain `pending`.

**Fix prep**

- Likely fix file: `internal/orchestrator/resume.go`.
- Use an uncanceled cleanup context for abort/delete, or convert the row into a terminal failed state using a cleanup context.
- Add a targeted regression test with context-aware session repo methods; current mocks are too forgiving for this edge case.

---

## BR-004 — High — `Source details` selection suppresses higher-priority content transitions

**Impact**

While the operator is parked on the `Source details` task row, the content pane stops following the parent work-item state. Escalated questions, interrupted sessions, and later reviewing/completed transitions are hidden until the operator manually leaves that row.

**Evidence**

- `internal/tui/views/app.go:1101-1106` — when `selectedTaskSessionID()` equals `taskSidebarSourceDetailsID`, `updateContentFromState` immediately switches to `ContentModeSourceDetails` and returns.
- `internal/tui/views/app.go:1137-1239` — the implementing/reviewing/completed/question/interrupted takeover logic lives after that early return and therefore never runs.
- `internal/tui/views/app.go:590-650` and `886-893` — `SessionsLoadedMsg`, `QuestionsLoadedMsg`, and `ReviewCompleteMsg` all trigger `updateContentFromState()`, so the stale view persists across async refreshes.
- Existing tests only cover the happy path of opening source details: `go test ./internal/tui/views -run 'TestSidebarSourceDetailsSelectionShowsSourceContent|TestAppSidebarEnterTaskSidebarIncludesSourceDetailsEntry' -count=1`.

**Why this is a bug**

`Source details` is a convenience subview. It must not outrank operator-action-required states such as escalated questions or interrupted sessions, and it must not block workflow progression in the content pane.

**Repro / verification idea**

- Open an implementing work item.
- Enter the task sidebar and select `Source details`.
- Let one session move to `waiting_for_answer` with an escalated question, or to `interrupted`, or let the work item transition to `reviewing` / `completed`.
- Observe the sidebar update while the content pane remains stuck on `Source details`.

**Fix prep**

- Likely fix file: `internal/tui/views/app.go`.
- Evaluate parent workflow state first, then allow `Source details` only when no higher-priority takeover is active.
- Add regression coverage for async transitions while `Source details` is selected.

---

## BR-005 — Medium — Mixed-provider lifecycle adapters are subscribed globally instead of by repo/provider

**Impact**

In a workspace that contains both GitHub and GitLab repos and has both auth paths available, every lifecycle event is delivered to every repo-lifecycle adapter. Wrong-provider automation attempts then run against unrelated repos, producing bad API/CLI calls and noisy warnings.

**Evidence**

- `internal/app/wire.go:61-93` — `BuildRepoLifecycleAdapters` can return one adapter per detected platform.
- `cmd/substrate/main.go:197-219` — each returned lifecycle adapter subscribes to the same `worktree.created` and `work_item.completed` event stream.
- `internal/tui/views/settings_service.go:329-350` — settings reload re-registers lifecycle adapters the same way.
- `internal/adapter/github/adapter.go:355-367` — GitHub lifecycle handler reacts to every matching event it receives.
- `internal/adapter/glab/adapter.go:73-83` — glab handler does the same.
- This workstation has `gh` installed (`command -v gh` → `/opt/homebrew/bin/gh`), so GitHub lifecycle registration is a realistic runtime path when auth is available.

**Why this is a bug**

Provider detection is done at workspace scope, but lifecycle events describe individual repos/worktrees. Broadcasting them to all lifecycle adapters loses the repo/provider boundary and turns mixed-host workspaces into cross-wired automation.

**Repro / verification idea**

- Create a workspace with one GitHub repo and one GitLab repo.
- Ensure GitHub auth is available and `glab` is installed.
- Trigger `worktree.created` for either repo.
- Observe both lifecycle handlers run, even though only one provider owns that repo.

**Fix prep**

- Likely fix files: `internal/app/wire.go`, `cmd/substrate/main.go`, `internal/tui/views/settings_service.go`, and possibly event payload/routing code.
- Route lifecycle events by repo/provider instead of simple fan-out.
- Add a mixed-host regression test asserting only the matching adapter receives each repo event.

---

## BR-006 — Medium — Source-item dedupe ignores repo/project/group scope for milestones and epics

**Impact**

Valid work items can be rejected as duplicates when two different repos/projects/groups reuse the same milestone number or epic IID.

**Evidence**

- `internal/service/work_item.go:103-140` — `duplicateSourceItemID` only compares `Source`, `SourceScope`, and raw `SourceItemIDs`.
- `internal/adapter/github/adapter.go:212-214` and `252-266` — GitHub milestone selections use raw milestone numbers in `SourceItemIDs`, while repo identity is carried separately in metadata / `ExternalID`.
- `internal/adapter/gitlab/adapter.go:139-141`, `188-200`, and `206-217` — GitLab milestone IDs and epic IIDs are likewise stored as raw IDs while project/group scope lives in metadata.
- Current service coverage only exercises repo-qualified issue IDs: `go test ./internal/service -run 'TestWorkItemService_Create' -count=1`.

**Why this is a bug**

For issues, the item IDs are already repo/project-qualified. For milestones and epics, they are not. Reusing the same dedupe rule across both shapes creates false collisions across otherwise independent containers.

**Repro / verification idea**

- Create a GitHub milestone-backed work item for repo A using milestone `1`.
- Create another GitHub milestone-backed work item for repo B using milestone `1`.
- Current dedupe logic treats the second work item as a duplicate even though it refers to a different repo.
- Repeat the same exercise for GitLab milestones or epics across different projects/groups.

**Fix prep**

- Likely fix files: `internal/service/work_item.go`, plus whichever adapters define the canonical milestone/epic source IDs.
- Either include container scope in `SourceItemIDs` for non-issue selections or extend dedupe to incorporate the relevant repo/project/group metadata.
- Add regression tests for cross-repo GitHub milestones and cross-group/project GitLab epics/milestones.

---

## Suggested fix order

1. **BR-001** and **BR-002** — implementation state consistency and phantom-session prevention.
2. **BR-003** — resume cleanup consistency.
3. **BR-004** — unblock operator-visible workflow takeovers.
4. **BR-005** — prevent wrong-provider automation attempts in mixed-host workspaces.
5. **BR-006** — correct false-positive dedupe for project/initiative sourcing.

## Notes for the fix pass

- Treat cancellation cleanup as a separate responsibility from request-scoped work. Cleanup needs its own context budget.
- For session lifecycle fixes, prefer one canonical invariant: no external harness session exists unless the session row is durably `running`.
- For mixed-provider routing, do a full cutover. A partial compatibility bridge that keeps broadcast fan-out alive will keep the bug alive.
- For source-item dedupe, choose one canonical identity model and migrate all adapters/tests to it together.