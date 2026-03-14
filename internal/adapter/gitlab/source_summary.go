package gitlab

import (
	"fmt"
	"strings"

	"github.com/beeemT/substrate/internal/domain"
)

func gitlabIssueSourceSummaries(issues []issue) []domain.SourceSummary {
	summaries := make([]domain.SourceSummary, 0, len(issues))
	for _, issue := range issues {
		ref := gitlabProjectPath(issue)
		if issue.IID > 0 {
			if ref != "" {
				ref = fmt.Sprintf("%s#%d", ref, issue.IID)
			} else {
				ref = fmt.Sprintf("#%d", issue.IID)
			}
		}
		summaries = append(summaries, domain.SourceSummary{
			Provider: "gitlab",
			Ref:      ref,
			Title:    strings.TrimSpace(issue.Title),
			Excerpt:  gitlabSummaryExcerpt(issue.Description),
			URL:      strings.TrimSpace(issue.WebURL),
		})
	}
	return summaries
}

func gitlabMilestoneSourceSummaries(projectID int64, milestones []milestone) []domain.SourceSummary {
	summaries := make([]domain.SourceSummary, 0, len(milestones))
	for _, milestone := range milestones {
		summaries = append(summaries, domain.SourceSummary{
			Provider: "gitlab",
			Ref:      fmt.Sprintf("project %d milestone #%d", projectID, milestone.ID),
			Title:    strings.TrimSpace(milestone.Title),
			Excerpt:  gitlabSummaryExcerpt(milestone.Description),
		})
	}
	return summaries
}

func gitlabSummaryExcerpt(text string) string {
	trimmed := strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if len(trimmed) <= 240 {
		return trimmed
	}
	return strings.TrimSpace(trimmed[:237]) + "..."
}
