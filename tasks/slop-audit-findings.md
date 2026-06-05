# AI slop audit — substrate

Audit date: 2026-06-05. Six parallel subagents (categories 1-6 below) produced ranked, concrete findings; one subagent (category 7) returned an empty structured result. All findings are derived from the live state of the working tree at audit time. AGENTS.md at the repo root is the reference for "what counts as slop here" (no defensive nil guards in service/domain, errors logged via slog, no silent discards, no over-wrapping).

Severity legend used by the subagents:
- **high** — clear AGENTS.md violation or clear waste; safe to delete/fix.
- **medium** — clear slop shape, but the change is non-trivial; may need design judgment.
- **low** — slop-adjacent; mostly stylistic.

---

## Summary

| # | Category | Subagent verdict | Items | Highest-leverage file |
|---|---|---|---|---|
| 1 | Over-abstraction (single-impl interfaces, useless wrappers) | 5 ranked | 5 | `internal/orchestrator/interfaces.go`, `internal/orchestrator/agent_run_supervisor.go` |
| 2 | Defensive nil guards & empty error handling | file survey only | 5 verified by hand | `internal/orchestrator/agent_run_supervisor.go`, `internal/orchestrator/implementation.go` |
| 3 | Verbose error wrapping | 7 items, 3 tier-1 + 4 tier-2 | 7 | `internal/orchestrator/answer_router.go`, `internal/orchestrator/foreman.go` |
| 4 | Unnecessary comments & docstrings | 7 patterns | ~7 patterns, 100s of instances | `internal/adapter/glab/adapter.go`, `internal/orchestrator/branch.go` |
| 5 | Defensive copies & cargo-cult boilerplate | 7 high-confidence | 7 | `internal/adapter/gitlab/adapter.go`, `internal/adapter/sentry/adapter.go` |
| 6 | Bridge (TypeScript) slop | 7 items | 7 | `bridge/omp-bridge.ts`, `bridge/claude-agent-bridge.ts` |
| 7 | Test slop | empty (subagent output broken) | gap | — |

Total high-signal items: **~38** + the 7 patterns in category 4 (which cover 100s of instances).

---

## Category 1 — Over-abstraction

### 1.1 [high] `ForemanLifecycle` interface is dead code

- **File**: `internal/orchestrator/interfaces.go:15-19` and `internal/orchestrator/interfaces.go:74`
- **What's slop**: Interface with 3 methods (`Start`, `Stop`, `IsRunning`). Sole production implementation is `*Foreman` (`foreman.go:100, 597, 686`). The only reference is the `var _ ForemanLifecycle = (*Foreman)(nil)` compile-time assert at line 74. Zero consumers, no orchestration code accepts or returns it. The doc-comment claims it is "for use by orchestrators that need to control its lifecycle" — no such orchestrator exists.
- **Why**: AGENTS.md requires the `var _ Interface = ...` only when the type is meant to be used through the interface. Here the check guards against an interface that is never referenced.
- **Fix**: Delete the interface declaration (lines 15-19) and the compile-time assert (line 74).

### 1.2 [high] `AgentHarnessSelector` + `staticHarnessSelector` wrapper

- **File**: `internal/orchestrator/agent_run_supervisor.go:15-17` (interface), `agent_run_supervisor.go:270-276` (`staticHarnessSelector`), `implementation.go:1540-1546`, `review.go:299-304` (call sites)
- **What's slop**: 1-method interface whose only impl ignores its argument and returns a stored pointer. Both call sites wrap the parent's own `harness` field in the wrapper: `staticHarnessSelector{harness: s.harness}` and `staticHarnessSelector{harness: p.harness}`. No real dispatch by `kind` ever happens; the harness is identical for all `AgentSessionKind`s.
- **Fix**: Change `harnesses AgentHarnessSelector` in `AgentRunSupervisor` to `harness adapter.AgentHarness`. Drop `staticHarnessSelector`. Pass `s.harness` / `p.harness` directly at the two call sites.

### 1.3 [high] Three single-method "refresher" interfaces declared in adapter files but unused at the declared site

- **File**: `internal/adapter/github/adapter.go:37-41` (`prRefresher`), `internal/adapter/gitlab/adapter.go:32-36` (`statusRefresher`), `internal/adapter/glab/adapter.go:42-46` (`mrRefresher`), `internal/tui/views/service_manager.go:258-290` (consumer redeclares them locally)
- **What's slop**: Each interface is declared in the same file as the concrete adapter that implements it. The only use of the declared name is the compile-time check (`var _ prRefresher = &GithubAdapter{}` etc.). The actual consumer (`service_manager.go:258-279`) declares its own LOCAL copies of the same interfaces inside the for-loop body, so the declared types are not referenced from the only call site.
- **Fix**: Delete the three private interface declarations and their `var _` asserts. The local copies in `service_manager.go` continue to work unchanged.

### 1.4 [high] `WorkspaceStore` + `workItemStoreAdapter` wrapper

