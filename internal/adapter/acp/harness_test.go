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
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/sessionlog"
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
			sessionCapabilities := map[string]any{"close": map[string]any{}, "setConfigOption": map[string]any{}}
			if os.Getenv("ACP_DISABLE_RESUME") != "1" {
				sessionCapabilities["resume"] = map[string]any{}
			}
			writeHelper(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"protocolVersion": 1, "agentInfo": map[string]any{"name": os.Getenv("ACP_AGENT_NAME"), "version": "test"}, "agentCapabilities": map[string]any{"loadSession": true, "sessionCapabilities": sessionCapabilities}, "authMethods": []map[string]any{{"id": os.Getenv("ACP_AUTH_ID")}}}})
		case "session/new":
			if err := validateHelperSessionParams(msg); err != nil {
				writeHelper(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32602, "message": err.Error()}})
				continue
			}
			writeHelper(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"sessionId": "acp-sess-1", "configOptions": []map[string]any{{"id": "model", "name": "Model", "category": "model", "type": "select", "currentValue": "old", "options": []map[string]any{{"value": "new", "name": "New"}}}}}})
		case "session/resume":
			if os.Getenv("ACP_FAIL_RESUME") == "1" {
				writeHelper(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32603, "message": "Internal error"}})
				continue
			}
			writeHelper(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"sessionId": "acp-resumed"}})
		case "session/load":
			if os.Getenv("ACP_FAIL_LOAD") == "1" {
				writeHelper(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32603, "message": "Internal error"}})
				continue
			}
			writeHelper(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"sessionId": "acp-loaded"}})
		case "session/set_config_option":
			writeHelper(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"configOptions": []map[string]any{{"id": "model", "name": "Model", "category": "model", "type": "select", "currentValue": "new", "options": []map[string]any{{"value": "new", "name": "New"}}}}}})
		case "session/prompt":
			captureHelperPrompt(msg)
			if os.Getenv("ACP_ECHO_USER_MESSAGE") == "1" {
				writeHelper(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "acp-sess-1", "update": map[string]any{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": helperPromptText(msg)}}}})
			}
			writeHelper(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "acp-sess-1", "update": map[string]any{"sessionUpdate": "available_commands_update", "availableCommands": []map[string]any{{"name": "compact", "description": "compact session"}}}}})
			writeHelper(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "acp-sess-1", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "hello"}}}})
			writeHelper(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "acp-sess-1", "update": map[string]any{"sessionUpdate": "tool_call", "toolCallId": "tc1", "title": "Read file", "kind": "read", "status": "pending"}}})
			writeHelper(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "acp-sess-1", "update": map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "tc1", "status": "completed", "content": []map[string]any{{"type": "content", "content": map[string]any{"type": "text", "text": "ok"}}}}}})
			writeHelper(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"stopReason": "end_turn"}})
			if os.Getenv("ACP_EXIT_AFTER_PROMPT") == "1" {
				os.Exit(0)
			}
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

func captureHelperPrompt(msg map[string]any) {
	path := os.Getenv("ACP_PROMPT_CAPTURE")
	if path == "" {
		return
	}
	text := helperPromptText(msg)
	data, err := json.Marshal(text)
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, string(data))
}

func helperPromptText(msg map[string]any) string {
	params, _ := msg["params"].(map[string]any)
	blocks, _ := params["prompt"].([]any)
	if len(blocks) == 0 {
		return ""
	}
	block, _ := blocks[0].(map[string]any)
	text, _ := block["text"].(string)
	return text
}

func helperACPConfig(t *testing.T) config.ACPConfig {
	t.Helper()
	return config.ACPConfig{Command: os.Args[0], Args: []string{"-test.run=TestHelperACPProcess", "--"}, Env: map[string]string{"GO_WANT_ACP_HELPER": "1", "ACP_AGENT_NAME": "Kilo", "ACP_AUTH_ID": "kilo-login"}, ClientFS: boolPtr(true), ClientTerminal: boolPtr(true)}
}

