# 11 - Session Overview Page Spec
<!-- docs:last-integrated-commit 2e3d5d91b5b4fc84631db2b783c2b2f8dc76ae78 -->

## Status

Planned. This document specifies the unified overview page for a single root work item/session. The overview is both the default summary surface and the primary control surface for any human action required to move the session forward.

---

## 1. Decision summary

### Agreed product decisions

- There is one canonical overview page for the root work item/session.
- That same overview page is rendered when the operator:
  - selects the work item in the main `Sessions` sidebar, or
  - selects the `Overview` row in the `{externalID} · Tasks` sidebar.
- The overview is not a passive dashboard. It is the main control surface for the session.
- If the user must act to unblock progress, that action must be invokable from the overview page.
- The overview may open overlays to provide more context, but it must not force the operator to navigate into a dedicated detail view just to perform a required action.
- Child execution status should be organized primarily by repo/sub-plan, not by raw child session ID.
- The initial shipped scope should focus on durable internal state already available in the app.
- PR/MR lifecycle surfacing is a committed follow-up because it will be highly valuable, but it needs durable UI-facing state first.

### Consequences

This is not only a view-layer polish change. It requires a coherent cutover of content routing and a stricter product contract around actionability:

1. the content pane needs one stable overview mode instead of many unrelated state-specific concepts
2. human-action states need overview-native actions and context
3. overlays need to become the mechanism for deeper inspection without forcing navigation
4. the data shown on the overview must be deliberately bounded and reliable
5. external lifecycle data needs a follow-up persistence model before PR/MR actions can be trusted in the UI

---

## 2. Current gap

Today the content pane is routed primarily by work item state in `internal/tui/views/app.go`, and the `Overview` row in the task sidebar effectively lands on the same state-driven branch rather than on a dedicated overview model.

Relevant current behavior:

- `internal/tui/views/app.go:1376-1535` switches content mode by `domain.SessionState`.
- `internal/tui/views/content.go:11-193` defines many state-specific content modes (`ReadyToPlan`, `Planning`, `PlanReview`, `Implementing`, `Completed`, `Failed`, `Question`, `Interrupted`, etc.).
- `internal/tui/views/sidebar.go:28-156` already computes concise session/task status metadata such as humanized state, progress, open-question state, interruption state, and last activity.
- `internal/tui/views/source_details_view.go:348-433` already knows how to format tracker refs from work item metadata.
- `internal/tui/views/completed_view.go:13-84` already has a `MRInfo` model, but `internal/tui/views/app.go:1528-1530` currently passes `nil` data into the completed surface.
- planning is only partially surfaced in the UI today: `internal/tui/views/app.go:1407-1418` falls back to placeholder copy when no live planning child session is selected, even though the orchestrator already has a planning session ID and draft path (`internal/orchestrator/planning.go:239-256`, `375-442`).

Important gaps against the desired overview:

1. there is no single stable overview renderer shared by both entry points
2. the user cannot perform every required action from the overview surface
3. the planning state does not yet expose a rich overview-native draft/progress snapshot
4. PR/MR lifecycle data is not durably modeled for the UI
5. git dirty-count data is not currently exposed by the available git-work plumbing
6. multi-source sessions do not currently persist per-source-item title/excerpt summaries in a canonical UI-ready shape

That last point matters. The current root work item model stores `SourceItemIDs`, root `Title`, root `Description`, and `tracker_refs`, but aggregate issue sessions today do not persist a first-class per-source-item summary list; adapters typically compress multi-source descriptions into one merged work item description. The overview must not reverse-parse those merged descriptions.

---

## 3. Goals and non-goals

### Goals

The overview page must answer, at a glance:

- What is this session about?
- Which source tickets/work items fed into it?
- What state is the overall session in?
- Is there a plan yet, and what does it broadly say?
- What repo tasks already exist, and what is their status?
- Is the user required to act right now?
- If so, can they make that decision safely from here?

### Non-goals for the initial ship scope

- full git diffstat or working-tree cleanliness badges
- full PR/MR lifecycle status and actions
- a full replacement for task log/session transcript views
- a full replacement for source-details metadata views
- rendering the full plan markdown inline in the overview page
- reverse-parsing aggregate source descriptions to fake per-ticket summaries

---

## 4. Target end state

### 4.1 Core product contract

The overview page is the root session control surface.

It must satisfy both of these constraints at the same time:

1. **First glance**: the operator should understand the session quickly.
2. **No dead-end**: if the system is waiting on the operator, the operator must be able to act from the overview.

