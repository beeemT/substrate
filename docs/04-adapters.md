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

The `answer` stdin message resolves a pending `ask_foreman` tool call in an agent session.
The `foreman_proposed` event carries the Foreman LLM's proposed answer; the orchestrator renders it in the TUI and may loop with further `message` sends before approving.
`uncertain` is `true` when the Foreman signalled `CONFIDENCE: uncertain`. The bridge strips the confidence marker line from `text` before emitting.

`mapEvent` returns `null` for unhandled event types; the caller filters before emitting (no null events reach Go).

Stderr is logged but not parsed as protocol.

### Go Side (sketch)

```go
func (h *OhMyPiHarness) StartSession(ctx context.Context, opts SessionOpts) (HarnessSession, error) {
    if opts.Mode == "" { opts.Mode = SessionModeAgent }
    workDir := opts.WorktreePath
    if workDir == "" { workDir = h.workspaceRoot } // foreman uses workspace root
    var cmd *exec.Cmd
    if runtime.GOOS == "darwin" {
        sessionTmpDir := fmt.Sprintf("/tmp/substrate-%s", opts.SessionID)
        profile := fmt.Sprintf(`(version 1)(allow default)(deny file-write* (subpath "/"))(allow file-write* (subpath "%s"))(allow file-write* (subpath "%s"))(allow file-write* (literal "/dev/null"))`, workDir, sessionTmpDir)
        cmd = exec.CommandContext(ctx, "sandbox-exec", "-p", profile, h.bunPath, "run", h.bridgePath)
    } else {
        cmd = exec.CommandContext(ctx, h.bunPath, "run", h.bridgePath)
    }
    cmd.Dir = workDir
    cmd.Env = append(os.Environ(),
        "SUBSTRATE_BRIDGE_MODE="+string(opts.Mode),
        "SUBSTRATE_THINKING_LEVEL="+h.cfg.ThinkingLevel,
        "SUBSTRATE_ALLOW_PUSH="+strconv.FormatBool(opts.AllowPush),
        "SUBSTRATE_SYSTEM_PROMPT="+base64.StdEncoding.EncodeToString([]byte(opts.SystemPrompt)))
    stdin, _ := cmd.StdinPipe()
    stdout, _ := cmd.StdoutPipe()
    if err := cmd.Start(); err != nil { return nil, fmt.Errorf("start bridge: %w", err) }
    s := &ohMyPiSession{id: opts.SessionID, cmd: cmd, stdin: stdin, events: make(chan SessionEvent, 64)}
    go s.readEvents(stdout)
    if opts.Mode == SessionModeAgent {
        s.send(bridgeMsg{Type: "prompt", Text: opts.SubPlan.RawMarkdown})
    }
    // Foreman session receives its first question via SendMessage after startup.
    return s, nil
}
```

### Subprocess Sandboxing

Agent subprocess file writes are restricted to the worktree directory at the OS level, preventing accidental or adversarial modification of `main/` worktrees or other workspace directories.

**macOS (`sandbox-exec`):** The bridge subprocess is wrapped with `sandbox-exec` using a profile that allows reads everywhere but restricts `file-write*` to the session worktree path (shown in the `StartSession` sketch above).

**Linux:** Mount namespaces (`unshare --mount`) with bind mounts achieve equivalent isolation. **Planning sessions** (no worktree) restrict writes to the `.substrate/sessions/<id>/` scratch directory only. **Review and foreman sessions** use `toolNames: ["read", "grep", "find"]` — no write tools are registered, making sandboxing redundant but retained for defence-in-depth.

Network-level git remote policy (preventing `git push origin HEAD:main`) is enforced by remote branch protection rules, not by Substrate. Substrate sets the agent's working directory to the feature worktree; `git push` from that worktree pushes only the feature branch.

