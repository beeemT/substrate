package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/repository"
)

func TestPlanService_CreatePlan(t *testing.T) {
	ctx := context.Background()
	planRepo := NewMockPlanRepository()
	subPlanRepo := NewMockSubPlanRepository()
	svc := NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: planRepo, SubPlans: subPlanRepo}}, newTestBus())

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
			sessionRepo := NewMockWorkItemRepository()
			svc := NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: planRepo, SubPlans: subPlanRepo, Sessions: sessionRepo}}, newTestBus())

			plan := domain.Plan{
				ID:               "plan-test",
				WorkItemID:       "wi-1",
				OrchestratorPlan: "Test",
				Status:           tc.from,
			}
			planRepo.plans["plan-test"] = plan

			// Create work item so TransitionPlan can load WorkspaceID
			sessionRepo.Create(ctx, domain.Session{ID: "wi-1", WorkspaceID: "ws-1"})

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
			svc := NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: planRepo, SubPlans: subPlanRepo}}, newTestBus())

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

func TestPlanService_ApplyReviewedPlanOutput(t *testing.T) {
	ctx := context.Background()
	planRepo := NewMockPlanRepository()
	subPlanRepo := NewMockSubPlanRepository()
	sessionRepo := NewMockWorkItemRepository()
	svc := NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: planRepo, SubPlans: subPlanRepo, Sessions: sessionRepo}}, newTestBus())

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
	if updatedPlan.Version != 1 {
		t.Fatalf("Version = %d, want 1 (review edits do not bump generation)", updatedPlan.Version)
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

func TestPlanService_ApplyReviewedPlanOutput_NoOpPreservesVersion(t *testing.T) {
	ctx := context.Background()
	planRepo := NewMockPlanRepository()
	subPlanRepo := NewMockSubPlanRepository()
	sessionRepo := NewMockWorkItemRepository()
	svc := NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: planRepo, SubPlans: subPlanRepo, Sessions: sessionRepo}}, newTestBus())

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

func TestPlanService_CreatePlanAtomic_VersionGeneration(t *testing.T) {
	ctx := context.Background()
	planRepo := NewMockPlanRepository()
	subPlanRepo := NewMockSubPlanRepository()
	sessionRepo := NewMockWorkItemRepository()
	svc := NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: planRepo, SubPlans: subPlanRepo, Sessions: sessionRepo}}, newTestBus())

	// First plan starts at version 1.
	planV1 := domain.Plan{ID: "plan-1", WorkItemID: "wi-1", OrchestratorPlan: "v1", Version: 1}
	if err := svc.CreatePlanAtomic(ctx, "", &planV1, nil); err != nil {
		t.Fatalf("CreatePlanAtomic v1: %v", err)
	}
	gotV1, err := svc.GetPlan(ctx, "plan-1")
	if err != nil {
		t.Fatalf("GetPlan v1: %v", err)
	}
	if gotV1.Version != 1 {
		t.Fatalf("v1.Version = %d, want 1", gotV1.Version)
	}

	// Second plan should be version 2 (generation counter from superseded plan).
	planV2 := domain.Plan{ID: "plan-2", WorkItemID: "wi-1", OrchestratorPlan: "v2"}
	if err := svc.CreatePlanAtomic(ctx, "plan-1", &planV2, nil); err != nil {
		t.Fatalf("CreatePlanAtomic v2: %v", err)
	}
	gotV2, err := svc.GetPlan(ctx, "plan-2")
	if err != nil {
		t.Fatalf("GetPlan v2: %v", err)
	}
	if gotV2.Version != 2 {
		t.Fatalf("v2.Version = %d, want 2", gotV2.Version)
	}
	// Old plan should be superseded.
	gotOld, err := svc.GetPlan(ctx, "plan-1")
	if err != nil {
		t.Fatalf("GetPlan old: %v", err)
	}
	if gotOld.Status != domain.PlanSuperseded {
		t.Fatalf("old plan status = %q, want %q", gotOld.Status, domain.PlanSuperseded)
	}
	// Third plan should be version 3.
	planV3 := domain.Plan{ID: "plan-3", WorkItemID: "wi-1", OrchestratorPlan: "v3"}
	if err := svc.CreatePlanAtomic(ctx, "plan-2", &planV3, nil); err != nil {
		t.Fatalf("CreatePlanAtomic v3: %v", err)
	}
	gotV3, err := svc.GetPlan(ctx, "plan-3")
	if err != nil {
		t.Fatalf("GetPlan v3: %v", err)
	}
	if gotV3.Version != 3 {
		t.Fatalf("v3.Version = %d, want 3", gotV3.Version)
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
			sessionRepo := NewMockWorkItemRepository()
			svc := NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: planRepo, SubPlans: subPlanRepo, Sessions: sessionRepo}}, newTestBus())

			// Create plan and work item so TransitionSubPlan can load them
			plan := domain.Plan{
				ID:         "plan-1",
				WorkItemID: "wi-1",
			}
			planRepo.plans["plan-1"] = plan
			workItem := domain.Session{
				ID:          "wi-1",
				WorkspaceID: "ws-1",
			}
			sessionRepo.items["wi-1"] = workItem

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
		// Note: in_progress -> pending is now valid (crash recovery); see validSubPlanTransitions.
		{domain.SubPlanCompleted, domain.SubPlanInProgress, "completed -> in_progress"},
		{domain.SubPlanCompleted, domain.SubPlanFailed, "completed -> failed"},
		{domain.SubPlanFailed, domain.SubPlanCompleted, "failed -> completed"},
		{domain.SubPlanFailed, domain.SubPlanInProgress, "failed -> in_progress"},
	}
	for _, tc := range invalidTransitions {
		t.Run(tc.name, func(t *testing.T) {
			planRepo := NewMockPlanRepository()
			subPlanRepo := NewMockSubPlanRepository()
			sessionRepo := NewMockWorkItemRepository()
			svc := NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: planRepo, SubPlans: subPlanRepo, Sessions: sessionRepo}}, newTestBus())

			// Create plan and work item so TransitionSubPlan can load them
			plan := domain.Plan{
				ID:         "plan-1",
				WorkItemID: "wi-1",
			}
			planRepo.plans["plan-1"] = plan
			workItem := domain.Session{
				ID:          "wi-1",
				WorkspaceID: "ws-1",
			}
			sessionRepo.items["wi-1"] = workItem

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
	svc := NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: planRepo, SubPlans: subPlanRepo}}, newTestBus())

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

