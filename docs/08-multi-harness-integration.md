# 08 - Multi-Harness Integration Plan

> Current implementation status for oh-my-pi, Claude Code, and Codex harness support in Substrate.

## Executive Summary

Substrate now has **implemented multi-harness selection infrastructure** in the codebase, but only **oh-my-pi is the default and fully interactive harness** today.

**Current shipped state:**
1. **oh-my-pi** remains the default harness in config and generated defaults because it is the only harness with proven interactive messaging support for planning correction, review correction, and Foreman question/answer flows.
2. **Claude Code** and **Codex CLI** now have selectable adapter packages and central wiring, but their implementations currently cover startup, prompt injection, streaming/progress, completion, and fallback selection — not verified interactive `SendMessage` parity.
3. **Homebrew packaging** now needs to treat Bun as a runtime dependency because the default oh-my-pi bridge depends on it.

**Most important status note:** The multi-harness architecture work is partially complete, but the Claude Code and Codex interactive messaging milestone is blocked on access to the real `claude` and `codex` binaries. They are not installed in the current development environment, and the repo does not contain vendored protocol fixtures for their live follow-up messaging behavior.

**Why this matters:** Substrate's current orchestrator uses `SendMessage` in correctness-critical paths. Without observing the real CLIs, implementing interactive follow-up messaging for Claude Code or Codex would be guesswork, not an engineered integration.

---

---

## 1. Current State Analysis

### 1.1 Existing Harness Interface

From `04-adapters.md`, the current interface is:

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

type HarnessCapabilities struct {
    SupportsStreaming  bool
    SupportsMessaging  bool
    SupportedTools     []string
}
```

### 1.2 Current oh-my-pi Implementation

The existing implementation uses:
- **Transport:** JSON Lines over stdio via Bun bridge script
- **Protocol:** Bidirectional message passing
- **Messages:** `prompt`, `message`, `answer`, `abort` (stdin) → `event`, `progress`, `question`, `complete` (stdout)
- **Sandboxing:** macOS `sandbox-exec`, Linux namespaces (deferred)
- **Session modes:** `agent`, `foreman`

### 1.3 Constraints Imposed by Current Contracts

The current contracts matter more than vendor feature lists:
- `04-adapters.md` defines only two execution modes today: `agent` and `foreman`.
- `HarnessSession` currently exposes `SendMessage`, but not a first-class `SendAnswer(questionID, answer)` API.
- `SessionResult` is intentionally small today: `ExitCode`, `Summary`, `Errors`.
- `03-event-system.md` already covers the orchestration events Substrate needs: session start/completion/failure, questions raised/answered, review start/completion, and reimplementation.

That means the chosen solution should be implemented in two layers:
1. **Near-term:** add Claude Code and Codex adapters behind the current contracts.
2. **Follow-on contract evolution:** extend the harness interfaces only where real adapter implementation proves the current surface is insufficient.

This is the main architectural correction to the earlier draft: proposed interface growth is reasonable, but it should not be presented as if Substrate already has those contracts.

### 1.4 What Is Implemented in the Repository Today

The following pieces are now implemented in code:
- Harness selection config and defaults exist in `internal/config/config.go`.
- A central harness builder/fallback path exists in `internal/app/harness.go`.
- Startup wiring in `cmd/substrate/main.go` now builds per-phase harnesses instead of hardcoding oh-my-pi.
- `internal/adapter/claudecode/` and `internal/adapter/codex/` exist and implement the current `AgentHarness` contract.
- Review log parsing was generalized so non-oh-my-pi plain-text logs are still consumable by the review pipeline.

### 1.5 What Remains Incomplete

The missing piece is **real interactive messaging support** for Claude Code and Codex. Specifically:
- `planning.go` uses `SendMessage` for plan correction retries.
- `review.go` uses `SendMessage` for critique-output correction retries.
- `foreman.go` uses `SendMessage` and expects a usable answer event flow for question handling.

At the time this note was written, neither `claude` nor `codex` was installed in the working environment, so their live interactive protocols could not be exercised or pinned by tests.

### 1.6 Current Recommendation

Treat the current state as:
- **Production-ready:** oh-my-pi default path
- **Infrastructure-ready but not interaction-complete:** Claude Code and Codex selection/wiring
- **Blocked follow-up:** interactive `SendMessage` parity for Claude Code and Codex once the binaries are available

---

## 2. Target Harnesses

### 2.1 Claude Code CLI

**Vendor:** Anthropic  
**License:** Proprietary (requires subscription)  
**Language:** Node.js/TypeScript

**Integration Characteristics:**
| Aspect | Details |
|--------|---------|
| **Non-interactive mode** | `-p` flag with `--output-format json` or `stream-json` |
| **System prompt** | `--system-prompt`, `--system-prompt-file`, `--append-system-prompt` |
| **Tool control** | `--tools "Bash,Edit,Read"` to restrict available tools |
| **Session management** | `--resume`, `--continue`, `--session-id` |
| **MCP support** | Yes - can connect to external MCP servers |
| **Structured output** | `stream-json` and schema-oriented output paths |
| **Permission modes** | `--permission-mode plan`, `--dangerously-skip-permissions` |

**CLI Example (non-interactive):**
```bash
claude -p --output-format stream-json \
    --system-prompt-file ./system-prompt.txt \
    --tools "Bash,Edit,Read,Glob,Grep" \
    "Implement the authentication middleware as described"
