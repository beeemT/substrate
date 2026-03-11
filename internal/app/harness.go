package app

import (
	"fmt"
	"os/exec"
	"strings"

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
	groups := d.warningGroups()
	if len(groups) == 0 {
		return ""
	}
	if len(groups) == 1 && len(groups[0].Phases) == 1 {
		return fmt.Sprintf("%s unavailable. Check Harness Routing.", groups[0].Phases[0])
	}
	return "Harnesses unavailable. Check Harness Routing."
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
		phaseLabel := displayHarnessPhase(phase.Phase)
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
	return fmt.Sprintf("%s: %s.", strings.Join(phases, ", "), reason)
}

func settingsHarnessFailureReason(harness config.HarnessName, message string) string {
	message = strings.TrimSpace(message)
	switch harness {
	case config.HarnessOhMyPi:
		return settingsOhMyPiFailureReason(message)
	case config.HarnessClaudeCode:
		return settingsBinaryFailureReason("Claude Code", "claude", message)
	case config.HarnessCodex:
		return settingsBinaryFailureReason("Codex", "codex", message)
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

func settingsBinaryFailureReason(name string, defaultBinary string, message string) string {
	binary, ok := extractQuotedValue(message)
	if !ok {
		return fmt.Sprintf("%s unavailable", name)
	}
	if binary == defaultBinary {
		return fmt.Sprintf("%s not found", name)
	}
	return fmt.Sprintf("%s binary not found", name)
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

	phases := []struct {
		name    string
		harness config.HarnessName
	}{
		{name: "planning", harness: cfg.Harness.Phase.Planning},
		{name: "implementation", harness: cfg.Harness.Phase.Implementation},
		{name: "review", harness: cfg.Harness.Phase.Review},
		{name: "foreman", harness: cfg.Harness.Phase.Foreman},
	}
	diagnostics := HarnessDiagnostics{Phases: make([]HarnessPhaseDiagnostic, 0, len(phases))}
	for _, phase := range phases {
		resolved := resolveHarnessPhase(cfg, phase.name, phase.harness, workspaceRoot)
		diagnostics.Phases = append(diagnostics.Phases, resolved.diagnostic)
	}
	return diagnostics
}

func BuildAgentHarnesses(cfg *config.Config, workspaceRoot string) (AgentHarnesses, error) {
	if cfg == nil {
		return AgentHarnesses{}, fmt.Errorf("config is nil")
	}

	planning := resolveHarnessPhase(cfg, "planning", cfg.Harness.Phase.Planning, workspaceRoot).harness
	implementation := resolveHarnessPhase(cfg, "implementation", cfg.Harness.Phase.Implementation, workspaceRoot).harness
	review := resolveHarnessPhase(cfg, "review", cfg.Harness.Phase.Review, workspaceRoot).harness
	foreman := resolveHarnessPhase(cfg, "foreman", cfg.Harness.Phase.Foreman, workspaceRoot).harness
	return AgentHarnesses{
		Planning:       planning,
		Implementation: implementation,
		Review:         review,
		Foreman:        foreman,
		Resume:         implementation,
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

func appendUniqueWarning(existing []string, message string) []string {
	for _, current := range existing {
		if current == message {
			return existing
		}
	}
	return append(existing, message)
}

func displayHarnessPhase(phase string) string {
	switch phase {
	case "planning":
		return "Planning"
	case "implementation":
		return "Implementation"
	case "review":
		return "Review"
	case "foreman":
		return "Foreman"
	default:
		return phase
	}
}

func instantiateHarness(cfg *config.Config, name config.HarnessName, workspaceRoot string) (adapter.AgentHarness, error) {
	switch name {
	case config.HarnessOhMyPi:
		if err := omp.ValidateReadiness(cfg.Adapters.OhMyPi); err != nil {
			return nil, fmt.Errorf("ohmypi unavailable: %w", err)
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