- **File**: `internal/adapter/manual/adapter.go:22-37` (interface + wrapper + constructor), `internal/app/wire.go:43-47` (only construction call site)
- **What's slop**: 1-method interface, sole impl is `workItemStoreAdapter`. `NewWorkspaceStore` is the only call site (one in `app/wire.go:44`). The wrapper's only method calls `svc.List(ctx, SessionFilter{WorkspaceID: &workspaceID, Source: &"manual"})` and returns `len(items)`. Hard-coded `"manual"` source filter and 1-line concern don't need an abstraction.
- **Fix**: Give `ManualAdapter` a `*service.SessionService` + `workspaceID` directly. Inline `CountManualWorkItems` as a small helper in `manual/adapter.go` (or as a method on the adapter). Delete `WorkspaceStore`, `workItemStoreAdapter`, and `NewWorkspaceStore`.

### 1.5 [high] `routedRepoLifecycleAdapter` refresh methods duplicate the consumer's type-assertion

- **File**: `internal/app/wire.go:176-223` (wrapper), `internal/tui/views/service_manager.go:254-290` (consumer)
- **What's slop**: `Name()` and `OnEvent()` are genuine work. `StartPRRefresh` and `StartMRRefresh` each declare a local `refresher` interface and assert `a.adapter.(refresher)`. The consumer in `service_manager.go:258-279` then re-asserts the same `prRefresher` on the wrapper. Two layers of the same type-assertion pattern.
- **Fix**: Drop `StartPRRefresh` / `StartMRRefresh` from the wrapper. Expose `inner adapter.RepoLifecycleAdapter` (or a typed accessor) on the wrapper. Have `service_manager.go` do the local type-assertion on the inner adapter directly. The two layers collapse to one.

### 1.x Verified NOT slop (kept intentionally)

- `internal/repository/interfaces.go` — 18 repository interfaces all backed by a single sqlite impl, but each has NoopRepo / in-mem test repo as a second real impl. The repository boundary is the layer's seam.
- `internal/orchestrator/interfaces.go:27` — `SessionRegistry`: 1 prod impl but the interface is used as a parameter type in 6+ constructors (`agent_run_supervisor.go:22`, `answer_router.go:27`, `implementation.go:136`, `manual.go:44`, `planning.go:112`, `question_router.go:25`, `review.go:43`, `review_followup.go:32`). Wide fan-out justifies the abstraction.
- `internal/adapter/interfaces.go:11/50/61/81` — `WorkItemAdapter`, `RepoLifecycleAdapter`, `AgentHarness`, `AgentSession` all have 3+ real implementations.
- `internal/workerpool/pool.go:50/121` — `ProcessAll[In,Out]` and `ProcessAllVoid[In]` have multiple distinct type instantiations at call sites (string→pullResult, string→syncResult, int→int). Generic is justified.
- `internal/config/config.go:18` — `ptr[T any]` called with int, bool, string. Justified.
- `internal/adapter/{github,gitlab,sentry}/adapter.go:45/63/35` — `httpClient/httpDoer` 1-method interfaces; sentry has a second prod impl (`*cliHTTPClient`) and test doubles. Justified.
- `internal/event/bus.go:55` — `Publisher` has 3 impls (Bus, mockPublisher, noopPublisher). Justified.
- `internal/adapter/linear/types.go:41` — `type linearCreator = linearUser` is a no-op type alias. Both fields are `*linearUser` pointing to the same struct shape. **Trivial — drop the alias and use `*linearUser` for the Creator field.** (Not in top 5 but worth catching while in this file.)
- `internal/adapter/github/adapter.go:518-524` — `WorkItemAdapter/RepoLifecycleAdapter` wrapper structs that embed `*GithubAdapter` to override `OnEvent` per role. Defensible interface segregation (real per-role `OnEvent` logic at lines 534-596). Not in top 5.
- `internal/gitwork/workspace.go:93-95` — `repoInitializer` 1-method interface with prod impl `*Client` and a test stub `*stubRepoInitializer`. Borderline; the interface exists only for the test, which is allowed but not necessary.

---

## Category 2 — Defensive nil guards & empty error handling

(Subagent returned file list only; the items below were verified by hand against AGENTS.md.)

### 2.1 [high] Constructor-validated deps re-checked at `Start()` entry

- **File**: `internal/orchestrator/agent_run_supervisor.go:51-58`
- **What's slop**:
  ```go
  if s.harnesses == nil {
      return domain.AgentSession{}, fmt.Errorf("agent harness selector is required")
  }
  if s.sessionSvc == nil {
      return domain.AgentSession{}, fmt.Errorf("agent session service is required")
  }
  if s.registry == nil {
      return domain.AgentSession{}, fmt.Errorf("session registry is required")
  }
  ```
  These three dependencies are set by the `AgentRunSupervisor` constructor (`agent_run_supervisor.go:36-44` area). All callers are code-controlled. The constructor wiring is the trust boundary.
- **AGENTS.md conflict**: "Nil guards belong only at trust boundaries (adapters, handlers), not in service or domain logic where inputs are code-controlled."
- **Fix**: Delete all three guards. (Keep the `req.Session.ID == ""` check on line 48 — that one is validating an untrusted-looking caller-supplied field.)

