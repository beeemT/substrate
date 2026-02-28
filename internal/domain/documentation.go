package domain

import "time"

// DocumentationSource is an abstracted reference to documentation.
type DocumentationSource struct {
	ID             string
	WorkspaceID    string
	RepositoryName string
	Type           DocumentationSourceType
	Path           string
	RepoURL        string
	Branch         string
	Description    string
	LastSyncedAt   *time.Time
	CreatedAt      time.Time
}

// DocumentationSourceType identifies how documentation is sourced.
type DocumentationSourceType string

const (
	DocSourceRepoEmbedded  DocumentationSourceType = "repo_embedded"
	DocSourceDedicatedRepo DocumentationSourceType = "dedicated_repo"
)
