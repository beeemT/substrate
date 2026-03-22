package github

import (
	"fmt"
	"strings"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/domain"
)

func githubIssueSourceSummaries(issues []githubIssue) []domain.SourceSummary {
	summaries := make([]domain.SourceSummary, 0, len(issues))
	for _, issue := range issues {
		owner, repo := issueOwnerRepo(issue)
		ref := formatIssueSelectionID(owner, repo, issue.Number)
		summaries = append(summaries, domain.SourceSummary{
			Provider:    "github",
			Kind:        "issue",
			Ref:         ref,
			Title:       strings.TrimSpace(issue.Title),
			Description: strings.TrimSpace(issue.Body),
			Excerpt:     adapter.SummaryExcerpt(issue.Body),
			State:       strings.TrimSpace(issue.State),
			Labels:      issueLabels(issue),
			Container:   githubSourceContainer(owner, repo),
			URL:         strings.TrimSpace(issue.HTMLURL),
			CreatedAt:   issue.CreatedAt,
			UpdatedAt:   issue.UpdatedAt,
		})
	}

	return summaries
}

func githubMilestoneSourceSummaries(owner, repo string, milestones []githubMilestone) []domain.SourceSummary {
	summaries := make([]domain.SourceSummary, 0, len(milestones))
	for _, milestone := range milestones {
		ref := fmt.Sprintf("%s/%s milestone #%d", owner, repo, milestone.Number)
		summaries = append(summaries, domain.SourceSummary{
			Provider:    "github",
			Kind:        "milestone",
			Ref:         ref,
			Title:       strings.TrimSpace(milestone.Title),
			Description: strings.TrimSpace(milestone.Description),
			Excerpt:     adapter.SummaryExcerpt(milestone.Description),
			State:       strings.TrimSpace(milestone.State),
			Container:   githubSourceContainer(owner, repo),
			URL:         strings.TrimSpace(milestone.HTMLURL),
			CreatedAt:   milestone.CreatedAt,
			UpdatedAt:   milestone.UpdatedAt,
		})
	}

	return summaries
}

func githubSourceContainer(owner, repo string) string {
	owner = strings.TrimSpace(owner)
	repo = strings.TrimSpace(repo)
	if owner != "" && repo != "" {
		return owner + "/" + repo
	}
	if repo != "" {
		return repo
	}

	return owner
}
