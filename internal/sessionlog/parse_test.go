package sessionlog

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLine_SessionMetaDropped(t *testing.T) {
	// session_meta lines are internal harness metadata and must be dropped.
	lines := []string{
		`2026-03-22T16:11:01.533836Z {"type":"session_meta","omp_session_id":"149ce16df8db4c92","omp_session_file":"/some/path.jsonl"}`,
		`{"type":"session_meta","omp_session_id":"abc","omp_session_file":"/x.jsonl"}`,
	}
	for _, line := range lines {
		entry, ok := ParseLine(line)
		if ok {
			t.Errorf("ParseLine(%q) returned ok=true, want false (entry: %+v)", line, entry)
		}
	}
}

func TestParseLine_EventParsed(t *testing.T) {
	line := `2026-03-22T16:11:02Z {"type":"event","event":{"type":"assistant_output","text":"hello"}}`
	entry, ok := ParseLine(line)
	if !ok {
		t.Fatalf("ParseLine returned ok=false for valid event line")
	}
	if entry.Kind != KindAssistant {
		t.Errorf("entry.Kind = %q, want %q", entry.Kind, KindAssistant)
	}
	if entry.Text != "hello" {
		t.Errorf("entry.Text = %q, want %q", entry.Text, "hello")
	}
}

func TestParseLine_SessionContextInput(t *testing.T) {
	line := `{"type":"event","event":{"type":"input","input_kind":"session_context","text":"full context"}}`
	entry, ok := ParseLine(line)
	if !ok {
		t.Fatal("ParseLine returned ok=false for session context input")
	}
	if entry.Kind != KindInput {
		t.Fatalf("entry.Kind = %q, want %q", entry.Kind, KindInput)
	}
	if entry.InputKind != "session_context" {
		t.Fatalf("entry.InputKind = %q, want session_context", entry.InputKind)
	}
	if entry.Text != "full context" {
		t.Fatalf("entry.Text = %q, want full context", entry.Text)
	}
}

func TestScanEntriesSuppressesMatchingACPHistoryEcho(t *testing.T) {
	log := strings.Join([]string{
		`{"type":"event","event":{"type":"input","input_kind":"session_context","text":"ctx"}}`,
		`{"type":"event","event":{"type":"input","input_kind":"prompt","text":"begin"}}`,
		`2026-06-01T12:09:45Z in {"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"ctx\n\nbegin"}}}}`,
		`2026-06-01T12:09:46Z in {"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"remote history"}}}}`,
	}, "\n")
	entries, err := ScanEntries(strings.NewReader(log))
	if err != nil {
		t.Fatalf("ScanEntries: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3: %+v", len(entries), entries)
	}
	if entries[0].InputKind != "session_context" || entries[1].InputKind != "prompt" {
		t.Fatalf("synthetic inputs not preserved: %+v", entries)
	}
	if entries[2].InputKind != "history" || entries[2].Text != "remote history" {
		t.Fatalf("unrelated history not preserved: %+v", entries[2])
	}
}

func TestParseLine_ToolStart(t *testing.T) {
	line := `{"type":"event","event":{"type":"tool_start","tool":"bash","text":"{\"command\":\"ls\"}","intent":"Listing files"}}`
	entry, ok := ParseLine(line)
	if !ok {
		t.Fatalf("ParseLine returned ok=false for tool_start")
	}
	if entry.Kind != KindToolStart {
		t.Errorf("entry.Kind = %q, want %q", entry.Kind, KindToolStart)
	}
	if entry.Tool != "bash" {
		t.Errorf("entry.Tool = %q, want %q", entry.Tool, "bash")
	}
	if entry.Intent != "Listing files" {
		t.Errorf("entry.Intent = %q, want %q", entry.Intent, "Listing files")
	}
}

func TestParseLine_LifecycleStarted(t *testing.T) {
	line := `{"type":"event","event":{"type":"lifecycle","stage":"started","message":"Session started"}}`
	entry, ok := ParseLine(line)
	if !ok {
		t.Fatalf("ParseLine returned ok=false for lifecycle")
	}
	if entry.Kind != KindLifecycle {
		t.Errorf("entry.Kind = %q, want %q", entry.Kind, KindLifecycle)
	}
	if entry.Stage != "started" {
		t.Errorf("entry.Stage = %q, want %q", entry.Stage, "started")
	}
}

