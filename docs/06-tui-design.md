# 06 - TUI Design

<!-- docs:last-integrated-commit 5cbffc696e10a65fb98b6957c93e3c5f68e837d8 -->

The Substrate terminal interface is built with Bubble Tea and lipgloss. The top-level app owns: left sidebar pane, right content pane, single footer/status row, centered overlays (work browser, session-history, workspace init, action menu), full-screen settings page, and a toast stack rendered over the shell.

---

## 1. Layout

### 1a. Two-Pane Shell

```
┌────── Sessions ──────┐┌────────────── Content ───────────────────────────────────────┐
│ SUB-123              ││ SUB-123 · Design system                                       │
│ Semantic cleanup     ││ <mode-specific work item, task log, or history summary>       │
│ Waiting for answer   ││                                                                │
│ SUB-118              ││                                                                │
│ Refresh docs         ││                                                                │
│ Completed            ││                                                                │
╰──────────────────────╯╰────────────────────────────────────────────────────────────────╯
[↑/↓] Sessions  [→] Tasks  [/] Search  [n] New  [s] Settings  [a] Add repo           workspace · 2 active
```

No header bar; workspace context and active session count live in the footer. Centered overlays sit above the shell; settings takes over the full screen.

### 1b. Sessions Sidebar

Fixed 34 characters wide. Entries are work-item overviews: status icon, external ID/repo prefix, title, and a subtitle (repo progress for implementing items; `Plan review needed`, `Waiting for answer`, or task-session status otherwise).

Press `→` to drill into `{externalID} · Tasks`: work-item overview, optional `Source details` and `Artifacts` rows, and child sessions in sub-plan order. Selecting a task opens its log in the content pane. `←`/`Esc` returns to sessions; `→` from the task pane moves focus to content.

`/` opens session-history search (separate from the live list), searching workspace or global scope.

**Status icons:** `●` running/active (green), `◐` pending human action (amber — plan review, open question, or PR with changes requested), `✓` completed (dim green — also `merged` with label distinguishing), `⊘` interrupted (amber), `✗` failed (red — also when any PR has failing CI), `◌` inactive/default (muted).

**Keys:** `↑`/`↓` navigate; `→` drill in / move to content; `←`/`Esc` go back; `d` delete work item; `/` session-history; `n` work browser; `a` add repository; `s` settings; `x` action menu; `q` quit.

**Filters/sort:** `Ctrl+F` cycles filter (All, Active, Needs Attention, Completed); `Ctrl+G` cycles grouping (flat, by status, by source); `Ctrl+D` toggles sort. Active filter/direction shown below title.

### 1c. Leaf-Based Status Derivation

Work-item status labels (sidebar subtitle, status icon, `HasInterrupted`, `HasOpenQuestion`) are derived from the **leaf tasks** of the agent-session graph rather than scanning the full session history. A leaf is any task with no children (no other task points to it via `ParentAgentSessionID`).

Manual tasks (`phase = manual`) are excluded from the graph and do not influence status labels.

**Per-sub-plan leaf projection:**

| Leaf status | Label |
|---|---|
| `waiting_for_answer` | Waiting for answer |
| `pending` / `running` | (active — no special label) |
| `interrupted` | Interrupted |
| `failed` | Failed |
| `completed` | Completed |

**Work-item display priority:**
1. Any leaf `waiting_for_answer` with an open question → `Waiting for answer`
2. Else any leaf `interrupted` → `Interrupted`
3. Else derive from work-item state (`Implementing`, `Under review`, etc.)

A historical `interrupted` or `failed` task that has been replaced by a child task (retry, follow-up, reimplementation) does not affect labels.

**Legacy fallback:** pre-migration tasks with no graph edges are grouped by `(kind, sub_plan_id, repository_name)`; the newest by `created_at` is treated as the leaf. As soon as any task in a group participates in a graph edge, the graph is authoritative for that group.

### 1d. Content Panel

| Selection / state | Mode |
||-------------------|
|| nothing selected | Empty |
|| work item selected | Overview |
|| `Source details` row | Source Details |
|| planning child session selected | Planning |
|| task-session row or historical result | Session Interaction |

---

## 2. Content Panel Modes

### 2e. Planning Mode

Live session log tailing as the planning agent runs.

```
│ LIN-FOO-789 · Update docs · Planning                          │
│ > Reading repository: backend-api...                           │
│ > Analyzing cross-repo dependencies...                         │
│ > Drafting sub-plan for backend-api...                         │
│ ▌                                                             │
│ [↑↓] Scroll  [p] Pause/unpause                                │
```

