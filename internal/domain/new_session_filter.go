package domain

import "time"

// NewSessionFilter persists a named New Session filter for a workspace/provider.
type NewSessionFilter struct {
	ID          string
	WorkspaceID string
	Name        string
	Provider    string
	Criteria    NewSessionFilterCriteria
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// NewSessionFilterCriteria captures persisted New Session overlay filter criteria.
type NewSessionFilterCriteria struct {
	Scope      SelectionScope
	View       string
	State      string
	Search     string
	Labels     []string
	Owner      string
	Repository string
	Group      string
	TeamID     string
}

// NewSessionFilterLock coordinates lease ownership while applying a New Session filter.
type NewSessionFilterLock struct {
	FilterID       string
	InstanceID     string
	LeaseExpiresAt time.Time
	AcquiredAt     time.Time
	UpdatedAt      time.Time
}
