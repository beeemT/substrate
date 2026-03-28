package omp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/beeemT/substrate/internal/adapter/bridge"
	"github.com/beeemT/substrate/internal/config"
)

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

	expectedTools := []string{"read", "grep", "find", "edit", "write", "bash", "ask_foreman"}
	if len(caps.SupportedTools) != len(expectedTools) {
		t.Errorf("SupportedTools count mismatch: got %d, want %d", len(caps.SupportedTools), len(expectedTools))
	}
}
