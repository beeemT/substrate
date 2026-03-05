// Package app wires together adapters and services from configuration.
package app

import (
	"github.com/beeemT/substrate/internal/adapter"
	gladapter "github.com/beeemT/substrate/internal/adapter/glab"
	linearadapter "github.com/beeemT/substrate/internal/adapter/linear"
	manualadapter "github.com/beeemT/substrate/internal/adapter/manual"
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

	return adapters
}

// BuildRepoLifecycleAdapters constructs all RepoLifecycleAdapters.
// The glab adapter is always included regardless of configuration.
func BuildRepoLifecycleAdapters(cfg *config.Config) []adapter.RepoLifecycleAdapter {
	return []adapter.RepoLifecycleAdapter{
		gladapter.New(cfg.Adapters.Glab),
	}
}
