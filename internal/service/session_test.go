package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

func implSession(id, workItemID, workspaceID, subPlanID string, status domain.AgentSessionStatus) domain.AgentSession {
	return domain.AgentSession{
		ID:             id,
		WorkItemID:     workItemID,
		WorkspaceID:    workspaceID,
		Kind:           domain.AgentSessionKindImplementation,
		SubPlanID:      subPlanID,
		RepositoryName: "repo1",
		HarnessName:    "omp",
		Status:         status,
	}
}

func TestSessionService_Create(t *testing.T) {
	ctx := context.Background()
	repo := NewMockSessionRepository()
	svc := NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: repo}}, newTestBus())

	t.Run("creates session with pending status", func(t *testing.T) {
		session := implSession("session-1", "wi-1", "ws-1", "sp-1", "")
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
		session := implSession("session-2", "wi-1", "ws-1", "sp-1", domain.AgentSessionRunning)
		err := svc.Create(ctx, session)
		if err == nil {
			t.Fatal("expected error for non-pending initial status")
		}
		if _, ok := err.(ErrInvalidInput); !ok {
			t.Errorf("error type = %T, want ErrInvalidInput", err)
		}
	})

	t.Run("rejects missing work item", func(t *testing.T) {
		session := implSession("session-3", "", "ws-1", "sp-1", "")
		if err := svc.Create(ctx, session); err == nil {
			t.Fatal("expected error for missing work item")
		}
	})

	t.Run("allows planning sessions without subplan", func(t *testing.T) {
		session := domain.AgentSession{
			ID:          "plan-1",
			WorkItemID:  "wi-1",
			WorkspaceID: "ws-1",
			Kind:        domain.AgentSessionKindPlanning,
			HarnessName: "omp",
		}
		if err := svc.Create(ctx, session); err != nil {
			t.Fatalf("Create planning session failed: %v", err)
		}
	})

	t.Run("allows manual session with required fields", func(t *testing.T) {
		session := domain.AgentSession{
			ID:             "manual-1",
			WorkItemID:     "wi-1",
			WorkspaceID:    "ws-1",
			Kind:           domain.AgentSessionKindManual,
			RepositoryName: "repo1",
			WorktreePath:   "/path/to/worktree",
			HarnessName:    "omp",
		}
		if err := svc.Create(ctx, session); err != nil {
			t.Fatalf("Create manual session failed: %v", err)
		}
	})

	t.Run("rejects manual session without repository", func(t *testing.T) {
		session := domain.AgentSession{
			ID:           "manual-2",
			WorkItemID:   "wi-1",
			WorkspaceID:  "ws-1",
			Kind:         domain.AgentSessionKindManual,
			WorktreePath: "/path/to/worktree",
			HarnessName:  "omp",
		}
		err := svc.Create(ctx, session)
		if err == nil {
			t.Fatal("expected error for manual session without repository")
		}
		if _, ok := err.(ErrInvalidInput); !ok {
			t.Errorf("error type = %T, want ErrInvalidInput", err)
		}
	})

	t.Run("rejects manual session without worktree path", func(t *testing.T) {
		session := domain.AgentSession{
			ID:             "manual-3",
			WorkItemID:     "wi-1",
			WorkspaceID:    "ws-1",
			Kind:           domain.AgentSessionKindManual,
			RepositoryName: "repo1",
			HarnessName:    "omp",
		}
		err := svc.Create(ctx, session)
		if err == nil {
			t.Fatal("expected error for manual session without worktree path")
		}
		if _, ok := err.(ErrInvalidInput); !ok {
			t.Errorf("error type = %T, want ErrInvalidInput", err)
		}
	})

	t.Run("rejects manual session without both repository and worktree path", func(t *testing.T) {
		session := domain.AgentSession{
			ID:          "manual-4",
			WorkItemID:  "wi-1",
			WorkspaceID: "ws-1",
			Kind:        domain.AgentSessionKindManual,
			HarnessName: "omp",
		}
		err := svc.Create(ctx, session)
		if err == nil {
			t.Fatal("expected error for manual session without both repository and worktree path")
		}
		if _, ok := err.(ErrInvalidInput); !ok {
			t.Errorf("error type = %T, want ErrInvalidInput", err)
		}
	})
}

