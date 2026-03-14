package domain

import (
	"strings"
	"testing"
)

func TestComposePlanDocument(t *testing.T) {
	plan := Plan{ID: "plan-1", WorkItemID: "wi-1", OrchestratorPlan: "Coordinate repo changes."}
	subPlans := []TaskPlan{
		{ID: "sp-b", PlanID: plan.ID, RepositoryName: "repo-b", Order: 1, Content: "### Goal\nShip repo b."},
		{ID: "sp-a", PlanID: plan.ID, RepositoryName: "repo-a", Order: 0, Content: "### Goal\nShip repo a."},
		{ID: "sp-c", PlanID: plan.ID, RepositoryName: "repo-c", Order: 1, Content: "### Goal\nShip repo c."},
	}

	doc := ComposePlanDocument(plan, subPlans)
	for _, want := range []string{
		"```substrate-plan",
		"execution_groups:",
		"  - [repo-a]",
		"  - [repo-b, repo-c]",
		"## Orchestration",
		"Coordinate repo changes.",
		"## SubPlan: repo-a",
		"## SubPlan: repo-b",
		"## SubPlan: repo-c",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("document = %q, want %q", doc, want)
		}
	}
}

func TestComposePlanDocument_EmptySubPlans(t *testing.T) {
	doc := ComposePlanDocument(Plan{OrchestratorPlan: "Only orchestration."}, nil)
	if !strings.Contains(doc, "execution_groups: []") {
		t.Fatalf("document = %q, want empty execution_groups", doc)
	}
	if !strings.Contains(doc, "## Orchestration\nOnly orchestration.") {
		t.Fatalf("document = %q, want orchestration section", doc)
	}
}
