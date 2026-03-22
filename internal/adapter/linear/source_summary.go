package linear

import (
	"strconv"
	"strings"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/domain"
)

func linearIssueSourceSummaries(issues []linearIssue) []domain.SourceSummary {
	summaries := make([]domain.SourceSummary, 0, len(issues))
	for _, issue := range issues {
		summaries = append(summaries, domain.SourceSummary{
			Provider:    "linear",
			Kind:        "issue",
			Ref:         strings.TrimSpace(issue.Identifier),
			Title:       strings.TrimSpace(issue.Title),
			Description: strings.TrimSpace(issue.Description),
			Excerpt:     adapter.SummaryExcerpt(issue.Description),
			State:       strings.TrimSpace(issue.State.Name),
			Labels:      labelNames(issue.Labels),
			Container:   strings.TrimSpace(issue.Team.Key),
			URL:         strings.TrimSpace(issue.URL),
			CreatedAt:   issue.CreatedAt,
			UpdatedAt:   issue.UpdatedAt,
			Metadata:    linearIssueSummaryMetadata(issue),
		})
	}

	return summaries
}

func linearProjectSourceSummaries(projects []linearProject) []domain.SourceSummary {
	summaries := make([]domain.SourceSummary, 0, len(projects))
	for _, project := range projects {
		summaries = append(summaries, domain.SourceSummary{
			Provider:    "linear",
			Kind:        "project",
			Ref:         strings.TrimSpace(project.ID),
			Title:       strings.TrimSpace(project.Name),
			Description: strings.TrimSpace(project.Description),
			Excerpt:     adapter.SummaryExcerpt(project.Description),
			State:       strings.TrimSpace(project.State),
			Metadata:    linearProjectSummaryMetadata(project),
		})
	}

	return summaries
}

func linearIssueSummaryMetadata(issue linearIssue) []domain.SourceMetadataField {
	fields := make([]domain.SourceMetadataField, 0, 4)
	if issue.Priority > 0 {
		fields = append(fields, domain.SourceMetadataField{Label: "Priority", Value: strconv.Itoa(issue.Priority)})
	}
	if issue.Assignee != nil && strings.TrimSpace(issue.Assignee.Name) != "" {
		fields = append(fields, domain.SourceMetadataField{Label: "Assignee", Value: strings.TrimSpace(issue.Assignee.Name)})
	}
	if issue.Creator != nil && strings.TrimSpace(issue.Creator.Name) != "" {
		fields = append(fields, domain.SourceMetadataField{Label: "Creator", Value: strings.TrimSpace(issue.Creator.Name)})
	}
	if strings.TrimSpace(issue.State.Type) != "" {
		fields = append(fields, domain.SourceMetadataField{Label: "Workflow", Value: strings.TrimSpace(issue.State.Type)})
	}

	return fields
}

func linearProjectSummaryMetadata(project linearProject) []domain.SourceMetadataField {
	fields := make([]domain.SourceMetadataField, 0, 2)
	if strings.TrimSpace(project.Icon) != "" {
		fields = append(fields, domain.SourceMetadataField{Label: "Icon", Value: strings.TrimSpace(project.Icon)})
	}
	if strings.TrimSpace(project.Color) != "" {
		fields = append(fields, domain.SourceMetadataField{Label: "Color", Value: strings.TrimSpace(project.Color)})
	}

	return fields
}
