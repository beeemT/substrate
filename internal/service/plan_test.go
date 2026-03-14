package service

import (
	"context"
	"testing"

	"github.com/beeemT/substrate/internal/domain"
)

func TestPlanService_CreatePlan(t *testing.T) {
	ctx := context.Background()
	planRepo := NewMockPlanRepository()
	subPlanRepo := NewMockSubPlanRepository()
	svc := NewPlanService(planRepo, subPlanRepo)

	t.Run("creates plan with draft status", func(t *testing.T) {
		plan := domain.Plan{
			ID:               "plan-1",
			WorkItemID:       "wi-1",
			OrchestratorPlan: "Test plan",
		}
		if err := svc.CreatePlan(ctx, plan); err != nil {
			t.Fatalf("CreatePlan failed: %v", err)
		}

		got, err := svc.GetPlan(ctx, "plan-1")
		if err != nil {
			t.Fatalf("GetPlan failed: %v", err)
		}
		if got.Status != domain.PlanDraft {
			t.Errorf("Status = %q, want %q", got.Status, domain.PlanDraft)
		}
		if got.Version != 1 {
			t.Errorf("Version = %d, want 1", got.Version)
		}
	})

	t.Run("rejects non-draft initial status", func(t *testing.T) {
		plan := domain.Plan{
			ID:               "plan-2",
			WorkItemID:       "wi-2",
			OrchestratorPlan: "Test plan",
			Status:           domain.PlanApproved,
		}
		err := svc.CreatePlan(ctx, plan)
		if err == nil {
			t.Fatal("expected error for non-draft initial status")
		}
		_, ok := err.(ErrInvalidInput)
		if !ok {
			t.Errorf("error type = %T, want ErrInvalidInput", err)
		}
	})
}

func TestPlanService_ValidTransitions(t *testing.T) {
	ctx := context.Background()

	validTransitions := []struct {
		from domain.PlanStatus
		to   domain.PlanStatus
		name string
	}{
		{domain.PlanDraft, domain.PlanPendingReview, "draft -> pending_review"},
		{domain.PlanPendingReview, domain.PlanApproved, "pending_review -> approved"},
		{domain.PlanPendingReview, domain.PlanRejected, "pending_review -> rejected"},
		{domain.PlanRejected, domain.PlanPendingReview, "rejected -> pending_review"},
	}

	for _, tc := range validTransitions {
		t.Run(tc.name, func(t *testing.T) {
			planRepo := NewMockPlanRepository()
			subPlanRepo := NewMockSubPlanRepository()
			svc := NewPlanService(planRepo, subPlanRepo)

			plan := domain.Plan{
				ID:               "plan-test",
				WorkItemID:       "wi-1",
				OrchestratorPlan: "Test",
				Status:           tc.from,
			}
			planRepo.plans["plan-test"] = plan

			if err := svc.TransitionPlan(ctx, "plan-test", tc.to); err != nil {
				t.Fatalf("Transition from %s to %s failed: %v", tc.from, tc.to, err)
			}

			got, err := svc.GetPlan(ctx, "plan-test")
			if err != nil {
				t.Fatalf("GetPlan failed: %v", err)
			}
			if got.Status != tc.to {
				t.Errorf("Status = %q, want %q", got.Status, tc.to)
			}
		})
	}
}

func TestPlanService_InvalidTransitions(t *testing.T) {
	ctx := context.Background()

	invalidTransitions := []struct {
		from domain.PlanStatus
		to   domain.PlanStatus
		name string
	}{
		{domain.PlanDraft, domain.PlanApproved, "draft -> approved"},
		{domain.PlanDraft, domain.PlanRejected, "draft -> rejected"},
		{domain.PlanPendingReview, domain.PlanDraft, "pending_review -> draft"},
		{domain.PlanApproved, domain.PlanPendingReview, "approved -> pending_review"},
		{domain.PlanApproved, domain.PlanRejected, "approved -> rejected"},
		{domain.PlanRejected, domain.PlanApproved, "rejected -> approved"},
		{domain.PlanRejected, domain.PlanDraft, "rejected -> draft"},
	}

	for _, tc := range invalidTransitions {
		t.Run(tc.name, func(t *testing.T) {
			planRepo := NewMockPlanRepository()
			subPlanRepo := NewMockSubPlanRepository()
			svc := NewPlanService(planRepo, subPlanRepo)

			plan := domain.Plan{
				ID:               "plan-test",
				WorkItemID:       "wi-1",
				OrchestratorPlan: "Test",
				Status:           tc.from,
			}
			planRepo.plans["plan-test"] = plan

			err := svc.TransitionPlan(ctx, "plan-test", tc.to)
			if err == nil {
				t.Fatalf("expected error for transition from %s to %s", tc.from, tc.to)
			}
			if _, ok := err.(ErrInvalidTransition); !ok {
				t.Errorf("error type = %T, want ErrInvalidTransition", err)
			}
		})
	}
}

