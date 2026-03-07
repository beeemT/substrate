package app

import (
	"testing"

	"github.com/beeemT/substrate/internal/config"
)

func TestBuildAgentHarnesses_DefaultsToClaudeAndCodexFallback(t *testing.T) {
	cfg := &config.Config{}
	cfg.Harness.Default = config.HarnessOhMyPi
	cfg.Harness.Phase.Planning = config.HarnessClaudeCode
	cfg.Harness.Phase.Implementation = config.HarnessClaudeCode
	cfg.Harness.Phase.Review = config.HarnessClaudeCode
	cfg.Harness.Phase.Foreman = config.HarnessClaudeCode
	cfg.Harness.Fallback = []config.HarnessName{config.HarnessCodex}
	cfg.Adapters.ClaudeCode.BinaryPath = "/bin/sh"
	cfg.Adapters.Codex.BinaryPath = "/bin/sh"
	cfg.Adapters.OhMyPi.BunPath = "/bin/sh"

	harnesses, err := BuildAgentHarnesses(cfg, "/tmp")
	if err != nil {
		t.Fatalf("BuildAgentHarnesses() error = %v", err)
	}
	if got := harnesses.Planning.Name(); got != "claude-code" {
		t.Fatalf("planning harness = %q, want claude-code", got)
	}
	if got := harnesses.Resume.Name(); got != "claude-code" {
		t.Fatalf("resume harness = %q, want claude-code", got)
	}
}

func TestBuildAgentHarnesses_FallsBackWhenPrimaryMissing(t *testing.T) {
	cfg := &config.Config{}
	cfg.Harness.Default = config.HarnessClaudeCode
	cfg.Harness.Phase.Planning = config.HarnessClaudeCode
	cfg.Harness.Phase.Implementation = config.HarnessClaudeCode
	cfg.Harness.Phase.Review = config.HarnessClaudeCode
	cfg.Harness.Phase.Foreman = config.HarnessClaudeCode
	cfg.Harness.Fallback = []config.HarnessName{config.HarnessCodex}
	cfg.Adapters.ClaudeCode.BinaryPath = "/definitely/missing/claude"
	cfg.Adapters.Codex.BinaryPath = "/bin/sh"

	harnesses, err := BuildAgentHarnesses(cfg, "/tmp")
	if err != nil {
		t.Fatalf("BuildAgentHarnesses() error = %v", err)
	}
	if got := harnesses.Planning.Name(); got != "codex" {
		t.Fatalf("planning harness = %q, want codex fallback", got)
	}
}
