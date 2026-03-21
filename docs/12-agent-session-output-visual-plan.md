# 12 - Agent Session Output Visual Plan
<!-- docs:last-integrated-commit d3f906409cee34c0e42bc47d93cbac9d149ed72d -->

## Status

Implemented. All acceptance criteria (section 13) are met. The optional follow-ups (detail overlay, read-group compaction) remain available as future enhancements.

---

## 1. Decision summary

### Agreed product direction

- Agent-session output should stop rendering as undifferentiated white text.
- Transcript rendering should move closer to Oh My Pi's coding-agent output model: muted thinking text, structured tool cards, clearer hierarchy, and explicit truncation.
- The TUI should reuse the existing design system where it already supports the desired behavior.
- The TUI should **not** be artificially constrained by the current component set. If the current primitives are insufficient, we should extend them or add new shared primitives rather than force the transcript into the wrong shape.
- The implementation should be a clean cutover from string-only transcript rendering to structured transcript rendering. No dual long-term rendering model should remain.

### Design-system stance

The right rule is:

1. prefer existing semantic styles and primitives when they express the desired behavior cleanly
2. extend shared primitives when the behavior is close but not yet supported
3. add a new shared primitive when the new behavior is genuinely reusable and the existing primitives would become contorted
4. do **not** keep transcript rendering plain or awkward just to avoid evolving the design system

This preserves consistency without turning existing components into a design constraint.

---

## 2. Current gap

### Current pipeline

Today the session-log pipeline is structurally rich at parse time and visually poor at render time:

1. `internal/sessionlog/parse.go`
   - `ParseLine` parses structured JSON events into `sessionlog.Entry`.
   - `FormatForTranscript` immediately flattens those entries into strings.
2. `internal/tui/views/cmds.go`
   - session-log loading and tailing normalize entries into `[]string`.
3. `internal/tui/views/planning_view.go`
   - `SessionLogModel` stores and renders string buffers in a viewport.
4. `internal/tui/views/implementing_view.go`
   - `ImplementingModel` stores per-repo string buffers and renders the active buffer as plain text.

### Consequences

- event kind, tool name, tool intent, lifecycle state, question state, and uncertainty are discarded before rendering
- tool input/output/result are visually flattened into the same transcript texture as assistant text
- live planning, historical session interaction, and implementation all share a weak string-only rendering path
- long tool output overwhelms the viewport because the UI has no semantic notion of what should collapse or summarize
- the current TUI has a design system, but session logs bypass it almost completely

---

## 3. Existing assets we should leverage

### Current TUI building blocks

Existing TUI code already provides most of the shell vocabulary we want to lean on:

- `RenderHeaderBlock` for transcript headers and meta rows
- `RenderCallout` and `CalloutInnerWidth` for bordered cards and boxed content
- `renderKeyValueLine` for aligned metadata rows inside a bounded width
- `fitViewBox`, `fitViewHeight`, `ansi.Hardwrap`, and `ansi.Truncate` for clipping and wrapping
- `RenderOverlayFrame` and `ComputeSplitOverlayLayout` for future detail overlays if we later need them
- semantic styles in `internal/tui/styles/theme.go` including `Title`, `Subtitle`, `Muted`, `SectionLabel`, `SettingsText`, `Warning`, and `Active`

### Current constraints that remain mandatory

`internal/tui/AGENTS.md` still governs this work:

- compute inner width and height before rendering nested content
- reserve every fixed row explicitly in viewport math
- keep empty/loading/populated states dimensionally stable
- wrap or truncate long lines before rendering
- add bounded-rendering tests for layout changes, including narrow terminal cases
- forward wheel events only to the focused viewport

These are implementation constraints, not suggestions.

---

## 4. Target UX

### Visual goals

The transcript should make these distinctions obvious at a glance:

- thinking vs assistant text
- prompt/input vs response
- tool start vs tool output vs tool completion
- success vs running vs error vs waiting
- compact preview vs expanded detail

### Target transcript shape

Recommended transcript treatment:

- thinking text: muted, optionally italicized if that remains readable in the current palette
- assistant prose: default body text, unboxed unless the content is a special structured event
- prompt / feedback / answer blocks: labeled transcript sections with subtle framing or labels
- tool activity: boxed cards with status-aware chrome and labeled sections
- lifecycle events: concise muted rows unless they need stronger emphasis
- question / waiting states: warning-style treatment using existing semantic warning language

### Target tool-card behavior

Each tool execution should render as one grouped block rather than as several unrelated lines.

