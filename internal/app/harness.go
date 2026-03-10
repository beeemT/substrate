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

func (f HarnessCandidateFailure) UserMessage() string {
	return userFacingHarnessFailure(f.Harness, f.Message)
}

type HarnessPhaseDiagnostic struct {
	Phase         string
	Requested     config.HarnessName
	Resolved      config.HarnessName
	Available     bool
	UsingFallback bool
	Failures      []HarnessCandidateFailure
}

func (d HarnessPhaseDiagnostic) WarningMessage() string {
	if len(d.Failures) == 0 {
		return ""
	}

	phase := displayHarnessPhase(d.Phase)
	if d.Available {
		reason := d.Failures[0].UserMessage()
		for _, failure := range d.Failures {
			if failure.Harness == d.Requested {
				reason = failure.UserMessage()
				break
			}
		}
		return fmt.Sprintf("%s requested %s, but %s Falling back to %s.", phase, displayHarnessName(d.Requested), reason, displayHarnessName(d.Resolved))
	}

	parts := make([]string, 0, len(d.Failures))
	for _, failure := range d.Failures {
		parts = append(parts, failure.UserMessage())
	}
	return fmt.Sprintf("%s unavailable. %s", phase, strings.Join(parts, " "))
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
	warnings := d.PhaseWarnings()
	if len(warnings) == 0 {
		return ""
	}
	if len(warnings) == 1 {
		return warnings[0]
	}
	return "Some harnesses are unavailable. Open Settings → Harness Routing."
}

func (d HarnessDiagnostics) PhaseWarnings() []string {
	warnings := make([]string, 0, len(d.Phases))
	for _, phase := range d.Phases {
		if warning := phase.WarningMessage(); warning != "" {
			warnings = append(warnings, warning)
		}
	}
	return warnings
}

func (d HarnessDiagnostics) HarnessWarnings() map[config.HarnessName][]string {
	warnings := make(map[config.HarnessName][]string)
	for _, phase := range d.Phases {
		for _, failure := range phase.Failures {
			prefix := displayHarnessPhase(phase.Phase)
			if failure.Harness != phase.Requested {
				prefix += " fallback"
			}
			message := fmt.Sprintf("%s: %s", prefix, failure.UserMessage())
			warnings[failure.Harness] = appendUniqueWarning(warnings[failure.Harness], message)
		}
	}
	return warnings
}

func userFacingHarnessFailure(harness config.HarnessName, message string) string {
	message = strings.TrimSpace(message)
	switch harness {
	case config.HarnessOhMyPi:
		return userFacingOhMyPiFailure(message)
	case config.HarnessClaudeCode:
		return userFacingBinaryFailure("Claude Code", "claude", message)
	case config.HarnessCodex:
		return userFacingBinaryFailure("Codex", "codex", message)
	default:
		return message
	}
}

func userFacingOhMyPiFailure(message string) string {
	detail := strings.TrimSpace(strings.TrimPrefix(message, "ohmypi unavailable:"))
	switch {
	case strings.HasPrefix(detail, "resolve ohmypi bridge: no bridge binary or script found"):
		return "Oh My Pi bridge not found. Install the bridge or set Bridge Path in Settings → Harness Routing → Oh My Pi."
	case strings.HasPrefix(detail, "resolve bun "):
		return "Bun runtime not found for the Oh My Pi bridge. Install Bun or set Bun Path in Settings → Harness Routing → Oh My Pi."
	case strings.Contains(detail, "source bridge dependencies missing under "):
		return "Oh My Pi bridge dependencies are missing. Run `bun install` in the bridge directory or use a packaged bridge."
	case strings.Contains(detail, "check bridge package metadata"):
		return "Oh My Pi bridge directory could not be read. Check Bridge Path in Settings → Harness Routing → Oh My Pi."
	default:
		return "Oh My Pi is unavailable. Check Bridge Path and Bun Path in Settings → Harness Routing → Oh My Pi."
	}
}

func userFacingBinaryFailure(name string, defaultBinary string, message string) string {
	binary, ok := extractQuotedValue(message)
	if !ok {
		return fmt.Sprintf("%s CLI is unavailable. Install %s or set Binary Path in Settings → Harness Routing → %s.", name, name, name)
	}
	if binary == defaultBinary {
		return fmt.Sprintf("%s CLI not found in PATH. Install %s or set Binary Path in Settings → Harness Routing → %s.", name, name, name)
	}
	return fmt.Sprintf("%s CLI %q not found. Install %s or set Binary Path in Settings → Harness Routing → %s.", name, binary, name, name)
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
	diagnostic := HarnessPhaseDiagnostic{Phase: phase, Requested: name}
	for _, candidate := range uniqueHarnessCandidates(name, cfg.Harness.Fallback) {
		harness, err := instantiateHarness(cfg, candidate, workspaceRoot)
		if err == nil {
			diagnostic.Resolved = candidate
			diagnostic.Available = true
			diagnostic.UsingFallback = candidate != name
			return resolvedHarnessPhase{harness: harness, diagnostic: diagnostic}
		}
		diagnostic.Failures = append(diagnostic.Failures, HarnessCandidateFailure{Harness: candidate, Message: err.Error()})
	}
	return resolvedHarnessPhase{diagnostic: diagnostic}
}

func uniqueHarnessCandidates(primary config.HarnessName, fallbacks []config.HarnessName) []config.HarnessName {
	seen := make(map[config.HarnessName]bool, 1+len(fallbacks))
	candidates := make([]config.HarnessName, 0, 1+len(fallbacks))
	appendCandidate := func(name config.HarnessName) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		candidates = append(candidates, name)
	}
	appendCandidate(primary)
	for _, fallback := range fallbacks {
		appendCandidate(fallback)
	}
	return candidates
}

func appendUniqueWarning(existing []string, message string) []string {
	for _, current := range existing {
		if current == message {
			return existing
		}
	}
	return append(existing, message)
}

func displayHarnessName(name config.HarnessName) string {
	switch name {
	case config.HarnessOhMyPi:
		return "Oh My Pi"
	case config.HarnessClaudeCode:
		return "Claude Code"
	case config.HarnessCodex:
		return "Codex"
	default:
		if name == "" {
			return "Unconfigured harness"
		}
		return string(name)
	}
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
