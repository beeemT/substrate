# BB3 — Review-thread → agent loop

Lets the user turn outstanding PR/MR review comments into agent follow-up work
without leaving the TUI. Two dispatch modes: address (default, keeps the plan)
and re-plan (escape hatch, replaces the plan).

---

## Goals

1. Make outstanding review feedback actionable from the artifacts view.
2. Default to implementation-only follow-up (`FollowUpSession` per repo) — the
   plan stays intact, the agent gets the comments as instructions.
3. Provide an explicit escape hatch (re-plan via `FollowUpPlan`) for the rare
   case where review feedback is architectural.
4. Let the user select which PRs and which comments are addressed; default is
   everything unresolved.
5. Group comments per repo → per file (with a `General` section for top-level
   review comments) — the agent cares about where, not who.

## Non-goals

- Auto-resolve comments on the PR after follow-up completes. The human resolves.
- Free-form addenda alongside review comments. Comments only for now.
- Show resolved comments. Hidden entirely; revisit if it becomes a nuisance.
- Threaded reply rendering. Only the opening comment of each thread is shown.
- BB7-style description sync from review feedback. Re-plan goes through the
  existing `FollowUpPlan` → BB7 sync path; address-mode does not touch
  descriptions.

---

## UX flow

### Entry: `f` keybind on the artifacts view

Hint shown when the focused work item is in `SessionCompleted` or
`SessionReviewing` state and at least one PR/MR exists. Pressing `f`:

1. Opens a loading overlay (spinner + "Fetching review comments…").
2. Fans out: for every PR/MR in the work item (regardless of review state),
   call the adapter's `FetchReviewComments` in parallel. We can't pre-filter
   on `changes_requested` because a reviewer can leave actionable comments
   without that state.
3. Each result is filtered to **unresolved** comments only. Records
   `fetchedAt = time.Now()`.
4. Aggregate result:
   - **0 PRs with unresolved comments** → close overlay, toast "No outstanding
     review comments".
   - **1 PR** → skip picker, open comment-selection overlay directly.
   - **>1 PRs** → open PR picker.

### PR picker (only when >1 PR has unresolved comments)

```
┌─ Select PRs to address ───────────────────────────┐
│ [x] acme/rocket #42  (3 comments)                 │
│ [x] acme/engine #18  (1 comment)                  │
│ [ ] acme/loader #7   (5 comments)                 │
└───────────────────────────────────────────────────┘
[Space] Toggle  [a] All  [n] None  [Enter] Continue  [Esc] Cancel
```

All checked by default. Enter → comment selection overlay scoped to the
checked PRs.

### Comment selection overlay (split view)

```
┌─ Review comments ─────────────────┬─ Preview ──────────────────────┐
│ acme/rocket                       │ alice — 2025-04-15 14:23       │
│   General                         │ ───────────────────────────── │
│   [x] alice: Please add tests…    │                                │
│                                   │ This retry loop doesn't        │
│   internal/handler/process.go     │ respect the context deadline.  │
│ ▸ [x] :42 alice — retry loop…     │ If the caller passes 5s, this  │
│   [x] :78 bob — switch statement  │ keeps going for 30s.           │
│                                   │                                │
│ acme/engine                       │ Consider:                      │
│   cmd/server/main.go              │   for {                        │
│   [x] :15 alice — graceful shut…  │     select { case <-ctx.Done():│
│                                   │       return ctx.Err()         │
│                                   │     ... }                      │
└───────────────────────────────────┴────────────────────────────────┘
[Enter] Address  [p] Re-plan  [Space] Toggle  [a] All  [n] None  [Esc] Cancel
```

- Left pane: hierarchical checklist. Cursor moves between rows; Space toggles
  the focused row.
- Toggling a file/repo header toggles all its children (cascading).
- Right pane: full body of the focused comment + author + timestamp + URL.
- Empty preview placeholder when cursor is on a header row.

Width: split 50/50 inside the existing overlay frame, respecting parent frame
size. KeybindHints used for the hint bar (no inline hints).

### Dispatch

