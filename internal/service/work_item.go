package service

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

// SessionService provides business logic for work items.
type SessionService struct {
	repo repository.SessionRepository
}

// NewSessionService creates a new WorkItemService.
func NewSessionService(repo repository.SessionRepository) *SessionService {
	return &SessionService{repo: repo}
}

// validSessionTransitions defines the allowed state transitions.
var validSessionTransitions = map[domain.SessionState][]domain.SessionState{
	domain.SessionIngested:     {domain.SessionPlanning},
	domain.SessionPlanning:     {domain.SessionIngested, domain.SessionPlanReview, domain.SessionFailed},
	domain.SessionPlanReview:   {domain.SessionApproved, domain.SessionPlanning, domain.SessionFailed},
	domain.SessionApproved:     {domain.SessionImplementing, domain.SessionFailed},
	domain.SessionImplementing: {domain.SessionReviewing, domain.SessionFailed},
	domain.SessionReviewing:    {domain.SessionCompleted, domain.SessionImplementing, domain.SessionFailed},
	domain.SessionCompleted:    {}, // Terminal state
	domain.SessionFailed:       {}, // Terminal state
}

// canTransition checks if a state transition is valid.
func canTransition(from, to domain.SessionState) bool {
	allowed, exists := validSessionTransitions[from]
	if !exists {
		return false
	}
	return slices.Contains(allowed, to)
}

// Get retrieves a work item by ID.
func (s *SessionService) Get(ctx context.Context, id string) (domain.Session, error) {
	item, err := s.repo.Get(ctx, id)
	if err != nil {
		return domain.Session{}, newNotFoundError("work item", id)
	}

	return item, nil
}

// List retrieves work items based on filter.
func (s *SessionService) List(ctx context.Context, filter repository.SessionFilter) ([]domain.Session, error) {
	return s.repo.List(ctx, filter)
}

// Create creates a new work item in the ingested state.
func (s *SessionService) Create(ctx context.Context, item domain.Session) error {
	if strings.TrimSpace(item.WorkspaceID) == "" {
		return newInvalidInputError("workspace_id is required", "workspace_id")
	}
	// Set initial state if not set
	if item.State == "" {
		item.State = domain.SessionIngested
	}
	// Validate initial state
	if item.State != domain.SessionIngested {
		return newInvalidInputError("initial state must be ingested", "state")
	}
	if strings.TrimSpace(item.ExternalID) != "" && shouldEnforceExternalIDUniqueness(item) {
		workspaceID := item.WorkspaceID
		externalID := item.ExternalID
		existing, err := s.repo.List(ctx, repository.SessionFilter{
			WorkspaceID: &workspaceID,
			ExternalID:  &externalID,
			Limit:       1,
		})
		if err != nil {
			return err
		}
		if len(existing) > 0 {
			return newAlreadyExistsError("work item", existing[0].ExternalID)
		}
	}
	if duplicateID, err := s.duplicateSourceItemID(ctx, item); err != nil {
		return err
	} else if duplicateID != "" {
		return newAlreadyExistsError("work item", duplicateID)
	}
	// Set timestamps
	now := time.Now()
	item.CreatedAt = now
	item.UpdatedAt = now

	return s.repo.Create(ctx, item)
}

func (s *SessionService) duplicateSourceItemID(ctx context.Context, item domain.Session) (string, error) {
	if strings.TrimSpace(item.Source) == "" || item.SourceScope == "" || len(item.SourceItemIDs) == 0 {
		return "", nil
	}
	selected := scopedSourceSelectionIDs(item)
	if len(selected) == 0 {
		return "", nil
	}
	workspaceID := item.WorkspaceID
	source := item.Source
	existing, err := s.repo.List(ctx, repository.SessionFilter{
		WorkspaceID: &workspaceID,
		Source:      &source,
	})
	if err != nil {
		return "", err
	}
	for _, current := range existing {
		if current.SourceScope != item.SourceScope {
			continue
		}
		for _, id := range current.SourceItemIDs {
			trimmed := strings.TrimSpace(id)
			if trimmed == "" {
				continue
			}
			selectedIdentity, ok := selected[trimmed]
			if !ok {
				continue
			}
			if !scopedSourceSelectionConflict(item.SourceScope, selectedIdentity, scopedSourceSelectionIdentity(current, trimmed)) {
				continue
			}
			if strings.TrimSpace(current.ExternalID) != "" {
				return current.ExternalID, nil
			}

			return trimmed, nil
		}
	}

	return "", nil
}

type sourceSelectionIdentity struct {
	itemID         string
	containerKey   string
	hasContainerID bool
}

func scopedSourceSelectionIDs(item domain.Session) map[string]sourceSelectionIdentity {
	containerKey, hasContainerID := scopedSourceContainerKey(item)
	selected := make(map[string]sourceSelectionIdentity, len(item.SourceItemIDs))
	for _, id := range item.SourceItemIDs {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		selected[trimmed] = sourceSelectionIdentity{
			itemID:         trimmed,
			containerKey:   containerKey,
			hasContainerID: hasContainerID,
		}
	}

	return selected
}

func scopedSourceSelectionIdentity(item domain.Session, itemID string) sourceSelectionIdentity {
	containerKey, hasContainerID := scopedSourceContainerKey(item)

	return sourceSelectionIdentity{
		itemID:         strings.TrimSpace(itemID),
		containerKey:   containerKey,
		hasContainerID: hasContainerID,
	}
}

