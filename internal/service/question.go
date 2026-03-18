package service

import (
	"context"
	"slices"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

// QuestionService provides business logic for questions.
type QuestionService struct {
	repo repository.QuestionRepository
}

// NewQuestionService creates a new QuestionService.
func NewQuestionService(repo repository.QuestionRepository) *QuestionService {
	return &QuestionService{repo: repo}
}

// Question state transitions
var validQuestionTransitions = map[domain.QuestionStatus][]domain.QuestionStatus{
	domain.QuestionPending:   {domain.QuestionAnswered, domain.QuestionEscalated},
	domain.QuestionAnswered:  {},                        // Terminal state
	domain.QuestionEscalated: {domain.QuestionAnswered}, // Human can still answer an escalated question
}

func canTransitionQuestion(from, to domain.QuestionStatus) bool {
	allowed, exists := validQuestionTransitions[from]
	if !exists {
		return false
	}
	return slices.Contains(allowed, to)
}

// Get retrieves a question by ID.
func (s *QuestionService) Get(ctx context.Context, id string) (domain.Question, error) {
	q, err := s.repo.Get(ctx, id)
	if err != nil {
		return domain.Question{}, newNotFoundError("question", id)
	}

	return q, nil
}

// ListBySessionID retrieves all questions for a session.
func (s *QuestionService) ListBySessionID(ctx context.Context, sessionID string) ([]domain.Question, error) {
	return s.repo.ListBySessionID(ctx, sessionID)
}

// Create creates a new question in pending status.
func (s *QuestionService) Create(ctx context.Context, q domain.Question) error {
	// Set initial status if not set
	if q.Status == "" {
		q.Status = domain.QuestionPending
	}
	// Validate initial status
	if q.Status != domain.QuestionPending {
		return newInvalidInputError("initial status must be pending", "status")
	}
	// Set timestamps
	now := time.Now()
	q.CreatedAt = now

	return s.repo.Create(ctx, q)
}

// Transition transitions a question to a new status.
func (s *QuestionService) Transition(ctx context.Context, id string, to domain.QuestionStatus) error {
	q, err := s.repo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("question", id)
	}

	if !canTransitionQuestion(q.Status, to) {
		return newInvalidTransitionError(
			questionStatusName(q.Status),
			questionStatusName(to),
			"question",
		)
	}

	q.Status = to

	return s.repo.Update(ctx, q)
}

// Answer transitions a question from pending to answered and records the answer.
func (s *QuestionService) Answer(ctx context.Context, id string, answer string, answeredBy string) error {
	q, err := s.repo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("question", id)
	}

	if !canTransitionQuestion(q.Status, domain.QuestionAnswered) {
		return newInvalidTransitionError(
			questionStatusName(q.Status),
			questionStatusName(domain.QuestionAnswered),
			"question",
		)
	}

	now := time.Now()
	q.Status = domain.QuestionAnswered
	q.Answer = answer
	q.AnsweredBy = answeredBy
	q.AnsweredAt = &now

	return s.repo.Update(ctx, q)
}

// Escalate transitions a question from pending to escalated.
func (s *QuestionService) Escalate(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.QuestionEscalated)
}

// EscalateWithProposal transitions a question from pending to escalated and records
// the Foreman's proposed answer so the TUI can pre-fill the human review form.
func (s *QuestionService) EscalateWithProposal(ctx context.Context, id string, proposedAnswer string) error {
	q, err := s.repo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("question", id)
	}
	if !canTransitionQuestion(q.Status, domain.QuestionEscalated) {
		return newInvalidTransitionError(
			questionStatusName(q.Status),
			questionStatusName(domain.QuestionEscalated),
			"question",
		)
	}
	q.Status = domain.QuestionEscalated
	q.ProposedAnswer = proposedAnswer

	return s.repo.Update(ctx, q)
}

// UpdateContext updates the context for a pending question.
func (s *QuestionService) UpdateContext(ctx context.Context, id string, context string) error {
	q, err := s.repo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("question", id)
	}

	if q.Status != domain.QuestionPending {
		return newConstraintViolationError("cannot update context for non-pending question")
	}

	q.Context = context

	return s.repo.Update(ctx, q)
}

// UpdateProposal replaces the Foreman's proposed answer for an already-escalated question.
// Uses UpdateProposedAnswer (conditional SQL: WHERE status='escalated') so a concurrent
// ResolveEscalated that already answered the question results in a no-op rather than
// reverting the row's status back to 'escalated'.
func (s *QuestionService) UpdateProposal(ctx context.Context, id, proposedAnswer string) error {
	return s.repo.UpdateProposedAnswer(ctx, id, proposedAnswer)
}

// HasPendingQuestions checks if there are any pending questions for a session.
func (s *QuestionService) HasPendingQuestions(ctx context.Context, sessionID string) (bool, error) {
	questions, err := s.repo.ListBySessionID(ctx, sessionID)
	if err != nil {
		return false, err
	}

	for _, q := range questions {
		if q.Status == domain.QuestionPending {
			return true, nil
		}
	}

	return false, nil
}
