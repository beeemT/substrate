package opencode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
)

// Stage 1 harness-contract checklist — OpenCode adapter:
//   §1  config/readiness path: Name, Capabilities, DetectServerURL
//   §2  stable session ID / logs use Substrate session ID
//   §3  initial prompt reaches child boundary
//   §4  assistant output → SSE text_delta or equivalent
//   §5  terminal success → done
//   §6  follow-up messaging (SupportsMessaging = true)
//   §7  SendAnswer / Steer / Compact work or documented unsupported
//   §8  abort closes event stream and process resources
//   §9  readiness/startup/malformed failures → useful errors
//   §10 canonical assistant log output review-parseable

// §1 — config/readiness path: harness name

func TestName(t *testing.T) {
	h := NewHarness(config.OpenCodeConfig{}, "/tmp")
	if got := h.Name(); got != "opencode" {
		t.Errorf("Name() = %q, want %q", got, "opencode")
	}
}

// §1 — config/readiness path: capabilities and supported tools

func TestCapabilities(t *testing.T) {
	h := NewHarness(config.OpenCodeConfig{}, "/tmp")
	caps := h.Capabilities()

	if !caps.SupportsStreaming {
		t.Error("expected SupportsStreaming == true")
	}
	if !caps.SupportsMessaging {
		t.Error("expected SupportsMessaging == true")
	}
	if !caps.SupportsNativeResume {
		t.Error("expected SupportsNativeResume == true")
	}

	expectedTools := []string{
		"Read", "Write", "Edit", "Bash", "Glob", "Grep",
		"mcp__substrate-foreman__ask_foreman",
	}
	for _, tool := range expectedTools {
		if !slices.Contains(caps.SupportedTools, tool) {
			t.Errorf("SupportedTools missing %q; got %v", tool, caps.SupportedTools)
		}
	}
}

// §7 — Compact: documented supported (SupportsCompact)

func TestSupportsCompact(t *testing.T) {
	h := NewHarness(config.OpenCodeConfig{}, "/tmp")
	if !h.SupportsCompact() {
		t.Error("expected SupportsCompact() == true")
	}
}

// §3 — initial prompt: system + user prompt folded

func TestBuildInitialPromptFoldsSystemAndUser(t *testing.T) {
	if got := buildInitialPrompt("system", "user"); got != "system\n\nuser" {
		t.Fatalf("buildInitialPrompt with system = %q", got)
	}
	if got := buildInitialPrompt("", "user"); got != "user" {
		t.Fatalf("buildInitialPrompt without system = %q", got)
	}
}

// §1 — config/readiness path: question tool policy registration

func TestShouldRegisterQuestionMCPRespectsQuestionPolicy(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		mode   adapter.SessionMode
		policy adapter.QuestionToolPolicy
		want   bool
	}{
		{name: "default agent", mode: adapter.SessionModeAgent, policy: adapter.QuestionToolPolicyDefault, want: true},
		{name: "foreman policy", mode: adapter.SessionModeAgent, policy: adapter.QuestionToolPolicyForeman, want: true},
		{name: "human policy", mode: adapter.SessionModeAgent, policy: adapter.QuestionToolPolicyHuman, want: false},
		{name: "none policy", mode: adapter.SessionModeAgent, policy: adapter.QuestionToolPolicyNone, want: false},
		{name: "foreman session", mode: adapter.SessionModeForeman, policy: adapter.QuestionToolPolicyDefault, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := shouldRegisterQuestionMCP(tc.mode, tc.policy); got != tc.want {
				t.Fatalf("shouldRegisterQuestionMCP = %v, want %v", got, tc.want)
			}
		})
	}
}

// §9 — readiness failure: binary not found

func TestValidateReadiness_BinaryNotFound(t *testing.T) {
	// Use a BinaryPath pointing to a binary that definitely doesn't exist.
	err := ValidateReadiness(config.OpenCodeConfig{
		BinaryPath: "nonexistent_opencode_binary_test",
	})
	if err == nil {
		t.Fatal("expected error for missing binary, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found', got: %v", err)
	}
}

