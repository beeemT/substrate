package views_test

import (
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beeemT/substrate/internal/sessionlog"
	"github.com/beeemT/substrate/internal/tui/views"
)

// TestTailSessionLogCmd_Basic verifies that reading a freshly-written file from
// offset 0 returns all lines and advances NextOffset to the file size.
func TestTailSessionLogCmd_Basic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "sess1.log")
	content := "alpha\nbeta\ngamma\n"
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := views.TailSessionLogCmd(logPath, "sess1", 0)()
	got, ok := msg.(views.SessionLogLinesMsg)
	if !ok {
		t.Fatalf("expected SessionLogLinesMsg, got %T", msg)
	}
	if got.SessionID != "sess1" {
		t.Errorf("SessionID: want %q, got %q", "sess1", got.SessionID)
	}
	want := []string{"alpha", "beta", "gamma"}
	if len(got.Entries) != len(want) {
		t.Fatalf("Entries: want %v, got %v", want, got.Entries)
	}
	for i, w := range want {
		if got.Entries[i].Text != w {
			t.Errorf("Entries[%d]: want %q, got %q", i, w, got.Entries[i].Text)
		}
	}
	if got.NextOffset != int64(len(content)) {
		t.Errorf("NextOffset: want %d, got %d", len(content), got.NextOffset)
	}
}

// TestTailSessionLogCmd_OffsetContinuation verifies that supplying a non-zero
// since offset causes only the bytes after that offset to be returned.
func TestTailSessionLogCmd_OffsetContinuation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f, err := os.CreateTemp(dir, "session-*.log")
	if err != nil {
		t.Fatal(err)
	}
	firstLine := "first\n"
	if _, err := f.WriteString(firstLine); err != nil {
		t.Fatal(err)
	}
	// Record the offset after the first line.
	offset := int64(len(firstLine))

	secondLine := "second\n"
	if _, err := f.WriteString(secondLine); err != nil {
		t.Fatal(err)
	}
	f.Close()

	msg := views.TailSessionLogCmd(f.Name(), "s", offset)()
	got, ok := msg.(views.SessionLogLinesMsg)
	if !ok {
		t.Fatalf("expected SessionLogLinesMsg, got %T", msg)
	}
	if len(got.Entries) != 1 || got.Entries[0].Text != "second" {
		t.Errorf("Entries: want [\"second\"], got %v", got.Entries)
	}
	wantOff := offset + int64(len(secondLine))
	if got.NextOffset != wantOff {
		t.Errorf("NextOffset: want %d, got %d", wantOff, got.NextOffset)
	}
}

// TestTailSessionLogCmd_RotationDetected verifies that when the file is smaller
// than the stored offset (rotation), scanning restarts from byte 0.
func TestTailSessionLogCmd_RotationDetected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := dir + "/rotated.log"

	// Simulate pre-rotation: old file had 1000 bytes.
	staleOffset := int64(1000)

	// New file (post-rotation) is much smaller.
	newContent := "fresh line after rotation\n"
	if err := os.WriteFile(path, []byte(newContent), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := views.TailSessionLogCmd(path, "r", staleOffset)()
	got, ok := msg.(views.SessionLogLinesMsg)
	if !ok {
		t.Fatalf("expected SessionLogLinesMsg, got %T", msg)
	}
	if len(got.Entries) != 1 || got.Entries[0].Text != "fresh line after rotation" {
		t.Errorf("Entries: want rotation-fresh line, got %v", got.Entries)
	}
	if got.NextOffset != int64(len(newContent)) {
		t.Errorf("NextOffset after rotation: want %d, got %d", len(newContent), got.NextOffset)
	}
}

// TestTailSessionLogCmd_LargeLine verifies that lines larger than the old
// 64 KiB scanner default (now 1 MiB) are returned correctly.
func TestTailSessionLogCmd_LargeLine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "big.log")
	// 100 KiB line — would have failed with the default bufio.Scanner buffer.
	bigPayload := strings.Repeat("x", 100*1024)
	content := bigPayload + "\n"
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := views.TailSessionLogCmd(logPath, "big", 0)()
	got, ok := msg.(views.SessionLogLinesMsg)
	if !ok {
		t.Fatalf("expected SessionLogLinesMsg, got %T", msg)
	}
	if len(got.Entries) != 1 {
		t.Fatalf("Entries: want 1 entry, got %d", len(got.Entries))
	}
	if got.Entries[0].Text != bigPayload {
		t.Errorf("Entries[0]: length %d, want %d", len(got.Entries[0].Text), len(bigPayload))
	}
	if got.NextOffset != int64(len(content)) {
		t.Errorf("NextOffset: want %d, got %d", len(content), got.NextOffset)
	}
}

