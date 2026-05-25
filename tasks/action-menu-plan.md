# Action Menu Implementation Plan

## Overview

Replace the current help overlay (triggered by `?`) with a unified action menu that:
- Is triggered by `x` (replacing `?`)
- Shows ALL available actions for the current context, sorted by priority
- Includes a fuzzy search bar that auto-filters on keystroke
- Displays keyboard shortcuts right-aligned at the end of each row
- Omits unavailable actions (no dimmed/disabled states)
- Closes on `Esc` or action execution
- Cursor starts at highest priority (first action)

Vim navigation bindings (`j`/`k`) are removed in favor of arrow keys.

---

## Design Decisions

| Decision | Choice |
|----------|--------|
| Structure | Flat list sorted by priority |
| Search | Fuzzy search at top, auto-filters on keystroke, no manual focus |
| Shortcut display | Right-aligned, no fixed column |
| Unavailable actions | Omitted entirely |
| Navigation bindings | Arrow keys only (no `j`/`k`) |
| Entry point | `x` keybinding |
| Help overlay | **Deleted** — `?` is removed |

---

## Phase 1: Foundation

### 1.1 Create `action_menu.go`

**File:** `internal/tui/views/action_menu.go`

Core types:
```go
type ActionContext int

type Action struct {
    ID       string
    Label    string
    Shortcut string
    Priority int
    Condition func(*App) bool
    Handler  func(*App) tea.Cmd
}

type ActionMenuModel struct {
    st     styles.Styles
    app    *App
    width  int
    height int

    actions []Action  // all available actions
    query   string   // search query
    matches []int    // indices into actions that match query
    cursor  int      // position within matches
}
```

Key methods:
- `NewActionMenuModel(styles.Styles) ActionMenuModel`
- `SetApp(*App)` — updates app pointer and refreshes action list
- `Update(tea.Msg) (ActionMenuModel, tea.Cmd)` — handles arrow keys, Enter, search
- `View() string` — renders the menu

### 1.2 Fuzzy Search

```go
func fuzzyMatch(query, label string) bool {
    if query == "" {
        return true
    }
    query = strings.ToLower(query)
    label = strings.ToLower(label)

    // Substring match (fast path)
    if strings.Contains(label, query) {
        return true
    }

    // Character-by-character match
    qi := 0
    for _, c := range label {
        if qi < len(query) && rune(query[qi]) == c {
            qi++
        }
    }
    return qi == len(query)
}
```

### 1.3 Search Key Handling

Extract printable characters from key messages for search:
- Letters `a-z`, `A-Z`
- Numbers `0-9`
- Space, hyphen, underscore
- Convert to lowercase for matching

Ignore: arrow keys, Enter, Escape, Tab, Ctrl+chords.

---

## Phase 2: Action Registry

### 2.1 Registry Structure

```go
func (a *App) BuildActionRegistry() []Action {
    ctx := a.currentActionContext()
    var actions []Action

    // Global actions (always included)
    actions = append(actions, globalActions(a)...)

    // Context-specific actions
    switch ctx {
    case ContextOverview:
        actions = append(actions, overviewActions(a)...)
        if card := a.content.overview.selectedActionCard(); card != nil {
            actions = append(actions, actionCardActions(a, card)...)
        }
    // ... other contexts
    }

    sort.Slice(actions, func(i, j int) bool {
        return actions[i].Priority < j.Priority
    })
    return actions
}
```

### 2.2 Context Determination

