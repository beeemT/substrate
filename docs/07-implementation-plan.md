# 07 - Implementation Plan

Phased build-out of Substrate over ~12 weeks. Each phase has deliverables, quality gates, and test commands. See `02-layered-architecture.md` for layering, `03-event-system.md` for events, `06-tui-design.md` for TUI.

## Directory Structure

```
cmd/substrate/main.go
internal/
    domain/            # domain model structs
    repository/        # interfaces + SQLite implementations
    service/           # state machines, domain logic
    orchestrator/      # composes services into workflows
    adapter/
        linear/        # Linear GraphQL adapter (issues/projects/initiatives)
        manual/        # Manual work item input adapter
        glab/          # glab CLI wrapper
        harness/omp/   # oh-my-pi agent harness
    event/             # pub/sub bus, hook dispatch
    gitwork/           # git-work CLI wrapper
    docsource/         # documentation source interface + impls
    config/            # TOML config loading
    tui/
        views/         # bubbletea view models
        components/    # reusable TUI components
        styles/        # lipgloss styles
bridge/omp-bridge.ts   # oh-my-pi SDK bridge (Bun)
migrations/001_initial.sql
~/.substrate/state.db  # global SQLite database (all workspaces)
```

## Phase 0: Project Bootstrap (Week 1)

Go module init, scaffold all packages, dependencies (`jmoiron/sqlx`, `modernc.org/sqlite`, `pelletier/go-toml`, `charmbracelet/bubbletea`, `go-atomic`). SQLite migration runner (embedded SQL via `embed.FS`, `_migrations` tracking table), TOML config loader into typed `config.Config`, `cmd/substrate/main.go` that loads config + opens DB via `sqlx.Open` + runs migrations. **Note:** `go-atomic`'s `isRetryable()` must be extended to include `SQLITE_BUSY` (error code 5) and `SQLITE_LOCKED` (error code 6) for SQLite retry support. This requires a PR to go-atomic before Substrate can use it for concurrent DB access; submit this PR as part of Phase 0.

Config loading also validates the `[commit]` block: `strategy` enum (`granular` | `semi-regular` | `single`, default `semi-regular`), `message_format` enum (`ai-generated` | `conventional` | `custom`, default `ai-generated`), optional `message_template` string (required when `message_format = "custom"`). Commit config is passed to the agent session factory so it is included in session context — agents receive commit cadence and message-format instructions alongside the sub-plan.

**Gate:** `go build ./...` passes. `go test ./...` passes (config loads, migrations run on fresh DB). `go vet ./...` clean.
**Test:** `go test ./internal/config/... ./internal/repository/...`

## Phase 1: Core Domain + Persistence (Week 2)

Domain structs in `internal/domain/`. Repository interfaces in `internal/repository/`. SQLite implementations using go-atomic's `generic.SQLXRemote` interface (satisfied by both `*sqlx.DB` and `*sqlx.Tx`) with `db:"column"` tagged row structs, pointer types for nullable columns, `GetContext`/`SelectContext`/`NamedExecContext` for queries. Explicit `toDomain`/`toRow` conversions. Migration `001_initial.sql` with all tables (scoped by `workspace_id` FK), indexes, CHECK constraints for state enums.

**go-atomic Resources pattern:** Each repo struct accepts `generic.SQLXRemote` in its constructor (e.g., `NewWorkItemRepo(remote generic.SQLXRemote)`). A `Resources` struct aggregates all repos. `ResourcesFactory` creates a `Resources` from a transaction handle. Business logic in the orchestrator uses `Transacter.Transact()` to wrap multi-repo operations in a single atomic transaction with automatic retry and backoff on transient errors. Transaction flattening: nested `Transact` calls reuse the outer transaction.

```go
// DI wiring
db, _ := sqlx.Open("sqlite", "~/.substrate/state.db")
db.MustExec("PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;")
executor := sqlxexec.NewExecuter(db)
transacter := generic.NewTransacter[generic.SQLXRemote, Resources](executor, ResourcesFactory)
```

**Gate:** 100% of repo interface methods have a test. FK constraint tests prove invalid references error. Transact wraps multi-repo write and rolls back on error. `go test ./internal/repository/... -count=1`

## Phase 2: Service Layer (Week 2-3)

