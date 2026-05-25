# Implementation Plan: Open Terminal in Worktree Action

## Context

This plan adds an action to open a new terminal in a session's worktree from both:
1. **Overview view** — multi-select repo+worktree via picker overlay
2. **Agent session view** — direct action with single-key binding

Terminal selection respects the user's active terminal (Warp, iTerm2, Terminal.app, Kitty, WezTerm, Alacritty) and opens a new tab/pane when supported.

Additionally, this plan introduces a **unified split-list picker component** that both the new WorktreePickerOverlay and the existing RepoManagerOverlay will use, eliminating duplicated split-pane logic.

---

## 1. Terminal Detection & Opening Package

### 1.1 New Package: `internal/terminal/`

Create `internal/terminal/detect.go` and `internal/terminal/open.go`.

### 1.2 Terminal Detection

Detect the user's active terminal via `TERM_PROGRAM` environment variable:

```go
// internal/terminal/detect.go

type TerminalType string

const (
    TerminalUnknown    TerminalType = ""
    TerminalWarp      TerminalType = "warp"
    TerminalITerm2    TerminalType = "iterm2"
    TerminalTerminal  TerminalType = "terminal"
    TerminalKitty     TerminalType = "kitty"
    TerminalWezTerm   TerminalType = "wezterm"
    TerminalAlacritty TerminalType = "alacritty"
)

// Detect returns the active terminal based on TERM_PROGRAM and other heuristics.
func Detect() TerminalType {
    term := os.Getenv("TERM_PROGRAM")
    switch term {
    case "WarpTerminal", "Warp":
        return TerminalWarp
    case "iTerm.app":
        return TerminalITerm2
    case "Apple_Terminal":
        return TerminalTerminal
    }
    // Check for kitty window ID. This identifies Kitty, but does not prove
    // remote-control authorization; launch code must still handle kitten errors.
    if _, ok := os.LookupEnv("KITTY_WINDOW_ID"); ok {
        return TerminalKitty
    }
    // Check for WezTerm. WEZTERM_PANE is the documented pane selector used by
    // `wezterm cli spawn`; keep WEZTERM_SOCK as an additional heuristic.
    if _, ok := os.LookupEnv("WEZTERM_PANE"); ok {
        return TerminalWezTerm
    }
    if _, ok := os.LookupEnv("WEZTERM_SOCK"); ok {
        return TerminalWezTerm
    }
    // Check parent process as fallback
    // ...
    return TerminalUnknown
}

// KittyRemoteControlTargetAvailable reports whether `kitten @` has an addressing
// target. Authorization still depends on kitty configuration
// (allow_remote_control/remote_control_password) and must be handled as a
// command error.
func KittyRemoteControlTargetAvailable() bool {
    if _, ok := os.LookupEnv("KITTY_WINDOW_ID"); ok {
        return true
    }
    if _, ok := os.LookupEnv("KITTY_LISTEN_ON"); ok {
        return true
    }
    return false
}
```

### 1.3 Terminal-Specific Opening Commands

| Terminal | Support | Command |
|----------|---------|---------|
| **WezTerm** | ✅ Full | `wezterm cli spawn --cwd /path` inside WezTerm; `wezterm start --new-tab --cwd /path` fallback |
| **Kitty** | ✅ Full (requires remote control) | `kitten @ launch --type=tab --cwd /path` |
| **iTerm2** | ✅ Full | `osascript` AppleScript (new tab with profile) |
| **Terminal.app** | ✅ Full | `osascript` AppleScript (do script in front window) |
| **Warp** | ❌ None | Only workarounds (see below) |
| **Alacritty** | ❌ None | No tab support; open new window |

