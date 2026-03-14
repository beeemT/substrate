package views

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

func TestSourceDetailsNoticeFitsRequestedSize(t *testing.T) {
	t.Parallel()

	m := NewSourceDetailsModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(48, 18)
	m.SetSession(&domain.Session{
		ID:            "wi-1",
		ExternalID:    "SUB-1",
		Source:        "github",
		SourceScope:   domain.ScopeIssues,
		SourceItemIDs: []string{"acme/rocket#42"},
		Title:         "Investigate overflow",
	})
	m.SetNotice(&sourceDetailsNotice{
		Title:   "Question waiting for answer",
		Body:    "repo-a is paused until someone answers the escalated question. Question: Need approval before continuing",
		Hint:    "Press [Enter] to open the overview.",
		Variant: components.CalloutWarning,
	})

	rendered := m.View()
	plain := ansi.Strip(rendered)
	for _, want := range []string{"Question waiting for answer", "repo-a is paused until someone answers", "Press [Enter] to open the overview."} {
		if !strings.Contains(plain, want) {
			t.Fatalf("view = %q, want %q", plain, want)
		}
	}
	if m.notice == nil || !strings.Contains(m.notice.Body, "Need approval before continuing") {
		t.Fatalf("notice = %#v, want question body with escalated question text", m.notice)
	}
	hints := m.KeybindHints()
	if len(hints) < 2 || hints[0].Label != "Scroll" || hints[1].Label != "Open overview" {
		t.Fatalf("keybind hints = %#v, want scroll + open overview", hints)
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

func TestSourceDetailsNoticeFromOverviewAction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		action      OverviewActionCard
		wantTitle   string
		wantBody    string
		wantHint    string
		wantVariant components.CalloutVariant
	}{
		{
			name:        "plan review",
			action:      OverviewActionCard{Kind: overviewActionPlanReview, Title: "Plan review required", Affected: []string{"repo-a", "repo-b"}},
			wantTitle:   "Plan review required",
			wantBody:    "Affected repos: 2.",
			wantHint:    "Press [Enter] to open the overview.",
			wantVariant: components.CalloutWarning,
		},
		{
			name:        "question",
			action:      OverviewActionCard{Kind: overviewActionQuestion, Title: "Question waiting for answer", QuestionRepo: "repo-a", Blocked: "Need approval before continuing"},
			wantTitle:   "Question waiting for answer",
			wantBody:    "Need approval before continuing",
			wantHint:    "Press [Enter] to open the overview.",
			wantVariant: components.CalloutWarning,
		},
		{
			name:        "interrupted",
			action:      OverviewActionCard{Kind: overviewActionInterrupted, Title: "Interrupted task needs recovery", Blocked: "repo-a"},
			wantTitle:   "Interrupted task needs recovery",
			wantBody:    "resumed or abandoned",
			wantHint:    "Press [Enter] to open the overview.",
			wantVariant: components.CalloutWarning,
		},
		{
			name:        "reviewing",
			action:      OverviewActionCard{Kind: overviewActionReviewing, Title: "Review requires decision", Affected: []string{"repo-a"}},
			wantTitle:   "Review requires decision",
			wantBody:    "human decision in repo-a",
			wantHint:    "Press [Enter] to open the overview and inspect the review.",
			wantVariant: components.CalloutWarning,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			notice := sourceDetailsNoticeFromOverviewAction(tc.action)
			if notice == nil {
				t.Fatal("expected source-details notice")
			}
			if notice.Title != tc.wantTitle {
				t.Fatalf("title = %q, want %q", notice.Title, tc.wantTitle)
			}
			if !strings.Contains(notice.Body, tc.wantBody) {
				t.Fatalf("body = %q, want substring %q", notice.Body, tc.wantBody)
			}
			if notice.Hint != tc.wantHint {
				t.Fatalf("hint = %q, want %q", notice.Hint, tc.wantHint)
			}
			if notice.Variant != tc.wantVariant {
				t.Fatalf("variant = %v, want %v", notice.Variant, tc.wantVariant)
			}
		})
	}
}
