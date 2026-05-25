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
    case overlayNewSession, overlayNewSessionAutonomous:
        return ContextNewSession
    case overlayAddRepo:
        return ContextAddRepo
    case overlayRepoManager:
        return ContextRepoManager
    case overlaySettings:
        return ContextSettings
    case overlayLogs:
        return ContextLogs
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
    case ContentModeSourceDetails:     return ContextSourceDetails
    }
    return ContextGlobal
}
```

### 2.3 Complete Action Registry

#### Global (Priority 10-99)

| Priority | Label | Shortcut | Condition | Handler |
|----------|-------|----------|-----------|----------|
| 10 | New session | `n` | Always | `a.openNewSession()` |
| 11 | New autonomous session | `A` | Always | `a.openNewSessionAutonomousOverlay()` |
| 20 | Open repo manager | `R` | Always | `a.openRepoManager()` |
| 30 | Open settings | `s` | Always | set `overlaySettings` |
| 40 | Open logs | `L` | Always | set `overlayLogs`, open logs |
| 50 | Search sessions | `/` | Always | `a.openSessionSearch()` |
| 60 | Delete session | `d` | `deletableSessionID() != ""` | `showDeleteSessionConfirm(id)` |
| 70 | Archive session | `a` | `archivablSessionID() != ""` AND NOT `unarchivablSessionID()` | `showArchiveConfirm(id)` |
| 71 | Unarchive session | `a` | `unarchivablSessionID() != ""` | `showUnarchiveConfirm(id)` |
| 80 | Interrupt sessions | `I` | `len(interruptibleFocusedSessionIDs()) > 0` | `ConfirmInterruptSessionsMsg{ids}` |
| 90 | Quit | `q` | Always | `a.handleQuitRequest()` |

#### Overview (Priority 100-199)

| Priority | Label | Shortcut | Condition | Handler |
|----------|-------|----------|-----------|----------|
| 100 | Cycle action cards | `Tab` | `len(actions) > 1` | delegate to overview |
| 110 | Inspect | `i` | Action card selected | delegate to overview |
| 115 | Execute action | `Enter` | Action card selected | delegate to overview |
| 275 | Open external links | `o` | Sources or reviews exist, no reviewing action | `OpenOverviewLinksMsg` |

#### Plan Review (Priority 200-299)

| Priority | Label | Shortcut | Condition | Handler |
|----------|-------|----------|-----------|----------|
| 200 | Approve plan | `a` | Plan review action card | `PlanApproveMsg` |
| 210 | Request changes | `i` | Plan review action card | `m.OpenFeedback()` |
| 220 | Copy plan | `c` | Plan review action card | clipboard.WriteAll |
| 225 | Edit plan in $EDITOR | `e` | Plan review action card | `editPlanInEditorCmd()` |

#### Session Log (Priority 300-399)

| Priority | Label | Shortcut | Condition | Handler |
|----------|-------|----------|-----------|----------|
| 300 | Steer / prompt | `p` | live/failed/completed session | set steerActive |
| 310 | Go to bottom | `f` | Always | `viewport.GotoBottom()` |
| 320 | Go to top | `g` | Always | `viewport.GotoTop()` |
| 330 | Toggle verbose | `v` | Always | toggle `m.verbose` |
| 340 | Toggle thinking | `t` | Always | toggle `m.collapseThinking` |
| 345 | Open plan | `i` | Plan exists | set `planOverlay` |
| 350 | Copy plan | `c` | Plan overlay open | clipboard.WriteAll |
| 355 | Open terminal | `o` | Session view, worktree exists | `OpenTerminalCmd(path)` |

#### Question (Priority 400-409)

| Priority | Label | Shortcut | Condition | Handler |
|----------|-------|----------|-----------|----------|
| 400 | Send answer | `Enter` | Input active | `AnswerQuestionMsg` |
| 405 | Inspect | `i` | Always | set `overviewOverlayQuestion` |

#### Interrupted (Priority 410-429)

| Priority | Label | Shortcut | Condition | Handler |
|----------|-------|----------|-----------|----------|
| 410 | Resume | `r` | canAct | `ResumeSessionMsg` or `RestartPlanMsg` |
| 420 | Abandon | `a` | Single session, canAct | `ConfirmAbandonMsg` |

#### Reviewing (Priority 430-479)

| Priority | Label | Shortcut | Condition | Handler |
|----------|-------|----------|-----------|----------|
| 430 | Navigate critique | `↑` | Always | `cursor--` |
| 431 | Navigate critique | `↓` | Always | `cursor++` |
| 440 | Switch repo | `Tab` | Multiple repos | `activeRepo++` |
| 450 | Re-implement | `r` | Always | `ReimplementMsg` |
| 460 | Override accept | `o` | Reviewing action card | `ConfirmOverrideAcceptMsg` |
| 470 | Inspect | `i` | Always | set `overviewOverlayReviewing` |

#### Completed (Priority 480-499)

| Priority | Label | Shortcut | Condition | Handler |
|----------|-------|----------|-----------|----------|
| 480 | Request changes | `i` | Always | `m.OpenFeedback()` |
| 490 | Submit feedback | `Enter` | Feedback active | `FollowUpSessionMsg` |

#### Review Followup (Priority 500-599)

| Priority | Label | Shortcut | Condition | Handler |
|----------|-------|----------|-----------|----------|
| 500 | Navigate | `↑` | Always | `moveCursor(-1)` |
| 501 | Navigate | `↓` | Always | `moveCursor(1)` |
| 510 | Toggle selection | `Space` | Always | `toggleAtCursor()` |
| 520 | Select all | `a` | Always | `selectAll()` |
| 530 | Deselect all | `n` | Always | `selectNone()` |
| 540 | Confirm | `Enter` | Picker: any selected | `applyPickerSelection()` |
| 550 | Focus list | `←` | Selector stage | `focus = reviewSelectorFocusList` |
| 560 | Focus preview | `→` | Selector stage | `focus = reviewSelectorFocusPreview` |
| 570 | Address critique | `p` | Selector: selection exists | `dispatchAddress()` |
| 580 | Confirm replan | `y` | Confirm stage | `dispatchReplan()` |

#### Artifacts (Priority 600-649)

| Priority | Label | Shortcut | Condition | Handler |
|----------|-------|----------|-----------|----------|
| 600 | Navigate | `↑` | Always | `cursor--` |
| 601 | Navigate | `↓` | Always | `cursor++` |
| 610 | Expand | `→` | Always | set `expanded[key] = true` |
| 615 | Toggle expansion | `Space` | Always | toggle `expanded[key]` |
| 620 | Open artifact | `o` | URL exists | `OpenExternalURLMsg` |
| 630 | Open all artifacts | `O` | Multiple items | `OpenArtifactLinksMsg` |
| 640 | Start review followup | `f` | review available | `FetchReviewCommentsMsg` |

#### Repo Manager (Priority 650-679)

| Priority | Label | Shortcut | Condition | Handler |
|----------|-------|----------|-----------|----------|
| 650 | Add repo | `a` | Always | set `overlayAddRepo` |
| 660 | Delete repo | `d` | Repo selected | show delete confirm |
| 665 | Init git-work | `i` | Plain git repo | init command |
| 670 | Switch focus | `Tab` | Always | toggle `focus` |

#### New Session (Priority 680-749)

| Priority | Label | Shortcut | Condition | Handler |
|----------|-------|----------|-----------|----------|
| 680 | Cycle provider | `Tab` | Always | `cycleProvider(1)` |
| 685 | Previous provider | `Shift+Tab` | Always | `cycleProvider(-1)` |
| 690 | Cycle scope | `Ctrl+S` | Always | `cycleScope(1)` |
| 695 | Cycle view | `Ctrl+V` | Always | `cycleView(1)` |
| 700 | Cycle state | `Ctrl+T` | Always | `cycleState(1)` |
| 705 | Reset | `Ctrl+R` | Always | `resetBrowseState()` |
| 710 | Save filter | `Ctrl+F` | Always | open save prompt |
| 715 | Load filter | `Ctrl+L` | Always | open load picker |
| 720 | Manual entry | `Ctrl+N` | Always | set `showManual = true` |
| 725 | Toggle selection | `Space` | Always | `toggleSelection()` |
| 730 | Open in browser | `Ctrl+O` | Item selected | open browser |

#### Add Repo (Priority 750-779)

| Priority | Label | Shortcut | Condition | Handler |
|----------|-------|----------|-----------|----------|
| 750 | Cycle source | `Tab` | Always | `sourceIndex++` |
| 755 | Manual URL | `Ctrl+N` | Always | set `showManual = true` |
| 760 | Clear search | `Ctrl+R` | Always | `searchInput.SetValue("")` |
| 765 | Toggle owned filter | `Ctrl+G` | Always | toggle `ownedOnly` |
| 770 | Clone | `Enter` | Repo selected | `AddRepoCloneMsg` |

#### Settings (Priority 780-839)

| Priority | Label | Shortcut | Condition | Handler |
|----------|-------|----------|-----------|----------|
| 780 | Move up | `↑` | Always | `moveSection(-1)` or `moveField(-1)` |
| 785 | Move down | `↓` | Always | `moveSection(1)` or `moveField(1)` |
| 790 | Focus sections | `←` | Always | `focusSections()` |
| 795 | Expand / focus fields | `→` | Always | expand or `focusFields()` |
| 800 | Edit field | `e` | Field focused | `openFieldEditor()` |
| 805 | Toggle boolean | `Space` | Bool field | toggle value |
| 810 | Reveal secrets | `r` | Always | toggle `revealSecrets` |
| 815 | Apply settings | `s` | Changes pending | `applyCmd()` |
| 820 | Test provider | `t` | Provider selected | `testProviderCmd()` |
| 825 | Login provider | `g` | Provider selected | `loginProviderCmd()` |

#### Logs (Priority 840-849)

| Priority | Label | Shortcut | Condition | Handler |
|----------|-------|----------|-----------|----------|
| 840 | Copy all | `y` | Always | clipboard.WriteAll |

#### Source Details (Priority 850-899)

| Priority | Label | Shortcut | Condition | Handler |
|----------|-------|----------|-----------|----------|
| 850 | Back to overview | `Enter` | Always | `jumpFromSourceDetailsToOverview()` |

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
        actionMenu: NewActionMenuModel(st),
    }
    // Set app pointer and recalc function after construction
    a.actionMenu.SetApp(a)
    return a
}
```

