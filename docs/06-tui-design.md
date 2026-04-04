# 06 - TUI Design
<!-- docs:last-integrated-commit a38128010038776df783ec0bdf305b2637b5603e -->
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
[↑/↓] Sessions  [→] Tasks  [/] Search sessions  [n] New session  [s] Settings      workspace · 2 active sessions
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
- `a` — open the Add Repository overlay for browsing and cloning remote repositories
- `s` — open Settings page
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

**Filters, grouping, and sort.** The sidebar supports filter modes (All, Active, Needs Attention, Completed), grouping dimensions (flat, by status, by source), and sort direction (ascending/descending). `Ctrl+F` cycles filter mode, `Ctrl+G` cycles grouping dimension, `Ctrl+D` toggles sort direction. A status line below the title shows active filter/dimension/direction when non-default. A custom scrollbar renders on the right edge of the sidebar.

The footer, not a header, carries workspace context such as `workspace · 2 active sessions`.

### 2c. Content Panel

The content panel re-renders in place based on the current selection. There is no navigation stack.

| Selection / state | Content panel mode |
|-------------------|--------------------|
| nothing selected | Empty |
| work item selected (any state) | Overview |
| selected `Source details` task row | Source details |
| work item `planning` (planning child session selected) | Planning |
| selected task-session row or historical search result | Session interaction |

The content panel has five modes:

```go
type ContentMode int

const (
    ContentModeEmpty              ContentMode = iota // no session selected
    ContentModeOverview                              // canonical root-session overview/control surface
    ContentModeSourceDetails                         // task-pane source metadata for the selected work item
    ContentModePlanning                              // planning/task session log tailing
    ContentModeSessionInteraction                    // historical or task session interaction view
)
```

`ContentModeOverview` is the default when a work item is selected — it handles all root states (ingested, planning, plan_review, approved, implementing, reviewing, completed, failed). When the session is blocked on a human action, the overview surfaces that action inline or through an overlay. The operator never has to navigate to a state-specific page to unblock progress.

`ContentModeSourceDetails` renders source metadata for the selected work item. `ContentModeSessionInteraction` is used for both live task drilldown and historical transcripts/summaries. `ContentModePlanning` renders the live planning transcript while a planning child session is active.

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

Full reconstructed plan document rendered in a scrollable viewport. The review surface shows the derived `substrate-plan` YAML block, the orchestrator section, and every repo sub-plan in order so the human can review and edit the entire implementation contract in one place.

```
│ LIN-FOO-456 · Rate limiting · Plan Review                                        │
│──────────────────────────────────────────────────────────────────────────────── │
│ ```substrate-plan                                                                │
│ execution_groups:                                                                │
│   - [shared-lib]                                                                 │
│   - [backend-api, frontend]                                                      │
│ ```                                                                               │
│                                                                                   │
│ ## Orchestration                                                                  │
│ Coordinate the shared contract and execution order.                               │
│                                                                                   │
│ ## SubPlan: shared-lib                                                            │
│ ### Goal                                                                          │
│ Ship rate limiter primitives.                                                     │
│ ### Scope                                                                         │
│ - pkg/ratelimit/...                                                               │
│                                                                                   │
│ ─────────────────────────────────────────────────────────────────────────────── │
│ [a] Approve  [c] Request changes  [e] Edit in $EDITOR  [r] Reject  [↑↓] Scroll  │
```

**Model**: `viewport.Model` for scrollable content, `textinput.Model` for feedback input — used for both `c` (request changes) and `r` (reject); appears at bottom on activation, `Enter` submits, `Esc` cancels.

- `[a]` **Approve** — status → Approved; emits `PlanApproved`; triggers implementation pipeline.
- `[c]` **Changes** — opens the plan overlay with the current full plan document and an inline feedback input. On `Enter`, spawns a new planning agent session with the current full plan document and feedback embedded in the prompt. The plan version is incremented on the revision.
- `[e]` **Edit in $EDITOR** — opens the full reconstructed plan markdown in `$EDITOR` via `tea.ExecProcess`. On editor exit, the modified file is re-parsed, re-validated, and both orchestrator/sub-plan sections are updated in the DB before presenting the revised plan for re-review.
- `[r]` **Reject** — opens inline rejection input. On `Enter`, work item returns to `Ingested` state; emits `PlanRejected`.
- `[i]` **Inspect** — opens the full reconstructed plan document in an overlay for read-only inspection without changing plan state. Also available from planning sessions and completed work items to inspect the current plan.
**Keys**: `↑`/`↓` scroll, `a` approve, `c` changes, `e` open in `$EDITOR` via `tea.ExecProcess`, `i` inspect plan, `r` reject.

