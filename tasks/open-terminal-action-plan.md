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
    // Check for kitty window ID
    if _, ok := os.LookupEnv("KITTY_WINDOW_ID"); ok {
        return TerminalKitty
    }
    // Kitty remote control requires allow_remote_control=yes in kitty.conf.
    // If KITTY_WINDOW_ID is not set, we cannot use kitten @ for the initial
    // tab open. Treat as unknown unless a socket is explicitly provided.
    // callers should verify KittyRemoteControlEnabled() before using kitten @.
    // Check for wezterm socket
    if _, ok := os.LookupEnv("WEZTERM_SOCK"); ok {
        return TerminalWezTerm
    }
    // Check parent process as fallback
    // ...
    return TerminalUnknown
}

// KittyRemoteControlEnabled checks whether Kitty's remote control is enabled.
// It returns true if either the KITTY_WINDOW_ID env var is set (indicating an
// active Kitty session that can be remote-controlled) or if the --listen-on
// flag was used when launching Kitty (socket path in KITTY_LISTEN_ON).
// Without this, kitten @ commands will fail.
func KittyRemoteControlEnabled() bool {
    if _, ok := os.LookupEnv("KITTY_WINDOW_ID"); ok {
        return true
    }
    // Also check for explicitly configured listen socket.
    if _, ok := os.LookupEnv("KITTY_LISTEN_ON"); ok {
        return true
    }
    return false
}
```

### 1.3 Terminal-Specific Opening Commands

| Terminal | Support | Command |
|----------|---------|---------|
| **WezTerm** | ✅ Full | `wezterm start --new-tab --cwd /path` |
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
// Falls back to Terminal.app if the terminal is not available.
func OpenWithTerminal(dir string, term TerminalType) (TerminalType, error)
```

### 1.5 Error Handling & User Feedback

- If no supported terminal detected → log warning, try `open` command as last resort
- If AppleScript fails → log warning with specific error
- Warp: Show info message that Warp lacks tab support; open in new window via `open -a Warp.app`
- Alacritty: Show info message about tmux recommendation; open new window

---

## 2. TUI Command Layer

### 2.1 Extend `internal/tui/views/cmds.go`

Replace the existing `OpenTerminalCmd` with a more sophisticated version:

```go
// OpenTerminalCmd opens a new terminal tab/window in the specified directory.
// The terminal is auto-detected based on the active terminal.
// Returns nil (fire-and-forget).
func OpenTerminalCmd(dir string) tea.Cmd {
    return func() tea.Msg {
        termType, err := terminal.Open(dir)
        if err != nil {
            slog.Warn("failed to open terminal", "path", dir, "error", err)
            return ErrMsg{Err: fmt.Errorf("open terminal in %s: %w", dir, err)}
        }
        slog.Debug("opened terminal", "path", dir, "terminal", termType)
        return nil
    }
}

// OpenTerminalWithCmd opens in a specific terminal type.
func OpenTerminalWithCmd(dir string, termType terminal.TerminalType) tea.Cmd {
    return func() tea.Cmd {
        _, err := terminal.OpenWithTerminal(dir, termType)
        if err != nil {
            slog.Warn("failed to open terminal", "path", dir, "terminal", termType, "error", err)
        }
        return nil
    }
}
```

### 2.2 Keep Existing Shell Escape Utility

The `shellEscape` function in `cmds.go:1135-1138` remains for other AppleScript uses.

---

## 3. Shared Split-List Picker Component

### 3.1 Design Rationale

Both the new **WorktreePickerOverlay** and the existing **RepoManagerOverlay** use a split-pane layout:

| Component | Left Pane | Right Pane | Purpose |
|-----------|-----------|------------|---------|
| RepoManagerOverlay | Repo list | Detail viewport (text, scrollable) | Manage repos with actions |
| WorktreePickerOverlay | Repo list | Worktree list | Select worktree to open |

The right pane differs in type (list vs. viewport), making full unification awkward. Instead, we create a **generic split-pane picker base** that handles:

- Split-pane geometry and layout computation
- Focus management (Tab/←/→)
- Navigation key handling (↑/↓)
- Size synchronization

Each overlay then composes this base and provides pane-specific data and actions.

### 3.2 New File: `internal/tui/components/split_list_picker.go`