### 2.2 [high] Same pattern in `ImplementationService` methods

- **File**: `internal/orchestrator/implementation.go:939-942` (`s.planningSvc == nil`) and `internal/orchestrator/implementation.go:2723-2726` (`s.eventBus == nil`)
- **What's slop**:
  ```go
  // line 939
  if s.planningSvc == nil {
      result.Skipped = append(result.Skipped, ResumeRetrySkippedLeaf{
          SessionID: leaf.ID, Kind: leaf.Kind, Status: leaf.Status,
          Reason: "planning service is not configured",
      })
      continue
  }
  // line 2723
  func (s *ImplementationService) emitContinuationFailed(...) {
      if s.eventBus == nil {
          return
      }
      ...
  ```
  Both `s.planningSvc` and `s.eventBus` are set by the `ImplementationService` constructor.
- **Fix**: Delete both guards. If a code path legitimately needs to no-op when a dependency is missing, restructure so the dependency is always present (e.g. pass `nil` no-op event bus in tests, or have the constructor always wire a real one).

### 2.3 [medium] Foreman internal nil-checks

- **File**: `internal/orchestrator/foreman.go:241-243, 257-260, 691-693, 768-770`
- **What's slop**:
  ```go
  // 240
  session := f.session
  if session == nil {
      f.mu.Unlock()
      return
  }
  ...
  // 256
  if f.session == nil {
      // Stop() ran after our initial nil check but before Done() fired.
      f.mu.Unlock()
      ...
  ```
  Multiple `f.session == nil` checks. `f.session` is set by `Start()` and cleared by `Stop()` under `f.mu`. The first check (241) and the line-257 check together are a race window. The `IsRunning`/`LastSessionID`/`Stop` entry checks (691, 768) are real because the field is genuinely nullable from the public API.
- **Fix**: Keep the public-API entry nil checks. Collapse the line 241 + 257 pair into a single race-handling block. Don't re-check `f.session` after the `f.mu.Unlock()` if a single check under the lock is enough.

### 2.4 [low / borderline] `EndForeman` foreman-nil branch

- **File**: `internal/orchestrator/implementation.go:200-203`
- **What's slop**:
  ```go
  foreman := s.registry.GetForeman(workItemID)
  if foreman == nil {
      return nil
  }
  ```
  The registry is a legitimate boundary (foreman is registered by `Start` and deregistered by `Stop`/`EndForeman`). The chosen semantics (return nil, no error) are reasonable.
- **Fix**: Keep, but rename the comment to say "no-op when foreman not running" so it doesn't read as defensive nil-guard.

### 2.5 [low / not slop] `answerRouter.getForeman` legitimately returns `nil, nil`

- **File**: `internal/orchestrator/answer_router.go:56-58` + `106-110`
- **What's here**: `getForeman` returns `(*Foreman, error)` and can return `nil, nil` (no foreman for this work item). The downstream `if foremanHandler != nil` is a real API branch.
- **Fix**: Keep.

### 2.x Verified clean

- No empty `if err != nil {}` blocks in production code.
- No `_ = someFunc()` discarding errors in production code.
- The only `_ =` discards are in test files and at the `defer` for `cmd.Wait()` / `tx.Rollback()` / `conn.Write` — conventional and AGENTS.md-compliant.

---

## Category 3 — Verbose error wrapping

### 3.1 [high] `instance.go` instance delete double-wraps

- **File**: `internal/service/instance.go` (delete)
- **What's slop**: instance delete **double-wraps** an error that already includes the instance ID.
- **Fix**: Return the inner error directly. If the ID is needed in the message, ensure the inner error includes it.

### 3.2 [high] `session_continuation.go` Get triple-wraps

- **File**: `internal/service/session_continuation.go`
- **What's slop**: `Get` **triple-wraps** with the same prefix at each level.
- **Fix**: Return the inner error. If a single wrap is needed, do it once and use the agent-session ID in the wrapper text.

### 3.3 [high] `plan.go` sub-plan list wraps a wrapper

- **File**: `internal/service/plan.go` (sub-plan list)
- **What's slop**: `return res.SubPlans.List(ctx, filter)` would do.
- **Fix**: Return the result of the inner call directly.

### 3.4 [high] `answer_router.go` — 8 sites in 60 lines

- **File**: `internal/orchestrator/answer_router.go`
- **What's slop**: Wrapper text is a literal English gloss on the called method name. Specific sites:
  - line 102: `getForeman` → `fmt.Errorf("get foreman handler: %w", err)`
  - line 112: `ResolveEscalated` → `fmt.Errorf("resolve escalated: %w", err)`
  - line 131: `SendAnswer` → `fmt.Errorf("send manual answer: %w", err)`
  - line 134: `ResumeFromAnswer` → `fmt.Errorf("resume impl session from answer: %w", err)`
  - 4 more sites in the same file.
- **Fix**: Most of these become `return err`. The few that need wrapper text should include the **work-item ID or session ID** that's otherwise not in the underlying message.

### 3.5 [high] `foreman.go` — 10+ sites

