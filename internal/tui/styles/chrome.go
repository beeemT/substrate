package styles

// BoxMetrics describes the border and padding consumed by a semantic chrome box.
type BoxMetrics struct {
	BorderLeft, BorderRight   int
	BorderTop, BorderBottom   int
	PaddingLeft, PaddingRight int
	PaddingTop, PaddingBottom int
}

func (m BoxMetrics) HorizontalFrame() int {
	return m.BorderLeft + m.BorderRight + m.PaddingLeft + m.PaddingRight
}

func (m BoxMetrics) VerticalFrame() int {
	return m.BorderTop + m.BorderBottom + m.PaddingTop + m.PaddingBottom
}

func (m BoxMetrics) InnerWidth(width int) int {
	return maxInt(1, width-m.HorizontalFrame())
}

func (m BoxMetrics) InnerHeight(height int) int {
	return maxInt(1, height-m.VerticalFrame())
}

// ChromeMetrics centralizes the geometry consumed by shared TUI chrome.
type ChromeMetrics struct {
	StatusBarHeight    int
	ToastTopInset      int
	Pane               BoxMetrics
	OverlayFrame       BoxMetrics
	OverlayPane        BoxMetrics
	Callout            BoxMetrics
	SettingsFooter     BoxMetrics
	MinWidthForPaneGap int
}

// DefaultChromeMetrics is the single source of truth for shared shell geometry.
var DefaultChromeMetrics = ChromeMetrics{
	StatusBarHeight: 1,
	ToastTopInset:   1,
	Pane: BoxMetrics{
		BorderLeft: 1, BorderRight: 1,
		BorderTop: 1, BorderBottom: 1,
	},
	OverlayFrame: BoxMetrics{
		BorderLeft: 1, BorderRight: 1,
		BorderTop: 1, BorderBottom: 1,
		PaddingLeft: 2, PaddingRight: 2,
	},
	OverlayPane: BoxMetrics{
		BorderLeft: 1, BorderRight: 1,
		BorderTop: 1, BorderBottom: 1,
		PaddingLeft: 1, PaddingRight: 1,
	},
	Callout: BoxMetrics{
		BorderLeft: 1, BorderRight: 1,
		BorderTop: 1, BorderBottom: 1,
		PaddingLeft: 1, PaddingRight: 1,
	},
	SettingsFooter: BoxMetrics{
		BorderTop:   1,
		PaddingLeft: 1, PaddingRight: 1,
	},
	MinWidthForPaneGap: 60,
}

// MainPageLayout captures shared pane shell geometry for the two-pane app shell.
type MainPageLayout struct {
	SidebarPaneWidth  int
	SidebarInnerWidth int
	ContentPaneWidth  int
	ContentInnerWidth int
	PaneGapWidth      int
	BodyHeight        int
	PaneInnerHeight   int
}

// ComputeMainPageLayout computes the app shell pane sizes from the shared chrome metrics.
func ComputeMainPageLayout(totalWidth, totalHeight, sidebarInnerWidth int, chrome ChromeMetrics, statusBarHeight int) MainPageLayout {
	bodyHeight := maxInt(0, totalHeight-statusBarHeight)
	paneInnerHeight := chrome.Pane.InnerHeight(bodyHeight)

	sidebarPaneWidth := minInt(maxInt(0, totalWidth), maxInt(0, sidebarInnerWidth)+chrome.Pane.HorizontalFrame())
	// Only allocate a gap column when the terminal is wide enough to afford it.
	// On narrow terminals every column belongs to content.
	paneGapWidth := 0
	if totalWidth >= chrome.MinWidthForPaneGap {
		paneGapWidth = 1
	}
	contentPaneWidth := maxInt(0, totalWidth-sidebarPaneWidth)
	minPaneWidth := chrome.Pane.HorizontalFrame()
	if sidebarPaneWidth > 0 && contentPaneWidth > 0 && paneGapWidth > 0 {
		requiredContentWidth := minPaneWidth + paneGapWidth
		if availableContentWidth := totalWidth - sidebarPaneWidth; availableContentWidth < requiredContentWidth {
			sidebarPaneWidth = maxInt(0, sidebarPaneWidth-(requiredContentWidth-availableContentWidth))
		}
		contentPaneWidth = maxInt(0, totalWidth-sidebarPaneWidth-paneGapWidth)
	}

	return MainPageLayout{
		SidebarPaneWidth:  sidebarPaneWidth,
		SidebarInnerWidth: chrome.Pane.InnerWidth(sidebarPaneWidth),
		ContentPaneWidth:  contentPaneWidth,
		ContentInnerWidth: chrome.Pane.InnerWidth(contentPaneWidth),
		PaneGapWidth:      paneGapWidth,
		BodyHeight:        bodyHeight,
		PaneInnerHeight:   paneInnerHeight,
	}
}

// ToastPlacement captures shared overlay anchoring against the shell chrome.
type ToastPlacement struct {
	TopInset    int
	BottomInset int
}

// ComputeToastPlacement anchors top-right overlays relative to shared shell chrome.
func ComputeToastPlacement(chrome ChromeMetrics) ToastPlacement {
	return ToastPlacement{
		TopInset:    chrome.ToastTopInset,
		BottomInset: chrome.StatusBarHeight,
	}
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