### 3.2 Delete `overlay_help.go`

Delete `internal/tui/views/overlay_help.go` entirely.

---

## Phase 4: Vim Bindings Removal

Replace all `j`/`k` vim-style navigation with `↓`/`↑` arrow keys in list navigation contexts. Do NOT change `j`/`k` in text input fields (e.g., GrowingTextArea).

### 4.1 `reviewing_view.go` (lines ~99-152)

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

### 4.2 `overlay_review_followup.go`

**Picker navigation (line ~992-999):**
```go
// BEFORE:
case "up", "k":
    if m.pickerCursor > 0 {
        m.pickerCursor--
    }
case "down", "j":
    if m.pickerCursor < len(m.pickerItems)-1 {
        m.pickerCursor++
    }

// AFTER:
case "up":
    if m.pickerCursor > 0 {
        m.pickerCursor--
    }
case "down":
    if m.pickerCursor < len(m.pickerItems)-1 {
        m.pickerCursor++
    }
```

**Selector navigation (line ~1039-1042):**
```go
// Note: This already uses "up"/"k" and "down"/"j" - standardize to just arrow keys
case "up":
    m.moveCursor(-1)
case "down":
    m.moveCursor(1)
```

**Selector focus switching (line ~1035-1038):**
```go
// Keep "left"/"h" and "right"/"l" as these are standard conventions
// NOT vim bindings - these are directional focus movements
```