```typescript
import {
    createAgentSession,
    SessionManager,
    Settings,
    type AgentSessionEvent,
    type CustomTool,
} from "@oh-my-pi/pi-coding-agent";
import { Type } from "@sinclair/typebox";
import { createInterface } from "readline";

const mode = process.env.SUBSTRATE_BRIDGE_MODE ?? "agent";  // "agent" | "foreman"
const thinkingLevel = process.env.SUBSTRATE_THINKING_LEVEL ?? "xhigh";
const systemPromptEnv = process.env.SUBSTRATE_SYSTEM_PROMPT ?? "";
const systemPrompt = systemPromptEnv
    ? Buffer.from(systemPromptEnv, "base64").toString("utf-8")
    : undefined;

const agentToolNames = mode === "agent"
    ? ["read", "grep", "find", "edit", "write", "bash"]
    : ["read", "grep", "find"]; // foreman and review: read-only, no write/bash

// ask_foreman tool: only registered in agent mode.
// Blocks until the orchestrator sends {type:"answer"} on stdin.
let pendingAnswerResolve: ((text: string) => void) | null = null;

const askForemanTool: CustomTool = {
    name: "ask_foreman",
    description: "Ask the foreman a question you cannot resolve from the plan or codebase.",
    parameters: Type.Object({
        question: Type.String({ description: "The question" }),
        context:  Type.Optional(Type.String({ description: "Surrounding context from your work" })),
    }),
    execute: async (_toolCallId, args) => {
        emit({ type: "question", question: args.question, context: args.context ?? "" });
        const answer = await new Promise<string>(resolve => { pendingAnswerResolve = resolve; });
        return answer;
    },
};

const { session } = await createAgentSession({
    cwd: process.cwd(),
    sessionManager: mode === "foreman" ? SessionManager.inMemory() : SessionManager.create(process.cwd()),
    thinkingLevel: thinkingLevel as any,
    toolNames: agentToolNames,
    spawns: "",         // prevent agent from spawning unmonitored sub-agents
    enableMCP: false,
    systemPrompt,
    customTools: mode === "agent" ? [askForemanTool] : [],
    ...(mode === "foreman" && {
        settings: Settings.isolated({ "compaction.enabled": false }),
    }),
});

let lastAssistantText = "";

session.subscribe((event: AgentSessionEvent) => {
    const mapped = mapEvent(event);
    if (mapped !== null) {
        emit(mapped);
    }
    // accumulate text for foreman_proposed / complete emission
    if (event.type === "message_update" && (event as any).assistantMessageEvent?.type === "text_delta") {
        lastAssistantText += (event as any).assistantMessageEvent.delta;
    }
});

const rl = createInterface({ input: process.stdin });
rl.on("line", async (line: string) => {
    const msg = JSON.parse(line);
    if (msg.type === "abort") { process.exit(0); }
    if (msg.type === "answer") { pendingAnswerResolve?.(msg.text); pendingAnswerResolve = null; return; }
    if (msg.type === "prompt" || msg.type === "message") {
        await runPrompt(msg.text);
    }
});

function extractConfidence(text: string): { text: string; uncertain: boolean } {
    const lines = text.split("\n");
    const last = lines[lines.length - 1].trim();
    if (last === "CONFIDENCE: high") {
        return { text: lines.slice(0, -1).join("\n").trimEnd(), uncertain: false };
    }
    if (last === "CONFIDENCE: uncertain") {
        return { text: lines.slice(0, -1).join("\n").trimEnd(), uncertain: true };
    }
    return { text, uncertain: true }; // missing marker → conservative escalation
}

async function runPrompt(text: string): Promise<void> {
    lastAssistantText = "";
    await session.prompt(text, { expandPromptTemplates: false });
    // session.prompt() resolves when the turn is complete
    if (mode === "foreman") {
        const { text: answer, uncertain } = extractConfidence(lastAssistantText);
        emit({ type: "foreman_proposed", text: answer, uncertain });
    } else {
        emit({ type: "complete", summary: "Turn completed" });
    }
}

function mapEvent(e: AgentSessionEvent): object | null {
    if (e.type === "message_update" && (e as any).assistantMessageEvent?.type === "text_delta")
        return { type: "progress", text: (e as any).assistantMessageEvent.delta };
    if (e.type === "tool_execution_start")
        return { type: "progress", text: `tool: ${(e as any).toolName}` };
    return null; // filtered by caller before emitting
}

function emit(event: object) { process.stdout.write(JSON.stringify({ type: "event", event }) + "\n"); }
```

```toml
[adapters.ohmypi]
bun_path        = "bun"
bridge_path     = "scripts/omp-bridge.ts"
thinking_level = "xhigh"  # oh-my-pi thinkingLevel; applied to all sessions (sub-agents + foreman)
                            # valid values are defined by oh-my-pi (e.g. xhigh, high, medium)
                            # maps to model-specific settings (extended thinking budget, etc.)
```
