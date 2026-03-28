# 04 - Adapter Implementations
<!-- docs:last-integrated-commit 15191d7174f9fd07787eb39e2a4763fb6c43cfeb -->

Concrete adapter behavior as implemented under `internal/adapter/` and wired from `internal/app/`.

---

## 1. Shared contracts

`internal/adapter/interfaces.go` defines four adapter roles:

- `WorkItemAdapter`: tracker-style sources used by the new-session flow and by tracker synchronization hooks.
- `RepoLifecycleAdapter`: repository event handlers used for MR/PR lifecycle work after a worktree exists.
- `AgentHarness`: execution backends for planning / implementation / review / foreman sessions. `StartSession` returns an `AgentSession` that supports streaming, messaging, and steering. Key session and capability contracts:
  - `Steer(ctx, msg) error` — interrupts active streaming to inject a steering prompt. Harnesses that lack this capability return `ErrSteerNotSupported` (defined in `internal/adapter/interfaces.go`).
  - `SessionOpts.ResumeSessionFile` — when set, the harness resumes from an existing session file rather than creating a fresh session.
  - `HarnessCapabilities.SupportsNativeResume` — advertises whether the harness supports session file resume. Used by the orchestrator to decide between native resume and fresh-session follow-up.
  - `HarnessCapabilities.SupportedTools` — lists the tool names a harness exposes to the orchestrator.
- `HarnessActionRunner`: executes short-lived control-plane actions (login, auth checks) via `RunAction(ctx, HarnessActionRequest)`. Used for provider login flows routed through the harness.

### Work item contract

`WorkItemAdapter.Resolve(...)` and `Fetch(...)` now return `domain.Session`, not a separate work-item type. User-facing flows still talk about creating a session, while the adapter contract stores the selected source data on the resulting `domain.Session`.

`internal/adapter/types.go` is the source of truth for browse and watch payloads:

- `AdapterCapabilities` declares whether a provider can browse, watch, or mutate.
- `BrowseScopes` and `BrowseFilters` describe the exact shared UI controls a provider/scope can honor.
- `WorkItemEvent` carries a `domain.Session` plus a string `Type`.
- `HarnessCapabilities` declares streaming, messaging, native resume, and supported tools.
- `HarnessActionRequest` / `HarnessActionResult` carry control-plane action inputs and outputs for `HarnessActionRunner.RunAction(...)`. Requests include `Action`, `Provider`, `HarnessName`, and optional `Inputs`.

Current watch implementations use these event types in practice:

- `"created"` when a newly observed item appears
- `"updated"` when a tracked item changes state
- `"error"` as a polling failure sentinel in the Linear, GitHub, and GitLab adapters

The type comment still mentions `created` / `updated` / `deleted`, but the runtime behavior above is what the shipped adapters emit today.

### Review artifact persistence (dual-write)

`internal/adapter/review_artifact_event.go` provides a shared dual-write pattern for PR and MR persistence. All three repo-lifecycle adapters (GitHub, glab, GitLab work-item adapter) use these functions to persist review artifacts.

`ReviewArtifactRepos` bundles the service dependencies needed for dual-write:

- `Events` (`EventService`) — audit trail
- `GithubPRs` (`GithubPRService`) — GitHub PR table
- `GitlabMRs` (`GitlabMRService`) — GitLab MR table
- `SessionArtifacts` (`SessionReviewArtifactService`) — cross-provider link table

Each PR/MR persistence function performs three writes:

1. `PersistReviewArtifact(...)` — writes a `ReviewArtifactRecorded` event (audit trail)
2. Upsert into the provider-specific table (`GithubPRs` or `GitlabMRs`)
3. Upsert into the session review artifact link table (`SessionArtifacts`) connecting the work item to the provider artifact

`domain.SourceSummary` is a durable per-source-item snapshot stored on `domain.Session.Metadata` under the `source_summaries` key. Each adapter populates these during resolve so the session retains structured metadata about its source items (title, state, URL, container, labels, timestamps).

---

## 2. Work item adapters

### Manual (`internal/adapter/manual`)

Manual is the only adapter that is always registered by `BuildWorkItemAdapters(...)`.

Capabilities:

- `CanBrowse=false`
- `CanWatch=false`
- `CanMutate=false`

Behavior:

- `Resolve(...)` requires `Selection.Manual` and creates a `domain.Session` with:
  - `Source="manual"`
  - `SourceScope=domain.ScopeManual`
  - sequential external IDs `MAN-1`, `MAN-2`, ... scoped by workspace