```go
func (a *App) currentActionContext() ActionContext {
    // Overlays first
    switch a.activeOverlay {
    case overlayNewSession:        return ContextNewSession
    case overlayAddRepo:           return ContextAddRepo
    case overlayRepoManager:        return ContextRepoManager
    case overlaySettings:          return ContextSettings
    case overlayLogs:              return ContextLogs
    }

    // Overview sub-overlays
    if a.content.Mode() == ContentModeOverview {
        switch a.content.overview.overlay {
        case overviewOverlayPlan:        return ContextPlanReview
        case overviewOverlayQuestion:    return ContextQuestion
        case overviewOverlayInterrupted: return ContextInterrupted
        case overviewOverlayReviewing:  return ContextReviewing
        case overviewOverlayCompleted:  return ContextCompleted
        }
        return ContextOverview
    }

    switch a.content.Mode() {
    case ContentModeSessionInteraction: return ContextSessionLog
    case ContentModeArtifacts:         return ContextArtifacts
    }
    return ContextGlobal
}
```

### 2.3 Complete Action Registry

#### Global (Priority 10-99)

| Priority | Label | Shortcut | Condition |
|----------|-------|----------|-----------|
| 10 | New session | `n` | Always |
| 11 | New autonomous session | `A` | Always |
| 20 | Open repo manager | `R` | Always |
| 30 | Open settings | `s` | Always |
| 40 | Open logs | `L` | Always |
| 50 | Search sessions | `/` | Always |
| 60 | Delete session | `d` | Deletable session selected |
| 70 | Archive session | `a` | Session archivable |
| 71 | Unarchive session | `a` | Session archived |
| 80 | Interrupt sessions | `I` | Interruptible sessions |
| 90 | Quit | `q` | Always |

#### Overview (Priority 100-199)

| Priority | Label | Shortcut | Condition |
|----------|-------|----------|-----------|
| 100 | Cycle action cards | `Tab` | Multiple actions |
| 110 | Inspect | `i` | Action card selected |
| 115 | Execute action | `Enter` | Action card selected |

#### Plan Review (Priority 200-299)

| Priority | Label | Shortcut | Condition |
|----------|-------|----------|-----------|
| 200 | Approve plan | `a` | Plan review action |
| 210 | Request changes | `i` | Plan review action |
| 220 | Copy plan | `c` | Plan review action |
| 225 | Edit plan in $EDITOR | `e` | Plan review action |

#### Session Log (Priority 300-399)

| Priority | Label | Shortcut | Condition |
|----------|-------|----------|-----------|
| 300 | Steer / prompt | `p` | Agent active/failed/completed |
| 310 | Go to bottom | `f` | Always |
| 320 | Go to top | `g` | Always |
| 330 | Toggle verbose | `v` | Always |
| 340 | Toggle thinking | `t` | Always |
| 345 | Open plan | `i` | Plan exists |
| 350 | Copy plan | `c` | Plan overlay open |
| 355 | Open terminal | `o` | Session log view, worktree exists |

#### Question (Priority 400-409)

| Priority | Label | Shortcut | Condition |
|----------|-------|----------|-----------|
| 400 | Send answer | `Enter` | Input active |
| 405 | Inspect | `i` | Always |

#### Interrupted (Priority 410-429)

| Priority | Label | Shortcut | Condition |
|----------|-------|----------|-----------|
| 410 | Resume | `r` | canAct |
| 420 | Abandon | `a` | Single session, canAct |

#### Reviewing (Priority 430-479)

| Priority | Label | Shortcut | Condition |
|----------|-------|----------|-----------|
| 430 | Navigate critique | `↑` | Always |
| 431 | Navigate critique | `↓` | Always |
| 440 | Switch repo | `Tab` | Multiple repos |
| 450 | Re-implement | `r` | Always |
| 460 | Override accept | `o` | Always |
| 470 | Inspect | `i` | Always |

#### Completed (Priority 480-499)

| Priority | Label | Shortcut | Condition |
|----------|-------|----------|-----------|
| 480 | Request changes | `i` | Always |
| 490 | Submit feedback | `Enter` | Feedback active |

#### Review Followup (Priority 500-599)