func TestPlanService_RevisePlan(t *testing.T) {
	ctx := context.Background()
	planRepo := NewMockPlanRepository()
	subPlanRepo := NewMockSubPlanRepository()
	svc := NewPlanService(planRepo, subPlanRepo)

	// Create rejected plan
	plan := domain.Plan{
		ID:               "plan-1",
		WorkItemID:       "wi-1",
		OrchestratorPlan: "Original",
		Status:           domain.PlanRejected,
		Version:          1,
	}
	planRepo.plans["plan-1"] = plan

	t.Run("revises rejected plan and increments version", func(t *testing.T) {
		if err := svc.RevisePlan(ctx, "plan-1", "Revised content"); err != nil {
			t.Fatalf("RevisePlan failed: %v", err)
		}

		got, _ := svc.GetPlan(ctx, "plan-1")
		if got.Status != domain.PlanPendingReview {
			t.Errorf("Status = %q, want %q", got.Status, domain.PlanPendingReview)
		}
		if got.Version != 2 {
			t.Errorf("Version = %d, want 2", got.Version)
		}
		if got.OrchestratorPlan != "Revised content" {
			t.Errorf("OrchestratorPlan = %q, want %q", got.OrchestratorPlan, "Revised content")
		}
	})

	t.Run("cannot revise non-rejected plan", func(t *testing.T) {
		plan := domain.Plan{
			ID:               "plan-2",
			WorkItemID:       "wi-2",
			OrchestratorPlan: "Test",
			Status:           domain.PlanDraft,
		}
		planRepo.plans["plan-2"] = plan

		err := svc.RevisePlan(ctx, "plan-2", "New content")
		if err == nil {
			t.Fatal("expected error for revising non-rejected plan")
		}
		if _, ok := err.(ErrInvalidTransition); !ok {
			t.Errorf("error type = %T, want ErrInvalidTransition", err)
		}
	})
}

func TestPlanService_ApplyReviewedPlanOutput(t *testing.T) {
	ctx := context.Background()
	planRepo := NewMockPlanRepository()
	subPlanRepo := NewMockSubPlanRepository()
	svc := NewPlanService(planRepo, subPlanRepo)

	planRepo.plans["plan-1"] = domain.Plan{
		ID:               "plan-1",
		WorkItemID:       "wi-1",
		OrchestratorPlan: "Old orchestration",
		Status:           domain.PlanPendingReview,
		Version:          1,
	}
	for _, sp := range []domain.TaskPlan{
		{ID: "sp-a", PlanID: "plan-1", RepositoryName: "repo-a", Content: "old repo a", Order: 0, Status: domain.SubPlanPending},
		{ID: "sp-drop", PlanID: "plan-1", RepositoryName: "repo-drop", Content: "remove me", Order: 1, Status: domain.SubPlanPending},
	} {
		subPlanRepo.subPlans[sp.ID] = sp
		subPlanRepo.byPlan[sp.PlanID] = append(subPlanRepo.byPlan[sp.PlanID], sp.ID)
	}

	raw := domain.RawPlanOutput{
		ExecutionGroups: [][]string{{"repo-b"}, {"repo-a", "repo-c"}},
		Orchestration:   "New orchestration",
		SubPlans: []domain.RawSubPlan{
			{RepoName: "repo-b", Content: "new repo b"},
			{RepoName: "repo-a", Content: "new repo a"},
			{RepoName: "repo-c", Content: "new repo c"},
		},
	}

	updatedPlan, updatedSubPlans, err := svc.ApplyReviewedPlanOutput(ctx, "plan-1", raw)
	if err != nil {
		t.Fatalf("ApplyReviewedPlanOutput failed: %v", err)
	}
	if updatedPlan.OrchestratorPlan != "New orchestration" {
		t.Fatalf("OrchestratorPlan = %q, want %q", updatedPlan.OrchestratorPlan, "New orchestration")
	}
	if updatedPlan.Version != 2 {
		t.Fatalf("Version = %d, want 2", updatedPlan.Version)
	}
	if _, ok := subPlanRepo.subPlans["sp-drop"]; ok {
		t.Fatal("expected repo-drop sub-plan to be deleted")
	}
	if len(updatedSubPlans) != 3 {
		t.Fatalf("updated sub-plans = %d, want 3", len(updatedSubPlans))
	}
	byRepo := make(map[string]domain.TaskPlan, len(updatedSubPlans))
	for _, sp := range updatedSubPlans {
		byRepo[sp.RepositoryName] = sp
	}
	if got := byRepo["repo-b"]; got.Content != "new repo b" || got.Order != 0 {
		t.Fatalf("repo-b = %#v, want content/order updated", got)
	}
	if got := byRepo["repo-a"]; got.ID != "sp-a" || got.Content != "new repo a" || got.Order != 1 {
		t.Fatalf("repo-a = %#v, want existing ID/content/order updated", got)
	}
	if got := byRepo["repo-c"]; got.ID == "" || got.Content != "new repo c" || got.Order != 1 {
		t.Fatalf("repo-c = %#v, want created sub-plan", got)
	}
}

