# 04 - Adapter Implementations

Concrete implementations of interfaces from `02-layered-architecture.md`. Each adapter lives under `internal/adapter/`.

---

## 1. Linear Adapter (WorkItemAdapter)

Package: `internal/adapter/linear`. Authentication via personal API key in config, sent as `Authorization: <api_key>`.

### Capabilities

```go
type LinearAdapter struct {
    client        *http.Client
    apiKey        string
    teamID        string
    assignee      string
    pollInterval  time.Duration
    stateMappings map[domain.WorkItemState]string
    gqlEndpoint   string
}

func (a *LinearAdapter) Capabilities() domain.AdapterCapabilities {
    return domain.AdapterCapabilities{
        CanWatch:     true,
        CanBrowse:    true,
        CanMutate:    true,
        BrowseScopes: []domain.SelectionScope{
            domain.ScopeIssues, domain.ScopeProjects, domain.ScopeInitiatives,
        },
    }
}
```

### Selection Model

Three selection scopes for TUI session creation, each determining how work items are discovered and aggregated.

**Issues** (`ScopeIssues`): Query issues from the configured team; user selects 1+. On `Resolve`: if 1 issue, WorkItem mirrors it directly. If N issues, title = first issue title + `(+N-1 more)`, description concatenates all with `---` separators, labels merged.

**Projects** (`ScopeProjects`): Query projects accessible to the team; user selects 1+. `Resolve` fetches all non-completed issues from selected projects, builds WorkItem with project context + all issue details in description.

**Initiatives** (`ScopeInitiatives`): Query initiatives; user selects exactly 1. `Resolve` fetches all projects + their issues, builds comprehensive WorkItem with initiative goals, project breakdown, and grouped issue details.

### ListSelectable and Resolve

```go
func (a *LinearAdapter) ListSelectable(ctx context.Context, opts domain.ListOpts) (*domain.ListResult, error) {
    switch opts.Scope {
    case domain.ScopeIssues:      return a.listIssues(ctx, opts)
    case domain.ScopeProjects:    return a.listProjects(ctx, opts)
    case domain.ScopeInitiatives: return a.listInitiatives(ctx, opts)
    default: return nil, fmt.Errorf("unsupported scope %q", opts.Scope)
    }
}

func (a *LinearAdapter) Resolve(ctx context.Context, sel domain.Selection) (domain.WorkItem, error) {
    switch sel.Scope {
    case domain.ScopeIssues:
        issues, err := a.fetchIssuesByIDs(ctx, sel.ItemIDs)
        if err != nil { return domain.WorkItem{}, err }
        if len(issues) == 1 { return issueToWorkItem(issues[0]), nil }
        return aggregateIssues(issues), nil // title+count, joined descriptions, merged labels
    case domain.ScopeProjects:
        var sections []string
        var allIssues []linearIssue
        for _, id := range sel.ItemIDs {
            proj, err := a.fetchProjectWithIssues(ctx, id)
            if err != nil { return domain.WorkItem{}, err }
            sections = append(sections, formatProjectSection(proj))
            allIssues = append(allIssues, proj.Issues...)
        }
        return buildProjectWorkItem(sections, allIssues), nil
    case domain.ScopeInitiatives:
        if len(sel.ItemIDs) != 1 {
            return domain.WorkItem{}, fmt.Errorf("initiatives scope requires exactly 1 selection")
        }
        init, err := a.fetchInitiativeDeep(ctx, sel.ItemIDs[0])
        if err != nil { return domain.WorkItem{}, err }
        return buildInitiativeWorkItem(init), nil
    default: return domain.WorkItem{}, fmt.Errorf("unsupported scope %q", sel.Scope)
    }
}
```

### GraphQL Queries

Issue queries (existing -- used by Watch and issue-scope selection):

```go
const queryAssignedIssues = `
query($assigneeId: String!, $teamId: String!) {
  issues(filter: { assignee: { id: { eq: $assigneeId } }, team: { id: { eq: $teamId } },
                    state: { type: { nin: ["canceled","completed"] } } }) {
    nodes { id identifier title description priority state { id name type }
            labels { nodes { name } } assignee { id name } team { key } url }
  }
}`

const queryIssueByID = `
query($id: String!) {
  issue(id: $id) { id identifier title description priority state { id name type }
    labels { nodes { name } } url
    comments { nodes { body createdAt user { name } } }
    relations { nodes { relatedIssue { id identifier title } type } } }
}`
```