- `ListSelectable(...)` and `Fetch(...)` are unsupported
- `Watch(...)` returns a closed channel
- `UpdateState(...)`, `AddComment(...)`, and `OnEvent(...)` are no-ops

### Linear (`internal/adapter/linear`)

Registered when `cfg.Adapters.Linear.APIKey` is populated.

Capabilities:

- browse: issues, projects, initiatives
- watch: yes
- mutate: yes
- issue filters: view, state, labels, search, cursor, team
- project filters: state, search, cursor, team
- initiative filters: state, search, cursor

Current scope model:

- `ScopeIssues`: browse Linear issues, resolve one or many issues into a session
- `ScopeProjects`: browse projects, then resolve selected projects into a session whose description contains per-project sections
- `ScopeInitiatives`: browse initiatives, resolve exactly one initiative into a session with initiative/project detail

Current IDs and mutation behavior:

- issue external IDs: `LIN-{TEAM}-{NUMBER}`
- project external IDs: `LIN-PRJ-{prefix}`
- initiative external IDs: `LIN-INIT-{prefix}`
- `Fetch(...)` only rehydrates issue-backed IDs (`LIN-{TEAM}-{NUMBER}`)
- `UpdateState(...)` maps `domain.TrackerState` through configured `state_mappings`
- `AddComment(...)` resolves the Substrate external ID back to the Linear internal UUID before mutation

Watch behavior:

- polls assigned issues on `poll_interval` (default from config is `30s`)
- resolves the viewer/assignee identity once up front
- emits `created`, `updated`, and `error`
- backs off exponentially on rate limiting and resets the interval after success

GraphQL query construction (`internal/adapter/linear/queries.go`):

- queries are built dynamically via `buildIssueQuery(...)`, `buildProjectQuery(...)`, and `buildInitiativeQuery(...)` — null filter values are omitted from the query entirely rather than passed as GraphQL nulls (Linear interprets null comparators as "field must be null", not "skip this filter")
- `stripNilVars(...)` removes nil-valued entries from the variable map so only variables declared in the query are sent
- entity IDs use the GraphQL `ID` type (not `String`) for `teamId`, `assigneeId`, `issueId`, etc.
- `teamId` is optional: the adapter falls back to `cfg.TeamID` when `opts.TeamID` is empty, and `optionalString(...)` maps an empty string to nil so the team filter is omitted entirely when unset
- cancelled state uses Linear's `canceled` spelling (not `cancelled`); the `linearIssueStateTypes` mapping converts the user-facing `cancelled` filter value to `canceled`

Error handling (`internal/adapter/linear/client.go`):

- non-OK HTTP responses include the response body (capped at 512 bytes) in the error message
- HTTP 429 returns `ErrRateLimited` for the watch loop's exponential backoff

Event handling:

- `plan.approved` -> move the tracked issue to `in_progress`
- `work_item.completed` -> move the tracked issue to `done`

### GitLab (`internal/adapter/gitlab`)

Registered when `cfg.Adapters.GitLab.Token` is populated and adapter construction succeeds.

This is a work-item adapter only. GitLab repository lifecycle automation lives in the separate `glab` adapter.

Capabilities:

- browse: issues, projects, initiatives
- watch: yes
- mutate: yes
- issue filters: view (`assigned_to_me`, `created_by_me`, `all`), state, labels, search, offset, repo, group
- project filters: offset, repo
- initiative filters: offset, group

Current scope mapping:

- `ScopeIssues` -> GitLab issues
- `ScopeProjects` -> GitLab milestones
- `ScopeInitiatives` -> GitLab epics

Resolve behavior:

- issues: fetch each selected issue; multi-select aggregates them into one session
- projects: requires `project_id` metadata so selected milestone IDs can be resolved correctly
- initiatives: requires `group_id` metadata and exactly one epic selection

IDs and mutation behavior:

- issue external IDs: `gl:issue:{projectID}#{iid}`
- milestone sessions use `gl:milestone:{projectID}`
- epic sessions use `gl:epic:{iid}`
- `Fetch(...)` only supports issue external IDs
- `UpdateState(...)` maps tracker state to GitLab `state_event`
- `AddComment(...)` posts issue notes

Watch behavior:

- polls assigned opened issues on the configured poll interval
- emits `created`, `updated`, and `error`

