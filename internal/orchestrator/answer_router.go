package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/service"
)

// answerRouter implements AnswerRouter. It is stateless and delegates
// to SessionRegistry and ForemanHandler based on question stage.
type answerRouter struct {
	registry    SessionRegistry // Interface, not concrete type
	questionSvc *service.QuestionService
	sessionSvc  *service.AgentSessionService
	eventBus    event.Publisher
}

// NewAnswerRouter creates a new AnswerRouter.
func NewAnswerRouter(
	registry SessionRegistry,
	questionSvc *service.QuestionService,
	sessionSvc *service.AgentSessionService,
	eventBus event.Publisher,
) *answerRouter {
	return &answerRouter{
		registry:    registry,
		questionSvc: questionSvc,
		sessionSvc:  sessionSvc,
		eventBus:    eventBus,
	}
}

// Compile-time check that answerRouter implements AnswerRouter.
var _ AnswerRouter = (*answerRouter)(nil)

// getForeman looks up the foreman for a question dynamically.
// It finds the workItemID from the question's session, then gets the foreman from registry.
func (r *answerRouter) getForeman(ctx context.Context, questionID string) (*Foreman, error) {
	q, err := r.questionSvc.Get(ctx, questionID)
	if err != nil {
		return nil, fmt.Errorf("get question: %w", err)
	}

	session, err := r.sessionSvc.Get(ctx, q.AgentSessionID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	foreman := r.registry.GetForeman(session.WorkItemID)
	if foreman == nil {
		return nil, nil // No foreman for this work item
	}
	return foreman, nil
}

// Answer routes an answer based on the question's stage (AgentSessionKind).
// Internal helper that does the actual routing; publishes event on success.
func (r *answerRouter) Answer(ctx context.Context, questionID, answer, answeredBy string) error {
	q, err := r.questionSvc.Get(ctx, questionID)
	if err != nil {
		return fmt.Errorf("get question: %w", err)
	}

	switch q.Stage {
	case domain.AgentSessionKindPlanning:
		return r.answerPlanningQuestion(ctx, q, answer, answeredBy)
	case domain.AgentSessionKindImplementation, domain.AgentSessionKindReview, "":
		return r.answerImplementationQuestion(ctx, q, answer, answeredBy)
	case domain.AgentSessionKindManual:
		return r.answerManualQuestion(ctx, q, answer, answeredBy)
	case domain.AgentSessionKindForeman:
		return fmt.Errorf("route answer: foreman sessions cannot receive answers (question %s)", questionID)
	default:
		return fmt.Errorf("unsupported stage: %q", q.Stage)
	}
}

func (r *answerRouter) answerPlanningQuestion(ctx context.Context, q domain.Question, answer, answeredBy string) error {
	if err := r.registry.SendAnswer(ctx, q.AgentSessionID, answer); err != nil && !errors.Is(err, ErrSessionNotRunning) {
		return fmt.Errorf("send planning answer: %w", err)
	}
	if err := r.questionSvc.Answer(ctx, q.ID, answer, answeredBy); err != nil {
		return fmt.Errorf("persist answer: %w", err)
	}
	if err := r.sessionSvc.ResumeFromAnswer(ctx, q.AgentSessionID); err != nil {
		slog.Warn("failed to resume planning session", "error", err, "session_id", q.AgentSessionID)
	}
	return r.publishAnswered(ctx, q.ID, q.AgentSessionID)
}

func (r *answerRouter) answerImplementationQuestion(ctx context.Context, q domain.Question, answer, answeredBy string) error {
	// Look up the foreman dynamically for this question's work item
	foremanHandler, err := r.getForeman(ctx, q.ID)
	if err != nil {
		return fmt.Errorf("get foreman handler: %w", err)
	}

	// Try foreman escalation first
	if foremanHandler != nil {
		err := foremanHandler.ResolveEscalated(ctx, q.ID, answer)
		if err == nil {
			return nil // Escalation handled; event already published by foreman
		}
		if !errors.Is(err, ErrQuestionNotEscalated) {
			return fmt.Errorf("resolve escalated: %w", err)
		}
		// Fall through to non-escalated path
	}

	// Non-escalated fallback: resume session and persist answer
	if q.AgentSessionID != "" {
		if err := r.sessionSvc.ResumeFromAnswer(ctx, q.AgentSessionID); err != nil {
			slog.Warn("failed to resume impl session", "error", err, "session_id", q.AgentSessionID)
		}
	}
	if err := r.questionSvc.Answer(ctx, q.ID, answer, answeredBy); err != nil {
		return fmt.Errorf("persist answer: %w", err)
	}
	return r.publishAnswered(ctx, q.ID, q.AgentSessionID)
}

func (r *answerRouter) answerManualQuestion(ctx context.Context, q domain.Question, answer, answeredBy string) error {
	if err := r.registry.SendAnswer(ctx, q.AgentSessionID, answer); err != nil && !errors.Is(err, ErrSessionNotRunning) {
		return fmt.Errorf("send manual answer: %w", err)
	}
	if err := r.sessionSvc.ResumeFromAnswer(ctx, q.AgentSessionID); err != nil {
		slog.Warn("failed to resume manual session", "error", err, "session_id", q.AgentSessionID)
	}
	if err := r.questionSvc.Answer(ctx, q.ID, answer, answeredBy); err != nil {
		return fmt.Errorf("persist answer: %w", err)
	}
	return r.publishAnswered(ctx, q.ID, q.AgentSessionID)
}

// Skip routes a skip based on the question's stage (AgentSessionKind).
func (r *answerRouter) Skip(ctx context.Context, questionID string) error {
	return r.Answer(ctx, questionID, "", "human")
}

// RefineAnswer sends human follow-up text to get a revised answer proposal.
// The escalation remains open; human must still call ResolveEscalated to finalize.
func (r *answerRouter) RefineAnswer(ctx context.Context, questionID, text string) (string, bool, error) {
	foreman, err := r.getForeman(ctx, questionID)
	if err != nil {
		return "", false, fmt.Errorf("get foreman: %w", err)
	}
	if foreman == nil {
		return "", false, fmt.Errorf("no foreman available for question")
	}
	return foreman.RefineAnswer(ctx, questionID, text)
}

func (r *answerRouter) publishAnswered(ctx context.Context, questionID, sessionID string) error {
	data, err := json.Marshal(map[string]string{"id": questionID, "agent_session_id": sessionID})
	if err != nil {
		return fmt.Errorf("marshal answered event: %w", err)
	}
	return r.eventBus.Publish(ctx, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventAgentQuestionAnswered),
		WorkspaceID: "",
		Payload:     string(data),
		CreatedAt:   time.Now(),
	})
}
