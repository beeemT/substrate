package app

import (
	"context"
	"testing"

	"github.com/beeemT/substrate/internal/config"
)

func TestBuildRepoLifecycleAdapters_SkipsGithubWhenTokenResolutionFails(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{}
	cfg.Adapters.GitHub.Owner = "acme"
	cfg.Adapters.GitHub.Repo = "rocket"

	adapters := BuildRepoLifecycleAdapters(context.Background(), cfg, "")
	if adapters != nil {
		t.Fatalf("expected nil adapters for empty workspace dir, got %d", len(adapters))
	}
}

func TestBuildWorkItemAdapters_ManualOnlyWithoutOptionalProviders(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{}
	adapters := BuildWorkItemAdapters(cfg, "workspace-1", nil)
	if len(adapters) != 1 {
		t.Fatalf("expected only manual adapter, got %d", len(adapters))
	}
	if adapters[0].Name() != "manual" {
		t.Fatalf("expected manual adapter, got %q", adapters[0].Name())
	}
}
