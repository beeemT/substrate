package service

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/beeemT/go-atomic"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/repository"
)

// AgentSessionService provides business logic for agent sessions.
type AgentSessionService struct {
	transacter atomic.Transacter[repository.Resources]
	eventBus   event.Publisher
}

// agentSessionEventPayload holds the JSON payload for agent session lifecycle events.
type agentSessionEventPayload struct {
	Session    domain.AgentSession `json:"session"`
	WorkItemID string              `json:"work_item_id"` // flat fields so TUI extractors don't need nested navigation
	SessionID  string              `json:"agent_session_id"`
}

// marshalAgentSessionPayload serializes an agent session event payload to JSON.
// work_item_id and agent_session_id are included at the top level so TUI extractors
// can read them without needing to navigate into the nested session object.
func marshalAgentSessionPayload(agentSession domain.AgentSession) string {
	p := agentSessionEventPayload{
		Session:    agentSession,
		WorkItemID: agentSession.WorkItemID,
		SessionID:  agentSession.ID,
	}
	b, _ := json.Marshal(p)
	return string(b)
}

// agentSessionStatusChangedPayload holds the JSON payload for generic agent session status changes.
type agentSessionStatusChangedPayload struct {
	Session    domain.AgentSession       `json:"session"`
	WorkItemID string                    `json:"work_item_id"`
	SessionID  string                    `json:"agent_session_id"`
	From       domain.AgentSessionStatus `json:"from"`
	To         domain.AgentSessionStatus `json:"to"`
}

// marshalAgentSessionStatusChangedPayload serializes a generic agent session status change payload.
func marshalAgentSessionStatusChangedPayload(agentSession domain.AgentSession, from, to domain.AgentSessionStatus) string {
	p := agentSessionStatusChangedPayload{
		Session:    agentSession,
		WorkItemID: agentSession.WorkItemID,
		SessionID:  agentSession.ID,
		From:       from,
		To:         to,
	}
	b, _ := json.Marshal(p)
	return string(b)
}

func NewAgentSessionService(transacter atomic.Transacter[repository.Resources], eventBus event.Publisher) *AgentSessionService {
	return &AgentSessionService{transacter: transacter, eventBus: eventBus}
}

// agentSession state transitions.
var validAgentSessionTransitions = map[domain.AgentSessionStatus][]domain.AgentSessionStatus{
	domain.AgentSessionPending: {domain.AgentSessionRunning, domain.AgentSessionFailed},
	domain.AgentSessionRunning: {
		domain.AgentSessionWaitingForAnswer,
		domain.AgentSessionCompleted,
		domain.AgentSessionInterrupted,
		domain.AgentSessionFailed,
	},
	domain.AgentSessionWaitingForAnswer: {domain.AgentSessionRunning, domain.AgentSessionFailed, domain.AgentSessionInterrupted},
	domain.AgentSessionCompleted:        {domain.AgentSessionRunning},
	domain.AgentSessionInterrupted:      {domain.AgentSessionRunning, domain.AgentSessionFailed},
	domain.AgentSessionFailed:           {domain.AgentSessionRunning},
}

func canTransitionAgentSession(from, to domain.AgentSessionStatus) bool {
	allowed, exists := validAgentSessionTransitions[from]
	if !exists {
		return false
	}
	return slices.Contains(allowed, to)
}

// Get retrieves an agent session by ID.
func (s *AgentSessionService) Get(ctx context.Context, id string) (domain.AgentSession, error) {
	var result domain.AgentSession
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		agentSession, err := res.AgentSessions.Get(ctx, id)
		if err != nil {
			return newNotFoundError("agent session", id)
		}
		result = agentSession
		return nil
	})
	return result, err
}

// ListByWorkItemID retrieves all child agent sessions for a work item.
func (s *AgentSessionService) ListByWorkItemID(ctx context.Context, workItemID string) ([]domain.AgentSession, error) {
	var result []domain.AgentSession
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		v, err := res.AgentSessions.ListByWorkItemID(ctx, workItemID)
		if err != nil {
			return err
		}
		result = v
		return nil
	})
	return result, err
}

// ListBySubPlanID retrieves all child agent sessions for a sub-plan.
func (s *AgentSessionService) ListBySubPlanID(ctx context.Context, subPlanID string) ([]domain.AgentSession, error) {
	var result []domain.AgentSession
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		v, err := res.AgentSessions.ListBySubPlanID(ctx, subPlanID)
		if err != nil {
			return err
		}
		result = v
		return nil
	})
	return result, err
}

