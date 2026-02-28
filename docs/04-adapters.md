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

**Issues** (`ScopeIssues`): Query issues from the configured team; user selects 1+. On `Resolve`: if 1 issue, WorkItem mirrors it directly. If N issues, title = first issue title + `(+N-1 more)`, description concatenates all with `---` separators, labels merged. Note: the adapter does NOT populate the `Repositories` field -- repos are discovered by the planning agent by scanning the workspace for git-work managed repos.

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
func linearExternalID(issue linearIssue) string {
		// issue.Team.Key = "FOO", issue.Identifier = "FOO-123"
		parts := strings.SplitN(issue.Identifier, "-", 2)
		return "LIN-" + issue.Team.Key + "-" + parts[1] // "LIN-FOO-123"
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
                    issues, err = a.fetchIssuesByIDs(ctx, filter.ExternalIDs)
                } else {
                    issues, err = a.fetchAssignedIssues(ctx)
                }
                if err != nil { ch <- domain.WorkItemEvent{Type: domain.WatchError, Err: err}; continue }
                for _, issue := range issues {
                    if len(filter.States) > 0 && !filter.States.Contains(issue.State) { continue }
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
		store WorkspaceStore // for sequential ExternalID generation
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
				ExternalID:   externalID, // "MAN-001", "MAN-002", ...
				Source:       "manual",
				SourceScope:  domain.ScopeManual,
				Title:        sel.ManualInput.Title,
				Description:  sel.ManualInput.Description,
				State:        domain.WorkItemIngested,
				CreatedAt:    time.Now(),
				UpdatedAt:    time.Now(),
		}, nil
}

// nextManualID returns the next sequential MAN-NNN identifier for this workspace.
// Counter is derived from the count of existing manual work items in the DB for this workspace.
func (a *ManualAdapter) nextManualID(ctx context.Context) (string, error) {
		n, err := a.store.CountManualWorkItems(ctx)
		if err != nil { return "", err }
		return fmt.Sprintf("MAN-%03d", n+1), nil
}

func (a *ManualAdapter) Watch(_ context.Context, _ domain.WorkItemFilter) (<-chan domain.WorkItemEvent, error) {
    ch := make(chan domain.WorkItemEvent)
    close(ch) // manual adapter never auto-discovers
    return ch, nil
}

func (a *ManualAdapter) Fetch(_ context.Context, id string) (domain.WorkItem, error) {
    return domain.WorkItem{}, ErrNotSupported // state lives only in substrate DB
}

func (a *ManualAdapter) UpdateState(_ context.Context, _ string, _ domain.WorkItemState) error { return nil }
func (a *ManualAdapter) AddComment(_ context.Context, _ string, _ string) error              { return nil }
func (a *ManualAdapter) OnEvent(_ context.Context, _ domain.SystemEvent) error                { return nil }
```

No TOML configuration needed. The manual adapter is always available as a built-in option, registered unconditionally at startup in `internal/app/wire.go`.

ExternalID format: `MAN-NNN` (zero-padded 3-digit sequence, e.g. `MAN-001`, `MAN-042`). The counter is derived by counting existing manual work items in the DB for the current workspace — no separate counter column is required.

---

## 3. glab Adapter (RepoLifecycleAdapter)

Package: `internal/adapter/glab`. Requires `glab` CLI installed and authenticated. Validates at startup.

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
  --title "<from-plan>" --draft --push --yes \
  --title "<from-plan>" --draft --push --yes \
  --reviewer @alice --reviewer @bob --label substrate

**BranchPushed** -- MR already exists; optionally update description via `glab mr update`. Otherwise no-op.

**WorkItemCompleted** -- mark MR ready: `glab mr update <id> --draft=false`.

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

## 4. Agent Harness Interface

Package: `internal/domain/harness`. The orchestrator is harness-agnostic.

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
    AllowCommit          bool
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
    Commits  []CommitInfo
    Errors   []string
}

type CommitInfo struct { SHA, Message string }

type SessionEventType int
const (
    SessionEventProgress        SessionEventType = iota
    SessionEventQuestion        // sub-agent called ask_foreman
    SessionEventForemanProposed // foreman session produced a proposed answer
    SessionEventCommit
    SessionEventPush
    SessionEventError
    SessionEventComplete
)

type SessionEvent struct {
    Type    SessionEventType
    Payload any
}

type HarnessCapabilities struct {
    SupportsStreaming  bool
    SupportsMessaging  bool
    SupportedTools     []string
}
```

The orchestrator calls `StartSession`, reads `Events()` for TUI updates and question detection, and `Wait` for final result. Review critiques are sent via `SendMessage`.

---

## 5. oh-my-pi Harness Implementation