func TestPlanService_ApplyReviewedPlanOutput_NoOpDoesNotBumpVersion(t *testing.T) {
	ctx := context.Background()
	planRepo := NewMockPlanRepository()
	subPlanRepo := NewMockSubPlanRepository()
	svc := NewPlanService(planRepo, subPlanRepo)

	planRepo.plans["plan-1"] = domain.Plan{
		ID:               "plan-1",
		WorkItemID:       "wi-1",
		OrchestratorPlan: "Same orchestration",
		Status:           domain.PlanPendingReview,
		Version:          4,
	}
	sp := domain.TaskPlan{ID: "sp-a", PlanID: "plan-1", RepositoryName: "repo-a", Content: "same repo a", Order: 0, Status: domain.SubPlanPending}
	subPlanRepo.subPlans[sp.ID] = sp
	subPlanRepo.byPlan[sp.PlanID] = []string{sp.ID}

	raw := domain.RawPlanOutput{
		ExecutionGroups: [][]string{{"repo-a"}},
		Orchestration:   "Same orchestration",
		SubPlans:        []domain.RawSubPlan{{RepoName: "repo-a", Content: "same repo a"}},
	}

	updatedPlan, updatedSubPlans, err := svc.ApplyReviewedPlanOutput(ctx, "plan-1", raw)
	if err != nil {
		t.Fatalf("ApplyReviewedPlanOutput failed: %v", err)
	}
	if updatedPlan.Version != 4 {
		t.Fatalf("Version = %d, want 4", updatedPlan.Version)
	}
	if len(updatedSubPlans) != 1 || updatedSubPlans[0].ID != "sp-a" {
		t.Fatalf("updated sub-plans = %#v, want original sub-plan preserved", updatedSubPlans)
	}
}

func TestSubPlanService_ValidTransitions(t *testing.T) {
	ctx := context.Background()

	validTransitions := []struct {
		from domain.TaskPlanStatus
		to   domain.TaskPlanStatus
		name string
	}{
		{domain.SubPlanPending, domain.SubPlanInProgress, "pending -> in_progress"},
		{domain.SubPlanInProgress, domain.SubPlanCompleted, "in_progress -> completed"},
		{domain.SubPlanInProgress, domain.SubPlanFailed, "in_progress -> failed"},
		{domain.SubPlanFailed, domain.SubPlanPending, "failed -> pending"},
	}

	for _, tc := range validTransitions {
		t.Run(tc.name, func(t *testing.T) {
			planRepo := NewMockPlanRepository()
			subPlanRepo := NewMockSubPlanRepository()
			svc := NewPlanService(planRepo, subPlanRepo)

			sp := domain.TaskPlan{
				ID:             "sp-test",
				PlanID:         "plan-1",
				RepositoryName: "repo1",
				Content:        "Test",
				Status:         tc.from,
			}
			subPlanRepo.subPlans["sp-test"] = sp

			if err := svc.TransitionSubPlan(ctx, "sp-test", tc.to); err != nil {
				t.Fatalf("Transition from %s to %s failed: %v", tc.from, tc.to, err)
			}

			got, err := svc.GetSubPlan(ctx, "sp-test")
			if err != nil {
				t.Fatalf("GetSubPlan failed: %v", err)
			}
			if got.Status != tc.to {
				t.Errorf("Status = %q, want %q", got.Status, tc.to)
			}
		})
	}
}

