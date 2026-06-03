package views

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
		Stage:   domain.AgentSessionKindImplementation,
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
	for _, want := range []string{"Agent question:", "Foreman's proposed answer", "Your answer"} {
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
		Stage:   domain.AgentSessionKindPlanning,
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

func TestQuestionEnterSubmitsTypedAnswer(t *testing.T) {
	t.Parallel()

	m := NewQuestionModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(60, 18)
	m.SetTitle("SUB-3 · Answer question")
	m.SetQuestion(domain.Question{
		ID:      "q-submit",
		Stage:   domain.AgentSessionKindImplementation,
		Content: "What should the agent do next?",
	}, "The foreman proposal is only reference text.", false)
	m.input.SetValue("  Use the safer migration path.  ")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if updated.inputActive {
		t.Fatal("inputActive = true, want false after submit")
	}

	msgs := runOverlayCmd(t, cmd)
	var got *AnswerQuestionMsg
	for _, msg := range msgs {
		if answer, ok := msg.(AnswerQuestionMsg); ok {
			got = &answer
		}
		if _, ok := msg.(SkipQuestionMsg); ok {
			t.Fatalf("cmd emitted SkipQuestionMsg on submit: %#v", msg)
		}
	}
	if got == nil {
		t.Fatalf("cmd messages = %#v, want AnswerQuestionMsg", msgs)
	}
	if got.QuestionID != "q-submit" || got.Answer != "Use the safer migration path." || got.AnsweredBy != "human" {
		t.Fatalf("answer msg = %#v, want typed human answer", *got)
	}
}

func TestQuestionEscClosesWithoutResolving(t *testing.T) {
	t.Parallel()

	m := NewQuestionModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(60, 18)
	m.SetQuestion(domain.Question{ID: "q-esc", Stage: domain.AgentSessionKindImplementation, Content: "Question?"}, "Proposal", false)
	m.input.SetValue("draft answer")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if updated.inputActive {
		t.Fatal("inputActive = true, want false after esc")
	}
	if got := updated.input.Value(); got != "" {
		t.Fatalf("input value = %q, want cleared draft after esc", got)
	}
	for _, msg := range runOverlayCmd(t, cmd) {
		switch msg.(type) {
		case AnswerQuestionMsg, SkipQuestionMsg:
			t.Fatalf("esc emitted resolving message %T", msg)
		}
	}
}

func TestQuestionViewScrollsLongContentAndFits(t *testing.T) {
	t.Parallel()

	lines := make([]string, 0, 30)
	for i := 1; i <= 30; i++ {
		lines = append(lines, "Question detail line "+string(rune('A'+i-1)))
	}

	m := NewQuestionModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(58, 14)
	m.SetTitle("SUB-4 · Long question")
	m.SetQuestion(domain.Question{
		ID:      "q-scroll",
		Stage:   domain.AgentSessionKindImplementation,
		Content: strings.Join(lines, "\n"),
	}, "", false)

	before := m.View()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	after := updated.View()
	if before == after {
		t.Fatalf("view did not change after pgdown\nview:\n%s", before)
	}
	plainAfter := ansi.Strip(after)
	if !strings.Contains(plainAfter, "Question detail line") {
		t.Fatalf("scrolled view lost question content: %q", plainAfter)
	}

	for name, rendered := range map[string]string{"before": before, "after": after} {
		lines := strings.Split(rendered, "\n")
		if got := len(lines); got != 14 {
			t.Fatalf("%s line count = %d, want 14\nview:\n%s", name, got, rendered)
		}
		for i, line := range lines {
			if got := ansi.StringWidth(line); got > 58 {
				t.Fatalf("%s line %d width = %d, want <= 58\nline: %q", name, i+1, got, line)
			}
		}
	}
}
