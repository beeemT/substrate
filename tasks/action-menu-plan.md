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

Keep this file in package `views`; action handlers need access to existing unexported view/model state. Do not introduce a parallel public API just for the registry.

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

    context ActionContext // source context captured before overlayActionMenu is activated
    actions []Action      // all available actions for context
    query   string        // search query
    matches []int         // indices into actions that match query
    cursor  int           // position within matches
}
```

Key methods:
- `NewActionMenuModel(styles.Styles) ActionMenuModel`
- `Open(*App, ActionContext)` — stores the app pointer, stores the source context, resets query/cursor, and refreshes actions
- `Refresh()` — rebuilds the current source-context action list without losing the current query
- `SetSize(width, height int)` — keeps rendering bounded to the current terminal size
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
- Handle Backspace/Delete by removing the last rune from the query

Ignore: arrow keys, Enter, Escape, Tab, Ctrl+chords.

`x` opens the action menu only from non-text-capturing contexts. Text inputs and `GrowingTextArea` fields must keep receiving literal `x` characters when focused.

---

## Phase 2: Action Registry

### 2.1 Registry Structure

```go
func (a *App) BuildActionRegistry(ctx ActionContext) []Action {
    var actions []Action

    // Global actions are included for every non-modal action-menu context.
    actions = append(actions, globalActions(a)...)

    // Context-specific actions.
    switch ctx {
    case ContextOverview:
        actions = append(actions, overviewActions(a)...)
        if card := a.content.overview.selectedActionCard(); card != nil {
            actions = append(actions, actionCardActions(a, card)...)
        }
    case ContextAgentSessionLog, ContextSessionInteractionLog:
        actions = append(actions, sessionLogActions(a, ctx)...)
    // ... every context listed below must have an explicit case, even if it returns no local actions.
    }

    actions = filterAvailableActions(a, actions)
    sort.Slice(actions, func(i, j int) bool {
        if actions[i].Priority == actions[j].Priority {
            return actions[i].Label < actions[j].Label
        }
        return actions[i].Priority < actions[j].Priority
    })
    return actions
}
```

### 2.2 Context Determination

```go
func (a *App) currentActionContext() ActionContext {
    // The action menu replaces activeOverlay while open, so use the captured
    // return overlay when recomputing actions from inside the menu.
    activeOverlay := a.activeOverlay
    if activeOverlay == overlayActionMenu {
        activeOverlay = a.actionMenuReturnOverlay
    }

    // Modal confirmations and duplicate-session dialogs keep exclusive input.
    // Do not open the action menu over them.
    if a.confirmActive || a.duplicateSessionActive {
        return ContextModalExclusive
    }

    // App-level overlays first.
    switch activeOverlay {
    case overlayWorkspaceInit:
        return ContextWorkspaceInit
    case overlayNewSession:
        return ContextNewSession
    case overlayNewSessionAutonomous:
        return ContextNewSessionAutonomous
    case overlaySessionSearch:
        return ContextSessionSearch
    case overlaySettings:
        return ContextSettings
    case overlaySourceItems:
        return ContextSourceItems
    case overlayLogs:
        return ContextLogs
    case overlayAddRepo:
        return ContextAddRepo
    case overlayRepoManager:
        return ContextRepoManager
    case overlayOverviewLinks:
        return ContextOverviewLinks
    case overlayReviewFollowup:
        switch a.reviewFollowupOverlay.Stage() {
        case reviewFollowupStageLoading:
            return ContextReviewFollowupLoading
        case reviewFollowupStagePicker:
            return ContextReviewFollowupPicker
        case reviewFollowupStageSelector:
            return ContextReviewFollowupSelector
        case reviewFollowupStageConfirm:
            return ContextReviewFollowupConfirm
        }
    }

    // Overview sub-overlays.
    if a.content.Mode() == ContentModeOverview {
        switch a.content.overview.overlay {
        case overviewOverlayPlan:        return ContextPlanReview
        case overviewOverlayQuestion:    return ContextQuestion
        case overviewOverlayInterrupted: return ContextInterrupted
        case overviewOverlayReviewing:   return ContextReviewing
        case overviewOverlayCompleted:   return ContextCompleted
        }
        return ContextOverview
    }

    switch a.content.Mode() {
    case ContentModeEmpty:              return ContextEmpty
    case ContentModeAgentSession:       return ContextAgentSessionLog
    case ContentModeSessionInteraction: return ContextSessionInteractionLog
    case ContentModeArtifacts:          return ContextArtifacts
    case ContentModeSourceDetails:      return ContextSourceDetails
    }
    return ContextGlobal
}
```

`ActionContext` values to define:
- `ContextGlobal`, `ContextEmpty`, `ContextModalExclusive`
- `ContextWorkspaceInit`, `ContextSessionSearch`, `ContextSettings`, `ContextLogs`
- `ContextNewSession`, `ContextNewSessionAutonomous`, `ContextAddRepo`, `ContextRepoManager`
- `ContextOverview`, `ContextPlanReview`, `ContextQuestion`, `ContextInterrupted`, `ContextReviewing`, `ContextCompleted`
- `ContextAgentSessionLog`, `ContextSessionInteractionLog`
- `ContextArtifacts`, `ContextSourceDetails`, `ContextSourceItems`, `ContextOverviewLinks`
- `ContextReviewFollowupLoading`, `ContextReviewFollowupPicker`, `ContextReviewFollowupSelector`, `ContextReviewFollowupConfirm`

`ContextModalExclusive` is not openable through `x`; it exists to make accidental calls explicit in tests.

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
| 91 | Move sidebar selection up | `↑` | Sidebar focused | `sidebar.MoveUp()` + `onSidebarMove()` |
| 92 | Move sidebar selection down | `↓` | Sidebar focused | `sidebar.MoveDown()` + `onSidebarMove()` |
| 93 | Enter tasks/content | `→` | Sidebar focused | `enterTaskSidebar()` or focus content |
| 94 | Exit tasks/content | `←`/`Esc` | Content or task sidebar focused | focus sidebar or `exitTaskSidebar()` |
| 95 | Cycle sidebar filter | `f` | Sessions sidebar focused | `sidebar.CycleFilter()` |
| 96 | Cycle sidebar grouping | `g` | Sessions sidebar focused | `sidebar.CycleDimension()` |
| 97 | Toggle sidebar sort direction | `o` | Sessions sidebar focused | `sidebar.ToggleDirection()` |
| 98 | Open overview from task notice | `Enter` | Task sidebar session selected with notice | `jumpFromSourceDetailsToOverview()` |

#### Overview (Priority 100-199)

| Priority | Label | Shortcut | Condition | Handler |
|----------|-------|----------|-----------|----------|
| 100 | Cycle action cards | `Tab` | `len(actions) > 1` | delegate to overview |
| 110 | Inspect | `i` | Action card selected | delegate to overview |
| 115 | Execute action | `Enter` | Action card selected | delegate to overview |
| 120 | View full plan | `i` | No action card selected, plan exists | set `overviewOverlayPlan` |
| 130 | Retry failed session | `r` | Failed action card selected | `RetryFailedMsg` |
| 140 | Finalize work item | `f` | Finalize action card selected | `FinalizeWorkItemMsg` |
| 190 | Open external links | `o` | Sources or reviews exist, no reviewing action | `OpenOverviewLinksMsg` |

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
| 300 | Steer / prompt | `p` | live/failed/completed session | set `steerActive` |
| 305 | Open overview | `Enter` | session log notice exists | `jumpFromSourceDetailsToOverview()` |
| 310 | Follow tail / go to bottom | `f` | Always | `viewport.GotoBottom()` |
| 320 | Go to top | `g` | Always | `viewport.GotoTop()` |
| 330 | Toggle verbose | `v` | Always | toggle `m.verbose` |
| 340 | Toggle thinking | `t` | Thinking blocks exist | toggle `m.collapseThinking` |
| 345 | Open plan | `i` | Plan exists and plan overlay closed | set `planOverlay` / `InspectPlanMsg` |
| 350 | Copy plan | `c` | Plan overlay open | clipboard.WriteAll |
| 355 | Open terminal | `o` | `ContentModeAgentSession`, focused content, worktree exists | `OpenTerminalCmd(path)` |
| 360 | Scroll session log | `PgUp/PgDn` | Always | delegate to viewport |

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

#### New Session (Priority 680-739)

| Priority | Label | Shortcut | Condition | Handler |
|----------|-------|----------|-----------|----------|
| 680 | Cycle provider | `Tab` | Always | `cycleProvider(1)` |
| 685 | Previous provider | `Shift+Tab` | Always | `cycleProvider(-1)` |
| 690 | Cycle scope | `Ctrl+S` | Always | `cycleScope(1)` |
| 695 | Cycle view | `Ctrl+V` | Always | `cycleView(1)` |
| 700 | Cycle state | `Ctrl+T` | Always | `cycleState(1)` |
| 705 | Reset | `Ctrl+R` | Always | `resetBrowseState()` |
| 710 | Save filter | `Ctrl+F` | Browse view | open save prompt |
| 715 | Load filter | `Ctrl+L` | Browse view | open load picker |
| 720 | Manual entry | `Ctrl+N` | Browse view | set `showManual = true` |
| 725 | Toggle selection | `Space` | Browse item selected | `toggleSelection()` |
| 730 | Open in browser | `Ctrl+O` | Item selected | open browser |
| 735 | Continue selected items | `Enter` | Browse selections exist | open extra-context modal |
| 736 | Start manual session | `Enter` | Manual entry title/description ready | `NewSessionManualMsg` |
| 737 | Start session with extra context | `Enter` | Extra-context modal open | `NewSessionBrowseMsg` |
| 738 | Close extra-context modal | `Esc` | Extra-context modal open | return to browse selection |

#### New Session Autonomous (Priority 740-749)

| Priority | Label | Shortcut | Condition | Handler |
|----------|-------|----------|-----------|----------|
| 740 | Start autonomous mode | `Enter` | Filters selected | `StartNewSessionAutonomousModeMsg` |
| 742 | Stop autonomous mode | `S` | Autonomous mode running | `StopNewSessionAutonomousModeMsg` |
| 744 | Toggle filter selection | `Space` | Filter focused | toggle current selection |
| 746 | Focus list/details | `←`/`→` | Always | change focus |
| 748 | Navigate filters | `↑`/`↓` | List/details focused | delegate to focused pane |

`x` and `X` MUST be removed from `NewSessionAutonomousOverlay.Update`; `x` is reserved for opening the action menu. Update `hintText()` and tests to advertise/use `S` for stop.

#### Add Repo (Priority 750-779)

| Priority | Label | Shortcut | Condition | Handler |
|----------|-------|----------|-----------|----------|
| 750 | Cycle source | `Tab` | Always | `sourceIndex++` |
| 755 | Manual URL | `Ctrl+N` | Always | set `showManual = true` |
| 760 | Clear search | `Ctrl+R` | Always | `searchInput.SetValue("")` |
| 765 | Toggle owned filter | `Ctrl+G`/`Space` | Filter control focused | toggle `ownedOnly` |
| 768 | Move focus/list selection up | `↑` | Always | `moveAddRepoFocus(-1)` or list/viewport update |
| 769 | Move focus/list selection down | `↓` | Always | `moveAddRepoFocus(1)` or list/viewport update |
| 770 | Clone | `Enter` | Repo selected and list focused | `AddRepoCloneMsg` |
| 772 | Focus list/details or adjust control | `←`/`→` | Always | delegate to add-repo overlay |
| 775 | Confirm manual URL | `Enter` | Manual URL modal open | clone/add manual URL |
| 778 | Close add repo | `Esc` | Always | `CloseOverlayMsg` |

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
| 845 | Scroll logs | `↑`/`↓`/`PgUp`/`PgDn` | Always | delegate to logs viewport |

#### Session Search (Priority 850-869)

| Priority | Label | Shortcut | Condition | Handler |
|----------|-------|----------|-----------|----------|
| 850 | Open selected session | `Enter` | Search result selected | `OpenSessionHistoryMsg` |
| 855 | Navigate results | `↑`/`↓` | Search result list focused | delegate to session search overlay |
| 860 | Edit search query | printable keys | Search input focused | delegate to session search overlay |
| 865 | Close search | `Esc` | Always | `CloseOverlayMsg` |

#### Source Items Overlay (Priority 870-889)

| Priority | Label | Shortcut | Condition | Handler |
|----------|-------|----------|-----------|----------|
| 870 | Open selected source URLs | `Enter`/`o` | URLs selected | `openSourceItemURLsMsg` |
| 875 | Navigate source URLs | `↑`/`↓` | Multiple URLs | delegate to source-items overlay |
| 880 | Toggle source URL selection | `Space` | URL focused | delegate to source-items overlay |
| 885 | Close source URLs | `Esc` | Always | `CloseOverlayMsg` |

#### Overview Links Overlay (Priority 890-909)

| Priority | Label | Shortcut | Condition | Handler |
|----------|-------|----------|-----------|----------|
| 890 | Open focused link | `Enter`/`o` | Link focused | `OpenExternalURLMsg` |
| 895 | Navigate links | `↑`/`↓` | Multiple links | delegate to overview-links overlay |
| 900 | Close links | `Esc` | Always | return to `overviewLinksReturnOverlay` or close |

#### Workspace Init (Priority 910-929)

| Priority | Label | Shortcut | Condition | Handler |
|----------|-------|----------|-----------|----------|
| 910 | Initialize workspace/repositories | `Enter` | Primary action available | delegate to workspace modal |
| 915 | Navigate workspace options | `↑`/`↓` | Multiple options | delegate to workspace modal |
| 920 | Toggle selection | `Space` | Selectable repo focused | delegate to workspace modal |
| 925 | Cancel workspace init | `Esc` | Cancel allowed | delegate to workspace modal |

#### Source Details (Priority 930-979)

| Priority | Label | Shortcut | Condition | Handler |
|----------|-------|----------|-----------|----------|
| 930 | Back to overview | `Enter` | `sourceDetails.notice != nil` | `jumpFromSourceDetailsToOverview()` |
| 935 | Navigate source | `↑` | Multiple source items | `cursor--` |
| 936 | Navigate source | `↓` | Multiple source items | `cursor++` |
| 940 | Expand source | `→` | Multiple source items, focused item collapsed | expand focused item |
| 945 | Toggle source expansion | `Space` | Multiple source items | toggle focused item |
| 950 | Open source in browser | `o` | Focused source has URL | open browser |
| 955 | Scroll source details | `PgUp/PgDn` | Always | delegate to viewport |

---

## Phase 3: App Integration

### 3.1 App.go Changes

**Add field to App struct:**
```go
type App struct {
    // ... existing fields ...
    actionMenu              ActionMenuModel
    actionMenuReturnOverlay overlayKind // overlay that was active before overlayActionMenu
}
```

**Add overlay constant:**
```go
const (
    overlayNone overlayKind = iota
    overlayNewSession
    overlayNewSessionAutonomous
    overlaySessionSearch
    overlaySettings
    overlayWorkspaceInit
    overlayHelp       // removed in the same cutover
    overlayActionMenu // NEW, inserted where overlayHelp used to route
    overlaySourceItems
    overlayLogs
    overlayAddRepo
    overlayRepoManager
    overlayOverviewLinks
    overlayReviewFollowup
)
```

**Add `x` keybinding in `handleKeyMsg`:**

Route `x` before overlay-specific key handling, but after confirm/duplicate dialogs and after any active text input has claimed the key. This makes `x` work from normal overlays while preserving literal `x` typing in inputs.

```go
if msg.String() == "x" && !a.anyInputCaptured() {
    return a, a.openActionMenu()
}
```

Add helpers:
```go
func (a *App) openActionMenu() tea.Cmd {
    ctx := a.currentActionContext()
    if ctx == ContextModalExclusive {
        return nil
    }
    a.actionMenuReturnOverlay = a.activeOverlay
    a.actionMenu.Open(a, ctx)
    a.actionMenu.SetSize(a.windowWidth, a.windowHeight)
    a.activeOverlay = overlayActionMenu
    return nil
}

