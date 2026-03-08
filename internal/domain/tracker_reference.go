package domain

type TrackerReference struct {
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	URL      string `json:"url,omitempty"`
	Owner    string `json:"owner,omitempty"`
	Repo     string `json:"repo,omitempty"`
	Number   int64  `json:"number,omitempty"`
}
