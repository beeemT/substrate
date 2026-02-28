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
    windowSize  tea.WindowSize
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
│   2h ago · 12 commits  │                                                          │
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
- Line 3 (otherwise): `  {subtitle}` — e.g. "Plan review needed", "2h ago · 12 commits", "Interrupted"

**Keys:**
- `↑`/`↓` or `j`/`k` — navigate sessions
- `n` — open New Session overlay
- `c` — open Configuration overlay
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

| Session state     | Content panel mode      |
|-------------------|------------------------|
| `planning`        | Planning output        |
| `plan_review`     | Plan review            |
| `implementing`    | Implementing           |
| `reviewing`       | Review output + diff   |
| `completed`       | Completion summary     |
| `interrupted`     | Interruption notice    |
| `waiting_question`| Agent question         |

```go
type ContentModel struct {
    mode        ContentMode
    // per-mode sub-models
    planOutput  viewport.Model
    planReview  PlanReviewModel
    implementing ImplementingModel
    reviewing   ReviewModel
    completed   CompletedModel
    interrupted InterruptedModel
    question    QuestionModel
}
```

---

## 3. Content Panel Modes

### 3a. Planning Mode

Tails `~/.substrate/sessions/<id>.log` (JSONL) as the planning agent runs. New lines are appended in real time via `fsnotify`.

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
│ [a] Approve  [e] Edit in $EDITOR  [r] Reject with feedback  [↑↓] Scroll          │
```

**Model**: `viewport.Model` for scrollable content, `textinput.Model` for rejection feedback (appears at bottom on `r`, `Enter` submits, `Esc` cancels).

**Keys**: `↑`/`↓` scroll, `a` approve, `e` open in `$EDITOR` via `tea.ExecProcess`, `r` reject with feedback.

### 3c. Implementing Mode

Two parts: a repo status row at the top, and the output stream for the currently selected repo below.

```
│ LIN-FOO-123 · Fix auth flow · Implementing                                        │
│──────────────────────────────────────────────────────────────────────────────── │
│ Repos:  ✓ shared-lib (14 commits)   ● backend-api (running)   ◌ frontend (queued)│
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

type Critique struct {
    Severity   string  // "error", "warning", "info"
    File       string
    Line       int
    Message    string
    Suggestion string
}
```

**Keys**: `j`/`k` navigate critiques, `Enter` expand/collapse detail+diff, `Tab` switch repo tabs, `r` re-implement, `o` override accept (with confirm).

### 3e. Completed Mode

Summary of what was done: repos changed, total commits, MR/PR links, any stale documentation warnings.

```
│ LIN-FOO-100 · Update docs · Completed  ✓ 2h ago                                  │
│──────────────────────────────────────────────────────────────────────────────── │
│ Completed 2h ago                                                                  │
│                                                                                   │
│ Repos:                                                                            │
│   ✓ backend-api       8 commits  MR !142 (open)                                  │
│   ✓ frontend-app      4 commits  MR !87  (open)                                  │
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

Surfaced when the foreman agent (see `05-orchestration.md`) cannot resolve an agent question automatically and escalates to the human.

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
│ Your answer: ░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░  │
│                                                                                   │
│ [Enter] Send answer  [Esc] Cancel (agent continues without answer)                │
```

The answer is forwarded to the foreman agent, which relays it to the sub-agent session via stdin.

**Keys**: type answer, `Enter` send, `Esc` cancel (agent proceeds without an answer).

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

### 4b. Configuration Overlay

Accessed via `c` from anywhere. Renders as a modal over the current layout (or takes over the content panel on narrow terminals).

Settings editor for adapter configs, workspace root, and `substrate.toml` defaults.

**Model**: `[]ConfigSection` with `[]ConfigField` (key, value, kind: string/path/bool/enum). Supports inline editing for simple values. Complex TOML blocks launch `$EDITOR` via `tea.ExecProcess`.

**Keys**: `j`/`k` navigate, `Enter` edit inline, `e` open in `$EDITOR`, `s` save, `Esc` close (prompt if dirty).

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
- Critique severity: error=`Error`, warning=`Warning`, info=`Muted`
- Diffs: additions=`DiffAdd`, deletions=`DiffDel`
- Interrupted state: `Interrupted` fg

---

## 7. Multi-Instance Support

Multiple substrate instances can open the same workspace simultaneously. There are no exclusive locks. The global DB (`~/.substrate/state.db`) is the shared state source — both instances see the same sessions.

**Instance ownership**: The instance that starts an agent session owns the subprocess. Other instances see the session's state from the DB but cannot send inputs to a running subprocess they didn't start. The `[a]nswer` and `[r]esume` keybinds are only active when the current instance owns the session.

**Agent output**: All agent output is persisted to `~/.substrate/sessions/<session-id>.log` (JSONL, timestamped). Any instance can tail this file to render live or historical output in the content panel. The TUI uses `fsnotify` to detect new lines appended to the log, feeding them into the appropriate `viewport.Model` via a `tea.Cmd`.

```go
func tailSessionLogCmd(logPath string, since int64) tea.Cmd {
    return func() tea.Msg {
        // fsnotify watcher fires; read new bytes from offset `since`
        lines, nextOffset := readNewLines(logPath, since)
        return SessionLogLinesMsg{Lines: lines, NextOffset: nextOffset}
    }
}
```

After receiving `SessionLogLinesMsg`, the view re-subscribes with the updated offset. This pattern is the same one-event-per-Cmd loop used for streaming agent events.

**State visibility**: Session state transitions written to the DB are visible to all instances within a poll interval (default 2s). Running sessions show their current status without coordination.

**PID reconciliation** (unchanged): On startup, any session marked `running` whose PID is no longer alive is transitioned to `interrupted`. This is a startup-only check and does not prevent concurrent instances from operating.

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

`?` help overlay, `q` quit, `Esc` close overlay / cancel input, `n` new session overlay, `c` configuration overlay, `Ctrl+c` force quit.

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

**Ticks**: Spinners use `tea.Tick(100ms, ...)`. DB state polling uses `tea.Every(2s, ...)` to pick up state changes from other instances or background processes.

### Optimistic Updates

For near-certain outcomes (plan approval, session abandon):
1. `Update` sets new state in model immediately, saves previous for rollback.
2. Returns `tea.Batch(actionCmd, toastCmd("Plan approved", Success))`.
3. View renders immediately.
4. On `ErrMsg`, revert model state, show error toast.
