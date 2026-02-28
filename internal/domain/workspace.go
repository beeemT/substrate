package domain

import "time"

// Workspace represents a pre-existing folder initialized with substrate init.
type Workspace struct {
	ID        string
	Name      string
	RootPath  string
	Status    WorkspaceStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}

// WorkspaceStatus represents the lifecycle state of a workspace.
type WorkspaceStatus string

const (
	WorkspaceCreating WorkspaceStatus = "creating"
	WorkspaceReady    WorkspaceStatus = "ready"
	WorkspaceArchived WorkspaceStatus = "archived"
	WorkspaceError    WorkspaceStatus = "error"
)