```go
// internal/tui/components/split_list_picker.go

package components

// PaneType identifies what's in a pane.
type PaneType int

const (
    PaneTypeList PaneType = iota
    PaneTypeViewport
)

// PaneConfig describes a pane in the split layout.
type PaneConfig struct {
    Type       PaneType
    Title      string      // Optional header
    List       *list.Model // Set if Type == PaneTypeList
    Viewport   *viewport.Model // Set if Type == PaneTypeViewport
    Width      int
    Height     int
}

// SplitListPicker is a reusable split-pane picker component.
// It manages two panes side-by-side with focus switching and shared key handling.
type SplitListPicker struct {
    Left  PaneConfig
    Right PaneConfig

    // Which pane has focus
    FocusLeft bool

    // Styling
    Styles styles.Styles

    // Computed layout
    layout SplitOverlayLayout

    // prevLeftIndex tracks the left pane index before the last Update call,
    // enabling callers to detect selection changes that should trigger side
    // effects (e.g. reloading worktrees when the repo selection changes).
    prevLeftIndex int
}

// NewSplitListPicker creates a new split-list picker with the given pane configs.
func NewSplitListPicker(left, right PaneConfig, st styles.Styles) SplitListPicker {
    return SplitListPicker{
        Left:   left,
        Right:  right,
        FocusLeft: true,
        Styles: st,
    }
}

// SetSize recalculates layout dimensions.
func (m *SplitListPicker) SetSize(width, height int) {
    // Use existing ComputeSplitOverlayLayout logic
    m.layout = ComputeSplitOverlayLayout(width, height, chromeLines, browseSizingSpec)
}

// FocusedPane returns the currently focused pane config.
func (m *SplitListPicker) FocusedPane() *PaneConfig {
    if m.FocusLeft {
        return &m.Left
    }
    return &m.Right
}

// SwitchFocus toggles focus between left and right panes.
func (m *SplitListPicker) SwitchFocus() {
    m.FocusLeft = !m.FocusLeft
}

// IsFocusLeft reports whether the left pane has focus.
func (m *SplitListPicker) IsFocusLeft() bool {
    return m.FocusLeft
}

// Update handles keyboard events and returns any commands.
func (m *SplitListPicker) Update(msg tea.Msg) (SplitListPicker, tea.Cmd) {
    var cmd tea.Cmd
    
    switch msg := msg.(type) {
    case tea.KeyMsg:
        switch msg.String() {
        case "tab", "left", "right":
            m.SwitchFocus()
            return m, nil
            
        case "up", "k", "down", "j":
            pane := m.FocusedPane()
            if pane.Type == PaneTypeList && pane.List != nil {
                *pane.List, cmd = pane.List.Update(msg)
            } else if pane.Type == PaneTypeViewport && pane.Viewport != nil {
                *pane.Viewport, cmd = pane.Viewport.Update(msg)
            }
            return m, cmd
        }
    }
    
    // Forward to focused pane
    pane := m.FocusedPane()
    if pane.Type == PaneTypeList && pane.List != nil {
        *pane.List, cmd = pane.List.Update(msg)
    } else if pane.Type == PaneTypeViewport && pane.Viewport != nil {
        *pane.Viewport, cmd = pane.Viewport.Update(msg)
    }
    
    return m, cmd
}

// View renders the split pane layout.
func (m SplitListPicker) View() string {
    return RenderSplitOverlayBody(m.Styles, m.layout, SplitOverlaySpec{
        LeftPane:  m.paneSpec(m.Left, m.FocusLeft),
        RightPane: m.paneSpec(m.Right, !m.FocusLeft),
    })
}

// paneSpec returns the OverlayPaneSpec for the given pane, with the rendered body.
func (m SplitListPicker) paneSpec(pane PaneConfig, focused bool) OverlayPaneSpec {
    var body string
    switch pane.Type {
    case PaneTypeList:
        if pane.List != nil {
            body = pane.List.View()
        }
    case PaneTypeViewport:
        if pane.Viewport != nil {
            body = pane.Viewport.View()
        }
    }
    return OverlayPaneSpec{
        Title:       pane.Title,
        DividerWidth: m.layout.LeftPaneWidth,
        Body:         body,
        Focused:      focused,
    }
}

// SelectedIndex returns the selected item index in the focused list pane, or -1.
func (m SplitListPicker) SelectedIndex() int {
    pane := m.FocusedPane()
    if pane.Type == PaneTypeList && pane.List != nil {
        return pane.List.Index()
    }
    return -1
}

// SetSelectedIndex sets the selection in the focused list pane.
func (m *SplitListPicker) SetSelectedIndex(idx int) {
    pane := m.FocusedPane()
    if pane.Type == PaneTypeList && pane.List != nil {
        pane.List.Select(idx)
    }
}

// FocusRight sets focus to the right pane.
func (m *SplitListPicker) FocusRight() {
    m.FocusLeft = false
}

// SetLeft replaces the left pane configuration.
func (m *SplitListPicker) SetLeft(pane PaneConfig) {
    m.Left = pane
    // Reset prevLeftIndex since pane content changed.
    m.prevLeftIndex = 0
}

// SetRight replaces the right pane configuration.
func (m *SplitListPicker) SetRight(pane PaneConfig) {
    m.Right = pane
}

// LeftIndex returns the selected index of the left pane's list, or -1.
func (m SplitListPicker) LeftIndex() int {
    if m.Left.Type == PaneTypeList && m.Left.List != nil {
        return m.Left.List.Index()
    }
    return -1
}

// RightIndex returns the selected index of the right pane's list, or -1.
func (m SplitListPicker) RightIndex() int {
    if m.Right.Type == PaneTypeList && m.Right.List != nil {
        return m.Right.List.Index()
    }
    return -1
}

// PrevLeftIndex returns the selected index before the last Update call,
// useful for detecting selection changes to trigger side effects (e.g. loading
// worktrees when the repo selection changes).
func (m *SplitListPicker) PrevLeftIndex() int {
    return m.prevLeftIndex
}

// TrackPrevLeft saves the current left pane index as the previous value.
func (m *SplitListPicker) TrackPrevLeft() {
    m.prevLeftIndex = m.LeftIndex()
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
// In an overlay's Update method
func (m *MyOverlay) Update(msg tea.Msg) (MyOverlay, tea.Cmd) {
    switch msg := msg.(type) {
    case tea.WindowSizeMsg:
        m.picker.SetSize(msg.Width, msg.Height)
        
    case tea.KeyMsg:
        m.picker, cmd := m.picker.Update(msg)
        // Handle pane-specific actions based on SelectedIndex()
        
    default:
        m.picker, cmd = m.picker.Update(msg)
    }
    return m, cmd
}
```

