# ACP Harness Integration Plan

## Goal

Add a first-class `acp` agent harness that lets Substrate run arbitrary local agents implementing Agent Client Protocol v1 while preserving the verified Oh My Pi workflow contract: planning, implementation, review, Foreman-backed questions, live transcripts, steering/follow-up where the protocol allows it, native resume, cancellation, logs, settings diagnostics, and honest capability degradation.

Substrate is the **ACP client**. ACP agents are subprocesses. The stable transport to implement first is newline-delimited JSON-RPC over stdio; Streamable HTTP/WebSocket is still an RFD and should be a later transport plug-in, not the initial integration baseline.

## Grounding

Current Substrate harness contract:

- `adapter.AgentHarness` and `adapter.AgentSession` are defined in `internal/adapter/interfaces.go`.
- OMP implements parity baseline in `internal/adapter/ohmypi/harness.go`, `internal/adapter/ohmypi/session.go`, and shared JSON-line process/session machinery in `internal/adapter/bridge/session.go`.
- OMP capabilities are streaming, messaging, native resume, compact, and tools `read`, `grep`, `find`, `edit`, `write`, `bash`, `ask`, `ask_foreman`.
- `BridgeSession` already owns subprocess lifecycle, stdout/stderr readers, session log writes, 10MB rotation, gzip compression, temp dir cleanup, event channel backpressure, and graceful abort.
- Question routing is already normalized through `adapter.AgentEvent{Type: "question"}` and `orchestrator.QuestionRouter`.
- Runtime routing currently chooses one harness from `harness.default` in `internal/app/harness.go` and wires it to planning, implementation, review, foreman, and resume.

ACP facts from current docs:

- Stable ACP uses JSON-RPC 2.0 over stdio; messages are newline-delimited UTF-8 JSON-RPC envelopes.
- Required agent methods: `initialize`, `session/new`, `session/prompt`; baseline notifications/methods include `session/update` and `session/cancel`.
- Optional agent methods/capabilities: `authenticate`, `session/load`, `session/resume`, `session/close`, `session/list`, `session/set_config_option`, `session/set_mode`.
- Client-side methods Substrate may need to serve: `session/request_permission`, `fs/read_text_file`, `fs/write_text_file`, `terminal/create`, `terminal/output`, `terminal/wait_for_exit`, `terminal/kill`, `terminal/release`.
- ACP session setup passes `cwd` and `mcpServers`; this is the correct path for exposing Substrate's Foreman `ask_foreman` MCP tool to arbitrary ACP agents.
- ACP Registry publishes install metadata at `https://cdn.agentclientprotocol.com/registry/v1/latest/registry.json`, with `binary`, `npx`, and `uvx` distribution types.

## Non-goals for the first cut

- Do not implement ACP remote HTTP/WebSocket transport until the RFD stabilizes.
- Do not turn Substrate itself into an ACP agent/server.
- Do not require arbitrary ACP agents to understand Substrate work items, plans, or review cycles.
- Do not invent an ACP extension for planning/review phases unless a real agent requires it; Substrate can encode phase-specific behavior in the prompt, cwd, MCP tools, and session config.
- Do not pretend unsupported features are parity-complete. Capability degradation must be surfaced in settings diagnostics and harness capabilities.

## Design decision

Implement ACP directly in Go under `internal/adapter/acp/`, not through another TypeScript bridge.

Reasons:

1. ACP already is the transport/protocol boundary. Adding a TS bridge would only translate JSON-RPC to another private JSON-line protocol and duplicate lifecycle complexity.
2. Go can reuse the existing `BridgeSession` ideas without inheriting its private `{type:"event"}` envelope.
3. ACP requires bidirectional JSON-RPC request handling: the agent can call client methods while a prompt is active. This is cleaner as one Go JSON-RPC connection with request correlation, pending calls, and client-method dispatch.
4. Security-critical client methods (`fs/*`, `terminal/*`) need Go-side path and process boundaries tied to Substrate worktree state.

