# Lessons Learned

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