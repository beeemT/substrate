package components

import (
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"

	"github.com/beeemT/substrate/internal/tui/styles"
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
func ApplyOverlayListStyles(m list.Model, st styles.Styles) list.Model {
	bg := lipgloss.Color(st.Theme.OverlayBg)
	muted := lipgloss.Color(st.Theme.Muted)
	m.Styles.NoItems = m.Styles.NoItems.Background(bg).Foreground(muted)
	m.Styles.StatusEmpty = m.Styles.StatusEmpty.Background(bg).Foreground(muted)
	return m
}

// ComputeSplitOverlayLayout calculates the shared geometry for split overlays.
func ComputeSplitOverlayLayout(termWidth, termHeight, chromeLines int, spec SplitOverlaySizingSpec) SplitOverlayLayout {
	chrome := styles.DefaultChromeMetrics
	frameWidth := overlayFrameWidth(termWidth, spec.MaxOverlayWidth, chrome)
	contentWidth := chrome.OverlayFrame.InnerWidth(frameWidth)
	bodyHeight := overlayBodyHeight(termHeight, chromeLines, spec)
	leftPaneWidth, rightPaneWidth := splitPaneWidths(contentWidth, spec)
	leftInnerWidth := chrome.OverlayPane.InnerWidth(leftPaneWidth)
	rightInnerWidth := chrome.OverlayPane.InnerWidth(rightPaneWidth)
	paneInnerHeight := chrome.OverlayPane.InnerHeight(bodyHeight)

	return SplitOverlayLayout{
		FrameWidth:      frameWidth,
		ContentWidth:    contentWidth,
		InputWidth:      maxInt(1, contentWidth-inputWidthOffset(spec.InputWidthOffset)),
		BodyHeight:      bodyHeight,
		LeftPaneWidth:   leftPaneWidth,
		RightPaneWidth:  rightPaneWidth,
		LeftInnerWidth:  leftInnerWidth,
		RightInnerWidth: rightInnerWidth,
		ListHeight:      paneInnerHeight,
		ViewportWidth:   maxInt(1, rightInnerWidth-2),
		ViewportHeight:  maxInt(1, paneInnerHeight-2),
	}
}

// RenderOverlayFrame renders the outer overlay shell around header, body, and footer content.
func RenderOverlayFrame(st styles.Styles, frameWidth int, spec OverlayFrameSpec) string {
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
	return st.OverlayFrame.Copy().
		Width(maxInt(1, frameWidth)).
		Render(content)
}

// RenderSplitOverlayBody renders a split left/right pane body using a computed layout.
func RenderSplitOverlayBody(st styles.Styles, layout SplitOverlayLayout, spec SplitOverlaySpec) string {
	leftPane := renderOverlayPane(st, layout.LeftPaneWidth, layout.BodyHeight, spec.LeftPane)
	rightPane := renderOverlayPane(st, layout.RightPaneWidth, layout.BodyHeight, spec.RightPane)
	return lipgloss.JoinHorizontal(lipgloss.Top, leftPane, " ", rightPane)
}

// RenderOverlayDivider renders a semantic divider line for overlay content.
func RenderOverlayDivider(st styles.Styles, width int) string {
	return st.Divider.Render(strings.Repeat("─", maxInt(1, width)))
}

func renderOverlayPane(st styles.Styles, width, height int, spec OverlayPaneSpec) string {
	body := spec.Body
	if spec.Title != "" {
		titleLines := []string{st.Title.Render(spec.Title)}
		if spec.DividerWidth > 0 {
			titleLines = append(titleLines, RenderOverlayDivider(st, spec.DividerWidth+st.Chrome.OverlayPane.HorizontalFrame()))
		}
		titleLines = append(titleLines, spec.Body)
		body = strings.Join(titleLines, "\n")
	}
	paneStyle := st.OverlayPane
	if spec.Focused {
		paneStyle = st.OverlayPaneFocused
	}
	return paneStyle.Copy().
		Width(st.Chrome.OverlayPane.InnerWidth(maxInt(1, width))).
		Height(st.Chrome.OverlayPane.InnerHeight(maxInt(1, height))).
		Render(body)
}

func overlayFrameWidth(termWidth, maxOverlayWidth int, chrome styles.ChromeMetrics) int {
	reservedHorizontalInset := chrome.OverlayFrame.BorderLeft + chrome.OverlayFrame.BorderRight
	if termWidth <= 0 {
		if maxOverlayWidth > 0 {
			return maxInt(1, maxOverlayWidth-reservedHorizontalInset)
		}
		return 1
	}
	frameWidth := maxInt(1, termWidth-reservedHorizontalInset)
	if maxOverlayWidth > 0 {
		frameWidth = maxInt(1, minInt(frameWidth, maxOverlayWidth-reservedHorizontalInset))
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
