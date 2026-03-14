package sentry

import (
	"strings"

	"github.com/beeemT/substrate/internal/domain"
)

func sentrySourceSummaries(issues []sentryIssue) []domain.SourceSummary {
	summaries := make([]domain.SourceSummary, 0, len(issues))
	for _, issue := range issues {
		summaries = append(summaries, domain.SourceSummary{
			Provider: "sentry",
			Ref:      issueIdentifier(issue),
			Title:    strings.TrimSpace(issue.Title),
			Excerpt:  sentrySummaryExcerpt(issue.Culprit + " " + issue.Status),
			URL:      strings.TrimSpace(issue.Permalink),
		})
	}
	return summaries
}

func sentrySummaryExcerpt(text string) string {
	trimmed := strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if len(trimmed) <= 240 {
		return trimmed
	}
	return strings.TrimSpace(trimmed[:237]) + "..."
}