func (a *App) closeActionMenu() {
    a.activeOverlay = a.actionMenuReturnOverlay
    a.actionMenuReturnOverlay = overlayNone
}
```

`Esc` in `ActionMenuModel.Update` calls `a.closeActionMenu()` and returns to the prior overlay/context. Executing an action also closes via `closeActionMenu()` before delegating to the target model unless the action intentionally opens a different overlay.

`anyInputCaptured()` must include `a.content.InputCaptured()` plus overlay-local editing states (session search input, new-session manual/extra-context/filter-name inputs, add-repo search/manual inputs, plan/completed/question feedback inputs, settings field editor, etc.). Do not add defensive nil guards; these are required model dependencies.

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

Add this branch in both `Update` message routing and `handleKeyMsg` key routing. Add a `View()` switch case:
```go
case overlayActionMenu:
    result = renderOverlay(a.actionMenu.View(), a.windowWidth, a.windowHeight)
```

On `tea.WindowSizeMsg`, call `a.actionMenu.SetSize(msg.Width, msg.Height)` along with the other overlays.

**Initialize in app construction:**
```go
func NewApp(...) *App {
    a := &App{
        // ... existing initialization ...
        actionMenu: NewActionMenuModel(st),
    }
    return a
}
```

### 3.2 Delete `overlay_help.go`

Delete `internal/tui/views/overlay_help.go` entirely and remove the `helpOverlay HelpOverlay` field and `NewHelpOverlay(st)` initialization from `app.go`.

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
// AFTER:
case "up":
    m.moveCursor(-1)
case "down":
    m.moveCursor(1)
```

