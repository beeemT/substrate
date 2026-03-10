// Package app wires together adapters and services from configuration.
package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/beeemT/substrate/internal/adapter"
	githubadapter "github.com/beeemT/substrate/internal/adapter/github"
	gitlabadapter "github.com/beeemT/substrate/internal/adapter/gitlab"
	gladapter "github.com/beeemT/substrate/internal/adapter/glab"
	linearadapter "github.com/beeemT/substrate/internal/adapter/linear"
	manualadapter "github.com/beeemT/substrate/internal/adapter/manual"
	"github.com/beeemT/substrate/internal/app/remotedetect"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/gitwork"
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
	if config.GitHubAuthConfigured(cfg.Adapters.GitHub) {
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
		return nil
	}

	platforms, err := detectWorkspaceLifecyclePlatforms(ctx, cfg, workspaceDir)
	if err != nil {
		slog.Warn("failed to detect repo lifecycle platforms; no repo lifecycle adapters registered", "workspace_dir", workspaceDir, "err", err)
		return nil
	}

	adapters := make([]adapter.RepoLifecycleAdapter, 0, len(platforms))
	for _, platform := range platforms {
		switch platform {
		case remotedetect.PlatformGitLab:
			adapters = append(adapters, gladapter.New(cfg.Adapters.Glab))
		case remotedetect.PlatformGitHub:
			if !config.GitHubAuthConfigured(cfg.Adapters.GitHub) {
				slog.Warn("skipping github lifecycle adapter: no github auth configured")
				continue
			}
			githubAdapter, err := githubadapter.New(ctx, cfg.Adapters.GitHub)
			if err != nil {
				slog.Warn("skipping github lifecycle adapter", "err", err)
				continue
			}
			adapters = append(adapters, githubAdapter)
		default:
			slog.Warn("skipping repo lifecycle adapters: remote platform is unknown", "workspace_dir", workspaceDir)
		}
	}
	return adapters
}

func detectWorkspaceLifecyclePlatforms(ctx context.Context, cfg *config.Config, workspaceDir string) ([]remotedetect.Platform, error) {
	repoPaths, err := gitwork.DiscoverRepos(workspaceDir)
	if err != nil {
		return nil, fmt.Errorf("discover workspace repos: %w", err)
	}
	if len(repoPaths) == 0 {
		return nil, fmt.Errorf("no git-work repos found in workspace %s", workspaceDir)
	}

	platforms := make([]remotedetect.Platform, 0, 2)
	seen := make(map[remotedetect.Platform]struct{}, 2)
	var firstErr error
	for _, repoPath := range repoPaths {
		platform, err := remotedetect.DetectPlatform(ctx, repoPath, cfg)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("detect platform for %s: %w", repoPath, err)
			}
			continue
		}
		if platform == remotedetect.PlatformUnknown {
			continue
		}
		if _, ok := seen[platform]; ok {
			continue
		}
		seen[platform] = struct{}{}
		platforms = append(platforms, platform)
	}

	if len(platforms) > 0 {
		return platforms, nil
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return nil, fmt.Errorf("no supported repo lifecycle platform detected in workspace %s", workspaceDir)
}
