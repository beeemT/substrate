package service

import (
	"context"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

// SessionService provides business logic for agent sessions.
type SessionService struct {
	repo repository.SessionRepository
}

// NewSessionService creates a new SessionService.
func NewSessionService(repo repository.SessionRepository) *SessionService {
	return &SessionService{repo: repo}
}

// AgentSession state transitions
var validSessionTransitions = map[domain.AgentSessionStatus][]domain.AgentSessionStatus{
	domain.AgentSessionPending:          {domain.AgentSessionRunning, domain.AgentSessionFailed},
	domain.AgentSessionRunning:          {domain.AgentSessionWaitingForAnswer, domain.AgentSessionCompleted, domain.AgentSessionInterrupted, domain.AgentSessionFailed},
	domain.AgentSessionWaitingForAnswer: {domain.AgentSessionRunning, domain.AgentSessionFailed},
	domain.AgentSessionCompleted:        {}, // Terminal state
	domain.AgentSessionInterrupted:      {domain.AgentSessionRunning, domain.AgentSessionFailed},
	domain.AgentSessionFailed:           {}, // Terminal state
}

func canTransitionSession(from, to domain.AgentSessionStatus) bool {
	allowed, exists := validSessionTransitions[from]
	if !exists {
		return false
	}
	for _, s := range allowed {
		if s == to {
			return true
		}
	}
	return false
}

// Get retrieves a session by ID.
func (s *SessionService) Get(ctx context.Context, id string) (domain.AgentSession, error) {
	session, err := s.repo.Get(ctx, id)
	if err != nil {
		return domain.AgentSession{}, newNotFoundError("session", id)
	}
	return session, nil
}

// ListBySubPlanID retrieves all sessions for a sub-plan.
func (s *SessionService) ListBySubPlanID(ctx context.Context, subPlanID string) ([]domain.AgentSession, error) {
	return s.repo.ListBySubPlanID(ctx, subPlanID)
}

// ListByWorkspaceID retrieves all sessions for a workspace.
func (s *SessionService) ListByWorkspaceID(ctx context.Context, workspaceID string) ([]domain.AgentSession, error) {
	return s.repo.ListByWorkspaceID(ctx, workspaceID)
}

// Create creates a new session in pending status.
func (s *SessionService) Create(ctx context.Context, session domain.AgentSession) error {
	// Set initial status if not set
	if session.Status == "" {
		session.Status = domain.AgentSessionPending
	}
	// Validate initial status
	if session.Status != domain.AgentSessionPending {
		return newInvalidInputError("initial status must be pending", "status")
	}
	// Set timestamps
	now := time.Now()
	session.CreatedAt = now
	session.UpdatedAt = now

	return s.repo.Create(ctx, session)
}

// Transition transitions a session to a new status.
func (s *SessionService) Transition(ctx context.Context, id string, to domain.AgentSessionStatus) error {
	session, err := s.repo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("session", id)
	}

	if !canTransitionSession(session.Status, to) {
		return newInvalidTransitionError(
			sessionStatusName(session.Status),
			sessionStatusName(to),
			"session",
		)
	}

	session.Status = to
	session.UpdatedAt = time.Now()

	return s.repo.Update(ctx, session)
}

// Start transitions a session from pending to running.
func (s *SessionService) Start(ctx context.Context, id string) error {
	session, err := s.repo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("session", id)
	}

	if !canTransitionSession(session.Status, domain.AgentSessionRunning) {
		return newInvalidTransitionError(
			sessionStatusName(session.Status),
			sessionStatusName(domain.AgentSessionRunning),
			"session",
		)
	}

	now := time.Now()
	session.Status = domain.AgentSessionRunning
	session.StartedAt = &now
	session.UpdatedAt = now

	return s.repo.Update(ctx, session)
}

// WaitForAnswer transitions a session from running to waiting_for_answer.
func (s *SessionService) WaitForAnswer(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.AgentSessionWaitingForAnswer)
}

