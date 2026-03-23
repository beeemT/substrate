package service

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

// PlanRepoTransacter runs plan + sub-plan operations in a single database transaction.
type PlanRepoTransacter interface {
	TransactPlanRepos(ctx context.Context, fn func(ctx context.Context, planRepo repository.PlanRepository, subPlanRepo repository.TaskPlanRepository) error) error
}

// NoopPlanTransacter executes the function directly using the provided repos.
// Used in tests where mock repositories don't require real transactions.
type NoopPlanTransacter struct {
	PlanRepo    repository.PlanRepository
	SubPlanRepo repository.TaskPlanRepository
}

func (t NoopPlanTransacter) TransactPlanRepos(ctx context.Context, fn func(ctx context.Context, planRepo repository.PlanRepository, subPlanRepo repository.TaskPlanRepository) error) error {
	return fn(ctx, t.PlanRepo, t.SubPlanRepo)
}

// PlanService provides business logic for plans and sub-plans.
type PlanService struct {
	planRepo    repository.PlanRepository
	subPlanRepo repository.TaskPlanRepository
	transacter  PlanRepoTransacter
}

// NewPlanService creates a new PlanService.
func NewPlanService(planRepo repository.PlanRepository, subPlanRepo repository.TaskPlanRepository, transacter PlanRepoTransacter) *PlanService {
	return &PlanService{
		planRepo:    planRepo,
		subPlanRepo: subPlanRepo,
		transacter:  transacter,
	}
}

// Plan state transitions
var validPlanTransitions = map[domain.PlanStatus][]domain.PlanStatus{
	domain.PlanDraft:         {domain.PlanPendingReview},
	domain.PlanPendingReview: {domain.PlanApproved, domain.PlanRejected},
	domain.PlanApproved:      {}, // Terminal state
	domain.PlanRejected:      {domain.PlanPendingReview},
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
	domain.SubPlanInProgress: {domain.SubPlanCompleted, domain.SubPlanFailed},
	domain.SubPlanCompleted:  {},                      // Terminal state
	domain.SubPlanFailed:     {domain.SubPlanPending}, // Allow retry
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
	plan, err := s.planRepo.Get(ctx, id)
	if err != nil {
		return domain.Plan{}, newNotFoundError("plan", id)
	}

	return plan, nil
}

// GetPlanByWorkItemID retrieves a plan by work item ID.
func (s *PlanService) GetPlanByWorkItemID(ctx context.Context, workItemID string) (domain.Plan, error) {
	plan, err := s.planRepo.GetByWorkItemID(ctx, workItemID)
	if err != nil {
		return domain.Plan{}, newNotFoundError("plan for work item", workItemID)
	}

	return plan, nil
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

	return s.planRepo.Create(ctx, plan)
}

// TransitionPlan transitions a plan to a new status.
func (s *PlanService) TransitionPlan(ctx context.Context, id string, to domain.PlanStatus) error {
	plan, err := s.planRepo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("plan", id)
	}

	if !canTransitionPlan(plan.Status, to) {
		return newInvalidTransitionError(
			planStatusName(plan.Status),
			planStatusName(to),
			"plan",
		)
	}

	plan.Status = to
	plan.UpdatedAt = time.Now()

	return s.planRepo.Update(ctx, plan)
}

// SubmitForReview transitions a plan from draft to pending_review.
func (s *PlanService) SubmitForReview(ctx context.Context, id string) error {
	return s.TransitionPlan(ctx, id, domain.PlanPendingReview)
}

// ApprovePlan transitions a plan from pending_review to approved.
func (s *PlanService) ApprovePlan(ctx context.Context, id string) error {
	return s.TransitionPlan(ctx, id, domain.PlanApproved)
}

// RejectPlan transitions a plan from pending_review to rejected.
func (s *PlanService) RejectPlan(ctx context.Context, id string) error {
	return s.TransitionPlan(ctx, id, domain.PlanRejected)
}

