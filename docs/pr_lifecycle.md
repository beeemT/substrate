# PR/MR Lifecycle — Current State and Expansion Roadmap

## Current State

### Three persistence layers

| Layer | Struct | Table | Purpose |
|---|---|---|---|
| Event log | `ReviewArtifactEventPayload` wrapping `ReviewArtifact` | `system_events` | Audit trail; replay fallback |
| Provider rows | `domain.GithubPullRequest` / `domain.GitlabMergeRequest` | `github_pull_requests` / `gitlab_merge_requests` | Live upserted state |
| Link table | `domain.SessionReviewArtifact` | `session_review_artifacts` | work item → provider row join |

Both `PersistGithubPR` and `PersistGitlabMR` write all three atomically.

### Events substrate reacts to today

| Event | GitHub action | GitLab action |
|---|---|---|
| `worktree.created` | Create draft PR (defer if no commits yet) | `glab mr create --draft` |
| `worktree.reused` | Update PR description with new sub-plan | Update MR description |
| `plan.approved` | Post plan as comment on source issue | Post plan as comment on source issue |
| `work_item.completed` | Promote draft → open | Promote draft → open |

### Inbound refresh

A 120-second ticker (`StartPRRefresh` / `StartMRRefresh`) calls `ListNonTerminal` (state NOT IN
`merged`, `closed`) and re-fetches top-level PR/MR state from the API, upserting into the provider
tables. This is the **only inbound channel from the remote platform** — no webhooks, no review
thread fetching, no CI status.

### What the TUI shows today

- **Overview page**: `buildOverviewExternalLifecycle` reads `SessionReviewArtifacts →
  GithubPRs/GitlabMRs` (with event-replay fallback) and renders `OverviewReviewRow` (kind ·
  repoName · ref · state · URL) as plain bullet text under "Review artifacts".
- **`o` keybind**: opens `OverviewLinksOverlay` — a flat list of tracker refs and PR/MR URLs.
- PR/MR state (draft/open/merged/closed) appears in the bullet but drives no action card,
  no keybind, no automation.

### What happens after completion

`overviewActionCompleted` offers `c` (follow-up re-plan) and `i` (inspect plan). Neither path is
PR-aware: the follow-up flow doesn't know whether the PR is still open, already merged, or blocked
on reviewer changes.

---

## Gaps

1. **No inbound reviewer feedback** — review state and who requested changes are never fetched.
   The refresh loop only touches top-level PR state.
2. **Merge detection is passive** — when `state` transitions to `merged` the DB updates and the
   bullet changes, but substrate takes no autonomous action (no issue close, no session archive, no
   notification).
3. **No CI/check status** — GitHub check-run state is never fetched; there's no way to surface or
   react to a failing build on the PR.
4. **No review-thread → agent loop** — if a reviewer requests changes, there is no path to feed
   that back into a follow-up agent session.
5. **PR description not updated on follow-up** — a `c`-triggered follow-up doesn't patch the
   existing PR description to reflect the new plan.
6. **No merge gate from within substrate** — no TUI action to approve or merge a PR.
7. **No configurable post-merge hooks** — e.g. close the linked issue on merge, archive the
   worktree, mark the work item as "truly done".

---

## Expansion Roadmap

### Building block 1 — Artifacts node in the task sidebar

The task sidebar tree today looks like:

```
▸ Overview                    (SidebarEntryTaskOverview)
  ◌ Source details            (SidebarEntryTaskSourceDetails)
  ─ Planning ──────────────   (SidebarEntryGroupHeader)
    ◌ Planning task           (SidebarEntryTaskSession)
  ─ acme/rocket ───────────   (SidebarEntryGroupHeader)
    ✓ Implementation          (SidebarEntryTaskSession)
```

Add a new synthetic node immediately after Source details:

```
▸ Overview
  ◌ Source details
  ◌ Artifacts                 (SidebarEntryTaskArtifacts) ← new
  ─ Planning ──────────────
    ...
```

The node is only emitted when the work item has at least one associated PR/MR.

#### View: accordion list

The content panel shows an accordion list of all PRs/MRs for the work item. The focus model
follows the same internal-cursor pattern as `SettingsPage` (`settingsFocusSections` /
`settingsFocusFields`): the view owns a cursor and expand-set; the App owns which panel has
focus (`mainFocusSidebar` / `mainFocusContent`).

**Navigation:**

| Key | Behaviour |
|---|---|
| `↑` / `↓` | Move cursor to previous / next item |
| `→` or `Space` | Expand focused collapsed item |
| `Space` | Collapse focused expanded item |
| `→` on expanded item | No-op |
| `←` | Return focus to sidebar (emit `FocusSidebarMsg`; handled by App) |

Multiple items can be expanded simultaneously. The full content area becomes scrollable when
expanded cards overflow the viewport.

**Single-PR shortcut:** when the work item has exactly one PR/MR, skip the list and render the
detail card directly — no accordion chrome needed.

#### Per-item display

Each list row (collapsed) shows the minimum needed to triage 30 items at a glance:

```
  #42  acme/auth-svc    feat: distribute config    [open]     ✗ CI  ◐ review
  #43  acme/billing     feat: distribute config    [open]     ✓ CI  ✓ review
  #44  acme/gateway     feat: distribute config    [draft]    ○ CI  —
```

Expanded card shows:

