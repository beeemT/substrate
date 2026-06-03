package orchestrator

import (
	"context"
	"log/slog"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/service"
)

func failSessionDurably(parent context.Context, sessionSvc *service.AgentSessionService, sessionID string, exitCode *int) error {
	cleanupCtx, cleanupCancel := durableCleanupContext(parent)
	defer cleanupCancel()
	return sessionSvc.Fail(cleanupCtx, sessionID, exitCode)
}

func agentSessionAlreadyInterrupted(parent context.Context, sessionSvc *service.AgentSessionService, sessionID string) bool {
	cleanupCtx, cleanupCancel := durableCleanupContext(parent)
	defer cleanupCancel()

	agentSession, err := sessionSvc.Get(cleanupCtx, sessionID)
	if err != nil {
		slog.Warn("failed to inspect agent session before failure transition", "error", err, "agent_session_id", sessionID)
		return false
	}
	return agentSession.Status == domain.AgentSessionInterrupted
}

// interruptSessionDurably marks a session as interrupted using a context detached from
// parent cancellation, ensuring the DB write completes even if the pipeline is shutting down.
func interruptSessionDurably(parent context.Context, sessionSvc *service.AgentSessionService, sessionID string) error {
	cleanupCtx, cleanupCancel := durableCleanupContext(parent)
	defer cleanupCancel()
	return sessionSvc.Interrupt(cleanupCtx, sessionID)
}

func completeSessionDurably(parent context.Context, sessionSvc *service.AgentSessionService, sessionID string) error {
	cleanupCtx, cleanupCancel := durableCleanupContext(parent)
	defer cleanupCancel()
	return sessionSvc.Complete(cleanupCtx, sessionID)
}
