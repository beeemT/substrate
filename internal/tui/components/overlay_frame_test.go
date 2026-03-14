package components

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/tui/styles"
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

func testOverlayStyles() styles.Styles {
	return styles.NewStyles(styles.DefaultTheme)
}

func TestComputeSplitOverlayLayoutClampsToViewportAndChrome(t *testing.T) {
	layout := ComputeSplitOverlayLayout(72, 18, 11, testSplitOverlaySpec)

	if layout.FrameWidth != 70 {
		t.Fatalf("frame width = %d, want 70", layout.FrameWidth)
	}
	if layout.ContentWidth != 64 {
		t.Fatalf("content width = %d, want 64", layout.ContentWidth)
	}
	if layout.InputWidth != 44 {
		t.Fatalf("input width = %d, want 44", layout.InputWidth)
	}
	if layout.BodyHeight != 7 {
		t.Fatalf("body height = %d, want 7", layout.BodyHeight)
	}
	if layout.LeftPaneWidth != 31 || layout.RightPaneWidth != 32 {
		t.Fatalf("pane widths = (%d, %d), want (31, 32)", layout.LeftPaneWidth, layout.RightPaneWidth)
	}
	if layout.LeftInnerWidth != 27 || layout.RightInnerWidth != 28 {
		t.Fatalf("inner widths = (%d, %d), want (27, 28)", layout.LeftInnerWidth, layout.RightInnerWidth)
	}
	if layout.ListHeight != 5 {
		t.Fatalf("list height = %d, want 5", layout.ListHeight)
	}
	if layout.ViewportWidth != 26 || layout.ViewportHeight != 4 {
		t.Fatalf("viewport = (%d, %d), want (26, 4)", layout.ViewportWidth, layout.ViewportHeight)
	}
	if layout.ViewportHeight+1 != layout.ListHeight {
		t.Fatalf("viewport height = %d, want detail title to reserve exactly one row from list height %d", layout.ViewportHeight, layout.ListHeight)
	}
}

func TestComputeSplitOverlayLayoutUsesDefaultDimensionsWhenUnset(t *testing.T) {
	layout := ComputeSplitOverlayLayout(0, 0, 10, testSplitOverlaySpec)

	if layout.FrameWidth != 178 {
		t.Fatalf("frame width = %d, want 178", layout.FrameWidth)
	}
	if layout.ContentWidth != 172 {
		t.Fatalf("content width = %d, want 172", layout.ContentWidth)
	}
	if layout.BodyHeight != 18 {
		t.Fatalf("body height = %d, want 18", layout.BodyHeight)
	}
	if layout.LeftPaneWidth != 68 || layout.RightPaneWidth != 103 {
		t.Fatalf("pane widths = (%d, %d), want (68, 103)", layout.LeftPaneWidth, layout.RightPaneWidth)
	}
}

func TestRenderOverlayFrameFitsComputedLayout(t *testing.T) {
	st := testOverlayStyles()
	layout := ComputeSplitOverlayLayout(72, 18, 11, testSplitOverlaySpec)
	body := RenderSplitOverlayBody(st, layout, SplitOverlaySpec{
		LeftPane: OverlayPaneSpec{
			Body:    "session list",
			Focused: true,
		},
		RightPane: OverlayPaneSpec{
			Title: "Preview",
			Body:  strings.Repeat("preview line\n", 2) + "wrapped preview content",
		},
	})
	view := RenderOverlayFrame(st, layout.FrameWidth, OverlayFrameSpec{
		HeaderLines: []string{
			st.Title.Render("Search Sessions"),
			"Search: query",
			RenderOverlayDivider(st, layout.ContentWidth),
			st.Muted.Render("Searching…"),
		},
		Body:   body,
		Footer: "[Tab] Focus  [Ctrl+S] Toggle scope  [Enter] Open  [Esc] Close",
	})

	assertFits(t, view, 72, 18)
}

func TestOverlayPaneChangesWhenFocused(t *testing.T) {
	st := testOverlayStyles()
	if reflect.DeepEqual(st.OverlayPane, st.OverlayPaneFocused) {
		t.Fatal("expected focused overlay pane style to differ from unfocused style")
	}
}

