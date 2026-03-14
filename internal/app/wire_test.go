package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

type stubWorkItemRepo struct{}

func (stubWorkItemRepo) Get(context.Context, string) (domain.Session, error) {
	return domain.Session{}, repository.ErrNotFound
}

func (stubWorkItemRepo) List(context.Context, repository.SessionFilter) ([]domain.Session, error) {
	return nil, nil
}
func (stubWorkItemRepo) Create(context.Context, domain.Session) error { return nil }
func (stubWorkItemRepo) Update(context.Context, domain.Session) error { return nil }
func (stubWorkItemRepo) Delete(context.Context, string) error         { return nil }

func writeExecutable(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestBuildWorkItemAdapters_RegistersGitHubAdapter(t *testing.T) {
	repo := stubWorkItemRepo{}
	cfg := &config.Config{}
	cfg.Adapters.GitHub.Token = "token"

	adapters := BuildWorkItemAdapters(cfg, "ws-1", repo)
	if len(adapters) != 2 {
		t.Fatalf("adapters len = %d, want 2", len(adapters))
	}
	if adapters[0].Name() != "manual" || adapters[1].Name() != "github" {
		t.Fatalf("adapter order = [%q %q], want [manual github]", adapters[0].Name(), adapters[1].Name())
	}
}

func TestBuildWorkItemAdapters_RegistersSentryAdapter(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	repo := stubWorkItemRepo{}
	cfg := &config.Config{}
	cfg.Adapters.Sentry.Token = "token"
	cfg.Adapters.Sentry.Organization = "acme"

	adapters := BuildWorkItemAdapters(cfg, "ws-1", repo)
	if len(adapters) != 2 {
		t.Fatalf("adapters len = %d, want 2", len(adapters))
	}
	if adapters[0].Name() != "manual" || adapters[1].Name() != "sentry" {
		t.Fatalf("adapter order = [%q %q], want [manual sentry]", adapters[0].Name(), adapters[1].Name())
	}
}

func TestBuildRepoLifecycleAdapters_EmptyWorkspace(t *testing.T) {
	cfg := &config.Config{}
	if adapters := BuildRepoLifecycleAdapters(context.Background(), cfg, "", nil); len(adapters) != 0 {
		t.Fatalf("adapters len = %d, want 0", len(adapters))
	}
}

func TestBuildRepoLifecycleAdapters_UsesWorkspaceRepoPlatforms(t *testing.T) {
	t.Parallel()

	workspaceDir := t.TempDir()
	repoDir := filepath.Join(workspaceDir, "repo-one")
	createWorkspaceRepo(t, repoDir, "git@gitlab.com:group/repo.git")

	adapters := BuildRepoLifecycleAdapters(context.Background(), &config.Config{}, workspaceDir, nil)
	if len(adapters) != 1 {
		t.Fatalf("adapters len = %d, want 1", len(adapters))
	}
	if adapters[0].Name() != "glab" {
		t.Fatalf("adapter name = %q, want glab", adapters[0].Name())
	}
}

func TestBuildRepoLifecycleAdapters_PreservesSupportedPlatformsInMixedWorkspace(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("LookPath(git): %v", err)
	}
	binDir := t.TempDir()
	if err := os.Symlink(gitPath, filepath.Join(binDir, "git")); err != nil {
		t.Fatalf("Symlink(git): %v", err)
	}
	t.Setenv("PATH", binDir)

	workspaceDir := t.TempDir()
	createWorkspaceRepo(t, filepath.Join(workspaceDir, "gitlab-repo"), "git@gitlab.com:group/repo.git")
	createWorkspaceRepo(t, filepath.Join(workspaceDir, "github-repo"), "git@github.com:org/repo.git")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" {
			t.Fatalf("unexpected github request path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"login":"octocat"}`))
	}))
	defer server.Close()

	cfg := &config.Config{}
	cfg.Adapters.GitHub.Token = "token"
	cfg.Adapters.GitHub.BaseURL = server.URL

	adapters := BuildRepoLifecycleAdapters(context.Background(), cfg, workspaceDir, nil)
	if len(adapters) != 2 {
		t.Fatalf("adapters len = %d, want 2", len(adapters))
	}
	seen := make(map[string]bool, len(adapters))
	for _, lifecycleAdapter := range adapters {
		seen[lifecycleAdapter.Name()] = true
	}
	if !seen["glab"] || !seen["github"] {
		t.Fatalf("adapter names = %#v, want glab and github", seen)
	}
}

func createWorkspaceRepo(t *testing.T, repoDir, remoteURL string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Join(repoDir, ".bare"), 0o755); err != nil {
		t.Fatalf("create git-work marker: %v", err)
	}
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
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v (output: %s)", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
}

func TestBuildWorkItemAdapters_RegistersGitHubAdapterWithGhCLI(t *testing.T) {
	repo := stubWorkItemRepo{}
	cfg := &config.Config{}
	cfg.Adapters.GitHub.Assignee = "someone"

	binDir := t.TempDir()
	writeExecutable(t, binDir, "gh", "#!/bin/sh\nif [ \"$1\" = \"auth\" ] && [ \"$2\" = \"token\" ]; then\n  printf 'gh-cli-token\\n'\n  exit 0\nfi\nexit 1\n")
	t.Setenv("PATH", binDir)

	adapters := BuildWorkItemAdapters(cfg, "ws-1", repo)
	if len(adapters) != 2 {
		t.Fatalf("adapters len = %d, want 2", len(adapters))
	}
	if adapters[0].Name() != "manual" || adapters[1].Name() != "github" {
		t.Fatalf("adapter order = [%q %q], want [manual github]", adapters[0].Name(), adapters[1].Name())
	}
}
