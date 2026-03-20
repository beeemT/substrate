package views

import (
	"github.com/beeemT/substrate/internal/tui/components"
)

func componentHints(hints []KeybindHint) []components.KeyHint {
	converted := make([]components.KeyHint, 0, len(hints))
	for _, hint := range hints {
		converted = append(converted, components.KeyHint{Key: hint.Key, Label: hint.Label})
	}

	return converted
}