## Proposed package layout

```text
internal/adapter/acp/
  harness.go          # adapter.AgentHarness + HarnessActionRunner
  session.go          # adapter.AgentSession implementation
  client.go           # JSON-RPC connection, request IDs, pending calls
  protocol.go         # ACP wire structs; no domain imports
  events.go           # ACP session/update -> adapter.AgentEvent mapping
  client_methods.go   # session/request_permission, fs/*, terminal/* handlers
  terminal.go         # terminal process table and output ring buffers
  registry.go         # optional registry lookup/install metadata parser
  readiness.go        # binary/npx/uvx/command validation
  harness_test.go
  client_test.go
  events_test.go
  client_methods_test.go
```

Keep `internal/adapter/bridge/` for the existing private bridges. Only lift logic from it when it is genuinely protocol-agnostic, e.g. log rotation/compression helpers. Do not force ACP into `BridgeSession` if that makes JSON-RPC request handling awkward.

## Config model

Add a new harness name:

```go
const HarnessACP HarnessName = "acp"
```

Add config:

```yaml
harness:
  default: acp

adapters:
  acp:
    agent: claude-acp          # optional registry id or display label
    command: npx               # executable to run; absolute path or PATH lookup
    args: ["@agentclientprotocol/claude-agent-acp@0.36.1"]
    env: {}
    registry_id: claude-acp    # optional; used for install hints/metadata only
    model: ""                 # optional desired model config option value
    mode: ""                  # optional desired mode/config option value
    thought_level: ""         # optional desired thought_level config option value
    client_fs: true            # advertise fs/read_text_file + fs/write_text_file
    client_terminal: true      # advertise terminal/*
    auth_terminal: true        # advertise auth.terminal
```

Rules:

- `command` is required for arbitrary local ACP agents unless `registry_id` plus installed distribution metadata can resolve it.
- `args` are exact argv entries; no shell interpolation.
- `env` values are literal non-secret values. Secrets should be passed through existing keychain mechanisms only after a dedicated secret-ref design exists.
- `client_fs` and `client_terminal` default to `true` for OMP parity, but their implementation must enforce worktree boundaries.
- Keep registry support optional. A configured command must be enough for power users and CI.

Update:

- `internal/config/config.go`: harness enum, `AdaptersConfig.ACP`, default/validation, allowed harness fields.
- `internal/app/harness.go`: instantiate `acp.NewHarness`, readiness diagnostics.
- `README.md` and `docs/04-adapters.md`: add ACP harness behavior and limitations.
- Settings UI: add ACP fields and status check in the same harness section.

## Runtime architecture

### 1. ACP process connection

`AcpHarness.StartSession(ctx, opts)` should:

1. Resolve working directory:
   - agent/planning/review/implementation: `opts.WorktreePath` when non-empty;
   - foreman: `workspaceRoot`.
2. Create `sessionLogDir` and log path exactly like OMP.
3. Build `exec.Cmd` from configured command/args without a shell.
4. Apply sandboxing where possible:
   - reuse `bridge.BuildSandboxCmd` only if it can accept arbitrary argv without bridge-specific assumptions;
   - otherwise introduce `internal/adapter/process` helpers for sandboxed subprocess construction.
5. Start stdin/stdout/stderr pipes.
6. Create an ACP JSON-RPC client around the pipes.
7. Start stdout reader and stderr logger before sending `initialize`.
8. Send `initialize` with protocolVersion `1`, clientInfo `{name:"substrate", title:"Substrate"}`, and negotiated client capabilities.
9. If `authMethods` are returned and the agent requires authentication before sessions, run `authenticate` only for supported methods; otherwise return an explicit readiness/action error.
10. Create or resume the ACP session.
11. Configure mode/model/thought-level if supported through `configOptions`; otherwise use `session/set_mode` only when the legacy `modes` API is present.
12. Send the initial prompt for agent-mode sessions.

### 2. JSON-RPC client

`client.go` needs four core loops/structures:

