# 08 - Multi-Harness Integration Plan

> Integrating multiple AI coding assistants (Claude Code, Copilot CLI, Aider, Goose, Crush, and future tools) as interchangeable harnesses within Substrate.

## Executive Summary

Substrate's architecture already defines a clean `AgentHarness` interface (see `04-adapters.md`) that abstracts agent execution from orchestration. This document outlines how to extend this interface to support multiple AI coding assistants as interchangeable "harnesses," enabling users to choose their preferred tool while maintaining Substrate's orchestration, planning, and review capabilities.

**Key Insight:** The current oh-my-pi harness uses a JSON-over-stdio protocol via a Bun bridge script. This pattern—subprocess with structured I/O—is the natural integration pattern for all CLI-based coding assistants.

---

## 1. Current State Analysis

### 1.1 Existing Harness Interface

From `04-adapters.md`, the current interface:

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
    SupportsMessaging  bool    // Can receive mid-session messages
    SupportedTools     []string
}
```

### 1.2 Current oh-my-pi Implementation

The existing implementation uses:
- **Transport:** JSON Lines over stdio via Bun bridge script
- **Protocol:** Bidirectional message passing
- **Messages:** `prompt`, `message`, `answer`, `abort` (stdin) → `event`, `progress`, `question`, `complete` (stdout)
- **Sandboxing:** macOS `sandbox-exec`, Linux namespaces (deferred)
- **Session modes:** `agent` (full tools), `foreman` (read-only)

---

## 2. Target Harnesses Analysis

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
| **Structured output** | `--json-schema` for validated JSON output |
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
- Agent SDK for advanced programmatic usage
- Strong session persistence and resumption
- Built-in sub-agent spawning capability

**Challenges:**
- Proprietary, requires Anthropic subscription
- Output format may change (need version pinning)
- No built-in way to inject custom tools (relies on MCP)

**Integration Pattern:** Stdout streaming with JSON parsing

---

### 2.2 GitHub Copilot CLI

**Vendor:** GitHub/Microsoft  
**License:** Requires Copilot subscription  
**Language:** Node.js

**Integration Characteristics:**
| Aspect | Details |
|--------|---------|
| **Non-interactive mode** | Limited - primarily interactive TUI |
| **Custom agents** | Yes - via `.github/agents/` markdown profiles |
| **Skills system** | Yes - composable command packages |
| **MCP support** | Yes - GitHub MCP server built-in, extensible |
| **Custom instructions** | `.github/copilot-instructions.md`, `AGENTS.md` |
| **Autonomous mode** | `--allow-all` or `--yolo` flags |
| **Agent selection** | `--agent=<name>` flag |

**CLI Example:**
```bash
copilot --agent=task --prompt "Implement auth middleware" --allow-all
```

**Strengths:**
- Deep GitHub integration
- Built-in MCP server for GitHub operations
- Custom agents via markdown profiles
- Skills for specialized tasks

**Challenges:**
- **Primary concern:** Designed for interactive use, non-interactive support is limited
- No structured JSON output mode documented
- TUI-centric design makes programmatic integration harder
- Requires GitHub Copilot subscription

**Integration Pattern:** TUI automation or PTY wrapper (lower confidence)

**Recommendation:** Mark as "experimental" integration due to interactive-first design. Monitor for API/CLI improvements.

---

### 2.3 Aider

**Vendor:** Open Source (Apache 2.0)  
**License:** Apache 2.0  
**Language:** Python

**Integration Characteristics:**
| Aspect | Details |
|--------|---------|
| **Non-interactive mode** | `--message` / `-m` flag for single command |
| **Auto-accept** | `--yes` flag to skip confirmations |
| **Multi-provider** | OpenAI, Anthropic, Gemini, Groq, Ollama, etc. |
| **Git integration** | Native, auto-commits changes |
| **Python API** | `Coder` class for programmatic usage |
| **Streaming** | `--stream` / `--no-stream` toggle |
| **Edit formats** | Multiple (diff-fenced, diff, udiff, etc.) |

**CLI Example:**
```bash
aider --yes --message "Implement auth middleware" \
    --auto-commits auth.py middleware.py
```

**Python API Example:**
```python
from aider.coders import Coder
from aider.models import Model
from aider.io import InputOutput

