package domain

import "time"

// ReviewArtifact is durable UI-facing PR/MR metadata for a repo task.
type ReviewArtifact struct {
	Provider     string    `json:"provider"`
	Kind         string    `json:"kind"`
	RepoName     string    `json:"repo_name"`
	Ref          string    `json:"ref"`
	URL          string    `json:"url,omitempty"`
	State        string    `json:"state,omitempty"`
	Branch       string    `json:"branch,omitempty"`
	WorktreePath string    `json:"worktree_path,omitempty"`
	Draft        bool      `json:"draft,omitempty"`
	UpdatedAt    time.Time `json:"updated_at,omitzero"`
}

// ReviewArtifactEventPayload persists a review artifact against a work item.
type ReviewArtifactEventPayload struct {
	WorkItemID string         `json:"work_item_id"`
	Artifact   ReviewArtifact `json:"artifact"`
}
