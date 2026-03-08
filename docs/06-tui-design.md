# 06 - TUI Design

bubbletea (Elm Architecture) with lipgloss styling and bubbles widgets. See `02-layered-architecture.md` for service integration, `03-event-system.md` for event bridging.

---

## 1. Framework

bubbletea enforces: `Msg -> Update(model, msg) -> (model', Cmd) -> View(model') -> terminal`.

```go
type App struct {
    sidebar     SidebarModel
    content     ContentModel
    overlay     tea.Model        // nil when no overlay is active
    header      HeaderModel
    statusBar   StatusBarModel
    toasts      ToastModel
    services    *service.Container
    windowSize  tea.WindowSizeMsg
}
```

The top-level `App` maintains a persistent two-pane layout: a fixed-width session sidebar on the left and a dynamic content panel on the right. Navigation between modes does not push/pop a stack — the content panel re-renders in place based on the selected session's state.

**Widgets**: viewport (plan review, agent output, diffs), list (sidebar sessions, issue picker), spinner (active ops), textinput (feedback, answers, filter), table (diffs, config), help (keybind overlay).

---

## 2. Layout

### 2a. Persistent Two-Pane Layout

The main chrome is always visible. The sidebar lists all work item sessions. The content panel fills the remainder of the terminal width and renders based on whichever session is selected and what state it is in.

```
┌─ Substrate ─ myproject ───────────────────────────────────────────────────────────┐
│ Sessions         F1    │ <dynamic content panel — fills remainder of width>       │
│──────────────────────  │                                                          │
│ ● LIN-FOO-123          │                                                          │
│   Fix auth flow        │                                                          │
│   ████░░ 2/3 repos     │                                                          │
│                        │                                                          │
│ ◐ LIN-FOO-456          │                                                          │
│   Rate limiting        │                                                          │
│   Plan review needed   │                                                          │
│                        │                                                          │
│ ✓ LIN-FOO-100          │                                                          │
│   Update docs          │                                                          │
│   2h ago               │                                                          │
│                        │                                                          │
│ ⊘ LIN-FOO-099          │                                                          │
│   Refactor billing     │                                                          │
│   Interrupted          │                                                          │
│                        │                                                          │
│ ──────────────────── │                                                          │
│ [n] New  [q] Quit      │                                                          │
└──────────────────────────────────────────────────────────────────────────────────┘
```

### 2b. Session Sidebar

Fixed ~26 characters wide. Lists all work item sessions, grouped by status (active first, then pending-action, then completed and interrupted last). Scrolls independently of the content panel when the list overflows.

**Status icons:**
- `●` running/active (green)
- `◐` pending human action (amber) — plan review needed, agent question, or interrupted awaiting decision
- `✓` completed (dim green)
- `⊘` interrupted (amber)
- `✗` failed (red)

**Entry layout** (two lines + optional progress bar):
- Line 1: `{icon} {workItemID}`
- Line 2: `  {short title}`
- Line 3 (implementing only): `  {progress bar} {n}/{m} repos`
- Line 3 (otherwise): `  {subtitle}` — e.g. "Plan review needed", "2h ago", "Interrupted"

**Keys:**
- `↑`/`↓` or `j`/`k` — navigate sessions
- `n` — open New Session overlay
- `c` — open Settings page
- `q` — quit

```go
type SidebarModel struct {
    sessions    []domain.SessionSummary
    cursor      int
    viewport    viewport.Model  // for overflow scrolling
}
```

**Workspace name** in the header comes from the `.substrate-workspace` YAML file (e.g., `myproject`).

### 2c. Content Panel

Renders based on the selected session's state. There is no navigation stack. The panel switches mode in place.

| WorkItem state    | Content panel mode      |
|-------------------|-------------------------|
| `ingested`        | Ready to plan           |
| `planning`        | Planning output         |
| `plan_review`     | Plan review             |
| `approved`        | Awaiting implementation |
| `implementing`    | Implementing            |
| `reviewing`       | Review output + diff    |
| `completed`       | Completion summary      |
| `failed`          | Failure detail          |