io = InputOutput(yes=True)
model = Model("claude-3-5-sonnet")
coder = Coder.create(main_model=model, fnames=["auth.py"], io=io)
coder.run("Implement authentication middleware")
```

**Strengths:**
- Excellent scripting support (CLI and Python)
- Multi-provider flexibility
- Open source with active development
- Strong git integration with auto-commits
- Repository map for efficient context

**Challenges:**
- Python API not officially stable
- No built-in question escalation mechanism
- Stream output format not structured JSON

**Integration Pattern:** 
- **Primary:** CLI with `--yes --message` and stdout parsing
- **Alternative:** Python subprocess with Coder API (more control)

---

### 2.4 Goose

**Vendor:** Block (Square)  
**License:** Apache 2.0  
**Language:** Rust

**Integration Characteristics:**
| Aspect | Details |
|--------|---------|
| **Non-interactive mode** | CLI supports headless execution |
| **Multi-provider** | OpenAI, Anthropic, and many others |
| **MCP support** | Yes - first-class extension mechanism |
| **Extensions** | Rust-based extension system |
| **Desktop + CLI** | Both available |

**CLI Example:**
```bash
goose run "Implement authentication middleware" --no-tui
```

**Strengths:**
- Open source with permissive license
- Rust-based (fast, reliable)
- Strong MCP support
- Extensible architecture
- Multi-provider support

**Challenges:**
- Newer project, API stability uncertain
- Less documentation on programmatic usage
- Rust makes custom bridge scripts harder

**Integration Pattern:** CLI with stdout parsing (needs investigation of headless mode)

---

### 2.5 Crush (formerly OpenCode)

**Vendor:** Charmbracelet  
**License:** MIT  
**Language:** Go

**Integration Characteristics:**
| Aspect | Details |
|--------|---------|
| **Non-interactive mode** | To be determined (project recently moved) |
| **Multi-provider** | Yes (inherited from OpenCode) |
| **TUI framework** | Bubble Tea (same as Substrate) |
| **SQLite storage** | Session persistence |
| **LSP support** | Yes |
| **Tools** | bash, edit, file, glob, grep, ls, patch, diagnostics |

**Note:** OpenCode was archived and moved to Charmbracelet as "Crush". Details on the new project's CLI interface are still emerging.

**Strengths:**
- Go-based (same as Substrate)
- Bubble Tea TUI (familiar patterns)
- Multi-provider support
- MIT license

**Challenges:**
- Project in transition
- Non-interactive mode details TBD
- Documentation still developing

**Integration Pattern:** TBD - monitor project development

---

## 3. Integration Architecture

### 3.1 Enhanced Harness Interface

The current interface needs extension to accommodate diverse harness capabilities:

```go
// HarnessMode defines the execution mode for a session
type HarnessMode string

const (
    HarnessModeAgent    HarnessMode = "agent"     // Full coding agent
    HarnessModeForeman  HarnessMode = "foreman"   // Read-only, Q&A
    HarnessModeReview   HarnessMode = "review"    // Code review
    HarnessModePlan     HarnessMode = "plan"      // Planning only
)

// HarnessCapabilities describes what a harness can do
type HarnessCapabilities struct {
    // Core capabilities
    SupportsStreaming    bool          // Real-time progress events
    SupportsMessaging    bool          // Mid-session message injection
    SupportsQuestions    bool          // Can escalate questions
    SupportsResume       bool          // Can resume interrupted sessions
    
    // Session modes supported
    SupportedModes       []HarnessMode
    
    // Tool restrictions
    DefaultTools         []string      // Tools available by default
    CanRestrictTools     bool          // Supports tool restriction
    
    // Provider info
    Providers            []string      // Supported LLM providers
    RequiresSubscription bool          // Needs paid subscription
    
    // Integration quality
    IntegrationMaturity  MaturityLevel // How well-tested the integration is
}

type MaturityLevel int

const (
    MaturityExperimental MaturityLevel = iota  // May have issues
    MaturityBeta                                // Works for common cases
    MaturityStable                              // Production-ready
    MaturityPreferred                           // Recommended default
)

// SessionOpts contains all configuration for a harness session
type SessionOpts struct {
    // Identity
    SessionID            string            // Substrate-generated ULID
    
    // Mode and behavior
    Mode                 HarnessMode       // Execution mode
    WorktreePath         string            // Working directory (empty for foreman)
    DraftPath            string            // Plan draft path (planning sessions)
    
    // Context
    SubPlan              SubPlan           // Repository-specific plan
    CrossRepoPlan        string            // Full orchestration context
    SystemPrompt         string            // Custom system prompt
    DocumentationContext string            // Additional docs
    
    // Behavior controls
    AllowPush            bool              // Can push to remote
    AutoCommit           bool              // Auto-commit changes
    Tools                []string          // Restricted tool set (if supported)
    
    // Harness-specific configuration
    HarnessConfig        map[string]any    // Provider-specific options
}

