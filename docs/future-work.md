# Future Work

Deferred follow-ups from the initial implementation. Each item is self-contained and can be picked up independently.

## 1. Durable per-source-item summaries

Aggregate sessions store `SourceItemIDs` and `tracker_refs` but carry no canonical per-source-item summary list. The overview renders provider + ref only for multi-source sessions and does not reverse-parse merged descriptions.

**Implementation requires:**

- A `source_summaries` persistence layer (provider, ref, title, excerpt, URL) — either a JSON column on the work item or a dedicated join table.
- Population logic in workspace lifecycle adapters (GitHub, GitLab).
- Rendering support in the overview and source-details views.

**Affected areas:** domain model, provider adapters (GitHub/GitLab), `overview.go`, `source_details_view.go`.

## 2. PR/MR durable persistence

~~Completed.~~ PR/MR data now uses provider-specific tables (`github_pull_requests`, `gitlab_merge_requests`) linked to work items via `session_review_artifacts`. Background state refresh polls provider APIs every 120s for non-terminal artifacts. Overview reads from indexed tables with event-replay fallback.

**Remaining follow-up:**

- Overview-native PR action buttons (merge, close, mark ready) — deferred until refresh proves trustworthy in practice.

## 3. Git/worktree health badges

Compact git dirty-state counts per repo in the overview task table. Currently completely absent. The `GitClient` service exists but `git-work` plumbing does not expose compact dirty-state summaries.

**Implementation requires:**

- Explicit repo-cleanliness summary plumbing in the gitwork package.
- Evaluate need only after the overview is in regular use — may not justify the cost.

**Affected areas:** `gitwork` package, `overview.go`.

## 4. Per-tool-card detail overlay

A focused single-card detail overlay for transcript tool cards, rather than only the global verbose mode toggle. Verbose mode works today. Overlay primitives (`ComputeSplitOverlayLayout`, `RenderOverlayFrame`) exist but are not wired to transcript cards.

**Implementation requires:**

- Wiring existing overlay primitives to per-card focus interaction.
- Moderate effort, mostly UX design for navigation and dismiss behavior.

**Affected areas:** `session_transcript.go`, planning/session log views.

## 5. Read-group compaction

Collapse adjacent repetitive file-read tool calls into a grouped summary line. Per-card rendering is acceptable today — tool output previews are already truncated and the bridge does not surface stable tool-call identifiers.

**Implementation requires:**

- Adjacency-based grouping logic in `RenderTranscript`.
- Small-to-medium effort; no new infrastructure.

**Affected areas:** `session_transcript.go`.
