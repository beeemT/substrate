package views

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/sessionlog"
	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

func TestSessionLogViewRespectsRequestedHeightWithMeta(t *testing.T) {
	t.Parallel()

	m := NewSessionLogModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(48, 10)
	m.SetTitle("SUB-1 · Investigate overflow")
	m.SetMeta("workspace · repo-1 · sess-1")
	m.SetStaticContent([]sessionlog.Entry{{Kind: sessionlog.KindPlain, Text: "line 1"}, {Kind: sessionlog.KindPlain, Text: "line 2"}, {Kind: sessionlog.KindPlain, Text: "line 3"}})

	lines := strings.Split(m.View(), "\n")
	if got := len(lines); got != 10 {
		t.Fatalf("line count = %d, want 10", got)
	}
}

func TestSessionLogSetLogPathKeepsExistingLiveContentForSameSession(t *testing.T) {
	t.Parallel()

	m := NewSessionLogModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(48, 10)
	m.SetLogPath("sess-1", "/tmp/sess-1.log")

	updated, _ := m.Update(SessionLogLinesMsg{SessionID: "sess-1", Entries: []sessionlog.Entry{{Kind: sessionlog.KindPlain, Text: "line 1"}, {Kind: sessionlog.KindPlain, Text: "line 2"}}, NextOffset: 12})
	m = updated

	m.SetLogPath("sess-1", "/tmp/sess-1.log")

	if got := len(m.entries); got != 2 {
		t.Fatalf("line count = %d, want 2", got)
	}
	if got := m.offset; got != 12 {
		t.Fatalf("offset = %d, want 12", got)
	}
	if view := m.View(); !strings.Contains(view, "line 1") || strings.Contains(view, "No session output captured.") {
		t.Fatalf("view = %q, want preserved live session content", view)
	}
}

func TestSessionLogSetLogPathResetsForDifferentSession(t *testing.T) {
	t.Parallel()

	m := NewSessionLogModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(48, 10)
	m.SetLogPath("sess-1", "/tmp/sess-1.log")
	updated, _ := m.Update(SessionLogLinesMsg{SessionID: "sess-1", Entries: []sessionlog.Entry{{Kind: sessionlog.KindPlain, Text: "line 1"}, {Kind: sessionlog.KindPlain, Text: "line 2"}}, NextOffset: 12})
	m = updated

	m.SetLogPath("sess-2", "/tmp/sess-2.log")

	if got := len(m.entries); got != 0 {
		t.Fatalf("line count = %d, want reset to 0", got)
	}
	if got := m.offset; got != 0 {
		t.Fatalf("offset = %d, want reset to 0", got)
	}
	if view := m.View(); strings.Contains(view, "line 1") || !strings.Contains(view, "No session output captured.") {
		t.Fatalf("view = %q, want cleared session content", view)
	}
}

func TestSessionLogNoticeFitsRequestedHeight(t *testing.T) {
	t.Parallel()

	m := NewSessionLogModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(48, 12)
	m.SetTitle("SUB-1 · Investigate overflow")
	m.SetMeta("workspace · repo-1 · sess-1")
	m.SetStaticContent([]sessionlog.Entry{{Kind: sessionlog.KindPlain, Text: "line 1"}, {Kind: sessionlog.KindPlain, Text: "line 2"}, {Kind: sessionlog.KindPlain, Text: "line 3"}})
	m.SetNotice(&sourceDetailsNotice{
		Title:   "Interrupted task needs recovery",
		Body:    "repo-1 was interrupted and cannot continue until it is resumed or abandoned.",
		Hint:    "Press [Enter] to open the overview.",
		Variant: components.CalloutWarning,
	})

	rendered := m.View()
	plain := stripBrowseANSI(rendered)
	for _, want := range []string{"Interrupted task needs recovery", "Press [Enter] to open the overview.", "line 1"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("view = %q, want %q", plain, want)
		}
	}
	hints := m.KeybindHints()
	hintLabels := make([]string, len(hints))
	for i, h := range hints {
		hintLabels[i] = h.Label
	}
	foundOpenOverview := false
	for _, h := range hints {
		if h.Label == "Open overview" {
			foundOpenOverview = true

			break
		}
	}
	if !foundOpenOverview {
		t.Fatalf("keybind hints = %#v, want open-overview action in hints: %v", hints, hintLabels)
	}
	lines := strings.Split(rendered, "\n")
	if got := len(lines); got != 12 {
		t.Fatalf("line count = %d, want 12", got)
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > 48 {
			t.Fatalf("line %d width = %d, want <= 48\nline: %q", i+1, got, line)
		}
	}
}
