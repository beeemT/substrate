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
