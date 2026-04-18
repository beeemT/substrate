package views_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/views"
)

// testOverviewLinksData returns a minimal set of sources and reviews for testing.
func testOverviewLinksData() ([]views.OverviewSourceItem, []views.OverviewReviewRow) {
	sources := []views.OverviewSourceItem{
		{Provider: "linear", Ref: "SUB-42", Title: "Implement auth module", URL: "https://linear.app/t/SUB-42"},
		{Provider: "linear", Ref: "SUB-43", Title: "Add tests", URL: "https://linear.app/t/SUB-43"},
	}
	reviews := []views.OverviewReviewRow{
		{Kind: "pull_request", RepoName: "acme/api", Ref: "!7", URL: "https://github.com/acme/api/pull/7", State: "merged"},
		{Kind: "pull_request", RepoName: "acme/frontend", Ref: "!12", URL: "https://github.com/acme/frontend/pull/12", State: "open", Branch: "feature/auth"},
	}
	return sources, reviews
}

func openedLinksOverlay(t *testing.T) views.OverviewLinksOverlay {
	t.Helper()
	m := views.NewOverviewLinksOverlay(newTestStyles(t))
	m.SetSize(120, 40)
	sources, reviews := testOverviewLinksData()
	m.Open(sources, reviews)
	return m
}

// ---------------------------------------------------------------------------
// Active / inactive gate
// ---------------------------------------------------------------------------

func TestOverviewLinksOverlayInactiveViewEmpty(t *testing.T) {
	t.Parallel()
	m := views.NewOverviewLinksOverlay(newTestStyles(t))
	if m.Active() {
		t.Fatal("overlay must be inactive before Open()")
	}
	if v := m.View(); v != "" {
		t.Fatalf("View() on inactive overlay = %q, want \"\"", v)
	}
}

// ---------------------------------------------------------------------------
// Layout: rendered width must stay within the requested terminal width.
// ---------------------------------------------------------------------------

func TestOverviewLinksOverlayViewFitsTerminalSize(t *testing.T) {
	t.Parallel()
	m := openedLinksOverlay(t)
	v := m.View()
	if v == "" {
		t.Fatal("View() returned empty string on active overlay")
	}
	assertViewFitsSize(t, v, 120, 40)
}

func TestOverviewLinksOverlayViewFitsNarrowTerminal(t *testing.T) {
	t.Parallel()
	m := views.NewOverviewLinksOverlay(newTestStyles(t))
	m.SetSize(70, 20)
	sources, reviews := testOverviewLinksData()
	m.Open(sources, reviews)
	assertViewFitsSize(t, m.View(), 70, 20)
}

// ---------------------------------------------------------------------------
// Content: both sections must appear in the rendered view.
// ---------------------------------------------------------------------------

func TestOverviewLinksOverlayContainsTicketsSection(t *testing.T) {
	t.Parallel()
	m := openedLinksOverlay(t)
	plain := ansi.Strip(m.View())
	for _, want := range []string{"Tickets", "SUB-42", "Implement auth module", "SUB-43"} {
		if !strings.Contains(plain, want) {
			t.Errorf("View missing %q\nfull view:\n%s", want, plain)
		}
	}
}

func TestOverviewLinksOverlayContainsMRsSection(t *testing.T) {
	t.Parallel()
	m := openedLinksOverlay(t)
	plain := ansi.Strip(m.View())
	for _, want := range []string{"MRs / PRs", "acme/api", "!7", "acme/frontend", "!12"} {
		if !strings.Contains(plain, want) {
			t.Errorf("View missing %q\nfull view:\n%s", want, plain)
		}
	}
}

func TestOverviewLinksOverlayTicketsOnlyWhenNoReviews(t *testing.T) {
	t.Parallel()
	m := views.NewOverviewLinksOverlay(newTestStyles(t))
	m.SetSize(120, 40)
	m.Open([]views.OverviewSourceItem{
		{Ref: "T-1", Title: "Only ticket", URL: "https://example.com/t/1"},
	}, nil)
	plain := ansi.Strip(m.View())
	if !strings.Contains(plain, "Tickets") {
		t.Error("expected Tickets section")
	}
	if strings.Contains(plain, "MRs") {
		t.Error("unexpected MRs / PRs section when reviews is nil")
	}
}

