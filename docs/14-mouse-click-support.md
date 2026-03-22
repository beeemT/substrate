# 14 - Mouse Click Support

Full left-click hit-testing for the Bubble Tea TUI. `tea.WithMouseCellMotion()` is already active — mouse events arrive but all non-wheel clicks are silently dropped. This plan adds hit-testing so left-clicks on interactive elements trigger the same `tea.Cmd`s as existing keyboard shortcuts. No new domain logic, no new messages, no keyboard behavior changes.

---

## Product-Level Acceptance Criteria

Every criterion below must be independently demonstrable. "Works" means the same observable effect as the equivalent keyboard action.

### AC-1: Sidebar session selection

| # | Criterion |
|---|-----------|
| 1.1 | Left-clicking a session row in the sidebar selects it and loads its content in the content pane, identical to pressing `↑`/`↓` to that row. |
| 1.2 | Clicking the already-selected sidebar row is a no-op (no content reload, no flicker). |
| 1.3 | When the sidebar is scrolled, clicking a visible row selects the correct absolute entry — not the visual slot. |
| 1.4 | Clicking the sidebar header (title/divider) or below the last entry is a no-op. |

### AC-2: Content pane interactions

| # | Criterion |
|---|-----------|
| 2.1 | **Implementing view tabs**: Clicking a repo tab switches to that repo, identical to pressing `Tab`/`Shift+Tab`. |
| 2.2 | **Completed view MR links**: Clicking an MR row opens the URL in the default browser, identical to pressing `Enter` on it. Clicking a row without a URL selects it without opening anything. |
| 2.3 | **Overview action cards**: Clicking an unselected action card selects it. Clicking the already-selected card activates it (same as `Enter`). |
| 2.4 | Clicking content pane chrome (border, padding, header, divider) is a no-op. |

### AC-3: Confirm dialog

| # | Criterion |
|---|-----------|
| 3.1 | When the confirm dialog is visible, clicking the confirm button zone executes the confirm action, identical to pressing `y`. |
| 3.2 | Clicking the cancel button zone dismisses the dialog, identical to pressing `n`. |
| 3.3 | Clicking outside the dialog bounds is a no-op (dialog stays open). |
| 3.4 | Clicking inside the dialog but outside the button row is a no-op. |

### AC-4: Overlay interactions

| # | Criterion |
|---|-----------|
| 4.1 | **New session overlay**: Clicking a toggle row (Source, Scope, View, State) cycles that control. Clicking a work-item in the list selects it. Clicking the detail pane sets focus to it. |
| 4.2 | **Session search overlay**: Clicking the scope toggle cycles scope. Clicking a result in the list selects it. Clicking the preview pane focuses it. |
| 4.3 | **Workspace init overlay**: Clicking Initialize confirms. Clicking Cancel dismisses. Clicks outside the dialog are a no-op. |
| 4.4 | **Help overlay**: Clicking anywhere dismisses it. |
| 4.5 | **Duplicate session dialog**: Clicking an option selects it. Clicking the already-selected option confirms (same as `Enter`). Clicks outside are a no-op. |

### AC-5: Settings page

| # | Criterion |
|---|-----------|
| 5.1 | Clicking a navigation node in the settings sidebar selects that section and scrolls the main viewport to it. |
| 5.2 | Clicking a field in the main settings viewport selects it. |
| 5.3 | When the edit modal is open, clicking a select option selects it. Clicks outside the edit modal are a no-op. |
| 5.4 | Settings coordinates are absolute terminal coordinates (settings is full-page, not a centered overlay). |

### AC-6: Overview internal sub-overlays

| # | Criterion |
|---|-----------|
| 6.1 | When an overview sub-overlay is visible (plan review, question, interrupted, completed, reviewing), clicks inside it dispatch to the sub-overlay. |
| 6.2 | Clicks outside the sub-overlay bounds are a no-op (do not fall through to sidebar/content). |

### AC-7: Non-regression guarantees

