package service

import (
	"context"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/domain"
)

// Additional tests to reach >90% coverage

func TestPlanService_AdditionalMethods(t *testing.T) {
	ctx := context.Background()
	planRepo := NewMockPlanRepository()
	subPlanRepo := NewMockSubPlanRepository()
	svc := NewPlanService(planRepo, subPlanRepo)

	// Setup plan
	plan := domain.Plan{
		ID:               "plan-1",
		WorkItemID:       "wi-1",
		OrchestratorPlan: "Test",
		Status:           domain.PlanDraft,
		Version:          1,
	}
	planRepo.plans["plan-1"] = plan
	planRepo.byWorkItem["wi-1"] = "plan-1"
	t.Run("GetPlanByWorkItemID", func(t *testing.T) {
		got, err := svc.GetPlanByWorkItemID(ctx, "wi-1")
		if err != nil {
			t.Fatalf("GetPlanByWorkItemID failed: %v", err)
		}
		if got.ID != "plan-1" {
			t.Errorf("ID = %q, want %q", got.ID, "plan-1")
		}
	})

	t.Run("SubmitForReview", func(t *testing.T) {
		if err := svc.SubmitForReview(ctx, "plan-1"); err != nil {
			t.Fatalf("SubmitForReview failed: %v", err)
		}
		got, _ := svc.GetPlan(ctx, "plan-1")
		if got.Status != domain.PlanPendingReview {
			t.Errorf("Status = %q, want %q", got.Status, domain.PlanPendingReview)
		}
	})

	t.Run("ApprovePlan", func(t *testing.T) {
		if err := svc.ApprovePlan(ctx, "plan-1"); err != nil {
			t.Fatalf("ApprovePlan failed: %v", err)
		}
		got, _ := svc.GetPlan(ctx, "plan-1")
		if got.Status != domain.PlanApproved {
			t.Errorf("Status = %q, want %q", got.Status, domain.PlanApproved)
		}
	})

	t.Run("RejectPlan", func(t *testing.T) {
		planRepo.plans["plan-2"] = domain.Plan{ID: "plan-2", WorkItemID: "wi-2", Status: domain.PlanPendingReview}
		if err := svc.RejectPlan(ctx, "plan-2"); err != nil {
			t.Fatalf("RejectPlan failed: %v", err)
		}
		got, _ := svc.GetPlan(ctx, "plan-2")
		if got.Status != domain.PlanRejected {
			t.Errorf("Status = %q, want %q", got.Status, domain.PlanRejected)
		}
	})

	t.Run("UpdatePlanContent", func(t *testing.T) {
		if err := svc.UpdatePlanContent(ctx, "plan-1", "New content"); err != nil {
			t.Fatalf("UpdatePlanContent failed: %v", err)
		}
		got, _ := svc.GetPlan(ctx, "plan-1")
		if got.OrchestratorPlan != "New content" {
			t.Errorf("OrchestratorPlan = %q, want %q", got.OrchestratorPlan, "New content")
		}
	})

	t.Run("DeletePlan", func(t *testing.T) {
		planRepo.plans["plan-del"] = domain.Plan{ID: "plan-del", WorkItemID: "wi-del", Status: domain.PlanDraft}
		if err := svc.DeletePlan(ctx, "plan-del"); err != nil {
			t.Fatalf("DeletePlan failed: %v", err)
		}
	})

	t.Run("ListSubPlansByPlanID", func(t *testing.T) {
		subPlanRepo.subPlans["sp-1"] = domain.TaskPlan{ID: "sp-1", PlanID: "plan-1", Status: domain.SubPlanPending}
		subPlanRepo.subPlans["sp-2"] = domain.TaskPlan{ID: "sp-2", PlanID: "plan-1", Status: domain.SubPlanPending}
		subPlanRepo.byPlan["plan-1"] = []string{"sp-1", "sp-2"}

		got, err := svc.ListSubPlansByPlanID(ctx, "plan-1")
		if err != nil {
			t.Fatalf("ListSubPlansByPlanID failed: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("got %d sub-plans, want 2", len(got))
		}
	})

	t.Run("CreateSubPlan", func(t *testing.T) {
		sp := domain.TaskPlan{
			ID:             "sp-new",
			PlanID:         "plan-1",
			RepositoryName: "repo1",
			Content:        "Test content",
			Order:          1,
		}
		if err := svc.CreateSubPlan(ctx, sp); err != nil {
			t.Fatalf("CreateSubPlan failed: %v", err)
		}
		got, err := svc.GetSubPlan(ctx, "sp-new")
		if err != nil {
			t.Fatalf("GetSubPlan failed: %v", err)
		}
		if got.Status != domain.SubPlanPending {
			t.Errorf("Status = %q, want %q", got.Status, domain.SubPlanPending)
		}
	})

	t.Run("CreateSubPlan rejects non-pending status", func(t *testing.T) {
		sp := domain.TaskPlan{
			ID:     "sp-bad",
			PlanID: "plan-1",
			Status: domain.SubPlanInProgress,
		}
		err := svc.CreateSubPlan(ctx, sp)
		if err == nil {
			t.Fatal("expected error for non-pending status")
		}
	})

	t.Run("StartSubPlan", func(t *testing.T) {
		subPlanRepo.subPlans["sp-start"] = domain.TaskPlan{ID: "sp-start", PlanID: "plan-1", Status: domain.SubPlanPending}
		if err := svc.StartSubPlan(ctx, "sp-start"); err != nil {
			t.Fatalf("StartSubPlan failed: %v", err)
		}
		got, _ := svc.GetSubPlan(ctx, "sp-start")
		if got.Status != domain.SubPlanInProgress {
			t.Errorf("Status = %q, want %q", got.Status, domain.SubPlanInProgress)
		}
	})

	t.Run("CompleteSubPlan", func(t *testing.T) {
		subPlanRepo.subPlans["sp-complete"] = domain.TaskPlan{ID: "sp-complete", PlanID: "plan-1", Status: domain.SubPlanInProgress}
		if err := svc.CompleteSubPlan(ctx, "sp-complete"); err != nil {
			t.Fatalf("CompleteSubPlan failed: %v", err)
		}
		got, _ := svc.GetSubPlan(ctx, "sp-complete")
		if got.Status != domain.SubPlanCompleted {
			t.Errorf("Status = %q, want %q", got.Status, domain.SubPlanCompleted)
		}
	})

	t.Run("FailSubPlan", func(t *testing.T) {
		subPlanRepo.subPlans["sp-fail"] = domain.TaskPlan{ID: "sp-fail", PlanID: "plan-1", Status: domain.SubPlanInProgress}
		if err := svc.FailSubPlan(ctx, "sp-fail"); err != nil {
			t.Fatalf("FailSubPlan failed: %v", err)
		}
		got, _ := svc.GetSubPlan(ctx, "sp-fail")
		if got.Status != domain.SubPlanFailed {
			t.Errorf("Status = %q, want %q", got.Status, domain.SubPlanFailed)
		}
	})

	t.Run("RetrySubPlan", func(t *testing.T) {
		subPlanRepo.subPlans["sp-retry"] = domain.TaskPlan{ID: "sp-retry", PlanID: "plan-1", Status: domain.SubPlanFailed}
		if err := svc.RetrySubPlan(ctx, "sp-retry"); err != nil {
			t.Fatalf("RetrySubPlan failed: %v", err)
		}
		got, _ := svc.GetSubPlan(ctx, "sp-retry")
		if got.Status != domain.SubPlanPending {
			t.Errorf("Status = %q, want %q", got.Status, domain.SubPlanPending)
		}
	})

	t.Run("UpdateSubPlanContent", func(t *testing.T) {
		subPlanRepo.subPlans["sp-update"] = domain.TaskPlan{ID: "sp-update", PlanID: "plan-1", Status: domain.SubPlanPending}
		if err := svc.UpdateSubPlanContent(ctx, "sp-update", "Updated content"); err != nil {
			t.Fatalf("UpdateSubPlanContent failed: %v", err)
		}
		got, _ := svc.GetSubPlan(ctx, "sp-update")
		if got.Content != "Updated content" {
			t.Errorf("Content = %q, want %q", got.Content, "Updated content")
		}
	})

	t.Run("DeleteSubPlan", func(t *testing.T) {
		subPlanRepo.subPlans["sp-del"] = domain.TaskPlan{ID: "sp-del", PlanID: "plan-1", Status: domain.SubPlanPending}
		subPlanRepo.byPlan["plan-1"] = append(subPlanRepo.byPlan["plan-1"], "sp-del")
		if err := svc.DeleteSubPlan(ctx, "sp-del"); err != nil {
			t.Fatalf("DeleteSubPlan failed: %v", err)
		}
	})

	t.Run("CreateSubPlansBatch", func(t *testing.T) {
		sps := []domain.TaskPlan{
			{ID: "sp-b1", PlanID: "plan-1", RepositoryName: "repo1"},
			{ID: "sp-b2", PlanID: "plan-1", RepositoryName: "repo2"},
		}
		if err := svc.CreateSubPlansBatch(ctx, sps); err != nil {
			t.Fatalf("CreateSubPlansBatch failed: %v", err)
		}
	})
}

func TestSessionService_AdditionalMethods(t *testing.T) {
	ctx := context.Background()
	repo := NewMockSessionRepository()
	svc := NewTaskService(repo)

	session := domain.Task{
		ID:             "session-1",
		WorkItemID:     "wi-1",
		WorkspaceID:    "ws-1",
		Phase:          domain.TaskPhaseImplementation,
		SubPlanID:      "sp-1",
		RepositoryName: "repo1",
		HarnessName:    "omp",
		Status:         domain.AgentSessionPending,
	}
	repo.sessions["session-1"] = session
	repo.byWorkItem["wi-1"] = []string{"session-1"}
	repo.bySubPlan["sp-1"] = []string{"session-1"}
	repo.byWorkspace["ws-1"] = []string{"session-1"}
	t.Run("ListByWorkItemID", func(t *testing.T) {
		got, err := svc.ListByWorkItemID(ctx, "wi-1")
		if err != nil {
			t.Fatalf("ListByWorkItemID failed: %v", err)
		}
		if len(got) != 1 {
			t.Errorf("got %d sessions, want 1", len(got))
		}
	})

	t.Run("ListBySubPlanID", func(t *testing.T) {
		got, err := svc.ListBySubPlanID(ctx, "sp-1")
		if err != nil {
			t.Fatalf("ListBySubPlanID failed: %v", err)
		}
		if len(got) != 1 {
			t.Errorf("got %d sessions, want 1", len(got))
		}
	})

	t.Run("ListByWorkspaceID", func(t *testing.T) {
		got, err := svc.ListByWorkspaceID(ctx, "ws-1")
		if err != nil {
			t.Fatalf("ListByWorkspaceID failed: %v", err)
		}
		if len(got) != 1 {
			t.Errorf("got %d sessions, want 1", len(got))
		}
	})

	t.Run("WaitForAnswer", func(t *testing.T) {
		repo.sessions["s-wait"] = domain.Task{ID: "s-wait", WorkItemID: "wi-wait", WorkspaceID: "ws-1", Phase: domain.TaskPhaseImplementation, SubPlanID: "sp-1", Status: domain.AgentSessionRunning}
		if err := svc.WaitForAnswer(ctx, "s-wait"); err != nil {
			t.Fatalf("WaitForAnswer failed: %v", err)
		}
		got, _ := svc.Get(ctx, "s-wait")
		if got.Status != domain.AgentSessionWaitingForAnswer {
			t.Errorf("Status = %q, want %q", got.Status, domain.AgentSessionWaitingForAnswer)
		}
	})

	t.Run("ResumeFromAnswer", func(t *testing.T) {
		repo.sessions["s-resume"] = domain.Task{ID: "s-resume", WorkItemID: "wi-resume", WorkspaceID: "ws-1", Phase: domain.TaskPhaseImplementation, SubPlanID: "sp-1", Status: domain.AgentSessionWaitingForAnswer}
		if err := svc.ResumeFromAnswer(ctx, "s-resume"); err != nil {
			t.Fatalf("ResumeFromAnswer failed: %v", err)
		}
		got, _ := svc.Get(ctx, "s-resume")
		if got.Status != domain.AgentSessionRunning {
			t.Errorf("Status = %q, want %q", got.Status, domain.AgentSessionRunning)
		}
	})

	t.Run("Resume", func(t *testing.T) {
		repo.sessions["s-resume2"] = domain.Task{ID: "s-resume2", WorkItemID: "wi-resume2", WorkspaceID: "ws-1", Phase: domain.TaskPhaseImplementation, SubPlanID: "sp-1", Status: domain.AgentSessionInterrupted}
		if err := svc.Resume(ctx, "s-resume2"); err != nil {
			t.Fatalf("Resume failed: %v", err)
		}
		got, _ := svc.Get(ctx, "s-resume2")
		if got.Status != domain.AgentSessionRunning {
			t.Errorf("Status = %q, want %q", got.Status, domain.AgentSessionRunning)
		}
	})

	t.Run("UpdateOwnerInstance", func(t *testing.T) {
		if err := svc.UpdateOwnerInstance(ctx, "session-1", "instance-1"); err != nil {
			t.Fatalf("UpdateOwnerInstance failed: %v", err)
		}
		got, _ := svc.Get(ctx, "session-1")
		if got.OwnerInstanceID == nil || *got.OwnerInstanceID != "instance-1" {
			t.Errorf("OwnerInstanceID = %v, want instance-1", got.OwnerInstanceID)
		}
	})

	t.Run("UpdatePID", func(t *testing.T) {
		if err := svc.UpdatePID(ctx, "session-1", 12345); err != nil {
			t.Fatalf("UpdatePID failed: %v", err)
		}
		got, _ := svc.Get(ctx, "session-1")
		if got.PID == nil || *got.PID != 12345 {
			t.Errorf("PID = %v, want 12345", got.PID)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		repo.sessions["s-del"] = domain.Task{ID: "s-del", WorkItemID: "wi-del", WorkspaceID: "ws-1", Phase: domain.TaskPhaseImplementation, SubPlanID: "sp-del", Status: domain.AgentSessionCompleted}
		repo.byWorkItem["wi-del"] = []string{"s-del"}
		repo.bySubPlan["sp-del"] = []string{"s-del"}
		repo.byWorkspace["ws-1"] = append(repo.byWorkspace["ws-1"], "s-del")
		if err := svc.Delete(ctx, "s-del"); err != nil {
			t.Fatalf("Delete failed: %v", err)
		}
		if _, err := svc.Get(ctx, "s-del"); err == nil {
			t.Fatal("expected deleted session lookup to fail")
		}
	})
}

func TestReviewService_AdditionalMethods(t *testing.T) {
	ctx := context.Background()
	repo := NewMockReviewRepository()
	svc := NewReviewService(repo)

	cycle := domain.ReviewCycle{
		ID:             "cycle-1",
		AgentSessionID: "session-1",
		CycleNumber:    1,
		Status:         domain.ReviewCycleReviewing,
	}
	repo.cycles["cycle-1"] = cycle

	t.Run("ListCyclesBySessionID", func(t *testing.T) {
		repo.bySession["session-1"] = []string{"cycle-1"}
		got, err := svc.ListCyclesBySessionID(ctx, "session-1")
		if err != nil {
			t.Fatalf("ListCyclesBySessionID failed: %v", err)
		}
		if len(got) != 1 {
			t.Errorf("got %d cycles, want 1", len(got))
		}
	})

	t.Run("RecordCritiques", func(t *testing.T) {
		if err := svc.RecordCritiques(ctx, "cycle-1"); err != nil {
			t.Fatalf("RecordCritiques failed: %v", err)
		}
		got, _ := svc.GetCycle(ctx, "cycle-1")
		if got.Status != domain.ReviewCycleCritiquesFound {
			t.Errorf("Status = %q, want %q", got.Status, domain.ReviewCycleCritiquesFound)
		}
	})

	t.Run("PassReview", func(t *testing.T) {
		repo.cycles["cycle-2"] = domain.ReviewCycle{ID: "cycle-2", AgentSessionID: "session-1", Status: domain.ReviewCycleReviewing}
		if err := svc.PassReview(ctx, "cycle-2"); err != nil {
			t.Fatalf("PassReview failed: %v", err)
		}
		got, _ := svc.GetCycle(ctx, "cycle-2")
		if got.Status != domain.ReviewCyclePassed {
			t.Errorf("Status = %q, want %q", got.Status, domain.ReviewCyclePassed)
		}
	})

	t.Run("StartReimplementation", func(t *testing.T) {
		repo.cycles["cycle-3"] = domain.ReviewCycle{ID: "cycle-3", AgentSessionID: "session-1", Status: domain.ReviewCycleCritiquesFound}
		if err := svc.StartReimplementation(ctx, "cycle-3"); err != nil {
			t.Fatalf("StartReimplementation failed: %v", err)
		}
		got, _ := svc.GetCycle(ctx, "cycle-3")
		if got.Status != domain.ReviewCycleReimplementing {
			t.Errorf("Status = %q, want %q", got.Status, domain.ReviewCycleReimplementing)
		}
	})

	t.Run("CompleteReimplementation", func(t *testing.T) {
		repo.cycles["cycle-4"] = domain.ReviewCycle{ID: "cycle-4", AgentSessionID: "session-1", Status: domain.ReviewCycleReimplementing}
		if err := svc.CompleteReimplementation(ctx, "cycle-4"); err != nil {
			t.Fatalf("CompleteReimplementation failed: %v", err)
		}
		got, _ := svc.GetCycle(ctx, "cycle-4")
		if got.Status != domain.ReviewCycleReviewing {
			t.Errorf("Status = %q, want %q", got.Status, domain.ReviewCycleReviewing)
		}
	})

	t.Run("FailReviewCycle", func(t *testing.T) {
		repo.cycles["cycle-5"] = domain.ReviewCycle{ID: "cycle-5", AgentSessionID: "session-1", Status: domain.ReviewCycleCritiquesFound}
		if err := svc.FailReviewCycle(ctx, "cycle-5"); err != nil {
			t.Fatalf("FailReviewCycle failed: %v", err)
		}
		got, _ := svc.GetCycle(ctx, "cycle-5")
		if got.Status != domain.ReviewCycleFailed {
			t.Errorf("Status = %q, want %q", got.Status, domain.ReviewCycleFailed)
		}
	})

	t.Run("UpdateCycleSummary", func(t *testing.T) {
		if err := svc.UpdateCycleSummary(ctx, "cycle-1", "Test summary"); err != nil {
			t.Fatalf("UpdateCycleSummary failed: %v", err)
		}
		got, _ := svc.GetCycle(ctx, "cycle-1")
		if got.Summary != "Test summary" {
			t.Errorf("Summary = %q, want %q", got.Summary, "Test summary")
		}
	})

	t.Run("ListCritiquesByCycleID", func(t *testing.T) {
		repo.critiques["c-1"] = domain.Critique{ID: "c-1", ReviewCycleID: "cycle-1"}
		repo.byCycle["cycle-1"] = []string{"c-1"}
		got, err := svc.ListCritiquesByCycleID(ctx, "cycle-1")
		if err != nil {
			t.Fatalf("ListCritiquesByCycleID failed: %v", err)
		}
		if len(got) != 1 {
			t.Errorf("got %d critiques, want 1", len(got))
		}
	})

	t.Run("CreateCritique", func(t *testing.T) {
		c := domain.Critique{
			ID:            "c-new",
			ReviewCycleID: "cycle-1",
			FilePath:      "test.go",
			Description:   "Test critique",
			Severity:      domain.CritiqueMajor,
		}
		if err := svc.CreateCritique(ctx, c); err != nil {
			t.Fatalf("CreateCritique failed: %v", err)
		}
	})

	t.Run("ResolveCritique", func(t *testing.T) {
		repo.critiques["c-resolve"] = domain.Critique{ID: "c-resolve", ReviewCycleID: "cycle-1", Status: domain.CritiqueOpen}
		if err := svc.ResolveCritique(ctx, "c-resolve"); err != nil {
			t.Fatalf("ResolveCritique failed: %v", err)
		}
		got, _ := svc.GetCritique(ctx, "c-resolve")
		if got.Status != domain.CritiqueResolved {
			t.Errorf("Status = %q, want %q", got.Status, domain.CritiqueResolved)
		}
	})

	t.Run("CreateCritiquesBatch", func(t *testing.T) {
		critiques := []domain.Critique{
			{ID: "c-b1", ReviewCycleID: "cycle-1", FilePath: "test1.go", Severity: domain.CritiqueMajor},
			{ID: "c-b2", ReviewCycleID: "cycle-1", FilePath: "test2.go", Severity: domain.CritiqueMinor},
		}
		if err := svc.CreateCritiquesBatch(ctx, critiques); err != nil {
			t.Fatalf("CreateCritiquesBatch failed: %v", err)
		}
	})
}

func TestWorkspaceService_AdditionalMethods(t *testing.T) {
	ctx := context.Background()
	repo := NewMockWorkspaceRepository()
	svc := NewWorkspaceService(repo)

	ws := domain.Workspace{
		ID:       "ws-1",
		Name:     "Test",
		RootPath: "/path",
		Status:   domain.WorkspaceReady,
	}
	repo.workspaces["ws-1"] = ws

	t.Run("Update", func(t *testing.T) {
		updated := domain.Workspace{
			ID:       "ws-1",
			Name:     "Updated",
			RootPath: "/new/path",
		}
		if err := svc.Update(ctx, updated); err != nil {
			t.Fatalf("Update failed: %v", err)
		}
		got, _ := svc.Get(ctx, "ws-1")
		if got.Name != "Updated" {
			t.Errorf("Name = %q, want %q", got.Name, "Updated")
		}
		// Status should be preserved
		if got.Status != domain.WorkspaceReady {
			t.Errorf("Status should be preserved, got %q", got.Status)
		}
	})
}

func TestQuestionService_AdditionalMethods(t *testing.T) {
	ctx := context.Background()
	repo := NewMockQuestionRepository()
	svc := NewQuestionService(repo)

	t.Run("ListBySessionID", func(t *testing.T) {
		repo.questions["q-1"] = domain.Question{ID: "q-1", AgentSessionID: "session-1", Status: domain.QuestionPending}
		repo.questions["q-2"] = domain.Question{ID: "q-2", AgentSessionID: "session-1", Status: domain.QuestionAnswered}
		repo.bySession["session-1"] = []string{"q-1", "q-2"}

		got, err := svc.ListBySessionID(ctx, "session-1")
		if err != nil {
			t.Fatalf("ListBySessionID failed: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("got %d questions, want 2", len(got))
		}
	})
}

func TestErrors(t *testing.T) {
	t.Run("ErrNotFound error message", func(t *testing.T) {
		err := ErrNotFound{Entity: "work item", ID: "123"}
		if err.Error() != "work item not found: 123" {
			t.Errorf("Error message = %q, want %q", err.Error(), "work item not found: 123")
		}
	})

	t.Run("ErrAlreadyExists error message", func(t *testing.T) {
		err := ErrAlreadyExists{Entity: "work item", ID: "123"}
		if err.Error() != "work item already exists: 123" {
			t.Errorf("Error message = %q, want %q", err.Error(), "work item already exists: 123")
		}
	})

	t.Run("ErrInvalidInput error message", func(t *testing.T) {
		err := ErrInvalidInput{Message: "test error", Field: "name"}
		if err.Error() != `invalid input for field "name": test error` {
			t.Errorf("Error message = %q, want %q", err.Error(), `invalid input for field "name": test error`)
		}
	})

	t.Run("ErrInvalidInput without field", func(t *testing.T) {
		err := ErrInvalidInput{Message: "test error"}
		if err.Error() != "invalid input: test error" {
			t.Errorf("Error message = %q, want %q", err.Error(), "invalid input: test error")
		}
	})

	t.Run("ErrConstraintViolation error message", func(t *testing.T) {
		err := ErrConstraintViolation{Message: "test constraint"}
		if err.Error() != "constraint violation: test constraint" {
			t.Errorf("Error message = %q, want %q", err.Error(), "constraint violation: test constraint")
		}
	})
}

func TestSessionService_FindRunningByOwner(t *testing.T) {
	ctx := context.Background()
	repo := NewMockSessionRepository()
	svc := NewTaskService(repo)

	// FindRunningByOwner is a placeholder - just verify it returns nil
	result, err := svc.FindRunningByOwner(ctx, "instance-1")
	if err != nil {
		t.Fatalf("FindRunningByOwner failed: %v", err)
	}
	if result != nil {
		t.Errorf("got %v, want nil (placeholder)", result)
	}
}

func TestPlanService_NotFoundErrors(t *testing.T) {
	ctx := context.Background()
	planRepo := NewMockPlanRepository()
	subPlanRepo := NewMockSubPlanRepository()
	svc := NewPlanService(planRepo, subPlanRepo)

	t.Run("GetPlan not found", func(t *testing.T) {
		_, err := svc.GetPlan(ctx, "nonexistent")
		if err == nil {
			t.Fatal("expected error")
		}
		_, ok := err.(ErrNotFound)
		if !ok {
			t.Errorf("error type = %T, want ErrNotFound", err)
		}
	})

	t.Run("GetPlanByWorkItemID not found", func(t *testing.T) {
		_, err := svc.GetPlanByWorkItemID(ctx, "nonexistent")
		if err == nil {
			t.Fatal("expected error")
		}
		_, ok := err.(ErrNotFound)
		if !ok {
			t.Errorf("error type = %T, want ErrNotFound", err)
		}
	})

	t.Run("DeletePlan not found", func(t *testing.T) {
		err := svc.DeletePlan(ctx, "nonexistent")
		if err == nil {
			t.Fatal("expected error")
		}
		_, ok := err.(ErrNotFound)
		if !ok {
			t.Errorf("error type = %T, want ErrNotFound", err)
		}
	})

	t.Run("GetSubPlan not found", func(t *testing.T) {
		_, err := svc.GetSubPlan(ctx, "nonexistent")
		if err == nil {
			t.Fatal("expected error")
		}
		_, ok := err.(ErrNotFound)
		if !ok {
			t.Errorf("error type = %T, want ErrNotFound", err)
		}
	})

	t.Run("DeleteSubPlan not found", func(t *testing.T) {
		err := svc.DeleteSubPlan(ctx, "nonexistent")
		if err == nil {
			t.Fatal("expected error")
		}
		_, ok := err.(ErrNotFound)
		if !ok {
			t.Errorf("error type = %T, want ErrNotFound", err)
		}
	})
}

func TestSessionService_NotFoundErrors(t *testing.T) {
	ctx := context.Background()
	repo := NewMockSessionRepository()
	svc := NewTaskService(repo)

	t.Run("Get not found", func(t *testing.T) {
		_, err := svc.Get(ctx, "nonexistent")
		if err == nil {
			t.Fatal("expected error")
		}
		_, ok := err.(ErrNotFound)
		if !ok {
			t.Errorf("error type = %T, want ErrNotFound", err)
		}
	})

	t.Run("Delete not found", func(t *testing.T) {
		err := svc.Delete(ctx, "nonexistent")
		if err == nil {
			t.Fatal("expected error")
		}
		_, ok := err.(ErrNotFound)
		if !ok {
			t.Errorf("error type = %T, want ErrNotFound", err)
		}
	})
}

func TestReviewService_NotFoundErrors(t *testing.T) {
	ctx := context.Background()
	repo := NewMockReviewRepository()
	svc := NewReviewService(repo)

	t.Run("GetCycle not found", func(t *testing.T) {
		_, err := svc.GetCycle(ctx, "nonexistent")
		if err == nil {
			t.Fatal("expected error")
		}
		_, ok := err.(ErrNotFound)
		if !ok {
			t.Errorf("error type = %T, want ErrNotFound", err)
		}
	})

	t.Run("GetCritique not found", func(t *testing.T) {
		_, err := svc.GetCritique(ctx, "nonexistent")
		if err == nil {
			t.Fatal("expected error")
		}
		_, ok := err.(ErrNotFound)
		if !ok {
			t.Errorf("error type = %T, want ErrNotFound", err)
		}
	})
}

func TestQuestionService_NotFoundErrors(t *testing.T) {
	ctx := context.Background()
	repo := NewMockQuestionRepository()
	svc := NewQuestionService(repo)

	t.Run("Get not found", func(t *testing.T) {
		_, err := svc.Get(ctx, "nonexistent")
		if err == nil {
			t.Fatal("expected error")
		}
		_, ok := err.(ErrNotFound)
		if !ok {
			t.Errorf("error type = %T, want ErrNotFound", err)
		}
	})
}

func TestInstanceService_NotFoundErrors(t *testing.T) {
	ctx := context.Background()
	repo := NewMockInstanceRepository()
	svc := NewInstanceService(repo)

	t.Run("Get not found", func(t *testing.T) {
		_, err := svc.Get(ctx, "nonexistent")
		if err == nil {
			t.Fatal("expected error")
		}
		_, ok := err.(ErrNotFound)
		if !ok {
			t.Errorf("error type = %T, want ErrNotFound", err)
		}
	})

	t.Run("UpdateHeartbeat not found", func(t *testing.T) {
		err := svc.UpdateHeartbeat(ctx, "nonexistent")
		if err == nil {
			t.Fatal("expected error")
		}
		_, ok := err.(ErrNotFound)
		if !ok {
			t.Errorf("error type = %T, want ErrNotFound", err)
		}
	})

	t.Run("Delete not found", func(t *testing.T) {
		err := svc.Delete(ctx, "nonexistent")
		if err == nil {
			t.Fatal("expected error")
		}
		_, ok := err.(ErrNotFound)
		if !ok {
			t.Errorf("error type = %T, want ErrNotFound", err)
		}
	})

	t.Run("IsAlive not found", func(t *testing.T) {
		_, err := svc.IsAlive(ctx, "nonexistent", 15*time.Second)
		if err == nil {
			t.Fatal("expected error")
		}
		_, ok := err.(ErrNotFound)
		if !ok {
			t.Errorf("error type = %T, want ErrNotFound", err)
		}
	})
}

func TestWorkspaceService_NotFoundErrors(t *testing.T) {
	ctx := context.Background()
	repo := NewMockWorkspaceRepository()
	svc := NewWorkspaceService(repo)

	t.Run("Delete not found", func(t *testing.T) {
		err := svc.Delete(ctx, "nonexistent")
		if err == nil {
			t.Fatal("expected error")
		}
		_, ok := err.(ErrNotFound)
		if !ok {
			t.Errorf("error type = %T, want ErrNotFound", err)
		}
	})
}

func TestErrors_AlreadyExists(t *testing.T) {
	err := newAlreadyExistsError("work item", "123")
	if err == nil {
		t.Fatal("expected error")
	}
	_, ok := err.(ErrAlreadyExists)
	if !ok {
		t.Errorf("error type = %T, want ErrAlreadyExists", err)
	}
}
