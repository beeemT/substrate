package service

import (
	"context"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

// ReviewService provides business logic for review cycles and critiques.
type ReviewService struct {
	repo repository.ReviewRepository
}

// NewReviewService creates a new ReviewService.
func NewReviewService(repo repository.ReviewRepository) *ReviewService {
	return &ReviewService{repo: repo}
}

// ReviewCycle state transitions
var validReviewCycleTransitions = map[domain.ReviewCycleStatus][]domain.ReviewCycleStatus{
	domain.ReviewCycleReviewing:      {domain.ReviewCycleCritiquesFound, domain.ReviewCyclePassed, domain.ReviewCycleFailed},
	domain.ReviewCycleCritiquesFound: {domain.ReviewCycleReimplementing, domain.ReviewCycleFailed},
	domain.ReviewCycleReimplementing: {domain.ReviewCycleReviewing, domain.ReviewCycleFailed},
	domain.ReviewCyclePassed:         {}, // Terminal state
	domain.ReviewCycleFailed:         {}, // Terminal state
}

func canTransitionReviewCycle(from, to domain.ReviewCycleStatus) bool {
	allowed, exists := validReviewCycleTransitions[from]
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

// Critique state transitions
var validCritiqueTransitions = map[domain.CritiqueStatus][]domain.CritiqueStatus{
	domain.CritiqueOpen:     {domain.CritiqueResolved},
	domain.CritiqueResolved: {}, // Terminal state
}

func canTransitionCritique(from, to domain.CritiqueStatus) bool {
	allowed, exists := validCritiqueTransitions[from]
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

// ReviewCycle operations

// GetCycle retrieves a review cycle by ID.
func (s *ReviewService) GetCycle(ctx context.Context, id string) (domain.ReviewCycle, error) {
	cycle, err := s.repo.GetCycle(ctx, id)
	if err != nil {
		return domain.ReviewCycle{}, newNotFoundError("review cycle", id)
	}
	return cycle, nil
}

// ListCyclesBySessionID retrieves all review cycles for a session.
func (s *ReviewService) ListCyclesBySessionID(ctx context.Context, sessionID string) ([]domain.ReviewCycle, error) {
	return s.repo.ListCyclesBySessionID(ctx, sessionID)
}

// CreateCycle creates a new review cycle in reviewing status.
func (s *ReviewService) CreateCycle(ctx context.Context, cycle domain.ReviewCycle) error {
	// Set initial status if not set
	if cycle.Status == "" {
		cycle.Status = domain.ReviewCycleReviewing
	}
	// Validate initial status
	if cycle.Status != domain.ReviewCycleReviewing {
		return newInvalidInputError("initial status must be reviewing", "status")
	}
	// Set timestamps
	now := time.Now()
	cycle.CreatedAt = now
	cycle.UpdatedAt = now

	return s.repo.CreateCycle(ctx, cycle)
}

// TransitionCycle transitions a review cycle to a new status.
func (s *ReviewService) TransitionCycle(ctx context.Context, id string, to domain.ReviewCycleStatus) error {
	cycle, err := s.repo.GetCycle(ctx, id)
	if err != nil {
		return newNotFoundError("review cycle", id)
	}

	if !canTransitionReviewCycle(cycle.Status, to) {
		return newInvalidTransitionError(
			reviewCycleStatusName(cycle.Status),
			reviewCycleStatusName(to),
			"review cycle",
		)
	}

	cycle.Status = to
	cycle.UpdatedAt = time.Now()

	return s.repo.UpdateCycle(ctx, cycle)
}

// RecordCritiques transitions a review cycle from reviewing to critiques_found.
func (s *ReviewService) RecordCritiques(ctx context.Context, id string) error {
	return s.TransitionCycle(ctx, id, domain.ReviewCycleCritiquesFound)
}

// PassReview transitions a review cycle from reviewing to passed.
func (s *ReviewService) PassReview(ctx context.Context, id string) error {
	return s.TransitionCycle(ctx, id, domain.ReviewCyclePassed)
}

// StartReimplementation transitions a review cycle from critiques_found to reimplementing.
func (s *ReviewService) StartReimplementation(ctx context.Context, id string) error {
	return s.TransitionCycle(ctx, id, domain.ReviewCycleReimplementing)
}

// CompleteReimplementation transitions a review cycle from reimplementing to reviewing.
func (s *ReviewService) CompleteReimplementation(ctx context.Context, id string) error {
	return s.TransitionCycle(ctx, id, domain.ReviewCycleReviewing)
}

// FailReviewCycle transitions a review cycle to failed.
func (s *ReviewService) FailReviewCycle(ctx context.Context, id string) error {
	cycle, err := s.repo.GetCycle(ctx, id)
	if err != nil {
		return newNotFoundError("review cycle", id)
	}

	if !canTransitionReviewCycle(cycle.Status, domain.ReviewCycleFailed) {
		return newInvalidTransitionError(
			reviewCycleStatusName(cycle.Status),
			reviewCycleStatusName(domain.ReviewCycleFailed),
			"review cycle",
		)
	}

	return s.TransitionCycle(ctx, id, domain.ReviewCycleFailed)
}

// UpdateCycleSummary updates the review cycle summary.
func (s *ReviewService) UpdateCycleSummary(ctx context.Context, id string, summary string) error {
	cycle, err := s.repo.GetCycle(ctx, id)
	if err != nil {
		return newNotFoundError("review cycle", id)
	}

	cycle.Summary = summary
	cycle.UpdatedAt = time.Now()

	return s.repo.UpdateCycle(ctx, cycle)
}

// Critique operations

// GetCritique retrieves a critique by ID.
func (s *ReviewService) GetCritique(ctx context.Context, id string) (domain.Critique, error) {
	critique, err := s.repo.GetCritique(ctx, id)
	if err != nil {
		return domain.Critique{}, newNotFoundError("critique", id)
	}
	return critique, nil
}

// ListCritiquesByCycleID retrieves all critiques for a review cycle.
func (s *ReviewService) ListCritiquesByCycleID(ctx context.Context, cycleID string) ([]domain.Critique, error) {
	return s.repo.ListCritiquesByReviewCycleID(ctx, cycleID)
}

// CreateCritique creates a new critique in open status.
func (s *ReviewService) CreateCritique(ctx context.Context, critique domain.Critique) error {
	// Set initial status if not set
	if critique.Status == "" {
		critique.Status = domain.CritiqueOpen
	}
	// Validate initial status
	if critique.Status != domain.CritiqueOpen {
		return newInvalidInputError("initial status must be open", "status")
	}
	// Set timestamps
	now := time.Now()
	critique.CreatedAt = now

	return s.repo.CreateCritique(ctx, critique)
}

// TransitionCritique transitions a critique to a new status.
func (s *ReviewService) TransitionCritique(ctx context.Context, id string, to domain.CritiqueStatus) error {
	critique, err := s.repo.GetCritique(ctx, id)
	if err != nil {
		return newNotFoundError("critique", id)
	}

	if !canTransitionCritique(critique.Status, to) {
		return newInvalidTransitionError(
			critiqueStatusName(critique.Status),
			critiqueStatusName(to),
			"critique",
		)
	}

	critique.Status = to

	return s.repo.UpdateCritique(ctx, critique)
}

// ResolveCritique transitions a critique from open to resolved.
func (s *ReviewService) ResolveCritique(ctx context.Context, id string) error {
	return s.TransitionCritique(ctx, id, domain.CritiqueResolved)
}

// CreateCritiquesBatch creates multiple critiques in a single call.
func (s *ReviewService) CreateCritiquesBatch(ctx context.Context, critiques []domain.Critique) error {
	for _, c := range critiques {
		if err := s.CreateCritique(ctx, c); err != nil {
			return err
		}
	}
	return nil
}

// CountMajorCritiques counts the number of major or critical critiques in a cycle.
func (s *ReviewService) CountMajorCritiques(ctx context.Context, cycleID string) (int, error) {
	critiques, err := s.repo.ListCritiquesByReviewCycleID(ctx, cycleID)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, c := range critiques {
		if c.Status == domain.CritiqueOpen && (c.Severity == domain.CritiqueMajor || c.Severity == domain.CritiqueCritical) {
			count++
		}
	}
	return count, nil
}

// HasUnresolvedCritiques checks if there are any unresolved critiques in a cycle.
func (s *ReviewService) HasUnresolvedCritiques(ctx context.Context, cycleID string) (bool, error) {
	critiques, err := s.repo.ListCritiquesByReviewCycleID(ctx, cycleID)
	if err != nil {
		return false, err
	}

	for _, c := range critiques {
		if c.Status == domain.CritiqueOpen {
			return true, nil
		}
	}
	return false, nil
}