```

**Strengths:**
- First-class non-interactive mode with structured output
- Native MCP support for tool extension
- Strong session persistence and resumption
- Built-in tool restriction model fits Substrate's foreman/read-only needs well

**Challenges:**
- Proprietary, requires Anthropic subscription
- Output format must be version-pinned and parser-tested
- Question escalation still needs an adapter-specific strategy compatible with Substrate's current `SendMessage`-oriented contract

**Integration Pattern:** Stdout streaming with JSON parsing

**Integration Readiness:** ✅ Highest

---

### 2.2 Codex CLI (OpenAI)

**Vendor:** OpenAI  
**License:** Apache 2.0  
**Language:** Go

Codex CLI is OpenAI's official coding agent that runs locally on the developer's machine.

**Integration Characteristics:**
| Aspect | Details |
|--------|---------|
| **Non-interactive mode** | Full headless mode via `codex` CLI |
| **Approval mode** | `--approval-mode` for autonomous execution |
| **Model selection** | `-m` flag for model choice |
| **Working directory** | `-w` flag for workspace isolation |
| **Full auto mode** | `--full-auto` for complete autonomy |
| **Sandboxing** | Built-in filesystem isolation |
| **Quiet mode** | `-q` for reduced output |

**CLI Example (non-interactive):**
```bash
codex -w /path/to/project -m o4 \
    --approval-mode full-auto \
    "Implement the authentication middleware as described"
```

**Strengths:**
- First-class headless/autonomous mode
- Go-based, which makes operational behavior familiar in a Go codebase
- Built-in filesystem sandboxing
- Approval modes map reasonably well to Substrate execution policies
- Apache 2.0 licensed

**Challenges:**
- Requires OpenAI API access
- Output/event contract must be pinned before Substrate depends on it for rich TUI updates
- No MCP-based extension story comparable to Claude Code
- Question/clarification flow needs deliberate adapter design rather than assuming parity with oh-my-pi

**Integration Pattern:** CLI subprocess with stdout parsing, initially targeting reliable completion semantics before rich event fidelity

**Integration Readiness:** ✅ High

---

## 3. Integration Architecture

### 3.1 Recommended Architecture

Use one adapter package per supported harness, each implementing the existing `AgentHarness` contract first.

```text
Substrate Orchestrator
        |
        v
  AgentHarness interface
   /                \
  v                  v
ClaudeCodeAdapter   CodexAdapter
  |                  |
  v                  v
claude CLI         codex CLI
```

This preserves the existing layering described in `04-adapters.md`: orchestration depends on a stable harness interface, while subprocess details remain adapter-private.

### 3.2 Contract Evolution, Carefully Sequenced

The earlier draft proposed a broader interface with extra modes, richer results, and explicit answer routing. That direction is sensible, but it should be staged.

**Recommended sequence:**

1. **Implement adapters against the current contract first**
   - `Name`
   - `StartSession`
   - `Capabilities`
   - `HarnessSession` with `Wait`, `Events`, `SendMessage`, `Abort`

2. **Promote only proven extensions into shared contracts**
   - Add richer capability fields only if orchestration actually consumes them.
   - Add explicit answer APIs only if Claude Code or Codex cannot be made reliable with the current message flow.
   - Expand `SessionResult` only after the UI, persistence layer, and event consumers have a concrete need.

3. **Keep orchestration events in the event bus, not duplicated in adapter contracts**
   - `03-event-system.md` already models session lifecycle and question events.
   - Adapter event translation should feed those orchestration concepts, not redefine the workflow model.

### 3.3 Proposed Future Interface Additions

The following remain reasonable candidates for future contract work, but they are **not current state**:

```go
type HarnessMode string

