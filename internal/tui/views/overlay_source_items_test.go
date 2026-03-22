package views_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/views"
)

func testSourceItems() []domain.SourceSummary {
	return []domain.SourceSummary{
		{Provider: "github", Kind: "issue", Ref: "acme/rocket#42", Title: "Fix auth", URL: "https://github.com/acme/rocket/issues/42", State: "open"},
		{Provider: "github", Kind: "issue", Ref: "acme/rocket#43", Title: "Fix billing", URL: "https://github.com/acme/rocket/issues/43", State: "open"},
		{Provider: "github", Kind: "issue", Ref: "acme/rocket#44", Title: "No URL item", State: "closed"},
	}
}

func assertViewFitsSize(t *testing.T, view string, width, height int) {
	t.Helper()

	lines := strings.Split(view, "\n")
	if len(lines) > height {
		t.Errorf("view has %d lines, want <= %d", len(lines), height)
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > width {
			t.Errorf("line %d width = %d, want <= %d: %q", i, got, width, line)
		}
	}
}

func TestSourceItemsOverlayViewFitsSize(t *testing.T) {
	t.Parallel()

	m := views.NewSourceItemsOverlay(newTestStyles(t))
	m.SetSize(120, 40)
	m.Open(testSourceItems())

	if !m.Active() {
		t.Fatal("expected Active() == true after Open")
	}

	v := m.View()
	if v == "" {
		t.Fatal("expected non-empty View()")
	}

	assertViewFitsSize(t, v, 120, 40)

	plain := ansi.Strip(v)
	if !strings.Contains(plain, "Source Items") {
		t.Error("expected View to contain 'Source Items' title")
	}
}

func TestSourceItemsOverlayViewNarrowFitsSize(t *testing.T) {
	t.Parallel()

	m := views.NewSourceItemsOverlay(newTestStyles(t))
	m.SetSize(60, 20)
	m.Open(testSourceItems())

	v := m.View()
	if v == "" {
		t.Fatal("expected non-empty View()")
	}

	assertViewFitsSize(t, v, 60, 20)
}

func TestSourceItemsOverlayEscClosesOverlay(t *testing.T) {
	t.Parallel()

	m := views.NewSourceItemsOverlay(newTestStyles(t))
	m.SetSize(120, 40)
	m.Open(testSourceItems())

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	_ = updated

	if cmd == nil {
		t.Fatal("expected non-nil cmd after Esc")
	}

	msg := cmd()
	if _, ok := msg.(views.CloseOverlayMsg); !ok {
		t.Fatalf("expected CloseOverlayMsg, got %T", msg)
	}
}

func TestSourceItemsOverlayEnterWithURL(t *testing.T) {
	t.Parallel()

	m := views.NewSourceItemsOverlay(newTestStyles(t))
	m.SetSize(120, 40)
	m.Open(testSourceItems())

	// First item has a URL, so Enter should produce a cmd.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected non-nil cmd after Enter on item with URL")
	}

	// The cmd should produce an openSourceItemURLsMsg (unexported),
	// so we just verify it returns a non-nil message.
	msg := cmd()
	if msg == nil {
		t.Fatal("expected non-nil message from cmd")
	}
}

func TestSourceItemsOverlaySpaceTogglesSelection(t *testing.T) {
	t.Parallel()

	items := []domain.SourceSummary{
		{Provider: "github", Kind: "issue", Ref: "acme/rocket#42", Title: "Fix auth", URL: "https://github.com/acme/rocket/issues/42", State: "open"},
		{Provider: "github", Kind: "issue", Ref: "acme/rocket#43", Title: "Fix billing", URL: "https://github.com/acme/rocket/issues/43", State: "open"},
	}

	m := views.NewSourceItemsOverlay(newTestStyles(t))
	m.SetSize(120, 40)
	m.Open(items)

	// Press Space to select first item.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})

	v := m.View()
	plain := ansi.Strip(v)
	if !strings.Contains(plain, "selected") {
		t.Error("expected View to contain 'selected' after Space on first item")
	}

	// Move down then press Space to select second item.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})

	v = m.View()
	plain = ansi.Strip(v)
	if count := strings.Count(plain, "selected"); count < 2 {
		t.Errorf("expected 'selected' to appear at least 2 times, got %d", count)
	}
}

