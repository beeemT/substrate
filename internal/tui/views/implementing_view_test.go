package views

import (
	"strings"
	"testing"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/styles"
)

func TestImplementingViewRespectsRequestedHeight(t *testing.T) {
	t.Parallel()

	m := NewImplementingModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(48, 12)
	m.SetTitle("SUB-1 · Implement overflow fix")
	m.SetRepos([]RepoProgress{{
		Name:      "repo-1",
		SubPlanID: "sp-1",
		SessionID: "sess-1",
		Status:    domain.SubPlanInProgress,
	}})

	lines := strings.Split(m.View(), "\n")
	if got := len(lines); got != 12 {
		t.Fatalf("line count = %d, want 12", got)
	}
	if !strings.Contains(lines[len(lines)-1], "[Tab] Cycle repos") {
		t.Fatalf("last line = %q, want implementing hints", lines[len(lines)-1])
	}
}
