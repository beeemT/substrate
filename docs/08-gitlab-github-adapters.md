# Plan: GitLab Work Item Adapter + GitHub Dual Adapter

## Context

The current adapter inventory:

| Package | WorkItemAdapter | RepoLifecycleAdapter |
|---|---|---|
| `adapter/glab` | no | yes — MR lifecycle via `glab` CLI |
| `adapter/linear` | yes — issues/projects/initiatives | no |
| `adapter/manual` | yes — manual entry | no |

Two units are missing:
1. **GitLab work items** — GitLab issues are completely unsupported today despite the MR lifecycle adapter existing.
2. **GitHub** — neither work items nor PR lifecycle is implemented.

---

## Decisions

### D1 — GitLab ExternalID format: numeric project ID
Format: `GL-{projectID}-{issueIID}` e.g. `GL-1234-42`

**Rationale**: `projectID` is the stable numeric GitLab project ID. It is immune to namespace renames and project transfers. The alternative (namespace path) is human-readable but silently breaks when a project is renamed or moved. A bug that surfaces months after a rename is worse than a slightly opaque ID.

### D2 — GitHub ScopeInitiatives (Projects v2): defer, stub to ErrBrowseNotSupported
`ScopeInitiatives` will return `ErrBrowseNotSupported` on first ship. A `// TODO(phase-N): GitHub Projects v2 via GraphQL` comment marks the stub.

**Rationale**: GitHub Projects v2 requires GraphQL, a separate `project_number` config field, and a different item model. The scope is large enough to warrant its own phase. Teams using GitHub for roadmap-level work will need it — it is not a permanent skip — but it is not required to deliver a useful work item adapter. This matches the GitLab epics approach (graceful degradation on non-Premium).

### D3 — GitHub: one struct implementing both interfaces
`GithubAdapter` in `internal/adapter/github/` implements both `WorkItemAdapter` and `RepoLifecycleAdapter`.

**Rationale**: both halves share the same config (`token`, `owner`, `repo`) and the same HTTP client. Splitting into two structs adds indirection with no benefit — they would share state anyway. This is the first dual-role adapter in the codebase; the pattern is clean when the two interfaces share auth and context.

### D4 — GitHub PR lifecycle: direct REST API, not `gh` CLI
`gh pr create` and `gh pr ready` are replaced with `POST /repos/{owner}/{repo}/pulls` and `PATCH /repos/{owner}/{repo}/pulls/{pull_number}`.

**Rationale**: the work item half already manages a REST HTTP client with token auth. Routing PR lifecycle through the same client keeps one auth code path, makes the adapter unit-testable without process spawning, and removes a subprocess dependency. The `gh` CLI wrapper pattern (used in glab) trades simplicity for fragile output parsing and external process coupling; that tradeoff is not worth repeating when we already own the HTTP layer.

### D5 — RepoLifecycleAdapter registration: auto-detect from git remotes at startup
`BuildRepoLifecycleAdapters` in `wire.go` inspects the git remotes of the workspace root at
startup rather than registering adapters unconditionally.

Detection logic:
1. Receive `workspaceDir` from the caller (see Decision D9).
2. Run `git remote get-url origin` in that directory.
3. Match the remote URL host against known platforms (see matching rules below).
4. Register only the adapter whose platform matches.

Matching rules:
- URL host is `github.com` → register github adapter (if `GithubConfig.Owner` + `Repo` set)
- URL host is `gitlab.com` → register glab adapter
- URL host matches any host listed in glab's own config (`~/.config/glab-cli/config.yml` `hosts` map) → register glab adapter
- No match → no lifecycle adapter registered; emit `slog.Warn` at startup

**Note on glab self-hosted**: `GlabConfig` requires no `BaseURL` field. The glab CLI auto-detects
which GitLab instance to operate against from the git remote in the worktree directory. Detection
here only needs to answer "is this a GitLab remote?" — consulting glab's known-hosts config is
sufficient for that question without duplicating base URL configuration.

**Edge case — `origin` absent**: fall back to the first remote alphabetically and log a warning
naming which remote was chosen.

**Rationale**: registering adapters unconditionally means a GitLab repo silently receives failed
`gh pr create` calls (and vice versa). The warn-on-failure policy suppresses those errors, making
misconfiguration invisible. Detecting from remotes ties registration to observable repository
state, is automatic for correctly configured workspaces, and requires no per-repo manual config.

### D6 — GitHub token: resolve once at startup, cache; `gh auth token` as fallback
When `GithubConfig.Token` is empty, the adapter constructor runs `gh auth token` once at startup,
captures the output, and stores it as the bearer token for all subsequent HTTP calls. The subprocess
is never spawned again after initialization. If both config token and `gh auth token` are absent,
construction returns an error and the adapter is not registered.

