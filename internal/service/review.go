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

// ReviewService provides business logic for review cycles and critiques.
type ReviewService struct {
	transacter atomic.Transacter[repository.Resources]
	eventBus   event.Publisher
}

// NewReviewService creates a new ReviewService.
func NewReviewService(transacter atomic.Transacter[repository.Resources], eventBus event.Publisher) *ReviewService {
	return &ReviewService{transacter: transacter, eventBus: eventBus}
}

// reviewCycleEventPayload holds the JSON payload for review cycle lifecycle events.
type reviewCycleEventPayload struct {
	CycleID   string `json:"cycle_id"`
	SessionID string `json:"session_id,omitempty"`
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
}

// marshalReviewCyclePayload serializes a review cycle event payload to JSON.
func marshalReviewCyclePayload(cycleID, sessionID, from, to string) string {
	p := reviewCycleEventPayload{CycleID: cycleID, SessionID: sessionID, From: from, To: to}
	b, _ := json.Marshal(p)
	return string(b)
}

// critiqueEventPayload holds the JSON payload for critique lifecycle events.
type critiqueEventPayload struct {
	CritiqueID    string `json:"critique_id"`
	ReviewCycleID string `json:"review_cycle_id,omitempty"`
	From          string `json:"from,omitempty"`
	To            string `json:"to,omitempty"`
}

// marshalCritiquePayload serializes a critique event payload to JSON.
func marshalCritiquePayload(critiqueID, reviewCycleID, from, to string) string {
	p := critiqueEventPayload{CritiqueID: critiqueID, ReviewCycleID: reviewCycleID, From: from, To: to}
	b, _ := json.Marshal(p)
	return string(b)
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
	return slices.Contains(allowed, to)
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
	return slices.Contains(allowed, to)
}

// ReviewCycle operations

// GetCycle retrieves a review cycle by ID.
func (s *ReviewService) GetCycle(ctx context.Context, id string) (domain.ReviewCycle, error) {
	var result domain.ReviewCycle
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		cycle, err := res.Reviews.GetCycle(ctx, id)
		if err != nil {
			return newNotFoundError("review cycle", id)
		}
		result = cycle
		return nil
	})
	return result, err
}

// ListCyclesBySessionID retrieves all review cycles for a session.
func (s *ReviewService) ListCyclesBySessionID(ctx context.Context, sessionID string) ([]domain.ReviewCycle, error) {
	var result []domain.ReviewCycle
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		var err error
		result, err = res.Reviews.ListCyclesBySessionID(ctx, sessionID)
		return err
	})
	return result, err
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

	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		return res.Reviews.CreateCycle(ctx, cycle)
	})
}

// TransitionCycle transitions a review cycle to a new status.
func (s *ReviewService) TransitionCycle(ctx context.Context, id string, to domain.ReviewCycleStatus) error {
	var cycle domain.ReviewCycle
	var from domain.ReviewCycleStatus
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		c, err := res.Reviews.GetCycle(ctx, id)
		if err != nil {
			return newNotFoundError("review cycle", id)
		}

		if !canTransitionReviewCycle(c.Status, to) {
			return newInvalidTransitionError(
				reviewCycleStatusName(c.Status),
				reviewCycleStatusName(to),
				"review cycle",
			)
		}

		from = c.Status
		c.Status = to
		c.UpdatedAt = time.Now()

		if err := res.Reviews.UpdateCycle(ctx, c); err != nil {
			return err
		}
		cycle = c
		return nil
	})
	if err != nil {
		return err
	}

	Emit(s.eventBus, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventReviewCycleStatusChanged),
		WorkspaceID: "",
		Payload:     marshalReviewCyclePayload(cycle.ID, cycle.AgentSessionID, string(from), string(to)),
		CreatedAt:   time.Now(),
	})
	return nil
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
	var cycle domain.ReviewCycle
	var from domain.ReviewCycleStatus
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		c, err := res.Reviews.GetCycle(ctx, id)
		if err != nil {
			return newNotFoundError("review cycle", id)
		}

		if !canTransitionReviewCycle(c.Status, domain.ReviewCycleFailed) {
			return newInvalidTransitionError(
				reviewCycleStatusName(c.Status),
				reviewCycleStatusName(domain.ReviewCycleFailed),
				"review cycle",
			)
		}

		// Delegate to TransitionCycle would open a nested transaction;
		// perform the transition inline instead.
		from = c.Status
		c.Status = domain.ReviewCycleFailed
		c.UpdatedAt = time.Now()

		if err := res.Reviews.UpdateCycle(ctx, c); err != nil {
			return err
		}
		cycle = c
		return nil
	})
	if err != nil {
		return err
	}

	Emit(s.eventBus, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventReviewCycleStatusChanged),
		WorkspaceID: "",
		Payload:     marshalReviewCyclePayload(cycle.ID, cycle.AgentSessionID, string(from), string(domain.ReviewCycleFailed)),
		CreatedAt:   time.Now(),
	})
	return nil
}