```
  ┌─ #42  acme/auth-svc ──────────────────────────────── [open] ──┐
  │  feat: distribute config                                       │
  │  feature/distribute-config → main                             │
  │  opened 2d ago · updated 3h ago                               │
  │                                                                │
  │  Review                                                        │
  │    ✓ alice    approved          2d ago                         │
  │    ✗ bob      changes requested  1h ago                        │
  │                                                                │
  │  CI                                                            │
  │    ✗ test     3 failures                                       │
  │    ✓ build                                                     │
  │    ✓ lint                                                      │
  └────────────────────────────────────────────────────────────────┘
```

#### Data shape

`ArtifactItem` — the view-layer struct, built from the full provider-row data:

```go
type ArtifactItem struct {
    Provider   string     // "github" | "gitlab"
    Kind       string     // "pr" | "mr"
    ProviderID string     // FK into github_pull_requests / gitlab_merge_requests
    RepoName   string
    Number     int        // PR/MR number
    Title      string     // fetched from API; not stored today
    Ref        string     // "#42" or "!7"
    URL        string
    State      string     // "draft" | "open" | "merged" | "closed"
    HeadBranch string
    BaseBranch string     // target branch; not stored today
    Draft      bool
    MergedAt   *time.Time
    CreatedAt  time.Time
    UpdatedAt  time.Time
    Reviews    []ArtifactReview
    Checks     []ArtifactCheck
}

type ArtifactReview struct {
    ReviewerLogin string
    State         string    // "approved" | "changes_requested" | "commented"
    SubmittedAt   time.Time
}

type ArtifactCheck struct {
    Name       string
    Status     string    // "queued" | "in_progress" | "completed"
    Conclusion string    // "success" | "failure" | "neutral" | "skipped" | "timed_out" | ...
}
```

`buildArtifactItems(wi)` in `app.go` queries the provider tables + new review/check tables and
returns `[]ArtifactItem`.

#### Sidebar node status icon

The Artifacts node icon reflects the worst-case state across all PRs for the work item:

| Condition | Icon |
|---|---|
| Any PR has `changes_requested` | `◐` (warning) |
| Any PR has failing CI | `✗` (error) |
| All PRs merged | `✓` (success) |
| Otherwise | `◌` (muted) |

#### Implementation touch-points

| File | Change |
|---|---|
| `sidebar.go` | Add `SidebarEntryTaskArtifacts` kind; `titlePrefix` and `StatusIcon` cases |
| `app.go` | Add `taskSidebarArtifactsID = "__artifacts__"`; emit entry in `taskSidebarEntries()`; route sentinel in content-switch block; add `buildArtifactItems()` |
| `content.go` | Add `ContentModeArtifacts`; add `artifacts ArtifactsModel` field; route `View` / `Update` / `KeybindHints` / `SetSize` |
| `artifacts_view.go` (new) | `ArtifactsModel` with cursor + expand-set + viewport; full accordion render |
| `msgs.go` | `ArtifactItem`, `ArtifactReview`, `ArtifactCheck` data structs |

---

### Building block 2 — Inbound review state

#### Goal

Extend the 120-second refresh loop to also fetch per-reviewer review state for non-terminal PRs/MRs.
Review comment **bodies** are not stored — only triage-level metadata (who, what state, when). Bodies
are fetched live at follow-up time only (see BB3).

#### GitHub API surface

After fetching `/repos/:owner/:repo/pulls/:number` (existing), also call:

```
GET /repos/:owner/:repo/pulls/:number/reviews
```

Response shape (relevant fields):

```json
[
  {
    "id": 12345,
    "user": { "login": "alice" },
    "state": "APPROVED",
    "submitted_at": "2025-04-15T10:30:00Z"
  }
]
```

GitHub review states: `APPROVED`, `CHANGES_REQUESTED`, `COMMENTED`, `DISMISSED`, `PENDING`.
Normalize to lowercase on storage: `approved`, `changes_requested`, `commented`, `dismissed`.
Drop `PENDING` reviews (incomplete; not yet submitted).

When the same reviewer submits multiple reviews, keep the **latest** one per reviewer. The API
returns them chronologically; take the last non-`PENDING` entry per `user.login`.

#### GitLab API surface

GitLab conflates approvals and discussions. For triage metadata:

```
GET /projects/:id/merge_requests/:iid/approval_state
```

Returns per-rule approval groups with per-user `approved` boolean. Map each user who approved
to `approved`, and each requested-but-not-approved user to `unapproved` (synthetic state for the
sidebar icon).

For `changes_requested` detection, GitLab has no native equivalent. Two options:

1. **Label convention** — treat a configurable label (e.g. `changes-requested`) as the signal.
   Fetched via the existing MR state call (the `labels` field on `glab mr view` JSON).
2. **Discussion resolution** — unresolved threads on the MR imply requested changes. Requires
   `GET /projects/:id/merge_requests/:iid/discussions` which is the same call BB3 needs anyway.
   Count unresolved threads; if > 0, synthesize `changes_requested` for the MR (not per-reviewer).

Option 2 is the better default — it requires no user config and maps naturally to the GitLab
workflow. Store a synthetic review row with `reviewer_login = "__unresolved_threads__"` and
`state = "changes_requested"` when unresolved threads exist, or omit the row when all are resolved.

#### Migration: `011_pr_review_state.sql`

```sql
CREATE TABLE IF NOT EXISTS github_pr_reviews (
    id              TEXT PRIMARY KEY,
    pr_id           TEXT NOT NULL REFERENCES github_pull_requests(id) ON DELETE CASCADE,
    reviewer_login  TEXT NOT NULL,
    state           TEXT NOT NULL,  -- approved | changes_requested | commented | dismissed
    submitted_at    TEXT NOT NULL,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(pr_id, reviewer_login)
);

CREATE TABLE IF NOT EXISTS gitlab_mr_reviews (
    id              TEXT PRIMARY KEY,
    mr_id           TEXT NOT NULL REFERENCES gitlab_merge_requests(id) ON DELETE CASCADE,
    reviewer_login  TEXT NOT NULL,
    state           TEXT NOT NULL,  -- approved | changes_requested | unapproved
    submitted_at    TEXT NOT NULL,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(mr_id, reviewer_login)
);
```

