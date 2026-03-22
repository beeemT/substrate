package sessionlog

import "testing"

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
