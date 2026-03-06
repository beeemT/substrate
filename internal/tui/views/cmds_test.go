package views_test

import (
	"os"
	"strings"
	"testing"

	"github.com/beeemT/substrate/internal/tui/views"
)

// TestTailSessionLogCmd_Basic verifies that reading a freshly-written file from
// offset 0 returns all lines and advances NextOffset to the file size.
func TestTailSessionLogCmd_Basic(t *testing.T) {
	t.Parallel()
	f, err := os.CreateTemp(t.TempDir(), "session-*.log")
	if err != nil {
		t.Fatal(err)
	}
	content := "alpha\nbeta\ngamma\n"
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	f.Close()

	msg := views.TailSessionLogCmd(f.Name(), "sess1", 0)()
	got, ok := msg.(views.SessionLogLinesMsg)
	if !ok {
		t.Fatalf("expected SessionLogLinesMsg, got %T", msg)
	}
	if got.SessionID != "sess1" {
		t.Errorf("SessionID: want %q, got %q", "sess1", got.SessionID)
	}
	want := []string{"alpha", "beta", "gamma"}
	if len(got.Lines) != len(want) {
		t.Fatalf("Lines: want %v, got %v", want, got.Lines)
	}
	for i, w := range want {
		if got.Lines[i] != w {
			t.Errorf("Lines[%d]: want %q, got %q", i, w, got.Lines[i])
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
	if len(got.Lines) != 1 || got.Lines[0] != "second" {
		t.Errorf("Lines: want [\"second\"], got %v", got.Lines)
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
	if len(got.Lines) != 1 || got.Lines[0] != "fresh line after rotation" {
		t.Errorf("Lines: want rotation-fresh line, got %v", got.Lines)
	}
	if got.NextOffset != int64(len(newContent)) {
		t.Errorf("NextOffset after rotation: want %d, got %d", len(newContent), got.NextOffset)
	}
}

// TestTailSessionLogCmd_LargeLine verifies that lines larger than the old
// 64 KiB scanner default (now 1 MiB) are returned correctly.
func TestTailSessionLogCmd_LargeLine(t *testing.T) {
	t.Parallel()
	f, err := os.CreateTemp(t.TempDir(), "session-*.log")
	if err != nil {
		t.Fatal(err)
	}
	// 100 KiB line — would have failed with the default bufio.Scanner buffer.
	bigPayload := strings.Repeat("x", 100*1024)
	content := bigPayload + "\n"
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	f.Close()

	msg := views.TailSessionLogCmd(f.Name(), "big", 0)()
	got, ok := msg.(views.SessionLogLinesMsg)
	if !ok {
		t.Fatalf("expected SessionLogLinesMsg, got %T", msg)
	}
	if len(got.Lines) != 1 {
		t.Fatalf("Lines: want 1 line, got %d", len(got.Lines))
	}
	if got.Lines[0] != bigPayload {
		t.Errorf("Lines[0]: length %d, want %d", len(got.Lines[0]), len(bigPayload))
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
	if len(got.Lines) != 0 {
		t.Errorf("Lines: want empty slice, got %v", got.Lines)
	}
}