**Selector focus switching (line ~1035-1038):**
```go
// BEFORE:
case "left", "h":
    m.focus = reviewSelectorFocusList
case "right", "l":
    m.focus = reviewSelectorFocusPreview

// AFTER:
case "left":
    m.focus = reviewSelectorFocusList
case "right":
    m.focus = reviewSelectorFocusPreview
```

`h`/`l` are vim-style directional bindings and must be removed under the arrow-keys-only decision.

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

### 4.4 Complete vim-binding removal list

Use the harness `search` tool, not shell grep, to verify no list/navigation `j`/`k`/`h`/`l` bindings remain:
- Pattern: `case "up", "k"|case "down", "j"|case "left", "h"|case "right", "l"|case "j"|case "k"`
- Path: `internal/tui/views`

Known current navigation callsites to update:
- `internal/tui/views/app.go`: remove `k`/`j` from global sidebar up/down routing.
- `internal/tui/views/artifacts_view.go`: remove `k`/`j` item navigation.
- `internal/tui/views/completed_view.go`: remove `k`/`j` viewport navigation; keep `pgup`/`pgdown`.
- `internal/tui/views/duplicate_session_dialog.go`: remove `k`/`j`; keep arrow keys, `Tab`, and `Shift+Tab`.
- `internal/tui/views/overlay_overview_links.go`: remove `k`/`j` link navigation.
- `internal/tui/views/overlay_repo_manager.go`: remove `k`/`j` repo navigation.
- `internal/tui/views/overlay_review_followup.go`: remove `k`/`j` and `h`/`l` directional focus bindings.
- `internal/tui/views/overview.go`: remove `k`/`j` viewport navigation; keep `pgup`/`pgdown`, `home`, `end`.
- `internal/tui/views/plan_review.go`: remove `k`/`j` viewport navigation; keep `pgup`/`pgdown`.
- `internal/tui/views/question_view.go`: remove `k`/`j` scroll handoff; do not alter `GrowingTextArea` text input behavior.
- `internal/tui/views/reviewing_view.go`: remove `k`/`j` critique navigation.
- `internal/tui/views/settings_page.go`: remove `k`/`j` and `h`/`l` navigation/edit-option bindings; keep `Tab`/`Shift+Tab` where present.
- `internal/tui/views/source_details_view.go`: remove `k`/`j` item navigation and `l` expansion; keep arrow keys.

