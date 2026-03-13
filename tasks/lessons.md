# Lessons Learned

## 2026-03-11 - Verify Current CLI Surface Before Planning Integrations

**Mistake**: I planned the Sentry CLI integration around stale `sentry-cli` assumptions (`sentry-cli login`, private file parsing) instead of first confirming the current binary, commands, and documented auth surfaces.
**Pattern**: I treated historical CLI naming and storage details as a stable contract, which pushed the plan toward brittle credential scraping rather than the documented command surface.
**Rule**: For third-party CLI integrations, verify the current install command, login/status commands, and documented auth interfaces before designing fallbacks; prefer documented commands over private credential storage formats when the tool does not expose tokens directly.
**Applied**: Sentry, GitHub, GitLab, and any provider integration that relies on an external CLI for authentication or API access.

## 2026-03-12 - Scroll Bugs Need Rendered-Pane Proof

**Mistake**: I treated the settings scroll report as a cursor/focus bookkeeping bug because the model state changed, but I did not prove that the rendered main pane visibly changed in the real scroll path the user described.
**Pattern**: I validated selection indices and viewport offsets without comparing the rendered pane before and after the scroll event, so I fixed an internally consistent state transition that still missed the user-visible failure mode.
**Rule**: For TUI scroll bugs, verify the owning pane's rendered output changes under the exact focused input path being fixed; cursor movement or `YOffset` changes alone are not enough evidence.
**Applied**: Settings-page wheel scrolling, split-pane focus routing, and any viewport-backed TUI surface where model state can change without an obvious visual delta.

## 2026-03-10 - Loading States Must Keep The Same Pane Chrome

**Mistake**: I stabilized the overlay layout math but still let the browse pane switch from a raw loading string to the list component after items arrived, which changed the pane’s internal chrome and made the loaded state look resized.
**Pattern**: I verified geometry structs and loaded rendering separately, but I did not compare the actual loading render against the loaded render that users transition through.
**Rule**: For async pane content, keep loading, empty, and loaded states on the same owning chrome. Do not replace a titled/scrollable component with a raw placeholder during loading; render the same pane header and scroll container in every state.
**Applied**: New-session browse panes, list/detail overlays, and any TUI surface that swaps between loading placeholders and interactive content.

## 2026-03-10 - Stable Overlays Need Stable Internal Chrome

**Mistake**: I treated the new-session detail-row mismatch as a pure viewport-height problem and then as a full-height fix, but I still let loaded detail content introduce a title row that empty state did not reserve.
**Pattern**: I checked outer pane alignment without verifying whether empty and loaded states reserved the same fixed internal chrome before the scrollable content started.
**Rule**: For overlay panes that switch between placeholder and loaded detail content, keep fixed headers stable across both states and move variable content into the scrollable viewport instead of letting loaded content steal rows from the body.
**Applied**: New-session details panes, placeholder-to-loaded overlays, and any split TUI surface where content appears after async loading.

## 2026-03-10 - Pane Height Must Follow The Owning Header

**Mistake**: I fixed the new-session overlay by changing shared titled-viewport math, but I did not re-check whether that overlay’s detail content already rendered its own header, so the details column still lost a visible row after tickets loaded.
**Pattern**: I optimized for a shared layout formula instead of verifying which layer actually owned the title row in the rendered pane.
**Rule**: For split-pane overlays, decide whether the header belongs to the pane chrome or the pane content before reserving viewport rows; if the content already includes its own title, do not spend another pane row on a duplicate header, and test the loaded state that makes the duplicate visible.
**Applied**: New-session detail panes, preview/detail overlays, and any TUI viewport where both container chrome and inner content can introduce headings.

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


## 2026-03-10 - Wheel Scroll Feel Needs Viewport Motion, Not Selection Nudges

**Mistake**: I treated the settings wheel-scroll stickiness as a pure selection-visibility problem and replaced viewport scrolling with single-step selection movement, which made reverse scrolling at overshoot edges still feel wrong even though the selected item stayed visible.
**Pattern**: I optimized around keeping the highlight on screen instead of preserving the operator’s expectation that wheel input moves the viewport by a meaningful amount and ignores stale edge ticks.
**Rule**: For wheel-driven TUI navigation, keep scrolling viewport-driven with an explicit line delta, update selection from the resulting visible range, and ignore wheel ticks that do not change the viewport offset at the edge.
**Applied**: Settings page wheel scrolling, overshoot reversal handling, and any Bubble Tea surface where selection and viewport state both react to mouse-wheel input.

## 2026-03-10 - No-Op Wheel Ticks Must Be Cheap

**Mistake**: I fixed the visible edge behavior but still rebuilt settings viewport content and rescanned anchors for wheel ticks that could not move at the top/bottom edge, which made overshoot floods feel sticky and delayed later inputs like Esc.
**Pattern**: I treated edge no-ops as harmless because the final state was unchanged, but I ignored the throughput cost of doing full viewport/selection work for every stale wheel tick in the queue.
**Rule**: For wheel-driven TUI surfaces, detect already-clamped edge ticks before rebuilding content or syncing selection; no-op wheel events at the boundary must return immediately so overshoot floods cannot starve later input.
**Applied**: Settings page overshoot handling, viewport-backed lists, and any Bubble Tea mouse-wheel path where repeated edge ticks can accumulate in the input queue.

## 2026-03-11 - Footer Actions Need Narrow-Width Render Tests

**Mistake**: I fixed footer hint priority only for widths where the full leading action label still fit, so the main-page delete action could still disappear once the footer became narrower than the full `[d] Delete session` text.
**Pattern**: I verified the logical hint list and one moderately constrained render, but I did not test the real failure mode where the action itself had to truncate to survive.
**Rule**: For one-line TUI footers, test both widths where the full action label fits only after dropping metadata and widths where the action label itself must truncate; the highest-priority local action must remain visible in both cases.
**Applied**: Status bars, sticky footers, key-hint rows, and any single-line action surface that competes with right-aligned metadata.

## 2026-03-10 - Smooth Scroll Floods Need Pacing And Cache Reuse

**Mistake**: I focused only on edge no-ops and missed that Apple trackpad smooth scrolling can keep emitting same-direction wheel ticks during normal motion, which still rebuilt settings view state often enough to feel sticky and delay unrelated keys.
**Pattern**: I optimized correctness of each wheel tick but not the throughput of an entire wheel burst across repeated renders of mostly unchanged content.
**Rule**: For high-frequency wheel input in Bubble Tea, pace same-direction bursts to a sane frame rate and reuse cached document state whenever the rendered selection/content has not changed; otherwise inertial trackpad tails will overwhelm the TUI even when every individual update is logically correct.
**Applied**: Settings page wheel handling, viewport-backed scroll surfaces, and any TUI view that processes trackpad momentum as many discrete mouse-wheel presses.

## 2026-03-11 - Verify The Exact Main-Page State Before Fixing Footer Hints

**Mistake**: I fixed delete-hint rendering for task-session drilldown states without first proving that the user was actually in the task pane; the reported footer was still the sessions-sidebar state, where delete availability was computed differently.
**Pattern**: I matched the words "session focused" to an internal state I already knew instead of reproducing the exact footer text and navigation state the user reported.
**Rule**: When a TUI bug report includes observed footer text or pane hints, reproduce that exact rendered state first and map it back to the owning mode/focus variables before changing rendering logic or hint priority.
**Applied**: Main-page footer hints, sidebar/content focus bugs, mode-specific keybind rows, and any TUI behavior where similar states render different controls.