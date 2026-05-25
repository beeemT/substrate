package orchestrator

import (
	"context"

	"github.com/beeemT/substrate/internal/adapter"
)

// ============================================================================
// Foreman Interfaces
// ============================================================================

// ForemanLifecycle abstracts Foreman for use by orchestrators that need to
// control its lifecycle. Implemented by *Foreman.
type ForemanLifecycle interface {
	Start(ctx context.Context, planID string, followUpContext string) error
	Stop(ctx context.Context) error
	IsRunning() bool
}

// ============================================================================
// Session Registry Interface
// ============================================================================

// SessionRegistry abstracts session and foreman registration.
// The concrete implementation is *sessionRegistry; consumers use this interface.
type SessionRegistry interface {
	// Session management
	Register(sessionID string, session adapter.AgentSession)
	Deregister(sessionID string)
	SendMessage(ctx context.Context, sessionID string, msg string) error
	Steer(ctx context.Context, sessionID string, msg string) error
	SendAnswer(ctx context.Context, sessionID string, answer string) error
	IsRunning(sessionID string) bool
	Registered(sessionID string) (adapter.AgentSession, bool)
	AbortAndDeregister(ctx context.Context, sessionID string)

	// Foreman management (per work item)
	RegisterForeman(workItemID string, foreman *Foreman)
	GetForeman(workItemID string) *Foreman
	DeregisterForeman(workItemID string)

	// Close shuts down all registered sessions and foremen.
	// Called during application shutdown. Uses context.WithoutCancel to ensure
	// graceful shutdown completes even if the provided context is cancelled.
	Close(ctx context.Context)
}

// ============================================================================
// Answer Router Interface
// ============================================================================

// AnswerRouter routes human answers and skips back to the correct handler
// based on question stage. It delegates to SessionRegistry and *Foreman
// based on the question's phase, looking up the foreman dynamically per question.
type AnswerRouter interface {
	// Answer routes an answer based on the question's phase.
	// Publishes EventAgentQuestionAnswered on success.
	Answer(ctx context.Context, questionID, answer, answeredBy string) error

	// Skip routes a skip for a question based on its phase.
	// Publishes EventAgentQuestionAnswered on success.
	Skip(ctx context.Context, questionID string) error

	// RefineAnswer sends human follow-up text to get a revised answer proposal.
	// Returns the updated proposal so the UI can refresh.
	RefineAnswer(ctx context.Context, questionID, text string) (newProposal string, uncertain bool, err error)
}

// ============================================================================
// Compile-Time Interface Checks
// ============================================================================

var _ ForemanLifecycle = (*Foreman)(nil)

var _ SessionRegistry = (*sessionRegistry)(nil)