func TestSessionService_ValidTransitions(t *testing.T) {
	ctx := context.Background()

	validTransitions := []struct {
		from domain.AgentSessionStatus
		to   domain.AgentSessionStatus
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
			svc := NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: repo}}, newTestBus())

			session := implSession("session-test", "wi-1", "ws-1", "sp-1", tc.from)
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
		from domain.AgentSessionStatus
		to   domain.AgentSessionStatus
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
			svc := NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: repo}}, newTestBus())

			session := implSession("session-test", "wi-1", "ws-1", "sp-1", tc.from)
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
	svc := NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: repo}}, newTestBus())

	session := implSession("session-1", "wi-1", "ws-1", "sp-1", domain.AgentSessionPending)
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
	svc := NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: repo}}, newTestBus())

	session := implSession("session-1", "wi-1", "ws-1", "sp-1", domain.AgentSessionRunning)
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
	svc := NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: repo}}, newTestBus())

	session := implSession("session-1", "wi-1", "ws-1", "sp-1", domain.AgentSessionRunning)
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
	svc := NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: repo}}, newTestBus())

	session := implSession("session-1", "wi-1", "ws-1", "sp-1", domain.AgentSessionRunning)
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
	svc := NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: repo}}, newTestBus())

	repo.sessions["s-1"] = implSession("s-1", "wi-1", "ws-1", "sp-1", domain.AgentSessionInterrupted)
	repo.sessions["s-2"] = implSession("s-2", "wi-2", "ws-1", "sp-1", domain.AgentSessionRunning)
	repo.sessions["s-3"] = implSession("s-3", "wi-3", "ws-1", "sp-1", domain.AgentSessionInterrupted)
	repo.sessions["s-4"] = implSession("s-4", "wi-4", "ws-2", "sp-2", domain.AgentSessionInterrupted)
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

func TestSessionService_RestartCompletedManualSessionClearsCompletedAtAndSetsOwner(t *testing.T) {
	ctx := context.Background()
	repo := NewMockSessionRepository()
	svc := NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: repo}}, newTestBus())

	now := time.Now()
	session := implSession("session-1", "wi-1", "ws-1", "sp-1", domain.AgentSessionCompleted)
	session.Kind = domain.AgentSessionKindManual
	session.CompletedAt = &now
	repo.sessions["session-1"] = session

	owner := "inst-follow-up"
	if err := svc.RestartCompletedManualSession(ctx, "session-1", &owner); err != nil {
		t.Fatalf("RestartCompletedManualSession failed: %v", err)
	}

	got, _ := svc.Get(ctx, "session-1")
	if got.Status != domain.AgentSessionRunning {
		t.Errorf("Status = %s, want running", got.Status)
	}
	if got.CompletedAt != nil {
		t.Error("CompletedAt should be nil after RestartCompletedManualSession")
	}
	if got.OwnerInstanceID == nil || *got.OwnerInstanceID != owner {
		t.Fatalf("OwnerInstanceID = %v, want %q", got.OwnerInstanceID, owner)
	}
}

func TestSessionService_RestartCompletedManualSessionRejectsGraphManagedSession(t *testing.T) {
	ctx := context.Background()
	repo := NewMockSessionRepository()
	svc := NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: repo}}, newTestBus())

	repo.sessions["session-1"] = implSession("session-1", "wi-1", "ws-1", "sp-1", domain.AgentSessionCompleted)

	err := svc.RestartCompletedManualSession(ctx, "session-1", nil)
	if err == nil {
		t.Fatal("expected error for graph-managed RestartCompletedManualSession")
	}
}