// RevisePlan transitions a rejected plan back to pending_review and increments version.
func (s *PlanService) RevisePlan(ctx context.Context, id string, newContent string) error {
	plan, err := s.planRepo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("plan", id)
	}

	if plan.Status != domain.PlanRejected {
		return newInvalidTransitionError(
			planStatusName(plan.Status),
			planStatusName(domain.PlanPendingReview),
			"plan",
		)
	}

	plan.Status = domain.PlanPendingReview
	plan.OrchestratorPlan = newContent
	plan.Version++
	plan.UpdatedAt = time.Now()

	return s.planRepo.Update(ctx, plan)
}

// UpdatePlanContent updates the plan content without changing status.
func (s *PlanService) UpdatePlanContent(ctx context.Context, id string, content string) error {
	plan, err := s.planRepo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("plan", id)
	}
	if plan.Status != domain.PlanDraft && plan.Status != domain.PlanPendingReview {
		return newInvalidInputError("can only update content of draft or pending_review plans", "status")

	}

	plan.OrchestratorPlan = content
	plan.UpdatedAt = time.Now()

	return s.planRepo.Update(ctx, plan)
}

// ApplyReviewedPlanOutput updates the persisted orchestration plan and sub-plans from a fully parsed review document.
func (s *PlanService) ApplyReviewedPlanOutput(ctx context.Context, id string, rawOutput domain.RawPlanOutput) (domain.Plan, []domain.TaskPlan, error) {
	var resultPlan domain.Plan
	var resultSubPlans []domain.TaskPlan

	err := s.transacter.TransactPlanRepos(ctx, func(ctx context.Context, planRepo repository.PlanRepository, subPlanRepo repository.TaskPlanRepository) error {
		plan, err := planRepo.Get(ctx, id)
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
		existingSubPlans, err := subPlanRepo.ListByPlanID(ctx, id)
		if err != nil {
			return err
		}

		now := time.Now()
		existingByRepo := make(map[string]domain.TaskPlan, len(existingSubPlans))
		for _, sp := range existingSubPlans {
			existingByRepo[strings.ToLower(sp.RepositoryName)] = sp
		}

		changed := plan.OrchestratorPlan != rawOutput.Orchestration || len(existingSubPlans) != len(rawOutput.SubPlans)
		newVersion := plan.Version + 1
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
					existing.PlanningRound = newVersion
					if existing.Status == domain.SubPlanCompleted {
						existing.Status = domain.SubPlanPending
					}
				}
				existing.RepositoryName = rawSubPlan.RepoName
				existing.Content = rawSubPlan.Content
				existing.Order = order
				existing.UpdatedAt = now
				if err := subPlanRepo.Update(ctx, existing); err != nil {
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
				PlanningRound:  newVersion,
				Status:         domain.SubPlanPending,
				CreatedAt:      now,
				UpdatedAt:      now,
			}
			if err := subPlanRepo.Create(ctx, created); err != nil {
				return err
			}
			updatedSubPlans = append(updatedSubPlans, created)
		}
		for _, existing := range existingSubPlans {
			if seen[strings.ToLower(existing.RepositoryName)] {
				continue
			}
			changed = true
			if err := subPlanRepo.Delete(ctx, existing.ID); err != nil {
				return err
			}
		}
		if !changed {
			resultPlan = plan
			resultSubPlans = updatedSubPlans
			return nil
		}
		plan.OrchestratorPlan = rawOutput.Orchestration
		plan.Version = newVersion
		plan.UpdatedAt = now
		if err := planRepo.Update(ctx, plan); err != nil {
			return err
		}

		resultPlan = plan
		resultSubPlans = updatedSubPlans
		return nil
	})
	if err != nil {
		return domain.Plan{}, nil, err
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
	_, err := s.planRepo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("plan", id)
	}

	return s.planRepo.Delete(ctx, id)
}