> Within `implementing`, the content panel switches to **Agent question** sub-mode when the active `AgentSession.Status` is `waiting_for_answer`, and to **Interruption notice** sub-mode when it is `interrupted`. These are driven by `AgentSessionStatus`, not `WorkItemState`.
```go
type ContentMode int

const (
    ContentModeEmpty        ContentMode = iota // no session selected
    ContentModeReadyToPlan                     // ingested: work item ready for planning
    ContentModePlanning                         // planning: agent running
    ContentModePlanReview                       // plan_review: awaiting human review
    ContentModeAwaitingImpl                     // approved: plan approved, awaiting implementation start
    ContentModeImplementing                     // implementing: agents running
    ContentModeReviewing                        // reviewing: review agent running
    ContentModeCompleted                        // completed: all repos passed review
    ContentModeFailed                           // failed: unrecoverable error
    ContentModeInterrupted                      // sub-mode of implementing: session interrupted
    ContentModeQuestion                         // sub-mode of implementing: waiting for human answer
)
```
```go
type ContentModel struct {
    mode        ContentMode
    // per-mode sub-models
    readyToPlan  viewport.Model   // ingested: static info panel
    awaitingImpl viewport.Model   // approved: static info panel
    planOutput   viewport.Model
    planReview  PlanReviewModel
    implementing ImplementingModel
    reviewing   ReviewModel
    completed   CompletedModel
    failed      FailedModel
    interrupted InterruptedModel
    question    QuestionModel
}
```

---

## 3. Content Panel Modes

### 3a. Planning Mode

Tails `~/.substrate/sessions/<session-id>.log` (JSONL) as the planning agent runs. New lines are appended in real time via `fsnotify`.

```
│ LIN-FOO-789 · Update docs · Planning                                              │
│──────────────────────────────────────────────────────────────────────────────── │
│ > Reading repository: backend-api...                                              │
│ > Reading repository: frontend-app...                                             │
│ > Analyzing cross-repo dependencies...                                            │
│ > Drafting sub-plan for backend-api...                                            │
│ ▌                                                                                 │
│                                                                                   │
│ [↑↓] Scroll  [p] Pause/unpause display                                            │
```

### 3b. Plan Review Mode

Full plan markdown rendered in a scrollable viewport. All sub-plans shown in sequence. The "Before marking complete" instruction appears in each sub-plan section.

```
│ LIN-FOO-456 · Rate limiting · Plan Review                                        │
│──────────────────────────────────────────────────────────────────────────────── │
│ ## Orchestration                                                                  │
│ Implement rate limiting across the stack. Execution:                              │
│   1. shared-lib (rate limiter primitives)                                         │
│   2. backend-api + frontend (parallel)                                            │
│                                                                                   │
│ ## SubPlan: shared-lib                                                            │
│ Add RateLimiter interface and token-bucket impl in pkg/ratelimit/...              │
│ Before marking complete: run formatters, compilation, and tests.                  │
│                                                                                   │
│ ## SubPlan: backend-api                                                           │
│ Wire RateLimiter to API gateway middleware...                                     │
│                                                                                   │
│ ─────────────────────────────────────────────────────────────────────────────── │
│ [a] Approve  [c] Request changes  [e] Edit in $EDITOR  [r] Reject  [↑↓] Scroll  │
```

**Model**: `viewport.Model` for scrollable content, `textinput.Model` for feedback input — used for both `c` (request changes) and `r` (reject); appears at bottom on activation, `Enter` submits, `Esc` cancels.

- `[a]` **Approve** — status → Approved; emits `PlanApproved`; triggers implementation pipeline.
- `[c]` **Request changes** — opens inline feedback input (textinput at bottom). On `Enter`, spawns a new planning agent session with the current plan text and feedback embedded in the prompt. The plan version is incremented on the revision.
- `[e]` **Edit in $EDITOR** — opens the plan markdown in `$EDITOR` via `tea.ExecProcess`. On editor exit, the modified file is read back and the plan is updated in the DB. Presents the revised plan for re-review.
- `[r]` **Reject** — opens inline rejection input. On `Enter`, work item returns to `Ingested` state; emits `PlanRejected`.

