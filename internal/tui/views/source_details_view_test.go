package views_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/views"
)

func TestSourceDetailsModelViewShowsSourceDetailsAndFitsSize(t *testing.T) {
	t.Parallel()

	m := views.NewSourceDetailsModel(newTestStyles(t))
	m.SetSize(48, 18)
	m.SetSession(&domain.Session{
		ID:            "wi-1",
		ExternalID:    "SUB-1",
		Source:        "github",
		SourceScope:   domain.ScopeIssues,
		SourceItemIDs: []string{"acme/rocket#42", "acme/rocket#43"},
		Title:         "Investigate overflow",
		Labels:        []string{"bug", "backend"},
		Metadata: map[string]any{
			"tracker_refs": []domain.TrackerReference{
				{Provider: "github", Kind: "issue", Owner: "acme", Repo: "rocket", Number: 42},
				{Provider: "github", Kind: "issue", Owner: "acme", Repo: "rocket", Number: 43},
			},
		},
	})

	rendered := m.View()
	plain := ansi.Strip(rendered)
	for _, want := range []string{"Investigate overflow", "Source details", "Summary", "Selected items", "Provider: GitHub", "Selected: 2 issues", "acme/rocket#42"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("view = %q, want %q", plain, want)
		}
	}
	if strings.Contains(plain, "No source details available.") {
		t.Fatalf("view = %q, want rendered source details content", plain)
	}

	lines := strings.Split(rendered, "\n")
	if got := len(lines); got != 18 {
		t.Fatalf("line count = %d, want 18", got)
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > 48 {
			t.Fatalf("line %d width = %d, want <= 48\nline: %q", i+1, got, line)
		}
	}
}

func TestSourceDetailsUsesDurableSourceSummariesWhenAvailable(t *testing.T) {
	t.Parallel()

	updatedAt := time.Date(2024, 1, 3, 4, 5, 0, 0, time.UTC)
	m := views.NewSourceDetailsModel(newTestStyles(t))
	m.SetSize(72, 60)
	m.SetSession(&domain.Session{
		ID:            "wi-1",
		ExternalID:    "SUB-1",
		Source:        "github",
		SourceScope:   domain.ScopeIssues,
		SourceItemIDs: []string{"acme/rocket#42", "acme/rocket#43"},
		Title:         "Investigate overflow",
		Description:   "## Work item plan\n\nCombine auth and billing fixes into one coordinated rollout.",
		Metadata: map[string]any{
			"source_summaries": []domain.SourceSummary{{
				Provider:    "github",
				Kind:        "issue",
				Ref:         "acme/rocket#42",
				Title:       "Fix auth",
				Description: "## Summary\n\nInvestigate auth timeouts in the login flow.",
				Excerpt:     "Investigate auth timeouts in the login flow.",
				State:       "open",
				Labels:      []string{"bug", "backend"},
				Container:   "acme/rocket",
				URL:         "https://github.com/acme/rocket/issues/42",
				UpdatedAt:   &updatedAt,
				Metadata:    []domain.SourceMetadataField{{Label: "Reporter", Value: "alice"}},
			}, {
				Provider:    "github",
				Kind:        "issue",
				Ref:         "acme/rocket#43",
				Title:       "Repair billing",
				Description: "Stabilize billing retries and remove duplicate charge errors.",
				Excerpt:     "Stabilize billing retries and remove duplicate charge errors.",
				State:       "open",
				Labels:      []string{"payments"},
				Container:   "acme/rocket",
				URL:         "https://github.com/acme/rocket/issues/43",
				Metadata:    []domain.SourceMetadataField{{Label: "Reporter", Value: "bob"}},
			}},
		},
	})

	rendered := m.View()
	plain := ansi.Strip(rendered)

	// Collapsed accordion: should show headings and state tags.
	for _, want := range []string{"Source details", "Work item", "Combine auth and billing fixes into one coordinated rollout.", "Selected items", "acme/rocket#42 · Fix auth", "acme/rocket#43 · Repair billing", "[open]"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("view missing %q in collapsed accordion", want)
		}
	}

	// Expand first item to see metadata.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	plain = ansi.Strip(m.View())
	for _, want := range []string{"Provider: GitHub", "Type: Issue", "Labels: bug, backend", "Reporter: alice", "Investigate auth timeouts in the login flow."} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expanded first item missing %q", want)
		}
	}

	// Expand second item.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	plain = ansi.Strip(m.View())
	if !strings.Contains(plain, "Repair billing") {
		t.Fatal("expanded second item missing heading")
	}
	if !strings.Contains(plain, "Reporter: bob") {
		t.Fatal("expanded second item missing Reporter: bob")
	}

	for i, line := range strings.Split(rendered, "\n") {
		if got := ansi.StringWidth(line); got > 72 {
			t.Fatalf("line %d width = %d, want <= 72\nline: %q", i+1, got, line)
		}
	}
}

