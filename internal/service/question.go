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

// QuestionService provides business logic for questions.
type QuestionService struct {
	transacter atomic.Transacter[repository.Resources]
	eventBus   event.Publisher
}

// NewQuestionService creates a new QuestionService.
func NewQuestionService(transacter atomic.Transacter[repository.Resources], eventBus event.Publisher) *QuestionService {
	return &QuestionService{transacter: transacter, eventBus: eventBus}
}

// questionEventPayload holds the JSON payload for question lifecycle events.
type questionEventPayload struct {
	QuestionID string `json:"question_id"`
	SessionID  string `json:"session_id,omitempty"`
	From       string `json:"from,omitempty"`
	To         string `json:"to,omitempty"`
}

// marshalQuestionPayload serializes a question event payload to JSON.
func marshalQuestionPayload(questionID, sessionID, from, to string) string {
	p := questionEventPayload{QuestionID: questionID, SessionID: sessionID, From: from, To: to}
	b, _ := json.Marshal(p)
	return string(b)
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
	var result domain.Question
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		q, err := res.Questions.Get(ctx, id)
		if err != nil {
			return newNotFoundError("question", id)
		}
		result = q
		return nil
	})
	return result, err
}

// ListBySessionID retrieves all questions for a session.
func (s *QuestionService) ListBySessionID(ctx context.Context, sessionID string) ([]domain.Question, error) {
	var result []domain.Question
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		var err error
		result, err = res.Questions.ListBySessionID(ctx, sessionID)
		return err
	})
	return result, err
}

// Create creates a new question in pending status.
func (s *QuestionService) Create(ctx context.Context, q domain.Question) error {
	if q.Stage == "" {
		q.Stage = domain.AgentSessionKindImplementation
	}
	if q.Source == "" {
		q.Source = domain.QuestionSourceAskForeman
	}
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

	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		return res.Questions.Create(ctx, q)
	})
}

// Transition transitions a question to a new status.
func (s *QuestionService) Transition(ctx context.Context, id string, to domain.QuestionStatus) error {
	var q domain.Question
	var from domain.QuestionStatus
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		question, err := res.Questions.Get(ctx, id)
		if err != nil {
			return newNotFoundError("question", id)
		}

		if !canTransitionQuestion(question.Status, to) {
			return newInvalidTransitionError(
				questionStatusName(question.Status),
				questionStatusName(to),
				"question",
			)
		}

		from = question.Status
		question.Status = to

		if err := res.Questions.Update(ctx, question); err != nil {
			return err
		}
		q = question
		return nil
	})
	if err != nil {
		return err
	}

	Emit(s.eventBus, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventQuestionStatusChanged),
		WorkspaceID: "",
		Payload:     marshalQuestionPayload(q.ID, q.AgentSessionID, string(from), string(to)),
		CreatedAt:   time.Now(),
	})
	return nil
}

// Answer transitions a question from pending to answered and records the answer.
func (s *QuestionService) Answer(ctx context.Context, id string, answer string, answeredBy string) error {
	return s.AnswerWithData(ctx, id, domain.AgentQuestionAnswer{Text: answer}, answeredBy)
}

// AnswerWithData records a normalized answer and preserves structured answer data when present.
func (s *QuestionService) AnswerWithData(ctx context.Context, id string, answer domain.AgentQuestionAnswer, answeredBy string) error {
	var q domain.Question
	var from domain.QuestionStatus
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		question, err := res.Questions.Get(ctx, id)
		if err != nil {
			return newNotFoundError("question", id)
		}

		if !canTransitionQuestion(question.Status, domain.QuestionAnswered) {
			return newInvalidTransitionError(
				questionStatusName(question.Status),
				questionStatusName(domain.QuestionAnswered),
				"question",
			)
		}

		from = question.Status
		now := time.Now()
		question.Status = domain.QuestionAnswered
		question.Answer = answer.Text
		question.AnswerData = &answer
		question.AnsweredBy = answeredBy
		question.AnsweredAt = &now

		if err := res.Questions.Update(ctx, question); err != nil {
			return err
		}
		q = question
		return nil
	})
	if err != nil {
		return err
	}

	Emit(s.eventBus, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventQuestionStatusChanged),
		WorkspaceID: "",
		Payload:     marshalQuestionPayload(q.ID, q.AgentSessionID, string(from), string(domain.QuestionAnswered)),
		CreatedAt:   time.Now(),
	})
	return nil
}

// Escalate transitions a question from pending to escalated.
func (s *QuestionService) Escalate(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.QuestionEscalated)
}

// EscalateWithProposal transitions a question from pending to escalated and records
// the Foreman's proposed answer so the TUI can pre-fill the human review form.
func (s *QuestionService) EscalateWithProposal(ctx context.Context, id string, proposedAnswer string) error {
	var q domain.Question
	var from domain.QuestionStatus
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		question, err := res.Questions.Get(ctx, id)
		if err != nil {
			return newNotFoundError("question", id)
		}
		if !canTransitionQuestion(question.Status, domain.QuestionEscalated) {
			return newInvalidTransitionError(
				questionStatusName(question.Status),
				questionStatusName(domain.QuestionEscalated),
				"question",
			)
		}
		from = question.Status
		question.Status = domain.QuestionEscalated
		question.ProposedAnswer = proposedAnswer

		if err := res.Questions.Update(ctx, question); err != nil {
			return err
		}
		q = question
		return nil
	})
	if err != nil {
		return err
	}

	Emit(s.eventBus, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventQuestionStatusChanged),
		WorkspaceID: "",
		Payload:     marshalQuestionPayload(q.ID, q.AgentSessionID, string(from), string(domain.QuestionEscalated)),
		CreatedAt:   time.Now(),
	})
	return nil
}

// UpdateContext updates the context for a pending question.
func (s *QuestionService) UpdateContext(ctx context.Context, id string, questionContext string) error {
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		q, err := res.Questions.Get(ctx, id)
		if err != nil {
			return newNotFoundError("question", id)
		}

		if q.Status != domain.QuestionPending {
			return newConstraintViolationError("cannot update context for non-pending question")
		}

		q.Context = questionContext

		return res.Questions.Update(ctx, q)
	})
}

// UpdateProposal replaces the Foreman's proposed answer for an already-escalated question.
// Uses UpdateProposedAnswer (conditional SQL: WHERE status='escalated') so a concurrent
// ResolveEscalated that already answered the question results in a no-op rather than
// reverting the row's status back to 'escalated'.
func (s *QuestionService) UpdateProposal(ctx context.Context, id, proposedAnswer string) error {
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		return res.Questions.UpdateProposedAnswer(ctx, id, proposedAnswer)
	})
}

// HasPendingQuestions checks if there are any pending questions for a session.
func (s *QuestionService) HasPendingQuestions(ctx context.Context, sessionID string) (bool, error) {
	var has bool
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		questions, err := res.Questions.ListBySessionID(ctx, sessionID)
		if err != nil {
			return err
		}

		for _, q := range questions {
			if q.Status == domain.QuestionPending {
				has = true
				return nil
			}
		}
		return nil
	})
	return has, err
}