The `UNIQUE(pr_id, reviewer_login)` constraint enables `INSERT OR REPLACE` upsert semantics —
a reviewer's state is always their latest review.

#### Domain types

Add to `internal/domain/review_artifact.go`:

```go
// GithubPRReview is the durable row for a GitHub PR review.
type GithubPRReview struct {
	ID            string
	PRID          string
	ReviewerLogin string
	State         string    // "approved" | "changes_requested" | "commented" | "dismissed"
	SubmittedAt   time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// GitlabMRReview is the durable row for a GitLab MR review.
type GitlabMRReview struct {
	ID            string
	MRID          string
	ReviewerLogin string
	State         string    // "approved" | "changes_requested" | "unapproved"
	SubmittedAt   time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
```

#### Repository interfaces

Add to `internal/repository/interfaces.go`:

```go
type GithubPRReviewRepository interface {
	Upsert(ctx context.Context, review domain.GithubPRReview) error
	ListByPRID(ctx context.Context, prID string) ([]domain.GithubPRReview, error)
	DeleteByPRID(ctx context.Context, prID string) error
}

type GitlabMRReviewRepository interface {
	Upsert(ctx context.Context, review domain.GitlabMRReview) error
	ListByMRID(ctx context.Context, mrID string) ([]domain.GitlabMRReview, error)
	DeleteByMRID(ctx context.Context, mrID string) error
}
```

`DeleteByPRID` / `DeleteByMRID` exist for the case where a PR/MR transitions to a terminal state —
we clean up stale review rows during the same refresh cycle.

Add both to `repository.Resources`. Wire in `sqlite.ResourcesFactory`.

#### Service layer

Two thin services following the transacter pattern: `GithubPRReviewService` and
`GitlabMRReviewService`. They wrap repository access through `Transact` and
nothing else — no business logic at this layer.

#### Adapter changes

**`internal/adapter/github/adapter.go`**

In `refreshPRs`, after the existing per-PR upsert:

```go
// Fetch reviews for non-terminal PRs.
if a.repos.GithubPRReviews != nil {
	var apiReviews []githubReview
	if err := a.getJSON(ctx, fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews", pr.Owner, pr.Repo, pr.Number), nil, &apiReviews); err != nil {
		slog.Warn("github: refresh pr reviews failed", "pr", pr.Number, "error", err)
	} else {
		a.upsertPRReviews(ctx, pr.ID, apiReviews)
	}
}
```

`upsertPRReviews` deduplicates by `user.login` (keep latest non-PENDING), maps state to lowercase,
and upserts each row. If the PR just transitioned to terminal state, call `DeleteByPRID` instead.

**`internal/adapter/glab/adapter.go`**

In `refreshSingleMR`, after the existing upsert:

- Call `glab api /projects/:id/merge_requests/:iid/approval_state` (JSON mode) to get per-user
  approval state. Map approved users to `approved`, others to `unapproved`.
- Call `glab api /projects/:id/merge_requests/:iid/discussions` to count unresolved threads.
  If count > 0, upsert a synthetic `__unresolved_threads__` / `changes_requested` row.
- On terminal state, `DeleteByMRID`.

#### Wiring: `ReviewArtifactRepos`

Extend `adapter.ReviewArtifactRepos` with:

```go
GithubPRReviews *service.GithubPRReviewService
GitlabMRReviews *service.GitlabMRReviewService
```

Wire in `cmd/substrate/main.go` alongside the existing PR/MR services.

#### Domain event: `EventPRReviewStateChanged`

Add to `internal/domain/event.go`:

```go
EventPRReviewStateChanged EventType = "pr.review_state_changed"
```

The refresh loop emits this when a reviewer's stored state differs from the freshly fetched state.
Payload: `{ "pr_id": "...", "reviewer": "alice", "old_state": "commented", "new_state": "approved" }`.

The TUI subscribes to this event to trigger an immediate re-read of `buildArtifactItems` rather
than waiting for the next full UI refresh cycle.

#### TUI changes

1. **Extend `ArtifactItem`** (in `overview.go`) with `Reviews []ArtifactReview`.
2. **Add `ArtifactReview`** struct to `overview.go` (view-layer projection):

```go
type ArtifactReview struct {
	ReviewerLogin string
	State         string    // "approved" | "changes_requested" | "commented" | "dismissed"
	SubmittedAt   time.Time
}
```

3. **Extend `buildArtifactItems`** — after loading the provider row, also load reviews
   via `a.svcs.GithubPRReviews.ListByPRID(ctx, pr.ID)` and map to `[]ArtifactReview`.
4. **Render reviews in expanded card** — show per-reviewer state lines as designed in BB1's
   expanded card mockup.
5. **Collapsed row summary** — derive a summary icon: `✓ review` (all approved), `✗ review`
   (changes requested), `◐ review` (mixed/pending), `—` (no reviews).
6. **Sidebar icon update** — in `StatusIcon`, when `SidebarEntry` items include review state,
   surface `◐` (warning) when any PR has `changes_requested`. This requires passing aggregate
   review state into the sidebar entry, likely via a new `ArtifactSummary` field on `SidebarEntry`.

#### Implementation touch-points

