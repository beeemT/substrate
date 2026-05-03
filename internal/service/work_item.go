package service

import (
	"context"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/beeemT/go-atomic"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/repository"
)

// SessionService provides business logic for work items.
type SessionService struct {
	transacter atomic.Transacter[repository.Resources]
	bus        *event.Bus
}

// NewSessionService creates a new WorkItemService.
func NewSessionService(transacter atomic.Transacter[repository.Resources], bus *event.Bus) *SessionService {
	return &SessionService{transacter: transacter, bus: bus}
}

// validSessionTransitions defines the allowed state transitions.
var validSessionTransitions = map[domain.SessionState][]domain.SessionState{
	domain.SessionIngested:     {domain.SessionPlanning},
	domain.SessionPlanning:     {domain.SessionIngested, domain.SessionPlanReview, domain.SessionFailed},
	domain.SessionPlanReview:   {domain.SessionApproved, domain.SessionPlanning, domain.SessionFailed},
	domain.SessionApproved:     {domain.SessionImplementing, domain.SessionFailed},
	domain.SessionImplementing: {domain.SessionReviewing, domain.SessionCompleted, domain.SessionFailed},
	domain.SessionReviewing:    {domain.SessionCompleted, domain.SessionImplementing, domain.SessionFailed},
	domain.SessionCompleted:    {domain.SessionPlanning, domain.SessionMerged, domain.SessionArchived},
	domain.SessionMerged:       {domain.SessionArchived},
	domain.SessionFailed:       {domain.SessionImplementing, domain.SessionArchived},
	domain.SessionArchived:     {domain.SessionCompleted, domain.SessionMerged, domain.SessionFailed},
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
	var result domain.Session
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		item, err := res.Sessions.Get(ctx, id)
		if err != nil {
			return newNotFoundError("work item", id)
		}
		result = item
		return nil
	})
	return result, err
}

// List retrieves work items based on filter.
func (s *SessionService) List(ctx context.Context, filter repository.SessionFilter) ([]domain.Session, error) {
	var result []domain.Session
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		var err error
		result, err = res.Sessions.List(ctx, filter)
		return err
	})
	return result, err
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

	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		if strings.TrimSpace(item.ExternalID) != "" && shouldEnforceExternalIDUniqueness(item) {
			workspaceID := item.WorkspaceID
			externalID := item.ExternalID
			existing, err := res.Sessions.List(ctx, repository.SessionFilter{
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
		if duplicateID, err := duplicateSourceItemID(ctx, res.Sessions, item); err != nil {
			return err
		} else if duplicateID != "" {
			return newAlreadyExistsError("work item", duplicateID)
		}
		// Set timestamps
		now := time.Now()
		item.CreatedAt = now
		item.UpdatedAt = now

		return res.Sessions.Create(ctx, item)
	})
}

func duplicateSourceItemID(ctx context.Context, sessions repository.SessionRepository, item domain.Session) (string, error) {
	if strings.TrimSpace(item.Source) == "" || item.SourceScope == "" || len(item.SourceItemIDs) == 0 {
		return "", nil
	}
	selected := scopedSourceSelectionIDs(item)
	if len(selected) == 0 {
		return "", nil
	}
	workspaceID := item.WorkspaceID
	source := item.Source
	existing, err := sessions.List(ctx, repository.SessionFilter{
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
	var committed stateChangeEvent
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		item, err := res.Sessions.Get(ctx, id)
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

		from := item.State
		item.State = to
		item.UpdatedAt = time.Now()

		if err := res.Sessions.Update(ctx, item); err != nil {
			return err
		}

		// Capture after DB write — will emit only if transaction commits.
		committed = stateChangeEvent{from: from, to: to, item: item}
		return nil
	})
	if err != nil {
		return err
	}

	// Transaction committed. Emit outside the transaction.
	if committed.item.ID != "" {
		s.emitStateChange(context.Background(), committed.from, committed.to, committed.item)
	}
	return nil
}

type stateChangeEvent struct {
	from, to domain.SessionState
	item     domain.Session
}

// stateToEventType maps session states to their corresponding event types.
// Returns empty string for states that should not emit events.
func stateToEventType(state domain.SessionState) domain.EventType {
	switch state {
	case domain.SessionIngested:
		return domain.EventWorkItemIngested
	case domain.SessionPlanning:
		return domain.EventWorkItemPlanning
	case domain.SessionPlanReview:
		return domain.EventWorkItemPlanReview
	case domain.SessionApproved:
		return domain.EventWorkItemApproved
	case domain.SessionImplementing:
		return domain.EventWorkItemImplementing
	case domain.SessionReviewing:
		return domain.EventWorkItemReviewing
	case domain.SessionCompleted:
		return domain.EventWorkItemCompleted
	case domain.SessionMerged:
		return domain.EventWorkItemMerged
	case domain.SessionFailed:
		return domain.EventWorkItemFailed
	default:
		return "" // SessionArchived and unknown states don't emit
	}
}

