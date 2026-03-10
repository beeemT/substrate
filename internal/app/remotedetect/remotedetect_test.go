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

func createRepoWithRemote(t *testing.T, remoteURL string) string {
	t.Helper()
	repoDir := t.TempDir()
	runGit(t, repoDir, "init")
	runGit(t, repoDir, "config", "user.email", "test@example.com")
	runGit(t, repoDir, "config", "user.name", "Test User")
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
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v (output: %s)", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
}