// §9 — readiness failure: unsupported RunAction

func TestRunAction_UnsupportedAction(t *testing.T) {
	h := NewHarness(config.OpenCodeConfig{}, "/tmp")
	_, err := h.RunAction(context.Background(), adapter.HarnessActionRequest{
		Action: "bogus",
	})
	if err == nil {
		t.Fatal("expected error for unsupported action, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported opencode action") {
		t.Errorf("error should mention 'unsupported opencode action', got: %v", err)
	}
}

// §1 — config/readiness path: server URL detection

func TestDetectServerURL_MatchesPattern(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Simulate stdout with the expected line among noise.
	pr, pw := io.Pipe()
	go func() {
		if _, err := fmt.Fprintln(pw, "loading config..."); err != nil {
			slog.Warn("write detect URL fixture failed", "error", err)
			return
		}
		if _, err := fmt.Fprintln(pw, "Server running on http://127.0.0.1:42187"); err != nil {
			slog.Warn("write detect URL fixture failed", "error", err)
			return
		}
		if _, err := fmt.Fprintln(pw, "ready"); err != nil {
			slog.Warn("write detect URL fixture failed", "error", err)
		}
		if err := pw.Close(); err != nil {
			slog.Warn("close detect URL pipe failed", "error", err)
		}
	}()

	url, err := detectServerURL(ctx, pr)
	if err != nil {
		t.Fatalf("detectServerURL: %v", err)
	}
	if url != "http://127.0.0.1:42187" {
		t.Fatalf("url = %q, want %q", url, "http://127.0.0.1:42187")
	}
}

// §9 — startup failure: server URL detection with context cancellation

func TestDetectServerURL_ContextCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe()

	// Cancel after a short delay without writing the expected line.
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
		pw.Close()
	}()

	_, err := detectServerURL(ctx, pr)
	if err == nil {
		t.Fatal("expected error on context cancel, got nil")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("error = %v, want context canceled", err)
	}
}

// §9 — startup failure: server URL detection with stream end

func TestDetectServerURL_StreamEnd(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Close stdout without ever writing the pattern.
	pr, pw := io.Pipe()
	pw.Close()

	_, err := detectServerURL(ctx, pr)
	if err == nil {
		t.Fatal("expected error when stream ends without URL, got nil")
	}
	if !strings.Contains(err.Error(), "server URL not found") {
		t.Fatalf("error = %v, want 'server URL not found'", err)
	}
}

