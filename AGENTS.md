# AGENTS

## Lip Gloss and Viewports
- Treat `lipgloss.Style.Width(...)` and `Height(...)` as the final rendered box size, not raw content size.
- Before sizing nested content, subtract the parent style frame size (`border + padding`, and margins if used). Use the remaining inner width/height for `viewport`, `list`, `textarea`, and wrapped text.
- For viewport content, set the viewport width to the pane's inner content width. If the pane also renders a title, divider, or footer, subtract those rows from the viewport height too.
- Any dynamic line that can outgrow the available width must be wrapped or truncated before rendering. Prefer ANSI-aware helpers so styled content does not leak escape sequences or overflow.
- For every non-trivial TUI layout change, add tests that assert rendered line width stays within the requested terminal width and rendered line count stays within the requested terminal height, including narrow-size cases.
