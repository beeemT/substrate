package sentry

import (
	"strings"

	"github.com/beeemT/substrate/internal/domain"
)

func sentrySourceSummaries(issues []sentryIssue) []domain.SourceSummary {
	summaries := make([]domain.SourceSummary, 0, len(issues))
	seen := make(map[string]struct{}, len(issues))
	for _, issue := range issues {
		issueID := strings.TrimSpace(issue.ID)
		if issueID == "" {
			continue
		}
		if _, ok := seen[issueID]; ok {
			continue
		}
		seen[issueID] = struct{}{}
		summaries = append(summaries, domain.SourceSummary{
			Provider:    "sentry",
			Kind:        "issue",
			Ref:         issueIdentifier(issue),
			Title:       strings.TrimSpace(issue.Title),
			Description: strings.TrimSpace(issue.Culprit),
			Excerpt:     sentrySummaryExcerpt(issue.Culprit + " " + issue.Status),
			State:       strings.TrimSpace(issue.Status),
			Container:   strings.TrimSpace(issue.Project.Slug),
			URL:         strings.TrimSpace(issue.Permalink),
			Metadata:    sentryIssueSummaryMetadata(issue),
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

func sentryIssueSummaryMetadata(issue sentryIssue) []domain.SourceMetadataField {
	fields := make([]domain.SourceMetadataField, 0, 6)
	if strings.TrimSpace(issue.Project.Name) != "" {
		fields = append(fields, domain.SourceMetadataField{Label: "Project", Value: strings.TrimSpace(issue.Project.Name)})
	}
	if strings.TrimSpace(issue.Level) != "" {
		fields = append(fields, domain.SourceMetadataField{Label: "Level", Value: strings.TrimSpace(issue.Level)})
	}
	if value := strings.TrimSpace(issue.Count.String()); value != "" {
		fields = append(fields, domain.SourceMetadataField{Label: "Events", Value: value})
	}
	if value := strings.TrimSpace(issue.UserCount.String()); value != "" {
		fields = append(fields, domain.SourceMetadataField{Label: "Users", Value: value})
	}
	if issue.FirstSeen != nil && !issue.FirstSeen.IsZero() {
		fields = append(fields, domain.SourceMetadataField{Label: "First seen", Value: issue.FirstSeen.Time.UTC().Format("2006-01-02 15:04 MST")})
	}
	if issue.LastSeen != nil && !issue.LastSeen.IsZero() {
		fields = append(fields, domain.SourceMetadataField{Label: "Last seen", Value: issue.LastSeen.Time.UTC().Format("2006-01-02 15:04 MST")})
	}

	return fields
}