| File | Change |
|---|---|
| `internal/domain/review_artifact.go` | `GithubPRReview`, `GitlabMRReview` |
| `internal/domain/event.go` | `EventPRReviewStateChanged` |
| `internal/repository/interfaces.go` | `GithubPRReviewRepository`, `GitlabMRReviewRepository` |
| `internal/repository/transacter.go` | Add fields to `Resources` |
| `internal/repository/sqlite/github_pr_review.go` (new) | SQLite impl |
| `internal/repository/sqlite/gitlab_mr_review.go` (new) | SQLite impl |
| `internal/service/github_pr_review.go` (new) | Thin transacter service |
| `internal/service/gitlab_mr_review.go` (new) | Thin transacter service |
| `internal/adapter/review_artifact_event.go` | Extend `ReviewArtifactRepos` |
| `internal/adapter/github/adapter.go` | Fetch + upsert reviews in `refreshPRs` |
| `internal/adapter/glab/adapter.go` | Fetch + upsert reviews in `refreshSingleMR` |
| `internal/tui/views/overview.go` | `ArtifactReview` struct; extend `ArtifactItem`; extend `buildArtifactItems` |
| `internal/tui/views/artifacts_view.go` | Render review lines in expanded card + collapsed summary |
| `internal/tui/views/sidebar.go` | Drive icon from aggregate review state |
| `internal/tui/views/app.go` | Pass review aggregate into sidebar entry |
| `cmd/substrate/main.go` | Wire review services + repos |
| `migrations/011_pr_review_state.sql` (new) | Review tables |

---

### Building block 3 — Review-thread → agent loop

#### Goal

When a PR has `changes_requested` and the work item is `completed`, let the user trigger a
follow-up agent session that addresses the reviewer's feedback directly.

#### Preconditions

- BB2 must be implemented (review state available in the artifacts view).
- The `FollowUpPlan` path in `internal/orchestrator/planning.go` already exists — this block
  extends the artifact view to feed reviewer comments into that path.

#### UX flow

1. User is on the Artifacts view, focused on a PR with `changes_requested` state.
2. Keybind `f` appears in hints: "follow-up on review".
3. On `f` press:
   a. Emit a `FetchReviewCommentsMsg` (async tea.Cmd).
   b. The cmd calls the adapter's new `FetchReviewComments` method — fetching bodies live.
   c. On success, emit `ReviewCommentsFetchedMsg` with the comment data.
4. The artifacts view transitions to a **review feedback overlay** showing:
   - Per-reviewer sections with their comments/threads.
   - Each thread has a checkbox (future: for selective inclusion).
   - Initially: all feedback is included; the overlay is informational.
5. User presses Enter to confirm → emit `FollowUpFromReviewMsg`.
6. App handles `FollowUpFromReviewMsg`:
   a. Formats the review comments into a structured follow-up context string.
   b. Calls `PlanningService.FollowUpPlan` with the formatted feedback as the changes argument
      (same path as the existing `c` flow, but with reviewer feedback instead of user-typed text).
   c. Restarts the foreman.

#### API calls (live fetch, not stored)

**GitHub:**

```
GET /repos/:owner/:repo/pulls/:number/reviews     → review bodies (top-level comments)
GET /repos/:owner/:repo/pulls/:number/comments     → inline review comments (file + line)
```

Map inline comments to their parent review via `pull_request_review_id`.
Group by reviewer → by review → inline threads.

**GitLab:**

```
GET /projects/:id/merge_requests/:iid/discussions  → threaded discussions with notes
```

Filter to unresolved discussions. Each discussion has notes; the first note is the opening comment.
Group by author.

#### Adapter interface

Add to the GitHub and GitLab adapter public surfaces:

```go
// ReviewComment represents a single review comment fetched live from the API.
type ReviewComment struct {
	ReviewerLogin string
	Body          string
	Path          string // file path for inline comments; empty for top-level
	Line          int    // 0 for top-level comments
	CreatedAt     time.Time
}

// FetchReviewComments retrieves all review comments for the given PR/MR.
// This is a live API call — results are not persisted.
func (a *GithubAdapter) FetchReviewComments(ctx context.Context, owner, repo string, number int) ([]ReviewComment, error)
func (a *GlabAdapter) FetchReviewComments(ctx context.Context, projectPath string, iid int) ([]ReviewComment, error)
```

The TUI calls these through a new `ReviewCommentFetcher` interface injected into `App.svcs`.
This keeps the TUI adapter-agnostic — it receives `[]ReviewComment` regardless of provider.

```go
type ReviewCommentFetcher interface {
	FetchReviewComments(ctx context.Context, provider, repoIdentifier string, number int) ([]ReviewComment, error)
}
```

A dispatcher implementation routes to the appropriate adapter based on `provider`.

#### Follow-up context format

The review comments are formatted into a structured string for the agent:

```
## Review feedback to address

### alice (changes_requested)

**Top-level:** Please add error handling for the timeout case.

**internal/handler/process.go:42:** This retry loop doesn't respect the context deadline.

### bob (commented)

**internal/handler/process.go:78:** Nit: consider using a switch here.
```

This string replaces the user-typed feedback in `FollowUpPlan`'s `changes` parameter.

#### Messages

```go
type FetchReviewCommentsMsg struct {
	Item ArtifactItem // which PR/MR to fetch for
}

type ReviewCommentsFetchedMsg struct {
	Item     ArtifactItem
	Comments []ReviewComment
	Err      error
}

type FollowUpFromReviewMsg struct {
	FormattedFeedback string
}
```

#### Implementation touch-points