- `writeMu`: serializes writes to stdin.
- `pending map[id]chan response`: correlates responses to client-initiated requests.
- `serverRequestHandler`: handles agent-initiated requests (`session/request_permission`, `fs/*`, `terminal/*`) and writes JSON-RPC responses.
- `notificationHandler`: handles `session/update` and other notifications.

Correctness requirements:

- stdout scanner buffer at least matches current 10MB bridge buffer.
- write every raw JSON-RPC line to the session log with RFC3339Nano prefix, preserving OMP transcript/debug behavior.
- never drop terminal events: `done`, `error`, and `question` must use blocking sends like `BridgeSession.emitEvent` already does.
- close event channel only after stdout is drained and all pending terminal events are emitted.
- preserve error chains on JSON-RPC, process, scanner, and method-handler failures.

### 3. ACP session identity and ResumeInfo

Persist ACP session identity in existing `domain.AgentSession.ResumeInfo`:

```go
map[string]string{
  "acp_agent_session_id": agentSessionID,
  "acp_agent_name": initialize.agentInfo.name,
  "acp_agent_version": initialize.agentInfo.version,
  "acp_protocol_version": "1",
  "acp_resume_method": "resume" | "load" | "new",
}
```

Resume logic:

- If `opts.ResumeInfo["acp_agent_session_id"]` exists and initialize advertises `sessionCapabilities.resume`, call `session/resume`.
- Else if top-level `loadSession` is true, call `session/load` and allow replay updates into the log/transcript.
- Else start `session/new` and mark `SupportsNativeResume=false` for this agent instance.

`HarnessCapabilities.SupportsNativeResume` cannot be a static ACP-wide true. It depends on the selected agent's initialize capabilities. Because `AgentHarness.Capabilities()` is not currently in the interface even though concrete harnesses expose it, add one of these before relying on dynamic routing:

- Preferred: extend `AgentHarness` with `Capabilities() adapter.HarnessCapabilities` and update all harnesses.
- Conservative: keep ACP harness `SupportsCompact()`/resume behavior instance-local and expose accurate docs/settings diagnostics after readiness check.

### 4. Prompt construction

Map Substrate prompt fields to one text ContentBlock:

- `opts.SystemPrompt` stays outside ACP if the agent has no system-prompt method. Since ACP v1 `session/prompt` only takes user content, prepend Substrate's system prompt to the first prompt as a clearly delimited instruction block.
- `opts.DocumentationContext`, `opts.CrossRepoPlan`, commit config, `AllowPush`, and phase instructions continue to be authored by existing orchestrator prompt builders.
- `opts.UserPrompt` becomes the final task section.

Do not depend on ACP `resource` embedded context until the selected agent advertises `promptCapabilities.embeddedContext`.

### 5. Event mapping

Map `session/update` to normalized `adapter.AgentEvent`:

| ACP update | Substrate event |
|---|---|
| `agent_message_chunk` text | `text_delta` |
| `user_message_chunk` | `input` with `input_kind=history` during load replay |
| `plan` | `tool_output` or new metadata-rich `plan_update`; transcript renderer can later become plan-aware |
| `tool_call` | `tool_start` with `tool`, `kind`, `rawInput`, `toolCallId` metadata |
| `tool_call_update` status/content | `tool_output` for incremental content, `tool_result` for completed/failed |
| `available_commands_update` | metadata event, not user-visible by default |
| `current_mode_update` | metadata event |
| `config_option_update` | metadata event |
| `session_info_update` | metadata event |
| prompt response `stopReason=end_turn` | `done` |
| prompt response `stopReason=cancelled` | `error` or interrupted path depending caller context |
| JSON-RPC/process failure | `error` |

Tool content mapping:

- text content: stringify text only, not full JSON when a user-facing text exists.
- diff content: emit `tool_output` with diff metadata (`path`, old/new text); do not apply it again.
- terminal content: attach `terminalId`; transcript can render terminal output fetched through terminal table if present.
- unknown content: bounded JSON serialization with truncation.

