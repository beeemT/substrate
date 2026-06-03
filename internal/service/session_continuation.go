package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/beeemT/go-atomic"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

// AgentSessionContinuationService owns durable lifecycle state for post-agent
// continuation work.
type AgentSessionContinuationService struct {
	transacter atomic.Transacter[repository.Resources]
}

func NewAgentSessionContinuationService(transacter atomic.Transacter[repository.Resources]) *AgentSessionContinuationService {
	return &AgentSessionContinuationService{transacter: transacter}
}

func (s *AgentSessionContinuationService) CreatePending(ctx context.Context, agentSessionID, kind string) (domain.AgentSessionContinuation, error) {
	var created domain.AgentSessionContinuation
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		session, err := res.AgentSessions.Get(ctx, agentSessionID)
		if err != nil {
			return newNotFoundError("agent session", agentSessionID)
		}

		active, err := res.AgentSessionContinuations.GetActive(ctx, agentSessionID, kind)
		if err == nil {
			created = active
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, repository.ErrNotFound) {
			return fmt.Errorf("get active continuation for session %s kind %s: %w", agentSessionID, kind, err)
		}

		now := time.Now()
		created = domain.AgentSessionContinuation{
			ID:             domain.NewID(),
			AgentSessionID: agentSessionID,
			WorkItemID:     session.WorkItemID,
			SubPlanID:      session.SubPlanID,
			Kind:           kind,
			Status:         domain.AgentSessionContinuationPending,
			Attempt:        1,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		return res.AgentSessionContinuations.Create(ctx, created)
	})
	if err != nil {
		slog.Error("create pending agent session continuation failed", "agent_session_id", agentSessionID, "kind", kind, "error", err)
		return domain.AgentSessionContinuation{}, err
	}
	return created, nil
}

func (s *AgentSessionContinuationService) GetActive(ctx context.Context, agentSessionID, kind string) (domain.AgentSessionContinuation, error) {
	var continuation domain.AgentSessionContinuation
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		var err error
		continuation, err = res.AgentSessionContinuations.GetActive(ctx, agentSessionID, kind)
		return err
	})
	if err != nil {
		return domain.AgentSessionContinuation{}, err
	}
	return continuation, nil
}

func (s *AgentSessionContinuationService) Start(ctx context.Context, continuationID string) (domain.AgentSessionContinuation, error) {
	return s.transition(ctx, continuationID, domain.AgentSessionContinuationRunning, "")
}

func (s *AgentSessionContinuationService) Complete(ctx context.Context, continuationID string) (domain.AgentSessionContinuation, error) {
	return s.transition(ctx, continuationID, domain.AgentSessionContinuationCompleted, "")
}

func (s *AgentSessionContinuationService) Fail(ctx context.Context, continuationID string, cause error) (domain.AgentSessionContinuation, error) {
	lastError := ""
	if cause != nil {
		lastError = cause.Error()
	}
	return s.transition(ctx, continuationID, domain.AgentSessionContinuationFailed, lastError)
}

func (s *AgentSessionContinuationService) ListRecoverable(ctx context.Context, workspaceID string) ([]domain.AgentSessionContinuation, error) {
	var continuations []domain.AgentSessionContinuation
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		var err error
		continuations, err = res.AgentSessionContinuations.ListRecoverable(ctx, workspaceID)
		return err
	})
	if err != nil {
		slog.Error("list recoverable agent session continuations failed", "workspace_id", workspaceID, "error", err)
		return nil, err
	}
	return continuations, nil
}

func (s *AgentSessionContinuationService) transition(ctx context.Context, id string, status domain.AgentSessionContinuationStatus, lastError string) (domain.AgentSessionContinuation, error) {
	var continuation domain.AgentSessionContinuation
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		current, err := res.AgentSessionContinuations.Get(ctx, id)
		if err != nil {
			return newNotFoundError("agent session continuation", id)
		}
		if !domain.CanTransitionAgentSessionContinuation(current.Status, status) {
			return newInvalidTransitionError(string(current.Status), string(status), "agent session continuation")
		}

		now := time.Now()
		current.Status = status
		current.LastError = lastError
		current.UpdatedAt = now
		switch status {
		case domain.AgentSessionContinuationRunning:
			current.StartedAt = &now
			current.CompletedAt = nil
		case domain.AgentSessionContinuationCompleted, domain.AgentSessionContinuationFailed, domain.AgentSessionContinuationSkipped, domain.AgentSessionContinuationSuperseded:
			current.CompletedAt = &now
		}
		if err := res.AgentSessionContinuations.Update(ctx, current); err != nil {
			return err
		}
		continuation = current
		return nil
	})
	if err != nil {
		slog.Error("agent session continuation transition failed", "continuation_id", id, "status", status, "error", err)
		return domain.AgentSessionContinuation{}, err
	}
	return continuation, nil
}