Event handling:

- `plan.approved` -> add the rendered plan comment to every referenced external ID, then move the primary `external_id` to `in_progress`
- `work_item.completed` -> move the tracked issue to `done`

### GitHub (`internal/adapter/github`)

Registered when `config.GitHubAuthConfigured(...)` is true and adapter construction succeeds. Auth may come from config or an authenticated `gh` CLI.

`GithubAdapter` is the only dual-role adapter in the tree: it implements both `WorkItemAdapter` and `RepoLifecycleAdapter`.

Work-item capabilities:

- browse: issues, projects
- watch: yes
- mutate: yes
- issue filters: view (`assigned_to_me`, `created_by_me`, `mentioned`, `subscribed`, `all`), state, labels, search, offset, owner, repo
- project filters: offset, repo

Current scope mapping:

- `ScopeIssues` -> GitHub issues
- `ScopeProjects` -> repository milestones
- `ScopeInitiatives` is not implemented; `ListSelectable(...)` returns `adapter.ErrBrowseNotSupported`

IDs and mutation behavior:

- issue external IDs: `gh:issue:{owner}/{repo}#{number}`
- milestone sessions use `gh:milestone:{owner}/{repo}`
- `Fetch(...)` only supports issue external IDs
- `UpdateState(...)` maps tracker state to the configured GitHub issue state string
- `AddComment(...)` posts issue comments

Watch behavior:

- polls assigned open issues on the configured/default poll interval
- emits `created`, `updated`, and `error`

Event handling across its two roles:

- `plan.approved` -> add plan comments to referenced issues, then move the primary issue to `in_progress`
- `worktree.created` -> repository-lifecycle path for PR discovery / creation on GitHub remotes
- `work_item.completed` -> attempt to move the issue to `done` and run PR completion handling

### Sentry (`internal/adapter/sentry`)

Sentry is a shipped, source-only work-item adapter. It supports issue browsing during session creation, resolving one or more selected issues into a `domain.Session`, fetching a Sentry-backed session again from its external ID, and exposing auth, login, and connectivity checks through Settings. It does not participate in repository lifecycle automation.

#### Config and auth model

Registered by `BuildWorkItemAdapters(...)` when `config.SentryAuthConfigured(...)` is true and `sentryadapter.New(...)` succeeds.

`adapters.sentry` config fields are:

```yaml
adapters:
  sentry:
    token_ref: keychain:sentry.token
    base_url: https://sentry.io/api/0
    organization: acme
    projects:
      - web
      - api
```

`internal/config/config.go` shape:

```go
type SentryConfig struct {
    TokenRef        string   `yaml:"token_ref"`
    Token           string   `yaml:"-"`
    BaseURL         string   `yaml:"base_url"`
    BaseURLExplicit bool     `yaml:"-"`
    Organization    string   `yaml:"organization"`
    Projects        []string `yaml:"projects"`
}
```

Base URL behavior:

- empty -> `https://sentry.io/api/0`
- `https://sentry.io` -> `https://sentry.io/api/0`
- self-hosted roots such as `https://sentry.example.com/self-hosted` -> `https://sentry.example.com/self-hosted/api/0`
- if `base_url` was not explicitly set in YAML, `ResolveSentryContext(...)` may inherit `SENTRY_URL` from the environment
- if the default SaaS host was explicitly configured, ambient `SENTRY_URL` must not override it

Auth-source precedence (`config.SentryAuthSource`):

1. config token (`cfg.Adapters.Sentry.Token`)
2. `SENTRY_AUTH_TOKEN`
3. keychain-reference status (`token_ref`)
4. authenticated `sentry auth status` (CLI)

`config.SentryAuthConfigured(...)` returns true for any source above except `unset`. Construction still requires a resolved organization plus usable runtime credentials:

- with a token source, the adapter uses direct HTTP bearer auth
- with no token but authenticated Sentry CLI, the adapter switches to a CLI-backed transport that shells out to `sentry api ... --verbose`
- with neither, construction fails
- with missing organization, the adapter attempts `resolveOrganizationFromCLI(...)` which runs `sentry org list --json`; if exactly one org is found it is used, if zero or multiple are found construction fails
- if the CLI is unavailable or org resolution fails, construction fails even if auth exists


CLI-backed transport response parsing (`parseCLIResponse`):

