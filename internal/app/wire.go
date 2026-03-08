// Package app wires together adapters and services from configuration.
package app

import (
	"context"
	"log/slog"

	"github.com/beeemT/substrate/internal/adapter"
	githubadapter "github.com/beeemT/substrate/internal/adapter/github"
	gitlabadapter "github.com/beeemT/substrate/internal/adapter/gitlab"
	gladapter "github.com/beeemT/substrate/internal/adapter/glab"
	linearadapter "github.com/beeemT/substrate/internal/adapter/linear"
	manualadapter "github.com/beeemT/substrate/internal/adapter/manual"
	"github.com/beeemT/substrate/internal/app/remotedetect"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/repository"
)

// BuildWorkItemAdapters constructs all available WorkItemAdapters for the given
// configuration and workspace. The manual adapter is always included. The linear
// adapter is included when an API key is present in configuration.
//
// repo is used to back the ManualAdapter's WorkspaceStore; it is typically a
// transaction-bound WorkItemRepository from the enclosing Transact call so that
// the ID counter and subsequent WorkItem.Create share the same transaction.
func BuildWorkItemAdapters(
	cfg *config.Config,
	workspaceID string,
	repo repository.WorkItemRepository,
) []adapter.WorkItemAdapter {
	store := manualadapter.NewWorkspaceStore(repo, workspaceID)
	adapters := []adapter.WorkItemAdapter{
		manualadapter.New(store, workspaceID),
	}

	if cfg.Adapters.Linear.APIKey != "" {
		adapters = append(adapters, linearadapter.New(cfg.Adapters.Linear))
	}
	if cfg.Adapters.GitLab.Token != "" {
		gitlabAdapter, err := gitlabadapter.New(context.Background(), cfg.Adapters.GitLab)
		if err != nil {
			slog.Warn("skipping gitlab work item adapter", "err", err)
		} else {
			adapters = append(adapters, gitlabAdapter)
		}
	}
	if cfg.Adapters.GitHub.Token != "" || cfg.Adapters.GitHub.TokenRef != "" {
		githubAdapter, err := githubadapter.New(context.Background(), cfg.Adapters.GitHub)
		if err != nil {
			slog.Warn("skipping github work item adapter", "err", err)
		} else {
			adapters = append(adapters, githubAdapter)
		}
	}

	return adapters
}

// BuildRepoLifecycleAdapters constructs repo lifecycle adapters for the current workspace.
func BuildRepoLifecycleAdapters(ctx context.Context, cfg *config.Config, workspaceDir string) []adapter.RepoLifecycleAdapter {
	if workspaceDir == "" {
		slog.Warn("skipping repo lifecycle adapters: workspace directory is empty")
		return nil
	}

	reviewCtx, err := remotedetect.ResolveReviewContext(ctx, workspaceDir)
	if err != nil {
		slog.Warn("failed to resolve review context; no repo lifecycle adapters registered", "workspace_dir", workspaceDir, "err", err)
		return nil
	}

	switch reviewCtx.Platform {
	case remotedetect.PlatformGitLab:
		return []adapter.RepoLifecycleAdapter{gladapter.New(cfg.Adapters.Glab)}
	case remotedetect.PlatformGitHub:
		githubAdapter, err := githubadapter.New(ctx, cfg.Adapters.GitHub)
		if err != nil {
			slog.Warn("skipping github lifecycle adapter", "err", err)
			return nil
		}
		return []adapter.RepoLifecycleAdapter{githubAdapter}
	default:
		slog.Warn("skipping repo lifecycle adapters: remote platform is unknown", "workspace_dir", workspaceDir)
		return nil
	}
}