### 4.2 High-level page structure

The page should be organized in this order:

1. `Summary`
2. `Action required` (only when applicable)
3. `Source`
4. `Plan`
5. `Tasks`
6. `External lifecycle`
7. `Recent activity` (optional, compact)

That order is deliberate. If the system is blocked on a human decision, the overview must surface that before passive information.

### 4.3 Summary section

Always visible.

Shows:

- external ID
- session title
- overall state label
- last updated time
- repo progress summary when a plan exists
- compact blocker badges when relevant, e.g.
  - waiting for approval
  - waiting for answer
  - interrupted
  - failed

This section should reuse existing status semantics already present in sidebar rendering rather than inventing a second status vocabulary.

### 4.4 Action required section

Only rendered when the session cannot proceed without user action.

Examples:

- plan waiting for approval
- open question waiting for answer
- interrupted execution that can be resumed/retried
- future PR/MR lifecycle actions once durable state exists

Each action card must show:

- what is blocked
- why it is blocked
- which repos/tasks are affected
- the primary action(s)
- a secondary `Inspect` or `View details` action that opens an overlay

### 4.5 Source section

Shows the source items that make up the root session.

Target fields per source item:

- provider / adapter
- source ref / identifier
- title
- short excerpt or description snippet
- optional URL later

Important rule:

- If the system does not durably have per-source-item title/excerpt data, the overview must show provider + ref only rather than inventing data by parsing the aggregate root description.

For single-source sessions, the root work item title/description is sufficient.
For multi-source sessions, the preferred end state is a dedicated persisted source-summary list in metadata or a stronger domain model. That can land in the same change if needed for correctness, or as an explicitly scoped follow-up if the initial implementation limits itself to refs for aggregated sessions.

### 4.6 Plan section

Shows a bounded plan snapshot, not the full plan document.

Fields:

- plan state / presence
- plan version
- updated time
- repo count
- bounded excerpt from `Plan.OrchestratorPlan`
- FAQ count when useful
- action buttons when relevant

Behavior by state:

- `ingested`: `No plan yet`
- `planning`: `Plan in progress`
- `plan_review`: show excerpt + approval actions
- `approved`, `implementing`, `reviewing`, `completed`: show compact approved/final plan snapshot
- `failed`: show the last known plan summary if present, plus failure context if available

The overview must never inline the entire plan markdown.

### 4.7 Tasks section

Shows repo/sub-plan rows once a plan exists.

Primary row identity:

- repo / sub-plan

Row fields:

- repo name
- sub-plan status
- active/latest child session status when present
- harness name when known
- last activity
- concise note for waiting/interrupted/failed states

The overview should treat raw child session IDs as secondary detail only. Users think in repos and tasks, not opaque session IDs.

### 4.8 External lifecycle section

Initial ship scope:

- original tracker refs / source refs only
- no speculative PR/MR rows

Follow-up target:

- created PR/MR rows with ref, URL, open/ready status, and later actions
- external ticket state sync visibility when durably known

### 4.9 Recent activity section

Optional and intentionally small.

Goal:

- answer `what happened most recently?` without forcing a log drill-down

This can be as small as the three most recent meaningful events, for example:

- plan v2 generated 18m ago
- repo-b asked a question 3m ago
- repo-e interrupted 15m ago

This is lower priority than the sections above, but likely more valuable than git dirty-counts.

---

## 5. Actionability contract

This is the key behavioral rule for the feature.

### 5.1 Required rule

If the session needs a human decision to proceed, the overview must provide:

1. the blocking reason
2. enough context to make an informed decision
3. the action itself

Detail views remain useful, but they must not be mandatory to unblock work.

### 5.2 Overview vs overlay split

The overview should show decision summary.
Overlays should show decision evidence.
The action must be invokable from either place.

Examples:

- The overview shows a bounded plan excerpt and `Approve` / `Request changes` actions.
- A `Review plan` overlay can show the full plan markdown and richer context.
- The operator may approve from the overview directly, or open the overlay first and approve there.

### 5.3 Action-specific context requirements

#### Approving or revising a plan

Inline overview context must include at least:

- plan version
- updated time
- affected repo count
- short excerpt
- unresolved question count if any

Overlay context should include:

- full plan
- repo-by-repo sub-plan summaries
- FAQ/open-question content
- later: revision diff vs prior plan version if available

Actions available from overview:

- approve
- request changes
- reject if that action remains supported
- optional `View full plan` overlay trigger

#### Answering an open question

Inline overview context must include at least:

- question text
- affected repo/task
- when it was asked
- short rationale/context