| File | Change |
|---|---|
| `internal/adapter/github/adapter.go` | `FetchReviewComments` method |
| `internal/adapter/glab/adapter.go` | `FetchReviewComments` method |
| `internal/adapter/review_comment.go` (new) | `ReviewComment` type, `ReviewCommentFetcher` interface + dispatcher |
| `internal/tui/views/artifacts_view.go` | `f` keybind; review feedback overlay rendering; `FetchReviewCommentsMsg` / `ReviewCommentsFetchedMsg` handling |
| `internal/tui/views/msgs.go` | `FetchReviewCommentsMsg`, `ReviewCommentsFetchedMsg`, `FollowUpFromReviewMsg` |
| `internal/tui/views/app.go` | Handle `FollowUpFromReviewMsg` → `FollowUpPlan`; wire `ReviewCommentFetcher` into svcs |
| `cmd/substrate/main.go` | Wire `ReviewCommentFetcher` dispatcher |

---

### Building block 4 — CI/check status

#### Goal

Fetch and display CI/check-run status for non-terminal PRs. GitHub-first; GitLab parity tracked
separately (GitLab pipelines use a different API shape).

#### GitHub API surface

```
GET /repos/:owner/:repo/commits/:ref/check-runs
```

Where `:ref` is `pr.HeadBranch`. Response shape (relevant fields):

```json
{
  "total_count": 3,
  "check_runs": [
    {
      "id": 1,
      "name": "test",
      "status": "completed",
      "conclusion": "failure",
      "started_at": "...",
      "completed_at": "..."
    }
  ]
}
```

Status values: `queued`, `in_progress`, `completed`.
Conclusion values (only when completed): `success`, `failure`, `neutral`, `cancelled`,
`skipped`, `timed_out`, `action_required`, `stale`.

#### GitLab API surface

```
GET /projects/:id/pipelines?ref=:source_branch&per_page=1&order_by=updated_at
GET /projects/:id/pipelines/:pipeline_id/jobs
```

Map pipeline jobs to `ArtifactCheck` rows. GitLab job statuses map as:
- `success` → conclusion `success`
- `failed` → conclusion `failure`
- `running` / `pending` → status `in_progress`
- `canceled` → conclusion `cancelled`
- `skipped` → conclusion `skipped`

#### Migration: `012_pr_check_status.sql`

```sql
CREATE TABLE IF NOT EXISTS github_pr_checks (
    id          TEXT PRIMARY KEY,
    pr_id       TEXT NOT NULL REFERENCES github_pull_requests(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    status      TEXT NOT NULL,  -- queued | in_progress | completed
    conclusion  TEXT NOT NULL DEFAULT '',  -- success | failure | neutral | ...
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at  TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(pr_id, name)
);

CREATE TABLE IF NOT EXISTS gitlab_mr_checks (
    id          TEXT PRIMARY KEY,
    mr_id       TEXT NOT NULL REFERENCES gitlab_merge_requests(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    status      TEXT NOT NULL,
    conclusion  TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at  TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(mr_id, name)
);
```

`UNIQUE(pr_id, name)` — each check-run name is unique per PR. On re-runs, the latest state wins.

#### Domain types

Add to `internal/domain/review_artifact.go`:

```go
type GithubPRCheck struct {
	ID         string
	PRID       string
	Name       string
	Status     string // "queued" | "in_progress" | "completed"
	Conclusion string // "success" | "failure" | "neutral" | "cancelled" | "skipped" | "timed_out" | ..."
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type GitlabMRCheck struct {
	ID         string
	MRID       string
	Name       string
	Status     string
	Conclusion string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
```

#### Repository interfaces

```go
type GithubPRCheckRepository interface {
	Upsert(ctx context.Context, check domain.GithubPRCheck) error
	ListByPRID(ctx context.Context, prID string) ([]domain.GithubPRCheck, error)
	DeleteByPRID(ctx context.Context, prID string) error
}

type GitlabMRCheckRepository interface {
	Upsert(ctx context.Context, check domain.GitlabMRCheck) error
	ListByMRID(ctx context.Context, mrID string) ([]domain.GitlabMRCheck, error)
	DeleteByMRID(ctx context.Context, mrID string) error
}
```

Add both to `repository.Resources`. Wire in `sqlite.ResourcesFactory`.

#### Adapter changes

**`internal/adapter/github/adapter.go`**

In `refreshPRs`, after review fetch (BB2):

```go
if a.repos.GithubPRChecks != nil {
	var checkResp struct {
		CheckRuns []githubCheckRun `json:"check_runs"`
	}
	if err := a.getJSON(ctx, fmt.Sprintf("/repos/%s/%s/commits/%s/check-runs", pr.Owner, pr.Repo, pr.HeadBranch), nil, &checkResp); err != nil {
		slog.Warn("github: refresh pr checks failed", "pr", pr.Number, "error", err)
	} else {
		a.upsertPRChecks(ctx, pr.ID, checkResp.CheckRuns)
	}
}
```

**`internal/adapter/glab/adapter.go`**

In `refreshSingleMR`, after review fetch:

- `glab api /projects/:id/pipelines?ref=:source_branch&per_page=1&order_by=updated_at`
- If a pipeline exists: `glab api /projects/:id/pipelines/:pipeline_id/jobs`
- Map jobs to `GitlabMRCheck` rows and upsert.

#### Domain event

```go
EventPRCIFailed EventType = "pr.ci_failed"
```

Emitted when any check transitions to `conclusion = failure`. Payload:
`{ "pr_id": "...", "check_name": "test", "conclusion": "failure" }`.

Future: this event can trigger an optional auto-follow-up agent session. Not wired in this block.

#### TUI changes