**Keys**: `↑`/`↓` scroll, `a` approve, `c` request changes, `e` open in `$EDITOR` via `tea.ExecProcess`, `r` reject.

### 3c. Implementing Mode

Two parts: a repo status row at the top, and the output stream for the currently selected repo below.

```
│ LIN-FOO-123 · Fix auth flow · Implementing                                        │
│──────────────────────────────────────────────────────────────────────────────── │
│ Repos:  ✓ shared-lib   ● backend-api (running)   ◌ frontend (queued)              │
│                                                                                   │
│ ─── backend-api ──────────────────────────────────────────────────────────────── │
│ > Analyzing auth middleware in internal/auth/handler.go...                        │
│ > Implementing JWT validation...                                                  │
│ > Running tests... PASS (17/17)                                                   │
│ > Committing: "fix(auth): add JWT validation middleware"                          │
│                                                                                   │
│ [Tab] Cycle repos  [↑↓] Scroll output  [p] Pause/unpause display                 │
```

**Repo status icons**: `✓` done, `●` running, `◌` queued, `⊘` interrupted, `✗` failed.

**Progress bar** in the sidebar entry reflects `done/total` repos.

Output for each repo is tailed from `~/.substrate/sessions/<session-id>.log` filtered by repo, or a per-repo log segment. `Tab` cycles focus across repos.

**Model**:
```go
type ImplementingModel struct {
    repos       []RepoProgress
    selectedRepo int
    outputs     map[string]*viewport.Model  // keyed by repo name
    paused      bool
}
```

**Keys**: `Tab` cycle repos, `↑`/`↓` scroll output, `p` pause/unpause live tailing.

### 3d. Reviewing Mode

Diff summaries and review agent critiques post-implementation. Per-repo tabs at top.

**Model**:
```go
type ReviewModel struct {
    repos      []RepoReviewResult  // per-repo: FilesChanged, Insertions, Deletions
    critiques  list.Model          // expandable critique list
    diffView   viewport.Model      // expanded diff for selected critique
    activeRepo int
}

// Critique: use domain.Critique from 01-domain-model.md.
// Rendering maps: FilePath → display path, Description → message text,
// LineNumber → optional line indicator, Suggestion → optional fix hint.
// Severity constants (CritiqueSeverityCritical etc.) map to theme colors.
```

**Keys**: `j`/`k` navigate critiques, `Enter` expand/collapse detail+diff, `Tab` switch repo tabs, `r` re-implement, `o` override accept (with confirm).

### 3e. Completed Mode

Summary of what was done: repos changed, MR/PR links, any stale documentation warnings.

```
│ LIN-FOO-100 · Update docs · Completed  ✓ 2h ago                                  │
│──────────────────────────────────────────────────────────────────────────────── │
│ Completed 2h ago                                                                  │
│                                                                                   │
│ Repos:                                                                            │
│   ✓ backend-api       MR !142 (open)                                              │
│   ✓ frontend-app      MR !87  (open)                                              │
│                                                                                   │
│ [↑↓] Scroll                                                                       │
```

### 3f. Interrupted Mode

Shown when an agent session exited unexpectedly (substrate closed, process killed, crash). Partial worktree changes may exist.

```
│ LIN-FOO-099 · Refactor billing · Interrupted                                      │
│──────────────────────────────────────────────────────────────────────────────── │
│ ⊘ Session interrupted (substrate closed while agent was running)                 │
│                                                                                   │
│ backend-api: partial changes in worktree sub-LIN-FOO-099-refactor-billing         │
│ Run `git status` in the worktree to inspect state.                                │
│                                                                                   │
│ Resume will start a new agent session in the same worktree with context about     │
│ the interruption and the original sub-plan.                                       │
│                                                                                   │
│ [r] Resume  [a] Abandon (mark failed)  [↑↓] Scroll                               │
```