| # | Criterion |
|---|-----------|
| 7.1 | All existing keyboard shortcuts produce identical behavior before and after. |
| 7.2 | Scroll wheel (`WheelUp`/`WheelDown`) behavior is identical in all views. |
| 7.3 | Right-click, middle-click, mouse release, and mouse motion events are no-ops. |
| 7.4 | `Shift+drag` native text selection continues to work (terminal-side bypass, no code change). |
| 7.5 | All existing tests pass without modification. |

### AC-8: Routing correctness

| # | Criterion |
|---|-----------|
| 8.1 | The click routing order matches the visual z-order: topmost rendered element receives the click. |
| 8.2 | Confirm dialog intercepts clicks before all overlays. Duplicate session dialog intercepts before overlays. Overlays intercept before overview sub-overlays. Overview sub-overlays intercept before sidebar/content. |
| 8.3 | Click on the pane gap (1-column separator between sidebar and content) is a no-op. |
| 8.4 | Click on the status bar row is a no-op. |

---

## Autonomous Testing Strategy

### Unit tests (per-component, co-located)

Each component gets its own `_test.go` file with table-driven tests exercising the `HandleClick` / `ClickSelect` method in isolation. No TUI rendering required — tests construct minimal model state, call the click handler with synthetic coordinates, and assert state changes.

| Component | Test file | What to assert |
|-----------|-----------|----------------|
| `HitRegion` | `hitregion_test.go` | `Contains`: boundary edges, zero-size. `HitTest`: first match wins, miss returns false. `OverlayOrigin`: matches `renderCenteredOverlay` for several size combos. |
| Sidebar `ClickSelect` | `sidebar_test.go` | Header → false. First/last visible item → correct index. Scrolled window → correct absolute index. Already-selected → false. Empty entries → false. Tiny height → false. |
| Completed `HandleClick` | `completed_view_test.go` | MR with URL → `OpenExternalURLMsg`. No URL → select only. Above/below MR rows → no-op. Dynamic offset with/without timestamp. |
| Overview `HandleClick` | `overview_test.go` | Card selection, double-click activation, scrolled viewport, click outside cards. |
| Implementing `HandleClick` | `implementing_view_test.go` | Each tab → correct index. Separator → no-op. Below tabs → no-op. ANSI-aware width. |
| Confirm `Size` + click | `confirm_test.go` | Size matches rendered output. Button row Y = 6. Left half → confirm, right half → cancel. |
| Settings click zones | `settings_page_test.go` | Nav node click → section change. Field click → selection. Edit modal click → option selection. |

### Integration tests (routing, co-located with App)

**File: `internal/tui/views/app_mouse_test.go`**

Tests construct an `App` with minimal state, send a `tea.MouseMsg`, and assert which handler was invoked (or that no handler was invoked).

| Test case | Setup | MouseMsg | Expected |
|-----------|-------|----------|----------|
| Wheel passthrough | Default app | `WheelUp at (10, 5)` | Falls through to existing handler (not consumed by click routing) |
| Left click sidebar | Default app, layout computed | `Left press at (5, 10)` | `handleSidebarClick` called |
| Left click content | Default app, layout computed | `Left press at (50, 10)` | `handleContentClick` called |
| Left click pane gap | Default app, layout computed | `Left press at (36, 10)` | No-op |
| Left click status bar | Default app | `Left press at (10, windowHeight-1)` | No-op |
| Confirm intercepts | `confirmActive = true` | `Left press at (10, 10)` | `handleConfirmClick` called, NOT sidebar |
| Duplicate intercepts | `duplicateSessionActive = true` | `Left press at (10, 10)` | `handleDuplicateDialogClick` called |
| Overlay intercepts | `activeOverlay = overlayNewSession` | `Left press at (10, 10)` | `handleNewSessionClick` called |
| Overview sub-overlay intercepts | `overviewOverlayOpen() = true` | `Left press at (40, 20)` | `handleOverviewOverlayClick` called |
| Help dismisses | `activeOverlay = overlayHelp` | `Left press anywhere` | `activeOverlay` becomes `overlayNone` |
| Right-click no-op | Default app | `Right press at (10, 10)` | No state change |
| Mouse release no-op | Default app | `Left release at (10, 10)` | No state change |
| Mouse motion no-op | Default app | `Motion at (10, 10)` | No state change |

### Property: all tests compile and pass with `go test ./internal/tui/...`

