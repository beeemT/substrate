package domain

import "time"

// SubstrateInstance is a running Substrate process registered for a workspace.
type SubstrateInstance struct {
	ID            string
	WorkspaceID   string
	PID           int
	Hostname      string
	LastHeartbeat time.Time
	StartedAt     time.Time
}