---

## 4. Refactor: RepoManagerOverlay to Use SplitListPicker

### 4.1 Current State

`RepoManagerOverlay` (overlay_repo_manager.go:47-606) has inline split-pane logic:

- Custom `focusPane` enum and switching logic
- Duplicate navigation handling
- Manual size calculations

### 4.2 Target State

```go
// overlay_repo_manager.go (refactored)

type RepoManagerOverlay struct {
    workspaceDir string
    gitClient    *gitwork.Client
    
    repos []managedRepo
    
    // Shared picker component
    picker components.SplitListPicker
    
    // Pane-specific state (extracted from picker for clarity)
    repoList   list.Model
    worktrees  []gitwork.Worktree
    worktreeReqID int
    
    // Overlay-specific state
    pendingDelete *managedRepo
    pendingInit   *managedRepo
    loading       bool
    
    styles  styles.Styles
    width   int
    height  int
    active  bool
}
```

### 4.3 Refactoring Steps

1. **Extract pane configuration** — identify how each pane is configured
2. **Wire picker to RepoManagerOverlay** — replace inline focus/panes with picker
3. **Preserve action handlers** — `a`, `d`, `i` keys remain overlay-specific
4. **Update detail rendering** — `syncDetailViewport` continues to use viewport pane

### 4.4 Key Changes

| Before | After |
|--------|-------|
| Custom `focusPane` tracking | `picker.FocusLeft` |
| Manual `handleKey` focus switching | `picker.Update` handles Tab/←/→ |
| Inline navigation → worktree loading | Navigation via picker, side effects in overlay |

### 4.5 Verify Behavior Preserved

Test these interactions after refactoring:

| Action | Test |
|--------|------|
| Tab/←/→ | Focus switches between repo list and detail |
| ↑/k, ↓/j (list focused) | Navigate repos, loads worktrees |
| ↑/k, ↓/j (detail focused) | Scroll detail viewport |
| `a` | Opens Add Repo overlay |
| `d` | Shows delete confirmation |
| `i` | Initiates repo initialization |
| Esc | Closes overlay |

---

## 5. New Worktree Picker Overlay

### 5.1 Overview

A split-pane picker overlay for selecting repo+worktree combinations to open in terminal.

```
┌─ Open Terminal ─────────────────────────────────────┐
│                                                      │
│  [Repo A]──────────────┬─ Worktrees ────────────────│
│  [Repo B]              │  ├─ main (main)           │
│  [Repo C]              │  ├─ feature-x (active)    │
│                        │  └─ feature-y              │
│                        │                            │
│                        ├─ Keybind hints ────────────│
│                        │  [t] Open terminal         │
└────────────────────────┴─────────────────────────────┘
```

### 5.2 New File: `internal/tui/views/overlay_worktree_picker.go`

```go
// overlay_worktree_picker.go

type WorktreePickerOverlay struct {
    workspaceDir string
    gitClient    *gitwork.Client
    
    repos    []managedRepo
    worktrees []gitwork.Worktree
    
    // Shared picker component
    picker components.SplitListPicker
    
    // Pane-specific models
    repoList      list.Model
    worktreeList  list.Model
    
    // Worktree loading state
    worktreesLoad   bool
    worktreeReqID   int
    
    // Overlay state
    styles  styles.Styles
    width   int
    height  int
    active  bool
}

// NewWorktreePickerOverlay creates the overlay.
func NewWorktreePickerOverlay(
    workspaceDir string,
    gitClient *gitwork.Client,
    st styles.Styles,
) WorktreePickerOverlay {
    // Create list models
    repoDelegate := list.NewDefaultDelegate()
    repoDelegate.ShowDescription = true
    repoList := list.New([]list.Item{}, repoDelegate, 60, 10)
    // ... configure
    
    worktreeDelegate := list.NewDefaultDelegate()
    worktreeList := list.New([]list.Item{}, worktreeDelegate, 60, 10)
    // ... configure
    
    // Build initial picker config
    picker := components.SplitListPicker{
        Left: components.PaneConfig{
            Type:  components.PaneTypeList,
            Title: "Repositories",
            List:  &repoList,
        },
        Right: components.PaneConfig{
            Type:  components.PaneTypeList,
            Title: "Worktrees",
            List:  &worktreeList,
        },
        Styles: st,
    }
    
    return WorktreePickerOverlay{
        workspaceDir: workspaceDir,
        gitClient:    gitClient,
        repoList:     repoList,
        worktreeList: worktreeList,
        picker:       picker,
        styles:       st,
    }
}
```

### 5.3 Key Bindings

| Key | Action |
|-----|--------|
| `Tab` / `←` / `→` | Switch focus between repo list and worktree list |
| `↑/k` `↓/j` | Navigate current pane |
| `t` | Open terminal in selected worktree |
| `Enter` | Open terminal in selected worktree (alternative) |
| `Esc` | Close overlay |

### 5.4 Data Flow

```
1. User opens picker (from overview action)
   └─ WorktreePickerOverlay.Open() called
      └─ Load repos via workspace scan
         └─ Populate repoList with managedRepo items

2. User navigates repo list
   └─ On index change: LoadWorktreesCmd for selected repo
      └─ WorktreesLoadedMsg received
         └─ Populate worktreeList

3. User presses t/Enter on selected worktree
   └─ Get selected worktree path
   └─ Return OpenTerminalInWorktreeMsg
      └─ App handler: OpenTerminalCmd(path)
```

### 5.5 Worktree Loading (Reuse Existing Pattern)

The `SplitListPicker` does not automatically trigger side effects on selection change.
`WorktreePickerOverlay` must detect left-pane selection changes and fire `LoadWorktreesCmd`:

