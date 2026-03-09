package components

import (
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"
)

const (
	// OverlayHorizontalFrame is the horizontal border width consumed by the outer overlay frame.
	OverlayHorizontalFrame = 2
	// OverlayHorizontalPad is the horizontal padding consumed by the outer overlay frame.
	OverlayHorizontalPad = 4
	// PaneHorizontalFrame is the horizontal border plus padding width consumed by a pane.
	PaneHorizontalFrame = 4
	// PaneVerticalFrame is the vertical border width consumed by a pane.
	PaneVerticalFrame = 2
	// OverlayBackgroundColor is the shared background color used for overlay content.
	OverlayBackgroundColor = "#1a1a2e"

	overlayBorderColor      = "#2d2d44"
	overlayFocusBorderColor = "#60a5fa"
)

// SplitOverlaySizingSpec describes the geometry constraints for a split list/detail overlay.
type SplitOverlaySizingSpec struct {
	MaxOverlayWidth   int
	LeftMinWidth      int
	RightMinWidth     int
	LeftWeight        int
	RightWeight       int
	MinBodyHeight     int
	DefaultBodyHeight int
	HeightRatioNum    int
	HeightRatioDen    int
	InputWidthOffset  int
}

// SplitOverlayLayout is the computed geometry for rendering a split overlay.
type SplitOverlayLayout struct {
	FrameWidth      int
	ContentWidth    int
	InputWidth      int
	BodyHeight      int
	LeftPaneWidth   int
	RightPaneWidth  int
	LeftInnerWidth  int
	RightInnerWidth int
	ListHeight      int
	ViewportWidth   int
	ViewportHeight  int
}

// OverlayFrameSpec describes the outer overlay shell.
type OverlayFrameSpec struct {
	HeaderLines []string
	Body        string
	Footer      string
}

// OverlayPaneSpec describes a bordered pane inside a split overlay body.
type OverlayPaneSpec struct {
	Title        string
	DividerWidth int
	Body         string
	Focused      bool
}

// SplitOverlaySpec describes the split-pane body content.
type SplitOverlaySpec struct {
	LeftPane  OverlayPaneSpec
	RightPane OverlayPaneSpec
}

// ApplyOverlayListStyles applies shared overlay background styling to list empty states.
func ApplyOverlayListStyles(m list.Model) list.Model {
	bg := lipgloss.Color(OverlayBackgroundColor)
	m.Styles.NoItems = m.Styles.NoItems.Background(bg)
	m.Styles.StatusEmpty = m.Styles.StatusEmpty.Background(bg)
	return m
}

// ComputeSplitOverlayLayout calculates the shared geometry for split overlays.
func ComputeSplitOverlayLayout(termWidth, termHeight, chromeLines int, spec SplitOverlaySizingSpec) SplitOverlayLayout {
	frameWidth := overlayFrameWidth(termWidth, spec.MaxOverlayWidth)
	contentWidth := maxInt(1, frameWidth-OverlayHorizontalPad)
	bodyHeight := overlayBodyHeight(termHeight, chromeLines, spec)
	leftPaneWidth, rightPaneWidth := splitPaneWidths(contentWidth, spec)

	return SplitOverlayLayout{
		FrameWidth:      frameWidth,
		ContentWidth:    contentWidth,
		InputWidth:      maxInt(1, contentWidth-inputWidthOffset(spec.InputWidthOffset)),
		BodyHeight:      bodyHeight,
		LeftPaneWidth:   leftPaneWidth,
		RightPaneWidth:  rightPaneWidth,
		LeftInnerWidth:  maxInt(1, leftPaneWidth-PaneHorizontalFrame),
		RightInnerWidth: maxInt(1, rightPaneWidth-PaneHorizontalFrame),
		ListHeight:      maxInt(1, bodyHeight-PaneVerticalFrame),
		ViewportWidth:   maxInt(1, rightPaneWidth-PaneHorizontalFrame-2),
		ViewportHeight:  maxInt(1, bodyHeight-PaneVerticalFrame-2),
	}
}

// RenderOverlayFrame renders the outer overlay shell around header, body, and footer content.
func RenderOverlayFrame(frameWidth int, spec OverlayFrameSpec) string {
	parts := append([]string{}, spec.HeaderLines...)
	if spec.Body != "" {
		if len(parts) > 0 {
			parts = append(parts, "")
		}
		parts = append(parts, spec.Body)
	}
	if spec.Footer != "" {
		parts = append(parts, spec.Footer)
	}
	content := strings.Join(parts, "\n")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(overlayBorderColor)).
		Background(lipgloss.Color(OverlayBackgroundColor)).
		Padding(0, 2).
		Width(maxInt(1, frameWidth)).
		Render(content)
}

