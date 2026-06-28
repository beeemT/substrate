package bridge

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
)

func TestNewBridgeSessionUsesLargeEventBuffer(t *testing.T) {
	t.Parallel()

	s := NewBridgeSession("test-session", "agent")
	defer s.CloseEvents()

	if got := cap(s.Events); got != bridgeEventChannelSize {
		t.Fatalf("event channel capacity = %d, want %d", got, bridgeEventChannelSize)
	}
	if bridgeEventChannelSize != 256 {
		t.Fatalf("bridgeEventChannelSize = %d, want 256", bridgeEventChannelSize)
	}
}

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
			name:      "input with input_kind session_context",
			rawType:   "event",
			eventJSON: `{"type":"input","input_kind":"session_context","text":"context"}`,
			wantType:  "input",
			checkMeta: func(t *testing.T, meta map[string]any) {
				t.Helper()
				if k, _ := meta["input_kind"].(string); k != "session_context" {
					t.Errorf("input_kind = %v, want session_context", meta["input_kind"])
				}
			},
			checkPayload: "context",
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

func TestBridgeSessionDoesNotDropTerminalEventsWhenChannelIsFull(t *testing.T) {
	t.Parallel()

	s := NewBridgeSession("test-session", "agent")
	defer s.CloseEvents()

	for i := 0; i < cap(s.Events); i++ {
		s.emitEvent(adapter.AgentEvent{Type: "text_delta", Payload: "filler"})
	}

	doneSent := make(chan struct{})
	go func() {
		defer close(doneSent)
		s.emitEvent(adapter.AgentEvent{Type: "done", Payload: "complete"})
	}()

	select {
	case <-doneSent:
		t.Fatal("done event send completed while the channel was full; terminal events must wait for a reader instead of dropping")
	default:
	}

	for i := 0; i < cap(s.Events); i++ {
		<-s.Events
	}

	select {
	case <-doneSent:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for done event send after draining channel")
	}

	select {
	case evt := <-s.Events:
		if evt.Type != "done" {
			t.Fatalf("event type = %q, want done", evt.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for done event")
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

func TestResolveGitDir(t *testing.T) {
	t.Parallel()

	t.Run("git-work repo returns .bare path", func(t *testing.T) {
		t.Parallel()
		repoRoot := t.TempDir()
		worktree := filepath.Join(repoRoot, "sub-branch-name")
		if err := os.MkdirAll(filepath.Join(repoRoot, ".bare"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(worktree, 0o755); err != nil {
			t.Fatal(err)
		}
		got := ResolveGitDir(worktree)
		want := filepath.Join(repoRoot, ".bare")
		if got != want {
			t.Errorf("ResolveGitDir(%q) = %q, want %q", worktree, got, want)
		}
	})

	t.Run("plain git repo returns empty", func(t *testing.T) {
		t.Parallel()
		worktree := t.TempDir()
		got := ResolveGitDir(worktree)
		if got != "" {
			t.Errorf("ResolveGitDir(%q) = %q, want empty string", worktree, got)
		}
	})

	t.Run("non-existent worktree returns empty", func(t *testing.T) {
		t.Parallel()
		// Use a path that doesn't exist — Stat will fail, so empty is returned.
		got := ResolveGitDir("/nonexistent/path/to/worktree")
		if got != "" {
			t.Errorf("ResolveGitDir(nonexistent) = %q, want empty string", got)
		}
	})

	t.Run(".bare exists but is a file not a directory", func(t *testing.T) {
		t.Parallel()
		repoRoot := t.TempDir()
		worktree := filepath.Join(repoRoot, "sub-branch-name")
		if err := os.MkdirAll(worktree, 0o755); err != nil {
			t.Fatal(err)
		}
		// Create .bare as a file, not a directory.
		if err := os.WriteFile(filepath.Join(repoRoot, ".bare"), []byte("not a dir"), 0o644); err != nil {
			t.Fatal(err)
		}
		got := ResolveGitDir(worktree)
		if got != "" {
			t.Errorf("ResolveGitDir(%q) = %q, want empty string (file not dir)", worktree, got)
		}
	})
}

// startFakeBridge launches the testdata/fake-bridge.ts fixture as a subprocess
// and returns a wired-up BridgeSession ready for use. t.Fatal on missing deps.
// extraEnv controls bridge error modes (e.g. ERROR_MODE=nonzero_exit).
func startFakeBridge(t *testing.T, extraEnv ...string) *BridgeSession {
	t.Helper()

	bunPath, err := exec.LookPath("bun")
	if err != nil {
		t.Skipf("bun not in PATH: %v", err)
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	fixturePath := filepath.Join(filepath.Dir(thisFile), "testdata", "fake-bridge.ts")
	if _, err := os.Stat(fixturePath); err != nil {
		t.Fatalf("fixture missing: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	cmd := exec.CommandContext(ctx, bunPath, fixturePath)
	cmd.Dir = filepath.Dir(fixturePath)
	cmd.Env = append(os.Environ(), extraEnv...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start bridge: %v", err)
	}

	bs := NewBridgeSession("fake-test", adapter.SessionModeAgent)
	bs.Cmd = cmd
	bs.Stdin = stdin
	bs.Stdout = stdout
	bs.Stderr = stderr
	bs.StartReaders()
	return bs
}

// drainEvents reads all events from ch until it closes or the deadline expires.
func drainEvents(ch <-chan adapter.AgentEvent, deadline time.Duration) ([]adapter.AgentEvent, bool) {
	var events []adapter.AgentEvent
	timer := time.After(deadline)
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return events, true
			}
			events = append(events, evt)
		case <-timer:
			return events, false
		}
	}
}

func TestBridgeProcessHappyPath(t *testing.T) {
	t.Parallel()

	bs := startFakeBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	if err := bs.SendPrompt("hello"); err != nil {
		t.Fatalf("SendPrompt: %v", err)
	}

	// Close stdin so the fake bridge detects EOF and exits cleanly.
	if err := bs.Stdin.Close(); err != nil {
		t.Fatalf("close stdin: %v", err)
	}

	// Wait for the bridge to exit.
	if err := bs.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	// Events channel must be closed after Wait returns.
	events, closed := drainEvents(bs.EventsChan(), 2*time.Second)
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3", len(events))
	}
	if !closed {
		t.Fatal("event channel not closed after session end")
	}

	if events[0].Type != "input" {
		t.Errorf("events[0].Type = %q, want input", events[0].Type)
	}
	if events[1].Type != "text_delta" {
		t.Errorf("events[1].Type = %q, want text_delta", events[1].Type)
	}
	if events[1].Payload != "echo: hello" {
		t.Errorf("events[1].Payload = %q, want 'echo: hello'", events[1].Payload)
	}
	if events[2].Type != "done" {
		t.Errorf("events[2].Type = %q, want done", events[2].Type)
	}
}

func TestBridgeProcessFollowUpMessage(t *testing.T) {
	t.Parallel()

	bs := startFakeBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	// Send initial prompt.
	if err := bs.SendPrompt("first"); err != nil {
		t.Fatalf("SendPrompt: %v", err)
	}

	// Send follow-up message.
	if err := bs.SendMessage(ctx, "second"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	// Close stdin so the fake bridge detects EOF and exits cleanly.
	if err := bs.Stdin.Close(); err != nil {
		t.Fatalf("close stdin: %v", err)
	}

	if err := bs.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	events, closed := drainEvents(bs.EventsChan(), 2*time.Second)
	// Expected: input, text_delta("echo: first"), done, text_delta("followup: second"), done
	if len(events) != 5 {
		t.Fatalf("got %d events, want 5: %v", len(events), events)
	}
	if !closed {
		t.Fatal("event channel not closed after session end")
	}

	if events[0].Type != "input" {
		t.Errorf("events[0].Type = %q, want input", events[0].Type)
	}
	if events[1].Payload != "echo: first" {
		t.Errorf("events[1].Payload = %q, want 'echo: first'", events[1].Payload)
	}
	if events[2].Type != "done" {
		t.Errorf("events[2].Type = %q, want done", events[2].Type)
	}
	if events[3].Payload != "followup: second" {
		t.Errorf("events[3].Payload = %q, want 'followup: second'", events[3].Payload)
	}
	if events[4].Type != "done" {
		t.Errorf("events[4].Type = %q, want done", events[4].Type)
	}
}

func TestBridgeProcessAbort(t *testing.T) {
	t.Parallel()

	bs := startFakeBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	// Send init first so the bridge is waiting for a prompt.
	if err := bs.WriteRawMsg([]byte(`{"type":"init"}`)); err != nil {
		t.Fatalf("WriteRawMsg: %v", err)
	}

	if err := bs.Abort(ctx); err != nil {
		t.Fatalf("Abort: %v", err)
	}

	// Events channel must close after Abort.
	events, closed := drainEvents(bs.EventsChan(), 2*time.Second)
	if len(events) != 0 {
		t.Errorf("expected 0 events after abort, got %d", len(events))
	}
	if !closed {
		t.Fatal("event channel not closed after session end")
	}
}

func TestBridgeProcessMalformedOutput(t *testing.T) {
	t.Parallel()

	bs := startFakeBridge(t, "ERROR_MODE=malformed")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	// The bridge emits one malformed line and exits 0.
	// readEvents skips the malformed line; Wait should return nil.
	if err := bs.Wait(ctx); err != nil {
		t.Fatalf("Wait should succeed for malformed output with clean exit: %v", err)
	}

	events, closed := drainEvents(bs.EventsChan(), 2*time.Second)
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
	if !closed {
		t.Fatal("event channel not closed after session end")
	}
}

func TestBridgeProcessNonZeroExit(t *testing.T) {
	t.Parallel()

	bs := startFakeBridge(t, "ERROR_MODE=nonzero_exit")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	err := bs.Wait(ctx)
	if err == nil {
		t.Fatal("Wait should return an error for non-zero exit")
	}
	if !strings.Contains(err.Error(), "bridge subprocess exited") {
		t.Errorf("error = %q, want it to contain 'bridge subprocess exited'", err.Error())
	}

	// Events channel must still close — no goroutine left dangling.
	events, closed := drainEvents(bs.EventsChan(), 2*time.Second)
	if len(events) != 0 {
		t.Errorf("expected 0 events on non-zero exit, got %d", len(events))
	}
	if !closed {
		t.Fatal("event channel not closed after session end")
	}
}