### 4.3 `artifacts_view.go`

**Item navigation (line ~156-165):**
```go
// BEFORE:
case "up", "k":
    if m.cursor > 0 {
        m.cursor--
        changed = true
    }
case "down", "j":
    if m.cursor < len(m.items)-1 {
        m.cursor++
        changed = true
    }

// AFTER:
case "up":
    if m.cursor > 0 {
        m.cursor--
        changed = true
    }
case "down":
    if m.cursor < len(m.items)-1 {
        m.cursor++
        changed = true
    }
```

### 4.4 Search for remaining `case "j":` / `case "k":`

Run this to find any remaining vim bindings:
```bash
grep -rn 'case "j":\|case "k":' internal/tui/views/
```

Review each match:
- If it's list navigation → replace with `down`/`up`
- If it's text input in a GrowingTextArea → KEEP `j`/`k`
- If it's page up/down → KEEP as `pgup`/`pgdown`

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
5. Verify `Esc` closes the action menu without executing
6. Verify `x` reopens the action menu
7. Verify cursor position starts at highest priority action
8. Verify action menu reflects state changes (e.g., archive action appears/disappears based on session state)

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

## Open Questions (RESOLVED)

### 1. New session autonomous ✅
- `openNewSessionAutonomousOverlay()` returns `tea.Cmd` (nil)
- Handler pattern:
  ```go
  Handler: func(a *App) tea.Cmd {
      return a.openNewSessionAutonomousOverlay()
  }
  ```

