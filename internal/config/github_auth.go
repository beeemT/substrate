package config

import (
	"os/exec"
	"strings"
)

const githubAuthSourceUnset = "unset"

func GitHubAuthConfigured(cfg GithubConfig) bool {
	return strings.TrimSpace(cfg.Token) != "" || strings.TrimSpace(cfg.TokenRef) != "" || HasGitHubCLI()
}

func GitHubAuthSource(cfg GithubConfig) string {
	if strings.TrimSpace(cfg.Token) != "" || strings.TrimSpace(cfg.TokenRef) != "" {
		return "config token"
	}
	if HasGitHubCLI() {
		return "gh cli"
	}

	return githubAuthSourceUnset
}

func HasGitHubCLI() bool {
	_, err := exec.LookPath("gh")

	return err == nil
}