After edits, rerun the same `search` pattern and manually justify any remaining match as text input, not navigation.

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
9. **Layout bounds**: `ActionMenuModel.View()` line widths stay `<= width` and line count stays `<= height`, including narrow widths/heights
10. **Context snapshot**: opening from another overlay preserves that overlay as the source context and `Esc` returns to it
11. **Text input preservation**: focused text inputs receive literal `x` instead of opening the menu

### 5.2 Integration Tests

1. Open action menu via `x` in various contexts
2. Verify correct actions appear per context
3. Verify search filters correctly
4. Verify action execution closes menu
5. Verify `Esc` closes the action menu without executing
6. Verify `x` reopens the action menu
7. Verify cursor position starts at highest priority action
8. Verify action menu reflects state changes (e.g., archive action appears/disappears based on session state)
9. Verify `x` opens the action menu from normal overlays such as settings/repo manager, and `Esc` returns to that overlay
10. Verify `x` does not open the action menu while a text input or `GrowingTextArea` is focused
11. Verify autonomous-mode stop moved from `x`/`X` to `S` in behavior, hints, and tests
12. Verify full `App.View()` still fits the requested terminal width/height when the action menu is open at normal and narrow sizes

---

## Phase 6: Cleanup

1. Remove any references to `overlayHelp` or `HelpOverlay` in production code and tests
2. Update `DefaultHints()` in `internal/tui/views/statusbar.go`: replace `[?] Help` with `[x] Actions`
3. Update documentation mentioning `?` for help or `overlay_help.go`, including:
   - `docs/06-tui-design.md`
   - `docs/08-tui-design-system.md`
   - `docs/13-electron-app-plan.md`
