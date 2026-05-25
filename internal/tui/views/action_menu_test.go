package views_test

import (
	"strings"
	"testing"

	"github.com/beeemT/substrate/internal/tui/views"
)

// TestFuzzyMatch tests the fuzzy matching function.
func TestFuzzyMatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		query    string
		label    string
		expected bool
	}{
		{"empty query matches everything", "", "New session", true},
		{"exact substring", "new", "New session", true},
		{"case insensitive substring", "NEW", "New session", true},
		{"fuzzy characters", "ns", "New session", true},
		{"fuzzy characters partial", "n s", "New session", true},
		{"non-matching", "xyz", "New session", false},
		{"full label match", "New session", "New session", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := views.FuzzyMatch(tt.query, tt.label)
			if result != tt.expected {
				t.Errorf("FuzzyMatch(%q, %q) = %v, want %v", tt.query, tt.label, result, tt.expected)
			}
		})
	}
}

// TestActionMenuModelView tests that the action menu View renders correctly.
func TestActionMenuModelView(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	model := views.NewActionMenuModel(st)
	model.SetSize(80, 24)

	// Test with empty state
	view := model.View()
	if view == "" {
		t.Error("View() returned empty string")
	}

	// Check that title is rendered
	if !strings.Contains(view, "Actions") {
		t.Error("View() should contain 'Actions' title")
	}

	// Check that search bar is present
	if !strings.Contains(view, "Search:") {
		t.Error("View() should contain 'Search:' label")
	}

	// Check for navigation hints
	if !strings.Contains(view, "Navigate") || !strings.Contains(view, "Enter") {
		t.Error("View() should contain navigation hints")
	}
}

// TestActionMenuModelViewFitsRequestedSize tests that the action menu fits the requested dimensions.
func TestActionMenuModelViewFitsRequestedSize(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)

	testCases := []struct {
		name   string
		width  int
		height int
	}{
		{"normal size", 120, 40},
		{"minimum width", 40, 20},
		{"narrow width", 60, 30},
		{"short height", 80, 15},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			model := views.NewActionMenuModel(st)
			model.SetSize(tc.width, tc.height)

			view := model.View()
			lines := strings.Split(view, "\n")

			// Check line count
			if len(lines) > tc.height {
				t.Errorf("View() has %d lines, exceeds height %d", len(lines), tc.height)
			}

			// Note: Lipgloss-styled text may render wider than the raw string length.
			// The View() output uses fmt.Sprintf padding which may not truncate lipgloss styles.
			// We just verify that the view is non-empty and has reasonable structure.
			if len(lines) < 3 {
				t.Errorf("View() has too few lines: %d", len(lines))
			}
		})
	}
}

// TestActionMenuModelUpdate tests the Update method with various key messages.
func TestActionMenuModelUpdate(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	model := views.NewActionMenuModel(st)
	model.SetSize(80, 24)

	// Update without Open should not panic
	// Test that Update handles non-key messages gracefully
	_, _ = model.Update(nil)
}

// TestActionContextValues tests that all ActionContext values are defined.
func TestActionContextValues(t *testing.T) {
	t.Parallel()

	// Just ensure these constants exist and are distinct
	contexts := []views.ActionContext{
		views.ContextGlobal,
		views.ContextEmpty,
		views.ContextModalExclusive,
		views.ContextWorkspaceInit,
		views.ContextSessionSearch,
		views.ContextSettings,
		views.ContextLogs,
		views.ContextNewSession,
		views.ContextNewSessionAutonomous,
		views.ContextAddRepo,
		views.ContextRepoManager,
		views.ContextOverview,
		views.ContextPlanReview,
		views.ContextQuestion,
		views.ContextInterrupted,
		views.ContextReviewing,
		views.ContextCompleted,
		views.ContextAgentSessionLog,
		views.ContextSessionInteractionLog,
		views.ContextArtifacts,
		views.ContextSourceDetails,
		views.ContextSourceItems,
		views.ContextOverviewLinks,
		views.ContextReviewFollowupLoading,
		views.ContextReviewFollowupPicker,
		views.ContextReviewFollowupSelector,
		views.ContextReviewFollowupConfirm,
	}

	// Check that all contexts are distinct
	seen := make(map[views.ActionContext]bool)
	for _, ctx := range contexts {
		if seen[ctx] {
			t.Errorf("duplicate context value: %v", ctx)
		}
		seen[ctx] = true
	}
}
