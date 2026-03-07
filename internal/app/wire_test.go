package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

type stubWorkItemRepo struct{}

func (stubWorkItemRepo) Get(context.Context, string) (domain.WorkItem, error) {
	return domain.WorkItem{}, repository.ErrNotFound
}

func (stubWorkItemRepo) List(context.Context, repository.WorkItemFilter) ([]domain.WorkItem, error) {
	return nil, nil
}
func (stubWorkItemRepo) Create(context.Context, domain.WorkItem) error { return nil }
func (stubWorkItemRepo) Update(context.Context, domain.WorkItem) error { return nil }
func (stubWorkItemRepo) Delete(context.Context, string) error          { return nil }

func writeExecutable(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestBuildWorkItemAdapters_DoesNotRegisterGitHubAdapter(t *testing.T) {
	repo := stubWorkItemRepo{}
	cfg := &config.Config{}
	cfg.Adapters.GitHub.Owner = "acme"
	cfg.Adapters.GitHub.Repo = "rocket"

	adapters := BuildWorkItemAdapters(cfg, "ws-1", repo)
	if len(adapters) != 1 {
		t.Fatalf("adapters len = %d, want 1", len(adapters))
	}
	if adapters[0].Name() != "manual" {
		t.Fatalf("first adapter = %q, want manual only", adapters[0].Name())
	}
}

func TestBuildRepoLifecycleAdapters_EmptyWorkspace(t *testing.T) {
	cfg := &config.Config{}
	if adapters := BuildRepoLifecycleAdapters(context.Background(), cfg, ""); len(adapters) != 0 {
		t.Fatalf("adapters len = %d, want 0", len(adapters))
	}
}
