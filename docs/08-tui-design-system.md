# TUI Design System
<!-- docs:last-integrated-commit 15191d7174f9fd07787eb39e2a4763fb6c43cfeb -->

This document is the living reference for the current TUI design system.

---

## Purpose

The Substrate TUI uses a small semantic design system that centralizes visual semantics and repeated chrome without over-componentizing Bubble Tea state.

The current architectural split is:

- `internal/tui/styles/` owns semantic tokens, shared chrome metrics, and prebuilt Lip Gloss styles
- `internal/tui/components/` owns reusable chrome and layout primitives
- `internal/tui/views/` owns Bubble Tea state, focus, message routing, viewport sizing, reserved-row math, and content-specific rendering decisions

This is intentionally not a React-style deep component tree. Bubble Tea needs explicit size propagation and local message handling, so the design system standardizes rendering contracts and shared chrome without hiding screen state in nested child models.

## Why the split exists

The design system exists to keep shared TUI chrome singular and maintainable.

Without a clear split, workflow screens, overlays, shell surfaces, and settings pages drift toward duplicated headers, borders, hint rows, pane framing, and palette usage. The current design-system boundary prevents that drift by making package ownership explicit:

- `styles/` defines the shared visual language
- `components/` renders the reusable chrome
- `views/` decides when and why a surface uses those primitives

That boundary matters most in Bubble Tea because the hard problems are usually sizing, focus, reserved-row accounting, and message flow. Those concerns stay close to the owning view even when the visuals are shared.

---

## Design principles

### Non-goals

- Do not turn the TUI into a React clone.
- Do not split screen-level Bubble Tea state into child state machines just to increase reuse.
- Do not hide viewport sizing or reserved-row math behind generic wrappers.
- Do not force settings to use the same composition model as workflow panes or overlays.
- Do not preserve palette ownership in both `views/` and `components/`; `styles/` is the single source of truth.

### Architectural contracts

The design system must continue to preserve these repository-level contracts:

- `docs/07-implementation-plan.md` owns the intended repository structure and TUI walkthrough gates.
- `docs/06-tui-design.md` owns operator-facing TUI behavior: shell layout, content modes, overlays, settings interaction, and runtime keyboard flow.
- `internal/tui/AGENTS.md` owns the exact Bubble Tea and Lip Gloss layout rules that keep rendering bounded.

### Layout contracts

The layout rules in `internal/tui/AGENTS.md` are part of the design system, not optional implementation detail. In particular:

- parent width and height do not protect children from overflow
- child content must be sized to inner dimensions after frame subtraction
- viewport heights must reserve every fixed chrome row
- dynamic chrome changes must trigger recalculation
- long lines must wrap or truncate before render
- empty, loading, and populated states must preserve outer dimensions
- layout changes need bounded-rendering tests, including narrow cases

### Regression floor

These tests define the minimum acceptable safety net for shared shell and design-system changes:

- `internal/tui/views/app_layout_test.go`
- `internal/tui/views/planning_view_test.go`
- `internal/tui/views/plan_review_test.go`
- `internal/tui/views/sidebar_test.go`
- `internal/tui/views/statusbar_test.go`
- `internal/tui/views/settings_page_test.go`
- `internal/tui/views/overview_test.go`
- `internal/tui/views/session_transcript_test.go`
- `internal/tui/views/overlay_source_items_test.go`
- `internal/tui/views/app_toast_test.go`

---

## Current ownership by package

### `internal/tui/styles/`

Owns:

- semantic color tokens
- semantic text roles
- semantic chrome roles
- shared geometry and frame metrics
- prebuilt Lip Gloss styles
- settings subtheme styles
- overlay styles
- status and diff styles

This layer defines roles such as:

- title, subtitle, muted, hint, label, accent, and link text
- divider, pane border, and focused-pane border
- overlay background, border, and focused border
- selected-row backgrounds and related selection treatment
- active and inactive tab treatment
- settings breadcrumbs, active selection, inactive selection, and scrollbars
- warning, error, success, and interrupted states