`WorkItemService`, `PlanService`, `WorkspaceService`, `SessionService`, `ReviewService`, `DocumentationService`. State machine enforcement: invalid transitions return typed `ErrInvalidTransition{From, To, Entity}`. Mock repositories (interface-based, hand-written or `moq`-generated).

**Gate:** All valid + invalid state transitions tested for WorkItem (7 states, ~10 valid edges, at least 1 invalid per state). Coverage >90%. `go test ./internal/service/... -cover`

## Phase 3: Event Bus + Adapter Interfaces (Week 3)

Channel-based pub/sub with topic routing. Synchronous pre-hooks (abort on error), async post-hooks. Configurable per-hook timeout (default 10s). Event persistence to `EventRepository` before dispatch. Adapter interfaces defined:

```go
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
    UpdateState(ctx context.Context, externalID string, state WorkItemState) error
    AddComment(ctx context.Context, externalID string, body string) error

    // System event hooks
    OnEvent(ctx context.Context, event SystemEvent) error
}

type AdapterCapabilities struct {
    CanWatch     bool
    CanBrowse    bool
    CanMutate    bool
    BrowseScopes []SelectionScope
}

type RepoLifecycleAdapter interface {
    Name() string
    OnEvent(ctx context.Context, event SystemEvent) error
}

type AgentHarness interface { StartSession(ctx context.Context, o SessionOpts) (AgentSession, error) }
type AgentSession interface {
    Prompt(ctx context.Context, text string) error
    Events() <-chan AgentEvent
    Abort() error
    Wait() error
}
```

**Gate:** Concurrent test passes under `-race` (100 goroutines publishing). Pre-hook abort prevents subscriber delivery. Timeout test: hook sleeps 5s with 100ms deadline, returns `DeadlineExceeded`. `go test ./internal/event/... -race -count=3`

## Phase 4: git-work Integration (Week 3-4)

```go
type Client struct{ BinPath string }
func (c *Client) Checkout(ctx context.Context, repoDir, branch string) (string, error)
func (c *Client) List(ctx context.Context, repoDir string) ([]Worktree, error)
func (c *Client) Remove(ctx context.Context, repoDir, branch string) error
```

Stdout parsed for machine-readable paths, stderr captured for diagnostics. **Workspace discovery:** `substrate init` creates a `.substrate-workspace` file (YAML with ULID, name, timestamp) in the current directory. On startup, Substrate scans the workspace folder for git-work repos (directories containing a `.bare/` subdirectory). The planning agent reads all discovered repos from their `main/` worktrees to determine which are relevant to the work item. Work items create feature worktrees only in repos the plan touches. Multiple work items coexist in the same workspace as separate worktrees/branches in shared repos.

**Gate:** Unit: canned output parsed correctly. Integration: `substrate init` creates `.substrate-workspace` with valid ULID. Workspace scan discovers repos with `.bare/`. Checkout → `test-branch/` exists. Remove → gone. `go test ./internal/gitwork/...` and `go test -tags=integration ./internal/gitwork/...`

## Phase 5: Documentation Source System (Week 4)

```go
type Source interface {
    ID() string
    Discover(ctx context.Context) ([]Document, error)
    Fetch(ctx context.Context, path string) (Document, error)
    Search(ctx context.Context, query string) ([]Document, error)
}
```

`RepoEmbeddedSource`: glob-based discovery in `main/` worktrees. `DedicatedRepoSource`: separate doc repo via git-work. Staleness detection: given changed file paths, flag documents via configurable path-to-doc mapping rules.

**Gate:** Glob finds `docs/arch.md` but not `vendor/README.md`. Changing `internal/auth/handler.go` flags `docs/auth.md`; changing `internal/billing/invoice.go` does not. `go test ./internal/docsource/...`

## Phase 6: Agent Harness + oh-my-pi Bridge (Week 4-5)

Bridge script (`bridge/omp-bridge.ts`): JSON-line protocol over stdio. Commands: `start`, `prompt`, `abort`. Events: `started`, `text_delta`, `tool_start`, `tool_end`, `done`, `error`. Pin SDK version.

Go side (`internal/adapter/harness/omp/`): spawns `bun run bridge/omp-bridge.ts`, manages JSON-line I/O, maps to `domain.AgentEvent`, handles lifecycle (close stdin for graceful shutdown, SIGKILL after timeout).

