package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/beeemT/go-atomic"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/repository"
)

// PlanService provides business logic for plans and sub-plans.
type PlanService struct {
	transacter atomic.Transacter[repository.Resources]
	eventBus   event.Publisher
}

// NewPlanService creates a new PlanService.
func NewPlanService(transacter atomic.Transacter[repository.Resources], eventBus event.Publisher) *PlanService {
	return &PlanService{transacter: transacter, eventBus: eventBus}
}

// planEventPayload holds the JSON payload for plan lifecycle events.
type planEventPayload struct {
	WorkItemID        string            `json:"work_item_id"`
	PlanID            string            `json:"plan_id,omitempty"`
	SubPlanID         string            `json:"sub_plan_id,omitempty"`
	Plan              *domain.Plan      `json:"plan,omitempty"`
	SubPlans          []domain.TaskPlan `json:"sub_plans,omitempty"`
	ExternalID        string            `json:"external_id,omitempty"`
	ExternalIDs       []string          `json:"external_ids,omitempty"`
	CommentBody       string            `json:"comment_body,omitempty"`
	RepoCommentScopes map[string]string `json:"repo_comment_scopes,omitempty"`
}

// PlanApprovalEventContext holds adapter-specific context for plan approval events.
type PlanApprovalEventContext struct {
	ExternalID        string
	ExternalIDs       []string
	CommentBody       string
	RepoCommentScopes map[string]string
}

// planEventExtra holds internal options for plan events.
type planEventExtra struct {
	approvalContext PlanApprovalEventContext
}

// PlanOption is a functional option for plan service methods.
type PlanOption func(*planEventExtra)

// WithPlanApprovalEventContext sets adapter-specific context for plan approval events.
func WithPlanApprovalEventContext(ctx PlanApprovalEventContext) PlanOption {
	return func(e *planEventExtra) {
		e.approvalContext = ctx
	}
}

// planStatusChangedPayload holds the JSON payload for generic plan status changes.
type planStatusChangedPayload struct {
	WorkItemID string `json:"work_item_id"`
	PlanID     string `json:"plan_id"`
	From       string `json:"from"`
	To         string `json:"to"`
}

// subPlanEventPayload holds the JSON payload for sub-plan state events.
// Used by EventSubPlanStarted, EventSubPlanCompleted, and EventSubPlanFailed.
type subPlanEventPayload struct {
	WorkItemID  string                `json:"work_item_id"`
	WorkspaceID string                `json:"workspace_id,omitempty"`
	PlanID      string                `json:"plan_id"`
	SubPlanID   string                `json:"sub_plan_id"`
	SubPlan     domain.TaskPlan       `json:"sub_plan"`
	Status      domain.TaskPlanStatus `json:"status"`
}

// SubPlanPRReadyContext holds adapter-specific context for PR-ready events.
type SubPlanPRReadyContext struct {
	Repository     string
	Branch         string
	WorktreePath   string
	WorkItemTitle  string
	SubPlanContent string
	TrackerRefs    []domain.TrackerReference
	Review         domain.ReviewRef
}

// subPlanPRReadyPayload holds the JSON payload for EventSubPlanPRReady.
type subPlanPRReadyPayload struct {
	WorkItemID     string                    `json:"work_item_id"`
	WorkspaceID    string                    `json:"workspace_id,omitempty"`
	PlanID         string                    `json:"plan_id"`
	SubPlanID      string                    `json:"sub_plan_id"`
	Repository     string                    `json:"repository"`
	Branch         string                    `json:"branch"`
	WorktreePath   string                    `json:"worktree_path,omitempty"`
	WorkItemTitle  string                    `json:"work_item_title,omitempty"`
	SubPlanContent string                    `json:"sub_plan_content,omitempty"`
	TrackerRefs    []domain.TrackerReference `json:"tracker_refs,omitempty"`
	Review         domain.ReviewRef          `json:"review"`
}

// marshalJSONOrEmpty marshals v to JSON, returning "{}" on error.
func marshalJSONOrEmpty(eventType string, v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		slog.Warn("failed to marshal event payload",
			slog.String("event_type", eventType),
			slog.String("error", err.Error()),
		)
		return "{}"
	}
	return string(b)
}

