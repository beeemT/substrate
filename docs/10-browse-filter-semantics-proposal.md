# Proposal: Converging Browse Filter Semantics Across Providers

## Status

Implemented for the current browse surface.

## Why this exists

The unified work-item browser now exists, and the current browse surface implements the first honest capability-driven filter model.

The current `adapter.ListOpts` contract already exposes a broad shared surface in `internal/adapter/types.go`:

- `Provider`
- `Scope`
- `Search`
- `Limit`
- `Offset`
- `View`
- `State`
- `Owner`
- `Repo`
- `Group`
- `Labels`
- `Cursor`
- `Sort`
- `Direction`
- `Metadata`

Provider implementations now consume meaningful subsets of that shape through declared capabilities instead of ad hoc UI assumptions:

- GitHub issues support normalized views/states plus labels/search/owner/repo/offset
- GitLab issues support normalized views/states plus labels/search/repo/group/offset
- Linear issues support normalized views, normalized and native states, labels/search/team, and cursor pagination
- Linear projects and initiatives now also expose search/state/cursor semantics

The remaining goal is to keep refining the UI and contract without losing honesty about provider differences.

## Goal

Bring provider filter semantics closer together without pretending the providers are identical.

The target is not a fake universal abstraction. The target is:

1. Common controls should mean the same thing whenever they are shown.
2. Unsupported controls should be hidden or disabled explicitly.
3. Provider-specific differences should be normalized into a documented compatibility layer.
4. The browser should stay honest about scope limitations for milestones, epics, and initiatives.

## Non-goals

1. Do not make every provider support every filter.
2. Do not collapse all provider-specific concepts into one lossy enum.
3. Do not block shipping on perfect parity for non-issue scopes.
4. Do not preserve today’s accidental semantics if they are misleading.

## Current semantic mismatches

### 1. Primary inbox/view filter is inconsistent

Desired UI concept:

- Assigned to me
- Created by me
- Mentioned
- Subscribed
- All accessible

Current reality:

- GitHub has a native `filter` query for issue inbox browsing with values like `assigned`, `created`, `mentioned`, `subscribed`, `repos`, `all`
- GitLab has `scope` values such as `assigned_to_me`, `created_by_me`, `all`; mentioned/subscribed are not symmetrical
- Linear now supports a narrower but real normalized view subset: `assigned_to_me`, `created_by_me`, `all`, while still remaining team-aware
### 2. State semantics differ

Desired UI concept:

- Open
- Closed
- All
- Optional provider-specific values when useful

Current reality:

- GitHub issues use `open`, `closed`, `all`
- GitLab issues use `opened`, `closed`, `all`
- Linear now classifies workflow states into normalized `open`/`closed` buckets while also exposing richer provider-native state names
### 3. Container narrowing differs

Desired UI concept:

- Narrow issue browsing by container without requiring container configuration for global browsing

Current reality:

- GitHub uses `Owner` and `Repo`
- GitLab uses `Group` and project path
- Linear uses `TeamID` as its primary narrowing container
### 4. Pagination is structurally common but not behaviorally common

Desired UI concept:

- Next / previous page or cursor with reliable continuation behavior

Current reality:

- GitHub and GitLab use offset/page-style pagination
- Linear now exposes cursor pagination through `HasMore` / `NextCursor`
- `BrowseFilterCapabilities` tells the UI whether offset or cursor semantics are actually supported
### 5. Search semantics differ

Desired UI concept:

- Free-text search within the active scope and provider selection

Current reality:

- GitHub pushes `Search` into its issue query model
- GitLab wires `search`
- Linear now wires search across issues, projects, and initiatives
## Proposed model

## 1. Split filters into three semantic tiers

### Tier A: truly common filters

These should be safe to show broadly for issue browsing:

- `Search`
- `View`
- `State`
- `Labels`
- `Limit`
- pagination (`Cursor` or `Offset`, but not both exposed blindly)

### Tier B: common but scope/provider-qualified filters

These should appear only when the active provider/scope supports them:

- `TeamID`
- `Owner`
- `Repo`
- `Group`
- `Sort`
- `Direction`

### Tier C: provider-specific escape hatches

These should live under `Metadata` until the shared contract earns a real field:

- GitHub `repos` issue filter mode
- GitLab author/assignee usernames
- Linear workflow-state names
- date-range filters like GitLab `updated_after`

This keeps the shared contract honest while preserving room for high-value provider-specific power.

## 2. Normalize around issue browsing first

The browser should explicitly treat `ScopeIssues` as the normalization baseline.

That means:

- `All providers` mode gets the richest filter parity only for issues
- project/milestone/initiative scopes remain provider-qualified
- the UI must not imply a universal milestone/initiative inbox if one does not exist

This matches the product reality already captured in `docs/09-unified-work-item-browsing.md`.

## 3. Define a normalized view vocabulary

Add a documented semantic mapping for `ListOpts.View`.

Normalized values:

- `assigned_to_me`
- `created_by_me`
- `mentioned`
- `subscribed`
- `all`

Provider mapping:

### GitHub

- `assigned_to_me` -> `filter=assigned`
- `created_by_me` -> `filter=created`
- `mentioned` -> `filter=mentioned`
- `subscribed` -> `filter=subscribed`
- `all` -> `filter=all`

### GitLab

- `assigned_to_me` -> `scope=assigned_to_me`
- `created_by_me` -> `scope=created_by_me`
- `all` -> `scope=all`
- `mentioned` -> unsupported in current issue endpoint semantics
- `subscribed` -> unsupported in current issue endpoint semantics

