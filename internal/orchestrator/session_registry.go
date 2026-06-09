package orchestrator

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
)

// ErrSessionNotRunning indicates the target session is not in the registry.
var ErrSessionNotRunning = errors.New("session is not running or not registered")

// sessionRegistry maps session IDs to running adapter.AgentSession handles
// and foreman instances per work item.
// It is safe for concurrent use.
type sessionRegistry struct {
	mu       sync.RWMutex
	sessions map[string]adapter.AgentSession
	foremen  map[string]*Foreman // workItemID → foreman
}

// Verify sessionRegistry satisfies SessionRegistry interface.
var _ SessionRegistry = (*sessionRegistry)(nil)

func NewSessionRegistry() *sessionRegistry {
	return &sessionRegistry{
		sessions: make(map[string]adapter.AgentSession),
		foremen:  make(map[string]*Foreman),
	}
}

// Register adds a running session to the registry.
func (r *sessionRegistry) Register(sessionID string, session adapter.AgentSession) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[sessionID] = session
}

// Deregister removes a session from the registry.
func (r *sessionRegistry) Deregister(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, sessionID)
}

// SendMessage sends a follow-up message to a running session.
// Returns ErrSessionNotRunning if the session is not registered.
func (r *sessionRegistry) SendMessage(ctx context.Context, sessionID string, msg string) error {
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
func (r *sessionRegistry) Steer(ctx context.Context, sessionID string, msg string) error {
	r.mu.RLock()
	session, ok := r.sessions[sessionID]
	r.mu.RUnlock()
	if !ok {
		return ErrSessionNotRunning
	}
	return session.Steer(ctx, msg)
}

// SendAnswer sends an answer to resolve a pending question tool call.
// Returns ErrSessionNotRunning if the session is not registered.
func (r *sessionRegistry) SendAnswer(ctx context.Context, sessionID string, answer string) error {
	r.mu.RLock()
	session, ok := r.sessions[sessionID]
	r.mu.RUnlock()
	if !ok {
		return ErrSessionNotRunning
	}
	return session.SendAnswer(ctx, answer)
}

// IsRunning reports whether the given session ID is registered.
func (r *sessionRegistry) IsRunning(sessionID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.sessions[sessionID]
	return ok
}

// Registered returns the running session handle for sessionID when it is still registered.
func (r *sessionRegistry) Registered(sessionID string) (adapter.AgentSession, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	session, ok := r.sessions[sessionID]
	return session, ok
}

// AbortAndDeregister aborts the agent session identified by sessionID and removes
// it from the registry. If the session is not registered this is a no-op.
// Abort errors are logged but not returned because the caller's intent is to
// tear down the session unconditionally.
func (r *sessionRegistry) AbortAndDeregister(ctx context.Context, sessionID string) {
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
		slog.Warn("session abort during deregister", "agent_session_id", sessionID, "err", err)
	}
}

// RegisterForeman registers a foreman instance for a work item.
func (r *sessionRegistry) RegisterForeman(workItemID string, foreman *Foreman) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.foremen[workItemID] = foreman
}

// GetForeman returns the foreman for a work item, or nil if none exists.
func (r *sessionRegistry) GetForeman(workItemID string) *Foreman {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.foremen[workItemID]
}

// DeregisterForeman removes the foreman for a work item.
func (r *sessionRegistry) DeregisterForeman(workItemID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.foremen, workItemID)
}

const registryCloseTimeout = 30 * time.Second

// Close stops all foremen and aborts all sessions.
// It uses a durable context (ignores parent cancellation, has 30s timeout)
// to ensure graceful shutdown completes even if the app is exiting.
func (r *sessionRegistry) Close(ctx context.Context) {
	// Create a durable context that survives parent cancellation.
	stopCtx, stopCancel := context.WithTimeout(context.WithoutCancel(ctx), registryCloseTimeout)
	defer stopCancel()

	r.mu.Lock()
	// Copy foremen map to avoid holding lock during Stop calls.
	foremen := make(map[string]*Foreman, len(r.foremen))
	for k, v := range r.foremen {
		foremen[k] = v
	}
	// Copy sessions map for same reason.
	sessions := make(map[string]adapter.AgentSession, len(r.sessions))
	for k, v := range r.sessions {
		sessions[k] = v
	}
	// Clear maps immediately so subsequent calls see empty state.
	r.foremen = make(map[string]*Foreman)
	r.sessions = make(map[string]adapter.AgentSession)
	r.mu.Unlock()

	// Stop all foremen.
	for workItemID, foreman := range foremen {
		if foreman.IsRunning() {
			if err := foreman.Stop(stopCtx); err != nil {
				slog.Warn("foreman stop during registry close", "work_item_id", workItemID, "err", err)
			}
		}
	}

	// Abort all sessions.
	for sessionID, session := range sessions {
		if err := session.Abort(stopCtx); err != nil {
			slog.Warn("session abort during registry close", "agent_session_id", sessionID, "err", err)
		}
	}
}
