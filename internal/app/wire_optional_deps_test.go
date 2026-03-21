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

	adapters := BuildWorkItemAdapters(cfg, "workspace-1", nil)
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