Package: `internal/adapter/ohmypi`. Go spawns a Bun subprocess running a bridge script.

### Protocol: JSON Lines over Stdio

**Go -> Bun (stdin):**
```json
{"type":"prompt","text":"...","system":"..."}
{"type":"message","text":"..."}          
{"type":"answer","text":"..."}           
{"type":"abort"}
```

**Bun -> Go (stdout):**
```json
{"type":"event","event":{"type":"progress","text":"Reading src/main.go..."}}          
{"type":"event","event":{"type":"question","question":"...","context":"..."}}   
{"type":"event","event":{"type":"foreman_proposed","text":"..."}}               
{"type":"event","event":{"type":"commit","sha":"a1b2c3d","message":"fix: auth flow"}}
{"type":"event","event":{"type":"complete","summary":"3 files, 2 commits"}}       
```

The `answer` stdin message resolves a pending `ask_foreman` tool call in an agent session.
The `foreman_proposed` event carries the Foreman LLM's proposed answer; the orchestrator renders it in the TUI and may loop with further `message` sends before approving.

Stderr is logged but not parsed as protocol.

### Go Side (sketch)

```go
func (h *OhMyPiHarness) StartSession(ctx context.Context, opts SessionOpts) (HarnessSession, error) {
    if opts.Mode == "" { opts.Mode = SessionModeAgent }
    workDir := opts.WorktreePath
    if workDir == "" { workDir = h.workspaceRoot } // foreman uses workspace root
    cmd := exec.CommandContext(ctx, h.bunPath, "run", h.bridgePath)
    cmd.Dir = workDir
    cmd.Env = append(os.Environ(),
        "SUBSTRATE_BRIDGE_MODE="+string(opts.Mode),
        "SUBSTRATE_REASONING_LEVEL="+h.cfg.ReasoningLevel,
        "SUBSTRATE_ALLOW_COMMIT="+strconv.FormatBool(opts.AllowCommit),
        "SUBSTRATE_ALLOW_PUSH="+strconv.FormatBool(opts.AllowPush))
    stdin, _ := cmd.StdinPipe()
    stdout, _ := cmd.StdoutPipe()
    if err := cmd.Start(); err != nil { return nil, fmt.Errorf("start bridge: %w", err) }
    s := &ohMyPiSession{id: uuid.NewString(), cmd: cmd, stdin: stdin, events: make(chan SessionEvent, 64)}
    go s.readEvents(stdout)
    if opts.Mode == SessionModeAgent {
        s.send(bridgeMsg{Type: "prompt", Text: opts.SubPlan.RawMarkdown, System: opts.SystemPrompt})
    }
    // Foreman session receives its first question via SendMessage after startup.
    return s, nil
}
```

```typescript
import { createAgentSession, SessionManager, type AgentEvent } from "@anthropic-ai/pi-coding-agent";
import { createInterface } from "readline";

const mode           = process.env.SUBSTRATE_BRIDGE_MODE    ?? "agent";     // "agent" | "foreman"
const reasoningLevel = process.env.SUBSTRATE_REASONING_LEVEL ?? "xhigh";
const allowCommit    = process.env.SUBSTRATE_ALLOW_COMMIT   === "true";

const agentTools = mode === "agent"
    ? ["read", "grep", "find", "edit", "write", "bash"]
    : ["read", "grep", "find"]; // foreman: read-only, no write/bash/commits

const session = await createAgentSession(SessionManager.inMemory(), {
    reasoningLevel,
    allowModelPromotion: false, // disabled for ALL modes — no silent context window expansion
    // Foreman: also disable auto-compaction; on context full, terminate cleanly and restart
    // with the amended plan (including FAQ). Agent sessions (planning + sub-agents) allow
    // auto-compaction; planning continuity is handled via plan-draft.md.
    ...(mode === "foreman" && {
        allowAutoCompaction: false,
    }),
    // Note: exact option names confirmed during Phase 0 oh-my-pi SDK integration.
});

// ask_foreman tool: agent mode only. Blocks until substrate sends {type:"answer"} on stdin.
let pendingAnswerResolve: ((text: string) => void) | null = null;
if (mode === "agent") {
    registerTool("ask_foreman", async (args: { question: string; context: string }) => {
        emit({ type: "question", question: args.question, context: args.context });
        return new Promise<string>(resolve => { pendingAnswerResolve = resolve; });
    });
}

const rl = createInterface({ input: process.stdin });
rl.on("line", async (line: string) => {
    const msg = JSON.parse(line);
    if (msg.type === "abort")   { await session.dispose(); process.exit(0); }
    if (msg.type === "answer")  { pendingAnswerResolve?.(msg.text); pendingAnswerResolve = null; return; }
    if (msg.type === "prompt" || msg.type === "message") await runPrompt(msg.text, msg.system);
});

async function runPrompt(text: string, system?: string) {
    const stream = session.prompt(text, { systemPrompt: system, toolNames: agentTools });
    for await (const event of stream) emit(mapEvent(event));
    // In foreman mode, each completed turn is a proposed answer.
    if (mode === "foreman") emit({ type: "foreman_proposed", text: await getLastAssistantText(stream) });
    else emit({ type: "complete", summary: "Prompt finished" });
}

function mapEvent(e: AgentEvent): object {
    if (e.type === "message_update") return { type: "progress", text: e.delta };
    if (e.type === "tool_execution_start") return { type: "progress", text: `tool: ${e.toolName}` };
    if (e.type === "tool_execution_end" && allowCommit && e.toolName === "bash"
        && e.args?.command?.startsWith("git commit"))
        return { type: "commit", sha: "pending", message: e.args.command };
    return { type: "progress", text: e.type };
}

function emit(event: object) { process.stdout.write(JSON.stringify({ type: "event", event }) + "\n"); }
```