### 3c. Session Interaction Mode

Used in two cases: (1) when the human selects a task-session row from the `{externalID} · Tasks` sidebar, and (2) when the human opens a historical result from session-history search.

For a selected task row, this mode tails `~/.substrate/sessions/<task-id>.log` live. The mode label becomes `Task`, and the header metadata includes the task status, harness name when known, and the task session ID. For a historical or remote result, the same surface loads the stored interaction transcript; when no session log exists, it falls back to a static summary instead of showing an empty viewport.

#### Steering & Follow-Up

The `p` key activates a text input at the bottom of the session interaction view. Its behavior depends on the state of the viewed session:

| Session state | Hint text | Enter action | Effect |
|---------------|-----------|--------------|--------|
| Running | `Prompt agent` | Sends a steer message routed through `SessionRegistry.Steer()` | Interrupts the agent's active streaming turn with the operator's guidance |
| Completed | `Changes` | Sends a follow-up message for the completed task | Opens the plan overlay with request-changes input for follow-up re-planning |
| Failed | `Changes` | Sends a follow-up message for the failed task | Same as completed follow-up — creates a new Task row and attempts OMP session resume |
| No session | Disabled | — | `p` is a no-op when no session is active |

The steer/follow-up input is mutually exclusive: only one of the three session-state targets (live, completed, or failed) can be active at a time. `Esc` cancels and returns to normal mode.

**Keys**: `↑`/`↓` scroll, `p` steer or follow up (context-dependent). Global back-navigation still applies from the footer (`←`/`Esc` back to tasks or sessions).

### 3d. Overview Mode

The overview is the canonical root-session control surface. It is rendered when the operator selects a work item in the main `Sessions` sidebar or selects the `Overview` row in the `{externalID} · Tasks` sidebar. Both entry points render the same overview.

**Page structure** (in order):

1. **Summary** — external ID, title, state label, last updated, repo progress, blocker badges
2. **Action required** — only when the session is blocked on the operator (plan approval, open question, interrupted session, review)
3. **Source** — provider, ref, title, excerpt for source items
4. **Plan** — bounded plan snapshot (state, version, repo count, excerpt), never the full plan inline
5. **Tasks** — repo/sub-plan rows with status, harness, last activity, notes
6. **External lifecycle** — tracker refs and PR/MR rows from `ReviewArtifact` events
7. **Recent activity** — compact recent-event summary

**Actionability contract**: if the session needs a human decision to proceed, the overview provides the blocking reason, enough context, and the action itself. Detail views remain useful but are never mandatory to unblock work.

**Overview vs overlay split**: the overview shows decision summary; overlays show decision evidence. Actions are invokable from either place.

**View model**:

```go
type SessionOverviewData struct {
    WorkItemID string
    State      domain.SessionState
    Header     OverviewHeader
    Actions    []OverviewActionCard
    Sources    []OverviewSourceItem
    Plan       OverviewPlan
    Tasks      []OverviewTaskRow
    External   OverviewExternalLifecycle
    Activity   []OverviewActivityItem
}
```

`SessionOverviewModel` embeds `PlanReviewModel`, `QuestionModel`, `InterruptedModel`, `CompletedModel`, and `ReviewModel` as overlay sub-models. The overview opens these overlays for deeper inspection and action without forcing a content mode switch.

**Action-required examples**:

- **Plan review**: bounded plan excerpt plus `Approve` / `Changes` / `Inspect` / `Reject` actions. A `Review plan` overlay shows the full plan document.
- **Open question**: question text, affected repo/task, Foreman's proposed answer and uncertainty. The human can approve, iterate with the Foreman, or skip — all from the overview.
- **Interrupted session**: affected repo/task, failure/interruption reason, `Resume` / `Retry` actions.
- **Under review**: review summary, critique list per repo, `Override accept` action for human escalation, `Re-implement` action for manual re-trigger when `AutoFeedbackLoop` is disabled. See `05-orchestration.md` for the orchestrator-owned review loop.
- **Completed**: the `CompletedModel` overlay provides a `f` keybind that opens a feedback input for requesting changes. `Enter` submits the feedback and opens the plan overlay with the changes input, where the existing plan review flow takes over. Success and error feedback display as toasts.

**Source section rules**: for single-source sessions, the root work item title/description is sufficient. For multi-source sessions, the overview shows provider + ref only rather than reverse-parsing merged descriptions. Durable per-source-item summaries are a follow-up (see `future-work.md`).