1. **Extend `ArtifactItem`** with `Checks []ArtifactCheck`.
2. **Add `ArtifactCheck`** struct:

```go
type ArtifactCheck struct {
	Name       string
	Status     string // "queued" | "in_progress" | "completed"
	Conclusion string // "success" | "failure" | ..."
}
```

3. **Extend `buildArtifactItems`** — load checks via `ListByPRID` / `ListByMRID` and map.
4. **Render checks in expanded card** — per-check lines with status icon + name + failure count.
5. **Collapsed row summary** — derive CI icon: `✓ CI` (all success), `✗ CI` (any failure),
   `○ CI` (in progress/queued), `—` (no checks).
6. **Sidebar icon update** — `✗` (error) when any PR has failing CI (see BB1 icon table).

#### Implementation touch-points

| File | Change |
|---|---|
| `internal/domain/review_artifact.go` | `GithubPRCheck`, `GitlabMRCheck` |
| `internal/domain/event.go` | `EventPRCIFailed` |
| `internal/repository/interfaces.go` | `GithubPRCheckRepository`, `GitlabMRCheckRepository` |
| `internal/repository/transacter.go` | Add fields to `Resources` |
| `internal/repository/sqlite/github_pr_check.go` (new) | SQLite impl |
| `internal/repository/sqlite/gitlab_mr_check.go` (new) | SQLite impl |
| `internal/service/github_pr_check.go` (new) | Thin transacter service |
| `internal/service/gitlab_mr_check.go` (new) | Thin transacter service |
| `internal/adapter/review_artifact_event.go` | Extend `ReviewArtifactRepos` with check services |
| `internal/adapter/github/adapter.go` | Fetch + upsert checks in `refreshPRs` |
| `internal/adapter/glab/adapter.go` | Fetch + upsert checks in `refreshSingleMR` |
| `internal/tui/views/overview.go` | `ArtifactCheck` struct; extend `ArtifactItem`; extend `buildArtifactItems` |
| `internal/tui/views/artifacts_view.go` | Render check lines in expanded card + collapsed summary |
| `internal/tui/views/sidebar.go` | Drive icon from aggregate check state |
| `internal/tui/views/app.go` | Pass check aggregate into sidebar entry |
| `cmd/substrate/main.go` | Wire check services + repos |
| `migrations/012_pr_check_status.sql` (new) | Check tables |

---

### Building block 5 — Post-merge automation

#### Goal

When the refresh loop detects that **all** PRs/MRs for a work item have reached `merged` state,
perform configurable post-merge actions: close the linked tracker issue and transition the work item
to a distinct `merged` state.

#### Why "all PRs merged" not "any PR merged"

A work item can span multiple repos (e.g. multi-repo implementation). The work isn't "shipped"
until every PR lands. The check is: for every `SessionReviewArtifact` link, the corresponding
provider row has `state = merged`.

#### New work item state: `SessionMerged`

Add to `internal/domain/work_item.go`:

```go
SessionMerged SessionState = "merged"
```

This is a terminal state distinct from `SessionCompleted` ("implementation done, PR open").
State machine transition: `SessionCompleted → SessionMerged` (only).

The `merged` state is set by the post-merge handler, not by user action. The TUI should treat it
as a final success state (same icon as `completed` but with distinct label).

Update `overviewActionCompleted` and sidebar rendering to handle the new state:
- Merged sessions show a `merged` badge instead of `completed`.
- The `c` (follow-up) keybind is hidden for merged sessions — you wouldn't re-plan on a merged PR.
- The `i` (inspect) keybind remains available.

#### Domain event: `EventPRMerged`

```go
EventPRMerged EventType = "pr.merged"
```

Emitted when the refresh loop detects that all PRs for a work item have `state = merged`.
Payload: `{ "work_item_id": "...", "workspace_id": "..." }`.

This is emitted **once** per work item, not per PR. The refresh loop must track whether the event
has already been emitted (check: work item state is already `merged`) to avoid duplicate emissions
across refresh cycles.

#### Merge detection in the refresh loop

The detection logic lives in the refresh loop, not in a separate subscriber, because that's where
state transitions are observed:

1. After upserting a PR/MR with `state = merged`, check:
   - Load all `SessionReviewArtifact` links for the work item.
   - For each link, load the provider row and check `state`.
   - If all are `merged` and the work item state is `SessionCompleted`:
     a. Transition work item to `SessionMerged`.
     b. Emit `EventPRMerged`.

This check runs only when a PR transitions to `merged` (i.e., the freshly fetched state differs
from the stored state). It does not run on every tick for already-merged PRs.

#### Config flags

Add to `internal/config/config.go`:

```go
// In GithubConfig:
PostMergeCloseIssue bool `yaml:"post_merge_close_issue"`

// In GlabConfig:
PostMergeCloseIssue bool `yaml:"post_merge_close_issue"`
```

When `true`, the adapter subscribes to `EventPRMerged` and closes the linked tracker issue.

#### Issue closing

**GitHub:** `PATCH /repos/:owner/:repo/issues/:number` with `{"state": "closed"}`.
The issue number and repo are available from the work item's source external ID
(e.g. `gh:issue:owner/repo#42`). Parse the external ID to extract coordinates.

**GitLab:** `PUT /projects/:id/issues/:iid` with `{"state_event": "close"}`.
Similar external ID parsing for `gl:issue:project/path#42`.

If the issue is already closed, the API call is a no-op (both platforms return 200).

#### Adapter subscription

In `cmd/substrate/main.go`, add `EventPRMerged` to the subscription list for both GitHub and
GitLab adapters. The handler checks `cfg.PostMergeCloseIssue` and calls the close API if enabled.