---

## Coordinate Reference

Terminal origin `(0,0)` = top-left. Status bar = last row (`windowHeight - 1`).

```
┌──────────── Sidebar Pane ─────────────┐ ┌────────────── Content Pane ─────────────────┐
│ border=1                               │g│ border=1                                     │
│ ┌─ inner ──────────────────────────┐   │a│ ┌─ padding=1 ──────────────────────────────┐ │
│ │ header row 0 (title)             │   │p│ │ ┌─ inner content ────────────────────┐   │ │
│ │ header row 1 (divider)           │   │ │ │ │                                    │   │ │
│ │ item 0: 3 content + 1 blank = 4 │   │1│ │ │ (view-specific, see phases)        │   │ │
│ │ item 1: 4 rows                   │   │ │ │ │                                    │   │ │
│ │ ...                              │   │ │ │ └────────────────────────────────────┘   │ │
│ └──────────────────────────────────┘   │ │ └──────────────────────────────────────────┘ │
└────────────────────────────────────────┘ └──────────────────────────────────────────────┘
[status bar — row windowHeight-1]

Sidebar pane width = SidebarWidth(34) + 2(border) = 36
Pane gap width = 1
Content pane X starts at 37
Content inner X: +1(border) +1(padding) = +2 from content pane left
Content inner Y: +1(border) from content pane top
```

**Overlay centering** (used by confirm, duplicate, workspace init, new session, search, overview sub-overlays):
```
left = max(0, (windowW - overlayW) / 2)
top  = max(0, (windowH - overlayH) / 2)
```

**Settings page** is full-screen (NOT centered overlay) — coordinates are absolute terminal coordinates.

---

## Concrete Implementation Todos

### Batch A: Infrastructure + App Routing (prerequisite for all other batches)

- [ ] **A.1** Create `internal/tui/views/hitregion.go`
  - `HitRegion` struct with `Top`, `Left`, `Width`, `Height`, `Tag` fields
  - `Contains(x, y int) bool` method
  - `HitTest(regions []HitRegion, x, y int) (HitRegion, bool)` function
  - `OverlayOrigin(windowW, windowH, overlayW, overlayH int) (left, top int)` function
  - `measureRendered(s string) (w, h int)` helper (ANSI-aware width via `ansi.StringWidth`)

- [ ] **A.2** Create `internal/tui/views/hitregion_test.go`
  - Table-driven tests for `Contains` (boundary edges, zero-size region)
  - Table-driven tests for `HitTest` (hit, miss, first-match-wins)
  - Table-driven tests for `OverlayOrigin` (several size combos, matches `renderCenteredOverlay` math)
  - Tests for `measureRendered` (multi-line string, empty string, ANSI sequences)

- [ ] **A.3** Add `layout styles.MainPageLayout` field to `App` struct in `app.go`
  - Populate in `tea.WindowSizeMsg` handler from existing `ComputeMainPageLayout` call

- [ ] **A.4** Add `tea.MouseMsg` case in `App.Update` switch
  - Wheel events (`WheelUp`, `WheelDown`): `break` to fall through to existing routing
  - Non-left-button or non-press: `return a, nil`
  - Left press: `return a.handleMouseClick(msg)`

- [ ] **A.5** Implement `handleMouseClick` master routing on `App`
  - Check `confirmActive` first → `handleConfirmClick`
  - Check `duplicateSessionActive` → `handleDuplicateDialogClick`
  - Check each `activeOverlay` value → corresponding handler
  - Check `overviewOverlayOpen()` → `handleOverviewOverlayClick`
  - Check `msg.X < layout.SidebarPaneWidth` → `handleSidebarClick`
  - Check `msg.X >= layout.SidebarPaneWidth + layout.PaneGapWidth` → `handleContentClick`
  - Gap / status bar → no-op

- [ ] **A.6** Implement `handleSidebarClick` stub (calls `sidebar.ClickSelect`, then `onSidebarMove` if changed)

- [ ] **A.7** Implement `handleContentClick` stub (translates coordinates: subtract border+padding, calls `content.HandleClick`)

- [ ] **A.8** Add `HandleClick(relX, relY int)` stub methods to content model and each view model that return `(Model, nil)` — compilation scaffolding for parallel batches