// TestTailSessionLogCmd_MissingFile verifies that a missing log file returns a
// no-op SessionLogLinesMsg (not an ErrMsg) so the tail loop stays alive.
func TestTailSessionLogCmd_MissingFile(t *testing.T) {
	t.Parallel()
	msg := views.TailSessionLogCmd("/nonexistent/path/session.log", "x", 0)()
	got, ok := msg.(views.SessionLogLinesMsg)
	if !ok {
		t.Fatalf("expected SessionLogLinesMsg on missing file, got %T", msg)
	}
	if len(got.Entries) != 0 {
		t.Errorf("Entries: want empty slice, got %v", got.Entries)
	}
}

func TestTailSessionLogCmd_NormalizesEventJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "json.log")
	content := strings.Join([]string{
		`{"type":"event","event":{"type":"input","input_kind":"prompt","text":"Begin planning"}}`,
		`{"type":"event","event":{"type":"assistant_output","text":"planning step"}}`,
		`{"type":"event","event":{"type":"tool_start","tool":"read","text":"{\"path\":\"AGENTS.md\"}","intent":"Reading guidance"}}`,
		`{"type":"event","event":{"type":"tool_output","tool":"read","text":"AGENTS contents"}}`,
		`{"type":"event","event":{"type":"tool_result","tool":"read","text":"done","is_error":false}}`,
		`{"type":"event","event":{"type":"question","question":"Need input","context":"missing token"}}`,
		"plain fallback line",
		"",
	}, "\n")
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := views.TailSessionLogCmd(logPath, "json", 0)()
	got, ok := msg.(views.SessionLogLinesMsg)
	if !ok {
		t.Fatalf("expected SessionLogLinesMsg, got %T", msg)
	}
	type wantEntry struct {
		kind      sessionlog.EntryKind
		inputKind string
		text      string
		tool      string
		intent    string
		question  string
		ctx       string
	}
	want := []wantEntry{
		{kind: sessionlog.KindInput, inputKind: "prompt", text: "Begin planning"},
		{kind: sessionlog.KindAssistant, text: "planning step"},
		{kind: sessionlog.KindToolStart, tool: "read", text: `{"path":"AGENTS.md"}`, intent: "Reading guidance"},
		{kind: sessionlog.KindToolOutput, tool: "read", text: "AGENTS contents"},
		{kind: sessionlog.KindToolResult, tool: "read", text: "done"},
		{kind: sessionlog.KindQuestion, question: "Need input", ctx: "missing token"},
		{kind: sessionlog.KindPlain, text: "plain fallback line"},
	}
	if len(got.Entries) != len(want) {
		t.Fatalf("Entries: want %d, got %d", len(want), len(got.Entries))
	}
	for i, w := range want {
		e := got.Entries[i]
		if e.Kind != w.kind {
			t.Errorf("Entries[%d].Kind: want %q, got %q", i, w.kind, e.Kind)
		}
		if e.Text != w.text {
			t.Errorf("Entries[%d].Text: want %q, got %q", i, w.text, e.Text)
		}
		if w.inputKind != "" && e.InputKind != w.inputKind {
			t.Errorf("Entries[%d].InputKind: want %q, got %q", i, w.inputKind, e.InputKind)
		}
		if w.tool != "" && e.Tool != w.tool {
			t.Errorf("Entries[%d].Tool: want %q, got %q", i, w.tool, e.Tool)
		}
		if w.intent != "" && e.Intent != w.intent {
			t.Errorf("Entries[%d].Intent: want %q, got %q", i, w.intent, e.Intent)
		}
		if w.question != "" && e.Question != w.question {
			t.Errorf("Entries[%d].Question: want %q, got %q", i, w.question, e.Question)
		}
		if w.ctx != "" && e.Context != w.ctx {
			t.Errorf("Entries[%d].Context: want %q, got %q", i, w.ctx, e.Context)
		}
	}
}