Recommended collapsed card structure:

- title/status row: tool name + short intent + running/success/error state
- args summary: compact single-line preview where possible
- output preview: first few lines only
- overflow affordance: explicit `… N more lines` marker
- hint row: `verbose` or detail affordance if implemented

Recommended expanded card structure:

- `Args`
- `Output`
- `Result`
- optional metadata rows such as duration, repo, or source session context if available

---

## 5. Oh My Pi behaviors worth mirroring

The proposal should intentionally mirror the strengths of Oh My Pi's transcript formatting:

- muted thinking text rather than same-weight body copy
- stateful tool status headers
- bordered/tinted tool execution blocks
- default preview truncation with explicit overflow markers
- compact summarization of repetitive read-heavy activity when appropriate

We should mirror those behaviors, not necessarily copy their implementation one-for-one.

---

## 6. Recommended architecture

### 6.1 Structured transcript cutover

This is the key architectural move.

The TUI should consume structured entries end-to-end:

- `sessionlog.Entry` or a small transcript-specific wrapper becomes the canonical rendering payload
- `TailSessionLogCmd` and `LoadSessionInteractionCmd` emit structured entries, not strings
- `SessionLogModel` and `ImplementingModel` store structured entries, not pre-rendered lines
- rendering happens only at the final view layer

#### Why this is required

If we keep the current flatten-then-style path, every visual improvement becomes brittle prefix parsing. That would lock the transcript into string heuristics precisely where we need event-aware rendering.

### 6.2 Shared transcript renderer

Add a dedicated transcript renderer, likely in one of these shapes:

- `internal/tui/views/session_log_render.go`
- or `internal/tui/components/session_transcript.go`

Responsibility of that renderer:

- group raw session-log entries into higher-level render blocks
- own transcript-specific wrapping and clipping rules
- return bounded text suitable for the viewport owned by the parent view
- avoid owning Bubble Tea state, focus routing, or viewport math if placed under `components/`

### 6.3 Render block model

The renderer should be able to map raw entries into block types such as:

- `PromptBlock`
- `AssistantBlock`
- `ThinkingBlock`
- `ToolBlock`
- `QuestionBlock`
- `LifecycleBlock`
- `ForemanBlock`
- `PlainFallbackBlock`

This does not have to be the public type API, but the renderer needs an equivalent internal model.

### 6.4 Tool grouping

Recommended first implementation:

- group adjacent `tool_start`, `tool_output`, and `tool_result` entries into one tool block
- preserve all captured output internally
- render a collapsed preview by default

If the bridge can surface a stable tool-call identifier later, thread it through the pipeline and replace adjacency grouping with identifier-based grouping.

---

## 7. Recommended component strategy

### Reuse what already works

Prefer existing shared primitives when they already fit:

- `RenderHeaderBlock` for transcript title and meta chrome
- `RenderCallout` for tool cards and question/warning surfaces
- `renderKeyValueLine` for metadata rows inside cards
- existing semantic text styles for labels, muted body copy, warnings, and emphasis

### Extend what is close

If current callouts or semantic styles are almost right but not quite enough, extend them rather than working around them in the view.

Likely extension points:

- transcript-specific callout variants for running/success/error/waiting cards
- transcript-specific semantic text roles in `styles.Styles`
- helper functions for bounded metadata rows or transcript section rendering

### Add a new primitive if the transcript needs one

If tool cards need behavior that `RenderCallout` cannot express cleanly, add a new shared primitive rather than overloading an existing one.

Examples of acceptable new shared primitives:

- transcript status-line renderer
- transcript section renderer
- transcript activity card that composes internal labels + truncation consistently

The bar for a new primitive is reusability and clarity, not just novelty.

---

## 8. Proposed visual language

### Text roles

Recommended text-role mapping:

- thinking: `Muted` or a new transcript-thinking role; italic if legible
- assistant prose: default text
- labels such as `Args`, `Output`, `Result`: `SectionLabel`
- prompt / feedback / answer annotations: `SettingsText` or a transcript-specific secondary label role
- warning or waiting states: `Warning`
- selected/focused transcript affordances: `Active`

### Tool-card states

Recommended visual treatment:

- running: accent or active border with lightly tinted background
- success: subdued neutral or success-adjacent border/background
- error: warning/error border with tinted error background
- waiting/question: warning styling

If the current theme lacks these semantics, add them to the design system rather than hardcoding transcript-only colors in the view.

---

