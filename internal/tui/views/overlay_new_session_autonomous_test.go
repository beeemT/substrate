package views

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/styles"
)

func testAutonomousFilters() []domain.NewSessionFilter {
	return []domain.NewSessionFilter{
		{
			ID:       "f-gh-open",
			Name:     "GitHub open assigned",
			Provider: "github",
			Criteria: domain.NewSessionFilterCriteria{Scope: domain.ScopeIssues, View: "assigned_to_me", State: "open", Search: "team:platform"},
		},
		{
			ID:       "f-gh-closed",
			Name:     "GitHub closed",
			Provider: "github",
			Criteria: domain.NewSessionFilterCriteria{Scope: domain.ScopeIssues, View: "all", State: "closed"},
		},
		{
			ID:       "f-linear-inbox",
			Name:     "Linear inbox",
			Provider: "linear",
			Criteria: domain.NewSessionFilterCriteria{Scope: domain.ScopeIssues, View: "inbox"},
		},
		{
			ID:       "f-all-issues",
			Name:     "All providers triage",
			Provider: viewFilterAll,
			Criteria: domain.NewSessionFilterCriteria{Scope: domain.ScopeIssues, View: "all"},
		},
	}
}

func newAutonomousOverlay() NewSessionAutonomousOverlay {
	st := styles.NewStyles(styles.DefaultTheme)
	overlay := NewNewSessionAutonomousOverlay(st)
	overlay.SetSize(120, 30)
	overlay.SetSavedFilters(testAutonomousFilters())
	overlay.Open()
	return overlay
}

func TestNewSessionAutonomousOverlayEnterStartsWithSelectedFilters(t *testing.T) {
	t.Parallel()
	overlay := newAutonomousOverlay()

	var cmd tea.Cmd
	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeySpace})
	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeyDown})
	overlay, _ = overlay.Update(tea.KeyMsg{Type: tea.KeySpace})
	overlay, cmd = overlay.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected start command")
	}
	msg := cmd()
	start, ok := msg.(StartNewSessionAutonomousModeMsg)
	if !ok {
		t.Fatalf("msg = %T, want StartNewSessionAutonomousModeMsg", msg)
	}
	if len(start.SelectedFilterIDs) != 2 {
		t.Fatalf("selected ids len = %d, want 2", len(start.SelectedFilterIDs))
	}
	if start.SelectedFilterIDs[0] != "f-gh-closed" || start.SelectedFilterIDs[1] != "f-gh-open" {
		t.Fatalf("selected ids = %#v, want sorted github IDs", start.SelectedFilterIDs)
	}
}

func TestNewSessionAutonomousOverlayExcludesProviderAllFilters(t *testing.T) {
	t.Parallel()
	overlay := newAutonomousOverlay()

	items := overlay.list.Items()
	if len(items) != 3 {
		t.Fatalf("items len = %d, want 3 (provider=all excluded)", len(items))
	}
	for _, item := range items {
		filterItem, ok := item.(newSessionAutonomousFilterItem)
		if !ok {
			t.Fatalf("item = %T, want newSessionAutonomousFilterItem", item)
		}
		if filterItem.filter.Provider == viewFilterAll {
			t.Fatalf("provider=all filter leaked into autonomous list: %#v", filterItem.filter)
		}
	}
}

func TestStartNewSessionAutonomousModeCmdRejectsProviderAllFilter(t *testing.T) {
	t.Parallel()

	cmd := StartNewSessionAutonomousModeCmd(
		"workspace-1",
		"instance-1",
		nil,
		nil,
		testAutonomousFilters(),
		[]string{"f-all-issues"},
	)
	msg := cmd()
	errMsg, ok := msg.(ErrMsg)
	if !ok {
		t.Fatalf("msg = %T, want ErrMsg", msg)
	}
	if !strings.Contains(errMsg.Err.Error(), "cannot be used for autonomous mode") {
		t.Fatalf("err = %q, want ineligible selection error", errMsg.Err.Error())
	}
}

func TestNewSessionAutonomousOverlayEnterWithoutSelectionErrors(t *testing.T) {
	t.Parallel()
	overlay := newAutonomousOverlay()

	_, cmd := overlay.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected error command")
	}
	msg := cmd()
	errMsg, ok := msg.(ErrMsg)
	if !ok {
		t.Fatalf("msg = %T, want ErrMsg", msg)
	}
	if !strings.Contains(errMsg.Err.Error(), "select at least one") {
		t.Fatalf("err = %q, want selection error", errMsg.Err.Error())
	}
}

func TestNewSessionAutonomousOverlayStopAndCloseCommands(t *testing.T) {
	t.Parallel()
	overlay := newAutonomousOverlay()

	_, stopCmd := overlay.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if stopCmd == nil {
		t.Fatal("expected stop command")
	}
	if _, ok := stopCmd().(StopNewSessionAutonomousModeMsg); !ok {
		t.Fatalf("stop msg = %T, want StopNewSessionAutonomousModeMsg", stopCmd())
	}

	_, closeCmd := overlay.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if closeCmd == nil {
		t.Fatal("expected close command")
	}
	if _, ok := closeCmd().(CloseOverlayMsg); !ok {
		t.Fatalf("close msg = %T, want CloseOverlayMsg", closeCmd())
	}
}

func TestNewSessionAutonomousOverlayViewFitsLayout(t *testing.T) {
	t.Parallel()
	overlay := newAutonomousOverlay()
	overlay.SetRuntimeState(true, []string{"f-gh-open"})

	view := ansi.Strip(overlay.View())
	if !strings.Contains(view, "New Session Autonomous Mode") {
		t.Fatalf("view = %q, want title", view)
	}
	if !strings.Contains(view, "Running") {
		t.Fatalf("view = %q, want running status", view)
	}
	assertOverlayFits(t, view, 120, 30)

	overlay.SetSize(72, 18)
	view = ansi.Strip(overlay.View())
	assertOverlayFits(t, view, 72, 18)
}
