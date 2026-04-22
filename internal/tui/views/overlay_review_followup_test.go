package views_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/tui/views"
)

func reviewItems() []views.ArtifactItem {
	return []views.ArtifactItem{
		{ID: "github:acme/api:#7", Provider: "github", Kind: "PR", RepoName: "acme/api", Ref: "#7"},
		{ID: "github:acme/web:#20", Provider: "github", Kind: "PR", RepoName: "acme/web", Ref: "#20"},
	}
}

func reviewCommentsForTwoPRs() map[string][]adapter.ReviewComment {
	return map[string][]adapter.ReviewComment{
		"github:acme/api:#7": {
			{ID: "a-1", ReviewerLogin: "alice", Body: "Please add tests for the retry loop.", Path: "", Line: 0, CreatedAt: time.Date(2025, 4, 15, 14, 23, 0, 0, time.UTC)},
			{ID: "a-2", ReviewerLogin: "alice", Body: "Retry loop doesn't respect the context deadline.", Path: "internal/handler/process.go", Line: 42},
			{ID: "a-3", ReviewerLogin: "bob", Body: "Consider a switch here.", Path: "internal/handler/process.go", Line: 78},
		},
		"github:acme/web:#20": {
			{ID: "w-1", ReviewerLogin: "alice", Body: "Missing graceful shutdown.", Path: "cmd/server/main.go", Line: 15},
		},
	}
}

func TestReviewFollowup_OpenLoading_Active(t *testing.T) {
	t.Parallel()
	m := views.NewReviewFollowupModel(newTestStyles(t))
	m.SetSize(120, 30)
	_, _ = m.OpenLoading("wi-1", reviewItems())
	if !m.Active() {
		t.Fatal("expected overlay active after OpenLoading")
	}
	if m.Stage() != views.ReviewFollowupStageLoading() {
		t.Fatalf("expected loading stage, got %v", m.Stage())
	}
}

func TestReviewFollowup_ApplyFetch_NoUnresolved_ReturnsFalse(t *testing.T) {
	t.Parallel()
	m := views.NewReviewFollowupModel(newTestStyles(t))
	m.SetSize(120, 30)
	_, _ = m.OpenLoading("wi-1", reviewItems())
	if keep := m.ApplyFetchResult(map[string][]adapter.ReviewComment{}, time.Now()); keep {
		t.Fatal("expected ApplyFetchResult to return false when no unresolved")
	}
}

func TestReviewFollowup_ApplyFetch_SinglePR_GoesToSelector(t *testing.T) {
	t.Parallel()
	m := views.NewReviewFollowupModel(newTestStyles(t))
	m.SetSize(120, 30)
	_, _ = m.OpenLoading("wi-1", reviewItems())
	comments := map[string][]adapter.ReviewComment{
		"github:acme/api:#7": {{ID: "a-1", ReviewerLogin: "alice", Body: "fix it"}},
	}
	if keep := m.ApplyFetchResult(comments, time.Now()); !keep {
		t.Fatal("expected ApplyFetchResult to keep overlay open")
	}
	if m.Stage() != views.ReviewFollowupStageSelector() {
		t.Fatalf("expected selector stage for single-PR case, got %v", m.Stage())
	}
}

func TestReviewFollowup_ApplyFetch_MultiPR_GoesToPicker(t *testing.T) {
	t.Parallel()
	m := views.NewReviewFollowupModel(newTestStyles(t))
	m.SetSize(120, 30)
	_, _ = m.OpenLoading("wi-1", reviewItems())
	if keep := m.ApplyFetchResult(reviewCommentsForTwoPRs(), time.Now()); !keep {
		t.Fatal("expected overlay retained")
	}
	if m.Stage() != views.ReviewFollowupStagePicker() {
		t.Fatalf("expected picker stage, got %v", m.Stage())
	}
}

func TestReviewFollowup_IsStale(t *testing.T) {
	t.Parallel()
	m := views.NewReviewFollowupModel(newTestStyles(t))
	m.SetSize(120, 30)
	_, _ = m.OpenLoading("wi-1", reviewItems())
	m.ApplyFetchResult(reviewCommentsForTwoPRs(), time.Now())
	if m.IsStale(time.Now()) {
		t.Fatal("fresh fetch should not be stale")
	}
	if !m.IsStale(time.Now().Add(10 * time.Minute)) {
		t.Fatal("fetch 10 minutes old should be stale")
	}
}