New queries for project/initiative selection:

```graphql
# Projects accessible to team
query($teamId: String!) {
  projects(filter: { accessibleTeams: { id: { eq: $teamId } },
                     state: { nin: ["completed","canceled"] } }) {
    nodes { id name description state icon color
            issues { nodes { id identifier title } } }
  }
}

# Single project with non-completed issues (used by Resolve)
query($projectId: String!) {
  project(id: $projectId) {
    id name description
    issues(filter: { state: { type: { nin: ["completed","canceled"] } } }) {
      nodes { id identifier title description state { id name }
              labels { nodes { name } } }
    }
  }
}

# Initiatives with nested projects and issues
query {
  initiatives(filter: { status: { nin: ["completed","canceled"] } }) {
    nodes { id name description status
      projects { nodes { id name
        issues { nodes { id identifier title } } } }
    }
  }
}
```

Mutations:

```go
const mutationUpdateIssueState = `
mutation($id: String!, $stateId: String!) {
  issueUpdate(id: $id, input: { stateId: $stateId }) { success issue { id state { id name } } }
}`

const mutationAddComment = `
mutation($issueId: String!, $body: String!) {
  commentCreate(input: { issueId: $issueId, body: $body }) { success comment { id } }
}`
```

### ExternalID Construction

Linear work items use a prefixed external ID: `LIN-{teamKey}-{issueNumber}` (e.g. `LIN-FOO-123`).

- `LIN` is the source prefix identifying this as a Linear issue
- `{teamKey}` is the Linear team's key identifier, sourced from `issue.team.key` in the GraphQL response
- `{issueNumber}` is the numeric suffix of the issue identifier (e.g. `identifier = "FOO-123"` → number = `"123"`)

```go
func linearExternalID(issue linearIssue) (string, error) {
		// issue.Team.Key = "FOO", issue.Identifier = "FOO-123"
		parts := strings.SplitN(issue.Identifier, "-", 2)
		if len(parts) != 2 {
				return "", fmt.Errorf("unexpected Linear identifier format %q: expected TEAM-N", issue.Identifier)
		}
		return "LIN-" + issue.Team.Key + "-" + parts[1], nil // "LIN-FOO-123"
}
```

### Watch: Poll-Based

Linear has webhooks but they require a server endpoint -- impractical for a local CLI. Polling is simpler.

```go
func (a *LinearAdapter) Watch(ctx context.Context, filter domain.WorkItemFilter) (<-chan domain.WorkItemEvent, error) {
    ch := make(chan domain.WorkItemEvent, 16)
    go func() {
        defer close(ch)
        ticker := time.NewTicker(a.pollInterval)
        defer ticker.Stop()
        known := make(map[string]string) // issue ID -> last state ID
        for {
            select {
            case <-ctx.Done(): return
            case <-ticker.C:
                var issues []domain.WorkItem
                var err error
                if len(filter.ExternalIDs) > 0 {
                // NOTE: filter.ExternalIDs are substrate-format ("LIN-FOO-123").
                // fetchIssuesByIDs must strip the "LIN-{teamKey}-" prefix and query
                // Linear by identifier ("FOO-123") or by internal UUID (stored at ingestion time),
                // NOT by passing the substrate ExternalID directly to issue(id: $id).
                    issues, err = a.fetchIssuesByIDs(ctx, filter.ExternalIDs)
                } else {
                    issues, err = a.fetchAssignedIssues(ctx)
                }
                if err != nil { ch <- domain.WorkItemEvent{Type: domain.WatchError, Err: err}; continue }
                for _, issue := range issues {
                    if len(filter.States) > 0 && !slices.Contains(filter.States, issue.State) { continue } // requires Go 1.21+ slices package
                    prev, seen := known[issue.ID]
                    known[issue.ID] = issue.StateID
                    if !seen { ch <- domain.WorkItemEvent{Type: domain.WorkItemDiscovered, Item: issue} }
                    if seen && prev != issue.StateID { ch <- domain.WorkItemEvent{Type: domain.WorkItemUpdated, Item: issue} }
                }
            }
        }
    }()
    return ch, nil
}
```

