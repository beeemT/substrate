package views

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/styles"
)

func TestQuestionViewFitsRequestedSize(t *testing.T) {
	t.Parallel()

	m := NewQuestionModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(48, 18)
	m.SetTitle("SUB-1 · Investigate overflow")
	m.SetQuestion(domain.Question{
		ID:      "q-1",
		Stage:   domain.TaskPhaseImplementation,
		Content: "A very long agent question that should wrap within the bordered callout instead of overflowing the available pane width.",
		Context: "repository-name-with-extra-context and a second clause that should still fit when rendered.",
	}, "A proposed answer that is also deliberately long so the answer card has to wrap cleanly.", true)

	rendered := m.View()
	lines := strings.Split(rendered, "\n")
	if got := len(lines); got != 18 {
		t.Fatalf("line count = %d, want 18\nview:\n%s", got, rendered)
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > 48 {
			t.Fatalf("line %d width = %d, want <= 48\nline: %q", i+1, got, line)
		}
	}
	plain := ansi.Strip(rendered)
	for _, want := range []string{"Agent question:", "Foreman's proposed answer", "Your reply"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("view = %q, want %q", plain, want)
		}
	}
}

func TestPlanningQuestionViewUsesPlannerCopyAndFitsNarrowSize(t *testing.T) {
	t.Parallel()

	recommended := 0
	m := NewQuestionModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(42, 16)
	m.SetTitle("SUB-2 · Plan routing")
	m.SetQuestion(domain.Question{
		ID:      "q-plan",
		Stage:   domain.TaskPhasePlanning,
		Content: "Which migration approach should the planner use?",
		Structured: &domain.StructuredQuestionSet{Questions: []domain.StructuredQuestion{{
			ID:               "approach",
			Header:           "Approach",
			Question:         "Which migration approach should the planner use?",
			RecommendedIndex: &recommended,
			Options: []domain.QuestionOption{
				{Label: "Full cutover", Description: "Remove old APIs"},
				{Label: "Compat", Description: "Keep aliases"},
			},
		}}},
	}, "", false)

	rendered := m.View()
	lines := strings.Split(rendered, "\n")
	if got := len(lines); got != 16 {
		t.Fatalf("line count = %d, want 16\nview:\n%s", got, rendered)
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > 42 {
			t.Fatalf("line %d width = %d, want <= 42\nline: %q", i+1, got, line)
		}
	}
	plain := ansi.Strip(rendered)
	for _, want := range []string{"Planning question", "The planner needs your input", "Full cutover", "Reply to planner"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("view = %q, want %q", plain, want)
		}
	}
	if strings.Contains(plain, "Foreman") {
		t.Fatalf("planning question view should not mention Foreman: %q", plain)
	}
}
