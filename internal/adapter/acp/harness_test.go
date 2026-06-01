package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
)

func TestHelperACPProcess(t *testing.T) {
	if os.Getenv("GO_WANT_ACP_HELPER") != "1" {
		return
	}
	r := bufio.NewScanner(os.Stdin)
	for r.Scan() {
		var msg map[string]any
		if err := json.Unmarshal(r.Bytes(), &msg); err != nil {
			continue
		}
		method, _ := msg["method"].(string)
		id, hasID := msg["id"]
		switch method {
		case "initialize":
			if err := validateHelperInitializeParams(msg); err != nil {
				writeHelper(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32602, "message": err.Error()}})
				continue
			}
			writeHelper(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"protocolVersion": 1, "agentInfo": map[string]any{"name": os.Getenv("ACP_AGENT_NAME"), "version": "test"}, "agentCapabilities": map[string]any{"loadSession": true, "sessionCapabilities": map[string]any{"resume": map[string]any{}, "close": map[string]any{}, "setConfigOption": map[string]any{}}}, "authMethods": []map[string]any{{"id": os.Getenv("ACP_AUTH_ID")}}}})
		case "session/new":
			if err := validateHelperSessionParams(msg); err != nil {
				writeHelper(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32602, "message": err.Error()}})
				continue
			}
			writeHelper(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"sessionId": "acp-sess-1", "configOptions": []map[string]any{{"id": "model", "name": "Model", "category": "model", "type": "select", "currentValue": "old", "options": []map[string]any{{"value": "new", "name": "New"}}}}}})
		case "session/resume":
			writeHelper(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"sessionId": "acp-resumed"}})
		case "session/set_config_option":
			writeHelper(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"configOptions": []map[string]any{{"id": "model", "name": "Model", "category": "model", "type": "select", "currentValue": "new", "options": []map[string]any{{"value": "new", "name": "New"}}}}}})
		case "session/prompt":
			writeHelper(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "acp-sess-1", "update": map[string]any{"sessionUpdate": "available_commands_update", "availableCommands": []map[string]any{{"name": "compact", "description": "compact session"}}}}})
			writeHelper(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "acp-sess-1", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "hello"}}}})
			writeHelper(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "acp-sess-1", "update": map[string]any{"sessionUpdate": "tool_call", "toolCallId": "tc1", "title": "Read file", "kind": "read", "status": "pending"}}})
			writeHelper(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "acp-sess-1", "update": map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "tc1", "status": "completed", "content": []map[string]any{{"type": "content", "content": map[string]any{"type": "text", "text": "ok"}}}}}})
			writeHelper(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"stopReason": "end_turn"}})
		case "session/cancel", "session/close":
			if hasID {
				writeHelper(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{}})
			}
		case "authenticate":
			writeHelper(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"authenticated": true}})
		}
	}
	os.Exit(0)
}

func validateHelperInitializeParams(msg map[string]any) error {
	params, _ := msg["params"].(map[string]any)
	clientInfo, _ := params["clientInfo"].(map[string]any)
	version, _ := clientInfo["version"].(string)
	if version == "" {
		return fmt.Errorf("clientInfo.version is required")
	}
	return nil
}

func validateHelperSessionParams(msg map[string]any) error {
	params, _ := msg["params"].(map[string]any)
	if _, ok := params["mcpServers"].([]any); !ok {
		return fmt.Errorf("mcpServers must be present as an array")
	}
	for _, field := range []struct{ env, key string }{{"ACP_EXPECT_AGENT", "agent"}, {"ACP_EXPECT_REGISTRY_ID", "registryId"}} {
		want := os.Getenv(field.env)
		if want == "" {
			continue
		}
		got, _ := params[field.key].(string)
		if got != want {
			return fmt.Errorf("%s = %q, want %q", field.key, got, want)
		}
	}
	return nil
}

func writeHelper(v any) {
	data, _ := json.Marshal(v)
	fmt.Println(string(data))
}

func helperACPConfig(t *testing.T) config.ACPConfig {
	t.Helper()
	return config.ACPConfig{Command: os.Args[0], Args: []string{"-test.run=TestHelperACPProcess", "--"}, Env: map[string]string{"GO_WANT_ACP_HELPER": "1", "ACP_AGENT_NAME": "Kilo", "ACP_AUTH_ID": "kilo-login"}, ClientFS: boolPtr(true), ClientTerminal: boolPtr(true)}
}

