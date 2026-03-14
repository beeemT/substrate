package views_test

import (
	"strings"
	"testing"

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
	for _, want := range []string{"SUB-1 · Investigate overflow", "Source details", "Summary", "Selected items", "Provider: GitHub", "Selected: 2 issues", "acme/rocket#42"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("view = %q, want %q", plain, want)
		}
	}
	if !strings.Contains(plain, "Labels are omitted here because") || !strings.Contains(plain, "multiple source") {
		t.Fatalf("view = %q, want multi-source labels note", plain)
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

	m := views.NewSourceDetailsModel(newTestStyles(t))
	m.SetSize(60, 20)
	m.SetSession(&domain.Session{
		ID:            "wi-1",
		ExternalID:    "SUB-1",
		Source:        "github",
		SourceScope:   domain.ScopeIssues,
		SourceItemIDs: []string{"acme/rocket#42", "acme/rocket#43"},
		Title:         "Investigate overflow",
		Metadata: map[string]any{
			"source_summaries": []domain.SourceSummary{{
				Provider: "github",
				Ref:      "acme/rocket#42",
				Title:    "Fix auth",
				Excerpt:  "Investigate auth timeouts",
			}, {
				Provider: "github",
				Ref:      "acme/rocket#43",
				Title:    "Repair billing",
				Excerpt:  "Stabilize billing retries",
			}},
		},
	})

	plain := ansi.Strip(m.View())
	for _, want := range []string{"Fix auth", "Repair billing", "Investigate auth timeouts", "Stabilize billing retries"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("view = %q, want %q", plain, want)
		}
	}
}
