package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/service"
)

// QuestionRouter is the single stage-aware routing point for normalized agent questions.
// It looks up the correct foreman dynamically per question using the work item ID.
type QuestionRouter struct {
	questionSvc *service.QuestionService
	sessionSvc  *service.AgentSessionService
	registry    SessionRegistry
	eventBus    event.Publisher
}

func NewQuestionRouter(questionSvc *service.QuestionService, sessionSvc *service.AgentSessionService, registry SessionRegistry, eventBus event.Publisher) *QuestionRouter {
	return &QuestionRouter{
		questionSvc: questionSvc,
		sessionSvc:  sessionSvc,
		registry:    registry,
		eventBus:    eventBus,
	}
}

func (r *QuestionRouter) Route(ctx context.Context, kind domain.AgentSessionKind, evt adapter.AgentEvent, sessionID string) error {
	switch kind {
	case domain.AgentSessionKindPlanning:
		return r.routePlanning(ctx, evt, sessionID)
	case domain.AgentSessionKindImplementation, domain.AgentSessionKindReview:
		return r.routeImplementation(ctx, evt, sessionID)
	case domain.AgentSessionKindManual:
		return r.routeManual(ctx, evt, sessionID)
	case domain.AgentSessionKindForeman:
		return fmt.Errorf("route question: foreman sessions cannot route questions (session %s)", sessionID)
	default:
		return fmt.Errorf("route question: unsupported kind %q", kind)
	}
}

func (r *QuestionRouter) routeManual(ctx context.Context, evt adapter.AgentEvent, sessionID string) error {
	q := questionFromEvent(evt, sessionID, domain.AgentSessionKindManual)
	if err := r.persistAndPublish(ctx, q, "manual question raised"); err != nil {
		return err
	}
	if err := r.sessionSvc.WaitForAnswer(ctx, sessionID); err != nil {
		return fmt.Errorf("mark manual session waiting for answer: %w", err)
	}
	return nil
}

func (r *QuestionRouter) routePlanning(ctx context.Context, evt adapter.AgentEvent, sessionID string) error {
	q := questionFromEvent(evt, sessionID, domain.AgentSessionKindPlanning)
	if err := r.persistAndPublish(ctx, q, "planning question raised"); err != nil {
		return err
	}
	if err := r.sessionSvc.WaitForAnswer(ctx, sessionID); err != nil {
		return fmt.Errorf("mark planning session waiting for answer: %w", err)
	}
	return nil
}

func (r *QuestionRouter) routeImplementation(ctx context.Context, evt adapter.AgentEvent, sessionID string) error {
	q := questionFromEvent(evt, sessionID, domain.AgentSessionKindImplementation)
	if err := r.persistAndPublish(ctx, q, "implementation question raised"); err != nil {
		return err
	}

	// Look up the foreman dynamically for this question's work item
	workItemID, err := r.sessionWorkItemID(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("route implementation question: look up work item: %w", err)
	}
	foreman := r.registry.GetForeman(workItemID)
	if foreman == nil {
		return fmt.Errorf("route implementation question: no foreman registered for work item %q", workItemID)
	}

	answerCh := foreman.Ask(ctx, q)
	go func() {
		select {
		case answer, ok := <-answerCh:
			if !ok || answer == "" {
				slog.Warn("foreman answer channel closed without answer", "question_id", q.ID)
				return
			}
			if err := r.registry.SendAnswer(ctx, sessionID, answer); err != nil {
				slog.Error("failed to send foreman answer to agent session", "error", err, "question_id", q.ID, "agent_session_id", sessionID)
				return
			}
			if err := r.publishAnswered(ctx, q.ID, sessionID); err != nil {
				slog.Warn("failed to publish question answered event", "error", err)
			}
		case <-ctx.Done():
			slog.Warn("context cancelled while waiting for foreman answer", "error", ctx.Err(), "question_id", q.ID)
		}
	}()
	return nil
}

