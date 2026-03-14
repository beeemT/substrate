package views

import (
	"strings"
	"testing"

	"github.com/beeemT/substrate/internal/tui/styles"
)

func TestSessionLogViewRespectsRequestedHeightWithMeta(t *testing.T) {
	t.Parallel()

	m := NewSessionLogModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(48, 10)
	m.SetTitle("SUB-1 · Investigate overflow")
	m.SetMeta("workspace · repo-1 · sess-1")
	m.SetStaticContent([]string{"line 1", "line 2", "line 3"})

	lines := strings.Split(m.View(), "\n")
	if got := len(lines); got != 10 {
		t.Fatalf("line count = %d, want 10", got)
	}
	if !strings.Contains(lines[len(lines)-1], "Pause/unpause") {
		t.Fatalf("last line = %q, want session log hints", lines[len(lines)-1])
	}
}

func TestSessionLogSetLogPathKeepsExistingLiveContentForSameSession(t *testing.T) {
	t.Parallel()

	m := NewSessionLogModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(48, 10)
	m.SetLogPath("sess-1", "/tmp/sess-1.log")

	updated, _ := m.Update(SessionLogLinesMsg{SessionID: "sess-1", Lines: []string{"line 1", "line 2"}, NextOffset: 12})
	m = updated

	m.SetLogPath("sess-1", "/tmp/sess-1.log")

	if got := len(m.lines); got != 2 {
		t.Fatalf("line count = %d, want 2", got)
	}
	if got := m.offset; got != 12 {
		t.Fatalf("offset = %d, want 12", got)
	}
	if view := m.View(); !strings.Contains(view, "line 1") || strings.Contains(view, "No session output captured.") {
		t.Fatalf("view = %q, want preserved live session content", view)
	}
}