### Batch B: Sidebar + Completed View (depends on A)

- [ ] **B.1** Implement `SidebarModel.ClickSelect(absX, absY int) bool` in `sidebar.go`
  - Subtract top border (1 row) to get inner Y
  - Reject clicks on header rows (inner Y < 2)
  - Compute `visibleCount = max(0, height - 2) / 4`
  - Guard: `visibleCount <= 0 || len(entries) == 0` → false
  - Compute item slot: `(innerY - headerRows) / 4`
  - Replicate scroll-window start logic from `View()` with bounds check: when `len(entries) <= visibleCount`, `start = 0`
  - Compute `absIdx = start + itemSlot`, bounds-check, set `cursor`, return true if changed

- [ ] **B.2** Write sidebar click tests in `sidebar_test.go`
  - Header click → false
  - First/last visible item → correct absolute index, true
  - Scrolled window → correct mapping
  - Already-selected → false
  - Empty entries → false
  - Height too small for items → false

- [ ] **B.3** Implement `CompletedModel.HandleClick(relX, relY int) (CompletedModel, tea.Cmd)` in `completed_view.go`
  - Dynamic `mrStartLine` computation: replicate line-counting from `View()` (header=1, divider=1, blank=1, optional timestamp+blank=2, "Repos:" label=1)
  - `clickedMR = relY - mrStartLine`; bounds-check against `len(mrLinks)`
  - Set `selectedLink`; if URL present, return `OpenExternalURLMsg`

- [ ] **B.4** Write completed view click tests in `completed_view_test.go`
  - MR with URL → correct msg
  - MR without URL → select only
  - Above/below MR rows → no-op
  - No MR links → no-op
  - With/without timestamp → correct offset

### Batch C: Overview + Implementing View (depends on A, parallel with B)

- [ ] **C.1** Add `lineRange` type and `cardLineRanges []lineRange` field to `SessionOverviewModel` in `overview.go`

- [ ] **C.2** Populate `cardLineRanges` during document rendering
  - Track cumulative line offsets when building the action section
  - Each card records `{start, end}` line numbers in the full document

- [ ] **C.3** Implement `SessionOverviewModel.HandleClick(relX, relY int) (SessionOverviewModel, tea.Cmd)`
  - `docLine = viewport.YOffset + relY`
  - Iterate `cardLineRanges` to find which card contains `docLine`
  - Unselected card → select. Already-selected → activate (`activateSelectedAction`)
  - No card hit → no-op

- [ ] **C.4** Write overview click tests in `overview_test.go`
  - Card selection, double-click activation, scrolled viewport, click outside cards

- [ ] **C.5** Implement `ImplementingModel.tabHitRegions() []HitRegion` in `implementing_view.go`
  - Compute X offset from "Repos:  " prefix using `ansi.StringWidth`
  - Each tab: icon + space + styled name, ANSI-aware width
  - Tab row at Y = 2 (after header + divider)
  - Separators: 3 spaces between tabs

- [ ] **C.6** Implement `ImplementingModel.HandleClick(relX, relY int) (ImplementingModel, tea.Cmd)`
  - Iterate `tabHitRegions()`, set `selectedRepo` on hit

- [ ] **C.7** Write implementing view click tests in `implementing_view_test.go`
  - Each tab → correct index
  - Click on separator → no-op
  - Click below tab row → no-op

### Batch D: Confirm + Workspace Init (depends on A, parallel with B/C)

- [ ] **D.1** Add `Size() (int, int)` method to `ConfirmDialog` in `confirm.go`
  - Render `View()`, split lines, measure max ANSI-aware width

- [ ] **D.2** Implement `handleConfirmClick` on `App` in `app.go`
  - Compute overlay origin via `OverlayOrigin(windowW, windowH, w, h)`
  - Translate to dialog-relative coordinates
  - Reject clicks outside dialog or not on button row (Y = 6)
  - Left half of dialog width → confirm (`OnYes`), right half → cancel (dismiss)

- [ ] **D.3** Implement `handleWorkspaceInitClick` on `App` in `app.go` / `overlay_workspace_init.go`
  - Uses stored `width`/`height` for overlay origin
  - Button row is last content row — compute Y from rendered height
  - Left half → Initialize, right half → Cancel