Current contract to preserve:

- `styles/` owns palette semantics and shared geometry.
- Shared primitives may consume semantic styles, but they must not reintroduce raw color ownership.
- View code may choose when to apply a semantic role, but it must not define a competing one.

### `internal/tui/components/`

Owns reusable render primitives such as:

- pane shells
- header blocks with title, optional meta, and divider treatment
- hint rows and keybind rows
- tabs rows
- callouts and bordered cards
- overlay shells and split overlay geometry
- semantic progress bars
- semantic confirm and toast surfaces
- shared input constructors (`NewTextInput`, `NewTextArea`) with macOS-compatible key bindings
- animated bunny art for empty-state decoration

Current primitives under `internal/tui/components/` include:

- `pane.go`
- `header_block.go`
- `keyhints.go`
- `tabs.go`
- `callout.go`
- `overlay_frame.go`
- `progress.go`
- `toast.go`
- `confirm.go`
- `input.go`
- `bunny.go`

Current contract to preserve:

- Components standardize rendering, borders, hint rows, and shared layout shells.
- Components do not own Bubble Tea state, viewport math, reserved-row accounting, or message routing.
- Shared helper APIs should accept semantic inputs, not raw caller-owned colors or parallel style conventions.

### `internal/tui/views/`

Owns:

- Bubble Tea model state
- focus and input-mode transitions
- viewport sizing and resizing
- reserved-row accounting
- content-specific rendering decisions
- list and viewport orchestration
- message routing and updates
- surface-specific state such as repo selection, critique cursor, question state, review state, and settings interaction

Current contract to preserve:

- Views remain responsible for `SetSize(...)`, list and viewport sizing, dynamic row reservation, focus transitions, and mode-specific behavior.
- Shared chrome must not erase surface-specific truth such as provider capability messaging, workflow state, or settings-specific interaction.

---

## Shared primitives and surface coverage

The design system covers the main TUI surfaces without flattening them into one composition model.

### Shell and navigation

- `app.go` renders the main panes through shared pane primitives and shared chrome geometry.
- `sidebar.go` uses shared selection, divider, and progress-bar semantics while keeping its own selection model and work-item-specific entry logic.
- `statusbar.go` uses shared keybind and label semantics while keeping focus-sensitive hint composition and workspace metadata rendering local.
- Toasts share the same semantic overlay language, remain anchored without growing the main layout, cap width at 30% of terminal width with up to 4 wrapped content lines, and support stacking via `StackView`.

### Workflow views

Workflow surfaces such as planning, reviewing, plan review, question, completed, and interrupted views consume shared header, hint, tab, callout, pane, and status semantics.

What remains local to the view:

- viewport height math
- repo selection state
- critique cursor behavior
- feedback and answer inputs
- editor integration
- question and interruption handling

### Transcript rendering

`views/session_transcript.go` owns `RenderTranscript`, the shared transcript renderer. It consumes `[]sessionlog.Entry` values and produces bounded, width-aware output. No string pre-flattening step exists in the TUI pipeline — structured `sessionlog.Entry` values flow end-to-end from the session log through to the final view layer, where `RenderTranscript` is the sole rendering point.

Entries are grouped into display blocks: plain text, assistant markdown, user prompts, tool cards, lifecycle events, questions, thinking blocks, and foreman directives. Grouping is structural, not text-based:

- **Tool cards**: `tool_start`, `tool_output`, and `tool_result` entries are grouped into single tool cards via per-tool FIFO queues. Each card uses state-aware callout chrome — `CalloutRunning` while in progress, `CalloutTool` on success, `CalloutError` on failure. Cards include smart args summaries for known tools (read, grep, find, write, edit, bash, lsp, ast_grep, ast_edit, fetch, web_search, task). Output lines are truncated (4 lines default, 12 verbose) with explicit overflow counts.
- **Thinking blocks**: rendered as collapsed single-line summaries or expanded muted markdown, controlled by the `collapseThinking` flag.
- **Callout variants**: tool cards and lifecycle chrome use `components.RenderCallout` with `CalloutRunning`, `CalloutError`, and `CalloutTool` variants.