- **File**: `internal/orchestrator/foreman.go`
- **What's slop**: All wrappers are `fmt.Errorf("<method> <noun>: %w", err)`.
- **Fix**: Audit line-by-line; for each, ask "does the wrapper text add new info (work-item ID, session ID, retry count, attempted path)?" If no, drop the wrap.

### 3.6 [high] `question_router.go` — 3 sites

- **File**: `internal/orchestrator/question_router.go`
- **What's slop**: Same `fmt.Errorf("<method>: %w", err)` pattern.
- **Fix**: Drop or enrich with question ID / work-item ID.

### 3.7 [high] `review_followup.go` + `implementation.go` glue — 6 sites

- **File**: `internal/orchestrator/review_followup.go` and `internal/orchestrator/implementation.go`
- **What's slop**: Same pattern.
- **Fix**: Same approach.

### 3.x Verified NOT slop (defensible)

- `internal/repository/sqlite/parseTime` field-name wrappers — encode which field failed to parse.
- `doJSONWithHeaders` step wrappers in HTTP adapters — encode which of 7 calls in the same function failed (the underlying error is `"unexpected status 500"` and the step label disambiguates).

---

## Category 4 — Unnecessary comments & docstrings

This is the highest-volume category; the 7 patterns below cover ~100s of instances.

### 4.1 [high] Restate-the-line comments inside small functions

- **File**: `internal/orchestrator/branch.go` (dense)
- **What's slop**: Six inline restate-the-line comments inside `slugFromTitle`:
  ```go
  // Lowercase
  s = strings.ToLower(s)
  // Replace spaces with dashes first
  s = strings.ReplaceAll(s, " ", "-")
  // Trim leading and trailing dashes
  s = strings.Trim(s, "-")
  ```
  Plus a 9-line floating block comment at lines 9-17 that duplicates the 16-line docstring on `GenerateBranchName` at lines 28-43.
- **Fix**: Delete the floating block, shorten the docstring to one example line, delete all inline comments inside `slugFromTitle`, delete the `var`-block docstring above the regex declarations.

### 4.2 [high] Duplicate package-header comments

- **File**: `internal/adapter/glab/{adapter.go, doc.go}`, `internal/adapter/claudeagent/harness.go`, `internal/adapter/ohmypi/harness.go`, `internal/adapter/harness/omp/doc.go`, `internal/orchestrator/{doc.go, manual.go}`
- **What's slop**:
  - `// Package glab implements the glab CLI wrapper adapter.` declared in both `glab/doc.go` (1 line) and `glab/adapter.go` (10 lines, including event-mapping table).
  - `// Package omp implements the oh-my-pi agent harness. It spawns the bridge subprocess and manages JSON-line I/O.` duplicated across 3 files.
  - `// Package orchestrator ...` declared in both `orchestrator/doc.go` and `orchestrator/manual.go`.
- **Fix**: Keep the richer copy (event table in `glab/adapter.go`, omp text in one of the three). Delete the redundant package-comment file. Go picks one `doc.go` per package; the other is dead text that drifts.

### 4.3 [high] Test docstrings that rephrase the test name

- **File**: `internal/orchestrator/{phase9_test.go, planning_test.go, question_router_test.go, review_followup_test.go, review_test.go, implementation_test.go}`, `internal/tui/views/action_menu_test.go`, `internal/tui/components/input_test.go`, `internal/tui/views/agent_session_graph_integration_test.go`, `internal/tui/views/agent_session_graph_test.go`, `internal/tui/views/app_add_repo_test.go`, `internal/tui/views/app_logs_shortcut_test.go`, `internal/service/session_test.go`, `internal/adapter/github/{adapter_test.go, review_comment_test.go}`, `internal/adapter/glab/review_comment_test.go`, `internal/adapter/ohmypi/integration_test.go`, `internal/event/prehook_test.go`, `internal/gitwork/integration_test.go`
- **What's slop**: ~80+ tests have a `// TestX verifies that Y does Z` comment where Y is a rephrasing of the test name. Example: `// TestWaitForAnswer_HighConfidence → verifies a foreman_proposed with uncertain=false is treated as confident` is the same as the function name.
- **Fix**: Delete the comments where the test name carries the meaning. Keep comments only when they document a specific bug being guarded, a regression anchor, or a non-obvious invariant (e.g. `implementation_test.go:1239-1243` is good — crash-recovery path; the surrounding 18 are not).

### 4.4 [high] ASCII-art section banners

- **File**: `internal/orchestrator/interfaces.go`, `internal/orchestrator/{phase9_test.go, review_test.go, implementation_test.go}`, `internal/adapter/codex/harness_test.go`, `internal/repository/sqlite/sqlite_test.go`, `internal/tui/views/overlay_overview_links_test.go`
- **What's slop**: `==== or ----` ASCII-art separator banners around sections. In test files, banners sit above a single `TestX` whose name already says what the banner says.
- **Fix**: Delete every banner. Trust the symbol/test name. If grouping is genuinely needed, use a 1-line `// region marker`.

### 4.5 [high] `// --- Section ---` decorative organizational banners