func TestOverviewLinksOverlayMRsOnlyWhenNoSources(t *testing.T) {
	t.Parallel()
	m := views.NewOverviewLinksOverlay(newTestStyles(t))
	m.SetSize(120, 40)
	m.Open(nil, []views.OverviewReviewRow{
		{RepoName: "acme/api", Ref: "!7", URL: "https://github.com/acme/api/pull/7", State: "merged"},
	})
	plain := ansi.Strip(m.View())
	if !strings.Contains(plain, "MRs / PRs") {
		t.Error("expected MRs / PRs section")
	}
	if strings.Contains(plain, "Tickets") {
		t.Error("unexpected Tickets section when sources is nil")
	}
}

// ---------------------------------------------------------------------------
// Keyboard: Esc closes, Enter/o opens URL.
// ---------------------------------------------------------------------------

func TestOverviewLinksOverlayEscEmitsCloseMsg(t *testing.T) {
	t.Parallel()
	m := openedLinksOverlay(t)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("Esc must return a non-nil cmd")
	}
	if _, ok := cmd().(views.CloseOverlayMsg); !ok {
		t.Fatalf("Esc cmd() = %T, want CloseOverlayMsg", cmd())
	}
}

func TestOverviewLinksOverlayEnterOpensFirstItemURL(t *testing.T) {
	t.Parallel()
	m := openedLinksOverlay(t)
	// cursor starts at 0 (first ticket)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter on item with URL must return a non-nil cmd")
	}
	msg := cmd()
	openMsg, ok := msg.(views.OpenExternalURLMsg)
	if !ok {
		t.Fatalf("Enter cmd() = %T, want OpenExternalURLMsg", msg)
	}
	if openMsg.URL != "https://linear.app/t/SUB-42" {
		t.Fatalf("Enter opened URL = %q, want first ticket URL", openMsg.URL)
	}
}

func TestOverviewLinksOverlayNavigateToMRAndOpen(t *testing.T) {
	t.Parallel()
	m := openedLinksOverlay(t)
	// tickets = 2, so navigate down twice to land on the first MR
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter on MR item must return non-nil cmd")
	}
	msg := cmd()
	openMsg, ok := msg.(views.OpenExternalURLMsg)
	if !ok {
		t.Fatalf("Enter cmd() after navigating to MR = %T, want OpenExternalURLMsg", msg)
	}
	if openMsg.URL != "https://github.com/acme/api/pull/7" {
		t.Fatalf("URL = %q, want first MR URL", openMsg.URL)
	}
}

func TestOverviewLinksOverlayOKeyOpensURL(t *testing.T) {
	t.Parallel()
	m := openedLinksOverlay(t)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if cmd == nil {
		t.Fatal("[o] on item with URL must return a non-nil cmd")
	}
	if _, ok := cmd().(views.OpenExternalURLMsg); !ok {
		t.Fatalf("[o] cmd() = %T, want OpenExternalURLMsg", cmd())
	}
}

// ---------------------------------------------------------------------------
// Cursor boundary: navigating past ends clamps gracefully.
// ---------------------------------------------------------------------------

func TestOverviewLinksOverlayCursorClampsAtBoundaries(t *testing.T) {
	t.Parallel()
	m := openedLinksOverlay(t)
	// navigate far up from position 0 — should stay at first item
	for range 10 {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter after clamped-up navigation must still work")
	}
	msg := cmd()
	open, ok := msg.(views.OpenExternalURLMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want OpenExternalURLMsg", msg)
	}
	if open.URL != "https://linear.app/t/SUB-42" {
		t.Fatalf("URL after clamp-up = %q, want first ticket", open.URL)
	}

	// navigate far down — should land on last MR
	for range 10 {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter after clamped-down navigation must still work")
	}
	open2, ok := cmd().(views.OpenExternalURLMsg)
	if !ok {
		t.Fatalf("cmd() after clamp-down = %T, want OpenExternalURLMsg", msg)
	}
	if open2.URL != "https://github.com/acme/frontend/pull/12" {
		t.Fatalf("URL after clamp-down = %q, want last MR URL", open2.URL)
	}
}

// ---------------------------------------------------------------------------
// Mouse wheel forwarded to navigation.
// ---------------------------------------------------------------------------

func TestOverviewLinksOverlayMouseWheelNavigates(t *testing.T) {
	t.Parallel()
	m := openedLinksOverlay(t)
	// WheelDown should move cursor forward; Enter should open second ticket
	m, _ = m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter after wheel-down must return non-nil cmd")
	}
	open, ok := cmd().(views.OpenExternalURLMsg)
	if !ok {
		t.Fatalf("cmd() after wheel-down = %T, want OpenExternalURLMsg", cmd())
	}
	if open.URL != "https://linear.app/t/SUB-43" {
		t.Fatalf("URL after wheel-down = %q, want second ticket URL", open.URL)
	}
}

