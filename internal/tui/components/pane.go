package components

import "github.com/beeemT/substrate/internal/tui/styles"

// PaneSpec describes a bordered application pane.
type PaneSpec struct {
	Content string
	Width   int
	Height  int
	Focused bool
}

// PaneInnerSize returns the content box available inside a shared pane shell.
func PaneInnerSize(st styles.Styles, width, height int) (int, int) {
	return st.Chrome.Pane.InnerWidth(width), st.Chrome.Pane.InnerHeight(height)
}

// RenderPane renders shared bordered pane chrome around already-sized content.
func RenderPane(st styles.Styles, spec PaneSpec) string {
	paneStyle := st.Pane
	if spec.Focused {
		paneStyle = st.PaneFocused
	}
	if spec.Width > 0 {
		paneStyle = paneStyle.Width(st.Chrome.Pane.InnerWidth(spec.Width))
	}
	if spec.Height > 0 {
		paneStyle = paneStyle.Height(st.Chrome.Pane.InnerHeight(spec.Height))
	}

	return paneStyle.Render(spec.Content)
}
