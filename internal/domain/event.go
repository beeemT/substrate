package domain

import "time"

// SystemEvent is a persisted system event for audit and replay.
type SystemEvent struct {
	ID          string
	EventType   string
	WorkspaceID string
	Payload     string
	CreatedAt   time.Time
}
