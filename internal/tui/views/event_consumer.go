package views

import (
	"encoding/json"
	"errors"
	"log/slog"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
)

// EventConsumer bridges the event.Bus to Bubble Tea's update loop.
// It provides helper methods to convert domain events to tea.Msg values.
type EventConsumer struct {
	app *App
	sub *event.Subscriber
}

// NewEventConsumer creates a new EventConsumer for the given subscriber.
func NewEventConsumer(app *App, sub *event.Subscriber) *EventConsumer {
	return &EventConsumer{app: app, sub: sub}
}

// BridgeCmd returns a tea.Cmd that reads events from the subscriber channel
// and forwards them as DomainEventMsg to the update loop.
func (ec *EventConsumer) BridgeCmd() tea.Cmd {
	return func() tea.Msg {
		for evt := range ec.sub.C {
			return DomainEventMsg{Event: evt}
		}
		return nil
	}
}

// eventDecoder is a function that decodes an event payload into a tea.Msg.
type eventDecoder func(payload string) tea.Msg

// eventHandlerRegistry maps domain event types to their decoder functions.
var eventHandlerRegistry = map[domain.EventType]eventDecoder{
	domain.EventWorkItemIngested:        decodeWorkItemIngested,
	domain.EventWorkItemPlanning:        decodeWorkItemState,
	domain.EventWorkItemPlanReview:      decodeWorkItemState,
	domain.EventWorkItemApproved:        decodeWorkItemState,
	domain.EventWorkItemImplementing:    decodeWorkItemState,
	domain.EventWorkItemReviewing:       decodeWorkItemState,
	domain.EventWorkItemCompleted:       decodeWorkItemState,
	domain.EventWorkItemFailed:          decodeWorkItemState,
	domain.EventWorkItemMerged:          decodeWorkItemState,
	domain.EventPlanGenerated:           decodePlanGenerated,
	domain.EventPlanSubmittedForReview:  decodePlanUpdated,
	domain.EventPlanApproved:            decodePlanUpdated,
	domain.EventPlanRejected:            decodePlanUpdated,
	domain.EventPlanRevised:             decodePlanUpdated,
	domain.EventPlanFailed:              decodePlanUpdated,
	domain.EventAgentSessionStarted:     decodeAgentSessionStarted,
	domain.EventAgentSessionCompleted:   decodeAgentSessionUpdated,
	domain.EventAgentSessionFailed:      decodeAgentSessionUpdated,
	domain.EventAgentSessionInterrupted: decodeAgentSessionUpdated,
	domain.EventAgentSessionResumed:     decodeAgentSessionResumed,
	domain.EventAgentQuestionRaised:     decodeQuestionRaised,
	domain.EventAgentQuestionAnswered:   decodeQuestionAnswered,
	domain.EventReviewStarted:           decodeReviewStarted,
	domain.EventReviewCompleted:         decodeReviewCompleted,
	domain.EventCritiquesFound:          decodeCritiquesFound,
	domain.EventReimplementationStarted: decodeReimplementationStarted,
	domain.EventAdapterError:            decodeAdapterError,
	domain.EventPRMerged:                decodePRMerged,
}

// toMsg converts a domain.SystemEvent to a tea.Msg for the update loop.
func (ec *EventConsumer) toMsg(evt domain.SystemEvent) tea.Msg {
	decoder, ok := eventHandlerRegistry[domain.EventType(evt.EventType)]
	if !ok {
		slog.Debug("unhandled bus event in TUI", "type", evt.EventType)
		return nil
	}
	return decoder(evt.Payload)
}

// --- Event decoders ---

func decodeWorkItemIngested(payload string) tea.Msg {
	var p struct {
		WorkspaceID string         `json:"workspace_id"`
		Session     domain.Session `json:"session"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		slog.Warn("failed to decode EventWorkItemIngested payload", "error", err)
		return nil
	}
	return WorkItemIngestedMsg{WorkspaceID: p.WorkspaceID, Session: p.Session}
}

func decodeWorkItemState(payload string) tea.Msg {
	var p struct {
		WorkItemID  string         `json:"work_item_id"`
		WorkspaceID string         `json:"workspace_id"`
		Session     domain.Session `json:"session"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		slog.Warn("failed to decode work item state payload", "error", err)
		return nil
	}
	return WorkItemUpdatedMsg{Session: p.Session}
}

