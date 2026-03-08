# Proposal: Converging Browse Filter Semantics Across Providers

## Status

Proposed.

## Why this exists

The unified work-item browser now exists, but its filter model is still only partially unified.

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

That is the right direction, but provider implementations still interpret only fragments of this shape:

- GitHub issues currently hardcode `filter=assigned` and `state=open`, only wiring `Search` and `Limit`
  - `internal/adapter/github/adapter.go:396-415`
- GitLab issues currently hardcode `scope=all`, only wiring generic search/pagination through `applyListOpts`
  - `internal/adapter/gitlab/adapter.go:327-335`
  - `internal/adapter/gitlab/adapter.go:651-661`
- Linear issues currently wire `TeamID` and `Search`, but not common concepts like view/state/labels/pagination
  - `internal/adapter/linear/adapter.go:78-107`

The result is that the UI exposes common controls, but the user cannot rely on those controls meaning the same thing across providers.

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
- Linear does not expose the same personal inbox model in the current adapter; it is primarily team-scoped browsing

### 2. State semantics differ

Desired UI concept:

- Open
- Closed
- All
- Optional provider-specific values when useful

Current reality:

- GitHub issues use `open`, `closed`, `all`
- GitLab issues use `opened`, `closed`, `all`
- Linear issue states are workspace-defined names, not portable open/closed enums

### 3. Container narrowing differs

Desired UI concept:

- Narrow issue browsing by container without requiring container configuration for global browsing

Current reality:

- GitHub uses `Owner` and `Repo`
- GitLab conceptually needs `Group` and project path, but current adapter only partially models this
- Linear uses `TeamID`

### 4. Pagination is structurally common but not behaviorally common

Desired UI concept:

- Next / previous page or cursor with reliable continuation behavior

Current reality:

- GitHub and GitLab mostly use page/per-page today
- `ListOpts` includes both `Offset` and `Cursor`, but providers do not expose a unified capability contract telling the UI which one is authoritative
- Linear currently returns all fetched results without a normalized paging contract in the adapter layer

### 5. Search semantics differ

Desired UI concept:

- Free-text search within the active scope and provider selection

Current reality:

- GitHub currently pushes `Search` into `q` on `/issues`, which is not equivalent to the other providers
- GitLab wires `search`
- Linear wires a team issue filter string

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

Linear should not fake this vocabulary. Instead:

- treat `assigned_to_me` as the only initially supported personal-inbox value if implemented
- otherwise treat browse as team-scoped and have the UI either hide `View` for Linear issues or show a provider-specific label such as `Team backlog`

This is the main place where honesty matters more than abstraction.

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
- Linear: no direct mapping unless the adapter can classify workflow states into open/closed buckets

### Layer 2: provider-native state

If a provider has useful native states beyond the portable layer:

- expose them as a provider-qualified secondary control or advanced filter
- carry them in `ListOpts.Metadata` until the UI/adapter contract earns a dedicated field

This avoids lying about Linear’s workflow states while still giving GitHub/GitLab a common baseline.

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
n
Do not force GitHub/GitLab semantics onto Linear.

Instead:

- keep `Search`
- keep `TeamID`
- decide whether `View` is hidden for Linear or narrowed to a Linear-specific subset
- consider a future `BrowseMode` value for team backlog vs assigned issues if Linear data can support it cleanly

## 8. Rollout sequence

1. Add browse filter capabilities to the adapter contract.
2. Teach the UI to render controls from capabilities instead of hardcoded global options.
3. Normalize GitHub issue view/state semantics.
4. Normalize GitLab issue view/state semantics.
5. Narrow or relabel Linear issue filtering honestly.
6. Disable or clearly annotate `All` mode for non-issue scopes until scope intersections are real.
7. Add adapter tests proving each provider honors the shared filters it claims to support.
8. Add TUI tests proving controls shown in a given provider/scope pair match capabilities.

## Concrete acceptance criteria

This proposal should be considered implemented when:

1. The UI does not show a filter that the active provider/scope cannot honor.
2. `View=assigned_to_me` means the same thing for GitHub and GitLab issue browsing.
3. `State=open|closed|all` means the same thing for GitHub and GitLab issue browsing.
4. Linear issue browsing is explicitly labeled/scoped so users are not misled into assuming GitHub/GitLab-style personal inbox semantics.
5. `All` mode only appears for scope/filter combinations with honest shared semantics.
6. Each provider has tests covering the shared filter fields it claims to implement.

## Recommendation

Do not try to fully unify every provider at once.

The pragmatic path is:

- normalize issue browsing semantics first
- make capabilities drive the UI
- stay explicit that Linear is team-centric while GitHub/GitLab are inbox-centric
- keep milestones and initiatives provider-qualified until a true shared model exists

That gets us semantic convergence without lying to the user or overfitting the abstraction.