func TestSourceDetailsRefreshPreservesScrollForSameSession(t *testing.T) {
	t.Parallel()

	summaries := make([]domain.SourceSummary, 0, 8)
	for i := 1; i <= 8; i++ {
		summaries = append(summaries, domain.SourceSummary{
			Provider:    "github",
			Kind:        "issue",
			Ref:         fmt.Sprintf("acme/rocket#%d", i),
			Title:       fmt.Sprintf("Issue %d", i),
			Description: strings.Repeat("Long description for scrolling. ", 8),
			Excerpt:     strings.Repeat("Long description for scrolling. ", 4),
			State:       "open",
			Container:   "acme/rocket",
		})
	}
	session := &domain.Session{
		ID:          "wi-1",
		ExternalID:  "SUB-1",
		Source:      "github",
		SourceScope: domain.ScopeIssues,
		Title:       "Investigate overflow",
		Metadata:    map[string]any{"source_summaries": summaries},
	}

	m := views.NewSourceDetailsModel(newTestStyles(t))
	m.SetSize(48, 18)
	m.SetSession(session)
	top := ansi.Strip(m.View())
	for range 6 {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = updated
	}
	scrolled := ansi.Strip(m.View())
	if scrolled == top {
		t.Fatal("expected scrolled source-details view to differ from the top of the document")
	}
	m.SetSession(session)
	if refreshed := ansi.Strip(m.View()); refreshed != scrolled {
		t.Fatalf("refreshed view changed after same-session update\nscrolled:\n%s\n\nrefreshed:\n%s", scrolled, refreshed)
	}
}

func TestSourceDetailsDurableSummariesFitRequestedSize(t *testing.T) {
	t.Parallel()

	updatedAt := time.Date(2024, 1, 3, 4, 5, 0, 0, time.UTC)
	m := views.NewSourceDetailsModel(newTestStyles(t))
	m.SetSize(48, 18)
	m.SetSession(&domain.Session{
		ID:          "wi-1",
		ExternalID:  "SUB-1",
		Source:      "github",
		SourceScope: domain.ScopeIssues,
		Title:       "Investigate overflow",
		Metadata: map[string]any{
			"source_summaries": []domain.SourceSummary{
				{
					Provider:    "github",
					Kind:        "issue",
					Ref:         "acme/rocket#42",
					Title:       "Fix auth timeouts in the login and callback flow",
					Description: strings.Repeat("Investigate auth timeouts in the login flow. ", 8),
					Excerpt:     strings.Repeat("Investigate auth timeouts in the login flow. ", 4),
					State:       "open",
					Labels:      []string{"bug", "backend"},
					Container:   "acme/rocket",
					URL:         "https://github.com/acme/rocket/issues/42",
					UpdatedAt:   &updatedAt,
				},
				{
					Provider:    "github",
					Kind:        "issue",
					Ref:         "acme/rocket#43",
					Title:       "Repair billing retries and duplicate charge handling",
					Description: strings.Repeat("Stabilize billing retries and duplicate charge handling. ", 8),
					Excerpt:     strings.Repeat("Stabilize billing retries and duplicate charge handling. ", 4),
					State:       "open",
					Labels:      []string{"payments"},
					Container:   "acme/rocket",
					URL:         "https://github.com/acme/rocket/issues/43",
				},
			},
		},
	})

	rendered := m.View()
	plain := ansi.Strip(rendered)
	for _, want := range []string{"Source details", "acme/rocket#42", "Fix auth"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("view = %q, want %q", plain, want)
		}
	}
	lines := strings.Split(rendered, "\n")
	if got := len(lines); got > 18 {
		t.Fatalf("line count = %d, want <= 18", got)
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > 48 {
			t.Fatalf("line %d width = %d, want <= 48\nline: %q", i+1, got, line)
		}
	}
}

