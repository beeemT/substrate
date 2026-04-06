package opencode

import (
	"encoding/json"
	"log/slog"
	"strconv"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
)

// toolNameMap maps OpenCode tool names to substrate short names.
var toolNameMap = map[string]string{
	"Read":   "Read",
	"Write":  "Write",
	"Edit":   "Edit",
	"Bash":   "Bash",
	"Glob":   "Glob",
	"Grep":   "Grep",
	"search": "Grep",
}

// mapToolName converts an OpenCode tool name to the substrate short name.
// Unrecognized names pass through unchanged.
func mapToolName(name string) string {
	if short, ok := toolNameMap[name]; ok {
		return short
	}
	return name
}

// mapSSEEvent parses a raw SSE data payload and returns zero or more
// canonical adapter.AgentEvents. Unknown event types are silently dropped
// (logged at Debug level).
func mapSSEEvent(raw json.RawMessage, lastPartText map[string]string) []adapter.AgentEvent {
	var evt SessionEvent
	if err := json.Unmarshal(raw, &evt); err != nil {
		slog.Debug("opencode: unparseable SSE event", "raw", string(raw), "err", err)
		return nil
	}

	now := time.Now()

	switch evt.Type {
	case "session.created":
		return []adapter.AgentEvent{{
			Type:      "started",
			Timestamp: now,
			Metadata:  map[string]any{"opencode_session_id": evt.SessionID},
		}}

	case "message.updated":
		return mapMessageUpdated(evt, now, lastPartText)

	case "session.completed":
		return []adapter.AgentEvent{{
			Type:      "done",
			Timestamp: now,
			Metadata:  map[string]any{"session_id": evt.SessionID},
		}}

	case "session.aborted":
		return []adapter.AgentEvent{{
			Type:      "done",
			Timestamp: now,
			Metadata:  map[string]any{"session_id": evt.SessionID, "aborted": true},
		}}

	case "session.error":
		return []adapter.AgentEvent{{
			Type:      "error",
			Timestamp: now,
			Payload:   evt.Error,
			Metadata:  map[string]any{"session_id": evt.SessionID},
		}}

	case "session.compacted":
		return []adapter.AgentEvent{{
			Type:      "lifecycle",
			Timestamp: now,
			Metadata:  map[string]any{"stage": "compaction_end"},
		}}

	case "question.asked":
		return mapQuestionAsked(evt, now)

	default:
		slog.Debug("opencode: unknown SSE event type", "type", evt.Type)
		return nil
	}
}

// mapMessageUpdated translates a message.updated event into AgentEvents.
// A single message.updated can contain multiple parts, each producing
// its own event.
func mapMessageUpdated(evt SessionEvent, now time.Time, lastPartText map[string]string) []adapter.AgentEvent {
	if evt.Message == nil {
		return nil
	}

	var events []adapter.AgentEvent
	for i, part := range evt.Message.Parts {
		key := evt.Message.ID + ":" + strconv.Itoa(i)
		switch part.Type {
		case "text":
			prev := lastPartText[key]
			if len(part.Text) <= len(prev) {
				continue
			}
			delta := part.Text[len(prev):]
			lastPartText[key] = part.Text
			events = append(events, adapter.AgentEvent{
				Type:      "text_delta",
				Timestamp: now,
				Payload:   delta,
			})

		case "tool-use":
			if part.State == "started" {
				events = append(events, adapter.AgentEvent{
					Type:      "tool_start",
					Timestamp: now,
					Payload:   mapToolName(part.ToolName),
					Metadata: map[string]any{
						"tool_use_id": part.ToolUseID,
						"input":       string(part.ToolInput),
					},
				})
			}

		case "tool-result":
			metadata := map[string]any{
				"tool_result_id": part.ToolResultID,
			}
			if part.ToolResultErr != "" {
				metadata["error"] = part.ToolResultErr
			}

			// Emit tool_output with the raw output text.
			if part.ToolOutput != "" {
				events = append(events, adapter.AgentEvent{
					Type:      "tool_output",
					Timestamp: now,
					Payload:   part.ToolOutput,
					Metadata:  metadata,
				})
			}

			// Always emit tool_result as the completion marker.
			events = append(events, adapter.AgentEvent{
				Type:      "tool_result",
				Timestamp: now,
				Payload:   part.ToolOutput,
				Metadata:  metadata,
			})

		case "thinking":
			prev := lastPartText[key]
			if len(part.Text) <= len(prev) {
				continue
			}
			delta := part.Text[len(prev):]
			lastPartText[key] = part.Text
			events = append(events, adapter.AgentEvent{
				Type:      "thinking_output",
				Timestamp: now,
				Payload:   delta,
			})
		}
	}

	return events
}

// mapQuestionAsked translates a question.asked event into an AgentEvent.
func mapQuestionAsked(evt SessionEvent, now time.Time) []adapter.AgentEvent {
	if evt.Question == nil {
		return nil
	}
	return []adapter.AgentEvent{{
		Type:      "question",
		Timestamp: now,
		Payload:   evt.Question.Question,
		Metadata:  map[string]any{"request_id": evt.Question.RequestID},
	}}
}
