package omp

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/config"
)

func TestBridgeMsgJSONRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		msg  bridgeMsg
	}{
		{
			name: "prompt message",
			msg:  bridgeMsg{Type: "prompt", Text: "Hello, agent!"},
		},
		{
			name: "message without text",
			msg:  bridgeMsg{Type: "abort"},
		},
		{
			name: "answer message",
			msg:  bridgeMsg{Type: "answer", Text: "This is the answer to your question."},
		},
		{
			name: "message type",
			msg:  bridgeMsg{Type: "message", Text: "Follow-up message"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Marshal to JSON
			data, err := json.Marshal(tt.msg)
			if err != nil {
				t.Fatalf("failed to marshal: %v", err)
			}

			// Unmarshal back
			var got bridgeMsg
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}

			// Verify fields match
			if got.Type != tt.msg.Type {
				t.Errorf("Type mismatch: got %q, want %q", got.Type, tt.msg.Type)
			}
			if got.Text != tt.msg.Text {
				t.Errorf("Text mismatch: got %q, want %q", got.Text, tt.msg.Text)
			}
		})
	}
}

func TestMapBridgeEvent(t *testing.T) {
	tests := []struct {
		name      string
		rawType   string
		eventJSON string
		wantType  string
		wantNil   bool
		wantErr   bool
	}{
		{
			name:      "progress event",
			rawType:   "event",
			eventJSON: `{"type":"progress","text":"Reading file..."}`,
			wantType:  "text_delta",
		},
		{
			name:      "question event",
			rawType:   "event",
			eventJSON: `{"type":"question","question":"What should I do?","context":"file xyz"}`,
			wantType:  "question",
		},
		{
			name:      "foreman_proposed event with uncertain",
			rawType:   "event",
			eventJSON: `{"type":"foreman_proposed","text":"The answer is 42","uncertain":true}`,
			wantType:  "foreman_proposed",
		},
		{
			name:      "foreman_proposed event confident",
			rawType:   "event",
			eventJSON: `{"type":"foreman_proposed","text":"The answer is 42","uncertain":false}`,
			wantType:  "foreman_proposed",
		},
		{
			name:      "complete event",
			rawType:   "event",
			eventJSON: `{"type":"complete","summary":"Task completed"}`,
			wantType:  "done",
		},
		{
			name:      "error event",
			rawType:   "event",
			eventJSON: `{"type":"error","message":"Something went wrong"}`,
			wantType:  "error",
		},
		{
			name:      "session_ready event",
			rawType:   "event",
			eventJSON: `{"type":"session_ready"}`,
			wantType:  "started",
		},
		{
			name:      "non-event type returns nil",
			rawType:   "response",
			eventJSON: `{"type":"something"}`,
			wantNil:   true,
		},
		{
			name:      "unknown event type returns nil",
			rawType:   "event",
			eventJSON: `{"type":"unknown_type"}`,
			wantNil:   true,
		},
		{
			name:      "progress missing text returns error",
			rawType:   "event",
			eventJSON: `{"type":"progress"}`,
			wantErr:   true,
		},
		{
			name:      "question missing question field returns error",
			rawType:   "event",
			eventJSON: `{"type":"question"}`,
			wantErr:   true,
		},
	}

	s := &ohMyPiSession{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := struct {
				Type  string          `json:"type"`
				Event json.RawMessage `json:"event"`
			}{
				Type:  tt.rawType,
				Event: json.RawMessage(tt.eventJSON),
			}

			got, err := s.mapBridgeEvent(raw)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantNil {
				if got != nil {
					t.Errorf("expected nil, got %+v", got)
				}
				return
			}

			if got == nil {
				t.Fatal("expected non-nil event, got nil")
			}

			if got.Type != tt.wantType {
				t.Errorf("Type mismatch: got %q, want %q", got.Type, tt.wantType)
			}

			// Verify timestamp is set and recent
			if time.Since(got.Timestamp) > time.Minute {
				t.Errorf("Timestamp seems incorrect: %v", got.Timestamp)
			}
		})
	}
}

func TestMapBridgeEventMetadata(t *testing.T) {
	s := &ohMyPiSession{}

	// Test question event has context in metadata
	raw := struct {
		Type  string          `json:"type"`
		Event json.RawMessage `json:"event"`
	}{
		Type:  "event",
		Event: json.RawMessage(`{"type":"question","question":"Q?","context":"some context"}`),
	}

	got, err := s.mapBridgeEvent(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil event")
	}

	ctx, ok := got.Metadata["context"].(string)
	if !ok {
		t.Fatal("expected context in metadata")
	}
	if ctx != "some context" {
		t.Errorf("context mismatch: got %q, want %q", ctx, "some context")
	}

	// Test foreman_proposed event has uncertain in metadata
	raw = struct {
		Type  string          `json:"type"`
		Event json.RawMessage `json:"event"`
	}{
		Type:  "event",
		Event: json.RawMessage(`{"type":"foreman_proposed","text":"Answer","uncertain":true}`),
	}

	got, err = s.mapBridgeEvent(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil event")
	}

	uncertain, ok := got.Metadata["uncertain"].(bool)
	if !ok {
		t.Fatal("expected uncertain in metadata")
	}
	if uncertain != true {
		t.Errorf("uncertain mismatch: got %v, want true", uncertain)
	}
}

func TestNewHarness(t *testing.T) {
	cfg := config.OhMyPiConfig{
		BunPath:       "/usr/local/bin/bun",
		BridgePath:    "/path/to/bridge.ts",
		ThinkingLevel: "high",
	}
	workspaceRoot := "/workspace"

	h := NewHarness(cfg, workspaceRoot)

	if h == nil {
		t.Fatal("expected non-nil harness")
	}

	if h.Name() != "omp" {
		t.Errorf("Name mismatch: got %q, want %q", h.Name(), "omp")
	}

	caps := h.Capabilities()
	if !caps.SupportsStreaming {
		t.Error("expected SupportsStreaming to be true")
	}
	if !caps.SupportsMessaging {
		t.Error("expected SupportsMessaging to be true")
	}

	expectedTools := []string{"read", "grep", "find", "edit", "write", "bash", "ask_foreman"}
	if len(caps.SupportedTools) != len(expectedTools) {
		t.Errorf("SupportedTools count mismatch: got %d, want %d", len(caps.SupportedTools), len(expectedTools))
	}
}