// ListByWorkspaceID retrieves all child agent sessions for a workspace.
func (s *AgentSessionService) ListByWorkspaceID(ctx context.Context, workspaceID string) ([]domain.AgentSession, error) {
	var result []domain.AgentSession
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		v, err := res.AgentSessions.ListByWorkspaceID(ctx, workspaceID)
		if err != nil {
			return err
		}
		result = v
		return nil
	})
	return result, err
}

// SearchHistory retrieves searchable session-history entries for the requested scope.
func (s *AgentSessionService) SearchHistory(ctx context.Context, filter domain.SessionHistoryFilter) ([]domain.SessionHistoryEntry, error) {
	var result []domain.SessionHistoryEntry
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		v, err := res.AgentSessions.SearchHistory(ctx, filter)
		if err != nil {
			return err
		}
		result = v
		return nil
	})
	return result, err
}

// Create creates a new child agent session in pending status.
func (s *AgentSessionService) Create(ctx context.Context, agentSession domain.AgentSession) error {
	if agentSession.WorkItemID == "" {
		return newInvalidInputError("work item is required", "work_item_id")
	}
	if agentSession.HarnessName == "" {
		return newInvalidInputError("harness name is required", "harness_name")
	}
	if agentSession.Phase == "" {
		return newInvalidInputError("phase is required", "phase")
	}
	switch agentSession.Phase {
	case domain.AgentSessionPhasePlanning:
		// Planning sessions run at the workspace/work-item level and may omit repo-specific fields.
	case domain.AgentSessionPhaseImplementation, domain.AgentSessionPhaseReview:
		if agentSession.SubPlanID == "" {
			return newInvalidInputError("sub-plan is required for this phase", "sub_plan_id")
		}
	default:
		return newInvalidInputError("unknown session phase", "phase")
	}
	if agentSession.Status == "" {
		agentSession.Status = domain.AgentSessionPending
	}
	if agentSession.Status != domain.AgentSessionPending {
		return newInvalidInputError("initial status must be pending", "status")
	}
	now := time.Now()
	agentSession.CreatedAt = now
	agentSession.UpdatedAt = now

	if err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		return res.AgentSessions.Create(ctx, agentSession)
	}); err != nil {
		return err
	}

	return nil
}

// Transition transitions an agent session to a new status.
func (s *AgentSessionService) Transition(ctx context.Context, id string, to domain.AgentSessionStatus) error {
	var agentSession domain.AgentSession
	var from domain.AgentSessionStatus
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		var err error
		agentSession, err = res.AgentSessions.Get(ctx, id)
		if err != nil {
			return newNotFoundError("agent session", id)
		}

		if !canTransitionAgentSession(agentSession.Status, to) {
			return newInvalidTransitionError(
				sessionStatusName(agentSession.Status),
				sessionStatusName(to),
				"agent session",
			)
		}

		from = agentSession.Status
		agentSession.Status = to
		agentSession.UpdatedAt = time.Now()

		if err := res.AgentSessions.Update(ctx, agentSession); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}

	Emit(s.eventBus, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventAgentSessionStatusChanged),
		WorkspaceID: agentSession.WorkspaceID,
		Payload:     marshalAgentSessionStatusChangedPayload(agentSession, from, to),
		CreatedAt:   time.Now(),
	})
	return nil
}

// Start transitions an agent session from pending to running and emits EventAgentSessionStarted
// so the TUI reloads the session list when an agent session begins executing.
func (s *AgentSessionService) Start(ctx context.Context, id string) error {
	var agentSession domain.AgentSession
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		var err error
		agentSession, err = res.AgentSessions.Get(ctx, id)
		if err != nil {
			return newNotFoundError("agent session", id)
		}

		if !canTransitionAgentSession(agentSession.Status, domain.AgentSessionRunning) {
			return newInvalidTransitionError(
				sessionStatusName(agentSession.Status),
				sessionStatusName(domain.AgentSessionRunning),
				"agent session",
			)
		}

		now := time.Now()
		agentSession.Status = domain.AgentSessionRunning
		agentSession.StartedAt = &now
		agentSession.UpdatedAt = now

		return res.AgentSessions.Update(ctx, agentSession)
	})
	if err != nil {
		return err
	}

	Emit(s.eventBus, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventAgentSessionStarted),
		WorkspaceID: agentSession.WorkspaceID,
		Payload:     marshalAgentSessionPayload(agentSession),
		CreatedAt:   time.Now(),
	})
	return nil
}

