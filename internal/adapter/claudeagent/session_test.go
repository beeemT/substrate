package claudeagent

import (
	"encoding/json"
	"testing"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/adapter/bridge"
)

func TestMapBridgeEvent(t *testing.T) {
	tests := []struct {
		name         string
		rawType      string
		eventJSON    string
		wantType     string
		wantNil      bool
		wantErr      bool
		checkMeta    func(t *testing.T, meta map[string]any)
		checkPayload string
	}{
		{
			name:      "input with input_kind prompt",
			rawType:   "event",
			eventJSON: `{"type":"input","input_kind":"prompt","text":"text"}`,
			wantType:  "input",
			checkMeta: func(t *testing.T, meta map[string]any) {
				t.Helper()
				if k, _ := meta["input_kind"].(string); k != "prompt" {
					t.Errorf("input_kind = %v, want prompt", meta["input_kind"])
				}
			},
			checkPayload: "text",
		},
		{
			name:         "assistant_output",
			rawType:      "event",
			eventJSON:    `{"type":"assistant_output","text":"hello"}`,
			wantType:     "text_delta",
			checkPayload: "hello",
		},
		{
			name:      "thinking_output",
			rawType:   "event",
			eventJSON: `{"type":"thinking_output","text":"hmm"}`,
			wantType:  "text_delta",
			checkMeta: func(t *testing.T, meta map[string]any) {
				t.Helper()
				if v, _ := meta["thinking"].(bool); !v {
					t.Errorf("thinking metadata = %v, want true", meta["thinking"])
				}
			},
		},
		{
			name:      "tool_start",
			rawType:   "event",
			eventJSON: `{"type":"tool_start","tool":"Bash","text":"cmd","intent":"run it"}`,
			wantType:  "tool_start",
			checkMeta: func(t *testing.T, meta map[string]any) {
				t.Helper()
				if tool, _ := meta["tool"].(string); tool != "Bash" {
					t.Errorf("tool = %v, want Bash", meta["tool"])
				}
				if intent, _ := meta["intent"].(string); intent != "run it" {
					t.Errorf("intent = %v, want 'run it'", meta["intent"])
				}
			},
		},
		{
			name:      "tool_result with is_error true",
			rawType:   "event",
			eventJSON: `{"type":"tool_result","tool":"Bash","text":"err","is_error":true}`,
			wantType:  "tool_result",
			checkMeta: func(t *testing.T, meta map[string]any) {
				t.Helper()
				if isErr, _ := meta["is_error"].(bool); !isErr {
					t.Errorf("is_error = %v, want true", meta["is_error"])
				}
			},
		},
		{
			name:         "tool_output",
			rawType:      "event",
			eventJSON:    `{"type":"tool_output","tool":"Bash","text":"output here"}`,
			wantType:     "tool_output",
			checkPayload: "output here",
			checkMeta: func(t *testing.T, meta map[string]any) {
				t.Helper()
				if tool, _ := meta["tool"].(string); tool != "Bash" {
					t.Errorf("tool = %v, want Bash", meta["tool"])
				}
			},
		},
		{
			name:         "question",
			rawType:      "event",
			eventJSON:    `{"type":"question","question":"what?","context":"ctx"}`,
			wantType:     "question",
			checkPayload: "what?",
			checkMeta: func(t *testing.T, meta map[string]any) {
				t.Helper()
				if ctx, _ := meta["context"].(string); ctx != "ctx" {
					t.Errorf("context = %v, want ctx", meta["context"])
				}
			},
		},
		{
			name:         "foreman_proposed with uncertain",
			rawType:      "event",
			eventJSON:    `{"type":"foreman_proposed","text":"answer","uncertain":true}`,
			wantType:     "foreman_proposed",
			checkPayload: "answer",
			checkMeta: func(t *testing.T, meta map[string]any) {
				t.Helper()
				if u, _ := meta["uncertain"].(bool); !u {
					t.Errorf("uncertain = %v, want true", meta["uncertain"])
				}
			},
		},
		{
			name:      "lifecycle started",
			rawType:   "event",
			eventJSON: `{"type":"lifecycle","stage":"started","message":"go"}`,
			wantType:  "started",
		},
		{
			name:      "lifecycle completed",
			rawType:   "event",
			eventJSON: `{"type":"lifecycle","stage":"completed","summary":"done"}`,
			wantType:  "done",
		},
		{
			name:         "lifecycle failed with message",
			rawType:      "event",
			eventJSON:    `{"type":"lifecycle","stage":"failed","message":"oops"}`,
			wantType:     "error",
			checkPayload: "oops",
		},
		{
			name:      "lifecycle retry_wait",
			rawType:   "event",
			eventJSON: `{"type":"lifecycle","stage":"retry_wait","message":"waiting"}`,
			wantType:  "retry_wait",
		},
		{
			name:      "lifecycle retry_resumed",
			rawType:   "event",
			eventJSON: `{"type":"lifecycle","stage":"retry_resumed"}`,
			wantType:  "retry_resumed",
		},
		{
			name:      "unknown event type returns nil",
			rawType:   "event",
			eventJSON: `{"type":"bogus"}`,
			wantNil:   true,
		},
		{
			name:      "non-event wrapper returns nil",
			rawType:   "response",
			eventJSON: `{"type":"something"}`,
			wantNil:   true,
		},
		{
			name:      "assistant_output missing text returns error",
			rawType:   "event",
			eventJSON: `{"type":"assistant_output"}`,
			wantErr:   true,
		},
		{
			name:      "question missing question field returns error",
			rawType:   "event",
			eventJSON: `{"type":"question"}`,
			wantErr:   true,
		},
		{
			name:      "foreman_proposed missing text returns error",
			rawType:   "event",
			eventJSON: `{"type":"foreman_proposed"}`,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := struct {
				Type  string          `json:"type"`
				Event json.RawMessage `json:"event"`
			}{
				Type:  tt.rawType,
				Event: json.RawMessage(tt.eventJSON),
			}

			got, err := bridge.MapBridgeEvent(raw)
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
				t.Errorf("Type = %q, want %q", got.Type, tt.wantType)
			}
			if tt.checkPayload != "" && got.Payload != tt.checkPayload {
				t.Errorf("Payload = %q, want %q", got.Payload, tt.checkPayload)
			}
			if tt.checkMeta != nil {
				tt.checkMeta(t, got.Metadata)
			}
		})
	}
}

func TestSessionMetaCapture(t *testing.T) {
	bs := bridge.NewBridgeSession("test", adapter.SessionModeAgent)
	s := &claudeAgentSession{bs: bs}
	bs.ParseSessionMeta = s.sessionMetaCallback

	bs.ParseSessionMeta([]byte(`{"type":"session_meta","session_id":"abc-123"}`))

	if got := s.ClaudeSessionID(); got != "abc-123" {
		t.Errorf("ClaudeSessionID() = %q, want %q", got, "abc-123")
	}
}

func TestFirstNonEmpty(t *testing.T) {
	tests := []struct {
		values []string
		want   string
	}{
		{[]string{"a", "b", "c"}, "a"},
		{[]string{"", "b", "c"}, "b"},
		{[]string{"", "", ""}, ""},
		{[]string{}, ""},
		{[]string{"", "", "z"}, "z"},
	}
	for _, tt := range tests {
		got := bridge.FirstNonEmpty(tt.values...)
		if got != tt.want {
			t.Errorf("FirstNonEmpty(%v) = %q, want %q", tt.values, got, tt.want)
		}
	}
}
