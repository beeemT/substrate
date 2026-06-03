package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

type mockSessionContinuationRepository struct {
	items map[string]domain.AgentSessionContinuation
}

func newMockSessionContinuationRepository() *mockSessionContinuationRepository {
	return &mockSessionContinuationRepository{items: make(map[string]domain.AgentSessionContinuation)}
}

func (m *mockSessionContinuationRepository) Get(_ context.Context, id string) (domain.AgentSessionContinuation, error) {
	c, ok := m.items[id]
	if !ok {
		return domain.AgentSessionContinuation{}, repository.ErrNotFound
	}
	return c, nil
}

func (m *mockSessionContinuationRepository) GetActive(_ context.Context, agentSessionID, kind string) (domain.AgentSessionContinuation, error) {
	var latest domain.AgentSessionContinuation
	found := false
	for _, c := range m.items {
		if c.AgentSessionID != agentSessionID || c.Kind != kind {
			continue
		}
		switch c.Status {
		case domain.AgentSessionContinuationPending, domain.AgentSessionContinuationRunning, domain.AgentSessionContinuationFailed:
		default:
			continue
		}
		if !found || c.Attempt > latest.Attempt {
			latest = c
			found = true
		}
	}
	if !found {
		return domain.AgentSessionContinuation{}, repository.ErrNotFound
	}
	return latest, nil
}

func (m *mockSessionContinuationRepository) ListRecoverable(_ context.Context, workspaceID string) ([]domain.AgentSessionContinuation, error) {
	var out []domain.AgentSessionContinuation
	for _, c := range m.items {
		if c.WorkItemID == "" || workspaceID == "" {
			continue
		}
		switch c.Status {
		case domain.AgentSessionContinuationPending, domain.AgentSessionContinuationRunning, domain.AgentSessionContinuationFailed:
			out = append(out, c)
		}
	}
	return out, nil
}

func (m *mockSessionContinuationRepository) Create(_ context.Context, c domain.AgentSessionContinuation) error {
	if _, ok := m.items[c.ID]; ok {
		return errors.New("duplicate continuation")
	}
	m.items[c.ID] = c
	return nil
}

func (m *mockSessionContinuationRepository) Update(_ context.Context, c domain.AgentSessionContinuation) error {
	if _, ok := m.items[c.ID]; !ok {
		return repository.ErrNotFound
	}
	m.items[c.ID] = c
	return nil
}

func TestAgentSessionContinuationService_CreatePendingIsIdempotent(t *testing.T) {
	ctx := context.Background()
	sessions := NewMockSessionRepository()
	continuations := newMockSessionContinuationRepository()
	session := implSession("agent-1", "wi-1", "ws-1", "sp-1", domain.AgentSessionCompleted)
	if err := sessions.Create(ctx, session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	svc := NewAgentSessionContinuationService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: sessions, AgentSessionContinuations: continuations}})

	first, err := svc.CreatePending(ctx, session.ID, "implementation_review")
	if err != nil {
		t.Fatalf("CreatePending first failed: %v", err)
	}
	second, err := svc.CreatePending(ctx, session.ID, "implementation_review")
	if err != nil {
		t.Fatalf("CreatePending second failed: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("CreatePending created duplicate active continuation: first=%s second=%s", first.ID, second.ID)
	}
	if first.WorkItemID != session.WorkItemID || first.SubPlanID != session.SubPlanID || first.Status != domain.AgentSessionContinuationPending {
		t.Fatalf("continuation = %+v, want session identifiers and pending status", first)
	}
}

func TestAgentSessionContinuationService_TransitionsAndFailureError(t *testing.T) {
	ctx := context.Background()
	continuations := newMockSessionContinuationRepository()
	now := time.Now()
	initial := domain.AgentSessionContinuation{
		ID:             "cont-1",
		AgentSessionID: "agent-1",
		WorkItemID:     "wi-1",
		SubPlanID:      "sp-1",
		Kind:           "implementation_review",
		Status:         domain.AgentSessionContinuationPending,
		Attempt:        1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := continuations.Create(ctx, initial); err != nil {
		t.Fatalf("seed continuation: %v", err)
	}
	svc := NewAgentSessionContinuationService(repository.NoopTransacter{Res: repository.Resources{AgentSessionContinuations: continuations}})

	running, err := svc.Start(ctx, initial.ID)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if running.Status != domain.AgentSessionContinuationRunning || running.StartedAt == nil {
		t.Fatalf("running continuation = %+v, want running with StartedAt", running)
	}

	failed, err := svc.Fail(ctx, initial.ID, errors.New("review failed"))
	if err != nil {
		t.Fatalf("Fail failed: %v", err)
	}
	if failed.Status != domain.AgentSessionContinuationFailed || failed.LastError != "review failed" || failed.CompletedAt == nil {
		t.Fatalf("failed continuation = %+v, want failed with LastError and CompletedAt", failed)
	}
}

func TestAgentSessionContinuationService_RejectsInvalidTransition(t *testing.T) {
	ctx := context.Background()
	continuations := newMockSessionContinuationRepository()
	now := time.Now()
	initial := domain.AgentSessionContinuation{ID: "cont-1", Status: domain.AgentSessionContinuationCompleted, Attempt: 1, CreatedAt: now, UpdatedAt: now}
	if err := continuations.Create(ctx, initial); err != nil {
		t.Fatalf("seed continuation: %v", err)
	}
	svc := NewAgentSessionContinuationService(repository.NoopTransacter{Res: repository.Resources{AgentSessionContinuations: continuations}})

	_, err := svc.Start(ctx, initial.ID)
	if err == nil {
		t.Fatal("expected invalid transition error")
	}
	if _, ok := err.(ErrInvalidTransition); !ok {
		t.Fatalf("error type = %T, want ErrInvalidTransition", err)
	}
}