```go
// maybeLoadWorktrees fires LoadWorktreesCmd for the currently selected repo.
func (m *WorktreePickerOverlay) maybeLoadWorktrees() tea.Cmd {
    selected := m.repoList.Index()
    if selected < 0 || selected >= len(m.repos) {
        return nil
    }

    // Clear stale worktrees
    m.worktrees = nil
    m.worktreeList.SetItems([]list.Item{})
    m.worktreesLoad = true
    m.worktreeReqID++

    return LoadWorktreesCmd(
        m.gitClient,
        m.repos[selected].Path,
        m.worktreeReqID,
    )
}

// In WorktreePickerOverlay.Update:
case tea.KeyMsg:
    // Track selection before processing so we can compare after.
    m.picker.TrackPrevLeft()
    m.picker, cmd := m.picker.Update(msg)
    // Detect left-pane selection change → reload worktrees.
    if m.picker.PrevLeftIndex() != m.picker.LeftIndex() {
        cmds = append(cmds, m.maybeLoadWorktrees())
    }
    return m, tea.Batch(cmds...)

// On receiving WorktreesLoadedMsg:
if msg.RequestID == m.worktreeReqID {
    m.worktrees = msg.Worktrees
    m.worktreesLoad = false
    // Populate worktreeList items...
}
```

---

## 6. Overview View Integration

### 6.1 Remove Existing `t` Viewport Navigation

The current overview has a `t` keybinding for viewport navigation (lines ~521). **Remove this binding** as part of the vim bindings removal plan. The `t` key will now be used exclusively for opening the terminal picker.

Find and remove from `overview.go`:
```go
case "up", keyDown, "j", "k", "pgup", "pgdown", "home", "end":
    m.viewport, cmd = m.viewport.Update(msg)
    return m, cmd
```

### 6.2 Add `t` Key Handler for Terminal Picker

Add `t` key handling directly in the overview's Update handler (near line 473, alongside the existing `o` handler):

```go
// overview.go - in SessionOverviewModel.Update() key handling
case "t":
    // Open the worktree picker overlay
    m.overlay = overviewOverlayWorktreePicker
    cmd := m.openWorktreePicker()
    return m, cmd
```

### 6.3 App Message and Overlay Routing

The picker overlay uses `OpenWorktreePickerMsg` to trigger opening:

```go
// msgs.go
type OpenWorktreePickerMsg struct{}
```

Route the message in the app-level Update handler to open the overlay.

---

## 7. Agent Session View Enhancement

### 7.1 Key Change: `o` → `t`

Change the terminal shortcut from `o` to `t` to align with the unified keybinding:

```go
case "t":
    // Open terminal in worktree when in session view.
    if a.mainFocus == mainFocusContent && a.content.Mode() == ContentModeAgentSession {
        if sessionID := a.content.sessionLog.SessionID(); sessionID != "" {
            if session := a.workItemTaskSession(a.currentWorkItemID, sessionID); session != nil && session.WorktreePath != "" {
                return a, OpenTerminalCmd(session.WorktreePath)
            }
        }
        break
    }
```

### 7.2 Status Bar Hints

In `sessionLog.KeybindHints()` or wherever session view hints are defined:

```go
// Add to existing hints
hints = append(hints, KeybindHint{Key: "t", Label: "Open Terminal"})
```

---

## 8. Worktree Picker Message Flow

### 8.1 Messages (msgs.go)

```go
// OpenWorktreePickerMsg signals opening the worktree picker overlay.
type OpenWorktreePickerMsg struct{}

// OpenTerminalInWorktreeMsg is sent when user selects a worktree to open.
type OpenTerminalInWorktreeMsg struct {
    WorktreePath string
}

// WorktreesLoadedMsg (already exists in cmds.go:1622-1635)
```

### 8.2 App Handler

```go
// app.go - in App.Update
case OpenTerminalInWorktreeMsg:
    return a, OpenTerminalCmd(msg.WorktreePath)
```

### 8.3 Worktree Picker Commands

