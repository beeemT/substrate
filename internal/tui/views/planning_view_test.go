package views

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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
	m.SetSize(48, 14)
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
	lines := strings.Split(rendered, "\n")
	if got := len(lines); got != 14 {
		t.Fatalf("line count = %d, want 14", got)
	}
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
	if got := len(lines); got != 14 {
		t.Fatalf("line count = %d, want 14", got)
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > 48 {
			t.Fatalf("line %d width = %d, want <= 48\nline: %q", i+1, got, line)
		}
	}
}

func TestSessionLogPromptInputResizesViewportPreservingBottom(t *testing.T) {
	t.Parallel()

	entries := make([]sessionlog.Entry, 0, 40)
	for i := 1; i <= 40; i++ {
		entries = append(entries, sessionlog.Entry{Kind: sessionlog.KindPlain, Text: fmt.Sprintf("line %02d", i)})
	}
	m := NewSessionLogModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(48, 12)
	m.SetLogPath("sess-1", "/tmp/sess-1.log")
	m, _ = m.Update(SessionLogLinesMsg{SessionID: "sess-1", Entries: entries, NextOffset: 1})

	if !m.viewport.AtBottom() {
		t.Fatal("test setup should start at transcript bottom")
	}
	initialHeight := m.viewport.Height
	initialOffset := m.viewport.YOffset

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if !m.steerActive {
		t.Fatal("p should activate prompt input")
	}
	if m.viewport.Height >= initialHeight {
		t.Fatalf("viewport height = %d, want less than %d after prompt opens", m.viewport.Height, initialHeight)
	}
	if m.viewport.YOffset <= initialOffset {
		t.Fatalf("viewport offset = %d, want > %d so bottom content is pushed above prompt", m.viewport.YOffset, initialOffset)
	}
	if !m.viewport.AtBottom() {
		t.Fatalf("viewport should stay at bottom after prompt opens; offset=%d height=%d total=%d", m.viewport.YOffset, m.viewport.Height, m.viewport.TotalLineCount())
	}

	openHeight := m.viewport.Height
	openOffset := m.viewport.YOffset
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(strings.Repeat("expanded prompt ", 80))})
	if m.viewport.Height >= openHeight {
		t.Fatalf("viewport height = %d, want less than %d after prompt expands", m.viewport.Height, openHeight)
	}
	if m.viewport.YOffset <= openOffset {
		t.Fatalf("viewport offset = %d, want > %d after prompt expands", m.viewport.YOffset, openOffset)
	}
	if !m.viewport.AtBottom() {
		t.Fatalf("viewport should stay at bottom after prompt expands; offset=%d height=%d total=%d", m.viewport.YOffset, m.viewport.Height, m.viewport.TotalLineCount())
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.steerActive {
		t.Fatal("prompt input should close after second Esc")
	}
	if m.viewport.Height != initialHeight {
		t.Fatalf("viewport height = %d, want restored %d after close", m.viewport.Height, initialHeight)
	}
	if !m.viewport.AtBottom() {
		t.Fatalf("viewport should stay at bottom after prompt closes; offset=%d height=%d total=%d", m.viewport.YOffset, m.viewport.Height, m.viewport.TotalLineCount())
	}
}

