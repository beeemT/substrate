package service

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

// WorkItemService provides business logic for work items.
type WorkItemService struct {
	repo repository.WorkItemRepository
}

// NewWorkItemService creates a new WorkItemService.
func NewWorkItemService(repo repository.WorkItemRepository) *WorkItemService {
	return &WorkItemService{repo: repo}
}

// validWorkItemTransitions defines the allowed state transitions.
var validWorkItemTransitions = map[domain.WorkItemState][]domain.WorkItemState{
	domain.WorkItemIngested:     {domain.WorkItemPlanning},
	domain.WorkItemPlanning:     {domain.WorkItemPlanReview, domain.WorkItemIngested, domain.WorkItemFailed},
	domain.WorkItemPlanReview:   {domain.WorkItemApproved, domain.WorkItemPlanning, domain.WorkItemFailed},
	domain.WorkItemApproved:     {domain.WorkItemImplementing, domain.WorkItemFailed},
	domain.WorkItemImplementing: {domain.WorkItemReviewing, domain.WorkItemFailed},
	domain.WorkItemReviewing:    {domain.WorkItemCompleted, domain.WorkItemImplementing, domain.WorkItemFailed},
	domain.WorkItemCompleted:    {}, // Terminal state
	domain.WorkItemFailed:       {}, // Terminal state
}

// canTransition checks if a state transition is valid.
func canTransition(from, to domain.WorkItemState) bool {
	allowed, exists := validWorkItemTransitions[from]
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

// Get retrieves a work item by ID.
func (s *WorkItemService) Get(ctx context.Context, id string) (domain.WorkItem, error) {
	item, err := s.repo.Get(ctx, id)
	if err != nil {
		return domain.WorkItem{}, newNotFoundError("work item", id)
	}
	return item, nil
}

// List retrieves work items based on filter.
func (s *WorkItemService) List(ctx context.Context, filter repository.WorkItemFilter) ([]domain.WorkItem, error) {
	return s.repo.List(ctx, filter)
}

// Create creates a new work item in the ingested state.
func (s *WorkItemService) Create(ctx context.Context, item domain.WorkItem) error {
	if strings.TrimSpace(item.WorkspaceID) == "" {
		return newInvalidInputError("workspace_id is required", "workspace_id")
	}
	// Set initial state if not set
	if item.State == "" {
		item.State = domain.WorkItemIngested
	}
	// Validate initial state
	if item.State != domain.WorkItemIngested {
		return newInvalidInputError("initial state must be ingested", "state")
	}
	if strings.TrimSpace(item.ExternalID) != "" && shouldEnforceExternalIDUniqueness(item) {
		workspaceID := item.WorkspaceID
		externalID := item.ExternalID
		existing, err := s.repo.List(ctx, repository.WorkItemFilter{
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

func (s *WorkItemService) duplicateSourceItemID(ctx context.Context, item domain.WorkItem) (string, error) {
	if strings.TrimSpace(item.Source) == "" || item.SourceScope == "" || len(item.SourceItemIDs) == 0 {
		return "", nil
	}
	selected := scopedSourceSelectionIDs(item)
	if len(selected) == 0 {
		return "", nil
	}
	workspaceID := item.WorkspaceID
	source := item.Source
	existing, err := s.repo.List(ctx, repository.WorkItemFilter{
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

func scopedSourceSelectionIDs(item domain.WorkItem) map[string]sourceSelectionIdentity {
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

func scopedSourceSelectionIdentity(item domain.WorkItem, itemID string) sourceSelectionIdentity {
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

func scopedSourceContainerKey(item domain.WorkItem) (string, bool) {
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

func shouldEnforceExternalIDUniqueness(item domain.WorkItem) bool {
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
func (s *WorkItemService) Transition(ctx context.Context, id string, to domain.WorkItemState) error {
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
func (s *WorkItemService) StartPlanning(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.WorkItemPlanning)
}

// SubmitPlanForReview transitions a work item from planning to plan_review.
func (s *WorkItemService) SubmitPlanForReview(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.WorkItemPlanReview)
}

// ApprovePlan transitions a work item from plan_review to approved.
func (s *WorkItemService) ApprovePlan(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.WorkItemApproved)
}

// RejectPlan transitions a work item from plan_review back to planning.
func (s *WorkItemService) RejectPlan(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.WorkItemPlanning)
}

// StartImplementation transitions a work item from approved to implementing.
func (s *WorkItemService) StartImplementation(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.WorkItemImplementing)
}

// SubmitForReview transitions a work item from implementing to reviewing.
func (s *WorkItemService) SubmitForReview(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.WorkItemReviewing)
}

// CompleteWorkItem transitions a work item from reviewing to completed.
func (s *WorkItemService) CompleteWorkItem(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.WorkItemCompleted)
}

// RequestReimplementation transitions a work item from reviewing to implementing.
func (s *WorkItemService) RequestReimplementation(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.WorkItemImplementing)
}

// FailWorkItem transitions a work item to failed from any applicable state.
func (s *WorkItemService) FailWorkItem(ctx context.Context, id string) error {
	item, err := s.repo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("work item", id)
	}

	if !canTransition(item.State, domain.WorkItemFailed) {
		return newInvalidTransitionError(
			workItemStateName(item.State),
			workItemStateName(domain.WorkItemFailed),
			"work item",
		)
	}

	return s.Transition(ctx, id, domain.WorkItemFailed)
}

// Update updates a work item's mutable fields.
func (s *WorkItemService) Update(ctx context.Context, item domain.WorkItem) error {
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
func (s *WorkItemService) Delete(ctx context.Context, id string) error {
	// Verify existence first
	_, err := s.repo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("work item", id)
	}

	return s.repo.Delete(ctx, id)
}