**Staleness check at dispatch time:** if `time.Since(fetchedAt) > 5min`,
silently re-fetch (with spinner), then proceed with the selection re-applied
to the new dataset (best-effort match by comment ID; comments that disappeared
are dropped, new ones default to deselected).

**Address (Enter):**

1. Group selected comments by repo.
2. For each repo: look up the active completed Task. Format the repo's
   comments into the feedback string. Emit `FollowUpSessionMsg{TaskID, Feedback}`.
3. Multiple repos → multiple messages in `tea.Batch`.
4. **Partial dispatch on missing tasks:** if a repo has selected comments but
   no completed task, skip that repo and surface in toast: "Addressed N of M
   repos (K skipped: no active task)."

**Re-plan (`p`):**

1. Show confirmation modal:
   ```
   ┌─ Re-plan from review feedback ─────────────────────┐
   │                                                    │
   │ This will discard the current plan and create a    │
   │ new one based on the selected review comments.     │
   │                                                    │
   │ Affected:                                          │
   │   • Plan will be replaced                          │
   │   • PR descriptions will be updated to the new     │
   │     plan when approved                             │
   │   • Implementation results from the previous       │
   │     plan will be lost from the active workflow     │
   │                                                    │
   │ Continue?                                          │
   │                                                    │
   │            [y] Yes, re-plan    [n/Esc] Cancel      │
   └────────────────────────────────────────────────────┘
   ```
2. On `y`: format **all selected comments across all repos** into one feedback
   string. Emit `FollowUpPlanMsg{WorkItemID, Feedback}`.
3. On `n`/`Esc`: return to comment selection overlay (preserving selection).

---

## Comment formatting

Same template for both modes. Grouped by repo → file; `General` section per
repo for top-level review comments.

```
## Review comments to address

### acme/rocket

#### General

- alice: Please add tests for the error cases in the retry loop.

#### internal/handler/process.go

- Line 42: This retry loop doesn't respect the context deadline.
- Line 78: Consider using a switch here.

### acme/engine

#### cmd/server/main.go

- Line 15: Missing graceful shutdown.
```

