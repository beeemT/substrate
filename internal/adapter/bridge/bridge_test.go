package bridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBridgeMsgJSONRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		msg  BridgeMsg
	}{
		{name: "prompt message", msg: BridgeMsg{Type: "prompt", Text: "Hello, agent!"}},
		{name: "message without text", msg: BridgeMsg{Type: "abort"}},
		{name: "answer message", msg: BridgeMsg{Type: "answer", Text: "This is the answer."}},
		{name: "message type", msg: BridgeMsg{Type: "message", Text: "Follow-up"}},
		{name: "steer type", msg: BridgeMsg{Type: "steer", Text: "Change direction"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.msg)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var got BridgeMsg
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got.Type != tt.msg.Type {
				t.Errorf("Type = %q, want %q", got.Type, tt.msg.Type)
			}
			if got.Text != tt.msg.Text {
				t.Errorf("Text = %q, want %q", got.Text, tt.msg.Text)
			}
		})
	}
}

func TestEscapeSandboxPath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain path", in: "/tmp/foo", want: "/tmp/foo"},
		{name: "backslash", in: "C:\\Users\\foo", want: "C:\\\\Users\\\\foo"},
		{name: "double quote", in: "/path/with \"quote\"", want: "/path/with \\\"quote\\\""},
		{name: "both", in: "C:\\path \"here\"", want: "C:\\\\path \\\"here\\\""},
		{name: "empty", in: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EscapeSandboxPath(tt.in)
			if got != tt.want {
				t.Errorf("EscapeSandboxPath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestDedupePaths(t *testing.T) {
	tests := []struct {
		name  string
		paths []string
		want  int
	}{
		{name: "no dupes", paths: []string{"/a", "/b", "/c"}, want: 3},
		{name: "exact dupes", paths: []string{"/a", "/a", "/b"}, want: 2},
		{name: "empty strings filtered", paths: []string{"", "/a", ""}, want: 1},
		{name: "normalized dupes", paths: []string{"/a/./b", "/a/b"}, want: 1},
		{name: "nil", paths: nil, want: 0},
		{name: "all empty", paths: []string{"", "", ""}, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DedupePaths(tt.paths)
			if len(got) != tt.want {
				t.Errorf("DedupePaths(%v) = %d paths, want %d", tt.paths, len(got), tt.want)
			}
		})
	}
}

func TestIsBridgeScript(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/foo/bridge.ts", true},
		{"/foo/bridge.mts", true},
		{"/foo/bridge.cts", true},
		{"/foo/bridge.js", true},
		{"/foo/bridge.mjs", true},
		{"/foo/bridge.cjs", true},
		{"/foo/bridge.TS", true},
		{"/foo/bridge", false},
		{"/foo/bridge.exe", false},
		{"/foo/bridge.go", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := IsBridgeScript(tt.path)
			if got != tt.want {
				t.Errorf("IsBridgeScript(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
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
		got := FirstNonEmpty(tt.values...)
		if got != tt.want {
			t.Errorf("FirstNonEmpty(%v) = %q, want %q", tt.values, got, tt.want)
		}
	}
}

func TestBridgeCandidatesDefault(t *testing.T) {
	// Default candidates should include the bridge name in both exec and share dirs.
	candidates := BridgeCandidates("", "/usr/local/bin/substrate", "omp-bridge")
	found := false
	for _, c := range candidates {
		if strings.Contains(c, "omp-bridge") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected omp-bridge in candidates, got %v", candidates)
	}
}

func TestBridgeCandidatesConfigured(t *testing.T) {
	abs := BridgeCandidates("/absolute/path/bridge", "/usr/local/bin/substrate", "omp-bridge")
	if len(abs) != 1 || abs[0] != "/absolute/path/bridge" {
		t.Errorf("absolute configured path: got %v", abs)
	}

	rel := BridgeCandidates("bridge/my-bridge", "/usr/local/bin/substrate", "omp-bridge")
	if len(rel) == 0 {
		t.Error("expected candidates for relative path, got none")
	}
	for _, c := range rel {
		if strings.Contains(c, "claude-agent-bridge") {
			t.Errorf("configured relative path contains unexpected 'claude-agent-bridge': %v", c)
		}
	}
}

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
			name:         "lifecycle retry_exhausted",
			rawType:      "event",
			eventJSON:    `{"type":"lifecycle","stage":"retry_exhausted","message":"Rate limit retries exhausted — session produced no work"}`,
			wantType:     "retry_exhausted",
			checkPayload: "Rate limit retries exhausted — session produced no work",
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
			got, err := MapBridgeEvent(raw)
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

func TestCompressFile(t *testing.T) {
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "test.log")
	dst := filepath.Join(srcDir, "test.log.gz")
	content := "hello world\n"
	if err := os.WriteFile(src, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CompressFile(src, dst); err != nil {
		t.Fatalf("CompressFile: %v", err)
	}
	// Source should be removed.
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("source file should be removed after compression")
	}
	// Destination should exist.
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("compressed file missing: %v", err)
	}
}

func TestEnsureBridgeDependenciesNoPackageJSON(t *testing.T) {
	root := t.TempDir()
	scriptPath := filepath.Join(root, "bridge.ts")
	if err := os.WriteFile(scriptPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	rt := BridgeRuntime{Path: scriptPath, NeedsBun: true}
	if err := EnsureBridgeDependencies(rt, "@oh-my-pi/pi-coding-agent", "test bridge"); err != nil {
		t.Errorf("expected no error without package.json, got: %v", err)
	}
}

func TestEnsureBridgeDependenciesMissing(t *testing.T) {
	root := t.TempDir()
	scriptPath := filepath.Join(root, "bridge.ts")
	if err := os.WriteFile(scriptPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	rt := BridgeRuntime{Path: scriptPath, NeedsBun: true}
	err := EnsureBridgeDependencies(rt, "@oh-my-pi/pi-coding-agent", "test bridge")
	if err == nil {
		t.Fatal("expected error for missing dependencies, got nil")
	}
	if !strings.Contains(err.Error(), "bun install") {
		t.Errorf("error should suggest bun install: %v", err)
	}
}

func TestResolveBridgeRuntimeFromUsesPackagedBridge(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	execPath := filepath.Join(root, "bin", "substrate")
	bridgePath := filepath.Join(root, "share", "substrate", "bridge", "test-bridge")
	if err := os.MkdirAll(filepath.Dir(execPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(execPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(bridgePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(bridgePath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	rt, err := ResolveBridgeRuntimeFrom("", execPath, "test-bridge", "test bridge")
	if err != nil {
		t.Fatalf("ResolveBridgeRuntimeFrom: %v", err)
	}
	if rt.NeedsBun {
		t.Error("expected NeedsBun == false for a binary bridge")
	}
	if !strings.Contains(rt.Path, "test-bridge") {
		t.Errorf("expected path to contain test-bridge, got %q", rt.Path)
	}
}

func TestResolveBridgeRuntimeFromSourceScriptSetsNeedsBun(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	execPath := filepath.Join(root, "bin", "substrate")
	bridgePath := filepath.Join(root, "share", "substrate", "bridge", "test-bridge.ts")
	if err := os.MkdirAll(filepath.Dir(execPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(execPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(bridgePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(bridgePath, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	rt, err := ResolveBridgeRuntimeFrom("", execPath, "test-bridge", "test bridge")
	if err != nil {
		t.Fatalf("ResolveBridgeRuntimeFrom: %v", err)
	}
	if !rt.NeedsBun {
		t.Error("expected NeedsBun == true for a .ts bridge script")
	}
}