| Priority | Label | Shortcut | Condition |
|----------|-------|----------|-----------|
| 500 | Navigate | `↑` | Always |
| 501 | Navigate | `↓` | Always |
| 510 | Toggle selection | `Space` | Always |
| 520 | Select all | `a` | Always |
| 530 | Deselect all | `n` | Always |
| 540 | Confirm | `Enter` | Selection exists |
| 550 | Focus list | `←` | Selector stage |
| 560 | Focus preview | `→` | Selector stage |
| 570 | Address critique | `p` | Selection exists (selector) |
| 580 | Confirm replan | `y` | Confirm stage |

#### Artifacts (Priority 600-649)

| Priority | Label | Shortcut | Condition |
|----------|-------|----------|-----------|
| 600 | Navigate | `↑` | Always |
| 601 | Navigate | `↓` | Always |
| 610 | Expand | `→` | Always |
| 615 | Toggle expansion | `Space` | Always |
| 620 | Open artifact | `o` | URL exists |
| 630 | Open all artifacts | `O` | Multiple items |
| 640 | Start review followup | `f` | Review available |

#### Repo Manager (Priority 650-679)

| Priority | Label | Shortcut | Condition |
|----------|-------|----------|-----------|
| 650 | Add repo | `a` | Always |
| 660 | Delete repo | `d` | Repo selected |
| 665 | Init git-work | `i` | Plain git repo |
| 670 | Switch focus | `Tab` | Always |

#### New Session (Priority 680-749)

| Priority | Label | Shortcut | Condition |
|----------|-------|----------|-----------|
| 680 | Cycle provider | `Tab` | Always |
| 685 | Previous provider | `Shift+Tab` | Always |
| 690 | Cycle scope | `Ctrl+S` | Always |
| 695 | Cycle view | `Ctrl+V` | Always |
| 700 | Cycle state | `Ctrl+T` | Always |
| 705 | Reset | `Ctrl+R` | Always |
| 710 | Save filter | `Ctrl+F` | Always |
| 715 | Load filter | `Ctrl+L` | Always |
| 720 | Manual entry | `Ctrl+N` | Always |
| 725 | Toggle selection | `Space` | Always |
| 730 | Open in browser | `Ctrl+O` | Item selected |

#### Add Repo (Priority 750-779)

| Priority | Label | Shortcut | Condition |
|----------|-------|----------|-----------|
| 750 | Cycle source | `Tab` | Always |
| 755 | Manual URL | `Ctrl+N` | Always |
| 760 | Clear search | `Ctrl+R` | Always |
| 765 | Toggle owned filter | `Ctrl+G` | Always |
| 770 | Clone | `Enter` | Repo selected |

#### Settings (Priority 780-839)

| Priority | Label | Shortcut | Condition |
|----------|-------|----------|-----------|
| 780 | Move up | `↑` | Always |
| 785 | Move down | `↓` | Always |
| 790 | Focus sections | `←` | Always |
| 795 | Expand / focus fields | `→` | Always |
| 800 | Edit field | `e` | Field focused |
| 805 | Toggle boolean | `Space` | Bool field |
| 810 | Reveal secrets | `r` | Always |
| 815 | Apply settings | `s` | Changes pending |
| 820 | Test provider | `t` | Provider selected |
| 825 | Login provider | `g` | Provider selected |

#### Logs (Priority 840-849)

| Priority | Label | Shortcut | Condition |
|----------|-------|----------|-----------|
| 840 | Copy all | `y` | Always |

---

## Phase 3: App Integration

### 3.1 App.go Changes

**Add field to App struct:**
```go
type App struct {
    // ... existing fields ...
    actionMenu ActionMenuModel
}
```

**Add overlay constant:**
```go
const (
    overlayNone             = iota
    overlayWorkspaceInit
    overlayNewSession
    overlayNewSessionAutonomous
    overlaySessionSearch
    overlaySettings
    overlayActionMenu  // NEW
    overlayLogs
    overlayRepoManager
    overlayAddRepo
    overlaySourceItems
    overlayOverviewLinks
    overlayReviewFollowup
)
```