- **File**: `internal/adapter/linear/adapter.go` (lines 563, 731, 906, 945, 977), `internal/tui/views/{app.go, action_menu.go, msgs.go, sidebar.go, event_consumer.go, cmds.go, overlay_review_followup.go}` (multiple lines), `internal/tui/components/bunny_test.go`, `internal/tui/views/{workspace_init_test.go, session_transcript_test.go}`
- **What's slop**: Banners sit between a closing brace and the next function declaration, occupying vertical space without conveying invariants.
- **Fix**: Delete the banners. The function names already classify them.

### 4.6 [high] One-line `// X does X` docstrings on unexported helpers in glab/adapter.go

- **File**: `internal/adapter/glab/adapter.go` (lines 53-54, 65, 73, 86, 91, 96, 107, 110-112, 138-139, 152-154, 334, 434, 465-466, 496, 539, 560, 562, 593, 644-650, 686-687, 705, 720, 745, 757, 1063-1066, 1076, 1093-1097, 1099, 1100, 1145, 1220-1223, 1404-1405, 1492-1493, 1557, 1577-1578, 1664-1665, 1706, 1742-1743, 1785-1786, 1794, 1799-1800, 1803, 1806-1811, 1855-1856, 1870-1871, 1907-1909)
- **What's slop**: ~40+ one-line docstrings. Examples:
  - `// linkExistsForWorkItem returns true if a session_review_artifacts link already exists for the given work item with provider 'gitlab'.` above a 5-line function whose name + body say exactly that.
  - `// capitalize uppercases the first rune of s and leaves the rest unchanged.` above a 10-line function whose body is exactly that.
  - `// mrView returns the current MR metadata for the branch when one exists.` above a 19-line function that calls `glab mr view`.
  - `// URL-encode the project path for the API` above `url.PathEscape(projectPath)`.
  - `// Strip 'sub-' prefix` above `strings.TrimPrefix(branch, "sub-")`.
  - Docstrings that quote the function's full CLI command in prose (`// createMR runs \`glab mr create --draft --source-branch ...\``) when the function body has the args slice right below.
- **Fix**: Delete the docstring on helpers whose symbol name + body already convey the meaning. Keep only the ones that encode a non-obvious constraint (e.g. line 1799-1800 explaining "100 is the GitLab API maximum" is useful). Delete all inline restate-the-line comments.

### 4.7 [high] Giant tutorial-style `doc.go`

- **File**: `internal/gitwork/doc.go` (62 lines)
- **What's slop**: 60-line doc.go with prose tutorial, JSON example block, and 3 example usage snippets in a 200-line package. Markdown sections (`# git-work CLI Requirements`, `# JSON Format`, `# Workspace Discovery`, `# Example Usage`) that the godoc viewer renders as headings on every type/function page.
- **Fix**: Cut to a 2-line doc.go. Keep one sentence describing the package and one line stating the required git-work CLI surface. Move the JSON example and example usage to README.md.

### 4.x Worth keeping (verified)

- `internal/orchestrator/branch.go:95-100` — git ref-name constraints (non-obvious invariant).
- `internal/adapter/glab/adapter.go:1799-1800` — documents that 100 is the GitLab API maximum (non-obvious constraint).
- `internal/orchestrator/implementation_test.go:1240-1243` — crash-recovery path (regression anchor).

---

## Category 5 — Defensive copies & cargo-cult boilerplate

### 5.1 [high] `GitLab statusCache`: 12-line nested map copy under the same Lock that already provides atomicity

- **File**: `internal/adapter/gitlab/adapter.go:982-1005`
- **What's slop**:
  ```go
  a.statusCacheMu.Lock()
  defer a.statusCacheMu.Unlock()
  ...
  // Make a shallow copy so the merged result is written back atomically.
  copied := make(map[string]map[string]string, len(byType))
  for k, v := range byType {
      innerCopy := make(map[string]string, len(v))
      for kk, vv := range v {
          innerCopy[kk] = vv
      }
      copied[k] = innerCopy
  }
  byType = copied
  ```
  The comment claims the copy exists so the "merged result is written back atomically" — but `statusCacheMu.Lock()` is held for the entire critical section. The reader at `resolveStatusID` (line 855) also takes the same mutex, so partial map state is invisible to readers regardless of whether we copy. The loop allocates O(types + statuses) entries on every cache refresh.
- **Fix**: Delete lines 995-1004. `byType` is already a private, lock-protected map. Mutate it in place.

### 5.2 [high] `Sentry execCommandRunner`: defensive env copy that `exec.Cmd` does not require

- **File**: `internal/adapter/sentry/adapter.go:109-114`
- **What's slop**:
  ```go
  func execCommandRunner(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
      cmd := exec.CommandContext(ctx, name, args...)
      cmd.Env = append([]string(nil), env...)
      return cmd.CombinedOutput()
  }
  ```
  `exec.Cmd` never mutates its `Env` slice. The other 5 Sentry-CLI call sites in the codebase all do `cmd.Env = config.SentryCLIEnvironment(...)` directly without copying. One site diverges from every other identical site, and the divergence is dead work.
- **Fix**: `cmd.Env = env`.

