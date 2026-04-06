package opencode

import (
	"encoding/json"
	"testing"
)

func TestMapSSEEvent_SessionCreated(t *testing.T) {
	raw := json.RawMessage(`{"type":"session.created","sessionID":"abc-123"}`)
	events := mapSSEEvent(raw, map[string]string{})

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	evt := events[0]
	if evt.Type != "started" {
		t.Errorf("Type = %q, want %q", evt.Type, "started")
	}
	if evt.Metadata == nil {
		t.Fatal("Metadata is nil")
	}
	if got := evt.Metadata["opencode_session_id"]; got != "abc-123" {
		t.Errorf("Metadata[opencode_session_id] = %v, want %q", got, "abc-123")
	}
}

func TestMapSSEEvent_TextDelta(t *testing.T) {
	raw := json.RawMessage(`{
		"type":"message.updated",
		"message":{
			"id":"msg-1",
			"parts":[{"type":"text","text":"hello world"}]
		}
	}`)
	events := mapSSEEvent(raw, map[string]string{})

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	evt := events[0]
	if evt.Type != "text_delta" {
		t.Errorf("Type = %q, want %q", evt.Type, "text_delta")
	}
	if evt.Payload != "hello world" {
		t.Errorf("Payload = %q, want %q", evt.Payload, "hello world")
	}
}

func TestMapSSEEvent_EmptyTextPartSkipped(t *testing.T) {
	raw := json.RawMessage(`{
		"type":"message.updated",
		"message":{
			"id":"msg-1",
			"parts":[{"type":"text","text":""}]
		}
	}`)
	events := mapSSEEvent(raw, map[string]string{})
	if len(events) != 0 {
		t.Errorf("expected 0 events for empty text part, got %d", len(events))
	}
}

func TestMapSSEEvent_ToolStart(t *testing.T) {
	raw := json.RawMessage(`{
		"type":"message.updated",
		"message":{
			"id":"msg-2",
			"parts":[{"type":"tool-use","state":"started","toolUseID":"tu-1","toolName":"Read","input":"{\"path\":\"/tmp/file\"}"}]
		}
	}`)
	events := mapSSEEvent(raw, map[string]string{})

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	evt := events[0]
	if evt.Type != "tool_start" {
		t.Errorf("Type = %q, want %q", evt.Type, "tool_start")
	}
	if evt.Payload != "Read" {
		t.Errorf("Payload = %q, want %q", evt.Payload, "Read")
	}
	if evt.Metadata == nil {
		t.Fatal("Metadata is nil")
	}
	if got := evt.Metadata["tool_use_id"]; got != "tu-1" {
		t.Errorf("Metadata[tool_use_id] = %v, want %q", got, "tu-1")
	}
	// Metadata["input"] stores the raw JSON bytes (including quotes) from json.RawMessage.
	wantInput := `"{\"path\":\"/tmp/file\"}"`
	if got, ok := evt.Metadata["input"].(string); !ok {
		t.Fatalf("Metadata[input] not a string: %T", evt.Metadata["input"])
	} else if got != wantInput {
		t.Errorf("Metadata[input] = %q, want %q", got, wantInput)
	}
}

func TestMapSSEEvent_ToolUseNotStartedIgnored(t *testing.T) {
	raw := json.RawMessage(`{
		"type":"message.updated",
		"message":{
			"id":"msg-2",
			"parts":[{"type":"tool-use","state":"completed","toolUseID":"tu-1","toolName":"Read"}]
		}
	}`)
	events := mapSSEEvent(raw, map[string]string{})
	if len(events) != 0 {
		t.Errorf("expected 0 events for non-started tool-use, got %d", len(events))
	}
}