### 6. Questions and Foreman parity

ACP does not have a generic clarifying-question primitive. Substrate should preserve its existing question router by providing the same `ask_foreman` tool to ACP agents through MCP.

Implementation path:

1. Reuse or generalize `bridge/opencode-foreman-mcp` as a packaged `substrate-foreman-mcp` stdio MCP server.
2. On `session/new`, include an MCP server entry:
   - `name: "substrate-foreman"`
   - `command: <packaged mcp bridge>`
   - env includes a per-session socket/path/token needed to route back to the live ACP session.
3. The MCP server emits a normalized question into the ACP harness/session (`adapter.AgentEvent{Type:"question", Question: ...}`) and blocks until `SendAnswer` resolves it.
4. `AcpSession.SendAnswer` resolves the pending MCP question handle, not an ACP protocol method.
5. Use existing `QuestionRouter` for stage-aware routing.

This avoids misusing `session/request_permission` for non-permission questions. `session/request_permission` should remain an authorization mechanism for tool execution and should map to Substrate's human question UI only if the agent is genuinely asking the operator to approve/reject an action.

Add a new source constant only if needed:

```go
AgentQuestionSourceACPAsk AgentQuestionSource = "acp_ask_foreman"
```

Do not rename product-level Tasks symbols per `AGENTS.md`.

### 7. Client filesystem methods

Advertise `fs.readTextFile` and `fs.writeTextFile` for parity, but enforce boundaries:

- Paths must be absolute, per ACP.
- Clean/evaluate symlinks where possible.
- Allow reads/writes only under the session worktree root.
- For foreman sessions, default to read-only unless the current OMP foreman behavior truly allows writes; parity should be with product intent, not accidental capability.
- Enforce line/limit semantics for read.
- Create parent directories on write only inside root.
- Log all handler errors with `slog` and return JSON-RPC errors preserving the underlying error in logs.

### 8. Client terminal methods

Implement terminal support because many ACP agents rely on client-side command execution:

- `terminal/create`: start process with command/args exactly as provided, no shell; cwd must be absolute and within worktree; env is additive allowlist/explicit values.
- Store terminal state in a per-session table keyed by ACP terminalId.
- Capture stdout/stderr into a byte-limited ring buffer; truncate from the beginning at UTF-8 boundaries as ACP requires.
- `terminal/output`: return current output, truncation flag, and exitStatus when available.
- `terminal/wait_for_exit`: block until process exit or context cancellation.
- `terminal/kill`: terminate process but keep terminal record.
- `terminal/release`: kill if still running and delete terminal record.
- `Abort` and `Wait` cleanup must release/kill every still-owned terminal.

### 9. Permission requests

`session/request_permission` must create a normalized operator question only when policy requires human approval:

- Map ACP options to `adapter.StructuredQuestionSet` with option IDs preserved in metadata.
- If the current prompt/session is cancelled, respond with ACP outcome `cancelled` as the spec requires.
- Add policy hooks later for auto-allow safe operations, but initial implementation should route permission requests to the human unless existing config already has a clear policy.
- Answer delivery should send the selected ACP `optionId`, not a label string.

### 10. Messaging, steering, compaction

ACP v1 supports `session/prompt`, not a distinct steer/control message.

Product decisions:

- ACP steering is defined as **interrupt + prompt**. If a prompt is active, Substrate cancels it and then sends the operator's steering message as the next prompt. This is acceptable product behavior and should be treated as ACP `Steer`, not hidden as unsupported.
- Generic ACP compaction is unsupported unless Substrate detects a known agent or advertised command with a verified compaction path.

Implement:

- `SendMessage(ctx, msg)`: call `session/prompt` with a text ContentBlock.
- `Steer(ctx, msg)`: if a prompt is active, first send `session/cancel`, wait for the current prompt response, then send `session/prompt` with a steering-prefixed message. If the agent cannot cancel promptly, return a real error. If no prompt is active, send the steering-prefixed prompt directly.
- `Compact(ctx)`: route through `detectCompactStrategy`:
  - `compact` slash command: send `session/prompt` with text `/compact`.
  - `compress` slash command: send `session/prompt` with text `/compress`.
  - extension method/custom capability: use only after a concrete agent profile proves it.
  - no strategy: return `adapter.ErrCompactNotSupported`.

Known compaction profiles from current research:

| Agent | Detection | Compaction command | Evidence |
|---|---|---|---|
| Kilo Code CLI ACP | `agentInfo.name == "Kilo"`, auth method `kilo-login`, or configured command `kilo acp` | `/compact` | Kilo CLI docs list `/compact` (`/summarize`) as “Compact/summarize session”; Kilo ACP source injects available command `compact` and handles prompt command `compact` by calling `sdk.session.summarize`. |
| Cursor CLI ACP | auth method `cursor_login`, configured command `agent acp`, or agentInfo identifying Cursor | `/compress` | Cursor CLI slash-command docs list `/compress` as “Summarize conversation to free context space”; Cursor ACP docs identify ACP server as `agent acp` with auth method `cursor_login`. |

Detection order:

1. Prefer runtime `available_commands_update` from ACP. If it advertises `compact` or `compress`, use that exact command.
2. Fall back to known profiles above from `initialize.agentInfo`, `authMethods`, and configured command/args.
3. Otherwise report `ErrCompactNotSupported`.

`SupportsCompact()` for the configured ACP harness should return true only after readiness/profile detection finds a strategy. If detection has not run yet, expose this in settings as “unknown until initialize” rather than claiming parity.

### 11. Auth and harness actions

`RunAction` behavior:

- `check_auth`: start the agent, run `initialize`, inspect `authMethods`, and return success if no auth is required or a supported method is available.
- `login_provider`: keep existing GitHub/Sentry behavior in provider-specific harnesses for now; ACP agents are not responsible for Substrate provider credentials.
- `authenticate`: implement as an ACP action if settings needs it, with support for:
  - `agent`: call `authenticate(methodId)` and let the agent handle it;
  - `env_var`: report needed variables; do not store entered secrets in YAML;
  - `terminal`: run the configured agent command with advertised args/env in an interactive terminal flow only after explicit operator action.

Registry note: the ACP registry requires agents to support at least one auth method. Substrate as client should consume that metadata; Substrate itself does not need a registry entry for this feature.

## Feature parity matrix

| Capability | OMP baseline | ACP plan | Initial status |
|---|---|---|---|
| Start local harness subprocess | bundled bridge + Bun/binary | configured command/args or registry distribution | full |
| Streaming text | bridge events | `session/update agent_message_chunk` | full |
| Tool lifecycle transcript | bridge tool events | `tool_call` / `tool_call_update` | full |
| Session logs | timestamped JSON lines + rotation | raw JSON-RPC lines + same rotation/compression | full |
| Abort | bridge abort + SIGINT/SIGKILL | `session/cancel` + process termination fallback | full |
| Native resume | `omp_session_file` | `session/resume` or `session/load` when advertised | conditional |
| Follow-up messages | bridge `message` | `session/prompt` | full |
| Steering | bridge `steer` interrupt | ACP-standard interrupt + prompt | full by product decision |
| Compact | bridge `compact` | `/compact` or `/compress` for detected Kilo/Cursor-style agents; otherwise unsupported | conditional |
| Foreman questions | `ask_foreman` custom/MCP tool | MCP `ask_foreman` server passed via `mcpServers` | full |
| Structured questions | OMP ask payload | MCP question payload + permission options | full |
| Provider login actions | `RunAction` custom | check ACP auth; keep provider auth separate | partial by design |
| Client file tools | agent internal tools | ACP `fs/*` with worktree boundary | full |
| Client terminal tools | agent internal bash | ACP `terminal/*` with worktree boundary | full |
| Model/mode selection | harness config/env | ACP configOptions/session modes | full when advertised |
| Slash commands | backend-specific | `available_commands_update`; prompt text execution | display later |