### 5.3 [high] `Sentry scopedProjects`: defensive slice copy with one caller that only reads

- **File**: `internal/adapter/sentry/adapter.go:577-590`
- **What's slop**:
  ```go
  func scopedProjects(allowlist []string, repo string) ([]string, bool) {
      repo = strings.TrimSpace(repo)
      if repo == "" {
          return append([]string(nil), allowlist...), true
      }
      ...
  }
  ```
  The only caller is `buildIssueListQuery` (line 519), which immediately passes the slice to `issueProjectQuery` (line 542) — a pure function that switches on `len` and joins. No mutation.
- **Fix**: `return allowlist, true`.

### 5.4 [high] TUI `source_details_view`: double defensive copy

- **File**: `internal/tui/views/source_details_view.go:717-719` (caller) and `:762-784` (callee)
- **What's slop**:
  ```go
  // caller (line 717)
  if names := sessionMetadataStrings(session.Metadata, "linear_project_names"); len(names) > 0 {
      return append([]string(nil), names...)
  }
  
  // callee (line 762)
  if typed, ok := raw.([]string); ok {
      return append([]string(nil), typed...)
  }
  ```
  Double defensive copy. The inner copy is meant to break aliasing between the returned slice and the metadata map. The outer copy breaks aliasing between the returned slice and the function's local. One is redundant.
- **Fix**: `return typed` in the `[]string` branch. Keep the outer copy in the caller (or push it down and drop the outer one — pick a single boundary).

### 5.5 [high] TUI `source_details_view`: `hydrateSourceSummary` copies Labels into a struct the render path only reads

- **File**: `internal/tui/views/source_details_view.go:563-565`
- **What's slop**:
  ```go
  if len(hydrated.Labels) == 0 && len(session.Labels) > 0 {
      hydrated.Labels = append([]string(nil), session.Labels...)
  }
  ```
  `hydrateSourceSummary` builds a local `SourceSummary` that is consumed read-only by the markdown/listing renderers. The labels are never appended to, sorted, or passed to anything that mutates.
- **Fix**: `hydrated.Labels = session.Labels`.

### 5.6 [high] TUI `app.go`: `InterruptSessionsMsg` copies `[]string` of IDs that the goroutine only iterates

- **File**: `internal/tui/views/app.go:1983-1984` and (same pattern) `:1970`
- **What's slop**:
  ```go
  case InterruptSessionsMsg:
      ids := append([]string(nil), msg.SessionIDs...)
      // Snapshot sessions before spawning goroutine to avoid racing with concurrent
      // event-loop writes (upsertSession, SessionsLoadedMsg, etc.).
      sessions := append([]domain.AgentSession(nil), a.sessions...)
  ```
  The `sessions` copy is justified by the comment. The `ids` copy is not: string slice elements are immutable, the goroutine iterates with `range` (no mutation), and the slice itself is captured by value into the closure.
- **Fix**: `ids := msg.SessionIDs`.

### 5.7 [medium] Sentry source aggregation: project iteration dedupes then re-walks the seen map

- **File**: `internal/adapter/sentry/adapter.go:750-766`
- **What's slop**: Two passes for what could be one. First loop fills `projects` as a side effect; a second loop later materializes the map to a slice purely to call `sort.Strings(projectList)` (or similar).
- **Fix**: Either iterate `issues` once and append to `projectList` after the `seen` check inline, or keep the map and replace the materialise pass with `slices.Sorted(maps.Keys(projects))`.

### 5.x Verified NOT slop (defensible)

- `internal/adapter/acp/client.go:172,197` — `append([]byte(nil), scanner.Bytes()...)` is required; `scanner.Bytes()` is invalidated by the next `Scan()` call.
- `internal/adapter/glab/adapter.go:802` — `append([]branchEntry(nil), a.tracked[p.Branch]...)` is required; the next line mutates `tracked[0].repo` and the slice shares the struct array with `a.tracked`.
- `internal/domain/plan_document.go:67-78` — `orderedTaskPlans` must copy because `sort.SliceStable` sorts in place and is called from exported functions with caller-owned slices.
- `internal/tui/views/cmds.go:561` — `paths := append([]string(nil), compressed...)` is required because subsequent appends could otherwise mutate the underlying array returned by `filepath.Glob` if cap > len. Borderline but defensible.
- `internal/orchestrator/foreman.go:582-618` — `SessionID/IsRunning/LastSessionID/LastPlanID` are 3-line critical sections with defer, but codegraph callers show they are only called from orchestrator internals and tests, not from the TUI render path. Defer overhead is real but calls are infrequent.

---

## Category 6 — Bridge (TypeScript) slop

### 6.1 [high] `promptPromise.catch(() => {})` in omp-bridge

- **File**: `bridge/omp-bridge.ts:291-297`
- **What's slop**:
  ```ts
  // Defensive: if the watchdog wins the race below and `session.prompt()`
  // later rejects ... Attach a no-op catch to mark it as handled.
  promptPromise.catch(() => {});
  ```
  Direct `.catch(() => {})` on the pattern list. The 6-line comment is longer than the code, and the justification is internally inconsistent — the `.then()` chain on the next line still propagates the rejection, so this catch is not what prevents the unhandled-rejection log.
