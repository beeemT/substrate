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

Extend the refresh loop to fetch review state for PRs in `open` state:

- GitHub: `GET /repos/:owner/:repo/pulls/:number/reviews` → per-reviewer state + timestamp.
- GitLab: `GET /projects/:id/merge_requests/:iid/approvals` for approval state.
- Store in a new `github_pr_reviews` table (prID, reviewerLogin, state, submittedAt) with FK to
  `github_pull_requests`. Equivalent `gitlab_mr_reviews` for GitLab.
- Review comment bodies are **not stored** — they are fetched live at agent-start time only (see
  Building block 3). Local storage is display state only.
- Emit `EventPRReviewStateChanged` when a reviewer's state transitions, so the TUI can react
  without a full DB poll.
- The Artifacts view populates `ArtifactReview` slice from this table on each `SetData` call.

---

### Building block 3 — Review-thread → agent loop

When a PR has `changes_requested` and the session is `completed`:

- On the Artifacts view, surface a `f` (follow-up) keybind.
- On invocation, fetch the full review comment bodies live from the API:
  - GitHub: `GET /repos/:owner/:repo/pulls/:number/reviews` (bodies) + `GET /pulls/:number/comments` (inline thread).
  - GitLab: `GET /projects/:id/merge_requests/:iid/discussions`.
- Present a selection UI (future: per-PR, per-reviewer, per-comment thread) letting the user
  choose which feedback to address.
- Pre-populate a follow-up agent session with the selected feedback as additional context
  alongside the current plan.
- No local storage of comment bodies — always fetched fresh so the agent acts on current state.

This closes the loop: PR opened → reviewer leaves feedback → substrate addresses feedback.

---

### Building block 4 — CI/check status

- Extend the refresh loop: `GET /repos/:owner/:repo/commits/:ref/check-runs` for `open` PRs.
- Store per-check rows in a new `github_pr_checks` table (prID, name, status, conclusion).
- The Artifacts view populates `ArtifactCheck` slice from this table.
- Sidebar node icon reflects failing checks (see Building block 1 icon table).
- Future: `EventPRCIFailed` → optional follow-up agent session to investigate.

---

### Building block 5 — Post-merge automation

When the refresh loop detects `state = merged`:

- Emit `EventPRMerged` (new domain event).
- Adapters subscribe: optionally close the linked tracker issue (same API auth already in use
  for comments).
- Work item transitions to a new `SessionMerged` state — distinct from `SessionCompleted`
  ("implementation done") which does not imply "shipped".
- Config flags: `post_merge_close_issue: true` on `GithubConfig` / `GlabConfig`.

---

### Building block 6 — PR description sync on follow-up

When a follow-up plan is approved (`EventPlanApproved` for a follow-up session):

- If a PR is still open for the work item, patch its description with the new plan text.
- Extract the existing description-patch logic from the `worktree.reused` handler into a shared
  helper; call it from the `plan.approved` handler as well.

---

### Building block 7 — Merge gate from within substrate

On the Artifacts view, when a PR is in `open` state, all reviews `approved`, and CI passing:

- Add an `m` keybind: confirm dialog → merge API call (`PUT /repos/:owner/:repo/merges` or
  `gh pr merge`).
- Opt-in via config (`allow_substrate_merge: true`); not shown when flag is absent.

---

## Key files

| File | Touches |
|---|---|
| `internal/domain/review_artifact.go` | `ArtifactItem`, `ArtifactReview`, `ArtifactCheck` |
| `internal/domain/event.go` | `EventPRMerged`, `EventPRReviewStateChanged`, `EventPRCIFailed` |
| `internal/adapter/github/adapter.go` | Refresh loop extension for review + CI state |
| `internal/adapter/glab/adapter.go` | Refresh loop extension for review state |
| `internal/adapter/bridge/review_artifact_event.go` | Shared persist helpers |
| `internal/tui/views/sidebar.go` | `SidebarEntryTaskArtifacts` kind |
| `internal/tui/views/app.go` | Sentinel ID, entry emission, content routing, `buildArtifactItems` |
| `internal/tui/views/content.go` | `ContentModeArtifacts`, sub-model routing |
| `internal/tui/views/artifacts_view.go` (new) | Accordion view model |
| `internal/tui/views/msgs.go` | `ArtifactItem`, `ArtifactReview`, `ArtifactCheck` structs |
| `migrations/` | `github_pr_reviews`, `gitlab_mr_reviews`, `github_pr_checks` tables |