Resuming starts a fresh agent session in the same worktree. The session context instructs the agent to inspect `git diff` and `git status` and continue from where the previous session left off.

**Keys**: `r` resume, `a` abandon (confirm dialog), `↑`/`↓` scroll.

### 3g. Waiting for Human Question

Surfaced when the Foreman agent escalates to the human (Tier 3). The human sees the sub-agent's question, the Foreman's proposed answer pre-filled (highlighted, read-only), and the Foreman's stated uncertainty. The human may:
- Press `[A]` to approve the Foreman's answer directly — it is forwarded to the blocked sub-agent and appended to the FAQ.
- Type a reply and press `[Enter]` — the message is sent to the Foreman session via `SendMessage()`, producing a refined `foreman_proposed` event which updates the pre-filled answer. This loop continues until the human presses `[A]`.
- Press `[Esc]` to skip — the question is forwarded without an answer; the sub-agent continues and may make its own decision.

```
│ LIN-FOO-123 · Fix auth flow · Implementing  ◐ Question from backend-api agent    │
│──────────────────────────────────────────────────────────────────────────────── │
│ Agent question (backend-api, session 3/3):                                        │
│                                                                                   │
│ "The existing auth middleware uses a custom JWT library (github.com/corp/jwtlib)  │
│  but the task says to use standard library. Should I replace the dependency or    │
│  wrap it?"                                                                        │
│                                                                                   │
│ Context: Sub-plan says 'use standard library JWT validation'. Current code uses   │
│ github.com/corp/jwtlib v2.3.1 in 4 files.                                        │
│                                                                                   │
│ Foreman's proposed answer (uncertain):                                            │
│ ┌──────────────────────────────────────────────────────────────────────────────┐ │
│ │ Replace the dependency. The orchestration plan specifies standard library     │ │
│ │ JWT only. I'm uncertain whether corp/jwtlib has specific behaviour your       │ │
│ │ team relies on — check with your team if unsure.                             │ │
│ └──────────────────────────────────────────────────────────────────────────────┘ │
│                                                                                   │
│ Your reply (or press [A] to approve Foreman's answer):                            │
│ ░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░  │
│                                                                                   │
│ [Enter] Send to Foreman  [A] Approve & forward to agent  [Esc] Skip               │
```

**Model**:
```go
type QuestionModel struct {
    question        domain.Question
    foremanProposed string          // Foreman's current proposed answer; updated on each foreman_proposed event
    foremanUncertain bool           // set from foreman_proposed event's `uncertain` field (CONFIDENCE: uncertain marker)
    input           textinput.Model // human reply input
    inputActive     bool
}
```

**Keys**: `[A]` approve (capital A), `[Enter]` send to Foreman, `[Esc]` skip.

---

## 4. Overlays

### 4a. New Session Overlay

Triggered by `n` from anywhere. A modal over the current layout.

**Linear source** — shows a filterable issue list:

```
┌─ New Session ────────────────────────────────────────────────────────┐
│                                                                      │
│  Source: [ Linear ▼ ]                                                │
│                                                                      │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │ Filter: ░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░          │   │
│  │──────────────────────────────────────────────────────────── │   │
│  │ ● LIN-FOO-124  Add OAuth2 provider support            High  │   │
│  │   LIN-FOO-125  Fix pagination in list endpoints       Med   │   │
│  │   LIN-FOO-126  Improve error messages                 Low   │   │
│  │   LIN-FOO-127  Database connection pooling            High  │   │
│  └──────────────────────────────────────────────────────────────┘   │
│                                                                      │
│  [Enter] Start session  [Space] Multi-select  [Esc] Cancel           │
└──────────────────────────────────────────────────────────────────────┘
```

**Manual source** — shows a text form instead of a list:

```
┌─ New Session ────────────────────────────────────────────────────────┐
│                                                                      │
│  Source: [ Manual ▼ ]                                                │
│                                                                      │
│  Title:       [                                               ]      │
│  Description: [                                               ]      │
│               [                                               ]      │
│                                                                      │
│  [Tab] Next field  [Enter] Start session  [Esc] Cancel               │
└──────────────────────────────────────────────────────────────────────┘
```