const (
    HarnessModeAgent   HarnessMode = "agent"
    HarnessModeForeman HarnessMode = "foreman"
    HarnessModeReview  HarnessMode = "review"
    HarnessModePlan    HarnessMode = "plan"
)

type HarnessCapabilities struct {
    SupportsStreaming    bool
    SupportsMessaging    bool
    SupportsQuestions    bool
    SupportsResume       bool
    SupportedModes       []HarnessMode
    DefaultTools         []string
    CanRestrictTools     bool
    RequiresSubscription bool
    IntegrationMaturity  MaturityLevel
}
```

Treat this as a target shape for a future refactor, not as an immediate prerequisite.

### 3.4 Canonical Adapter Event Shape

Even if the shared Go interfaces stay small initially, adapter implementations still benefit from a canonical internal translation model:

```go
type BridgeEvent struct {
    Type    string          `json:"type"`
    Time    time.Time       `json:"time"`
    Payload json.RawMessage `json:"payload"`
}

type ProgressPayload struct {
    Text string `json:"text"`
}

type QuestionPayload struct {
    ID      string `json:"id"`
    Content string `json:"content"`
    Context string `json:"context,omitempty"`
}

type CompletePayload struct {
    Summary     string   `json:"summary"`
    ExitCode    int      `json:"exit_code"`
    FilesChanged []string `json:"files_changed"`
}
```

This internal shape is useful because it isolates vendor output parsing from the rest of the adapter code, but it should remain an internal adapter concern until there is a demonstrated shared package need.

---

## 4. Harness-Specific Integration Plans

### 4.1 Claude Code Integration

**Priority:** Highest  
**Maturity Target:** Stable → Preferred

#### Implementation Approach

```go
// internal/adapter/claudecode/adapter.go

type ClaudeCodeAdapter struct {
    cfg       ClaudeCodeConfig
    cmdRunner exec.CmdRunner
}

type ClaudeCodeConfig struct {
    BinaryPath     string  `toml:"binary_path"`
    Model          string  `toml:"model"`
    PermissionMode string  `toml:"permission_mode"`
    MaxTurns       int     `toml:"max_turns"`
    MaxBudgetUSD   float64 `toml:"max_budget_usd"`
}

func (a *ClaudeCodeAdapter) StartSession(ctx context.Context, opts SessionOpts) (HarnessSession, error) {
    args := a.buildArgs(opts)
    cmd := exec.CommandContext(ctx, a.cfg.BinaryPath, args...)
    cmd.Dir = opts.WorktreePath

    stdin, _ := cmd.StdinPipe()
    stdout, _ := cmd.StdoutPipe()
    stderr, _ := cmd.StderrPipe()

    if err := cmd.Start(); err != nil {
        return nil, fmt.Errorf("start claude code: %w", err)
    }

    session := &claudeCodeSession{
        id:     opts.SessionID,
        cmd:    cmd,
        stdin:  stdin,
        stdout: stdout,
        stderr: stderr,
        events: make(chan SessionEvent, 256),
    }

    go session.readEvents()

    if opts.SubPlan.RawMarkdown != "" {
        session.sendPrompt(opts.SubPlan.RawMarkdown)
    }

    return session, nil
}
```

#### Review Notes on This Approach

This is the strongest part of the original plan.
- It matches the current adapter boundary in `04-adapters.md`.
- It preserves subprocess isolation.
- It gives Substrate a parser-friendly event stream.

The only material caution is to avoid depending on rich event variants until parser tests pin the exact `stream-json` shapes Substrate expects.

#### Event Translation

Claude Code should be the reference adapter for structured event translation:

```go
func (s *claudeCodeSession) readEvents() {
    defer close(s.events)

    scanner := bufio.NewScanner(s.stdout)
    for scanner.Scan() {
        line := scanner.Bytes()

        var event map[string]any
        if err := json.Unmarshal(line, &event); err != nil {
            continue
        }

        eventType, _ := event["type"].(string)
        switch eventType {
        case "assistant":
            s.translateAssistantEvent(event)
        case "tool_use":
            s.translateToolUseEvent(event)
        case "result":
            s.translateResultEvent(event)
        case "error":
            s.translateErrorEvent(event)
        }
    }
}
```

#### Configuration

```toml
[adapters.claude-code]
enabled = true
binary_path = "claude"
model = "sonnet"
permission_mode = "auto"
max_turns = 50
max_budget_usd = 10.00
```

---

### 4.2 Codex CLI Integration

**Priority:** High  
**Maturity Target:** Stable

#### Implementation Approach

```go
// internal/adapter/codex/adapter.go

