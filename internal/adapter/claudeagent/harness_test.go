package claudeagent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/adapter/bridge"
	"github.com/beeemT/substrate/internal/config"
)

// Stage 1 harness-contract checklist — Claude Agent adapter:
//   §1  config/readiness path: NewHarness, bridge candidates, dependency checks
//   §2  stable session ID / logs use Substrate session ID
//   §3  initial prompt reaches child boundary
//   §4  assistant output → text_delta or equivalent
//   §5  terminal success → done
//   §6  follow-up messaging (SupportsMessaging = true)
//   §7  SendAnswer / Steer / Compact work or documented unsupported
//   §8  abort closes event stream and process resources
//   §9  readiness/startup/malformed failures → useful errors
//   §10 canonical assistant log output review-parseable

// §1 — config/readiness path: harness name and capabilities

func TestHarnessNameAndCapabilities(t *testing.T) {
	h := NewHarness(config.ClaudeCodeConfig{}, "/tmp")

	if got := h.Name(); got != "claude-code" {
		t.Errorf("Name() = %q, want %q", got, "claude-code")
	}

	caps := h.Capabilities()
	if !caps.SupportsStreaming {
		t.Error("expected SupportsStreaming == true")
	}
	if !caps.SupportsMessaging {
		t.Error("expected SupportsMessaging == true")
	}
	if !caps.SupportsNativeResume {
		t.Error("expected SupportsNativeResume == true")
	}
	if !slices.Contains(caps.SupportedTools, "mcp__substrate__ask_foreman") {
		t.Errorf("SupportedTools does not contain mcp__substrate__ask_foreman; got %v", caps.SupportedTools)
	}
	if !slices.Contains(caps.SupportedTools, "mcp__substrate__ask_user") {
		t.Errorf("SupportedTools does not contain mcp__substrate__ask_user; got %v", caps.SupportedTools)
	}
	if !slices.Contains(caps.SupportedTools, "AskUserQuestion") {
		t.Errorf("SupportedTools does not contain AskUserQuestion; got %v", caps.SupportedTools)
	}
}

func ptrS(s string) *string { return &s }

// §3 — initial prompt serialization reaches child boundary