// Plan state transitions
var validPlanTransitions = map[domain.PlanStatus][]domain.PlanStatus{
	domain.PlanDraft:         {domain.PlanPendingReview},
	domain.PlanPendingReview: {domain.PlanApproved, domain.PlanRejected},
	domain.PlanApproved:      {}, // Terminal state — superseding is handled outside the transition table.
	domain.PlanRejected:      {domain.PlanPendingReview},
	domain.PlanSuperseded:    {}, // Terminal state — a superseded plan is historical and immutable.
}

func canTransitionPlan(from, to domain.PlanStatus) bool {
	allowed, exists := validPlanTransitions[from]
	if !exists {
		return false
	}
	return slices.Contains(allowed, to)
}

// SubPlan state transitions
var validSubPlanTransitions = map[domain.TaskPlanStatus][]domain.TaskPlanStatus{
	domain.SubPlanPending:    {domain.SubPlanInProgress},
	domain.SubPlanInProgress: {domain.SubPlanCompleted, domain.SubPlanFailed, domain.SubPlanPending},
	// Allow crash recovery: pending resets a stranded in_progress
	domain.SubPlanCompleted: {},                      // Terminal state
	domain.SubPlanFailed:    {domain.SubPlanPending}, // Allow retry
}

func canTransitionSubPlan(from, to domain.TaskPlanStatus) bool {
	allowed, exists := validSubPlanTransitions[from]
	if !exists {
		return false
	}
	return slices.Contains(allowed, to)
}

// GetPlan retrieves a plan by ID.
func (s *PlanService) GetPlan(ctx context.Context, id string) (domain.Plan, error) {
	var result domain.Plan
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		plan, err := res.Plans.Get(ctx, id)
		if err != nil {
			return newNotFoundError("plan", id)
		}
		result = plan
		return nil
	})
	return result, err
}

// GetPlanByWorkItemID retrieves a plan by work item ID.
func (s *PlanService) GetPlanByWorkItemID(ctx context.Context, workItemID string) (domain.Plan, error) {
	var result domain.Plan
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		plan, err := res.Plans.GetByWorkItemID(ctx, workItemID)
		if err != nil {
			return newNotFoundError("plan for work item", workItemID)
		}
		result = plan
		return nil
	})
	return result, err
}

// CreatePlan creates a new plan in draft status.
func (s *PlanService) CreatePlan(ctx context.Context, plan domain.Plan) error {
	// Set initial status if not set
	if plan.Status == "" {
		plan.Status = domain.PlanDraft
	}
	// Validate initial status
	if plan.Status != domain.PlanDraft {
		return newInvalidInputError("initial status must be draft", "status")
	}
	// Set timestamps and initial version
	now := time.Now()
	plan.CreatedAt = now
	plan.UpdatedAt = now
	if plan.Version == 0 {
		plan.Version = 1
	}

	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		return res.Plans.Create(ctx, plan)
	})
}

// TransitionPlan transitions a plan to a new status.
func (s *PlanService) TransitionPlan(ctx context.Context, id string, to domain.PlanStatus) error {
	var plan domain.Plan
	var from domain.PlanStatus
	var workspaceID string
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		p, err := res.Plans.Get(ctx, id)
		if err != nil {
			return newNotFoundError("plan", id)
		}

		if !canTransitionPlan(p.Status, to) {
			return newInvalidTransitionError(
				planStatusName(p.Status),
				planStatusName(to),
				"plan",
			)
		}

		from = p.Status
		p.Status = to
		p.UpdatedAt = time.Now()

		if err := res.Plans.Update(ctx, p); err != nil {
			return err
		}
		plan = p

		// Load work item to get real WorkspaceID
		workItem, err := res.Sessions.Get(ctx, p.WorkItemID)
		if err != nil {
			slog.Warn("failed to load work item for event workspace ID", "plan_id", id, "work_item_id", p.WorkItemID, "error", err)
			return nil // Non-fatal: continue without workspace ID
		}
		workspaceID = workItem.WorkspaceID
		return nil
	})
	if err != nil {
		return err
	}

	Emit(s.eventBus, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventPlanStatusChanged),
		WorkspaceID: workspaceID,
		Payload:     marshalJSONOrEmpty(string(domain.EventPlanStatusChanged), planStatusChangedPayload{WorkItemID: plan.WorkItemID, PlanID: plan.ID, From: string(from), To: string(to)}),
		CreatedAt:   time.Now(),
	})
	return nil
}

