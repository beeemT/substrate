# 06 - TUI Design
<!-- docs:last-integrated-commit f6b8e6e5f8374bd4c2f467852266f01cc2f323a2 -->

bubbletea (Elm Architecture) with lipgloss styling and bubbles widgets. See `02-layered-architecture.md` for service integration and `03-event-system.md` for event bridging.

---

## 1. Framework

bubbletea still enforces `Msg -> Update(model, msg) -> (model', Cmd) -> View(model') -> terminal`, but the shipped TUI is organized around a small set of explicit shell models rather than a stacked navigation tree. The top-level app owns:

- a left sidebar pane
- a right content pane
- a single footer/status row
- centered overlays for the unified work browser, session-history search, workspace init, and help
- a full-screen settings page
- a toast stack rendered over the shell

`internal/tui/views/` owns Bubble Tea state, routing, and sizing decisions. Shared visual semantics, reusable chrome, and design-system ownership boundaries are summarized here where they affect runtime behavior, with `docs/08-tui-design-system.md` as the canonical design-system contract.

**Widgets**: viewport (plan review, agent output, historical session interaction, diffs), list (sidebar, search results, browser results), spinner (active ops), textinput (feedback, answers, filter), and table-like structured rows in settings and review surfaces.

---

## 2. Layout

### 2a. Persistent Two-Pane Layout

The main shell is always a pair of rounded panes plus a single footer row. There is no persistent header bar; workspace metadata and the count of truly active agent sessions live in the footer, and each pane renders its own centered title.

The default sidebar shows work-item overviews. The content pane renders the selected work item, selected task session, or selected historical result in place. Centered overlays sit above the shell without changing the underlying layout, while the settings page temporarily takes over the full screen.

```
┌────── Sessions ──────┐┌────────────── Content ───────────────────────────────────────┐
│ SUB-123              ││ SUB-123 · Design system                                       │
│ Semantic cleanup     ││ <mode-specific work item, task log, or history summary>       │
│ Waiting for answer   ││                                                                │
│                      ││                                                                │
│ SUB-118              ││                                                                │
│ Refresh docs         ││                                                                │
│ Completed            ││                                                                │
╰──────────────────────╯╰────────────────────────────────────────────────────────────────╯
[↑/↓] Sessions  [→] Tasks  [/] Search sessions  [n] New session  [c] Settings      workspace · 2 active sessions
```

### 2b. Sessions Sidebar

Fixed 34 characters wide. The default title is `Sessions`. Entries are **work-item overviews**, not a flat list of individual agent sessions. Each row summarizes the work item state plus the latest child-task metadata that should be visible at a glance: current status, repo progress, and whether the work item currently has an open question or interruption.

Press `→` on a selected work item to drill into a second sidebar pane titled `{externalID} · Tasks`. That pane contains the work-item overview row, an optional `Source details` row when the work item has non-manual source metadata, and the child agent-task sessions for that work item in sub-plan order. Selecting a task row opens that task's log in the content pane. Selecting `Source details` opens the source-details content mode. Press `←`/`Esc` from the task pane to return to the top-level sessions list; press `→` from the task pane to move focus into the content pane.

Historical search is separate from the live sidebar list: `/` opens the session-history overlay, which can search within the current workspace or across all workspaces and then open either the live work item or a historical interaction transcript/summary.

**Status icons:**
- `●` running/active (green)
- `◐` pending human action (amber) — plan review needed, open question, or similar attention state
- `✓` completed (dim green)
- `⊘` interrupted (amber)
- `✗` failed (red)
- `◌` inactive/default (muted)

**Entry layout** (three rendered lines plus a blank separator row):
- Line 1: `{icon} {external ID / repo / source prefix}`
- Line 2: `  {title}`
- Line 3: implementing work items show repo progress; other rows show a concise subtitle such as `Plan review needed`, `Waiting for answer`, `Source details`, or the task-session status