### State Mapping

Linear workflow states are team-specific. Config maps substrate states to Linear workflow state UUIDs. Reverse mapping derived at startup by inverting the map.

```toml
[adapters.linear]
api_key         = "lin_api_..."   # or "$LINEAR_API_KEY" for env ref
team_id         = "uuid"
assignee_filter = "me"            # resolves via viewer query, or explicit user ID
poll_interval   = "30s"
[adapters.linear.state_mappings]
backlog = "uuid-1"
todo = "uuid-2"
in_progress = "uuid-3"
in_review = "uuid-4"
done = "uuid-5"
canceled = "uuid-6"
```

### Event Handling

| Substrate Event | Linear Action |
|---|---|
| `PlanApproved` | Move issue to `in_progress` |
| `WorkItemCompleted` | Move issue to `done` |
| `AgentSessionFailed` | Add comment with error summary |
| `ReviewCritique` | Add comment with critique |

---

## 2. Manual Adapter (WorkItemAdapter)

Package: `internal/adapter/manual`. Creates work items without an external tracker. All items are entered directly by the human operator through the TUI form.

```go
type ManualAdapter struct {
		store WorkspaceStore // *sqlx.Tx from the enclosing Transact call; COUNT and subsequent Create always share the same transaction
}

func (a *ManualAdapter) Name() string { return "manual" }

func (a *ManualAdapter) Capabilities() domain.AdapterCapabilities {
    return domain.AdapterCapabilities{
        CanWatch:  false,
        CanBrowse: false,
        CanMutate: false,
    }
}

func (a *ManualAdapter) ListSelectable(_ context.Context, _ domain.ListOpts) (*domain.ListResult, error) {
    return nil, ErrNotSupported
}

func (a *ManualAdapter) Resolve(ctx context.Context, sel domain.Selection) (domain.WorkItem, error) {
		if sel.ManualInput == nil {
				return domain.WorkItem{}, fmt.Errorf("manual adapter requires ManualInput in selection")
		}
		externalID, err := a.nextManualID(ctx)
		if err != nil { return domain.WorkItem{}, fmt.Errorf("generate manual ID: %w", err) }
		return domain.WorkItem{
				ID:           ulid.Make().String(),
				ExternalID:   externalID, // "MAN-1", "MAN-2", ...
				Source:       "manual",
				SourceScope:  domain.ScopeManual,
				Title:        sel.ManualInput.Title,
				Description:  sel.ManualInput.Description,
				State:        domain.WorkItemIngested,
				CreatedAt:    time.Now(),
				UpdatedAt:    time.Now(),
		}, nil
}

// nextManualID returns the next sequential MAN-N identifier for this workspace.
// store is always a *sqlx.Tx supplied by ResourcesFactory, so this COUNT and
// the caller's subsequent WorkItem.Create execute in the same transaction.
func (a *ManualAdapter) nextManualID(ctx context.Context) (string, error) {
		n, err := a.store.CountManualWorkItems(ctx)
		if err != nil { return "", err }
		return fmt.Sprintf("MAN-%d", n+1), nil
}

func (a *ManualAdapter) Watch(_ context.Context, _ domain.WorkItemFilter) (<-chan domain.WorkItemEvent, error) {
    ch := make(chan domain.WorkItemEvent)
    close(ch) // manual adapter never auto-discovers
    return ch, nil
}

func (a *ManualAdapter) Fetch(_ context.Context, id string) (domain.WorkItem, error) {
    return domain.WorkItem{}, ErrNotSupported // state lives only in substrate DB
}

func (a *ManualAdapter) UpdateState(_ context.Context, _ string, _ domain.TrackerState) error { return nil }
func (a *ManualAdapter) AddComment(_ context.Context, _ string, _ string) error              { return nil }
func (a *ManualAdapter) OnEvent(_ context.Context, _ domain.SystemEvent) error                { return nil }
```

No TOML configuration needed. The manual adapter is always available as a built-in option, registered unconditionally at startup in `internal/app/wire.go`.

