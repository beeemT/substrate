package domain

// SourceSummary is a durable per-source-item summary for aggregated sessions.
type SourceSummary struct {
	Provider string `json:"provider"`
	Ref      string `json:"ref"`
	Title    string `json:"title,omitempty"`
	Excerpt  string `json:"excerpt,omitempty"`
	URL      string `json:"url,omitempty"`
}
