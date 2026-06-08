package sessionlog

import (
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
