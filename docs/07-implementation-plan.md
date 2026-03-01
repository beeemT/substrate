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
        ohmypi/        # oh-my-pi agent harness (renamed from harness/omp)
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

**[UPDATED - IMPLEMENTED]** Go module init, scaffold all packages, dependencies (`jmoiron/sqlx`, `modernc.org/sqlite`, `pelletier/go-toml`, `charmbracelet/bubbletea`, `go-atomic`). SQLite migration runner (embedded SQL via `embed.FS`, `_migrations` tracking table), TOML config loader into typed `config.Config`, `cmd/substrate/main.go` that loads config + opens DB via `sqlx.Open` + runs migrations.

Config loading validates:
- `[commit]` block: `strategy` enum (`granular` | `semi-regular` | `single`, default `semi-regular`), `message_format` enum (`ai-generated` | `conventional` | `custom`, default `ai-generated`), optional `message_template` string (required when `message_format = "custom"`).
- `[plan]` block (`max_parse_retries` int, default 2).
- `[review]` block (`pass_threshold` enum, default `minor_ok`; `max_cycles` int, default 3).
- `[foreman]` block **[UPDATED - IMPLEMENTED]**: `enabled` bool (default true), `question_timeout` duration string (default "0" = wait indefinitely).
- `[adapters.ohmypi]` block **[UPDATED - IMPLEMENTED]**: `bun_path`, `bridge_path`, `thinking_level` (maps to oh-my-pi thinkingLevel for all sessions).
- Per-repo `[repos.<name>]` sections are optional.

**First-start flow:** Detect absence of `~/.substrate/`, create directory, run migrations. If cwd has no `.substrate-workspace`, present the Workspace Initialization Modal (see `06-tui-design.md` §4c). `substrate init` is the programmatic equivalent of the modal flow: creates `.substrate-workspace` with a ULID, scans for git-work repos, warns about plain clones, inserts workspace into DB.

**[UPDATED - IMPLEMENTED]** `go-atomic`'s `isRetryable()` must be extended to include `SQLITE_BUSY` (error code 5) and `SQLITE_LOCKED` (error code 6). go-atomic is a first-party library; add this in Phase 0 as a minor internal change.

**Gate:** `go build ./...` passes. `go test ./...` passes (config loads, migrations run on fresh DB). `go vet ./...` clean.
**Test:** `go test ./internal/config/... ./internal/repository/...`

## Phase 1: Core Domain + Persistence (Week 2)

**[UPDATED - IMPLEMENTED]** Domain structs in `internal/domain/`. Repository interfaces in `internal/repository/`. SQLite implementations using go-atomic's `generic.SQLXRemote` interface (satisfied by both `*sqlx.DB` and `*sqlx.Tx`) with `db:"column"` tagged row structs, pointer types for nullable columns, `GetContext`/`SelectContext`/`NamedExecContext` for queries. Explicit `toDomain`/`toRow` conversions. Migration `001_initial.sql` with all tables (scoped by `workspace_id` FK), indexes, CHECK constraints for state enums.

**[UPDATED - IMPLEMENTED]** Schema includes `substrate_instances` table: `id, workspace_id, pid, hostname, last_heartbeat, started_at`. The `agent_sessions` table includes `owner_instance_id` FK to `substrate_instances` for session ownership tracking.

**[UPDATED - IMPLEMENTED]** Additional columns vs. original spec:
- `critiques.suggestion TEXT` — optional improvement suggestion from review agent.
- `questions.context TEXT` — surrounding context from agent.
- `documentation_sources.repository_name TEXT` — for repo_embedded: name of the workspace repo.

**[UPDATED - IMPLEMENTED]** go-atomic Resources pattern: Each repo struct accepts `generic.SQLXRemote` in its constructor. A `Resources` struct aggregates all repos AND services constructed from them. `ResourcesFactory` creates a `Resources` from a transaction handle. Business logic in the orchestrator uses `Transacter.Transact()` to wrap multi-repo operations in a single atomic transaction with automatic retry and backoff on transient errors. Transaction flattening: nested `Transact` calls reuse the outer transaction.

```go
// DI wiring
db, _ := sqlx.Open("sqlite", "~/.substrate/state.db")
db.MustExec("PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;")
executor := sqlxexec.NewExecuter(db)
transacter := generic.NewTransacter[generic.SQLXRemote, Resources](executor, ResourcesFactory)
```

