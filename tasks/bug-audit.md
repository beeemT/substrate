# Substrate Bug Audit

Date: 2026-03-10
Scope: startup/workspace wiring, adapter/harness selection, TUI reload flows, settings application, implementation/resume orchestration, and current targeted test health.
Status: investigation only; no fixes applied.

## Severity legend

- **Critical**: data loss, broken core workflow, or a failure mode likely to block normal use outright.
- **High**: a major workflow breaks or becomes inconsistent under realistic conditions.
- **Medium**: partial workflow breakage, stale state, or important edge-case failure.
- **Low**: weaker runtime impact, but still worth fixing for reliability or fix safety.

## Validation snapshot

Commands run during audit:

- `go test ./internal/app -run TestBuildRepoLifecycleAdapters_SkipsMixedWorkspacePlatforms -count=1` → passed
- `go test ./internal/app -run TestBuildAgentHarnesses_FallsBackWhenPrimaryMissing -count=1` → passed
- `go test ./internal/tui/views -run 'TestApp_WorkspaceInitDoneTriggersServiceReload|TestApp_WorkspaceServicesReloadedMsgAppliesReload' -count=1` → passed
- `go test ./internal/app ./internal/tui/views ./internal/orchestrator` → failed in `internal/orchestrator` because `mockSessionRepo` no longer implements `repository.SessionRepository` (missing `SearchHistory`)

That last failure is included below as a fix-prep blocker.

## Findings summary

| ID | Severity | Area | Short description |
|---|---|---|---|
| BA-001 | High | Event wiring | Plan approval and work-item completion hooks are effectively dead because runtime code never emits the events adapters subscribe to |
| BA-002 | High | Harness fallback | `ohmypi` is selected before its bridge/runtime dependencies are validated, so configured fallback harnesses never engage |
| BA-003 | High | Workspace recovery | A valid `.substrate-workspace` file without a matching DB row drops the user into an init flow that cannot recover |
| BA-004 | High | Implementation lifecycle | Implementation marks the work item `implementing` before repo/worktree preflight; early failures can leave it stuck in the wrong state |
| BA-005 | High | Resume lifecycle | Resume can start a real harness session and then return an error without aborting it if the DB transition to `running` fails |
| BA-006 | High | Settings apply | Applying broken settings persists the broken config before rebuild succeeds, leaving the app degraded even though Apply reports an error |
| BA-007 | Medium | Repo lifecycle routing | Mixed GitHub/GitLab workspaces get zero repo lifecycle adapters |
| BA-008 | Medium | Enterprise host support | GitHub Enterprise remotes are classified as `unknown`, so repo lifecycle automation never registers |
| BA-009 | Medium | TUI reload race | Delayed poll results from the old workspace can overwrite newly reloaded workspace state |
| BA-010 | Medium | Settings validation | Invalid scalar input in settings is silently coerced to `0`/`false` instead of being rejected |
| BA-011 | Low | Test health | `internal/orchestrator` tests do not compile because mocks drifted from `SessionRepository` |

---

## BA-001 — High — Plan approval and work-item completion hooks are never emitted

**Impact**

Substrate wires adapters to react to `plan.approved` and `work_item.completed`, but the runtime paths that approve a plan or complete a work item never publish those events. That means tracker state updates, plan comments, PR/MR readiness updates, and any downstream hook logic tied to those events simply never fire.

**Evidence**

- `internal/tui/views/cmds.go:127-140` — `ApprovePlanCmd` updates the plan and work item, then returns a TUI `PlanApprovedMsg`; it does not publish `domain.EventPlanApproved`.
- `internal/tui/views/cmds.go:531-538` — `OverrideAcceptCmd` completes the work item but does not publish `domain.EventWorkItemCompleted`.
- `internal/app/wire.go:197-215` and `internal/tui/views/settings_service.go:300-320` — adapters are subscribed to the event bus.
- `internal/adapter/linear/adapter.go:442-458` — expects `EventPlanApproved` / `EventWorkItemCompleted` to update tracker state.
- `internal/adapter/github/adapter.go:340-364` — expects `EventPlanApproved` / `EventWorkItemCompleted` for GitHub state/comment/PR flows.
- `internal/adapter/glab/adapter.go:73-84,99-156` — expects `EventWorkItemCompleted` to undraft MRs.
- A global search of runtime code found handlers/tests for these events, but no non-test publisher for them.

**Why this is a bug**

The system has wiring for these events on the consumer side but no producer on the main runtime path. This is a dead integration, not just missing polish.

**Fix prep**

