package gitlab

import (
	"fmt"
	"sort"
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
			Provider:    "gitlab",
			Kind:        "issue",
			Ref:         ref,
			Title:       strings.TrimSpace(issue.Title),
			Description: strings.TrimSpace(issue.Description),
			Excerpt:     gitlabSummaryExcerpt(issue.Description),
			State:       strings.TrimSpace(issue.State),
			Labels:      gitlabSourceLabels(issue.Labels),
			Container:   gitlabProjectPath(issue),
			URL:         strings.TrimSpace(issue.WebURL),
			CreatedAt:   issue.CreatedAt,
			UpdatedAt:   issue.UpdatedAt,
		})
	}

	return summaries
}

func gitlabMilestoneSourceSummaries(projectID int64, milestones []milestone) []domain.SourceSummary {
	summaries := make([]domain.SourceSummary, 0, len(milestones))
	for _, milestone := range milestones {
		summaries = append(summaries, domain.SourceSummary{
			Provider:    "gitlab",
			Kind:        "milestone",
			Ref:         fmt.Sprintf("project %d milestone #%d", projectID, milestone.ID),
			Title:       strings.TrimSpace(milestone.Title),
			Description: strings.TrimSpace(milestone.Description),
			Excerpt:     gitlabSummaryExcerpt(milestone.Description),
			State:       strings.TrimSpace(milestone.State),
			Container:   fmt.Sprintf("project %d", projectID),
			CreatedAt:   milestone.CreatedAt,
			UpdatedAt:   milestone.UpdatedAt,
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

func gitlabSourceLabels(labels []string) []string {
	if len(labels) == 0 {
		return nil
	}
	clone := append([]string(nil), labels...)
	sort.Strings(clone)

	return clone
}