**Keys:**
- `↑`/`↓` or `j`/`k` — navigate sessions, task rows, or source details
- `→` — drill into tasks from the sessions pane, or move focus from the task pane to content
- `←`/`Esc` — return from content to tasks, or from tasks to sessions
- `d` — when a work item, task row, or historical result is selected, confirm deletion of the full work item and its related task/session artifacts
- `/` — open session-history search
- `n` — open the Unified Work Browser
- `c` — open Settings page
- `?` — open Help
- `q` — quit

```go
type SidebarModel struct {
    entries []SidebarEntry
    cursor  int
    title   string
    styles  styles.Styles
    width   int
    height  int
}
```

The footer, not a header, carries workspace context such as `workspace · 2 active sessions`.

### 2c. Content Panel

The content panel re-renders in place based on the selected work item, selected task row, selected source-details row, or selected historical search result. There is no navigation stack.

| Selection / state | Content panel mode |
|-------------------|--------------------|
| nothing selected | Empty |
| work item `ingested` | Ready to plan |
| work item `planning` | Planning output |
| selected `Source details` task row on an implementing work item | Source details |
| selected task-session row | Session interaction |
| selected remote or historical search result | Session interaction |
| work item `plan_review` | Plan review |
| work item `approved` | Awaiting implementation |
| work item `implementing` with open question | Question |
| work item `implementing` with interrupted child session | Interrupted |
| work item `implementing` otherwise | Implementing |
| work item `reviewing` | Reviewing |
| work item `completed` | Completed |
| work item `failed` | Failed |

`ContentModeSourceDetails` renders source metadata for the selected work item. `ContentModeSessionInteraction` is used for both live task drilldown and historical transcripts/summaries. `ContentModeQuestion` and `ContentModeInterrupted` are live implementation sub-modes selected from current task-session state and take precedence over source-details or repo-output views while they are active.

```go
type ContentMode int

const (
    ContentModeEmpty              ContentMode = iota // no session selected
    ContentModeReadyToPlan                           // ingested: work item ready for planning
    ContentModeSourceDetails                         // implementing: source metadata row selected
    ContentModePlanning                              // planning: agent running, log tailing
    ContentModeSessionInteraction                    // selected task log or historical transcript/summary
    ContentModePlanReview                            // plan_review: awaiting human review
    ContentModeAwaitingImpl                          // approved: plan approved, awaiting impl start
    ContentModeImplementing                          // implementing: agents running
    ContentModeReviewing                             // reviewing: review agent running
    ContentModeCompleted                             // completed: all repos passed review
    ContentModeFailed                                // failed: unrecoverable error
    ContentModeInterrupted                           // live implementation sub-mode
    ContentModeQuestion                              // waiting for human answer
}
```

---

## 3. Content Panel Modes

### 3a. Planning Mode

Reads incremental output from `~/.substrate/sessions/<session-id>.log` (JSONL) as the planning agent runs. New lines are appended into the viewport while the session is active.

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

### 3c. Session Interaction Mode

Used in two cases: (1) when the human selects a task-session row from the `{externalID} · Tasks` sidebar, and (2) when the human opens a historical result from session-history search.

For a selected task row, this mode tails `~/.substrate/sessions/<task-id>.log` live. The mode label becomes `Task`, and the header metadata includes the task status, harness name when known, and the task session ID. For a historical or remote result, the same surface loads the stored interaction transcript; when no session log exists, it falls back to a static summary instead of showing an empty viewport.

**Keys**: `↑`/`↓` scroll. Global back-navigation still applies from the footer (`←`/`Esc` back to tasks or sessions).

### 3d. Implementing Mode

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

### 3e. Reviewing Mode

Review summaries and critiques post-implementation, grouped by repository tabs at the top. The active repo shows either a success message (`✓ No critiques for this repo.`) or a critique list. The selected critique is indicated inline, and its suggestion is expanded in place rather than in a separate diff pane.

**Model**:
```go
type ReviewModel struct {
    repos      []RepoReviewResult  // each repo carries review cycles and critiques
    activeRepo int
    cursor     int                 // critique cursor within the active repo
}
```
`RepoReviewResult` carries the repo name plus accumulated `[]domain.ReviewCycle` and `[]domain.Critique`. Severity styling comes from shared semantic status styles, and the selected critique may show its `Suggestion` inline.