func TestSourceDetailsOpenInBrowserSingleItem(t *testing.T) {
	t.Parallel()

	m := views.NewSourceDetailsModel(newTestStyles(t))
	m.SetSize(48, 18)
	m.SetSession(&domain.Session{
		ID:            "wi-1",
		Source:        "github",
		SourceScope:   domain.ScopeIssues,
		SourceItemIDs: []string{"acme/rocket#42"},
		Title:         "Fix auth",
		Metadata: map[string]any{
			"source_summaries": []domain.SourceSummary{{
				Provider: "github",
				Kind:     "issue",
				Ref:      "acme/rocket#42",
				Title:    "Fix auth",
				URL:      "https://github.com/acme/rocket/issues/42",
			}},
		},
	})

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	_ = updated

	if cmd == nil {
		t.Fatal("expected non-nil cmd for single-URL source item")
	}

	msg := cmd()
	openMsg, ok := msg.(views.OpenExternalURLMsg)
	if !ok {
		t.Fatalf("expected OpenExternalURLMsg, got %T", msg)
	}

	const wantURL = "https://github.com/acme/rocket/issues/42"
	if openMsg.URL != wantURL {
		t.Fatalf("URL = %q, want %q", openMsg.URL, wantURL)
	}
}

func TestSourceDetailsOpenInBrowserMultiItemFocused(t *testing.T) {
	t.Parallel()

	m := views.NewSourceDetailsModel(newTestStyles(t))
	m.SetSize(48, 18)
	m.SetSession(&domain.Session{
		ID:            "wi-2",
		Source:        "github",
		SourceScope:   domain.ScopeIssues,
		SourceItemIDs: []string{"acme/rocket#42", "acme/rocket#43"},
		Title:         "Fix multiple issues",
		Metadata: map[string]any{
			"source_summaries": []domain.SourceSummary{
				{
					Provider: "github",
					Kind:     "issue",
					Ref:      "acme/rocket#42",
					Title:    "Fix auth",
					URL:      "https://github.com/acme/rocket/issues/42",
				},
				{
					Provider: "github",
					Kind:     "issue",
					Ref:      "acme/rocket#43",
					Title:    "Repair billing",
					URL:      "https://github.com/acme/rocket/issues/43",
				},
			},
		},
	})

	// Cursor starts at first item — 'o' opens that item's URL.
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	m = updated
	if cmd == nil {
		t.Fatal("expected non-nil cmd for focused item open")
	}
	msg := cmd()
	openMsg, ok := msg.(views.OpenExternalURLMsg)
	if !ok {
		t.Fatalf("expected OpenExternalURLMsg, got %T", msg)
	}
	if openMsg.URL != "https://github.com/acme/rocket/issues/42" {
		t.Fatalf("URL = %q, want first item URL", openMsg.URL)
	}

	// Move to second item, open it.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if cmd == nil {
		t.Fatal("expected non-nil cmd for second item open")
	}
	msg = cmd()
	openMsg, ok = msg.(views.OpenExternalURLMsg)
	if !ok {
		t.Fatalf("expected OpenExternalURLMsg, got %T", msg)
	}
	if openMsg.URL != "https://github.com/acme/rocket/issues/43" {
		t.Fatalf("URL = %q, want second item URL", openMsg.URL)
	}
}

func TestSourceDetailsOpenInBrowserNoURL(t *testing.T) {
	t.Parallel()

	m := views.NewSourceDetailsModel(newTestStyles(t))
	m.SetSize(48, 18)
	m.SetSession(&domain.Session{
		ID:            "wi-3",
		Source:        "github",
		SourceScope:   domain.ScopeIssues,
		SourceItemIDs: []string{"acme/rocket#42"},
		Title:         "No URL item",
		Metadata: map[string]any{
			"source_summaries": []domain.SourceSummary{{
				Provider: "github",
				Kind:     "issue",
				Ref:      "acme/rocket#42",
				Title:    "Fix auth",
			}},
		},
	})

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if cmd != nil {
		t.Fatal("expected nil cmd when source items have no URL")
	}
}

