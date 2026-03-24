package config

import (
	"os/exec"
	"strings"
)

// GitLabAuthConfigured reports whether GitLab authentication is available.
// It mirrors GitHubAuthConfigured: a token in config, a keychain reference,
// or the glab CLI on PATH is sufficient.
func GitLabAuthConfigured(cfg GitlabConfig) bool {
	return strings.TrimSpace(cfg.Token) != "" || strings.TrimSpace(cfg.TokenRef) != "" || HasGlabCLI()
}

// GitLabAuthSource returns a human-readable label describing the active
// GitLab auth source ("config token", "glab cli", or "unset").
func GitLabAuthSource(cfg GitlabConfig) string {
	if strings.TrimSpace(cfg.Token) != "" || strings.TrimSpace(cfg.TokenRef) != "" {
		return "config token"
	}
	if HasGlabCLI() {
		return "glab cli"
	}

	return "unset"
}

// HasGlabCLI reports whether the glab binary is on PATH.
func HasGlabCLI() bool {
	_, err := exec.LookPath("glab")

	return err == nil
}
