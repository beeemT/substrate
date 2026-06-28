package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/sessionlog"
)

// Stage 1 harness-contract checklist — Codex adapter:
//   §1  config/readiness path: buildArgs, capabilities, check_auth
//   §2  stable session ID / logs use Substrate session ID
//   §3  initial prompt reaches child boundary
//   §4  assistant output → text_delta (JSONL event mapping)
//   §5  terminal success → done
//   §6  follow-up messaging via resume (SupportsMessaging = true)
//   §7  SendAnswer / Steer / Compact work or documented unsupported
//   §8  abort closes event stream and process resources
//   §9  readiness/startup/malformed failures → useful errors
//   §10 canonical assistant log output review-parseable

// §9 — event emission: terminal event backpressure

func TestSessionEmitEventBlocksTerminalEventsWhenChannelFull(t *testing.T) {
	t.Parallel()

	s := &session{events: make(chan adapter.AgentEvent, 1)}
	s.events <- adapter.AgentEvent{Type: "text_delta", Payload: "filler"}
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		s.emitEvent(adapter.AgentEvent{Type: "done"})
	}()

	select {
	case <-sent:
		t.Fatal("terminal event send completed while channel was full")
	case <-time.After(10 * time.Millisecond):
	}

	<-s.events
	select {
	case <-sent:
	case <-time.After(time.Second):
		t.Fatal("terminal event send did not complete after draining channel")
	}
	if got := <-s.events; got.Type != "done" {
		t.Fatalf("event type = %q, want done", got.Type)
	}
}

// ---------------------------------------------------------------------------
// buildArgs — §1 config/readiness path, §3 initial prompt, §6 follow-up
// ---------------------------------------------------------------------------

// §1 — config/readiness path: new session build args

func TestBuildArgs_NewSession(t *testing.T) {
	cfg := config.CodexConfig{Model: "o4"}
	opts := adapter.SessionOpts{WorktreePath: "/tmp/work"}
	args := buildArgs(opts, cfg)

	// Must start with exec + --json.
	if len(args) < 2 || args[0] != "exec" || args[1] != jsonFlag {
		t.Fatalf("args must start with [exec, %s], got %v", jsonFlag, args)
	}
	for _, want := range []string{"--full-auto", "--cd", "/tmp/work", "-m", "o4"} {
		if !slices.Contains(args, want) {
			t.Fatalf("args missing %q: %v", want, args)
		}
	}
	// No legacy flags.
	for _, banned := range []string{"-w", "-q", "--approval-mode"} {
		if slices.Contains(args, banned) {
			t.Fatalf("args must not contain legacy flag %q: %v", banned, args)
		}
	}
	// No positional prompt argument (no bare non-flag non-path non-model token).
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") && arg != "exec" && arg != "/tmp/work" && arg != "o4" {
			t.Fatalf("args must not include a positional prompt arg; unexpected token %q: %v", arg, args)
		}
	}
}

// §6 — follow-up messaging: resume build args

func TestBuildArgs_Resume(t *testing.T) {
	cfg := config.CodexConfig{}
	opts := adapter.SessionOpts{
		WorktreePath: "/tmp/work",
		ResumeInfo:   map[string]string{"codex_thread_id": "tid-abc123"},
	}
	args := buildArgs(opts, cfg)

	resumeIdx := slices.Index(args, "resume")
	if resumeIdx < 0 {
		t.Fatalf("resume subcommand not found in args: %v", args)
	}
	if resumeIdx+1 >= len(args) || args[resumeIdx+1] != "tid-abc123" {
		t.Fatalf("thread ID not present after resume: %v", args)
	}
}

// §1 — config/readiness path: reasoning effort config

func TestBuildArgs_ReasoningEffort(t *testing.T) {
	tests := []struct {
		name   string
		effort string
		want   bool
	}{
		{name: "empty effort omitted", effort: "", want: false},
		{name: "high effort included", effort: "high", want: true},
		{name: "minimal effort included", effort: "minimal", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.CodexConfig{ReasoningEffort: tc.effort}
			opts := adapter.SessionOpts{WorktreePath: "/tmp/work"}
			args := buildArgs(opts, cfg)
			found := slices.ContainsFunc(args, func(s string) bool {
				return strings.HasPrefix(s, "model_reasoning_effort=")
			})
			if found != tc.want {
				t.Fatalf("reasoning effort presence = %v, want %v; args: %v", found, tc.want, args)
			}
			if tc.want {
				// Verify the -c flag precedes the key=value.
				cIdx := slices.Index(args, "-c")
				if cIdx < 0 {
					t.Fatal("missing -c flag")
				}
				if cIdx+1 >= len(args) || args[cIdx+1] != "model_reasoning_effort="+tc.effort {
					t.Fatalf("-c value = %q, want model_reasoning_effort=%s", args[cIdx+1], tc.effort)
				}
			}
		})
	}
}

// §3 — initial prompt: buildPrompt folds system + user