func TestMapSSEEvent_ToolResult(t *testing.T) {
	raw := json.RawMessage(`{
		"type":"message.updated",
		"message":{
			"id":"msg-3",
			"parts":[{"type":"tool-result","toolResultID":"tr-1","output":"file contents here"}]
		}
	}`)
	events := mapSSEEvent(raw, map[string]string{})

	// tool-result should emit tool_output + tool_result (2 events).
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	// First event: tool_output.
	if events[0].Type != "tool_output" {
		t.Errorf("events[0].Type = %q, want %q", events[0].Type, "tool_output")
	}
	if events[0].Payload != "file contents here" {
		t.Errorf("events[0].Payload = %q, want %q", events[0].Payload, "file contents here")
	}
	if events[0].Metadata == nil {
		t.Fatal("events[0].Metadata is nil")
	}
	if got := events[0].Metadata["tool_result_id"]; got != "tr-1" {
		t.Errorf("events[0].Metadata[tool_result_id] = %v, want %q", got, "tr-1")
	}

	// Second event: tool_result (always emitted).
	if events[1].Type != "tool_result" {
		t.Errorf("events[1].Type = %q, want %q", events[1].Type, "tool_result")
	}
	if events[1].Metadata == nil {
		t.Fatal("events[1].Metadata is nil")
	}
	if got := events[1].Metadata["tool_result_id"]; got != "tr-1" {
		t.Errorf("events[1].Metadata[tool_result_id] = %v, want %q", got, "tr-1")
	}
}

func TestMapSSEEvent_ToolResultEmptyOutput(t *testing.T) {
	raw := json.RawMessage(`{
		"type":"message.updated",
		"message":{
			"id":"msg-3",
			"parts":[{"type":"tool-result","toolResultID":"tr-2"}]
		}
	}`)
	events := mapSSEEvent(raw, map[string]string{})

	// Empty output: no tool_output, but tool_result is always emitted.
	if len(events) != 1 {
		t.Fatalf("expected 1 event (tool_result only), got %d", len(events))
	}
	if events[0].Type != "tool_result" {
		t.Errorf("Type = %q, want %q", events[0].Type, "tool_result")
	}
}

func TestMapSSEEvent_ToolResultWithError(t *testing.T) {
	raw := json.RawMessage(`{
		"type":"message.updated",
		"message":{
			"id":"msg-3",
			"parts":[{"type":"tool-result","toolResultID":"tr-3","error":"command failed","output":""}]
		}
	}`)
	events := mapSSEEvent(raw, map[string]string{})

	// Error set but no output: no tool_output, but tool_result emitted with error in metadata.
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "tool_result" {
		t.Errorf("Type = %q, want %q", events[0].Type, "tool_result")
	}
	if got := events[0].Metadata["error"]; got != "command failed" {
		t.Errorf("Metadata[error] = %v, want %q", got, "command failed")
	}
}

func TestMapSSEEvent_Thinking(t *testing.T) {
	raw := json.RawMessage(`{
		"type":"message.updated",
		"message":{
			"id":"msg-4",
			"parts":[{"type":"thinking","text":"let me think..."}]
		}
	}`)
	events := mapSSEEvent(raw, map[string]string{})

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	evt := events[0]
	if evt.Type != "thinking_output" {
		t.Errorf("Type = %q, want %q", evt.Type, "thinking_output")
	}
	if evt.Payload != "let me think..." {
		t.Errorf("Payload = %q, want %q", evt.Payload, "let me think...")
	}
}

func TestMapSSEEvent_EmptyThinkingSkipped(t *testing.T) {
	raw := json.RawMessage(`{
		"type":"message.updated",
		"message":{
			"id":"msg-4",
			"parts":[{"type":"thinking","text":""}]
		}
	}`)
	events := mapSSEEvent(raw, map[string]string{})
	if len(events) != 0 {
		t.Errorf("expected 0 events for empty thinking part, got %d", len(events))
	}
}

func TestMapSSEEvent_SessionCompleted(t *testing.T) {
	raw := json.RawMessage(`{"type":"session.completed","sessionID":"abc"}`)
	events := mapSSEEvent(raw, map[string]string{})

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	evt := events[0]
	if evt.Type != "done" {
		t.Errorf("Type = %q, want %q", evt.Type, "done")
	}
	if got := evt.Metadata["session_id"]; got != "abc" {
		t.Errorf("Metadata[session_id] = %v, want %q", got, "abc")
	}
}

func TestMapSSEEvent_SessionAborted(t *testing.T) {
	raw := json.RawMessage(`{"type":"session.aborted","sessionID":"abc"}`)
	events := mapSSEEvent(raw, map[string]string{})

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	evt := events[0]
	if evt.Type != "done" {
		t.Errorf("Type = %q, want %q", evt.Type, "done")
	}
	aborted, ok := evt.Metadata["aborted"].(bool)
	if !ok || !aborted {
		t.Errorf("Metadata[aborted] = %v, want true", evt.Metadata["aborted"])
	}
}

