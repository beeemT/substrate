# TUI Design System
<!-- docs:last-integrated-commit 10e50295fb75f72c67233e191ae34fb8fc091f1e -->
This document is the living reference for the current TUI design system.

---

## Purpose

The Substrate TUI uses a small semantic design system that centralizes visual semantics and repeated chrome without over-componentizing Bubble Tea state.

The current architectural split is:

- `styles/` owns semantic tokens, shared chrome metrics, and prebuilt Lip Gloss styles
- `components/` owns reusable chrome and layout primitives
- `views/` owns Bubble Tea state, focus, message routing, viewport sizing, reserved-row math, and content-specific rendering decisions

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

---

## Shared primitives and surface coverage

The design system covers the main TUI surfaces without flattening them into one composition model.

### Shell and navigation

The main shell renders panes through shared pane primitives and shared chrome geometry. The sidebar uses shared selection, divider, and progress-bar semantics while keeping its own selection model and work-item-specific entry logic. The statusbar uses shared keybind and label semantics while keeping focus-sensitive hint composition and workspace metadata rendering local. Toasts share the same semantic overlay language, remain anchored without growing the main layout, cap width at 30% of terminal width with up to 4 wrapped content lines, and support stacking.

### Workflow views

Workflow surfaces such as planning, reviewing, plan review, question, completed, and interrupted views consume shared header, hint, tab, callout, pane, and status semantics.

What remains local to the view: viewport height math, repo selection state, critique cursor behavior, feedback and answer inputs, editor integration, and question and interruption handling.

### Transcript rendering

The shared transcript renderer produces bounded, width-aware output. No string pre-flattening step exists in the TUI pipeline — structured session log entries flow end-to-end from the session log through to the final view layer, where rendering happens once.

Entries are grouped into display blocks: plain text, assistant markdown, user prompts, tool cards, lifecycle events, questions, thinking blocks, and foreman directives. Grouping is structural, not text-based.

Tool cards use state-aware callout chrome — running state while in progress, tool variant on success, error variant on failure. Cards include smart args summaries for known tools with output truncation and explicit overflow counts. Thinking blocks render as collapsed single-line summaries or expanded muted markdown, controlled by a collapse flag.

### Overlays

Session history search, the unified work browser, help, workspace initialization, log viewer, add repository, confirm dialogs, duplicate-session dialogs, and toasts share overlay framing, divider behavior, and focused/unfocused pane styling.

Overlay and modal styling uses transparent backgrounds. See `internal/tui/AGENTS.md` for the full rule set covering ANSI-reset propagation and inter-pane separator constraints.

What remains local to the view: provider-specific filter logic, search behavior, content generation, and capability-driven messaging. Shared chrome must not erase provider-specific truth about scopes, filters, or status messaging.

### Settings

Settings shares semantic tokens and chrome language for borders, selections, breadcrumbs, scrollbars, and error styling. Settings does not need to mimic the composition of workflow panes or overlays. It keeps its own full-screen split layout, sticky-header behavior, field rendering, and interaction model while using shared semantic styling.

---

## Failure modes to avoid

1. **Over-abstracting viewport screens**
   If shared components start owning reserved-row math, they will violate the layout contracts and break bounded-layout assumptions.

2. **Partial cutover**
   If views and components both retain palette ownership, the repository drifts back toward competing design systems.

3. **Forcing settings into the wrong shell**
   Settings should share semantic tokens, not the same compositional model as overlays or workflow panes.

4. **Leaking raw colors through APIs**
   Helper APIs should continue to accept semantic inputs, not caller-owned hex colors or parallel style conventions.

5. **Treating layout rules as optional**
   The design system is not only colors and borders; it also includes the hard sizing rules that keep Bubble Tea rendering bounded.

---

## Reviewer checklist

Future TUI work should continue to satisfy all of the following:

- `styles/` remains the single authority for shared visual semantics and chrome metrics
- repeated pane, header, divider, hints, tabs, callout, and overlay rendering continues to live in `components/`
- workflow, overlay, shell, and settings views consume shared semantics instead of reintroducing local variants
- layout math stays in the views that own the state
- layout tests remain green and cover bounded rendering, including narrow cases