func TestReviewFollowup_FormatPerRepo_GroupsByRepo(t *testing.T) {
	t.Parallel()
	m := views.NewReviewFollowupModel(newTestStyles(t))
	m.SetSize(120, 30)
	_, _ = m.OpenLoading("wi-1", reviewItems())
	m.ApplyFetchResult(reviewCommentsForTwoPRs(), time.Now())
	// Advance past picker so every PR is scoped.
	m.ApplyPickerAllForTest()

	per := m.FormatPerRepo()
	if len(per) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(per))
	}
	api := per["acme/api"]
	if !strings.Contains(api, "### acme/api") {
		t.Fatalf("missing repo header for acme/api: %q", api)
	}
	if strings.Contains(api, "acme/web") {
		t.Fatalf("acme/api bucket leaked acme/web comments: %q", api)
	}
	if !strings.Contains(api, "#### General") {
		t.Fatalf("missing General section in acme/api: %q", api)
	}
	if !strings.Contains(api, "#### internal/handler/process.go") {
		t.Fatalf("missing file section in acme/api: %q", api)
	}
	if !strings.Contains(api, "Line 42:") {
		t.Fatalf("expected line-42 inline comment: %q", api)
	}
}

func TestReviewFollowup_FormatAllSelected_SingleBlock(t *testing.T) {
	t.Parallel()
	m := views.NewReviewFollowupModel(newTestStyles(t))
	m.SetSize(120, 30)
	_, _ = m.OpenLoading("wi-1", reviewItems())
	m.ApplyFetchResult(reviewCommentsForTwoPRs(), time.Now())
	m.ApplyPickerAllForTest()

	all := m.FormatAllSelected()
	if !strings.Contains(all, "### acme/api") || !strings.Contains(all, "### acme/web") {
		t.Fatalf("expected both repos in all-selected: %q", all)
	}
}

func TestReviewFollowup_Layout_FitsNarrowWidth(t *testing.T) {
	t.Parallel()
	m := views.NewReviewFollowupModel(newTestStyles(t))
	// Narrow terminal.
	m.SetSize(80, 24)
	_, _ = m.OpenLoading("wi-1", reviewItems())
	m.ApplyFetchResult(reviewCommentsForTwoPRs(), time.Now())
	// Force into picker stage (multi-PR default) then selector.
	view := m.View()
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		w := ansi.StringWidth(line)
		if w > 80 {
			t.Fatalf("picker line %d exceeds width 80: %d (%q)", i, w, line)
		}
	}
	if len(lines) > 24 {
		t.Fatalf("picker view taller than 24 lines: %d", len(lines))
	}
	// Advance to selector and re-check.
	m.ApplyPickerAllForTest()
	view = m.View()
	lines = strings.Split(view, "\n")
	for i, line := range lines {
		w := ansi.StringWidth(line)
		if w > 80 {
			t.Fatalf("selector line %d exceeds width 80: %d (%q)", i, w, line)
		}
	}
	if len(lines) > 24 {
		t.Fatalf("selector view taller than 24 lines: %d", len(lines))
	}
}

// TestReviewFollowup_Layout_FitsBelowFloor exercises the floor-less frame math:
// at terminal sizes smaller than the historic 60w/8h floors, the overlay must
// shrink to fit rather than overflow.
func TestReviewFollowup_Layout_FitsBelowFloor(t *testing.T) {
	t.Parallel()
	cases := []struct{ w, h int }{
		{50, 12},
		{40, 10},
		{30, 8},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(fmt.Sprintf("%dx%d", tc.w, tc.h), func(t *testing.T) {
			t.Parallel()
			m := views.NewReviewFollowupModel(newTestStyles(t))
			m.SetSize(tc.w, tc.h)
			_, _ = m.OpenLoading("wi-1", reviewItems())
			m.ApplyFetchResult(reviewCommentsForTwoPRs(), time.Now())
			for _, label := range []string{"picker", "selector"} {
				if label == "selector" {
					m.ApplyPickerAllForTest()
				}
				lines := strings.Split(m.View(), "\n")
				for i, line := range lines {
					if w := ansi.StringWidth(line); w > tc.w {
						t.Fatalf("%s line %d exceeds width %d: %d (%q)", label, i, tc.w, w, line)
					}
				}
				if len(lines) > tc.h {
					t.Fatalf("%s view taller than %d lines: %d", label, tc.h, len(lines))
				}
			}
		})
	}
}

func TestReviewFollowup_ConfirmStage_EmitsReplan(t *testing.T) {
	t.Parallel()
	m := views.NewReviewFollowupModel(newTestStyles(t))
	m.SetSize(120, 30)
	_, _ = m.OpenLoading("wi-1", reviewItems())
	m.ApplyFetchResult(reviewCommentsForTwoPRs(), time.Now())
	m.ApplyPickerAllForTest()
	// In selector: press "p" to reach confirm.
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	if m2.Stage() != views.ReviewFollowupStageConfirm() {
		t.Fatalf("expected confirm stage after p, got %v", m2.Stage())
	}
	// "y" → emit replan msg.
	m3, cmd := m2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	_ = m3
	if cmd == nil {
		t.Fatal("expected dispatch command from y in confirm stage")
	}
	msg := cmd()
	if _, ok := msg.(views.FollowUpFromReviewReplanMsg); !ok {
		t.Fatalf("expected FollowUpFromReviewReplanMsg, got %T", msg)
	}
}