**Rationale**: minimises friction for developers already using `gh` while keeping the HTTP client
clean and subprocess-free at call time. One startup resolution is acceptable cost; per-request
subprocess invocation is not.

### D7 — GitLab group ID for epics: auto-discover via API, no config field
`GET /projects/{id}` returns a `namespace` object: `{ id, kind, path }`. At adapter startup,
if `namespace.kind == "group"`, store `namespace.id` as the group ID and enable
`ScopeInitiatives`. If `namespace.kind == "user"` (personal namespace), epics are unavailable;
`ScopeInitiatives` returns `ErrBrowseNotSupported` with a logged explanation. No `group_id` config
field is needed.

**Rationale**: avoids a config field that users would need to look up manually. The project API
call is already required at startup to validate connectivity. Deriving the group ID from it is
free. A config field would also drift if the project is transferred to a different group.

### D8 — GitHub default branch: detect via REST at startup, cache; fallback to `main`
At adapter construction, call `GET /repos/{owner}/{repo}` and cache `default_branch`. If the
call fails (network, auth), fall back to `"main"` and log a warning. No config field.

**Rationale**: avoids a config field that would silently break if the default branch is ever
renamed. The startup API call validates connectivity anyway. Fallback to `main` covers the
common case without blocking startup.

### D9 — Workspace root for remote detection: passed as parameter from startup, not from config
`BuildRepoLifecycleAdapters` receives the workspace directory as an explicit `workspaceDir string`
parameter alongside the config. In `main.go`, `wsDir` is already resolved via
`gitwork.FindWorkspace(cwd)` before wire functions are called — this value is passed directly.
No workspace root field is added to `Config`.

**Rationale**: the workspace root is a runtime-detected value, not a user-configured one.
Putting runtime-detected state into `Config` conflates configuration with environment detection.
`wsDir` is already available at the call site; threading it as a parameter is clean and testable.
If no workspace is detected at startup (`wsDir == ""`), remote detection is skipped and no
lifecycle adapters are registered (with a startup warning).
---

## What gets built

### Phase A — GitLab work item adapter (`internal/adapter/gitlab/`)

New package, separate from `adapter/glab/`. The glab adapter owns MR lifecycle. This package owns issue data.

**Config addition** (`internal/config/config.go`):
```go
The `GitlabConfig` struct no longer has a `GroupID` field. The group ID is discovered at startup
via `GET /projects/{id}` → `namespace.{id,kind}`. The config struct is:

type GitlabConfig struct {
    Token        string            `toml:"token"`
    BaseURL      string            `toml:"base_url"`      // default: https://gitlab.com
    ProjectID    int64             `toml:"project_id"`    // numeric; required
    Assignee     string            `toml:"assignee"`      // filter Watch to this username
    PollInterval string            `toml:"poll_interval"` // default: 60s
    StateMappings map[string]string `toml:"state_mappings"`
    // No GroupID — discovered at startup via GET /projects/{id} → namespace.id (D7)
}
```

**Capabilities**:
```
CanWatch:     true
CanBrowse:    true
CanMutate:    true
BrowseScopes: [issues, projects, initiatives]
```

Where:
- `ScopeIssues` → `GET /projects/{id}/issues`
- `ScopeProjects` → `GET /projects/{id}/milestones` (structural analog to Linear projects)
- `ScopeInitiatives` → `GET /groups/{group_id}/epics` — requires GitLab Premium; returns `ErrBrowseNotSupported` with a logged warning if the API returns 403

**ExternalID**: `GL-{projectID}-{issueIID}` (Decision D1)

**Watch**: poll `/projects/{id}/issues?assignee_username={assignee}&state=opened` on `poll_interval`. Dedup map: `iid → state`, same pattern as the Linear adapter. Min interval: 30s.

**Mutations**:
- `UpdateState`: `PUT /projects/{id}/issues/{iid}` with `state_event: "close" | "reopen"` — mapped via `state_mappings`
- `AddComment`: `POST /projects/{id}/issues/{iid}/notes`

**OnEvent**:
- `PlanApproved` → `UpdateState("in_progress")` (requires `external_id` in payload — Phase 12/13 dependency, same gap as Linear)
- `WorkItemCompleted` → `UpdateState("done")`
- `AgentSessionFailed` → `AddComment` (deferred — same Phase 12/13 payload gap as Linear)

**Wire registration**: conditional on `GitlabConfig.ProjectID != 0 && GitlabConfig.Token != ""`

---

### Phase B — GitHub dual adapter (`internal/adapter/github/`)

Single package. `GithubAdapter` implements both `WorkItemAdapter` and `RepoLifecycleAdapter` (Decision D3).