**Keys**: `j`/`k` navigate critiques in the active repo, `Tab` switch repo tabs, `r` re-implement, `o` override accept.

### 3f. Completed Mode

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

### 3g. Interrupted Mode

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

### 3h. Waiting for Human Question

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

### 4a. Session History Search Overlay

Triggered by `/` from the main shell. This is a centered split overlay with four focusable regions: scope, query input, results list, and preview.

When Substrate is inside a workspace, the default search scope is `workspace`; otherwise the overlay falls back to `global`. Typing in the search box requests a fresh history search immediately. Results are work-item-centric `SessionHistoryEntry` records ordered by most recent activity and enriched with latest task-session metadata, `AgentSessionCount`, `HasOpenQuestion`, and `HasInterrupted`. The preview pane shows work-item identity, workspace, latest repo/harness/status, timestamps, and delete/open hints for the current selection.

`Enter` opens the selected result. If the result belongs to the current workspace, the TUI restores the live work-item context. If it is historical or remote, the content pane switches to the session-interaction view and loads the stored transcript or static summary.

`d` from the results list opens a confirmation dialog to delete the full work item and related records. `Tab` / `Shift-Tab` cycle scope, input, results, and preview; `↑` / `↓` move between regions or results; `←` / `→` move focus or change scope; `Ctrl+S` toggles workspace/global; `Esc` closes.

### 4b. Unified Work Browser

Triggered by `n` from anywhere. This is the shipped replacement for the older provider-specific new-session modal. The browser is keyboard-first and capability-driven: the header always includes `Source` and `Scope`, and may add `View`, `State`, or a provider-specific status message when the active adapter combination supports them.

- **Sources**: `All`, `Linear`, `GitHub`, `GitLab`, `Sentry`, limited to providers with active browse adapters
- **Scope**: capability-driven; `All` is issue-only and never advertises shared project/initiative scopes it cannot support honestly
- **Search**: always shown as a text field; advanced filters (`Labels`, `Owner`, `Repo`, `Group`, `Team`) appear only when the active source/scope supports them
- **Details pane**: shows metadata plus rendered description for the currently highlighted work item
- **Selection model**: `Space` toggles multi-select, but every selected item must come from exactly one provider
- **Start action**: `Enter` starts a work item from the current selection; if nothing is selected yet, `Enter` first selects the highlighted row and then starts
- **Open in browser**: `Ctrl+O` opens the currently highlighted work item externally
- **Manual work item creation**: `Ctrl+N` switches to a two-field form (`Title`, `Description`). `Tab` moves title → description → back to the browser, and `Enter` submits through the `manual` adapter once the title is non-empty.

Container-scoped providers can intentionally hide inbox-style view controls. For example, Linear issue browsing may show a warning that view filters are hidden because browsing is team/container-scoped. Sentry stays issues-only and source-only; provider-specific Sentry browse and auth constraints are documented in `04-adapters.md` under `### Sentry`, while this document owns the shared UI behavior.

Common browser shortcuts: `Tab` / `Shift-Tab` cycle sources, `Ctrl+S` cycles scope, `Ctrl+V` cycles view when present, `Ctrl+T` cycles state when present, `Ctrl+R` clears filters, `Esc` closes.

### 4c. Settings Page

Accessed via `c` from anywhere. The settings UI is a full-screen page with a left navigation tree and a right detail/editor pane. It covers commit, planning, review, Foreman, harness, provider, and repo-lifecycle configuration, with provider status and field descriptions visible alongside editable values.

Provider secrets owned by Substrate are stored in the OS keychain, while the config file stores stable secret references such as `api_key_ref` and `token_ref`. Harness-owned credentials are handled through harness actions instead of being written directly by the TUI. oh-my-pi remains the default verified interactive harness; Claude Code and Codex are selectable but are not documented as having full interaction parity for every corrective workflow. Provider-specific Sentry auth, login, and connectivity-test behavior is documented in `04-adapters.md` under `### Sentry`, while this section owns the shared Settings interaction model.

