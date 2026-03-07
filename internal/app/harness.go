package app

import (
	"fmt"
	"os/exec"

	"github.com/beeemT/substrate/internal/adapter"
	claudecode "github.com/beeemT/substrate/internal/adapter/claudecode"
	codexadapter "github.com/beeemT/substrate/internal/adapter/codex"
	omp "github.com/beeemT/substrate/internal/adapter/ohmypi"
	"github.com/beeemT/substrate/internal/config"
)

type AgentHarnesses struct {
	Planning       adapter.AgentHarness
	Implementation adapter.AgentHarness
	Review         adapter.AgentHarness
	Foreman        adapter.AgentHarness
	Resume         adapter.AgentHarness
}

func BuildAgentHarnesses(cfg *config.Config, workspaceRoot string) (AgentHarnesses, error) {
	planning, err := buildAgentHarness(cfg, cfg.Harness.Phase.Planning, workspaceRoot)
	if err != nil {
		return AgentHarnesses{}, fmt.Errorf("planning harness: %w", err)
	}
	implementation, err := buildAgentHarness(cfg, cfg.Harness.Phase.Implementation, workspaceRoot)
	if err != nil {
		return AgentHarnesses{}, fmt.Errorf("implementation harness: %w", err)
	}
	review, err := buildAgentHarness(cfg, cfg.Harness.Phase.Review, workspaceRoot)
	if err != nil {
		return AgentHarnesses{}, fmt.Errorf("review harness: %w", err)
	}
	foreman, err := buildAgentHarness(cfg, cfg.Harness.Phase.Foreman, workspaceRoot)
	if err != nil {
		return AgentHarnesses{}, fmt.Errorf("foreman harness: %w", err)
	}
	return AgentHarnesses{
		Planning:       planning,
		Implementation: implementation,
		Review:         review,
		Foreman:        foreman,
		Resume:         implementation,
	}, nil
}

func buildAgentHarness(cfg *config.Config, name config.HarnessName, workspaceRoot string) (adapter.AgentHarness, error) {
	candidates := append([]config.HarnessName{name}, cfg.Harness.Fallback...)
	var lastErr error
	for _, candidate := range candidates {
		harness, err := instantiateHarness(cfg, candidate, workspaceRoot)
		if err == nil {
			return harness, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no harness candidates configured")
	}
	return nil, lastErr
}

func instantiateHarness(cfg *config.Config, name config.HarnessName, workspaceRoot string) (adapter.AgentHarness, error) {
	switch name {
	case config.HarnessOhMyPi:
		if cfg.Adapters.OhMyPi.BunPath != "" {
			if _, err := exec.LookPath(cfg.Adapters.OhMyPi.BunPath); err != nil {
				return nil, fmt.Errorf("ohmypi bun_path %q not found: %w", cfg.Adapters.OhMyPi.BunPath, err)
			}
		}
		return omp.NewHarness(cfg.Adapters.OhMyPi, workspaceRoot), nil
	case config.HarnessClaudeCode:
		binary := cfg.Adapters.ClaudeCode.BinaryPath
		if binary == "" {
			binary = "claude"
		}
		if _, err := exec.LookPath(binary); err != nil {
			return nil, fmt.Errorf("claude-code binary %q not found: %w", binary, err)
		}
		return claudecode.NewHarness(cfg.Adapters.ClaudeCode), nil
	case config.HarnessCodex:
		binary := cfg.Adapters.Codex.BinaryPath
		if binary == "" {
			binary = "codex"
		}
		if _, err := exec.LookPath(binary); err != nil {
			return nil, fmt.Errorf("codex binary %q not found: %w", binary, err)
		}
		return codexadapter.NewHarness(cfg.Adapters.Codex), nil
	default:
		return nil, fmt.Errorf("unsupported harness: %s", name)
	}
}