**Keys:** `↑`/`↓` scroll, `p` pause/unpause.

### 2f. Plan Review Mode

Full reconstructed plan in a scrollable viewport: YAML block, orchestrator section, and all repo sub-plans.

```
│ LIN-FOO-456 · Rate limiting · Plan Review                     │
│ ```substrate-plan                                              │
│ execution_groups: [shared-lib], [backend-api, frontend]       │
│ ```                                                            │
│ ## Orchestration                                               │
│ ## SubPlan: shared-lib  Goal: Ship rate limiter primitives.    │
│ ─────────────────────────────────────────────────────────────  │
│ [a] Approve  [c] Copy  [e] Edit  [i] Request changes  [↑↓] │
```

- `[a]` **Approve** — triggers implementation.
- `[c]` **Copy** — copies plan to clipboard.
- `[e]` **Edit in $EDITOR** — opens plan markdown in `$EDITOR`. Re-parsed and re-validated on exit.
- `[i]` **Request changes** — opens plan overlay with inline feedback. `Enter` spawns a new planning session with plan and feedback embedded.
- `[i]` **Inspect** — read-only full plan overlay. Available from planning and completed work items.

**Keys:** `↑`/`↓` scroll, `a` approve, `c` copy, `e` edit, `i` request changes / inspect.

### 2g. Session Interaction Mode

Live task log tailing (from tasks sidebar) or historical transcript/summary (from session-history search). Header shows task status, harness, and session ID.

`p` activates a text input. Behavior depends on session state:

| Session state | Hint | Enter action | Effect |
|---------------|------|--------------|--------|
| Running | `Prompt agent` | Sends steer message | Interrupts agent's streaming turn |
| Completed | `Changes` | Sends follow-up | Opens plan overlay with request-changes input |
| Failed | `Changes` | Sends follow-up | Same as completed; creates new Task row and resumes |
| No session | — | `p` is no-op | — |

`Esc` cancels.

**Keys:** `↑`/`↓` scroll, `p` steer/follow up (context-dependent), `Esc` cancel/navigate back.

### 2h. Overview Mode

Canonical root-session control surface. Shown when a work item is selected in sessions sidebar or `Overview` row in tasks sidebar.

**Sections:** (1) Summary — ID, title, state, last updated, repo progress, blocker badges; (2) Action required — only when blocked on human; (3) Source — provider, ref, title, excerpt; (4) Plan — bounded snapshot, never full plan inline; (5) Tasks — repo/sub-plan rows with status, harness, last activity; (6) External lifecycle — tracker refs and PR/MR rows; (7) Recent activity.

**Actionability contract:** if a decision is needed, the overview provides blocking reason, context, and the action. Detail views are never mandatory to unblock.

**Action-required examples:**
- **Plan review**: bounded excerpt + `Approve`/`Changes`/`Inspect`/`Reject`. Full plan in `Review plan` overlay.
- **Open question**: question text, affected repo/task, Foreman's proposed answer and uncertainty. Approve, iterate, or skip from overview.
- **Interrupted session**: repo/task, reason, `Resume`/`Retry` actions.
- **Under review**: summary, critique list, `Override accept` for human escalation, `Re-implement` when auto-feedback disabled.
- **Completed**: `f` opens feedback input. `Enter` submits and opens plan overlay with changes.

**Plan display by state:** `ingested` → `No plan yet`; `planning` → `Plan in progress` + draft preview; `plan_review` → excerpt + version + review actions; `approved`–`completed` → approved/final snapshot; `failed` → last known snapshot if any.

**Keys:** `↑`/`↓` scroll, action card keys, `Enter` open overlays, `f` follow-up re-plan (completed), `o` review artifacts / override accept (under review).

### 2i. Transcript Rendering

Groups session log entries into: assistant prose (markdown), thinking (muted, collapsed to single-line preview), prompt/feedback/answer (labeled callout), tool execution (grouped cards with state chrome), lifecycle events (muted status), question/Foreman (warning callout).

**Tool cards** group adjacent tool-start/output/result entries per tool-name FIFO. Each shows: tool name + primary arg label (file path, pattern, command) + running/success/error icon; smart args summary; output preview (4 lines collapsed, 12 verbose); overflow marker `… N more lines` when truncated; result line in verbose mode or when no output.