// SubmitForReview transitions a plan from draft to pending_review.
func (s *PlanService) SubmitForReview(ctx context.Context, id string) error {
	var plan domain.Plan
	var subPlans []domain.TaskPlan
	var workspaceID string
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		var err error
		plan, err = res.Plans.Get(ctx, id)
		if err != nil {
			return newNotFoundError("plan", id)
		}
		// Load sub-plans for the event payload
		subPlans, err = res.SubPlans.ListByPlanID(ctx, id)
		if err != nil {
			return fmt.Errorf("list sub-plans: %w", err)
		}
		// Load work item to get real WorkspaceID
		workItem, err := res.Sessions.Get(ctx, plan.WorkItemID)
		if err != nil {
			slog.Warn("failed to load work item for event workspace ID", "plan_id", id, "work_item_id", plan.WorkItemID, "error", err)
			return nil // Non-fatal: continue without workspace ID
		}
		workspaceID = workItem.WorkspaceID
		return nil
	})
	if err != nil {
		return err
	}
	if err := s.TransitionPlan(ctx, id, domain.PlanPendingReview); err != nil {
		return err
	}
	Emit(s.eventBus, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventPlanSubmitted),
		WorkspaceID: workspaceID,
		Payload:     marshalJSONOrEmpty(string(domain.EventPlanSubmitted), planEventPayload{WorkItemID: plan.WorkItemID, PlanID: plan.ID, Plan: &plan, SubPlans: subPlans}),
		CreatedAt:   time.Now(),
	})
	return nil
}

// ApprovePlan transitions a plan from pending_review to approved.
// It accepts optional PlanOptions for adapter-specific event context.
func (s *PlanService) ApprovePlan(ctx context.Context, id string, opts ...PlanOption) error {
	// Apply options
	var extra planEventExtra
	for _, opt := range opts {
		opt(&extra)
	}

	var plan domain.Plan
	var subPlans []domain.TaskPlan
	var workspaceID string
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		var err error
		plan, err = res.Plans.Get(ctx, id)
		if err != nil {
			return newNotFoundError("plan", id)
		}
		if !canTransitionPlan(plan.Status, domain.PlanApproved) {
			return newInvalidTransitionError(
				planStatusName(plan.Status),
				planStatusName(domain.PlanApproved),
				"plan",
			)
		}
		plan.Status = domain.PlanApproved
		plan.UpdatedAt = time.Now()
		if err := res.Plans.Update(ctx, plan); err != nil {
			return err
		}
		// Load sub-plans for the event payload
		subPlans, err = res.SubPlans.ListByPlanID(ctx, id)
		if err != nil {
			return fmt.Errorf("list sub-plans: %w", err)
		}
		// Load work item to get real WorkspaceID
		workItem, err := res.Sessions.Get(ctx, plan.WorkItemID)
		if err != nil {
			slog.Warn("failed to load work item for event workspace ID", "plan_id", id, "work_item_id", plan.WorkItemID, "error", err)
			return nil // Non-fatal: continue with plan.WorkItemID as fallback
		}
		workspaceID = workItem.WorkspaceID
		return nil
	})
	if err != nil {
		return err
	}

	// Emit enriched event with full plan, sub-plans, and adapter context
	Emit(s.eventBus, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventPlanApproved),
		WorkspaceID: workspaceID,
		Payload: marshalJSONOrEmpty(string(domain.EventPlanApproved), planEventPayload{
			WorkItemID:        plan.WorkItemID,
			PlanID:            plan.ID,
			Plan:              &plan,
			SubPlans:          subPlans,
			ExternalID:        extra.approvalContext.ExternalID,
			ExternalIDs:       extra.approvalContext.ExternalIDs,
			CommentBody:       extra.approvalContext.CommentBody,
			RepoCommentScopes: extra.approvalContext.RepoCommentScopes,
		}),
		CreatedAt: time.Now(),
	})
	return nil
}