## Implementation phases

### Phase 1 — Protocol core and static config

Files:

- `internal/config/config.go`
- `internal/config/config_test.go`
- `internal/config/harness_auth.go`
- `internal/app/harness.go`
- new `internal/adapter/acp/protocol.go`
- new `internal/adapter/acp/client.go`

Tasks:

1. Add `HarnessACP` and `ACPConfig`.
2. Validate command/args/env and harness.default.
3. Wire `acp.NewHarness` into `instantiateHarness` with readiness diagnostics.
4. Implement JSON-RPC envelope structs and stdio connection.
5. Implement initialize request/response parsing and version/capability negotiation.

Acceptance tests:

- config loads `harness.default: acp`.
- invalid empty ACP command fails validation/readiness.
- fake ACP subprocess receives initialize and returns capabilities.
- JSON-RPC pending request correlation handles out-of-order responses.
- malformed stdout line logs warning and does not panic.

### Phase 2 — Session lifecycle and event streaming

Files:

- `internal/adapter/acp/harness.go`
- `internal/adapter/acp/session.go`
- `internal/adapter/acp/events.go`
- shared log helper if extracted from `internal/adapter/bridge/session.go`

Tasks:

1. Implement `adapter.AgentHarness` and `adapter.AgentSession` compile-time checks.
2. Implement `StartSession`, `Wait`, `Events`, `Abort`, `ResumeInfo`.
3. Implement `session/new`, `session/prompt`, `session/cancel`, `session/resume`, `session/load`, `session/close` based on capabilities.
4. Map session updates and prompt responses into `adapter.AgentEvent`.
5. Write raw JSON-RPC transcript logs and reuse rotation/compression semantics.

Acceptance tests:

- fake ACP agent session lifecycle emits `started`, `text_delta`, `tool_start`, `tool_result`, `done`.
- event channel closes after process exit.
- `Abort` sends `session/cancel` for active prompt and kills process if it does not exit.
- `ResumeInfo` contains ACP session ID after `session/new`.
- `session/load` replay is logged and mapped without marking the new Substrate session completed early.

### Phase 3 — Client methods: permission, fs, terminal

Files:

- `internal/adapter/acp/client_methods.go`
- `internal/adapter/acp/terminal.go`
- tests for path/process boundaries

Tasks:

1. Serve `session/request_permission` as structured question/answer flow.
2. Serve `fs/read_text_file` and `fs/write_text_file` with absolute-path and worktree-boundary checks.
3. Serve `terminal/create/output/wait_for_exit/kill/release` with process table cleanup.
4. Ensure pending permission requests receive `cancelled` when prompt/session aborts.

Acceptance tests:

- fs read honors line and limit.
- fs write creates files inside worktree and rejects paths outside via `..` or symlink escape.
- terminal output truncates from beginning at UTF-8 boundary.
- terminal release kills a running command and invalidates terminal ID.
- cancelled prompt responds to pending permission with `cancelled`.

### Phase 4 — Foreman question parity through MCP

Files:

- generalize `bridge/opencode-foreman-mcp` or create `bridge/substrate-foreman-mcp`
- `internal/adapter/acp/harness.go`
- `internal/adapter/acp/session.go`
- `internal/orchestrator/question_router_test.go` only if adapter exposes a new source constant

Tasks:

1. Package a reusable stdio MCP server exposing `ask_foreman`.
2. Allocate per-session answer handles so `AcpSession.SendAnswer` unblocks the MCP call.
3. Pass MCP server config in ACP `session/new` for agent and implementation/review phases.
4. Emit normalized `question` events with structured metadata.

Acceptance tests:

- fake MCP question produces `adapter.AgentEvent{Type:"question"}`.
- `SendAnswer` resolves the pending question exactly once.
- planning-stage question routes directly to human; implementation-stage question routes through Foreman using existing router tests.
- abort cleans pending question handles without deadlock.