**Plan section behavior by state**:

| Root state | Plan display |
|------------|-------------|
| `ingested` | `No plan yet` |
| `planning` | `Plan in progress` with bounded draft preview |
| `plan_review` | Excerpt + version + review actions |
| `approved` through `completed` | Approved/final plan snapshot |
| `failed` | Last known plan snapshot if any |

**Keys**: `↑`/`↓` scroll, action-specific keys from action cards, `Enter` to open overlays, `f` follow-up re-planning (completed work items), `o` open review artifacts / override accept (under review).

### 3e. Transcript Rendering

Planning mode, session interaction mode, and overview overlays all share one canonical transcript renderer (`RenderTranscript` in `session_transcript.go`). The renderer consumes structured `sessionlog.Entry` values end-to-end — there is no string pre-flattening step in the TUI pipeline. Non-event JSON lines (e.g. `session_meta` harness bookkeeping) are dropped during log parsing and never reach the transcript.

The renderer groups raw session-log entries into higher-level blocks:

- **Assistant prose** — rendered as markdown body text
- **Thinking** — muted text; collapsed to a single-line preview by default, expandable to full indented markdown
- **Prompt / feedback / answer** — labeled callout cards
- **Tool execution** — grouped cards with state-aware chrome
- **Lifecycle events** — concise muted status lines
- **Question / Foreman** — warning-styled callout cards

**Tool cards** group adjacent `tool_start`, `tool_output`, and `tool_result` entries into a single block using per-tool-name FIFO queues. Each card shows:

- Title/status row: tool name + primary argument label (file path for read/write/edit, pattern for grep, command for bash) + running/success/error icon
- Smart args summary: semantically important arguments for known tools (file paths, grep patterns, bash commands, LSP actions)
- Output preview: multi-line tool results are split into separate rendered lines; first 4 lines in collapsed mode, 12 in verbose mode
- Overflow marker: `… N more lines` when truncated
- Write tool content preview: shows content line count and first-line preview in the args summary
- Result line: shown in verbose mode or when no output exists

**Tool card states**: running (active accent border), success (neutral border), error (error border/tint).

**Interaction model**: `[o] Verbose logs` toggles between collapsed and verbose mode across all blocks. All lines are hard-wrapped or truncated to the available inner width.

**Keys**: `↑`/`↓` scroll, `o` toggle verbose mode.

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

Accessed via `s` from anywhere. The settings UI is a full-screen page with a left navigation tree and a right detail/editor pane. It covers commit, planning, review, Foreman, harness, provider, and repo-lifecycle configuration, with provider status and field descriptions visible alongside editable values.

Provider secrets owned by Substrate are stored in the OS keychain, while the config file stores stable secret references such as `api_key_ref` and `token_ref`. Harness-owned credentials are handled through harness actions instead of being written directly by the TUI. oh-my-pi remains the default verified interactive harness; Claude Code and Codex are selectable but are not documented as having full interaction parity for every corrective workflow. Provider-specific Sentry auth, login, and connectivity-test behavior is documented in `04-adapters.md` under `### Sentry`, while this section owns the shared Settings interaction model.

The footer hints are focus-sensitive. In the tree view they expose navigation, expand/collapse, focus transfer, close, save, test, login, and reveal actions. In the field view they expose field navigation, edit, boolean toggle, return-to-groups, save, test, login, and reveal actions. While editing a field, the footer collapses to save/cancel hints only.

**Keys:**
- Tree focus: `↑`/`↓` navigate, `→` expand/open, `←` collapse/up, `Enter` focus settings, `Esc` close
- Field focus: `↑`/`↓` settings, `Enter` edit, `Space` toggle bool, `←`/`Esc` back to groups, `s` save and apply, `t` test, `g` login, `r` reveal
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
### 4e. Source Items Overlay

Opened from the source details view via `o` (single-item sessions open directly in the browser; multi-item sessions open this overlay for selection). Displays a split-pane overlay listing all source items for the selected work item. Items without URLs are shown in a disabled state and cannot be selected or opened.

The left pane is a list of source items with provider, ref, state, and selection status. The right pane is a scrollable preview showing heading, metadata, description, and an action hint. Multi-select is supported via `Space` — toggling multiple items allows opening them all at once.

**Keys:** `↑`/`↓` navigate list, `←`/`→` or `Tab` cycle focus between list and preview, `Space` toggle multi-select, `Enter`/`o` open selected item(s) in browser, `Esc` close.

### 4f. Logs Overlay