// HarnessSession represents an active session
type HarnessSession interface {
    ID() string
    
    // Lifecycle
    Wait(ctx context.Context) (SessionResult, error)
    Abort(ctx context.Context) error
    
    // Event stream
    Events() <-chan SessionEvent
    
    // Bidirectional communication (if supported)
    SendMessage(ctx context.Context, msg string) error
    SendAnswer(ctx context.Context, questionID, answer string) error
    
    // Capability query
    Capabilities() HarnessCapabilities
}

// SessionEvent types
type SessionEventType int

const (
    EventProgress        SessionEventType = iota  // Text/progress update
    EventQuestion                                  // Agent needs clarification
    EventToolUse                                   // Tool execution started
    EventToolResult                                // Tool execution completed
    EventFileEdit                                  // File modified
    EventCommit                                    // Commit created
    EventPush                                      // Pushed to remote
    EventError                                     // Error occurred
    EventComplete                                  // Session finished
)

type SessionEvent struct {
    Type      SessionEventType
    Timestamp time.Time
    Payload   any                   // Type-specific payload
}

// SessionResult is the final outcome
type SessionResult struct {
    ExitCode    int
    Summary     string
    FilesChanged []string
    Commits     []CommitInfo
    Errors      []string
    TokensUsed  int64
    CostUSD     float64
}
```

### 3.2 Bridge Adapter Pattern

Each harness gets a "bridge adapter" that translates between Substrate's protocol and the harness's native interface:

```
┌─────────────────────────────────────────────────────────────────┐
│                        Substrate Core                           │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                    AgentHarness Interface                 │   │
│  └──────────────────────────────────────────────────────────┘   │
│                              │                                   │
│              ┌───────────────┼───────────────┐                  │
│              ▼               ▼               ▼                  │
│  ┌────────────────┐ ┌────────────────┐ ┌────────────────┐      │
│  │  Bridge Adapter│ │  Bridge Adapter│ │  Bridge Adapter│      │
│  │   (Common)     │ │   (Common)     │ │   (Common)     │      │
│  └────────┬───────┘ └────────┬───────┘ └────────┬───────┘      │
│           │                  │                  │               │
└───────────┼──────────────────┼──────────────────┼───────────────┘
            │                  │                  │
     ┌──────▼──────┐    ┌──────▼──────┐    ┌──────▼──────┐
     │   oh-my-pi  │    │ Claude Code │    │    Aider    │
     │   Bridge    │    │   Bridge    │    │   Bridge    │
     │   Script    │    │   Wrapper   │    │   Wrapper   │
     └──────┬──────┘    └──────┬──────┘    └──────┬──────┘
            │                  │                  │
     ┌──────▼──────┐    ┌──────▼──────┐    ┌──────▼──────┐
     │  oh-my-pi   │    │ Claude Code │    │   Aider     │
     │   (Bun)     │    │    CLI      │    │   CLI       │
     └─────────────┘    └─────────────┘    └─────────────┘
```

### 3.3 Common Bridge Protocol

Define a common internal protocol that all bridges translate to/from:

```go
// BridgeProtocol defines the canonical event format
// All adapters translate their harness's output to this format

type BridgeEvent struct {
    Type    string          `json:"type"`
    Time    time.Time       `json:"time"`
    Payload json.RawMessage `json:"payload"`
}

// Event payloads (canonical forms)
type ProgressPayload struct {
    Text string `json:"text"`
}

type QuestionPayload struct {
    ID      string `json:"id"`
    Content string `json:"content"`
    Context string `json:"context,omitempty"`
}

type FileEditPayload struct {
    Path        string `json:"path"`
    Operation   string `json:"operation"` // create, modify, delete
    LinesAdded  int    `json:"lines_added"`
    LinesRemoved int   `json:"lines_removed"`
}

type CommitPayload struct {
    SHA     string `json:"sha"`
    Message string `json:"message"`
    Files   []string `json:"files"`
}

type CompletePayload struct {
    Summary     string   `json:"summary"`
    ExitCode    int      `json:"exit_code"`
    FilesChanged []string `json:"files_changed"`
}
```

---

## 4. Harness-Specific Integration Plans

### 4.1 Claude Code Integration

**Priority:** High (best non-interactive support)  
**Maturity Target:** Stable → Preferred

#### Implementation Approach

```go
// internal/adapter/claudecode/adapter.go