- `--verbose` format: stderr debug lines containing the \u2699 marker; response headers use \u2699 followed by `< key: value`, status uses \u2699 followed by `< HTTP NNN`; body is non-marker lines
- legacy `--include` format: HTTP status line + headers, blank line, then body
- plain JSON output with no metadata: assumed HTTP 200 OK
- `config.ListSentryCLIOrganizations(...)` exposes org list resolution for use by the TUI without constructing the full adapter

#### Browse contract

Capabilities:

- browse: issues only
- watch: no
- mutate: no
- issue filters: view (`assigned_to_me`, `all`), state (`unresolved`, `for_review`, `regressed`, `escalating`, `resolved`, `archived`), search, cursor, repo
- no labels filter
- no owner filter

Filter mapping in `ListSelectable(...)`:

- `View=assigned_to_me` -> `assigned:me`
- `View=all` -> no assignment clause
- `State=*` -> `is:<state>`
- `Search` -> appended raw to the Sentry query string
- `Repo` -> reused as the Sentry project selector
- configured `projects` -> enforced as an allowlist
- if the UI asks for a project outside the allowlist, the adapter returns an empty result instead of broadening scope
- `Cursor` -> forwarded to Sentry and parsed back out of the `Link` header

Provider-specific UI implications:

- Sentry remains an issues-only provider
- the shared repo field acts as project selection for Sentry
- owner and label controls stay hidden
- switching across the Sentry/non-Sentry boundary clears the repo/project filter to avoid leaking values between providers
- layout stays stable when switching providers

#### Resolve and fetch behavior

`Resolve(...)` only supports `domain.ScopeIssues`, and both resolve and fetch return `domain.Session`.

Single-issue resolution produces a session with:

- `Source="sentry"`
- `SourceScope=domain.ScopeIssues`
- `SourceItemIDs=[]string{issueID}`
- `State=domain.SessionIngested`
- durable metadata for organization, identifiers, project slugs, permalinks, and `tracker_refs`

Multi-select resolution:

- fetches each selected issue individually
- keeps the first issue as the canonical title base
- formats `Title` as `first title (+N more)`
- joins per-issue sections with `---`
- preserves every selected issue ID in `SourceItemIDs`

Stable Sentry external IDs use:

```text
SEN-{organization}-{issueID}
```

Example:

```text
SEN-acme-123456789
```

`Fetch(...)` reparses that ID, extracts organization and issue ID, and re-queries Sentry so a stored session can be rehydrated without depending on stale browse results.

Per-issue formatting keeps Sentry-native facts instead of inventing repository semantics. Sessions include fields such as:

- short identifier (`ShortID` when present)
- project slug or name
- status
- culprit
- event count
- affected user count
- level
- permalink

Tracker references are emitted as `domain.TrackerReference{Provider:"sentry", Kind:"issue", ...}`.

#### Source-only constraints and role boundaries

The adapter intentionally declines source-tracker side effects:

- `Watch(...)` returns a closed channel
- `UpdateState(...)` returns `nil`
- `AddComment(...)` returns `nil`
- `OnEvent(...)` returns `nil`
- no tracker mutation back to Sentry state
- no comment sync back to Sentry
- no repository lifecycle registration
- no remote-detection integration
- no worktree, branch, PR, or MR automation

Treat Sentry as a source adapter. Any documentation that describes it as a repository-lifecycle system is stale.

#### Settings integration

`internal/tui/views/settings_service.go` exposes a dedicated `provider.sentry` section with fields for:

- token
- base URL
- organization
- projects

Provider status uses the same auth-source logic as the runtime adapter:

- `config token`
- `env token`
- `sentry cli`
- `keychain`
- `unset`

The description shown in Settings matches the implementation: Sentry can use keychain-backed config, environment variables, or an authenticated Sentry CLI session.

Login flow:

- `SettingsPage.loginProviderCmd(...)` special-cases `provider == "sentry"`
- it runs `sentry auth login` directly
- `config.SentryCLIEnvironment(...)` sets `SENTRY_URL` to the self-hosted root when needed and clears inherited values when the default host should be used
- on success the page refreshes provider status without marking the config dirty
- harness `RunAction(Action="login_provider", Provider="sentry")` is implemented in oh-my-pi, Claude Code, and Codex
- the Settings service also knows how to package the correct root `base_url` input for those actions

Connectivity testing via `SettingsService.TestProvider("sentry", ...)`:

1. rebuilds config from Settings fields
2. constructs `sentryadapter.New(...)`
3. runs `ListSelectable(... ScopeIssues, Limit: 1)`
4. marks the provider connected only on success