func TestInitMessageSerialization(t *testing.T) {
	msg := bridgeInitMsg{
		Type:         "init",
		Mode:         "agent",
		SystemPrompt: "sys",
		Model:        ptrS("claude-3"),
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	checkStr := func(key, want string) {
		t.Helper()
		v, ok := result[key].(string)
		if !ok || v != want {
			t.Errorf("result[%q] = %v, want %q", key, result[key], want)
		}
	}
	checkStr("type", "init")
	checkStr("mode", "agent")
	checkStr("system_prompt", "sys")
	checkStr("model", "claude-3")
}

// §1 — config/readiness path: bridge candidate resolution

func TestBridgeCandidatesDefault(t *testing.T) {
	candidates := bridge.BridgeCandidates("", "/usr/local/bin/substrate", "claude-agent-bridge")
	found := false
	for _, c := range candidates {
		if strings.Contains(c, "claude-agent-bridge") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected claude-agent-bridge in candidates, got %v", candidates)
	}
}

// §1 — config/readiness path: configured bridge candidates

func TestBridgeCandidatesConfigured(t *testing.T) {
	// Absolute path is returned as-is.
	abs := bridge.BridgeCandidates("/absolute/path/bridge", "/usr/local/bin/substrate", "claude-agent-bridge")
	if len(abs) != 1 || abs[0] != "/absolute/path/bridge" {
		t.Errorf("absolute configured path: got %v", abs)
	}

	// Relative path is resolved against exec dir and share dir.
	rel := bridge.BridgeCandidates("bridge/my-bridge", "/usr/local/bin/substrate", "claude-agent-bridge")
	if len(rel) == 0 {
		t.Error("expected candidates for relative path, got none")
	}
	for _, c := range rel {
		if strings.Contains(c, "omp-bridge") {
			t.Errorf("configured relative path contains unexpected 'omp-bridge': %v", c)
		}
	}
}

// §9 — readiness failure: missing bridge dependency message

func TestBridgeDependencyCheckMessage(t *testing.T) {
	// With empty config, bridge won't be found in test env.
	err := ValidateReadiness(config.ClaudeCodeConfig{})
	if err == nil {
		t.Skip("bridge unexpectedly available; skipping error-message test")
	}
	// Error should mention "bridge" context.
	if !strings.Contains(err.Error(), "bridge") {
		t.Errorf("error message does not mention bridge: %v", err)
	}
}

// §1 — config/readiness path: packaged bridge resolution

func TestResolveBridgeRuntimeFromUsesPackagedBridge(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	execPath := filepath.Join(root, "bin", "substrate")
	bridgePath := filepath.Join(root, "share", "substrate", "bridge", "claude-agent-bridge")

	if err := os.MkdirAll(filepath.Dir(execPath), 0o755); err != nil {
		t.Fatalf("mkdir executable dir: %v", err)
	}
	if err := os.WriteFile(execPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(bridgePath), 0o755); err != nil {
		t.Fatalf("mkdir bridge dir: %v", err)
	}
	if err := os.WriteFile(bridgePath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write bridge: %v", err)
	}

	rt, err := bridge.ResolveBridgeRuntimeFrom("", execPath, "claude-agent-bridge", "claude-agent bridge")
	if err != nil {
		t.Fatalf("ResolveBridgeRuntimeFrom() error = %v", err)
	}
	if rt.NeedsBun {
		t.Error("expected NeedsBun == false for a binary bridge")
	}
	if !strings.Contains(rt.Path, "claude-agent-bridge") {
		t.Errorf("expected path to contain claude-agent-bridge, got %q", rt.Path)
	}
}

// §9 — readiness failure: missing node_modules for source bridge

func TestEnsureBridgeDependenciesNoPackageJSON(t *testing.T) {
	// A script in a dir with no package.json → dependencies check is skipped.
	root := t.TempDir()
	scriptPath := filepath.Join(root, "bridge.ts")
	if err := os.WriteFile(scriptPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	rt := bridge.BridgeRuntime{Path: scriptPath, NeedsBun: true}
	if err := bridge.EnsureBridgeDependencies(rt, "@anthropic-ai/claude-agent-sdk", "claude-agent bridge"); err != nil {
		t.Errorf("expected no error without package.json, got: %v", err)
	}
}

// §9 — readiness failure: missing dependencies

func TestEnsureBridgeDependenciesMissing(t *testing.T) {
	root := t.TempDir()
	scriptPath := filepath.Join(root, "bridge.ts")
	if err := os.WriteFile(scriptPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create package.json but no node_modules.
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	rt := bridge.BridgeRuntime{Path: scriptPath, NeedsBun: true}
	err := bridge.EnsureBridgeDependencies(rt, "@anthropic-ai/claude-agent-sdk", "claude-agent bridge")
	if err == nil {
		t.Fatal("expected error for missing dependencies, got nil")
	}
	if !strings.Contains(err.Error(), "bun install") {
		t.Errorf("error should suggest bun install: %v", err)
	}
}

func fakeBridgeFixturePath(t *testing.T) string {
	t.Helper()

	rel := filepath.Join("..", "bridge", "testdata", "fake-bridge.ts")
	abs, err := filepath.Abs(rel)
	if err != nil {
		t.Fatalf("resolve fake bridge path: %v", err)
	}
	if _, err := os.Stat(abs); os.IsNotExist(err) {
		t.Skipf("fake bridge fixture not found at %s", abs)
	}
	return abs
}

func requireBun(t *testing.T) string {
	t.Helper()

	bunPath, err := exec.LookPath("bun")
	if err != nil {
		t.Skip("bun not found in PATH, skipping fake bridge test")
	}
	return bunPath
}

func collectEventsUntil(t *testing.T, ch <-chan adapter.AgentEvent, terminalType string, ctx context.Context) []adapter.AgentEvent {
	t.Helper()

	var out []adapter.AgentEvent
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
			if ev.Type == terminalType {
				return out
			}
		case <-ctx.Done():
			t.Fatalf("timed out collecting events (wanted %q): %v", terminalType, ctx.Err())
			return nil
		}
	}
}

func requireEvent(t *testing.T, events []adapter.AgentEvent, eventType string) adapter.AgentEvent {
	t.Helper()
	for _, ev := range events {
		if ev.Type == eventType {
			return ev
		}
	}
	t.Fatalf("missing event %q in %#v", eventType, events)
	return adapter.AgentEvent{}
}

// §2,§3,§4,§5,§10 — Claude StartSession uses the shared fake bridge child process.
func TestStartSessionWithFakeBridge(t *testing.T) {
	bunPath := requireBun(t)
	bridgePath := fakeBridgeFixturePath(t)

	tmpDir := t.TempDir()
	sessionLogDir := filepath.Join(tmpDir, "sessions")
	echoPath := filepath.Join(tmpDir, "echo.json")
	worktreePath := filepath.Join(tmpDir, "worktree")
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatalf("mkdir worktree dir: %v", err)
	}

	t.Setenv("FAKE_BRIDGE_MODE", "echo_init")
	t.Setenv("FAKE_BRIDGE_ECHO_PATH", echoPath)
	t.Setenv("FAKE_BRIDGE_SESSION_ID", "claude-session-123")

	cfg := config.ClaudeCodeConfig{
		BunPath:    bunPath,
		BridgePath: bridgePath,
		Model:      "claude-test-model",
		Thinking:   "enabled",
		Effort:     "high",
	}
	harness := NewHarness(cfg, tmpDir)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	session, err := harness.StartSession(ctx, adapter.SessionOpts{
		SessionID:          "claude-wire-001",
		Mode:               adapter.SessionModeAgent,
		WorktreePath:       worktreePath,
		SessionLogDir:      sessionLogDir,
		SystemPrompt:       "You are a Claude test assistant.",
		UserPrompt:         "Say hello",
		AnswerTimeoutMs:    45000,
		QuestionToolPolicy: adapter.QuestionToolPolicyForeman,
		ResumeInfo:         map[string]string{"claude_session_id": "resume-previous"},
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	defer session.Abort(ctx) //nolint:errcheck // best-effort cleanup

	if got := session.ID(); got != "claude-wire-001" {
		t.Fatalf("session ID = %q, want claude-wire-001", got)
	}

	events := collectEventsUntil(t, session.Events(), "done", ctx)
	input := requireEvent(t, events, "input")
	if input.Payload != "Say hello" {
		t.Fatalf("input payload = %q, want Say hello", input.Payload)
	}
	text := requireEvent(t, events, "text_delta")
	if text.Payload != "echo: Say hello" {
		t.Fatalf("text_delta payload = %q, want echo output", text.Payload)
	}
	done := requireEvent(t, events, "done")
	if done.Payload != "done" {
		t.Fatalf("done payload = %q, want done", done.Payload)
	}

	if got := session.ResumeInfo()["claude_session_id"]; got != "claude-session-123" {
		t.Fatalf("ResumeInfo claude_session_id = %q, want claude-session-123", got)
	}

	var echo struct {
		Init struct {
			Type               string  `json:"type"`
			Mode               string  `json:"mode"`
			SystemPrompt       string  `json:"system_prompt"`
			ResumeSessionID    string  `json:"resume_session_id"`
			Model              *string `json:"model"`
			Thinking           *string `json:"thinking"`
			Effort             *string `json:"effort"`
			AnswerTimeoutMs    int64   `json:"answer_timeout_ms"`
			QuestionToolPolicy string  `json:"question_tool_policy"`
		} `json:"init"`
		Env       map[string]string `json:"env"`
		SessionID string            `json:"session_id"`
	}
	data, err := os.ReadFile(echoPath)
	if err != nil {
		t.Fatalf("read echo file: %v", err)
	}
	if err := json.Unmarshal(data, &echo); err != nil {
		t.Fatalf("unmarshal echo file: %v\nraw: %s", err, data)
	}
	if echo.Init.Type != "init" || echo.Init.Mode != string(adapter.SessionModeAgent) {
		t.Fatalf("init type/mode = %q/%q, want init/agent", echo.Init.Type, echo.Init.Mode)
	}
	if echo.Init.SystemPrompt != "You are a Claude test assistant." {
		t.Fatalf("system_prompt = %q", echo.Init.SystemPrompt)
	}
	if echo.Init.ResumeSessionID != "resume-previous" {
		t.Fatalf("resume_session_id = %q, want resume-previous", echo.Init.ResumeSessionID)
	}
	if echo.Init.Model == nil || *echo.Init.Model != "claude-test-model" {
		t.Fatalf("model = %v, want claude-test-model", echo.Init.Model)
	}
	if echo.Init.Thinking == nil || *echo.Init.Thinking != "enabled" {
		t.Fatalf("thinking = %v, want enabled", echo.Init.Thinking)
	}
	if echo.Init.Effort == nil || *echo.Init.Effort != "high" {
		t.Fatalf("effort = %v, want high", echo.Init.Effort)
	}
	if echo.Init.AnswerTimeoutMs != 45000 {
		t.Fatalf("answer_timeout_ms = %d, want 45000", echo.Init.AnswerTimeoutMs)
	}
	if echo.Init.QuestionToolPolicy != string(adapter.QuestionToolPolicyForeman) {
		t.Fatalf("question_tool_policy = %q, want foreman", echo.Init.QuestionToolPolicy)
	}
	if got := echo.Env["SUBSTRATE_WORKTREE_PATH"]; got != worktreePath {
		t.Fatalf("SUBSTRATE_WORKTREE_PATH = %q, want %q", got, worktreePath)
	}
	wantLogPath := filepath.Join(sessionLogDir, "claude-wire-001.log")
	if got := echo.Env["SUBSTRATE_SESSION_LOG_PATH"]; got != wantLogPath {
		t.Fatalf("SUBSTRATE_SESSION_LOG_PATH = %q, want %q", got, wantLogPath)
	}
	logData, err := os.ReadFile(wantLogPath)
	if err != nil {
		t.Fatalf("read session log: %v", err)
	}
	if !strings.Contains(string(logData), "echo: Say hello") {
		t.Fatalf("session log missing canonical assistant output: %s", logData)
	}
}

// §6,§7 — Claude shared bridge supports follow-up messaging, answer, steer, and compact.
func TestStartSessionWithFakeBridgeControls(t *testing.T) {
	bunPath := requireBun(t)
	bridgePath := fakeBridgeFixturePath(t)
	tmpDir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	session, err := NewHarness(config.ClaudeCodeConfig{BunPath: bunPath, BridgePath: bridgePath}, tmpDir).StartSession(ctx, adapter.SessionOpts{
		SessionID:     "claude-controls-001",
		Mode:          adapter.SessionModeAgent,
		WorktreePath:  tmpDir,
		SessionLogDir: filepath.Join(tmpDir, "sessions"),
		UserPrompt:    "first",
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	defer session.Abort(ctx) //nolint:errcheck // best-effort cleanup

	collectEventsUntil(t, session.Events(), "done", ctx)

	if err := session.SendMessage(ctx, "second"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	messageEvents := collectEventsUntil(t, session.Events(), "done", ctx)
	if ev := requireEvent(t, messageEvents, "text_delta"); ev.Payload != "followup: second" {
		t.Fatalf("follow-up payload = %q, want followup: second", ev.Payload)
	}

	if err := session.SendAnswer(ctx, "operator answer"); err != nil {
		t.Fatalf("SendAnswer: %v", err)
	}
	answerDone := collectEventsUntil(t, session.Events(), "done", ctx)
	if ev := requireEvent(t, answerDone, "done"); ev.Payload != "answer_received" {
		t.Fatalf("answer done payload = %q, want answer_received", ev.Payload)
	}

	if err := session.Steer(ctx, "change direction"); err != nil {
		t.Fatalf("Steer: %v", err)
	}
	steerEvents := collectEventsUntil(t, session.Events(), "done", ctx)
	if ev := requireEvent(t, steerEvents, "text_delta"); ev.Payload != "steer: change direction" {
		t.Fatalf("steer payload = %q, want steer output", ev.Payload)
	}

	if err := session.Compact(ctx); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	compactEvents := collectEventsUntil(t, session.Events(), "compaction_end", ctx)
	requireEvent(t, compactEvents, "compaction_start")
	requireEvent(t, compactEvents, "compaction_end")
}

// §8 — abort closes the bridge event stream and process resources.
func TestStartSessionWithFakeBridgeAbortClosesEvents(t *testing.T) {
	bunPath := requireBun(t)
	bridgePath := fakeBridgeFixturePath(t)
	tmpDir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	session, err := NewHarness(config.ClaudeCodeConfig{BunPath: bunPath, BridgePath: bridgePath}, tmpDir).StartSession(ctx, adapter.SessionOpts{
		SessionID:    "claude-abort-001",
		Mode:         adapter.SessionModeForeman,
		WorktreePath: tmpDir,
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	if err := session.Abort(ctx); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	select {
	case <-session.Done():
	case <-ctx.Done():
		t.Fatalf("session Done did not close: %v", ctx.Err())
	}
	if _, ok := <-session.Events(); ok {
		t.Fatal("events channel should be closed after abort")
	}
}

// §9 — malformed and non-zero bridge exits surface useful errors without hanging.
func TestStartSessionWithFakeBridgeFailureModes(t *testing.T) {
	bunPath := requireBun(t)
	bridgePath := fakeBridgeFixturePath(t)
	for _, tc := range []struct {
		name    string
		mode    string
		wantErr bool
	}{
		{name: "malformed", mode: "malformed"},
		{name: "nonzero_exit", mode: "nonzero_exit", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ERROR_MODE", tc.mode)
			tmpDir := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			session, err := NewHarness(config.ClaudeCodeConfig{BunPath: bunPath, BridgePath: bridgePath}, tmpDir).StartSession(ctx, adapter.SessionOpts{
				SessionID:    "claude-failure-" + tc.name,
				Mode:         adapter.SessionModeForeman,
				WorktreePath: tmpDir,
			})
			if err != nil {
				if !strings.Contains(err.Error(), "send init message") && !strings.Contains(err.Error(), "broken pipe") {
					t.Fatalf("StartSession error = %v, want init/write failure", err)
				}
				return
			}
			err = session.Wait(ctx)
			if tc.wantErr {
				if err == nil {
					t.Fatal("Wait() error = nil, want bridge exit error")
				}
				if !strings.Contains(err.Error(), "bridge subprocess exited") && !errors.Is(err, context.Canceled) {
					t.Fatalf("Wait error = %v, want bridge subprocess exit", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Wait() error = %v, want nil for clean malformed-output exit", err)
			}
			if _, ok := <-session.Events(); ok {
				t.Fatal("events channel should be closed after malformed-output exit")
			}
		})
	}
}