> **Note:** `getLastAssistantText` reads the accumulated assistant message from the stream.
> The exact oh-my-pi API for `reasoningLevel` and the `registerTool` signature are confirmed during
> Phase 0 integration; the shape above reflects intent.

```toml
[adapters.ohmypi]
bun_path        = "bun"
bridge_path     = "scripts/omp-bridge.ts"
reasoning_level = "xhigh"  # oh-my-pi reasoning level; applied to all sessions (sub-agents + foreman)
                            # valid values are defined by oh-my-pi (e.g. xhigh, high, medium)
                            # maps to model-specific settings (extended thinking budget, etc.)
```

---

## 6. Documentation Source Interface

Package: `internal/domain/docs`

```go
type DocSourceType int
const (
    DocSourceRepoEmbedded DocSourceType = iota
    DocSourceDedicatedRepo
)

type DocType int
const (
    DocTypeADR DocType = iota
    DocTypeAPISpec
    DocTypeArchitecture
    DocTypeConvention
    DocTypeRunbook
    DocTypeOther
)

type DocumentationSource interface {
    Name() string
    Type() DocSourceType
    Fetch(ctx context.Context, opts DocFetchOpts) ([]Document, error)
    Search(ctx context.Context, query string) ([]DocumentMatch, error)
    Sync(ctx context.Context) error
    CheckStale(ctx context.Context, changes []FileChange) ([]StaleDoc, error)
}

type DocFetchOpts struct {
    Types    []DocType
    RepoName string
    Paths    []string // specific paths; empty = use configured globs
}

type Document struct {
    Path         string
    Title        string // from first # heading or filename
    Content      string
    Type         DocType
    RepoName     string
    LastModified time.Time
}

type DocumentMatch struct {
    Document  Document
    MatchLine int
    MatchText string
    Score     float64
}

type FileChange struct { RepoName, FilePath, ChangeType string }
type StaleDoc struct { Document Document; Reason string; RelatedFiles []string }
```

### RepoEmbeddedSource

Reads docs from glob patterns within each repo's **main** worktree. Planning always uses canonical baseline, never feature worktrees. `Sync` is a no-op (workspace setup handles git pull). `Search` does in-process substring/regex matching -- file sizes are small enough.

```toml
[[documentation.repo_embedded]]
repo_name = "backend-api"
globs     = ["docs/**/*.md", "README.md", "ARCHITECTURE.md"]
[documentation.repo_embedded.type_map]
"docs/adr/*" = "adr"
"docs/api/*" = "api_spec"
"ARCHITECTURE.md" = "architecture"
```

### DedicatedRepoSource

Separate git repo cloned via git-work into the workspace. `Sync` runs `git pull --ff-only` in its main worktree before every planning phase. Fetch/Search work identically to RepoEmbeddedSource.

```toml
[documentation.dedicated_repo]
repo_url  = "git@gitlab.com:org/engineering-docs.git"
repo_name = "engineering-docs"
globs     = ["**/*.md"]
[documentation.dedicated_repo.type_map]
"adr/*" = "adr"
"api/*" = "api_spec"
"conventions/*" = "convention"
"runbooks/*" = "runbook"
```

### Planning Integration & Registration

All sources are synced, fetched, and concatenated (with `--- path ---` boundaries) into the planner's context. Sub-plans receive only docs filtered by `RepoName` and keyword matching. After implementation, `CheckStale` is called with changed files; `StaleDoc` results emit `DocumentationStale` events routed to work item adapter and TUI. Adapters are wired at startup in `internal/app/wire.go` based on config. Domain logic depends only on interfaces -- no adapter is hard-coded.
