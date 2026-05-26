# Plan: Kiro CLI ACP Integration for Substrate

## What This Is

Add first-class support for **Kiro CLI** as an ACP harness harness in Substrate.
The generic ACP harness in `internal/adapter/acp/` already speaks JSON-RPC 2.0 over stdio,
which is exactly what `kiro-cli acp` implements. This plan closes the gaps that prevent
Kiro from working out of the box.

---

## What We Learned (Ground Truth)

### Kiro CLI ACP Surface

- **Command**: `kiro-cli acp`
- **Protocol**: JSON-RPC 2.0 over stdio — identical to what the existing `acp` harness already speaks.
- **ACP version**: 1
- **Session management**: `initialize`, `session/new`, `session/load`, `session/resume`, `session/prompt`, `session/cancel`, `session/close`, `session/set_config_option`, `session/set_mode`.
- **Streaming**: `session/notification` with types: `agent_message_chunk`, `tool_call`, `tool_call_update`, `turn_end`, `available_commands_update`, `_kiro.dev/*` Kiro extensions.
- **Client-to-agent requests** (server → agent → client): `fs/read_text_file`, `fs/write_text_file`, `terminal/create`, `session/request_permission`.
- **Auth**: `kiro-cli login` (device flow / browser). After login, `kiro-cli whoami` confirms auth. ACP `initialize` returns `authMethods` if not logged in. Known bug (JetBrains #6603, fixed April 2026): older JetBrains versions misinterpreted `authMethods` presence as "not authenticated". Latest JetBrains (261.22158.366+) fixed; Substrate is unaffected because it does not require `authMethods` to be absent.
- **Kiro-specific extensions**: `_kiro.dev/commands/execute`, `_kiro.dev/commands/options`, `_kiro.dev/mcp/*`, `_kiro.dev/compaction/status`, `_kiro.dev/clear/status`. These are optional and safe to ignore if not supported.
- **Compaction**: advertised via `available_commands_update` after `session/new`. Known strategies: `compact`, `compress`.
- **Native resume**: supported (`session/resume` if advertised, else `session/load`).
- **Auth methods returned**: `kiro-login` method ID when not authenticated via `kiro-cli login`.

### Current ACP Harness State

- **Already speaks the right protocol** — JSON-RPC 2.0 over stdio, same as Kiro CLI.
- **`ACPConfig`** already has `Agent`, `Command`, `Args`, `Env`, `RegistryID`, `Model`, `Mode`, `ThoughtLevel`, `ClientFS`, `ClientTerminal`, `AuthTerminal`.
- **`Agent` and `RegistryID` are declared in config but never wired through** — no `session/new` parameters, no env vars, no args construction.
- **`AuthTerminal`** is declared but never used — no TTY/terminal auth flow.
- **Foreman MCP bridge** is now generic (`foreman-mcp`) and can be registered by both OpenCode and Kiro ACP sessions.
- **`Capabilities()` returns hardcoded tool names** — Kiro's actual tools come from MCP servers, not a fixed list. The hardcoded list (`read`, `write`, `bash`, `ask_foreman`, `acp_fs`, `acp_terminal`) is incorrect for Kiro.
- **Test helper** simulates a generic ACP agent, not Kiro-specific behavior.
- **`validateReadiness`** only checks command existence, not authentication.

### Gap Analysis

| Gap | Severity | Notes |
|-----|----------|-------|
| `Agent` and `RegistryID` not wired to `session/new` | High | Kiro uses registry to locate agent configs |
| Foreman MCP bridge not available for Kiro | High | `ask_foreman` tool unavailable |
| `AuthTerminal` not wired | Medium | Device flow auth requires TTY |
| Hardcoded `SupportedTools` list incorrect | Medium | Kiro tools come from MCP; list is misleading |
| `validateReadiness` doesn't check auth | Low | `check_auth` action handles it at runtime |
| No Kiro-specific test helper | Low | Generic ACP helper is sufficient |

---

## Implementation Plan

### Phase 1: Wire Agent + RegistryID Through Session Creation

**File**: `internal/adapter/acp/protocol.go`

Add `agent` and `registry_id` fields to `sessionCreateParams`:

```go
type sessionCreateParams struct {
    SessionID  string     `json:"sessionId,omitempty"`
    CWD        string     `json:"cwd"`
    MCPServers []mcpServer `json:"mcpServers,omitempty"`
    Agent      string     `json:"agent,omitempty"`
    RegistryID string     `json:"registryId,omitempty"`
}
```

**File**: `internal/adapter/acp/session.go`

In `setupACPSession`, pass `Agent` and `RegistryID` from `ACPConfig` into `sessionCreateParams`:

```go
params := sessionCreateParams{
    CWD:        s.root,
    MCPServers: mcpServers,
    Agent:      s.acpCfg.Agent,
    RegistryID: s.acpCfg.RegistryID,
}
```

**Verification**: Add a test that verifies `session/new` is sent with the configured `Agent` and `RegistryID`.

---

### Phase 2: Foreman MCP Bridge for Kiro CLI

Use one generic bridge at `bridge/foreman-mcp/`. The bridge is agent-agnostic: it only exposes `ask_foreman` over a Unix socket via the `SUBSTRATE_FOREMAN_SOCKET` environment variable. Both Kiro ACP and OpenCode should resolve this bridge name directly; no legacy agent-specific bridge name is needed.

**File**: `internal/adapter/acp/harness.go`

Update `resolveForemanMCPBridge()` to search for `foreman-mcp` only:

```go
for _, c := range bridge.BridgeCandidates("", execPath, "foreman-mcp") {
```

**Acceptance**: `ask_foreman` tool available in Kiro CLI ACP sessions.

---

### Phase 3: Auth Terminal Wiring (Optional but Recommended)

**File**: `internal/adapter/acp/session.go`

When `acpCfg.AuthTerminal` is true and the agent returns `authMethods` in `initialize`, trigger a terminal-based auth flow instead of failing.

Kiro CLI's `kiro-cli login` uses device flow — no port forwarding needed. The process is:
1. Detect `authMethods` in `initialize` response.
2. If `AuthTerminal` is set, spawn a new TTY-attached `kiro-cli login` subprocess.
3. Wait for `kiro-cli whoami` to confirm auth success.
4. Proceed with session.

**Alternative (simpler)**: Document that users must run `kiro-cli login` before starting Substrate. Add a `check_auth` action that validates auth and surfaces clear instructions if not logged in.

---

### Phase 4: Fix SupportedTools (Cosmetic)

**File**: `internal/adapter/acp/harness.go`

Make `Capabilities()` return the actual Kiro tool list, or make it dynamic based on the agent's advertised tools. For now, update the hardcoded list to include Kiro-specific tools:

```go
SupportedTools: []string{
    "read", "write", "bash", "grep", "glob",
    "ask_foreman", "acp_fs", "acp_terminal",
    // Kiro-specific
    "@kiro/code_search", "@kiro/web_search",
},
```

**Better approach**: Make `SupportedTools` reflect what MCP servers are registered. The `Session.buildMCPServers()` already computes the list. Expose it via a channel or session metadata.

---

### Phase 5: Test Helper for Kiro CLI

**File**: `internal/adapter/acp/harness_test.go`

Add a Kiro-specific test helper variant that simulates Kiro CLI behavior:
- Returns Kiro agent info in `initialize`.
- Advertises `compact` strategy via `available_commands_update`.
- Returns Kiro-specific `_kiro.dev/*` notifications.

**Or**: Extend the existing helper with an env var to switch between generic/Kiro behavior.

---

## Configuration Example

```yaml
harness:
  default: acp

adapters:
  acp:
    command: kiro-cli           # or /full/path/to/kiro-cli
    args: ["acp"]
    agent: ""                   # optional: specific agent name from registry
    registry_id: ""             # optional: registry identifier
    model: ""                    # optional: e.g. "claude-sonnet-4"
    mode: "agent"               # optional: agent mode
    thought_level: ""           # optional: thinking level
    client_fs: true             # allow file system access
    client_terminal: true       # allow terminal access
    auth_terminal: false        # require pre-auth via kiro-cli login
    env:
      KIRO_API_KEY: ""          # optional: for headless/CI use
```

---

## Open Questions

1. **Should `AuthTerminal` trigger interactive login in-session?** Kiro's device flow requires a browser. In a Substrate TTY session this is feasible. Alternatively, require `kiro-cli login` as a prerequisite and surface clear error messages when not authenticated.

2. **Should we auto-detect `kiro-cli` vs other ACP agents?** The harness is generic by design. Auto-detection is possible by parsing the `initialize` response's `agentInfo.name` field (e.g., "kiro-cli"). This could unlock Kiro-specific behavior (tool names, compaction strategies) without config changes.

3. **Foreman MCP bridge naming** is settled: use only `bridge/foreman-mcp/`. The bridge is truly agent-agnostic because it uses only the Substrate Unix socket protocol and process environment.

---

## Files to Change

| File | Change |
|------|--------|
| `internal/adapter/acp/protocol.go` | Add `Agent`, `RegistryID` to `sessionCreateParams` |
| `internal/adapter/acp/session.go` | Wire `Agent`, `RegistryID` in `setupACPSession` |
| `internal/adapter/acp/harness.go` | Update `resolveForemanMCPBridge` to search for Kiro bridge |
| `bridge/foreman-mcp/` | Generic MCP bridge used by Kiro ACP and OpenCode |
| `internal/adapter/acp/harness_test.go` | Add Kiro test helper variant |
| `internal/adapter/acp/harness.go` | (Optional) Fix `SupportedTools` to reflect dynamic MCP tools |

---

## Verification

1. `go build ./... && go test ./internal/adapter/acp/... -count=1` passes.
2. Manual: configure `adapters.acp.command = "kiro-cli"`, start Substrate, create a work item. Kiro CLI ACP session starts and `ask_foreman` is available.
3. Auth flow: verify `check_auth` action returns "ACP agent is reachable" with Kiro version info after `kiro-cli login`.
