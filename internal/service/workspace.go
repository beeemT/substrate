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

// WorkspaceService provides business logic for workspaces.
type WorkspaceService struct {
	transacter atomic.Transacter[repository.Resources]
	eventBus   *event.Bus
}

// NewWorkspaceService creates a new WorkspaceService.
func NewWorkspaceService(transacter atomic.Transacter[repository.Resources], eventBus *event.Bus) *WorkspaceService {
	return &WorkspaceService{transacter: transacter, eventBus: eventBus}
}

// workspaceEventPayload holds the JSON payload for workspace lifecycle events.
type workspaceEventPayload struct {
	WorkspaceID string `json:"workspace_id"`
	Status      string `json:"status,omitempty"`
	From        string `json:"from,omitempty"`
	To          string `json:"to,omitempty"`
}

// marshalWorkspacePayload serializes a workspace event payload to JSON.
func marshalWorkspacePayload(workspaceID, status, from, to string) string {
	p := workspaceEventPayload{WorkspaceID: workspaceID, Status: status, From: from, To: to}
	b, _ := json.Marshal(p)
	return string(b)
}

// Workspace state transitions
var validWorkspaceTransitions = map[domain.WorkspaceStatus][]domain.WorkspaceStatus{
	domain.WorkspaceCreating: {domain.WorkspaceReady, domain.WorkspaceError},
	domain.WorkspaceReady:    {domain.WorkspaceArchived},
	domain.WorkspaceArchived: {},                      // Terminal state
	domain.WorkspaceError:    {domain.WorkspaceReady}, // Can recover from error
}

func canTransitionWorkspace(from, to domain.WorkspaceStatus) bool {
	allowed, exists := validWorkspaceTransitions[from]
	if !exists {
		return false
	}
	return slices.Contains(allowed, to)
}

// Get retrieves a workspace by ID.
func (s *WorkspaceService) Get(ctx context.Context, id string) (domain.Workspace, error) {
	var result domain.Workspace
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		ws, err := res.Workspaces.Get(ctx, id)
		if err != nil {
			return newNotFoundError("workspace", id)
		}
		result = ws
		return nil
	})
	return result, err
}

// Create creates a new workspace in creating status.
func (s *WorkspaceService) Create(ctx context.Context, ws domain.Workspace) error {
	// Set initial status if not set
	if ws.Status == "" {
		ws.Status = domain.WorkspaceCreating
	}
	// Set timestamps
	now := time.Now()
	ws.CreatedAt = now
	ws.UpdatedAt = now

	if err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		return res.Workspaces.Create(ctx, ws)
	}); err != nil {
		return err
	}

	Emit(s.eventBus, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventWorkspaceCreated),
		WorkspaceID: ws.ID,
		Payload:     marshalWorkspacePayload(ws.ID, string(ws.Status), "", ""),
		CreatedAt:   time.Now(),
	})
	return nil
}

// Transition transitions a workspace to a new status.
func (s *WorkspaceService) Transition(ctx context.Context, id string, to domain.WorkspaceStatus) error {
	var ws domain.Workspace
	var from domain.WorkspaceStatus
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		w, err := res.Workspaces.Get(ctx, id)
		if err != nil {
			return newNotFoundError("workspace", id)
		}

		if !canTransitionWorkspace(w.Status, to) {
			return newInvalidTransitionError(
				workspaceStatusName(w.Status),
				workspaceStatusName(to),
				"workspace",
			)
		}

		from = w.Status
		w.Status = to
		w.UpdatedAt = time.Now()

		if err := res.Workspaces.Update(ctx, w); err != nil {
			return err
		}
		ws = w
		return nil
	})
	if err != nil {
		return err
	}

	Emit(s.eventBus, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventWorkspaceStatusChanged),
		WorkspaceID: ws.ID,
		Payload:     marshalWorkspacePayload(ws.ID, string(ws.Status), string(from), string(to)),
		CreatedAt:   time.Now(),
	})
	return nil
}

// MarkReady transitions a workspace from creating to ready.
func (s *WorkspaceService) MarkReady(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.WorkspaceReady)
}

// MarkError transitions a workspace from creating to error.
func (s *WorkspaceService) MarkError(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.WorkspaceError)
}

// Archive transitions a workspace from ready to archived.
func (s *WorkspaceService) Archive(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.WorkspaceArchived)
}

// Recover transitions a workspace from error to ready.
func (s *WorkspaceService) Recover(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.WorkspaceReady)
}

// Update updates a workspace's mutable fields.
func (s *WorkspaceService) Update(ctx context.Context, ws domain.Workspace) error {
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		existing, err := res.Workspaces.Get(ctx, ws.ID)
		if err != nil {
			return newNotFoundError("workspace", ws.ID)
		}

		// Preserve immutable fields
		ws.ID = existing.ID
		ws.CreatedAt = existing.CreatedAt
		ws.Status = existing.Status // Status changes must go through Transition
		ws.UpdatedAt = time.Now()

		return res.Workspaces.Update(ctx, ws)
	})
}

// Delete deletes a workspace.
func (s *WorkspaceService) Delete(ctx context.Context, id string) error {
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		_, err := res.Workspaces.Get(ctx, id)
		if err != nil {
			return newNotFoundError("workspace", id)
		}

		return res.Workspaces.Delete(ctx, id)
	})
}