- Likely fix files: `internal/tui/views/cmds.go`, possibly service/orchestrator layer if event emission should be centralized there.
- Prefer one canonical place to publish domain events after state transitions succeed.
- Add regression coverage that proves:
  - approving a plan publishes `plan.approved`
  - accepting/completing a work item publishes `work_item.completed`
  - subscribed adapters receive those events

---

## BA-002 — High — `ohmypi` fallback chain never activates when bridge/runtime dependencies are missing

**Impact**

If `ohmypi` is selected and its bridge or Bun runtime is missing, every phase can fail at session start even when Claude Code or Codex are configured as fallbacks.

**Evidence**

- `internal/app/harness.go:48-61` — fallback selection only advances when `instantiateHarness` returns an error.
- `internal/app/harness.go:64-73` — the `ohmypi` branch only validates the optional `bun_path` override, then immediately returns `omp.NewHarness(...)`.
- `internal/adapter/ohmypi/harness.go:49-145` — real dependency checks (`resolveBridgeRuntime`, `resolveBunExecutable`, `ensureBridgeDependencies`) happen later in `StartSession`, after the harness has already been chosen.

**Why this is a bug**

Fallbacks only work when unusable harnesses fail during selection. `ohmypi` currently fails too late, so the fallback chain is bypassed.

**Fix prep**

- Likely fix files: `internal/app/harness.go`, `internal/adapter/ohmypi/harness.go`.
- Move the same readiness checks used by `StartSession` into harness selection, or add a harness readiness probe used during `BuildAgentHarnesses`.
- Regression tests should cover:
  - missing compiled bridge
  - source checkout without Bun
  - fallback to Claude/Codex when `ohmypi` is not runnable

---

## BA-003 — High — Existing workspace marker without DB row is not recoverable from the TUI

**Impact**

If `state.db` is reset/corrupted but `.substrate-workspace` still exists on disk, startup drops the user into workspace init even though the workspace already exists. Confirming init then fails with `workspace already exists`, leaving the app unable to proceed from the TUI. In the same startup path, `workspaceDir` also stays empty, so repo lifecycle adapters are never registered even though the filesystem still identifies the workspace root.

**Evidence**

- `cmd/substrate/main.go:155-167` — when `gitwork.FindWorkspace` succeeds but `workspaceSvc.Get` fails, startup logs a warning and leaves `workspaceID/workspaceDir` empty so the app behaves as if no workspace exists.
- `cmd/substrate/main.go:196` — `app.BuildRepoLifecycleAdapters(ctx, cfg, workspaceDir)` is then called with the empty `workspaceDir`, dropping repo lifecycle wiring for that run.
- `internal/tui/views/overlay_workspace_init.go:80-100` — confirming init calls `gitwork.InitWorkspace(cwd, name)`.
- `internal/gitwork/workspace.go:101-107` and `103-105` — `InitWorkspace` returns `ErrWorkspaceExists` when `.substrate-workspace` is already present.

**Why this is a bug**

Filesystem state and DB state can legitimately drift. The current recovery path treats an existing workspace as a brand-new one, dead-ends on the marker file, and also disables repo lifecycle automation by forgetting the known workspace path.

**Fix prep**

- Likely fix files: `cmd/substrate/main.go`, `internal/tui/views/overlay_workspace_init.go`, possibly `internal/service/workspace.go` / a new recovery helper.
- Desired behavior is probably “re-register existing workspace” rather than “initialize new workspace”, while preserving the discovered filesystem workspace root for downstream wiring even before DB recovery completes.
- Regression test should cover: marker file exists, DB row missing, app offers recovery, repo lifecycle wiring still receives the filesystem workspace path, and registration can be restored without re-running full init.

---

## BA-004 — High — Implementation can leave a work item stuck in `implementing` before any real work starts

**Impact**

Repo discovery or worktree preparation failures can happen after the work item is transitioned to `implementing`, leaving the persisted state inconsistent even though no implementation session successfully started.

**Evidence**

- `internal/orchestrator/implementation.go:150-153` — work item is transitioned to `domain.WorkItemImplementing` early.
- `internal/orchestrator/implementation.go:175-186` — `discoverRepoPaths` and `prepareWorktrees` run afterward and can still return fatal errors.
- Those fatal returns happen before the final success/failure state rewrite at `231-240`.

**Why this is a bug**

A failed preflight should not persist the same state as a live implementation run. This creates misleading UI state and can block retries or operator diagnosis.

**Fix prep**