// RejectPlan transitions a plan from pending_review to rejected.
func (s *PlanService) RejectPlan(ctx context.Context, id string) error {
	var plan domain.Plan
	var subPlans []domain.TaskPlan
	var workspaceID string
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		var err error
		plan, err = res.Plans.Get(ctx, id)
		if err != nil {
			return newNotFoundError("plan", id)
		}
		if !canTransitionPlan(plan.Status, domain.PlanRejected) {
			return newInvalidTransitionError(
				planStatusName(plan.Status),
				planStatusName(domain.PlanRejected),
				"plan",
			)
		}
		plan.Status = domain.PlanRejected
		plan.UpdatedAt = time.Now()
		if err := res.Plans.Update(ctx, plan); err != nil {
			return err
		}
		// Load sub-plans for the event payload
		subPlans, err = res.SubPlans.ListByPlanID(ctx, id)
		if err != nil {
			return fmt.Errorf("list sub-plans: %w", err)
		}
		// Load work item to get real WorkspaceID
		workItem, err := res.Sessions.Get(ctx, plan.WorkItemID)
		if err != nil {
			slog.Warn("failed to load work item for event workspace ID", "plan_id", id, "work_item_id", plan.WorkItemID, "error", err)
			return nil // Non-fatal
		}
		workspaceID = workItem.WorkspaceID
		return nil
	})
	if err != nil {
		return err
	}
	Emit(s.eventBus, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventPlanRejected),
		WorkspaceID: workspaceID,
		Payload:     marshalJSONOrEmpty(string(domain.EventPlanRejected), planEventPayload{WorkItemID: plan.WorkItemID, PlanID: plan.ID, Plan: &plan, SubPlans: subPlans}),
		CreatedAt:   time.Now(),
	})
	return nil
}

// UpdatePlanContent updates the plan content without changing status.
func (s *PlanService) UpdatePlanContent(ctx context.Context, id string, content string) error {
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		plan, err := res.Plans.Get(ctx, id)
		if err != nil {
			return newNotFoundError("plan", id)
		}
		if plan.Status != domain.PlanDraft && plan.Status != domain.PlanPendingReview {
			return newInvalidInputError("can only update content of draft or pending_review plans", "status")
		}

		plan.OrchestratorPlan = content
		plan.UpdatedAt = time.Now()

		return res.Plans.Update(ctx, plan)
	})
}

