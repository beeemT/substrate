package views_test

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/views"
)

func testArtifactItems() []views.ArtifactItem {
	return []views.ArtifactItem{
		{
			ID:        "github:acme/auth-svc:#42",
			Provider:  "github",
			Kind:      "PR",
			RepoName:  "acme/auth-svc",
			Ref:       "#42",
			URL:       "https://github.com/acme/auth-svc/pull/42",
			State:     "open",
			Branch:    "feat-config",
			CreatedAt: time.Date(2024, 1, 3, 4, 5, 0, 0, time.UTC),
			UpdatedAt: time.Date(2024, 1, 3, 6, 0, 0, 0, time.UTC),
		},
		{
			ID:        "github:acme/billing:#43",
			Provider:  "github",
			Kind:      "PR",
			RepoName:  "acme/billing",
			Ref:       "#43",
			URL:       "https://github.com/acme/billing/pull/43",
			State:     "draft",
			Branch:    "feat-config",
			Draft:     true,
			CreatedAt: time.Date(2024, 1, 3, 4, 5, 0, 0, time.UTC),
			UpdatedAt: time.Date(2024, 1, 3, 6, 0, 0, 0, time.UTC),
		},
		{
			ID:        "github:acme/gateway:#44",
			Provider:  "github",
			Kind:      "PR",
			RepoName:  "acme/gateway",
			Ref:       "#44",
			URL:       "https://github.com/acme/gateway/pull/44",
			State:     "merged",
			Branch:    "feat-config",
			MergedAt:  timePtr(time.Date(2024, 1, 4, 10, 0, 0, 0, time.UTC)),
			CreatedAt: time.Date(2024, 1, 3, 4, 5, 0, 0, time.UTC),
			UpdatedAt: time.Date(2024, 1, 4, 10, 0, 0, 0, time.UTC),
		},
	}
}

func timePtr(t time.Time) *time.Time { return &t }

func TestArtifactsViewFitsRequestedSize(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(72, 24)
	m.SetItems(testArtifactItems())

	rendered := m.View()
	lines := strings.Split(rendered, "\n")
	if got := len(lines); got != 24 {
		t.Fatalf("line count = %d, want 24", got)
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > 72 {
			t.Fatalf("line %d width = %d, want <= 72\nline: %q", i+1, got, line)
		}
	}
}

func TestArtifactsViewShowsHeaderAndItems(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(80, 30)
	m.SetItems(testArtifactItems())

	plain := ansi.Strip(m.View())
	for _, want := range []string{"Artifacts", "Pull requests and merge requests", "#42", "acme/auth-svc", "#43", "acme/billing", "#44", "acme/gateway"} {
		if !strings.Contains(plain, want) {
			t.Errorf("view missing %q", want)
		}
	}
}

func TestArtifactsViewEmptyState(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(60, 20)
	m.SetItems(nil)

	plain := ansi.Strip(m.View())
	if !strings.Contains(plain, "No artifacts") {
		t.Fatalf("empty state missing 'No artifacts', got: %q", plain)
	}
}

func TestArtifactsViewSingleItemShowsDetailDirectly(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(80, 30)
	m.SetItems(testArtifactItems()[:1])

	plain := ansi.Strip(m.View())
	// Single item should render expanded card directly — check for key-value pairs.
	for _, want := range []string{"Kind: PR", "Repo: acme/auth-svc", "Ref: #42", "Branch: feat-config", "State: open"} {
		if !strings.Contains(plain, want) {
			t.Errorf("single-item detail missing %q", want)
		}
	}
}

func TestArtifactsViewExpandCollapse(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(80, 40)
	m.SetItems(testArtifactItems())

	// 3 items auto-expand on SetItems — check expanded state.
	plain := ansi.Strip(m.View())
	if !strings.Contains(plain, "⌄ #42") {
		t.Fatal("expected auto-expanded state for 3 items")
	}
	if !strings.Contains(plain, "Kind: PR") {
		t.Fatal("expected expanded card to show Kind")
	}

	// Press space to collapse first item.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	plain = ansi.Strip(m.View())
	// First item collapsed (>, not ⌄), but #2 and #3 still expanded.
	if !strings.Contains(plain, "> #42") {
		t.Fatal("first item should be collapsed after space")
	}

	// Press space again to re-expand first item.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	plain = ansi.Strip(m.View())
	if !strings.Contains(plain, "⌄ #42") {
		t.Fatal("first item should be re-expanded after second space")
	}
}