func TestParseLine_PlainText(t *testing.T) {
	line := "some plain text without JSON"
	entry, ok := ParseLine(line)
	if !ok {
		t.Fatalf("ParseLine returned ok=false for plain text")
	}
	if entry.Kind != KindPlain {
		t.Errorf("entry.Kind = %q, want %q", entry.Kind, KindPlain)
	}
	if entry.Text != line {
		t.Errorf("entry.Text = %q, want %q", entry.Text, line)
	}
}

func TestParseLine_MalformedJSON(t *testing.T) {
	line := `2026-03-22T00:00:00Z {invalid json`
	entry, ok := ParseLine(line)
	if !ok {
		t.Fatalf("ParseLine returned ok=false for malformed JSON")
	}
	if entry.Kind != KindPlain {
		t.Errorf("entry.Kind = %q, want %q", entry.Kind, KindPlain)
	}
}

func TestParseLine_EmptyLine(t *testing.T) {
	_, ok := ParseLine("")
	if ok {
		t.Error("ParseLine(\"\") returned ok=true, want false")
	}
	_, ok = ParseLine("   ")
	if ok {
		t.Error("ParseLine(\"   \") returned ok=true, want false")
	}
}

func TestParseLine_UnknownNonEventTypeDropped(t *testing.T) {
	// Any JSON with a recognized structure but type != "event" should be dropped.
	line := `{"type":"internal_something","data":"value"}`
	_, ok := ParseLine(line)
	if ok {
		t.Errorf("ParseLine(%q) returned ok=true, want false for non-event JSON type", line)
	}
}

func TestParseLine_EmptyEventKindBecomesPlain(t *testing.T) {
	// An event with an empty event.type should be treated as plain.
	line := `{"type":"event","event":{"type":"","text":"orphaned"}}`
	entry, ok := ParseLine(line)
	if !ok {
		t.Fatalf("ParseLine returned ok=false")
	}
	// Empty kind falls back to KindPlain per the existing logic.
	if entry.Kind != KindPlain {
		t.Errorf("entry.Kind = %q, want %q", entry.Kind, KindPlain)
	}
}

func TestParseLine_ACPAgentMessageChunk(t *testing.T) {
	line := `2026-06-01T12:09:45.518652+02:00 in {"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":" understand"}}}}`
	entry, ok := ParseLine(line)
	if !ok {
		t.Fatal("ParseLine returned ok=false for ACP agent chunk")
	}
	if entry.Kind != KindAssistant || entry.Text != " understand" {
		t.Fatalf("entry = %+v, want assistant chunk", entry)
	}
}

func TestParseLine_ACPToolCallRawInput(t *testing.T) {
	line := `2026-06-01T12:09:48.282706+02:00 in {"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"tool_call","toolCallId":"tc1","title":"read","kind":"read","rawInput":{"operations":[{"mode":"Line","path":"/tmp/plan-draft.md","limit":10}]}}}}`
	entry, ok := ParseLine(line)
	if !ok {
		t.Fatal("ParseLine returned ok=false for ACP rawInput")
	}
	if entry.Kind != KindToolStart {
		t.Fatalf("entry.Kind = %q, want %q", entry.Kind, KindToolStart)
	}
	if entry.Tool != "read" {
		t.Fatalf("entry.Tool = %q, want read", entry.Tool)
	}
	if entry.Intent != "read" {
		t.Fatalf("entry.Intent = %q, want read", entry.Intent)
	}
	if entry.Text == "" || entry.Text[0] != '{' {
		t.Fatalf("entry.Text = %q, want raw JSON args", entry.Text)
	}
}

func TestParseLine_ACPToolCallTitleFallback(t *testing.T) {
	line := `2026-06-01T12:09:48.282706+02:00 in {"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"tool_call","toolCallId":"tc1","title":"ask_foreman","rawInput":{"question":"Proceed?"}}}}`
	entry, ok := ParseLine(line)
	if !ok {
		t.Fatal("ParseLine returned ok=false for ACP title-only tool")
	}
	if entry.Tool != "ask_foreman" {
		t.Fatalf("entry.Tool = %q, want ask_foreman", entry.Tool)
	}
}