### 2. Interrupt sessions ✅
- `I` key → `ConfirmInterruptSessionsMsg{SessionIDs: ids}` → shows confirm dialog
- Confirmed → `InterruptSessionsMsg` → actually interrupts
- Handler pattern:
  ```go
  Handler: func(a *App) tea.Cmd {
      ids := a.interruptibleFocusedSessionIDs()
      if len(ids) == 0 {
          return nil
      }
      return func() tea.Msg { return ConfirmInterruptSessionsMsg{SessionIDs: ids} }
  }
  ```
- Condition: `len(a.interruptibleFocusedSessionIDs()) > 0`

### 3. Open terminal ✅
- Uses `OpenTerminalCmd(dir string)` from `cmds.go`
- Extracts worktree path from current session via `workItemTaskSession()`
- Handler pattern:
  ```go
  Handler: func(a *App) tea.Cmd {
      if a.content.Mode() != ContentModeAgentSession &&
         a.content.Mode() != ContentModeSessionInteraction {
          return nil
      }
      sessionID := a.content.sessionLog.SessionID()
      if sessionID == "" {
          return nil
      }
      session := a.workItemTaskSession(a.currentWorkItemID, sessionID)
      if session == nil || session.WorktreePath == "" {
          return nil
      }
      return OpenTerminalCmd(session.WorktreePath)
  }
  ```
- Condition: `session.WorktreePath != ""` when in session view

### 4. Override accept ✅
- `o` in reviewing → `ConfirmOverrideAcceptMsg{WorkItemID: ...}` → shows confirm dialog
- Confirmed → `OverrideAcceptMsg` → actually overrides
- Handler pattern:
  ```go
  Handler: func(a *App) tea.Cmd {
      // Only available in reviewing context
      if a.content.Mode() != ContentModeOverview ||
         a.content.overview.overlay != overviewOverlayReviewing {
          return nil
      }
      card := a.content.overview.selectedActionCard()
      if card == nil || card.Kind != overviewActionReviewing {
          return nil
      }
      return func() tea.Msg {
          return ConfirmOverrideAcceptMsg{WorkItemID: a.currentWorkItemID}
      }
  }
  ```
- Condition: reviewing overlay with reviewing action card selected

### 5. Extra context modal ✅
- `showExtraContext` is a **state within** `ContextNewSession`, not a separate context
- This modal appears after selecting items and before final submission
- No changes needed to context determination; actions for this modal are:
  - `Enter`: Submit with extra context
  - `Esc`: Close modal (returns to selection state)
- Both actions already covered under `ContextNewSession`

### Additional Resolved Details