func TestSessionLogSteerInputPreservesLongFeedback(t *testing.T) {
	t.Parallel()

	m := NewSessionLogModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(48, 12)
	m.SetCompletedSession("task-1")
	m.SetStaticContent([]sessionlog.Entry{{Kind: sessionlog.KindPlain, Text: "completed session output"}})

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if !m.steerActive {
		t.Fatal("p should activate steering input for a completed session")
	}

	longFeedback := strings.Repeat("research result line with enough detail\n", 160) // > 5000 chars.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(longFeedback)})

	view := m.View()
	lines := strings.Split(view, "\n")
	if got, want := len(lines), 12; got != want {
		t.Fatalf("line count = %d, want %d\nview:\n%s", got, want, view)
	}
	for i, line := range lines {
		if got, want := ansi.StringWidth(line), 48; got > want {
			t.Fatalf("line %d width = %d, want <= %d\nline: %q", i+1, got, want, line)
		}
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if updated.steerActive {
		t.Fatal("steering input should be inactive after Enter submit")
	}
	if cmd == nil {
		t.Fatal("Enter submit must return a command")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("expected BatchMsg, got %T", cmd())
	}

	var followUpMsg FollowUpSessionMsg
	foundAction := false
	foundMouse := false
	for _, c := range batch {
		switch msg := c().(type) {
		case FollowUpSessionMsg:
			followUpMsg = msg
			foundAction = true
		default:
			if msg == tea.EnableMouseCellMotion() {
				foundMouse = true
			}
		}
	}
	if !foundAction {
		t.Fatal("batch did not contain FollowUpSessionMsg")
	}
	if !foundMouse {
		t.Fatal("batch did not contain EnableMouseCellMotion")
	}
	if followUpMsg.TaskID != "task-1" {
		t.Fatalf("FollowUpSessionMsg.TaskID = %q, want task-1", followUpMsg.TaskID)
	}
	if followUpMsg.Feedback != longFeedback {
		t.Fatalf("FollowUpSessionMsg.Feedback length = %d, want %d", len(followUpMsg.Feedback), len(longFeedback))
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
	if cmd == nil {
		t.Fatal("SetAgentActive(false) must return final tail cmd for live logs")
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
	if updated.toolAnimationFrame != sessionLogToolAnimationStep {
		t.Fatalf("toolAnimationFrame = %d, want %d after first tick", updated.toolAnimationFrame, sessionLogToolAnimationStep)
	}
	if cmd == nil {
		t.Fatal("spinner tick must return a follow-up tick cmd")
	}
}

func TestSessionLogRunningToolSeparatorAnimatesOnTick(t *testing.T) {
	t.Parallel()

	st := styles.NewStyles(styles.DefaultTheme)
	m := NewSessionLogModel(st)
	m.SetSize(80, 14)
	m.SetLogPath("sess-1", "/tmp/sess-1.log")
	m.SetAgentActive(true)

	model, _ := m.Update(SessionLogLinesMsg{
		SessionID: "sess-1",
		Entries: []sessionlog.Entry{
			{Kind: sessionlog.KindToolStart, Tool: "read", Intent: "Reading", Text: `{"path":"x.go"}`},
			{Kind: sessionlog.KindToolOutput, Tool: "read", Text: "partial output"},
		},
		NextOffset: 10,
	})
	before := renderedToolSeparatorLine(model.viewport.View(), true)
	if before == "" {
		t.Fatalf("expected initial animated separator, got:\n%s", ansi.Strip(model.viewport.View()))
	}

	updated, cmd := model.Update(sessionLogSpinnerTickMsg{})
	after := renderedToolSeparatorLine(updated.viewport.View(), true)
	if after == "" {
		t.Fatalf("expected animated separator after tick, got:\n%s", ansi.Strip(updated.viewport.View()))
	}
	if updated.toolAnimationFrame <= model.toolAnimationFrame {
		t.Fatalf("toolAnimationFrame = %d, want > %d", updated.toolAnimationFrame, model.toolAnimationFrame)
	}
	if cmd == nil {
		t.Fatal("spinner tick must continue while agent is active")
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

func TestSessionLogSpinnerAtBottomRightWhenNoContent(t *testing.T) {
	t.Parallel()

	st := styles.NewStyles(styles.DefaultTheme)
	m := NewSessionLogModel(st)
	m.SetSize(60, 10)
	m.SetTitle("Test")
	m.SetLogPath("sess-1", "/tmp/sess-1.log")
	m.SetAgentActive(true)

	view := m.View()
	lines := strings.Split(view, "\n")
	if len(lines) != 10 {
		t.Fatalf("line count = %d, want 10", len(lines))
	}

	// Spinner must be on the last line (bottom-right corner), not directly under the header.
	lastLine := lines[len(lines)-1]
	if !strings.Contains(lastLine, sessionLogSpinnerFrames[0]) {
		t.Fatalf("spinner must be on last line (bottom-right corner), got last line: %q\nfull view:\n%s", lastLine, view)
	}

	// All earlier lines must not contain the spinner frame.
	for i, line := range lines[:len(lines)-1] {
		if strings.Contains(line, sessionLogSpinnerFrames[0]) {
			t.Errorf("spinner found on non-last line %d: %q", i+1, line)
		}
	}
}

func TestSessionLogSpinnerAtBottomRightWithPartialContent(t *testing.T) {
	t.Parallel()

	st := styles.NewStyles(styles.DefaultTheme)
	m := NewSessionLogModel(st)
	m.SetSize(60, 15)
	m.SetTitle("Test")
	m.SetLogPath("sess-1", "/tmp/sess-1.log")
	m.SetAgentActive(true)

	// Feed a few lines that don't fill the viewport.
	updated, _ := m.Update(SessionLogLinesMsg{
		SessionID:  "sess-1",
		Entries:    []sessionlog.Entry{{Kind: sessionlog.KindPlain, Text: "line 1"}, {Kind: sessionlog.KindPlain, Text: "line 2"}},
		NextOffset: 10,
	})
	m = updated

	view := m.View()
	lines := strings.Split(view, "\n")
	if len(lines) != 15 {
		t.Fatalf("line count = %d, want 15", len(lines))
	}

	// Spinner must be on the last line (bottom-right corner),
	// not on the last content line.
	lastLine := lines[len(lines)-1]
	if !strings.Contains(lastLine, sessionLogSpinnerFrames[0]) {
		t.Fatalf("spinner must be on last line (bottom-right corner), got last line: %q\nfull view:\n%s", lastLine, view)
	}

	for i, line := range lines[:len(lines)-1] {
		if strings.Contains(line, sessionLogSpinnerFrames[0]) {
			t.Errorf("spinner found on non-last line %d: %q", i+1, line)
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
	cm.SetMode(ContentModeAgentSession)
	cm.sessionLog.SetSize(80, 40)
	cm.sessionLog.SetAgentActive(true)

	// Transition away from planning.
	cm.SetMode(ContentModeOverview)
	if cm.sessionLog.agentActive {
		t.Fatal("SetMode to overview must deactivate spinner")
	}

	// Re-entering planning: SetAgentActive(true) must restart the tick chain.
	cm.SetMode(ContentModeAgentSession)
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

// TestSessionLogSilenceNoticeStableViewportHeight verifies that the silence
// warning occupies the divider slot rather than adding an extra header line,
// so the viewport height stays the same whether the warning is active or not.
func TestSessionLogSilenceNoticeStableViewportHeight(t *testing.T) {
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
	if heightWith != heightWithout {
		t.Fatalf("silence notice must not change viewport height (warning replaces divider slot), without=%d, with=%d", heightWithout, heightWith)
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
	m.SetTitle("Test")
	m.SetLogPath("sess-1", "/tmp/sess-1.log")
	m.SetAgentActive(true)
	m.lastEventAt = time.Now().Add(-sessionLogSilenceThreshold - time.Second)
	m.silenceNoticeActive = true
	m.syncViewportSize()
	// Warning must be visible before deactivation.
	if !strings.Contains(stripBrowseANSI(m.View()), "No output for") {
		t.Fatal("view must contain silence warning before agent deactivation")
	}
	m.SetAgentActive(false)
	if m.silenceNoticeActive {
		t.Fatal("silenceNoticeActive must be cleared when agent deactivates")
	}
	// Warning must no longer appear after deactivation.
	if strings.Contains(stripBrowseANSI(m.View()), "No output for") {
		t.Fatal("silence notice must not appear in view after agent deactivates")
	}
}