// WaitForAnswer transitions an agent session from running to waiting_for_answer.
func (s *AgentSessionService) WaitForAnswer(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.AgentSessionWaitingForAnswer)
}

// ResumeFromAnswer transitions an agent session from waiting_for_answer to running.
func (s *AgentSessionService) ResumeFromAnswer(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.AgentSessionRunning)
}

// Complete transitions an agent session from running to completed.
func (s *AgentSessionService) Complete(ctx context.Context, id string) error {
	var agentSession domain.AgentSession
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		var err error
		agentSession, err = res.AgentSessions.Get(ctx, id)
		if err != nil {
			return newNotFoundError("agent session", id)
		}

		if !canTransitionAgentSession(agentSession.Status, domain.AgentSessionCompleted) {
			return newInvalidTransitionError(
				sessionStatusName(agentSession.Status),
				sessionStatusName(domain.AgentSessionCompleted),
				"agent session",
			)
		}

		now := time.Now()
		agentSession.Status = domain.AgentSessionCompleted
		agentSession.CompletedAt = &now
		agentSession.UpdatedAt = now

		if err := res.AgentSessions.Update(ctx, agentSession); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Emit event asynchronously after transaction commits
	Emit(s.eventBus, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventAgentSessionCompleted),
		WorkspaceID: agentSession.WorkspaceID,
		Payload:     marshalAgentSessionPayload(agentSession),
		CreatedAt:   time.Now(),
	})

	return nil
}

// Interrupt transitions an agent session from running to interrupted.
func (s *AgentSessionService) Interrupt(ctx context.Context, id string) error {
	var agentSession domain.AgentSession
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		var err error
		agentSession, err = res.AgentSessions.Get(ctx, id)
		if err != nil {
			return newNotFoundError("agent session", id)
		}

		if !canTransitionAgentSession(agentSession.Status, domain.AgentSessionInterrupted) {
			return newInvalidTransitionError(
				sessionStatusName(agentSession.Status),
				sessionStatusName(domain.AgentSessionInterrupted),
				"agent session",
			)
		}

		now := time.Now()
		agentSession.Status = domain.AgentSessionInterrupted
		agentSession.ShutdownAt = &now
		agentSession.UpdatedAt = now

		if err := res.AgentSessions.Update(ctx, agentSession); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Emit event asynchronously after transaction commits
	Emit(s.eventBus, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventAgentSessionInterrupted),
		WorkspaceID: agentSession.WorkspaceID,
		Payload:     marshalAgentSessionPayload(agentSession),
		CreatedAt:   time.Now(),
	})

	return nil
}

// Resume transitions an agent session from interrupted to running.
func (s *AgentSessionService) Resume(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.AgentSessionRunning)
}

// FollowUpRestart transitions a completed agent session back to running for a follow-up session.
// Unlike Start(), this preserves the original StartedAt and clears CompletedAt.
func (s *AgentSessionService) FollowUpRestart(ctx context.Context, id string) error {
	var agentSession domain.AgentSession
	var from domain.AgentSessionStatus
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		var err error
		agentSession, err = res.AgentSessions.Get(ctx, id)
		if err != nil {
			return newNotFoundError("agent session", id)
		}

		if !canTransitionAgentSession(agentSession.Status, domain.AgentSessionRunning) {
			return newInvalidTransitionError(
				sessionStatusName(agentSession.Status),
				sessionStatusName(domain.AgentSessionRunning),
				"agent session",
			)
		}

		from = agentSession.Status
		now := time.Now()
		agentSession.Status = domain.AgentSessionRunning
		agentSession.CompletedAt = nil
		agentSession.UpdatedAt = now

		if err := res.AgentSessions.Update(ctx, agentSession); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}

	Emit(s.eventBus, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventAgentSessionStatusChanged),
		WorkspaceID: agentSession.WorkspaceID,
		Payload:     marshalAgentSessionStatusChangedPayload(agentSession, from, domain.AgentSessionRunning),
		CreatedAt:   time.Now(),
	})
	return nil
}

// UpdateResumeInfo stores harness-specific resume data on the agent session record.
// The info map is harness-defined; callers must not interpret individual keys.
func (s *AgentSessionService) UpdateResumeInfo(ctx context.Context, id string, info map[string]string) error {
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		agentSession, err := res.AgentSessions.Get(ctx, id)
		if err != nil {
			return newNotFoundError("agent session", id)
		}

		agentSession.ResumeInfo = info
		agentSession.UpdatedAt = time.Now()

		return res.AgentSessions.Update(ctx, agentSession)
	})
}

