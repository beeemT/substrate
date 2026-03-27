# Claude Agent SDK Migration

Replace the raw `claude` CLI subprocess harness with a bridge-backed harness that uses
`@anthropic-ai/claude-agent-sdk`. This unlocks SendMessage, Steer, session resume,
richer event types, subagents, hooks, and structured permissions.

**Status:** Decisions resolved. Ready to implement.

---

## 1. Current State and Limitations

`internal/adapter/claudecode/harness.go` shells out directly to the `claude` binary:

```
claude -p --output-format stream-json [--model ...] [--permission-mode ...] [prompt]
```

It reads JSONL from stdout and maps exactly three event types:
- `assistant` → `text_delta`
- `result` → `done`
- `error` → `error`

**Gaps against the adapter interface:**

| Interface method | Current behaviour |
|-----------------|-------------------|
| `SendMessage` | Returns `errors.New("does not support SendMessage")` |
| `Steer` | Returns `ErrSteerNotSupported` |
| `SupportsNativeResume` | False; new process every time |
| `SupportsMessaging` | False |
| Tool events | Dropped — `tool_use` and `tool_result` are silently ignored |
| `ask_foreman` | Not supported |

The current integration is not in active use, so there are no existing sessions to
preserve and no migration period needed.

---

## 2. Why the Bridge Pattern

The Agent SDK is TypeScript/Python only; Substrate is Go. The oh-my-pi harness already
proves the bridge pattern for exactly this situation. We reuse the same architecture:
a TypeScript process manages the SDK lifecycle over stdio, Go drives it via JSON-line
messages.

The Claude Agent SDK spawns the user's existing `claude` binary — so their auth (OAuth,
API key, Bedrock, etc.) is inherited exactly as today. The SDK also picks up the user's
Claude Code settings by default via `settingSources: ["user", "project"]`, so their
model preferences, custom instructions, and per-project config apply without any
Substrate config required.

---

## 3. Decisions Made

| # | Question | Decision |
|---|----------|----------|
| Q1 | `ask_foreman` tool | Implement now via `createSdkMcpServer()` |
| Q2 | OS-level sandboxing | Copy sandbox-exec / bwrap from omp harness |
| Q3 | Harness naming | Transparent replacement: keep `claude-code` name and `claude_code` config key; replace implementation in-place |
| Q4 | `effort` / `binary_path` | Drop both; SDK picks up user's Claude defaults via `settingSources` |

**Q3 consequence:** `ClaudeCodeConfig` is modified, not replaced. It gains `bun_path`
and `bridge_path`; loses `binary_path`. The harness name string stays `"claude-code"`,
the YAML key stays `claude_code`. The internal Go package moves from `claudecode` to
`claudeagent`.

---

## 4. Target Architecture

```
Substrate (Go)
  └─ claudeagent.Harness.StartSession()
       └─ exec: bun run bridge/claude-agent-bridge.ts  (or compiled binary)
            ├─ stdin  JSON-line protocol (Go → Bridge)
            ├─ stdout JSON-line protocol (Bridge → Go)
            └─ @anthropic-ai/claude-agent-sdk
                 └─ query({ prompt: asyncIterable, options: {...} })
                      └─ spawns: claude  [user's auth + user's settings]
                           └─ MCP: substrate in-process server
                                └─ ask_foreman tool
```

---

## 5. Stdio Protocol

### Go → Bridge (stdin)

All messages are JSON objects, one per line.

| Message | When | Fields |
|---------|------|--------|
| `init` | First line, always | `mode: "agent"\|"foreman"`, `system_prompt?: string`, `resume_session_id?: string`, `permission_mode?: string`, `model?: string`, `max_turns?: number`, `max_budget_usd?: number` |
| `prompt` | Initial task | `text: string` |
| `message` | Follow-up (SendMessage) | `text: string` |
| `steer` | Mid-stream interrupt | `text: string` |
| `answer` | Resolve pending ask_foreman | `text: string` |
| `abort` | Terminate session | — |

`init` must be first. All config travels through `init` — not through environment
variables (which appear in `/proc/pid/environ`). Exception: `SUBSTRATE_WORKTREE_PATH`
must be in the environment because the bridge sets its `cwd` from it before `init`
arrives and before the bridge process can read stdin.

### Bridge → Go (stdout)

| Message | When | Fields |
|---------|------|--------|
| `session_meta` | After SDK init | `session_id: string` |
| `event` | During execution | `event: EventObject` |

#### EventObject inner types

