package app

import (
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/beeemT/substrate/internal/adapter"
	claudeagent "github.com/beeemT/substrate/internal/adapter/claudeagent"
	codexadapter "github.com/beeemT/substrate/internal/adapter/codex"
	omp "github.com/beeemT/substrate/internal/adapter/ohmypi"
	opencodeadapter "github.com/beeemT/substrate/internal/adapter/opencode"
	"github.com/beeemT/substrate/internal/config"
)

type AgentHarnesses struct {
	Planning       adapter.AgentHarness
	Implementation adapter.AgentHarness
	Review         adapter.AgentHarness
	Foreman        adapter.AgentHarness
	Resume         adapter.AgentHarness
}

type HarnessCandidateFailure struct {
	Harness config.HarnessName
	Message string
}

func (f HarnessCandidateFailure) SettingsReason() string {
	return settingsHarnessFailureReason(f.Harness, f.Message)
}

type HarnessPhaseDiagnostic struct {
	Phase     string
	Available bool
	Failures  []HarnessCandidateFailure
}

type settingsWarningGroup struct {
	Harness config.HarnessName
	Reason  string
	Phases  []string
}

type HarnessDiagnostics struct {
	Phases []HarnessPhaseDiagnostic
}

func (d HarnessDiagnostics) HasWarnings() bool {
	for _, phase := range d.Phases {
		if len(phase.Failures) > 0 {
			return true
		}
	}
	return false
}

func (d HarnessDiagnostics) WarningSummary() string {
	if !d.HasWarnings() {
		return ""
	}
	return "Harness unavailable. Check Harness Routing."
}

func (d HarnessDiagnostics) PhaseWarnings() []string {
	groups := d.warningGroups()
	warnings := make([]string, 0, len(groups))
	for _, group := range groups {
		warnings = append(warnings, formatGroupedPhaseWarning(group.Phases, group.Reason))
	}
	return warnings
}

func (d HarnessDiagnostics) HarnessWarnings() map[config.HarnessName][]string {
	warnings := make(map[config.HarnessName][]string)
	for _, group := range d.warningGroups() {
		warnings[group.Harness] = append(warnings[group.Harness], formatGroupedPhaseWarning(group.Phases, group.Reason))
	}
	return warnings
}

func (d HarnessDiagnostics) warningGroups() []settingsWarningGroup {
	groups := make([]settingsWarningGroup, 0, len(d.Phases))
	indexes := make(map[string]int, len(d.Phases))
	for _, phase := range d.Phases {
		if len(phase.Failures) == 0 {
			continue
		}
		failure := phase.Failures[0]
		reason := failure.SettingsReason()
		phaseLabel := phase.Phase
		key := string(failure.Harness) + "\x00" + reason
		if idx, ok := indexes[key]; ok {
			groups[idx].Phases = append(groups[idx].Phases, phaseLabel)
			continue
		}
		indexes[key] = len(groups)
		groups = append(groups, settingsWarningGroup{
			Harness: failure.Harness,
			Reason:  reason,
			Phases:  []string{phaseLabel},
		})
	}
	return groups
}

func formatGroupedPhaseWarning(phases []string, reason string) string {
	return strings.Join(phases, ", ") + ": " + reason + "."
}

func settingsHarnessFailureReason(harness config.HarnessName, message string) string {
	message = strings.TrimSpace(message)
	switch harness {
	case config.HarnessOhMyPi:
		return settingsOhMyPiFailureReason(message)
	case config.HarnessClaudeCode:
		return settingsClaudeAgentFailureReason(message)
	case config.HarnessCodex:
		return settingsBinaryFailureReason("Codex", "codex", message)
	case config.HarnessOpenCode:
		return settingsBinaryFailureReason("OpenCode", "opencode", message)
	default:
		return message
	}
}

func settingsOhMyPiFailureReason(message string) string {
	detail := strings.TrimSpace(strings.TrimPrefix(message, "ohmypi unavailable:"))
	switch {
	case strings.HasPrefix(detail, "resolve ohmypi bridge: no bridge binary or script found"):
		return "Oh My Pi bridge not found"
	case strings.HasPrefix(detail, "resolve bun "):
		return "Bun not found for Oh My Pi"
	case strings.Contains(detail, "source bridge dependencies missing under "):
		return "Oh My Pi bridge dependencies missing"
	case strings.Contains(detail, "check bridge package metadata"):
		return "Oh My Pi bridge path unreadable"
	default:
		return "Oh My Pi unavailable"
	}
}