func TestTailSessionLogCmd_PreservesLegacyErrorAndCompleteEvents(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "legacy.log")
	content := strings.Join([]string{
		`{"type":"event","event":{"type":"error","message":"bridge crashed"}}`,
		`{"type":"event","event":{"type":"complete","summary":"Legacy completion summary"}}`,
		`{"type":"event","event":{"type":"complete"}}`,
		"",
	}, "\n")
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := views.TailSessionLogCmd(logPath, "legacy", 0)()
	got, ok := msg.(views.SessionLogLinesMsg)
	if !ok {
		t.Fatalf("expected SessionLogLinesMsg, got %T", msg)
	}
	if len(got.Entries) != 3 {
		t.Fatalf("Entries: want 3, got %d", len(got.Entries))
	}
	// "error" kind — legacy event
	if got.Entries[0].Kind != sessionlog.EntryKind("error") {
		t.Errorf("Entries[0].Kind: want %q, got %q", "error", got.Entries[0].Kind)
	}
	if got.Entries[0].Message != "bridge crashed" {
		t.Errorf("Entries[0].Message: want %q, got %q", "bridge crashed", got.Entries[0].Message)
	}
	// "complete" kind with summary — legacy event
	if got.Entries[1].Kind != sessionlog.EntryKind("complete") {
		t.Errorf("Entries[1].Kind: want %q, got %q", "complete", got.Entries[1].Kind)
	}
	if got.Entries[1].Summary != "Legacy completion summary" {
		t.Errorf("Entries[1].Summary: want %q, got %q", "Legacy completion summary", got.Entries[1].Summary)
	}
	// "complete" kind, empty summary — legacy event
	if got.Entries[2].Kind != sessionlog.EntryKind("complete") {
		t.Errorf("Entries[2].Kind: want %q, got %q", "complete", got.Entries[2].Kind)
	}
}