type CodexAdapter struct {
    cfg       CodexConfig
    cmdRunner exec.CmdRunner
}

type CodexConfig struct {
    BinaryPath   string `toml:"binary_path"`
    Model        string `toml:"model"`
    ApprovalMode string `toml:"approval_mode"`
    FullAuto     bool   `toml:"full_auto"`
    Quiet        bool   `toml:"quiet"`
}

func (a *CodexAdapter) StartSession(ctx context.Context, opts SessionOpts) (HarnessSession, error) {
    args := a.buildArgs(opts)
    cmd := exec.CommandContext(ctx, a.cfg.BinaryPath, args...)
    cmd.Dir = opts.WorktreePath

    stdin, _ := cmd.StdinPipe()
    stdout, _ := cmd.StdoutPipe()
    stderr, _ := cmd.StderrPipe()

    if err := cmd.Start(); err != nil {
        return nil, fmt.Errorf("start codex: %w", err)
    }

    session := &codexSession{
        id:     opts.SessionID,
        cmd:    cmd,
        stdin:  stdin,
        stdout: stdout,
        stderr: stderr,
        events: make(chan SessionEvent, 256),
    }

    go session.readEvents()

    if opts.SubPlan.RawMarkdown != "" {
        session.sendPrompt(opts.SubPlan.RawMarkdown)
    }

    return session, nil
}
```

#### Design Guidance

Codex should not be forced to match Claude Code's richness on day one. The safer rollout is:
1. reliable process startup and completion handling,
2. robust prompt injection,
3. conservative progress extraction,
4. richer event mapping only after fixture-based parser coverage exists.

That keeps Codex support real without pretending its observable CLI contract is already as friendly as Claude Code's.

#### Configuration

```toml
[adapters.codex]
enabled = true
binary_path = "codex"
model = "o4"
approval_mode = "full-auto"
full_auto = false
quiet = false
```

---

## 5. Configuration Architecture

### 5.1 TOML Configuration Schema

```toml
# substrate.toml

[harness]
# Default harness for all sessions
default = "ohmypi"  # "ohmypi" | "claude-code" | "codex"

# Fallback harnesses if default/phase target is unavailable
fallback = ["claude-code", "codex"]

# Per-phase harness overrides
[harness.phase]
planning = "ohmypi"
implementation = "ohmypi"
review = "ohmypi"
foreman = "ohmypi"

[adapters.ohmypi]
bun_path = "bun"
bridge_path = "bridge/omp-bridge.ts"
thinking_level = "high"

[adapters.claude_code]
binary_path = "claude"
model = "sonnet"
permission_mode = "auto"
max_turns = 50
max_budget_usd = 10.00

[adapters.codex]
binary_path = "codex"
model = "o4"
approval_mode = "full-auto"
full_auto = false
quiet = false
```

### 5.2 Environment Variables

Each harness respects its native environment variables:

```bash
# oh-my-pi default path
BUN_INSTALL=...

# Claude Code
ANTHROPIC_API_KEY=sk-ant-...