The footer hints are focus-sensitive. In the tree view they expose navigation, expand/collapse, focus transfer, close, save, apply, test, login, and reveal actions. In the field view they expose field navigation, edit, boolean toggle, return-to-groups, save, apply, test, login, and reveal actions. While editing a field, the footer collapses to save/cancel hints only.

**Keys:**
- Tree focus: `↑`/`↓` navigate, `→` expand/open, `←` collapse/up, `Enter` focus settings, `Esc` close
- Field focus: `↑`/`↓` settings, `Enter` edit, `Space` toggle bool, `←`/`Esc` back to groups, `s` save, `a` apply, `t` test, `g` login, `r` reveal
- Edit mode: `Enter` save edit, `Esc` cancel edit

### 4d. First-Start Initialization Modal

Global initialization (creating `~/.substrate/`, `config.yaml`, `state.db`, `sessions/`) happens automatically on first CLI launch (see `07-implementation-plan.md` Phase 0). The TUI modal handles **workspace initialization** only.

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
│    • Detect git-work repos        (directories with .bare/)                 │
│    • Convert plain git repos      (child dirs with .git/)                   │
│    • Register workspace in        ~/.substrate/state.db                     │
│                                                                             │
│  git-work repos detected: backend-api/, frontend-app/                       │
│  Plain git repos to initialize: legacy-service/                             │
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

The shipped shell geometry is shared rather than redefined per view. The sidebar and content panes use the same pane chrome, centered overlays reuse a common overlay frame, and the settings page intentionally keeps its own full-screen split layout while speaking the same visual language.

Implementation-facing ownership for shared geometry, chrome primitives, and layout guardrails lives in `docs/08-tui-design-system.md`.

**Footer / status bar**

The main shell footer is a single borderless row. Its left side shows focus-sensitive key hints from the sidebar or content pane plus the global commands. Its right side shows workspace context and the count of truly active sessions. That count includes only agent sessions in `pending`, `running`, or `waiting_for_answer`; completed, failed, and interrupted sessions do not inflate the number.

**Toasts**

Toasts render as stacked top-right overlays anchored below the top inset and above the footer. They do not add rows to the main layout.

---

## 6. Visual Language

The TUI uses a muted, professional visual language built around semantic roles instead of per-view palette code. Status icons and badges use shared status semantics, selected rows use one selection language across panes and lists, question and interruption states use warning and interrupted treatment, and diffs use shared add/delete styling.

For token ownership, reusable primitives, and package boundaries, see `docs/08-tui-design-system.md`.

## 7. Multi-Instance Support

Multiple substrate instances can open the same workspace simultaneously. The global DB (`~/.substrate/state.db`) is the shared state source.

**Instance registration:** On startup each instance registers a row in `substrate_instances` (ULID, PID, hostname, last_heartbeat). A background goroutine updates `last_heartbeat` every 5 seconds. On clean shutdown the row is deleted. An instance is live if its `last_heartbeat` is within 15 seconds of the current time.

**Session ownership:** When an instance starts an agent session it writes its own `id` into `agent_sessions.owner_instance_id`. Only the owning instance can:
- Send messages / answers to the running subprocess
- Resume an interrupted session
- Trigger `[a]bandon`

If the owning instance is dead (row missing or heartbeat stale >15s), any other instance may take over: it updates `owner_instance_id` to its own ID and proceeds as if it were the original owner.

**Keybind gating:** `[a]nswer`, `[r]esume`, `[a]bandon` are active only when `currentInstanceOwnsSession || ownerIsDead`.

**Agent output:** All output is persisted to `~/.substrate/sessions/<session-id>.log` (JSONL). Any instance can tail this file from disk. The tailing logic handles log rotation: on detecting a size regression or inode change at the watched path, the offset is reset to 0 to follow the new segment.

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

`?` help overlay, `q` quit, `Esc` close overlay / cancel input, `n` unified work browser, `c` settings page, contextual `d` delete-session shortcut when a work item, task row, or history result is active, `Ctrl+c` force quit.

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

Destructive actions (delete session/work item, abandon, reject, override) show a modal overlay. Generic `ConfirmDialog` wraps a `tea.Cmd` as `onYes`. `y` confirms, `n`/`Esc` cancels.

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