ExternalID format: `MAN-N` (incrementing sequence with no fixed width, e.g. `MAN-1`, `MAN-42`, `MAN-1000`). The counter is derived by counting existing manual work items in the DB for the current workspace — no separate counter column is required.

---

## 3. GitLab Adapter (WorkItemAdapter)

> This section captures the tracker-focused GitLab adapter. The broader browsing redesign in `09-unified-work-item-browsing.md` remains authoritative for future global inbox behavior; this section documents the concrete adapter shape, lifecycle hooks, and config decisions for the initial implementation.

Package: `internal/adapter/gitlab`. This package is separate from `internal/adapter/glab`: `gitlab` owns work item browsing/watch/mutation, while `glab` owns GitLab merge request lifecycle.

### Configuration

> `09-unified-work-item-browsing.md` supersedes project-scoped issue browsing as the long-term UX. The config below describes the initial adapter contract and the fields still required for tracker mutation and scoped browsing until that redesign lands.

```go
type GitlabConfig struct {
    Token         string            `toml:"token"`
    BaseURL       string            `toml:"base_url"`      // default: https://gitlab.com
    ProjectID     int64             `toml:"project_id"`    // numeric; required for scoped mutation/fetch
    Assignee      string            `toml:"assignee"`      // filter Watch to this username
    PollInterval  string            `toml:"poll_interval"` // default: 60s, minimum 30s
    StateMappings map[string]string `toml:"state_mappings"`
    // No GroupID: discover from GET /projects/{id} -> namespace.id when namespace.kind == "group".
}
```

`ProjectID` remains numeric in the adapter because the ExternalID format uses the stable numeric project identifier rather than namespace path. ExternalID format: `GL-{projectID}-{issueIID}` (example: `GL-1234-42`). Namespace paths are more readable but break silently on project rename or transfer.

At startup, the adapter validates connectivity with `GET /projects/{id}` and caches `namespace.id` when `namespace.kind == "group"`. If the namespace is personal (`kind == "user"`), epics are unavailable and initiatives browsing degrades to `ErrBrowseNotSupported` with a logged explanation rather than a config requirement for `group_id`.

### Capabilities

```go
func (a *GitlabAdapter) Capabilities() domain.AdapterCapabilities {
    return domain.AdapterCapabilities{
        CanWatch:     true,
        CanBrowse:    true,
        CanMutate:    true,
        BrowseScopes: []domain.SelectionScope{
            domain.ScopeIssues, domain.ScopeProjects, domain.ScopeInitiatives,
        },
    }
}
```

### Browse, Watch, and Mutate

> `09-unified-work-item-browsing.md` changes issue browsing to global-by-default. Until that redesign ships, the concrete scoped endpoints below define the adapter implementation.

- `ScopeIssues` -> `GET /projects/{id}/issues`
- `ScopeProjects` -> `GET /projects/{id}/milestones`
- `ScopeInitiatives` -> `GET /groups/{group_id}/epics`
  - Requires GitLab Premium.
  - If the API returns 403, return `ErrBrowseNotSupported` and log a warning rather than failing adapter construction.

Watch polls `/projects/{id}/issues?assignee_username={assignee}&state=opened` on `poll_interval`, using the same dedup pattern as Linear (`iid -> last state`). The minimum poll interval is 30s.

Mutations:
- `UpdateState` -> `PUT /projects/{id}/issues/{iid}` with `state_event: "close" | "reopen"`, mapped from substrate tracker states via `state_mappings`.
- `AddComment` -> `POST /projects/{id}/issues/{iid}/notes`.

### Event Handling

> Work item event hooks depend on the Phase 12/13 payload shape carrying `external_id`, the same payload dependency called out for Linear.

| Substrate Event | GitLab tracker action |
|---|---|
| `PlanApproved` | `UpdateState("in_progress")` |
| `WorkItemCompleted` | `UpdateState("done")` |
| `AgentSessionFailed` | Deferred comment hook until the failure payload carries the needed external tracker context |

**Wire registration:** register the GitLab work item adapter only when tracker credentials are present. In the initial scoped form that means `GitlabConfig.ProjectID != 0 && GitlabConfig.Token != ""`; once the unified global browsing redesign from `09-unified-work-item-browsing.md` lands, `project_id` becomes an optional narrowing default rather than a hard browse prerequisite.

---

