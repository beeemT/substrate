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
	for _, want := range []string{"Source details", "Work item", "Combine auth and billing fixes into one coordinated rollout.", "Selected items", "acme/rocket#42 · Fix auth", "Provider: GitHub", "Type: Issue", "Labels: bug, backend", "Reporter: alice", "Investigate auth timeouts in the login flow.", "acme/rocket#43 · Repair billing", "Stabilize billing retries and remove duplicate charge errors."} {
		if !strings.Contains(plain, want) {
			t.Fatalf("view = %q, want %q", plain, want)
		}
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
