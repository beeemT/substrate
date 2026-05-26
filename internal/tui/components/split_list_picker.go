package components

import "github.com/beeemT/substrate/internal/tui/styles"

// SplitPaneFocus tracks which pane is focused inside a split overlay.
type SplitPaneFocus int

const (
	SplitPaneFocusLeft SplitPaneFocus = iota
	SplitPaneFocusRight
)

// SplitListPaneSpec describes the content for a single pane in a split list picker.
type SplitListPaneSpec struct {
	Title string
	Body  string
}

// SplitListPicker manages focus and layout state for a split list picker overlay.
// It does not own list or viewport models; those remain owned by the overlay.
type SplitListPicker struct {
	focus  SplitPaneFocus
	layout SplitOverlayLayout
	spec   SplitOverlaySizingSpec
}

// NewSplitListPicker constructs a SplitListPicker with the given sizing spec.
func NewSplitListPicker(spec SplitOverlaySizingSpec) SplitListPicker {
	return SplitListPicker{focus: SplitPaneFocusLeft, spec: spec}
}

// SetSize recalculates layout dimensions. chromeLines is supplied by the overlay
// because each overlay has different header/footer/hint wrapping.
func (m *SplitListPicker) SetSize(width, height, chromeLines int) {
	m.layout = ComputeSplitOverlayLayout(width, height, chromeLines, m.spec)
}

// Layout returns the computed split overlay layout.
func (m SplitListPicker) Layout() SplitOverlayLayout { return m.layout }

// Focus returns the current focus state.
func (m SplitListPicker) Focus() SplitPaneFocus { return m.focus }

// IsFocusLeft reports whether the left pane is focused.
func (m SplitListPicker) IsFocusLeft() bool { return m.focus == SplitPaneFocusLeft }

// FocusLeft sets focus to the left pane.
func (m *SplitListPicker) FocusLeft() { m.focus = SplitPaneFocusLeft }

// FocusRight sets focus to the right pane.
func (m *SplitListPicker) FocusRight() { m.focus = SplitPaneFocusRight }

// SwitchFocus toggles focus between left and right panes.
func (m *SplitListPicker) SwitchFocus() {
	if m.focus == SplitPaneFocusLeft {
		m.focus = SplitPaneFocusRight
		return
	}
	m.focus = SplitPaneFocusLeft
}

// View renders the split-pane body using the computed layout.
func (m SplitListPicker) View(st styles.Styles, left, right SplitListPaneSpec) string {
	return RenderSplitOverlayBody(st, m.layout, SplitOverlaySpec{
		LeftPane: OverlayPaneSpec{
			Title:        left.Title,
			DividerWidth: m.layout.LeftInnerWidth,
			Body:         left.Body,
			Focused:      m.focus == SplitPaneFocusLeft,
		},
		RightPane: OverlayPaneSpec{
			Title:        right.Title,
			DividerWidth: m.layout.RightInnerWidth,
			Body:         right.Body,
			Focused:      m.focus == SplitPaneFocusRight,
		},
	})
}