// RenderSplitOverlayBody renders a split left/right pane body using a computed layout.
func RenderSplitOverlayBody(layout SplitOverlayLayout, spec SplitOverlaySpec) string {
	leftPane := renderOverlayPane(layout.LeftInnerWidth, layout.ListHeight, spec.LeftPane)
	rightPane := renderOverlayPane(layout.RightInnerWidth, layout.ListHeight, spec.RightPane)
	return lipgloss.JoinHorizontal(lipgloss.Top, leftPane, " ", rightPane)
}

// RenderOverlayDivider renders a muted divider line for overlay content.
func RenderOverlayDivider(width int) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(overlayBorderColor)).
		Width(maxInt(1, width)).
		Render(strings.Repeat("─", maxInt(1, width)))
}

func renderOverlayPane(width, height int, spec OverlayPaneSpec) string {
	body := spec.Body
	if spec.Title != "" {
		dividerWidth := spec.DividerWidth
		if dividerWidth <= 0 {
			dividerWidth = maxInt(1, width-2)
		}
		body = strings.Join([]string{spec.Title, RenderOverlayDivider(dividerWidth), spec.Body}, "\n")
	}
	return lipgloss.NewStyle().
		Width(maxInt(1, width)).
		Height(maxInt(1, height)).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(overlayPaneBorderColor(spec.Focused)).
		Padding(0, 1).
		Render(body)
}

func overlayPaneBorderColor(focused bool) lipgloss.Color {
	if focused {
		return lipgloss.Color(overlayFocusBorderColor)
	}
	return lipgloss.Color(overlayBorderColor)
}

func overlayFrameWidth(termWidth, maxOverlayWidth int) int {
	if termWidth <= 0 {
		if maxOverlayWidth > 0 {
			return maxInt(1, maxOverlayWidth-OverlayHorizontalFrame)
		}
		return 1
	}
	frameWidth := maxInt(1, termWidth-OverlayHorizontalFrame)
	if maxOverlayWidth > 0 {
		frameWidth = maxInt(1, minInt(frameWidth, maxOverlayWidth-OverlayHorizontalFrame))
	}
	return frameWidth
}

func overlayBodyHeight(termHeight, chromeLines int, spec SplitOverlaySizingSpec) int {
	minBodyHeight := maxInt(1, spec.MinBodyHeight)
	target := maxInt(minBodyHeight, spec.DefaultBodyHeight)
	if termHeight > 0 && spec.HeightRatioNum > 0 && spec.HeightRatioDen > 0 {
		target = maxInt(minBodyHeight, ceilDiv(termHeight*spec.HeightRatioNum, spec.HeightRatioDen))
	}
	if termHeight <= 0 {
		return maxInt(1, target)
	}
	maxHeight := termHeight - chromeLines
	if maxHeight < 1 {
		return 1
	}
	return maxInt(1, minInt(target, maxHeight))
}

func splitPaneWidths(contentWidth int, spec SplitOverlaySizingSpec) (int, int) {
	available := maxInt(1, contentWidth-1)
	leftMinWidth := maxInt(1, spec.LeftMinWidth)
	rightMinWidth := maxInt(1, spec.RightMinWidth)
	if contentWidth <= leftMinWidth+rightMinWidth+1 {
		leftWidth := maxInt(1, available/2)
		rightWidth := maxInt(1, available-leftWidth)
		return leftWidth, rightWidth
	}

	leftWeight, rightWeight := splitWeights(spec)
	leftWidth := maxInt(leftMinWidth, contentWidth*leftWeight/(leftWeight+rightWeight))
	rightWidth := maxInt(rightMinWidth, available-leftWidth)
	if leftWidth+rightWidth > available {
		rightWidth = maxInt(1, available-leftWidth)
	}
	if rightWidth < rightMinWidth {
		rightWidth = rightMinWidth
		leftWidth = maxInt(1, available-rightWidth)
	}
	return leftWidth, rightWidth
}

func inputWidthOffset(offset int) int {
	if offset > 0 {
		return offset
	}
	return 20
}

func splitWeights(spec SplitOverlaySizingSpec) (int, int) {
	leftWeight := spec.LeftWeight
	rightWeight := spec.RightWeight
	if leftWeight <= 0 {
		leftWeight = 2
	}
	if rightWeight <= 0 {
		rightWeight = 3
	}
	return leftWeight, rightWeight
}

func ceilDiv(n, d int) int {
	if d <= 0 {
		return 0
	}
	return (n + d - 1) / d
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
