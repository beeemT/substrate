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

func TestSessionLogSpinnerStartsWhenAgentActive(t *testing.T) {
	t.Parallel()

	m := NewSessionLogModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(60, 10)
	m.SetLogPath("sess-1", "/tmp/sess-1.log")

	cmd := m.SetAgentActive(true)
	if !m.agentActive {
		t.Fatal("agentActive must be true after SetAgentActive(true)")
	}
	if cmd == nil {
		t.Fatal("SetAgentActive(true) must return a tick cmd")
	}
	if m.spinnerFrame != 0 {
		t.Fatalf("spinnerFrame = %d, want 0", m.spinnerFrame)
	}
}

func TestSessionLogSpinnerStopsWhenAgentInactive(t *testing.T) {
	t.Parallel()

	m := NewSessionLogModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(60, 10)
	m.SetLogPath("sess-1", "/tmp/sess-1.log")
	m.SetAgentActive(true)

	cmd := m.SetAgentActive(false)
	if m.agentActive {
		t.Fatal("agentActive must be false after SetAgentActive(false)")
	}
	if cmd != nil {
		t.Fatal("SetAgentActive(false) must not return a cmd")
	}
}

func TestSessionLogSpinnerNoopOnDuplicate(t *testing.T) {
	t.Parallel()

	m := NewSessionLogModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(60, 10)
	m.SetAgentActive(true)

	cmd := m.SetAgentActive(true)
	if cmd != nil {
		t.Fatal("duplicate SetAgentActive(true) must be a no-op")
	}
}

func TestSessionLogSpinnerTickAdvancesFrame(t *testing.T) {
	t.Parallel()

	m := NewSessionLogModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(60, 10)
	m.SetAgentActive(true)

	updated, cmd := m.Update(sessionLogSpinnerTickMsg{})
	if updated.spinnerFrame != 1 {
		t.Fatalf("spinnerFrame = %d, want 1 after first tick", updated.spinnerFrame)
	}
	if cmd == nil {
		t.Fatal("spinner tick must return a follow-up tick cmd")
	}
}

func TestSessionLogSpinnerTickIgnoredWhenInactive(t *testing.T) {
	t.Parallel()

	m := NewSessionLogModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(60, 10)
	// agentActive is false by default

	updated, cmd := m.Update(sessionLogSpinnerTickMsg{})
	if updated.spinnerFrame != 0 {
		t.Fatalf("spinnerFrame = %d, want 0 when inactive", updated.spinnerFrame)
	}
	if cmd != nil {
		t.Fatal("spinner tick must not return cmd when agent is inactive")
	}
}

func TestSessionLogSpinnerRendersInView(t *testing.T) {
	t.Parallel()

	st := styles.NewStyles(styles.DefaultTheme)
	m := NewSessionLogModel(st)
	m.SetSize(60, 10)
	m.SetTitle("Test")
	m.SetLogPath("sess-1", "/tmp/sess-1.log")
	m.SetAgentActive(true)

	view := m.View()
	// The spinner frame should appear somewhere in the rendered output.
	// Frame 0 is the first braille character.
	if !strings.Contains(view, sessionLogSpinnerFrames[0]) {
		t.Fatalf("view must contain spinner frame %q, got:\n%s", sessionLogSpinnerFrames[0], view)
	}
}

func TestSessionLogSpinnerNotRenderedWhenInactive(t *testing.T) {
	t.Parallel()

	m := NewSessionLogModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(60, 10)
	m.SetTitle("Test")
	m.SetStaticContent([]sessionlog.Entry{{Kind: sessionlog.KindPlain, Text: "hello"}})

	view := m.View()
	for _, frame := range sessionLogSpinnerFrames {
		if strings.Contains(view, frame) {
			t.Fatalf("view must not contain spinner frame %q when inactive, got:\n%s", frame, view)
		}
	}
}

func TestSessionLogSpinnerFitsWidth(t *testing.T) {
	t.Parallel()

	st := styles.NewStyles(styles.DefaultTheme)
	m := NewSessionLogModel(st)
	width := 40
	m.SetSize(width, 10)
	m.SetTitle("Test")
	m.SetLogPath("sess-1", "/tmp/sess-1.log")
	m.SetAgentActive(true)
	m.SetStaticContent([]sessionlog.Entry{{Kind: sessionlog.KindPlain, Text: "content line"}})
	// Re-enable live + spinner after static content (which clears agentActive).
	m.SetLogPath("sess-1", "/tmp/sess-1.log")
	m.SetAgentActive(true)

	view := m.View()
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > width {
			t.Errorf("line %d width = %d, want <= %d\nline: %q", i+1, got, width, line)
		}
	}
}

func TestSessionLogEmptyBodyShowsWaitingWhenActive(t *testing.T) {
	t.Parallel()

	m := NewSessionLogModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(60, 10)
	m.SetTitle("Test")
	m.SetLogPath("sess-1", "/tmp/sess-1.log")
	m.SetAgentActive(true)

	view := stripBrowseANSI(m.View())
	if !strings.Contains(view, "Waiting for agent output") {
		t.Fatalf("view must show \"Waiting for agent output\" when active with no entries, got:\n%s", view)
	}
}

func TestSessionLogStaticContentClearsSpinner(t *testing.T) {
	t.Parallel()

	m := NewSessionLogModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(60, 10)
	m.SetAgentActive(true)

	m.SetStaticContent([]sessionlog.Entry{{Kind: sessionlog.KindPlain, Text: "static"}})
	if m.agentActive {
		t.Fatal("SetStaticContent must clear agentActive")
	}
}