- **Fix**: Track both branches explicitly: `await Promise.race([...])` alongside `promptPromise.then(..., e => ({ok:false, e}))`, then inspect the prompt outcome after the race settles. No silent swallow.

### 6.2 [high] Empty `try { … } catch { /* swallow */ }` handlers in the bridges

- **File**: `bridge/omp-bridge.ts:114-119, 560-570`, `bridge/claude-agent-bridge.ts:326-331, 399-404`, `bridge/foreman-mcp/index.ts:93-95`
- **What's slop**:
  - `omp-bridge.ts:114-119` — `try { JSON.parse(raw) } catch { /* plain text answer */ }`
  - `omp-bridge.ts:560-570` — `try { process.stdout.write(...) } catch { /* Best-effort */ }`
  - `claude-agent-bridge.ts:326-331` — `try { msg = JSON.parse(line) } catch { continue; }`
  - `claude-agent-bridge.ts:399-404` — similar
  - `foreman-mcp/index.ts:93-95` — similar
  Errors are silently dropped with no log line. The narration comments (`/* plain text answer */`, `/* Best-effort */`) are the tell. Malformed Go->Bun messages are silently lost mid-loop in `claude-agent-bridge.ts`; the `session_meta` line can be dropped without any signal to the operator.
- **Fix**: At minimum `process.stderr.write` the original error in every catch. For `parseAnswerMessage`, surface the parse failure to the caller rather than collapsing to `{text: raw}`. For `session_meta`, treat a write failure as a `lifecycle=failed` event.

### 6.3 [high] `as ThinkingLevel` type assertion from env var

- **File**: `bridge/omp-bridge.ts:31-34`
- **What's slop**:
  ```ts
  const thinkingLevel: ThinkingLevel | undefined = process.env.SUBSTRATE_THINKING_LEVEL as
    | ThinkingLevel
    | undefined;
  ```
  An arbitrary env-var string is asserted into a closed enum. A typo (`SUBSTRATE_THINKING_LEVEL=meduium`) passes type-checking and only crashes deep in the SDK with no useful diagnostic.
- **Fix**: Validate against the known `ThinkingLevel` values at startup, or accept `string|undefined` here and validate in `initSession()` before passing to the SDK.

### 6.4 [high] `Record<string, unknown>` + `as any` at the SDK boundary

- **File**: `bridge/claude-agent-bridge.ts:425-467` and `:234-302` (`mapSDKMessage`)
- **What's slop**:
  ```ts
  const options: Record<string, unknown> = { cwd: worktreePath, ... };
  ...
  activeQuery = query({ prompt: generator as any, options: options as any });
  ```
  `options` is hand-rolled as `Record<string, unknown>` and then `as any`-cast at the call site. The Anthropic SDK ships full `Options` and `Query` types. Using them would catch option-shape drift on SDK revs. Same anti-pattern in `mapSDKMessage` where every `msg` field is `msg as any` (lines 234-302).
- **Fix**: Import `Options` and `SDKMessage` from `@anthropic-ai/claude-agent-sdk`, type `options` against `Options`, and narrow `msg` via a discriminated union. Keep `as unknown as Foo` only at the SDK boundary, once.

### 6.5 [high] Duplicated helper surface between the two bridges

- **File**: `bridge/omp-bridge.ts:64-186`, `bridge/claude-agent-bridge.ts:40-158`
- **What's slop**: The whole helper surface and `LineQueue` class are copy-pasted between the two bridges. Comments literally say "verbatim from omp-bridge.ts" — documentation of duplication rather than justification. Bug fixes in one bridge's `LineQueue` won't reach the other.
- **Fix**: Move to `bridge/protocol.ts` (or `bridge/stdio.ts`) and import from both. `LineQueue` is the obvious first target since it is the largest chunk.

### 6.6 [high] `mapEvent`: 8 `as any` casts in 90 lines

- **File**: `bridge/omp-bridge.ts:188-275`
- **What's slop**:
  ```ts
  function mapEvent(e: unknown): object[] {
    const event = e as Record<string, unknown>;
    ...
    const assistantEvent = (event as Record<string, any>).assistantMessageEvent;
    ...
    const toolName = String((event as Record<string, any>).toolName ?? "tool");
    const args = truncateText(safeJson((event as Record<string, any>).args ?? {}));
    ...
    const attempt = Number((event as any).attempt ?? 1);
  ```
  The same event is cast three ways and re-asserted in every branch. The `String(... ?? 'tool')` defends against a value that should be statically typed as `string`. The function's signature (`e: unknown`) is the only honest type in the whole body.
- **Fix**: Define a discriminated union for the OMP event shapes (`AgentEndEvent | AutoRetryStartEvent | ToolExecutionStartEvent | ...`). A `switch (event.type)` will narrow correctly and the `as any` disappears in each arm.

### 6.7 [medium] Watchdog tests read private fields instead of testing the public contract