Overlay context should include:

- fuller prompt
- recent relevant activity or transcript excerpt
- suggested options if available

Actions available from overview:

- answer
- optional inspect/details overlay trigger

#### Recovering from interruption/failure

Inline overview context must include at least:

- affected repo/task
- last update time
- concise failure/interruption reason

Overlay context should include:

- recent relevant transcript or event excerpt
- worktree/session identifiers if useful
- resume/retry implications

Actions available from overview:

- resume
- retry
- any supported acknowledge/cancel flow if applicable

#### Future PR/MR lifecycle actions

Inline overview context must include at least:

- repo
- PR/MR ref
- URL if available
- current external state

Overlay context should include:

- PR/MR title
- linked source refs
- review/open/draft state
- last update

Actions available from overview once supported:

- open/view
- mark ready / undraft
- merge if the workflow allows it
- sync or update external tracker state where supported

---

## 6. State-to-overview matrix

| Root state | Summary label | Plan section | Tasks section | Action-required section |
|------------|---------------|--------------|---------------|-------------------------|
| `ingested` | Ready to plan | `No plan yet` | hidden | optional future `Start planning` if surfaced |
| `planning` | Planning | `Plan in progress` | hidden by default | none unless planning fails or needs input |
| `plan_review` | Plan review needed | excerpt + version + review actions | optional pending repos if already known | visible |
| `approved` | Awaiting implementation | approved plan snapshot | visible, mostly pending | usually hidden |
| `implementing` | Implementing / Waiting / Interrupted | approved plan snapshot | primary content | visible when waiting/interrupted |
| `reviewing` | Under review | approved/final plan snapshot | review-oriented repo statuses | visible only if user action is needed |
| `completed` | Completed | final plan snapshot | all complete | hidden |
| `failed` | Failed | last known plan snapshot if any | failure rows if known | visible when recovery action exists |

Important precedence rule:

- `Action required` takes precedence over passive state copy.
- The presence of an open question or interruption must be reflected in both summary badges and the action-required block.

---

## 7. Proposed view model

Introduce a dedicated overview view model instead of composing the page ad hoc from state branches.

```go
type SessionOverviewModel struct {
    Header   OverviewHeader
    Actions  []OverviewActionCard
    Sources  []OverviewSourceItem
    Plan     OverviewPlan
    Tasks    []OverviewTaskRow
    External OverviewExternalLifecycle
    Activity []OverviewActivityItem
}
```

Suggested sub-structures:

```go
type OverviewHeader struct {
    ExternalID   string
    Title        string
    StatusLabel  string
    UpdatedAt    time.Time
    ProgressText string
    Badges       []string
}

type OverviewSourceItem struct {
    Provider string
    Ref      string
    Title    string
    Excerpt  string
    URL      string
}

type OverviewPlan struct {
    State      string
    Exists     bool
    Version    int
    UpdatedAt  time.Time
    RepoCount  int
    FAQCount   int
    Excerpt    []string
    Actionable bool
}

type OverviewTaskRow struct {
    RepoName       string
    TaskPlanStatus string
    SessionStatus  string
    HarnessName    string
    UpdatedAt      time.Time
    Note           string
    SessionID      string
}

type OverviewExternalLifecycle struct {
    TrackerRefs []string
    PullRequests []OverviewPRRow
    MergeRequests []OverviewPRRow
}
```

The exact names can vary; the important point is that the overview should be driven by one explicit mapping layer, not by a scattered sequence of per-state render tweaks.

---

## 8. Data sources and gaps

### Available now

These inputs already exist and are enough for the initial ship scope:

- root work item state, title, description, metadata: `internal/domain/work_item.go`
- child task/session status: `internal/domain/session.go`
- plan + sub-plans + FAQ: `internal/domain/plan.go`
- sidebar-friendly derived status semantics: `internal/tui/views/sidebar.go`
- tracker refs from metadata: `internal/tui/views/source_details_view.go`
- current routing seam for unified overview adoption: `internal/tui/views/app.go`

### Known gaps

#### Multi-source per-item summaries

Aggregate sessions currently keep `SourceItemIDs` and `tracker_refs`, but not a canonical per-source-item summary list. The overview must not infer individual source titles/descriptions by parsing the merged root description.

Preferred follow-up direction:

- persist a lightweight `source_summaries` structure for aggregated sessions containing provider, ref, title, excerpt, and optional URL
- render those summaries directly in both Overview and Source Details

#### Live planning preview

