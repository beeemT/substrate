package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
)

func clearSentryEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"SENTRY_AUTH_TOKEN", "SENTRY_URL", "SENTRY_ORG", "SENTRY_PROJECT"} {
		t.Setenv(key, "")
	}
}

func TestBuildWorkItemAdapters_RegistersSentryAdapterWithEnvToken(t *testing.T) {
	clearSentryEnv(t)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("SENTRY_AUTH_TOKEN", "env-token")

	repo := stubWorkItemRepo{}
	cfg := &config.Config{}
	cfg.Adapters.Sentry.Organization = "acme"

	adapters, _ := BuildWorkItemAdapters(
		cfg,
		"ws-1",
		service.NewSessionService(
			repository.NoopTransacter{
				Res: repository.Resources{Sessions: repo},
			},
		),
	)
	if len(adapters) != 2 {
		t.Fatalf("adapters len = %d, want 2", len(adapters))
	}
	if adapters[0].Name() != "manual" || adapters[1].Name() != "sentry" {
		t.Fatalf("adapter order = [%q %q], want [manual sentry]", adapters[0].Name(), adapters[1].Name())
	}
}

func TestBuildWorkItemAdapters_RegistersSentryAdapterWithCLIAuth(t *testing.T) {
	clearSentryEnv(t)
	binDir := t.TempDir()
	writeExecutable(t, binDir, "sentry", "#!/bin/sh\nif [ \"$1\" = \"auth\" ] && [ \"$2\" = \"status\" ]; then\n  exit 0\nfi\nexit 1\n")
	t.Setenv("PATH", binDir)

	repo := stubWorkItemRepo{}
	cfg := &config.Config{}
	cfg.Adapters.Sentry.Organization = "acme"

	adapters, _ := BuildWorkItemAdapters(
		cfg,
		"ws-1",
		service.NewSessionService(
			repository.NoopTransacter{
				Res: repository.Resources{Sessions: repo},
			},
		),
	)
	if len(adapters) != 2 {
		t.Fatalf("adapters len = %d, want 2", len(adapters))
	}
	if adapters[1].Name() != "sentry" {
		t.Fatalf("second adapter = %q, want sentry", adapters[1].Name())
	}
}

func TestBuildWorkItemAdapters_SkipsSentryWithoutOrganizationEvenWithCLIAuth(t *testing.T) {
	clearSentryEnv(t)
	binDir := t.TempDir()
	writeExecutable(t, binDir, "sentry", "#!/bin/sh\nif [ \"$1\" = \"auth\" ] && [ \"$2\" = \"status\" ]; then\n  exit 0\nfi\nexit 1\n")
	t.Setenv("PATH", binDir)

	repo := stubWorkItemRepo{}
	adapters, warnings := BuildWorkItemAdapters(
		&config.Config{},
		"ws-1",
		service.NewSessionService(
			repository.NoopTransacter{
				Res: repository.Resources{Sessions: repo},
			},
		),
	)
	if len(adapters) != 1 {
		t.Fatalf("adapters len = %d, want manual-only when organization is missing", len(adapters))
	}
	if adapters[0].Name() != "manual" {
		t.Fatalf("adapter = %q, want manual", adapters[0].Name())
	}
	if len(warnings) == 0 {
		t.Fatal("warnings = empty, want sentry warning")
	}
}

func TestBuildRepoLifecycleAdapters_StillIgnoresSentryCLIAuth(t *testing.T) {
	clearSentryEnv(t)
	binDir := t.TempDir()
	writeExecutable(t, binDir, "sentry", "#!/bin/sh\nif [ \"$1\" = \"auth\" ] && [ \"$2\" = \"status\" ]; then\n  exit 0\nfi\nexit 1\n")
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("LookPath(git): %v", err)
	}
	if err := os.Symlink(gitPath, filepath.Join(binDir, "git")); err != nil {
		t.Fatalf("Symlink(git): %v", err)
	}
	t.Setenv("PATH", binDir)

	workspaceDir := t.TempDir()
	createWorkspaceRepo(t, workspaceDir+"/repo-one", "git@gitlab.com:group/repo.git")
	cfg := &config.Config{}
	cfg.Adapters.Sentry.Organization = "acme"
	adapters := BuildRepoLifecycleAdapters(context.Background(), cfg, workspaceDir, adapter.ReviewArtifactRepos{})
	if len(adapters) != 1 || adapters[0].Name() != "glab" {
		t.Fatalf("repo lifecycle adapters = %#v, want only glab", adapters)
	}
}
