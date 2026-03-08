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