- Likely fix files: `internal/orchestrator/implementation.go`.
- Either preflight before changing work-item state, or guarantee rollback/fail-state transition on any later fatal error.
- Add regression coverage for:
  - repo discovery failure
  - missing repository in workspace
  - worktree creation failure
  - final persisted work-item state after each failure

---

## BA-005 — High — Resume can orphan a live harness session if DB transition to `running` fails

**Impact**

A resumed agent session can be started in the harness, then the method can return an error before the DB row is updated to `running`. That leaves a real agent process executing without corresponding durable state.

**Evidence**

- `internal/orchestrator/resume.go:112-117` — `r.harness.StartSession(...)` is called and may succeed.
- `internal/orchestrator/resume.go:119-122` — if `r.sessionSvc.Start(...)` then fails, `ResumeSession` returns the error immediately.
- There is no cleanup/abort path for the already-started `harnessSession` in that failure branch.

**Why this is a bug**

Once the external process is started, failing to record its running state must trigger cleanup. Returning early leaves process state and DB state out of sync.

**Fix prep**

- Likely fix files: `internal/orchestrator/resume.go`.
- On post-start persistence failure, abort the harness session and mark the new session failed if possible.
- Add regression tests for “harness starts, `sessionSvc.Start` fails” and verify no live session remains running.

---

## BA-006 — High — Settings Apply persists bad config before rebuild succeeds

**Impact**

The settings UI can save a broken config to disk, stop the current Foreman, and then fail while rebuilding services. The user sees an apply error, but the bad config is already committed and the running system is partially degraded.

**Evidence**

- `internal/tui/views/settings_service.go:173-176` — `Apply` writes the raw config immediately via `SaveRaw`.
- `internal/tui/views/settings_service.go:181-191` — it only reloads/rebuilds services after writing.
- `internal/tui/views/settings_service.go:188-190` — the current Foreman is stopped before rebuild completes.
- `internal/app/harness.go:22-45` and `48-93` — rebuild can fail on missing harness binaries.

**Why this is a bug**

Apply should be atomic from the user’s perspective: either the new config is valid and the process is reloaded, or the old config remains in effect. The current flow does neither.

**Fix prep**

- Likely fix files: `internal/tui/views/settings_service.go`.
- Validate and build a full replacement service graph before writing the config and tearing down the old runtime.
- Add regression coverage for “apply invalid harness path” and assert the on-disk config and live services remain unchanged.

---

## BA-007 — Medium — Mixed GitHub/GitLab workspaces disable all repo lifecycle adapters

**Impact**

A workspace containing both GitHub and GitLab repositories gets zero repo lifecycle adapters, so PR/MR automation never runs for either side.

**Evidence**

- `internal/app/wire.go:73-90` — `BuildRepoLifecycleAdapters` chooses a single platform for the entire workspace.
- `internal/app/wire.go:115-127` — `detectWorkspaceLifecyclePlatform` returns an error for mixed platforms.
- `internal/app/wire_test.go:75-85` — current test suite explicitly expects mixed-platform workspaces to produce zero adapters.
- Confirmed by command: `go test ./internal/app -run TestBuildRepoLifecycleAdapters_SkipsMixedWorkspacePlatforms -count=1`.

**Why this is a bug**

Substrate is positioned as a multi-repo orchestrator. A mixed-platform workspace is a realistic multi-repo setup, and dropping all lifecycle automation is a poor failure mode.

**Fix prep**

- Likely fix files: `internal/app/wire.go`, possibly adapter registration shape.
- Prefer per-repo lifecycle routing over a single workspace-wide platform.
- Add regression coverage with one GitHub repo + one GitLab repo and verify both lifecycle adapters receive relevant events for their repos only.

---

## BA-008 — Medium — GitHub Enterprise remotes are treated as `unknown`

**Impact**

Repo lifecycle automation never registers for GitHub Enterprise or other non-`github.com` GitHub hosts.

**Evidence**

- `internal/app/remotedetect/remotedetect.go:50-67` — only `github.com`, `gitlab.com`, and GitLab hosts from `glab` config are recognized.
- There is no config path for declaring GitHub hosts analogous to the GitLab host discovery path.

**Why this is a bug**

Enterprise/self-hosted GitHub is a common deployment shape. Treating every non-`github.com` host as unsupported blocks lifecycle automation unnecessarily.

**Fix prep**

- Likely fix files: `internal/app/remotedetect/remotedetect.go`, `internal/config/config.go`.
- Add configurable known GitHub hosts or infer GitHub API shape from remote patterns.
- Regression coverage should include a GitHub Enterprise SSH/HTTPS remote.

