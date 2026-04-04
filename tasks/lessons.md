# Lessons Learned

## TUI Layout & Rendering

### Lip Gloss Box Model
**Mistake**: I treated Lip Gloss `Width` values as pure content width in the new-session overlay, which caused padded/bordered panes and the modal itself to overflow and wrap unexpectedly under constrained terminal sizes.
**Pattern**: I changed visual layout code without validating the library's actual box-model semantics.
**Rule**: For every non-trivial TUI layout, calculate with the rendering library's frame sizes explicitly and add tests that assert rendered width and height stay within the requested viewport across narrow and normal terminal sizes.
**Applied**: Overlays, split panes, sticky footers/headers, scrollable viewports, and any TUI component that combines borders, padding, and nested boxes.

### Pane Chrome Must Stay Stable Across States
**Mistake**: I stabilized overlay layout math but let browse panes switch from a raw loading string to a list component after items arrived, changing the pane's internal chrome and making the loaded state look resized. A similar issue occurred when loaded detail content introduced a title row that empty state did not reserve, and when the detail column lost a visible row after tickets loaded because both the pane chrome and the content rendered their own headers.
**Pattern**: I verified geometry structs and loaded rendering separately, and checked outer pane alignment without verifying whether empty and loaded states reserved the same fixed internal chrome.
**Rule**: For async pane content, keep loading, empty, and loaded states on the same owning chrome. Do not replace a titled/scrollable component with a raw placeholder during loading. Decide whether the header belongs to the pane chrome or the content before reserving viewport rows; if content already includes its own title, do not spend another pane row on a duplicate. Add layout tests that assert stable chrome across state transitions.
**Applied**: New-session browse panes, list/detail overlays, placeholder-to-loaded transitions, split-pane overlays, preview/detail panes.

### Overlay Alignment Must Use Absolute Edges
**Mistake**: I added a new upper-right toast flow without checking whether the toast stack was actually right-bound, and fixed stacked toast alignment by shifting narrower toasts relative to wider ones, which still let the first toast drift left from the shared right edge once multiple toast widths were involved.
**Pattern**: I verified interaction behavior but stopped short of validating the visual contract of adjacent overlay elements, and satisfied local alignment assertions without checking that every line terminated on the same final screen column.
**Rule**: When adding or moving overlays, verify alignment and background ownership explicitly in the rendered view. For top-right overlay stacks, test and implement against the final shared right edge of every rendered line, not just relative ordering between columns. Add layout tests for final placement.
**Applied**: Toast stacks, modal dialogs, callouts, notification piles, right-anchored overlays.

### Scroll Bugs Need Rendered-Pane Proof
**Mistake**: I treated a settings scroll report as a cursor/focus bookkeeping bug because the model state changed, but did not prove that the rendered main pane visibly changed under the exact focused input path the user described.
**Pattern**: I validated selection indices and viewport offsets without comparing the rendered pane before and after the scroll event.
**Rule**: For TUI scroll bugs, verify the owning pane's rendered output changes under the exact focused input path being fixed; cursor movement or `YOffset` changes alone are not enough evidence.
**Applied**: Settings-page wheel scrolling, split-pane focus routing, any viewport-backed TUI surface where model state can change without an obvious visual delta.

### Viewport Scroll State Must Clamp And Pace
**Mistake**: I let the settings page carry stale viewport offsets without clamping to the current content height, and focused only on edge no-ops while missing that Apple trackpad smooth scrolling emits high-frequency same-direction ticks during normal motion. I also rebuilt settings view state for wheel ticks that could not move at top/bottom edges, making overshoot floods feel sticky and delaying later inputs.
**Pattern**: I treated viewport content refresh as enough state management, optimized correctness of each tick but not throughput of an entire wheel burst, and treated edge no-ops as harmless because final state was unchanged.
**Rule**: Any TUI viewport that rebuilds content dynamically must clamp `YOffset` after sizing/content changes and before applying wheel deltas. For high-frequency wheel input, pace same-direction bursts to a sane frame rate and reuse cached document state when rendered selection/content has not changed. No-op wheel events at the boundary must return immediately so overshoot floods cannot starve later input. Keep scrolling viewport-driven with an explicit line delta; update selection from the resulting visible range; ignore wheel ticks that do not change the viewport offset at the edge.
**Applied**: Settings page wheel handling, detail panes with rebuilt content, viewport-backed lists, any Bubble Tea mouse-wheel path where repeated edge ticks or trackpad momentum can accumulate.