// ---------------------------------------------------------------------------
// SessionOverviewModel integration: 'o' key emits OpenOverviewLinksMsg.
// ---------------------------------------------------------------------------

func TestOverviewPageOKeyEmitsOpenLinksMsg(t *testing.T) {
	t.Parallel()
	m := views.NewSessionOverviewModel(newTestStyles(t))
	m.SetTerminalSize(120, 40)
	m.SetSize(90, 30)
	m.SetData(views.SessionOverviewData{
		WorkItemID: "wi-links",
		State:      domain.SessionCompleted,
		Header:     views.OverviewHeader{ExternalID: "SUB-1", Title: "Links test", StatusLabel: "✓ Completed"},
		Sources: []views.OverviewSourceItem{
			{Provider: "linear", Ref: "SUB-42", Title: "Auth module", URL: "https://linear.app/t/SUB-42"},
		},
		External: views.OverviewExternalLifecycle{
			Reviews: []views.OverviewReviewRow{
				{RepoName: "acme/api", Ref: "!7", URL: "https://github.com/acme/api/pull/7", State: "merged"},
			},
		},
	})

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if cmd == nil {
		t.Fatal("[o] with sources and reviews must emit a command")
	}
	msg := cmd()
	linksMsg, ok := msg.(views.OpenOverviewLinksMsg)
	if !ok {
		t.Fatalf("[o] cmd() = %T, want OpenOverviewLinksMsg", msg)
	}
	if len(linksMsg.Sources) != 1 || linksMsg.Sources[0].Ref != "SUB-42" {
		t.Fatalf("Sources in msg = %v, want one item with Ref SUB-42", linksMsg.Sources)
	}
	if len(linksMsg.Reviews) != 1 || linksMsg.Reviews[0].Ref != "!7" {
		t.Fatalf("Reviews in msg = %v, want one item with Ref !7", linksMsg.Reviews)
	}
}

func TestOverviewPageOKeyOnSourcesOnlyEmitsOpenLinksMsg(t *testing.T) {
	t.Parallel()
	m := views.NewSessionOverviewModel(newTestStyles(t))
	m.SetTerminalSize(120, 40)
	m.SetSize(90, 30)
	m.SetData(views.SessionOverviewData{
		WorkItemID: "wi-src",
		State:      domain.SessionCompleted,
		Header:     views.OverviewHeader{ExternalID: "SUB-2", Title: "Sources only"},
		Sources: []views.OverviewSourceItem{
			{Provider: "linear", Ref: "SUB-99", URL: "https://linear.app/t/SUB-99"},
		},
	})

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if cmd == nil {
		t.Fatal("[o] with sources only must emit a command")
	}
	if _, ok := cmd().(views.OpenOverviewLinksMsg); !ok {
		t.Fatalf("[o] cmd() = %T, want OpenOverviewLinksMsg", cmd())
	}
}


// ---------------------------------------------------------------------------
// Open-all: 'a' key opens every URL in the overlay.
// ---------------------------------------------------------------------------

func TestOverviewLinksOverlayOpenAllEmitsBatchCmd(t *testing.T) {
	t.Parallel()
	m := openedLinksOverlay(t) // 2 tickets + 2 MRs = 4 items
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd == nil {
		t.Fatal("[a] must return a non-nil cmd")
	}
	batchResult := cmd()
	batchCmds, ok := batchResult.(tea.BatchMsg)
	if !ok {
		t.Fatalf("[a] cmd() = %T, want tea.BatchMsg", batchResult)
	}

	wantURLs := map[string]bool{
		"https://linear.app/t/SUB-42":               false,
		"https://linear.app/t/SUB-43":               false,
		"https://github.com/acme/api/pull/7":         false,
		"https://github.com/acme/frontend/pull/12":   false,
	}

	for _, c := range batchCmds {
		if c == nil {
			continue
		}
		msg, ok := c().(views.OpenExternalURLMsg)
		if !ok {
			t.Fatalf("inner cmd() = %T, want OpenExternalURLMsg", c())
		}
		if _, exists := wantURLs[msg.URL]; !exists {
			t.Fatalf("unexpected URL %q", msg.URL)
		}
		wantURLs[msg.URL] = true
	}

	for url, seen := range wantURLs {
		if !seen {
			t.Errorf("URL not opened: %s", url)
		}
	}
}