// ApplyReviewedPlanOutput updates the persisted orchestration plan and sub-plans from a fully parsed review document.
func (s *PlanService) ApplyReviewedPlanOutput(ctx context.Context, id string, rawOutput domain.RawPlanOutput) (domain.Plan, []domain.TaskPlan, error) {
	var resultPlan domain.Plan
	var resultSubPlans []domain.TaskPlan
	var planChanged bool

	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		plan, err := res.Plans.Get(ctx, id)
		if err != nil {
			return newNotFoundError("plan", id)
		}
		if plan.Status != domain.PlanPendingReview {
			return newInvalidTransitionError(
				planStatusName(plan.Status),
				planStatusName(domain.PlanPendingReview),
				"plan",
			)
		}
		existingSubPlans, err := res.SubPlans.ListByPlanID(ctx, id)
		if err != nil {
			return err
		}

		now := time.Now()
		existingByRepo := make(map[string]domain.TaskPlan, len(existingSubPlans))
		for _, sp := range existingSubPlans {
			existingByRepo[strings.ToLower(sp.RepositoryName)] = sp
		}

		changed := plan.OrchestratorPlan != rawOutput.Orchestration || len(existingSubPlans) != len(rawOutput.SubPlans)
		seen := make(map[string]bool, len(rawOutput.SubPlans))
		updatedSubPlans := make([]domain.TaskPlan, 0, len(rawOutput.SubPlans))
		for _, rawSubPlan := range rawOutput.SubPlans {
			key := strings.ToLower(rawSubPlan.RepoName)
			seen[key] = true
			order := findSubPlanOrder(rawSubPlan.RepoName, rawOutput.ExecutionGroups)
			if existing, ok := existingByRepo[key]; ok {
				subPlanChanged := existing.RepositoryName != rawSubPlan.RepoName || existing.Content != rawSubPlan.Content || existing.Order != order
				if subPlanChanged {
					changed = true
					if existing.Status == domain.SubPlanCompleted {
						existing.Status = domain.SubPlanPending
					}
				}
				existing.RepositoryName = rawSubPlan.RepoName
				existing.Content = rawSubPlan.Content
				existing.Order = order
				existing.UpdatedAt = now
				if err := res.SubPlans.Update(ctx, existing); err != nil {
					return err
				}
				updatedSubPlans = append(updatedSubPlans, existing)
				continue
			}
			changed = true
			created := domain.TaskPlan{
				ID:             domain.NewID(),
				PlanID:         id,
				RepositoryName: rawSubPlan.RepoName,
				Content:        rawSubPlan.Content,
				Order:          order,
				Status:         domain.SubPlanPending,
				CreatedAt:      now,
				UpdatedAt:      now,
			}
			if err := res.SubPlans.Create(ctx, created); err != nil {
				return err
			}
			updatedSubPlans = append(updatedSubPlans, created)
		}
		for _, existing := range existingSubPlans {
			if seen[strings.ToLower(existing.RepositoryName)] {
				continue
			}
			changed = true
			if err := res.SubPlans.Delete(ctx, existing.ID); err != nil {
				return err
			}
		}
		if !changed {
			resultPlan = plan
			resultSubPlans = updatedSubPlans
			return nil
		}
		plan.OrchestratorPlan = rawOutput.Orchestration
		plan.UpdatedAt = now
		if err := res.Plans.Update(ctx, plan); err != nil {
			return err
		}

		resultPlan = plan
		resultSubPlans = updatedSubPlans
		planChanged = true
		return nil
	})
	if err != nil {
		return domain.Plan{}, nil, err
	}

	if planChanged {
		// Load work item to get real WorkspaceID
		var workspaceID string
		err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
			workItem, err := res.Sessions.Get(ctx, resultPlan.WorkItemID)
			if err != nil {
				slog.Warn("failed to load work item for event workspace ID", "plan_id", resultPlan.ID, "work_item_id", resultPlan.WorkItemID, "error", err)
				return nil // Non-fatal
			}
			workspaceID = workItem.WorkspaceID
			return nil
		})
		if err != nil {
			return domain.Plan{}, nil, err
		}

		Emit(s.eventBus, domain.SystemEvent{
			ID:          domain.NewID(),
			EventType:   string(domain.EventPlanRevised),
			WorkspaceID: workspaceID,
			Payload:     marshalJSONOrEmpty(string(domain.EventPlanRevised), planEventPayload{WorkItemID: resultPlan.WorkItemID, PlanID: resultPlan.ID, Plan: &resultPlan, SubPlans: resultSubPlans}),
			CreatedAt:   time.Now(),
		})
	}

	return resultPlan, resultSubPlans, nil
}

func findSubPlanOrder(repoName string, groups [][]string) int {
	for i, group := range groups {
		for _, name := range group {
			if strings.EqualFold(name, repoName) {
				return i
			}
		}
	}

	return 0
}

// DeletePlan deletes a plan.
func (s *PlanService) DeletePlan(ctx context.Context, id string) error {
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		_, err := res.Plans.Get(ctx, id)
		if err != nil {
			return newNotFoundError("plan", id)
		}

		return res.Plans.Delete(ctx, id)
	})
}

// AppendFAQ adds a question-answer entry to the plan's FAQ.
func (s *PlanService) AppendFAQ(ctx context.Context, entry domain.FAQEntry) error {
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		return res.Plans.AppendFAQ(ctx, entry)
	})
}

