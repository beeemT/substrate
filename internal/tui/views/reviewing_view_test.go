package views

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/styles"
)

func TestReviewViewFitsRequestedSize(t *testing.T) {
	t.Parallel()

	m := NewReviewModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(64, 12)
	m.SetTitle("SUB-1 · Review overflow handling")
	m.SetRepos([]RepoReviewResult{{
		RepoName: "repository-with-a-long-name",
		Critiques: []domain.Critique{{
			Severity:    domain.CritiqueMajor,
			Description: "A very long critique description that should be clipped to the requested width instead of leaking past the pane boundary.",
			FilePath:    "internal/service/review/overflow_handler.go",
			Suggestion:  "Refactor the renderer to keep the selected critique inside the available viewport width.",
		}},
	}})

	rendered := m.View()
	lines := strings.Split(rendered, "\n")
	if got := len(lines); got != 12 {
		t.Fatalf("line count = %d, want 12\nview:\n%s", got, rendered)
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > 64 {
			t.Fatalf("line %d width = %d, want <= 64\nline: %q", i+1, got, line)
		}
	}
	plain := ansi.Strip(rendered)
	for _, want := range []string{"Reviewing", "repository-with-a-long-name"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("view = %q, want %q", plain, want)
		}
	}
}