## 9. Truncation and ergonomics

### Default behavior

The transcript should be readable by default and inspectable on demand.

Recommended defaults:

- tool args preview in collapsed mode: one-line summary when practical
- tool output in collapsed mode: first 4 lines
- tool output in verbose mode: first 12 lines in-pane
- long single lines: hard-truncate to available width with ellipsis
- all truncation: explicit overflow indicator such as `… N more lines`

### Interaction model

Recommended first step:

- add `[o] Verbose logs` toggle in both `SessionLogModel` and `ImplementingModel`
- collapsed mode favors summary cards
- verbose mode expands args/output within the existing viewport model

This is preferable to jumping immediately to per-card focus or detail overlays because it matches the current viewport-centric interaction model and keeps phase 1 complexity under control.

### Possible later follow-up

> **Note:** This remains an available follow-up enhancement and was intentionally deferred from the initial implementation.

If verbose mode proves too blunt, we can add a focused detail overlay using:

- `ComputeSplitOverlayLayout`
- `RenderOverlayFrame`

That should be a follow-up, not the starting point.

---

## 10. Implementation phases

### Phase 1 — Structured transcript cutover

Files likely involved:

- `internal/sessionlog/parse.go`
- `internal/tui/views/cmds.go`
- `internal/tui/views/msgs.go`
- `internal/tui/views/planning_view.go`
- `internal/tui/views/implementing_view.go`

Work:

- keep `ParseLine`
- remove `FormatForTranscript` from the live TUI path
- change session-log message payloads from `[]string` to structured transcript entries
- update live tailing and historical interaction loading to emit structured entries
- update both transcript-owning views to buffer structured entries instead of rendered lines
- delete string-only normalization helpers once cutover is complete

### Phase 2 — Shared transcript renderer

Files likely involved:

- new transcript renderer file under `internal/tui/views/` or `internal/tui/components/`
- `internal/tui/views/planning_view.go`
- `internal/tui/views/implementing_view.go`
- possibly `internal/tui/styles/theme.go`
- possibly one or more files under `internal/tui/components/`

Work:

- build grouped render blocks from structured entries
- render tool executions as cards
- render thinking/input/lifecycle/question states distinctly
- reuse one renderer for planning/history and implementation views
- choose whether to extend `RenderCallout` or introduce a new shared primitive based on what the renderer actually needs

### Phase 3 — Truncation and interaction polish

Files likely involved:

- transcript renderer file(s)
- `internal/tui/views/planning_view.go`
- `internal/tui/views/implementing_view.go`
- `internal/tui/views/overlay_help.go` if key hints change

Work:

- add collapsed vs verbose transcript mode
- add explicit truncation affordances
- optionally compact adjacent read-heavy activity into a grouped summary block
- preserve current pause/unpause behavior in planning transcripts

### Phase 4 — Design-system polish and cleanup

Files likely involved:

- `internal/tui/styles/theme.go`
- any new or extended shared transcript components
- renderer tests and TUI layout tests

Work:

- add or refine semantic transcript theme roles if needed
- remove obsolete transcript string formatting assumptions
- tighten any callout/component APIs that were temporarily stretched during the cutover
- ensure the final component/API surface is coherent rather than transcript-specific glue

---

## 11. Concrete TODO

### Phase 1 TODO — Data-path cutover

- [x] Replace `SessionLogLinesMsg` string payloads with structured transcript entries.
- [x] Update `TailSessionLogCmd` to emit parsed structured entries for live logs.
- [x] Update `LoadSessionInteractionCmd` to emit structured entries for historical logs.
- [x] Remove `FormatForTranscript` from the live session-log rendering path.
- [x] Update `SessionLogModel` to store structured entries rather than flattened strings.
- [x] Update `ImplementingModel` to store per-repo structured entries rather than flattened strings.

### Phase 2 TODO — Shared transcript rendering

- [x] Add a shared transcript renderer that accepts a bounded width and returns bounded transcript output.
- [x] Introduce internal block grouping for assistant, thinking, tool, lifecycle, and question-style transcript events.
- [x] Group adjacent tool start/output/result entries into one tool card.
- [x] Preserve multiline tool output internally rather than flattening it during parsing.
- [x] Render tool cards with labeled sections and state-aware chrome.
- [x] Render thinking text distinctly from assistant text.

### Phase 3 TODO — Design-system evolution