func (r *QuestionRouter) persistAndPublish(ctx context.Context, q domain.Question, label string) error {
	if err := r.questionSvc.Create(ctx, q); err != nil {
		return fmt.Errorf("%s: persist question: %w", label, err)
	}
	// Publish with top-level work_item_id (looked up from the session) and nested question.
	workItemID, err := r.sessionWorkItemID(ctx, q.AgentSessionID)
	if err != nil {
		slog.Warn("failed to look up work_item_id for question event", "error", err, "session_id", q.AgentSessionID)
		workItemID = ""
	}
	payload := questionRaisedPayload{
		WorkItemID:     workItemID,
		AgentSessionID: q.AgentSessionID,
		Question:       q,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("%s: marshal question raised payload: %w", label, err)
	}
	if err := r.eventBus.Publish(ctx, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventAgentQuestionRaised),
		WorkspaceID: "",
		Payload:     string(data),
		CreatedAt:   time.Now(),
	}); err != nil {
		slog.Warn("failed to publish question raised event", "error", err, "question_id", q.ID)
	}
	return nil
}

// questionRaisedPayload is the typed payload for EventAgentQuestionRaised.
type questionRaisedPayload struct {
	WorkItemID     string          `json:"work_item_id,omitempty"`
	AgentSessionID string          `json:"agent_session_id"`
	Question       domain.Question `json:"question"`
}

// sessionWorkItemID looks up the work item ID for an agent session.
func (r *QuestionRouter) sessionWorkItemID(ctx context.Context, sessionID string) (string, error) {
	session, err := r.sessionSvc.Get(ctx, sessionID)
	if err != nil {
		return "", err
	}
	return session.WorkItemID, nil
}

func PublishQuestionAnswered(ctx context.Context, eventBus event.Publisher, questionID, sessionID string) error {
	return eventBus.Publish(ctx, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventAgentQuestionAnswered),
		WorkspaceID: "",
		Payload:     marshalJSONOrEmpty("agent_question.answered", map[string]string{"id": questionID, "agent_session_id": sessionID}),
		CreatedAt:   time.Now(),
	})
}

func (r *QuestionRouter) publishAnswered(ctx context.Context, questionID, sessionID string) error {
	return PublishQuestionAnswered(ctx, r.eventBus, questionID, sessionID)
}

func questionFromEvent(evt adapter.AgentEvent, sessionID string, kind domain.AgentSessionKind) domain.Question {
	content := evt.Payload
	contextText := ""
	source := domain.QuestionSourceAskForeman
	var structured *domain.StructuredQuestionSet
	if evt.Metadata != nil {
		if c, ok := evt.Metadata["context"].(string); ok {
			contextText = c
		}
		if src, ok := evt.Metadata["source"].(string); ok && src != "" {
			source = domain.QuestionSource(src)
		}
	}
	if evt.Question != nil {
		if evt.Question.FreeText != "" {
			content = evt.Question.FreeText
		}
		if evt.Question.Context != "" {
			contextText = evt.Question.Context
		}
		if evt.Question.Source != "" {
			source = domain.QuestionSource(evt.Question.Source)
		}
		structured = domainStructuredQuestionSet(evt.Question.Structured)
		if content == "" && structured != nil && len(structured.Questions) > 0 {
			content = structured.Questions[0].Question
		}
	}
	return domain.Question{
		ID:             domain.NewID(),
		AgentSessionID: sessionID,
		Stage:          kind,
		Source:         source,
		Content:        content,
		Context:        contextText,
		Structured:     structured,
		Status:         domain.QuestionPending,
	}
}

func domainStructuredQuestionSet(src *adapter.StructuredQuestionSet) *domain.StructuredQuestionSet {
	if src == nil {
		return nil
	}
	questions := make([]domain.StructuredQuestion, 0, len(src.Questions))
	for _, q := range src.Questions {
		options := make([]domain.QuestionOption, 0, len(q.Options))
		for _, opt := range q.Options {
			options = append(options, domain.QuestionOption{Label: opt.Label, Description: opt.Description, Preview: opt.Preview})
		}
		questions = append(questions, domain.StructuredQuestion{ID: q.ID, Question: q.Question, Header: q.Header, Options: options, MultiSelect: q.MultiSelect, RecommendedIndex: q.RecommendedIndex})
	}
	return &domain.StructuredQuestionSet{Questions: questions, SupportsCustomAnswer: src.SupportsCustomAnswer, SupportsAnnotations: src.SupportsAnnotations, NativeResponseFormat: src.NativeResponseFormat}
}