func TestStartSessionLifecycleMapsACPEvents(t *testing.T) {
	cfg := helperACPConfig(t)
	cfg.Agent = "kiro-coder"
	cfg.RegistryID = "kiro-registry"
	cfg.Env["ACP_EXPECT_AGENT"] = cfg.Agent
	cfg.Env["ACP_EXPECT_REGISTRY_ID"] = cfg.RegistryID
	h := NewHarness(cfg, t.TempDir())
	sess, err := h.StartSession(context.Background(), adapter.SessionOpts{SessionID: "s1", WorktreePath: t.TempDir(), SessionLogDir: t.TempDir(), UserPrompt: "do work", Model: strPtr("new")})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Abort(context.Background())
	events := collectUntilDone(t, sess)
	for _, typ := range []string{"started", "text_delta", "tool_start", "tool_result", "done"} {
		if !slices.ContainsFunc(events, func(e adapter.AgentEvent) bool { return e.Type == typ }) {
			t.Fatalf("missing event %s in %#v", typ, events)
		}
	}
	info := sess.ResumeInfo()
	if info["acp_agent_session_id"] != "acp-sess-1" || info["acp_agent_name"] != "Kilo" {
		t.Fatalf("ResumeInfo = %#v", info)
	}
	if err := sess.Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

func TestMapSessionUpdateToolCallRawOutput(t *testing.T) {
	events := mapSessionUpdate(json.RawMessage(`{"sessionUpdate":"tool_call_update","toolCallId":"tc1","kind":"read","status":"completed","rawOutput":{"items":[{"Text":"User id: 502\n-rw-r--r-- file.yaml"}]}}`))
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1: %#v", len(events), events)
	}
	if events[0].Type != "tool_result" {
		t.Fatalf("event type = %q, want tool_result", events[0].Type)
	}
	if events[0].Payload != "User id: 502\n-rw-r--r-- file.yaml" {
		t.Fatalf("payload = %q", events[0].Payload)
	}
}

func TestStartSessionIncludesEmptyMCPServers(t *testing.T) {
	cfg := helperACPConfig(t)
	h := NewHarness(cfg, t.TempDir())
	sess, err := h.StartSession(context.Background(), adapter.SessionOpts{SessionID: "s1", Mode: adapter.SessionModeForeman, WorktreePath: t.TempDir(), SessionLogDir: t.TempDir()})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if err := sess.Abort(context.Background()); err != nil {
		t.Fatalf("Abort: %v", err)
	}
}

func TestResolveForemanMCPBridgeUsesConfiguredExecutable(t *testing.T) {
	dir := t.TempDir()
	bridgePath := filepath.Join(dir, "custom-foreman")
	if err := os.WriteFile(bridgePath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write bridge: %v", err)
	}
	cmd, args, err := resolveForemanMCPBridgeFrom(bridgePath, filepath.Join(dir, "substrate"))
	if err != nil {
		t.Fatalf("resolveForemanMCPBridgeFrom: %v", err)
	}
	if cmd != bridgePath || len(args) != 0 {
		t.Fatalf("resolved cmd=%q args=%v, want configured executable with no args", cmd, args)
	}
}

func TestResolveForemanMCPBridgeUsesConfiguredScript(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Skipf("bun not on PATH: %v", err)
	}
	dir := t.TempDir()
	bridgePath := filepath.Join(dir, "foreman-mcp", "index.ts")
	if err := os.MkdirAll(filepath.Dir(bridgePath), 0o755); err != nil {
		t.Fatalf("create bridge dir: %v", err)
	}
	if err := os.WriteFile(bridgePath, []byte("console.log('foreman')\n"), 0o644); err != nil {
		t.Fatalf("write bridge script: %v", err)
	}
	cmd, args, err := resolveForemanMCPBridgeFrom(bridgePath, filepath.Join(dir, "substrate"))
	if err != nil {
		t.Fatalf("resolveForemanMCPBridgeFrom: %v", err)
	}
	if cmd != bun || len(args) != 1 || args[0] != bridgePath {
		t.Fatalf("resolved cmd=%q args=%v, want bun %q with configured script", cmd, args, bun)
	}
}

func TestCompactStrategyDetection(t *testing.T) {
	cfg := config.ACPConfig{Command: "agent", Args: []string{"acp"}}
	if got := detectConfiguredCompactStrategy(cfg).command; got != "compress" {
		t.Fatalf("cursor command strategy = %q, want compress", got)
	}
	init := initializeResponse{AgentInfo: implementationInfo{Name: "Kilo"}}
	if got := detectCompactStrategy(init, config.ACPConfig{}, nil).command; got != "compact" {
		t.Fatalf("Kilo strategy = %q, want compact", got)
	}
	cmds := []availableCommand{{Name: "compress"}}
	if got := detectCompactStrategy(initializeResponse{AgentInfo: implementationInfo{Name: "Kilo"}}, config.ACPConfig{}, cmds).command; got != "compress" {
		t.Fatalf("advertised strategy = %q, want compress", got)
	}
}

