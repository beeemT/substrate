package views_test

import (
	"strings"
	"testing"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/views"
	"github.com/charmbracelet/x/ansi"
)

func TestSidebarArtifactsEntryRendersCorrectly(t *testing.T) {
	t.Parallel()

	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetHeight(20)
	m.SetEntries([]views.SidebarEntry{
		{Kind: views.SidebarEntryTaskOverview, WorkItemID: "wi-1", Title: "Test session", State: domain.SessionImplementing},
		{Kind: views.SidebarEntryTaskSourceDetails, WorkItemID: "wi-1", SessionID: "__source_details__", Title: "Source details"},
		{Kind: views.SidebarEntryTaskArtifacts, WorkItemID: "wi-1", SessionID: "__artifacts__", Title: "Pull requests & merge requests", SubtitleText: "3 artifacts"},
		{Kind: views.SidebarEntryGroupHeader, GroupTitle: "Planning"},
		{Kind: views.SidebarEntryTaskSession, WorkItemID: "wi-1", SessionID: "s1", RepositoryName: "Planning"},
	})

	// Select the artifacts entry.
	m.MoveDown() // → overview
	m.MoveDown() // → source details
	m.MoveDown() // → artifacts

	sel := m.Selected()
	if sel == nil {
		t.Fatal("expected a selected entry")
	}
	if sel.Kind != views.SidebarEntryTaskArtifacts {
		t.Fatalf("selected kind = %v, want SidebarEntryTaskArtifacts", sel.Kind)
	}
	if sel.SessionID != "__artifacts__" {
		t.Fatalf("selected session ID = %q, want __artifacts__", sel.SessionID)
	}

	// Verify the view renders correctly.
	rendered := m.View()
	plain := ansi.Strip(rendered)
	if !strings.Contains(plain, "Artifacts") {
		t.Error("view missing 'Artifacts' title prefix")
	}
	if !strings.Contains(plain, "Pull requests & merge requests") {
		t.Error("view missing artifacts title")
	}
}

func TestSidebarArtifactsEntryStatusIcon(t *testing.T) {
	t.Parallel()

	st := makeSidebarStyles()

	tests := []struct {
		name     string
		aggState string
		wantIcon string
	}{
		{"no reviews", "", "◌"},
		{"approved", "approved", "✓"},
		{"changes requested", "changes_requested", "◐"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			entry := views.SidebarEntry{
				Kind:                         views.SidebarEntryTaskArtifacts,
				WorkItemID:                   "wi-1",
				SessionID:                    "__artifacts__",
				Title:                        "Artifacts",
				ArtifactAggregateReviewState: tt.aggState,
			}
			icon := entry.StatusIcon(st)
			plain := ansi.Strip(icon)
			if plain != tt.wantIcon {
				t.Fatalf("status icon = %q, want %q", plain, tt.wantIcon)
			}
		})
	}
}

func TestSidebarNavigationIncludesArtifactsEntry(t *testing.T) {
	t.Parallel()

	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetHeight(30)
	m.SetEntries([]views.SidebarEntry{
		{Kind: views.SidebarEntryTaskOverview, WorkItemID: "wi-1"},
		{Kind: views.SidebarEntryTaskArtifacts, WorkItemID: "wi-1", SessionID: "__artifacts__"},
		{Kind: views.SidebarEntryGroupHeader, GroupTitle: "Planning"},
		{Kind: views.SidebarEntryTaskSession, WorkItemID: "wi-1", SessionID: "s1"},
	})

	// Navigate down through entries: overview → artifacts → s1 (skips group header).
	m.MoveDown() // → overview
	m.MoveDown() // → artifacts
	sel := m.Selected()
	if sel == nil || sel.Kind != views.SidebarEntryTaskArtifacts {
		t.Fatalf("second MoveDown should land on artifacts, got %v", sel)
	}

	m.MoveDown() // → s1 (skip group header)
	sel = m.Selected()
	if sel == nil || sel.SessionID != "s1" {
		t.Fatalf("third MoveDown should land on s1, got %v", sel)
	}

	// Navigate back up: s1 → artifacts → overview.
	m.MoveUp()
	sel = m.Selected()
	if sel == nil || sel.Kind != views.SidebarEntryTaskArtifacts {
		t.Fatalf("MoveUp from s1 should land on artifacts, got %v", sel)
	}

	m.MoveUp()
	sel = m.Selected()
	if sel == nil || sel.Kind != views.SidebarEntryTaskOverview {
		t.Fatalf("MoveUp from artifacts should land on overview, got %v", sel)
	}
}

func TestSidebarArtifactsEntryFitsWidth(t *testing.T) {
	t.Parallel()

	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetHeight(20)
	m.SetWidth(34)
	m.SetEntries([]views.SidebarEntry{
		{Kind: views.SidebarEntryTaskOverview, WorkItemID: "wi-1", Title: "Test"},
		{Kind: views.SidebarEntryTaskArtifacts, WorkItemID: "wi-1", SessionID: "__artifacts__", Title: "Pull requests & merge requests", SubtitleText: "30 artifacts"},
	})

	rendered := m.View()
	for i, line := range strings.Split(rendered, "\n") {
		if got := ansi.StringWidth(line); got > 34 {
			t.Fatalf("line %d width = %d, want <= 34\nline: %q", i+1, got, line)
		}
	}
}