func TestSourceDetailsKeybindHintsIncludeOpenWhenURLAvailable(t *testing.T) {
	t.Parallel()

	m := views.NewSourceDetailsModel(newTestStyles(t))
	m.SetSize(48, 18)
	m.SetSession(&domain.Session{
		ID:            "wi-4",
		Source:        "github",
		SourceScope:   domain.ScopeIssues,
		SourceItemIDs: []string{"acme/rocket#42"},
		Title:         "With URL",
		Metadata: map[string]any{
			"source_summaries": []domain.SourceSummary{{
				Provider: "github",
				Kind:     "issue",
				Ref:      "acme/rocket#42",
				Title:    "Fix auth",
				URL:      "https://github.com/acme/rocket/issues/42",
			}},
		},
	})

	hints := m.KeybindHints()
	found := false
	for _, h := range hints {
		if h.Key == "o" && h.Label == "Open in browser" {
			found = true
			break
		}
	}

	if !found {
		t.Fatalf("expected keybind hint for 'o', got %v", hints)
	}
}

func TestSourceDetailsKeybindHintsExcludeOpenWhenNoURL(t *testing.T) {
	t.Parallel()

	m := views.NewSourceDetailsModel(newTestStyles(t))
	m.SetSize(48, 18)
	m.SetSession(&domain.Session{
		ID:          "wi-5",
		Source:      "github",
		SourceScope: domain.ScopeIssues,
		Title:       "No summaries",
	})

	hints := m.KeybindHints()
	for _, h := range hints {
		if h.Key == "o" {
			t.Fatalf("unexpected keybind hint for 'o' when no URL available: %v", hints)
		}
	}
}

func TestSourceDetailsAccordionExpandCollapse(t *testing.T) {
	t.Parallel()

	m := views.NewSourceDetailsModel(newTestStyles(t))
	m.SetSize(80, 60)
	m.SetSession(&domain.Session{
		ID:            "wi-1",
		Source:        "github",
		SourceScope:   domain.ScopeIssues,
		SourceItemIDs: []string{"acme/rocket#42", "acme/rocket#43"},
		Title:         "Two items",
		Metadata: map[string]any{
			"source_summaries": []domain.SourceSummary{
				{Provider: "github", Kind: "issue", Ref: "acme/rocket#42", Title: "Fix auth", State: "open"},
				{Provider: "github", Kind: "issue", Ref: "acme/rocket#43", Title: "Repair billing", State: "open"},
			},
		},
	})

	plain := ansi.Strip(m.View())
	// Items visible as collapsed rows.
	if !strings.Contains(plain, "acme/rocket#42 · Fix auth") {
		t.Fatal("collapsed view missing first item heading")
	}
	// Metadata not visible when collapsed.
	if strings.Contains(plain, "Type: Issue") {
		t.Fatal("metadata should not be visible in collapsed state")
	}

	// Expand first item.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	plain = ansi.Strip(m.View())
	if !strings.Contains(plain, "Type: Issue") {
		t.Fatal("expected metadata after expanding first item")
	}

	// Collapse first item.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	plain = ansi.Strip(m.View())
	if strings.Contains(plain, "Type: Issue") {
		t.Fatal("metadata should be hidden after collapsing")
	}
}

func TestSourceDetailsAccordionRightArrowExpands(t *testing.T) {
	t.Parallel()

	m := views.NewSourceDetailsModel(newTestStyles(t))
	m.SetSize(80, 60)
	m.SetSession(&domain.Session{
		ID:     "wi-1",
		Source: "github",
		Title:  "Two items",
		Metadata: map[string]any{
			"source_summaries": []domain.SourceSummary{
				{Provider: "github", Kind: "issue", Ref: "#42", Title: "A", State: "open"},
				{Provider: "github", Kind: "issue", Ref: "#43", Title: "B", State: "open"},
			},
		},
	})

	// Right arrow expands.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	plain := ansi.Strip(m.View())
	if !strings.Contains(plain, "Provider: GitHub") {
		t.Fatal("right arrow did not expand item")
	}

	// Right on already expanded is no-op.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	plain = ansi.Strip(m.View())
	if !strings.Contains(plain, "Provider: GitHub") {
		t.Fatal("right on expanded should be no-op")
	}
}

func TestSourceDetailsAccordionCursorNavigation(t *testing.T) {
	t.Parallel()

	m := views.NewSourceDetailsModel(newTestStyles(t))
	m.SetSize(80, 60)
	m.SetSession(&domain.Session{
		ID:     "wi-1",
		Source: "github",
		Title:  "Three items",
		Metadata: map[string]any{
			"source_summaries": []domain.SourceSummary{
				{Provider: "github", Ref: "#1", Title: "First"},
				{Provider: "github", Ref: "#2", Title: "Second"},
				{Provider: "github", Ref: "#3", Title: "Third"},
			},
		},
	})

	// Move to second, expand to verify cursor.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	plain := ansi.Strip(m.View())
	if !strings.Contains(plain, "Ref: #2") {
		t.Fatal("cursor should be on second item after down")
	}

	// Move back up, expand first to verify.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	plain = ansi.Strip(m.View())
	if !strings.Contains(plain, "Ref: #1") {
		t.Fatal("cursor should be on first item after up")
	}
}