type ClaudeCodeAdapter struct {
    cfg          ClaudeCodeConfig
    cmdRunner    exec.CmdRunner
}

type ClaudeCodeConfig struct {
    BinaryPath     string        `toml:"binary_path"`      // "claude" by default
    Model          string        `toml:"model"`            // "sonnet", "opus", or full name
    PermissionMode string        `toml:"permission_mode"`  // "plan", "accept", "auto"
    FallbackModel  string        `toml:"fallback_model"`   // For rate limit fallback
    MaxTurns       int           `toml:"max_turns"`        // Limit agentic turns
    MaxBudgetUSD   float64       `toml:"max_budget_usd"`   // Cost limit
}

func (a *ClaudeCodeAdapter) StartSession(ctx context.Context, opts SessionOpts) (HarnessSession, error) {
    args := a.buildArgs(opts)
    
    cmd := exec.CommandContext(ctx, a.cfg.BinaryPath, args...)
    cmd.Dir = opts.WorktreePath
    
    // Set up pipes
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
    
    // Send initial prompt if provided
    if opts.SubPlan.RawMarkdown != "" {
        session.sendPrompt(opts.SubPlan.RawMarkdown)
    }
    
    return session, nil
}

func (a *ClaudeCodeAdapter) buildArgs(opts SessionOpts) []string {
    args := []string{
        "-p",                           // Print mode (non-interactive)
        "--output-format", "stream-json", // Structured output
    }
    
    // Model selection
    if a.cfg.Model != "" {
        args = append(args, "--model", a.cfg.Model)
    }
    
    // Tool restrictions
    if len(opts.Tools) > 0 && a.Capabilities().CanRestrictTools {
        args = append(args, "--tools", strings.Join(opts.Tools, ","))
    }
    
    // Permission mode
    if a.cfg.PermissionMode != "" {
        args = append(args, "--permission-mode", a.cfg.PermissionMode)
    }
    
    // System prompt
    if opts.SystemPrompt != "" {
        args = append(args, "--append-system-prompt", opts.SystemPrompt)
    }
    
    // Cost/turn limits
    if a.cfg.MaxTurns > 0 {
        args = append(args, "--max-turns", fmt.Sprintf("%d", a.cfg.MaxTurns))
    }
    if a.cfg.MaxBudgetUSD > 0 {
        args = append(args, "--max-budget-usd", fmt.Sprintf("%.2f", a.cfg.MaxBudgetUSD))
    }
    
    // Mode-specific tool restrictions
    switch opts.Mode {
    case HarnessModeForeman, HarnessModeReview:
        args = append(args, "--tools", "Read,Grep,Glob")
    }
    
    return args
}
```

#### Event Translation

Claude Code's `stream-json` output format needs translation:

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
        
        // Translate Claude Code events to canonical form
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
[adapters.claudecode]
enabled = true
binary_path = "claude"
model = "sonnet"
permission_mode = "auto"
max_turns = 50
max_budget_usd = 10.00

[adapters.claudecode.providers.anthropic]
# Uses ANTHROPIC_API_KEY by default

[adapters.claudecode.providers.bedrock]
# AWS Bedrock configuration if needed
```

---

### 4.2 Aider Integration

**Priority:** High (excellent scripting support)  
**Maturity Target:** Stable

#### Implementation Approach