// ResumeFromAnswer transitions a session from waiting_for_answer to running.
func (s *SessionService) ResumeFromAnswer(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.AgentSessionRunning)
}

// Complete transitions a session from running to completed.
func (s *SessionService) Complete(ctx context.Context, id string) error {
	session, err := s.repo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("session", id)
	}

	if !canTransitionSession(session.Status, domain.AgentSessionCompleted) {
		return newInvalidTransitionError(
			sessionStatusName(session.Status),
			sessionStatusName(domain.AgentSessionCompleted),
			"session",
		)
	}

	now := time.Now()
	session.Status = domain.AgentSessionCompleted
	session.CompletedAt = &now
	session.UpdatedAt = now

	return s.repo.Update(ctx, session)
}

// Interrupt transitions a session from running to interrupted.
func (s *SessionService) Interrupt(ctx context.Context, id string) error {
	session, err := s.repo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("session", id)
	}

	if !canTransitionSession(session.Status, domain.AgentSessionInterrupted) {
		return newInvalidTransitionError(
			sessionStatusName(session.Status),
			sessionStatusName(domain.AgentSessionInterrupted),
			"session",
		)
	}

	now := time.Now()
	session.Status = domain.AgentSessionInterrupted
	session.ShutdownAt = &now
	session.UpdatedAt = now

	return s.repo.Update(ctx, session)
}

// Resume transitions a session from interrupted to running.
func (s *SessionService) Resume(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.AgentSessionRunning)
}

// Fail transitions a session to failed.
func (s *SessionService) Fail(ctx context.Context, id string, exitCode *int) error {
	session, err := s.repo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("session", id)
	}

	if !canTransitionSession(session.Status, domain.AgentSessionFailed) {
		return newInvalidTransitionError(
			sessionStatusName(session.Status),
			sessionStatusName(domain.AgentSessionFailed),
			"session",
		)
	}

	now := time.Now()
	session.Status = domain.AgentSessionFailed
	session.CompletedAt = &now
	session.ExitCode = exitCode
	session.UpdatedAt = now

	return s.repo.Update(ctx, session)
}

// UpdateOwnerInstance updates the owner instance ID for a session.
func (s *SessionService) UpdateOwnerInstance(ctx context.Context, id string, instanceID string) error {
	session, err := s.repo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("session", id)
	}

	session.OwnerInstanceID = &instanceID
	session.UpdatedAt = time.Now()

	return s.repo.Update(ctx, session)
}

// UpdatePID updates the PID for a session.
func (s *SessionService) UpdatePID(ctx context.Context, id string, pid int) error {
	session, err := s.repo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("session", id)
	}

	session.PID = &pid
	session.UpdatedAt = time.Now()

	return s.repo.Update(ctx, session)
}

// Delete deletes a session.
func (s *SessionService) Delete(ctx context.Context, id string) error {
	_, err := s.repo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("session", id)
	}
	return s.repo.Delete(ctx, id)
}

// FindInterruptedByWorkspace finds all interrupted sessions for a workspace.
func (s *SessionService) FindInterruptedByWorkspace(ctx context.Context, workspaceID string) ([]domain.AgentSession, error) {
	sessions, err := s.repo.ListByWorkspaceID(ctx, workspaceID)
	if err != nil {
		return nil, err
	}

	var interrupted []domain.AgentSession
	for _, s := range sessions {
		if s.Status == domain.AgentSessionInterrupted {
			interrupted = append(interrupted, s)
		}
	}
	return interrupted, nil
}

// FindRunningByOwner finds all running sessions owned by an instance.
func (s *SessionService) FindRunningByOwner(ctx context.Context, instanceID string) ([]domain.AgentSession, error) {
	sessions, err := s.repo.ListByOwnerInstanceID(ctx, instanceID)
	if err != nil {
		return nil, err
	}

	var running []domain.AgentSession
	for _, session := range sessions {
		if session.Status == domain.AgentSessionRunning {
			running = append(running, session)
		}
	}
	return running, nil
}
