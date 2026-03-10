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
}

// RenderDivider renders a full-width semantic divider.
func RenderDivider(st styles.Styles, width int) string {
	return st.Divider.Copy().Width(maxInt(1, width)).Render(strings.Repeat("─", maxInt(1, width)))
}

// RenderHeaderBlock renders shared title/meta/divider workflow chrome.
func RenderHeaderBlock(st styles.Styles, spec HeaderBlockSpec) string {
	lines := []string{st.Title.Render(spec.Title)}
	if strings.TrimSpace(spec.Meta) != "" {
		lines = append(lines, st.SectionLabel.Render(spec.Meta))
	}
	if spec.Divider {
		lines = append(lines, RenderDivider(st, spec.Width))
	}
	return strings.Join(lines, "\n")
}