func TestKiroACPAgentConfigUsesCommandArg(t *testing.T) {
	cfg := config.ACPConfig{Command: "kiro-cli", Args: []string{"acp"}, Agent: "my-agent", RegistryID: "ignored"}
	command, args := acpCommand(cfgWithSessionAgent(cfg))
	if command != "kiro-cli" {
		t.Fatalf("command = %q, want kiro-cli", command)
	}
	if !slices.Equal(args, []string{"acp", "--agent", "my-agent"}) {
		t.Fatalf("args = %#v, want --agent appended", args)
	}
	params := newSessionCreateParams("/workspace", []mcpServer{}, cfg)
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	if _, ok := raw["agent"]; ok {
		t.Fatalf("kiro session params include non-standard agent field: %s", data)
	}
	if _, ok := raw["registryId"]; ok {
		t.Fatalf("kiro session params include non-standard registryId field: %s", data)
	}
}

func TestNonKiroACPAgentConfigUsesSessionParams(t *testing.T) {
	cfg := config.ACPConfig{Command: "agent", Args: []string{"acp"}, Agent: "cursor", RegistryID: "cursor"}
	command, args := acpCommand(cfgWithSessionAgent(cfg))
	if command != "agent" || !slices.Equal(args, []string{"acp"}) {
		t.Fatalf("command,args = %q,%#v, want unchanged", command, args)
	}
	params := newSessionCreateParams("/workspace", []mcpServer{}, cfg)
	if params.Agent != "cursor" || params.RegistryID != "cursor" {
		t.Fatalf("params agent,registry = %q,%q, want cursor,cursor", params.Agent, params.RegistryID)
	}
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

func TestStartSessionFallsBackToNewWhenACPLoadFails(t *testing.T) {
	cfg := helperACPConfig(t)
	cfg.Env["ACP_DISABLE_RESUME"] = "1"
	cfg.Env["ACP_FAIL_LOAD"] = "1"
	h := NewHarness(cfg, t.TempDir())
	sess, err := h.StartSession(context.Background(), adapter.SessionOpts{
		SessionID:     "s-load-fallback",
		WorktreePath:  t.TempDir(),
		SessionLogDir: t.TempDir(),
		ResumeInfo:    map[string]string{"acp_agent_session_id": "stale-acp-session"},
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Abort(context.Background())
	info := sess.ResumeInfo()
	if info["acp_agent_session_id"] != "acp-sess-1" || info["acp_resume_method"] != "new" {
		t.Fatalf("ResumeInfo = %#v, want new ACP session after load failure", info)
	}
}

func TestStartSessionFallsBackToNewWhenACPResumeFails(t *testing.T) {
	cfg := helperACPConfig(t)
	cfg.Env["ACP_FAIL_RESUME"] = "1"
	h := NewHarness(cfg, t.TempDir())
	sess, err := h.StartSession(context.Background(), adapter.SessionOpts{
		SessionID:     "s-resume-fallback",
		WorktreePath:  t.TempDir(),
		SessionLogDir: t.TempDir(),
		ResumeInfo:    map[string]string{"acp_agent_session_id": "stale-acp-session"},
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Abort(context.Background())
	info := sess.ResumeInfo()
	if info["acp_agent_session_id"] != "acp-sess-1" || info["acp_resume_method"] != "new" {
		t.Fatalf("ResumeInfo = %#v, want new ACP session after resume failure", info)
	}
}

func TestStartSessionFoldsSessionContextIntoInitialPrompt(t *testing.T) {
	cfg := helperACPConfig(t)
	capturePath := filepath.Join(t.TempDir(), "prompts.jsonl")
	cfg.Env["ACP_PROMPT_CAPTURE"] = capturePath
	cfg.Env["ACP_ECHO_USER_MESSAGE"] = "1"
	logDir := t.TempDir()
	h := NewHarness(cfg, t.TempDir())
	sess, err := h.StartSession(context.Background(), adapter.SessionOpts{SessionID: "s-fold", WorktreePath: t.TempDir(), SessionLogDir: logDir, SystemPrompt: "system context", UserPrompt: "begin"})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Abort(context.Background())
	collectUntilDone(t, sess)
	prompts := readCapturedPrompts(t, capturePath)
	if len(prompts) != 1 || prompts[0] != "system context\n\nbegin" {
		t.Fatalf("captured prompts = %#v, want folded context+prompt", prompts)
	}
	entries := readACPLogEntries(t, logDir, "s-fold")
	if len(entries) < 3 {
		t.Fatalf("entries = %+v, want synthetic inputs and assistant output", entries)
	}
	if entries[0].InputKind != "session_context" || entries[0].Text != "system context" {
		t.Fatalf("first entry = %+v, want session context", entries[0])
	}
	if entries[1].InputKind != "prompt" || entries[1].Text != "begin" {
		t.Fatalf("second entry = %+v, want prompt", entries[1])
	}
	if slices.ContainsFunc(entries, func(e sessionlog.Entry) bool { return e.InputKind == "history" && e.Text == "system context\n\nbegin" }) {
		t.Fatalf("matching ACP history echo was not suppressed: %+v", entries)
	}
}

func TestStartSessionWithOnlySystemContextDoesNotAutoPrompt(t *testing.T) {
	cfg := helperACPConfig(t)
	capturePath := filepath.Join(t.TempDir(), "prompts.jsonl")
	cfg.Env["ACP_PROMPT_CAPTURE"] = capturePath
	h := NewHarness(cfg, t.TempDir())
	sess, err := h.StartSession(context.Background(), adapter.SessionOpts{SessionID: "s-idle", WorktreePath: t.TempDir(), SessionLogDir: t.TempDir(), SystemPrompt: "system context"})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Abort(context.Background())
	if _, err := os.Stat(capturePath); !os.IsNotExist(err) {
		t.Fatalf("capture file exists or stat failed: %v", err)
	}
}

func TestSendMessageFoldsPendingSessionContextOnce(t *testing.T) {
	cfg := helperACPConfig(t)
	capturePath := filepath.Join(t.TempDir(), "prompts.jsonl")
	cfg.Env["ACP_PROMPT_CAPTURE"] = capturePath
	logDir := t.TempDir()
	h := NewHarness(cfg, t.TempDir())
	sess, err := h.StartSession(context.Background(), adapter.SessionOpts{SessionID: "s-message", Mode: adapter.SessionModeForeman, WorktreePath: t.TempDir(), SessionLogDir: logDir, SystemPrompt: "system context"})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Abort(context.Background())
	if err := sess.SendMessage(context.Background(), "first"); err != nil {
		t.Fatalf("SendMessage first: %v", err)
	}
	collectUntilEventType(t, sess, "foreman_proposed")
	if err := sess.SendMessage(context.Background(), "second"); err != nil {
		t.Fatalf("SendMessage second: %v", err)
	}
	collectUntilEventType(t, sess, "foreman_proposed")
	prompts := readCapturedPrompts(t, capturePath)
	want := []string{"system context\n\nfirst", "second"}
	if !slices.Equal(prompts, want) {
		t.Fatalf("captured prompts = %#v, want %#v", prompts, want)
	}
	entries, err := sessionlog.ReadFile(filepath.Join(logDir, "s-message.log"))
	if err != nil {
		t.Fatalf("read session log: %v", err)
	}
	var inputs []sessionlog.Entry
	for _, entry := range entries {
		if entry.Kind == sessionlog.KindInput {
			inputs = append(inputs, entry)
		}
	}
	if len(inputs) != 3 {
		t.Fatalf("input entries = %+v, want session_context + two messages", inputs)
	}
	if inputs[0].InputKind != "session_context" || inputs[1].InputKind != "message" || inputs[2].InputKind != "message" {
		t.Fatalf("input entries = %+v, want session_context/message/message", inputs)
	}
}

func TestSteerAndCompactDoNotConsumePendingSessionContext(t *testing.T) {
	cfg := helperACPConfig(t)
	capturePath := filepath.Join(t.TempDir(), "prompts.jsonl")
	cfg.Env["ACP_PROMPT_CAPTURE"] = capturePath
	h := NewHarness(cfg, t.TempDir())
	sess, err := h.StartSession(context.Background(), adapter.SessionOpts{SessionID: "s-no-consume", Mode: adapter.SessionModeForeman, WorktreePath: t.TempDir(), SessionLogDir: t.TempDir(), SystemPrompt: "system context"})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Abort(context.Background())
	if err := sess.Compact(context.Background()); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	collectUntilEventType(t, sess, "foreman_proposed")
	if err := sess.Steer(context.Background(), "steer note"); err != nil {
		t.Fatalf("Steer: %v", err)
	}
	collectUntilEventType(t, sess, "foreman_proposed")
	if err := sess.SendMessage(context.Background(), "after controls"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	collectUntilEventType(t, sess, "foreman_proposed")
	prompts := readCapturedPrompts(t, capturePath)
	if len(prompts) != 3 {
		t.Fatalf("captured prompts = %#v, want compact, steer, and message", prompts)
	}
	if prompts[0] != "/compact" {
		t.Fatalf("compact prompt = %q, want /compact", prompts[0])
	}
	if prompts[1] != "Steering update from operator:\n\nsteer note" {
		t.Fatalf("steer prompt = %q, want steer prompt", prompts[1])
	}
	if prompts[2] != "system context\n\nafter controls" {
		t.Fatalf("message prompt = %q, want folded context", prompts[2])
	}
}

func TestStartSessionLogsCanonicalRecordsNotRawProtocol(t *testing.T) {
	cfg := helperACPConfig(t)
	h := NewHarness(cfg, t.TempDir())
	logDir := t.TempDir()
	const sessionID = "s-canon"
	sess, err := h.StartSession(context.Background(), adapter.SessionOpts{SessionID: sessionID, WorktreePath: t.TempDir(), SessionLogDir: logDir, UserPrompt: "do work"})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Abort(context.Background())
	collectUntilDone(t, sess)

	// Read the active log before Abort compresses it.
	entries, err := sessionlog.ReadFile(filepath.Join(logDir, sessionID+".log"))
	if err != nil {
		t.Fatalf("read session log: %v", err)
	}
	if !slices.ContainsFunc(entries, func(e sessionlog.Entry) bool {
		return e.Kind == sessionlog.KindAssistant && strings.Contains(e.Text, "hello")
	}) {
		t.Fatalf("missing canonical assistant entry in %+v", entries)
	}
	if !slices.ContainsFunc(entries, func(e sessionlog.Entry) bool { return e.Kind == sessionlog.KindToolStart }) {
		t.Fatalf("missing canonical tool_start entry in %+v", entries)
	}
	// The raw JSON-RPC handshake (e.g. initialize) must never leak into the
	// transcript log; only canonical event records are persisted there.
	for _, e := range entries {
		if strings.Contains(e.Text, "jsonrpc") || strings.Contains(e.Text, "initialize") {
			t.Fatalf("raw protocol frame leaked into session log: %q", e.Text)
		}
	}
}

func TestStartSessionArchivesACPLogWithDiscoverableSegmentName(t *testing.T) {
	cfg := helperACPConfig(t)
	h := NewHarness(cfg, t.TempDir())
	logDir := t.TempDir()
	const sessionID = "s1"
	sess, err := h.StartSession(context.Background(), adapter.SessionOpts{SessionID: sessionID, WorktreePath: t.TempDir(), SessionLogDir: logDir, UserPrompt: "do work"})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	events := collectUntilDone(t, sess)
	if !slices.ContainsFunc(events, func(e adapter.AgentEvent) bool { return e.Type == "text_delta" }) {
		t.Fatalf("missing text_delta in %#v", events)
	}
	if err := sess.Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	pattern := filepath.Join(logDir, sessionID+".log.*.gz")
	deadline := time.Now().Add(2 * time.Second)
	var matches []string
	for time.Now().Before(deadline) {
		matches, err = filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("glob archived ACP logs: %v", err)
		}
		if len(matches) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(matches) == 0 {
		t.Fatalf("archived logs matching %q: none", pattern)
	}
	if _, err := os.Stat(filepath.Join(logDir, sessionID+".log.gz")); !os.IsNotExist(err) {
		t.Fatalf("legacy final archive exists or stat failed: %v", err)
	}
}

func TestMapSessionUpdateToolCallRawInput(t *testing.T) {
	events := mapSessionUpdate(json.RawMessage(`{"sessionUpdate":"tool_call","toolCallId":"tc1","title":"read","kind":"read","status":"pending","rawInput":{"operations":[{"mode":"Line","path":"/tmp/plan.md","limit":10}]}}`))
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1: %#v", len(events), events)
	}
	if events[0].Type != "tool_start" {
		t.Fatalf("event type = %q, want tool_start", events[0].Type)
	}
	if events[0].Payload == "" || events[0].Payload[0] != '{' {
		t.Fatalf("payload = %q, want raw JSON args", events[0].Payload)
	}
	if got, _ := events[0].Metadata["tool"].(string); got != "read" {
		t.Fatalf("metadata tool = %q, want read", got)
	}
	if got, _ := events[0].Metadata["intent"].(string); got != "read" {
		t.Fatalf("metadata intent = %q, want read", got)
	}
}

func TestMapSessionUpdateToolCallTitleFallback(t *testing.T) {
	events := mapSessionUpdate(json.RawMessage(`{"sessionUpdate":"tool_call","toolCallId":"tc1","title":"ask_foreman","status":"pending","rawInput":{"question":"Proceed?"}}`))
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1: %#v", len(events), events)
	}
	if got, _ := events[0].Metadata["tool"].(string); got != "ask_foreman" {
		t.Fatalf("metadata tool = %q, want ask_foreman", got)
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

func TestMapSessionUpdateTodoToolCall(t *testing.T) {
	start := mapSessionUpdate(json.RawMessage(`{"sessionUpdate":"tool_call","toolCallId":"tc1","title":"Creating task list: Implement chart","rawInput":{"command":"create","task_list_description":"Implement chart","tasks":[{"task_description":"Create chart"}]}}`))
	if len(start) != 1 {
		t.Fatalf("got %d start events, want 1: %#v", len(start), start)
	}
	if start[0].Type != "tool_start" {
		t.Fatalf("start event type = %q, want tool_start", start[0].Type)
	}
	if got, _ := start[0].Metadata["tool"].(string); got != "todo_list" {
		t.Fatalf("start metadata tool = %q, want todo_list", got)
	}
	if got, _ := start[0].Metadata["intent"].(string); got != "Creating task list: Implement chart" {
		t.Fatalf("start metadata intent = %q", got)
	}

	result := mapSessionUpdate(json.RawMessage(`{"sessionUpdate":"tool_call_update","toolCallId":"tc1","title":"Creating task list: Implement chart","kind":"other","status":"completed","rawInput":{"command":"create","task_list_description":"Implement chart","tasks":[{"task_description":"Create chart"}]},"rawOutput":{"items":[{"Json":{"tasks":[{"id":"1","task_description":"Create chart","completed":false}],"description":"Implement chart","context":[],"modified_files":[]}}]}}`))
	if len(result) != 1 {
		t.Fatalf("got %d result events, want 1: %#v", len(result), result)
	}
	if result[0].Type != "tool_result" {
		t.Fatalf("result event type = %q, want tool_result", result[0].Type)
	}
	if got, _ := result[0].Metadata["tool"].(string); got != "todo_list" {
		t.Fatalf("result metadata tool = %q, want todo_list", got)
	}
	if result[0].Payload != "" {
		t.Fatalf("result payload = %q, want todo state payload suppressed", result[0].Payload)
	}
}

func TestBuildMCPServersUsesHumanQuestionToolForHumanPolicy(t *testing.T) {
	dir := t.TempDir()
	bridgePath := filepath.Join(dir, "question-mcp")
	if err := os.WriteFile(bridgePath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write bridge: %v", err)
	}
	s := &Session{
		mode:      adapter.SessionModeAgent,
		acpCfg:    config.ACPConfig{QuestionBridgePath: bridgePath},
		questions: newQuestionBroker("manual-session", adapter.SessionModeAgent, func(adapter.AgentEvent) {}),
	}
	servers := s.buildQuestionMCPServers(adapter.QuestionToolPolicyHuman)
	if s.questionSocket != nil {
		defer s.questionSocket.close()
	}
	if len(servers) != 1 {
		t.Fatalf("mcp servers = %d, want 1", len(servers))
	}
	if servers[0].Name != "substrate-user" {
		t.Fatalf("server name = %q, want substrate-user", servers[0].Name)
	}
	if s.questionSocket == nil || s.questionSocket.source != adapter.AgentQuestionSourceAskUser {
		t.Fatalf("socket source = %#v, want ask_user", s.questionSocket)
	}
	var modeEnv string
	for _, env := range servers[0].Env {
		if env.Name == questionToolModeEnv {
			modeEnv = env.Value
		}
	}
	if modeEnv != "human" {
		t.Fatalf("%s = %q, want human", questionToolModeEnv, modeEnv)
	}
}

func TestBuildMCPServersSkipsQuestionToolsForNonePolicy(t *testing.T) {
	s := &Session{mode: adapter.SessionModeAgent}
	if servers := s.buildQuestionMCPServers(adapter.QuestionToolPolicyNone); len(servers) != 0 {
		t.Fatalf("mcp servers = %#v, want none", servers)
	}
}

func TestSessionSendAnswerWritesAnswerInputLog(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "session.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	broker := newQuestionBroker("manual-session", adapter.SessionModeAgent, func(adapter.AgentEvent) {})
	broker.pending["q-1"] = &pendingQuestion{id: "q-1", answer: make(chan string, 1)}
	s := &Session{questions: broker, logFile: logFile}

	if err := s.SendAnswer(context.Background(), "operator answer"); err != nil {
		t.Fatalf("SendAnswer: %v", err)
	}
	if err := logFile.Close(); err != nil {
		t.Fatalf("close log: %v", err)
	}
	entries, err := sessionlog.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if len(entries) != 1 || entries[0].InputKind != "answer" || entries[0].Text != "operator answer" {
		t.Fatalf("entries = %+v, want answer input", entries)
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

func TestAbortAfterSessionFinished(t *testing.T) {
	cfg := helperACPConfig(t)
	cfg.Env["ACP_EXIT_AFTER_PROMPT"] = "1"
	h := NewHarness(cfg, t.TempDir())
	sess, err := h.StartSession(context.Background(), adapter.SessionOpts{SessionID: "s-finished", WorktreePath: t.TempDir(), SessionLogDir: t.TempDir(), UserPrompt: "do work"})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	collectUntilDone(t, sess)
	concrete := sess.(*Session)
	select {
	case <-concrete.client.wait():
	case <-time.After(time.Second):
		t.Fatal("rpc client did not close after helper exit")
	}
	select {
	case <-concrete.processDone:
	case <-time.After(time.Second):
		t.Fatal("helper process was not reaped after exit")
	}
	if !concrete.client.Closed() {
		t.Fatal("rpc client is not closed before abort")
	}
	// Abort on a fully-finished session must be a silent no-op: the rpc
	// client is already closed, the process is reaped, and any further
	// cancel/close attempt would just produce a noisy "acp rpc client
	// closed" error up the stack.
	if err := sess.Abort(context.Background()); err != nil {
		t.Fatalf("Abort after finished: %v", err)
	}
}

func TestResolveQuestionMCPBridgeUsesConfiguredExecutable(t *testing.T) {
	dir := t.TempDir()
	bridgePath := filepath.Join(dir, "custom-question-mcp")
	if err := os.WriteFile(bridgePath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write bridge: %v", err)
	}
	cmd, args, err := resolveQuestionMCPBridgeFrom(bridgePath, filepath.Join(dir, "substrate"))
	if err != nil {
		t.Fatalf("resolveQuestionMCPBridgeFrom: %v", err)
	}
	if cmd != bridgePath || len(args) != 0 {
		t.Fatalf("resolved cmd=%q args=%v, want configured executable with no args", cmd, args)
	}
}

func TestResolveQuestionMCPBridgeUsesConfiguredScript(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Skipf("bun not on PATH: %v", err)
	}
	dir := t.TempDir()
	bridgePath := filepath.Join(dir, "question-mcp", "index.ts")
	if err := os.MkdirAll(filepath.Dir(bridgePath), 0o755); err != nil {
		t.Fatalf("create bridge dir: %v", err)
	}
	if err := os.WriteFile(bridgePath, []byte("console.log('question')\n"), 0o644); err != nil {
		t.Fatalf("write bridge script: %v", err)
	}
	cmd, args, err := resolveQuestionMCPBridgeFrom(bridgePath, filepath.Join(dir, "substrate"))
	if err != nil {
		t.Fatalf("resolveQuestionMCPBridgeFrom: %v", err)
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
		"mcp__substrate-user__ask_user",
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
	for _, questionTool := range []string{"mcp__substrate-foreman__ask_foreman", "mcp__substrate-user__ask_user"} {
		if !slices.Contains(caps.SupportedTools, questionTool) {
			t.Fatalf("SupportedTools missing question MCP tool %q; got %v", questionTool, caps.SupportedTools)
		}
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

func TestForemanEmitKeepsProposalOutOfBackpressure(t *testing.T) {
	t.Parallel()

	sess := &Session{
		mode:   adapter.SessionModeForeman,
		events: make(chan adapter.AgentEvent, 1),
		done:   make(chan struct{}),
	}

	for range 512 {
		sess.emit(adapter.AgentEvent{Type: "text_delta", Payload: "x"})
		sess.emit(adapter.AgentEvent{Type: "tool_output", Payload: "ignored"})
	}
	if got := len(sess.Events()); got != 0 {
		t.Fatalf("foreman event buffer len = %d, want 0 before proposal", got)
	}
	if got := len(sess.foremanText); got != 512 {
		t.Fatalf("foremanText len = %d, want 512", got)
	}

	sess.emit(adapter.AgentEvent{Type: "foreman_proposed", Payload: sess.foremanText})

	select {
	case evt := <-sess.Events():
		if evt.Type != "foreman_proposed" {
			t.Fatalf("event type = %q, want foreman_proposed", evt.Type)
		}
		if len(evt.Payload) != 512 {
			t.Fatalf("proposal payload len = %d, want 512", len(evt.Payload))
		}
	default:
		t.Fatal("foreman proposal was not emitted")
	}
}

func collectUntilEventType(t *testing.T, sess adapter.AgentSession, eventType string) adapter.AgentEvent {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case e, ok := <-sess.Events():
			if !ok {
				t.Fatalf("session closed before %s", eventType)
			}
			if e.Type == eventType {
				return e
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", eventType)
		}
	}
}

func readCapturedPrompts(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read prompt capture: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	prompts := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		var prompt string
		if err := json.Unmarshal([]byte(line), &prompt); err != nil {
			t.Fatalf("decode prompt capture %q: %v", line, err)
		}
		prompts = append(prompts, prompt)
	}
	return prompts
}

func readACPLogEntries(t *testing.T, logDir, sessionID string) []sessionlog.Entry {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		candidates := []string{filepath.Join(logDir, sessionID+".log")}
		matches, err := filepath.Glob(filepath.Join(logDir, sessionID+".log.*.gz"))
		if err != nil {
			t.Fatalf("glob compressed logs: %v", err)
		}
		candidates = append(candidates, matches...)
		for _, path := range candidates {
			entries, err := sessionlog.ReadFile(path)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				t.Fatalf("read session log %s: %v", path, err)
			}
			if len(entries) > 0 {
				return entries
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no ACP log entries for %s", sessionID)
	return nil
}

func strPtr(s string) *string { return &s }

func boolPtr(v bool) *bool { return &v }