func TestSourceItemsOverlayEnterOnDisabledItemIsNoop(t *testing.T) {
	t.Parallel()

	// Single item with no URL → disabled.
	items := []domain.SourceSummary{
		{Provider: "github", Kind: "issue", Ref: "acme/rocket#44", Title: "No URL item", State: "closed"},
	}

	m := views.NewSourceItemsOverlay(newTestStyles(t))
	m.SetSize(120, 40)
	m.Open(items)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("expected nil cmd after Enter on disabled item (no URL)")
	}
}

func TestSourceItemsOverlaySpaceOnDisabledItemIsNoop(t *testing.T) {
	t.Parallel()

	items := []domain.SourceSummary{
		{Provider: "github", Kind: "issue", Ref: "acme/rocket#44", Title: "No URL item", State: "closed"},
	}

	m := views.NewSourceItemsOverlay(newTestStyles(t))
	m.SetSize(120, 40)
	m.Open(items)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})

	v := m.View()
	plain := ansi.Strip(v)
	if strings.Contains(plain, "selected") {
		t.Error("expected disabled item to not be selectable")
	}
}

func TestSourceItemsOverlayFocusCycling(t *testing.T) {
	t.Parallel()

	m := views.NewSourceItemsOverlay(newTestStyles(t))
	m.SetSize(120, 40)
	m.Open(testSourceItems())

	// Verify overlay renders before cycling.
	if v := m.View(); v == "" {
		t.Fatal("expected non-empty view")
	}

	// Tab should cycle focus to preview pane without error.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if v := m.View(); v == "" {
		t.Fatal("expected non-empty view after first Tab")
	}

	// Tab again should return focus to list.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if v := m.View(); v == "" {
		t.Fatal("expected non-empty view after second Tab")
	}

	// Verify arrow key focus: right should move to preview, left should return.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if v := m.View(); v == "" {
		t.Fatal("expected non-empty view after Right")
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if v := m.View(); v == "" {
		t.Fatal("expected non-empty view after Left")
	}

	// Verify shift-tab focus.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if v := m.View(); v == "" {
		t.Fatal("expected non-empty view after Shift+Tab")
	}
}

func TestSourceItemsOverlayCloseResetsState(t *testing.T) {
	t.Parallel()

	m := views.NewSourceItemsOverlay(newTestStyles(t))
	m.SetSize(120, 40)
	m.Open(testSourceItems())

	if !m.Active() {
		t.Fatal("expected Active() == true after Open")
	}

	m.Close()

	if m.Active() {
		t.Fatal("expected Active() == false after Close")
	}

	v := m.View()
	if v != "" {
		t.Fatalf("expected empty View() after Close, got %q", v)
	}
}

func TestSourceItemsOverlayEmptyItems(t *testing.T) {
	t.Parallel()

	m := views.NewSourceItemsOverlay(newTestStyles(t))
	m.SetSize(120, 40)
	m.Open([]domain.SourceSummary{})

	if !m.Active() {
		t.Fatal("expected Active() == true even with no items")
	}

	v := m.View()
	if v == "" {
		t.Fatal("expected non-empty View() even with no items")
	}

	assertViewFitsSize(t, v, 120, 40)
}

func TestSourceItemsOverlayOKeyOpensItem(t *testing.T) {
	t.Parallel()

	m := views.NewSourceItemsOverlay(newTestStyles(t))
	m.SetSize(120, 40)
	m.Open(testSourceItems())

	// "o" should behave the same as Enter for opening.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if cmd == nil {
		t.Fatal("expected non-nil cmd after 'o' key on item with URL")
	}

	msg := cmd()
	if msg == nil {
		t.Fatal("expected non-nil message from cmd")
	}
}
