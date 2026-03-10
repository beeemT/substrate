package components

import (
	"strings"

	"github.com/beeemT/substrate/internal/tui/styles"
)

// KeyHint is a semantic key/label pair.
type KeyHint struct {
	Key   string
	Label string
}

// RenderedKeyHint preserves both rendered and plain-text forms for layout decisions.
type RenderedKeyHint struct {
	Raw      string
	Rendered string
}

// RenderKeyHintFragments styles individual keybind hints for shared consumption.
func RenderKeyHintFragments(st styles.Styles, hints []KeyHint) []RenderedKeyHint {
	parts := make([]RenderedKeyHint, 0, len(hints))
	for _, hint := range hints {
		keyRaw := "[" + hint.Key + "]"
		labelRaw := " " + hint.Label
		parts = append(parts, RenderedKeyHint{
			Raw:      keyRaw + labelRaw,
			Rendered: st.KeybindAccent.Render(keyRaw) + st.Hint.Render(labelRaw),
		})
	}
	return parts
}

// RenderKeyHints renders a semantic keybind row.
func RenderKeyHints(st styles.Styles, hints []KeyHint, separator string) string {
	if separator == "" {
		separator = "  "
	}
	parts := RenderKeyHintFragments(st, hints)
	rendered := make([]string, 0, len(parts))
	for _, part := range parts {
		rendered = append(rendered, part.Rendered)
	}
	return strings.Join(rendered, separator)
}