func TestMapSSEEvent_SessionError(t *testing.T) {
	raw := json.RawMessage(`{"type":"session.error","sessionID":"abc","error":"something went wrong"}`)
	events := mapSSEEvent(raw, map[string]string{})

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	evt := events[0]
	if evt.Type != "error" {
		t.Errorf("Type = %q, want %q", evt.Type, "error")
	}
	if evt.Payload != "something went wrong" {
		t.Errorf("Payload = %q, want %q", evt.Payload, "something went wrong")
	}
}

func TestMapSSEEvent_SessionCompacted(t *testing.T) {
	raw := json.RawMessage(`{"type":"session.compacted"}`)
	events := mapSSEEvent(raw, map[string]string{})

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	evt := events[0]
	if evt.Type != "lifecycle" {
		t.Errorf("Type = %q, want %q", evt.Type, "lifecycle")
	}
	if got := evt.Metadata["stage"]; got != "compaction_end" {
		t.Errorf("Metadata[stage] = %v, want %q", got, "compaction_end")
	}
}

func TestMapSSEEvent_QuestionAsked(t *testing.T) {
	raw := json.RawMessage(`{
		"type":"question.asked",
		"question":{
			"requestID":"req-42",
			"question":"Which approach should I use?"
		}
	}`)
	events := mapSSEEvent(raw, map[string]string{})

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	evt := events[0]
	if evt.Type != "question" {
		t.Errorf("Type = %q, want %q", evt.Type, "question")
	}
	if evt.Payload != "Which approach should I use?" {
		t.Errorf("Payload = %q, want %q", evt.Payload, "Which approach should I use?")
	}
	if evt.Metadata == nil {
		t.Fatal("Metadata is nil")
	}
	if got := evt.Metadata["request_id"]; got != "req-42" {
		t.Errorf("Metadata[request_id] = %v, want %q", got, "req-42")
	}
}

func TestMapSSEEvent_UnknownType(t *testing.T) {
	raw := json.RawMessage(`{"type":"session.unknown_event"}`)
	events := mapSSEEvent(raw, map[string]string{})
	if events != nil {
		t.Errorf("expected nil for unknown event type, got %v", events)
	}
}

func TestMapSSEEvent_InvalidJSON(t *testing.T) {
	raw := json.RawMessage(`not valid json{{{`)
	events := mapSSEEvent(raw, map[string]string{})
	if events != nil {
		t.Errorf("expected nil for invalid JSON, got %v", events)
	}
}

func TestMapSSEEvent_NilMessage(t *testing.T) {
	// message.updated without a message field.
	raw := json.RawMessage(`{"type":"message.updated"}`)
	events := mapSSEEvent(raw, map[string]string{})
	if events != nil {
		t.Errorf("expected nil for message.updated with nil message, got %v", events)
	}
}

func TestMapSSEEvent_NilQuestion(t *testing.T) {
	// question.asked without a question field.
	raw := json.RawMessage(`{"type":"question.asked"}`)
	events := mapSSEEvent(raw, map[string]string{})
	if events != nil {
		t.Errorf("expected nil for question.asked with nil question, got %v", events)
	}
}

func TestMapSSEEvent_MultipleParts(t *testing.T) {
	raw := json.RawMessage(`{
		"type":"message.updated",
		"message":{
			"id":"msg-multi",
			"parts":[
				{"type":"text","text":"first"},
				{"type":"tool-use","state":"started","toolUseID":"tu-1","toolName":"Bash","input":"{}"},
				{"type":"text","text":"second"}
			]
		}
	}`)
	events := mapSSEEvent(raw, map[string]string{})

	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0].Type != "text_delta" || events[0].Payload != "first" {
		t.Errorf("events[0] = %+v, want text_delta with 'first'", events[0])
	}
	if events[1].Type != "tool_start" || events[1].Payload != "Bash" {
		t.Errorf("events[1] = %+v, want tool_start with 'Bash'", events[1])
	}
	if events[2].Type != "text_delta" || events[2].Payload != "second" {
		t.Errorf("events[2] = %+v, want text_delta with 'second'", events[2])
	}
}

func TestMapToolName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Read", "Read"},
		{"Write", "Write"},
		{"Edit", "Edit"},
		{"Bash", "Bash"},
		{"Glob", "Glob"},
		{"Grep", "Grep"},
		{"search", "Grep"},
		{"CustomTool", "CustomTool"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := mapToolName(tt.input)
			if got != tt.want {
				t.Errorf("mapToolName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