func TestArtifactsViewExpansionSurvivesSetItemsRefresh(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(80, 40)
	items := testArtifactItems()
	m.SetItems(items)

	// 3 items auto-expand on SetItems.
	plain := ansi.Strip(m.View())
	if !strings.Contains(plain, "⌄ #42") {
		t.Fatalf("expected auto-expanded down-caret; view: %q", plain)
	}

	// Collapse via space.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	items[0].State = "merged"
	m.SetItems(items)

	plain = ansi.Strip(m.View())
	if !strings.Contains(plain, "⌄ #42") {
		t.Fatal("expanded item collapsed after refreshed SetItems")
	}
	if !strings.Contains(plain, "⌄ #42") {
		t.Fatalf("expanded row missing down-caret indicator; view: %q", plain)
	}
}

func TestArtifactsViewCollapsedAndExpandedIndicators(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(80, 40)
	m.SetItems(testArtifactItems())

	// 3 items auto-expand.
	plain := ansi.Strip(m.View())
	if !strings.Contains(plain, "⌄ #42") {
		t.Fatalf("expanded row missing down-caret indicator; view: %q", plain)
	}

	// Collapse with space.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	plain = ansi.Strip(m.View())
	if !strings.Contains(plain, "> #42") {
		t.Fatalf("collapsed row missing > indicator; view: %q", plain)
	}
}

func TestArtifactsViewTwoItemsAutoExpand(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(80, 40)
	m.SetItems(testArtifactItems()[:2])

	// 2 items should auto-expand.
	plain := ansi.Strip(m.View())
	if !strings.Contains(plain, "⌄ #42") {
		t.Fatalf("expected auto-expanded down-caret for 2 items; view: %q", plain)
	}
	if !strings.Contains(plain, "⌄ #43") {
		t.Fatalf("expected both items expanded; view: %q", plain)
	}
}

func TestArtifactsViewFourItemsRemainCollapsed(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(80, 40)
	items := []views.ArtifactItem{
		{ID: "1", Provider: "github", Kind: "PR", RepoName: "r", Ref: "#1", URL: "https://x/1", State: "open"},
		{ID: "2", Provider: "github", Kind: "PR", RepoName: "r", Ref: "#2", URL: "https://x/2", State: "open"},
		{ID: "3", Provider: "github", Kind: "PR", RepoName: "r", Ref: "#3", URL: "https://x/3", State: "open"},
		{ID: "4", Provider: "github", Kind: "PR", RepoName: "r", Ref: "#4", URL: "https://x/4", State: "open"},
	}
	m.SetItems(items)

	// 4 items should stay collapsed.
	plain := ansi.Strip(m.View())
	if strings.Contains(plain, "⌄ #1") {
		t.Fatalf("expected collapsed for 4 items; view: %q", plain)
	}
}

func TestArtifactsViewRightArrowExpands(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(80, 40)
	items := []views.ArtifactItem{
		{ID: "1", Provider: "github", Kind: "PR", RepoName: "r", Ref: "#1", URL: "https://x/1", State: "open"},
		{ID: "2", Provider: "github", Kind: "PR", RepoName: "r", Ref: "#2", URL: "https://x/2", State: "open"},
	}
	m.SetItems(items)

	// 2 items auto-expand.
	plain := ansi.Strip(m.View())
	if !strings.Contains(plain, "⌄ #1") {
		t.Fatal("expected auto-expanded for 2 items")
	}

	// Collapse via space, then right arrow re-expands.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	plain = ansi.Strip(m.View())
	if !strings.Contains(plain, "⌄ #1") {
		t.Fatal("right arrow did not expand item")
	}

	// Right arrow on already expanded → noop (still expanded, no crash).
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	plain = ansi.Strip(m.View())
	if !strings.Contains(plain, "Kind: PR") {
		t.Fatal("right arrow on expanded should be noop")
	}
}