**Gate:** Unit: JSON-line round-trip correct. Integration: session starts, trivial prompt produces `text_delta`, clean shutdown. `Abort()` terminates subprocess within 5s. `go test ./internal/adapter/harness/omp/...` and `go test -tags=integration ./internal/adapter/harness/omp/...`

**Agent output log:** All JSON events emitted by the bridge stdout are tailed and written to `~/.substrate/sessions/<session-id>.log` in JSONL format with timestamps. This file is the source of truth for session output, enabling:
- **Multi-instance:** any Substrate instance can tail the log to display live or historical output without holding the subprocess.
- **Resume context:** interrupted sessions include last N lines of the log as context for the new session preamble.
- **Audit:** full session history persisted independently of the DB.

The Go harness adapter opens the log file on session start (`O_CREATE|O_APPEND`), writes each received event, and closes it when the session exits or is aborted.

## Phase 7: Planning Pipeline (Week 5-6)

Context assembly: work item + documentation + repo file trees (depth-filtered). Token budget enforcement. Go `text/template` prompt (embedded file). Plan parsing: heading-based (`## Sub-plan: <repo>`) → `domain.Plan` + `[]domain.SubPlan`. Malformed input → `ErrPlanParseFailed`. Plan review loop: approve/reject/revise, feedback appended, version incremented, all versions preserved.

**Gate:** 3-repo markdown → exactly 3 SubPlans. Missing headings → `ErrPlanParseFailed`. After 2 revisions, version is 3 and all retrievable. `go test ./internal/orchestrator/...`

## Phase 8: Implementation Orchestrator (Week 6-7)

Sub-plan dependency ordering via topological sort (cycle detection → `ErrCyclicDependency`). Worktree creation per sub-plan (emits `WorktreeCreated`). Agent sessions spawned with sub-plan + cross-repo plan + docs. Independent sub-plans execute concurrently (`errgroup`). All events forwarded to bus.

**Gate:** `{A:[], B:[A], C:[A], D:[B,C]}` → A first, D last. `{A:[B], B:[A]}` → error. Independent sub-plans start within 100ms of each other. Dependent waits. `go test ./internal/orchestrator/... -race`

## Phase 9: Foreman + Review Pipeline (Week 7-8)

**Foreman:** monitors agent events for questions (heuristic: `?` suffix or `QUESTION:` prefix). Answers from plan/docs or escalates to human. **Review:** on `SessionCompleted`, diff vs `main/`, spawn review agent, parse `[]Critique{Severity, File, LineRange, Description}`. Major critiques → re-implementation. Cycle limit (default 3) → escalate. Post-review documentation staleness check.

**Gate:** Answerable question resolved without human. Unanswerable question escalated. 2 major critiques → re-implement → 0 major → done at round 2. 3 rounds of majors → `escalated`. `go test ./internal/orchestrator/... -race`

## Phase 9b: Resume & Recovery (Week 8)

**PID tracking:** Add `pid` column (INTEGER, nullable) to `agent_sessions` table. Store subprocess PID when agent session starts. New session status: `"interrupted"` — means the session was running but Substrate crashed or was closed and the process is gone.

**Startup reconciliation protocol:**
1. Find workspace: look for `.substrate-workspace` in cwd or ancestors.
2. Load workspace from global DB by ID from the file.
3. Update workspace path in DB if the folder moved.
4. For any `agent_sessions` in `"running"` state:
   a. Check stored PID: is process still alive? (`kill -0 pid`)
   b. If alive: another Substrate instance owns this session. This instance can observe via session log but cannot send inputs. Skip.
   c. If dead: mark session as `"interrupted"`.
5. For any work items in `"implementing"` or `"reviewing"` state with ALL sessions completed/failed/interrupted:
   a. Check if any sessions are `"interrupted"` → surface in TUI with resume option.
6. Present state in dashboard.

**Resume protocol:** TUI shows interrupted sessions with `[R]esume [A]bandon`. Resume starts a fresh agent session in the SAME worktree with context: *"You are continuing work on this sub-plan. The worktree contains partial changes from a previous session. Review the current state via `git diff` and `git status`, then continue implementing the remaining items from the sub-plan."* On resume, the orchestrator reads the last 50 lines of the interrupted session's `.log` file and prepends them to the new session preamble so the agent has immediate context on what was last happening. Abandon marks the session failed; human can manually fix.

