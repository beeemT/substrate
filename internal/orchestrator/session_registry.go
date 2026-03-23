package orchestrator

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/beeemT/substrate/internal/adapter"
)

// ErrSessionNotRunning indicates the target session is not in the registry.
var ErrSessionNotRunning = errors.New("session is not running or not registered")

// SessionRegistry maps session IDs to running adapter.AgentSession handles.
// It is safe for concurrent use.
type SessionRegistry struct {
	mu       sync.RWMutex
	sessions map[string]adapter.AgentSession
}

func NewSessionRegistry() *SessionRegistry {
	return &SessionRegistry{sessions: make(map[string]adapter.AgentSession)}
}

// Register adds a running session to the registry.
func (r *SessionRegistry) Register(sessionID string, session adapter.AgentSession) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[sessionID] = session
}

// Deregister removes a session from the registry.
func (r *SessionRegistry) Deregister(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, sessionID)
}

// SendMessage sends a follow-up message to a running session.
// Returns ErrSessionNotRunning if the session is not registered.
func (r *SessionRegistry) SendMessage(ctx context.Context, sessionID string, msg string) error {
	r.mu.RLock()
	session, ok := r.sessions[sessionID]
	r.mu.RUnlock()
	if !ok {
		return ErrSessionNotRunning
	}
	return session.SendMessage(ctx, msg)
}

// Steer sends a steering prompt that interrupts a running session's active streaming turn.
// Returns ErrSessionNotRunning if the session is not registered.
func (r *SessionRegistry) Steer(ctx context.Context, sessionID string, msg string) error {
	r.mu.RLock()
	session, ok := r.sessions[sessionID]
	r.mu.RUnlock()
	if !ok {
		return ErrSessionNotRunning
	}
	return session.Steer(ctx, msg)
}

// IsRunning reports whether the given session ID is registered.
func (r *SessionRegistry) IsRunning(sessionID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.sessions[sessionID]
	return ok
}

// AbortAndDeregister aborts the agent session identified by sessionID and removes
// it from the registry. If the session is not registered this is a no-op.
// Abort errors are logged but not returned because the caller's intent is to
// tear down the session unconditionally.
func (r *SessionRegistry) AbortAndDeregister(ctx context.Context, sessionID string) {
	r.mu.Lock()
	session, ok := r.sessions[sessionID]
	if ok {
		delete(r.sessions, sessionID)
	}
	r.mu.Unlock()
	if !ok {
		return
	}
	if err := session.Abort(ctx); err != nil {
		slog.Warn("session abort during deregister", "session_id", sessionID, "err", err)
	}
}