func TestArtifactsViewCursorNavigation(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(80, 60)
	items := []views.ArtifactItem{
		{ID: "github:acme/auth-svc:#42", Provider: "github", Kind: "PR", RepoName: "acme/auth-svc", Ref: "#42", URL: "https://github.com/acme/auth-svc/pull/42", State: "open"},
		{ID: "github:acme/billing:#43", Provider: "github", Kind: "PR", RepoName: "acme/billing", Ref: "#43", URL: "https://github.com/acme/billing/pull/43", State: "open"},
		{ID: "github:acme/gateway:#44", Provider: "github", Kind: "PR", RepoName: "acme/gateway", Ref: "#44", URL: "https://github.com/acme/gateway/pull/44", State: "open"},
	}
	m.SetItems(items)

	// 3 items auto-expand on SetItems. Cursor starts at 0.
	plain := ansi.Strip(m.View())
	if !strings.Contains(plain, "Repo: acme/auth-svc") {
		t.Fatal("first item should be expanded")
	}
	if !strings.Contains(plain, "Repo: acme/billing") {
		t.Fatal("second item should be expanded")
	}

	// Collapse all items to test navigation without expansion confusion.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}}) // collapse #42
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}}) // collapse #43
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}}) // collapse #44
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})                        // back to #43

	// Expand second item (#43).
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	plain = ansi.Strip(m.View())
	if !strings.Contains(plain, "Repo: acme/billing") {
		t.Fatal("cursor should show second item expanded")
	}

	// Move up back to first and expand.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	plain = ansi.Strip(m.View())
	if !strings.Contains(plain, "Repo: acme/auth-svc") {
		t.Fatal("cursor should show first item expanded")
	}
}

