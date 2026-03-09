package components

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/x/ansi"
)

var testSplitOverlaySpec = SplitOverlaySizingSpec{
	MaxOverlayWidth:   180,
	LeftMinWidth:      40,
	RightMinWidth:     52,
	LeftWeight:        2,
	RightWeight:       3,
	MinBodyHeight:     8,
	DefaultBodyHeight: 18,
	HeightRatioNum:    3,
	HeightRatioDen:    5,
	InputWidthOffset:  20,
}

func TestComputeSplitOverlayLayoutClampsToViewportAndChrome(t *testing.T) {
	layout := ComputeSplitOverlayLayout(72, 18, 11, testSplitOverlaySpec)

	if layout.FrameWidth != 70 {
		t.Fatalf("frame width = %d, want 70", layout.FrameWidth)
	}
	if layout.ContentWidth != 66 {
		t.Fatalf("content width = %d, want 66", layout.ContentWidth)
	}
	if layout.InputWidth != 46 {
		t.Fatalf("input width = %d, want 46", layout.InputWidth)
	}
	if layout.BodyHeight != 7 {
		t.Fatalf("body height = %d, want 7", layout.BodyHeight)
	}
	if layout.LeftPaneWidth != 32 || layout.RightPaneWidth != 33 {
		t.Fatalf("pane widths = (%d, %d), want (32, 33)", layout.LeftPaneWidth, layout.RightPaneWidth)
	}
	if layout.LeftInnerWidth != 28 || layout.RightInnerWidth != 29 {
		t.Fatalf("inner widths = (%d, %d), want (28, 29)", layout.LeftInnerWidth, layout.RightInnerWidth)
	}
	if layout.ListHeight != 5 {
		t.Fatalf("list height = %d, want 5", layout.ListHeight)
	}
	if layout.ViewportWidth != 27 || layout.ViewportHeight != 3 {
		t.Fatalf("viewport = (%d, %d), want (27, 3)", layout.ViewportWidth, layout.ViewportHeight)
	}
}

func TestComputeSplitOverlayLayoutUsesDefaultDimensionsWhenUnset(t *testing.T) {
	layout := ComputeSplitOverlayLayout(0, 0, 10, testSplitOverlaySpec)

	if layout.FrameWidth != 178 {
		t.Fatalf("frame width = %d, want 178", layout.FrameWidth)
	}
	if layout.ContentWidth != 174 {
		t.Fatalf("content width = %d, want 174", layout.ContentWidth)
	}
	if layout.BodyHeight != 18 {
		t.Fatalf("body height = %d, want 18", layout.BodyHeight)
	}
	if layout.LeftPaneWidth != 69 || layout.RightPaneWidth != 104 {
		t.Fatalf("pane widths = (%d, %d), want (69, 104)", layout.LeftPaneWidth, layout.RightPaneWidth)
	}
}

func TestRenderOverlayFrameFitsComputedLayout(t *testing.T) {
	layout := ComputeSplitOverlayLayout(72, 18, 11, testSplitOverlaySpec)
	body := RenderSplitOverlayBody(layout, SplitOverlaySpec{
		LeftPane: OverlayPaneSpec{
			Body:    "session list",
			Focused: true,
		},
		RightPane: OverlayPaneSpec{
			Title: "Preview",
			Body:  strings.Repeat("preview line\n", 2) + "wrapped preview content",
		},
	})
	view := RenderOverlayFrame(layout.FrameWidth, OverlayFrameSpec{
		HeaderLines: []string{
			"Search Sessions",
			"Search: query",
			RenderOverlayDivider(layout.ContentWidth),
			"Searching…",
		},
		Body:   body,
		Footer: "[Tab] Focus  [Ctrl+S] Toggle scope  [Enter] Open  [Esc] Close",
	})

	assertFits(t, view, 72, 18)
}

func TestOverlayPaneBorderColorUsesFocusColor(t *testing.T) {
	if string(overlayPaneBorderColor(false)) != overlayBorderColor {
		t.Fatalf("unfocused border color = %q, want %q", overlayPaneBorderColor(false), overlayBorderColor)
	}
	if string(overlayPaneBorderColor(true)) != overlayFocusBorderColor {
		t.Fatalf("focused border color = %q, want %q", overlayPaneBorderColor(true), overlayFocusBorderColor)
	}
}

func TestApplyOverlayListStylesReturnsUpdatedModel(t *testing.T) {
	delegate := list.NewDefaultDelegate()
	model := list.New(nil, delegate, 20, 5)
	updated := ApplyOverlayListStyles(model)
	if reflect.DeepEqual(model.Styles.NoItems, updated.Styles.NoItems) {
		t.Fatal("expected overlay list styles to change no-items style")
	}
	if reflect.DeepEqual(model.Styles.StatusEmpty, updated.Styles.StatusEmpty) {
		t.Fatal("expected overlay list styles to change empty-status style")
	}
}

func TestRenderSplitOverlayBodyUsesConfiguredDividerWidth(t *testing.T) {
	layout := ComputeSplitOverlayLayout(72, 18, 11, testSplitOverlaySpec)
	pane := renderOverlayPane(layout.RightInnerWidth, layout.ListHeight, OverlayPaneSpec{
		Title:        "Preview",
		DividerWidth: layout.ViewportWidth,
		Body:         "details",
	})
	lines := strings.Split(pane, "\n")
	dividerLine := ""
	for i, line := range lines {
		if strings.Contains(line, "Preview") && i+1 < len(lines) {
			dividerLine = lines[i+1]
			break
		}
	}
	if dividerLine == "" {
		t.Fatalf("pane missing divider line: %q", pane)
	}
	if got := strings.Count(dividerLine, "─"); got != layout.ViewportWidth {
		t.Fatalf("divider width = %d, want %d\nline: %q", got, layout.ViewportWidth, dividerLine)
	}
}

func assertFits(t *testing.T, view string, width, height int) {
	t.Helper()
	lines := strings.Split(view, "\n")
	if len(lines) > height {
		t.Fatalf("line count = %d, want <= %d\nview:\n%s", len(lines), height, view)
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > width {
			t.Fatalf("line %d width = %d, want <= %d\nline: %q", i+1, got, width, line)
		}
	}
}