func settingsClaudeAgentFailureReason(message string) string {
	detail := strings.TrimSpace(strings.TrimPrefix(message, "claude-agent unavailable:"))
	switch {
	case strings.HasPrefix(detail, "resolve claude-agent bridge: no bridge binary or script found"):
		return "Claude agent bridge not found"
	case strings.HasPrefix(detail, "resolve bun "):
		return "Bun not found for Claude agent"
	case strings.Contains(detail, "claude-agent bridge dependencies missing under "):
		return "Claude agent bridge dependencies missing"
	case strings.Contains(detail, "check bridge package metadata"):
		return "Claude agent bridge path unreadable"
	default:
		return "Claude agent unavailable"
	}
}

func settingsBinaryFailureReason(name string, defaultBinary string, message string) string {
	binary, ok := extractQuotedValue(message)
	if !ok {
		return name + " unavailable"
	}
	if binary == defaultBinary {
		return name + " not found"
	}
	return name + " binary not found"
}

func extractQuotedValue(message string) (string, bool) {
	start := strings.Index(message, `"`)
	if start == -1 {
		return "", false
	}
	end := strings.Index(message[start+1:], `"`)
	if end == -1 {
		return "", false
	}
	return message[start+1 : start+1+end], true
}

func DiagnoseHarnesses(cfg *config.Config, workspaceRoot string) HarnessDiagnostics {
	if cfg == nil {
		return HarnessDiagnostics{}
	}
	resolved := resolveHarnessPhase(cfg, "Harness", cfg.Harness.Default, workspaceRoot)
	return HarnessDiagnostics{Phases: []HarnessPhaseDiagnostic{resolved.diagnostic}}
}

func BuildAgentHarnesses(cfg *config.Config, workspaceRoot string) (AgentHarnesses, error) {
	if cfg == nil {
		return AgentHarnesses{}, errors.New("config is nil")
	}
	resolved := resolveHarnessPhase(cfg, "Harness", cfg.Harness.Default, workspaceRoot)
	if len(resolved.diagnostic.Failures) > 0 {
		f := resolved.diagnostic.Failures[0]
		slog.Warn("harness unavailable", "harness", f.Harness, "error", f.Message)
	}
	return AgentHarnesses{
		Planning:       resolved.harness,
		Implementation: resolved.harness,
		Review:         resolved.harness,
		Foreman:        resolved.harness,
		Resume:         resolved.harness,
	}, nil
}

type resolvedHarnessPhase struct {
	harness    adapter.AgentHarness
	diagnostic HarnessPhaseDiagnostic
}

func resolveHarnessPhase(cfg *config.Config, phase string, name config.HarnessName, workspaceRoot string) resolvedHarnessPhase {
	diagnostic := HarnessPhaseDiagnostic{Phase: phase}
	harness, err := instantiateHarness(cfg, name, workspaceRoot)
	if err == nil {
		diagnostic.Available = true
		return resolvedHarnessPhase{harness: harness, diagnostic: diagnostic}
	}
	diagnostic.Failures = append(diagnostic.Failures, HarnessCandidateFailure{Harness: name, Message: err.Error()})
	return resolvedHarnessPhase{diagnostic: diagnostic}
}


func instantiateHarness(cfg *config.Config, name config.HarnessName, workspaceRoot string) (adapter.AgentHarness, error) {
	switch name {
	case config.HarnessOhMyPi:
		if err := omp.ValidateReadiness(cfg.Adapters.OhMyPi); err != nil {
			return nil, fmt.Errorf("ohmypi unavailable: %w", err)
		}
		return omp.NewHarness(cfg.Adapters.OhMyPi, workspaceRoot), nil
	case config.HarnessClaudeCode:
		if err := claudeagent.ValidateReadiness(cfg.Adapters.ClaudeCode); err != nil {
			return nil, fmt.Errorf("claude-agent unavailable: %w", err)
		}
		return claudeagent.NewHarness(cfg.Adapters.ClaudeCode, workspaceRoot), nil
	case config.HarnessCodex:
		binary := cfg.Adapters.Codex.BinaryPath
		if binary == "" {
			binary = "codex"
		}
		if _, err := exec.LookPath(binary); err != nil {
			return nil, fmt.Errorf("codex binary %q not found: %w", binary, err)
		}
		return codexadapter.NewHarness(cfg.Adapters.Codex), nil
	case config.HarnessOpenCode:
		if err := opencodeadapter.ValidateReadiness(cfg.Adapters.OpenCode); err != nil {
			return nil, fmt.Errorf("opencode unavailable: %w", err)
		}
		return opencodeadapter.NewHarness(cfg.Adapters.OpenCode, workspaceRoot), nil
	default:
		return nil, errors.New("unsupported harness: " + string(name))
	}
}