func TestArtifactsViewCursorClamps(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(80, 40)
	m.SetItems(testArtifactItems())

	// 3 items auto-expand on SetItems — first item is already expanded.
	plain := ansi.Strip(m.View())
	if !strings.Contains(plain, "Repo: acme/auth-svc") {
		t.Fatal("cursor should clamp at first item")
	}

	// Move down past the end — should clamp at last.
	for range 10 {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	plain = ansi.Strip(m.View())
	if !strings.Contains(plain, "Repo: acme/gateway") {
		t.Fatal("cursor should clamp at last item")
	}
}

func TestArtifactsViewOpenURLCommand(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(80, 30)
	m.SetItems(testArtifactItems())

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if cmd == nil {
		t.Fatal("expected command from 'o' key")
	}
	msg := cmd()
	urlMsg, ok := msg.(views.OpenExternalURLMsg)
	if !ok {
		t.Fatalf("expected OpenExternalURLMsg, got %T", msg)
	}
	if urlMsg.URL != "https://github.com/acme/auth-svc/pull/42" {
		t.Fatalf("URL = %q, want first item URL", urlMsg.URL)
	}
}

func TestArtifactsViewNoCommandWhenEmptyURL(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(80, 30)
	m.SetItems([]views.ArtifactItem{{
		Provider: "github",
		Kind:     "PR",
		RepoName: "acme/test",
		Ref:      "#1",
		State:    "open",
	}})

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if cmd != nil {
		t.Fatal("expected no command when URL is empty")
	}
}

func TestArtifactsViewMergedAtShownInExpandedCard(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(80, 30)
	// Use the third item which has MergedAt set.
	m.SetItems(testArtifactItems()[2:3])

	plain := ansi.Strip(m.View())
	if !strings.Contains(plain, "Merged:") {
		t.Fatal("expected Merged line in expanded card for merged PR")
	}
}

func TestArtifactsViewKeybindHints(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(80, 30)

	// Empty — no hints.
	hints := m.KeybindHints()
	if len(hints) != 0 {
		t.Fatalf("expected no hints for empty items, got %d", len(hints))
	}

	// With items — should have navigate + expand + open hints.
	m.SetItems(testArtifactItems())
	hints = m.KeybindHints()
	if len(hints) < 2 {
		t.Fatalf("expected at least 2 hints, got %d", len(hints))
	}

	keys := make([]string, len(hints))
	for i, h := range hints {
		keys[i] = h.Key
	}
	joined := strings.Join(keys, " ")
	if !strings.Contains(joined, "↑↓") {
		t.Error("hints missing navigate")
	}
	if !strings.Contains(joined, "Space") {
		t.Error("hints missing expand/collapse")
	}
}

func TestArtifactsViewNarrowWidthFits(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(30, 10)
	m.SetItems(testArtifactItems())

	rendered := m.View()
	lines := strings.Split(rendered, "\n")
	if got := len(lines); got != 10 {
		t.Fatalf("line count = %d, want 10", got)
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > 30 {
			t.Fatalf("line %d width = %d, want <= 30\nline: %q", i+1, got, line)
		}
	}
}

func TestArtifactsViewMultipleExpanded(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(80, 60)
	m.SetItems(testArtifactItems())

	// 3 items auto-expand on SetItems.
	plain := ansi.Strip(m.View())
	// All should be expanded.
	if !strings.Contains(plain, "Repo: acme/auth-svc") {
		t.Fatal("first expanded card missing")
	}
	if !strings.Contains(plain, "Repo: acme/billing") {
		t.Fatal("second expanded card missing")
	}
	if !strings.Contains(plain, "Repo: acme/gateway") {
		t.Fatal("third expanded card missing")
	}

	// Collapse first item.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	plain = ansi.Strip(m.View())
	if strings.Contains(plain, "Repo: acme/auth-svc") {
		t.Fatal("first card should be collapsed after space")
	}
	if !strings.Contains(plain, "Repo: acme/billing") {
		t.Fatal("second card should remain expanded")
	}
}

func testArtifactItemsWithReviews() []views.ArtifactItem {
	return []views.ArtifactItem{
		{
			Provider: "github",
			Kind:     "PR",
			RepoName: "acme/auth-svc",
			Ref:      "#42",
			URL:      "https://github.com/acme/auth-svc/pull/42",
			State:    "open",
			Branch:   "feat-config",
			Reviews: []views.ArtifactReview{
				{ReviewerLogin: "alice", State: "approved", SubmittedAt: time.Date(2024, 1, 3, 10, 0, 0, 0, time.UTC)},
				{ReviewerLogin: "bob", State: "changes_requested", SubmittedAt: time.Date(2024, 1, 3, 12, 0, 0, 0, time.UTC)},
			},
			CreatedAt: time.Date(2024, 1, 3, 4, 5, 0, 0, time.UTC),
			UpdatedAt: time.Date(2024, 1, 3, 6, 0, 0, 0, time.UTC),
		},
		{
			Provider: "github",
			Kind:     "PR",
			RepoName: "acme/billing",
			Ref:      "#43",
			URL:      "https://github.com/acme/billing/pull/43",
			State:    "open",
			Branch:   "feat-config",
			Reviews: []views.ArtifactReview{
				{ReviewerLogin: "charlie", State: "approved", SubmittedAt: time.Date(2024, 1, 3, 11, 0, 0, 0, time.UTC)},
			},
			CreatedAt: time.Date(2024, 1, 3, 4, 5, 0, 0, time.UTC),
			UpdatedAt: time.Date(2024, 1, 3, 6, 0, 0, 0, time.UTC),
		},
	}
}

func TestArtifactsViewCollapsedRowShowsReviewSummary(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(100, 30)
	m.SetItems(testArtifactItemsWithReviews())

	plain := ansi.Strip(m.View())
	// First item has changes_requested, so collapsed row should show review indicator.
	if !strings.Contains(plain, "review") {
		t.Error("collapsed row missing review summary")
	}
}

func TestArtifactsViewExpandedCardShowsReviews(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(100, 40)
	items := testArtifactItemsWithReviews()
	m.SetItems(items)

	// 2 items auto-expand on SetItems.

	plain := ansi.Strip(m.View())
	// Should show Review section header.
	if !strings.Contains(plain, "Review") {
		t.Error("expanded card missing Review section")
	}
	// Should show reviewer names.
	if !strings.Contains(plain, "alice") {
		t.Error("expanded card missing reviewer alice")
	}
	if !strings.Contains(plain, "bob") {
		t.Error("expanded card missing reviewer bob")
	}
	// Should show review states.
	if !strings.Contains(plain, "approved") {
		t.Error("expanded card missing approved state")
	}
	if !strings.Contains(plain, "changes_requested") {
		t.Error("expanded card missing changes_requested state")
	}
}

func TestArtifactsViewNoReviewSectionWhenNoReviews(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(100, 30)
	// Use items without reviews.
	m.SetItems([]views.ArtifactItem{{
		Provider: "github",
		Kind:     "PR",
		RepoName: "acme/empty",
		Ref:      "#99",
		State:    "open",
		Branch:   "main",
	}})

	plain := ansi.Strip(m.View())
	// Single item renders expanded directly. Should NOT show Review section.
	if strings.Contains(plain, "Review") {
		t.Error("expanded card should not show Review section when no reviews")
	}
}

func TestArtifactsViewUnresolvedThreadsDisplayName(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(100, 30)
	m.SetItems([]views.ArtifactItem{{
		Provider: "gitlab",
		Kind:     "MR",
		RepoName: "group/project",
		Ref:      "!7",
		State:    "open",
		Branch:   "feat",
		Reviews: []views.ArtifactReview{
			{ReviewerLogin: "__unresolved_threads__", State: "changes_requested", SubmittedAt: time.Date(2024, 1, 3, 10, 0, 0, 0, time.UTC)},
		},
	}})

	plain := ansi.Strip(m.View())
	// Should display "unresolved threads" not the raw "__unresolved_threads__".
	if !strings.Contains(plain, "unresolved threads") {
		t.Error("expanded card should show 'unresolved threads' not raw login")
	}
	if strings.Contains(plain, "__unresolved_threads__") {
		t.Error("expanded card should not show raw __unresolved_threads__ login")
	}
}

func TestArtifactsViewReviewFitsWidth(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(60, 30)
	m.SetItems(testArtifactItemsWithReviews())

	// Expand first item.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})

	lines := strings.Split(m.View(), "\n")
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > 60 {
			t.Fatalf("line %d width = %d, want <= 60\nline: %q", i+1, got, line)
		}
	}
}