```go
// internal/adapter/aider/adapter.go

type AiderAdapter struct {
    cfg       AiderConfig
    cmdRunner exec.CmdRunner
}

type AiderConfig struct {
    BinaryPath    string   `toml:"binary_path"`     // "aider" by default
    Model         string   `toml:"model"`           // "claude-3-5-sonnet", etc.
    EditFormat    string   `toml:"edit_format"`     // "diff-fenced", "diff", etc.
    AutoCommits   bool     `toml:"auto_commits"`    // Auto-commit changes
    Stream        bool     `toml:"stream"`          // Stream responses
    ContextFile   string   `toml:"context_file"`    // Context from AGENTS.md
}

func (a *AiderAdapter) StartSession(ctx context.Context, opts SessionOpts) (HarnessSession, error) {
    args := a.buildArgs(opts)
    
    // Aider takes files as positional arguments
    files := opts.SubPlan.FileTargets
    if len(files) == 0 {
        // Default to common source patterns
        files = []string{"."} // Current directory
    }
    
    cmd := exec.CommandContext(ctx, a.cfg.BinaryPath, append(args, files...)...)
    cmd.Dir = opts.WorktreePath
    
    stdin, _ := cmd.StdinPipe()
    stdout, _ := cmd.StdoutPipe()
    
    if err := cmd.Start(); err != nil {
        return nil, fmt.Errorf("start aider: %w", err)
    }
    
    session := &aiderSession{
        id:     opts.SessionID,
        cmd:    cmd,
        stdin:  stdin,
        stdout: stdout,
        events: make(chan SessionEvent, 256),
    }
    
    go session.readEvents()
    
    // Send initial prompt
    if opts.SubPlan.RawMarkdown != "" {
        session.sendPrompt(opts.SubPlan.RawMarkdown)
    }
    
    return session, nil
}

func (a *AiderAdapter) buildArgs(opts SessionOpts) []string {
    args := []string{
        "--yes",           // Auto-accept all
        "--no-check-update",
    }
    
    // Message mode (non-interactive)
    if opts.SubPlan.RawMarkdown != "" {
        args = append(args, "--message", opts.SubPlan.RawMarkdown)
    }
    
    // Model selection
    if a.cfg.Model != "" {
        args = append(args, "--model", a.cfg.Model)
    }
    
    // Edit format
    if a.cfg.EditFormat != "" {
        args = append(args, "--edit-format", a.cfg.EditFormat)
    }
    
    // Auto-commits
    if a.cfg.AutoCommits || opts.AutoCommit {
        args = append(args, "--auto-commits")
    } else {
        args = append(args, "--no-auto-commits")
    }
    
    // Stream mode
    if a.cfg.Stream {
        args = append(args, "--stream")
    }
    
    return args
}
```

#### Aider Question Handling

Aider doesn't have a native question escalation mechanism. We need to detect when it needs clarification:

```go
func (s *aiderSession) readEvents() {
    defer close(s.events)
    
    scanner := bufio.NewScanner(s.stdout)
    var outputBuffer strings.Builder
    
    for scanner.Scan() {
        line := scanner.Text()
        outputBuffer.WriteString(line + "\n")
        
        // Detect Aider's question patterns
        if strings.Contains(line, "How would you like me to") ||
           strings.Contains(line, "Do you want me to") ||
           strings.HasSuffix(line, "?") {
            // Escalate as question
            s.events <- SessionEvent{
                Type: EventQuestion,
                Payload: QuestionPayload{
                    ID:      ulid.Make().String(),
                    Content: line,
                },
            }
        }
        
        // Emit progress
        s.events <- SessionEvent{
            Type: EventProgress,
            Payload: ProgressPayload{
                Text: line,
            },
        }
    }
}
```

#### Configuration

```toml
[adapters.aider]
enabled = true
binary_path = "aider"
model = "claude-3-5-sonnet-20241022"
edit_format = "diff-fenced"
auto_commits = true
stream = true

# API keys via environment: ANTHROPIC_API_KEY, OPENAI_API_KEY, etc.
```

---

### 4.3 Copilot CLI Integration

**Priority:** Medium (experimental due to interactive-first design)  
**Maturity Target:** Experimental

#### Implementation Approach

```go
// internal/adapter/copilot/adapter.go

type CopilotAdapter struct {
    cfg CopilotConfig
}

type CopilotConfig struct {
    BinaryPath string `toml:"binary_path"` // "copilot" by default
    Agent      string `toml:"agent"`       // Default agent to use
    AllowAll   bool   `toml:"allow_all"`   // --allow-all flag
}

func (a *CopilotAdapter) StartSession(ctx context.Context, opts SessionOpts) (HarnessSession, error) {
    // WARNING: Copilot CLI is designed for interactive use
    // This integration uses PTY automation which may be fragile
    
    args := []string{
        "--agent", a.selectAgent(opts.Mode),
    }
    
    if a.cfg.AllowAll {
        args = append(args, "--allow-all")
    }
    
    if opts.SubPlan.RawMarkdown != "" {
        args = append(args, "--prompt", opts.SubPlan.RawMarkdown)
    }
    
    // Use PTY for TUI automation
    pty, err := pty.Start(exec.Command(a.cfg.BinaryPath, args...))
    if err != nil {
        return nil, fmt.Errorf("start copilot: %w", err)
    }
    
    session := &copilotSession{
        id:   opts.SessionID,
        pty:  pty,
        // ... PTY-based event reading
    }
    
    return session, nil
}

func (a *CopilotAdapter) selectAgent(mode HarnessMode) string {
    switch mode {
    case HarnessModeReview:
        return "code-review"
    case HarnessModePlan:
        return "explore"
    default:
        return a.cfg.Agent // "task" or user-configured
    }
}
```

