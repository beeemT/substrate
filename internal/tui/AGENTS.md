# AGENTS

## Bubble Tea and Lip Gloss Layout
- Treat `lipgloss.Style.Width(...)` and `Height(...)` as the intended pane box, not as overflow protection. Child content must already be wrapped, truncated, clipped, or padded to the pane's inner width and height before the parent style renders it.
- Before sizing nested content, subtract the parent style frame size (`border + padding`, and margins if used) from the allocated pane size. Use the remaining inner width and height for `viewport`, `list`, `textarea`, wrapped text, and any child renderer.
- For viewport-backed content, set the viewport width to the pane's inner content width. Set the viewport height to the remaining rows after every reserved row is accounted for: titles, dividers, metadata, tabs, repo headers, footers, hints, and any other fixed chrome.
- If optional rows can appear or disappear at runtime, recompute viewport height when those inputs change. Do not assume a fixed reservation if metadata, hints, or status rows are conditional.
- Any dynamic line that can outgrow the available width must be wrapped or truncated before rendering. Prefer ANSI-aware helpers so styled content does not leak escape sequences, overflow, or mis-measure width.
- When a pane renders multi-line entry blocks, cards, or list rows, keep clipping stable at block boundaries when possible. Do not slice a selected item mid-block unless that behavior is explicitly intended.
- When composing body panes with a footer or status bar, reserve the footer rows in layout math first, then ensure the body panes render to exactly the remaining height so the bottom pane border lands directly above the footer.
- Empty, loading, and populated states for the same pane **must** keep identical outer chrome (headers, titles, borders, padding). When async data changes the body later, recompute inner list/viewport sizing as needed, but keep overflow clipped or scrollable inside the pane instead of letting the parent box grow and reshuffle sibling panes.

## Overlay and Modal Styling
- Overlays and modals use **transparent backgrounds**. Do not add `Background()` or `BorderBackground()` to
  `OverlayFrame`, `OverlayFrameFocused`, `OverlayPane`, or `OverlayPaneFocused` styles, nor to any
  per-component styles inside an overlay. The terminal's own background shows through everywhere.
- **Why**: lipgloss renders a `Width()`-constrained style by padding every line to the full width with the
  background colour. This paints entire terminal lines with the background colour — not just the styled
  content. When the cursor advances, Bubble Tea may issue `\x1b[K]` (erase-to-EOL) with the background
  colour still active, painting the cleared cells with that colour. **This applies everywhere in the TUI,
  not only to overlays**: any `Background()` or `BorderBackground()` set on a `Width()`-constrained style
  inside a viewport will cause the same bleed on scroll. Keep all viewport content background-free.
- **`Background()` vs `BorderBackground()`**: in lipgloss v1, `Background(color)` applies to the content
  area and padding only. Border characters (`╭─╮╰╯│`) require `BorderBackground(color)`. Both must be set
  if a style ever needs to colour behind its border characters. This distinction is moot for transparent
  overlays but matters if a future design requires a coloured background on a bordered component.
- **ANSI-reset propagation**: `Background()` set on an outer style is cleared by any inner `\x1b[0m]` emitted
  by a nested component (`list.Model`, `viewport.Model`, nested lipgloss renders). Every segment that must
  carry a colour must have that colour set on its own style — inheritance across a reset does not work.
  This applies to separator columns, hints rows, and any other multi-segment join.
- **Inter-pane separators**: the separator column in `RenderSplitOverlayBody` is a plain repeated space:
  `strings.TrimSuffix(strings.Repeat(" \n", h), "\n")`. Do not switch it to a lipgloss-styled cell
  unless the whole split body also moves to an explicit background.
- **Hints rows**: content-pane views expose hints via their `KeybindHints()` method. The main status
  bar renders them through `app.go:currentHints()`. Do not render inline hint bars in content views.
  Overlays and modals that replace the entire screen keep their own hint rendering.

## Overlay Alignment
- For top-right overlay stacks (toasts, notifications), test and implement against the **final shared right edge** of every rendered line, not just relative ordering between columns inside the stack. Every line must terminate on the same screen column.
- When adding or moving overlays, verify alignment and background ownership explicitly in the rendered view. Add layout tests for the final placement rather than assuming container styling propagates.

## State Transitions
- A state transition that introduces or removes a title row, header, or fixed chrome row is a layout bug.
- Decide whether the header belongs to the pane chrome or the pane content **before** reserving viewport rows. If the content already includes its own title, do not spend another pane row on a duplicate header. Test the loaded state that makes the duplicate visible.
- When a pane has a sticky detail panel alongside a scrollable list, the detail chrome **must not** consume so much of the column that the focused field can scroll offscreen. Assert the focused field remains reachable within the scrollable region.

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

## Scroll Bug Diagnosis
- For TUI scroll bugs, **prove the rendered pane visibly changes** under the exact focused input path being fixed. Cursor movement or `YOffset` changes alone are not evidence. Compare the rendered output before and after the scroll event.
- When a bug report includes observed footer text or pane hints, **reproduce that exact rendered state first** and map it back to the owning mode/focus variables before changing rendering logic.

## Footer & Status Bar
- Footer action labels **must** remain visible at narrow widths. Test widths where metadata drops first, and widths where the action label itself must truncate. The highest-priority local action **must** survive both cases.
- When moving a footer or CTA block, render the label and box inside **one owning layout container** with explicit outer insets. Do not float label and card as separate aligned blocks.
- When simplifying footer chrome (removing headings, labels), also **delete tests that only defend the removed element**. Keep only coverage for surviving behavior.

## Settings Surfaces
- Collapse identical multi-phase failures into **one grouped message**. Do not repeat the same per-phase harness failure multiple times.
- Keep settings footer to a **short summary** and section errors to short phase-scoped detail. Do not list unrelated harness/tool warnings from other sections.

## List & Sidebar Labels
- In session/sidebar lists, **strip transport or adapter prefixes** (e.g. `gh:issue:`) from the primary visible ref. The first visible token **must** be the part users differentiate on, not the shared adapter plumbing. Move provider context later in the node.
- When a ticket or work item appears in both a selection/details screen and an overview screen, **compare the metadata shown on both surfaces** and carry forward the context users need after the handoff. An information gap in the downstream surface is a bug.

## Conflict & Duplicate Flows
- When a duplicate or conflict is detected, **provide explicit user choice** (cancel, open existing, proceed). Do not hard-code a single redirect path. Test each available action.
