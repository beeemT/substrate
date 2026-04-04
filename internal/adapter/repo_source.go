package adapter

import "context"

// RepoSource provides remote repository listing for the Add Repo overlay.
type RepoSource interface {
	// Name returns the source identifier (e.g., "github", "gitlab", "manual").
	Name() string

	// ListRepos returns repositories available for cloning.
	ListRepos(ctx context.Context, opts RepoListOpts) (*RepoListResult, error)
}

// RepoListOpts controls repository listing behavior.
type RepoListOpts struct {
	Search string // text filter -- when non-empty, repo sources use their provider's search API
	Limit  int    // max results per page
	Page   int    // pagination offset
}

// RepoListResult holds a page of repository results.
type RepoListResult struct {
	Repos   []RepoItem
	HasMore bool
}

// RepoItem describes a single repository from a remote source.
type RepoItem struct {
	Name          string // e.g., "substrate"
	FullName      string // e.g., "beeemT/substrate"
	Description   string
	URL           string // clone URL (HTTPS)
	SSHURL        string // clone URL (SSH)
	DefaultBranch string
	IsPrivate     bool
	Source        string // provider name ("github", "gitlab")
	Owner         string // org/user name
}