**Note:** This integration is marked experimental. The PTY-based approach may break with Copilot CLI updates. Monitor for official non-interactive mode support.

---

### 4.4 Goose Integration

**Priority:** Medium  
**Maturity Target:** Beta

#### Implementation Approach

```go
// internal/adapter/goose/adapter.go

type GooseAdapter struct {
    cfg GooseConfig
}

type GooseConfig struct {
    BinaryPath string `toml:"binary_path"` // "goose" by default
    Provider   string `toml:"provider"`    // LLM provider
    Model      string `toml:"model"`       // Model name
    NoTUI      bool   `toml:"no_tui"`      // Disable TUI
}

func (g *GooseAdapter) StartSession(ctx context.Context, opts SessionOpts) (HarnessSession, error) {
    args := []string{
        "run",
        "--no-tui",
    }
    
    if g.cfg.Provider != "" {
        args = append(args, "--provider", g.cfg.Provider)
    }
    
    if g.cfg.Model != "" {
        args = append(args, "--model", g.cfg.Model)
    }
    
    // Goose takes the prompt as the last argument
    prompt := opts.SubPlan.RawMarkdown
    if opts.SystemPrompt != "" {
        prompt = opts.SystemPrompt + "\n\n" + prompt
    }
    args = append(args, prompt)
    
    cmd := exec.CommandContext(ctx, g.cfg.BinaryPath, args...)
    cmd.Dir = opts.WorktreePath
    
    // ... similar to other adapters
}
```

---

### 4.5 Crush Integration

**Priority:** Low (monitor development)  
**Maturity Target:** Experimental