func TestPlanService_MarkSubPlanPRReady(t *testing.T) {
	ctx := context.Background()

	t.Run("emits EventSubPlanPRReady on success", func(t *testing.T) {
		planRepo := NewMockPlanRepository()
		subPlanRepo := NewMockSubPlanRepository()
		sessionRepo := NewMockWorkItemRepository()

		planRepo.plans["plan-1"] = domain.Plan{ID: "plan-1", WorkItemID: "wi-1"}
		sessionRepo.items["wi-1"] = domain.Session{ID: "wi-1", WorkspaceID: "ws-1"}
		subPlanRepo.subPlans["sp-1"] = domain.TaskPlan{
			ID:             "sp-1",
			PlanID:         "plan-1",
			RepositoryName: "repo1",
			Status:         domain.SubPlanCompleted,
		}

		bus := event.NewBus(event.BusConfig{})
		svc := NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: planRepo, SubPlans: subPlanRepo, Sessions: sessionRepo}}, bus)
		sub, _ := bus.Subscribe("test", string(domain.EventSubPlanPRReady))

		ready := SubPlanPRReadyContext{
			Repository:    "repo1",
			Branch:        "feature/test",
			WorktreePath:  "/tmp/worktree",
			WorkItemTitle: "Test PR",
			Review: domain.ReviewRef{
				BaseRepo:   domain.RepoRef{Owner: "owner", Repo: "repo1", Provider: "gitlab"},
				HeadRepo:   domain.RepoRef{Owner: "owner", Repo: "repo1", Provider: "gitlab"},
				BaseBranch: "main",
			},
		}
		if err := svc.MarkSubPlanPRReady(ctx, "sp-1", ready); err != nil {
			t.Fatalf("MarkSubPlanPRReady failed: %v", err)
		}

		var evt domain.SystemEvent
		select {
		case evt = <-sub.C:
		case <-time.After(500 * time.Millisecond):
			t.Fatal("timeout waiting for EventSubPlanPRReady")
		}

		if evt.EventType != string(domain.EventSubPlanPRReady) {
			t.Errorf("event type = %q, want %q", evt.EventType, domain.EventSubPlanPRReady)
		}
		if evt.WorkspaceID != "ws-1" {
			t.Errorf("WorkspaceID = %q, want %q", evt.WorkspaceID, "ws-1")
		}

		var payload struct {
			SubPlanID  string `json:"sub_plan_id"`
			Repository string `json:"repository"`
			Branch     string `json:"branch"`
		}
		if err := json.Unmarshal([]byte(evt.Payload), &payload); err != nil {
			t.Fatalf("failed to unmarshal payload: %v", err)
		}
		if payload.SubPlanID != "sp-1" {
			t.Errorf("SubPlanID = %q, want %q", payload.SubPlanID, "sp-1")
		}
		if payload.Repository != "repo1" {
			t.Errorf("Repository = %q, want %q", payload.Repository, "repo1")
		}
		if payload.Branch != "feature/test" {
			t.Errorf("Branch = %q, want %q", payload.Branch, "feature/test")
		}
	})

	t.Run("returns error when repository is missing", func(t *testing.T) {
		svc := NewPlanService(repository.NoopTransacter{Res: repository.Resources{
			Plans:    NewMockPlanRepository(),
			SubPlans: NewMockSubPlanRepository(),
			Sessions: NewMockWorkItemRepository(),
		}}, newTestBus())

		err := svc.MarkSubPlanPRReady(ctx, "sp-1", SubPlanPRReadyContext{Branch: "feature/test"})
		if err == nil {
			t.Fatal("expected error for missing repository")
		}
		if _, ok := err.(ErrInvalidInput); !ok {
			t.Errorf("error type = %T, want ErrInvalidInput", err)
		}
	})

	t.Run("returns error when branch is missing", func(t *testing.T) {
		svc := NewPlanService(repository.NoopTransacter{Res: repository.Resources{
			Plans:    NewMockPlanRepository(),
			SubPlans: NewMockSubPlanRepository(),
			Sessions: NewMockWorkItemRepository(),
		}}, newTestBus())

		err := svc.MarkSubPlanPRReady(ctx, "sp-1", SubPlanPRReadyContext{Repository: "repo1"})
		if err == nil {
			t.Fatal("expected error for missing branch")
		}
		if _, ok := err.(ErrInvalidInput); !ok {
			t.Errorf("error type = %T, want ErrInvalidInput", err)
		}
	})

	t.Run("returns error when sub-plan not found", func(t *testing.T) {
		svc := NewPlanService(repository.NoopTransacter{Res: repository.Resources{
			Plans:    NewMockPlanRepository(),
			SubPlans: NewMockSubPlanRepository(),
			Sessions: NewMockWorkItemRepository(),
		}}, newTestBus())

		err := svc.MarkSubPlanPRReady(ctx, "sp-1", SubPlanPRReadyContext{
			Repository: "repo1",
			Branch:     "feature/test",
			Review: domain.ReviewRef{
				BaseRepo:   domain.RepoRef{Owner: "owner", Repo: "repo1", Provider: "gitlab"},
				HeadRepo:   domain.RepoRef{Owner: "owner", Repo: "repo1", Provider: "gitlab"},
				BaseBranch: "main",
			},
		})
		if err == nil {
			t.Fatal("expected error for nonexistent sub-plan")
		}
		if _, ok := err.(ErrNotFound); !ok {
			t.Errorf("error type = %T, want ErrNotFound", err)
		}
	})

	t.Run("returns error when sub-plan status is not completed", func(t *testing.T) {
		planRepo := NewMockPlanRepository()
		subPlanRepo := NewMockSubPlanRepository()
		sessionRepo := NewMockWorkItemRepository()

		planRepo.plans["plan-1"] = domain.Plan{ID: "plan-1", WorkItemID: "wi-1"}
		sessionRepo.items["wi-1"] = domain.Session{ID: "wi-1", WorkspaceID: "ws-1"}
		subPlanRepo.subPlans["sp-1"] = domain.TaskPlan{
			ID:             "sp-1",
			PlanID:         "plan-1",
			RepositoryName: "repo1",
			Status:         domain.SubPlanInProgress, // Not completed
		}

		svc := NewPlanService(repository.NoopTransacter{Res: repository.Resources{
			Plans: planRepo, SubPlans: subPlanRepo, Sessions: sessionRepo,
		}}, newTestBus())

		err := svc.MarkSubPlanPRReady(ctx, "sp-1", SubPlanPRReadyContext{
			Repository: "repo1",
			Branch:     "feature/test",
			Review: domain.ReviewRef{
				BaseRepo:   domain.RepoRef{Owner: "owner", Repo: "repo1", Provider: "gitlab"},
				HeadRepo:   domain.RepoRef{Owner: "owner", Repo: "repo1", Provider: "gitlab"},
				BaseBranch: "main",
			},
		})
		if err == nil {
			t.Fatal("expected error for non-completed sub-plan")
		}
		if _, ok := err.(ErrInvalidTransition); !ok {
			t.Errorf("error type = %T, want ErrInvalidTransition", err)
		}
	})

	t.Run("idempotent: skips emission if already emitted", func(t *testing.T) {
		planRepo := NewMockPlanRepository()
		subPlanRepo := NewMockSubPlanRepository()
		sessionRepo := NewMockWorkItemRepository()

		planRepo.plans["plan-1"] = domain.Plan{ID: "plan-1", WorkItemID: "wi-1"}
		sessionRepo.items["wi-1"] = domain.Session{ID: "wi-1", WorkspaceID: "ws-1"}
		subPlanRepo.subPlans["sp-1"] = domain.TaskPlan{
			ID:             "sp-1",
			PlanID:         "plan-1",
			RepositoryName: "repo1",
			Status:         domain.SubPlanCompleted,
		}

		// Simulate a previous emission
		prevEvents := []domain.SystemEvent{
			{
				ID:        "prev-event",
				EventType: string(domain.EventSubPlanPRReady),
				Payload:   `{"sub_plan_id":"sp-1","plan_id":"plan-1","work_item_id":"wi-1","repository":"repo1","branch":"feature/old"}`,
			},
		}
		eventRepo := &mockEventRepoForEmit{events: prevEvents}
		bus := event.NewBus(event.BusConfig{EventRepo: eventRepo})
		svc := NewPlanService(repository.NoopTransacter{Res: repository.Resources{
			Plans: planRepo, SubPlans: subPlanRepo, Sessions: sessionRepo, Events: eventRepo,
		}}, bus)
		sub, _ := bus.Subscribe("test", string(domain.EventSubPlanPRReady))

		ready := SubPlanPRReadyContext{
			Repository: "repo1",
			Branch:     "feature/new",
			Review: domain.ReviewRef{
				BaseRepo:   domain.RepoRef{Owner: "owner", Repo: "repo1", Provider: "gitlab"},
				HeadRepo:   domain.RepoRef{Owner: "owner", Repo: "repo1", Provider: "gitlab"},
				BaseBranch: "main",
			},
		}
		if err := svc.MarkSubPlanPRReady(ctx, "sp-1", ready); err != nil {
			t.Fatalf("MarkSubPlanPRReady failed: %v", err)
		}

		// Should not emit a new event since one already exists for this sub-plan
		select {
		case <-sub.C:
			t.Fatal("expected no event for idempotent call")
		case <-time.After(100 * time.Millisecond):
			// Expected: no event received
		}
	})
}

