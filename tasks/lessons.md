# Lessons Learned

## 2026-03-10 - Settings Focused State Must Stay Visible And Non-Repetitive

**Mistake**: I let settings warnings repeat the same per-phase harness failure four times and let the sticky detail pane consume so much of the main column that wheel scrolling could leave the focused field offscreen.
**Pattern**: I preserved raw diagnostic granularity and a fixed detail-pane budget instead of shaping both around what the operator can actually scan in the visible viewport.
**Rule**: On settings surfaces, collapse identical multi-phase failures into one grouped message and ensure sticky detail chrome never leaves the focused field effectively hidden from the scrollable region.
**Applied**: Harness routing warnings, grouped settings diagnostics, sticky detail panes, and any scrollable inspector UI that keeps a separate focused-details panel on screen.


## 2026-03-10 - Settings Scroll State Must Clamp To Viewport Bounds

**Mistake**: I let the settings page carry stale viewport offsets without clamping them to the current content height and ignored direct mouse-wheel scrolling semantics.
**Pattern**: I treated viewport content refresh as enough state management and forgot that wrapped content, resized panes, and wheel input all need the scroll offset normalized against the current visible range.
**Rule**: Any TUI viewport that rebuilds content dynamically must clamp `YOffset` after sizing/content changes and before applying wheel deltas so reverse scrolling responds immediately at the real edge.
**Applied**: Settings overlay viewports, detail panes with rebuilt content, and any Bubble viewport that keeps scroll state across layout/content recomputation.


## 2026-03-10 - Settings Warning Copy Must Stay Terse

**Mistake**: I reused full harness remediation copy for the settings footer and section warnings, which turned one missing harness into long repetitive error text and exposed irrelevant fallback/tooling noise.
**Pattern**: I optimized for reusing existing human-readable strings instead of matching the information density each settings surface actually needs.
**Rule**: When surfacing configuration problems inside Settings, keep the footer to a short summary, keep section errors to short phase-scoped detail, and avoid listing unrelated harness/tool warnings just because other harness sections exist.
**Applied**: Harness routing warnings, settings-page footers, per-section validation copy, and any TUI surface where detailed remediation belongs in the selected section rather than the global chrome.


## 2026-03-10 - Duplicate-Flow UX Corrections

**Mistake**: I implemented duplicate work-item handling as a hard redirect to the existing item after fixing the dedup bug, even though the user actually wanted a choice between canceling, opening the duplicate, or proceeding with the duplicate work item.
**Pattern**: I stopped at the first correct backend guard instead of re-checking whether the surrounding interaction still matched the user’s intended workflow.
**Rule**: When a user corrects a workflow from automatic behavior to user choice, replace the hard-coded branch with an explicit decision surface and test each available action instead of preserving the old one-path UX.
**Applied**: Duplicate detection prompts, conflict-resolution dialogs, overwrite flows, and any TUI action that can validly continue in more than one user-directed way.


## 2026-03-10 - Overlay Visual Consistency

**Mistake**: I added a new upper-right toast/modal flow without checking whether the toast stack was actually right-bound or whether the modal content rows inherited the same background as the overlay frame.
**Pattern**: I verified the interaction behavior but stopped short of validating the visual contract of adjacent overlay elements.
**Rule**: When adding or moving overlays, verify alignment and background ownership explicitly in the rendered view and add layout tests for the final placement rather than assuming container styling propagates correctly.
**Applied**: Toast stacks, modal dialogs, callouts, and any TUI overlay composed from nested lipgloss blocks.


## 2026-03-10 - Right-Edge Alignment Means Shared Edge, Not Relative Shift

**Mistake**: I fixed stacked toast alignment by shifting narrower transient toasts relative to wider transient toasts, which still let the first toast drift left from the shared right edge once multiple toast widths were involved.
**Pattern**: I satisfied a local alignment assertion inside the stack without checking that every toast line actually terminated on the same final screen column.
**Rule**: For top-right overlay stacks, test and implement against the final shared right edge of every rendered line, not just relative ordering between toast columns inside the stack.
**Applied**: Toast stacks, notification piles, right-anchored callouts, and any overlay where multiple independently sized blocks must share one visual edge.


## 2026-03-10 - Overview Granularity vs Source Detail