**Card states:** running (accent border), success (neutral), error (error border/tint). `[o] Verbose logs` toggles collapsed/verbose.

**Keys:** `↑`/`↓` scroll, `o` toggle verbose.

### 2j. Artifacts Mode

PR/MR accordion from the `Artifacts` row in tasks sidebar. Single artifact renders directly.

```
  #42  acme/auth-svc  feat: distribute config  [open]  ✗ CI  ◐ review
  #43  acme/billing   feat: distribute config  [open]  ✓ CI  ✓ review
  #44  acme/gateway   feat: distribute config  [draft] ○ CI  —
```

Expanded:
```
  ┌─ #42  acme/auth-svc ──────────────────────────────── [open] ──┐
  │  feature/distribute-config → main                             │
  │  opened 2d ago · updated 3h ago                              │
  │  Review: ✓ alice approved, ✗ bob changes requested 1h ago     │
  │  CI: ✗ test 3 failures, ✓ build, ✓ lint                      │
  └───────────────────────────────────────────────────────────────┘
```

**Sidebar icon** (worst case across PRs): `◐` if any changes requested, `✗` if any failing CI, `✓` if all merged, `◌` otherwise.

**Keys:** `↑`/`↓` move; `→`/`Space` expand; `Space` collapse; `←` return to sidebar; `o` open PR in browser; `O` open links dialog; `f` review-comment follow-up (completed or under review).

### 2k. SessionMerged Handling

Sidebar shows `✓` with `merged` badge. Follow-up re-plan keybind is hidden. `i` (inspect) remains available.

---

## 3. Overlays

### 3a. Session History Search (`/`)

Scope (workspace/default), query input, results list, preview. Typing requests a fresh search. Results are work-item-centric, ordered by recent activity, enriched with session count and state flags. Preview shows identity, workspace, latest repo/harness/status, timestamps.

`Enter` opens: restores live work-item context if current workspace, switches to session-interaction view otherwise. `d` deletes the full work item. `Ctrl+S` toggles workspace/global.

**Keys:** `Tab`/`Shift-Tab` cycle scope/input/results/preview; `↑`/`↓` move; `←`/`→` move focus or change scope; `Esc` close.

### 3b. Unified Work Browser (`n`)

Keyboard-first, capability-driven. Header includes `Source` and `Scope`; adds `View`, `State`, or provider-specific status when supported. Sources: `All`, `Linear`, `GitHub`, `GitLab`, `Sentry` (limited to active browse adapters). `All` scope is issues-only. Advanced filters appear only when supported.

Details pane shows metadata and rendered description for the highlighted item. `Space` multi-selects (all items must share one provider). `Enter` starts from selection (or selects highlighted row first). `Ctrl+O` opens externally. `Ctrl+N` switches to `Title`/`Description` form.

**Keys:** `Tab`/`Shift-Tab` cycle sources; `Ctrl+S` cycles scope; `Ctrl+V` cycles view; `Ctrl+T` cycles state; `Ctrl+R` clears filters; `Esc` closes.

### 3c. Settings Page (`s`)

Full-screen with left navigation tree and right detail/editor pane. Covers commit, planning, review, Foreman, harness, provider, and repo-lifecycle configuration. Provider secrets stored in OS keychain; config file stores stable references.

Footer hints are focus-sensitive. Tree view: navigation, expand/collapse, focus transfer, close, save, test, login, reveal. Field view: field navigation, edit, toggle, return-to-groups, save, test, login, reveal. Editing collapses footer to save/cancel.

**Keys:** Tree: `↑`/`↓` navigate, `→` expand, `←` collapse, `Enter` focus, `Esc` close (confirms if dirty). Field: `↑`/`↓` navigate, `Enter` edit, `Space` toggle, `Esc` back, `t` test, `g` login, `r` reveal. Edit: `Enter` save, `Esc` cancel. Settings auto-save when navigating away with dirty state.

### 3d. Workspace Initialization Modal

```
┌─ Initialize Workspace ──────────────────────────────────────────────────────┐
│  No workspace found at: ~/myproject/                                       │
│  Initialize this directory as a Substrate workspace?                       │
│  This will:                                                                 │
│    • Create .substrate-workspace  (workspace identity file)                 │
│    • Detect git-work repos        (directories with .bare/)                 │
│    • Convert plain git repos      (child dirs with .git/)                   │
│    • Register workspace in        ~/.substrate/state.db                     │
│  git-work repos detected: backend-api/, frontend-app/                       │
│  Plain git repos to initialize: legacy-service/                             │
│  [y] Initialize  [n] Cancel                                                 │
└─────────────────────────────────────────────────────────────────────────────┘
```