**[UPDATED - IMPLEMENTED]** `Resources` struct includes services (PlanSvc, WorkItemSvc, SessionSvc, ReviewSvc) constructed from transaction-bound repos. Business logic calls service methods directly inside `Transact` — state-machine guards and persistence are always atomic.

**Gate:** 100% of repo interface methods have a test. FK constraint tests prove invalid references error. Transact wraps multi-repo write and rolls back on error. `go test ./internal/repository/... -count=1`

## Phase 2: Service Layer (Week 2-3)

**[UPDATED - IMPLEMENTED]** `WorkItemService`, `PlanService`, `WorkspaceService`, `SessionService`, `ReviewService`, `DocumentationService`, `EventService`. State machine enforcement: invalid transitions return typed `ErrInvalidTransition{From, To, Entity}`. Mock repositories (interface-based, hand-written or `moq`-generated).

**[UPDATED - IMPLEMENTED]** Services own domain model types. Services depend on repository interfaces (injected). Services never call other services — cross-service coordination belongs in business logic layer. Services return domain errors, not SQL errors.

**Gate:** All valid + invalid state transitions tested for WorkItem (7 states, ~10 valid edges, at least 1 invalid per state). Coverage >90%. `go test ./internal/service/... -cover`

## Phase 3: Event Bus + Adapter Interfaces (Week 3)