- [x] Audit whether `RenderCallout` can express the needed transcript card states cleanly.
- [x] If yes, extend existing callout variants or styles rather than reimplementing card chrome in a view.
- [x] If no, add a new shared transcript-oriented primitive under `internal/tui/components/`.
- [x] Add transcript-specific semantic theme roles in `internal/tui/styles/` if existing roles are insufficient.
- [x] Keep semantic ownership in `styles/` rather than hardcoding transcript colors in view code.

### Phase 4 TODO — Truncation and operator ergonomics

- [x] Add a verbose transcript toggle shared by planning and implementation transcript views.
- [x] Truncate tool output previews by default and render explicit overflow markers.
- [x] Ensure long single lines wrap or truncate to the real inner width.
- [x] Evaluate whether repetitive read activity should collapse into a grouped summary block.
- [x] Leave detail-overlay work out of the initial cut unless verbose mode proves insufficient.

### Phase 5 TODO — Verification and cleanup

- [x] Add renderer unit tests for grouping, truncation, and narrow-width output.
- [x] Add or update view tests for planning and implementation transcript sizing.
- [x] Verify width and height remain bounded in empty, loading, and populated transcript states.
- [x] Remove any obsolete helpers, tests, or comments that describe transcript rendering as string-only.
- [x] Confirm both live planning and historical session interaction use the same canonical transcript renderer.

---

## 12. Autonomous validation I can perform

These are the checks I can execute during implementation without needing more product input.

### Parser and transcript-shape validation

- add or update tests covering structured session-log parsing and transcript grouping
- verify tool output still preserves multiline content after the string-path cutover
- verify the transcript renderer uses structured entries for both live and historical paths

### Renderer validation

- add renderer-focused tests for:
  - grouped tool cards
  - thinking text styling decisions
  - output truncation markers
  - narrow-width wrapping/truncation behavior
  - deterministic render output for the same entry sequence

### TUI layout validation

- extend existing view tests to verify transcript panes stay within width and height budgets
- verify empty, loading, and populated transcript states preserve stable outer dimensions
- verify verbose-mode toggling does not overflow the pane or break reserved-row accounting
- verify wheel behavior still targets the focused viewport only

### Focused commands likely to be relevant during implementation

- `go test ./internal/sessionlog ./internal/tui/components ./internal/tui/views`
- `go test ./internal/tui/...`
- any additional targeted test package that ends up owning the transcript renderer

If transcript behavior spans both parsing and views after the refactor, run both targeted packages rather than relying on only one suite.

---

## 13. Acceptance criteria

> **All 13 acceptance criteria below are met as of the initial implementation.**

The work is done when all of the following are true:

1. Live planning, historical session interaction, and per-repo implementation transcripts all render from structured session-log entries rather than pre-flattened strings.
2. `FormatForTranscript` is no longer part of the live TUI rendering path.
3. Tool execution is visibly distinct from assistant prose and lifecycle text.
4. Thinking text is visually muted relative to assistant output.
5. Tool start/output/result for a single execution render as one grouped transcript block.
6. Tool cards show clear state treatment for at least running, success, and error states.
7. Tool output is truncated by default with explicit overflow affordances rather than silent clipping.
8. Planning and implementation transcript views share one canonical renderer rather than maintaining divergent formatting logic.
9. The implementation reuses existing design-system primitives where they fit, but extends or adds shared primitives where needed instead of forcing the wrong abstraction.
10. No transcript view exceeds its allocated width or height in tested layouts, including narrow terminal cases.
11. Empty, loading, and populated transcript states keep stable pane dimensions.
12. Focus and wheel behavior remain correct for the owning viewport after the transcript redesign.
13. Any newly introduced transcript styles live in the design system rather than as raw ad hoc colors in a view.

---

## 14. Recommended implementation order

1. structured transcript cutover _(complete)_
2. shared renderer introduction _(complete)_
3. tool-card styling and grouped rendering _(complete)_
4. truncation + verbose-mode ergonomics _(complete)_
5. design-system cleanup and API tightening _(complete)_
6. optional read-group compaction _(available follow-up)_
7. optional detail overlay if needed later _(available follow-up)_

---

## 15. Bottom line

The right plan is not to make the current string transcript slightly prettier.

The right plan is to:

- preserve structured session-log events through the TUI pipeline
- render them as event-aware transcript blocks
- reuse the existing design system where it genuinely fits
- extend or add shared primitives where the current system falls short
- mirror Oh My Pi's strongest transcript behaviors: muted thinking, stateful tool cards, and explicit truncation
- enforce layout correctness with bounded-rendering tests before calling the work done