func TestApplyOverlayListStylesReturnsUpdatedModel(t *testing.T) {
	delegate := list.NewDefaultDelegate()
	model := list.New(nil, delegate, 20, 5)
	updated := ApplyOverlayListStyles(model, testOverlayStyles())
	if reflect.DeepEqual(model.Styles.NoItems, updated.Styles.NoItems) {
		t.Fatal("expected overlay list styles to change no-items style")
	}
	if reflect.DeepEqual(model.Styles.StatusEmpty, updated.Styles.StatusEmpty) {
		t.Fatal("expected overlay list styles to change empty-status style")
	}
}

func TestOverlayFrameChangesWhenFocused(t *testing.T) {
	st := testOverlayStyles()
	if reflect.DeepEqual(st.OverlayFrame, st.OverlayFrameFocused) {
		t.Fatal("expected focused overlay frame style to differ from unfocused style")
	}
}

func TestRenderOverlayFrameChangesWhenFocused(t *testing.T) {
	st := testOverlayStyles()
	base := RenderOverlayFrame(st, 48, OverlayFrameSpec{Body: "body"})
	focused := RenderOverlayFrame(st, 48, OverlayFrameSpec{Body: "body", Focused: true})
	assertFits(t, base, 50, 3)
	assertFits(t, focused, 50, 3)
}

func TestRenderSplitOverlayBodyUsesConfiguredDividerWidth(t *testing.T) {
	st := testOverlayStyles()
	layout := ComputeSplitOverlayLayout(72, 18, 11, testSplitOverlaySpec)
	pane := renderOverlayPane(st, layout.RightPaneWidth, layout.BodyHeight, OverlayPaneSpec{
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
	if got := strings.Count(dividerLine, "─"); got == 0 || got > layout.RightInnerWidth {
		t.Fatalf("divider width = %d, want > 0 and <= %d\nline: %q", got, layout.RightInnerWidth, dividerLine)
	}
}

func TestRenderOverlayPaneClipsBodyToRequestedHeight(t *testing.T) {
	st := testOverlayStyles()
	layout := ComputeSplitOverlayLayout(72, 18, 11, testSplitOverlaySpec)
	pane := renderOverlayPane(st, layout.RightPaneWidth, layout.BodyHeight, OverlayPaneSpec{
		Title: "Preview",
		Body:  strings.Repeat("overflow line that must stay inside the pane\n", layout.BodyHeight+4) + "tail",
	})

	assertFits(t, pane, layout.RightPaneWidth, layout.BodyHeight)
	if !strings.Contains(pane, "Preview") {
		t.Fatalf("pane = %q, want title preserved after clipping", pane)
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

func TestRenderSplitOverlayBodySeparatorWidth(t *testing.T) {
	// In lipgloss v1, Width(w) sets the inner area including padding; visual width = w + border(2).
	// renderOverlayPane passes Width(InnerWidth(paneWidth)) where InnerWidth removes border+padding.
	// So each pane renders at visual = InnerWidth(paneWidth) + border(2) = paneWidth - paddingH.
	// With paddingH=2, each pane is paneWidth-2 visually. The separator must be exactly 1 char.
	st := testOverlayStyles()
	layout := ComputeSplitOverlayLayout(72, 18, 11, testSplitOverlaySpec)
	body := RenderSplitOverlayBody(st, layout, SplitOverlaySpec{
		LeftPane: OverlayPaneSpec{
			Title:   "Work Items",
			Body:    strings.Repeat("item\n", layout.ListHeight),
			Focused: true,
		},
		RightPane: OverlayPaneSpec{
			Title: "Details",
			Body:  "detail content",
		},
	})

	// Each pane renders 2 chars narrower than its allocated width (border adds 2 outside Width).
	panePaddingH := styles.DefaultChromeMetrics.OverlayPane.PaddingLeft + styles.DefaultChromeMetrics.OverlayPane.PaddingRight
	wantWidth := (layout.LeftPaneWidth - panePaddingH) + 1 + (layout.RightPaneWidth - panePaddingH)
	lines := strings.Split(body, "\n")
	if len(lines) != layout.BodyHeight {
		t.Fatalf("split body line count = %d, want %d", len(lines), layout.BodyHeight)
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got != wantWidth {
			t.Fatalf("line %d width = %d, want %d (leftPane=%d + sep=1 + rightPane=%d)\nline: %q",
				i+1, got, wantWidth, layout.LeftPaneWidth-panePaddingH, layout.RightPaneWidth-panePaddingH, line)
		}
	}
}