| `event.type` | `adapter.AgentEvent.Type` | Notes |
|-------------|--------------------------|-------|
| `input` | `input` | Echo; `input_kind: "prompt"\|"message"\|"steer"` |
| `assistant_output` | `text_delta` | Text content block |
| `thinking_output` | `text_delta` | Thinking block; `metadata.thinking: true` |
| `tool_start` | `tool_start` | `tool`, `text` (JSON input), `intent?` |
| `tool_result` | `tool_result` | `tool`, `text`, `is_error: bool` |
| `lifecycle` | see below | `stage`, `summary?`, `message?` |
| `question` | `question` | `question: string`, `context: string` |
| `foreman_proposed` | `foreman_proposed` | `text: string`, `uncertain: bool` |

`lifecycle.stage` → `adapter.AgentEvent.Type`:
- `started` → `started`
- `completed` → `done`
- `failed` → `error`
- `retry_wait` → `retry_wait`
- `retry_resumed` → `retry_resumed`

---

## 6. SDK Message → Bridge Event Mapping

```
SystemMessage  { type: "system", subtype: "init", session_id }
  → emit session_meta { session_id }
  → emit event { type: "lifecycle", stage: "started" }

AssistantMessage  { type: "assistant", message: { content: [...] } }
  → for each content block:
       text block       → event { type: "assistant_output", text }
       thinking block   → event { type: "thinking_output", text }
       tool_use block   → event { type: "tool_start", tool: name, text: JSON(input) }

UserMessage  { type: "user", message: { content: [...] } }
  → for each tool_result block:
       → event { type: "tool_result", tool: tool_use_id, text: content, is_error }

ResultMessage  { type: "result", subtype: "success", result }
  → if foreman mode: emit event { type: "foreman_proposed", text, uncertain }
  → else: emit event { type: "lifecycle", stage: "completed", summary: result }

ResultMessage  { type: "result", subtype: "error_*" | "error_max_turns" }
  → emit event { type: "lifecycle", stage: "failed", message }
```

The Agent SDK does not stream partial tool results between `tool_use` and `tool_result`.
There are no `tool_output` (streaming) events in this harness, unlike omp-bridge. This
can be added if the SDK gains partial tool result streaming in future.

---

## 7. Multi-Turn via Streaming Input Mode

The bridge uses the SDK's async iterable prompt mode to keep the session alive across
multiple Go `SendMessage` calls. A `LineQueue` (identical to the one in omp-bridge)
feeds stdin lines to the SDK generator:

```typescript
async function* userTurns(): AsyncIterable<SDKUserMessage> {
  while (true) {
    const line = await lines.next();
    if (line === null) return;           // stdin closed → end session

    const msg = JSON.parse(line);

    if (msg.type === "abort") {
      process.exit(0);
    }
    if (msg.type === "answer") {
      // Handled separately by pendingAnswerResolve; not a user turn.
      if (pendingAnswerResolve) {
        emitInput("answer", msg.text);
        pendingAnswerResolve(msg.text);
        pendingAnswerResolve = null;
      }
      continue;
    }
    if (msg.type === "prompt" || msg.type === "message") {
      emitInput(msg.type, msg.text);
      yield { role: "user", content: [{ type: "text", text: msg.text }] };
    }
    if (msg.type === "steer") {
      emitInput("steer", msg.text);
      await activeQuery.interrupt();     // interrupt current agent turn
      yield { role: "user", content: [{ type: "text", text: msg.text }] };
    }
  }
}
```

`activeQuery` is the `Query` object returned by `query()`, stored in a variable so the
`steer` branch can call `.interrupt()` on it.

---

## 8. `ask_foreman` Tool