// SetPlanID records the plan produced by a planning session.
func (s *AgentSessionService) SetPlanID(ctx context.Context, id string, planID string) error {
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		agentSession, err := res.AgentSessions.Get(ctx, id)
		if err != nil {
			return newNotFoundError("agent session", id)
		}

		agentSession.PlanID = planID
		agentSession.UpdatedAt = time.Now()

		return res.AgentSessions.Update(ctx, agentSession)
	})
}

// Fail transitions an agent session to failed.
func (s *AgentSessionService) Fail(ctx context.Context, id string, exitCode *int) error {
	var agentSession domain.AgentSession
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		var err error
		agentSession, err = res.AgentSessions.Get(ctx, id)
		if err != nil {
			return newNotFoundError("agent session", id)
		}

		if !canTransitionAgentSession(agentSession.Status, domain.AgentSessionFailed) {
			return newInvalidTransitionError(
				sessionStatusName(agentSession.Status),
				sessionStatusName(domain.AgentSessionFailed),
				"agent session",
			)
		}

		now := time.Now()
		agentSession.Status = domain.AgentSessionFailed
		agentSession.CompletedAt = &now
		agentSession.ExitCode = exitCode
		agentSession.UpdatedAt = now

		if err := res.AgentSessions.Update(ctx, agentSession); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Emit event asynchronously after transaction commits
	Emit(s.eventBus, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventAgentSessionFailed),
		WorkspaceID: agentSession.WorkspaceID,
		Payload:     marshalAgentSessionPayload(agentSession),
		CreatedAt:   time.Now(),
	})

	return nil
}

// UpdateOwnerInstance updates the owner instance ID for an agent session.
func (s *AgentSessionService) UpdateOwnerInstance(ctx context.Context, id string, instanceID string) error {
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		agentSession, err := res.AgentSessions.Get(ctx, id)
		if err != nil {
			return newNotFoundError("agent session", id)
		}

		agentSession.OwnerInstanceID = &instanceID
		agentSession.UpdatedAt = time.Now()

		return res.AgentSessions.Update(ctx, agentSession)
	})
}

// UpdatePID updates the PID for an agent session.
func (s *AgentSessionService) UpdatePID(ctx context.Context, id string, pid int) error {
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		agentSession, err := res.AgentSessions.Get(ctx, id)
		if err != nil {
			return newNotFoundError("agent session", id)
		}

		agentSession.PID = &pid
		agentSession.UpdatedAt = time.Now()

		return res.AgentSessions.Update(ctx, agentSession)
	})
}

// Delete deletes an agent session.
func (s *AgentSessionService) Delete(ctx context.Context, id string) error {
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		_, err := res.AgentSessions.Get(ctx, id)
		if err != nil {
			return newNotFoundError("agent session", id)
		}

		return res.AgentSessions.Delete(ctx, id)
	})
}

// FindInterruptedByWorkspace finds all interrupted agent sessions for a workspace.
func (s *AgentSessionService) FindInterruptedByWorkspace(ctx context.Context, workspaceID string) ([]domain.AgentSession, error) {
	var sessions []domain.AgentSession
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		v, err := res.AgentSessions.ListByWorkspaceID(ctx, workspaceID)
		if err != nil {
			return err
		}
		sessions = v
		return nil
	})
	if err != nil {
		return nil, err
	}

	var interrupted []domain.AgentSession
	for _, agentSession := range sessions {
		if agentSession.Status == domain.AgentSessionInterrupted {
			interrupted = append(interrupted, agentSession)
		}
	}

	return interrupted, nil
}

// FindRunningByOwner finds all running agent sessions owned by an instance.
func (s *AgentSessionService) FindRunningByOwner(ctx context.Context, instanceID string) ([]domain.AgentSession, error) {
	var sessions []domain.AgentSession
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		v, err := res.AgentSessions.ListByOwnerInstanceID(ctx, instanceID)
		if err != nil {
			return err
		}
		sessions = v
		return nil
	})
	if err != nil {
		return nil, err
	}

	var running []domain.AgentSession
	for _, agentSession := range sessions {
		if agentSession.Status == domain.AgentSessionRunning {
			running = append(running, agentSession)
		}
	}

	return running, nil
}
