# Plan: Unified Work Item Browsing and Session Creation

## Status

Implemented for the current pre-release unified browsing scope.

## Decision Summary

We will replace the current provider-specific, first-adapter-only new-session flow with a unified browsing experience that lets a user access all work items they can reach from configured providers without leaving Substrate.

This includes:

1. Registering GitHub as a work item adapter.
2. Redesigning GitHub issue browsing from repo-scoped to user-accessible/global issue browsing.
3. Redesigning GitLab issue browsing from project-scoped to user-accessible/global issue browsing.
4. Reworking the TUI new-session overlay into a real multi-provider browsing flow.
5. Treating manual work item creation as a separate explicit action, not as a pseudo-provider tab.
6. Allowing breaking changes freely because no released version exists yet.

## Problem Statement

The current new-session workflow is too narrow:

- It uses only the first browse-capable adapter.
- The UI labels that browse source as "Linear" even when it is not Linear.
- It hardcodes scope to issues.
- It does not pass server-side search into adapters.
- It does not expose pagination, team/group/project/repo filters, or explicit source selection.
- GitHub work item browsing exists in code but is not wired in.
- GitLab browsing is incorrectly constrained to a configured project ID for issue discovery.

This is inconsistent with the product goal:

> Substrate should be the only window a person needs to have open to access all of the work items they might need to work on.

## Product Goals

1. A user can browse work from all configured providers directly in Substrate.
2. A user can explicitly choose one provider or browse across all providers.
3. A user can search and filter server-side, not only within a small locally fetched subset.
4. A user can browse more than just issues when the provider supports richer scopes.
5. The workflow is keyboard-first and fast enough to replace provider-specific browser tabs for day-to-day selection.
6. The model supports future provider additions without a second redesign.

## Non-Goals

1. Do not preserve compatibility with the current narrow config and UI behavior if that behavior blocks the redesign.
2. Do not implement mixed-provider multi-select aggregation in the first cut unless the semantics are clearly correct.
3. Do not attempt a perfect universal abstraction for every provider entity type before shipping a useful unified issue inbox.

## Current State

### Adapter registration

Current work item adapters are built in `internal/app/wire.go`.

- Manual is always registered.
- Linear is registered when configured.
- GitLab is registered when configured; global issue browsing no longer hard-requires `project_id`.
- GitHub is registered as a work item adapter when configured.

### TUI new-session behavior

Current new-session overlay behavior in `internal/tui/views/overlay_new_session.go`:

- The overlay exposes explicit provider selection: All / Linear / GitHub / GitLab.
- Scope selection is provider-aware, and `All` mode is honestly constrained to issues only.
- Search input is passed through to adapter `ListSelectable` requests.
- View, state, labels, owner/repo/group/team filters, and pagination hints are capability-driven rather than hardcoded by provider name.
- The user can multi-select items only within the same provider.
- Session creation dispatches to the selected provider adapter, not the first adapter matching a scope.

### GitHub current browsing model

GitHub issue browsing is global-by-default via the authenticated-user issue inbox API.

- browse scopes are issues and projects.
- issues support normalized views (`assigned_to_me`, `created_by_me`, `mentioned`, `subscribed`, `all`), normalized states, labels, search, owner/repo narrowing, and offset pagination.
- projects remain milestone-backed and repo-scoped.

### GitLab current browsing model

GitLab issue browsing is global-by-default via `/api/v4/issues`.

- browse scopes are issues, projects, and initiatives.
- issues support normalized views (`assigned_to_me`, `created_by_me`, `all`), normalized states, labels, search, repo/group narrowing, and offset pagination.
- projects remain milestone-backed and initiatives remain epic-backed, with capability exposure depending on available backing context.

### Linear current browsing model

Linear is now a capability-rich first-class provider rather than a team-only special case.

- browse scopes are issues, projects, and initiatives.
- issues support normalized views (`assigned_to_me`, `created_by_me`, `all`), normalized and native Linear states, labels, search, team narrowing, and cursor pagination.
- projects support state, search, team narrowing, and cursor pagination.
- initiatives support state, search, and cursor pagination.
## Product Design