```go
// overlay_worktree_picker.go

// openTerminalCmd returns the command to open terminal in selected worktree.
func (m *WorktreePickerOverlay) openTerminalCmd() tea.Cmd {
    if m.worktreesLoad || len(m.worktrees) == 0 {
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

---

## 9. Keyboard Shortcut Summary

| Location | Key | Action |
|----------|-----|--------|
| Overview | `t` | Open worktree picker overlay |
| Worktree picker | `t` | Open terminal in selected worktree |
| Worktree picker | `Enter` | Open terminal in selected worktree |
| Worktree picker | `Tab` / `←/→` | Switch focus between panes |
| Repo manager | `Tab` / `←/→` | Switch focus between panes (existing) |
| Agent session view | `t` | Open terminal in session's worktree |

---

## 10. Implementation Order

### Phase 1: Terminal Package (Foundation)
1. Create `internal/terminal/detect.go` — terminal detection
2. Create `internal/terminal/open.go` — terminal opening logic
3. Update `OpenTerminalCmd` in `cmds.go` to use new terminal package
4. Test with each terminal type

### Phase 2: SplitListPicker Component
1. Create `internal/tui/components/split_list_picker.go`
2. Define `PaneConfig`, `SplitListPicker` types
3. Implement `SetSize`, `Update`, `View`, `SwitchFocus`, `FocusedPane`
4. Write unit tests for component behavior
5. Verify compatible with existing `ComputeSplitOverlayLayout`

### Phase 3: Refactor RepoManagerOverlay
1. Add `SplitListPicker` to `RepoManagerOverlay`
2. Extract pane configurations
3. Wire picker `Update` call in overlay's `Update`
4. Preserve action handlers (`a`, `d`, `i`)
5. Verify all interactions work (see test matrix in §4.5)
6. Delete now-redundant inline split-pane logic

### Phase 4: WorktreePickerOverlay
1. Create `overlay_worktree_picker.go`
2. Compose `SplitListPicker` with two list panes
3. Wire `LoadWorktreesCmd` on repo selection change
4. Add `t`/`Enter` key handler for terminal opening
5. Test picker navigation and terminal opening

### Phase 5: Overview Integration
1. Remove existing `t` viewport navigation binding from overview.go
2. Add `t` key handler to open worktree picker overlay
3. Add `OpenWorktreePickerMsg` to msgs.go
4. Route `OpenWorktreePickerMsg` in app-level Update to show picker overlay

### Phase 6: Polish
1. Add status bar hints for session view
2. Update help overlay with new shortcuts
3. Error handling improvements
4. Edge case handling (no repos, no worktrees, etc.)

---

## 11. Testing Strategy

### 11.1 Unit Tests

**SplitListPicker Component:**
```go
// internal/tui/components/split_list_picker_test.go
func TestSplitListPicker_FocusSwitch(t *testing.T) { /* ... */ }
func TestSplitListPicker_NavigationLeft(t *testing.T) { /* ... */ }
func TestSplitListPicker_NavigationRight(t *testing.T) { /* ... */ }
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
func TestOpen_TerminalApp(t *testing.T) { /* ... */ }
func TestOpen_ITerm2(t *testing.T) { /* ... */ }
func TestOpen_Kitty(t *testing.T) { /* ... */ }
func TestOpen_WezTerm(t *testing.T) { /* ... */ }
func TestOpen_Warp(t *testing.T) { /* ... */ }
func TestOpen_Alacritty(t *testing.T) { /* ... */ }
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
- Fall back to `open /path` (opens Finder on macOS if path is a directory)
- Or: use `open -a Terminal.app /path`

### 12.4 Permission Denied (AppleScript)
- macOS may require user permission for AppleScript
- Log detailed error for debugging

### 12.5 Kitty Remote Control Not Enabled
- Kitty requires `allow_remote_control=yes` in `~/.config/kitty/kitty.conf`
- Verify with `KittyRemoteControlEnabled()` before attempting `kitten @` (checks `KITTY_WINDOW_ID` or `KITTY_LISTEN_ON`)
- If not enabled: show user a one-time info message with instructions
- Fall back to `open /path` (opens file manager) if user declines

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
| `internal/tui/components/split_list_picker.go` | **NEW** — shared picker component |
| `internal/tui/components/split_list_picker_test.go` | **NEW** — component tests |
| `internal/tui/views/overlay_repo_manager.go` | **REFACTOR** — use `SplitListPicker` |
| `internal/tui/views/overlay_worktree_picker.go` | **NEW** — picker overlay |
| `internal/tui/views/overview.go` | **MODIFY** — remove `t` viewport nav, add `t` handler for picker |
| `internal/tui/views/app.go` | **MODIFY** — add `overlayWorktreePicker` enum, message handlers |
| `internal/tui/views/cmds.go` | **MODIFY** — update `OpenTerminalCmd` to use terminal package |
| `internal/tui/views/msgs.go` | **MODIFY** — add `OpenWorktreePickerMsg`, `OpenTerminalInWorktreeMsg` |
| `internal/tui/views/help.go` | **MODIFY** — add new keyboard shortcuts |