## Settings UX

### Settings Focused State Must Stay Visible And Non-Repetitive
**Mistake**: I let settings warnings repeat the same per-phase harness failure four times and let the sticky detail pane consume so much of the main column that wheel scrolling could leave the focused field offscreen.
**Pattern**: I preserved raw diagnostic granularity and a fixed detail-pane budget instead of shaping both around what the operator can actually scan in the visible viewport.
**Rule**: On settings surfaces, collapse identical multi-phase failures into one grouped message and ensure sticky detail chrome never leaves the focused field effectively hidden from the scrollable region.
**Applied**: Harness routing warnings, grouped settings diagnostics, sticky detail panes, any scrollable inspector UI with a separate focused-details panel.

### Settings Warning Copy Must Stay Terse
**Mistake**: I reused full harness remediation copy for settings footer and section warnings, which turned one missing harness into long repetitive error text and exposed irrelevant fallback/tooling noise.
**Pattern**: I optimized for reusing existing human-readable strings instead of matching the information density each settings surface actually needs.
**Rule**: When surfacing configuration problems inside Settings, keep the footer to a short summary, keep section errors to short phase-scoped detail, and avoid listing unrelated harness/tool warnings.
**Applied**: Harness routing warnings, settings-page footers, per-section validation copy, any TUI surface where detailed remediation belongs in the selected section rather than the global chrome.

## Footer & CTA Design

### Footer Layout Needs An Owning Container And Explicit Insets
**Mistake**: I moved the ready-to-plan CTA to the bottom but treated the label and card as separate right-aligned blocks, which let the label float away from the box with no explicit right or bottom breathing room. I also fixed footer hint priority only for widths where the full leading action label still fit, so the delete action could still disappear on narrower footers.
**Pattern**: I improved placement without asserting the spatial relationship between adjacent elements, and verified the logical hint list without testing the real failure mode where the action itself had to truncate.
**Rule**: When moving a footer or CTA block, render the label and box inside one owning layout container with explicit outer insets. Add tests that assert left/right inset plus bottom padding. For one-line TUI footers, test both widths where the full action label fits after dropping metadata and widths where the action label itself must truncate; the highest-priority local action must remain visible in both cases.
**Applied**: Status bars, sticky footers, key-hint rows, action cards, helper callouts, any single-line action surface that competes with right-aligned metadata.

### Verify The Exact State Before Fixing Footer Hints
**Mistake**: I fixed delete-hint rendering for task-session drilldown states without first proving the user was actually in the task pane; the reported footer was still the sessions-sidebar state where delete availability was computed differently.
**Pattern**: I matched the words "session focused" to an internal state I already knew instead of reproducing the exact footer text and navigation state the user reported.
**Rule**: When a TUI bug report includes observed footer text or pane hints, reproduce that exact rendered state first and map it back to the owning mode/focus variables before changing rendering logic or hint priority.
**Applied**: Main-page footer hints, sidebar/content focus bugs, mode-specific keybind rows, any TUI behavior where similar states render different controls.

### Remove Obsolete Chrome When Simplifying
**Mistake**: After fixing the detached footer card, I kept a `Next step` heading even though the cleaner design was the card alone. After removing that label, I kept negative assertions that only checked the deleted label stayed gone.
**Pattern**: I preserved explanatory UI chrome after the user clarified the affordance was self-explanatory, and treated tests added during a short-lived UI change as harmless residue.
**Rule**: When a user prefers the simpler presentation, remove non-essential labels. When a UI element or behavior is removed, delete tests that exist only to defend that removed detail; keep only coverage for surviving behavior.
**Applied**: Footer CTAs, callout headings, helper captions, TUI labels, short-lived UX experiments, any feature rollback or simplification.

## Work Item & Session UX

### Overview Must Match Selection And Respect Granularity
**Mistake**: I reacted to missing ticket context by pasting source metadata into the overview itself, even though multi-ticket work items make labels ambiguous at the overview level. Separately, I treated a session overview tweak as isolated copy work and missed that the selection screen was already showing ticket context the overview still dropped.
**Pattern**: I fixed information gaps without first separating which facts belong to the whole work item versus individual source tickets, and changed one UI surface without comparing the information contract of the adjacent surface that hands off into it.
**Rule**: When a work item aggregates multiple source objects, keep the overview limited to whole-item context and move ticket-level metadata into a dedicated detail surface. When a ticket or work item appears in both selection and overview screens, compare the metadata shown on both surfaces and carry forward the context users need after the handoff.
**Applied**: Work-item overviews, task-pane synthetic rows, browse-to-task handoffs, TUI handoff views, summary/detail panes.

