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

## Wheel / Scroll Handling
- Every view with a scrollable `viewport.Model` or `list.Model` **must** forward `tea.MouseMsg` wheel events (`MouseButtonWheelUp`, `MouseButtonWheelDown`) to the component's `Update` method. Only forward press actions (`MouseActionPress`); ignore motion and release.
- When a view has multiple focus zones (list pane, detail pane, controls), route wheel events to the focused component only. Do not forward wheel to unfocused or non-scrollable areas.
- The bubbles `viewport` and `list` handle offset clamping and edge behaviour internally. Do **not** add custom edge detection or throttling when forwarding to these standard components.
- For views with custom content rendering (not backed by a single bubbles component), use viewport-driven scrolling: change `viewport.YOffset` via `ScrollDown`/`ScrollUp`, then sync any selection cursor to the visible range. Do **not** rebuild the rendered document inside `Update`; let `View()` render once per frame.
- Edge ticks (viewport already at top or bottom) must be O(1). Detect the edge before any content work and return immediately.
- Do **not** throttle wheel events. Apple trackpads generate many discrete events after a gesture ends; throttling makes scrolling feel sticky and laggy. Instead, make each `Update` call as cheap as possible.
- After wheel-scrolling a `list.Model` with infinite scroll or pagination, check the load-more trigger (e.g. `maybeLoadMore`) on wheel-down, same as for keyboard navigation.

### Update / View cache boundary
- Bubble Tea `Update` returns the modified model by value; `View` receives a copy and returns only a string. Cache fields mutated by pointer-receiver methods during `View()` are **discarded** after the frame. Only cache writes in `Update` survive to the next cycle.
- For views with expensive document builds: perform the rebuild **once** at the end of `Update` (where cache persists), and have `View` reuse the pre-built viewport directly. Use a dimension/content check (`vp.Width != expected || vp.Height != expected || vp.TotalLineCount() == 0`) as a fallback for first render or resize.
- The `View` fallback rebuild must use `alignSelection=false`. Selection alignment is the responsibility of `Update` (keyboard path via `syncMainViewport`, wheel path via `preparedMainViewport`). If `View` realigns, it can jump the viewport to unexpected positions because it operates on a stale copy.
- Content-key-based caching (e.g. `mainDocumentContentKey`) must include all state that affects rendered output: cursor positions, focus, editing mode, reveal-secrets, and a mutation revision counter. Invalidate the cache at every content mutation point.


## Overlay Background and ANSI Reset Hygiene
- Lipgloss background set on an outer style (`Background(overlayBg)`) is cleared the first time
  any child emits a full ANSI reset (`\x1b[0m`). Bubbles `list.Model`, `viewport.Model`, and
  nested lipgloss renders all produce resets. **Never rely on an outer `Background()` to cover
  content rendered by those components.** The background vanishes after the first `\x1b[0m`.
- **`Background()` vs `BorderBackground()`**: in lipgloss v1, `Background(color)` applies to the
  content area and padding only. Border characters (╭ ─ ╮ │ ╰ ╯) require `BorderBackground(color)`
  to carry the background. Set both on any pane style that must show the overlay colour behind its
  borders (e.g. `OverlayPane` and `OverlayPaneFocused` in `styles/theme.go`).
- **Pane styles must be self-contained**: each pane rendered by `renderOverlayPane` must carry its
  own `Background()` and `BorderBackground()` so the pane is correct regardless of what ANSI state
  preceded it. Do not rely on the outer frame's background injection for pane content.
- **Hints / keybind rows inside overlays**: use `renderOverlayHintsRow` in `views/component_helpers.go`.
  It applies `Background(overlayBg)` to every individual accent/hint segment and to the separators,
  then wraps with `Padding(0,1)` and `Width(width)`. Never pass keybind hint rows through
  `components.RenderKeyHints` with only an outer background; the resets in each piece will break it.
- **Inter-pane separators in split overlays**: use a per-row repeat of a single styled space, not
  `Width(1).Height(N).Render("")` (which emits a spurious `bg\x1b[0m` on the first row). Example:
  ```go
  sepLine := lipgloss.NewStyle().Background(overlayBg).Render(" ")
  sep := strings.TrimSuffix(strings.Repeat(sepLine+"\n", bodyHeight), "\n")
  lipgloss.JoinHorizontal(lipgloss.Top, leftPane, sep, rightPane)
  ```
  A plain `" "` loses the background after the left pane's terminating reset, creating an
  uncoloured gap that is most visible at the bottom border row.
- **General rule**: every gap, spacer, or separator that must appear with the overlay background
  **must** have `Background(overlayBg)` applied to its own style. Inheritance from an outer render
  does not survive a reset. When in doubt, use `lipgloss.NewStyle().Background(bg).Render(s)` on
  each piece rather than wrapping the joined result.