**Mistake**: I reacted to missing ticket context by pasting source metadata into the overview itself, even though multi-ticket work items make labels and other source facts ambiguous at the overview level.
**Pattern**: I fixed an information gap without first separating which facts belong to the whole work item versus which facts belong to individual source tickets.
**Rule**: When a work item aggregates multiple source objects, keep the overview limited to whole-item context and move ticket-level metadata into a dedicated detail surface instead of flattening it into an inline summary.
**Applied**: Work-item overviews, task-pane synthetic rows, browse-to-task handoffs, and any UI that summarizes many source records into one operating unit.

## 2026-03-10 - Overview vs Selection Parity

**Mistake**: I treated the session overview tweak as isolated copy/spacing work and missed that the new-session selection screen was already showing ticket context the overview still dropped.
**Pattern**: I changed one UI surface without comparing the information contract of the adjacent surface that hands off into it.
**Rule**: When a ticket or work item appears in both selection/details and overview screens, compare the metadata shown on both surfaces and carry forward the context users need after the handoff instead of stopping at the first requested visual tweak.
**Applied**: TUI handoff views, browse-to-overview transitions, summary/detail panes, and any workflow where one screen previews richer metadata than the next screen retains.

## 2026-03-08 - Communication

**Mistake**: I left workspace initialization as a scan-and-warn flow for plain git clones even though the expected behavior was to git-work initialize repos inside a new workspace.
**Pattern**: I trusted prior implementation/docs over the user's explicit correction about the actual required outcome.
**Rule**: When a user corrects runtime behavior, re-check the full action path against the user expectation and convert any warning-only path into the required state-changing behavior if that is what the product promises.
**Applied**: Workspace initialization flows, setup wizards, preflight checks, and any UI path that describes side effects but currently only reports state.


## 2026-03-08 - TUI Layout Math

**Mistake**: I treated Lip Gloss `Width` values as pure content width in the new-session overlay, which caused padded/bordered panes and the modal itself to overflow and wrap unexpectedly.
**Pattern**: I changed visual layout code without validating the library's actual box-model semantics under constrained terminal sizes.
**Rule**: For every non-trivial TUI layout, calculate with the rendering library's frame sizes explicitly and add tests that assert rendered width and height stay within the requested viewport across narrow and normal terminal sizes.
**Applied**: Overlays, split panes, sticky footers/headers, scrollable viewports, and any TUI component that combines borders, padding, and nested boxes.

## 2026-03-09 - Footer Callout Layout

**Mistake**: I moved the ready-to-plan CTA to the bottom but treated the label and card as separate right-aligned blocks, which let the label float away from the box and left no explicit right or bottom breathing room.
**Pattern**: I improved a TUI component's placement without asserting the spatial relationship between adjacent elements inside the pane.
**Rule**: When moving a footer or CTA block, render the label and box inside one owning layout container, give that container explicit outer insets, and add tests that assert left/right inset plus bottom padding in the rendered output.
**Applied**: Ready-to-plan footers, action cards, helper callouts, and any bottom-anchored pane content that mixes headings with bordered boxes.

## 2026-03-09 - Terminology Cutovers

**Mistake**: I changed the visible status-bar label from runs to tasks but stopped before renaming the internal sidebar symbols and related tests in the same feature area.
**Pattern**: I treated a terminology change as a one-string tweak instead of a full cutover through the owning subsystem.
**Rule**: When product terminology changes, search the entire subsystem for user-facing labels, internal symbol names, test names, and helper APIs using the old term, then cut them over together before considering the work complete.
**Applied**: UI terminology updates, navigation labels, symbol renames, and any refactor where product language becomes part of the code structure.

## 2026-03-10 - Footer CTA Minimalism

**Mistake**: After fixing the detached footer card, I kept a `Next step` heading even though the cleaner design was the card alone.
**Pattern**: I preserved explanatory UI chrome after the user clarified that the affordance itself was already self-explanatory.
**Rule**: When a user prefers the simpler presentation, remove non-essential labels instead of defending the extra structure; then update tests to assert the obsolete label stays gone.
**Applied**: Footer CTAs, callout headings, helper captions, and any UI element where the action card already conveys the needed meaning.

## 2026-03-10 - Remove Obsolete Tests With Removed UI

**Mistake**: After removing the `Next step` label, I kept negative assertions that only checked the deleted label stayed gone.
**Pattern**: I treated tests added during a short-lived UI change as harmless residue instead of pruning them once the feature itself disappeared.
**Rule**: When a UI element or behavior is removed, delete tests that exist only to defend that removed detail; keep only coverage for the surviving behavior.
**Applied**: TUI labels, helper captions, short-lived UX experiments, and any feature rollback or simplification where the old behavior no longer exists.