The orchestrator already knows planning `sessionID` and `SessionDraftPath`, but the overview currently has no dedicated planning-preview model.

Preferred direction:

- surface a bounded draft/progress snippet in overview while planning is active
- keep the live planning transcript available through task/session drill-down and/or overlay

#### PR/MR lifecycle durability

Repo lifecycle adapters can create and update PRs/MRs, but current tracking is not durably modeled for the UI:

- GitHub adapter tracks branch -> PR number in memory (`internal/adapter/github/adapter.go`)
- glab adapter tracks branch -> worktree entries in memory (`internal/adapter/glab/adapter.go`)
- completed view already has `MRInfo`, but app wiring does not populate it

Preferred direction:

- persist created PR/MR artifacts in a canonical store or durable metadata shape
- render them on the overview and completed views
- only enable overview-native PR actions once this data is durable and restart-safe

#### Git status badges

The TUI has a `GitClient` service available, but the current git-work plumbing is not yet a natural source of compact dirty-state counts. This should remain explicitly deferred.

---

## 9. Concrete TODO

### Phase 1 — Unify the overview surface

- [ ] Add a dedicated overview content mode/model in `internal/tui/views/content.go`.
- [ ] Route main work-item selection and task-pane `Overview` selection to the same overview renderer.
- [ ] Remove the current conceptual split where the root work item content is mostly state-specific while the task-pane `Overview` row is only a routing alias.
- [ ] Preserve `Source details` and task/session drill-down as separate surfaces.

### Phase 2 — Ship-first overview content

- [ ] Implement the summary section using existing root state and sidebar-style derived status semantics.
- [ ] Implement the source section using durable source data already available.
- [ ] For aggregated sessions without canonical per-source summaries, render provider + ref only rather than guessing titles/excerpts.
- [ ] Implement the plan snapshot section using bounded excerpts from `Plan.OrchestratorPlan`.
- [ ] Implement the repo/sub-plan task table using sub-plan order and latest/active child session state.
- [ ] Implement the minimal external lifecycle section using original tracker refs only.
- [ ] Keep the layout concise and stable across all root states.

### Phase 3 — Overview-native action surfaces

- [ ] Add an `Action required` section that appears whenever the session is blocked on the user.
- [ ] Surface plan review actions directly from overview.
- [ ] Surface open-question answering directly from overview.
- [ ] Surface interruption recovery actions directly from overview.
- [ ] Add overlays for deeper context so the user can inspect without leaving the overview.
- [ ] Ensure all required actions remain available without navigating to dedicated detail pages.

### Phase 4 — Planning enrichment

- [ ] Replace static planning placeholder copy with a bounded live planning snapshot on the overview.
- [ ] Reuse planning session ID / draft path plumbing already present in the orchestrator.
- [ ] Ensure planning progress remains visible both from overview and task/session drill-down.

### Phase 5 — Durable source summary enrichment

- [ ] Add a canonical persisted source-summary structure for aggregated source sessions.
- [ ] Populate it in adapters that create aggregated work items.
- [ ] Use it in overview and source-details rendering.
- [ ] Do not preserve any fallback that parses merged descriptions to simulate per-source items.

### Phase 6 — PR/MR lifecycle follow-up

- [ ] Add a durable UI-facing persistence model for created PR/MR artifacts.
- [ ] Record enough data to render repo, ref, URL, and current known state.
- [ ] Populate completed and overview surfaces from that durable data.
- [ ] Add overview-native PR/MR actions once durability and restart behavior are correct.
- [ ] Surface original ticket state and created PR/MR state together in the external lifecycle section.

### Phase 7 — Optional git/worktree health

- [ ] Only after the overview is in regular use, evaluate whether compact git dirty-state badges still add enough value.
- [ ] If yes, add explicit plumbing for repo cleanliness summaries rather than ad hoc shell calls.

### Phase 8 — Cleanup and cutover

- [ ] Remove obsolete content-routing assumptions that require separate state-specific summary pages where the overview now owns the job.
- [ ] Update tests and fixtures that assume the old routing/content model.
- [ ] Update docs once the implementation stabilizes.

---

## 10. Autonomous validation I can perform

These validations can be executed during implementation without new product decisions.

### TUI rendering validation

- add/extend tests showing that selecting a work item from the main sidebar and selecting `Overview` from the task sidebar render the same overview content
- add/extend tests for narrow widths and limited heights to ensure the overview remains within the requested terminal size
- verify that action-required cards, source rows, and task rows clip deterministically
- verify that overlays do not break the underlying layout contract

### Overview behavior validation

