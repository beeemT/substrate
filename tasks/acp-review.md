# ACP Adapter Review Assignment

## Files to Review
1. `internal/adapter/acp/harness.go` - ACP harness capabilities and bridge resolution
2. `internal/adapter/acp/harness_test.go` - Tests for ACP harness
3. `internal/adapter/acp/protocol.go` - Protocol struct changes
4. `internal/adapter/acp/session.go` - Session setup changes
5. `internal/adapter/opencode/harness.go` - Simple rename constant

## Key Changes Summary
1. **Capabilities**: Replaced hardcoded tool list with `acpSupportedTools()` function that conditionally includes tools based on config (`ClientFS`, `ClientTerminal`)
2. **Bridge path**: Renamed from `opencode-foreman-mcp` to `foreman-mcp` throughout
3. **Session params**: Added `Agent` and `RegistryID` fields to `sessionCreateParams` and passed them from config
4. **Tests**: Added `validateHelperSessionParams()`, new tests for tool capabilities filtering

## Review Focus
- Is `acpSupportedTools()` logic correct? Does it match the config semantics?
- Are the new session params (`Agent`, `RegistryID`) being passed correctly?
- Do the tests adequately cover the new conditional logic?
- Any edge cases or error handling concerns?

Use the diffs provided in the task description (NEVER re-run git diff).

## Diff for internal/adapter/acp/harness.go
```diff
@@ -46,7 +46,18 @@ func (h *Harness) SupportsCompact() bool {
 }
 
 func (h *Harness) Capabilities() adapter.HarnessCapabilities {
-	return adapter.HarnessCapabilities{SupportsStreaming: true, SupportsMessaging: true, SupportsNativeResume: h.lastSupportsResume(), SupportedTools: []string{"read", "write", "bash", "ask_foreman", "acp_fs", "acp_terminal"}}
+	return adapter.HarnessCapabilities{SupportsStreaming: true, SupportsMessaging: true, SupportsNativeResume: h.lastSupportsResume(), SupportedTools: acpSupportedTools(h.cfg)}
+}
+
+func acpSupportedTools(cfg config.ACPConfig) []string {
+	tools := []string{"mcp__substrate-foreman__ask_foreman"}
+	if boolPtrValue(cfg.ClientFS, true) {
+		tools = append(tools, "acp/fs.read_text_file", "acp/fs.write_text_file")
+	}
+	if boolPtrValue(cfg.ClientTerminal, true) {
+		tools = append(tools, "acp/terminal.create", "acp/terminal.output", "acp/terminal.wait_for_exit", "acp/terminal.kill", "acp/terminal.release")
+	}
+	return tools
 }

 func (h *Harness) lastSupportsResume() bool {
@@ -306,7 +317,7 @@ func resolveForemanMCPBridge() (string, []string) {
 	if err != nil {
 		return "", nil
 	}
-	for _, c := range bridge.BridgeCandidates("", execPath, "opencode-foreman-mcp") {
+	for _, c := range bridge.BridgeCandidates("", execPath, "foreman-mcp") {
 		if c == "" {
 			continue
 		}
@@ -315,8 +326,8 @@ func resolveForemanMCPBridge() (string, []string) {
 	}
 	rootCandidates := []string{
-		filepath.Join(filepath.Dir(execPath), "bridge", "opencode-foreman-mcp", "index.ts"),
-		filepath.Join("bridge", "opencode-foreman-mcp", "index.ts"),
+		filepath.Join(filepath.Dir(execPath), "bridge", "foreman-mcp", "index.ts"),
+		filepath.Join("bridge", "foreman-mcp", "index.ts"),
 	}
 	bun, err := exec.LookPath("bun")
 	if err != nil {
```