That means Settings exercises the same constructor, auth-resolution path, and browse transport that the runtime uses.

#### Verification coverage

The shipped behavior is covered in multiple layers.

Adapter tests (`internal/adapter/sentry/*_test.go`) cover:

- constructor requires organization and credentials
- capability declaration stays source-only and issues-only
- issue-list query mapping honors views, states, search, cursor, and allowlist scoping
- next-cursor parsing from Sentry `Link` headers
- list-item mapping and pagination propagation
- single-issue resolve
- multi-issue aggregate resolve
- fetch by `SEN-{organization}-{issueID}`
- CLI-backed transport via `sentry api ... --verbose`
- source-only methods remain no-ops

Config tests (`internal/config/*sentry*_test.go`) cover:

- default `base_url`
- YAML loading for organization, projects, and `token_ref`
- invalid `base_url` rejection
- keychain secret hydration and persistence for `adapters.sentry.token`
- auth-source precedence
- `SENTRY_URL`, `SENTRY_ORG`, and `SENTRY_PROJECT` fallback behavior
- self-hosted URL normalization and root-URL handling for CLI auth

App wiring tests (`internal/app/wire*_test.go`) cover:

- Sentry work-item adapter registration with config token
- registration with `SENTRY_AUTH_TOKEN`
- registration with authenticated Sentry CLI
- skip behavior when organization is missing
- repo-lifecycle registration continues to ignore Sentry

TUI and settings tests (`internal/tui/views/*sentry*_test.go`, `app_browse_test.go`) cover:

- Sentry provider section rendering
- provider status auth-source reporting
- direct settings login refresh behavior
- provider test path for both token-backed HTTP and CLI-backed transport
- stable new-session layout when switching to Sentry
- clearing repo/project filter state across the Sentry boundary

---

## 3. Repo lifecycle adapters

### glab (`internal/adapter/glab`)

`GlabAdapter` is the GitLab repo-lifecycle adapter. It is separate from the GitLab work-item adapter on purpose.

Behavior:

- `worktree.created` -> `glab mr create --draft ...`; if an MR already exists for the branch, it is discovered via `glab mr view --output json` and recorded instead
- `worktree.reused` -> `glab mr update --description ...` with the updated sub-plan content from the payload. This keeps the MR description in sync after differential re-planning or follow-up.
- `work_item.completed` -> `glab mr update --draft=false ...`; on completion the adapter also re-queries `glab mr view` to record the final artifact state
- configured reviewers and labels are forwarded to MR creation
- failures are logged at WARN and do not block the workflow
- MR persistence uses the dual-write pattern (`adapter.PersistGitlabMR`): the adapter records a `ReviewArtifactRecorded` event, upserts into the `GitlabMRs` provider table, and upserts into the session review artifact link table. `GlabAdapter` receives `adapter.ReviewArtifactRepos` and `workspaceDir` through `NewWithEventRepo(...)`.
- `entriesForCompletion(...)` enriches in-memory tracking with event-sourced MR records when the event repo is available, so MRs created across worktrees are not missed during completion
`BuildRepoLifecycleAdapters(...)` registers `glab` when remote detection says a workspace repo is GitLab.

### GitHub repo lifecycle

GitHub lifecycle handling is implemented by `GithubAdapter` itself rather than a second GitHub-specific repo adapter.

`BuildRepoLifecycleAdapters(...)` wraps it in `routedRepoLifecycleAdapter`, which suppresses events that do not match the detected remote platform. That keeps GitHub PR automation from reacting to GitLab payloads and vice versa.

Behavior:

- `worktree.created` -> create PR via GitHub API with draft status, sub-plan as body, and configured reviewers/labels
- `worktree.reused` -> update existing PR body via GitHub API PATCH with the updated sub-plan content from the payload. This keeps the PR description in sync after differential re-planning or follow-up.
- `work_item.completed` -> PR completion handling

PR persistence uses the dual-write pattern (`adapter.PersistGithubPR`): on `worktree.created` and `worktree.reused`, the adapter records a `ReviewArtifactRecorded` event, upserts into the `GithubPRs` provider table, and upserts into the session review artifact link table. `GithubAdapter` receives `adapter.ReviewArtifactRepos` through `NewRepoLifecycle(...)`.

---

## 4. Remote detection for repo lifecycle registration