#### Implementation touch-points

| File | Change |
|---|---|
| `internal/domain/work_item.go` | `SessionMerged` state |
| `internal/domain/event.go` | `EventPRMerged` |
| `internal/config/config.go` | `PostMergeCloseIssue` on `GithubConfig`, `GlabConfig` |
| `internal/adapter/github/adapter.go` | Merge detection in `refreshPRs`; `onPRMerged` handler; issue close |
| `internal/adapter/glab/adapter.go` | Merge detection in `refreshSingleMR`; `onPRMerged` handler; issue close |
| `internal/tui/views/overview.go` | Handle `SessionMerged` in overview rendering |
| `internal/tui/views/sidebar.go` | Handle `SessionMerged` in `StatusIcon` |
| `internal/tui/views/app.go` | Handle `SessionMerged` in action routing |
| `cmd/substrate/main.go` | Subscribe adapters to `EventPRMerged` |

---

### Building block 6 — PR description sync on follow-up

#### Goal

When a follow-up plan is approved and an open PR exists for the work item, update the PR/MR
description with the new plan text.

#### Current state

The `onWorktreeReused` handler in `internal/adapter/github/adapter.go` (line ~610) already
patches the PR description:

```go
description := appendTrackerFooter(strings.TrimSpace(p.SubPlan), renderGitHubTrackerRefs(p.TrackerRefs, p.Review.BaseRepo))
a.patchJSON(ctx, fmt.Sprintf("/repos/%s/%s/pulls/%d", ...), map[string]any{"body": description}, nil)
```

The equivalent logic exists in `internal/adapter/glab/adapter.go` via `glab mr update --description`.

Neither adapter reacts to `plan.approved` for description updates — `onPlanApproved` only posts
a comment on the source issue.

#### Design

1. **Extract a shared PR description builder** from the `onWorktreeReused` handlers:

```go
// In internal/adapter/github/adapter.go:
func (a *GithubAdapter) updatePRDescription(ctx context.Context, owner, repo string, number int, planText string, trackerRefs []trackerRef, baseRepo repoCoordinates) error

// In internal/adapter/glab/adapter.go:
func (a *GlabAdapter) updateMRDescription(ctx context.Context, repoDir, sourceBranch, planText string) error
```

2. **Extend `onPlanApproved`** to also update PR descriptions:

The `plan.approved` event payload already includes the plan text (it's what gets posted as a
comment). The handler needs to:
   a. Look up open PRs/MRs for the work item (via `SessionReviewArtifact` links).
   b. For each open PR/MR, call the description update helper.

The challenge: `onPlanApproved` currently receives a `commentBody` + `externalIDs` payload
(see `extractPlanCommentPayload`). It does not receive work item ID or PR coordinates.

**Solution:** Extend the `plan.approved` event payload to include `work_item_id`. The handler
can then:
- Look up `SessionReviewArtifact` links by work item ID.
- For each link, load the provider row to get PR/MR coordinates.
- Call the description update helper.

3. **Call `onWorktreeReused`'s extracted helper** from the extended `onPlanApproved`:

```go
func (a *GithubAdapter) onPlanApproved(ctx context.Context, payload string) error {
	// Existing: post comment on source issue.
	commentBody, externalIDs := extractPlanCommentPayload(payload)
	// ... existing comment logic ...

	// New: update PR description if work item has open PRs.
	workItemID := extractWorkItemID(payload)
	if workItemID != "" {
		a.updateOpenPRDescriptions(ctx, workItemID, commentBody)
	}
	return nil
}
```

#### Edge cases

- **No open PR**: Skip description update (PR may have been merged between plan approval and
  this handler running).
- **Multiple open PRs**: Update all of them — each gets the same plan text.
- **Follow-up on a merged PR**: The `SessionMerged` state (BB5) prevents follow-up, so this
  path shouldn't trigger. But defensively: skip closed/merged PRs.

#### Implementation touch-points

| File | Change |
|---|---|
| `internal/adapter/github/adapter.go` | Extract `updatePRDescription` helper; extend `onPlanApproved` |
| `internal/adapter/glab/adapter.go` | Extract `updateMRDescription` helper; extend `onPlanApproved` |
| `internal/orchestrator/planning.go` | Include `work_item_id` in `plan.approved` event payload |

---

### Building block 7 — Merge gate from within substrate

#### Goal

Let the user merge a PR/MR directly from the Artifacts view when all preconditions are met.

#### Preconditions for the `m` keybind

The keybind is shown only when ALL of:

1. Config flag `allow_substrate_merge: true` is set (opt-in).
2. The focused PR/MR is in `open` state (not `draft`, `merged`, or `closed`).
3. All reviews are `approved` (no `changes_requested`, no pending reviews). If no reviews
   exist, this condition is satisfied (some repos don't require reviews).
4. All CI checks are `success` or `skipped` (no `failure`, `in_progress`, `queued`).
   If no checks exist, this condition is satisfied.

#### Config

Add to `internal/config/config.go`:

```go
// In GithubConfig:
AllowSubstrateMerge bool `yaml:"allow_substrate_merge"`

// In GlabConfig:
AllowSubstrateMerge bool `yaml:"allow_substrate_merge"`
```

The config value must be threaded through to the TUI so the artifacts view can check it.
Add it to the `AppServices` or `AppConfig` struct that `App` holds.

#### UX flow

1. User focuses a PR that meets all preconditions.
2. `m` appears in hints: "merge".
3. On `m` press:
   a. Show a confirmation dialog: "Merge #42 into main? [y/n]"
   b. On `y`: emit `MergePRMsg`.
   c. On `n`: dismiss dialog.
4. App handles `MergePRMsg`:
   a. Calls the adapter's merge method.
   b. On success: the next refresh cycle will pick up `state = merged`.
   c. On failure: show error in status bar.

#### API calls

**GitHub:**

```
PUT /repos/:owner/:repo/pulls/:number/merge
```

Body: `{ "merge_method": "merge" }` (or `squash` / `rebase` — future config option).
Response: 200 on success, 405 if not mergeable, 409 if SHA mismatch.

**GitLab:**

```
PUT /projects/:id/merge_requests/:iid/merge
```

Or via CLI: `glab mr merge :iid --yes` in the repo directory.

#### Adapter interface

```go
// In internal/adapter/github/adapter.go:
func (a *GithubAdapter) MergePR(ctx context.Context, owner, repo string, number int) error

// In internal/adapter/glab/adapter.go:
func (a *GlabAdapter) MergeMR(ctx context.Context, projectPath string, iid int) error
```

The TUI calls these through a `PRMerger` interface similar to `ReviewCommentFetcher` (BB3):

```go
type PRMerger interface {
	MergePR(ctx context.Context, provider, repoIdentifier string, number int) error
}
```

Dispatcher routes to the appropriate adapter.

#### Messages

```go
type MergePRMsg struct {
	Item ArtifactItem
}

type PRMergedMsg struct {
	Item ArtifactItem
	Err  error
}
```

#### Implementation touch-points

| File | Change |
|---|---|
| `internal/config/config.go` | `AllowSubstrateMerge` on `GithubConfig`, `GlabConfig` |
| `internal/adapter/github/adapter.go` | `MergePR` method |
| `internal/adapter/glab/adapter.go` | `MergeMR` method |
| `internal/adapter/pr_merger.go` (new) | `PRMerger` interface + dispatcher |
| `internal/tui/views/artifacts_view.go` | `m` keybind with precondition check; confirm dialog; `MergePRMsg` emission |
| `internal/tui/views/msgs.go` | `MergePRMsg`, `PRMergedMsg` |
| `internal/tui/views/app.go` | Handle `MergePRMsg` → adapter call; handle `PRMergedMsg` → status bar feedback |
| `cmd/substrate/main.go` | Wire `PRMerger` dispatcher; thread merge config to TUI |

---

## Dependency graph

```
BB1 (artifacts view) ← DONE
 │
 ├─► BB2 (review state)        ← DONE
 │    │
 │    ├─► BB3 (review → agent)  ← requires: BB2 review data + follow-up path
 │    │
 │    └─► BB7 (merge gate)      ← requires: BB2 reviews + BB4 checks + config
 │
 ├─► BB4 (CI/check status)      ← IN PROGRESS
 │    │
 │    └─► BB7 (merge gate)      ← requires: BB2 + BB4
 │
 ├─► BB5 (post-merge)           ← independent of BB2/BB4; requires: config + state
 │
 └─► BB6 (description sync)     ← independent; requires: plan.approved payload extension
```

**Recommended implementation order:** BB2 → BB4 → BB3 → BB6 → BB5 → BB7.

BB2 and BB4 can be parallelized (different API endpoints, different tables, no shared types).
BB6 is small and self-contained — can be done at any point after BB1. BB5 depends on the
`SessionMerged` state but not on BB2/BB4 data. BB7 is the capstone: it gates on review + CI state.

---

## Key files

| File | Touches |
|---|---|
| `internal/domain/review_artifact.go` | `GithubPRReview`, `GitlabMRReview`, `GithubPRCheck`, `GitlabMRCheck` |
| `internal/domain/work_item.go` | `SessionMerged` state |
| `internal/domain/event.go` | `EventPRMerged`, `EventPRReviewStateChanged`, `EventPRCIFailed` |
| `internal/config/config.go` | `PostMergeCloseIssue`, `AllowSubstrateMerge` |
| `internal/repository/interfaces.go` | Review + check repository interfaces |
| `internal/repository/transacter.go` | New fields on `Resources` |
| `internal/repository/sqlite/` | Review + check SQLite implementations |
| `internal/service/` | Review + check thin services |
| `internal/adapter/review_artifact_event.go` | Extend `ReviewArtifactRepos` |
| `internal/adapter/review_comment.go` (new) | `ReviewCommentFetcher` interface + types |
| `internal/adapter/pr_merger.go` (new) | `PRMerger` interface + dispatcher |
| `internal/adapter/github/adapter.go` | Refresh loop extensions, `FetchReviewComments`, `MergePR` |
| `internal/adapter/glab/adapter.go` | Refresh loop extensions, `FetchReviewComments`, `MergeMR` |
| `internal/orchestrator/planning.go` | Extend `plan.approved` payload with `work_item_id` |
| `internal/tui/views/overview.go` | `ArtifactReview`, `ArtifactCheck` structs; extend `ArtifactItem`; extend `buildArtifactItems` |
| `internal/tui/views/artifacts_view.go` | Review/check rendering; `f` keybind; `m` keybind; review feedback overlay |
| `internal/tui/views/sidebar.go` | Aggregate review/check state driving icon |
| `internal/tui/views/app.go` | `FollowUpFromReviewMsg`, `MergePRMsg` handling; wire new services |
| `internal/tui/views/msgs.go` | New message types |
| `cmd/substrate/main.go` | Wire all new services, repos, subscriptions |
| `migrations/011_pr_review_state.sql` (new) | Review tables |
| `migrations/012_pr_check_status.sql` (new) | Check tables |