For address-mode: each repo gets its own slice of the format (the `## Review
comments` header + only that repo's `### repoName` section).

---

## Architecture

### New domain type

`internal/adapter/review_comment.go` (new file):

```go
package adapter

type ReviewComment struct {
    ID            string    // provider-specific stable identifier
    ReviewerLogin string
    Body          string
    Path          string    // empty for top-level comments
    Line          int       // 0 for top-level
    URL           string    // direct link to the comment
    CreatedAt     time.Time
    Resolved      bool      // filtered out at fetch boundary, kept here for completeness
}

type ReviewCommentFetcher interface {
    FetchReviewComments(ctx context.Context, provider, repoIdentifier string, number int) ([]ReviewComment, error)
}
```

Dispatcher implementation in same file routes by `provider`. Adapter instances
implement a per-provider `FetchReviewComments` method; dispatcher holds both.

### Adapter methods

**GitHub (`internal/adapter/github/adapter.go`):**

```go
func (a *Adapter) FetchReviewComments(ctx context.Context, owner, repo string, number int) ([]ReviewComment, error)
```

Calls:
- `GET /repos/:owner/:repo/pulls/:number/reviews` — top-level review bodies.
- `GET /repos/:owner/:repo/pulls/:number/comments` — inline review comments.
- For each inline comment, use the GraphQL `pullRequestReview` API to get
  `isResolved` on the parent thread, OR use the REST
  `/repos/:owner/:repo/pulls/:number/reviews/:review_id/comments` ladder.
  Decision: GraphQL single call is cleaner; we already have a token. Use
  `repository.pullRequest.reviewThreads(first: 100) { nodes { isResolved, comments { nodes { id, body, path, line, author, createdAt, url } } } }`.

Filter to `isResolved == false`. Return the flat list.

**GitLab (`internal/adapter/glab/adapter.go`):**

```go
func (a *Adapter) FetchReviewComments(ctx context.Context, projectPath string, iid int) ([]ReviewComment, error)
```

Uses `glab api /projects/:id/merge_requests/:iid/discussions`. Filter to
`resolved == false`. Each discussion's notes flatten into review comments;
for inline comments, take `position.new_path` and `position.new_line`. For
top-level discussions, leave path/line empty.

### TUI changes

**New service field on `App.svcs`:** `ReviewComments adapter.ReviewCommentFetcher`.

**New messages (`internal/tui/views/msgs.go`):**

```go
type FetchReviewCommentsMsg struct {
    WorkItemID string
    Items      []ArtifactItem // PRs to fetch for
}

type ReviewCommentsFetchedMsg struct {
    WorkItemID string
    Result     map[string][]adapter.ReviewComment // keyed by ArtifactItem.ID
    FetchedAt  time.Time
    Err        error
}

type FollowUpFromReviewAddressMsg struct {
    WorkItemID string
    PerRepo    map[string]string // repoName → formatted feedback
}

type FollowUpFromReviewReplanMsg struct {
    WorkItemID string
    Feedback   string // single concatenated string
}
```

**New view: `internal/tui/views/overlay_review_followup.go`** — owns the PR
picker, comment selection (split view), and re-plan confirmation modal as
internal sub-states (`stagePicker | stageSelector | stageConfirm`). Holds:

```go
type ReviewFollowupModel struct {
    workItemID string
    items      []ArtifactItem               // PRs in scope
    comments   map[string][]adapter.ReviewComment // per-PR
    selected   map[string]bool              // comment ID → selected
    pickerSel  map[string]bool              // PR ID → included
    fetchedAt  time.Time
    stage      stage
    cursor     int
    width, height int
    styles     styles.Styles
}
```

**App wiring (`internal/tui/views/app.go`):**

- `f` keybind on the artifacts overlay (`overviewOverlayReviewing`) when state
  is `SessionCompleted` or `SessionReviewing` and ≥1 PR exists.
- `FetchReviewCommentsMsg` handler → spawn cmd that calls
  `svcs.ReviewComments.FetchReviewComments` for each item in parallel
  (`errgroup`-style), aggregate, emit `ReviewCommentsFetchedMsg`.
- `ReviewCommentsFetchedMsg` handler:
  - On error → toast, close overlay.
  - Filter to PRs with ≥1 unresolved comment.
  - 0 → toast "No outstanding review comments".
  - 1 → open `ReviewFollowupModel` at `stageSelector`.
  - >1 → open at `stagePicker`.
- `FollowUpFromReviewAddressMsg` handler:
  - For each `repoName → feedback`, look up the completed Task for that repo
    (via existing task service).
  - Dispatch `FollowUpSessionMsg{TaskID, Feedback}` for each match.
  - Toast partial-dispatch result.
- `FollowUpFromReviewReplanMsg` handler:
  - Emit `FollowUpPlanMsg{WorkItemID, Feedback}` (reuses existing path).

**Artifacts view (`internal/tui/views/artifacts_view.go`):**

- `KeybindHints()` adds `{Key: "f", Label: "Follow up on review"}` when:
  - len(items) > 0
  - work item state is `SessionCompleted` or `SessionReviewing`
- `f` press emits `FetchReviewCommentsMsg{WorkItemID, Items: m.items}`.

Note: artifacts view doesn't currently know the work item state. Pass it down
via a setter or include it in the existing data the view holds.

### Wiring (`cmd/substrate/main.go` and `settings_service.go`)

Construct dispatcher:

```go
fetcher := adapter.NewReviewCommentDispatcher(map[string]adapter.ReviewCommentFetcher{
    "github": ghAdapter,
    "gitlab": glAdapter,
})
```

Inject into `App.svcs.ReviewComments`. Mirror in `settings_service.go`.

---

## Repo task lookup for address-mode

Address-mode needs `repoName → completed Task ID`. The existing services that
expose this:

- `service.TaskService` — list tasks by work item ID.
- Each task has a `RepoName` (or equivalent — to be confirmed during impl).

Lookup: `tasks := svcs.Task.ListByWorkItem(workItemID)`; build
`map[repoName]Task` filtered to `task.State == TaskCompleted`. If a selected
repo has no completed task, it's a skip (counted in the partial-dispatch toast).

---

## File touch list

| File | Change |
|---|---|
| `internal/adapter/review_comment.go` | NEW: `ReviewComment` type, `ReviewCommentFetcher` interface, dispatcher. |
| `internal/adapter/github/adapter.go` | NEW method `FetchReviewComments` (GraphQL for resolution state). |
| `internal/adapter/github/adapter_test.go` | Tests for `FetchReviewComments` filtering and grouping. |
| `internal/adapter/glab/adapter.go` | NEW method `FetchReviewComments` (discussions API). |
| `internal/adapter/glab/adapter_test.go` | Tests for `FetchReviewComments` filtering. |
| `internal/tui/views/overlay_review_followup.go` | NEW: PR picker + split-view selector + confirmation modal as one model with stages. |
| `internal/tui/views/overlay_review_followup_test.go` | NEW: layout/width tests, selection cascading, dispatch correctness. |
| `internal/tui/views/artifacts_view.go` | `f` keybind hint + dispatch; carry work item state. |
| `internal/tui/views/artifacts_view_test.go` | Test `f` hint visibility per state, dispatch payload. |
| `internal/tui/views/msgs.go` | `FetchReviewCommentsMsg`, `ReviewCommentsFetchedMsg`, `FollowUpFromReviewAddressMsg`, `FollowUpFromReviewReplanMsg`. |
| `internal/tui/views/app.go` | Wire fetch cmd, fetched-handler stage routing, address fan-out, re-plan dispatch. Add `ReviewComments` to `Services`. |
| `internal/tui/views/app_test.go` | End-to-end: fetch → picker → selector → address dispatches per-repo `FollowUpSessionMsg`. End-to-end: re-plan path emits `FollowUpPlanMsg` after confirmation. |
| `cmd/substrate/main.go` | Construct dispatcher, inject. |
| `internal/tui/views/settings_service.go` | Mirror dispatcher injection. |
| `docs/pr_lifecycle.md` | Mark BB3 DONE; update spec to reflect two-mode design (current spec only describes single-path FollowUpPlan). |

---

## Phasing

### Phase 1 — Adapter surface (parallelizable)

- GitHub `FetchReviewComments` + tests.
- GitLab `FetchReviewComments` + tests.
- `internal/adapter/review_comment.go` types + dispatcher + tests.

### Phase 2 — TUI overlay (sequential after Phase 1)

- `overlay_review_followup.go` model with three stages.
- Layout/width tests including narrow sizes (per AGENTS.md TUI Rendering rule).
- Selection cascading tests.

### Phase 3 — Wiring (sequential after Phases 1 + 2)

- `app.go` handlers and fan-out.
- `artifacts_view.go` keybind.
- `cmd/substrate/main.go` + `settings_service.go` injection.
- End-to-end `app_test.go` covering both dispatch modes including partial
  dispatch and stale-data refetch.

### Phase 4 — Docs

- Update `docs/pr_lifecycle.md` BB3 section to reflect the two-mode design.
- Mark BB3 DONE in dependency graph.

---

## Risks / open items

1. **GitHub GraphQL token scope:** confirm the existing token has GraphQL
   permissions during Phase 1. If not, fall back to REST + thread-id heuristic.
2. **Comment ID stability:** GitHub returns stable `node_id` values; GitLab's
   discussion-note IDs are stable. Used as map keys for selection persistence
   across re-fetch.
3. **Task → repo mapping:** verify `Task` actually carries a repo identifier
   we can match against `ArtifactItem.RepoName`. Confirm during Phase 3 impl;
   if not, we may need to add it (small change, not blocking).
4. **Spinner overlay during fetch:** the existing TUI has `Spinner` from
   bubbles; reuse rather than rolling new.
5. **Re-fetch staleness with selection drift:** if a comment the user selected
   is gone after re-fetch (resolved/deleted), it's silently dropped. New
   comments default to deselected. Toast on dispatch: "N comment(s) selected
   were no longer available."

---

## Verification

Before yielding:

- `go build ./...` clean.
- `go test ./internal/adapter/github/ ./internal/adapter/glab/
  ./internal/tui/views/ -count=1` passes.
- Manual smoke (if possible): create a fake PR with reviewer comments, run
  through both modes.
- `docs/pr_lifecycle.md` BB3 section updated and dependency graph marked DONE.