## 4. GitHub Adapter (WorkItemAdapter + RepoLifecycleAdapter)

> `09-unified-work-item-browsing.md` is authoritative for the future global inbox UX. This section captures the concrete adapter decisions that replace the standalone GitHub/GitLab adapter plan.

Package: `internal/adapter/github`. `GithubAdapter` is the first dual-role adapter in the codebase: one struct implements both `WorkItemAdapter` and `RepoLifecycleAdapter` because both halves share the same config (`token`, `owner`, `repo`) and the same HTTP client.

### Configuration

```go
type GithubConfig struct {
    Token         string            `toml:"token"`         // optional; falls back to gh auth token once at startup
    Owner         string            `toml:"owner"`         // required for scoped repo behavior and PR lifecycle targeting
    Repo          string            `toml:"repo"`          // required for scoped repo behavior and PR lifecycle targeting
    Assignee      string            `toml:"assignee"`      // filter Watch; "me" resolves via /user
    PollInterval  string            `toml:"poll_interval"` // default: 60s
    Reviewers     []string          `toml:"reviewers"`
    Labels        []string          `toml:"labels"`
    StateMappings map[string]string `toml:"state_mappings"`
    // No DefaultBranch field: discover via GET /repos/{owner}/{repo} at startup.
}
```

If `Token` is empty, adapter construction runs `gh auth token` exactly once at startup, caches the result, and uses that bearer token for all subsequent HTTP calls. If both config token and `gh auth token` are unavailable, construction fails and the adapter is not registered. The subprocess fallback exists only at initialization time; request handling stays REST-only.

At startup, the adapter calls `GET /repos/{owner}/{repo}` and caches `default_branch`. If that call fails, fall back to `main` and log a warning instead of adding a `default_branch` config field that could drift.

ExternalID format: `GH-{owner}-{repo}-{number}` (example: `GH-myorg-myrepo-42`).

### Work Item Capabilities

```go
func (a *GithubAdapter) Capabilities() domain.AdapterCapabilities {
    return domain.AdapterCapabilities{
        CanWatch:     true,
        CanBrowse:    true,
        CanMutate:    true,
        BrowseScopes: []domain.SelectionScope{
            domain.ScopeIssues, domain.ScopeProjects,
        },
    }
}
```

`ScopeInitiatives` is intentionally unsupported in the first cut. Return `ErrBrowseNotSupported` and keep a TODO for a dedicated GitHub Projects v2 design rather than pretending milestones or PRs are a substitute.

### Browse, Watch, and Mutate

> The initial concrete adapter remains repo-scoped. `09-unified-work-item-browsing.md` broadens issue browsing to `GET /issues` in the later redesign.

- `ScopeIssues` -> `GET /repos/{owner}/{repo}/issues`
- `ScopeProjects` -> `GET /repos/{owner}/{repo}/milestones`
- `ScopeInitiatives` -> `ErrBrowseNotSupported`
- Watch -> poll `/repos/{owner}/{repo}/issues?assignee={me}&state=open` with dedup map `number -> state`
- `UpdateState` -> `PATCH /repos/{owner}/{repo}/issues/{number}` with `{ "state": "open" | "closed" }`, mapped via `state_mappings`
- `AddComment` -> `POST /repos/{owner}/{repo}/issues/{number}/comments`

### PR Lifecycle via REST

> GitHub PR lifecycle deliberately does not shell out to `gh`. The adapter already owns an authenticated REST client, and keeping PR creation/readiness on that path removes subprocess coupling and keeps the lifecycle code unit-testable.

Repo lifecycle behavior:
- `OnEvent(WorktreeCreated)` -> idempotency guard with `GET /repos/{owner}/{repo}/pulls?head={branch}&state=open`; if none exists, `POST /repos/{owner}/{repo}/pulls` with `{ draft: true, head: branch, base: default_branch, title: ... }`.
- `OnEvent(WorkItemCompleted)` -> find the PR with `GET /repos/{owner}/{repo}/pulls?head={branch}`, then `PATCH /repos/{owner}/{repo}/pulls/{number}` with `{ draft: false }`.
- Maintain the same warn-on-failure policy as `glab`: log at WARN, never block workflow completion.
- Protect the in-memory `branch -> PR` tracking map with `sync.RWMutex`, matching the codebase rule for shared mutable state.