For unsupported values, the adapter should return `adapter.ErrBrowseNotSupported` only if the whole view is invalid for the current provider/scope. Prefer downgrading in the UI instead of silently changing semantics in the adapter.

### Linear

Linear should not fake GitHub/GitLab semantics, but it no longer needs to hide from the shared vocabulary entirely.

- `assigned_to_me`, `created_by_me`, and `all` are now honest supported issue views
- team/container-scoped semantics still matter and should be surfaced by capability-driven UI messaging when no richer inbox view exists
- provider-native Linear state names remain valuable beyond the normalized layer

## 4. Define a normalized state vocabulary

Use a two-layer state model.

### Layer 1: portable browser state

Shared UI values for issue browsing:

- `open`
- `closed`
- `all`

Provider mapping:

- GitHub: `open`, `closed`, `all`
- GitLab: `open` -> `opened`, `closed` -> `closed`, `all` -> `all`
- Linear: `open` -> active workflow state types, `closed` -> completed/cancelled, `all` -> no state restriction

### Layer 2: provider-native state

If a provider has useful native states beyond the portable layer:

- expose them through the same capability-driven control when the adapter declares them honestly
- keep the UI adapter-first: render what the current provider/scope declares instead of assuming GitHub/GitLab are the universal baseline

This lets Linear expose richer workflow states without lying about portability.

## 5. Add browse capabilities for filters

The missing piece in the current contract is not just more fields. It is discoverability.

Add a capability structure for browsing filters, for example:

```go
type BrowseFilterCapabilities struct {
    Views          []string
    States         []string
    SupportsLabels bool
    SupportsSearch bool
    SupportsCursor bool
    SupportsOffset bool
    SupportsOwner  bool
    SupportsRepo   bool
    SupportsGroup  bool
    SupportsTeam   bool
}
```

Then extend adapter capabilities along the lines of:

```go
type AdapterCapabilities struct {
    CanWatch        bool
    CanBrowse       bool
    CanMutate       bool
    BrowseScopes    []domain.SelectionScope
    BrowseFilters   map[domain.SelectionScope]BrowseFilterCapabilities
}
```

Why this matters:

- the UI can stop showing controls that the active provider/scope cannot honor
- `All` mode can compute the intersection of supported filters across active providers
- provider-specific controls can appear only when the current selection makes them meaningful

## 6. Explicit UI rules for `All` mode

For `Provider = All`:

### Issues

Show only the intersection-safe controls by default:

- Search
- View values supported by every active provider in issue mode
- portable state values supported by every active provider in issue mode
- pagination controls

Hide container-specific narrowing unless the UI is explicitly switched into advanced filtering.

### Projects / Initiatives

Do not attempt semantic unification by default.

Instead:

- either disable `All` for scopes where providers do not share honest semantics
- or show a clear banner that results are provider-specific and filtering is reduced

My recommendation: disable `All` for non-issue scopes until capability-driven filtering is in place.

## 7. Adapter implementation plan

### GitHub

Update `listIssues` in `internal/adapter/github/adapter.go` to honor:

- `View`
- `State`
- `Labels`
- `Offset`/page mapping
- optional repo narrowing when `Owner` and `Repo` are supplied

This is the provider closest to the desired common issue-inbox contract.

### GitLab

Update `listIssues` in `internal/adapter/gitlab/adapter.go` and `applyListOpts(...)` to honor:

- `View` mapped to GitLab `scope`
- normalized `State`
- `Labels`
- `Offset`/page mapping
- optional `Group` / project-path narrowing when implemented

GitLab should be the second provider brought into line because its issue inbox is also naturally global.

### Linear

Do not force GitHub/GitLab semantics onto Linear, but do let Linear advertise the semantics it can now back honestly.

Instead:

- keep `Search`
- keep `TeamID`
- support the implemented issue view subset (`assigned_to_me`, `created_by_me`, `all`)
- expose normalized and provider-native state values through capabilities
- keep room for future provider-native browse modes only if they are actually adapter-backed

## 8. Rollout sequence

1. Add browse filter capabilities to the adapter contract.
2. Teach the UI to render controls from capabilities instead of hardcoded global options.
3. Normalize GitHub issue view/state semantics.
4. Normalize GitLab issue view/state semantics.
5. Expand Linear issue semantics honestly.
6. Expand Linear non-issue search/state/pagination semantics.
7. Disable or clearly annotate `All` mode for non-issue scopes until scope intersections are real.
8. Add adapter tests proving each provider honors the shared filters it claims to support.
9. Add TUI tests proving controls shown in a given provider/scope pair match capabilities.

## Concrete acceptance criteria

This proposal should be considered implemented when:

1. The UI does not show a filter that the active provider/scope cannot honor.
2. `View=assigned_to_me` means the same thing for GitHub and GitLab issue browsing.
3. `State=open|closed|all` means the same thing for GitHub and GitLab issue browsing.
4. Linear issue browsing supports both normalized and provider-native state semantics without pretending they are identical to GitHub/GitLab.
5. `All` mode only appears for scope/filter combinations with honest shared semantics.
6. Each provider has tests covering the shared filter fields it claims to implement.

## Recommendation

Do not try to fully unify every provider at once.

The pragmatic path is:

- normalize issue browsing semantics first
- make capabilities drive the UI
- let adapters surface their strongest honest semantics instead of flattening everything to the weakest provider
- keep milestones and initiatives provider-qualified until a true shared model exists

That gets us semantic convergence without lying to the user or overfitting the abstraction.