**Config addition**:
```go
type GithubConfig struct {
    Token        string            `toml:"token"`         // optional; falls back to gh auth token (D6)
    Owner        string            `toml:"owner"`         // required
    Repo         string            `toml:"repo"`          // required
    Assignee     string            `toml:"assignee"`      // filter Watch; "me" resolves via /user
    PollInterval string            `toml:"poll_interval"` // default: 60s
    Reviewers    []string          `toml:"reviewers"`
    Labels       []string          `toml:"labels"`
    StateMappings map[string]string `toml:"state_mappings"`
    // No DefaultBranch — detected via GET /repos/{owner}/{repo} at startup (D8)
}
```

**ExternalID**: `GH-{owner}-{repo}-{number}` e.g. `GH-myorg-myrepo-42`

**WorkItemAdapter capabilities**:
```
CanWatch:     true
CanBrowse:    true
CanMutate:    true
BrowseScopes: [issues, projects]
```

- `ScopeIssues` → `GET /repos/{owner}/{repo}/issues`
- `ScopeProjects` → `GET /repos/{owner}/{repo}/milestones`
- `ScopeInitiatives` → `ErrBrowseNotSupported` with TODO comment (Decision D2)

**Watch**: poll `/repos/{owner}/{repo}/issues?assignee={me}&state=open`. Dedup: `number → state`.

**Mutations**:
- `UpdateState`: `PATCH /repos/{owner}/{repo}/issues/{number}` with `{ "state": "open" | "closed" }` — mapped via `state_mappings`
- `AddComment`: `POST /repos/{owner}/{repo}/issues/{number}/comments`

**RepoLifecycleAdapter — PR lifecycle** (Decision D4, direct REST):
- `OnEvent(WorktreeCreated)` → `POST /repos/{owner}/{repo}/pulls` with `{ draft: true, head: branch, base: default_branch, title: ... }`
- `OnEvent(WorkItemCompleted)` → `GET /repos/{owner}/{repo}/pulls?head={branch}` to find PR, then `PATCH /repos/{owner}/{repo}/pulls/{number}` with `{ draft: false }`
- Idempotency guard before create: `GET /repos/{owner}/{repo}/pulls?head={branch}&state=open`, skip if result non-empty
- Same warn-on-failure policy as glab — never propagates errors
- Same in-memory branch→PR tracking map, `sync.RWMutex` protected

**OnEvent**:
- Work item events: same as GitLab (Phase 12/13 payload dependency)
- Lifecycle events: WorktreeCreated, WorkItemCompleted (same as glab)

**Wire registration**: conditional on `GithubConfig.Owner != "" && GithubConfig.Repo != ""`. Token optional if `gh auth token` is available (fall back via exec).

---

## Remote detection implementation (Decision D5)

New helper in `internal/app/remotedetect/`:

```go
type Platform int
const (
    PlatformUnknown Platform = iota
    PlatformGitHub
    PlatformGitLab
)

// DetectPlatform resolves the git remote URL for origin in dir (falling back to the
// first remote alphabetically) and returns the hosting platform.
//
// GitLab detection: matches gitlab.com, or any host listed in the glab CLI's own
// known-hosts config (~/.config/glab-cli/config.yml). No BaseURL config field required.
func DetectPlatform(ctx context.Context, dir string) (Platform, error)
```

`BuildRepoLifecycleAdapters` in `wire.go` receives `workspaceDir string` as a parameter (Decision D9)
and calls `remotedetect.DetectPlatform(ctx, workspaceDir)`:
- `PlatformGitHub` → `[github.New(cfg.Adapters.Github)]` if configured, else warn + empty
- `PlatformGitLab` → `[glab.New(cfg.Adapters.Glab)]` if configured, else warn + empty
- `PlatformUnknown` → warn + empty

---

## File layout

```
internal/adapter/
    glab/                   existing (unchanged)
    gitlab/                 NEW — Phase A
        adapter.go          GitlabAdapter struct, Capabilities, OnEvent
        client.go           REST HTTP client, token auth
        browse.go           ListSelectable, Resolve, Fetch
        watch.go            Watch, poll loop, dedup
        mutate.go           UpdateState, AddComment
        adapter_test.go
    github/                 NEW — Phase B
        adapter.go          GithubAdapter struct, both interface registrations
        client.go           REST HTTP client, token auth + gh CLI fallback
        browse.go           ListSelectable, Resolve, Fetch (WorkItemAdapter)
        watch.go            Watch, poll loop
        mutate.go           UpdateState, AddComment
        lifecycle.go        OnEvent (PR create/ready), idempotency guard, tracking map
        adapter_test.go
    remotedetect/           NEW — Decision D5
        detect.go
        detect_test.go
internal/config/
    config.go               add GitlabConfig, GithubConfig to AdaptersConfig
internal/app/
    wire.go                 update BuildWorkItemAdapters, BuildRepoLifecycleAdapters
```

---

**Resolved decisions**: D6 (token fallback), D7 (group ID auto-discover), D8 (default branch
auto-detect), D9 (workspace dir as parameter). No open questions remain. Implementation may proceed.