func TestSessionService_RestartCompletedManualSessionRejectsNonCompleted(t *testing.T) {
	ctx := context.Background()
	repo := NewMockSessionRepository()
	svc := NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: repo}}, newTestBus())

	session := implSession("session-1", "wi-1", "ws-1", "sp-1", domain.AgentSessionRunning)
	session.Kind = domain.AgentSessionKindManual
	repo.sessions["session-1"] = session

	err := svc.RestartCompletedManualSession(ctx, "session-1", nil)
	if err == nil {
		t.Fatal("expected error for RestartCompletedManualSession on running session")
	}
}

// TestSessionService_Resume_SetsParentAgentSessionID verifies that Resume
// links the new session row to the interrupted session via the agent-session
// graph edge so leaf-derivation in the TUI treats the interrupted session as
// superseded by the new one.
func TestSessionService_Resume_SetsParentAgentSessionID(t *testing.T) {
	ctx := context.Background()
	repo := NewMockSessionRepository()
	svc := NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: repo}}, newTestBus())

	interrupted := implSession("session-old", "wi-1", "ws-1", "sp-1", domain.AgentSessionInterrupted)
	repo.sessions[interrupted.ID] = interrupted

	owner := "inst-1"
	created, err := svc.CreateResumeChild(ctx, interrupted.ID, "omp", &owner)
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}

	if created.ParentAgentSessionID != interrupted.ID {
		t.Errorf("created.ParentAgentSessionID = %q, want %q", created.ParentAgentSessionID, interrupted.ID)
	}
	stored, err := svc.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get new session: %v", err)
	}
	if stored.ParentAgentSessionID != interrupted.ID {
		t.Errorf("stored.ParentAgentSessionID = %q, want %q", stored.ParentAgentSessionID, interrupted.ID)
	}
	if stored.ID == interrupted.ID {
		t.Error("Resume must create a new row; got same ID")
	}
}

func TestSessionService_ResumePayloadIncludesCanonicalGraphEdge(t *testing.T) {
	interrupted := implSession("session-old", "wi-1", "ws-1", "sp-1", domain.AgentSessionInterrupted)
	child := implSession("session-new", "wi-1", "ws-1", "sp-1", domain.AgentSessionRunning)
	child.ParentAgentSessionID = interrupted.ID

	payload := marshalAgentSessionPayloadWithOld(child, interrupted.ID)
	var decoded struct {
		WorkItemID      string              `json:"work_item_id"`
		SessionID       string              `json:"agent_session_id"`
		SourceSessionID string              `json:"source_session_id"`
		OldSessionID    string              `json:"old_session_id"`
		Session         domain.AgentSession `json:"session"`
	}
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if decoded.WorkItemID != child.WorkItemID {
		t.Fatalf("work_item_id = %q, want %q", decoded.WorkItemID, child.WorkItemID)
	}
	if decoded.SessionID != child.ID {
		t.Fatalf("agent_session_id = %q, want %q", decoded.SessionID, child.ID)
	}
	if decoded.SourceSessionID != interrupted.ID {
		t.Fatalf("source_session_id = %q, want %q", decoded.SourceSessionID, interrupted.ID)
	}
	if decoded.OldSessionID != interrupted.ID {
		t.Fatalf("old_session_id = %q, want %q", decoded.OldSessionID, interrupted.ID)
	}
	if decoded.Session.ParentAgentSessionID != interrupted.ID {
		t.Fatalf("session.parent_agent_session_id = %q, want %q", decoded.Session.ParentAgentSessionID, interrupted.ID)
	}
}