- [ ] **D.4** Write confirm dialog tests in `confirm_test.go`
  - `Size()` matches rendered output dimensions
  - Click confirm zone → `OnYes` cmd
  - Click cancel zone → dismiss
  - Click outside → no-op
  - Click inside but not button row → no-op

- [ ] **D.5** Write workspace init click tests in `overlay_workspace_init_test.go`
  - Click Initialize → confirm action
  - Click Cancel → dismiss
  - Click outside → no-op

### Batch E: New Session + Session Search Overlays (depends on A, parallel with B/C/D)

- [ ] **E.1** Implement `handleNewSessionClick` on `App` in `app.go` / `overlay_new_session.go`
  - Compute overlay origin from rendered overlay dimensions
  - Translate to overlay-relative coordinates
  - Dispatch to toggle rows, work-item list, or detail pane based on Y zone

- [ ] **E.2** Implement toggle-row click handling in new session overlay
  - Inner row 1 → cycle Source (same as cycling provider)
  - Inner row 2 → cycle Scope
  - Inner row 3 → cycle View (when `budget.hasViews`)
  - Inner row 4 → cycle State (when `budget.hasStates`)

- [ ] **E.3** Implement work-item list click handling in new session overlay
  - Account for list internal chrome (title bar, filter bar when active)
  - Item height = 3 (`DefaultDelegate.Height()` 2 + `DefaultDelegate.Spacing()` 1)
  - Compute `absoluteIndex` from `Paginator.Page * PerPage + itemSlot`
  - Call `m.issueList.Select(absoluteIndex)`

- [ ] **E.4** Implement detail pane focus on click in new session overlay
  - Click in right pane X range → set focus to detail

- [ ] **E.5** Write new session overlay click tests
  - Toggle row clicks → correct control cycled
  - List item click → correct selection
  - Detail pane click → focus change
  - Row count adapts to `hasViews`/`hasStates`

- [ ] **E.6** Implement `handleSessionSearchClick` on `App` in `app.go` / `overlay_session_search.go`
  - Scope toggle row click → cycle scope (same as `ctrl+s`)
  - Result list click → select item (same list math as E.3, item height = 3)
  - Preview pane click → `m.focus = focusPreview`

- [ ] **E.7** Write session search overlay click tests
  - Scope toggle → cycles scope
  - List item → selects
  - Preview pane → focuses

### Batch F: Settings + Duplicate Dialog + Overview Sub-overlays (depends on A, parallel with B/C/D/E)

- [ ] **F.1** Implement `handleSettingsClick` on `App` in `app.go` / `settings_page.go`
  - **Critical**: Settings is full-page — coordinates are absolute terminal coordinates, NOT overlay-relative
  - Check `m.editing` first → route to edit modal
  - Otherwise: sidebar zone (X < settings sidebar width) or main zone

- [ ] **F.2** Implement settings sidebar click in `settings_page.go`
  - Inner Y: subtract border (1)
  - Skip header (2 rows: "Settings" + blank)
  - Map `(innerY - 2)` to `visibleNavNodes()` index
  - Set `navCursor`, scroll main viewport to section

- [ ] **F.3** Implement settings main pane field click in `settings_page.go`
  - Compute `docLine = mainViewport.YOffset + (relY - mainPaneInnerTop)`
  - Iterate `fieldAnchors` to find containing section/field
  - Select the field

- [ ] **F.4** Implement settings edit modal click in `settings_page.go`
  - Modal centered via `renderCenteredOverlay` — use `OverlayOrigin` for translation
  - Click on a select option → `editOptionCursor = optionIndex`
  - Click outside modal → no-op

- [ ] **F.5** Write settings click tests in `settings_page_test.go`
  - Nav node click → section change
  - Field click → selection
  - Edit modal option click → option selection
  - Absolute coordinates (not overlay-relative)

- [ ] **F.6** Implement `handleDuplicateDialogClick` on `App` in `app.go`
  - Render dialog via `duplicateSessionDialogView()`
  - Measure with `measureRendered`, compute `OverlayOrigin`
  - Count rendered lines before option list to get option Y offsets
  - Click on option → select it. Click on already-selected → confirm