func TestHelperOpenCodeProcess(t *testing.T) {
	if os.Getenv("GO_WANT_OPENCODE_HELPER") != "1" {
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		if _, err := fmt.Println("opencode fake"); err != nil {
			slog.Error("fake opencode: write version failed", "error", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if os.Getenv("FAKE_OPENCODE_EXIT_NONZERO") == "1" {
		os.Exit(1)
	}

	helper := newFakeOpenCodeHelper()
	if err := helper.serve(); err != nil {
		slog.Error("fake opencode helper failed", "error", err)
		os.Exit(1)
	}
}

type fakeOpenCodeHelper struct {
	mu        sync.Mutex
	created   bool
	capture   string
	events    chan string
	sessions  map[string]bool
	malformed bool
	turn      int
}

func newFakeOpenCodeHelper() *fakeOpenCodeHelper {
	return &fakeOpenCodeHelper{
		capture:   os.Getenv("FAKE_OPENCODE_CAPTURE_PATH"),
		events:    make(chan string, 32),
		sessions:  make(map[string]bool),
		malformed: os.Getenv("FAKE_OPENCODE_MALFORMED_SSE") == "1",
	}
}

func (h *fakeOpenCodeHelper) serve() error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	if _, err := fmt.Printf("Server running on http://%s\n", listener.Addr().String()); err != nil {
		return fmt.Errorf("write server URL: %w", err)
	}
	if err := os.Stdout.Sync(); err != nil {
		slog.Debug("fake opencode: stdout sync skipped", "error", err)
	}
	return http.Serve(listener, h)
}

func (h *fakeOpenCodeHelper) emit(ctx context.Context, event string) {
	select {
	case h.events <- event:
	case <-ctx.Done():
	default:
	}
}

func (h *fakeOpenCodeHelper) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/session":
		if os.Getenv("FAKE_OPENCODE_BAD_HEALTH") == "1" {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodPost && r.URL.Path == "/session":
		h.handleCreateSession(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/event":
		h.handleEvents(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/session/") && strings.HasSuffix(r.URL.Path, "/message"):
		h.handleMessage(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/session/") && strings.HasSuffix(r.URL.Path, "/summarize"):
		h.captureRequest(r, "summarize")
		h.emit(r.Context(), `{"type":"session.compacted","sessionID":"`+h.sessionIDFromPath(r.URL.Path)+`"}`)
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/session/") && strings.HasSuffix(r.URL.Path, "/abort"):
		h.captureRequest(r, "abort")
		h.emit(r.Context(), `{"type":"session.aborted","sessionID":"`+h.sessionIDFromPath(r.URL.Path)+`"}`)
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/question/") && strings.HasSuffix(r.URL.Path, "/reply"):
		h.captureRequest(r, "answer")
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodPost && r.URL.Path == "/mcp":
		h.captureRequest(r, "mcp")
		w.WriteHeader(http.StatusOK)
	default:
		http.NotFound(w, r)
	}
}

func (h *fakeOpenCodeHelper) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	h.captureRequest(r, "create")
	switch os.Getenv("FAKE_OPENCODE_CREATE_FAILURE") {
	case "status":
		http.Error(w, "create failed", http.StatusInternalServerError)
		return
	case "decode":
		w.WriteHeader(http.StatusOK)
		if _, err := fmt.Fprint(w, "{"); err != nil {
			slog.Warn("fake opencode: write malformed create response failed", "error", err)
		}
		return
	case "empty":
		w.Header().Set("Content-Type", "application/json")
		if _, err := fmt.Fprint(w, `{"id":""}`); err != nil {
			slog.Warn("fake opencode: write empty create response failed", "error", err)
		}
		return
	}
	sessionID := os.Getenv("FAKE_OPENCODE_SESSION_ID")
	if sessionID == "" {
		sessionID = "oc-fake-1"
	}
	h.mu.Lock()
	h.sessions[sessionID] = true
	h.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if _, err := fmt.Fprintf(w, `{"id":%q}`, sessionID); err != nil {
		slog.Warn("fake opencode: write create session response failed", "error", err)
	}
}

func (h *fakeOpenCodeHelper) handleMessage(w http.ResponseWriter, r *http.Request) {
	h.captureRequest(r, "message")
	if os.Getenv("FAKE_OPENCODE_MESSAGE_FAILURE") == "1" {
		http.Error(w, "message failed", http.StatusInternalServerError)
		return
	}
	sessionID := h.sessionIDFromPath(r.URL.Path)
	h.mu.Lock()
	emitCreated := !h.created
	if emitCreated {
		h.created = true
	}
	h.turn++
	turn := h.turn
	h.mu.Unlock()
	if emitCreated {
		h.emit(r.Context(), `{"type":"session.created","sessionID":"`+sessionID+`"}`)
	}
	if h.malformed {
		h.emit(r.Context(), `not-json`)
	}
	h.emit(r.Context(), `{"type":"question.asked","sessionID":"`+sessionID+`","question":{"requestID":"q-1","question":"Need answer?"}}`)
	messageID := fmt.Sprintf("m%d", turn)
	h.emit(r.Context(), `{"type":"message.updated","sessionID":"`+sessionID+`","message":{"id":"`+messageID+`","parts":[{"type":"text","text":"Hello from fake OpenCode"}]}}`)
	h.emit(r.Context(), `{"type":"message.updated","sessionID":"`+sessionID+`","message":{"id":"`+messageID+`","parts":[{"type":"tool-use","state":"started","toolUseID":"tu1","toolName":"Bash","input":{"cmd":"echo fake"}},{"type":"tool-result","toolResultID":"tr1","output":"fake output"}]}}`)
	h.emit(r.Context(), `{"type":"session.completed","sessionID":"`+sessionID+`"}`)
	w.WriteHeader(http.StatusOK)
}

func (h *fakeOpenCodeHelper) handleEvents(w http.ResponseWriter, r *http.Request) {
	h.captureRequest(r, "event")
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	for {
		select {
		case event := <-h.events:
			if _, err := fmt.Fprintf(w, "data: %s\n\n", event); err != nil {
				slog.Warn("fake opencode: write SSE event failed", "error", err)
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		case <-r.Context().Done():
			return
		}
	}
}

func (h *fakeOpenCodeHelper) captureRequest(r *http.Request, kind string) {
	if h.capture == "" {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Warn("fake opencode: read request body failed", "kind", kind, "error", err)
		return
	}
	f, err := os.OpenFile(h.capture, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		slog.Warn("fake opencode: open capture file failed", "error", err)
		return
	}
	defer func() {
		if err := f.Close(); err != nil {
			slog.Warn("fake opencode: close capture file failed", "error", err)
		}
	}()
	if _, err := fmt.Fprintf(f, "%s %s %s\n", kind, r.URL.Path, strings.TrimSpace(string(body))); err != nil {
		slog.Warn("fake opencode: write capture file failed", "error", err)
	}
}

func (h *fakeOpenCodeHelper) sessionIDFromPath(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) >= 3 {
		return parts[2]
	}
	return "oc-fake-1"
}

func fakeOpenCodeBinary(t *testing.T) string {
	t.Helper()
	binDir := t.TempDir()
	path := filepath.Join(binDir, "opencode")
	content := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'opencode fake'; exit 0; fi\nGO_WANT_OPENCODE_HELPER=1 %q -test.run=TestHelperOpenCodeProcess -- \"$@\"\n", os.Args[0])
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake opencode: %v", err)
	}
	return path
}

func collectOpenCodeUntil(t *testing.T, sess adapter.AgentSession, eventType string, timeout time.Duration) []adapter.AgentEvent {
	t.Helper()
	timer := time.After(timeout)
	var events []adapter.AgentEvent
	for {
		select {
		case ev, ok := <-sess.Events():
			if !ok {
				t.Fatalf("events closed before %s; events=%#v", eventType, events)
			}
			events = append(events, ev)
			if ev.Type == eventType {
				return events
			}
		case <-timer:
			t.Fatalf("timed out waiting for %s; events=%#v", eventType, events)
		}
	}
}

func opencodeEventPayloads(events []adapter.AgentEvent, eventType string) string {
	var b strings.Builder
	for _, ev := range events {
		if ev.Type == eventType {
			b.WriteString(ev.Payload)
		}
	}
	return b.String()
}

func readOpenCodeCapture(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	return string(data)
}

func TestStartSessionWithFakeOpenCodeServer(t *testing.T) {
	binary := fakeOpenCodeBinary(t)
	tmpDir := t.TempDir()
	capturePath := filepath.Join(tmpDir, "requests.log")
	t.Setenv("FAKE_OPENCODE_CAPTURE_PATH", capturePath)
	h := NewHarness(config.OpenCodeConfig{BinaryPath: binary, Agent: "build", Model: "test-model", Variant: "fast"}, tmpDir)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	session, err := h.StartSession(ctx, adapter.SessionOpts{
		SessionID:     "opencode-wire-001",
		Mode:          adapter.SessionModeAgent,
		WorktreePath:  tmpDir,
		SessionLogDir: filepath.Join(tmpDir, "sessions"),
		SystemPrompt:  "System prompt",
		UserPrompt:    "User prompt",
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer func() {
		if err := session.Abort(ctx); err != nil {
			t.Logf("Abort cleanup: %v", err)
		}
	}()

	if got := session.ID(); got != "opencode-wire-001" {
		t.Fatalf("session ID = %q, want opencode-wire-001", got)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		captureNow := readOpenCodeCapture(t, capturePath)
		if strings.Contains(captureNow, "message /session/oc-fake-1/message") && strings.Contains(captureNow, "event /event") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("initial message or SSE connection missing within deadline: %s", captureNow)
		}
		time.Sleep(50 * time.Millisecond)
	}
	events := collectOpenCodeUntil(t, session, "done", 5*time.Second)
	if got := session.ResumeInfo()["opencode_session_id"]; got != "oc-fake-1" {
		t.Fatalf("opencode_session_id = %q, want oc-fake-1", got)
	}
	if text := opencodeEventPayloads(events, "text_delta"); text != "Hello from fake OpenCode" {
		t.Fatalf("text_delta = %q, want fake output", text)
	}
	if tool := opencodeEventPayloads(events, "tool_result"); tool != "fake output" {
		t.Fatalf("tool_result = %q, want fake output", tool)
	}
	capture := readOpenCodeCapture(t, capturePath)
	if !strings.Contains(capture, `create /session {"agent":"build","model":"test-model"}`) {
		t.Fatalf("capture missing create request: %s", capture)
	}
	if !strings.Contains(capture, "message /session/oc-fake-1/message") || !strings.Contains(capture, "System prompt\\n\\nUser prompt") {
		t.Fatalf("capture missing folded initial message: %s", capture)
	}
	if !strings.Contains(capture, `"variant":"fast"`) {
		t.Fatalf("capture missing variant: %s", capture)
	}
	logContent, err := os.ReadFile(filepath.Join(tmpDir, "sessions", "opencode-wire-001.log"))
	if err != nil {
		t.Fatalf("read session log: %v", err)
	}
	if !strings.Contains(string(logContent), "Hello from fake OpenCode") {
		t.Fatalf("session log missing assistant output: %s", logContent)
	}

	if err := session.SendMessage(ctx, "follow-up"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	followEvents := collectOpenCodeUntil(t, session, "done", 5*time.Second)
	if text := opencodeEventPayloads(followEvents, "text_delta"); text != "Hello from fake OpenCode" {
		t.Fatalf("follow-up text_delta = %q", text)
	}
	if err := session.SendAnswer(ctx, "operator answer"); err != nil {
		t.Fatalf("SendAnswer: %v", err)
	}
	if err := session.Compact(ctx); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	collectOpenCodeUntil(t, session, "lifecycle", 5*time.Second)
	if err := session.Steer(ctx, "direction"); !errors.Is(err, adapter.ErrSteerNotSupported) {
		t.Fatalf("Steer error = %v, want ErrSteerNotSupported", err)
	}
}

func TestStartSessionWithFakeOpenCodeNativeResume(t *testing.T) {
	binary := fakeOpenCodeBinary(t)
	tmpDir := t.TempDir()
	capturePath := filepath.Join(tmpDir, "requests.log")
	t.Setenv("FAKE_OPENCODE_CAPTURE_PATH", capturePath)
	h := NewHarness(config.OpenCodeConfig{BinaryPath: binary}, tmpDir)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	session, err := h.StartSession(ctx, adapter.SessionOpts{
		SessionID:     "opencode-resume-001",
		Mode:          adapter.SessionModeAgent,
		WorktreePath:  tmpDir,
		SessionLogDir: filepath.Join(tmpDir, "sessions"),
		UserPrompt:    "resume prompt",
		ResumeInfo:    map[string]string{"opencode_session_id": "oc-existing"},
	})
	if err != nil {
		t.Fatalf("StartSession resume: %v", err)
	}
	defer func() {
		if err := session.Abort(ctx); err != nil {
			t.Logf("Abort cleanup: %v", err)
		}
	}()
	collectOpenCodeUntil(t, session, "done", 5*time.Second)
	capture := readOpenCodeCapture(t, capturePath)
	if strings.Contains(capture, "create /session") {
		t.Fatalf("resume should not create a new server session: %s", capture)
	}
	if !strings.Contains(capture, "message /session/oc-existing/message") {
		t.Fatalf("resume did not send to existing session: %s", capture)
	}
}

func TestStartSessionWithFakeOpenCodeAbortClosesEvents(t *testing.T) {
	binary := fakeOpenCodeBinary(t)
	tmpDir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	session, err := NewHarness(config.OpenCodeConfig{BinaryPath: binary}, tmpDir).StartSession(ctx, adapter.SessionOpts{
		SessionID:    "opencode-abort-001",
		Mode:         adapter.SessionModeForeman,
		WorktreePath: tmpDir,
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if err := session.Abort(ctx); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	select {
	case <-session.Done():
	case <-ctx.Done():
		t.Fatalf("Done did not close: %v", ctx.Err())
	}
	for {
		select {
		case _, ok := <-session.Events():
			if !ok {
				return
			}
		case <-ctx.Done():
			t.Fatalf("events channel did not close after abort: %v", ctx.Err())
		}
	}
}

func TestStartSessionWithFakeOpenCodeFailureModes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		envKey  string
		envVal  string
		wantErr string
	}{
		{name: "bad_health", envKey: "FAKE_OPENCODE_BAD_HEALTH", envVal: "1", wantErr: "health check"},
		{name: "create_status", envKey: "FAKE_OPENCODE_CREATE_FAILURE", envVal: "status", wantErr: "create session: HTTP 500"},
		{name: "create_decode", envKey: "FAKE_OPENCODE_CREATE_FAILURE", envVal: "decode", wantErr: "decode create session response"},
		{name: "create_empty", envKey: "FAKE_OPENCODE_CREATE_FAILURE", envVal: "empty", wantErr: "empty session ID"},
		{name: "message_status", envKey: "FAKE_OPENCODE_MESSAGE_FAILURE", envVal: "1", wantErr: "send initial prompt"},
		{name: "nonzero_exit", envKey: "FAKE_OPENCODE_EXIT_NONZERO", envVal: "1", wantErr: "detect server URL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binary := fakeOpenCodeBinary(t)
			t.Setenv(tc.envKey, tc.envVal)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			_, err := NewHarness(config.OpenCodeConfig{BinaryPath: binary}, t.TempDir()).StartSession(ctx, adapter.SessionOpts{
				SessionID:    "opencode-failure-" + tc.name,
				Mode:         adapter.SessionModeAgent,
				WorktreePath: t.TempDir(),
				UserPrompt:   "prompt",
			})
			if err == nil {
				t.Fatal("StartSession error = nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("StartSession error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestStartSessionWithFakeOpenCodeMalformedSSE(t *testing.T) {
	binary := fakeOpenCodeBinary(t)
	t.Setenv("FAKE_OPENCODE_MALFORMED_SSE", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	session, err := NewHarness(config.OpenCodeConfig{BinaryPath: binary}, t.TempDir()).StartSession(ctx, adapter.SessionOpts{
		SessionID:    "opencode-malformed-sse",
		Mode:         adapter.SessionModeAgent,
		WorktreePath: t.TempDir(),
		UserPrompt:   "prompt",
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer func() {
		if err := session.Abort(ctx); err != nil {
			t.Logf("Abort cleanup: %v", err)
		}
	}()
	events := collectOpenCodeUntil(t, session, "done", 5*time.Second)
	if text := opencodeEventPayloads(events, "text_delta"); text != "Hello from fake OpenCode" {
		t.Fatalf("text_delta after malformed SSE = %q", text)
	}
}

func TestRunActionCheckAuthWithFakeOpenCode(t *testing.T) {
	binary := fakeOpenCodeBinary(t)
	result, err := NewHarness(config.OpenCodeConfig{BinaryPath: binary}, t.TempDir()).RunAction(context.Background(), adapter.HarnessActionRequest{Action: "check_auth"})
	if err != nil {
		t.Fatalf("RunAction check_auth: %v", err)
	}
	if !result.Success || result.Identity != binary {
		t.Fatalf("result = %+v, want fake binary identity", result)
	}
}