func TestSessionService_Resume_RejectsNonLeafSource(t *testing.T) {
	ctx := context.Background()
	repo := NewMockSessionRepository()
	svc := NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: repo}}, newTestBus())

	interrupted := implSession("session-old", "wi-1", "ws-1", "sp-1", domain.AgentSessionInterrupted)
	terminalChild := implSession("session-child", "wi-1", "ws-1", "sp-1", domain.AgentSessionFailed)
	terminalChild.ParentAgentSessionID = interrupted.ID
	repo.sessions[interrupted.ID] = interrupted
	repo.sessions[terminalChild.ID] = terminalChild

	_, err := svc.CreateResumeChild(ctx, interrupted.ID, "omp", nil)
	if !errors.Is(err, ErrAgentSessionNotLeaf) {
		t.Fatalf("Resume error = %v, want ErrAgentSessionNotLeaf", err)
	}
	if len(repo.sessions) != 2 {
		t.Fatalf("session count = %d, want 2 (no duplicate resume child)", len(repo.sessions))
	}
}

func TestSessionService_Resume_ReloadsSourceAndPreservesCurrentKind(t *testing.T) {
	ctx := context.Background()
	repo := NewMockSessionRepository()
	svc := NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: repo}}, newTestBus())

	stale := implSession("session-old", "stale-wi", "stale-ws", "stale-sp", domain.AgentSessionInterrupted)
	stale.Kind = domain.AgentSessionKindReview
	current := implSession(stale.ID, "wi-1", "ws-1", "sp-1", domain.AgentSessionInterrupted)
	current.Kind = domain.AgentSessionKindImplementation
	repo.sessions[current.ID] = current

	created, err := svc.CreateResumeChild(ctx, stale.ID, "omp", nil)
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}
	if created.WorkItemID != current.WorkItemID {
		t.Fatalf("created.WorkItemID = %q, want reloaded %q", created.WorkItemID, current.WorkItemID)
	}
	if created.Kind != current.Kind {
		t.Fatalf("created.Kind = %q, want reloaded %q", created.Kind, current.Kind)
	}
}

// TestSessionService_FollowUpFailed_SetsParentAgentSessionID verifies that the
// new agent session created from a failed source has its ParentAgentSessionID
// set to the failed session's ID. This is the audit trail that tells the leaf
// algorithm the failed row has been replaced.
func TestSessionService_FollowUpFailed_SetsParentAgentSessionID(t *testing.T) {
	ctx := context.Background()
	repo := NewMockSessionRepository()
	svc := NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: repo}}, newTestBus())

	failed := implSession("session-failed", "wi-1", "ws-1", "sp-1", domain.AgentSessionFailed)
	repo.sessions[failed.ID] = failed

	owner := "inst-1"
	created, err := svc.CreateRetryChild(ctx, failed.ID, "omp", &owner)
	if err != nil {
		t.Fatalf("FollowUpFailed failed: %v", err)
	}

	if created.ParentAgentSessionID != failed.ID {
		t.Errorf("created.ParentAgentSessionID = %q, want %q", created.ParentAgentSessionID, failed.ID)
	}
	stored, err := svc.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get new session: %v", err)
	}
	if stored.ParentAgentSessionID != failed.ID {
		t.Errorf("stored.ParentAgentSessionID = %q, want %q", stored.ParentAgentSessionID, failed.ID)
	}
	if stored.ID == failed.ID {
		t.Error("FollowUpFailed must create a new row; got same ID")
	}
}

func TestSessionService_FollowUpChildEmitsFollowUpEvent(t *testing.T) {
	ctx := context.Background()
	repo := NewMockSessionRepository()
	bus := newTestBus()
	sub, err := bus.Subscribe("follow-up-test", string(domain.EventAgentSessionFollowUp))
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}
	svc := NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: repo}}, bus)

	failed := implSession("session-failed", "wi-1", "ws-1", "sp-1", domain.AgentSessionFailed)
	repo.sessions[failed.ID] = failed

	created, err := svc.CreateFollowUpChild(ctx, failed.ID, "omp", nil)
	if err != nil {
		t.Fatalf("CreateFollowUpChild failed: %v", err)
	}

	select {
	case evt := <-sub.C:
		if evt.EventType != string(domain.EventAgentSessionFollowUp) {
			t.Fatalf("event type = %q, want %q", evt.EventType, domain.EventAgentSessionFollowUp)
		}
		var decoded struct {
			SessionID       string `json:"agent_session_id"`
			SourceSessionID string `json:"source_session_id"`
			OldSessionID    string `json:"old_session_id"`
		}
		if err := json.Unmarshal([]byte(evt.Payload), &decoded); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if decoded.SessionID != created.ID {
			t.Fatalf("agent_session_id = %q, want %q", decoded.SessionID, created.ID)
		}
		if decoded.SourceSessionID != failed.ID {
			t.Fatalf("source_session_id = %q, want %q", decoded.SourceSessionID, failed.ID)
		}
		if decoded.OldSessionID != failed.ID {
			t.Fatalf("old_session_id = %q, want %q", decoded.OldSessionID, failed.ID)
		}
	default:
		t.Fatal("expected follow-up event")
	}
}

