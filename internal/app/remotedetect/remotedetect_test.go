package remotedetect

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beeemT/substrate/internal/config"
)

func TestRemoteHost(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "https", url: "https://github.com/org/repo.git", want: "github.com"},
		{name: "ssh scp", url: "git@gitlab.com:group/repo.git", want: "gitlab.com"},
		{name: "ssh url", url: "ssh://git@code.example.com/group/repo.git", want: "code.example.com"},
		{name: "invalid", url: "not-a-remote", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := remoteHost(tt.url); got != tt.want {
				t.Fatalf("remoteHost(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestRemoteHost_SSHAliasResolution(t *testing.T) {
	// Create a temp SSH config directory and config file
	sshDir := t.TempDir()
	sshConfigDir := filepath.Join(sshDir, ".ssh")
	if err := os.MkdirAll(sshConfigDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	configPath := filepath.Join(sshConfigDir, "config")
	cfg := `Host github-justtrack
    Hostname github.com
    User git
Host gitlab-internal
    Hostname gitlab.internal.example.com
    User git
`
	if err := os.WriteFile(configPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Override HOME for the test
	t.Setenv("HOME", sshDir)

	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "ssh alias to github", url: "git@github-justtrack:org/repo.git", want: "github.com"},
		{name: "ssh alias to gitlab internal", url: "git@gitlab-internal:org/repo.git", want: "gitlab.internal.example.com"},
		{name: "unknown alias", url: "git@unknown-alias:org/repo.git", want: "unknown-alias"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := remoteHost(tt.url); got != tt.want {
				t.Fatalf("remoteHost(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestLoadGlabKnownHosts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfgDir := filepath.Join(home, ".config", "glab-cli")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	cfg := []byte("hosts:\n  gitlab.example.com:\n    token: abc\n  GitLab.Internal:\n    token: def\n")
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yml"), cfg, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	hosts, err := loadGlabKnownHosts()
	if err != nil {
		t.Fatalf("loadGlabKnownHosts() error = %v", err)
	}
	if len(hosts) != 2 || hosts[0] != "gitlab.example.com" || hosts[1] != "gitlab.internal" {
		t.Fatalf("loadGlabKnownHosts() = %v, want normalized sorted hosts", hosts)
	}
}

func TestDetectPlatform_EmptyDir(t *testing.T) {
	platform, err := DetectPlatform(context.Background(), "", nil)
	if err == nil {
		t.Fatal("DetectPlatform() error = nil, want error")
	}
	if platform != PlatformUnknown {
		t.Fatalf("DetectPlatform() platform = %v, want unknown", platform)
	}
}

func TestDetectPlatform_ConfiguredGitHubEnterpriseHost(t *testing.T) {
	repoDir := createRepoWithRemote(t, "git@github.internal:org/repo.git")
	cfg := &config.Config{}
	cfg.Adapters.GitHub.BaseURL = "https://github.internal/api/v3"

	platform, err := DetectPlatform(context.Background(), repoDir, cfg)
	if err != nil {
		t.Fatalf("DetectPlatform() error = %v", err)
	}
	if platform != PlatformGitHub {
		t.Fatalf("DetectPlatform() platform = %v, want github", platform)
	}
}

func TestDetectPlatform_SelfHostedGitLabViaGlabCLI(t *testing.T) {
	repoDir := createRepoWithRemote(t, "git@gitlab.internal.example:org/repo.git")

	// Create a fake glab binary that outputs valid JSON.
	binDir := t.TempDir()
	glabBin := filepath.Join(binDir, "glab")
	glabScript := `#!/bin/sh` + "\n" + `echo '{"web_url":"https://gitlab.internal.example/org/repo"}'`
	if err := os.WriteFile(glabBin, []byte(glabScript), 0o755); err != nil {
		t.Fatalf("write fake glab: %v", err)
	}

	// Point PATH at the fake binary and ensure no glab config exists.
	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+originalPath)
	home := t.TempDir()
	t.Setenv("HOME", home)

	platform, err := DetectPlatform(context.Background(), repoDir, nil)
	if err != nil {
		t.Fatalf("DetectPlatform() error = %v", err)
	}
	if platform != PlatformGitLab {
		t.Fatalf("DetectPlatform() platform = %v, want gitlab", platform)
	}
}

func createRepoWithRemote(t *testing.T, remoteURL string) string {
	t.Helper()
	repoDir := t.TempDir()
	runGit(t, repoDir, "init")
	runGit(t, repoDir, "config", "user.email", "test@example.com")
	runGit(t, repoDir, "config", "user.name", "Test User")
	runGit(t, repoDir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit(t, repoDir, "add", "README.md")
	runGit(t, repoDir, "commit", "-m", "initial commit")
	runGit(t, repoDir, "remote", "add", "origin", remoteURL)

	return repoDir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v (output: %s)", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
}