**Graceful shutdown:** On SIGINT/SIGTERM, mark all active sessions as `"interrupted"` before exit. Drain in-flight events. Close DB cleanly.

**Idempotency guards:**
- Worktree creation: check if exists first (`git-work list`), skip if already present.
- MR creation: glab adapter checks if MR exists for branch before creating.
- Linear state updates: inherently idempotent.

**Gate:** Kill Substrate mid-session, restart, verify interrupted detection, resume session, verify continuation picks up partial work. Verify `.log` file contains partial JSONL output after kill. Verify resumed session preamble contains last-50-lines context from the interrupted log. Graceful shutdown completes within 15s. `go test ./internal/orchestrator/... -race` and `go test -tags=integration ./internal/orchestrator/...`

## Phase 10: Linear Adapter + Selection Model (Week 8)

GraphQL client (`net/http` + JSON). `Watch`: poll assigned issues, dedup, exponential backoff on 429. `Fetch`, `UpdateState` (maps domain states to Linear workflow state IDs from config), `AddComment`. Event hooks: `PlanApproved` -> "In Progress", `WorkItemCompleted` -> "Done".

**Selection model** -- `ListSelectable` and `Resolve` for all three scopes:

- `ScopeIssues`: GraphQL query for team issues with filtering. Select 1+ issues, `Resolve` aggregates into a single `WorkItem` with a rich description containing all issue details.
- `ScopeProjects`: GraphQL query for projects visible to team. Select 1+ projects, `Resolve` fetches all child issues for each project and builds a comprehensive `WorkItem` with project context and full issue listing.
- `ScopeInitiatives`: GraphQL query for initiatives. Select 1 initiative, `Resolve` fetches all child projects and their issues, builds `WorkItem` with initiative-level context and full breakdown.

Each scope has its own GraphQL query and response parsing. `Capabilities()` returns `CanWatch: true, CanBrowse: true, CanMutate: true, BrowseScopes: [issues, projects, initiatives]`.

**Gate:** Correct GraphQL query construction for all three scopes. Parsed response -> valid `WorkItem`. Backoff: 429 -> delay >= 2x. Unit tests: `ListSelectable` returns correct items per scope, `Resolve` aggregates correctly for multi-issue selection, `Resolve` for project scope fetches all child issues. Integration (requires `SUBSTRATE_LINEAR_API_KEY`): browse real team issues, select, resolve into `WorkItem`, fetch + update round-trip. `go test ./internal/adapter/linear/...`

## Phase 10b: Manual Adapter (Week 8)

Implement `ManualAdapter` struct in `internal/adapter/manual/`. This is a lightweight adapter for ad-hoc work items not tracked in an external system.

- `Name()` returns `"manual"`.
- `Capabilities()` returns `CanWatch: false, CanBrowse: false, CanMutate: false, BrowseScopes: nil`.
- `ListSelectable` returns empty result (not supported).
- `Resolve` takes `Selection` with `ManualWorkItemInput` (title, description, repositories) and creates a `WorkItem` directly from user input.
- `Watch` returns a closed channel immediately.
- `Fetch`, `UpdateState`, `AddComment` are no-ops (return nil or zero values).
- `OnEvent` is a no-op.

This is a small phase, likely 1-2 days of effort.

**Gate:** Unit tests: `Resolve` produces valid `WorkItem` from `ManualWorkItemInput`, `Watch` returns closed channel, `UpdateState` and `AddComment` are no-ops. `go test ./internal/adapter/manual/...`

## Phase 11: glab Adapter (Week 8-9)

Wraps `glab` CLI. Event-driven: `OnEvent(WorktreeCreatedEvent)`: `glab mr create --draft --source-branch ... --reviewer ... --label ...`, parse MR URL. `OnEvent(BranchPushedEvent)`: update MR description, mark `--ready` if applicable. `OnEvent(WorkItemCompletedEvent)`: `glab mr view --output json`, finalize MR status.

**Gate:** WorktreeCreated event → OnEvent fires → MR created. JSON parsing from `mr view`. Integration (requires glab auth): event fires → MR created. `go test ./internal/adapter/glab/...`