Triggered by `L` from the main shell. Displays captured slog entries in a scrollable viewport. The overlay is sized to 75% of terminal width (minimum 60 chars) and fills available height.

Each log entry shows a right-aligned line number, timestamp, level (color-coded: error red, warning amber, info themed, debug muted), message, and optional attributes. Content is ANSI-aware word-wrapped to fit the overlay width.

**Keys:** `↑`/`↓` or mouse scroll to navigate, `c` copy all log entries to clipboard as raw unwrapped plain text (one entry per line, no ANSI codes), `Esc` close.


### 4g. Add Repository Overlay

Triggered by `a` from the main shell. This is a centered split overlay for browsing and cloning remote repositories into the workspace.

The overlay has three focus areas: controls (search input, source cycling), repo list, and details pane. Sources include GitHub, GitLab, and Manual (clone by URL). `Tab` / `Shift-Tab` cycle focus areas; `↑` / `↓` navigate the repo list; `Enter` starts cloning the selected repository; `Esc` closes.

When a source is selected, the adapter fetches repositories via the `RepoSource.ListRepos(...)` API. Search filters results server-side. The manual source shows a URL input field instead of a list.

Cloning delegates to `gitwork.Client.Clone()` and creates a git-work managed repository in the workspace.

**Keys:** `Tab` / `Shift-Tab` cycle focus, `↑` / `↓` navigate, `Enter` clone, `Esc` close.
---

## 5. Layout System

The shipped shell geometry is shared rather than redefined per view. The sidebar and content panes use the same pane chrome, centered overlays reuse a common overlay frame, and the settings page intentionally keeps its own full-screen split layout while speaking the same visual language.

Implementation-facing ownership for shared geometry, chrome primitives, and layout guardrails lives in `docs/08-tui-design-system.md`.

The sidebar supports a custom scrollbar rendered on the right edge. Render caching is used for the sidebar and status bar to avoid redundant recomputation — cached output is invalidated on content or dimension changes.

**Footer / status bar**

The main shell footer is a single borderless row. Its left side shows focus-sensitive key hints from the sidebar or content pane plus the global commands. Its right side shows workspace context and the count of truly active sessions. That count includes only agent sessions in `pending`, `running`, or `waiting_for_answer`; completed, failed, and interrupted sessions do not inflate the number.

**Toasts**

Toasts render as stacked top-right overlays anchored below the top inset and above the footer. They do not add rows to the main layout. Toast width is capped at 30% of terminal width. Long messages word-wrap up to 4 lines with ellipsis truncation. Toasts expire after 20 seconds and are pruned on a 1-second tick. Toasts render over all overlays (including modal dialogs) and stack vertically when multiple are active.

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
| Mouse scroll | Scroll viewports and lists | Overview, overlays, logs |
| `Enter` | Select / confirm | Lists, overlays |
| `p` | Steer running agent / follow up on completed or failed session | Session interaction view (context-dependent on session state) |
| `f` | Open follow-up re-planning feedback | Completed work item overlay |
| `a` | Add Repository overlay | Main shell |
| `i` | Inspect plan | Plan review, overview |

### Global Keybinds (handled before delegation)

`?` help overlay, `a` Add Repository overlay, `q` quit, `Esc` close overlay / cancel input, `n` unified work browser, `s` settings page, `L` logs overlay, contextual `d` delete-session shortcut when a work item, task row, or history result is active, `Ctrl+c` force quit.

### Input Modes

Two modes: **Normal** (keypresses = commands) and **Input** (keypresses go to textinput widget). Input mode entered by explicit action (feedback, answer, filter, steer/follow-up). Exited via `Enter` (submit) or `Esc` (cancel).

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

Quitting while agent sessions are running shows a confirmation dialog listing the count of active sessions that will be killed. `y` confirms quit, `n`/`Esc` cancels. When no sessions are running, `q` quits immediately. SIGTERM is intercepted to route through the same confirmation path.

### Escalation & Manual Intervention

The orchestrator owns the full per-repo review lifecycle (implement → review → reimpl → re-review → pass/escalate/fail; see `05-orchestration.md`). `ImplementationCompleteMsg` signals that the entire lifecycle — implementation and review — is finished. The TUI does not dispatch review commands.

The TUI intervenes only when human input is required:

- **`Override accept`** — accepts a repo that review escalated (max review cycles reached without passing). Handled via `OverrideAcceptCmd`.
- **`Re-implement`** — manually triggers reimplementation for a repo when `AutoFeedbackLoop` is disabled and review found issues. Handled via `ReimplementMsg`.

Both actions are available from the overview's "Under review" action card.

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
