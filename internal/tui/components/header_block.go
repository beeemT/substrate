package components

import (
	"strings"

	"github.com/beeemT/substrate/internal/tui/styles"
)

// HeaderBlockSpec describes a semantic title/meta/divider block.
type HeaderBlockSpec struct {
	Title   string
	Meta    string
	Width   int
	Divider bool
	// StatusLine, when non-empty and Divider is true, replaces the ─── divider
	// row with this pre-rendered string. The header line count is unchanged;
	// callers can use this to show a transient warning in-place without pushing
	// subsequent content down.
	StatusLine string
}

// RenderDivider renders a full-width semantic divider.
func RenderDivider(st styles.Styles, width int) string {
	return st.Divider.Width(maxInt(1, width)).Render(strings.Repeat("─", maxInt(1, width)))
}

// RenderHeaderBlock renders shared title/meta/divider workflow chrome.
func RenderHeaderBlock(st styles.Styles, spec HeaderBlockSpec) string {
	lines := []string{st.Title.Render(spec.Title)}
	if strings.TrimSpace(spec.Meta) != "" {
		lines = append(lines, st.SectionLabel.Render(spec.Meta))
	}
	if spec.Divider {
		if spec.StatusLine != "" {
			lines = append(lines, spec.StatusLine)
		} else {
			lines = append(lines, RenderDivider(st, spec.Width))
		}
	}
	return strings.Join(lines, "\n")
}
