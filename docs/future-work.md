# Future Work

<!-- docs:last-integrated-commit 10e50295fb75f72c67233e191ae34fb8fc091f1e -->

Deferred follow-ups from the initial implementation. Each item is self-contained and can be picked up independently.

## 1. Git/worktree health badges

Compact git dirty-state counts per repo in the overview task table. Currently completely absent. The `GitClient` service exists but `git-work` plumbing does not expose compact dirty-state summaries.

**Implementation requires:**

- Explicit repo-cleanliness summary plumbing in the gitwork package.
- Evaluate need only after the overview is in regular use — may not justify the cost.

**Affected areas:** `gitwork` package, `overview.go`.

## 2. Per-tool-card detail overlay

A focused single-card detail overlay for transcript tool cards, rather than only the global verbose mode toggle. Verbose mode works today. Overlay primitives (`ComputeSplitOverlayLayout`, `RenderOverlayFrame`) exist but are not wired to transcript cards.

**Implementation requires:**

- Wiring existing overlay primitives to per-card focus interaction.
- Moderate effort, mostly UX design for navigation and dismiss behavior.

**Affected areas:** `session_transcript.go`, planning/session log views.

## 3. Read-group compaction

Collapse adjacent repetitive file-read tool calls into a grouped summary line. Per-card rendering is acceptable today — tool output previews are already truncated and the bridge does not surface stable tool-call identifiers.

**Implementation requires:**

- Adjacency-based grouping logic in `RenderTranscript`.
- Small-to-medium effort; no new infrastructure.

**Affected areas:** `session_transcript.go`.