# Codex CLI
OPENAI_API_KEY=sk-...
```

### 5.3 Selection Policy

Current policy in the repository is:
- **Default:** oh-my-pi
- **Fallback order:** Claude Code, then Codex CLI
- **Per-phase overrides:** supported via config
- **Health checks:** binary existence is checked before harness selection succeeds

### 5.4 Packaging Status

Current packaging/install state:
- Substrate's release workflow generates the Homebrew formula in `.github/workflows/release.yml`.
- The formula depends on `beeemT/tap/git-work` and `oven-sh/bun/bun`, matching Substrate's required worktree-management and default harness runtime dependencies.
- `gh` and `glab` are intentionally treated as optional CLIs rather than hard formula dependencies: missing `gh` disables GitHub CLI fallback/login flows, and missing `glab` disables GitLab MR lifecycle automation.
- README install guidance taps the required Homebrew sources while documenting `gh`/`glab` as optional capabilities.

---

## 6. Implementation Phases

### Phase 1: Interface Refinement (Week 1)

1. Keep the current `AgentHarness` and `HarnessSession` contracts as the implementation baseline.
2. Identify the smallest contract additions actually required by Claude Code and Codex.
3. Define internal adapter parsing types and fixtures.
4. Write interface tests using mock harnesses.
5. Document which proposed interface fields are deferred.

**Gate:** Existing contracts still compile cleanly; adapter tests prove the current interface is sufficient or justify each extension.

### Phase 2: Harness Selection and Basic Adapters (Completed in Repo)

1. Add config-driven harness selection.
2. Add per-phase harness wiring.
3. Add Claude Code adapter package.
4. Add Codex adapter package.
5. Add fallback selection when the preferred harness binary is unavailable.
6. Update tests for config and harness construction.

**Status:** Completed in the repository.

### Phase 3: Claude Code Interactive Messaging (Blocked)

1. Verify the real `claude` CLI supports resumable or iterative follow-up messaging compatible with `SendMessage`.
2. Pin the exact protocol for continuing an in-flight or resumable session.
3. Implement adapter-side follow-up messaging.
4. Add integration tests against the real binary.

**Status:** Blocked by missing `claude` binary in the current environment.

### Phase 4: Codex Interactive Messaging (Blocked)

1. Verify the real `codex` CLI supports resumable or iterative follow-up messaging compatible with `SendMessage`.
2. Pin the exact protocol for follow-up messaging and completion semantics.
3. Implement adapter-side follow-up messaging.
4. Add integration tests against the real binary.

**Status:** Blocked by missing `codex` binary in the current environment.

### Phase 5: Question Routing and Foreman Parity (Blocked on 3 and 4)

1. Make `planning.go`, `review.go`, and `foreman.go` safe against harnesses without proven interactive messaging.
2. Complete answer-event parity needed by Foreman flows.
3. Add end-to-end validation with the real harness binaries.

**Status:** Not complete. Depends on Phases 3 and 4.

### Phase 6: Documentation and Packaging Follow-through (Partially Complete)

1. Record the current implementation status in this document.
2. Keep oh-my-pi as the default documented path.
3. Ensure Brew/tap output reflects Bun dependency for the default harness.
4. Update installation and troubleshooting docs after interactive messaging lands for Claude/Codex.

**Status:** Partially complete. Current-state documentation and Bun dependency updates are done; final docs depend on the blocked interactive work.

---

## 7. Testing Strategy

### 7.1 Unit Tests

Each adapter should have unit tests for:
- Argument building
- Event parsing
- Error handling
- Configuration validation
- Fallback behavior when expected event shapes are absent

```go
func TestClaudeCodeAdapter_BuildArgs(t *testing.T) {
    adapter := &ClaudeCodeAdapter{cfg: ClaudeCodeConfig{
        Model: "sonnet",
        PermissionMode: "auto",
    }}

    opts := SessionOpts{
        SubPlan: SubPlan{RawMarkdown: "test prompt"},
    }

    args := adapter.buildArgs(opts)

    assert.Contains(t, args, "-p")
    assert.Contains(t, args, "--output-format")
    assert.Contains(t, args, "stream-json")
    assert.Contains(t, args, "--model")
    assert.Contains(t, args, "sonnet")
}
```

### 7.2 Integration Tests

Tagged tests should cover real harness binaries:

```go
//go:build integration

func TestClaudeCodeSession_FullLifecycle(t *testing.T) {
    adapter := NewClaudeCodeAdapter(loadConfig(t))

    opts := SessionOpts{
        SessionID:   ulid.Make().String(),
        WorktreePath: t.TempDir(),
        SubPlan: SubPlan{
            RawMarkdown: "Create a file called hello.txt with content 'Hello, World!'",
        },
    }

    session, err := adapter.StartSession(context.Background(), opts)
    require.NoError(t, err)

    result, err := session.Wait(context.Background())
    require.NoError(t, err)
    assert.Equal(t, 0, result.ExitCode)
    assert.FileExists(t, filepath.Join(opts.WorktreePath, "hello.txt"))
}
```

### 7.3 E2E Tests

Full Substrate workflow with each supported harness:

```go
//go:build e2e

func TestE2E_ClaudeCode_FullWorkflow(t *testing.T) {
    // ingest → plan → implement → review → complete
}