func TestSourceDetailsAccordionCursorClamps(t *testing.T) {
	t.Parallel()

	m := views.NewSourceDetailsModel(newTestStyles(t))
	m.SetSize(80, 40)
	m.SetSession(&domain.Session{
		ID:     "wi-1",
		Source: "github",
		Title:  "Two items",
		Metadata: map[string]any{
			"source_summaries": []domain.SourceSummary{
				{Provider: "github", Ref: "#1", Title: "First"},
				{Provider: "github", Ref: "#2", Title: "Second"},
			},
		},
	})

	// Move up past beginning — should stay at first.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	plain := ansi.Strip(m.View())
	if !strings.Contains(plain, "Ref: #1") {
		t.Fatal("cursor should clamp to first item")
	}

	// Move down past end — should stay at last.
	for range 5 {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	plain = ansi.Strip(m.View())
	if !strings.Contains(plain, "Ref: #2") {
		t.Fatal("cursor should clamp to last item")
	}
}

func TestSourceDetailsAccordionMultipleExpanded(t *testing.T) {
	t.Parallel()

	m := views.NewSourceDetailsModel(newTestStyles(t))
	m.SetSize(80, 80)
	m.SetSession(&domain.Session{
		ID:     "wi-1",
		Source: "github",
		Title:  "Two items",
		Metadata: map[string]any{
			"source_summaries": []domain.SourceSummary{
				{Provider: "github", Ref: "#1", Title: "First", State: "open"},
				{Provider: "github", Ref: "#2", Title: "Second", State: "merged"},
			},
		},
	})

	// Expand first.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	// Move to second and expand.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})

	plain := ansi.Strip(m.View())
	if !strings.Contains(plain, "Ref: #1") {
		t.Fatal("first expanded card missing")
	}
	if !strings.Contains(plain, "Ref: #2") {
		t.Fatal("second expanded card missing")
	}
}

func TestSourceDetailsSingleItemRendersInline(t *testing.T) {
	t.Parallel()

	m := views.NewSourceDetailsModel(newTestStyles(t))
	m.SetSize(80, 30)
	m.SetSession(&domain.Session{
		ID:            "wi-1",
		Source:        "github",
		SourceScope:   domain.ScopeIssues,
		SourceItemIDs: []string{"acme/rocket#42"},
		Title:         "Fix auth",
		Metadata: map[string]any{
			"source_summaries": []domain.SourceSummary{{
				Provider:    "github",
				Kind:        "issue",
				Ref:         "acme/rocket#42",
				Title:       "Fix auth",
				Description: "Look into the timeout bug.",
				State:       "open",
				URL:         "https://github.com/acme/rocket/issues/42",
			}},
		},
	})

	plain := ansi.Strip(m.View())
	// Single item: rendered inline with full metadata, no accordion chrome.
	for _, want := range []string{"Provider: GitHub", "Type: Issue", "State: open", "Look into the timeout bug."} {
		if !strings.Contains(plain, want) {
			t.Fatalf("single-item view missing %q", want)
		}
	}
}

func TestSourceDetailsNarrowWidthFits(t *testing.T) {
	t.Parallel()

	m := views.NewSourceDetailsModel(newTestStyles(t))
	m.SetSize(30, 10)
	m.SetSession(&domain.Session{
		ID:     "wi-1",
		Source: "github",
		Title:  "Narrow test",
		Metadata: map[string]any{
			"source_summaries": []domain.SourceSummary{
				{Provider: "github", Ref: "#1", Title: "First"},
				{Provider: "github", Ref: "#2", Title: "Second"},
			},
		},
	})

	rendered := m.View()
	lines := strings.Split(rendered, "\n")
	if got := len(lines); got != 10 {
		t.Fatalf("line count = %d, want 10", got)
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > 30 {
			t.Fatalf("line %d width = %d, want <= 30\nline: %q", i+1, got, line)
		}
	}
}