---

## 15. Risks & Mitigations

| Risk | Likelihood | Mitigation |
|------|------------|------------|
| RepoManagerOverlay refactor breaks behavior | Medium | Extensive test matrix; incremental changes |
| SplitListPicker too generic | Low | Start simple, add complexity only when needed |
| Warp API never added | High | Document limitation; use `open -a Warp.app` as fallback |
| Kitty remote control not enabled | Medium | Check for `KITTY_WINDOW_ID`, warn user if not available |
| AppleScript permissions | Low | Guide user through System Preferences |
| Terminal detection wrong | Low | Provide manual override via settings |
| Worktrees stale after picker open | Low | Re-load on repo selection (already implemented) |
| **Vim bindings removal blocks `t` key** | High | This plan assumes vim bindings (including `t` viewport nav) are removed first. Coordinate with the vim bindings removal plan before implementing Phase 5. |

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
# New tab in existing window, with CWD
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
tell application "iTerm2"
    tell current window
        create tab with default profile
        tell current session
            write text "cd /path/to/worktree"
        end tell
    end tell
end tell
```

### Terminal.app
```applescript
tell application "Terminal"
    do script "cd /path/to/worktree" in front window
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
| WezTerm | Not set (use `WEZTERM_SOCK`) |
| Alacritty | Not set |

---

## Appendix C: SplitListPicker API Reference

```go
// Constructors
func NewSplitListPicker(left, right PaneConfig, st styles.Styles) SplitListPicker

// Configuration
func (m *SplitListPicker) SetSize(width, height int)
func (m *SplitListPicker) SetLeft(pane PaneConfig)
func (m *SplitListPicker) SetRight(pane PaneConfig)

// Focus
func (m *SplitListPicker) FocusedPane() *PaneConfig
func (m *SplitListPicker) IsFocusLeft() bool       // Returns true if left pane is focused
func (m *SplitListPicker) SwitchFocus()            // Toggle focus between panes
func (m *SplitListPicker) FocusRight()             // Set focus to right pane

// Selection — operate on the currently focused pane
func (m *SplitListPicker) SelectedIndex() int      // Focused pane list index, or -1
func (m *SplitListPicker) SetSelectedIndex(idx int)

// Selection — operate on a specific pane (useful for detecting changes)
func (m *SplitListPicker) LeftIndex() int         // Left pane list index, or -1
func (m *SplitListPicker) RightIndex() int        // Right pane list index, or -1

// Change tracking — call TrackPrevLeft before Update to detect selection changes
func (m *SplitListPicker) PrevLeftIndex() int     // Left index before last Update
func (m *SplitListPicker) TrackPrevLeft()         // Save current left index as previous

// Lifecycle
func (m *SplitListPicker) Update(msg tea.Msg) (SplitListPicker, tea.Cmd)
func (m *SplitListPicker) View() string
```

---

*Plan generated: 2026-05-18*
*Updated: 2026-05-18 — Added SplitListPicker component and RepoManagerOverlay refactor*
*Updated: 2026-05-19 — Fixed SplitListPicker View() to use correct OverlayPaneSpec fields; added FocusRight/SetLeft/SetRight/LeftIndex/RightIndex/PrevLeftIndex/TrackPrevLeft methods; fixed OverviewWorktreeRow.Worktrees to use gitwork.Worktree type; added KittyRemoteControlEnabled() detection helper; clarified worktree reload hook pattern*
*Updated: 2026-05-22 — Fixed KITTY_PUBLIC_HOST → KITTY_LISTEN_ON (correct env var for --listen-on socket); fixed worktree reload comparison to use picker state consistently; reset prevLeftIndex in SetLeft/SetRight*
*Updated: 2026-05-25 — Changed overview integration from action-card approach to direct key binding; use `t` universally for terminal actions; remove existing `t` viewport nav binding from overview (vim bindings removal); changed agent session `o` key to `t`; added IsFocusLeft() to SplitListPicker*
*Research sources: Warp GitHub Discussion #612, Issue #3959; iTerm2 AppleScript docs; Kitty remote control docs; WezTerm CLI docs; Alacritty Issue #6340*