func testArtifactItemsWithChecks() []views.ArtifactItem {
	return []views.ArtifactItem{
		{
			Provider: "github",
			Kind:     "PR",
			RepoName: "acme/auth-svc",
			Ref:      "#42",
			URL:      "https://github.com/acme/auth-svc/pull/42",
			State:    "open",
			Branch:   "feat-config",
			Checks: []views.ArtifactCheck{
				{Name: "test", Status: "completed", Conclusion: "failure"},
				{Name: "build", Status: "completed", Conclusion: "success"},
				{Name: "lint", Status: "completed", Conclusion: "success"},
			},
			CreatedAt: time.Date(2024, 1, 3, 4, 5, 0, 0, time.UTC),
			UpdatedAt: time.Date(2024, 1, 3, 6, 0, 0, 0, time.UTC),
		},
		{
			Provider: "github",
			Kind:     "PR",
			RepoName: "acme/billing",
			Ref:      "#43",
			URL:      "https://github.com/acme/billing/pull/43",
			State:    "open",
			Branch:   "feat-config",
			Checks: []views.ArtifactCheck{
				{Name: "test", Status: "completed", Conclusion: "success"},
				{Name: "build", Status: "completed", Conclusion: "success"},
			},
			CreatedAt: time.Date(2024, 1, 3, 4, 5, 0, 0, time.UTC),
			UpdatedAt: time.Date(2024, 1, 3, 6, 0, 0, 0, time.UTC),
		},
	}
}

func TestArtifactsViewCollapsedRowShowsCISummary(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(100, 30)
	m.SetItems(testArtifactItemsWithChecks())

	plain := ansi.Strip(m.View())
	// First item has CI failure, so collapsed row should show CI indicator.
	if !strings.Contains(plain, "CI") {
		t.Error("collapsed row missing CI summary")
	}
}

func TestArtifactsViewExpandedCardShowsChecks(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(100, 40)
	m.SetItems(testArtifactItemsWithChecks())

	// 2 items auto-expand on SetItems. Collapse all to test expansion on specific item.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}}) // collapse #42
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}}) // collapse #43
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})                        // back to #42

	// Expand first item.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})

	plain := ansi.Strip(m.View())
	if !strings.Contains(plain, "CI") {
		t.Error("expanded card missing CI section")
	}
	if !strings.Contains(plain, "test") {
		t.Error("expanded card missing check name 'test'")
	}
	if !strings.Contains(plain, "build") {
		t.Error("expanded card missing check name 'build'")
	}
	if !strings.Contains(plain, "failure") {
		t.Error("expanded card missing failure conclusion")
	}
	if !strings.Contains(plain, "success") {
		t.Error("expanded card missing success conclusion")
	}
}

func TestArtifactsViewNoCISectionWhenNoChecks(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(100, 30)
	m.SetItems([]views.ArtifactItem{{
		Provider: "github",
		Kind:     "PR",
		RepoName: "acme/empty",
		Ref:      "#99",
		State:    "open",
		Branch:   "main",
	}})

	plain := ansi.Strip(m.View())
	if strings.Contains(plain, "CI") {
		t.Error("expanded card should not show CI section when no checks")
	}
}

