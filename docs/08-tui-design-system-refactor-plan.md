# TUI Design System Refactor Plan

## Goal

Move the TUI toward a small semantic design system that centralizes visual semantics and repeated chrome without over-componentizing Bubble Tea state.

The target architecture is:

- `internal/tui/styles/` owns semantic tokens and prebuilt Lip Gloss styles
- `internal/tui/components/` owns reusable chrome and layout primitives
- `internal/tui/views/` continues to own state, focus, message routing, viewport sizing, and reserved-row math

This is explicitly not a React-style deep component tree. Bubble Tea requires explicit size propagation and local message handling, so the design system should standardize rendering and layout contracts, not hide workflow state in nested child models.

---

## Why this refactor makes sense

The repository already intends a split between views, components, and styles:

- `docs/07-implementation-plan.md:27-29`

The TUI design already assumes a central theme and shared palette semantics:

- `docs/06-tui-design.md:584-614`

The repository also already has partial shared infrastructure:

- Semantic theme base: `internal/tui/styles/theme.go:5-97`
- Overlay geometry and split-pane layout primitives: `internal/tui/components/overlay_frame.go:10-166`
- Reusable toast, confirm, and progress components: `internal/tui/components/toast.go:19-118`, `internal/tui/components/confirm.go:8-41`, `internal/tui/components/progress.go:10-30`

The gap is that only part of the styling is centralized today. Many views still recreate the same headers, dividers, hint rows, borders, pane chrome, and color choices inline.

Examples of repeated or drifting chrome:

- Workflow views:
  - `internal/tui/views/planning_view.go:121-151`
  - `internal/tui/views/implementing_view.go:161-209`
  - `internal/tui/views/reviewing_view.go:100-147`
  - `internal/tui/views/plan_review.go:133-162`
  - `internal/tui/views/question_view.go:104-159`
- Shell/navigation:
  - `internal/tui/views/app.go:1383-1458`
  - `internal/tui/views/sidebar.go:260-302`
  - `internal/tui/views/statusbar.go:25-95`
- Overlays:
  - `internal/tui/views/overlay_session_search.go:443-497`
  - `internal/tui/views/overlay_new_session.go:1083-1139`
  - `internal/tui/views/overlay_new_session.go:1681-1750`
  - `internal/tui/views/overlay_new_session.go:1846-1856`
- Settings:
  - `internal/tui/views/settings_page.go:809-1182`

That makes a semantic design-system refactor worthwhile.

---

## Non-goals

- Do not turn the TUI into a React clone.
- Do not split screen-level Bubble Tea state into many child models.
- Do not hide viewport sizing and reserved-row math behind generic wrappers.
- Do not force settings to use the same exact structure as workflow screens or overlays.
- Do not preserve palette ownership in both `views/` and `components/`; styles must become the single source of truth.

---

## Existing contracts that must remain true

### Architectural contracts

- `docs/07-implementation-plan.md:27-29` — intended split between `views/`, `components/`, and `styles/`
- `docs/06-tui-design.md:24-34` — persistent two-pane shell
- `docs/06-tui-design.md:198-205` — unified work browser / overlay behavior
- `docs/06-tui-design.md:568-570` — status bar and toast behavior
- `docs/07-implementation-plan.md:431-461` — TUI sub-phase gates and final walkthrough

### Layout contracts

The TUI layout rules in `internal/tui/AGENTS.md:4-16` are hard constraints:

- width/height set on a parent box are not overflow protection
- child content must be sized to inner dimensions
- viewport height must account for every reserved chrome row
- dynamic rows must trigger recalculation
- long lines must wrap or truncate before render
- tests must confirm bounded rendering

These rules are part of the design system. They are not optional implementation details.

### Regression tests that define the floor

The following tests should remain green through the migration:

- `internal/tui/views/app_layout_test.go:99-122`
- `internal/tui/views/planning_view_test.go:10-25`
- `internal/tui/views/implementing_view_test.go:11-31`
- `internal/tui/views/plan_review_test.go:89-100`
- `internal/tui/views/sidebar_test.go:60-78`
- `internal/tui/views/sidebar_test.go:188-206`
- `internal/tui/views/statusbar_test.go:15-57`
- `internal/tui/views/settings_page_test.go:435-467`

---

## Target end state

### `internal/tui/styles/`

Owns:

- semantic color tokens
- semantic text roles
- semantic chrome roles
- prebuilt Lip Gloss styles
- settings subtheme styles
- overlay styles

### `internal/tui/components/`

Owns:

- bordered pane shells
- title/meta/divider header blocks
- keybind/hint rows
- tabs
- callouts / bordered content boxes
- overlay shell rendering
- semantic progress bars
- semantic modal and toast surfaces

### `internal/tui/views/`

Owns:

- Bubble Tea model state
- focus and input mode transitions
- viewport sizing and resizing
- reserved-row math
- content-specific rendering decisions
- message routing and updates

---

## Design-system boundary

### What belongs in `styles/`

The style layer should own semantic roles such as:

- Title
- Subtitle
- Muted
- Hint
- Label
- Accent
- Link
- Divider
- Pane border
- Focused pane border
- Overlay background
- Overlay border
- Overlay focused border
- Selected row background
- Active tab text
- Inactive tab text
- Settings breadcrumb
- Settings selection active/inactive
- Scrollbar track/thumb
- Warning / error / success / interrupted

### What belongs in `components/`

The component layer should own reusable render primitives such as:

- pane shell
- header block
- hint row
- tabs row
- callout/card
- overlay shell
- semantic progress bar

### What stays in `views/`

The view layer must keep:

- `SetSize(...)`
- viewport height calculations
- dynamic row reservation
- textinput lifecycle
- list/model orchestration
- repo tab selection state
- question / review / settings interaction state

---

## Styles cutover plan

Current issue: `internal/tui/styles/theme.go:5-97` centralizes only part of the visual language. Reusable components and many views still own raw palette values.

### Phase 1A — expand `Theme`

Add semantic tokens for roles already duplicated in views/components.

Recommended additions:

- `Divider`
- `Hint`
- `Label`
- `Link`
- `PaneBorder`
- `PaneBorderFocused`
- `OverlayBg`
- `OverlayBorder`
- `OverlayBorderFocused`
- `SelectionActive`
- `SelectionInactive`
- `SettingsText`
- `SettingsTextStrong`
- `SettingsBreadcrumb`
- `ScrollbarTrack`
- `ScrollbarThumb`
- `ScrollbarThumbFocused`

### Phase 1B — expand `Styles`

Add prebuilt semantic Lip Gloss styles so views stop building ad hoc style chains.

Recommended additions:

- `Divider`
- `Hint`
- `Label`
- `Accent`
- `Link`
- `Pane`
- `PaneFocused`
- `OverlayFrame`
- `OverlayPane`
- `OverlayPaneFocused`
- `SectionLabel`
- `TabActive`
- `TabInactive`
- `Callout`
- `CalloutWarning`
- `SettingsBreadcrumb`
- `SettingsSection`
- `SettingsSelectionActive`
- `SettingsSelectionInactive`

### Phase 1C — introduce semantic chrome metrics and geometry helpers

Color cutover alone is not enough. The current two-pane shell and overlay system still encode padding, border widths, overlay sizing, and focus-shell selection as ad-hoc constants in places like `internal/tui/views/app.go:1369-1458` and `internal/tui/components/overlay_frame.go:84-220`.

Introduce a shared chrome geometry primitive plus semantic metrics for:

- pane border widths and inner-frame deductions
- pane padding
- overlay frame width and body height rules
- split-overlay pane sizing
- toast overlay anchoring
- focus treatment that affects shell selection or reserved space

This geometry layer should be wired into `App.View(...)` and the overlay shell before per-view visual cleanups start.

### Phase 1D — remove raw color ownership outside `styles/`

First targets:

- `internal/tui/components/overlay_frame.go:19-24`, `119-138`
- `internal/tui/components/confirm.go:29-39`
- `internal/tui/components/toast.go:71-94`
- `internal/tui/components/progress.go:13-29`

First view-level consumers of the new semantic tokens should be the places with the clearest palette drift:

- `internal/tui/views/statusbar.go:37-94`
- `internal/tui/views/overlay_session_search.go:449-496`
- `internal/tui/views/overlay_new_session.go:1681-1753`

Those files currently hard-code overlay and hint colors directly and should be the first evidence that palette ownership has actually moved into `styles`.

Important cutover requirement:

- `RenderProgressBar(...)` should stop taking raw colors from callers.

Done when:

- reusable components no longer own private hex colors
- shared chrome colors no longer appear directly in `views/`
- `styles` is the only palette authority

---

## Component extraction plan

This refactor should extract render primitives, not state machines.

### Components to add or formalize

#### `internal/tui/components/pane.go`

Purpose:

- standard bordered pane shell
- focused/unfocused pane shell

Primary consumers:

- `internal/tui/views/app.go:1386-1404`
- `internal/tui/views/settings_page.go:844-923`

#### `internal/tui/components/header_block.go`

Purpose:

- title
- optional meta row
- divider

Primary consumers:

- `internal/tui/views/planning_view.go:126-150`
- `internal/tui/views/implementing_view.go:166-206`
- `internal/tui/views/reviewing_view.go:101-146`
- `internal/tui/views/plan_review.go:134-160`

#### `internal/tui/components/keyhints.go`

Purpose:

- render keybind rows with accent key + muted label semantics

Primary consumers:

- `internal/tui/views/plan_review.go:149-154`
- `internal/tui/views/question_view.go:147-152`
- `internal/tui/views/statusbar.go:42-94`
- `internal/tui/views/planning_view.go:145`
- `internal/tui/views/implementing_view.go:205-206`
- `internal/tui/views/reviewing_view.go:143-144`

#### `internal/tui/components/tabs.go`

Purpose:

- active/inactive tabs row

Primary consumers:

- `internal/tui/views/implementing_view.go:173-183`
- `internal/tui/views/reviewing_view.go:107-115`

#### `internal/tui/components/callout.go`

Purpose:

- bordered content box
- metadata card
- warning callout

Primary consumers:

- `internal/tui/views/question_view.go:121-145`
- `internal/tui/views/plan_review.go:229-242`
- `internal/tui/views/overlay_new_session.go:1088-1103`

#### `internal/tui/components/overlay_frame.go`

Purpose:

- remain the canonical overlay shell and split-pane geometry primitive
- stop owning private colors
- render from semantic styles instead

Primary consumers:

- `internal/tui/views/overlay_session_search.go:479-497`
- `internal/tui/views/overlay_new_session.go:1745-1749`

### Extraction constraint

Do not move viewport math into these components.

The views must still own:

- body height calculation
- reserved row calculation
- dynamic input-row reservation
- viewport/list size propagation

---

## Recommended implementation order

## Phase 2 — chrome geometry foundation

Before workflow view cleanup, surface the shell/layout math that is currently implicit in:

- `internal/tui/views/app.go:1369-1458`
- `internal/tui/components/overlay_frame.go:84-220`

Deliverables:

- a shared chrome geometry primitive for pane widths, inner dimensions, overlay sizing, and toast anchoring
- semantic metrics for padding, border widths, and focus-shell variants
- `App.View(...)` and overlay rendering consuming that geometry instead of magic numbers

Validation:

- re-run `go test ./internal/tui/...`
- re-check the documented TUI walkthrough expectations in `docs/07-implementation-plan.md:431-461` whenever shell or overlay infrastructure changes


## Phase 3 — workflow chrome first

### Step 3.1 — `planning_view.go`

Refactor first:

- `internal/tui/views/planning_view.go:121-151`

Why:

- cleanest header/divider/body/hints structure
- least stateful screen using the repeated workflow chrome
- best proving ground for the new style and header primitives

### Step 3.2 — `implementing_view.go`

Refactor next:

- `internal/tui/views/implementing_view.go:161-209`

Extract:

- title/divider semantics
- hint row semantics
- repo row label styles
- later shared repo tab treatment

Keep local:

- `viewportHeight()`
- repo selection state
- per-repo output logic

### Step 3.3 — `reviewing_view.go`

Refactor next:

- `internal/tui/views/reviewing_view.go:100-147`

Extract:

- workflow shell
- repo tab row
- hint row

Keep local:

- critique cursor
- severity rendering logic
- override/re-implement actions

### Step 3.4 — `plan_review.go`

Refactor after the simpler workflow views:

- `internal/tui/views/plan_review.go:133-162`
- `internal/tui/views/plan_review.go:229-262`

Extract:

- shell chrome
- hint row
- description / next-step bordered blocks

Keep local:

- input mode transitions
- feedback input ownership
- plan editor integration

### Step 3.5 — `question_view.go`

Refactor after workflow shell primitives exist:

- `internal/tui/views/question_view.go:104-159`

Extract:

- shell chrome
- question card / answer card callouts
- hint row

Keep local:

- answer approval / send flow
- uncertain-state handling
- textinput lifecycle

### Step 3.6 — summary/end-state workflow views

Cut over smaller screens:

- `internal/tui/views/completed_view.go:49-80`
- `internal/tui/views/failed_view.go:38-49`
- `internal/tui/views/interrupted_view.go`

These should become thin consumers of shared styles.

---

## Phase 4 — shell, sidebar, and status bar

### Step 4.1 — app shell

Refactor:

- `internal/tui/views/app.go:1383-1458`

Move to shared primitives:

- pane borders
- pane style selection
- semantic border/focus colors

Keep local:

- `mainPageLayoutMetrics(...)`
- overlay selection and routing
- toast placement orchestration

### Step 4.2 — sidebar

Refactor:

- `internal/tui/views/sidebar.go:260-302`

Move to shared styles/components:

- selected row styling
- divider styling
- semantic title/header rendering
- progress bar styling

### Step 4.3 — status bar

Refactor:

- `internal/tui/views/statusbar.go:25-95`

Move to shared styles/components:

- key accent style
- label style
- right-side muted metadata style

---

## Phase 5 — overlay system cutover

### Step 5.1 — search + browse overlays

Refactor:

- `internal/tui/views/overlay_session_search.go:449-496`
- `internal/tui/views/overlay_new_session.go:1681-1750`

Shared extraction targets:

- overlay title style
- active/inactive filter label styles
- footer hints
- loading/empty-state muted text
- pane title styling
- shared overlay header/body-divider/hint-row helpers built on the existing split-overlay infrastructure in `internal/tui/components/overlay_frame.go:10-159`

The shell should become uniform across overlays while provider-specific filters, labels, and content remain in the view models.

### Step 5.2 — overlay detail and metadata cards

Refactor:

- `internal/tui/views/overlay_new_session.go:1088-1139`
- `internal/tui/views/overlay_new_session.go:1846-1856`

Shared extraction targets:

- section labels
- metadata callout box
- form label styling
- form hints styling

### Step 5.3 — modal surfaces

Refactor:

- `internal/tui/components/confirm.go:29-39`
- `internal/tui/components/toast.go:71-94`
- `internal/tui/views/overlay_workspace_init.go:177-180`
- `internal/tui/views/overlay_help.go:91-93`

Goal:

- all modal and overlay surfaces should use the same semantic shell language

---

## Phase 6 — settings subtheme convergence

Refactor:

- `internal/tui/views/settings_page.go:809-1182`

### Move to shared semantic styles

- border colors and focus colors: `830-841`, `851-853`, `920-922`, `1178-1180`
- selection colors: `879-885`, `1072-1113`
- sticky header / breadcrumb / dividers: `1021-1028`, `1049-1051`, `1063`
- scrollbar colors: `957-984`
- field error styling: `1171-1172`

### Keep local to settings

- tree/detail pane behavior
- sticky header logic
- viewport + scrollbar layout logic
- settings-specific field and section rendering

Goal:

- settings shares the same palette and focus language
- settings does not lose its bespoke interaction model

---

## Suggested file layout after refactor

```text
internal/tui/styles/
  theme.go
  chrome.go
  overlay.go
  settings.go

internal/tui/components/
  overlay_frame.go
  pane.go
  header_block.go
  keyhints.go
  tabs.go
  callout.go
  progress.go
  toast.go
  confirm.go
```

This is a recommended direction, not a mandatory exact file list.

---

## PR / change sequence

### PR 1 — style foundation

- expand `styles.Theme`
- expand `styles.Styles`
- add semantic chrome metrics for padding, border widths, overlay sizing, and focus-shell variants
- remove private color ownership from `overlay_frame`, `progress`, `toast`, `confirm`

### PR 2 — chrome geometry foundation

- `app.go`
- `overlay_frame.go`
- shared geometry helpers for pane sizing, overlay sizing, and toast anchoring

### PR 3 — simplest workflow views

- `planning_view.go`
- `implementing_view.go`
- `reviewing_view.go`

### PR 4 — stateful workflow consumers of the new primitives

- `plan_review.go`
- `question_view.go`
- `completed_view.go`
- `failed_view.go`
- `interrupted_view.go`

### PR 5 — shell/navigation cleanup

- `sidebar.go`
- `statusbar.go`
- any remaining `app.go` visual cleanup not covered by the geometry foundation

### PR 6 — overlays

- `overlay_session_search.go`
- `overlay_new_session.go`
- `overlay_help.go`
- `overlay_workspace_init.go`
- modal surfaces

### PR 7 — settings alignment

- `settings_page.go`

---

## Verification plan

### After styles foundation

Run targeted tests for any shared component or view touched.
For the geometry foundation, also re-run:

- `go test ./internal/tui/...`
- the documented TUI walkthrough expectations in `docs/07-implementation-plan.md:431-461` for the affected shell and overlay phases
### After workflow phases

Run targeted view tests, including:

- `go test ./internal/tui/views -run 'TestSessionLogViewRespectsRequestedHeightWithMeta|TestImplementingViewRespectsRequestedHeight|TestPlanReview'`

### After shell/navigation phase

Run:

- `go test ./internal/tui/views -run 'TestAppViewWithSessionInteractionFitsWindow|TestSidebar|TestStatusBar'`

### After settings phase

Run:

- `go test ./internal/tui/views -run 'TestSettingsPage_'`

### Final gate

Run:

- `go test ./internal/tui/...`

This matches the documented TUI gate in `docs/07-implementation-plan.md:461`.

---

## Major risks

1. **Over-abstracting viewport screens**
   - If shared components start owning reserved-row math, they will violate `internal/tui/AGENTS.md:4-16` and likely break layout tests.

2. **Partial cutover**
   - If views and components both retain palette ownership, the repository will end up with two competing design systems.

3. **Forcing settings into the wrong shell**
   - Settings should share tokens, not the same compositional model as overlays or workflow panes.

4. **Leaking raw colors through APIs**
   - Helper APIs like `RenderProgressBar(...)` should not require raw colors from callers.

---

## Recommended starting point

Start with:

1. `internal/tui/styles/theme.go`
2. `internal/tui/components/overlay_frame.go`
3. `internal/tui/views/app.go`
4. `internal/tui/views/planning_view.go`

Why:

- `theme.go` is the style cutover root
- `overlay_frame.go` already contains the strongest shared geometry foundation
- `app.go` is where pane widths, borders, and toast anchoring still rely on magic numbers
- once shell geometry is centralized, `planning_view.go` is the safest first workflow consumer

---

## TODO list

- [x] Add semantic chrome metrics for pane padding, border widths, overlay frame sizing, and focus-shell variants
- [x] Extract a shared chrome geometry primitive from `internal/tui/views/app.go` and `internal/tui/components/overlay_frame.go`
- [x] Route app shell sizing, overlay sizing, and toast placement through the shared geometry primitive
- [x] Expand `internal/tui/styles/theme.go` with the missing semantic tokens
- [x] Expand `internal/tui/styles.Styles` with reusable semantic Lip Gloss styles
- [x] Remove private color ownership from `components/overlay_frame.go`
- [x] Remove private color ownership from `components/confirm.go`
- [x] Remove private color ownership from `components/toast.go`
- [x] Refactor `components/progress.go` so callers do not pass raw colors
- [x] Add shared pane primitive(s) under `internal/tui/components/`
- [x] Add shared header/meta/divider primitive(s) under `internal/tui/components/`
- [x] Add shared key hint row renderer under `internal/tui/components/`
- [x] Add shared tabs renderer under `internal/tui/components/`
- [x] Add shared callout/card renderer under `internal/tui/components/`
- [x] Migrate `internal/tui/views/planning_view.go` to the new styles/components
- [x] Migrate `internal/tui/views/implementing_view.go` to the new styles/components
- [x] Migrate `internal/tui/views/reviewing_view.go` to the new styles/components
- [x] Migrate `internal/tui/views/plan_review.go` to the new styles/components
- [x] Migrate `internal/tui/views/question_view.go` to the new styles/components
- [x] Migrate `internal/tui/views/completed_view.go` to the new styles/components
- [x] Migrate `internal/tui/views/failed_view.go` to the new styles/components
- [x] Migrate `internal/tui/views/interrupted_view.go` to the new styles/components
- [x] Migrate `internal/tui/views/app.go` pane chrome to shared pane primitives
- [x] Migrate `internal/tui/views/sidebar.go` to shared selection/divider/progress styles
- [x] Migrate `internal/tui/views/statusbar.go` to shared keybind/hint styles
- [x] Migrate `internal/tui/views/overlay_session_search.go` to semantic overlay styles
- [x] Migrate `internal/tui/views/overlay_new_session.go` to semantic overlay styles and shared cards
- [x] Migrate `internal/tui/views/overlay_help.go` to semantic overlay styles
- [x] Migrate `internal/tui/views/overlay_workspace_init.go` to semantic overlay styles
- [x] Migrate `internal/tui/views/settings_page.go` to a shared settings subtheme
- [x] Run targeted tests after each migration phase
- [x] Re-check the documented TUI walkthrough expectations in `docs/07-implementation-plan.md:431-461` after shell and overlay infrastructure changes
- [x] Run `go test ./internal/tui/...` as the final verification gate

---

## Definition of done

This refactor is complete when all of the following are true:

- `styles` is the single authority for shared TUI visual semantics
- repeated pane/header/divider/hints/tabs/callout rendering lives in `components`
- workflow, overlay, shell, and settings views consume those shared semantics
- layout math still lives in the views that own the state
- existing width/height/layout tests remain green
- the final TUI gate `go test ./internal/tui/...` passes