// CreatePlanAtomic atomically deletes the old plan (when replacePlanID is non-empty)
// and creates the new plan and sub-plans in a single transaction, so the UNIQUE
// constraint on plans.work_item_id is never violated during a replace.
func (s *PlanService) CreatePlanAtomic(ctx context.Context, replacePlanID string, plan domain.Plan, subPlans []domain.TaskPlan) error {
	return s.transacter.TransactPlanRepos(ctx, func(ctx context.Context, planRepo repository.PlanRepository, subPlanRepo repository.TaskPlanRepository) error {
		// When replacing a prior plan, delete it first so the UNIQUE constraint on
		// plans.work_item_id is cleared before the INSERT. Sub-plans cascade-delete
		// automatically. Both operations land in the same transaction so there is
		// never a window where the work item has no plan row.
		if replacePlanID != "" {
			if err := planRepo.Delete(ctx, replacePlanID); err != nil {
				return fmt.Errorf("delete replaced plan: %w", err)
			}
		}
		if err := planRepo.Create(ctx, plan); err != nil {
			return fmt.Errorf("create plan: %w", err)
		}
		for i := range subPlans {
			if err := subPlanRepo.Create(ctx, subPlans[i]); err != nil {
				return fmt.Errorf("create sub-plan for %s: %w", subPlans[i].RepositoryName, err)
			}
		}
		return nil
	})
}

// SubPlan operations

// GetSubPlan retrieves a sub-plan by ID.
func (s *PlanService) GetSubPlan(ctx context.Context, id string) (domain.TaskPlan, error) {
	sp, err := s.subPlanRepo.Get(ctx, id)
	if err != nil {
		return domain.TaskPlan{}, newNotFoundError("sub-plan", id)
	}

	return sp, nil
}

// ListSubPlansByPlanID retrieves all sub-plans for a plan.
func (s *PlanService) ListSubPlansByPlanID(ctx context.Context, planID string) ([]domain.TaskPlan, error) {
	return s.subPlanRepo.ListByPlanID(ctx, planID)
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

	return s.subPlanRepo.Create(ctx, sp)
}

// TransitionSubPlan transitions a sub-plan to a new status.
func (s *PlanService) TransitionSubPlan(ctx context.Context, id string, to domain.TaskPlanStatus) error {
	sp, err := s.subPlanRepo.Get(ctx, id)
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

	return s.subPlanRepo.Update(ctx, sp)
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

// UpdateSubPlanContent updates the sub-plan content without changing status.
func (s *PlanService) UpdateSubPlanContent(ctx context.Context, id string, content string) error {
	sp, err := s.subPlanRepo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("sub-plan", id)
	}
	if sp.Status != domain.SubPlanPending && sp.Status != domain.SubPlanFailed {
		return newInvalidInputError("can only update content of pending or failed sub-plans", "status")
	}

	sp.Content = content
	sp.UpdatedAt = time.Now()

	return s.subPlanRepo.Update(ctx, sp)
}

// DeleteSubPlan deletes a sub-plan.
func (s *PlanService) DeleteSubPlan(ctx context.Context, id string) error {
	_, err := s.subPlanRepo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("sub-plan", id)
	}

	return s.subPlanRepo.Delete(ctx, id)
}

// CreateSubPlansBatch creates multiple sub-plans in a single call.
func (s *PlanService) CreateSubPlansBatch(ctx context.Context, subPlans []domain.TaskPlan) error {
	for _, sp := range subPlans {
		if err := s.CreateSubPlan(ctx, sp); err != nil {
			return err
		}
	}

	return nil
}

// AllSubPlansCompleted checks if all sub-plans for a plan are completed.
func (s *PlanService) AllSubPlansCompleted(ctx context.Context, planID string) (bool, error) {
	subPlans, err := s.subPlanRepo.ListByPlanID(ctx, planID)
	if err != nil {
		return false, err
	}

	for _, sp := range subPlans {
		if sp.Status != domain.SubPlanCompleted {
			return false, nil
		}
	}

	return len(subPlans) > 0, nil // Return true only if there are sub-plans and all are completed
}