// UpdateCycleSummary updates the review cycle summary.
func (s *ReviewService) UpdateCycleSummary(ctx context.Context, id string, summary string) error {
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		cycle, err := res.Reviews.GetCycle(ctx, id)
		if err != nil {
			return newNotFoundError("review cycle", id)
		}

		cycle.Summary = summary
		cycle.UpdatedAt = time.Now()

		return res.Reviews.UpdateCycle(ctx, cycle)
	})
}

// Critique operations

// GetCritique retrieves a critique by ID.
func (s *ReviewService) GetCritique(ctx context.Context, id string) (domain.Critique, error) {
	var result domain.Critique
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		critique, err := res.Reviews.GetCritique(ctx, id)
		if err != nil {
			return newNotFoundError("critique", id)
		}
		result = critique
		return nil
	})
	return result, err
}

// ListCritiquesByCycleID retrieves all critiques for a review cycle.
func (s *ReviewService) ListCritiquesByCycleID(ctx context.Context, cycleID string) ([]domain.Critique, error) {
	var result []domain.Critique
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		var err error
		result, err = res.Reviews.ListCritiquesByReviewCycleID(ctx, cycleID)
		return err
	})
	return result, err
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

	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		return res.Reviews.CreateCritique(ctx, critique)
	})
}

// TransitionCritique transitions a critique to a new status.
func (s *ReviewService) TransitionCritique(ctx context.Context, id string, to domain.CritiqueStatus) error {
	var critique domain.Critique
	var from domain.CritiqueStatus
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		c, err := res.Reviews.GetCritique(ctx, id)
		if err != nil {
			return newNotFoundError("critique", id)
		}

		if !canTransitionCritique(c.Status, to) {
			return newInvalidTransitionError(
				critiqueStatusName(c.Status),
				critiqueStatusName(to),
				"critique",
			)
		}

		from = c.Status
		c.Status = to

		if err := res.Reviews.UpdateCritique(ctx, c); err != nil {
			return err
		}
		critique = c
		return nil
	})
	if err != nil {
		return err
	}

	Emit(s.eventBus, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventCritiqueStatusChanged),
		WorkspaceID: "",
		Payload:     marshalCritiquePayload(critique.ID, critique.ReviewCycleID, string(from), string(to)),
		CreatedAt:   time.Now(),
	})
	return nil
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
	var count int
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		critiques, err := res.Reviews.ListCritiquesByReviewCycleID(ctx, cycleID)
		if err != nil {
			return err
		}

		for _, c := range critiques {
			if c.Status == domain.CritiqueOpen && (c.Severity == domain.CritiqueMajor || c.Severity == domain.CritiqueCritical) {
				count++
			}
		}
		return nil
	})
	return count, err
}

// HasUnresolvedCritiques checks if there are any unresolved critiques in a cycle.
func (s *ReviewService) HasUnresolvedCritiques(ctx context.Context, cycleID string) (bool, error) {
	var has bool
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		critiques, err := res.Reviews.ListCritiquesByReviewCycleID(ctx, cycleID)
		if err != nil {
			return err
		}

		for _, c := range critiques {
			if c.Status == domain.CritiqueOpen {
				has = true
				return nil
			}
		}
		return nil
	})
	return has, err
}
