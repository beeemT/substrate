package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beeemT/substrate/internal/config"
)

func newHarnessConfig(primary config.HarnessName) *config.Config {
	cfg := &config.Config{}
	cfg.Harness.Phase.Planning = primary
	cfg.Harness.Phase.Implementation = primary
	cfg.Harness.Phase.Review = primary
	cfg.Harness.Phase.Foreman = primary
	cfg.Harness.Fallback = []config.HarnessName{config.HarnessCodex}
	cfg.Adapters.Codex.BinaryPath = "/bin/sh"
	return cfg
}

func writeTestFile(t *testing.T, path, contents string, mode os.FileMode) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writePackagedBridge(t *testing.T) string {
	t.Helper()

	bridgePath := filepath.Join(t.TempDir(), "omp-bridge")
	writeTestFile(t, bridgePath, "#!/bin/sh\n", 0o755)
	return bridgePath
}

func writeSourceBridge(t *testing.T) string {
	t.Helper()

	runtimeDir := t.TempDir()
	bridgePath := filepath.Join(runtimeDir, "omp-bridge.ts")
	writeTestFile(t, bridgePath, "console.log('bridge');\n", 0o644)
	writeTestFile(t, filepath.Join(runtimeDir, "package.json"), `{"name":"bridge","private":true}`, 0o644)
	if err := os.MkdirAll(filepath.Join(runtimeDir, "node_modules", "@oh-my-pi", "pi-coding-agent"), 0o755); err != nil {
		t.Fatalf("mkdir bridge dependencies: %v", err)
	}
	return bridgePath
}

func TestBuildAgentHarnesses_FallsBackWhenOhMyPiBridgeUnavailable(t *testing.T) {
	cfg := newHarnessConfig(config.HarnessOhMyPi)
	cfg.Adapters.OhMyPi.BridgePath = filepath.Join(t.TempDir(), "missing-bridge")

	harnesses, err := BuildAgentHarnesses(cfg, "/tmp")
	if err != nil {
		t.Fatalf("BuildAgentHarnesses() error = %v", err)
	}
	if got := harnesses.Planning.Name(); got != "codex" {
		t.Fatalf("planning harness = %q, want codex fallback", got)
	}
	if got := harnesses.Resume.Name(); got != "codex" {
		t.Fatalf("resume harness = %q, want codex fallback", got)
	}
}

func TestBuildAgentHarnesses_FallsBackWhenOhMyPiBunOverrideMissing(t *testing.T) {
	cfg := newHarnessConfig(config.HarnessOhMyPi)
	cfg.Adapters.OhMyPi.BridgePath = writeSourceBridge(t)
	cfg.Adapters.OhMyPi.BunPath = filepath.Join(t.TempDir(), "missing-bun")

	harnesses, err := BuildAgentHarnesses(cfg, "/tmp")
	if err != nil {
		t.Fatalf("BuildAgentHarnesses() error = %v", err)
	}
	if got := harnesses.Planning.Name(); got != "codex" {
		t.Fatalf("planning harness = %q, want codex fallback", got)
	}
}

func TestBuildAgentHarnesses_UsesOhMyPiWhenPackagedBridgeReady(t *testing.T) {
	cfg := newHarnessConfig(config.HarnessOhMyPi)
	cfg.Adapters.OhMyPi.BridgePath = writePackagedBridge(t)

	harnesses, err := BuildAgentHarnesses(cfg, "/tmp")
	if err != nil {
		t.Fatalf("BuildAgentHarnesses() error = %v", err)
	}
	if got := harnesses.Planning.Name(); got != "omp" {
		t.Fatalf("planning harness = %q, want omp", got)
	}
	if got := harnesses.Resume.Name(); got != "omp" {
		t.Fatalf("resume harness = %q, want omp", got)
	}
}

func TestBuildAgentHarnesses_UsesOhMyPiWhenSourceBridgeAndBunReady(t *testing.T) {
	cfg := newHarnessConfig(config.HarnessOhMyPi)
	cfg.Adapters.OhMyPi.BridgePath = writeSourceBridge(t)
	cfg.Adapters.OhMyPi.BunPath = "/bin/sh"

	harnesses, err := BuildAgentHarnesses(cfg, "/tmp")
	if err != nil {
		t.Fatalf("BuildAgentHarnesses() error = %v", err)
	}
	if got := harnesses.Planning.Name(); got != "omp" {
		t.Fatalf("planning harness = %q, want omp", got)
	}
}

func TestBuildAgentHarnesses_DoesNotBlockWhenHarnessBinaryMissing(t *testing.T) {
	cfg := newHarnessConfig(config.HarnessCodex)
	cfg.Harness.Fallback = nil
	cfg.Adapters.Codex.BinaryPath = filepath.Join(t.TempDir(), "missing-codex")

	harnesses, err := BuildAgentHarnesses(cfg, "/tmp")
	if err != nil {
		t.Fatalf("BuildAgentHarnesses() error = %v", err)
	}
	if harnesses.Planning != nil {
		t.Fatalf("planning harness = %v, want nil when codex is unavailable", harnesses.Planning)
	}
	if harnesses.Resume != nil {
		t.Fatalf("resume harness = %v, want nil when implementation harness is unavailable", harnesses.Resume)
	}

	diagnostics := DiagnoseHarnesses(cfg, "/tmp")
	if !diagnostics.HasWarnings() {
		t.Fatal("expected harness diagnostics to report warnings")
	}
	if warnings := diagnostics.PhaseWarnings(); len(warnings) != 4 {
		t.Fatalf("phase warnings = %d, want 4", len(warnings))
	}
	if summary := diagnostics.WarningSummary(); !strings.Contains(summary, "Some harnesses are unavailable") {
		t.Fatalf("warning summary = %q, want aggregated warning", summary)
	}
	if warning := diagnostics.PhaseWarnings()[0]; !strings.Contains(warning, "Codex CLI") || !strings.Contains(warning, "Binary Path") {
		t.Fatalf("planning warning = %q, want concise codex guidance", warning)
	}
}

func TestDiagnoseHarnesses_SummarizesOhMyPiBridgeLookupForUsers(t *testing.T) {
	cfg := newHarnessConfig(config.HarnessOhMyPi)
	cfg.Harness.Fallback = nil
	cfg.Adapters.OhMyPi.BridgePath = filepath.Join(t.TempDir(), "missing-bridge")

	diagnostics := DiagnoseHarnesses(cfg, "/tmp")
	warning := diagnostics.PhaseWarnings()[0]
	if !strings.Contains(warning, "Oh My Pi bridge not found") {
		t.Fatalf("warning = %q, want concise bridge guidance", warning)
	}
	if strings.Contains(warning, "checked ") {
		t.Fatalf("warning = %q, want concise message without checked path dump", warning)
	}
}