func TestArtifactsViewCIWithInProgressShows(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(100, 30)
	m.SetItems([]views.ArtifactItem{{
		Provider: "github",
		Kind:     "PR",
		RepoName: "acme/running",
		Ref:      "#50",
		State:    "open",
		Branch:   "feat",
		Checks: []views.ArtifactCheck{
			{Name: "test", Status: "in_progress", Conclusion: ""},
		},
	}})

	plain := ansi.Strip(m.View())
	// Single item renders expanded. Should show CI section with in_progress.
	if !strings.Contains(plain, "CI") {
		t.Error("expanded card missing CI section for in-progress check")
	}
	if !strings.Contains(plain, "in_progress") {
		t.Error("expanded card missing in_progress status")
	}
}

func TestArtifactsViewCIFitsWidth(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(60, 30)
	m.SetItems(testArtifactItemsWithChecks())

	// Expand first item.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})

	lines := strings.Split(m.View(), "\n")
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > 60 {
			t.Fatalf("line %d width = %d, want <= 60\nline: %q", i+1, got, line)
		}
	}
}

func TestArtifactsViewShiftOSingleItemOpensDirectly(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(80, 30)
	m.SetItems(testArtifactItems()[:1])

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'O'}})
	if cmd == nil {
		t.Fatal("expected command from 'O' key on single item")
	}
	msg := cmd()
	urlMsg, ok := msg.(views.OpenExternalURLMsg)
	if !ok {
		t.Fatalf("expected OpenExternalURLMsg, got %T", msg)
	}
	if urlMsg.URL != "https://github.com/acme/auth-svc/pull/42" {
		t.Fatalf("URL = %q, want first item URL", urlMsg.URL)
	}
}

func TestArtifactsViewShiftOMultiItemEmitsArtifactLinksMsg(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(80, 30)
	m.SetItems(testArtifactItems())

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'O'}})
	if cmd == nil {
		t.Fatal("expected command from 'O' key on multiple items")
	}
	msg := cmd()
	linksMsg, ok := msg.(views.OpenArtifactLinksMsg)
	if !ok {
		t.Fatalf("expected OpenArtifactLinksMsg, got %T", msg)
	}
	if got := len(linksMsg.Items); got != 3 {
		t.Fatalf("len(Items) = %d, want 3", got)
	}
}

func TestArtifactsViewShiftOEmptyItemsDoesNothing(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(80, 30)
	m.SetItems(nil)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'O'}})
	if cmd != nil {
		t.Fatal("expected no command when items are empty")
	}
}

func TestArtifactsViewKeybindHintsIncludeShiftO(t *testing.T) {
	t.Parallel()

	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(80, 30)
	m.SetItems(testArtifactItems())

	hints := m.KeybindHints()
	found := false
	for _, h := range hints {
		if h.Key == "O" && h.Label == "PR links" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("keybind hints missing O/PR links, got: %+v", hints)
	}
}

func TestArtifactsViewFollowUpKeyDisabledWithoutState(t *testing.T) {
	t.Parallel()
	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(80, 30)
	m.SetItems(testArtifactItems())
	// No SetWorkItem call → zero state value → follow-up disabled.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if cmd != nil {
		t.Fatalf("expected no command when work item state not set")
	}
}

func TestArtifactsViewFollowUpKeyEmitsFetchMsg(t *testing.T) {
	t.Parallel()
	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(80, 30)
	m.SetItems(testArtifactItems())
	m.SetWorkItem("wi-1", domain.SessionCompleted)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if cmd == nil {
		t.Fatal("expected command from 'f' when state is completed")
	}
	msg := cmd()
	fetch, ok := msg.(views.FetchReviewCommentsMsg)
	if !ok {
		t.Fatalf("expected FetchReviewCommentsMsg, got %T", msg)
	}
	if fetch.WorkItemID != "wi-1" {
		t.Fatalf("WorkItemID = %q", fetch.WorkItemID)
	}
	if len(fetch.Items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(fetch.Items))
	}
	if fetch.Items[0].ID != "github:acme/auth-svc:#42" {
		t.Fatalf("Items[0].ID = %q, want %q", fetch.Items[0].ID, "github:acme/auth-svc:#42")
	}
}

func TestArtifactsViewFollowUpHintVisibleWhenEnabled(t *testing.T) {
	t.Parallel()
	st := newTestStyles(t)
	m := views.NewArtifactsModel(st)
	m.SetSize(80, 30)
	m.SetItems(testArtifactItems())
	m.SetWorkItem("wi-1", domain.SessionReviewing)
	hints := m.KeybindHints()
	found := false
	for _, h := range hints {
		if h.Key == "f" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected 'f' keybind hint; got: %+v", hints)
	}
}