func TestCapabilitiesReportSubstrateACPTools(t *testing.T) {
	caps := NewHarness(config.ACPConfig{}, t.TempDir()).Capabilities()
	expectedTools := []string{
		"mcp__substrate-foreman__ask_foreman",
		"acp/fs.read_text_file",
		"acp/fs.write_text_file",
		"acp/terminal.create",
		"acp/terminal.output",
		"acp/terminal.wait_for_exit",
		"acp/terminal.kill",
		"acp/terminal.release",
	}
	for _, tool := range expectedTools {
		if !slices.Contains(caps.SupportedTools, tool) {
			t.Fatalf("SupportedTools missing %q; got %v", tool, caps.SupportedTools)
		}
	}
	for _, staleTool := range []string{"read", "write", "bash", "acp_fs", "acp_terminal"} {
		if slices.Contains(caps.SupportedTools, staleTool) {
			t.Fatalf("SupportedTools contains stale generic tool %q; got %v", staleTool, caps.SupportedTools)
		}
	}
}

func TestCapabilitiesRespectDisabledACPClientTools(t *testing.T) {
	caps := NewHarness(config.ACPConfig{ClientFS: boolPtr(false), ClientTerminal: boolPtr(false)}, t.TempDir()).Capabilities()
	for _, disabledTool := range []string{"acp/fs.read_text_file", "acp/fs.write_text_file", "acp/terminal.create"} {
		if slices.Contains(caps.SupportedTools, disabledTool) {
			t.Fatalf("SupportedTools contains disabled tool %q; got %v", disabledTool, caps.SupportedTools)
		}
	}
	if !slices.Contains(caps.SupportedTools, "mcp__substrate-foreman__ask_foreman") {
		t.Fatalf("SupportedTools missing foreman MCP tool; got %v", caps.SupportedTools)
	}
}

func TestFileSystemClientMethodsEnforceRoot(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "dir", "file.txt")
	s := &Session{root: root}
	if err := s.handleWriteTextFile(mustJSON(t, fsWriteTextFileParams{Path: inside, Content: "a\nb\nc\n"})); err != nil {
		t.Fatalf("write inside: %v", err)
	}
	resp, err := s.handleReadTextFile(mustJSON(t, fsReadTextFileParams{Path: inside, Line: 2, Limit: 1}))
	if err != nil {
		t.Fatalf("read inside: %v", err)
	}
	if resp.Content != "b\n" {
		t.Fatalf("read content = %q, want b\\n", resp.Content)
	}
	outside := filepath.Join(t.TempDir(), "escape.txt")
	if err := s.handleWriteTextFile(mustJSON(t, fsWriteTextFileParams{Path: outside, Content: "no"})); err == nil {
		t.Fatal("write outside succeeded; want rejection")
	}
}

func TestTerminalRingTruncatesAtUTF8Boundary(t *testing.T) {
	var r terminalRing
	r.limit = 5
	r.write([]byte("åååå"))
	if !r.truncated {
		t.Fatal("truncated = false, want true")
	}
	if !utf8.Valid(r.buf) {
		t.Fatalf("buffer is not valid UTF-8: %q", string(r.buf))
	}
}

func TestSessionResponseDecodesSpecModeState(t *testing.T) {
	data := []byte(`{"sessionId":"acp-sess-1","modes":{"currentModeId":"code","availableModes":[{"id":"code","name":"Code","description":"write code"}]}}`)
	var resp sessionResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("Unmarshal sessionResponse: %v", err)
	}
	if resp.Modes.CurrentModeID != "code" {
		t.Fatalf("CurrentModeID = %q, want code", resp.Modes.CurrentModeID)
	}
	if len(resp.Modes.AvailableModes) != 1 || resp.Modes.AvailableModes[0].ID != "code" {
		t.Fatalf("AvailableModes = %#v, want code mode", resp.Modes.AvailableModes)
	}
}

func TestSessionResponseDecodesLegacyModeList(t *testing.T) {
	data := []byte(`{"sessionId":"acp-sess-1","modes":[{"id":"ask","name":"Ask"}]}`)
	var resp sessionResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("Unmarshal legacy sessionResponse: %v", err)
	}
	if len(resp.Modes.AvailableModes) != 1 || resp.Modes.AvailableModes[0].ID != "ask" {
		t.Fatalf("AvailableModes = %#v, want ask mode", resp.Modes.AvailableModes)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

func collectUntilDone(t *testing.T, sess adapter.AgentSession) []adapter.AgentEvent {
	t.Helper()
	deadline := time.After(5 * time.Second)
	var events []adapter.AgentEvent
	for {
		select {
		case e, ok := <-sess.Events():
			if !ok {
				return events
			}
			events = append(events, e)
			if e.Type == "done" {
				return events
			}
		case <-deadline:
			t.Fatalf("timed out waiting for done; events=%#v", events)
		}
	}
}

func strPtr(s string) *string { return &s }

func boolPtr(v bool) *bool { return &v }
