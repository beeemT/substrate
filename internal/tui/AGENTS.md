# AGENTS

## Bubble Tea and Lip Gloss Layout
- Treat `lipgloss.Style.Width(...)` and `Height(...)` as the intended pane box, not as overflow protection. Child content must already be wrapped, truncated, clipped, or padded to the pane's inner width and height before the parent style renders it.
- Before sizing nested content, subtract the parent style frame size (`border + padding`, and margins if used) from the allocated pane size. Use the remaining inner width and height for `viewport`, `list`, `textarea`, wrapped text, and any child renderer.
- For viewport-backed content, set the viewport width to the pane's inner content width. Set the viewport height to the remaining rows after every reserved row is accounted for: titles, dividers, metadata, tabs, repo headers, footers, hints, and any other fixed chrome.
- If optional rows can appear or disappear at runtime, recompute viewport height when those inputs change. Do not assume a fixed reservation if metadata, hints, or status rows are conditional.
- Any dynamic line that can outgrow the available width must be wrapped or truncated before rendering. Prefer ANSI-aware helpers so styled content does not leak escape sequences, overflow, or mis-measure width.
- When a pane renders multi-line entry blocks, cards, or list rows, keep clipping stable at block boundaries when possible. Do not slice a selected item mid-block unless that behavior is explicitly intended.
- When composing body panes with a footer or status bar, reserve the footer rows in layout math first, then ensure the body panes render to exactly the remaining height so the bottom pane border lands directly above the footer.

- Empty, loading, and populated states for the same pane must keep identical outer dimensions. When async data changes the body later, recompute inner list/viewport sizing as needed, but keep overflow clipped or scrollable inside the pane instead of letting the parent box grow and reshuffle sibling panes.
## TUI Layout Tests
- For every non-trivial TUI layout change, add tests that assert rendered line width stays within the requested terminal width and rendered line count stays within the requested terminal height, including narrow-size cases.
- Add coverage for session-present states, not only empty states, so dynamic headers, metadata rows, logs, and status hints cannot silently push layouts past the terminal bounds.
- When you add a clipping, truncation, or viewport-sizing helper, add targeted tests for the helper or the specific view that depends on it.