func TestOverviewLinksOverlayOpenAllEmptyOverlayReturnsNil(t *testing.T) {
	t.Parallel()
	m := views.NewOverviewLinksOverlay(newTestStyles(t))
	m.SetSize(120, 40)
	m.Open(nil, nil)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd != nil {
		t.Fatalf("[a] on empty overlay must return nil cmd, got %T", cmd)
	}
}

// ---------------------------------------------------------------------------
// OpenFromArtifacts: overlay populated from artifact items.
// ---------------------------------------------------------------------------

func TestOverviewLinksOverlayOpenFromArtifactsShowsMRsSection(t *testing.T) {
	t.Parallel()
	m := views.NewOverviewLinksOverlay(newTestStyles(t))
	m.SetSize(120, 40)
	m.OpenFromArtifacts([]views.ArtifactItem{
		{Provider: "github", Kind: "PR", RepoName: "acme/api", Ref: "#7", URL: "https://github.com/acme/api/pull/7", State: "open", Branch: "feat/auth"},
		{Provider: "github", Kind: "PR", RepoName: "acme/web", Ref: "#20", URL: "https://github.com/acme/web/pull/20", State: "merged"},
	})

	if !m.Active() {
		t.Fatal("overlay must be active after OpenFromArtifacts")
	}

	plain := ansi.Strip(m.View())
	for _, want := range []string{"MRs / PRs", "acme/api", "#7", "acme/web", "#20"} {
		if !strings.Contains(plain, want) {
			t.Errorf("View missing %q\nfull view:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "Tickets") {
		t.Error("unexpected Tickets section in OpenFromArtifacts overlay")
	}
}

func TestOverviewLinksOverlayOpenFromArtifactsNavigateAndOpen(t *testing.T) {
	t.Parallel()
	m := views.NewOverviewLinksOverlay(newTestStyles(t))
	m.SetSize(120, 40)
	m.OpenFromArtifacts([]views.ArtifactItem{
		{Provider: "github", Kind: "PR", RepoName: "acme/api", Ref: "#7", URL: "https://github.com/acme/api/pull/7", State: "open"},
		{Provider: "github", Kind: "PR", RepoName: "acme/web", Ref: "#20", URL: "https://github.com/acme/web/pull/20", State: "merged"},
	})

	// Navigate down once to second item
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter on second artifact must return non-nil cmd")
	}
	msg, ok := cmd().(views.OpenExternalURLMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want OpenExternalURLMsg", cmd())
	}
	if msg.URL != "https://github.com/acme/web/pull/20" {
		t.Fatalf("URL = %q, want second artifact URL", msg.URL)
	}
}

func TestOverviewLinksOverlayOpenFromArtifactsFitsSize(t *testing.T) {
	t.Parallel()
	m := views.NewOverviewLinksOverlay(newTestStyles(t))
	m.SetSize(80, 25)
	m.OpenFromArtifacts([]views.ArtifactItem{
		{Provider: "github", Kind: "PR", RepoName: "acme/api", Ref: "#7", URL: "https://github.com/acme/api/pull/7", State: "open"},
	})
	assertViewFitsSize(t, m.View(), 80, 25)
}

// ---------------------------------------------------------------------------
// Hint text: [a] Open all shown conditionally.
// ---------------------------------------------------------------------------

func TestOverviewLinksOverlayHintShowsOpenAllWhenMultipleItems(t *testing.T) {
	t.Parallel()
	m := openedLinksOverlay(t) // 4 items
	plain := ansi.Strip(m.View())
	if !strings.Contains(plain, "Open all") {
		t.Errorf("View with multiple items must contain 'Open all' hint\nfull view:\n%s", plain)
	}
}

func TestOverviewLinksOverlayHintNoOpenAllWhenSingleItem(t *testing.T) {
	t.Parallel()
	m := views.NewOverviewLinksOverlay(newTestStyles(t))
	m.SetSize(120, 40)
	m.Open([]views.OverviewSourceItem{
		{Ref: "T-1", Title: "Only ticket", URL: "https://example.com/t/1"},
	}, nil)
	plain := ansi.Strip(m.View())
	if strings.Contains(plain, "Open all") {
		t.Errorf("View with single item must NOT contain 'Open all' hint\nfull view:\n%s", plain)
	}
}