func TestParseLine_ACPToolCallRawOutput(t *testing.T) {
	line := `2026-06-01T12:09:48.282706+02:00 in {"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"tool_call_update","toolCallId":"tc1","kind":"read","status":"completed","rawOutput":{"items":[{"Text":"User id: 502\n-rw-r--r-- file.yaml"}]}}}}`
	entry, ok := ParseLine(line)
	if !ok {
		t.Fatal("ParseLine returned ok=false for ACP rawOutput")
	}
	if entry.Kind != KindToolResult {
		t.Fatalf("entry.Kind = %q, want %q", entry.Kind, KindToolResult)
	}
	if entry.Tool != "read" {
		t.Fatalf("entry.Tool = %q, want read", entry.Tool)
	}
	if entry.Text != "User id: 502\n-rw-r--r-- file.yaml" {
		t.Fatalf("entry.Text = %q", entry.Text)
	}
}

func TestParseLine_ACPTodoToolNameMatchesResult(t *testing.T) {
	startLine := `2026-06-01T12:09:48.282706+02:00 in {"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"tool_call","toolCallId":"tc1","title":"Creating task list: Implement chart","rawInput":{"command":"create","task_list_description":"Implement chart","tasks":[{"task_description":"Create chart"}]}}}}`
	start, ok := ParseLine(startLine)
	if !ok {
		t.Fatal("ParseLine returned ok=false for ACP todo tool_call")
	}
	if start.Kind != KindToolStart {
		t.Fatalf("start.Kind = %q, want %q", start.Kind, KindToolStart)
	}
	if start.Tool != "todo_list" {
		t.Fatalf("start.Tool = %q, want todo_list", start.Tool)
	}
	if start.Intent != "Creating task list: Implement chart" {
		t.Fatalf("start.Intent = %q", start.Intent)
	}
	if !strings.Contains(start.Text, `"command":"create"`) {
		t.Fatalf("start.Text = %q, want raw todo args", start.Text)
	}

	resultLine := `2026-06-01T12:09:49.282706+02:00 in {"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"tool_call_update","toolCallId":"tc1","title":"Creating task list: Implement chart","kind":"other","status":"completed","rawInput":{"command":"create","task_list_description":"Implement chart","tasks":[{"task_description":"Create chart"}]},"rawOutput":{"items":[{"Json":{"tasks":[{"id":"1","task_description":"Create chart","completed":false}],"description":"Implement chart","context":[],"modified_files":[]}}]}}}}`
	result, ok := ParseLine(resultLine)
	if !ok {
		t.Fatal("ParseLine returned ok=false for ACP todo tool_call_update")
	}
	if result.Kind != KindToolResult {
		t.Fatalf("result.Kind = %q, want %q", result.Kind, KindToolResult)
	}
	if result.Tool != "todo_list" {
		t.Fatalf("result.Tool = %q, want todo_list", result.Tool)
	}
	if result.Text != "" {
		t.Fatalf("result.Text = %q, want todo state payload suppressed", result.Text)
	}
}

func TestParseLine_ACPControlFramesDropped(t *testing.T) {
	lines := []string{
		`2026-06-01T12:09:45.851972+02:00 in {"jsonrpc":"2.0","method":"_kiro.dev/session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"tool_call_chunk","toolCallId":"tc1","title":"read","kind":"read"}}}`,
		`2026-06-01T12:09:45.851972+02:00 out {"jsonrpc":"2.0","id":1,"method":"session/prompt","params":{"sessionId":"s1"}}`,
	}
	for _, line := range lines {
		if entry, ok := ParseLine(line); ok {
			t.Fatalf("ParseLine(%q) = %+v, true; want dropped", line, entry)
		}
	}
}

