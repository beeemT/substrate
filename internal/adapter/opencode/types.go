package opencode

// OpenCode HTTP API request/response types.
// These mirror the opencode serve REST API.

import (
	"encoding/json"
)


// CreateSessionRequest is the body for POST /session.
type CreateSessionRequest struct {
	// Agent type: "build" (default) or "plan".
	Agent string `json:"agent,omitempty"`
}

// CreateSessionResponse is the response from POST /session.
type CreateSessionResponse struct {
	ID string `json:"id"`
}

// SendMessageRequest is the body for POST /session/:id/message.
type SendMessageRequest struct {
	Content string `json:"content"`
}

// SummarizeRequest is the body for POST /session/:id/summarize.
type SummarizeRequest struct{}

// ConnectMCPRequest is the body for POST /mcp.
type ConnectMCPRequest struct {
	Transport string `json:"transport"`
	Command   string `json:"command,omitempty"`
	URL       string `json:"url,omitempty"`
	Name      string `json:"name"`
}

// QuestionReplyRequest is the body for POST /question/:requestID/reply.
type QuestionReplyRequest struct {
	Content string `json:"content"`
}

// QuestionListResponse is the response from GET /session/:id/todo (questions).
type QuestionListResponse struct {
	Questions []QuestionEntry `json:"questions,omitempty"`
}

// QuestionEntry represents a single pending question.
type QuestionEntry struct {
	RequestID string `json:"requestID,omitempty"`
	Question  string `json:"question,omitempty"`
}

// SessionEvent represents a typed event from the OpenCode SSE stream.
// The SSE data payload is JSON-encoded; this struct captures the
// common fields across all event types.
type SessionEvent struct {
	// Type is the event discriminator, e.g. "session.created", "message.updated".
	Type string `json:"type"`

	// SessionID is present on session-level events.
	SessionID string `json:"sessionID,omitempty"`

	// Message is present on message-level events.
	Message *MessageEvent `json:"message,omitempty"`

	// Error message (present on session.error).
	Error string `json:"error,omitempty"`

	// Question fields (present on question.asked, question.replied).
	Question *QuestionEvent `json:"question,omitempty"`
}

// MessageEvent represents the message payload in a message.updated event.
type MessageEvent struct {
	ID    string `json:"id,omitempty"`
	Parts []Part `json:"parts,omitempty"`
}

// Part represents a single part within a message event.
type Part struct {
	Type  string `json:"type"`
	State string `json:"state,omitempty"`
	Text  string `json:"text,omitempty"`

	// Tool fields (present when Type is "tool-use").
	ToolUseID  string          `json:"toolUseID,omitempty"`
	ToolName   string          `json:"toolName,omitempty"`
	ToolInput  json.RawMessage `json:"input,omitempty"`

	// Tool result fields (present when Type is "tool-result").
	ToolResultID  string `json:"toolResultID,omitempty"`
	ToolResultErr string `json:"error,omitempty"`
	ToolOutput    string `json:"output,omitempty"`
}

// QuestionEvent represents the question payload in question.asked events.
type QuestionEvent struct {
	RequestID string `json:"requestID,omitempty"`
	Question  string `json:"question,omitempty"`
}
