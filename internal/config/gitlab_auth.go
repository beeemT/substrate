package config

import (
	"context"
	"os/exec"
	"strings"
	"time"
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

// InferGlabBaseURL attempts to detect the GitLab instance URL from the glab
// CLI by running "glab auth status" and extracting the first authenticated
// host. Returns "https://gitlab.com" if glab is absent, not authenticated, or
// the output cannot be parsed.
func InferGlabBaseURL() string {
	if !HasGlabCLI() {
		return "https://gitlab.com"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "glab", "auth", "status").CombinedOutput()
	if err != nil && len(out) == 0 {
		return "https://gitlab.com"
	}

	host := parseGlabAuthStatusHost(string(out))
	if host == "" {
		return "https://gitlab.com"
	}

	return "https://" + host
}

// parseGlabAuthStatusHost extracts the first authenticated hostname from the
// output of "glab auth status". It looks for lines containing "Logged in to
// <host> as", which is stable across glab versions.
func parseGlabAuthStatusHost(output string) string {
	const needle = "logged in to "

	for _, line := range strings.Split(output, "\n") {
		lower := strings.ToLower(line)
		idx := strings.Index(lower, needle)
		if idx == -1 {
			continue
		}

		rest := line[idx+len(needle):]
		parts := strings.Fields(rest)
		if len(parts) >= 1 && strings.Contains(parts[0], ".") {
			return parts[0]
		}
	}

	return ""
}