// TestTailSessionLogCmd_LoadsArchivedContentOnFirstCall verifies that when
// since==0, TailSessionLogCmd reads gzipped archives in addition to the active
// log so that sessions whose log has been rotated show their full history.
func TestTailSessionLogCmd_LoadsArchivedContentOnFirstCall(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const sessionID = "tail-archive"

	// Write two gzipped archive segments (sorted by name so older comes first).
	writeGZ := func(name, line string) {
		t.Helper()
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		gw := gzip.NewWriter(f)
		if _, err := gw.Write([]byte(line + "\n")); err != nil {
			t.Fatal(err)
		}
		if err := gw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	writeGZ(sessionID+".log.1000000000.gz", "archived line 1")
	writeGZ(sessionID+".log.2000000000.gz", "archived line 2")

	// Active log file with recent content.
	activePath := filepath.Join(dir, sessionID+".log")
	if err := os.WriteFile(activePath, []byte("live line\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := views.TailSessionLogCmd(activePath, sessionID, 0)()
	got, ok := msg.(views.SessionLogLinesMsg)
	if !ok {
		t.Fatalf("expected SessionLogLinesMsg, got %T", msg)
	}
	if got.SessionID != sessionID {
		t.Errorf("SessionID: want %q, got %q", sessionID, got.SessionID)
	}
	// All three lines (two archived + one live) must be present.
	if len(got.Entries) != 3 {
		t.Fatalf("Entries: want 3, got %d (%v)", len(got.Entries), got.Entries)
	}
	wantTexts := []string{"archived line 1", "archived line 2", "live line"}
	for i, want := range wantTexts {
		if got.Entries[i].Text != want {
			t.Errorf("Entries[%d].Text: want %q, got %q", i, want, got.Entries[i].Text)
		}
	}
	// NextOffset must equal the current active file size so the first
	// continuation poll reads only bytes written after this initial load.
	wantOffset := int64(len("live line\n"))
	if got.NextOffset != wantOffset {
		t.Errorf("NextOffset: want %d, got %d", wantOffset, got.NextOffset)
	}
}

func TestLoadSessionInteractionCmd_ReadsCompressedHistory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sessionID := "sess-history"
	compressedPath := filepath.Join(dir, sessionID+".log.20260308.gz")
	compressedFile, err := os.Create(compressedPath)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(compressedFile)
	compressedContent := `{"type":"event","event":{"type":"assistant_output","text":"first chunk"}}` + "\n" + `{"type":"event","event":{"type":"lifecycle","stage":"completed","summary":"done"}}` + "\n"
	if _, err := gz.Write([]byte(compressedContent)); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressedFile.Close(); err != nil {
		t.Fatal(err)
	}

	activePath := filepath.Join(dir, sessionID+".log")
	if err := os.WriteFile(activePath, []byte("live tail line\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := views.LoadSessionInteractionCmd(dir, sessionID)()
	got, ok := msg.(views.SessionInteractionLoadedMsg)
	if !ok {
		t.Fatalf("expected SessionInteractionLoadedMsg, got %T", msg)
	}
	if len(got.Entries) != 3 {
		t.Fatalf("Entries: want 3, got %d (%v)", 3, got.Entries)
	}
	if got.Entries[0].Kind != sessionlog.KindAssistant || got.Entries[0].Text != "first chunk" {
		t.Errorf("Entries[0]: want {KindAssistant, \"first chunk\"}, got %+v", got.Entries[0])
	}
	if got.Entries[1].Kind != sessionlog.KindLifecycle || got.Entries[1].Stage != "completed" || got.Entries[1].Summary != "done" {
		t.Errorf("Entries[1]: want {KindLifecycle, stage=completed, summary=done}, got %+v", got.Entries[1])
	}
	if got.Entries[2].Kind != sessionlog.KindPlain || got.Entries[2].Text != "live tail line" {
		t.Errorf("Entries[2]: want {KindPlain, \"live tail line\"}, got %+v", got.Entries[2])
	}
	if got.SessionID != sessionID {
		t.Fatalf("SessionID: want %q, got %q", sessionID, got.SessionID)
	}
}

// TestTailSessionLogCmd_ArchivesOnly_NoActiveLog verifies that when only gzipped
// rotations exist (no active .log file), TailSessionLogCmd returns a non-zero
// NextOffset so that subsequent polls enter the continuation path instead of
// re-triggering a full archive reload every cycle (which would cause unbounded
// entry growth and O(n²) rendering cost).
func TestTailSessionLogCmd_ArchivesOnly_NoActiveLog(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const sessionID = "tail-archives-only"

	writeGZ := func(name, line string) {
		t.Helper()
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		gw := gzip.NewWriter(f)
		if _, err := gw.Write([]byte(line + "\n")); err != nil {
			t.Fatal(err)
		}
		if err := gw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	writeGZ(sessionID+".log.1000000000.gz", "archived line 1")
	writeGZ(sessionID+".log.2000000000.gz", "archived line 2")

	// No active .log file exists — only the rotated archives above.
	activePath := filepath.Join(dir, sessionID+".log")
	msg := views.TailSessionLogCmd(activePath, sessionID, 0)()
	got, ok := msg.(views.SessionLogLinesMsg)
	if !ok {
		t.Fatalf("expected SessionLogLinesMsg, got %T", msg)
	}
	if got.SessionID != sessionID {
		t.Errorf("SessionID: want %q, got %q", sessionID, got.SessionID)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("Entries: want 2, got %d (%v)", len(got.Entries), got.Entries)
	}
	if got.Entries[0].Text != "archived line 1" {
		t.Errorf("Entries[0].Text: want %q, got %q", "archived line 1", got.Entries[0].Text)
	}
	if got.Entries[1].Text != "archived line 2" {
		t.Errorf("Entries[1].Text: want %q, got %q", "archived line 2", got.Entries[1].Text)
	}
	// Critical: NextOffset must be non-zero so that a subsequent
	// TailSessionLogCmd(logPath, sessionID, got.NextOffset) enters the
	// continuation path and does NOT re-read all archives.
	if got.NextOffset == 0 {
		t.Fatalf("NextOffset must be non-zero when active .log is missing (got 0); " +
			"this causes an infinite full-reload loop with unbounded entry growth")
	}

	// Second call with the returned offset should enter the continuation path,
	// NOT re-read archives. Since the active .log still doesn't exist, it should
	// return zero entries with the same non-zero offset.
	msg2 := views.TailSessionLogCmd(activePath, sessionID, got.NextOffset)()
	got2, ok := msg2.(views.SessionLogLinesMsg)
	if !ok {
		t.Fatalf("expected SessionLogLinesMsg on second call, got %T", msg2)
	}
	if len(got2.Entries) != 0 {
		t.Errorf("second call should return zero entries when .log is missing, got %d", len(got2.Entries))
	}
	if got2.NextOffset == 0 {
		t.Fatalf("second call NextOffset must be non-zero to prevent re-entering initial load path")
	}
}