func decodePlanGenerated(payload string) tea.Msg {
	var p struct {
		WorkItemID string            `json:"work_item_id"`
		Plan       *domain.Plan      `json:"plan"`
		SubPlans   []domain.TaskPlan `json:"sub_plans"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		slog.Warn("failed to decode EventPlanGenerated payload", "error", err)
		return nil
	}
	return PlanGeneratedMsg{WorkItemID: p.WorkItemID, Plan: p.Plan, SubPlans: p.SubPlans}
}

func decodePlanUpdated(payload string) tea.Msg {
	var p struct {
		WorkItemID string       `json:"work_item_id"`
		Plan       *domain.Plan `json:"plan"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		slog.Warn("failed to decode plan event payload", "error", err)
		return nil
	}
	return PlanUpdatedMsg{WorkItemID: p.WorkItemID, Plan: p.Plan}
}

func decodeAgentSessionStarted(payload string) tea.Msg {
	var p struct {
		Session    domain.Task `json:"session"`
		SessionID  string      `json:"session_id"`
		WorkItemID string      `json:"work_item_id"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		slog.Warn("failed to decode EventAgentSessionStarted payload", "error", err)
		return nil
	}
	return TaskStartedMsg{
		WorkItemID: p.WorkItemID,
		Task:       p.Session,
	}
}

func decodeAgentSessionUpdated(payload string) tea.Msg {
	var p struct {
		Session    domain.Task `json:"session"`
		SessionID  string      `json:"session_id"`
		WorkItemID string      `json:"work_item_id"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		slog.Warn("failed to decode agent task event payload", "error", err)
		return nil
	}
	return TaskUpdatedMsg{
		WorkItemID: p.WorkItemID,
		Task:       p.Session,
	}
}

// decodeAgentSessionResumed handles EventAgentSessionResumed by extracting the work_item_id
// and triggering a targeted reload of the affected tasks. The full task data is loaded
// by the TUI command that reads from the repository.
func decodeAgentSessionResumed(payload string) tea.Msg {
	var p struct {
		WorkItemID string `json:"work_item_id"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		slog.Warn("failed to decode EventAgentSessionResumed payload", "error", err)
		return nil
	}
	return SessionResumedMsg{WorkItemID: p.WorkItemID, Message: ""}
}

func decodeQuestionRaised(payload string) tea.Msg {
	var p struct {
		SessionID string          `json:"session_id"`
		Question  domain.Question `json:"question"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		slog.Warn("failed to decode EventAgentQuestionRaised payload", "error", err)
		return nil
	}
	return QuestionRaisedMsg{SessionID: p.SessionID, Question: p.Question}
}

func decodeQuestionAnswered(payload string) tea.Msg {
	var p struct {
		SessionID  string `json:"session_id"`
		QuestionID string `json:"question_id"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		slog.Warn("failed to decode EventAgentQuestionAnswered payload", "error", err)
		return nil
	}
	return QuestionAnsweredMsg{SessionID: p.SessionID, QuestionID: p.QuestionID}
}

func decodeReviewStarted(payload string) tea.Msg {
	var p struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		slog.Warn("failed to decode EventReviewStarted payload", "error", err)
		return nil
	}
	return ReviewStartedMsg{SessionID: p.SessionID}
}

func decodeReviewCompleted(payload string) tea.Msg {
	var p struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		slog.Warn("failed to decode EventReviewCompleted payload", "error", err)
		return nil
	}
	return ReviewCompletedMsg{SessionID: p.SessionID}
}

func decodeCritiquesFound(payload string) tea.Msg {
	var p struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		slog.Warn("failed to decode EventCritiquesFound payload", "error", err)
		return nil
	}
	return CritiquesFoundMsg{SessionID: p.SessionID}
}

func decodeReimplementationStarted(payload string) tea.Msg {
	var p struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		slog.Warn("failed to decode EventReimplementationStarted payload", "error", err)
		return nil
	}
	return ReimplementationStartedMsg{SessionID: p.SessionID}
}

func decodeAdapterError(payload string) tea.Msg {
	var p struct {
		Adapter   string `json:"adapter"`
		EventType string `json:"event_type"`
		Err       string `json:"error"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		slog.Warn("failed to decode EventAdapterError payload", "error", err)
		return nil
	}
	return AdapterErrorMsg{Adapter: p.Adapter, EventType: p.EventType, Err: errors.New(p.Err)}
}

func decodePRMerged(payload string) tea.Msg {
	var p struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		slog.Warn("failed to decode EventPRMerged payload", "error", err)
		return nil
	}
	return PRMergedMsg{SessionID: p.SessionID}
}
