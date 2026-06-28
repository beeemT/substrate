package omp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/adapter/bridge"
	"github.com/beeemT/substrate/internal/config"
)

// Stage 1 harness-contract checklist — OMP adapter:
//   §1  config/readiness path: NewHarness, bridge resolution, dependency checks
//   §2  stable session ID / logs use Substrate session ID
//   §3  initial prompt reaches child boundary
//   §4  assistant output → text_delta
//   §5  terminal success → done
//   §6  follow-up messaging (SupportsMessaging = true)
//   §7  SendAnswer / Steer / Compact work or documented unsupported
//   §8  abort closes event stream and process resources
//   §9  readiness/startup/malformed failures → useful errors
//   §10 canonical assistant log output review-parseable

// §1 — config/readiness path: packaged bridge resolution

func TestResolveBridgeRuntimeFromUsesPackagedBridge(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	execPath := filepath.Join(root, "bin", "substrate")
	bridgePath := filepath.Join(root, "share", "substrate", "bridge", "omp-bridge")

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

	rt, err := bridge.ResolveBridgeRuntimeFrom("", execPath, "omp-bridge", "ohmypi bridge")
	if err != nil {
		t.Fatalf("resolveBridgeRuntimeFrom() error = %v", err)
	}
	if rt.Path != bridgePath {
		t.Fatalf("Path = %q, want %q", rt.Path, bridgePath)
	}
	if rt.NeedsBun {
		t.Fatal("NeedsBun = true, want false for packaged bridge binary")
	}
	if got := rt.LaunchDir("/workspace"); got != "/workspace" {
		t.Fatalf("LaunchDir() = %q, want /workspace", got)
	}
}

// §1 — config/readiness path: legacy bridge override via pkgshare

func TestResolveBridgeRuntimeFromHonorsLegacyRelativeOverrideViaPkgshare(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	execPath := filepath.Join(root, "bin", "substrate")
	bridgePath := filepath.Join(root, "share", "substrate", "bridge", "omp-bridge.ts")

	if err := os.MkdirAll(filepath.Dir(execPath), 0o755); err != nil {
		t.Fatalf("mkdir executable dir: %v", err)
	}
	if err := os.WriteFile(execPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(bridgePath), 0o755); err != nil {
		t.Fatalf("mkdir bridge dir: %v", err)
	}
	if err := os.WriteFile(bridgePath, []byte("console.log('bridge');\n"), 0o644); err != nil {
		t.Fatalf("write bridge: %v", err)
	}

	rt, err := bridge.ResolveBridgeRuntimeFrom("bridge/omp-bridge.ts", execPath, "omp-bridge", "ohmypi bridge")
	if err != nil {
		t.Fatalf("resolveBridgeRuntimeFrom() error = %v", err)
	}
	if rt.Path != bridgePath {
		t.Fatalf("Path = %q, want %q", rt.Path, bridgePath)
	}
	if !rt.NeedsBun {
		t.Fatal("NeedsBun = false, want true for source bridge script")
	}
	if got := rt.LaunchDir("/workspace"); got != filepath.Dir(bridgePath) {
		t.Fatalf("LaunchDir() = %q, want %q", got, filepath.Dir(bridgePath))
	}
}

// §9 — readiness failure: source bridge without installed node_modules