**Model**:
```go
type NewSessionOverlay struct {
    source        SourceKind        // Linear | Manual
    filter        textinput.Model
    issueList     list.Model        // Linear only; filterable, multi-select
    manualTitle   textinput.Model   // Manual only
    manualDesc    textarea.Model    // Manual only
    selectedItems []domain.SelectableItem
    focusIndex    int
}
```

**Keys (Linear)**: `↑`/`↓` navigate, `Space` toggle selection, `/` focus filter, `Tab` cycle source dropdown, `Enter` start, `Esc` cancel.

**Keys (Manual)**: `Tab`/`S-Tab` cycle fields, `Enter` on last field starts, `Esc` cancel.

### 4b. Settings Page

Accessed via `c` from anywhere. Renders as a full-screen settings page rather than the legacy configuration modal.

Settings are organized into sections for commit/planning/review/Foreman behavior, harness routing, harness configuration, provider configuration, and repo overrides.

Provider secrets owned by Substrate are stored in the OS keychain. The config file stores stable secret references such as `api_key_ref` / `token_ref`, while runtime hydration loads the actual secret values before adapters are built. GitHub still supports `gh auth token` fallback when no keychain-backed token is configured.

Harness-owned credentials are handled by the relevant harness via structured harness actions rather than being persisted directly by the TUI. The settings page must make the current maturity boundary clear: oh-my-pi is the default verified interactive harness, while Claude Code and Codex are selectable but may lack proven `SendMessage` parity for planning/review correction and Foreman handling.

**Model**: typed settings sections/fields with secret-aware rendering, inline editing, provider status, save/apply actions, connection tests, and harness-driven login actions. Harness routing fields include default harness, fallback order, and per-phase overrides.

**Keys**: `j`/`k` navigate fields, `h`/`l` switch sections, `Enter` edit, `Space` toggle booleans, `r` reveal/hide secrets, `s` save, `a` apply, `t` test provider connection, `g` run provider login through the relevant harness, `Esc` close.


### 4c. First-Start Initialization Modal

Global initialization (creating `~/.substrate/`, `config.toml`, `state.db`, `sessions/`) happens automatically on first CLI launch (see `07-implementation-plan.md` Phase 0). The TUI modal handles **workspace initialization** only.

When Substrate launches and the current directory is not a registered workspace, the Workspace Initialization Modal is shown:

```
┌─ Initialize Workspace ──────────────────────────────────────────────────────┐
│                                                                             │
│  No workspace found at:                                                     │
│    ~/myproject/                                                             │
│                                                                             │
│  Initialize this directory as a Substrate workspace?                        │
│                                                                             │
│  This will:                                                                 │
│    • Create .substrate-workspace  (workspace identity file)                 │
│    • Scan for git-work repos      (directories with .bare/)                 │
│    • Warn about plain git clones  (require gw init conversion)              │
│    • Register workspace in        ~/.substrate/state.db                     │
│                                                                             │
│  git-work repos detected: backend-api/, frontend-app/                      │
│  Plain git clones (need conversion): legacy-service/                       │
│                                                                             │
│  [y] Initialize  [n] Cancel                                                 │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

**Model:**
```go
type WorkspaceInitModal struct {
    cwd      string
    detected []RepoPointer      // discovered git-work repos
    warnings []string           // plain git clones
}
```

Note: Global init is handled automatically by CLI bootstrap before TUI starts. The modal only handles workspace initialization.

**Workspace init on `[y]`:** calls `substrate.InitWorkspace(cwd)` which:
1. Creates `.substrate-workspace` (YAML: ULID, name from dir basename, created_at).
2. Inserts workspace record into DB.
3. Returns discovered repos and warnings.

If `[n]` is pressed, Substrate exits cleanly.

**Keys:** `y` / `Enter` confirm, `n` / `Esc` cancel.
---

## 5. Layout System

```go
func (a App) View() string {
    header    := a.header.View(workspaceName, a.windowSize.Width)
    sidebar   := a.sidebar.View(a.windowSize.Height - 4)
    content   := a.content.View(a.windowSize.Width - sidebarWidth - 1, a.windowSize.Height - 4)
    statusBar := a.statusBar.View(a.content.KeybindHints(), a.windowSize.Width)

    body := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, divider, content)

    rendered := lipgloss.JoinVertical(lipgloss.Left, header, body, statusBar)
    if a.overlay != nil {
        rendered = renderOverlay(rendered, a.overlay.View(), a.windowSize)
    }
    return rendered
}
```

**Header** (1 line): Left-aligned `Substrate ─ {workspace}`. Right-aligned phase badge with status color. Workspace name read from `.substrate-workspace`.

**Sidebar** (fixed ~26 chars wide): Always visible. Scrolls independently.

**Content panel** (fills remainder): Re-renders in place on mode change.

**Status Bar** (1 line): Context-sensitive keybinds from the active content panel mode. Bracket letter styled with accent color. Right-aligned metadata (e.g., session count).

**Toasts**: Transient overlays, bottom-right. Auto-dismiss after 3s or on keypress. Levels: Info, Success, Warning, Error. Examples: "MR created for backend-api", "Agent session failed: timeout".

```go
type Toast struct {
    Message string
    Level   ToastLevel
    Expires time.Time
}
```

---

## 6. Color Scheme

Muted, professional palette (Linear aesthetic). Central `Theme` struct for future customization.

```go
var DefaultTheme = Theme{
    // Chrome
    HeaderBg:      "#1a1a2e",  HeaderFg:      "#e0e0e0",
    StatusBarBg:   "#16213e",  StatusBarFg:    "#a0a0a0",
    KeybindAccent: "#5b8def",
    // Status
    Pending:     "#6b7280",  // dim gray
    Active:      "#5b8def",  // blue
    Success:     "#34d399",  // green
    Error:       "#f87171",  // red
    Warning:     "#fbbf24",  // amber
    Interrupted: "#f59e0b",  // amber (same as pending-action)
    // Content
    Title: "#f0f0f0", Subtitle: "#b0b0b0", Muted: "#6b7280",
    Border: "#2d2d44", SelectedBg: "#1e293b",
    // Diff + Plan
    DiffAdd: "#34d399", DiffDel: "#f87171", CodeBlockBg: "#0f0f1a",
}
```

**Application rules**:
- Status badges: status color as foreground, transparent bg
- Selected items: `SelectedBg` background, no fg override
- Borders: `Border` color, rounded style
- Progress bars: `Muted` (incomplete), `Active` (in progress), `Success` (done)
- Agent questions: `Warning` fg + bold
- Critique severity: critical=`Error`, major=`Warning`, minor=`Muted`, nit=`Muted` (extra dim)
- Diffs: additions=`DiffAdd`, deletions=`DiffDel`
- Interrupted state: `Interrupted` fg

---

## 7. Multi-Instance Support

Multiple substrate instances can open the same workspace simultaneously. The global DB (`~/.substrate/state.db`) is the shared state source.

**Instance registration:** On startup each instance registers a row in `substrate_instances` (ULID, PID, hostname, last_heartbeat). A background goroutine updates `last_heartbeat` every 5 seconds. On clean shutdown the row is deleted. An instance is live if its `last_heartbeat` is within 15 seconds of the current time.

**Session ownership:** When an instance starts an agent session it writes its own `id` into `agent_sessions.owner_instance_id`. Only the owning instance can:
- Send messages / answers to the running subprocess
- Resume an interrupted session
- Trigger `[a]bandon`

If the owning instance is dead (row missing or heartbeat stale >15s), any other instance may take over: it updates `owner_instance_id` to its own ID and proceeds as if it were the original owner.

**Keybind gating:** `[a]nswer`, `[r]esume`, `[a]bandon` are active only when `currentInstanceOwnsSession || ownerIsDead`.

**Agent output:** All output is persisted to `~/.substrate/sessions/<session-id>.log` (JSONL). Any instance can tail this file via `fsnotify`. The tailing logic handles log rotation: on detecting a size regression or inode change at the watched path, the offset is reset to 0 to follow the new segment.

**State visibility:** Session state changes in the DB are visible to all instances within a poll interval (2s).
---

## 8. Interaction Model

### Navigation

Vim-style primary, arrow keys as aliases.

| Key | Action | Scope |
|-----|--------|-------|
| `j`/`k` or `↑`/`↓` | Navigate / scroll | Sidebar, lists, viewport |
| `Tab` | Cycle repos / panels | Implementing mode |
| `g`/`G` | Top/bottom | Lists |
| `Enter` | Select / confirm | Lists, overlays |

### Global Keybinds (handled before delegation)

`?` help overlay, `q` quit, `Esc` close overlay / cancel input, `n` new session overlay, `c` settings page, `Ctrl+c` force quit.

### Input Modes

Two modes: **Normal** (keypresses = commands) and **Input** (keypresses go to textinput widget). Input mode entered by explicit action (feedback, answer, filter). Exited via `Enter` (submit) or `Esc` (cancel).

```go
// In any model with input mode:
if v.inputActive {
    switch key.Type {
    case tea.KeyEnter:
        v.inputActive = false
        return v, submitCmd(v.feedback.Value())
    case tea.KeyEsc:
        v.inputActive = false
        return v, nil
    }
    v.feedback, cmd = v.feedback.Update(msg)
    return v, cmd
}
// ...normal keybind handling
```

### Confirmation Dialogs

Destructive actions (abandon, reject, override) show a modal overlay. Generic `ConfirmDialog` wraps a `tea.Cmd` as `onYes`. `y` confirms, `n`/`Esc` cancels.

---

## 9. Async State Management

bubbletea is single-threaded. All async work flows through `tea.Cmd` -> `tea.Msg`.

### Patterns

**One-shot** (DB queries, subprocess calls):
```go
func fetchSessionsCmd(svc *service.SessionService) tea.Cmd {
    return func() tea.Msg {
        sessions, err := svc.List(context.Background())
        if err != nil { return ErrMsg{Err: err} }
        return SessionsLoadedMsg{Sessions: sessions}
    }
}
```

**Streaming** (agent session log tailing): The TUI reads one batch of new lines per Cmd, then re-subscribes for the next:
```go
func (v *ContentModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case SessionLogLinesMsg:
        v.appendLines(msg.Lines)
        if !v.paused {
            v.viewport.GotoBottom()
        }
        return v, tailSessionLogCmd(v.logPath, msg.NextOffset)
    case AgentSessionEndedMsg:
        v.markComplete(msg.SessionID)
        return v, nil
    }
}
```

```go
// tailSessionLogCmd tracks inode changes to handle log rotation.
// If the file at logPath is smaller than `since` (rotation occurred),
// the offset resets to 0 and reading resumes from the new segment start.
func tailSessionLogCmd(logPath string, since int64) tea.Cmd {
    return func() tea.Msg {
        stat, err := os.Stat(logPath)
        if err != nil {
            return SessionLogLinesMsg{Lines: nil, NextOffset: since}
        }
        if stat.Size() < since {
            since = 0 // file rotated; restart from beginning of new segment
        }
        lines, nextOffset := readNewLines(logPath, since)
        return SessionLogLinesMsg{Lines: lines, NextOffset: nextOffset}
    }
}
```

**Ticks**: Spinners use `tea.Tick(100ms, ...)`. DB state polling uses `tea.Every(2s, ...)` to pick up state changes from other instances or background processes.

### Optimistic Updates

For near-certain outcomes (plan approval, session abandon):
1. `Update` sets new state in model immediately, saves previous for rollback.
2. Returns `tea.Batch(actionCmd, toastCmd("Plan approved", Success))`.
3. View renders immediately.
4. On `ErrMsg`, revert model state, show error toast.
