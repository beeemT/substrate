package domain

import "time"

// SourceMetadataField is one durable metadata row for a source item snapshot.
type SourceMetadataField struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// SourceSummary is a durable per-source-item snapshot for sessions sourced from trackers.
type SourceSummary struct {
	Provider    string                `json:"provider"`
	Kind        string                `json:"kind,omitempty"`
	Ref         string                `json:"ref"`
	Title       string                `json:"title,omitempty"`
	Description string                `json:"description,omitempty"`
	Excerpt     string                `json:"excerpt,omitempty"`
	State       string                `json:"state,omitempty"`
	Labels      []string              `json:"labels,omitempty"`
	Container   string                `json:"container,omitempty"`
	URL         string                `json:"url,omitempty"`
	CreatedAt   *time.Time            `json:"created_at,omitempty"`
	UpdatedAt   *time.Time            `json:"updated_at,omitempty"`
	Metadata    []SourceMetadataField `json:"metadata,omitempty"`
}