- **File**: `bridge/agent-end-watchdog.test.ts:32-34, 53-54, 69-70, 86, 147-148`
- **What's slop**:
  ```ts
  expect(s.agentEndTerminalAt).toBe(T0);
  expect(s.lastAgentStopReason).toBe("stop");
  expect(s.postTurnWorkInFlight).toBe(false);
  
  expect(shouldFireAgentEndWatchdog(s, T0)).toBe(false);
  expect(shouldFireAgentEndWatchdog(s, T0 + POST_TURN_GRACE_MS - 1)).toBe(false);
  expect(shouldFireAgentEndWatchdog(s, T0 + POST_TURN_GRACE_MS)).toBe(true);
  ```
  The first three asserts read private fields directly. They will break on any rename of `agentEndTerminalAt` / `lastAgentStopReason` / `postTurnWorkInFlight` even when the public contract (`shouldFireAgentEndWatchdog`) is correct. The fourth-through-sixth asserts are the actual behavior test and are sufficient.
- **Fix**: Delete the internal-state asserts. `shouldFireAgentEndWatchdog()` is the public contract; if it returns the right value, the underlying state is correct by construction. Keep the deterministic clock injection (passing `now`) — that is real test infrastructure, not a mirror.

### 6.x Verified clean

- The watchdog implementation file (`agent-end-watchdog.ts`) and `compact-helpers.ts`.
- No triple-slash references or unused imports in the bridges.
- `compact-helpers.test.ts` is fine — it tests behavior only.

---

## Category 7 — Test slop (gap)

The "test slop" subagent returned an empty structured result (output was 98 bytes — just a header line). I confirmed by my own targeted searches that the codebase is **mostly clean of the worst test-slop shapes** (`assert.NotNil(t, x)` as a sole assertion, `assert.NotEmpty(t, x)`, `assert.NoError(t, x())` as the only check): no matches for those patterns in the production tree. The slop that exists in tests is instead covered by other categories:

- Test docstring rephrasings — covered in category 4.3.
- Test banner comments above single tests — covered in category 4.4 / 4.5.
- Internal-state asserts in `agent-end-watchdog.test.ts` — covered in category 6.7.

If a deeper test-slop pass is wanted (mirror mocks, table-driven tests where every case asserts the same thing, tests for private helpers that just verify the helper does what its name says), it should be re-dispatched with a tighter output schema.

---

## Cross-cutting observations

- **Two AGENTS.md rules are being followed well**:
  - (a) no silently-discarded errors in production code,
  - (b) errors are logged via `slog` (`slog.Warn` for recoverable, `slog.Error` for unrecoverable). No violation found.
- **The orchestrator is the worst offender** for categories 1, 2, 3, 5. `internal/orchestrator/agent_run_supervisor.go`, `implementation.go`, `foreman.go`, `answer_router.go`, `question_router.go`, `review_followup.go` together account for the majority of category 1, 2, 3 hits.
- **`internal/adapter/glab/adapter.go` (1900+ lines) is the worst single file for category 4** (one-line docstrings on unexported helpers) and also contributes to category 3.
- **`internal/tui/views/app.go` (~4500 lines) and `internal/tui/views/source_details_view.go` are the worst TUI files** for categories 4 (banners) and 5 (defensive copies in render paths).
- **`bridge/omp-bridge.ts` is the worst TypeScript file** for categories 1, 2, 3, 4, 5, 6 — basically all of them. The `LineQueue` and helpers duplication (item 6.5) is the single highest-leverage refactor in the bridge.
- **No category 1-6 items require a design change** — every fix is mechanical. The largest is category 4.6 (~40 docstring deletions in one file), which is purely a search-and-delete pass.

---

## Suggested grouping for remediation PRs

If processing asynchronously, a reasonable PR sequencing (each one independently reviewable + mergeable):

1. **PR 1: Dead interfaces and wrappers** — category 1 (all 5 items). ~50 lines deleted.
2. **PR 2: Constructor-validated nil guards in orchestrator** — category 2.1, 2.2, 2.3, 2.4. ~30 lines deleted.
3. **PR 3: Verbose error wrapping in orchestrator** — category 3.4, 3.5, 3.6, 3.7. ~50 wrappers trimmed. (Defer 3.1, 3.2, 3.3 in `internal/service/` to a separate PR if preferred.)
4. **PR 4: Docstring and banner cleanup in glab/adapter.go** — category 4.6, plus a single grep over the orchestrator for banners (4.4, 4.5). ~80 lines removed.
5. **PR 5: Defensive copies in adapters and TUI** — category 5 (all 7 items). ~25 lines removed.
6. **PR 6: Bridge cleanup** — category 6 (all 7 items, ordered: 6.5 first since it unblocks 6.6, then 6.1, 6.2, 6.3, 6.4, 6.6, 6.7). ~150 lines changed.
7. **PR 7 (optional): test docstrings and test banners** — category 4.3, 4.4 in test files only. ~80 lines removed.
8. **PR 8 (optional): duplicate package headers + giant doc.go** — category 4.2, 4.7. ~60 lines removed.

PR 1-6 cover the highest-signal items. PRs 7-8 are pure cleanup with no functional impact.