**Add `x` keybinding in `handleKeyMsg`:**
```go
case "x":
    a.actionMenu.SetApp(a)
    a.activeOverlay = overlayActionMenu
    return a, nil
```

**Remove `?` keybinding:**
```go
// REMOVE:
// case "?":
//     a.activeOverlay = overlayHelp
//     return a, nil
```

**Add overlay routing:**
```go
} else if a.activeOverlay == overlayActionMenu {
    a.actionMenu, cmd = a.actionMenu.Update(msg)
    return a, cmd
```

**Initialize in app construction:**
```go
func NewApp(...) *App {
    a := &App{
        // ... existing initialization ...
        actionMenu: NewActionMenuModel(st, (*App).BuildActionRegistry),
    }
    // Set app pointer after construction
    a.actionMenu.SetApp(a)
    return a
}
```

### 3.2 Delete `overlay_help.go`

Delete `internal/tui/views/overlay_help.go` entirely.

---

## Phase 4: Vim Bindings Removal

### 4.1 `reviewing_view.go`

Replace `j`/`k` with `↑`/`↓`:
```go
// BEFORE:
case "j":
    if m.cursor < len(m.repos[m.activeRepo].Critiques)-1 {
        m.cursor++
    }
case "k":
    if m.cursor > 0 {
        m.cursor--
    }

// AFTER:
case "down":
    if m.cursor < len(m.repos[m.activeRepo].Critiques)-1 {
        m.cursor++
    }
case "up":
    if m.cursor > 0 {
        m.cursor--
    }
```

### 4.2 Other Files

Search for `case "j":` and `case "k":` patterns that are list navigation (not vim-style editing in text inputs). Replace with `down`/`up`.

Files to check:
- `overlay_review_followup.go` — selector navigation
- `artifacts_view.go` — item navigation
- Any other list navigation patterns

---

## Phase 5: Testing

### 5.1 `action_menu_test.go`

Test cases:
1. **Fuzzy matching**: "new" matches "New session", "ns" matches "New session"
2. **Priority sorting**: Actions sorted correctly
3. **Condition filtering**: Conditional actions only appear when condition is true
4. **Search accumulation**: Typing "new s" filters to matching actions
5. **Cursor navigation**: Up/down moves through matches, wraps at edges
6. **Enter execution**: Enter triggers action handler
7. **Empty results**: Shows "No matching actions" message
8. **Rendering**: Shortcuts right-aligned, truncation works

### 5.2 Integration Tests

1. Open action menu via `x` in various contexts
2. Verify correct actions appear per context
3. Verify search filters correctly
4. Verify action execution closes menu

---

## Phase 6: Cleanup

1. Remove any references to `overlayHelp` or `HelpOverlay` in tests
2. Update any documentation mentioning `?` for help
3. Verify all keyboard shortcuts in status bar are correct

---

## File Changes Summary

| File | Change |
|------|--------|
| `internal/tui/views/action_menu.go` | **NEW** — ActionMenuModel and action registry |
| `internal/tui/views/action_menu_test.go` | **NEW** — Tests |
| `internal/tui/views/app.go` | Add action menu field, `x` binding, remove `?` binding |
| `internal/tui/views/overlay_help.go` | **DELETE** |
| `internal/tui/views/reviewing_view.go` | Replace `j`/`k` with `↓`/`↑` |
| Other files with `case "j":`/`case "k":` | Replace list navigation bindings |

---

## Open Questions (for verification during implementation)

1. **New session autonomous**: Does `A` for autonomous mode have a handler that needs wrapping in the registry?

2. **Interrupt sessions**: `I` key sends `ConfirmInterruptSessionsMsg`. Verify this is the correct handler signature.

3. **Open terminal**: `o` in session log view opens terminal. Confirm the handler extracts worktree path correctly.

4. **Override accept**: `o` in reviewing sends `ConfirmOverrideAcceptMsg`. Verify the context detection is correct.

5. **Extra context modal**: New session has an extra context modal that appears after selection. Does this need its own context?
