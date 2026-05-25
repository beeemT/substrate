package components

import (
	"strings"
	"testing"
)

func TestSplitListPicker_FocusSwitch(t *testing.T) {
	spec := testSplitOverlaySpec
	picker := NewSplitListPicker(spec)

	// Initially focused on left
	if !picker.IsFocusLeft() {
		t.Error("expected initial focus to be left")
	}
	if picker.Focus() != SplitPaneFocusLeft {
		t.Errorf("expected initial focus %v, got %v", SplitPaneFocusLeft, picker.Focus())
	}

	// Switch to right
	picker.SwitchFocus()
	if picker.IsFocusLeft() {
		t.Error("expected focus to be right after SwitchFocus")
	}
	if picker.Focus() != SplitPaneFocusRight {
		t.Errorf("expected focus %v, got %v", SplitPaneFocusRight, picker.Focus())
	}

	// Switch back to left
	picker.SwitchFocus()
	if !picker.IsFocusLeft() {
		t.Error("expected focus to be left after second SwitchFocus")
	}

	// Test FocusLeft/FocusRight helpers
	picker.FocusRight()
	if picker.Focus() != SplitPaneFocusRight {
		t.Errorf("expected FocusRight to set %v, got %v", SplitPaneFocusRight, picker.Focus())
	}
	picker.FocusLeft()
	if picker.Focus() != SplitPaneFocusLeft {
		t.Errorf("expected FocusLeft to set %v, got %v", SplitPaneFocusLeft, picker.Focus())
	}
}

func TestSplitListPicker_HandleFocusKeyConsumesSwitchKeys(t *testing.T) {
	spec := testSplitOverlaySpec
	picker := NewSplitListPicker(spec)

	tests := []struct {
		key           string
		wantConsumed  bool
		wantFocusLeft bool
	}{
		{"tab", true, false},   // tab → switch from left to right
		{"left", true, false},  // left → switch from left to right
		{"right", true, false}, // right → switch from left to right
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			picker.FocusLeft() // reset to left for each test
			consumed := picker.HandleFocusKey(tt.key)
			if consumed != tt.wantConsumed {
				t.Errorf("HandleFocusKey(%q) consumed = %v, want %v", tt.key, consumed, tt.wantConsumed)
			}
			if tt.wantFocusLeft {
				if !picker.IsFocusLeft() {
					t.Errorf("HandleFocusKey(%q) focus = right, want left", tt.key)
				}
			}
		})
	}
}

func TestSplitListPicker_DoesNotConsumeNavigationKeys(t *testing.T) {
	spec := testSplitOverlaySpec
	picker := NewSplitListPicker(spec)

	navigationKeys := []string{"up", "down", "k", "j", "enter", "esc", "a", "d", "i", " "}
	for _, key := range navigationKeys {
		consumed := picker.HandleFocusKey(key)
		if consumed {
			t.Errorf("HandleFocusKey(%q) should not consume navigation keys", key)
		}
	}
}

func TestSplitListPicker_SetSize(t *testing.T) {
	spec := testSplitOverlaySpec
	picker := NewSplitListPicker(spec)

	// Initially zero layout
	layout := picker.Layout()
	if layout.FrameWidth != 0 {
		t.Errorf("expected initial frame width 0, got %d", layout.FrameWidth)
	}

	// SetSize updates layout
	picker.SetSize(72, 18, 11)
	layout = picker.Layout()
	if layout.FrameWidth == 0 {
		t.Error("expected SetSize to update FrameWidth")
	}
	if layout.BodyHeight == 0 {
		t.Error("expected SetSize to update BodyHeight")
	}
}

func TestSplitListPicker_View(t *testing.T) {
	st := testOverlayStyles()
	spec := testSplitOverlaySpec
	picker := NewSplitListPicker(spec)

	picker.SetSize(72, 18, 11)

	left := SplitListPaneSpec{
		Title: "Repositories",
		Body:  "repo-a\nrepo-b\nrepo-c",
	}
	right := SplitListPaneSpec{
		Title: "Worktrees",
		Body:  "main\nfeature-x",
	}

	// View should produce output when layout is set
	view := picker.View(st, left, right)
	if view == "" {
		t.Error("expected non-empty View output")
	}

	// Verify the body height matches layout
	lines := splitLines(view)
	if len(lines) != picker.Layout().BodyHeight {
		t.Errorf("View line count = %d, want %d", len(lines), picker.Layout().BodyHeight)
	}
}

func TestSplitListPicker_NewSplitListPickerDefaults(t *testing.T) {
	spec := testSplitOverlaySpec
	picker := NewSplitListPicker(spec)

	// Verify initial state
	if picker.focus != SplitPaneFocusLeft {
		t.Errorf("expected initial focus %v, got %v", SplitPaneFocusLeft, picker.focus)
	}
	if picker.spec != spec {
		t.Errorf("expected spec to be set to %+v, got %+v", spec, picker.spec)
	}
}

func TestSplitPaneFocusConstants(t *testing.T) {
	if SplitPaneFocusLeft != 0 {
		t.Errorf("SplitPaneFocusLeft = %d, want 0", SplitPaneFocusLeft)
	}
	if SplitPaneFocusRight != 1 {
		t.Errorf("SplitPaneFocusRight = %d, want 1", SplitPaneFocusRight)
	}
}

func splitLines(s string) []string {
	return strings.Split(s, "\n")
}