---

## BA-009 — Medium — Delayed poll results can overwrite freshly reloaded workspace state

**Impact**

After services reload into a new workspace, delayed `LoadWorkItemsCmd` / `LoadSessionsCmd` results from the old workspace can still arrive and overwrite the current in-memory state.

**Evidence**

- `internal/tui/views/cmds.go:48-68` — load commands capture `workspaceID` when invoked.
- `internal/tui/views/msgs.go:12-16` — `WorkItemsLoadedMsg` and `SessionsLoadedMsg` contain no workspace ID or request token.
- `internal/tui/views/app.go:507-577` — reload schedules new loads, but handlers blindly assign `a.workItems = msg.Items` and `a.sessions = msg.Sessions`.
- Existing happy-path reload tests pass (`go test ./internal/tui/views -run 'TestApp_WorkspaceInitDoneTriggersServiceReload|TestApp_WorkspaceServicesReloadedMsgAppliesReload' -count=1`) but they do not cover delayed old responses.

**Why this is a bug**

The TUI has asynchronous polling and can legitimately receive late responses. Without workspace/request identity, stale data can clobber the new workspace view.

**Fix prep**

- Likely fix files: `internal/tui/views/msgs.go`, `internal/tui/views/cmds.go`, `internal/tui/views/app.go`.
- Add workspace ID or request generation to load messages and ignore stale results.
- Regression test should simulate an old workspace response arriving after `WorkspaceServicesReloadedMsg`.

---

## BA-010 — Medium — Invalid settings input is silently coerced to `0` or `false`

**Impact**

Malformed numeric/bool input in the settings UI can be accepted and persisted as a different value than the user entered, without any validation error.

**Evidence**

- `internal/tui/views/settings_service.go:612-625` — many fields are parsed through `parseInt`, `parseFloat`, `parseBool`.
- `internal/tui/views/settings_service.go:870-887` — parse helpers ignore conversion errors and return zero values.
- `internal/tui/views/settings_service.go:670-700` — later validation re-encodes the already-coerced config, so the original invalid input is lost.

**Examples**

- invalid bool like `tru` becomes `false`
- invalid float like `abc` becomes `0`
- invalid optional int like `abc` becomes `0`, which may still pass validation for some fields

**Why this is a bug**

Settings validation should reject invalid user input, not silently reinterpret it.

**Fix prep**

- Likely fix files: `internal/tui/views/settings_service.go`.
- Replace lossy parse helpers with parsers that return `(value, error)` and preserve field-level validation errors.
- Add regression coverage for malformed int/bool/float values and assert Apply/Save fails with a precise error.

---

## BA-011 — Low — `internal/orchestrator` tests are currently broken by interface drift

**Impact**

This does not directly break runtime behavior, but it materially reduces confidence for any follow-up work in the orchestration layer.

**Evidence**

Observed output from `go test ./internal/app ./internal/tui/views ./internal/orchestrator`:

- `internal/orchestrator/instance_manager_test.go:173:43: cannot use sessionRepo ... missing method SearchHistory`
- same interface mismatch repeats in `instance_manager_test.go` and `phase9_test.go`

**Why this is a bug**

The test harness no longer matches the repository contract, so orchestration changes cannot be safely verified in-package.

**Fix prep**

- Likely fix files: `internal/orchestrator/instance_manager_test.go`, `internal/orchestrator/phase9_test.go`, shared mocks.
- Update `mockSessionRepo` to implement `SearchHistory` consistently with the current `repository.SessionRepository` interface.

---

## Suggested fix order

1. **BA-001** — dead event wiring for plan approval/completion
2. **BA-002** — broken harness fallback when `ohmypi` is not runnable
3. **BA-003** — workspace marker / DB mismatch recovery
4. **BA-004** and **BA-005** — implementation/resume state consistency
5. **BA-006** and **BA-010** — settings safety and validation correctness
6. **BA-009** — stale TUI reload race
7. **BA-007** and **BA-008** — repo lifecycle routing breadth
8. **BA-011** — restore orchestration test safety before refactoring that layer heavily

## Notes for follow-up fix pass

- Prefer fixing lifecycle/event issues at the domain/service/orchestrator boundary rather than only in the TUI command layer if a more canonical emitter location exists.
- Do not paper over inconsistent state with UI-only guards. Several issues above are source-of-truth problems, not presentation problems.
- Before changing event payloads, inventory all adapter consumers so the fix is a full cutover rather than a partial compatibility bridge.