func scopedSourceSelectionConflict(scope domain.SelectionScope, left, right sourceSelectionIdentity) bool {
	if left.itemID == "" || right.itemID == "" || left.itemID != right.itemID {
		return false
	}
	if scope == domain.ScopeIssues {
		return true
	}
	if !left.hasContainerID || !right.hasContainerID {
		return true
	}

	return left.containerKey == right.containerKey
}

func scopedSourceContainerKey(item domain.Session) (string, bool) {
	switch item.Source {
	case "github":
		if item.SourceScope != domain.ScopeProjects {
			return "", false
		}

		return parseExternalIDScope(item.ExternalID, "gh:milestone:", "repo:")
	case "gitlab":
		switch item.SourceScope {
		case domain.ScopeProjects:
			if projectID, ok := metadataInt64(item.Metadata, "project_id"); ok {
				return projectID, true
			}

			return parseExternalIDScope(item.ExternalID, "gl:milestone:", "project:")
		case domain.ScopeInitiatives:
			if groupID, ok := metadataInt64(item.Metadata, "group_id"); ok {
				return groupID, true
			}
		}
	}

	return "", false
}

func parseExternalIDScope(externalID, prefix, kind string) (string, bool) {
	if !strings.HasPrefix(externalID, prefix) {
		return "", false
	}
	scope := strings.TrimSpace(strings.TrimPrefix(externalID, prefix))
	if scope == "" {
		return "", false
	}

	return kind + scope, true
}

func metadataInt64(metadata map[string]any, key string) (string, bool) {
	if len(metadata) == 0 {
		return "", false
	}
	raw, ok := metadata[key]
	if !ok {
		return "", false
	}
	var value int64
	switch v := raw.(type) {
	case int:
		value = int64(v)
	case int32:
		value = int64(v)
	case int64:
		value = v
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return "", false
		}

		return key[:len(key)-3] + ":" + trimmed, true
	default:
		return "", false
	}

	return key[:len(key)-3] + ":" + strconv.FormatInt(value, 10), true
}

func shouldEnforceExternalIDUniqueness(item domain.Session) bool {
	if len(item.SourceItemIDs) == 0 {
		return true
	}
	switch item.SourceScope {
	case domain.ScopeProjects, domain.ScopeInitiatives:
		return false
	default:
		return true
	}
}

// Transition transitions a work item to a new state.
func (s *SessionService) Transition(ctx context.Context, id string, to domain.SessionState) error {
	item, err := s.repo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("work item", id)
	}

	if !canTransition(item.State, to) {
		return newInvalidTransitionError(
			workItemStateName(item.State),
			workItemStateName(to),
			"work item",
		)
	}

	item.State = to
	item.UpdatedAt = time.Now()

	return s.repo.Update(ctx, item)
}

// StartPlanning transitions a work item from ingested to planning.
func (s *SessionService) StartPlanning(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.SessionPlanning)
}

// SubmitPlanForReview transitions a work item from planning to plan_review.
func (s *SessionService) SubmitPlanForReview(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.SessionPlanReview)
}

// ApprovePlan transitions a work item from plan_review to approved.
func (s *SessionService) ApprovePlan(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.SessionApproved)
}

// RejectPlan transitions a work item from plan_review back to planning.
func (s *SessionService) RejectPlan(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.SessionPlanning)
}

// StartImplementation transitions a work item from approved to implementing.
func (s *SessionService) StartImplementation(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.SessionImplementing)
}

// SubmitForReview transitions a work item from implementing to reviewing.
func (s *SessionService) SubmitForReview(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.SessionReviewing)
}

// CompleteWorkItem transitions a work item from reviewing to completed.
func (s *SessionService) CompleteWorkItem(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.SessionCompleted)
}

// RequestReimplementation transitions a work item from reviewing to implementing.
func (s *SessionService) RequestReimplementation(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.SessionImplementing)
}

// FailWorkItem transitions a work item to failed from any applicable state.
func (s *SessionService) FailWorkItem(ctx context.Context, id string) error {
	item, err := s.repo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("work item", id)
	}

	if !canTransition(item.State, domain.SessionFailed) {
		return newInvalidTransitionError(
			workItemStateName(item.State),
			workItemStateName(domain.SessionFailed),
			"work item",
		)
	}

	return s.Transition(ctx, id, domain.SessionFailed)
}

// Update updates a work item's mutable fields.
func (s *SessionService) Update(ctx context.Context, item domain.Session) error {
	existing, err := s.repo.Get(ctx, item.ID)
	if err != nil {
		return newNotFoundError("work item", item.ID)
	}

	// Preserve immutable fields
	item.ID = existing.ID
	item.WorkspaceID = existing.WorkspaceID
	item.CreatedAt = existing.CreatedAt
	item.State = existing.State // State changes must go through Transition
	item.UpdatedAt = time.Now()

	return s.repo.Update(ctx, item)
}

// Delete deletes a work item.
func (s *SessionService) Delete(ctx context.Context, id string) error {
	// Verify existence first
	_, err := s.repo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("work item", id)
	}

	return s.repo.Delete(ctx, id)
}
