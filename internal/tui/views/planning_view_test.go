package views

import (
	"fmt"
	"strings"
	"testing"
	"time"

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

func TestSessionLogSpinnerRestartsAfterDeactivateReactivate(t *testing.T) {
	t.Parallel()

	m := NewSessionLogModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(60, 10)

	// Start spinner.
	cmd := m.SetAgentActive(true)
	if cmd == nil {
		t.Fatal("first SetAgentActive(true) must return tick cmd")
	}

	// Simulate navigating away: spinner is deactivated.
	cmd = m.SetAgentActive(false)
	if cmd != nil {
		t.Fatal("SetAgentActive(false) must not return a cmd")
	}
	if m.agentActive {
		t.Fatal("agentActive must be false after deactivation")
	}

	// Navigate back: reactivation must restart the tick chain.
	cmd = m.SetAgentActive(true)
	if cmd == nil {
		t.Fatal("SetAgentActive(true) after deactivation must return tick cmd to restart spinner")
	}
	if m.spinnerFrame != 0 {
		t.Fatalf("spinnerFrame = %d, want 0 after reactivation", m.spinnerFrame)
	}
}

func TestContentSetModeDeactivatesSpinnerOnPlanningExit(t *testing.T) {
	t.Parallel()

	cm := NewContentModel(styles.NewStyles(styles.DefaultTheme))
	cm.SetSize(80, 40)
	cm.SetMode(ContentModePlanning)
	cm.sessionLog.SetSize(80, 40)
	cm.sessionLog.SetAgentActive(true)

	// Transition away from planning.
	cm.SetMode(ContentModeOverview)
	if cm.sessionLog.agentActive {
		t.Fatal("SetMode to overview must deactivate spinner")
	}

	// Re-entering planning: SetAgentActive(true) must restart the tick chain.
	cm.SetMode(ContentModePlanning)
	cmd := cm.sessionLog.SetAgentActive(true)
	if cmd == nil {
		t.Fatal("SetAgentActive(true) after mode transition must return tick cmd")
	}
}

func TestSessionLogSetAgentActiveSetsLastEventAt(t *testing.T) {
	t.Parallel()
	m := NewSessionLogModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(60, 10)
	m.SetLogPath("sess-1", "/tmp/sess-1.log")
	before := time.Now()
	m.SetAgentActive(true)
	after := time.Now()
	if m.lastEventAt.IsZero() {
		t.Fatal("lastEventAt must be set when agent becomes active")
	}
	if m.lastEventAt.Before(before) || m.lastEventAt.After(after) {
		t.Fatalf("lastEventAt = %v not in expected range [%v, %v]", m.lastEventAt, before, after)
	}
}

func TestSessionLogSilenceNoticeAppearsInView(t *testing.T) {
	t.Parallel()
	m := NewSessionLogModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(60, 10)
	m.SetTitle("Test")
	m.SetLogPath("sess-1", "/tmp/sess-1.log")
	m.SetAgentActive(true)
	// Simulate threshold crossed.
	m.lastEventAt = time.Now().Add(-sessionLogSilenceThreshold - time.Second)
	m.silenceNoticeActive = true
	m.syncViewportSize()
	view := stripBrowseANSI(m.View())
	if !strings.Contains(view, "No output for") {
		t.Fatalf("view must contain silence notice, got:\n%s", view)
	}
}

func TestSessionLogSilenceNoticeFitsWidth(t *testing.T) {
	t.Parallel()
	for _, width := range []int{20, 40, 80} {
		width := width
		t.Run(fmt.Sprintf("width=%d", width), func(t *testing.T) {
			t.Parallel()
			m := NewSessionLogModel(styles.NewStyles(styles.DefaultTheme))
			m.SetSize(width, 10)
			m.SetTitle("T")
			m.SetLogPath("sess-1", "/tmp/sess-1.log")
			m.SetAgentActive(true)
			m.lastEventAt = time.Now().Add(-sessionLogSilenceThreshold - time.Second)
			m.silenceNoticeActive = true
			m.syncViewportSize()
			lines := strings.Split(m.View(), "\n")
			for i, line := range lines {
				if got := ansi.StringWidth(line); got > width {
					t.Errorf("line %d width = %d, want <= %d\nline: %q", i+1, got, width, line)
				}
			}
		})
	}
}

func TestSessionLogSilenceNoticeReducesViewportHeight(t *testing.T) {
	t.Parallel()
	m := NewSessionLogModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(60, 15)
	m.SetTitle("Test")
	m.SetLogPath("sess-1", "/tmp/sess-1.log")
	m.SetAgentActive(true)
	heightWithout := m.viewport.Height
	// Activate notice.
	m.lastEventAt = time.Now().Add(-sessionLogSilenceThreshold - time.Second)
	m.silenceNoticeActive = true
	m.syncViewportSize()
	heightWith := m.viewport.Height
	if diff := heightWithout - heightWith; diff != 1 {
		t.Fatalf("silence notice should reduce viewport height by 1, got diff=%d (without=%d, with=%d)", diff, heightWithout, heightWith)
	}
}

func TestSessionLogSilenceNoticeFitsHeight(t *testing.T) {
	t.Parallel()
	m := NewSessionLogModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(80, 10)
	m.SetTitle("Test")
	m.SetLogPath("sess-1", "/tmp/sess-1.log")
	m.SetAgentActive(true)
	m.lastEventAt = time.Now().Add(-sessionLogSilenceThreshold - time.Second)
	m.silenceNoticeActive = true
	m.syncViewportSize()
	lines := strings.Split(m.View(), "\n")
	if got := len(lines); got != 10 {
		t.Fatalf("line count with silence notice = %d, want 10", got)
	}
}

func TestSessionLogSilenceNoticeClearedOnEntries(t *testing.T) {
	t.Parallel()
	m := NewSessionLogModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(60, 10)
	m.SetLogPath("sess-1", "/tmp/sess-1.log")
	m.SetAgentActive(true)
	m.lastEventAt = time.Now().Add(-sessionLogSilenceThreshold - time.Second)
	m.silenceNoticeActive = true
	m.syncViewportSize()
	updated, _ := m.Update(SessionLogLinesMsg{
		SessionID:  "sess-1",
		Entries:    []sessionlog.Entry{{Kind: sessionlog.KindPlain, Text: "output"}},
		NextOffset: 10,
	})
	m = updated
	if m.silenceNoticeActive {
		t.Fatal("silenceNoticeActive must be cleared when entries arrive")
	}
	if strings.Contains(stripBrowseANSI(m.View()), "No output for") {
		t.Fatal("silence notice must not appear after entries arrive")
	}
}

func TestSessionLogSilenceNoticeClearedOnDeactivate(t *testing.T) {
	t.Parallel()
	m := NewSessionLogModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(60, 10)
	m.SetLogPath("sess-1", "/tmp/sess-1.log")
	m.SetAgentActive(true)
	m.lastEventAt = time.Now().Add(-sessionLogSilenceThreshold - time.Second)
	m.silenceNoticeActive = true
	m.syncViewportSize()
	heightWith := m.viewport.Height
	m.SetAgentActive(false)
	if m.silenceNoticeActive {
		t.Fatal("silenceNoticeActive must be cleared when agent deactivates")
	}
	if m.viewport.Height <= heightWith {
		t.Fatalf("viewport height must increase when silence notice is cleared, got %d, was %d", m.viewport.Height, heightWith)
	}
}