func TestSessionService_FollowUpFailed_ReloadsSourceAndPreservesCurrentKind(t *testing.T) {
	ctx := context.Background()
	repo := NewMockSessionRepository()
	svc := NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: repo}}, newTestBus())

	stale := implSession("session-failed", "stale-wi", "stale-ws", "stale-sp", domain.AgentSessionFailed)
	stale.Kind = domain.AgentSessionKindReview
	current := implSession(stale.ID, "wi-1", "ws-1", "sp-1", domain.AgentSessionFailed)
	current.Kind = domain.AgentSessionKindImplementation
	repo.sessions[current.ID] = current

	created, err := svc.CreateRetryChild(ctx, stale.ID, "omp", nil)
	if err != nil {
		t.Fatalf("FollowUpFailed failed: %v", err)
	}
	if created.WorkItemID != current.WorkItemID {
		t.Fatalf("created.WorkItemID = %q, want reloaded %q", created.WorkItemID, current.WorkItemID)
	}
	if created.Kind != current.Kind {
		t.Fatalf("created.Kind = %q, want reloaded %q", created.Kind, current.Kind)
	}
}

func TestSessionService_FollowUpFailed_RejectsNonLeafSource(t *testing.T) {
	ctx := context.Background()
	repo := NewMockSessionRepository()
	svc := NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: repo}}, newTestBus())

	failed := implSession("session-failed", "wi-1", "ws-1", "sp-1", domain.AgentSessionFailed)
	child := implSession("session-child", "wi-1", "ws-1", "sp-1", domain.AgentSessionFailed)
	child.ParentAgentSessionID = failed.ID
	repo.sessions[failed.ID] = failed
	repo.sessions[child.ID] = child

	_, err := svc.CreateRetryChild(ctx, failed.ID, "omp", nil)
	if !errors.Is(err, ErrAgentSessionNotLeaf) {
		t.Fatalf("FollowUpFailed error = %v, want ErrAgentSessionNotLeaf", err)
	}
	if len(repo.sessions) != 2 {
		t.Fatalf("session count = %d, want 2 (no duplicate retry child)", len(repo.sessions))
	}
}

func TestSessionService_UpdateResumeInfo(t *testing.T) {
	ctx := context.Background()
	repo := NewMockSessionRepository()
	svc := NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: repo}}, newTestBus())

	session := implSession("session-1", "wi-1", "ws-1", "sp-1", domain.AgentSessionRunning)
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