func TestE2E_Codex_FullWorkflow(t *testing.T) {
    // same workflow with Codex CLI
}
```

---

## 8. Harness Comparison Matrix

| Feature | oh-my-pi | Claude Code | Codex CLI |
|---------|----------|-------------|-----------|
| **Default in repo today** | ✅ Yes | ❌ No | ❌ No |
| **Non-interactive mode** | ✅ Via bridge | ✅ `-p` flag | ✅ Full headless |
| **Streaming output** | ✅ Proven | ✅ Basic adapter support | ✅ Basic adapter support |
| **Interactive `SendMessage` parity** | ✅ Implemented | ❌ Not yet verified | ❌ Not yet verified |
| **Foreman/review correction compatibility** | ✅ Implemented | ⚠️ Incomplete | ⚠️ Incomplete |
| **Tool restriction** | ✅ Bridge-controlled | ✅ CLI flag support | ⚠️ Approval/sandbox model only |
| **Packaging readiness for default path** | ✅ With Bun installed | N/A | N/A |
| **Current implementation state** | Production path | Adapter present, messaging blocked | Adapter present, messaging blocked |

**Conclusion:** The repository is ready to continue from a solid OMP-default multi-harness base, but Claude Code and Codex should not be treated as fully equivalent harnesses until their interactive messaging behavior is implemented against the real binaries.

---

## 9. Risk Register

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Claude/Codex interactive protocol differs from assumptions | High | High | Do not implement from guesswork; validate against installed binaries first |
| Harness binary not installed | High | Medium | Keep oh-my-pi as default; fail early in harness construction; document prerequisites |
| Bun missing on default install path | Medium | High | Make Homebrew formula depend on Bun; document Bun as required for default harness |
| Review/planning correction loops assume `SendMessage` works | High | High | Keep OMP as default until Claude/Codex messaging parity is real and tested |
| CLI output format changes | Medium | High | Pin versions, parser fixtures, compatibility tests |
| Codex event fidelity weaker than Claude Code | Medium | Medium | Keep Codex adapter conservative until protocol is pinned |

---

## 10. Future Considerations

### 10.1 MCP as a Harness Differentiator

Claude Code's MCP support makes it the better long-term target for shared tool integration. If Substrate later exposes capabilities via MCP, Claude Code can likely adopt them earlier and more naturally than Codex.

### 10.2 Shared Question/Answer Contract

If both adapters need explicit answer routing, promote that into the shared harness contract only after implementation proves the shape. Avoid baking oh-my-pi-specific assumptions into the new abstraction.

### 10.3 Harness Performance Metrics

Track per-harness metrics:
- Success rate
- Time to completion
- Cost per task
- Revision rate after review
- Question rate

### 10.4 Automatic Harness Selection

Do not implement this initially. Revisit only after collecting enough production data to justify task-based selection instead of explicit configuration.

### 10.5 Resume Point for Future Work

When this work resumes, start with these concrete tasks:
1. Install and authenticate the real `claude` CLI in the development environment.
2. Install and authenticate the real `codex` CLI in the development environment.
3. Capture real transcript fixtures for:
   - initial session start
   - follow-up `SendMessage`/continuation
   - completion after a follow-up message
4. Update `internal/adapter/claudecode/` and `internal/adapter/codex/` to use those verified flows.
5. Add integration tests that prove planning correction, review correction, and foreman interaction semantics.

Until then, the correct operational position is: use oh-my-pi as the default harness.

---

## 11. Appendix: Event Format Mappings

### 11.1 Claude Code Event Mapping

```yaml
Claude Code JSON:
  type: "assistant"
  content: [...]
Maps to:
  SessionEvent.Type: EventProgress
  Payload: ProgressPayload{Text: content.text}

Claude Code JSON:
  type: "tool_use"
  name: "Edit"
  input: {...}
Maps to:
  SessionEvent.Type: EventProgress or adapter-private tool event
  Payload: translated tool metadata

Claude Code JSON:
  type: "result"
  content: "..."
Maps to:
  SessionEvent.Type: EventComplete
  Payload: CompletePayload{Summary: content}
```

### 11.2 Codex Event Mapping

```yaml
Codex CLI stdout:
  "...progress line..."
Maps to:
  SessionEvent.Type: EventProgress
  Payload: ProgressPayload{Text: line}

Codex CLI stdout/stderr or exit state:
  completion summary + exit code
Maps to:
  SessionEvent.Type: EventComplete
  Payload: CompletePayload{Summary: summary, ExitCode: code}
```

---

## 12. References

- **Claude Code CLI Reference:** https://code.claude.com/docs/en/cli-reference
- **Claude Code MCP:** https://code.claude.com/docs/en/mcp
- **Codex CLI:** https://github.com/openai/codex
- **Substrate Adapters:** `04-adapters.md`
- **Substrate Event System:** `03-event-system.md`