// CreatePlanAtomic atomically supersedes the old plan (when replacePlanID is non-empty)
// and creates the new plan and sub-plans in a single transaction.
// The partial unique index on plans(work_item_id) WHERE status != 'superseded' ensures
// at most one active plan per work item. The old plan and its sub-plans remain in the
// database for historical reference.
// It emits EventPlanGenerated with the full plan and sub-plans after successful creation.
func (s *PlanService) CreatePlanAtomic(ctx context.Context, replacePlanID string, plan *domain.Plan, subPlans []domain.TaskPlan) error {
	var supersededWorkItemID string
	var workspaceID string
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		if replacePlanID != "" {
			old, err := res.Plans.Get(ctx, replacePlanID)
			if err != nil {
				return fmt.Errorf("get replaced plan: %w", err)
			}
			old.Status = domain.PlanSuperseded
			old.UpdatedAt = time.Now()
			plan.Version = old.Version + 1 // generation counter
			if err := res.Plans.Update(ctx, old); err != nil {
				return fmt.Errorf("supersede old plan: %w", err)
			}
			supersededWorkItemID = old.WorkItemID
		}
		if err := res.Plans.Create(ctx, *plan); err != nil {
			return fmt.Errorf("create plan %s: %w", plan.ID, err)
		}
		for i := range subPlans {
			if err := res.SubPlans.Create(ctx, subPlans[i]); err != nil {
				return fmt.Errorf("create sub-plan for %s: %w", subPlans[i].RepositoryName, err)
			}
		}
		// Load work item to get real WorkspaceID for events
		workItem, err := res.Sessions.Get(ctx, plan.WorkItemID)
		if err != nil {
			slog.Warn("failed to load work item for event workspace ID", "plan_id", plan.ID, "work_item_id", plan.WorkItemID, "error", err)
			return nil // Non-fatal: continue without workspace ID
		}
		workspaceID = workItem.WorkspaceID
		return nil
	})
	if err != nil {
		return err
	}

	// Emit EventPlanGenerated with full plan and sub-plans
	Emit(s.eventBus, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventPlanGenerated),
		WorkspaceID: workspaceID,
		Payload: marshalJSONOrEmpty(string(domain.EventPlanGenerated), planEventPayload{
			WorkItemID: plan.WorkItemID,
			PlanID:     plan.ID,
			Plan:       plan,
			SubPlans:   subPlans,
		}),
		CreatedAt: time.Now(),
	})

	if supersededWorkItemID != "" {
		// Load work item to get real WorkspaceID for superseded event
		var supersededWorkspaceID string
		_ = s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
			if res.Sessions == nil {
				slog.Warn("Sessions repository not available for superseded event workspace ID")
				return nil // Non-fatal
			}
			workItem, err := res.Sessions.Get(ctx, supersededWorkItemID)
			if err != nil {
				slog.Warn("failed to load work item for superseded event workspace ID", "work_item_id", supersededWorkItemID, "error", err)
				return nil // Non-fatal
			}
			supersededWorkspaceID = workItem.WorkspaceID
			return nil
		})
		Emit(s.eventBus, domain.SystemEvent{
			ID:          domain.NewID(),
			EventType:   string(domain.EventPlanSuperseded),
			WorkspaceID: supersededWorkspaceID,
			Payload:     marshalJSONOrEmpty(string(domain.EventPlanSuperseded), planEventPayload{WorkItemID: supersededWorkItemID, PlanID: replacePlanID}),
			CreatedAt:   time.Now(),
		})
	}
	return nil
}

// SubPlan operations

// GetSubPlan retrieves a sub-plan by ID.
func (s *PlanService) GetSubPlan(ctx context.Context, id string) (domain.TaskPlan, error) {
	var result domain.TaskPlan
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		sp, err := res.SubPlans.Get(ctx, id)
		if err != nil {
			return newNotFoundError("sub-plan", id)
		}
		result = sp
		return nil
	})
	return result, err
}

// ListSubPlansByPlanID retrieves all sub-plans for a plan.
func (s *PlanService) ListSubPlansByPlanID(ctx context.Context, planID string) ([]domain.TaskPlan, error) {
	var result []domain.TaskPlan
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		plans, err := res.SubPlans.ListByPlanID(ctx, planID)
		if err != nil {
			return err
		}
		result = plans
		return nil
	})
	return result, err
}

// CreateSubPlan creates a new sub-plan in pending status.
func (s *PlanService) CreateSubPlan(ctx context.Context, sp domain.TaskPlan) error {
	// Set initial status if not set
	if sp.Status == "" {
		sp.Status = domain.SubPlanPending
	}
	// Validate initial status
	if sp.Status != domain.SubPlanPending {
		return newInvalidInputError("initial status must be pending", "status")
	}
	// Set timestamps
	now := time.Now()
	sp.CreatedAt = now
	sp.UpdatedAt = now

	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		return res.SubPlans.Create(ctx, sp)
	})
}