func TestSessionService_Create_Foreman(t *testing.T) {
	ctx := context.Background()
	repo := NewMockSessionRepository()
	svc := NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: repo}}, newTestBus())

	t.Run("creates foreman session with pending status", func(t *testing.T) {
		session := domain.AgentSession{
			ID:          "foreman-1",
			WorkItemID:  "wi-1",
			WorkspaceID: "ws-1",
			Kind:        domain.AgentSessionKindForeman,
			HarnessName: "omp",
		}
		if err := svc.Create(ctx, session); err != nil {
			t.Fatalf("Create failed: %v", err)
		}

		got, err := svc.Get(ctx, "foreman-1")
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if got.Status != domain.AgentSessionPending {
			t.Errorf("Status = %q, want %q", got.Status, domain.AgentSessionPending)
		}
	})

	t.Run("Start transitions to running", func(t *testing.T) {
		session := domain.AgentSession{
			ID:          "foreman-start",
			WorkItemID:  "wi-1",
			WorkspaceID: "ws-1",
			Kind:        domain.AgentSessionKindForeman,
			HarnessName: "omp",
		}
		if err := svc.Create(ctx, session); err != nil {
			t.Fatalf("Create failed: %v", err)
		}

		if err := svc.Start(ctx, "foreman-start"); err != nil {
			t.Fatalf("Start failed: %v", err)
		}

		got, _ := svc.Get(ctx, "foreman-start")
		if got.Status != domain.AgentSessionRunning {
			t.Errorf("Status = %q, want %q", got.Status, domain.AgentSessionRunning)
		}
		if got.StartedAt == nil {
			t.Error("StartedAt should be set")
		}
	})

	t.Run("Complete transitions to completed", func(t *testing.T) {
		session := domain.AgentSession{
			ID:          "foreman-complete",
			WorkItemID:  "wi-1",
			WorkspaceID: "ws-1",
			Kind:        domain.AgentSessionKindForeman,
			HarnessName: "omp",
		}
		if err := svc.Create(ctx, session); err != nil {
			t.Fatalf("Create failed: %v", err)
		}
		if err := svc.Start(ctx, "foreman-complete"); err != nil {
			t.Fatalf("Start failed: %v", err)
		}

		if err := svc.Complete(ctx, "foreman-complete"); err != nil {
			t.Fatalf("Complete failed: %v", err)
		}

		got, _ := svc.Get(ctx, "foreman-complete")
		if got.Status != domain.AgentSessionCompleted {
			t.Errorf("Status = %q, want %q", got.Status, domain.AgentSessionCompleted)
		}
		if got.CompletedAt == nil {
			t.Error("CompletedAt should be set")
		}
	})

	t.Run("Interrupt transitions to interrupted", func(t *testing.T) {
		session := domain.AgentSession{
			ID:          "foreman-interrupt",
			WorkItemID:  "wi-1",
			WorkspaceID: "ws-1",
			Kind:        domain.AgentSessionKindForeman,
			HarnessName: "omp",
		}
		if err := svc.Create(ctx, session); err != nil {
			t.Fatalf("Create failed: %v", err)
		}
		if err := svc.Start(ctx, "foreman-interrupt"); err != nil {
			t.Fatalf("Start failed: %v", err)
		}

		if err := svc.Interrupt(ctx, "foreman-interrupt"); err != nil {
			t.Fatalf("Interrupt failed: %v", err)
		}

		got, _ := svc.Get(ctx, "foreman-interrupt")
		if got.Status != domain.AgentSessionInterrupted {
			t.Errorf("Status = %q, want %q", got.Status, domain.AgentSessionInterrupted)
		}
		if got.ShutdownAt == nil {
			t.Error("ShutdownAt should be set")
		}
	})

	t.Run("Fail transitions to failed", func(t *testing.T) {
		session := domain.AgentSession{
			ID:          "foreman-fail",
			WorkItemID:  "wi-1",
			WorkspaceID: "ws-1",
			Kind:        domain.AgentSessionKindForeman,
			HarnessName: "omp",
		}
		if err := svc.Create(ctx, session); err != nil {
			t.Fatalf("Create failed: %v", err)
		}

		exitCode := 42
		if err := svc.Fail(ctx, "foreman-fail", &exitCode); err != nil {
			t.Fatalf("Fail failed: %v", err)
		}

		got, _ := svc.Get(ctx, "foreman-fail")
		if got.Status != domain.AgentSessionFailed {
			t.Errorf("Status = %q, want %q", got.Status, domain.AgentSessionFailed)
		}
		if got.ExitCode == nil || *got.ExitCode != 42 {
			t.Errorf("ExitCode = %v, want 42", got.ExitCode)
		}
	})
}
