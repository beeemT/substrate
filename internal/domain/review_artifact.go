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

// GithubPullRequest is the durable row for a GitHub PR.
type GithubPullRequest struct {
	ID         string
	Owner      string
	Repo       string
	Number     int
	State      string
	Draft      bool
	HeadBranch string
	HTMLURL    string
	MergedAt   *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// GitlabMergeRequest is the durable row for a GitLab MR.
type GitlabMergeRequest struct {
	ID           string
	ProjectPath  string
	IID          int
	State        string
	Draft        bool
	SourceBranch string
	WebURL       string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// SessionReviewArtifact links a work item to a provider-specific PR/MR record.
type SessionReviewArtifact struct {
	ID                 string
	WorkspaceID        string
	WorkItemID         string
	Provider           string
	ProviderArtifactID string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}


// GithubPRReview is the durable row for a GitHub PR review.
type GithubPRReview struct {
	ID            string
	PRID          string
	ReviewerLogin string
	State         string    // "approved" | "changes_requested" | "commented" | "dismissed"
	SubmittedAt   time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// GitlabMRReview is the durable row for a GitLab MR review.
type GitlabMRReview struct {
	ID            string
	MRID          string
	ReviewerLogin string
	State         string    // "approved" | "changes_requested" | "unapproved"
	SubmittedAt   time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// GithubPRCheck is the durable row for a GitHub PR check run.
type GithubPRCheck struct {
	ID         string
	PRID       string
	Name       string
	Status     string // "queued" | "in_progress" | "completed"
	Conclusion string // "success" | "failure" | "neutral" | "cancelled" | "skipped" | "timed_out" | ...
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// GitlabMRCheck is the durable row for a GitLab MR pipeline job.
type GitlabMRCheck struct {
	ID         string
	MRID       string
	Name       string
	Status     string
	Conclusion string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}