## Core UX Model

The current "New Session" overlay will become a unified work browser.

### Top controls

1. **Source**
   - All
   - Linear
   - GitHub
   - GitLab
   - Manual action separate, not part of the source switch

2. **Scope**
   - Issues
   - Projects
   - Initiatives
   - Only show scopes supported by the active provider selection
   - In `All` mode, start with Issues only in v1

3. **Search**
   - Server-side when supported
   - Debounced reload

4. **Primary filter**
   - Assigned to me
   - Created by me
   - Mentioned / subscribed where provider supports it
   - All accessible

5. **Secondary filters**
   - State
   - Labels
   - Team / group / project / repo narrowing
   - Updated recently

6. **Pagination controls**
   - Next / previous page or infinite scroll
   - Must be adapter-backed, not local-only

### Main list

Each row should clearly display:

- Provider badge
- Item identifier
- Title
- Parent container (repo/project/team/group)
- State
- Labels
- Updated time

### Detail pane

Selected item preview should include:

- Description excerpt or full body
- Parent references
- Provider-specific metadata
- Source link if available

### Actions

- Create session from selected item
- Create manual work item
- Multi-select only when semantically valid

## Provider Semantics

### Linear

Linear remains a first-class provider and now carries the richest declared browse semantics in the system.

- Issues, projects, and initiatives remain supported.
- Team-aware browsing remains valid, but issue browsing also supports normalized personal views where the adapter can back them honestly.
- Search, state, labels, and pagination are adapter-backed rather than UI-only.
- Provider-native Linear state richness is exposed through capabilities instead of being flattened away.
### GitHub

GitHub issue browsing should become global-by-default.

#### Issues

Use the user-accessible issues endpoint:

- `GET /issues`

Support filters aligned with GitHub’s API:

- filter: assigned, created, mentioned, subscribed, repos, all
- state: open, closed, all
- labels
- since
- pagination

A repo filter can still narrow results, but should not be required to browse.

#### Projects scope

The current adapter uses milestones as `ScopeProjects`.

That can remain initially, but milestones are repo-scoped. Therefore:

- in provider mode `GitHub`, `ScopeProjects` should require repo context or an explicit narrowing filter
- in provider mode `All`, we should not pretend GitHub milestones are globally browsable unless we implement the right UX for container scoping

#### Initiatives

Still unsupported until a dedicated GitHub Projects v2 design exists.

### GitLab

GitLab issue browsing should become global-by-default.

#### Issues

Use the global issue inbox endpoint:

- `GET /issues?scope=all`

Support filters aligned with GitLab’s API:

- scope: assigned_to_me, created_by_me, all
- state
- labels
- search
- author / assignee where useful
- updated_after / updated_before
- pagination

A group or project filter can narrow results, but should not be required.

#### Projects scope

Current `ScopeProjects` maps to milestones.
Milestones are still naturally project-scoped.

So:

- `ScopeProjects` should require an explicit project or group narrowing context
- if absent, the UI should ask for or constrain the container rather than returning misleading partial data

#### Initiatives

Current `ScopeInitiatives` maps to epics.
Epics are group-scoped.

So:

- `ScopeInitiatives` should require or infer group context
- do not keep deriving epic capability only from a single configured project

### Manual

Manual work item creation becomes a separate action.

It should not occupy one of the provider tabs because it is not a browsable source.

## Data Model and Adapter Contract Changes

The current `adapter.ListOpts` is too narrow for unified browsing.

Current fields:

- `WorkspaceID`
- `Scope`
- `TeamID`
- `Search`
- `Limit`
- `Offset`

We should replace or extend it with a richer browse filter model.

Proposed shape:

```go
type BrowseFilters struct {
    Scope        domain.SelectionScope
    Search       string
    Limit        int
    Offset       int

    ViewMode     string   // assigned_to_me, created_by_me, mentioned, subscribed, all
    State        string   // provider-native mapping layer can interpret this
    Labels       []string

    TeamID       string
    TeamKey      string
    GroupID      string
    GroupPath    string
    ProjectID    string
    ProjectPath  string
    Owner        string
    Repo         string

    UpdatedAfter  *time.Time
    UpdatedBefore *time.Time
}
```