func TestEnsureBridgeDependenciesRequiresInstalledNodeModulesForSourceBridge(t *testing.T) {
	t.Parallel()

	runtimeDir := t.TempDir()
	bridgePath := filepath.Join(runtimeDir, "omp-bridge.ts")

	if err := os.WriteFile(bridgePath, []byte("console.log('bridge');\n"), 0o644); err != nil {
		t.Fatalf("write bridge: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "package.json"), []byte(`{"name":"bridge","private":true}`), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}

	err := bridge.EnsureBridgeDependencies(bridge.BridgeRuntime{Path: bridgePath, NeedsBun: true}, "@oh-my-pi/pi-coding-agent", "ohmypi bridge")
	if err == nil {
		t.Fatal("ensureBridgeDependencies() error = nil, want missing dependency error")
	}
}

// §1 — config/readiness path: NewHarness from config

func TestNewHarness(t *testing.T) {
	cfg := config.OhMyPiConfig{
		BunPath:       "/usr/local/bin/bun",
		BridgePath:    "/path/to/bridge.ts",
		ThinkingLevel: "high",
	}
	workspaceRoot := "/workspace"

	h := NewHarness(cfg, workspaceRoot)

	if h == nil {
		t.Fatal("expected non-nil harness")
	}

	if h.Name() != "omp" {
		t.Errorf("Name mismatch: got %q, want %q", h.Name(), "omp")
	}

	caps := h.Capabilities()
	if !caps.SupportsStreaming {
		t.Error("expected SupportsStreaming to be true")
	}
	if !caps.SupportsMessaging {
		t.Error("expected SupportsMessaging to be true")
	}

	expectedTools := []string{"read", "grep", "find", "edit", "write", "bash", "ask", "ask_foreman"}
	if len(caps.SupportedTools) != len(expectedTools) {
		t.Errorf("SupportedTools count mismatch: got %d, want %d", len(caps.SupportedTools), len(expectedTools))
	}
}

// fakeBridgeFixturePath returns the absolute path to the fake-bridge.ts test
// fixture. The test is skipped when the fixture has not been generated yet.
func fakeBridgeFixturePath(t *testing.T) string {
	t.Helper()

	// When `go test` runs the working directory is the package directory
	// (internal/adapter/ohmypi), so navigate to the sibling testdata dir.
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

// requireBun skips the test when bun is not available in PATH.
func requireBun(t *testing.T) string {
	t.Helper()

	bunPath, err := exec.LookPath("bun")
	if err != nil {
		t.Skip("bun not found in PATH, skipping fake bridge test")
	}
	return bunPath
}

// collectEventsDraining reads from ch until it sees an event whose Type
// matches terminalType or the context is cancelled. It returns every event
// collected (including the terminal one).
func collectEventsDraining(t *testing.T, ch <-chan adapter.AgentEvent, terminalType string, ctx context.Context) []adapter.AgentEvent {
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
			return nil // unreachable
		}
	}
}

// §2,§3,§4,§5,§10 — TestStartSessionWithFakeBridge verifies the full wiring through
// OhMyPiHarness.StartSession → bridge subprocess → JSON-line event stream.
//
// It asserts:
//   - §2: StartSession returns a non-nil session with the requested ID.
//   - §3: The initial prompt reaches the bridge subprocess.
//   - §4,§5: The fake bridge emits text_delta and done events.
//   - §10: A session log file is created with review-parseable output.
func TestStartSessionWithFakeBridge(t *testing.T) {
	t.Parallel()

	bunPath := requireBun(t)
	bridgePath := fakeBridgeFixturePath(t)

	tmpDir := t.TempDir()
	sessionLogDir := filepath.Join(tmpDir, "sessions")
	if err := os.MkdirAll(sessionLogDir, 0o755); err != nil {
		t.Fatalf("mkdir session log dir: %v", err)
	}

	cfg := config.OhMyPiConfig{
		BunPath:       bunPath,
		BridgePath:    bridgePath,
		ThinkingLevel: "medium",
	}
	harness := NewHarness(cfg, tmpDir)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	session, err := harness.StartSession(ctx, adapter.SessionOpts{
		SessionID:     "test-harness-wire-001",
		Mode:          adapter.SessionModeAgent,
		WorktreePath:  tmpDir,
		SessionLogDir: sessionLogDir,
		SystemPrompt:  "You are a test assistant.",
		UserPrompt:    "Say hello",
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	defer session.Abort(ctx) //nolint:errcheck // best-effort cleanup

	if got := session.ID(); got != "test-harness-wire-001" {
		t.Errorf("session ID = %q, want %q", got, "test-harness-wire-001")
	}

	events := collectEventsDraining(t, session.Events(), "done", ctx)

	var hasInput, hasTextDelta, hasDone bool
	for _, ev := range events {
		switch ev.Type {
		case "input":
			hasInput = true
		case "text_delta":
			hasTextDelta = true
		case "done":
			hasDone = true
		}
	}
	if !hasInput {
		t.Error("expected at least one input event from fake bridge")
	}
	if !hasTextDelta {
		t.Error("expected at least one text_delta event from fake bridge")
	}
	if !hasDone {
		t.Error("expected done event from fake bridge")
	}

	// Verify session log was created.
	logPath := filepath.Join(sessionLogDir, "test-harness-wire-001.log")
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Errorf("session log not created at %s", logPath)
	}
}

// §3 — initial prompt and config reach the child boundary

// TestStartSessionConfigReachesFakeBridge verifies that environment variables
// and init-message fields propagated by StartSession actually reach the bridge
// subprocess. The fake bridge (in echo_init mode) writes the received init and
// env values to a JSON file that this test inspects.
//
// Assertions:
//   - init.system_prompt matches opts.SystemPrompt.
//   - init.answer_timeout_ms matches opts.AnswerTimeoutMs.
//   - init.question_tool_policy matches opts.QuestionToolPolicy.
//   - init.model is set from cfg.Model when opts.Model is nil.
//   - SUBSTRATE_BRIDGE_MODE equals opts.Mode.
//   - SUBSTRATE_WORKTREE_PATH equals opts.WorktreePath.
//   - SUBSTRATE_THINKING_LEVEL equals cfg.ThinkingLevel.
func TestStartSessionConfigReachesFakeBridge(t *testing.T) {
	// Cannot use t.Parallel: t.Setenv mutates process-global state.

	bunPath := requireBun(t)
	bridgePath := fakeBridgeFixturePath(t)

	tmpDir := t.TempDir()
	sessionLogDir := filepath.Join(tmpDir, "sessions")
	if err := os.MkdirAll(sessionLogDir, 0o755); err != nil {
		t.Fatalf("mkdir session log dir: %v", err)
	}

	// Direct the fake bridge to echo the init message and env to a file.
	echoPath := filepath.Join(tmpDir, "echo.json")
	t.Setenv("FAKE_BRIDGE_MODE", "echo_init")
	t.Setenv("FAKE_BRIDGE_ECHO_PATH", echoPath)

	model := "test/model-1"
	cfg := config.OhMyPiConfig{
		BunPath:       bunPath,
		BridgePath:    bridgePath,
		ThinkingLevel: "high",
		Model:         model,
	}
	harness := NewHarness(cfg, tmpDir)

	worktreePath := filepath.Join(tmpDir, "worktree")
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatalf("mkdir worktree dir: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	session, err := harness.StartSession(ctx, adapter.SessionOpts{
		SessionID:          "test-harness-wire-002",
		Mode:               adapter.SessionModeAgent,
		WorktreePath:       worktreePath,
		SessionLogDir:      sessionLogDir,
		SystemPrompt:       "test system prompt for echo",
		UserPrompt:         "echo test",
		AnswerTimeoutMs:    60000,
		QuestionToolPolicy: adapter.QuestionToolPolicyForeman,
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	defer session.Abort(ctx) //nolint:errcheck // best-effort cleanup

	// Wait until the fake bridge has processed the prompt (which means
	// init was processed first and the echo file has been written).
	_ = collectEventsDraining(t, session.Events(), "done", ctx)

	echoData, err := os.ReadFile(echoPath)
	if err != nil {
		t.Fatalf("read echo file %s: %v", echoPath, err)
	}

	var echo struct {
		Init struct {
			Type               string  `json:"type"`
			SystemPrompt       string  `json:"system_prompt"`
			AnswerTimeoutMs    int64   `json:"answer_timeout_ms"`
			QuestionToolPolicy string  `json:"question_tool_policy"`
			Model              *string `json:"model"`
		} `json:"init"`
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(echoData, &echo); err != nil {
		t.Fatalf("unmarshal echo data: %v\nraw: %s", err, string(echoData))
	}

	// --- init message assertions ---

	if echo.Init.Type != "init" {
		t.Errorf("init.type = %q, want %q", echo.Init.Type, "init")
	}
	if echo.Init.SystemPrompt != "test system prompt for echo" {
		t.Errorf("init.system_prompt = %q, want %q", echo.Init.SystemPrompt, "test system prompt for echo")
	}
	if echo.Init.AnswerTimeoutMs != 60000 {
		t.Errorf("init.answer_timeout_ms = %d, want %d", echo.Init.AnswerTimeoutMs, 60000)
	}
	wantPolicy := string(adapter.QuestionToolPolicyForeman)
	if echo.Init.QuestionToolPolicy != wantPolicy {
		t.Errorf("init.question_tool_policy = %q, want %q", echo.Init.QuestionToolPolicy, wantPolicy)
	}
	if echo.Init.Model == nil || *echo.Init.Model != model {
		t.Errorf("init.model = %v, want %q", echo.Init.Model, model)
	}

	// --- environment variable assertions ---

	if got := echo.Env["SUBSTRATE_BRIDGE_MODE"]; got != string(adapter.SessionModeAgent) {
		t.Errorf("SUBSTRATE_BRIDGE_MODE = %q, want %q", got, string(adapter.SessionModeAgent))
	}
	if got := echo.Env["SUBSTRATE_WORKTREE_PATH"]; got != worktreePath {
		t.Errorf("SUBSTRATE_WORKTREE_PATH = %q, want %q", got, worktreePath)
	}
	if got := echo.Env["SUBSTRATE_THINKING_LEVEL"]; got != "high" {
		t.Errorf("SUBSTRATE_THINKING_LEVEL = %q, want %q", got, "high")
	}
}