- [ ] **F.7** Write duplicate dialog click tests
  - Option click → select
  - Already-selected click → confirm
  - Outside click → no-op

- [ ] **F.8** Implement `handleOverviewOverlayClick` on `App` in `app.go` / `overview.go`
  - Render active sub-overlay, measure, compute origin
  - Translate to sub-overlay-relative coordinates
  - Dispatch to `content.HandleOverlayClick(relX, relY)`

- [ ] **F.9** Implement `HandleOverlayClick` on content/overview for each sub-overlay type
  - **Completed** sub-overlay: reuse `CompletedModel.HandleClick`
  - **Interrupted**: resume/abandon button zone detection (left/right half split)
  - **Question**: focus text input on click
  - **Plan review**: viewport scrolling or input focus for feedback
  - **Reviewing**: repo tab selection (similar to implementing tabs)

- [ ] **F.10** Write overview sub-overlay click tests
  - Click inside overlay → dispatched to correct sub-overlay
  - Click outside overlay → no-op

### Batch G: Integration Tests (depends on all above)

- [ ] **G.1** Create `internal/tui/views/app_mouse_test.go`
  - All test cases from the "Integration tests" table in the testing strategy section
  - Verify routing priority: confirm > duplicate > overlay > overview sub-overlay > sidebar/content
  - Verify wheel events fall through unchanged
  - Verify non-left-button / non-press events are no-ops
  - Verify pane gap and status bar clicks are no-ops

- [ ] **G.2** Run full test suite: `go test ./internal/tui/...`
  - All new tests pass
  - All existing tests pass unchanged

- [ ] **G.3** Manual smoke test
  - Click through every interactive element in every view
  - Verify `Shift+drag` text selection still works
  - Verify scroll wheel in all views

---

## Key Technical Decisions

### 1. Hit-testing via coordinate math, not viewport introspection

All hit regions are computed from layout constants and model state, not by inspecting rendered terminal output. This is deterministic, testable without a terminal, and avoids coupling to lipgloss rendering internals.

### 2. Click handlers return the same types as keyboard handlers

Every `HandleClick` returns `(Model, tea.Cmd)` — the same contract as `Update`. The App's mouse routing mirrors keyboard routing: translate coordinates, dispatch, collect commands.

### 3. Sidebar returns bool, not Cmd

The sidebar `ClickSelect` returns `bool` (cursor moved). The App calls `onSidebarMove()` when true. This matches the keyboard path (`MoveUp()` → `onSidebarMove()`) and keeps side-effect ownership in the App.

### 4. Routing order mirrors z-order

The `handleMouseClick` cascade checks state in reverse render order (topmost first): confirm dialog → duplicate dialog → overlays → overview sub-overlays → sidebar/content. This guarantees the visually topmost element receives the click.

### 5. Settings uses absolute coordinates

Settings is a full-page view, not a centered overlay. Its click handler receives raw terminal coordinates, not overlay-relative ones. This is explicitly called out because every other dialog/overlay applies origin translation.

### 6. Bubbles list item height is 3

With `ShowDescription: true`, `DefaultDelegate.Height() = 2` and `DefaultDelegate.Spacing() = 1`. Each visible list item occupies 3 rows. This applies to both the new session overlay and session search overlay.

---

## What This Does NOT Change

- No keyboard shortcuts are altered.
- No domain messages are added — clicks emit the same `tea.Cmd`s that key handlers already emit.
- `WithMouseCellMotion()` stays. `Shift+drag` remains the only path to native terminal text selection.
- Scroll wheel behavior is identical — existing `WheelUp`/`WheelDown` handlers are untouched.
- No new dependencies are introduced.

---

## Batch Dependency Graph

```
        ┌──── B (sidebar, completed)
        │
        ├──── C (overview, implementing)
        │
A ──────┼──── D (confirm, workspace init)
        │
        ├──── E (new session, search)
        │
        └──── F (settings, duplicate, overview sub-overlays)
                  │
                  └──── G (integration tests)
```

B through F are fully parallel after A lands. G depends on all.
