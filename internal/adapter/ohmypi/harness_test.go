package omp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/adapter/bridge"
	"github.com/beeemT/substrate/internal/config"
)

func TestBridgeMsgJSONRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		msg  bridge.BridgeMsg
	}{
		{
			name: "prompt message",
			msg:  bridge.BridgeMsg{Type: "prompt", Text: "Hello, agent!"},
		},
		{
			name: "message without text",
			msg:  bridge.BridgeMsg{Type: "abort"},
		},
		{
			name: "answer message",
			msg:  bridge.BridgeMsg{Type: "answer", Text: "This is the answer to your question."},
		},
		{
			name: "message type",
			msg:  bridge.BridgeMsg{Type: "message", Text: "Follow-up message"},
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
			var got bridge.BridgeMsg
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
			name:      "input event",
			rawType:   "event",
			eventJSON: `{"type":"input","input_kind":"prompt","text":"Begin planning"}`,
			wantType:  "input",
		},
		{
			name:      "assistant output event",
			rawType:   "event",
			eventJSON: `{"type":"assistant_output","text":"Reading file..."}`,
			wantType:  "text_delta",
		},
		{
			name:      "tool start event",
			rawType:   "event",
			eventJSON: `{"type":"tool_start","tool":"read","text":"{\"path\":\"AGENTS.md\"}","intent":"Reading guidance"}`,
			wantType:  "tool_start",
		},
		{
			name:      "tool output event",
			rawType:   "event",
			eventJSON: `{"type":"tool_output","tool":"read","text":"line 1\nline 2"}`,
			wantType:  "tool_output",
		},
		{
			name:      "tool result event",
			rawType:   "event",
			eventJSON: `{"type":"tool_result","tool":"read","text":"done","is_error":false}`,
			wantType:  "tool_result",
		},
		{
			name:      "question event",
			rawType:   "event",
			eventJSON: `{"type":"question","question":"What should I do?","context":"file xyz"}`,
			wantType:  "question",
		},
		{
			name:      "foreman proposed event",
			rawType:   "event",
			eventJSON: `{"type":"foreman_proposed","text":"The answer is 42","uncertain":true}`,
			wantType:  "foreman_proposed",
		},
		{
			name:      "lifecycle complete event",
			rawType:   "event",
			eventJSON: `{"type":"lifecycle","stage":"completed","summary":"Task completed"}`,
			wantType:  "done",
		},
		{
			name:      "lifecycle failure event",
			rawType:   "event",
			eventJSON: `{"type":"lifecycle","stage":"failed","message":"Something went wrong"}`,
			wantType:  "error",
		},
		{
			name:      "lifecycle started event",
			rawType:   "event",
			eventJSON: `{"type":"lifecycle","stage":"started","message":"Session started"}`,
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
			name:      "assistant output missing text returns error",
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
				t.Errorf("Type mismatch: got %q, want %q", got.Type, tt.wantType)
			}
			if time.Since(got.Timestamp) > time.Minute {
				t.Errorf("Timestamp seems incorrect: %v", got.Timestamp)
			}
		})
	}
}

func TestMapBridgeEventMetadata(t *testing.T) {
	// Test question event has context in metadata
	raw := struct {
		Type  string          `json:"type"`
		Event json.RawMessage `json:"event"`
	}{
		Type:  "event",
		Event: json.RawMessage(`{"type":"question","question":"Q?","context":"some context"}`),
	}

	got, err := bridge.MapBridgeEvent(raw)
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

	// Test tool_start event preserves tool metadata and intent.
	raw = struct {
		Type  string          `json:"type"`
		Event json.RawMessage `json:"event"`
	}{
		Type:  "event",
		Event: json.RawMessage(`{"type":"tool_start","tool":"read","text":"{}","intent":"Inspecting file"}`),
	}

	got, err = bridge.MapBridgeEvent(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil event")
	}
	if tool, ok := got.Metadata["tool"].(string); !ok || tool != "read" {
		t.Fatalf("tool metadata = %#v, want read", got.Metadata["tool"])
	}
	if intent, ok := got.Metadata["intent"].(string); !ok || intent != "Inspecting file" {
		t.Fatalf("intent metadata = %#v, want Inspecting file", got.Metadata["intent"])
	}

	// Test foreman_proposed event has uncertain in metadata
	raw = struct {
		Type  string          `json:"type"`
		Event json.RawMessage `json:"event"`
	}{
		Type:  "event",
		Event: json.RawMessage(`{"type":"foreman_proposed","text":"Answer","uncertain":true}`),
	}

	got, err = bridge.MapBridgeEvent(raw)
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
	if !uncertain {
		t.Errorf("uncertain mismatch: got %v, want true", uncertain)
	}
}

