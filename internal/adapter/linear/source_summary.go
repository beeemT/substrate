package linear

import (
	"strings"

	"github.com/beeemT/substrate/internal/domain"
)

func linearIssueSourceSummaries(issues []linearIssue) []domain.SourceSummary {
	summaries := make([]domain.SourceSummary, 0, len(issues))
	for _, issue := range issues {
		summaries = append(summaries, domain.SourceSummary{
			Provider: "linear",
			Ref:      strings.TrimSpace(issue.Identifier),
			Title:    strings.TrimSpace(issue.Title),
			Excerpt:  linearSummaryExcerpt(issue.Description),
			URL:      strings.TrimSpace(issue.URL),
		})
	}
	return summaries
}

func linearProjectSourceSummaries(projects []linearProject) []domain.SourceSummary {
	summaries := make([]domain.SourceSummary, 0, len(projects))
	for _, project := range projects {
		summaries = append(summaries, domain.SourceSummary{
			Provider: "linear",
			Ref:      strings.TrimSpace(project.ID),
			Title:    strings.TrimSpace(project.Name),
			Excerpt:  linearSummaryExcerpt(project.Description),
		})
	}
	return summaries
}

func linearSummaryExcerpt(text string) string {
	trimmed := strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if len(trimmed) <= 240 {
		return trimmed
	}
	return strings.TrimSpace(trimmed[:237]) + "..."
}