- add/extend tests showing that each root state maps to the correct overview sections
- verify that plan-review, question, and interruption states surface actionable controls from overview
- verify that selecting `Source details` or a task row still opens the dedicated drill-down surface

### Planning validation

- once planning enrichment lands, verify that active planning shows bounded live draft/progress context instead of placeholder copy
- verify that planning overview content remains readable while a planning child session is active

### Source summary validation

- add adapter/service tests for any new canonical source-summary persistence shape
- verify that aggregated sessions render multiple source rows without reverse-parsing the merged description

### External lifecycle validation

- once PR/MR durability lands, add tests showing that created PR/MR artifacts survive reload and render on overview/completed surfaces
- verify that external lifecycle rows stay associated with the correct repo/sub-plan/work item

### Suggested focused commands during implementation

These are the likely targeted validation commands, depending on the exact touched packages:

- `go test ./internal/tui/views`
- `go test ./internal/orchestrator/...`
- `go test ./internal/repository/sqlite ./internal/service`
- targeted adapter package tests if PR/MR/source-summary persistence changes touch adapter behavior

The operator should continue to prefer focused tests for the touched subsystems rather than broad project-wide runs unless the implementation scope truly requires it.

---

## 11. Acceptance criteria

### 11.1 Initial ship scope acceptance criteria

The initial overview feature is done when all of the following are true:

1. Selecting a root work item in the main `Sessions` sidebar renders the overview page.
2. Selecting the `Overview` row in the task sidebar renders the same overview page, not a divergent variant.
3. The overview page always shows a summary/header section with root status and last-update context.
4. The overview page shows source information for the root session using only durable source data.
5. For aggregated sessions without per-source-item summaries, the overview does not fabricate source titles/excerpts from merged descriptions.
6. The overview page shows a bounded plan snapshot whenever a plan exists.
7. The overview page shows repo/sub-plan task rows whenever a plan exists.
8. The overview page shows the current blocker state prominently when the work item is waiting for human action or is interrupted.
9. The layout remains within width and height constraints, including narrow terminal cases.
10. Existing drill-down behavior for `Source details` and task/session rows still works.
11. Focused tests covering the touched TUI behavior pass.

### 11.2 Actionability acceptance criteria

The overview control-surface behavior is done when all of the following are true:

1. Any human action required to unblock the session is invokable from the overview page.
2. Plan approval and plan-change actions are available from the overview page.
3. Open-question answering is available from the overview page.
4. Interruption recovery actions are available from the overview page.
5. The overview provides enough inline context that the operator can understand what they are deciding about.
6. A richer overlay is available whenever the inline summary is intentionally bounded.
7. The operator never has to navigate into a dedicated detail view just to perform a required action.

### 11.3 PR/MR lifecycle follow-up acceptance criteria

The PR/MR follow-up is done when all of the following are true:

1. Created PR/MR artifacts are stored durably enough to survive app reload/restart.
2. The overview page shows created PR/MR rows with repo, ref, and URL when known.
3. The completed surface also renders those PR/MR rows from the same durable source.
4. External lifecycle information clearly distinguishes source-ticket state from created PR/MR state.
5. Overview-native PR/MR actions use durable state rather than ephemeral adapter memory.
6. Focused tests covering adapter persistence, UI mapping, and rendering pass.

---

## 12. Risks and watch-outs

- The current content routing is heavily state-centric; partial overview adoption could create duplicate concepts unless the cutover is deliberate.
- Multi-source sessions need honest rendering. Faking per-source detail from a concatenated description will be brittle and misleading.
- Action-required context can easily bloat the overview if overlays are not used well.
- PR/MR lifecycle work will be tempting to shortcut through in-memory adapter state; that would make the UI unreliable after restart and should be rejected.
- The more dense the overview becomes, the more important the existing width/height layout tests become.

---

## 13. Explicitly rejected alternatives

### Keep separate state-specific content pages and just add more links between them

Rejected because it would:

- preserve the current conceptual fragmentation of the content pane
- keep the `Overview` row and main work-item selection only loosely equivalent
- force the operator to remember which state-specific page owns which action
- make future actionability and PR lifecycle work harder to reason about

### Put every detail inline on the overview page

Rejected because it would:

- destroy first-glance readability
- turn the overview into a noisy document dump
- create layout instability in narrow terminals
- duplicate the purpose of transcript/detail surfaces

The correct balance is:

- concise overview
- action-required cards when needed
- overlays for evidence-heavy inspection
- dedicated drill-down views for logs, detailed source metadata, and historical session interaction