Registered as an in-process MCP tool using the SDK's `createSdkMcpServer()`. Only
added in agent mode (foreman sessions don't need to ask questions).

```typescript
import { query, tool, createSdkMcpServer } from "@anthropic-ai/claude-agent-sdk";
import { z } from "zod";

let pendingAnswerResolve: ((text: string) => void) | null = null;

const askForemanTool = tool(
  "ask_foreman",
  "Ask the foreman a clarifying question you cannot resolve from the plan or codebase.",
  {
    question: z.string().describe("The question to ask"),
    context:  z.string().optional().describe("Surrounding context (optional)"),
  },
  async ({ question, context }) => {
    emit({ type: "question", question, context: context ?? "" });

    const answer = await new Promise<string>(resolve => {
      pendingAnswerResolve = resolve;
      setTimeout(() => {
        if (pendingAnswerResolve === resolve) {
          pendingAnswerResolve = null;
          resolve("[No answer received within timeout. Proceed with your best judgment.]");
        }
      }, 10 * 60 * 1000);
    });

    return { content: [{ type: "text", text: answer }] };
  }
);

const substrateMcpServer = createSdkMcpServer({
  name: "substrate",
  version: "1.0.0",
  tools: [askForemanTool],
});
```

The MCP tool is referenced in `allowedTools` as `"mcp__substrate__ask_foreman"` (the
SDK's naming convention: `mcp__<serverName>__<toolName>`).

The `answer` stdin message resolves `pendingAnswerResolve` directly — the same pattern
as omp-bridge, but without the async iterable yield path since answers aren't user turns.

---

## 9. Session Resume

1. Bridge emits `session_meta { session_id }` from the `system/init` SDK message.
2. Go session captures `claudeSessionID` string (like `ompSessionID` on omp session).
3. On a new `StartSession` with `opts.ResumeSessionID` set, Go passes it in the `init`
   message as `resume_session_id`.
4. Bridge passes `resume: resumeSessionId` to `query()` options.

The SDK manages the session file location internally; no filesystem path needs to be
persisted on the Go side — only the UUID.

---

## 10. Foreman Mode

- `allowedTools: ["Read", "Grep", "Glob"]` — no write tools
- `permissionMode: "dontAsk"` — no prompts since no human is present
- No `substrateMcpServer` / `ask_foreman` tool (foreman does not ask questions)
- `settingSources: ["user"]` only — no project settings to avoid polluting foreman's
  isolated read view
- Confidence signal: the system prompt instructs Claude to append `CONFIDENCE: high` or
  `CONFIDENCE: uncertain` as its last line. The bridge inspects `ResultMessage.result`
  text for this suffix, strips it, and emits `foreman_proposed { text, uncertain }`.
  This is identical to the omp-bridge foreman approach.

---

## 11. SDK Options Applied at Startup

```typescript
const options = {
  cwd: worktreePath,                         // from SUBSTRATE_WORKTREE_PATH env
  permissionMode: init.permission_mode       // from init message, or "acceptEdits"
      ?? "acceptEdits",
  model:          init.model,                // from init message, or undefined (SDK default)
  maxTurns:       init.max_turns,            // 0 / undefined → no limit
  maxBudgetUsd:   init.max_budget_usd,       // 0 / undefined → no limit
  resume:         init.resume_session_id,    // undefined for new sessions
  systemPrompt:   init.system_prompt         // substrate-supplied system prompt
      ? { type: "preset", preset: "claude_code", append: init.system_prompt }
      : { type: "preset", preset: "claude_code" },
  settingSources: mode === "foreman"
      ? ["user"]
      : ["user", "project"],                 // picks up user's claude config + project CLAUDE.md
  allowedTools: mode === "foreman"
      ? ["Read", "Grep", "Glob"]
      : ["Read", "Write", "Edit", "Bash", "Glob", "Grep",
         "WebSearch", "WebFetch", "mcp__substrate__ask_foreman"],
  mcpServers: mode === "agent"
      ? { substrate: substrateMcpServer }
      : undefined,
};
```

**`systemPrompt` note:** Using the `preset: "claude_code"` form with `append` preserves
Claude Code's full system prompt and appends the Substrate system prompt after it. This
is strictly better than the current approach where the system prompt is string-prepended
to the user prompt, leaking it into the conversation history.

---

## 12. Sandboxing

Copy sandbox-exec (macOS) and bwrap (Linux) from the omp harness. Writable paths:

- `workDir` — the git worktree
- `sessionTmpDir` — process-scoped OS temp dir
- `~/.claude` — Claude Code auth store and config (required for the SDK to authenticate)
- bun cache dir (when running as a `.ts` script)

The `~/.omp` path from the omp harness is replaced with `~/.claude`. Everything else
is identical.

---

## 13. Config Changes

`ClaudeCodeConfig` is modified in-place (same YAML key `claude_code`, same struct name):

```go
type ClaudeCodeConfig struct {
    // BunPath is the path to the bun executable. Defaults to "bun" in PATH.
    BunPath string `yaml:"bun_path"`

    // BridgePath overrides the default bridge script location.
    // Defaults to bridge/claude-agent-bridge.ts next to the substrate binary.
    BridgePath string `yaml:"bridge_path"`

    // Model is the Claude model to use. Empty = use user's Claude default.
    Model string `yaml:"model"`

    // PermissionMode controls tool permissions. Defaults to "acceptEdits".
    // Valid: "default", "acceptEdits", "bypassPermissions", "dontAsk".
    PermissionMode string `yaml:"permission_mode"`

    // MaxTurns caps agentic turn count. 0 = SDK default (no cap).
    MaxTurns int `yaml:"max_turns"`

    // MaxBudgetUSD caps spending per session. 0 = no cap.
    MaxBudgetUSD float64 `yaml:"max_budget_usd"`
}
```

Removed: `BinaryPath`. The SDK locates `claude` itself.

No changes to `HarnessName` constants, `AdaptersConfig` field name, or YAML keys.

---

## 14. File Layout

### New files

```
bridge/
  claude-agent-bridge.ts       # new bridge script

internal/adapter/claudeagent/
  harness.go                   # Harness, StartSession, RunAction, bridge resolution
  session.go                   # claudeAgentSession, readEvents, mapBridgeEvent
  harness_test.go
  session_test.go
  integration_test.go
```

### Modified files

```
bridge/package.json
  + @anthropic-ai/claude-agent-sdk dependency
  + zod dependency (required by tool() for schema definition)

internal/config/config.go
  ClaudeCodeConfig: remove BinaryPath, add BunPath + BridgePath

internal/app/harness.go
  HarnessClaudeCode case → instantiate claudeagent.Harness instead of claudecode.Harness

internal/tui/views/settings_service.go
  Update claude-code section: remove binary_path field, add bun_path + bridge_path

internal/tui/views/settings_page.go
  Update auth hint (no login needed; auth inherits from Claude Code)

cmd/substrate/main.go
  Update sample config: remove binary_path, add bun_path comment
```

### Deleted files

```
internal/adapter/claudecode/   # entire package
```

---

## 15. Go Session Type

```go
// internal/adapter/claudeagent/session.go
type claudeAgentSession struct {
    id             string
    mode           adapter.SessionMode
    cmd            *exec.Cmd
    stdin          io.WriteCloser
    stdout         io.Reader
    stderr         io.Reader
    events         chan adapter.AgentEvent
    logFile        *os.File
    logPath        string
    logDir         string
    workDir        string
    claudeSessionID string  // Claude's UUID; captured from session_meta for resume

    mu        sync.Mutex
    aborted   bool
    closeOnce sync.Once
}
```

Methods: `ID()`, `Wait()`, `Events()`, `SendMessage()`, `Steer()`, `Abort()` — all
implemented (no stubs). `SendMessage` writes `{"type":"message","text":"..."}`. `Steer`
writes `{"type":"steer","text":"..."}`.

---

## 16. Testing

### Unit tests

**`harness_test.go`**
- Capabilities: `SupportsStreaming`, `SupportsMessaging`, `SupportsNativeResume` all true
- `SupportedTools` includes `ask_foreman` MCP tool name
- Init message serialisation: all fields round-trip correctly
- Bridge resolution: same candidate-path logic as omp harness

**`session_test.go`** — `mapBridgeEvent` table tests:
- `assistant_output` → `text_delta`
- `thinking_output` → `text_delta` with `metadata.thinking: true`
- `tool_start` → `tool_start` with tool name and serialised input
- `tool_result` → `tool_result` with `is_error`
- `lifecycle/started` → `started`
- `lifecycle/completed` → `done`
- `lifecycle/failed` → `error`
- `lifecycle/retry_wait` → `retry_wait`
- `question` → `question` with context
- `foreman_proposed` → `foreman_proposed` with `uncertain`
- `session_meta` → captured on session struct (not emitted as AgentEvent)

### Integration test

```go
// internal/adapter/claudeagent/integration_test.go
// Skip if claude binary or bun not in PATH, or bridge deps not installed.
// Sends: {"type":"prompt","text":"What is 1+1? Reply with only the number."}
// Asserts: at least one text_delta event with content, one done event.
```

---

## 17. Implementation Phases

```
Phase 1 — Bridge script
  bridge/claude-agent-bridge.ts
  bridge/package.json (add SDK + zod deps)

Phase 2 — Go harness
  internal/adapter/claudeagent/harness.go
  internal/adapter/claudeagent/session.go
  internal/adapter/claudeagent/harness_test.go
  internal/adapter/claudeagent/session_test.go

Phase 3 — Config + wiring
  internal/config/config.go  (modify ClaudeCodeConfig)
  internal/app/harness.go    (swap instantiation)

Phase 4 — Settings TUI
  internal/tui/views/settings_service.go
  internal/tui/views/settings_page.go

Phase 5 — Cutover (single atomic commit)
  Delete internal/adapter/claudecode/
  Update cmd/substrate/main.go sample config
  Update any test fixtures referencing the old harness

Phase 6 — Integration test
  internal/adapter/claudeagent/integration_test.go
```

Each phase is one commit. Phase 5 is atomic: old code deleted, new code wired, all tests
pass in the same commit.
