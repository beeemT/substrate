# 09 - Sentry Work Item Adapter Plan
<!-- docs:last-integrated-commit 5e781aa42d618e68f9968f210ffdab5acb733435 -->

## Status

Proposed. This document is the implementation plan for adding Sentry as a new work item source.

## Goal

Add a new `internal/adapter/sentry` package that implements `adapter.WorkItemAdapter` for Sentry issues and nothing else.

The adapter should let Substrate:
- browse Sentry issues during new-session creation
- resolve one or more selected Sentry issues into a `domain.WorkItem`
- fetch a Sentry issue again by stable external ID
- expose Sentry configuration and connection testing through the existing settings surface

The adapter should **not** participate in repository lifecycle automation.

## Explicit non-goals

This work item does **not** include:
- any `RepoLifecycleAdapter` for Sentry
- any worktree / branch / PR / MR creation behavior
- any `OnEvent` side effects for planning, review, or completion events
- any mutation back to Sentry state or comments
- any watch-based auto-assignment or background polling in v1
- any repository-flow coupling in `internal/app/remotedetect` or `BuildRepoLifecycleAdapters`
- any provider-specific browse controls beyond the existing shared browse UI

This is intentionally a source-only adapter.

## Why this fits the current architecture

The codebase already separates external systems into two different roles:

- `internal/adapter/interfaces.go`
  - `WorkItemAdapter` owns browse / resolve / fetch / watch / mutate hooks for tracker-like systems
  - `RepoLifecycleAdapter` owns repository event handling such as worktree-created flows
- `internal/app/wire.go`
  - `BuildWorkItemAdapters(...)` registers tracker sources
  - `BuildRepoLifecycleAdapters(...)` registers repo-host-specific automation separately
- `internal/tui/views/overlay_new_session.go`
  - the new-session browser is capability-driven; a new provider only needs honest `Capabilities()` plus `ListSelectable` / `Resolve`
- `internal/tui/views/settings_service.go`
  - provider config, provider status, and provider test actions already exist for Linear / GitHub / GitLab

Sentry belongs in the first bucket only. Treating it as a work-item source and **not** a repo-lifecycle system preserves the architecture instead of forcing a fake repository abstraction onto an error-tracking product.

## External API basis

Use Sentry's REST API, not a repo-host workflow.

Primary references:
- List organization issues: `GET /api/0/organizations/{organization}/issues/`
  - https://docs.sentry.io/api/events/list-an-organizations-issues/
- Retrieve one issue: `GET /api/0/organizations/{organization}/issues/{issue_id}/`
  - https://docs.sentry.io/api/events/retrieve-an-issue/
- Pagination via `Link` headers / cursor tokens
  - https://docs.sentry.io/api/pagination/
- Issue search syntax / issue properties
  - https://docs.sentry.io/concepts/search/searchable-properties/issues/
- API auth / bearer token usage
  - https://docs.sentry.io/api/auth/
- Token types and scopes
  - https://docs.sentry.io/account/auth-tokens/
  - https://docs.sentry.io/api/permissions/

Implementation choice:
- use bearer-token auth over HTTPS
- use the organization issues endpoint, not the deprecated project issues endpoint
- rely on cursor pagination from the response `Link` header because the browse UI already supports cursor-backed providers

## Target end state

### 1. New config block

Add a new provider config section under `adapters`:

```yaml
adapters:
  sentry:
    token_ref: keychain:sentry.token
    base_url: https://sentry.io/api/0
    organization: my-org
    projects:
      - web
      - api
```

Planned Go shape:

```go
type SentryConfig struct {
    TokenRef     string   `yaml:"token_ref"`
    Token        string   `yaml:"-"`
    BaseURL      string   `yaml:"base_url"`
    Organization string   `yaml:"organization"`
    Projects     []string `yaml:"projects"`
}
```

Notes:
- `base_url` should default to `https://sentry.io/api/0`
- `organization` is required for any live request
- `projects` is optional and acts as an allowlist / scope limiter for browsing
- secret handling should follow the existing keychain-backed provider pattern used by Linear / GitHub / GitLab

### 2. New adapter package

Add `internal/adapter/sentry/` with the same shape used by other providers:
- `doc.go`
- `adapter.go`
- `types.go` or `client.go` as needed
- `adapter_test.go`

### 3. Honest capability contract

The adapter should declare itself as browse-and-resolve only:

```go
func (a *SentryAdapter) Capabilities() adapter.AdapterCapabilities {
    return adapter.AdapterCapabilities{
        CanWatch:   false,
        CanBrowse:  true,
        CanMutate:  false,
        BrowseScopes: []domain.SelectionScope{domain.ScopeIssues},
        BrowseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
            domain.ScopeIssues: {
                Views:          []string{"assigned_to_me", "all"},
                States:         []string{"unresolved", "for_review", "regressed", "escalating", "resolved", "archived"},
                SupportsSearch: true,
                SupportsCursor: true,
                SupportsRepo:   true,
            },
        },
    }
}
```

Important decisions:
- **issues only** in v1
- reuse the shared `repo` filter as the Sentry project selector; update the field placeholder from `Repository / project path…` to `Repository / Project…`
- owner and labels remain unsupported for Sentry and should be disabled or hidden in place when Sentry is selected, without causing overlay resize or choppy reflow
- no fake repo-lifecycle abstraction for Sentry
- no new Sentry-only browse controls in v1; stay within the shared overlay filter model

## Browse model

### Selection scope

Only `domain.ScopeIssues` is supported.

### ListSelectable behavior

`ListSelectable` should call the organization issues endpoint and translate the existing generic browse controls into Sentry query terms:

- view `assigned_to_me` -> add `assigned:me`
- view `all` -> add no assignment clause
- state `unresolved` -> add `is:unresolved`
- state `for_review` -> add `is:for_review`
- state `regressed` -> add `is:regressed`
- state `escalating` -> add `is:escalating`
- state `resolved` -> add `is:resolved`
- state `archived` -> add `is:archived`
- free-text search -> append the raw search text to the query expression
- repo -> map to a Sentry project constraint; prefer a first-class project request parameter when the endpoint supports it cleanly, otherwise append `project:<value>` to the Sentry query string
- owner -> no mapping; disable or hide this control in place when the Sentry provider is selected
- labels -> no mapping; disable or hide this control in place when the Sentry provider is selected
- configured `projects` -> constrain the request to those projects as an allowlist
- cursor pagination -> parse / forward Sentry cursor values from the `Link` header

The browse UI should not gain new Sentry-only controls in this work item. When Sentry is selected, keep the overlay dimensions stable: reuse the shared repo field with placeholder `Repository / Project…`, and disable or hide owner / labels without resizing the overlay.

### List item mapping

Each `adapter.ListItem` should include enough context for the current detail pane:
- `ID`: provider-internal selection ID derived from the Sentry issue ID
- `Identifier`: Sentry short issue code if available; otherwise numeric issue ID
- `Title`: Sentry issue title
- `Description`: concise summary built from culprit / level / status / last seen / permalink
- `State`: Sentry issue status token
- `Provider`: `"sentry"`
- `ContainerRef`: project slug or project name when available
- `URL`: Sentry issue permalink
- `Metadata`: raw fields needed later by `Resolve` / `Fetch` if useful

## Resolve and fetch model

### External ID format

Use a self-describing Substrate external ID that can be parsed without extra state:

```text
SEN-{organization}-{issueID}
```

Example:

```text
SEN-acme-123456789
```

Rationale:
- stable across sessions
- scoped strongly enough for `Fetch`
- matches the existing pattern where external IDs are provider-prefixed and parseable

### Resolve behavior

For one selected Sentry issue:
- fetch or reuse the full issue payload
- create a `domain.WorkItem` with `Source = "sentry"`
- set `SourceScope = domain.ScopeIssues`
- set `SourceItemIDs` to the selected Sentry issue IDs
- place durable source facts into description / metadata rather than inventing repo semantics

For multiple selected Sentry issues:
- follow the existing multi-selection pattern already used by tracker adapters
- title should be the first issue title plus `(+N more)`
- description should become a structured aggregate with one section per issue
- each section should include short ID, project, status, culprit, counts, and permalink

### Fetch behavior

`Fetch` should parse `SEN-{organization}-{issueID}` and call the issue-detail endpoint.

Even though this adapter is source-only, `Fetch` is still worth implementing so the system can rehydrate a Sentry-backed work item from its external ID without relying on stale browse results.

## Unsupported operations behavior

Because this adapter is intentionally source-only, unsupported operations should mirror that design instead of pretending to work.

Planned behavior:
- `Watch(...)` returns a closed channel and no error
- `UpdateState(...)` is a no-op and returns `nil`
- `AddComment(...)` is a no-op and returns `nil`
- `OnEvent(...)` is a no-op and returns `nil`

