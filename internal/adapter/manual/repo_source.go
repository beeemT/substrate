package manual

import (
	"context"

	"github.com/beeemT/substrate/internal/adapter"
)

// ManualRepoSource is a repo source that provides no remote listing.
// Manual URL entry is handled by the overlay's text input, not the adapter.
type ManualRepoSource struct{}

// NewRepoSource creates a new ManualRepoSource.
func NewRepoSource() *ManualRepoSource {
	return &ManualRepoSource{}
}

// Name returns the source identifier.
func (m *ManualRepoSource) Name() string { return "manual" }

// ListRepos returns an empty result. Manual input is handled by the overlay.
func (m *ManualRepoSource) ListRepos(_ context.Context, _ adapter.RepoListOpts) (*adapter.RepoListResult, error) {
	return &adapter.RepoListResult{Repos: []adapter.RepoItem{}}, nil
}