### Duplicate-Flow UX Needs User Choice
**Mistake**: I implemented duplicate work-item handling as a hard redirect to the existing item after fixing the dedup bug, even though the user wanted a choice between canceling, opening the duplicate, or proceeding.
**Pattern**: I stopped at the first correct backend guard instead of re-checking whether the surrounding interaction matched the user's intended workflow.
**Rule**: When a user corrects a workflow from automatic behavior to user choice, replace the hard-coded branch with an explicit decision surface and test each available action.
**Applied**: Duplicate detection prompts, conflict-resolution dialogs, overwrite flows, any TUI action that can validly continue in more than one user-directed way.

### Sidebar Labels Must Lead With The Distinguishing Ref
**Mistake**: I let provider-specific raw external ID prefixes like `gh:issue:` lead the visible session label in the sidebar, which made many adjacent nodes start with the same low-signal text and slowed scanning.
**Pattern**: I preserved storage-oriented identifiers in a user-facing list instead of reordering the information around what operators need to distinguish first.
**Rule**: In session/sidebar lists, strip transport or adapter prefixes from the primary visible ref and move provider context later; the first visible token should be the part users differentiate on, not the shared adapter plumbing.
**Applied**: Session sidebar nodes, task-sidebar overview rows, search/result list titles, any TUI list that surfaces external work-item identifiers.

## Product Terminology

### Terminology Cutovers Must Be Complete
**Mistake**: I changed the visible status-bar label from "runs" to "tasks" but stopped before renaming the internal sidebar symbols and related tests. I also reused the obsolete phrase "Repository agent sessions" in planning-state copy after the product renamed that surface to repository tasks.
**Pattern**: I treated terminology changes as one-string tweaks instead of full cutovers through the owning subsystem, and updated flow behavior without re-checking exact user-facing terminology against current product language.
**Rule**: When product terminology changes, search the entire subsystem for user-facing labels, internal symbol names, test names, and helper APIs using the old term, then cut them over together. Verify current canonical product terms in the owning UI and tests; reject historical labels if the surface has already been renamed.
**Applied**: UI terminology updates, navigation labels, symbol renames, planning-state copy, status messages, onboarding copy, any refactor where product language becomes part of the code structure.

## Agent Orchestration

### Progressive Agent Writes Need An Explicit Done Boundary
**Mistake**: I treated the first write to the planning draft as completion even though the planning prompt explicitly told the agent to keep updating that file as it worked.
**Pattern**: I used artifact existence as a completion signal for an interactive agent flow, which let an intermediate valid draft short-circuit the run and abort the agent before it finished refining its output.
**Rule**: When an agent is instructed to update a file progressively, finalize only on the agent turn's explicit completion signal and treat intermediate file writes as provisional state unless the protocol says otherwise.
**Applied**: Planning-draft orchestration, live agent revisions, any workflow where a file is incrementally rewritten during a still-running session.

## Third-Party Integrations

### Verify Current CLI Surface Before Planning Integrations
**Mistake**: I planned the Sentry CLI integration around stale `sentry-cli` assumptions instead of first confirming the current binary, commands, and documented auth surfaces.
**Pattern**: I treated historical CLI naming and storage details as a stable contract, which pushed the plan toward brittle credential scraping rather than the documented command surface.
**Rule**: For third-party CLI integrations, verify the current install command, login/status commands, and documented auth interfaces before designing fallbacks; prefer documented commands over private credential storage formats.
**Applied**: Sentry, GitHub, GitLab, any provider integration that relies on an external CLI for authentication or API access.

## Communication & Behavior

### Trust User Corrections Over Prior Implementation
**Mistake**: I left workspace initialization as a scan-and-warn flow for plain git clones even though the expected behavior was to git-work initialize repos inside a new workspace.
**Pattern**: I trusted prior implementation/docs over the user's explicit correction about the actual required outcome.
**Rule**: When a user corrects runtime behavior, re-check the full action path against the user expectation and convert any warning-only path into the required state-changing behavior if that is what the product promises.
**Applied**: Workspace initialization flows, setup wizards, preflight checks, any UI path that describes side effects but currently only reports state.