`y`/`Enter` confirms and initializes; `n`/`Esc` cancels and exits Substrate.

### 3e. Source Items (`o`), Logs (`L`), Add Repository (`a`)

**Source Items (`o`):** Split-pane listing source items for the selected work item. Single-item sessions open directly; multi-item sessions show this overlay. Items without URLs are disabled. `Space` multi-selects; `Enter`/`o` opens selected. `↑`/`↓` navigate; `←`/`→`/`Tab` cycle focus; `Esc` close.

**Logs (`L`):** Captured log entries in a scrollable viewport (75% terminal width, min 60 chars). Each entry: right-aligned line number, timestamp, level (error red, warning amber, info themed, debug muted), message, optional attributes. ANSI-aware word-wrap. `↑`/`↓` or mouse scroll navigate; `c` copy all as raw plain text; `Esc` close.

**Add Repository (`a`):** Browse and clone remote repositories. Three focus areas: controls (search input, source cycling), repo list, details pane. Sources: GitHub, GitLab, Manual (URL input). Server-side search filtering. Cloning creates a git-work managed repository. `Tab`/`Shift-Tab` cycle focus; `↑`/`↓` navigate list; `Enter` clone; `Esc` close.

### 3f. Action Menu (`x`)

A unified action menu replaces the old help overlay. Triggered by `x` from any non-modal context, it displays all available actions for the current context sorted by priority with fuzzy search filtering.

**Structure:**
- Flat list sorted by priority (lower = higher priority)
- Fuzzy search bar at top, filters on keystroke
- Keyboard shortcuts right-aligned at end of each row
- Unavailable actions are omitted entirely (no dimmed/disabled states)
- Cursor starts at highest priority action

**Priority ranges by category:**

| Range | Category |
|-------|----------|
| 10-99 | Global actions |
| 100-199 | Overview |
| 200-299 | Plan Review |
| 300-399 | Session Log |
| 400-409 | Question |
| 410-429 | Interrupted |
| 430-479 | Reviewing |
| 480-499 | Completed |
| 500-599 | Review Followup |
| 600-649 | Artifacts |
| 650-679 | Repo Manager |
| 680-739 | New Session |
| 740-749 | New Session Autonomous |
| 750-779 | Add Repo |
| 780-839 | Settings |
| 840-849 | Logs |
| 850-869 | Session Search |
| 870-889 | Source Items Overlay |
| 890-909 | Overview Links Overlay |
| 910-929 | Workspace Init |
| 930-979 | Source Details |

**Global actions (priority 10-99):**

| Priority | Action | Shortcut | Condition |
|----------|--------|----------|-----------|
| 10 | New session | `n` | Always |
| 11 | New autonomous session | `A` | Always |
| 20 | Open repo manager | `R` | Always |
| 30 | Open settings | `s` | Always |
| 40 | Open logs | `L` | Always |
| 50 | Search sessions | `/` | Always |
| 60 | Delete session | `d` | Session selected |
| 70 | Archive/Unarchive session | `a` | Context-dependent |
| 80 | Interrupt sessions | `I` | Sessions selected |
| 90 | Quit | `q` | Always |

**Keys:** `↑`/`↓` navigate matches; `Enter` execute; `Esc` close and return to previous context; typing filters the list; `Backspace` deletes from query.

**Text input preservation:** When a text input or `GrowingTextArea` is focused, `x` is typed literally rather than opening the action menu.

### 3g. Review-Comment Follow-Up (`f`)

Triggered from artifacts view when work item is `SessionCompleted` or `SessionReviewing`. Turns unresolved PR/MR review comments into agent follow-up work.

**Dispatch modes:**
- **Address (Enter)** — implementation-only; plan stays intact. One follow-up session per affected repo.
- **Re-plan (`p`)** — replaces the plan; PR/MR descriptions resync via plan approval.

**Stages:**
1. `Loading` — spinner fetching unresolved comments in parallel. Resolved filtered at fetch boundary.
2. Routing: 0 unresolved → toast + close; 1 → skip picker; >1 → PR picker (all checked by default).
3. `Selector` — checklist by repo → file (with `General` section); right pane shows comment preview. Toggling a header cascades. All selected by default.
4. `Confirm` — only for re-plan; warns plan will be replaced and PR descriptions will resync.

