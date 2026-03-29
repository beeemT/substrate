package domain

import (
	"fmt"
	"sort"
	"strings"
)

// ComposePlanDocument reconstructs the full reviewable plan document from the
// persisted orchestration plan and repo sub-plans.
func ComposePlanDocument(plan Plan, subPlans []TaskPlan) string {
	ordered := orderedTaskPlans(subPlans)
	groups := executionGroupsFromTaskPlans(ordered)

	var b strings.Builder
	b.WriteString("```substrate-plan\n")
	if len(groups) == 0 {
		b.WriteString("execution_groups: []\n")
	} else {
		b.WriteString("execution_groups:\n")
		for _, group := range groups {
			b.WriteString("  - [")
			b.WriteString(strings.Join(group, ", "))
			b.WriteString("]\n")
		}
	}
	b.WriteString("```\n\n## Orchestration\n")
	if orchestration := strings.TrimSpace(plan.OrchestratorPlan); orchestration != "" {
		b.WriteString(orchestration)
		b.WriteString("\n")
	}
	for _, sp := range ordered {
		b.WriteString("\n## SubPlan: ")
		b.WriteString(sp.RepositoryName)
		b.WriteString("\n")
		b.WriteString(strings.TrimSpace(sp.Content))
		b.WriteString("\n")
	}

	return strings.TrimRight(b.String(), "\n")
}

// ComposeSubPlansContent assembles all sub-plan contents into a single comment-ready string.
// A single sub-plan is returned as-is (trimmed). Multiple sub-plans are each prefixed with
// a "## SubPlan: {repo}" heading and separated by a blank line.
func ComposeSubPlansContent(subPlans []TaskPlan) string {
	ordered := orderedTaskPlans(subPlans)
	if len(ordered) == 0 {
		return ""
	}
	if len(ordered) == 1 {
		return strings.TrimSpace(ordered[0].Content)
	}
	var b strings.Builder
	for i, sp := range ordered {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("## SubPlan: ")
		b.WriteString(sp.RepositoryName)
		b.WriteString("\n")
		b.WriteString(strings.TrimSpace(sp.Content))
	}
	return b.String()
}

func orderedTaskPlans(subPlans []TaskPlan) []TaskPlan {
	ordered := append([]TaskPlan(nil), subPlans...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Order != ordered[j].Order {
			return ordered[i].Order < ordered[j].Order
		}

		return strings.ToLower(ordered[i].RepositoryName) < strings.ToLower(ordered[j].RepositoryName)
	})

	return ordered
}

func executionGroupsFromTaskPlans(subPlans []TaskPlan) [][]string {
	if len(subPlans) == 0 {
		return nil
	}
	ordered := orderedTaskPlans(subPlans)
	groups := make([][]string, 0, 1)
	currentOrder := ordered[0].Order
	currentGroup := make([]string, 0, 1)
	for _, sp := range ordered {
		if sp.Order != currentOrder {
			groups = append(groups, append([]string(nil), currentGroup...))
			currentOrder = sp.Order
			currentGroup = make([]string, 0, 1)
		}
		currentGroup = append(currentGroup, sp.RepositoryName)
	}
	groups = append(groups, append([]string(nil), currentGroup...))

	return groups
}

// FormatIncompleteSubPlanIssue standardizes parser feedback for insufficiently detailed sub-plans.
func FormatIncompleteSubPlanIssue(repoName, detail string) string {
	return fmt.Sprintf("%s: %s", repoName, detail)
}