// TransitionSubPlan transitions a sub-plan to a new status.
// It emits semantic events based on the destination status:
// - SubPlanInProgress → EventSubPlanStarted
// - SubPlanCompleted → EventSubPlanCompleted
// - SubPlanFailed → EventSubPlanFailed
func (s *PlanService) TransitionSubPlan(ctx context.Context, id string, to domain.TaskPlanStatus) error {
	var sp domain.TaskPlan
	var workItem domain.Session
	var plan domain.Plan

	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		var err error
		sp, err = res.SubPlans.Get(ctx, id)
		if err != nil {
			return newNotFoundError("sub-plan", id)
		}

		if !canTransitionSubPlan(sp.Status, to) {
			return newInvalidTransitionError(
				subPlanStatusName(sp.Status),
				subPlanStatusName(to),
				"sub-plan",
			)
		}

		sp.Status = to
		sp.UpdatedAt = time.Now()

		if err := res.SubPlans.Update(ctx, sp); err != nil {
			return err
		}

		plan, err = res.Plans.Get(ctx, sp.PlanID)
		if err != nil {
			return fmt.Errorf("get plan for sub-plan event: %w", err)
		}

		workItem, err = res.Sessions.Get(ctx, plan.WorkItemID)
		if err != nil {
			return fmt.Errorf("get work item for sub-plan event: %w", err)
		}

		return nil
	})
	if err != nil {
		return err
	}

	evtType := subPlanEventType(to)
	if evtType == "" {
		return nil // No event for SubPlanPending (retry/reset)
	}

	Emit(s.eventBus, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(evtType),
		WorkspaceID: workItem.WorkspaceID,
		Payload: marshalJSONOrEmpty(string(evtType), subPlanEventPayload{
			WorkItemID:  workItem.ID,
			WorkspaceID: workItem.WorkspaceID,
			PlanID:      plan.ID,
			SubPlanID:   sp.ID,
			SubPlan:     sp,
			Status:      sp.Status,
		}),
		CreatedAt: time.Now(),
	})
	return nil
}

// subPlanEventType returns the semantic event type for a sub-plan status transition.
// Returns empty string for SubPlanPending (retry/reset) which has no semantic event.
func subPlanEventType(status domain.TaskPlanStatus) domain.EventType {
	switch status {
	case domain.SubPlanInProgress:
		return domain.EventSubPlanStarted
	case domain.SubPlanCompleted:
		return domain.EventSubPlanCompleted
	case domain.SubPlanFailed:
		return domain.EventSubPlanFailed
	default:
		return ""
	}
}

// StartSubPlan transitions a sub-plan from pending to in_progress.
func (s *PlanService) StartSubPlan(ctx context.Context, id string) error {
	return s.TransitionSubPlan(ctx, id, domain.SubPlanInProgress)
}

// CompleteSubPlan transitions a sub-plan from in_progress to completed.
func (s *PlanService) CompleteSubPlan(ctx context.Context, id string) error {
	return s.TransitionSubPlan(ctx, id, domain.SubPlanCompleted)
}

// FailSubPlan transitions a sub-plan from in_progress to failed.
func (s *PlanService) FailSubPlan(ctx context.Context, id string) error {
	return s.TransitionSubPlan(ctx, id, domain.SubPlanFailed)
}

// RetrySubPlan transitions a failed sub-plan back to pending.
func (s *PlanService) RetrySubPlan(ctx context.Context, id string) error {
	return s.TransitionSubPlan(ctx, id, domain.SubPlanPending)
}