**Staleness:** if >5 min since fetch, silently re-fetches, reapplies prior selection by comment ID, toasts `N comment(s) selected were no longer available` if any disappeared. New comments default deselected.

**Partial dispatch:** if a repo has selected comments but no completed task, skipped with toast: `Addressed N of M repos (K skipped: no active task)`.

**Keys:** `↑`/`↓` move; `Space` toggle; `a` all; `n` none; `Enter` Address; `p` Re-plan; `Esc` cancel.

---

## 4. Layout System

Shell geometry is shared across views: sidebar and content panes share pane chrome; overlays reuse a common frame; settings keeps its own full-screen split but uses the same visual language. Sidebar renders a custom scrollbar. Render caching avoids redundant recomputation; invalidated on content or dimension changes.

**Footer:** single borderless row. Left: focus-sensitive key hints. Right: workspace context + active session count (only `pending`, `running`, `waiting_for_answer` sessions count).

**Toasts:** stacked top-right overlays above footer. Width capped at 30% terminal. Word-wrap to 4 lines with ellipsis. Expire after 20 seconds, pruned on 1-second tick. Render over all overlays. Stack vertically.

---

## 5. Interaction Model

| Key | Action | Scope |
|-----|--------|-------|
| `↑`/`↓` | Navigate / scroll | Sidebar, lists, viewport |
| `Tab` | Cycle repos / panels | Implementing mode |
| `g`/`G` | Top / bottom | Lists |
| Mouse scroll | Scroll | Viewports and lists |
| `Enter` | Select / confirm | Lists, overlays |
| `p` | Steer or follow up | Session interaction |
| `f` | Follow-up re-plan | Completed overview |
| `a` | Add Repository | Main shell |
| `i` | Inspect plan | Plan review, overview |
| `x` | Action menu | Global |
| `s` | Settings | Global |
| `L` | Logs | Global |
| `d` | Delete | Contextual |
| `Ctrl+c` | Force quit | Global |
| `Esc` | Close / cancel | Global |

**Input modes:** Normal (keypresses = commands) and Input (keypresses go to text widget). Entered explicitly (feedback, answer, filter, steer/follow-up). Exited via `Enter` (submit) or `Esc` (cancel).

**Confirmation dialogs:** Destructive actions (delete, abandon, reject, override) show a modal. `y` confirms, `n`/`Esc` cancels. Quitting with active sessions shows a confirmation listing the count. `y` quits, `n`/`Esc` cancels. No active sessions: `q` quits immediately. SIGTERM routes through the same confirmation path.

**Human escalation:** `Override accept` (accepts a repo escalated by review — max cycles reached) and `Re-implement` (manually triggers reimplementation when auto-feedback disabled) are available from the overview's "Under review" action card.

**Async dispatch contract.** Long-running commands — focused retry, focused follow-up (completed and failed), focused resumed-session continuation, bulk work-item resume and retry, planning restart — return a dispatch acknowledgement immediately. The actual graph entry point runs in a background goroutine; async errors surface as `ErrMsg` so dispatch failures are observable. The TUI never blocks on harness or review completion. Candidate selection (which leaves are eligible for a given bulk action) is delegated to the orchestrator entry point; the TUI does not recompute eligibility. The state for resumed, retried, or followed-up sessions arrives through the event bus, and the UI decoders preserve both the superseded source session ID and the new child session ID so the graph edge is reflected in the sidebar and overview.

---

## 6. Multi-Instance Support

Multiple substrate instances can open the same workspace simultaneously. The global state database is the shared state source.

**Instance registration:** Each instance registers (ULID, PID, hostname, heartbeat) on startup. Background goroutine updates heartbeat every 5 seconds. Clean shutdown deletes the row. An instance is live if its heartbeat is within 15 seconds.

**Session ownership:** Starting an agent session writes the instance's ID into the session record. Only the owning instance can send messages/answers, resume, or abandon. If the owner is dead (row missing or heartbeat stale >15s), any other instance may take over: updates the owner ID and proceeds as the original owner.

**Keybind gating:** answer, resume, and abandon are active only when current instance owns the session or the owner is dead.

**Agent output:** All output is persisted to the session log file. Any instance can tail from disk. Tailing handles log rotation: on size regression or inode change, offset resets to 0.

**State visibility:** Session state changes are visible to all instances immediately via the event bus. The TUI subscribes to domain events for targeted state reloads without polling.
