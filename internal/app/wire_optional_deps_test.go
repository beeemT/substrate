package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
)

func TestBuildRepoLifecycleAdapters_SkipsGithubWhenTokenResolutionFails(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{}

	adapters := BuildRepoLifecycleAdapters(context.Background(), cfg, "", adapter.ReviewArtifactRepos{})
	if adapters != nil {
		t.Fatalf("expected nil adapters for empty workspace dir, got %d", len(adapters))
	}
}

func TestBuildWorkItemAdapters_ManualOnlyWithoutCompleteOptionalProviders(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	cfg := &config.Config{}
	cfg.Adapters.Sentry.Token = "token-without-organization"

	adapters, _ := BuildWorkItemAdapters(cfg, "workspace-1", nil)
	if len(adapters) != 1 {
		t.Fatalf("expected only manual adapter, got %d", len(adapters))
	}
	if adapters[0].Name() != "manual" {
		t.Fatalf("expected manual adapter, got %q", adapters[0].Name())
	}
}

func TestBuildRepoLifecycleAdapters_IgnoresSentryConfig(t *testing.T) {
	t.Parallel()

	workspaceDir := t.TempDir()
	createWorkspaceRepo(t, filepath.Join(workspaceDir, "repo-one"), "git@gitlab.com:group/repo.git")

	cfg := &config.Config{}
	cfg.Adapters.Sentry.Token = "token"
	cfg.Adapters.Sentry.Organization = "acme"

	adapters := BuildRepoLifecycleAdapters(context.Background(), cfg, workspaceDir, adapter.ReviewArtifactRepos{})
	if len(adapters) != 1 {
		t.Fatalf("adapters len = %d, want 1", len(adapters))
	}
	if adapters[0].Name() != "glab" {
		t.Fatalf("adapter name = %q, want glab", adapters[0].Name())
	}
}

func TestBuildRepoSources_AlwaysIncludesManual(t *testing.T) {
	// Isolate PATH so no gh/glab CLI is found; only manual should be included.
	t.Setenv("PATH", t.TempDir())
	cfg := &config.Config{}

	sources := BuildRepoSources(context.Background(), cfg)
	if len(sources) != 1 {
		t.Fatalf("sources len = %d, want 1", len(sources))
	}
	if sources[0].Name() != "manual" {
		t.Fatalf("sources[0].Name() = %q, want manual", sources[0].Name())
	}
}

func TestBuildRepoSources_IncludesGitHubWhenConfigTokenPresent(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	cfg := &config.Config{}
	cfg.Adapters.GitHub.Token = "ghp_test"

	sources := BuildRepoSources(context.Background(), cfg)
	if len(sources) != 2 {
		t.Fatalf("sources len = %d, want 2 (manual + github)", len(sources))
	}
	if sources[0].Name() != "manual" || sources[1].Name() != "github" {
		t.Fatalf("source names = [%q %q], want [manual github]", sources[0].Name(), sources[1].Name())
	}
}

func TestBuildRepoSources_IncludesGitHubWhenGhCLIAvailable(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, binDir, "gh", "#!/bin/sh\nif [ \"$1\" = \"auth\" ] && [ \"$2\" = \"token\" ]; then\n  printf 'gh-cli-token\\n'\n  exit 0\nfi\nexit 1\n")
	t.Setenv("PATH", binDir)
	cfg := &config.Config{}

	sources := BuildRepoSources(context.Background(), cfg)
	if len(sources) != 2 {
		t.Fatalf("sources len = %d, want 2 (manual + github)", len(sources))
	}
	if sources[0].Name() != "manual" || sources[1].Name() != "github" {
		t.Fatalf("source names = [%q %q], want [manual github]", sources[0].Name(), sources[1].Name())
	}
}

func TestBuildRepoSources_SkipsGitHubWhenTokenResolutionFails(t *testing.T) {
	// gh CLI exists but exits non-zero: token resolution fails; source is skipped.
	binDir := t.TempDir()
	writeExecutable(t, binDir, "gh", "#!/bin/sh\nexit 1\n")
	t.Setenv("PATH", binDir)
	cfg := &config.Config{}

	sources := BuildRepoSources(context.Background(), cfg)
	if len(sources) != 1 {
		t.Fatalf("sources len = %d, want 1 (manual only)", len(sources))
	}
	if sources[0].Name() != "manual" {
		t.Fatalf("sources[0].Name() = %q, want manual", sources[0].Name())
	}
}