That keeps the adapter compatible with the existing runtime shape while making the absence of repo-flow integration explicit in both capability flags and behavior.

## Wiring plan

### Phase 1 - Config and secrets

Files:
- `internal/config/config.go`
- `internal/config/config_test.go`
- `internal/config/secrets.go`

Changes:
- add `SentryConfig` to `config.Config.Adapters`
- add default `base_url`
- validate `base_url` like other URL-backed providers
- add keychain secret mapping for `adapters.sentry.token`
- cover load / defaults / secret hydration tests

### Phase 2 - Sentry adapter package

Files:
- `internal/adapter/sentry/doc.go`
- `internal/adapter/sentry/adapter.go`
- `internal/adapter/sentry/adapter_test.go`
- optional helper file(s) for HTTP client, response types, and cursor parsing

Changes:
- implement constructor and provider client setup
- implement `Capabilities`, `ListSelectable`, `Resolve`, `Fetch`, and source-only no-op methods
- normalize Sentry payloads into `adapter.ListItem` and `domain.WorkItem`
- centralize query construction and cursor parsing so tests can pin it precisely

### Phase 3 - App wiring and settings

Files:
- `internal/app/wire.go`
- `internal/app/wire_test.go`
- `internal/tui/views/settings_service.go`
- `internal/tui/views/settings_service_test.go`
- `internal/tui/views/app_browse_test.go`

Changes:
- register the Sentry adapter from `BuildWorkItemAdapters(...)` when config is complete
- do **not** touch `BuildRepoLifecycleAdapters(...)`
- add settings section for provider configuration
- add provider status entry and provider test flow (`ListSelectable(..., Limit: 1)` like existing providers)
- keep login manual for now; no harness-mediated Sentry login is required in this work item
- update the shared browse overlay so the repo field placeholder reads `Repository / Project…`
- when the Sentry provider is active, map the repo field to Sentry project filtering
- when the Sentry provider is active, disable or hide owner / labels in place without changing overlay dimensions
- add browse tests that show Sentry appears as a provider with the intended states / views and stable filter layout

### Phase 4 - Documentation follow-through after implementation

Files:
- `docs/04-adapters.md`
- optionally `docs/07-implementation-plan.md` if the roadmap should reflect shipped status

Changes:
- document Sentry as a work-item adapter only
- explicitly call out that there is no repo-lifecycle integration
- document config shape, supported browse filters, external ID format, and source-only behavior

## Verification plan

### Unit tests

Add focused tests for:
- config defaults and secret loading for `adapters.sentry`
- adapter registration only when required Sentry config is present
- Sentry query generation from view / state / search / repo combinations
- cursor parsing from `Link` headers
- issue list response mapping into `adapter.ListResult`
- single-item resolve
- multi-item aggregate resolve
- fetch by `SEN-{organization}-{issueID}`
- source-only no-op behavior for watch / mutate / event hooks

### Settings tests

Add tests proving:
- Sentry provider section renders in settings
- provider status reflects configured vs unconfigured token state
- provider test marks the adapter connected when `ListSelectable(..., Limit: 1)` succeeds
- provider test surfaces API errors cleanly

### UI / browse tests

Add tests proving:
- Sentry appears as a browse provider when configured
- Sentry only exposes `ScopeIssues`
- the shared repo field is available for Sentry project filtering and uses placeholder text `Repository / Project…`
- owner and labels are disabled or hidden when Sentry is selected without resizing the overlay
- Sentry-specific state options appear and unsupported controls stay hidden
- cursor pagination integrates cleanly with the existing browse overlay state machine

## Key design decisions already made

These choices should be treated as settled for this work item:

1. **Sentry is a work-item source, not a repo-lifecycle system.**
2. **No repository-flow integration belongs in this adapter.**
3. **No watch / mutate behavior in v1.**
4. **No new provider-specific browse UI controls in v1; reuse the shared repo filter as the Sentry project selector.**
5. **Use organization issues API plus cursor pagination, not deprecated project issues endpoints.**
6. **Project scoping should come from config allowlists plus the shared repo filter, not repo lifecycle integration.**

## Definition of done

This work item is done when:
- Substrate can browse Sentry issues from the new-session flow
- a selected Sentry issue resolves into a `domain.WorkItem`
- the adapter can re-fetch issue details by stable external ID
- the settings UI can configure and test the provider
- no Sentry code is wired into repo lifecycle flows
- tests prove the source-only contract and browse behavior