## Diff for internal/adapter/acp/harness_test.go
```diff
@@ -32,6 +32,10 @@ func TestHelperACPProcess(t *testing.T) {
 		case "initialize":
 			writeHelper(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"protocolVersion": 1, "agentInfo": map[string]any{"name": os.Getenv("ACP_AGENT_NAME"), "version": "test"}, "agentCapabilities": map[string]any{"loadSession": true, "sessionCapabilities": map[string]any{"resume": map[string]any{}, "close": map[string]any{}, "setConfigOption": map[string]any{}}}, "authMethods": []map[string]any{{"id": os.Getenv("ACP_AUTH_ID")}}}})
 		case "session/new":
+			if err := validateHelperSessionParams(msg); err != nil {
+				writeHelper(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32602, "message": err.Error()}})
+				continue
+			}
 			writeHelper(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"sessionId": "acp-sess-1", "configOptions": []map[string]any{{"id": "model", "name": "Model", "category": "model", "type": "select", "currentValue": "old", "options": []map[string]any{{"value": "new", "name": "New"}}}}})
 		case "session/resume":
 			writeHelper(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"sessionId": "acp-resumed"}})
@@ -54,6 +58,21 @@ func TestHelperACPProcess(t *testing.T) {
 	os.Exit(0)
 }

+func validateHelperSessionParams(msg map[string]any) error {
+	params, _ := msg["params"].(map[string]any)
+	for _, field := range []struct{ env, key string }{{"ACP_EXPECT_AGENT", "agent"}, {"ACP_EXPECT_REGISTRY_ID", "registryId"}} {
+		want := os.Getenv(field.env)
+		if want == "" {
+			continue
+		}
+		got, _ := params[field.key].(string)
+		if got != want {
+			return fmt.Errorf("%s = %q, want %q", field.key, got, want)
+		}
+	}
+	return nil
+}
+
 func writeHelper(v any) {
 	data, _ := json.Marshal(v)
 	fmt.Println(string(data))
@@ -65,7 +84,12 @@ func helperACPConfig(t *testing.T) config.ACPConfig {
 }

 func TestStartSessionLifecycleMapsACPEvents(t *testing.T) {
-	h := NewHarness(helperACPConfig(t), t.TempDir())
+	cfg := helperACPConfig(t)
+	cfg.Agent = "kiro-coder"
+	cfg.RegistryID = "kiro-registry"
+	cfg.Env["ACP_EXPECT_AGENT"] = cfg.Agent
+	cfg.Env["ACP_EXPECT_REGISTRY_ID"] = cfg.RegistryID
+	h := NewHarness(cfg, t.TempDir())
 	sess, err := h.StartSession(context.Background(), adapter.SessionOpts{SessionID: "s1", WorktreePath: t.TempDir(), SessionLogDir: t.TempDir(), UserPrompt: "do work", Model: strPtr("new")})
 	if err != nil {
 		t.Fatalf("StartSession: %v", err)
@@ -101,6 +125,42 @@ func TestCompactStrategyDetection(t *testing.T) {
 	}
 }

+func TestCapabilitiesReportSubstrateACPTools(t *testing.T) {
+	caps := NewHarness(config.ACPConfig{}, t.TempDir()).Capabilities()
+	expectedTools := []string{
+		"mcp__substrate-foreman__ask_foreman",
+		"acp/fs.read_text_file",
+		"acp/fs.write_text_file",
+		"acp/terminal.create",
+		"acp/terminal.output",
+		"acp/terminal.wait_for_exit",
+		"acp/terminal.kill",
+		"acp/terminal.release",
+	}
+	for _, tool := range expectedTools {
+		if !slices.Contains(caps.SupportedTools, tool) {
+			t.Fatalf("SupportedTools missing %q; got %v", tool, caps.SupportedTools)
+		}
+	}
+	for _, staleTool := range []string{"read", "write", "bash", "acp_fs", "acp_terminal"} {
+		if slices.Contains(caps.SupportedTools, staleTool) {
+			t.Fatalf("SupportedTools contains stale generic tool %q; got %v", staleTool, caps.SupportedTools)
+		}
+	}
+}
+
+func TestCapabilitiesRespectDisabledACPClientTools(t *testing.T) {
+	caps := NewHarness(config.ACPConfig{ClientFS: boolPtr(false), ClientTerminal: boolPtr(false)}, t.TempDir()).Capabilities()
+	for _, disabledTool := range []string{"acp/fs.read_text_file", "acp/fs.write_text_file", "acp/terminal.create"} {
+		if slices.Contains(caps.SupportedTools, disabledTool) {
+			t.Fatalf("SupportedTools contains disabled tool %q; got %v", disabledTool, caps.SupportedTools)
+		}
+	}
+	if !slices.Contains(caps.SupportedTools, "mcp__substrate-foreman__ask_foreman") {
+		t.Fatalf("SupportedTools missing foreman MCP tool; got %v", caps.SupportedTools)
+	}
+}
+
 func TestFileSystemClientMethodsEnforceRoot(t *testing.T) {
 	root := t.TempDir()
 	inside := filepath.Join(root, "dir", "file.txt")
```

## Diff for internal/adapter/acp/protocol.go
```diff
@@ -121,6 +121,8 @@ type sessionCreateParams struct {
 	SessionID  string      `json:"sessionId,omitempty"`
 	CWD        string      `json:"cwd"`
 	MCPServers []mcpServer `json:"mcpServers,omitempty"`
+	Agent      string      `json:"agent,omitempty"`
+	RegistryID string      `json:"registryId,omitempty"`
 }
```

## Diff for internal/adapter/acp/session.go
```diff
@@ -179,7 +179,7 @@ func (s *Session) ResumeInfo() map[string]string {
 }

 func (s *Session) setupACPSession(ctx context.Context, opts adapter.SessionOpts, mcpServers []mcpServer) (sessionResponse, string, error) {
-	params := sessionCreateParams{CWD: s.root, MCPServers: mcpServers}
+	params := sessionCreateParams{CWD: s.root, MCPServers: mcpServers, Agent: s.acpCfg.Agent, RegistryID: s.acpCfg.RegistryID}
```

## Diff for internal/adapter/opencode/harness.go
```diff
@@ -43,8 +43,8 @@ const (
 // serverURLPattern matches the "Server running on http://..." line from stdout.
 var serverURLPattern = regexp.MustCompile(`Server running on (http://[^\s]+)`)

-// foremanMCPBridgeName is the filename stem used to locate the foreman MCP bridge.
-const foremanMCPBridgeName = "opencode-foreman-mcp"
+// foremanMCPBridgeName is the filename stem used to locate the generic foreman MCP bridge.
+const foremanMCPBridgeName = "foreman-mcp"
```

## Instructions
1. Call `report_finding` tool per issue found (severity: high/medium/low)
2. Call `yield` tool with your final verdict when done reviewing