**Status:** Waiting for Crush (Charmbracelet's fork of OpenCode) to stabilize. Go-based architecture makes it a natural fit, but non-interactive mode details are TBD.

---

## 5. Configuration Architecture

### 5.1 TOML Configuration Schema

```toml
# substrate.toml

[harness]
# Default harness for all sessions
default = "claude-code"  # "oh-my-pi" | "claude-code" | "aider" | "copilot" | "goose"

# Fallback harness if default fails
fallback = "aider"

# Per-phase harness overrides
[harness.phases]
planning = "claude-code"     # Use Claude Code for planning
implementation = "aider"     # Use Aider for implementation
review = "claude-code"       # Use Claude Code for review
foreman = "claude-code"      # Use Claude Code for foreman

# Harness-specific configurations
[adapters.ohmypi]
enabled = true
bun_path = "bun"
bridge_path = "scripts/omp-bridge.ts"
thinking_level = "xhigh"

[adapters.claude-code]
enabled = true
binary_path = "claude"
model = "sonnet"
permission_mode = "auto"
max_turns = 50
max_budget_usd = 10.00

[adapters.aider]
enabled = true
binary_path = "aider"
model = "claude-3-5-sonnet-20241022"
edit_format = "diff-fenced"
auto_commits = true
stream = true

[adapters.copilot]
enabled = true
binary_path = "copilot"
agent = "task"
allow_all = false  # Set true for autonomous mode

[adapters.goose]
enabled = false  # Experimental
binary_path = "goose"
provider = "anthropic"
model = "claude-3-5-sonnet-latest"
```

### 5.2 Environment Variables

Each harness respects its native environment variables:

```bash
# Claude Code
ANTHROPIC_API_KEY=sk-ant-...

# Aider (supports multiple)
ANTHROPIC_API_KEY=sk-ant-...
OPENAI_API_KEY=sk-...

# Copilot CLI
GITHUB_TOKEN=ghp_...

# Goose
ANTHROPIC_API_KEY=sk-ant-...
```

---

## 6. Implementation Phases

### Phase 1: Interface Refinement (Week 1)

1. **Extend `HarnessCapabilities`** with new fields
2. **Add `HarnessMode`** type for mode-specific behavior
3. **Define canonical event types** in `internal/domain/harness/`
4. **Update `SessionOpts`** with new configuration options
5. **Write interface tests** using mock harnesses

**Gate:** All existing tests pass, new interface compiles cleanly

### Phase 2: Claude Code Integration (Week 2-3)

1. **Create `internal/adapter/claudecode/` package**
2. **Implement `ClaudeCodeAdapter`** with CLI invocation
3. **Implement event stream parser** for `stream-json` format
4. **Handle system prompt injection** via `--append-system-prompt`
5. **Implement tool restriction** via `--tools` flag
6. **Add configuration support** in TOML
7. **Write integration tests** (requires ANTHROPIC_API_KEY)
8. **Document setup and usage**

**Gate:** Full session lifecycle works (start, monitor, complete)

### Phase 3: Aider Integration (Week 3-4)

1. **Create `internal/adapter/aider/` package**
2. **Implement `AiderAdapter`** with CLI invocation
3. **Implement stdout parsing** for progress and questions
4. **Handle auto-commit integration** with Substrate's commit strategy
5. **Add configuration support** in TOML
6. **Write integration tests** (requires API key)
7. **Document setup and usage**

**Gate:** Full session lifecycle works with auto-commits

### Phase 4: Harness Selection Logic (Week 4)

1. **Create `internal/orchestrator/harness_selector.go`**
2. **Implement phase-based harness selection**
3. **Implement fallback logic** when primary harness fails
4. **Add per-repo harness override** (via `[repos.<name>]` config)
5. **Implement harness health checks** (binary exists, API key set)
6. **Write unit tests** for selection logic

**Gate:** Can configure different harnesses per phase, fallback works

### Phase 5: Question Routing Abstraction (Week 5)

1. **Abstract question escalation** across harnesses
2. **Implement `ask_foreman` equivalent** for each harness
   - Claude Code: Custom MCP tool or prompt-based
   - Aider: Detect question patterns, pause for answer
   - Copilot: Leverage built-in question handling
3. **Standardize question/answer protocol**
4. **Update Foreman to route to active harness**

**Gate:** Questions from any harness route through Foreman → Human

### Phase 6: Copilot CLI (Experimental) (Week 5-6)

1. **Create `internal/adapter/copilot/` package**
2. **Implement PTY-based TUI automation**
3. **Add configuration support** in TOML
4. **Mark as experimental** in capabilities
5. **Write basic integration tests**
6. **Document limitations and risks**

**Gate:** Basic session works, documented as experimental

### Phase 7: Goose Integration (Week 6-7)

1. **Create `internal/adapter/goose/` package**
2. **Investigate headless CLI mode**
3. **Implement adapter** based on findings
4. **Add configuration support** in TOML
5. **Write integration tests**
6. **Document usage**

**Gate:** Basic session works with Goose

### Phase 8: Testing & Documentation (Week 7-8)

1. **Comprehensive integration test suite**
2. **Harness comparison documentation**
3. **Migration guide** from oh-my-pi to other harnesses
4. **Performance benchmarking** across harnesses
5. **Cost comparison** documentation
6. **Troubleshooting guide** per harness

**Gate:** Full E2E test passes with each harness

---

## 7. Testing Strategy

### 7.1 Unit Tests

Each adapter has unit tests for:
- Argument building
- Event parsing
- Error handling
- Configuration validation

```go
func TestClaudeCodeAdapter_BuildArgs(t *testing.T) {
    adapter := &ClaudeCodeAdapter{cfg: ClaudeCodeConfig{
        Model: "sonnet",
        PermissionMode: "auto",
    }}
    
    opts := SessionOpts{
        Mode: HarnessModeAgent,
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

Tagged tests that require real harness binaries:

```go
//go:build integration

func TestClaudeCodeSession_FullLifecycle(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping integration test")
    }
    
    adapter := NewClaudeCodeAdapter(loadConfig(t))
    
    opts := SessionOpts{
        SessionID: ulid.Make().String(),
        Mode:      HarnessModeAgent,
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

Full Substrate workflow with each harness:

```go
//go:build e2e

func TestE2E_ClaudeCode_FullWorkflow(t *testing.T) {
    // Test full workflow: ingest → plan → implement → review → complete
    // Using Claude Code as the harness
}

func TestE2E_Aider_FullWorkflow(t *testing.T) {
    // Same test with Aider
}
```

---

## 8. Harness Comparison Matrix

| Feature | oh-my-pi | Claude Code | Aider | Copilot CLI | Goose |
|---------|----------|-------------|-------|-------------|-------|
| **Non-interactive mode** | ✅ Native | ✅ `-p` flag | ✅ `--message` | ⚠️ Limited | ✅ `--no-tui` |
| **Streaming output** | ✅ JSON | ✅ `stream-json` | ⚠️ Text | ❌ TUI only | ✅ |
| **Question escalation** | ✅ Custom tool | ⚠️ Via MCP | ⚠️ Pattern detect | ✅ Built-in | ❓ TBD |
| **Tool restriction** | ✅ | ✅ `--tools` | ❌ | ⚠️ Agent-based | ❓ TBD |
| **Session resume** | ✅ | ✅ `--resume` | ✅ | ✅ `--resume` | ❓ TBD |
| **Multi-provider** | ❌ Claude only | ❌ Claude only | ✅ Many | ❌ GitHub only | ✅ Many |
| **MCP support** | ❌ | ✅ Native | ❌ | ✅ Native | ✅ Native |
| **Open source** | ✅ | ❌ | ✅ Apache 2.0 | ❌ | ✅ Apache 2.0 |
| **Auto-commit** | ✅ Configurable | ✅ | ✅ Native | ⚠️ Via git | ❓ TBD |
| **Cost tracking** | ❌ | ✅ | ⚠️ Via provider | ✅ Dashboard | ❓ TBD |
| **Integration maturity** | Preferred | Target: Stable | Target: Stable | Experimental | Target: Beta |

---

## 9. Risk Register

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| CLI output format changes | Medium | High | Pin versions, version detection, extensive parsing tests |
| API rate limiting | High | Medium | Fallback harness, exponential backoff, budget limits |
| Subscription requirements | High | Low | Clear documentation, open-source alternatives (Aider, Goose) |
| Question detection unreliable | Medium | Medium | Multiple detection strategies, conservative escalation |
| PTY automation fragile (Copilot) | High | Medium | Mark experimental, monitor for official API |
| Harness binary not installed | Medium | Low | Pre-flight checks, helpful error messages |
| Context overflow across harnesses | Medium | Medium | Standardize context format, compact on overflow |
| Cost overrun with multiple harnesses | Medium | Medium | Per-harness budget limits, aggregate tracking |
| Goose API instability | Medium | Medium | Mark as beta, monitor releases |
| Crush direction unclear | Medium | Low | Monitor Charmbracelet announcements |

---

## 10. Future Considerations

### 10.1 MCP as Universal Tool Protocol

As MCP (Model Context Protocol) gains adoption, consider:
- **MCP bridge:** Create a Substrate MCP server that exposes Substrate's capabilities
- **Tool sharing:** Allow harnesses to use Substrate's tools via MCP
- **Cross-harness tools:** Tools defined once, usable by any MCP-compliant harness

### 10.2 Custom Tool Injection

For harnesses without native custom tools:
- **Prompt-based tools:** Define tool behavior in system prompt
- **Output parsing:** Detect tool invocations in output, execute, return results
- **File-based protocol:** Write tool requests to file, external executor handles

### 10.3 Harness Performance Metrics

Track per-harness metrics:
- **Success rate:** Percentage of sessions completing successfully
- **Time to completion:** Average session duration
- **Cost per task:** API costs for similar tasks
- **Revision rate:** How often review finds issues
- **Question rate:** How often human intervention needed

### 10.4 Automatic Harness Selection

ML-based harness selection:
- Track which harness performs best for which task types
- Automatically select optimal harness based on task characteristics
- A/B testing for harness comparison

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
  SessionEvent.Type: EventToolUse
  Payload: ToolUsePayload{Name: "Edit", Input: input}

Claude Code JSON:
  type: "result"
  content: "..."
  cost_usd: 0.05
Maps to:
  SessionEvent.Type: EventComplete
  Payload: CompletePayload{Summary: content, CostUSD: 0.05}
```

### 11.2 Aider Event Mapping

```yaml
Aider stdout:
  "Added file.py with 50 lines"
Maps to:
  SessionEvent.Type: EventFileEdit
  Payload: FileEditPayload{Path: "file.py", Operation: "create", LinesAdded: 50}

Aider stdout:
  "Commit abc123: Add feature"
Maps to:
  SessionEvent.Type: EventCommit
  Payload: CommitPayload{SHA: "abc123", Message: "Add feature"}

Aider stdout:
  "How would you like me to proceed?"
Maps to:
  SessionEvent.Type: EventQuestion
  Payload: QuestionPayload{Content: "How would you like me to proceed?"}
```

---

## 12. References

- **Claude Code CLI Reference:** https://code.claude.com/docs/en/cli-reference
- **Claude Code MCP:** https://code.claude.com/docs/en/mcp
- **Aider Documentation:** https://aider.chat/docs/
- **Aider Scripting:** https://aider.chat/docs/scripting.html
- **Copilot CLI:** https://docs.github.com/en/copilot/how-tos/copilot-cli/use-copilot-cli
- **Goose:** https://github.com/block/goose
- **OpenCode/Crush:** https://github.com/charmbracelet/crush
- **Substrate Adapters:** `04-adapters.md`
- **Substrate Event System:** `03-event-system.md`