func TestBuildPrompt(t *testing.T) {
	tests := []struct {
		name   string
		system string
		user   string
		want   string
	}{
		{name: "both non-empty", system: "sys", user: "usr", want: "sys\n\nusr"},
		{name: "system only", system: "sys", user: "", want: "sys"},
		{name: "user only", system: "", user: "usr", want: "usr"},
		{name: "both empty", system: "", user: "", want: ""},
		{name: "system whitespace trimmed", system: "  sys  ", user: "", want: "sys"},
		{name: "user whitespace trimmed", system: "", user: "  usr  ", want: "usr"},
		{name: "both whitespace only", system: "  ", user: "  ", want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildPrompt(tc.system, tc.user)
			if got != tc.want {
				t.Fatalf("buildPrompt(%q, %q) = %q, want %q", tc.system, tc.user, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// StartSession: prompt delivered via stdin — §2 session ID, §3 initial prompt
// ---------------------------------------------------------------------------

func TestStartSession_PromptDeliveredViaStdin(t *testing.T) {
	binDir := t.TempDir()
	stdinCapture := filepath.Join(binDir, "stdin.txt")
	// Fake codex: reads the full stdin, emits JSONL events that echo the
	// first line, then exits.
	writeHarnessExecutable(t, binDir, "codex", fmt.Sprintf(`#!/bin/sh
if [ "$1" = "exec" ]; then
  PROMPT=$(cat)
  printf '%%s' "$PROMPT" > %q
  printf '{"type":"thread.started","thread_id":"tid-test"}\n'
  printf '{"type":"item.started","item":{"id":"i1","type":"agent_message","text":""}}\n'
  printf '{"type":"item.completed","item":{"id":"i1","type":"agent_message","text":"echo"}}\n'
  printf '{"type":"turn.completed","usage":{"input_tokens":10,"cached_input_tokens":0,"output_tokens":5}}\n'
  exit 0
fi
exit 1
`, stdinCapture))
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	h := NewHarness(config.CodexConfig{})
	logDir := t.TempDir()
	sess, err := h.StartSession(context.Background(), adapter.SessionOpts{
		SessionID:     "s1",
		WorktreePath:  t.TempDir(),
		SessionLogDir: logDir,
		SystemPrompt:  "Be helpful",
		UserPrompt:    "hello world",
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Abort(context.Background())

	collectEventsUntilDone(t, sess, 5*time.Second)

	raw, err := os.ReadFile(stdinCapture)
	if err != nil {
		t.Fatalf("read stdin capture: %v", err)
	}
	got := string(raw)
	// buildPrompt joins system + user with \n\n.
	if !strings.Contains(got, "Be helpful") {
		t.Fatalf("stdin = %q; want it to contain the system prompt", got)
	}
	if !strings.Contains(got, "hello world") {
		t.Fatalf("stdin = %q; want it to contain the user prompt", got)
	}
	entries, err := sessionlog.ReadFile(filepath.Join(logDir, "s1.log"))
	if err != nil {
		t.Fatalf("read session log: %v", err)
	}
	if len(entries) < 2 {
		t.Fatalf("entries = %+v, want synthetic input entries", entries)
	}
	if entries[0].InputKind != "session_context" || entries[0].Text != "Be helpful" {
		t.Fatalf("first entry = %+v, want session context", entries[0])
	}
	if entries[1].InputKind != "prompt" || entries[1].Text != "hello world" {
		t.Fatalf("second entry = %+v, want prompt", entries[1])
	}
}

func installFakeCodex(t *testing.T) string {
	t.Helper()

	fixturePath := filepath.Join("testdata", "fake-codex")
	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fake codex fixture: %v", err)
	}
	binDir := t.TempDir()
	writeHarnessExecutable(t, binDir, "codex", string(content))
	return filepath.Join(binDir, "codex")
}

func readTextFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func joinedPayloads(events []adapter.AgentEvent) string {
	var b strings.Builder
	for _, ev := range events {
		b.WriteString(ev.Payload)
	}
	return b.String()
}

func waitForEventType(t *testing.T, sess adapter.AgentSession, eventType string, timeout time.Duration) []adapter.AgentEvent {
	t.Helper()
	deadline := time.After(timeout)
	var events []adapter.AgentEvent
	for {
		select {
		case ev, ok := <-sess.Events():
			if !ok {
				t.Fatalf("session closed before %s; events=%#v", eventType, events)
			}
			events = append(events, ev)
			if ev.Type == eventType {
				return events
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s; events=%#v", eventType, events)
		}
	}
}

func TestStartSession_WithFakeCodexExecutable(t *testing.T) {
	binary := installFakeCodex(t)
	tmpDir := t.TempDir()
	stdinCapture := filepath.Join(tmpDir, "stdin.txt")
	argvCapture := filepath.Join(tmpDir, "argv.txt")
	t.Setenv("FAKE_CODEX_CAPTURE_STDIN", stdinCapture)
	t.Setenv("FAKE_CODEX_CAPTURE_ARGV", argvCapture)

	h := NewHarness(config.CodexConfig{BinaryPath: binary, Model: "o4", ReasoningEffort: "high"})
	logDir := filepath.Join(tmpDir, "sessions")
	worktree := filepath.Join(tmpDir, "worktree")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}

	sess, err := h.StartSession(context.Background(), adapter.SessionOpts{
		SessionID:     "s-fake-codex",
		WorktreePath:  worktree,
		SessionLogDir: logDir,
		SystemPrompt:  "Be deterministic",
		UserPrompt:    "first turn",
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Abort(context.Background())

	if got := sess.ID(); got != "s-fake-codex" {
		t.Fatalf("session ID = %q, want s-fake-codex", got)
	}
	events := collectEventsUntilDone(t, sess, 5*time.Second)
	if got := sess.ResumeInfo()["codex_thread_id"]; got != "tid-fixture" {
		t.Fatalf("codex_thread_id = %q, want tid-fixture", got)
	}
	if text := joinedPayloads(filterEvents(events, "text_delta")); text != "Hello from fake codex" {
		t.Fatalf("text_delta = %q, want fake assistant output", text)
	}
	if done := findEvent(events, "done"); done == nil {
		t.Fatal("missing done event")
	} else if got, _ := done.Metadata["input_tokens"].(int64); got != 11 {
		t.Fatalf("input_tokens = %v, want 11", got)
	}
	if cmd := findEventWhere(events, "tool_result", func(e adapter.AgentEvent) bool { return e.Payload == "printf fake" }); cmd == nil {
		t.Fatal("missing command tool_result from fake codex")
	}
	if mcp := findEventWhere(events, "tool_result", func(e adapter.AgentEvent) bool { return e.Payload == "fake.lookup" }); mcp == nil {
		t.Fatal("missing error mcp tool_result from fake codex")
	} else if got, _ := mcp.Metadata["error"].(string); got != "fake tool error" {
		t.Fatalf("mcp error = %q, want fake tool error", got)
	}

	stdin := readTextFile(t, stdinCapture)
	if !strings.Contains(stdin, "Be deterministic\n\nfirst turn") {
		t.Fatalf("stdin capture = %q, want folded prompt", stdin)
	}
	argv := readTextFile(t, argvCapture)
	for _, want := range []string{"exec", "--json", "--full-auto", "--cd " + worktree, "-m o4", "-c model_reasoning_effort=high"} {
		if !strings.Contains(argv, want) {
			t.Fatalf("argv capture = %q, missing %q", argv, want)
		}
	}
	logContent := readTextFile(t, filepath.Join(logDir, "s-fake-codex.log"))
	if !strings.Contains(logContent, "Hello from fake codex") {
		t.Fatalf("session log missing fake assistant output: %s", logContent)
	}

	if err := sess.SendMessage(context.Background(), "second turn"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	resumeEvents := collectEventsUntilDone(t, sess, 5*time.Second)
	if text := joinedPayloads(filterEvents(resumeEvents, "text_delta")); text != "Follow-up from fake codex" {
		t.Fatalf("resume text_delta = %q, want follow-up output", text)
	}
	argv = readTextFile(t, argvCapture)
	if !strings.Contains(argv, "resume tid-fixture") {
		t.Fatalf("argv capture = %q, want resume tid-fixture", argv)
	}
	stdin = readTextFile(t, stdinCapture)
	if !strings.Contains(stdin, "second turn") {
		t.Fatalf("stdin capture = %q, want follow-up prompt", stdin)
	}
}

func TestSessionControls_WithFakeCodexExecutable(t *testing.T) {
	binary := installFakeCodex(t)
	h := NewHarness(config.CodexConfig{BinaryPath: binary})
	sess, err := h.StartSession(context.Background(), adapter.SessionOpts{
		SessionID:    "s-fake-controls",
		WorktreePath: t.TempDir(),
		UserPrompt:   "test",
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Abort(context.Background())
	collectEventsUntilDone(t, sess, 5*time.Second)

	if err := sess.SendAnswer(context.Background(), "answer"); !errors.Is(err, adapter.ErrSendAnswerNotSupported) {
		t.Fatalf("SendAnswer error = %v, want ErrSendAnswerNotSupported", err)
	}
	if err := sess.Compact(context.Background()); !errors.Is(err, adapter.ErrCompactNotSupported) {
		t.Fatalf("Compact error = %v, want ErrCompactNotSupported", err)
	}
	if err := sess.Steer(context.Background(), "steer"); !errors.Is(err, adapter.ErrSteerNotSupported) {
		t.Fatalf("Steer error = %v, want ErrSteerNotSupported", err)
	}
}

func TestAbort_MidTurnWithFakeCodexExecutable(t *testing.T) {
	binary := installFakeCodex(t)
	t.Setenv("FAKE_CODEX_SLOW", "1")
	h := NewHarness(config.CodexConfig{BinaryPath: binary})
	sess, err := h.StartSession(context.Background(), adapter.SessionOpts{
		SessionID:    "s-fake-abort",
		WorktreePath: t.TempDir(),
		UserPrompt:   "slow",
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	events := waitForEventType(t, sess, "started", 5*time.Second)
	if len(events) == 0 {
		t.Fatal("missing started event before abort")
	}
	if err := sess.Abort(context.Background()); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("session Done did not close after abort")
	}
	if _, ok := <-sess.Events(); ok {
		t.Fatal("events channel should be closed after abort")
	}
}

func TestRunAction_CheckAuthWithFakeCodexExecutable(t *testing.T) {
	binary := installFakeCodex(t)
	h := NewHarness(config.CodexConfig{BinaryPath: binary})
	result, err := h.RunAction(context.Background(), adapter.HarnessActionRequest{Action: "check_auth"})
	if err != nil {
		t.Fatalf("RunAction check_auth: %v", err)
	}
	if !result.Success || result.Identity != binary {
		t.Fatalf("result = %+v, want success with fake binary identity", result)
	}
}

func TestStartSession_FakeCodexFailureModes(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  string
	}{
		{name: "malformed", env: "FAKE_CODEX_MALFORMED"},
		{name: "nonzero", env: "FAKE_CODEX_NONZERO"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binary := installFakeCodex(t)
			t.Setenv(tc.env, "1")
			h := NewHarness(config.CodexConfig{BinaryPath: binary})
			sess, err := h.StartSession(context.Background(), adapter.SessionOpts{
				SessionID:    "s-fake-" + tc.name,
				WorktreePath: t.TempDir(),
				UserPrompt:   "fail",
			})
			if err != nil {
				t.Fatalf("StartSession: %v", err)
			}
			events := collectEventsUntilDone(t, sess, 5*time.Second)
			if tc.name == "malformed" {
				if findEvent(events, "done") == nil {
					t.Fatalf("events = %#v, want done after malformed line is skipped", events)
				}
				return
			}
			waitErr := sess.Wait(context.Background())
			if waitErr == nil || !strings.Contains(waitErr.Error(), "fake codex failure") {
				t.Fatalf("Wait error = %v, want fake codex failure", waitErr)
			}
		})
	}
}

// §4,§5 — JSONL event mapping: text_delta and done

// ---------------------------------------------------------------------------
// JSONL event mapping — §4 assistant output, §5 terminal success, §9 edge cases, §10 logs
// ---------------------------------------------------------------------------

func TestReadStdout_EventMapping(t *testing.T) {
	binDir := t.TempDir()
	writeHarnessExecutable(t, binDir, "codex", `#!/bin/sh
if [ "$1" = "exec" ]; then
  printf '{"type":"thread.started","thread_id":"tid-xyz"}\n'
  printf '{"type":"turn.started"}\n'
  printf '{"type":"item.started","item":{"id":"m1","type":"agent_message","text":""}}\n'
  printf '{"type":"item.updated","item":{"id":"m1","type":"agent_message","text":"Hello"}}\n'
  printf '{"type":"item.completed","item":{"id":"m1","type":"agent_message","text":"Hello world"}}\n'
  printf '{"type":"item.started","item":{"id":"c1","type":"command_execution","command":"ls","aggregated_output":"","status":"in_progress"}}\n'
  printf '{"type":"item.completed","item":{"id":"c1","type":"command_execution","command":"ls","aggregated_output":"a.go","exit_code":0,"status":"completed"}}\n'
  printf '{"type":"item.completed","item":{"id":"f1","type":"file_change","changes":[{"path":"foo.go","kind":"update"}],"status":"completed"}}\n'
  printf '{"type":"turn.completed","usage":{"input_tokens":20,"cached_input_tokens":2,"output_tokens":10}}\n'
  exit 0
fi
exit 1
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	h := NewHarness(config.CodexConfig{})
	sess, err := h.StartSession(context.Background(), adapter.SessionOpts{
		SessionID:    "s2",
		WorktreePath: t.TempDir(),
		UserPrompt:   "go",
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Abort(context.Background())

	evts := collectEventsUntilDone(t, sess, 5*time.Second)

	// started event carries codex_thread_id.
	started := findEvent(evts, "started")
	if started == nil {
		t.Fatal("missing started event")
	}
	if got, _ := started.Metadata["codex_thread_id"].(string); got != "tid-xyz" {
		t.Fatalf("codex_thread_id = %q, want tid-xyz", got)
	}

	// text_delta events carry incremental text.
	deltas := filterEvents(evts, "text_delta")
	if len(deltas) == 0 {
		t.Fatal("no text_delta events emitted")
	}
	var allTextSB strings.Builder
	for _, d := range deltas {
		allTextSB.WriteString(d.Payload)
	}
	allText := allTextSB.String()
	if allText != "Hello world" {
		t.Fatalf("concatenated text_delta = %q, want %q", allText, "Hello world")
	}

	// tool_result for command_execution.
	cmdResult := findEventWhere(evts, "tool_result", func(e adapter.AgentEvent) bool {
		return e.Payload == "ls"
	})
	if cmdResult == nil {
		t.Fatal("missing tool_result for command execution")
	}
	if got, _ := cmdResult.Metadata["output"].(string); !strings.Contains(got, "a.go") {
		t.Fatalf("command output = %q, want a.go", got)
	}

	// tool_result for file_change.
	fileResult := findEventWhere(evts, "tool_result", func(e adapter.AgentEvent) bool {
		return e.Payload == "foo.go"
	})
	if fileResult == nil {
		t.Fatal("missing tool_result for file_change")
	}

	// done event carries usage metadata.
	done := findEvent(evts, "done")
	if done == nil {
		t.Fatal("missing done event")
	}
	if got, _ := done.Metadata["input_tokens"].(int64); got != 20 {
		t.Fatalf("input_tokens = %v, want 20", got)
	}
	if got, _ := done.Metadata["output_tokens"].(int64); got != 10 {
		t.Fatalf("output_tokens = %v, want 10", got)
	}
}

// §4 — assistant output: streaming text_delta

func TestReadStdout_StreamingDelta(t *testing.T) {
	binDir := t.TempDir()
	writeHarnessExecutable(t, binDir, "codex", `#!/bin/sh
if [ "$1" = "exec" ]; then
  printf '{"type":"thread.started","thread_id":"tid-delta"}\n'
  printf '{"type":"item.started","item":{"id":"d1","type":"agent_message","text":""}}\n'
  printf '{"type":"item.updated","item":{"id":"d1","type":"agent_message","text":"Hi"}}\n'
  printf '{"type":"item.updated","item":{"id":"d1","type":"agent_message","text":"Hi there"}}\n'
  printf '{"type":"item.updated","item":{"id":"d1","type":"agent_message","text":"Hi there!"}}\n'
  printf '{"type":"item.completed","item":{"id":"d1","type":"agent_message","text":"Hi there!"}}\n'
  printf '{"type":"turn.completed","usage":{"input_tokens":1,"cached_input_tokens":0,"output_tokens":3}}\n'
  exit 0
fi
exit 1
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	h := NewHarness(config.CodexConfig{})
	sess, err := h.StartSession(context.Background(), adapter.SessionOpts{
		SessionID:    "s3",
		WorktreePath: t.TempDir(),
		UserPrompt:   "hi",
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Abort(context.Background())

	evts := collectEventsUntilDone(t, sess, 5*time.Second)

	deltas := filterEvents(evts, "text_delta")
	if len(deltas) != 3 {
		t.Fatalf("got %d text_delta events, want 3; events: %v", len(deltas), summarizeEvents(evts))
	}
	wantDeltas := []string{"Hi", " there", "!"}
	for i, want := range wantDeltas {
		if deltas[i].Payload != want {
			t.Fatalf("text_delta[%d] = %q, want %q", i, deltas[i].Payload, want)
		}
	}
}

// §9 — malformed/non-zero: edge cases in event parsing

func TestEventMapping_EdgeCases(t *testing.T) {
	binDir := t.TempDir()
	writeHarnessExecutable(t, binDir, "codex", `#!/bin/sh
if [ "$1" = "exec" ]; then
  printf '{"type":"thread.started","thread_id":"tid-edge"}\n'
  # Malformed JSON — should be silently skipped.
  printf 'not valid json\n'
  # Unknown event type — should be silently skipped.
  printf '{"type":"some_future_event","data":42}\n'
  # turn.started — no-op, should not produce an event.
  printf '{"type":"turn.started"}\n'
  # mcp_tool_call with error.
  printf '{"type":"item.completed","item":{"id":"mc1","type":"mcp_tool_call","server":"github","tool":"search","status":"error","error":{"message":"rate limited"}}}\n'
  # web_search item.
  printf '{"type":"item.completed","item":{"id":"ws1","type":"web_search","query":"how to test go code"}}\n'
  # error item with ItemMessage.
  printf '{"type":"item.completed","item":{"id":"e1","type":"error","message":"permission denied","text":""}}\n'
  # top-level error event.
  printf '{"type":"error","message":"something went wrong"}\n'
  printf '{"type":"turn.completed","usage":{"input_tokens":1,"cached_input_tokens":0,"output_tokens":1}}\n'
  exit 0
fi
exit 1
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	h := NewHarness(config.CodexConfig{})
	sess, err := h.StartSession(context.Background(), adapter.SessionOpts{
		SessionID:    "s-edge",
		WorktreePath: t.TempDir(),
		UserPrompt:   "test",
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Abort(context.Background())

	evts := collectEventsUntilDone(t, sess, 5*time.Second)

	// Verify malformed JSON and unknown types didn't produce events.
	// We should see: started, mcp tool_result, web_search tool_result, error item, top-level error, done.

	// mcp_tool_call with error.
	mcpResult := findEventWhere(evts, "tool_result", func(e adapter.AgentEvent) bool {
		return e.Payload == "github.search"
	})
	if mcpResult == nil {
		t.Fatal("missing tool_result for mcp_tool_call")
	}
	if got, _ := mcpResult.Metadata["error"].(string); got != "rate limited" {
		t.Fatalf("mcp error = %q, want 'rate limited'", got)
	}

	// web_search.
	webResult := findEventWhere(evts, "tool_result", func(e adapter.AgentEvent) bool {
		return e.Payload == "how to test go code"
	})
	if webResult == nil {
		t.Fatal("missing tool_result for web_search")
	}

	// error item.
	errEvt := findEvent(evts, "error")
	if errEvt == nil {
		t.Fatal("missing error event")
	}

	// done event.
	if findEvent(evts, "done") == nil {
		t.Fatal("missing done event")
	}
}

// §10 — canonical log: session log records assistant output

func TestSessionLog_RecordsOutput(t *testing.T) {
	logDir := t.TempDir()
	binDir := t.TempDir()
	writeHarnessExecutable(t, binDir, "codex", `#!/bin/sh
if [ "$1" = "exec" ]; then
  printf '{"type":"thread.started","thread_id":"tid-log"}\n'
  printf '{"type":"item.completed","item":{"id":"m1","type":"agent_message","text":"hello from codex"}}\n'
  printf '{"type":"turn.completed","usage":{"input_tokens":1,"cached_input_tokens":0,"output_tokens":1}}\n'
  exit 0
fi
exit 1
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	h := NewHarness(config.CodexConfig{})
	sess, err := h.StartSession(context.Background(), adapter.SessionOpts{
		SessionID:     "s-log",
		WorktreePath:  t.TempDir(),
		UserPrompt:    "test",
		SessionLogDir: logDir,
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Abort(context.Background())

	collectEventsUntilDone(t, sess, 5*time.Second)

	logPath := filepath.Join(logDir, "s-log.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "thread.started") {
		t.Fatal("log missing thread.started event")
	}
	if !strings.Contains(content, "hello from codex") {
		t.Fatal("log missing item text")
	}
}

// ---------------------------------------------------------------------------
// Multi-turn: SendMessage via resume — §6 follow-up, §9 error guards
// ---------------------------------------------------------------------------

// §6 — follow-up messaging: SendMessage resumes a turn

func TestSendMessage_ResumesTurn(t *testing.T) {
	binDir := t.TempDir()
	counterFile := filepath.Join(binDir, "count.txt")
	// First invocation emits turn 1; second (resume) emits turn 2.
	writeHarnessExecutable(t, binDir, "codex", fmt.Sprintf(`#!/bin/sh
if [ "$1" = "exec" ]; then
  COUNT=0
  if [ -f %q ]; then COUNT=$(cat %q); fi
  COUNT=$((COUNT+1))
  printf '%%s' "$COUNT" > %q
  if [ "$COUNT" = "1" ]; then
    printf '{"type":"thread.started","thread_id":"tid-resume"}\n'
    printf '{"type":"item.completed","item":{"id":"t1","type":"agent_message","text":"turn one"}}\n'
    printf '{"type":"turn.completed","usage":{"input_tokens":1,"cached_input_tokens":0,"output_tokens":1}}\n'
  else
    printf '{"type":"item.completed","item":{"id":"t2","type":"agent_message","text":"turn two"}}\n'
    printf '{"type":"turn.completed","usage":{"input_tokens":1,"cached_input_tokens":0,"output_tokens":1}}\n'
  fi
  exit 0
fi
exit 1
`, counterFile, counterFile, counterFile))
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	h := NewHarness(config.CodexConfig{})
	sess, err := h.StartSession(context.Background(), adapter.SessionOpts{
		SessionID:    "s4",
		WorktreePath: t.TempDir(),
		UserPrompt:   "first prompt",
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Abort(context.Background())

	// Wait for the first turn to complete (done event).
	collectEventsUntilDone(t, sess, 5*time.Second)

	// Send the second message; the session should start a resume process.
	if err := sess.SendMessage(context.Background(), "second prompt"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	// Collect second turn events.
	evts2 := collectEventsUntilDone(t, sess, 5*time.Second)
	if findEvent(evts2, "done") == nil {
		t.Fatal("missing done event in second turn")
	}
	var textSB strings.Builder
	for _, e := range filterEvents(evts2, "text_delta") {
		textSB.WriteString(e.Payload)
	}
	text := textSB.String()
	if text != "turn two" {
		t.Fatalf("second turn text = %q, want %q", text, "turn two")
	}

	// Verify the binary was invoked twice.
	raw, _ := os.ReadFile(counterFile)
	if strings.TrimSpace(string(raw)) != "2" {
		t.Fatalf("expected 2 binary invocations, counter = %q", string(raw))
	}
}

// §10 — canonical log: multi-turn continuity in session log

func TestSessionLog_MultiTurnContinuity(t *testing.T) {
	logDir := t.TempDir()
	binDir := t.TempDir()
	counterFile := filepath.Join(binDir, "count-log.txt")
	writeHarnessExecutable(t, binDir, "codex", fmt.Sprintf(`#!/bin/sh
if [ "$1" = "exec" ]; then
  COUNT=0
  if [ -f %q ]; then COUNT=$(cat %q); fi
  COUNT=$((COUNT+1))
  printf '%%s' "$COUNT" > %q
  if [ "$COUNT" = "1" ]; then
    printf '{"type":"thread.started","thread_id":"tid-log-multi"}\n'
    printf '{"type":"item.completed","item":{"id":"t1","type":"agent_message","text":"turn one output"}}\n'
    printf '{"type":"turn.completed","usage":{"input_tokens":1,"cached_input_tokens":0,"output_tokens":1}}\n'
  else
    printf '{"type":"item.completed","item":{"id":"t2","type":"agent_message","text":"turn two output"}}\n'
    printf '{"type":"turn.completed","usage":{"input_tokens":1,"cached_input_tokens":0,"output_tokens":1}}\n'
  fi
  exit 0
fi
exit 1
`,
		counterFile, counterFile, counterFile))
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	h := NewHarness(config.CodexConfig{})
	sess, err := h.StartSession(context.Background(), adapter.SessionOpts{
		SessionID:     "s-log-multi",
		WorktreePath:  t.TempDir(),
		UserPrompt:    "first",
		SessionLogDir: logDir,
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Abort(context.Background())

	collectEventsUntilDone(t, sess, 5*time.Second)

	if err := sess.SendMessage(context.Background(), "second"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	collectEventsUntilDone(t, sess, 5*time.Second)

	logPath := filepath.Join(logDir, "s-log-multi.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "turn one output") {
		t.Fatal("log missing turn 1 output")
	}
	if !strings.Contains(content, "turn two output") {
		t.Fatal("log missing turn 2 output")
	}
}

// §9 — error: SendMessage rejected while turn is running

func TestSendMessage_RejectsWhileRunning(t *testing.T) {
	binDir := t.TempDir()
	// Fake codex that sleeps long enough for us to call SendMessage mid-turn.
	writeHarnessExecutable(t, binDir, "codex", `#!/bin/sh
if [ "$1" = "exec" ]; then
  printf '{"type":"thread.started","thread_id":"tid-slow"}\n'
  sleep 10
  printf '{"type":"turn.completed","usage":{"input_tokens":0,"cached_input_tokens":0,"output_tokens":0}}\n'
  exit 0
fi
exit 1
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	h := NewHarness(config.CodexConfig{})
	sess, err := h.StartSession(context.Background(), adapter.SessionOpts{
		SessionID:    "s5",
		WorktreePath: t.TempDir(),
		UserPrompt:   "slow",
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Abort(context.Background())

	// Drain until we see thread.started so the session is definitely running.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for thread.started")
		}
		for _, e := range drainAvailable(sess) {
			if e.Type == "started" {
				goto ready
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
ready:
	err = sess.SendMessage(context.Background(), "interrupt attempt")
	if err == nil {
		t.Fatal("SendMessage returned nil, want error while session is running")
	}
	if !strings.Contains(err.Error(), "in progress") {
		t.Fatalf("error = %q, want 'in progress' mention", err)
	}
}

// §8 — abort: clean turn does not hang

func TestAbort_AfterCleanTurn_DoesNotHang(t *testing.T) {
	binDir := t.TempDir()
	writeHarnessExecutable(t, binDir, "codex", `#!/bin/sh
if [ "$1" = "exec" ]; then
  printf '{"type":"thread.started","thread_id":"tid-abort"}\n'
  printf '{"type":"turn.completed","usage":{"input_tokens":1,"cached_input_tokens":0,"output_tokens":1}}\n'
  exit 0
fi
exit 1
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	h := NewHarness(config.CodexConfig{})
	sess, err := h.StartSession(context.Background(), adapter.SessionOpts{
		SessionID:    "s-abort",
		WorktreePath: t.TempDir(),
		UserPrompt:   "test",
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	collectEventsUntilDone(t, sess, 5*time.Second)

	// Abort after clean turn must return well under the 5 s kill timeout.
	start := time.Now()
	if err := sess.Abort(context.Background()); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Abort took %v after clean turn; expected < 1s (5s timeout indicates bug)", elapsed)
	}
}

// §9 — error: failed turn preserves error message

func TestWait_TurnFailed_PreservesMessage(t *testing.T) {
	binDir := t.TempDir()
	writeHarnessExecutable(t, binDir, "codex", `#!/bin/sh
if [ "$1" = "exec" ]; then
  printf '{"type":"thread.started","thread_id":"tid-fail"}\n'
  printf '{"type":"turn.failed","error":{"message":"rate limit exceeded"}}\n'
  exit 1
fi
exit 1
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	h := NewHarness(config.CodexConfig{})
	sess, err := h.StartSession(context.Background(), adapter.SessionOpts{
		SessionID:    "s-fail",
		WorktreePath: t.TempDir(),
		UserPrompt:   "test",
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	waitErr := sess.Wait(context.Background())
	if waitErr == nil {
		t.Fatal("Wait: expected error, got nil")
	}
	if !strings.Contains(waitErr.Error(), "rate limit exceeded") {
		t.Fatalf("Wait error = %q; want it to contain 'rate limit exceeded'", waitErr.Error())
	}
}

// ---------------------------------------------------------------------------
// Session lifecycle: Abort, Wait, SendMessage guards — §7 Steer, §8 abort
// ---------------------------------------------------------------------------

// §8 — abort: idempotent close

func TestAbort_Idempotent(t *testing.T) {
	binDir := t.TempDir()
	writeHarnessExecutable(t, binDir, "codex", `#!/bin/sh
if [ "$1" = "exec" ]; then
  printf '{"type":"thread.started","thread_id":"tid-idem"}
'
  printf '{"type":"turn.completed","usage":{"input_tokens":1,"cached_input_tokens":0,"output_tokens":1}}
'
  exit 0
fi
exit 1
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	h := NewHarness(config.CodexConfig{})
	sess, err := h.StartSession(context.Background(), adapter.SessionOpts{
		SessionID:    "s-idem",
		WorktreePath: t.TempDir(),
		UserPrompt:   "test",
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	collectEventsUntilDone(t, sess, 5*time.Second)

	if err := sess.Abort(context.Background()); err != nil {
		t.Fatalf("first Abort: %v", err)
	}
	if err := sess.Abort(context.Background()); err != nil {
		t.Fatalf("second Abort: %v", err)
	}
}

// §8 — abort: mid-turn terminates session and closes resources

func TestAbort_MidTurn_TerminatesSession(t *testing.T) {
	binDir := t.TempDir()
	writeHarnessExecutable(t, binDir, "codex", `#!/bin/sh
if [ "$1" = "exec" ]; then
  printf '{"type":"thread.started","thread_id":"tid-abort-mid"}
'
  sleep 10
  printf '{"type":"turn.completed","usage":{"input_tokens":0,"cached_input_tokens":0,"output_tokens":0}}
'
  exit 0
fi
exit 1
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	h := NewHarness(config.CodexConfig{})
	sess, err := h.StartSession(context.Background(), adapter.SessionOpts{
		SessionID:    "s-abort-mid",
		WorktreePath: t.TempDir(),
		UserPrompt:   "test",
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	// Wait for started event so we know the session is running.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for started event")
		}
		for _, e := range drainAvailable(sess) {
			if e.Type == "started" {
				goto ready
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
ready:
	sess.Abort(context.Background())

	waitErr := sess.Wait(context.Background())
	if waitErr != nil {
		t.Fatalf("Wait after Abort: %v", waitErr)
	}

	// Events channel should be closed.
	_, ok := <-sess.Events()
	if ok {
		t.Fatal("events channel should be closed after Wait")
	}
}

// §8 — abort: SendMessage rejected after abort

func TestSendMessage_RejectsAfterAbort(t *testing.T) {
	binDir := t.TempDir()
	writeHarnessExecutable(t, binDir, "codex", `#!/bin/sh
if [ "$1" = "exec" ]; then
  printf '{"type":"thread.started","thread_id":"tid-aborted"}
'
  printf '{"type":"turn.completed","usage":{"input_tokens":1,"cached_input_tokens":0,"output_tokens":1}}
'
  exit 0
fi
exit 1
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	h := NewHarness(config.CodexConfig{})
	sess, err := h.StartSession(context.Background(), adapter.SessionOpts{
		SessionID:    "s-aborted",
		WorktreePath: t.TempDir(),
		UserPrompt:   "test",
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	collectEventsUntilDone(t, sess, 5*time.Second)
	sess.Abort(context.Background())

	err = sess.SendMessage(context.Background(), "should fail")
	if err == nil {
		t.Fatal("SendMessage after Abort: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "aborted") {
		t.Fatalf("SendMessage error = %q, want 'aborted' mention", err.Error())
	}
}

// §8 — abort: Wait returns on context cancellation

func TestWait_ContextCancellation(t *testing.T) {
	binDir := t.TempDir()
	writeHarnessExecutable(t, binDir, "codex", `#!/bin/sh
if [ "$1" = "exec" ]; then
  printf '{"type":"thread.started","thread_id":"tid-ctx"}
'
  sleep 10
  exit 0
fi
exit 1
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	h := NewHarness(config.CodexConfig{})
	sess, err := h.StartSession(context.Background(), adapter.SessionOpts{
		SessionID:    "s-ctx",
		WorktreePath: t.TempDir(),
		UserPrompt:   "test",
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	// Wait for started so session is running.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for started event")
		}
		for _, e := range drainAvailable(sess) {
			if e.Type == "started" {
				goto ready
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
ready:

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	waitErr := sess.Wait(ctx)
	if waitErr == nil {
		t.Fatal("Wait with cancelled context: expected error, got nil")
	}
	if !errors.Is(waitErr, context.Canceled) {
		t.Fatalf("Wait error = %v, want context.Canceled", waitErr)
	}
}

// §7 — Steer: documented unsupported error

func TestSteer_NotSupported(t *testing.T) {
	binDir := t.TempDir()
	writeHarnessExecutable(t, binDir, "codex", `#!/bin/sh
if [ "$1" = "exec" ]; then
  printf '{"type":"thread.started","thread_id":"tid-steer"}
'
  printf '{"type":"turn.completed","usage":{"input_tokens":1,"cached_input_tokens":0,"output_tokens":1}}
'
  exit 0
fi
exit 1
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	h := NewHarness(config.CodexConfig{})
	sess, err := h.StartSession(context.Background(), adapter.SessionOpts{
		SessionID:    "s-steer",
		WorktreePath: t.TempDir(),
		UserPrompt:   "test",
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Abort(context.Background())

	err = sess.Steer(context.Background(), "change direction")
	if !errors.Is(err, adapter.ErrSteerNotSupported) {
		t.Fatalf("Steer error = %v, want ErrSteerNotSupported", err)
	}
}

// ---------------------------------------------------------------------------
// check_auth / RunAction — §9 readiness/startup failures
// ---------------------------------------------------------------------------

// §9 — readiness failure: check_auth rejects missing exec subcommand

func TestCheckAuth_FailsOnMissingExecSubcommand(t *testing.T) {
	binDir := t.TempDir()
	writeHarnessExecutable(t, binDir, "codex", `#!/bin/sh
if [ "$1" = "exec" ]; then
  echo "error: unrecognized subcommand 'exec'" >&2
  exit 1
fi
exit 0
`)
	t.Setenv("PATH", binDir)

	h := NewHarness(config.CodexConfig{})
	_, err := h.RunAction(context.Background(), adapter.HarnessActionRequest{Action: "check_auth"})
	if err == nil {
		t.Fatal("RunAction check_auth returned nil, want error for missing exec subcommand")
	}
	if !strings.Contains(err.Error(), "exec") {
		t.Fatalf("error = %q, want mention of 'exec' subcommand", err)
	}
}

// §9 — readiness failure: unsupported RunAction

func TestRunAction_UnsupportedAction(t *testing.T) {
	h := NewHarness(config.CodexConfig{})
	_, err := h.RunAction(context.Background(), adapter.HarnessActionRequest{Action: "do_magic"})
	if err == nil {
		t.Fatal("RunAction unsupported action: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported codex action") {
		t.Fatalf("error = %q, want 'unsupported codex action'", err.Error())
	}
}

// §9 — readiness failure: unsupported provider

func TestRunAction_UnsupportedProvider(t *testing.T) {
	h := NewHarness(config.CodexConfig{})
	_, err := h.RunAction(context.Background(), adapter.HarnessActionRequest{
		Action:   "login_provider",
		Provider: "gitlab",
	})
	if err == nil {
		t.Fatal("RunAction unsupported provider: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported provider") {
		t.Fatalf("error = %q, want 'unsupported provider'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Capabilities — §1 config/readiness path
// ---------------------------------------------------------------------------

// §1 — config/readiness path: harness name and capabilities

func TestHarnessNameAndCapabilities(t *testing.T) {
	h := NewHarness(config.CodexConfig{})
	if got := h.Name(); got != "codex" {
		t.Fatalf("Name() = %q, want codex", got)
	}
	caps := h.Capabilities()
	if !caps.SupportsStreaming {
		t.Fatal("SupportsStreaming = false, want true")
	}
	if !caps.SupportsMessaging {
		t.Fatal("SupportsMessaging = false, want true")
	}
	if !caps.SupportsNativeResume {
		t.Fatal("SupportsNativeResume = false, want true")
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// collectEventsUntilDone drains events until the channel is closed or a "done"
// event is seen, whichever comes first, with a deadline.
func collectEventsUntilDone(t *testing.T, sess adapter.AgentSession, timeout time.Duration) []adapter.AgentEvent {
	t.Helper()
	deadline := time.After(timeout)
	var evts []adapter.AgentEvent
	for {
		select {
		case e, ok := <-sess.Events():
			if !ok {
				return evts
			}
			evts = append(evts, e)
			if e.Type == "done" {
				return evts
			}
		case <-deadline:
			t.Fatalf("timed out waiting for done event; events so far: %v", summarizeEvents(evts))
		}
	}
}

// drainAvailable drains all currently-buffered events without blocking.
func drainAvailable(sess adapter.AgentSession) []adapter.AgentEvent {
	var evts []adapter.AgentEvent
	for {
		select {
		case e, ok := <-sess.Events():
			if !ok {
				return evts
			}
			evts = append(evts, e)
		default:
			return evts
		}
	}
}

func findEvent(evts []adapter.AgentEvent, typ string) *adapter.AgentEvent {
	for i := range evts {
		if evts[i].Type == typ {
			return &evts[i]
		}
	}
	return nil
}

func findEventWhere(evts []adapter.AgentEvent, typ string, pred func(adapter.AgentEvent) bool) *adapter.AgentEvent {
	for i := range evts {
		if evts[i].Type == typ && pred(evts[i]) {
			return &evts[i]
		}
	}
	return nil
}

func filterEvents(evts []adapter.AgentEvent, typ string) []adapter.AgentEvent {
	var out []adapter.AgentEvent
	for _, e := range evts {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

func summarizeEvents(evts []adapter.AgentEvent) []string {
	out := make([]string, len(evts))
	for i, e := range evts {
		out[i] = fmt.Sprintf("{%s %q}", e.Type, e.Payload)
	}
	return out
}