// MarkSubPlanPRReady emits EventSubPlanPRReady after a sub-plan's branch has been
// finalized and pushed. It validates that the sub-plan status is SubPlanCompleted
// and that required fields (Repository, Branch, Review) are present. This method
// is event-only: it does not mutate sub-plan status or persist a separate readiness state.
func (s *PlanService) MarkSubPlanPRReady(ctx context.Context, subPlanID string, ready SubPlanPRReadyContext) error {
	if ready.Repository == "" {
		return newInvalidInputError("repository is required for PR-ready event", "repository")
	}
	if ready.Branch == "" {
		return newInvalidInputError("branch is required for PR-ready event", "branch")
	}

	var workItem domain.Session
	var plan domain.Plan
	var sp domain.TaskPlan

	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		var err error
		sp, err = res.SubPlans.Get(ctx, subPlanID)
		if err != nil {
			return newNotFoundError("sub-plan", subPlanID)
		}

		if sp.Status != domain.SubPlanCompleted {
			return newInvalidTransitionError(
				subPlanStatusName(sp.Status),
				subPlanStatusName(domain.SubPlanCompleted),
				"sub-plan PR-ready",
			)
		}

		plan, err = res.Plans.Get(ctx, sp.PlanID)
		if err != nil {
			return fmt.Errorf("get plan for sub-plan PR-ready event: %w", err)
		}

		workItem, err = res.Sessions.Get(ctx, plan.WorkItemID)
		if err != nil {
			return fmt.Errorf("get work item for sub-plan PR-ready event: %w", err)
		}

		return nil
	})
	if err != nil {
		return err
	}

	Emit(s.eventBus, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventSubPlanPRReady),
		WorkspaceID: workItem.WorkspaceID,
		Payload: marshalJSONOrEmpty(string(domain.EventSubPlanPRReady), subPlanPRReadyPayload{
			WorkItemID:     workItem.ID,
			WorkspaceID:    workItem.WorkspaceID,
			PlanID:         plan.ID,
			SubPlanID:      sp.ID,
			Repository:     ready.Repository,
			Branch:         ready.Branch,
			WorktreePath:   ready.WorktreePath,
			WorkItemTitle:  ready.WorkItemTitle,
			SubPlanContent: ready.SubPlanContent,
			TrackerRefs:    ready.TrackerRefs,
			Review:         ready.Review,
		}),
		CreatedAt: time.Now(),
	})
	return nil
}

// UpdateSubPlanContent updates the sub-plan content without changing status.
func (s *PlanService) UpdateSubPlanContent(ctx context.Context, id string, content string) error {
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		sp, err := res.SubPlans.Get(ctx, id)
		if err != nil {
			return newNotFoundError("sub-plan", id)
		}
		if sp.Status != domain.SubPlanPending && sp.Status != domain.SubPlanFailed {
			return newInvalidInputError("can only update content of pending or failed sub-plans", "status")
		}

		sp.Content = content
		sp.UpdatedAt = time.Now()

		return res.SubPlans.Update(ctx, sp)
	})
}

// DeleteSubPlan deletes a sub-plan.
func (s *PlanService) DeleteSubPlan(ctx context.Context, id string) error {
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		_, err := res.SubPlans.Get(ctx, id)
		if err != nil {
			return newNotFoundError("sub-plan", id)
		}

		return res.SubPlans.Delete(ctx, id)
	})
}

// CreateSubPlansBatch creates multiple sub-plans in a single call.
func (s *PlanService) CreateSubPlansBatch(ctx context.Context, subPlans []domain.TaskPlan) error {
	now := time.Now()
	for i := range subPlans {
		if subPlans[i].Status == "" {
			subPlans[i].Status = domain.SubPlanPending
		}
		if subPlans[i].Status != domain.SubPlanPending {
			return newInvalidInputError("initial status must be pending", "status")
		}
		subPlans[i].CreatedAt = now
		subPlans[i].UpdatedAt = now
	}
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		for i := range subPlans {
			if err := res.SubPlans.Create(ctx, subPlans[i]); err != nil {
				return err
			}
		}
		return nil
	})
}

// AllSubPlansCompleted checks if all sub-plans for a plan are completed.
func (s *PlanService) AllSubPlansCompleted(ctx context.Context, planID string) (bool, error) {
	var result bool
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		subPlans, err := res.SubPlans.ListByPlanID(ctx, planID)
		if err != nil {
			return err
		}

		for _, sp := range subPlans {
			if sp.Status != domain.SubPlanCompleted {
				return nil
			}
		}

		result = len(subPlans) > 0 // Return true only if there are sub-plans and all are completed
		return nil
	})
	return result, err
}
