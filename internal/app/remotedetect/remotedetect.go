package remotedetect

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type Platform int

const (
	PlatformUnknown Platform = iota
	PlatformGitHub
	PlatformGitLab
)

func (p Platform) String() string {
	switch p {
	case PlatformGitHub:
		return "github"
	case PlatformGitLab:
		return "gitlab"
	default:
		return "unknown"
	}
}

func DetectPlatform(ctx context.Context, dir string) (Platform, error) {
	if strings.TrimSpace(dir) == "" {
		return PlatformUnknown, fmt.Errorf("workspace directory is empty")
	}

	remoteName, remoteURL, err := resolveRemote(ctx, dir)
	if err != nil {
		return PlatformUnknown, err
	}

	host := remoteHost(remoteURL)
	if host == "" {
		return PlatformUnknown, fmt.Errorf("parse remote %q (%s): unsupported url", remoteName, remoteURL)
	}

	switch host {
	case "github.com":
		return PlatformGitHub, nil
	case "gitlab.com":
		return PlatformGitLab, nil
	}

	knownHosts, err := loadGlabKnownHosts()
	if err != nil {
		return PlatformUnknown, err
	}
	for _, knownHost := range knownHosts {
		if strings.EqualFold(host, knownHost) {
			return PlatformGitLab, nil
		}
	}

	return PlatformUnknown, nil
}

func resolveRemote(ctx context.Context, dir string) (string, string, error) {
	if originURL, err := gitOutput(ctx, dir, "remote", "get-url", "origin"); err == nil {
		return "origin", originURL, nil
	}

	remotesOutput, err := gitOutput(ctx, dir, "remote")
	if err != nil {
		return "", "", fmt.Errorf("list git remotes: %w", err)
	}

	remoteNames := strings.Fields(remotesOutput)
	if len(remoteNames) == 0 {
		return "", "", fmt.Errorf("no git remotes found in %s", dir)
	}
	sort.Strings(remoteNames)

	chosen := remoteNames[0]
	chosenURL, err := gitOutput(ctx, dir, "remote", "get-url", chosen)
	if err != nil {
		return "", "", fmt.Errorf("get git remote url for %s: %w", chosen, err)
	}
	return chosen, chosenURL, nil
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func remoteHost(remoteURL string) string {
	if strings.Contains(remoteURL, "://") {
		parsed, err := url.Parse(remoteURL)
		if err != nil {
			return ""
		}
		return normalizeHost(parsed.Hostname())
	}

	if strings.HasPrefix(remoteURL, "git@") || strings.HasPrefix(remoteURL, "ssh://") {
		trimmed := strings.TrimPrefix(remoteURL, "ssh://")
		if at := strings.Index(trimmed, "@"); at >= 0 {
			trimmed = trimmed[at+1:]
		}
		if slash := strings.Index(trimmed, "/"); slash >= 0 {
			trimmed = trimmed[:slash]
		}
		if colon := strings.Index(trimmed, ":"); colon >= 0 {
			trimmed = trimmed[:colon]
		}
		return normalizeHost(trimmed)
	}

	return ""
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.TrimSuffix(host, ".")
	return host
}

type glabConfig struct {
	Hosts map[string]any `yaml:"hosts"`
}

func loadGlabKnownHosts() ([]string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve user home for glab config: %w", err)
	}

	cfgPath := filepath.Join(homeDir, ".config", "glab-cli", "config.yml")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read glab config: %w", err)
	}

	var cfg glabConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse glab config: %w", err)
	}

	hosts := make([]string, 0, len(cfg.Hosts))
	for host := range cfg.Hosts {
		hosts = append(hosts, normalizeHost(host))
	}
	sort.Strings(hosts)
	return hosts, nil
}