Possible direction for adapter interface evolution:

```go
type BrowseRequest struct {
    Provider string // optional when a concrete adapter handles the request
    Filters  BrowseFilters
}
```

And if needed later:

```go
type BrowsePage struct {
    Items      []ListItem
    TotalCount int
    HasMore    bool
    NextCursor string
}
```

We should also enrich `ListItem` with source-container metadata instead of overloading title strings.

Suggested additions:

```go
type ListItem struct {
    ID            string
    Title         string
    Description   string
    State         string
    Labels        []string
    Provider      string
    Identifier    string
    ContainerRef  string // repo, project path, team key, etc.
    URL           string
    ParentRef     *ParentRef
    CreatedAt     time.Time
    UpdatedAt     time.Time
}
```

## Configuration Changes

Because breaking changes are acceptable, we should simplify config toward the actual product model.

### GitHub config

Current required fields:

- owner
n- repo

These should stop being required for issue browsing.

New model:

- token/token_ref still required
- optional defaults:
  - default_owner
  - default_repo
  - default_issue_view
- repo lifecycle PR behavior may still need repo identity, but that belongs to lifecycle targeting, not browse capability

### GitLab config

Current required field:

- project_id

This should stop being required for issue browsing.

New model:

- token/token_ref still required
- base_url optional as today
- optional defaults:
  - default_group_id / default_group_path
  - default_project_id / default_project_path
  - default_issue_view
- project ID should remain valid as a narrowing default, not a global requirement

### Config split

If needed, split provider config into:

1. browsing defaults
2. lifecycle mutation defaults

That avoids forcing repo/project scope onto the browse UX.

## TUI Implementation Plan

## Phase 1 — Replace the overlay model

Files likely involved:

- `internal/tui/views/overlay_new_session.go`
- `internal/tui/views/msgs.go`
- `internal/tui/views/app.go`
- `internal/adapter/types.go`

Changes:

1. Replace `SourceLinear` / `SourceManual` with a real provider mode model.
2. Add explicit provider selection.
3. Add scope selection.
4. Add filter state in the overlay model.
5. Wire search input into adapter requests instead of local-only filtering.
6. Add pagination state.
7. Remove the "first browse-capable adapter" behavior.
8. Manual work item creation becomes an action path reachable from the browser.

## Phase 2 — Register GitHub browsing

Files likely involved:

- `internal/app/wire.go`
- `internal/config/config.go`
- tests for adapter registration

Changes:

1. Register GitHub as a work item adapter when GitHub credentials are available.
2. Remove tests that assert GitHub must not be registered.
3. Update settings connection checks if needed to align with broader browsing behavior.

## Phase 3 — Redesign GitHub issue browsing

Files likely involved:

- `internal/adapter/github/adapter.go`
- `internal/adapter/github/adapter_test.go`
- possibly config and docs

Changes:

1. Add global issue listing using `GET /issues`.
2. Preserve repository metadata on returned items.
3. Support provider-wide filters: assigned, created, mentioned, subscribed, all.
4. Support labels, state, since, limit, offset/page.
5. Keep repo-scoped issue browsing as a narrowing path when owner/repo filter is supplied.
6. Keep milestone browsing as scoped rather than pretending it is globally uniform.

## Phase 4 — Redesign GitLab issue browsing

Files likely involved:

- `internal/adapter/gitlab/adapter.go`
- `internal/adapter/gitlab/adapter_test.go`
- `internal/config/config.go`

Changes:

1. Add global issue listing using `GET /issues?scope=all`.
2. Preserve project path on returned items.
3. Support provider-wide filters: assigned_to_me, created_by_me, all.
4. Support labels, state, search, updated windows, pagination.
5. Keep project milestone browsing and group epic browsing as explicit narrowed scopes.
6. Remove `project_id` as a hard requirement for issue browsing.

## Phase 5 — Scope-aware browsing across providers

Files likely involved:

- overlay and adapter contract files
- Linear, GitHub, GitLab adapters

Changes:

1. In provider-specific mode, show only scopes that provider supports.
2. In `All` mode:
   - support unified issue browsing first
   - delay unified projects/initiatives view until container semantics are clear
3. Prevent mixed-provider multi-select resolution unless intentionally designed.
4. Allow multi-select within same provider and compatible scope.

## Phase 6 — Unify metadata and selection UX

Changes:

1. Make list rows provider-rich and container-aware.
2. Add preview/details panel.
3. Add keyboard-friendly filter cycling.
4. Add clear empty/loading/error states per provider.

## Implementation Plan

Because no release has shipped yet, we should implement the full redesign in one coherent branch rather than preserving intermediate compatibility shims.

### Order of implementation

1. **Contract and model update**
   - update adapter browse filter types
   - update list item metadata model
   - update selection flow to carry provider/container metadata where needed

2. **UI overhaul first enough to consume the new model**
   - provider picker
   - scope picker
   - search/filter controls
   - pagination state
   - manual action separation

3. **Adapter registration cleanup**
   - wire GitHub in
   - remove assumptions that only one browse provider exists

4. **GitHub browse redesign**
   - global issues first
   - scoped milestone browsing second

5. **GitLab browse redesign**
   - global issues first
   - scoped milestones/epics second

6. **Linear integration alignment**
   - complete Linear issue parity on views/state/labels/pagination
   - extend Linear project and initiative browsing with search/state/cursor semantics
   - keep overlay semantics adapter-first so richer Linear capabilities are discovered from declarations, not provider name checks

7. **Selection and resolution rules**
   - preserve same-provider multi-select for issue aggregation
   - reject invalid mixed-provider selections with explicit UI feedback

8. **Testing and verification**
   - adapter unit tests for global browsing filters and pagination
   - overlay tests for provider switching and search dispatch
   - end-to-end TUI flow tests for creating work items from each provider

### Breaking changes we should allow

1. Remove the assumption that GitLab browsing needs `project_id`.
2. Remove the assumption that GitHub browsing needs `owner` and `repo`.
3. Remove the `Linear`-named browse mode from the overlay.
4. Replace the first-browse-capable-adapter behavior completely.
5. Change adapter list/query contracts freely if that simplifies the final design.

### Verification matrix

At minimum, verify:

1. **GitHub**
   - browse assigned issues across multiple visible repos
   - search issues server-side
   - filter by state
   - create session from a selected GitHub issue

2. **GitLab**
   - browse accessible issues across multiple projects
   - search issues server-side
   - filter by state/scope
   - create session from a selected GitLab issue

3. **Linear**
   - browse issues/projects/initiatives
   - team filter works
   - issue views work: assigned_to_me / created_by_me / all
   - issue state, labels, search, and cursor pagination work
   - project and initiative search/state/cursor flows work

4. **Multi-provider UI**
   - source switch works
   - `All` mode works for issues
   - pagination works
   - empty/error states are provider-specific and understandable

5. **Manual**
   - manual work item creation still works via explicit action

### Risk areas

1. GitHub and GitLab non-issue scopes are not naturally global in the same way as issues.
2. Cross-provider aggregation semantics can become confusing if allowed too early.
3. Search behavior and result ordering differ across providers; the unified UI must not imply identical semantics when they are provider-native.
4. Provider-wide issue inboxes may produce large result sets; pagination and result metadata are mandatory.

## Recommended v1 after redesign

The first fully redesigned version should guarantee:

- Provider picker: All / Linear / GitHub / GitLab
- Scope picker: Issues always; additional scopes when valid
- Server-side search
- Provider-aware filters
- Pagination
- GitHub global issue inbox
- GitLab global issue inbox
- Linear expanded browsing with issue/project/initiative search and state semantics
- Manual creation as separate action
- No misleading labels
- No first-adapter-only behavior

## Why this is the right cut

This is the first version that matches the product claim that Substrate should be the only window needed to find work.

Anything less would continue preserving the current core mistake: treating provider browsing as a narrow implementation detail instead of a first-class workflow.