func TestReviewFollowup_Address_EmitsPerRepoMsg(t *testing.T) {
	t.Parallel()
	m := views.NewReviewFollowupModel(newTestStyles(t))
	m.SetSize(120, 30)
	_, _ = m.OpenLoading("wi-1", reviewItems())
	m.ApplyFetchResult(reviewCommentsForTwoPRs(), time.Now())
	m.ApplyPickerAllForTest()
	// Enter → address.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected address dispatch command")
	}
	msg := cmd()
	addr, ok := msg.(views.FollowUpFromReviewAddressMsg)
	if !ok {
		t.Fatalf("expected FollowUpFromReviewAddressMsg, got %T", msg)
	}
	if _, has := addr.PerRepo["acme/api"]; !has {
		t.Fatalf("missing acme/api in per-repo: %+v", addr.PerRepo)
	}
	if _, has := addr.PerRepo["acme/web"]; !has {
		t.Fatalf("missing acme/web in per-repo: %+v", addr.PerRepo)
	}
}

func TestReviewFollowup_StaleDispatch_EmitsRefetch(t *testing.T) {
	t.Parallel()
	m := views.NewReviewFollowupModel(newTestStyles(t))
	m.SetSize(120, 30)
	_, _ = m.OpenLoading("wi-1", reviewItems())
	// Fetch stamped 10 minutes ago → stale.
	m.ApplyFetchResult(reviewCommentsForTwoPRs(), time.Now().Add(-10*time.Minute))
	m.ApplyPickerAllForTest()
	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected dispatch cmd")
	}
	// Stale dispatch returns a tea.Batch of (spinner.Tick, refetch). Find the
	// refetch payload by executing every command and inspecting BatchMsg if needed.
	refetch := findRefetchMsg(t, cmd)
	if refetch.Mode != "address" {
		t.Fatalf("expected mode=address, got %q", refetch.Mode)
	}
	// Stale dispatch also gates further input by transitioning to loading stage.
	if m2.Stage() != views.ReviewFollowupStageLoading() {
		t.Fatalf("expected loading stage during refetch, got %v", m2.Stage())
	}
}

// findRefetchMsg drains the cmd (which may be a tea.Batch) and returns the
// ReviewFollowupRefetchMsg buried inside.
func findRefetchMsg(t *testing.T, cmd tea.Cmd) views.ReviewFollowupRefetchMsg {
	t.Helper()
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			if sub == nil {
				continue
			}
			if r, ok := sub().(views.ReviewFollowupRefetchMsg); ok {
				return r
			}
		}
		t.Fatalf("refetch msg not found in batch of %d", len(batch))
	}
	if r, ok := msg.(views.ReviewFollowupRefetchMsg); ok {
		return r
	}
	t.Fatalf("expected refetch msg, got %T", msg)
	return views.ReviewFollowupRefetchMsg{}
}

func TestReviewFollowup_SelectAll_SelectNone(t *testing.T) {
	t.Parallel()
	m := views.NewReviewFollowupModel(newTestStyles(t))
	m.SetSize(120, 30)
	_, _ = m.OpenLoading("wi-1", reviewItems())
	m.ApplyFetchResult(reviewCommentsForTwoPRs(), time.Now())
	m.ApplyPickerAllForTest()
	// Deselect all.
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if m2.HasAnySelection() {
		t.Fatal("expected no selection after 'n'")
	}
	// Select all.
	m3, _ := m2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if !m3.HasAnySelection() {
		t.Fatal("expected selection after 'a'")
	}
}

func TestReviewFollowup_MergeRefetch_DropsMissing(t *testing.T) {
	t.Parallel()
	m := views.NewReviewFollowupModel(newTestStyles(t))
	m.SetSize(120, 30)
	_, _ = m.OpenLoading("wi-1", reviewItems())
	m.ApplyFetchResult(reviewCommentsForTwoPRs(), time.Now().Add(-10*time.Minute))
	m.ApplyPickerAllForTest()

	// Fresh fetch: comment "a-2" disappeared.
	fresh := map[string][]adapter.ReviewComment{
		"github:acme/api:#7": {
			{ID: "a-1", ReviewerLogin: "alice", Body: "fix"},
			{ID: "a-3", ReviewerLogin: "bob", Body: "switch"},
			{ID: "a-4", ReviewerLogin: "carol", Body: "new comment"},
		},
		"github:acme/web:#20": {
			{ID: "w-1", ReviewerLogin: "alice", Body: "shutdown"},
		},
	}
	dropped := m.MergeRefetch(fresh, time.Now())
	if dropped != 1 {
		t.Fatalf("expected 1 dropped selection, got %d", dropped)
	}
}