**Warp Limitations:**
- No programmatic API exists (GitHub Discussion #612, Issue #3959, #4548, #7926 all unresolved)
- Workaround: `open -a Warp.app /path` opens Warp in that directory (new window, not tab)
- Cannot create new tabs programmatically

**Alacritty Limitations:**
- Explicitly refuses tab support (design philosophy; GitHub Issue #6340)
- Users expected to use tmux/zellij inside Alacritty
- Open new window: `alacritty --working-directory /path`

### 1.4 Open Function Signature

```go
// internal/terminal/open.go

// Open opens a new terminal tab/window in the specified directory.
// Returns the terminal type used, or an error if no supported terminal is found.
func Open(dir string) (TerminalType, error)

// OpenWithTerminal opens in the specified terminal (ignoring detection).
// On macOS, falls back to Terminal.app if the requested terminal is unavailable.
func OpenWithTerminal(dir string, term TerminalType) (TerminalType, error)
```

### 1.5 Error Handling & User Feedback

- If no supported terminal detected → log warning and fall back to `open -a Terminal.app` on macOS; otherwise return an actionable error
- If AppleScript fails after startup → log warning with specific error; do not block the TUI waiting for completion
- Warp: Show info message that Warp lacks tab support; open in new window via `open -a Warp.app`
- Alacritty: Show info message about tmux recommendation; open new window

---

## 2. TUI Command Layer

### 2.1 Extend `internal/tui/views/cmds.go`

Replace the existing Terminal.app-only `OpenTerminalCmd` with a terminal-package-backed command. The terminal package must return quickly: it may validate the target directory and start/detach the terminal opener, but it must not run a long AppleScript/CLI command synchronously on the Bubble Tea update path.

```go
// OpenTerminalCmd opens a new terminal tab/window in the specified directory.
// The terminal is auto-detected based on the active terminal.
func OpenTerminalCmd(dir string) tea.Cmd {
    return func() tea.Msg {
        termType, err := terminal.Open(dir) // starts detached opener; returns startup errors only
        if err != nil {
            slog.Warn("failed to open terminal", "path", dir, "error", err)
            return ErrMsg{Err: fmt.Errorf("open terminal in %s: %w", dir, err)}
        }
        slog.Debug("opened terminal", "path", dir, "terminal", termType)
        return ActionDoneMsg{Message: "Opened terminal"}
    }
}

// OpenTerminalWithCmd opens in a specific terminal type.
func OpenTerminalWithCmd(dir string, termType terminal.TerminalType) tea.Cmd {
    return func() tea.Msg {
        used, err := terminal.OpenWithTerminal(dir, termType)
        if err != nil {
            slog.Warn("failed to open terminal", "path", dir, "terminal", termType, "error", err)
            return ErrMsg{Err: fmt.Errorf("open terminal in %s: %w", dir, err)}
        }
        slog.Debug("opened terminal", "path", dir, "terminal", used)
        return ActionDoneMsg{Message: "Opened terminal"}
    }
}
```

### 2.2 Delete Unsafe Shell Escaping for Terminal Launches

Do not reuse the current `shellEscape` helper for terminal AppleScript. It only escapes double quotes and is not safe for shell `cd` commands. Terminal/iTerm AppleScript must use AppleScript/POSIX quoting (`quoted form of POSIX path ...`) or pass the working directory as a process argument where the terminal supports it. Delete `shellEscape` if no other code uses it after the terminal package cutover.

## 3. Shared Split-List Picker Component

### 3.1 Design Rationale

Both the new **WorktreePickerOverlay** and the existing **RepoManagerOverlay** use a split-pane layout:

| Component | Left Pane | Right Pane | Purpose |
|-----------|-----------|------------|---------|
| RepoManagerOverlay | Repo list | Detail viewport (text, scrollable) | Manage repos with actions |
| WorktreePickerOverlay | Repo list | Worktree list | Select worktree to open |

The shared component must not own `list.Model` or `viewport.Model` values. The overlays already own those models, and storing duplicate model copies or pointers inside a shared component will make selection state diverge. The shared component should own only:

- split-pane geometry and layout computation
- focus state (`left` vs `right`)
- focus-switch key handling (`Tab`, `←`, `→`)
- rendering of caller-provided pane bodies

Each overlay remains responsible for updating its own list/viewport models and for firing side effects such as worktree loads when selection changes.

### 3.2 New File: `internal/tui/components/split_list_picker.go`

```go
package components

type SplitPaneFocus int

const (
    SplitPaneFocusLeft SplitPaneFocus = iota
    SplitPaneFocusRight
)

type SplitListPicker struct {
    focus  SplitPaneFocus
    layout SplitOverlayLayout
    spec   SplitOverlaySizingSpec
}

type SplitListPaneSpec struct {
    Title string
    Body  string
}

func NewSplitListPicker(spec SplitOverlaySizingSpec) SplitListPicker {
    return SplitListPicker{focus: SplitPaneFocusLeft, spec: spec}
}

// SetSize recalculates layout dimensions. chromeLines is supplied by the overlay
// because each overlay has different header/footer/hint wrapping.
func (m *SplitListPicker) SetSize(width, height, chromeLines int) {
    m.layout = ComputeSplitOverlayLayout(width, height, chromeLines, m.spec)
}

func (m SplitListPicker) Layout() SplitOverlayLayout { return m.layout }
func (m SplitListPicker) Focus() SplitPaneFocus { return m.focus }
func (m SplitListPicker) IsFocusLeft() bool { return m.focus == SplitPaneFocusLeft }
func (m *SplitListPicker) FocusLeft() { m.focus = SplitPaneFocusLeft }
func (m *SplitListPicker) FocusRight() { m.focus = SplitPaneFocusRight }
func (m *SplitListPicker) SwitchFocus() {
    if m.focus == SplitPaneFocusLeft { m.focus = SplitPaneFocusRight; return }
    m.focus = SplitPaneFocusLeft
}

// HandleFocusKey handles only focus-switching keys and reports whether it consumed the key.
func (m *SplitListPicker) HandleFocusKey(key string) bool {
    switch key {
    case "tab", "left", "right":
        m.SwitchFocus()
        return true
    default:
        return false
    }
}

func (m SplitListPicker) View(st styles.Styles, left, right SplitListPaneSpec) string {
    return RenderSplitOverlayBody(st, m.layout, SplitOverlaySpec{
        LeftPane: OverlayPaneSpec{
            Title:        left.Title,
            DividerWidth: m.layout.LeftInnerWidth,
            Body:         left.Body,
            Focused:      m.focus == SplitPaneFocusLeft,
        },
        RightPane: OverlayPaneSpec{
            Title:        right.Title,
            DividerWidth: m.layout.RightInnerWidth,
            Body:         right.Body,
            Focused:      m.focus == SplitPaneFocusRight,
        },
    })
}
```

### 3.3 Shared Layout Components

The existing `ComputeSplitOverlayLayout` and `RenderSplitOverlayBody` in `overlay_frame.go` provide the foundation. The `SplitListPicker` will reuse:

- `SplitOverlaySizingSpec` — geometry constraints
- `ComputeSplitOverlayLayout` — layout computation
- `RenderSplitOverlayBody` — split-pane rendering
- `RenderOverlayDivider` — semantic divider

### 3.4 Usage Pattern

```go
// In an overlay's Update method.
case tea.KeyMsg:
    if m.picker.HandleFocusKey(msg.String()) {
        return m, nil
    }
    if m.picker.IsFocusLeft() {
        prevIdx := m.repoList.Index()
        m.repoList, cmd = m.repoList.Update(msg)
        if m.repoList.Index() != prevIdx {
            wtCmd = m.maybeLoadWorktrees()
        }
        return m, tea.Batch(cmd, wtCmd)
    }
    m.detailViewport, cmd = m.detailViewport.Update(msg)
    return m, cmd
```

## 4. Refactor: RepoManagerOverlay to Use SplitListPicker

### 4.1 Current State

`RepoManagerOverlay` (`internal/tui/views/overlay_repo_manager.go`) has inline split-pane focus and rendering logic:

- Custom `repoManagerFocusArea` enum and switching logic
- Duplicate split-pane rendering setup
- Manual size calculations

### 4.2 Target State

```go
type RepoManagerOverlay struct {
    workspaceDir string
    gitClient    *gitwork.Client

    repos    []managedRepo
    repoList list.Model

    worktrees       []gitwork.Worktree
    worktreeErr     error
    worktreeLoading bool
    worktreeReqID   int
    detailViewport  viewport.Model

    picker components.SplitListPicker

    pendingDelete *managedRepo
    pendingInit   *managedRepo
    loading       bool

    styles        styles.Styles
    width, height int
    active        bool
}
```

The overlay continues to own `repoList` and `detailViewport`. The picker owns only focus/layout/rendering.

### 4.3 Refactoring Steps

1. Add `picker components.SplitListPicker` initialized with `repoManagerSizingSpec`.
2. Replace `focus repoManagerFocusArea` with `picker.Focus()` / `picker.IsFocusLeft()`.
3. Keep `syncSizes` responsible for applying `picker.Layout()` dimensions to `repoList` and `detailViewport`.
4. Preserve action handlers (`a`, `d`, `i`, `Esc`) in `RepoManagerOverlay.handleKey`.
5. Preserve worktree side effects by comparing `repoList.Index()` before/after list updates.
6. Delete the now-redundant `repoManagerFocusArea` enum after tests pass.

### 4.4 Key Changes

| Before | After |
|--------|-------|
| Custom `focus repoManagerFocusArea` tracking | `picker.Focus()` |
| Manual `Tab`/`←`/`→` handling | `picker.HandleFocusKey(...)` |
| Inline split-pane render setup | `picker.View(...)` with caller-provided pane bodies |
| Overlay/list state duplicated in picker | Overlay remains sole owner of models |

### 4.5 Verify Behavior Preserved

Test these interactions after refactoring:

| Action | Test |
|--------|------|
| Tab/←/→ | Focus switches between repo list and detail |
| ↑/k, ↓/j (list focused) | Navigate repos, loads worktrees |
| ↑/k, ↓/j (detail focused) | Scroll detail viewport |
| Mouse wheel (list focused) | Scroll list and load worktrees if selection changes |
| Mouse wheel (detail focused) | Scroll detail viewport |
| `a` | Opens Add Repo overlay |
| `d` | Shows delete confirmation |
| `i` | Initiates repo initialization |
| Esc | Closes overlay |

## 5. New Worktree Picker Overlay

### 5.1 Overview

A global App-level split-pane picker overlay for selecting repo+worktree combinations to open in terminal.

```
┌─ Open Terminal ─────────────────────────────────────┐
│                                                      │
│  Repositories────────┬─ Worktrees ─────────────────│
│  repo-a              │  main                       │
│  repo-b              │  feature-x                  │
│  repo-c              │  feature-y                  │
│                      │                              │
│                      ├─ Keybind hints ─────────────│
│                      │  [t/Enter] Open terminal    │
└──────────────────────┴──────────────────────────────┘
```

### 5.2 New File: `internal/tui/views/overlay_worktree_picker.go`

```go
type WorktreePickerOverlay struct {
    workspaceDir string
    gitClient    *gitwork.Client

    repos        []managedRepo
    repoList     list.Model
    worktrees    []gitwork.Worktree
    worktreeList list.Model

    picker components.SplitListPicker

    worktreesLoading bool
    worktreeErr     error
    worktreeReqID   int

    loading bool
    styles  styles.Styles
    width   int
    height  int
    active  bool
}
```

`WorktreePickerOverlay` owns both list models. `SplitListPicker` owns no list state.

### 5.3 App-Level Ownership

Add the overlay to `App`, not `SessionOverviewModel`:

```go
type overlayKind int

const (
    // ... existing overlays
    overlayWorktreePicker
)

type App struct {
    // ... existing fields
    worktreePicker WorktreePickerOverlay
}
```

Initialization, resizing, key routing, message routing, closing, and rendering must mirror `repoManager`:

- construct in `NewApp`
- update in `applyServicesReload`
- resize in `WindowSizeMsg`
- route keys while `activeOverlay == overlayWorktreePicker`
- render in `App.View()` via `renderOverlay(a.worktreePicker.View(), ...)`
- close in the global `CloseOverlayMsg` path

### 5.4 Key Bindings

| Key | Action |
|-----|--------|
| `Tab` / `←` / `→` | Switch focus between repo list and worktree list |
| `↑/k` `↓/j` | Navigate current pane |
| `t` | Open terminal in selected worktree |
| `Enter` | Open terminal in selected worktree |
| `Esc` | Close overlay |

### 5.5 Data Flow

```
1. User presses `t` in Overview
   └─ SessionOverviewModel returns OpenWorktreePickerMsg
      └─ App opens overlayWorktreePicker
         └─ WorktreePickerOverlay.Open() returns LoadManagedReposCmd

2. ManagedReposLoadedMsg received
   └─ App updates workspace slug cache
   └─ App routes the message to WorktreePickerOverlay when activeOverlay == overlayWorktreePicker
   └─ Overlay populates repoList and fires LoadWorktreesCmd for the selected repo

3. User navigates repo list
   └─ On index change: LoadWorktreesCmd(..., WorktreeLoadTargetPicker)
      └─ WorktreesLoadedMsg received
         └─ App routes by msg.Target to the owning overlay
         └─ Overlay populates worktreeList

4. User presses t/Enter on selected worktree
   └─ Overlay returns OpenTerminalInWorktreeMsg
      └─ App closes the overlay and runs OpenTerminalCmd(path)
```

### 5.6 Worktree Loading (Reuse Existing Pattern, Scoped)

Extend `WorktreesLoadedMsg` and `LoadWorktreesCmd` so repo-manager and picker responses cannot be misrouted after an overlay closes or a stale request returns:

```go
type WorktreeLoadTarget string

const (
    WorktreeLoadTargetRepoManager WorktreeLoadTarget = "repo_manager"
    WorktreeLoadTargetPicker      WorktreeLoadTarget = "worktree_picker"
)

type WorktreesLoadedMsg struct {
    Target    WorktreeLoadTarget
    RequestID int
    RepoPath  string
    Worktrees []gitwork.Worktree
    Err       error
}

func LoadWorktreesCmd(client *gitwork.Client, repo managedRepo, requestID int, target WorktreeLoadTarget) tea.Cmd
```

Update existing RepoManager call sites to pass `WorktreeLoadTargetRepoManager`. WorktreePicker passes `WorktreeLoadTargetPicker`.

```go
func (m *WorktreePickerOverlay) maybeLoadWorktrees() tea.Cmd {
    idx := m.repoList.Index()
    if idx < 0 || idx >= len(m.repos) {
        return nil
    }

    m.worktrees = nil
    m.worktreeList.SetItems(nil)
    m.worktreesLoading = true
    m.worktreeErr = nil
    m.worktreeReqID++

    return LoadWorktreesCmd(
        m.gitClient,
        m.repos[idx],
        m.worktreeReqID,
        WorktreeLoadTargetPicker,
    )
}
```

## 6. Overview View Integration

### 6.1 Add `t` Key Handler for Terminal Picker

The current overview does not bind `t` for viewport navigation. Add `t` key handling directly in `SessionOverviewModel.Update()` alongside the existing overview shortcuts:

```go
case "t":
    return m, func() tea.Msg { return OpenWorktreePickerMsg{} }
```

Only handle this when no overview-local overlay is open; existing overlay routing at the top of `Update` should continue to capture keys first.

### 6.2 Add Status Bar Hint

In `SessionOverviewModel.KeybindHints()`, append:

```go
hints = append(hints, KeybindHint{Key: "t", Label: "Open terminal"})
```

Keep source/review link hints on `o` unchanged.

### 6.3 App Message and Overlay Routing

Add the message and route it in `App.Update`:

```go
type OpenWorktreePickerMsg struct{}

case OpenWorktreePickerMsg:
    a.activeOverlay = overlayWorktreePicker
    a.worktreePicker.SetSize(a.windowWidth, a.windowHeight)
    return a, a.worktreePicker.Open()
```

## 7. Agent Session View Enhancement

### 7.1 Key Change: `o` → `t`

Change the terminal shortcut from `o` to `t` to align with the overview and picker terminal action.

Because the session log currently uses `t` for “toggle thinking,” move that behavior to `ctrl+t` first:

```go
// planning_view.go / SessionLogModel.Update
case "ctrl+t":
    m.collapseThinking = !m.collapseThinking
    m.doRebuildTranscript()
```

Then change the App-level terminal shortcut:

```go
case "t":
    // Open terminal in worktree when in session view.
    if a.mainFocus == mainFocusContent && a.content.Mode() == ContentModeAgentSession {
        if sessionID := a.content.sessionLog.SessionID(); sessionID != "" {
            if session := a.workItemTaskSession(a.currentWorkItemID, sessionID); session != nil && session.WorktreePath != "" {
                return a, OpenTerminalCmd(session.WorktreePath)
            }
        }
        break // let content handle non-terminal `t` cases if any remain
    }
```

Keep `o` available for session/sidebar sort behavior outside agent-session content.

### 7.2 Status Bar Hints

Update `SessionLogModel.KeybindHints()`:

```go
hints = append(hints, KeybindHint{Key: "t", Label: "Open Terminal"})
if hasThinkingBlocks(m.entries) {
    hints = append(hints, KeybindHint{Key: "Ctrl+T", Label: "Toggle thinking"})
}
```

Also update `overlay_help.go`, `action_menu.go`, and `app_open_terminal_test.go` so displayed shortcuts and tests match the new keymap.

## 8. Worktree Picker Message Flow

### 8.1 Messages (`msgs.go`)

```go
// OpenWorktreePickerMsg signals opening the worktree picker overlay.
type OpenWorktreePickerMsg struct{}

// OpenTerminalInWorktreeMsg is sent when user selects a worktree to open.
type OpenTerminalInWorktreeMsg struct {
    WorktreePath string
}

// WorktreesLoadedMsg is extended with Target; see §5.6.
```

### 8.2 App Handler

```go
case OpenTerminalInWorktreeMsg:
    a.activeOverlay = overlayNone
    a.worktreePicker.Close()
    return a, OpenTerminalCmd(msg.WorktreePath)

case WorktreesLoadedMsg:
    switch msg.Target {
    case WorktreeLoadTargetPicker:
        a.worktreePicker, cmd = a.worktreePicker.Update(msg)
    default:
        a.repoManager, cmd = a.repoManager.Update(msg)
    }
    return a, cmd
```

### 8.3 Worktree Picker Command

```go
func (m *WorktreePickerOverlay) openTerminalCmd() tea.Cmd {
    if m.worktreesLoading || len(m.worktrees) == 0 {
        return nil
    }

    selectedIdx := m.worktreeList.Index()
    if selectedIdx < 0 || selectedIdx >= len(m.worktrees) {
        return nil
    }

    path := m.worktrees[selectedIdx].Path
    return func() tea.Msg {
        return OpenTerminalInWorktreeMsg{WorktreePath: path}
    }
}
```

## 9. Keyboard Shortcut Summary

| Location | Key | Action |
|----------|-----|--------|
| Overview | `t` | Open worktree picker overlay |
| Worktree picker | `t` | Open terminal in selected worktree |
| Worktree picker | `Enter` | Open terminal in selected worktree |
| Worktree picker | `Tab` / `←/→` | Switch focus between panes |
| Repo manager | `Tab` / `←/→` | Switch focus between panes (existing) |
| Agent session view | `t` | Open terminal in session's worktree |
| Agent session view | `Ctrl+T` | Toggle thinking blocks |

---

## 10. Implementation Order

### Phase 1: Terminal Package (Foundation)
1. Create `internal/terminal/types.go` — terminal type enum
2. Create `internal/terminal/detect.go` — terminal detection
3. Create `internal/terminal/open.go` — nonblocking/detached terminal opening logic
4. Update `OpenTerminalCmd` in `cmds.go` to use new terminal package
5. Delete unsafe terminal `shellEscape` usage
6. Test command construction for each terminal type

### Phase 2: SplitListPicker Component
1. Create `internal/tui/components/split_list_picker.go`
2. Define focus/layout-only `SplitListPicker` types
3. Implement `SetSize`, `HandleFocusKey`, `View`, `SwitchFocus`, `Focus`
4. Write unit tests for focus and layout behavior
5. Verify compatible with existing `ComputeSplitOverlayLayout`

### Phase 3: Refactor RepoManagerOverlay
1. Add `SplitListPicker` to `RepoManagerOverlay`
2. Extract pane configurations
3. Wire picker focus/layout helpers in overlay's `Update`
4. Preserve action handlers (`a`, `d`, `i`)
5. Verify all interactions work (see test matrix in §4.5)
6. Delete now-redundant inline split-pane logic

### Phase 4: WorktreePickerOverlay
1. Create `overlay_worktree_picker.go`
2. Compose `SplitListPicker` with two list panes
3. Wire scoped `LoadWorktreesCmd(..., WorktreeLoadTargetPicker)` on repo selection change
4. Add `t`/`Enter` key handler for terminal opening
5. Test picker navigation and terminal opening

### Phase 5: Overview Integration
1. Add `t` key handler to open worktree picker overlay
2. Add `OpenWorktreePickerMsg` to msgs.go
3. Add App-level `overlayWorktreePicker` field/routing/rendering
4. Route `ManagedReposLoadedMsg` and scoped `WorktreesLoadedMsg` to the picker when appropriate

### Phase 6: Polish
1. Move session-log thinking toggle from `t` to `ctrl+t`
2. Add status bar hints for overview/session/picker
3. Update action menu and help overlay with new shortcuts
4. Error handling improvements
5. Edge case handling (no repos, no worktrees, invalid paths, missing terminal)

---

## 11. Testing Strategy

### 11.1 Unit Tests

**SplitListPicker Component:**
```go
// internal/tui/components/split_list_picker_test.go
func TestSplitListPicker_FocusSwitch(t *testing.T) { /* ... */ }
func TestSplitListPicker_HandleFocusKeyConsumesSwitchKeys(t *testing.T) { /* ... */ }
func TestSplitListPicker_DoesNotConsumeNavigationKeys(t *testing.T) { /* ... */ }
func TestSplitListPicker_SetSize(t *testing.T) { /* ... */ }
func TestSplitListPicker_View(t *testing.T) { /* ... */ }
```

**Terminal Detection:**
```go
// internal/terminal/detect_test.go
func TestDetect(t *testing.T) {
    // Test TERM_PROGRAM parsing for each terminal
    // Test fallback detection
}
```

**Terminal Opening:**
```go
// internal/terminal/open_test.go
func TestCommand_TerminalAppUsesQuotedPOSIXPath(t *testing.T) { /* ... */ }
func TestCommand_ITerm2UsesQuotedPOSIXPath(t *testing.T) { /* ... */ }
func TestCommand_KittyUsesCwdArg(t *testing.T) { /* ... */ }
func TestCommand_WezTermUsesCwdArg(t *testing.T) { /* ... */ }
func TestCommand_WarpUsesOpenAppWithPathArg(t *testing.T) { /* ... */ }
func TestCommand_AlacrittyUsesWorkingDirectoryArg(t *testing.T) { /* ... */ }
func TestOpenRejectsMissingDirectory(t *testing.T) { /* ... */ }
```

### 11.2 Integration Tests

**RepoManagerOverlay Refactor:**
- Focus switching (Tab/←/→) works correctly
- Navigation keys propagate to correct pane
- Action keys (`a`, `d`, `i`) still work
- Layout renders correctly at various terminal sizes

**WorktreePickerOverlay:**
- Full flow: overview action → picker → terminal opened
- Repo selection triggers worktree loading
- `t`/`Enter` opens terminal in selected worktree
- Scoped worktree responses do not update the wrong overlay
- Error cases: terminal not found, path invalid

### 11.3 Manual Testing Matrix

**Terminal Support:**
| Terminal | Open Tab? | Open Window? | CWD Works? |
|----------|-----------|--------------|------------|
| WezTerm | ✅ | ✅ | ✅ |
| Kitty | ✅ | ✅ | ✅ |
| iTerm2 | ✅ | ✅ | ✅ |
| Terminal.app | ✅ | ✅ | ✅ |
| Warp | ❌ | ✅ (via `open`) | ✅ |
| Alacritty | ❌ | ✅ | ✅ |

**Picker Component:**
| Scenario | Test |
|----------|------|
| Tab key | Focus switches panes |
| ← / → keys | Focus switches panes |
| ↑ / k (left focused) | Navigate repo list |
| ↑ / k (right focused) | Navigate worktree list |
| Repo selection | Worktrees load for selected repo |
| `t` key | Terminal opens in worktree |
| Enter key | Terminal opens in worktree |
| Esc key | Overlay closes |
| Window resize | Layout adapts |

---

## 12. Edge Cases & Error Handling

### 12.1 No Worktrees Exist
- Show message in picker: "No worktrees found"
- Disable `t` / `Enter` key action

### 12.2 Worktree Path Invalid
- Log warning when opening terminal
- Show toast/notification to user

### 12.3 Terminal Not Detected
- On macOS, fall back to `open -a Terminal.app /path`, not bare `open /path` (Finder).
- On Linux, try a known installed terminal command only when executable discovery succeeds; otherwise return an error toast.

### 12.4 Permission Denied (AppleScript)
- macOS may require user permission for AppleScript
- Log detailed error for debugging

### 12.5 Kitty Remote Control Not Enabled
- Kitty requires `allow_remote_control=yes` or `remote_control_password` in `kitty.conf`.
- Verify an addressing target with `KittyRemoteControlTargetAvailable()` before attempting `kitten @` (checks `KITTY_WINDOW_ID` or `KITTY_LISTEN_ON`).
- Treat `kitten @` authorization failures as normal command errors and show a concise instruction toast.
- Fall back to Terminal.app on macOS or return an actionable error; do not use bare `open /path` because that opens Finder

### 12.6 Empty Repo List
- Show "No repositories found" in left pane
- Disable `t` / `Enter` key action

---

## 13. Dependencies

### 13.1 New Dependencies
None — all terminal APIs are via standard library (`os/exec`, `osascript`) or CLI (`wezterm`, `kitten`).

### 13.2 Existing Dependencies Used
- `internal/gitwork` — worktree listing (already used)
- `internal/tui/views/cmds.go` — existing commands
- `internal/tui/components` — overlay rendering (extended)

### 13.3 Optional: External CLI Requirements
- `wezterm` — if WezTerm is installed
- `kitten` — if Kitty is installed (comes with Kitty)
- `git-work` — already required

---

## 14. File Changes Summary

| File | Change |
|------|--------|
| `internal/terminal/detect.go` | **NEW** — terminal detection |
| `internal/terminal/open.go` | **NEW** — terminal opening |
| `internal/terminal/types.go` | **NEW** — `TerminalType` enum |
| `internal/tui/components/split_list_picker.go` | **NEW** — shared focus/layout picker component |
| `internal/tui/components/split_list_picker_test.go` | **NEW** — component tests |
| `internal/tui/views/overlay_repo_manager.go` | **REFACTOR** — use `SplitListPicker` |
| `internal/tui/views/overlay_worktree_picker.go` | **NEW** — picker overlay |
| `internal/tui/views/overview.go` | **MODIFY** — add `t` handler for picker |
| `internal/tui/views/app.go` | **MODIFY** — add `overlayWorktreePicker` enum, overlay field, size/key/message routing, render branch |
| `internal/tui/views/cmds.go` | **MODIFY** — update `OpenTerminalCmd` to use terminal package |
| `internal/tui/views/msgs.go` | **MODIFY** — add `OpenWorktreePickerMsg`, `OpenTerminalInWorktreeMsg`, scoped worktree load target |
| `internal/tui/views/overlay_help.go` | **MODIFY** — add new keyboard shortcuts |

---

## 15. Risks & Mitigations

| Risk | Likelihood | Mitigation |
|------|------------|------------|
| RepoManagerOverlay refactor breaks behavior | Medium | Extensive test matrix; incremental changes |
| SplitListPicker too generic | Low | Start simple, add complexity only when needed |
| Warp API never added | High | Document limitation; use `open -a Warp.app` as fallback |
| Kitty remote control not enabled | Medium | Require a target (`KITTY_WINDOW_ID` or `KITTY_LISTEN_ON`) and surface `kitten @` authorization failures as actionable errors |
| AppleScript permissions | Low | Guide user through System Preferences |
| Terminal detection wrong | Low | Prefer explicit env vars and executable discovery; future manual override via settings |
| Worktrees stale after picker open | Low | Re-load on repo selection (already implemented) |
| `t` conflicts with thinking toggle | Medium | Move thinking toggle to `Ctrl+T` before changing terminal shortcut; update hints/tests/help in the same commit. |

---

## 16. Future Enhancements (Out of Scope)

- **Terminal profile selection** — choose which iTerm2/Terminal profile to use
- **Open in existing tab** — re-use existing tab in same directory instead of new tab
- **SSH tunneling** — open terminal and auto-connect to remote
- **Multiple terminals** — open terminals in multiple worktrees at once
- **Warp Launch Configurations** — leverage Warp's launch configs feature once API exists
- **Configurable pane weights** — let user adjust split ratio
- **Persist picker state** — remember last selected repo/worktree

---

## Appendix A: Terminal Commands Reference

### WezTerm
```bash
# Preferred from inside WezTerm; uses WEZTERM_PANE to target the current window
wezterm cli spawn --cwd /path/to/worktree

# Fallback: ask an existing GUI instance for a new tab
wezterm start --new-tab --cwd /path/to/worktree

# New window
wezterm start --cwd /path/to/worktree

# With specific workspace
wezterm start --new-tab --cwd /path --workspace my-workspace
```

### Kitty
```bash
# From within kitty (uses KITTY_WINDOW_ID)
kitten @ launch --type=tab --cwd /path/to/worktree

# With title
kitten @ launch --type=tab --cwd /path --tab-title "feature-x"

# From outside (needs --listen-on configured)
kitten @ --to unix:/tmp/mykitty launch --type=tab --cwd /path
```

### iTerm2
```applescript
set worktreePath to "/path/to/worktree"
set cdCommand to "cd " & quoted form of worktreePath
tell application "iTerm2"
    tell current window
        create tab with default profile
        tell current session
            write text cdCommand
        end tell
    end tell
end tell
```

### Terminal.app
```applescript
set worktreePath to "/path/to/worktree"
set cdCommand to "cd " & quoted form of worktreePath
tell application "Terminal"
    do script cdCommand in front window
end tell
```

### Warp (Limited)
```bash
# Opens Warp in directory (new window, not tab)
open -a Warp.app /path/to/worktree
```

### Alacritty (Limited)
```bash
# Opens new window (no tab support)
alacritty --working-directory /path/to/worktree
```

---

## Appendix B: TERM_PROGRAM Values

| Terminal | `TERM_PROGRAM` Value |
|----------|---------------------|
| Warp | `WarpTerminal` or `Warp` |
| iTerm2 | `iTerm.app` |
| Terminal.app | `Apple_Terminal` |
| Kitty | Not set (use `KITTY_WINDOW_ID`) |
| WezTerm | Not set (use `WEZTERM_PANE`; `WEZTERM_SOCK` as fallback heuristic) |
| Alacritty | Not set |

---

## Appendix C: SplitListPicker API Reference

```go
// Constructors
func NewSplitListPicker(spec SplitOverlaySizingSpec) SplitListPicker

// Configuration
func (m *SplitListPicker) SetSize(width, height, chromeLines int)
func (m SplitListPicker) Layout() SplitOverlayLayout

// Focus
func (m SplitListPicker) Focus() SplitPaneFocus
func (m SplitListPicker) IsFocusLeft() bool
func (m *SplitListPicker) SwitchFocus()
func (m *SplitListPicker) FocusLeft()
func (m *SplitListPicker) FocusRight()
func (m *SplitListPicker) HandleFocusKey(key string) bool

// Rendering
func (m SplitListPicker) View(st styles.Styles, left, right SplitListPaneSpec) string
```

---

*Plan generated: 2026-05-18*
*Updated: 2026-05-18 — Added SplitListPicker component and RepoManagerOverlay refactor*
*Updated: 2026-05-19 — Fixed SplitListPicker View() to use correct OverlayPaneSpec fields; added focus helpers; fixed OverviewWorktreeRow.Worktrees to use gitwork.Worktree type; added Kitty remote-control target detection helper; clarified worktree reload hook pattern*
*Updated: 2026-05-22 — Fixed KITTY_PUBLIC_HOST → KITTY_LISTEN_ON (correct env var for --listen-on socket); fixed worktree reload comparison guidance*
*Updated: 2026-05-25 — Changed overview integration from action-card approach to direct key binding; use `t` for terminal actions; move session-log thinking toggle to `Ctrl+T`; changed agent session `o` key to `t`; changed SplitListPicker to focus/layout-only ownership; made worktree load responses scoped; corrected App-level overlay routing, terminal command safety, Kitty/WezTerm detection guidance.*
*Research sources: Warp GitHub Discussion #612, Issue #3959; iTerm2 AppleScript docs; Kitty remote control docs; WezTerm CLI docs; Alacritty Issue #6340*