func TestResolveBridgeRuntimeFromUsesPackagedBridge(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	execPath := filepath.Join(root, "bin", "substrate")
	bridgePath := filepath.Join(root, "share", "substrate", "bridge", "omp-bridge")

	if err := os.MkdirAll(filepath.Dir(execPath), 0o755); err != nil {
		t.Fatalf("mkdir executable dir: %v", err)
	}
	if err := os.WriteFile(execPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(bridgePath), 0o755); err != nil {
		t.Fatalf("mkdir bridge dir: %v", err)
	}
	if err := os.WriteFile(bridgePath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write bridge: %v", err)
	}

	rt, err := bridge.ResolveBridgeRuntimeFrom("", execPath, "omp-bridge", "ohmypi bridge")
	if err != nil {
		t.Fatalf("resolveBridgeRuntimeFrom() error = %v", err)
	}
	if rt.Path != bridgePath {
		t.Fatalf("Path = %q, want %q", rt.Path, bridgePath)
	}
	if rt.NeedsBun {
		t.Fatal("NeedsBun = true, want false for packaged bridge binary")
	}
	if got := rt.LaunchDir("/workspace"); got != "/workspace" {
		t.Fatalf("LaunchDir() = %q, want /workspace", got)
	}
}

func TestResolveBridgeRuntimeFromHonorsLegacyRelativeOverrideViaPkgshare(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	execPath := filepath.Join(root, "bin", "substrate")
	bridgePath := filepath.Join(root, "share", "substrate", "bridge", "omp-bridge.ts")

	if err := os.MkdirAll(filepath.Dir(execPath), 0o755); err != nil {
		t.Fatalf("mkdir executable dir: %v", err)
	}
	if err := os.WriteFile(execPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(bridgePath), 0o755); err != nil {
		t.Fatalf("mkdir bridge dir: %v", err)
	}
	if err := os.WriteFile(bridgePath, []byte("console.log('bridge');\n"), 0o644); err != nil {
		t.Fatalf("write bridge: %v", err)
	}

	rt, err := bridge.ResolveBridgeRuntimeFrom("bridge/omp-bridge.ts", execPath, "omp-bridge", "ohmypi bridge")
	if err != nil {
		t.Fatalf("resolveBridgeRuntimeFrom() error = %v", err)
	}
	if rt.Path != bridgePath {
		t.Fatalf("Path = %q, want %q", rt.Path, bridgePath)
	}
	if !rt.NeedsBun {
		t.Fatal("NeedsBun = false, want true for source bridge script")
	}
	if got := rt.LaunchDir("/workspace"); got != filepath.Dir(bridgePath) {
		t.Fatalf("LaunchDir() = %q, want %q", got, filepath.Dir(bridgePath))
	}
}

func TestEnsureBridgeDependenciesRequiresInstalledNodeModulesForSourceBridge(t *testing.T) {
	t.Parallel()

	runtimeDir := t.TempDir()
	bridgePath := filepath.Join(runtimeDir, "omp-bridge.ts")

	if err := os.WriteFile(bridgePath, []byte("console.log('bridge');\n"), 0o644); err != nil {
		t.Fatalf("write bridge: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "package.json"), []byte(`{"name":"bridge","private":true}`), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}

	err := bridge.EnsureBridgeDependencies(bridge.BridgeRuntime{Path: bridgePath, NeedsBun: true}, "@oh-my-pi/pi-coding-agent", "ohmypi bridge")
	if err == nil {
		t.Fatal("ensureBridgeDependencies() error = nil, want missing dependency error")
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