Lifecycle event coverage matches `glab`: respond to `WorktreeCreated` and `WorkItemCompleted`. Work item tracker events (`PlanApproved`, `WorkItemCompleted`) share the same payload dependency as the GitLab tracker adapter for external tracker mutation.

**Wire registration:** register the GitHub adapter when lifecycle targeting is configured (`Owner` + `Repo`) and credentials are available either directly or through one-time `gh auth token` resolution. `09-unified-work-item-browsing.md` remains the source of truth for the later browse-config split that removes `owner`/`repo` as a hard requirement for issue browsing.

---

## 5. glab Adapter (RepoLifecycleAdapter)

Package: `internal/adapter/glab`. Requires `glab` CLI installed and authenticated. Validates at startup. Unlike `internal/adapter/gitlab`, this package owns GitLab merge request lifecycle only; tracker issue data lives in the GitLab work item adapter.

```go
type GlabAdapter struct {
    defaultReviewers []string
    defaultLabels    []string
    draftByDefault   bool
    autoPush         bool
    runner           CmdRunner // abstracts exec for testability
}
```

### Event Handling

**WorktreeCreated** -- create draft MR from worktree directory:
```sh
glab mr create --source-branch <branch> --target-branch <default> \
  --title "<WorkItemTitle; fallback: title-cased slug from branch name>" --draft --push --yes \
  --reviewer @alice --reviewer @bob --label substrate
```

**WorkItemCompleted** -- mark MR ready: `glab mr update <id> --draft=false`.


```go
func (a *GlabAdapter) OnEvent(ctx context.Context, event SystemEvent) error {
    switch e := event.(type) {
    case WorktreeCreatedEvent:
        // Prefer the plan title carried in the event; fall back to a human-readable
        // title derived from the branch slug (e.g. "sub-LIN-FOO-123-fix-auth-flow" →
        // "Fix auth flow [LIN-FOO-123]").
        title := e.WorkItemTitle
        if title == "" {
            title = titleFromBranch(e.Branch) // strips prefix, capitalises slug
        }
        return a.createDraftMR(ctx, e.RepositoryName, e.Branch, title)
    case WorkItemCompletedEvent:
        // Mark all MRs for repos in this work item ready for review.
        for _, repo := range e.WorkItem.Repos {
            if err := a.markMRReady(ctx, repo.Branch); err != nil {
                slog.Warn("failed to mark MR ready", "repo", repo.RepoName, "err", err)
            }
        }
        return nil
    default:
        return nil
    }
}
```

`markMRReady` shells out to `glab mr update --source-branch <branch> --draft=false`. Failures are logged at WARN and do not block completion (matching the glab error policy).
### Error Policy

glab failures log at WARN and **never block** the workflow. Users can always manage MRs manually.

```toml
[adapters.glab]
default_reviewers = ["@alice", "@bob"]
default_labels    = ["substrate", "auto"]
draft_by_default  = true
auto_push         = true
```

---

## 6. Remote Detection for RepoLifecycleAdapter Registration

> Repo lifecycle adapters are not registered unconditionally. Startup inspects the workspace's git remotes and only enables the lifecycle adapter that matches the observed hosting platform.

Package: `internal/app/remotedetect`. `BuildRepoLifecycleAdapters` receives `workspaceDir string` from startup rather than reading it from config. In `main.go`, `wsDir` is already resolved via `gitwork.FindWorkspace(cwd)`; the wire layer threads that runtime value into lifecycle registration.

```go
type Platform int

const (
    PlatformUnknown Platform = iota
    PlatformGitHub
    PlatformGitLab
)

func DetectPlatform(ctx context.Context, dir string) (Platform, error)
```

Detection rules:
1. Run `git remote get-url origin` in the workspace directory.
2. If `origin` is absent, fall back to the first remote alphabetically and log a warning naming the chosen remote.
3. Match the remote host:
   - `github.com` -> GitHub lifecycle adapter, if GitHub lifecycle config is present.
   - `gitlab.com` -> `glab` lifecycle adapter.
   - Any host listed in `~/.config/glab-cli/config.yml` under `hosts` -> `glab` lifecycle adapter.
   - No match -> no lifecycle adapter; log a startup warning.