// emitStateChange emits a state change event asynchronously.
// Nil bus is handled gracefully by skipping the emit.
func (s *SessionService) emitStateChange(ctx context.Context, from, to domain.SessionState, item domain.Session) {
	if s.bus == nil {
		return
	}

	eventType := stateToEventType(to)
	if eventType == "" {
		return // no event for this state
	}

	go func() {
		if err := s.bus.Publish(ctx, domain.SystemEvent{
			ID:          item.ID,
			EventType:   string(eventType),
			WorkspaceID: item.WorkspaceID,
			CreatedAt:   time.Now(),
		}); err != nil {
			slog.Error("failed to emit state change event",
				"error", err,
				"session_id", item.ID,
				"from", from,
				"to", to,
				"event_type", eventType,
			)
		}
	}()
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

// MergeWorkItem transitions a work item from completed to merged.
func (s *SessionService) MergeWorkItem(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.SessionMerged)
}

// RequestReimplementation transitions a work item from reviewing to implementing.
func (s *SessionService) RequestReimplementation(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.SessionImplementing)
}

// FailWorkItem transitions a work item to failed from any applicable state.
func (s *SessionService) FailWorkItem(ctx context.Context, id string) error {
	var item domain.Session
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		var err error
		item, err = res.Sessions.Get(ctx, id)
		return err
	})
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

// RetryFailedWorkItem transitions a failed work item back to implementing for retry.
func (s *SessionService) RetryFailedWorkItem(ctx context.Context, id string) error {
	var item domain.Session
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		var err error
		item, err = res.Sessions.Get(ctx, id)
		return err
	})
	if err != nil {
		return newNotFoundError("work item", id)
	}

	if !canTransition(item.State, domain.SessionImplementing) {
		return newInvalidTransitionError(
			workItemStateName(item.State),
			workItemStateName(domain.SessionImplementing),
			"work item",
		)
	}

	return s.Transition(ctx, id, domain.SessionImplementing)
}

// Archive archives a terminal work item (completed, merged, or failed).
// It captures the current state in PreviousState so it can be restored on unarchive.
func (s *SessionService) Archive(ctx context.Context, id string) error {
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		item, err := res.Sessions.Get(ctx, id)
		if err != nil {
			return newNotFoundError("work item", id)
		}

		if !canTransition(item.State, domain.SessionArchived) {
			return newInvalidTransitionError(
				workItemStateName(item.State),
				workItemStateName(domain.SessionArchived),
				"work item",
			)
		}

		now := time.Now()
		item.PreviousState = item.State
		item.State = domain.SessionArchived
		item.UpdatedAt = now

		return res.Sessions.Update(ctx, item)
	})
}

// Unarchive restores an archived work item to its PreviousState.
func (s *SessionService) Unarchive(ctx context.Context, id string) error {
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		item, err := res.Sessions.Get(ctx, id)
		if err != nil {
			return newNotFoundError("work item", id)
		}

		if item.State != domain.SessionArchived {
			return newInvalidTransitionError(
				workItemStateName(item.State),
				"archived", // unarchive requires state to be archived
				"work item",
			)
		}
		if item.PreviousState == "" {
			return newInvalidInputError("no previous state recorded", "previous_state")
		}

		now := time.Now()
		previousState := item.PreviousState // preserve target state before overwriting
		item.PreviousState = item.State     // record that we transitioned from archived
		item.State = previousState          // restore to the pre-archive state
		item.UpdatedAt = now

		return res.Sessions.Update(ctx, item)
	})
}

// StartFollowUpPlanning transitions a completed work item back to planning for a follow-up round.
func (s *SessionService) StartFollowUpPlanning(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.SessionPlanning)
}

// RollbackPlanningInterrupt transitions a work item from planning back to ingested
// when its planning task was interrupted. Idempotent: if the work item is already
// in a different state (rolled back by a prior call or already advanced), it is a no-op.
func (s *SessionService) RollbackPlanningInterrupt(ctx context.Context, id string) error {
	var item domain.Session
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		var err error
		item, err = res.Sessions.Get(ctx, id)
		return err
	})
	if err != nil {
		return newNotFoundError("work item", id)
	}
	if item.State != domain.SessionPlanning {
		// Already rolled back or in a later state — no-op.
		return nil
	}

	return s.Transition(ctx, id, domain.SessionIngested)
}

// Update updates a work item's mutable fields.
func (s *SessionService) Update(ctx context.Context, item domain.Session) error {
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		existing, err := res.Sessions.Get(ctx, item.ID)
		if err != nil {
			return newNotFoundError("work item", item.ID)
		}

		// Preserve immutable fields
		item.ID = existing.ID
		item.WorkspaceID = existing.WorkspaceID
		item.CreatedAt = existing.CreatedAt
		item.State = existing.State // State changes must go through Transition
		item.UpdatedAt = time.Now()

		return res.Sessions.Update(ctx, item)
	})
}

// Delete deletes a work item.
func (s *SessionService) Delete(ctx context.Context, id string) error {
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		// Verify existence first
		_, err := res.Sessions.Get(ctx, id)
		if err != nil {
			return newNotFoundError("work item", id)
		}

		return res.Sessions.Delete(ctx, id)
	})
}