### Phase 5 — Config options, modes, registry metadata, settings

Files:

- `internal/adapter/acp/registry.go`
- settings-related TUI/service files
- `README.md`
- `docs/04-adapters.md`

Tasks:

1. Parse `configOptions` from session setup/resume and select requested model/mode/thought_level when possible.
2. Fall back to legacy `modes` only when configOptions are absent.
3. Implement `detectCompactStrategy` from runtime `available_commands_update`, Kilo profile, Cursor profile, or unsupported fallback.
4. Parse registry JSON for install hints and display metadata; do not auto-download binaries in the first cut.
5. Add Settings status: command found, initialize works, protocol version, agentInfo, authMethods, capability badges, resume/close/list/config/compact support.
6. Document config examples for direct command and registry-backed command.

Acceptance tests:

- model/mode config option calls `session/set_config_option` and handles complete returned state.
- legacy mode calls `session/set_mode` only when advertised.
- compact strategy detection chooses advertised `compact`, advertised `compress`, Kilo `/compact`, Cursor `/compress`, or unsupported in that order.
- registry parser handles `binary`, `npx`, `uvx` entries and platform selection.
- settings diagnostics show unavailable ACP harness without blocking app startup.

### Phase 6 — End-to-end orchestration parity gate

Tasks:

1. Add fake ACP harness integration tests that run through planning, implementation, review, follow-up, and resume paths without a real LLM.
2. Add one optional integration test against a real registry agent when installed, behind build tag/environment gate.
3. Verify transcript rendering for ACP tool calls and question events.
4. Verify session deletion/abort tears down ACP subprocesses and terminals.

Acceptance tests:

- planning session writes a draft plan and completes.
- implementation session emits tool events, asks Foreman, receives answer, and completes.
- review critique follow-up uses ACP resume when available.
- failed/interrupted ACP session can be resumed or abandoned through existing resumption flow.
- TUI transcript width/height tests updated only if ACP rendering changes layout.

## Security and reliability requirements

- No shell execution for configured ACP command or terminal methods unless an explicit shell is the configured command.
- Every filesystem and terminal request is bounded to the worktree/session root.
- Agent stdout must be valid ACP JSON-RPC; invalid lines are logged and ignored, not interpreted as text.
- Stderr is logged at debug/warn level without polluting protocol parsing.
- All process cleanup paths must be idempotent.
- All errors are logged with `slog` and original error values.
- Do not silently auto-approve ACP permission requests.
- Do not store ACP auth secrets in YAML.

## Main risks

1. **Generic compact parity is not real.** ACP v1 has no compact method. Treat compact as unsupported unless runtime command detection or a known Kilo/Cursor-style profile proves a slash-command strategy.
2. **Steering semantics differ from OMP.** ACP steering is intentionally implemented as interrupt + prompt. Tests must verify cancellation completes before the redirect prompt is sent.
3. **Client terminal/fs methods increase blast radius.** Boundary enforcement and tests are mandatory before enabling by default.
4. **Question parity depends on MCP support.** ACP says all agents must support stdio MCP servers passed during session setup; if a specific agent ignores MCP servers, Foreman parity is unavailable for that agent.
5. **Dynamic capabilities are not represented in current `AgentHarness` interface.** If settings/orchestration need exact per-agent capabilities before StartSession, extend the interface cleanly rather than adding type assertions everywhere.
6. **Registry metadata is install metadata, not trust.** A registry entry does not prove Substrate-specific parity. Keep verification local.

## Recommended first implementation target

Use a fake ACP agent binary checked into tests as the primary contract target. It should support:

- initialize with configurable capabilities;
- session/new, session/prompt, session/cancel;
- optional resume/load/close/config;
- emitting every session/update variant Substrate maps;
- issuing `session/request_permission`, `fs/read_text_file`, and terminal calls;
- deterministic stop reasons and errors.

This gives a stable parity gate without depending on remote model behavior or third-party CLI auth.
