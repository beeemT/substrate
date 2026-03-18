package domain

type RepoRef struct {
	Provider  string `json:"provider"`
	Host      string `json:"host,omitempty"`
	Owner     string `json:"owner,omitempty"`
	Repo      string `json:"repo,omitempty"`
	ProjectID int64  `json:"project_id,omitempty"`
	URL       string `json:"url,omitempty"`
}

type ReviewRef struct {
	BaseRepo   RepoRef `json:"base_repo"`
	HeadRepo   RepoRef `json:"head_repo"`
	BaseBranch string  `json:"base_branch,omitempty"`
	HeadBranch string  `json:"head_branch,omitempty"`
}

type TrackerReference struct {
	Provider   string  `json:"provider"`
	Kind       string  `json:"kind"`
	ID         string  `json:"id"`
	URL        string  `json:"url,omitempty"`
	Owner      string  `json:"owner,omitempty"`
	Repo       string  `json:"repo,omitempty"`
	ProjectID  int64   `json:"project_id,omitempty"`
	Number     int64   `json:"number,omitempty"`
	Repository RepoRef `json:"repository,omitzero"`
}
