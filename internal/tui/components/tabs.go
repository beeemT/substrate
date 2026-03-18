package components

import (
	"strings"

	"github.com/beeemT/substrate/internal/tui/styles"
)

// RenderTabs renders an active/inactive semantic tabs row.
func RenderTabs(st styles.Styles, labels []string, active int, separator string) string {
	if separator == "" {
		separator = "  │  "
	}
	parts := make([]string, 0, len(labels))
	for i, label := range labels {
		if i == active {
			parts = append(parts, st.TabActive.Render(label))

			continue
		}
		parts = append(parts, st.TabInactive.Render(label))
	}

	return strings.Join(parts, separator)
}