## Phase 12: TUI (Week 9-11)

| Sub-phase | Scope | Gate |
|-----------|-------|------|
| 12a | Shell + router + dashboard (work item list with status) + New Session wizard | Dashboard renders list, navigation works, New Session flow completes |
| 12b | Work item detail + plan review (approve/reject/revise keys) | Approve triggers `PlanApproved` event |
| 12c | Agent sessions (live streaming, multi-session split, tool indicators) | Events render real-time |
| 12d | Review view (diff + critiques with severity) + notifications | Critiques render, toast on escalation |
| 12e | Config view (view/edit TOML, validate on save) | Changes persist without restart |

**New Session wizard** (part of 12a): triggered by `[N]ew` from Dashboard. Adapter selection view (Linear / Manual). If Linear: scope selection (Issues / Projects / Initiatives), then a searchable paginated list with fuzzy filtering and multi-select (toggle with space, confirm with enter), then aggregated work item preview before confirmation. If Manual: form view with Title (required), Description (text area), and Repositories (add/remove list with clone URLs), then confirm to start. Requires a multi-select list component for Linear browsing and a form component for Manual input.

**Gate:** Full walkthrough: launch → dashboard → select item → view plan → approve → see sessions → view review → completion. `go test ./internal/tui/...`

## Phase 13: End-to-End Integration (Week 11-12)

Full workflow: Linear issue → plan → approve → implement → review → done. Work item traverses all states. MRs created in GitLab. Error recovery: killed agent restartable, network failure recovers via backoff, corrupt plan surfaced to human, git-work failure shows remediation hint. Performance: 5-repo plan < 5 min, total workflow < 30 min.

**Gate:** `go test -tags=integration,e2e -timeout=30m ./test/e2e/...`

## Autonomous Validation Strategy

```bash
# Unit (no external deps, every push + CI)
go test ./...
go test -race ./...
go vet ./...

# Integration (tagged, nightly CI with secrets)
go test -tags=integration ./internal/gitwork/...          # needs git-work + network
go test -tags=integration ./internal/docsource/...        # needs git-work + network
go test -tags=integration ./internal/adapter/harness/omp/... # needs bun + omp creds
go test -tags=integration ./internal/adapter/linear/...   # needs SUBSTRATE_LINEAR_API_KEY
go test -tags=integration ./internal/adapter/glab/...     # needs glab auth

# End-to-end (manual trigger, all deps)
go test -tags=integration,e2e -timeout=30m ./test/e2e/...
```

CI: every push runs `go build/vet/test` + `-race`. Nightly runs integration. Manual trigger for e2e.

## Risk Register

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| oh-my-pi SDK breaks bridge | Medium | High | Pin version, version protocol (`"protocol":1` handshake), integration test catches |
| git-work output format changes | Low | Medium | Regex parsing (not positional), typed wrapper isolates blast radius |
| Linear rate limiting | Medium | Low | Exponential backoff with jitter on 429, configurable interval (default 30s) |
| Agent produces unparseable plan | High | Medium | Retry with format instructions, fallback to raw markdown + human decomposition, max 2 retries |
| Review loop doesn't converge | Medium | High | Hard cycle limit (default 3), escalate to human, preserve critique history |
| Large repos slow planning | Medium | Medium | `.substrateignore` filtering, token budget with priority ordering, depth-2 summarization |
| SQLite write contention | Low | Medium | Single writer goroutine + channel queue, WAL mode, separate read connections |
| Bridge subprocess zombies | Medium | Medium | PID tracking, watchdog reaper, SIGTERM → 10s → SIGKILL |
| git-work not installed | High | High | Startup PATH check, actionable error with install instructions, fail fast |
| Linear API schema changes break project/initiative queries | Low | Medium | Typed response structs catch at compile time, integration tests catch at runtime, graceful degradation (fall back to issues-only scope) |

| go-atomic SQLITE_BUSY retry not yet implemented | Certain | High | Contribute PR to go-atomic adding `SQLITE_BUSY` (5) and `SQLITE_LOCKED` (6) to `isRetryable()` before Phase 1, or fork temporarily |
| Agent output log growth (unbounded log files for long sessions) | Low | Low | Log rotation after session completes: keep last 10 MB, compress older segments. Clean up on workspace prune command. No correctness impact — disk space only. |