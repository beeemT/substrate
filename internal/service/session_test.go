package service

import (
	"context"
	"testing"

	"github.com/beeemT/substrate/internal/domain"
)

func TestSessionService_Create(t *testing.T) {
	ctx := context.Background()
	repo := NewMockSessionRepository()
	svc := NewTaskService(repo)

	t.Run("creates session with pending status", func(t *testing.T) {
		session := domain.Task{
			ID:             "session-1",
			WorkspaceID:    "ws-1",
			SubPlanID:      "sp-1",
			RepositoryName: "repo1",
			HarnessName:    "omp",
		}
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
		session := domain.Task{
			ID:             "session-2",
			WorkspaceID:    "ws-1",
			SubPlanID:      "sp-1",
			RepositoryName: "repo1",
			Status:         domain.AgentSessionRunning,
		}
		err := svc.Create(ctx, session)
		if err == nil {
			t.Fatal("expected error for non-pending initial status")
		}
		_, ok := err.(ErrInvalidInput)
		if !ok {
			t.Errorf("error type = %T, want ErrInvalidInput", err)
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
	}

	for _, tc := range validTransitions {
		t.Run(tc.name, func(t *testing.T) {
			repo := NewMockSessionRepository()
			svc := NewTaskService(repo)

			session := domain.Task{
				ID:             "session-test",
				WorkspaceID:    "ws-1",
				SubPlanID:      "sp-1",
				RepositoryName: "repo1",
				Status:         tc.from,
			}
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
		{domain.AgentSessionWaitingForAnswer, domain.AgentSessionInterrupted, "waiting_for_answer -> interrupted"},
		{domain.AgentSessionCompleted, domain.AgentSessionRunning, "completed -> running"},
		{domain.AgentSessionCompleted, domain.AgentSessionFailed, "completed -> failed"},
		{domain.AgentSessionInterrupted, domain.AgentSessionCompleted, "interrupted -> completed"},
		{domain.AgentSessionFailed, domain.AgentSessionRunning, "failed -> running"},
		{domain.AgentSessionFailed, domain.AgentSessionPending, "failed -> pending"},
	}

	for _, tc := range invalidTransitions {
		t.Run(tc.name, func(t *testing.T) {
			repo := NewMockSessionRepository()
			svc := NewTaskService(repo)

			session := domain.Task{
				ID:             "session-test",
				WorkspaceID:    "ws-1",
				SubPlanID:      "sp-1",
				RepositoryName: "repo1",
				Status:         tc.from,
			}
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
	svc := NewTaskService(repo)

	session := domain.Task{
		ID:             "session-1",
		WorkspaceID:    "ws-1",
		SubPlanID:      "sp-1",
		RepositoryName: "repo1",
		Status:         domain.AgentSessionPending,
	}
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
	svc := NewTaskService(repo)

	session := domain.Task{
		ID:             "session-1",
		WorkspaceID:    "ws-1",
		SubPlanID:      "sp-1",
		RepositoryName: "repo1",
		Status:         domain.AgentSessionRunning,
	}
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
	svc := NewTaskService(repo)

	session := domain.Task{
		ID:             "session-1",
		WorkspaceID:    "ws-1",
		SubPlanID:      "sp-1",
		RepositoryName: "repo1",
		Status:         domain.AgentSessionRunning,
	}
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
	svc := NewTaskService(repo)

	session := domain.Task{
		ID:             "session-1",
		WorkspaceID:    "ws-1",
		SubPlanID:      "sp-1",
		RepositoryName: "repo1",
		Status:         domain.AgentSessionRunning,
	}
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
	svc := NewTaskService(repo)

	// Create sessions with different statuses
	repo.sessions["s-1"] = domain.Task{ID: "s-1", WorkspaceID: "ws-1", SubPlanID: "sp-1", Status: domain.AgentSessionInterrupted}
	repo.sessions["s-2"] = domain.Task{ID: "s-2", WorkspaceID: "ws-1", SubPlanID: "sp-1", Status: domain.AgentSessionRunning}
	repo.sessions["s-3"] = domain.Task{ID: "s-3", WorkspaceID: "ws-1", SubPlanID: "sp-1", Status: domain.AgentSessionInterrupted}
	repo.sessions["s-4"] = domain.Task{ID: "s-4", WorkspaceID: "ws-2", SubPlanID: "sp-2", Status: domain.AgentSessionInterrupted}
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