4. Verify all keyboard shortcuts in status bar are correct
5. Update autonomous overlay hints/tests from `X stop` to `S stop`

---

## File Changes Summary

| File | Change |
|------|--------|
| `internal/tui/views/action_menu.go` | **NEW** — ActionMenuModel, context snapshotting, and action registry |
| `internal/tui/views/action_menu_test.go` | **NEW** — Unit and layout tests |
| `internal/tui/views/app.go` | Add action menu field/return overlay, `x` binding, action-menu routing/view/resize, remove `?` binding and help field |
| `internal/tui/views/statusbar.go` | Replace `? Help` default hint with `x Actions` |
| `internal/tui/views/overlay_help.go` | **DELETE** |
| `internal/tui/views/overlay_new_session_autonomous.go` | Remap stop from `x`/`X` to `S`, update hints |
| `internal/tui/views/overlay_new_session_autonomous_test.go` | Update stop-key expectations |
| `internal/tui/views/reviewing_view.go` | Replace `j`/`k` with `↓`/`↑` |
| `internal/tui/views/app.go`, `artifacts_view.go`, `completed_view.go`, `duplicate_session_dialog.go`, `overlay_overview_links.go`, `overlay_repo_manager.go`, `overlay_review_followup.go`, `overview.go`, `plan_review.go`, `question_view.go`, `settings_page.go`, `source_details_view.go` | Remove vim-style navigation bindings listed in Phase 4 |
| `docs/06-tui-design.md`, `docs/08-tui-design-system.md`, `docs/13-electron-app-plan.md` | Replace help-overlay / `?` references with action-menu / `x` references |