func TestPlanService_ApplyReviewedPlanOutput_EmitsEventWithPlanID(t *testing.T) {
	ctx := context.Background()
	planRepo := NewMockPlanRepository()
	subPlanRepo := NewMockSubPlanRepository()
	sessionRepo := NewMockWorkItemRepository()
	sessionRepo.items["wi-1"] = domain.Session{ID: "wi-1", WorkspaceID: "ws-1"}
	repo := &mockEventRepoForEmit{events: []domain.SystemEvent{}}
	bus := event.NewBus(event.BusConfig{EventRepo: repo})
	svc := NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: planRepo, SubPlans: subPlanRepo, Sessions: sessionRepo}}, bus)

	planRepo.plans["plan-1"] = domain.Plan{
		ID:               "plan-1",
		WorkItemID:       "wi-1",
		OrchestratorPlan: "Old orchestration",
		Status:           domain.PlanPendingReview,
		Version:          1,
	}

	raw := domain.RawPlanOutput{
		ExecutionGroups: [][]string{{"repo-a"}},
		Orchestration:   "New orchestration",
		SubPlans:        []domain.RawSubPlan{{RepoName: "repo-a", Content: "new repo a"}},
	}

	sub, _ := bus.Subscribe("test", string(domain.EventPlanRevised))

	if _, _, err := svc.ApplyReviewedPlanOutput(ctx, "plan-1", raw); err != nil {
		t.Fatalf("ApplyReviewedPlanOutput failed: %v", err)
	}

	var got domain.SystemEvent
	select {
	case got = <-sub.C:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for EventPlanRevised")
	}

	if got.EventType != string(domain.EventPlanRevised) {
		t.Errorf("event type = %q, want %q", got.EventType, domain.EventPlanRevised)
	}
	if got.WorkspaceID != "ws-1" {
		t.Errorf("WorkspaceID = %q, want %q", got.WorkspaceID, "ws-1")
	}
	if got.Payload == "" || got.Payload == "{}" {
		t.Errorf("Payload = %q, want non-empty JSON with plan_id", got.Payload)
	}
	var payload struct {
		PlanID string `json:"plan_id"`
	}
	if err := json.Unmarshal([]byte(got.Payload), &payload); err != nil {
		t.Fatalf("failed to unmarshal payload %q: %v", got.Payload, err)
	}
	if payload.PlanID != "plan-1" {
		t.Errorf("PlanID = %q, want %q", payload.PlanID, "plan-1")
	}
}
