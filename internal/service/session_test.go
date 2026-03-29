package service

import (
	"context"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

func implTask(id, workItemID, workspaceID, subPlanID string, status domain.TaskStatus) domain.Task {
	return domain.Task{
		ID:             id,
		WorkItemID:     workItemID,
		WorkspaceID:    workspaceID,
		Phase:          domain.TaskPhaseImplementation,
		SubPlanID:      subPlanID,
		RepositoryName: "repo1",
		HarnessName:    "omp",
		Status:         status,
	}
}

func TestSessionService_Create(t *testing.T) {
	ctx := context.Background()
	repo := NewMockSessionRepository()
	svc := NewTaskService(repository.NoopTransacter{Res: repository.Resources{Tasks: repo}})

	t.Run("creates session with pending status", func(t *testing.T) {
		session := implTask("session-1", "wi-1", "ws-1", "sp-1", "")
		if err := svc.Create(ctx, session); err != nil {
			t.Fatalf("Create failed: %v", err)
		}

		got, err := svc.Get(ctx, "session-1")
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if got.Status != domain.AgentSessionPending {
			t.Errorf("Status = %q, want %q", got.Status, domain.AgentSessionPending)
		}
	})

	t.Run("rejects non-pending initial status", func(t *testing.T) {
		session := implTask("session-2", "wi-1", "ws-1", "sp-1", domain.AgentSessionRunning)
		err := svc.Create(ctx, session)
		if err == nil {
			t.Fatal("expected error for non-pending initial status")
		}
		if _, ok := err.(ErrInvalidInput); !ok {
			t.Errorf("error type = %T, want ErrInvalidInput", err)
		}
	})

	t.Run("rejects missing work item", func(t *testing.T) {
		session := implTask("session-3", "", "ws-1", "sp-1", "")
		if err := svc.Create(ctx, session); err == nil {
			t.Fatal("expected error for missing work item")
		}
	})

	t.Run("allows planning sessions without subplan", func(t *testing.T) {
		session := domain.Task{
			ID:          "plan-1",
			WorkItemID:  "wi-1",
			WorkspaceID: "ws-1",
			Phase:       domain.TaskPhasePlanning,
			HarnessName: "omp",
		}
		if err := svc.Create(ctx, session); err != nil {
			t.Fatalf("Create planning session failed: %v", err)
		}
	})
}

func TestSessionService_ValidTransitions(t *testing.T) {
	ctx := context.Background()

	validTransitions := []struct {
		from domain.TaskStatus
		to   domain.TaskStatus
		name string
	}{
		{domain.AgentSessionPending, domain.AgentSessionRunning, "pending -> running"},
		{domain.AgentSessionPending, domain.AgentSessionFailed, "pending -> failed"},
		{domain.AgentSessionRunning, domain.AgentSessionWaitingForAnswer, "running -> waiting_for_answer"},
		{domain.AgentSessionRunning, domain.AgentSessionCompleted, "running -> completed"},
		{domain.AgentSessionRunning, domain.AgentSessionInterrupted, "running -> interrupted"},
		{domain.AgentSessionRunning, domain.AgentSessionFailed, "running -> failed"},
		{domain.AgentSessionWaitingForAnswer, domain.AgentSessionRunning, "waiting_for_answer -> running"},
		{domain.AgentSessionWaitingForAnswer, domain.AgentSessionFailed, "waiting_for_answer -> failed"},
		{domain.AgentSessionInterrupted, domain.AgentSessionRunning, "interrupted -> running"},
		{domain.AgentSessionInterrupted, domain.AgentSessionFailed, "interrupted -> failed"},
		{domain.AgentSessionFailed, domain.AgentSessionRunning, "failed -> running"},
	}

	for _, tc := range validTransitions {
		t.Run(tc.name, func(t *testing.T) {
			repo := NewMockSessionRepository()
			svc := NewTaskService(repository.NoopTransacter{Res: repository.Resources{Tasks: repo}})

			session := implTask("session-test", "wi-1", "ws-1", "sp-1", tc.from)
			repo.sessions["session-test"] = session

			if err := svc.Transition(ctx, "session-test", tc.to); err != nil {
				t.Fatalf("Transition from %s to %s failed: %v", tc.from, tc.to, err)
			}

			got, err := svc.Get(ctx, "session-test")
			if err != nil {
				t.Fatalf("Get failed: %v", err)
			}
			if got.Status != tc.to {
				t.Errorf("Status = %q, want %q", got.Status, tc.to)
			}
		})
	}
}

func TestSessionService_InvalidTransitions(t *testing.T) {
	ctx := context.Background()

	invalidTransitions := []struct {
		from domain.TaskStatus
		to   domain.TaskStatus
		name string
	}{
		{domain.AgentSessionPending, domain.AgentSessionCompleted, "pending -> completed"},
		{domain.AgentSessionPending, domain.AgentSessionInterrupted, "pending -> interrupted"},
		{domain.AgentSessionPending, domain.AgentSessionWaitingForAnswer, "pending -> waiting_for_answer"},
		{domain.AgentSessionRunning, domain.AgentSessionPending, "running -> pending"},
		{domain.AgentSessionWaitingForAnswer, domain.AgentSessionCompleted, "waiting_for_answer -> completed"},
		{domain.AgentSessionCompleted, domain.AgentSessionFailed, "completed -> failed"},
		{domain.AgentSessionCompleted, domain.AgentSessionPending, "completed -> pending"},
		{domain.AgentSessionInterrupted, domain.AgentSessionCompleted, "interrupted -> completed"},
		{domain.AgentSessionFailed, domain.AgentSessionPending, "failed -> pending"},
	}

	for _, tc := range invalidTransitions {
		t.Run(tc.name, func(t *testing.T) {
			repo := NewMockSessionRepository()
			svc := NewTaskService(repository.NoopTransacter{Res: repository.Resources{Tasks: repo}})

			session := implTask("session-test", "wi-1", "ws-1", "sp-1", tc.from)
			repo.sessions["session-test"] = session

			err := svc.Transition(ctx, "session-test", tc.to)
			if err == nil {
				t.Fatalf("expected error for transition from %s to %s", tc.from, tc.to)
			}
			if _, ok := err.(ErrInvalidTransition); !ok {
				t.Errorf("error type = %T, want ErrInvalidTransition", err)
			}
		})
	}
}

func TestSessionService_StartSetsStartedAt(t *testing.T) {
	ctx := context.Background()
	repo := NewMockSessionRepository()
	svc := NewTaskService(repository.NoopTransacter{Res: repository.Resources{Tasks: repo}})

	session := implTask("session-1", "wi-1", "ws-1", "sp-1", domain.AgentSessionPending)
	repo.sessions["session-1"] = session

	if err := svc.Start(ctx, "session-1"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	got, _ := svc.Get(ctx, "session-1")
	if got.StartedAt == nil {
		t.Error("StartedAt should be set")
	}
}

func TestSessionService_CompleteSetsCompletedAt(t *testing.T) {
	ctx := context.Background()
	repo := NewMockSessionRepository()
	svc := NewTaskService(repository.NoopTransacter{Res: repository.Resources{Tasks: repo}})

	session := implTask("session-1", "wi-1", "ws-1", "sp-1", domain.AgentSessionRunning)
	repo.sessions["session-1"] = session

	if err := svc.Complete(ctx, "session-1"); err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	got, _ := svc.Get(ctx, "session-1")
	if got.CompletedAt == nil {
		t.Error("CompletedAt should be set")
	}
}

func TestSessionService_InterruptSetsShutdownAt(t *testing.T) {
	ctx := context.Background()
	repo := NewMockSessionRepository()
	svc := NewTaskService(repository.NoopTransacter{Res: repository.Resources{Tasks: repo}})

	session := implTask("session-1", "wi-1", "ws-1", "sp-1", domain.AgentSessionRunning)
	repo.sessions["session-1"] = session

	if err := svc.Interrupt(ctx, "session-1"); err != nil {
		t.Fatalf("Interrupt failed: %v", err)
	}

	got, _ := svc.Get(ctx, "session-1")
	if got.ShutdownAt == nil {
		t.Error("ShutdownAt should be set")
	}
}

func TestSessionService_FailSetsExitCode(t *testing.T) {
	ctx := context.Background()
	repo := NewMockSessionRepository()
	svc := NewTaskService(repository.NoopTransacter{Res: repository.Resources{Tasks: repo}})

	session := implTask("session-1", "wi-1", "ws-1", "sp-1", domain.AgentSessionRunning)
	repo.sessions["session-1"] = session

	exitCode := 1
	if err := svc.Fail(ctx, "session-1", &exitCode); err != nil {
		t.Fatalf("Fail failed: %v", err)
	}

	got, _ := svc.Get(ctx, "session-1")
	if got.ExitCode == nil || *got.ExitCode != 1 {
		t.Errorf("ExitCode = %v, want 1", got.ExitCode)
	}
}

func TestSessionService_FindInterruptedByWorkspace(t *testing.T) {
	ctx := context.Background()
	repo := NewMockSessionRepository()
	svc := NewTaskService(repository.NoopTransacter{Res: repository.Resources{Tasks: repo}})

	repo.sessions["s-1"] = implTask("s-1", "wi-1", "ws-1", "sp-1", domain.AgentSessionInterrupted)
	repo.sessions["s-2"] = implTask("s-2", "wi-2", "ws-1", "sp-1", domain.AgentSessionRunning)
	repo.sessions["s-3"] = implTask("s-3", "wi-3", "ws-1", "sp-1", domain.AgentSessionInterrupted)
	repo.sessions["s-4"] = implTask("s-4", "wi-4", "ws-2", "sp-2", domain.AgentSessionInterrupted)
	repo.byWorkspace["ws-1"] = []string{"s-1", "s-2", "s-3"}
	repo.byWorkspace["ws-2"] = []string{"s-4"}

	interrupted, err := svc.FindInterruptedByWorkspace(ctx, "ws-1")
	if err != nil {
		t.Fatalf("FindInterruptedByWorkspace failed: %v", err)
	}

	if len(interrupted) != 2 {
		t.Errorf("got %d interrupted sessions, want 2", len(interrupted))
	}
}

func TestSessionService_FollowUpRestartClearsCompletedAt(t *testing.T) {
	ctx := context.Background()
	repo := NewMockSessionRepository()
	svc := NewTaskService(repository.NoopTransacter{Res: repository.Resources{Tasks: repo}})

	now := time.Now()
	session := implTask("session-1", "wi-1", "ws-1", "sp-1", domain.AgentSessionCompleted)
	session.CompletedAt = &now
	repo.sessions["session-1"] = session

	if err := svc.FollowUpRestart(ctx, "session-1"); err != nil {
		t.Fatalf("FollowUpRestart failed: %v", err)
	}

	got, _ := svc.Get(ctx, "session-1")
	if got.Status != domain.AgentSessionRunning {
		t.Errorf("Status = %s, want running", got.Status)
	}
	if got.CompletedAt != nil {
		t.Error("CompletedAt should be nil after FollowUpRestart")
	}
}

func TestSessionService_FollowUpRestartRejectsNonCompleted(t *testing.T) {
	ctx := context.Background()
	repo := NewMockSessionRepository()
	svc := NewTaskService(repository.NoopTransacter{Res: repository.Resources{Tasks: repo}})

	repo.sessions["session-1"] = implTask("session-1", "wi-1", "ws-1", "sp-1", domain.AgentSessionRunning)

	err := svc.FollowUpRestart(ctx, "session-1")
	if err == nil {
		t.Fatal("expected error for FollowUpRestart on running session")
	}
}

func TestSessionService_UpdateResumeInfo(t *testing.T) {
	ctx := context.Background()
	repo := NewMockSessionRepository()
	svc := NewTaskService(repository.NoopTransacter{Res: repository.Resources{Tasks: repo}})

	session := implTask("session-1", "wi-1", "ws-1", "sp-1", domain.AgentSessionRunning)
	repo.sessions["session-1"] = session

	wantInfo := map[string]string{
		"omp_session_file": "/tmp/x.jsonl",
		"omp_session_id":   "abc",
	}
	if err := svc.UpdateResumeInfo(ctx, "session-1", wantInfo); err != nil {
		t.Fatalf("UpdateResumeInfo failed: %v", err)
	}

	got, _ := svc.Get(ctx, "session-1")
	if got.ResumeInfo["omp_session_file"] != wantInfo["omp_session_file"] {
		t.Errorf("ResumeInfo[omp_session_file] = %q, want %q", got.ResumeInfo["omp_session_file"], wantInfo["omp_session_file"])
	}
	if got.ResumeInfo["omp_session_id"] != wantInfo["omp_session_id"] {
		t.Errorf("ResumeInfo[omp_session_id] = %q, want %q", got.ResumeInfo["omp_session_id"], wantInfo["omp_session_id"])
	}
}
