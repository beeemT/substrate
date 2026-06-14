package readmodel

import (
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/logic"
)

func TestSidebarIncludesVirtualAndAgentSessionNodes(t *testing.T) {
	now := time.Now()
	snapshot := logic.InitialSnapshot{
		Sessions: []domain.Session{{ID: "work-1", Source: "github", Title: "Fix bug", State: domain.SessionReviewing, UpdatedAt: now}},
		AgentSessions: []domain.AgentSession{{
			ID:             "agent-1",
			WorkItemID:     "work-1",
			Kind:           domain.AgentSessionKindImplementation,
			RepositoryName: "repo-a",
		}},
	}
	entries := New().TaskSidebar(snapshot, "work-1")
	if len(entries) < 4 {
		t.Fatalf("len(entries) = %d, want at least 4: %#v", len(entries), entries)
	}
	if entries[0].Kind != SidebarTaskOverview {
		t.Fatalf("entries[0].Kind = %q, want %q", entries[0].Kind, SidebarTaskOverview)
	}
	if entries[1].Kind != SidebarTaskSource {
		t.Fatalf("entries[1].Kind = %q, want %q", entries[1].Kind, SidebarTaskSource)
	}
	foundSession := false
	for _, entry := range entries {
		if entry.Kind == SidebarTaskSession && entry.SessionID == "agent-1" {
			foundSession = true
		}
	}
	if !foundSession {
		t.Fatalf("task session entry not found: %#v", entries)
	}
}

func TestSessionOverviewDerivesCountsAndActions(t *testing.T) {
	snapshot := logic.InitialSnapshot{
		Sessions: []domain.Session{{ID: "work-1", Title: "Fix bug", State: domain.SessionPlanReview}},
		Plans: map[string]domain.Plan{"work-1": {
			ID:         "plan-1",
			WorkItemID: "work-1",
		}},
		SubPlans:      map[string][]domain.TaskPlan{"work-1": {{ID: "sp-1", RepositoryName: "repo-a"}}},
		AgentSessions: []domain.AgentSession{{ID: "agent-1", WorkItemID: "work-1"}},
		Questions:     map[string][]domain.Question{"agent-1": {{ID: "q-1"}}},
		Reviews:       map[string][]domain.ReviewCycle{"agent-1": {{ID: "review-1"}}},
	}
	overview := New().SessionOverview(snapshot, "work-1")
	if overview.WorkItemID != "work-1" || overview.Header.Title != "Fix bug" {
		t.Fatalf("overview identity = %+v", overview)
	}
	if len(overview.Actions) == 0 || overview.Actions[0].Kind != ActionPlanReview {
		t.Fatalf("actions = %#v", overview.Actions)
	}
}