---

## Open Questions (RESOLVED)

### 1. New session autonomous ✅
- `openNewSessionAutonomousOverlay()` returns `tea.Cmd` (nil)
- Stop autonomous mode is remapped from `x`/`X` to `S`; `x` is reserved for the action menu.
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
      if a.content.Mode() != ContentModeAgentSession {
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
- Condition: focused `ContentModeAgentSession` has `session.WorktreePath != ""`

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
- Priority: 190 (last Overview action, before Plan Review actions)

---

## Implementation Notes

### Action Card Delegation Pattern

Many actions in the overview and its sub-overlays delegate to the existing `Update()` method rather than being handled directly in the action menu. This ensures consistency with existing behavior.

**Delegation approach:**
1. When an action is selected in the action menu and Enter is pressed
2. The action handler sends the appropriate message (e.g., `tea.KeyMsg`)
3. The message is routed to the appropriate handler (overview, plan review, etc.)
4. The action menu restores the source overlay/context, then the action executes

**Example: Execute action in overview**
```go
{
    ID: "execute_action", Label: "Execute action", Shortcut: "Enter", Priority: 115,
    Condition: func(a *App) bool {
        return a.content.Mode() == ContentModeOverview &&
               a.content.overview.selectedActionCard() != nil
    },
    Handler: func(a *App) tea.Cmd {
        // Restore the source context before delegating.
        a.closeActionMenu()
        var cmd tea.Cmd
        a.content, cmd = a.content.Update(tea.KeyMsg{Type: tea.KeyEnter})
        return cmd
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

### Review Followup Stages

The review followup overlay has 4 stages, each with its own set of actions:
1. **Loading** (no local actions): Spinner only; global actions remain available if action menu is opened
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