func TestLoadInteractionEntries_NoActiveLogOffsetZero(t *testing.T) {
	// When the active log is absent (e.g. session not yet started), the
	// returned offset must be 0 so a tail that reconnects with that offset
	// can read the first byte of the active log as soon as it appears.
	dir := t.TempDir()
	entries, nextOffset, err := LoadInteractionEntries(dir, "agent-1", false)
	if err != nil {
		t.Fatalf("LoadInteractionEntries() error = %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("entries = %+v, want empty", entries)
	}
	if nextOffset != 0 {
		t.Errorf("nextOffset = %d, want 0", nextOffset)
	}
}

func TestLoadInteractionEntries_ActiveLogExistsOffsetConsumed(t *testing.T) {
	// When the active log exists with content, the offset reports consumed
	// bytes; a follow-up tail can read only newly appended bytes.
	dir := t.TempDir()
	active := filepath.Join(dir, "agent-1.log")
	body := `{"type":"event","event":{"type":"assistant_output","text":"alpha"}}` + "\n"
	if err := os.WriteFile(active, []byte(body), 0o600); err != nil {
		t.Fatalf("write active log: %v", err)
	}
	entries, nextOffset, err := LoadInteractionEntries(dir, "agent-1", false)
	if err != nil {
		t.Fatalf("LoadInteractionEntries() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %+v, want 1 entry", entries)
	}
	if entries[0].Text != "alpha" {
		t.Errorf("entries[0].Text = %q, want alpha", entries[0].Text)
	}
	if nextOffset != int64(len(body)) {
		t.Errorf("nextOffset = %d, want %d", nextOffset, len(body))
	}
}

func TestLoadInteractionEntries_NoActiveLog_DoesNotPanicOnSubsequentTail(t *testing.T) {
	// Regression for the dropped-first-byte bug: after LoadInteractionEntries
	// returns nextOffset=0 with no active log, a subsequent tail that reads
	// from offset 0 must surface the active log's first line once the file
	// is created. This mirrors the daemon TailAgentSessionLog path.
	dir := t.TempDir()
	active := filepath.Join(dir, "agent-1.log")
	_, nextOffset, err := LoadInteractionEntries(dir, "agent-1", false)
	if err != nil {
		t.Fatalf("initial LoadInteractionEntries() error = %v", err)
	}
	if nextOffset != 0 {
		t.Fatalf("initial nextOffset = %d, want 0", nextOffset)
	}
	body := `{"type":"event","event":{"type":"assistant_output","text":"alpha"}}` + "\n"
	if err := os.WriteFile(active, []byte(body), 0o600); err != nil {
		t.Fatalf("create active log: %v", err)
	}
	entries, nextOffset, err := LoadInteractionEntries(dir, "agent-1", false)
	if err != nil {
		t.Fatalf("post-create LoadInteractionEntries() error = %v", err)
	}
	if len(entries) != 1 || !strings.Contains(entries[0].Text, "alpha") {
		t.Errorf("entries = %+v, want one alpha entry", entries)
	}
	if nextOffset != int64(len(body)) {
		t.Errorf("nextOffset = %d, want %d (first byte must not be dropped)", nextOffset, len(body))
	}
}

func TestLoadInteractionEntries_CorruptGzipReturnsError(t *testing.T) {
	// A corrupt (truncated) gzip archive must surface as an error rather
	// than being silently dropped, so rotation/transport damage is
	// observable to callers instead of being swallowed. The previous
	// "transient" classification was both unsafe (masked real corruption)
	// and unprovable from the read result alone, so it has been removed.
	dir := t.TempDir()
	corrupt := filepath.Join(dir, "agent-1.log.1.gz")
	// Truncate a valid gzip stream mid-record: the footer is missing so
	// the reader hits an unexpected EOF, which previously caused the
	// segment to be skipped.
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	body := `{"type":"event","event":{"type":"assistant_output","text":"alpha"}}` + "\n"
	if _, err := gw.Write([]byte(body)); err != nil {
		t.Fatalf("seed gzip write: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("seed gzip close: %v", err)
	}
	// Drop the trailing checksum bytes to corrupt the stream without
	// invalidating the gzip header.
	full := buf.Bytes()
	if len(full) < 8 {
		t.Fatalf("seed gzip too short: %d bytes", len(full))
	}
	if err := os.WriteFile(corrupt, full[:len(full)-4], 0o600); err != nil {
		t.Fatalf("write corrupt gzip: %v", err)
	}

	entries, nextOffset, err := LoadInteractionEntries(dir, "agent-1", false)
	if err == nil {
		t.Fatalf("LoadInteractionEntries() error = nil, entries = %+v, nextOffset = %d; want error for corrupt gzip", entries, nextOffset)
	}
	if !strings.Contains(err.Error(), corrupt) {
		t.Errorf("error %q does not name the corrupt archive %q", err.Error(), corrupt)
	}
	if nextOffset != 0 {
		t.Errorf("nextOffset = %d, want 0 on read failure", nextOffset)
	}
}