#### Archive/Unarchive (both use `a` key)
Both `Archive session` (priority 70) and `Unarchive session` (priority 71) use shortcut `a`:
- They are **mutually exclusive** based on session state
- Conditions ensure only one appears:
  ```go
  Archive:   func(a *App) bool { return a.archivablSessionID() != "" && a.unarchivablSessionID() == "" }
  Unarchive: func(a *App) bool { return a.unarchivablSessionID() != "" }
  ```

#### Confirmation Actions Pattern
Several actions show confirm dialogs before executing:
- Interrupt sessions → `ConfirmInterruptSessionsMsg`
- Abandon session → `ConfirmAbandonMsg`
- Override accept → `ConfirmOverrideAcceptMsg`
- Archive/Unarchive → via `showArchiveConfirm()`/`showUnarchiveConfirm()`
- Delete session → via `showDeleteSessionConfirm()`

All return the appropriate confirm message which triggers the confirm dialog handler.

#### Open external links
`o` in overview (when sources or reviews exist) → `OpenOverviewLinksMsg`:
- Mutually exclusive with override accept (different conditions)
- Handler pattern:
  ```go
  Handler: func(a *App) tea.Cmd {
      if a.content.Mode() != ContentModeOverview ||
         a.content.overview.overlay != overviewOverlayNone {
          return nil
      }
      data := a.content.overview.data
      if len(data.Sources) == 0 && len(data.External.Reviews) == 0 {
          return nil
      }
      return func() tea.Msg {
          return OpenOverviewLinksMsg{
              Sources: data.Sources,
              Reviews: data.External.Reviews,
          }
      }
  }
  ```
- Priority: 275 (between Overview actions and Plan Review actions)

---

## Implementation Notes

### Action Card Delegation Pattern

Many actions in the overview and its sub-overlays delegate to the existing `Update()` method rather than being handled directly in the action menu. This ensures consistency with existing behavior.

**Delegation approach:**
1. When an action is selected in the action menu and Enter is pressed
2. The action handler sends the appropriate message (e.g., `tea.KeyMsg`)
3. The message is routed to the appropriate handler (overview, plan review, etc.)
4. The overlay closes, and the action executes

**Example: Execute action in overview**
```go
{
    ID: "execute_action", Label: "Execute action", Shortcut: "Enter", Priority: 115,
    Condition: func(a *App) bool {
        return a.content.Mode() == ContentModeOverview &&
               a.content.overview.selectedActionCard() != nil
    },
    Handler: func(a *App) tea.Cmd {
        // Close the action menu
        a.activeOverlay = overlayNone
        // Send Enter key to content to execute the action
        return a.content.Update(tea.KeyMsg{Type: tea.KeyEnter})
    },
}
```

### Action Menu Overlay Closing

When the action menu is open:
1. `Esc` → closes the action menu (returns to previous context)
2. `Enter` on an action → executes the action, which may:
   - Close the action menu AND open a new overlay (e.g., settings)
   - Close the action menu AND perform an action (e.g., delete session)
   - Show a confirm dialog (action menu closes, confirm shows)

### Priority Ranges by Category

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
| 680-749 | New Session |
| 750-779 | Add Repo |
| 780-839 | Settings |
| 840-849 | Logs |
| 850-899 | Source Details |

### Review Followup Stages

The review followup overlay has 4 stages, each with its own set of actions:
1. **Loading** (no actions): Spinner only
2. **Picker** (priority 500-540): Select which PRs/MRs to address (>1 PR available)
3. **Selector** (priority 550-570): Select which comments to address (1 PR, or after picker)
4. **Confirm** (priority 580): Confirm replan

**Stage detection in `currentActionContext()`:**
```go
case overlayReviewFollowup:
    switch a.reviewFollowupOverlay.Stage() {
    case reviewFollowupStageLoading:
        return ContextReviewFollowupLoading // no actions
    case reviewFollowupStagePicker:
        return ContextReviewFollowupPicker
    case reviewFollowupStageSelector:
        return ContextReviewFollowupSelector
    case reviewFollowupStageConfirm:
        return ContextReviewFollowupConfirm
    }
```

**Note:** The picker stage currently uses `k`/`j` for navigation (vim bindings). These will be replaced with `up`/`down` in Phase 4.