func TestSubPlanService_InvalidTransitions(t *testing.T) {
	ctx := context.Background()

	invalidTransitions := []struct {
		from domain.TaskPlanStatus
		to   domain.TaskPlanStatus
		name string
	}{
		{domain.SubPlanPending, domain.SubPlanCompleted, "pending -> completed"},
		{domain.SubPlanPending, domain.SubPlanFailed, "pending -> failed"},
		{domain.SubPlanInProgress, domain.SubPlanPending, "in_progress -> pending"},
		{domain.SubPlanCompleted, domain.SubPlanInProgress, "completed -> in_progress"},
		{domain.SubPlanCompleted, domain.SubPlanFailed, "completed -> failed"},
		{domain.SubPlanFailed, domain.SubPlanCompleted, "failed -> completed"},
		{domain.SubPlanFailed, domain.SubPlanInProgress, "failed -> in_progress"},
	}

	for _, tc := range invalidTransitions {
		t.Run(tc.name, func(t *testing.T) {
			planRepo := NewMockPlanRepository()
			subPlanRepo := NewMockSubPlanRepository()
			svc := NewPlanService(planRepo, subPlanRepo)

			sp := domain.TaskPlan{
				ID:             "sp-test",
				PlanID:         "plan-1",
				RepositoryName: "repo1",
				Content:        "Test",
				Status:         tc.from,
			}
			subPlanRepo.subPlans["sp-test"] = sp

			err := svc.TransitionSubPlan(ctx, "sp-test", tc.to)
			if err == nil {
				t.Fatalf("expected error for transition from %s to %s", tc.from, tc.to)
			}
			if _, ok := err.(ErrInvalidTransition); !ok {
				t.Errorf("error type = %T, want ErrInvalidTransition", err)
			}
		})
	}
}

func TestPlanService_AllSubPlansCompleted(t *testing.T) {
	ctx := context.Background()
	planRepo := NewMockPlanRepository()
	subPlanRepo := NewMockSubPlanRepository()
	svc := NewPlanService(planRepo, subPlanRepo)

	t.Run("returns false when no sub-plans", func(t *testing.T) {
		done, err := svc.AllSubPlansCompleted(ctx, "plan-1")
		if err != nil {
			t.Fatalf("AllSubPlansCompleted failed: %v", err)
		}
		if done {
			t.Error("expected false for empty sub-plans")
		}
	})

	t.Run("returns true when all completed", func(t *testing.T) {
		subPlanRepo.subPlans["sp-1"] = domain.TaskPlan{ID: "sp-1", PlanID: "plan-2", Status: domain.SubPlanCompleted}
		subPlanRepo.subPlans["sp-2"] = domain.TaskPlan{ID: "sp-2", PlanID: "plan-2", Status: domain.SubPlanCompleted}
		subPlanRepo.byPlan["plan-2"] = []string{"sp-1", "sp-2"}

		done, err := svc.AllSubPlansCompleted(ctx, "plan-2")
		if err != nil {
			t.Fatalf("AllSubPlansCompleted failed: %v", err)
		}
		if !done {
			t.Error("expected true when all sub-plans completed")
		}
	})

	t.Run("returns false when some incomplete", func(t *testing.T) {
		subPlanRepo.subPlans["sp-3"] = domain.TaskPlan{ID: "sp-3", PlanID: "plan-3", Status: domain.SubPlanCompleted}
		subPlanRepo.subPlans["sp-4"] = domain.TaskPlan{ID: "sp-4", PlanID: "plan-3", Status: domain.SubPlanInProgress}
		subPlanRepo.byPlan["plan-3"] = []string{"sp-3", "sp-4"}

		done, err := svc.AllSubPlansCompleted(ctx, "plan-3")
		if err != nil {
			t.Fatalf("AllSubPlansCompleted failed: %v", err)
		}
		if done {
			t.Error("expected false when some sub-plans incomplete")
		}
	})
}
