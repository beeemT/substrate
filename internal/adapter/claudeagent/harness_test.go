package claudeagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/beeemT/substrate/internal/adapter/bridge"
	"github.com/beeemT/substrate/internal/config"
)

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
}

func TestInitMessageSerialization(t *testing.T) {
	msg := bridgeInitMsg{
		Type:         "init",
		Mode:         "agent",
		SystemPrompt: "sys",
		Model:        "claude-3",
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