If `workspaceDir == ""`, skip remote detection and register no lifecycle adapters, again with a warning. This keeps runtime-discovered environment state out of static config and prevents a GitLab repo from silently trying GitHub PR creation, or the inverse.

`glab` still needs no `BaseURL` field for self-hosted GitLab lifecycle. The CLI infers the instance from the worktree remote; remote detection only answers whether the repository host is GitLab-like enough to route lifecycle hooks to `glab`.

---

## 7. Agent Harness Interface

Package: `internal/domain/harness`. The orchestrator is harness-agnostic, but harness selection is now multi-provider: oh-my-pi remains the default documented execution path because it is the only harness with proven interactive correction and Foreman messaging support. Claude Code and Codex are wired behind the same contract for startup, progress, completion, and fallback selection, but they are not yet considered interaction-complete substitutes for oh-my-pi.

```go
type AgentHarness interface {
    Name() string
    StartSession(ctx context.Context, opts SessionOpts) (HarnessSession, error)
    Capabilities() HarnessCapabilities
}

type HarnessSession interface {
    ID() string
    Wait(ctx context.Context) (SessionResult, error)
    Events() <-chan SessionEvent
    SendMessage(ctx context.Context, msg string) error
    Abort(ctx context.Context) error
}

type SessionMode string

const (
    SessionModeAgent   SessionMode = "agent"   // coding sub-agent; full tool set
    SessionModeForeman SessionMode = "foreman" // question answering; read-only tools
)

type SessionOpts struct {
    SessionID            string      // substrate-generated ULID; used for DB record and session directory
    Mode                 SessionMode // defaults to Agent
    WorktreePath         string      // empty for foreman sessions (uses workspace root)
    DraftPath            string      // absolute path to plan-draft.md; set for planning/revision sessions only
    SubPlan              SubPlan
    CrossRepoPlan        string
    SystemPrompt         string
    AllowPush            bool
    DocumentationContext string
}

type SubPlan struct {
    RepoName    string
    Branch      string
    Objectives  []string
    FileTargets []string
    RawMarkdown string
}

type SessionResult struct {
    ExitCode int
    Summary  string
    Errors   []string
}

type SessionEventType int

const (
    SessionEventProgress        SessionEventType = iota
    SessionEventQuestion        // sub-agent called ask_foreman
    SessionEventForemanProposed // foreman session produced a proposed answer
    SessionEventPush
    SessionEventError
    SessionEventComplete
)

type SessionEvent struct {
    Type    SessionEventType
    Payload any
}

type HarnessCapabilities struct {
    SupportsStreaming bool
    SupportsMessaging bool
    SupportedTools    []string
}
```

The current shared contract is intentionally small. Do not document richer shared modes or explicit answer-routing APIs as current state; those remain future candidates until the non-OMP harnesses prove they need them. Today, the key operational boundary is whether a harness has verified `SendMessage` parity for planning correction, review correction, and Foreman flows.

### Harness Selection and Operational Policy

Harness routing is config-driven. The repository supports a default harness, fallback order, and per-phase overrides (planning, implementation, review, foreman). The current operational policy is:
- default to **oh-my-pi**
- allow **Claude Code** then **Codex** as fallback/opt-in harnesses
- fail early when the selected harness binary is unavailable
- keep oh-my-pi as the documented safe path until Claude Code and Codex have real interactive messaging coverage

Representative config shape:
```toml
[harness]
default = "ohmypi"
fallback = ["claude-code", "codex"]

[harness.phase]
planning = "ohmypi"
implementation = "ohmypi"
review = "ohmypi"
foreman = "ohmypi"
```

### 7a. oh-my-pi Harness (default, fully interactive)

Package: `internal/adapter/ohmypi`. Go spawns a Bun subprocess running a bridge script. This remains the default harness in generated config and runtime selection because its bidirectional protocol is the only one currently verified for planning correction loops, review correction loops, and Foreman question/answer flows.

**Transport and protocol:** JSON lines over stdio.

**Go -> Bun (stdin):**
```json
{"type":"prompt","text":"..."}
{"type":"message","text":"..."}
{"type":"answer","text":"..."}
{"type":"abort"}
```