**[UPDATED - IMPLEMENTED]** Channel-based pub/sub with topic routing. Synchronous pre-hooks (abort on error), async post-hooks. Configurable per-hook timeout (default 30s for post-hooks; pre-hooks use caller's context deadline). Event persistence to `EventRepository` before dispatch. Pre-hook types tracked in `map[EventType]bool` with `EventWorktreeCreating` as the only pre-hook by default.

**[UPDATED - IMPLEMENTED]** Bus supports `WithDropHandler(fn func(subscriberID string, event SystemEvent)) BusOption` — when set (by TUI), dropped events enqueue a warning toast instead of just logging.

**[UPDATED - IMPLEMENTED]** Adapter interfaces defined:

```go
type TrackerState string
const (
    TrackerStateTodo       TrackerState = "todo"
    TrackerStateInProgress TrackerState = "in_progress"
    TrackerStateInReview   TrackerState = "in_review"
    TrackerStateDone       TrackerState = "done"
)

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
    UpdateState(ctx context.Context, externalID string, state TrackerState) error
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

type AgentHarness interface {
    Name() string
    Capabilities() HarnessCapabilities
    StartSession(ctx context.Context, o SessionOpts) (HarnessSession, error)
}

type HarnessSession interface {
    ID() string
    Wait(ctx context.Context) (SessionResult, error)
    Events() <-chan SessionEvent
    SendMessage(ctx context.Context, msg string) error
    Abort(ctx context.Context) error
}

type SessionMode string
const (
    SessionModeAgent   SessionMode = "agent"   // coding sub-agent; full tool set
    SessionModeForeman SessionMode = "foreman" // question answering; read-only tools
)

type SessionOpts struct {
    SessionID            string      // substrate-generated ULID
    Mode                 SessionMode // defaults to Agent
    WorktreePath         string      // empty for foreman sessions (uses workspace root)
    DraftPath            string      // absolute path to plan-draft.md; set for planning sessions
    SubPlan              SubPlan
    CrossRepoPlan        string
    SystemPrompt         string
    AllowPush            bool
    DocumentationContext string
}
```

**[UPDATED - IMPLEMENTED]** Event catalog includes `WorktreeCreating` (pre-hook, before git-work checkout) in addition to `WorktreeCreated` (post-hook). See `03-event-system.md` for full catalog.

**Gate:** Concurrent test passes under `-race` (100 goroutines publishing). Pre-hook abort prevents subscriber delivery. Timeout test: hook sleeps 5s with 100ms deadline, returns `DeadlineExceeded`. `go test ./internal/event/... -race -count=3`

## Phase 4: git-work Integration (Week 3-4)

**[UPDATED - IMPLEMENTED]**

```go
type Client struct{ BinPath string }
func (c *Client) Checkout(ctx context.Context, repoDir, branch string) (string, error)
func (c *Client) List(ctx context.Context, repoDir string) ([]Worktree, error)
func (c *Client) Remove(ctx context.Context, repoDir, branch string) error
```

**[UPDATED - IMPLEMENTED]** Workspace discovery: `substrate init` creates a `.substrate-workspace` file (YAML with ULID, name, timestamp) in the current directory. On startup, Substrate walks from cwd upward looking for `.substrate-workspace`. If DB-stored path differs from current filesystem path (user moved folder), DB is updated — workspace ID is stable identity, not path.

**[UPDATED - IMPLEMENTED]** Repo discovery scans workspace folder for git-work repos (directories containing a `.bare/` subdirectory). Plain git clones (`.git/` present, no `.bare/`) are surfaced as workspace health warnings requiring acknowledgement. Other directories are ignored.

**[UPDATED - IMPLEMENTED]** Pre-flight check (before each plan): re-verify git-work repos, surface any new plain clones as warnings.

**Gate:** Unit: canned output parsed correctly. Integration: `substrate init` creates `.substrate-workspace` with valid ULID. Workspace scan discovers repos with `.bare/`. Checkout → `test-branch/` exists. Remove → gone. `go test ./internal/gitwork/...` and `go test -tags=integration ./internal/gitwork/...`

## Phase 5: Documentation Source System (Week 4)

**[UPDATED - IMPLEMENTED]**

```go
type DocSourceType int
const (
    DocSourceRepoEmbedded DocSourceType = iota
    DocSourceDedicatedRepo
)

type DocumentationSource interface {
    Name() string
    Type() DocSourceType
    Fetch(ctx context.Context, opts DocFetchOpts) ([]Document, error)
    Search(ctx context.Context, query string) ([]DocumentMatch, error)
    Sync(ctx context.Context) error
}
```

**[UPDATED - IMPLEMENTED]** `RepoEmbeddedSource`: glob-based discovery in `main/` worktrees. `DedicatedRepoSource`: separate doc repo via git-work; `Sync` runs `git pull --ff-only` in its main worktree before every planning phase.

**[UPDATED - IMPLEMENTED]** Documentation staleness check: After final review passes, spawn a short documentation harness session (foreman mode, read-only tools) with list of changed files and documentation sources as context. Agent decides whether docs are stale and may update them. Results are advisory and do not block completion.

**Gate:** Glob finds `docs/arch.md` but not `vendor/README.md`. Changing `internal/auth/handler.go` flags `docs/auth.md`; changing `internal/billing/invoice.go` does not. `go test ./internal/docsource/...`

## Phase 6: Agent Harness + oh-my-pi Bridge (Week 4-5)

Bridge script (`bridge/omp-bridge.ts`): JSON-line protocol over stdio.

**Go → Bun (stdin):**
- `{"type":"prompt","text":"..."}` — initial prompt or continuation
- `{"type":"message","text":"..."}` — follow-up message (human iteration)
- `{"type":"answer","text":"..."}` — resolve pending `ask_foreman` tool call
- `{"type":"abort"}` — terminate session

**Bun → Go (stdout):**
- `{"type":"event","event":{"type":"progress","text":"..."}}` — text delta
- `{"type":"event","event":{"type":"question","question":"...","context":"..."}}` — agent called `ask_foreman`
- `{"type":"event","event":{"type":"foreman_proposed","text":"...","uncertain":true}}` — foreman session produced answer with confidence marker
- `{"type":"event","event":{"type":"complete","summary":"..."}}` — turn completed

**[NEW]** `foreman_proposed` event carries the Foreman LLM's proposed answer. `uncertain` is `true` when the Foreman signalled `CONFIDENCE: uncertain` (last line of response). The bridge strips the confidence marker line from `text` before emitting. Missing confidence marker → conservative `uncertain: true`.

**[NEW]** `mapEvent` returns `null` for unhandled event types; the caller filters before emitting (no null events reach Go).

Go side (`internal/adapter/ohmypi/`): spawns `bun run bridge/omp-bridge.ts`, manages JSON-line I/O, maps to `domain.AgentEvent`, handles lifecycle.

**[NEW] Subprocess Sandboxing:**
- **macOS:** Bridge subprocess wrapped with `sandbox-exec` using a profile that allows reads everywhere but restricts `file-write*` to the session worktree path and a session-specific temp directory (`/tmp/substrate-<session-id>/`).
- **Linux:** Mount namespaces (`unshare --mount`) with bind mounts achieve equivalent isolation.
- **Planning sessions** (no worktree): restrict writes to `.substrate/sessions/<id>/` scratch directory only.
- **Review and foreman sessions:** use read-only tool set (`read`, `grep`, `find`) — no write tools registered.

**[NEW] Custom Tool — ask_foreman:** Only registered in agent mode. Blocks until orchestrator sends `{type:"answer"}` on stdin. The tool's `execute` function emits a `question` event and awaits resolution via Promise.

**Gate:** Unit: JSON-line round-trip correct. Integration: session starts, trivial prompt produces `text_delta`, clean shutdown. `Abort()` terminates subprocess within 5s. `go test ./internal/adapter/ohmypi/...` and `go test -tags=integration ./internal/adapter/ohmypi/...`

**Agent output log:** All JSON events emitted by the bridge stdout are tailed and written to `~/.substrate/sessions/<session-id>.log` in JSONL format with timestamps. This file is the source of truth for session output, enabling:
- **Multi-instance:** any Substrate instance can tail the log to display live or historical output without holding the subprocess.
- **Resume context:** interrupted sessions include last N lines of the log as context for the new session preamble.
- **Audit:** full session history persisted independently of the DB.

The Go harness adapter opens the log file on session start (`O_CREATE|O_APPEND`), writes each received event, and closes it when the session exits or is aborted.

**Log rotation** runs during session execution: rotate at 10 MB segments, compressing the previous segment. Keep maximum 5 compressed segments. After session completion, compress the final segment. TUI tailing handles rotation by detecting size regression or inode change and resetting offset to 0.

## Phase 7: Planning Pipeline (Week 5-6)

**[NEW] Step 0: Pull main worktrees.** Before discovery, run `git pull --ff-only` in the `main/` worktree of every git-work managed repo. If pull fails, surface as workspace health warning requiring acknowledgement — do not fail hard. Continue with whatever state is present.

Context assembly:
1. **Pre-flight workspace check:** Scan direct child directories. git-work initialized (`.bare/` present) → will be discovered. Plain git clone (`.git/` present, no `.bare/`) → surface as health warning. Other directories → ignored.
2. **Discover repos:** Scan for `.bare/` subdirectories. For each: record Name, Path, MainDir, detect language/framework from manifest files, check for `AGENTS.md`, collect configured doc paths.
3. **Read workspace-root `AGENTS.md`:** This is the only file content read before planning agent starts.
4. **Build context bundle:** `PlanningContext` with WorkItem snapshot, WorkspaceAgentsMd, RepoPointers, SessionDraftPath.

**[NEW] Session directory:** Substrate generates a session ULID, creates `.substrate/sessions/<session-id>/` in the workspace root. Agent writes plan progressively to `SessionOpts.DraftPath` (`.substrate/sessions/<session-id>/plan-draft.md`).

**[NEW] Planning prompt template:** Includes workspace guidance (from AGENTS.md), work item details, repo list with language/framework and doc paths, and instructions to explore before finalizing and write to draft path incrementally. Plan format: fenced `substrate-plan` YAML block with `execution_groups`, followed by Orchestration section, then `## SubPlan: <repo-name>` sections.

**Plan parsing:** Read draft file. Find fenced `substrate-plan` YAML block. Parse `execution_groups: [][]string`. Flatten to declared repos. Validate: every declared repo matches a discovered repo name; every declared repo has a matching SubPlan section; no SubPlan sections for undeclared repos.

**[NEW] Automatic correction loop:** On parse errors or missing draft, send correction message to planning agent (same session — conversation continues, full history preserved). Retry up to `plan.max_parse_retries` (default 2). On exhaustion: emit `PlanFailed`, surface to human, work item returns to `Ingested`.

**Persist:** Build `Plan` + `SubPlan` domain objects. Assign `SubPlan.Order` from group index. Save via go-atomic transaction. Emit `PlanGenerated`. Session directory retained as audit trail.

**Gate:** 3-repo markdown → exactly 3 SubPlans. Missing headings → `ErrPlanParseFailed`. After 2 revisions, the same plan record has version 3. `go test ./internal/orchestrator/...`

## Phase 8: Implementation Orchestrator (Week 6-7)

**[UPDATED]** Sub-plan wave scheduling via `BuildWaves`: sub-plans with equal `Order` form a wave and run in parallel; waves execute sequentially. Worktree creation per sub-plan (emits `WorktreeCreating` pre-hook, then `WorktreeCreated` post-hook). Agent sessions spawned with sub-plan + cross-repo plan + docs. Independent sub-plans execute concurrently (`errgroup`). All events forwarded to bus.

**[NEW] Branch naming:** `sub-<externalID>-<short-slug>` where externalID is `WorkItem.ExternalID` (e.g., `LIN-FOO-123` or `MAN-001`) and slug is derived from work item title (lowercased, spaces→dashes, stripped to `[a-z0-9-]`, max 30 chars). Same branch name used in every repo touched by this work item.

**[NEW] Idempotency guards:**
- Worktree creation: check via `git-work list` before creating; skip if exists.
- MR creation: glab adapter checks if MR exists for branch before creating.

**[NEW] Build and test validation:** Each repo's `AGENTS.md` is the canonical source for validation instructions. Agent reads `AGENTS.md` at session start and runs whatever build, format, and test checks are specified. No separate validation command in `substrate.toml`.

**Gate:** `BuildWaves` with `Order` values `[0,0,1]` produces 2 waves: 2 parallel sub-plans then 1. Sub-plans in the same wave start within 100ms of each other. Wave 1 does not start until all wave 0 sub-plans reach `completed`. `go test ./internal/orchestrator/... -race`

## Phase 9: Foreman + Review Pipeline (Week 7-8)

**Foreman:** A persistent oh-my-pi harness session running for the duration of implementation. Started on `PlanApproved`, terminated when all sub-plans reach terminal state.

**[NEW] Two-tier resolution:**
1. **Foreman LLM answer:** Send question to persistent Foreman session (holds full context: plan + docs + prior Q&A). Foreman emits `foreman_proposed` event with answer and confidence marker.
   - `CONFIDENCE: high` → auto-answer, append to FAQ.
   - `CONFIDENCE: uncertain` → escalate to human.

2. **Human escalation:** Surface to TUI with Foreman's proposed answer pre-filled. Human may iterate (each message forwarded to Foreman via `SendMessage()`) until pressing `[A]pprove`. On approval, answer is forwarded to blocked sub-agent and appended to FAQ.

**[NEW] FAQ:** A `faq` section is appended to the live plan document (DB field, rendered in TUI, passed to review agents). Each entry: ID, PlanID, AgentSessionID, RepoName, Question, Answer, AnsweredBy, CreatedAt.

**[NEW] Foreman recovery:** If Foreman session dies while answering a question, re-queue the in-flight question and restart the session with current plan from DB (including FAQ) as system prompt. Re-queued question is delivered first via priority channel. Questions are serialized through single persistent session.

**Review:** on `SessionCompleted`, diff vs `main/`, spawn review agent (foreman mode, read-only tools). Agent explores worktree and forms own picture — orchestrator does not dump diff into prompt. Parse `CRITIQUE`/`END_CRITIQUE` blocks. Major/critical critiques → re-implementation. Cycle limit (default 3) → escalate. Post-review documentation staleness check.

**[NEW] Review correction loop:** If output neither contains `NO_CRITIQUES` nor valid `CRITIQUE` blocks, send correction message to review session (same session, full history). Retry up to `plan.max_parse_retries`. On exhaustion: treat as zero critiques, log warning.

**Gate:** Answerable question resolved without human. Unanswerable question escalated. 2 major critiques → re-implement → 0 major → done at round 2. 3 rounds of majors → `escalated`. `go test ./internal/orchestrator/... -race`

## Phase 9b: Resume & Recovery (Week 8)

**[UPDATED]** Instance lock table: `substrate_instances` table tracks running Substrate processes. Each instance inserts its row on startup, updates `last_heartbeat` every 5s, deletes on clean exit. Session ownership tracked via `agent_sessions.owner_instance_id`. On startup reconciliation: for any `running` session whose owner instance row is missing or has stale heartbeat (>15s), transition to `interrupted`. No PID-based crash detection; no PID reuse hazard.

**Resume protocol:** TUI shows interrupted sessions with `[R]esume [A]bandon`. Resume availability: session's `owner_instance_id` is NULL, owner row missing, heartbeat stale, or current instance is owner.

**[NEW]** On resume: update `agent_sessions.owner_instance_id` to current instance ID. Start fresh agent session in SAME worktree with context:
- Original sub-plan + orchestration context
- Last 50 lines from `~/.substrate/sessions/<interrupted-session-id>.log`
- Resume preamble: *"You are continuing work on this sub-plan. The worktree may contain partial changes from a previous session. Run `git status` and `git diff` to understand current state, then continue implementing remaining items."*

Old session stays in DB as `interrupted` (audit trail). New session links to same `SubPlan`. Emit `AgentSessionResumed { OldSessionID, NewSessionID, SubPlanID }`.

**Abandon:** Session status → `failed`. Human can reset worktree, manually fix, or remove worktree via `git-work remove`.

**Graceful shutdown:** On SIGINT/SIGTERM, mark all active sessions as `interrupted`, record `shutdown_at` timestamp, send SIGTERM to subprocesses, wait up to 10s, SIGKILL survivors, DELETE instance row from `substrate_instances`.

**Gate:** Launch two Substrate instances against the same workspace. Instance A starts a session. Kill instance A (simulating crash). Instance B detects stale heartbeat within 20s, marks session interrupted, offers Resume. Resumed session continues in the same worktree. Clean shutdown: instance row deleted, no false interrupts.

## Phase 10: Linear Adapter + Selection Model (Week 8)

GraphQL client (`net/http` + JSON). `Watch`: poll assigned issues, dedup, exponential backoff on 429. `Fetch`, `UpdateState` (maps `TrackerState` to Linear workflow state IDs from config), `AddComment`. Event hooks: `PlanApproved` → "In Progress", `WorkItemCompleted` → "Done".

**[NEW] ExternalID format:** `LIN-{teamKey}-{issueNumber}` (e.g., `LIN-FOO-123`). Team key from `issue.team.key`, issue number from identifier suffix.

**[NEW] Selection model** — `ListSelectable` and `Resolve` for all three scopes:

- `ScopeIssues`: GraphQL query for team issues with filtering. Select 1+ issues. `Resolve`: if 1 issue, WorkItem mirrors it; if N issues, aggregate with joined title (+N-1 more), concatenated descriptions, merged labels.
- `ScopeProjects`: GraphQL query for projects visible to team. Select 1+ projects. `Resolve` fetches all non-completed issues from each project, builds WorkItem with project context + full issue listing.
- `ScopeInitiatives`: GraphQL query for initiatives. Select exactly 1. `Resolve` fetches all child projects + their issues, builds comprehensive WorkItem with initiative goals, project breakdown, grouped issue details.

Each scope has its own GraphQL query and response parsing. `Capabilities()` returns `CanWatch: true, CanBrowse: true, CanMutate: true, BrowseScopes: [issues, projects, initiatives]`.

**Gate:** Correct GraphQL query construction for all three scopes. Parsed response -> valid `WorkItem`. Backoff: 429 -> delay >= 2x. Unit tests: `ListSelectable` returns correct items per scope, `Resolve` aggregates correctly for multi-issue selection, `Resolve` for project scope fetches all child issues. Integration (requires `SUBSTRATE_LINEAR_API_KEY`): browse real team issues, select, resolve into `WorkItem`, fetch + update round-trip. `go test ./internal/adapter/linear/...`

## Phase 10b: Manual Adapter (Week 8)

Implement `ManualAdapter` struct in `internal/adapter/manual/`. Lightweight adapter for ad-hoc work items not tracked in an external system.

- `Name()` returns `"manual"`.
- `Capabilities()` returns `CanWatch: false, CanBrowse: false, CanMutate: false, BrowseScopes: nil`.
- `ListSelectable` returns `ErrNotSupported`.
- `Resolve` takes `Selection` with `ManualInput` (title, description) and creates a `WorkItem` directly from user input.
- `Watch` returns a closed channel immediately.
- `Fetch`, `UpdateState`, `AddComment` are no-ops (return nil or zero values).
- `OnEvent` is a no-op.

**[NEW]** ExternalID format: `MAN-N` (incrementing sequence: `MAN-1`, `MAN-42`, `MAN-1000`). Counter derived by counting existing manual work items in DB for current workspace — no separate counter column. `ManualAdapter.store` is a `WorkspaceStore` (wraps `*sqlx.Tx` from enclosing `Transact`), so COUNT and subsequent Create share same transaction.

**[NEW]** No TOML configuration needed. Manual adapter is always available as built-in option, registered unconditionally at startup in `internal/app/wire.go`.

This is a small phase, likely 1-2 days of effort.

**Gate:** Unit tests: `Resolve` produces valid `WorkItem` from `ManualWorkItemInput`, `Watch` returns closed channel, `UpdateState` and `AddComment` are no-ops. `go test ./internal/adapter/manual/...`

## Phase 11: glab Adapter (Week 8-9)

Wraps `glab` CLI. Event-driven: `OnEvent(WorktreeCreatedEvent)`: `glab mr create --draft --source-branch ... --reviewer ... --label ...`, parse MR URL. `OnEvent(WorkItemCompletedEvent)`: `glab mr update --source-branch <branch> --draft=false` for each repo.

**[NEW] MR title:** Prefer `WorkItemTitle` from event payload. Fallback to title derived from branch slug (e.g., "sub-LIN-FOO-123-fix-auth-flow" → "Fix auth flow [LIN-FOO-123]").

**[NEW] Error policy:** glab failures log at WARN and **never block** the workflow. Users can always manage MRs manually.

**Gate:** WorktreeCreated event → OnEvent fires → MR created. JSON parsing from `mr view`. Integration (requires glab auth): event fires → MR created. `go test ./internal/adapter/glab/...`

## Phase 12: TUI (Week 9-11)

| Sub-phase | Scope | Gate |
|-----------|-------|------|
| 12a | Shell + two-pane layout + sidebar (session list with status icons) + header + status bar + New Session overlay | Dashboard renders list, navigation works, New Session flow completes |
| 12b | Content panel modes: Plan review (approve/request changes/edit in `$EDITOR`/reject) | Approve triggers `PlanApproved` event |
| 12c | Implementing mode (repo status row, output stream per repo, Tab cycling), Question sub-mode (Foreman proposed answer, human iteration) | Events render real-time, question escalation works |
| 12d | Reviewing mode (diff summaries, critiques with severity, per-repo tabs) + toast notifications | Critiques render, toast on escalation |
| 12e | Configuration overlay (view/edit TOML, validate on save, `$EDITOR` for complex blocks) | Changes persist without restart |
| 12f | First-start modal (global init + workspace init with repo discovery and warnings) | Modal displays on fresh install, workspace registers in DB |

**[NEW] Persistent two-pane layout:** Fixed-width (~26 char) session sidebar on left, dynamic content panel on right. No navigation stack — content panel re-renders in place based on selected session state.

**[NEW] Session sidebar status icons:**
- `●` running/active (green)
- `◐` pending human action (amber)
- `✓` completed (dim green)
- `⊘` interrupted (amber)
- `✗` failed (red)

**[NEW] Content panel modes:** Driven by `WorkItemState` plus `AgentSessionStatus` for sub-modes (question, interrupted). See `06-tui-design.md` §2c for mapping.

**[NEW] New Session overlay:**
- **Linear source:** Filterable issue list with multi-select (Space), scope dropdown (Issues/Projects/Initiatives).
- **Manual source:** Title (textinput) + Description (textarea) form.

**[NEW] Multi-instance support:**
- Instance registration in `substrate_instances` with heartbeat every 5s.
- Session ownership via `owner_instance_id`. Only owning instance can answer/resume/abandon.
- Dead owner (missing row or stale heartbeat >15s) → any instance may take over.
- Agent output tailed from session log file; TUI handles log rotation via inode/size detection.

**Gate:** Full walkthrough: launch → first-start modal → dashboard → select item → view plan → approve → see sessions → answer question → view review → completion. `go test ./internal/tui/...`

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
go test -tags=integration ./internal/adapter/ohmypi/...   # needs bun + omp creds
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
| Agent output log growth (unbounded log files for long sessions) | Low | Low | Rolling log segments during session: rotate at 10 MB threshold, compress previous segment. TUI tailing follows newest segment by tracking inode/size. |
| go-atomic SQLITE_BUSY retry not yet in isRetryable | Low | Low | **[RESOLVED - IMPLEMENTED]** go-atomic isRetryable extended in Phase 0 to include SQLITE_BUSY (5) and SQLITE_LOCKED (6). |
| Foreman context degrades gradually from Q&A history | Medium | Medium | Periodic compacted restart: after N questions (configurable, default 20), restart the Foreman session with a summarized FAQ as system prompt instead of full history. Note: compaction is disabled in the bridge (`compaction.enabled: false`), so Go-side restart with summary is the only mitigation. |
