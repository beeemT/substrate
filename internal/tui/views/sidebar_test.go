package views_test

import (
	"testing"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/styles"
	"github.com/beeemT/substrate/internal/tui/views"
)

func makeSidebarStyles() styles.Styles {
	return styles.NewStyles(styles.DefaultTheme)
}

func makeSessions(n int) []views.SessionSummary {
	sessions := make([]views.SessionSummary, n)
	for i := range sessions {
		sessions[i] = views.SessionSummary{
			WorkItemID: string(rune('A' + i)),
			ExternalID: string(rune('A' + i)),
			Title:      "Session " + string(rune('A'+i)),
			State:      domain.WorkItemIngested,
		}
	}
	return sessions
}

func TestSidebarEmpty(t *testing.T) {
	t.Helper()
	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetHeight(20)
	out := m.View()
	if out == "" {
		t.Fatal("expected non-empty View() from sidebar with height set")
	}
}

func TestSidebarNavigation(t *testing.T) {
	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetHeight(40)
	sessions := makeSessions(3)
	m.SetSessions(sessions)

	// Default: first item selected
	sel := m.Selected()
	if sel == nil {
		t.Fatal("expected selected item, got nil")
	}
	if sel.WorkItemID != sessions[0].WorkItemID {
		t.Fatalf("expected first session, got %q", sel.WorkItemID)
	}

	// MoveDown -> second
	m.MoveDown()
	sel = m.Selected()
	if sel == nil || sel.WorkItemID != sessions[1].WorkItemID {
		t.Fatalf("after MoveDown expected second session, got %v", sel)
	}

	// MoveDown -> third
	m.MoveDown()
	sel = m.Selected()
	if sel == nil || sel.WorkItemID != sessions[2].WorkItemID {
		t.Fatalf("after second MoveDown expected third session, got %v", sel)
	}

	// MoveDown at end -> still third (no wrap)
	m.MoveDown()
	sel = m.Selected()
	if sel == nil || sel.WorkItemID != sessions[2].WorkItemID {
		t.Fatalf("MoveDown past end should stay at last, got %v", sel)
	}

	// MoveUp -> second
	m.MoveUp()
	sel = m.Selected()
	if sel == nil || sel.WorkItemID != sessions[1].WorkItemID {
		t.Fatalf("after MoveUp expected second session, got %v", sel)
	}
}

func TestSidebarMoveUpAtTop(t *testing.T) {
	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetHeight(20)
	sessions := makeSessions(2)
	m.SetSessions(sessions)

	// Already at index 0; MoveUp should stay at 0
	m.MoveUp()
	sel := m.Selected()
	if sel == nil {
		t.Fatal("expected selected item, got nil")
	}
	if sel.WorkItemID != sessions[0].WorkItemID {
		t.Fatalf("MoveUp at top should keep first item selected, got %q", sel.WorkItemID)
	}
}

func TestSidebarSingleSession(t *testing.T) {
	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetHeight(20)
	sessions := makeSessions(1)
	m.SetSessions(sessions)

	m.MoveDown()
	sel := m.Selected()
	if sel == nil || sel.WorkItemID != sessions[0].WorkItemID {
		t.Fatalf("MoveDown with single session should stay at only item, got %v", sel)
	}

	m.MoveUp()
	sel = m.Selected()
	if sel == nil || sel.WorkItemID != sessions[0].WorkItemID {
		t.Fatalf("MoveUp with single session should stay at only item, got %v", sel)
	}
}

func TestSidebarSelected_Empty(t *testing.T) {
	m := views.NewSidebarModel(makeSidebarStyles())
	m.SetHeight(20)
	// No sessions set
	sel := m.Selected()
	if sel != nil {
		t.Fatalf("expected nil Selected() with no sessions, got %+v", sel)
	}
}