**Bun -> Go (stdout):**
```json
{"type":"event","event":{"type":"progress","text":"Reading src/main.go..."}}
{"type":"event","event":{"type":"question","question":"...","context":"..."}}
{"type":"event","event":{"type":"foreman_proposed","text":"...","uncertain":true}}
{"type":"event","event":{"type":"complete","summary":"3 files, 2 commits"}}
```

The `answer` stdin message resolves a pending `ask_foreman` tool call in an agent session. The `foreman_proposed` event carries the Foreman LLM's proposed answer; the orchestrator renders it in the TUI and may loop with further `message` sends before approving. `uncertain` is `true` when the Foreman signalled `CONFIDENCE: uncertain`. The bridge strips the confidence marker line from `text` before emitting. `mapEvent` returns `null` for unhandled event types; the caller filters before emitting. Stderr is logged but not parsed as protocol.

**Sandboxing and session modes:**
- macOS uses `sandbox-exec` to restrict writes to the worktree and session temp directory.
- Linux namespace-based isolation remains the intended equivalent.
- Session modes remain `agent` and `foreman`; review reuses the read-only foreman-style tool restriction rather than introducing a separate shared mode in the current contract.

### 7b. Claude Code Harness (wired, messaging parity not yet verified)

Package: `internal/adapter/claudecode`. This adapter exists behind the shared `AgentHarness` contract and is a viable non-interactive/streaming harness, but it is not yet a fully equivalent replacement for oh-my-pi because real interactive `SendMessage` continuation behavior has not been validated against an installed `claude` binary.

Current documented state:
- supports startup, prompt injection, streaming/progress parsing, completion handling, and config-driven selection/fallback
- should treat `stream-json` as the reference structured output mode
- should restrict tools via Claude Code CLI flags when running read-only or limited modes
- must not be considered production-equivalent for planning correction, review correction, or Foreman Q&A until real interactive session continuation is pinned and tested

Representative config:
```toml
[adapters.claude_code]
binary_path = "claude"
model = "sonnet"
permission_mode = "auto"
max_turns = 50
max_budget_usd = 10.00
```

**Integration notes:**
- strongest long-term candidate for rich non-OMP integration because it offers structured output, session persistence, and tool restriction flags
- still blocked on verifying the live CLI continuation protocol needed for correctness-critical `SendMessage` flows
- should remain behind explicit selection/fallback rather than replacing the oh-my-pi default until that verification exists

### 7c. Codex Harness (wired, messaging parity not yet verified)

Package: `internal/adapter/codex`. This adapter also exists behind the shared contract and is intended to provide reliable headless execution with conservative progress extraction first, richer event fidelity second.

Current documented state:
- supports startup, prompt injection, progress/completion parsing, and config-driven selection/fallback
- should prefer conservative CLI integration rather than assuming feature parity with Claude Code or oh-my-pi
- must not be considered production-equivalent for planning correction, review correction, or Foreman Q&A until real `SendMessage`-compatible behavior is validated against an installed `codex` binary

Representative config:
```toml
[adapters.codex]
binary_path = "codex"
model = "o4"
approval_mode = "full-auto"
full_auto = false
quiet = false
```

**Integration notes:**
- built-in sandboxing and headless execution are useful, but the observable event contract is not yet pinned enough to document richer behavior as stable
- should be rolled out conservatively: startup/completion first, richer event mapping only after fixture-backed parser coverage exists

### Packaging, Testing, and Risk Notes

- Bun is a runtime dependency for the default harness path and should remain documented in packaging/install flows.
- `gh` and `glab` stay optional CLIs: missing `gh` disables GitHub token fallback/login flows; missing `glab` disables GitLab MR lifecycle automation.
- The correctness-critical risk is unverified interactive messaging in Claude Code and Codex. Planning correction, review correction, and Foreman orchestration all rely on `SendMessage`; without real binary verification, documenting parity would be guesswork rather than an engineered contract.
- Unit tests for non-OMP harnesses should cover argument building, event parsing, config validation, and fallback behavior when expected event shapes are absent.
- Integration and end-to-end tests for Claude Code and Codex remain blocked until the real binaries are available to pin continuation/message semantics. Until then, the correct operational position is to use oh-my-pi as the default harness.