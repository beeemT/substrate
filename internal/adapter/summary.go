package adapter

import "strings"

// SummaryExcerpt normalises whitespace and truncates text to a short summary.
func SummaryExcerpt(text string) string {
	trimmed := strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if len(trimmed) <= 240 {
		return trimmed
	}

	return strings.TrimSpace(trimmed[:237]) + "..."
}