Semantic styles consumed: `Thinking` for expanded thinking-block text, `Accent` for highlighted tool args, and the callout variant styles from `styles/` for tool-card state chrome.

### Overlays

Session history search, the unified work browser (source items), help, workspace initialization, log viewer, confirm dialogs, duplicate-session dialogs, and toasts share overlay framing, divider behavior, and focused/unfocused pane styling.

Overlay and modal styling uses **transparent backgrounds** — no `Background()` or `BorderBackground()` on overlay or modal styles. See `internal/tui/AGENTS.md` "Overlay and Modal Styling" for the full rule set covering ANSI-reset propagation and inter-pane separator constraints.

What remains local to the view:

- provider-specific filter logic
- search behavior
- content generation
- capability-driven messaging

The unified work browser must stay capability-driven. Shared chrome must not erase provider-specific truth about scopes, filters, or status messaging.

### Settings

Settings shares semantic tokens and chrome language for borders, selections, breadcrumbs, scrollbars, and error styling.

Settings does not need to mimic the composition of workflow panes or overlays. It keeps its own full-screen split layout, sticky-header behavior, field rendering, and interaction model while using shared semantic styling.

---

## Current file-layout contract

The design system is reflected in the current repository structure:

```text
internal/tui/styles/
  theme.go
  chrome.go

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
  input.go
  bunny.go

internal/tui/views/
  app.go
  app_macos_keys.go
  sidebar.go
  statusbar.go
  overview.go
  session_transcript.go
  planning_view.go
  reviewing_view.go
  plan_review.go
  question_view.go
  completed_view.go
  interrupted_view.go
  duplicate_session_dialog.go
  overlay_session_search.go
  overlay_source_items.go
  overlay_logs.go
  overlay_new_session.go
  overlay_help.go
  overlay_workspace_init.go
  settings_page.go
```

This contract is about responsibility boundaries, not about eliminating view-specific code.

---

## Verification guidance for future changes

When touching shared styles, components, or layout helpers:

- run the targeted tests for the views or components you changed
- when touching shell geometry, overlay geometry, or shared chrome metrics, run `go test ./internal/tui/...`
- re-check the TUI walkthrough expectations in `docs/07-implementation-plan.md` Phase 12 for broad shell or overlay changes

Useful smoke suites include:

- `go test ./internal/tui/views -run 'TestSessionLogViewRespectsRequestedHeightWithMeta|TestPlanReview'`
- `go test ./internal/tui/views -run 'TestAppViewWithSessionInteractionFitsWindow|TestSidebar|TestStatusBar'`
- `go test ./internal/tui/views -run 'TestSettingsPage_'`

These checks matter because the common failure mode is silent layout drift, overflow, or re-fragmented chrome ownership rather than compilation failure.

## Reviewer checklist

Future TUI work should continue to satisfy all of the following:

- `styles/` remains the single authority for shared visual semantics and chrome metrics
- repeated pane, header, divider, hints, tabs, callout, and overlay rendering continues to live in `components/`
- workflow, overlay, shell, and settings views consume shared semantics instead of reintroducing local variants
- layout math stays in the views that own the state
- width, height, and layout tests remain green
- broad shell or overlay changes continue to pass `go test ./internal/tui/...`

---

## Failure modes to avoid

1. **Over-abstracting viewport screens**
   - If shared components start owning reserved-row math, they will violate `internal/tui/AGENTS.md` and break bounded-layout assumptions.

2. **Partial cutover**
   - If views and components both retain palette ownership, the repository drifts back toward competing design systems.

3. **Forcing settings into the wrong shell**
   - Settings should share semantic tokens, not the same compositional model as overlays or workflow panes.

4. **Leaking raw colors through APIs**
   - Helper APIs should continue to accept semantic inputs, not caller-owned hex colors or parallel style conventions.

5. **Treating layout rules as optional**
   - The design system is not only colors and borders; it also includes the hard sizing rules that keep Bubble Tea rendering bounded.
