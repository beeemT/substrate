package github

import (
	"fmt"
	"strings"

	"github.com/beeemT/substrate/internal/domain"
)

func githubIssueSourceSummaries(issues []githubIssue) []domain.SourceSummary {
	summaries := make([]domain.SourceSummary, 0, len(issues))
	for _, issue := range issues {
		owner, repo := issueOwnerRepo(issue)
		ref := formatIssueSelectionID(owner, repo, issue.Number)
		summaries = append(summaries, domain.SourceSummary{
			Provider: "github",
			Ref:      ref,
			Title:    strings.TrimSpace(issue.Title),
			Excerpt:  summaryExcerpt(issue.Body),
			URL:      strings.TrimSpace(issue.HTMLURL),
		})
	}
	return summaries
}

func githubMilestoneSourceSummaries(owner, repo string, milestones []githubMilestone) []domain.SourceSummary {
	summaries := make([]domain.SourceSummary, 0, len(milestones))
	for _, milestone := range milestones {
		ref := fmt.Sprintf("%s/%s milestone #%d", owner, repo, milestone.Number)
		summaries = append(summaries, domain.SourceSummary{
			Provider: "github",
			Ref:      ref,
			Title:    strings.TrimSpace(milestone.Title),
			Excerpt:  summaryExcerpt(milestone.Description),
		})
	}
	return summaries
}

func summaryExcerpt(text string) string {
	trimmed := strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if len(trimmed) <= 240 {
		return trimmed
	}
	return strings.TrimSpace(trimmed[:237]) + "..."
}