`internal/app/remotedetect` drives repo-lifecycle adapter registration.

Current behavior:

- `DetectPlatform(...)` resolves `origin` first, otherwise the first sorted remote
- GitHub is recognized from `github.com` plus hosts implied by `cfg.Adapters.GitHub.BaseURL`
- GitLab is recognized from `gitlab.com`, hosts in `~/.config/glab-cli/config.yml`, and the host implied by `cfg.Adapters.GitLab.BaseURL`
- unknown hosts return `PlatformUnknown`

`BuildRepoLifecycleAdapters(...)` then:

1. discovers git repos inside the workspace
2. detects each repo's platform
3. registers one routed lifecycle adapter per detected platform

Current outcomes:

- GitLab remotes -> `glab`
- GitHub remotes -> `github` if GitHub auth is configured
- Sentry config is ignored for repo lifecycle registration
- unknown remotes produce warnings and no lifecycle adapter

---

## 5. Harness notes

`AgentHarness` lives in `internal/adapter/interfaces.go`, not in a separate harness package.

Current shipped harnesses:

- `internal/adapter/ohmypi` (`Name() == "omp"`)
  - streaming: yes
  - messaging / `SendMessage`: yes
  - native resume: yes (`SupportsNativeResume`)
  - supported tools: `read`, `grep`, `find`, `edit`, `write`, `bash`, `ask_foreman`
  - remains the default harness family in config defaults
- `internal/adapter/claudeagent` (`Name() == "claude-code")`
  - streaming: yes
  - messaging: yes (`SendMessage`, `Steer`)
  - native resume: yes (`SupportsNativeResume`)
  - supported tools: `Read`, `Write`, `Edit`, `Bash`, `Glob`, `Grep`, `WebSearch`, `WebFetch`, `mcp__substrate__ask_foreman`
- `internal/adapter/codex` (`Name() == "codex"`)
  - streaming: yes
  - messaging: no
  - supported tools: `sandboxed-cli`

Routing behavior from `internal/app/harness.go`:

- defaults are `ohmypi` for `planning`, `implementation`, `review`, and `foreman`
- unavailable configured harnesses degrade to `nil` with diagnostics so the app can still reach Settings
- `Resume` reuses the implementation harness

Provider login notes:

- GitHub login is routed through harness `RunAction(...)`
- Sentry login has two current paths:
  - the Settings page runs `sentry auth login` directly and refreshes provider status afterward
  - all three harness implementations also support `RunAction(Action="login_provider", Provider="sentry")`
- self-hosted Sentry login uses `config.SentryCLIEnvironment(...)` so `SENTRY_URL` points at the root host, not `/api/0`

The practical boundary today is messaging and steering support: oh-my-pi is the only harness with verified `SendMessage` and `Steer()` capability, while Claude Code and Codex return `ErrSteerNotSupported` from `Steer()` and are wired and usable for non-messaging flows but do not yet provide the same interactive correction surface.

### OMP-specific capabilities

Beyond basic streaming and messaging, the OMP bridge (`bridge/omp-bridge.ts`) supports:

**Steering**: The bridge accepts `{"type":"steer","text":"..."}` commands and calls `session.prompt(text, { streamingBehavior: "steer" })` to interrupt active streaming. This makes OMP the only harness with verified `Steer()` support. Claude Code and Codex stubs return `ErrSteerNotSupported`.

**Resume**: OMP supports native session resume via `SessionManager.open(filePath)`. When the `SUBSTRATE_RESUME_SESSION_FILE` environment variable is set, the bridge opens the existing session instead of creating a new one. This enables follow-up on completed repos without losing conversation context. `HarnessCapabilities.SupportsNativeResume` is `true` for OMP.

**Session metadata**: After session creation, the bridge emits a `session_meta` event containing `omp_session_id` and `omp_session_file`:

```json
{"type":"session_meta","omp_session_id":"...","omp_session_file":"..."}
```

These values are persisted on the `Task` row (`OmpSessionFile`, `OmpSessionID`) for later resume and follow-up. The orchestrator captures them via type assertion on the harness session after completion.

**Retry events**: The OMP harness emits `retry_wait` (with a `message` payload) when the agent enters a retry wait state, and `retry_resumed` when it resumes from retry. These are surfaced through the standard `AgentEvent` channel alongside `started`, `text_delta`, `tool_start`, `tool_result`, `done`, `error`, `question`, and `foreman_proposed` events.