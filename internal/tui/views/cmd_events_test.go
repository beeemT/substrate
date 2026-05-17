package views

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
)

type cmdWorkItemRepo struct{ items map[string]domain.Session }

func (r *cmdWorkItemRepo) Get(_ context.Context, id string) (domain.Session, error) {
	item, ok := r.items[id]
	if !ok {
		return domain.Session{}, repository.ErrNotFound
	}

	return item, nil
}

func (r *cmdWorkItemRepo) List(_ context.Context, filter repository.SessionFilter) ([]domain.Session, error) {
	items := make([]domain.Session, 0, len(r.items))
	for _, item := range r.items {
		if filter.WorkspaceID != nil && item.WorkspaceID != *filter.WorkspaceID {
			continue
		}
		items = append(items, item)
	}

	return items, nil
}

func (r *cmdWorkItemRepo) Create(_ context.Context, item domain.Session) error {
	r.items[item.ID] = item

	return nil
}

func (r *cmdWorkItemRepo) Update(_ context.Context, item domain.Session) error {
	r.items[item.ID] = item

	return nil
}

func (r *cmdWorkItemRepo) Delete(_ context.Context, id string) error {
	delete(r.items, id)

	return nil
}

type cmdPlanRepo struct{ plans map[string]domain.Plan }

func (r *cmdPlanRepo) Get(_ context.Context, id string) (domain.Plan, error) {
	plan, ok := r.plans[id]
	if !ok {
		return domain.Plan{}, repository.ErrNotFound
	}

	return plan, nil
}

func (r *cmdPlanRepo) GetByWorkItemID(_ context.Context, workItemID string) (domain.Plan, error) {
	for _, plan := range r.plans {
		if plan.WorkItemID == workItemID {
			return plan, nil
		}
	}

	return domain.Plan{}, repository.ErrNotFound
}

func (r *cmdPlanRepo) Create(_ context.Context, plan domain.Plan) error {
	r.plans[plan.ID] = plan

	return nil
}

func (r *cmdPlanRepo) Update(_ context.Context, plan domain.Plan) error {
	r.plans[plan.ID] = plan

	return nil
}

func (r *cmdPlanRepo) Delete(_ context.Context, id string) error {
	delete(r.plans, id)

	return nil
}
func (r *cmdPlanRepo) AppendFAQ(_ context.Context, _ domain.FAQEntry) error { return nil }

type cmdSubPlanRepo struct{ subPlans map[string]domain.TaskPlan }

func (r *cmdSubPlanRepo) Get(_ context.Context, id string) (domain.TaskPlan, error) {
	sp, ok := r.subPlans[id]
	if !ok {
		return domain.TaskPlan{}, repository.ErrNotFound
	}

	return sp, nil
}

func (r *cmdSubPlanRepo) GetForUpdate(_ context.Context, id string) (domain.TaskPlan, error) {
	// GetForUpdate behaves identically to Get for mock purposes.
	// Row locking is tested in integration tests with real SQLite.
	return r.Get(context.Background(), id)
}

func (r *cmdSubPlanRepo) ListByPlanID(_ context.Context, planID string) ([]domain.TaskPlan, error) {
	result := make([]domain.TaskPlan, 0, len(r.subPlans))
	for _, sp := range r.subPlans {
		if sp.PlanID == planID {
			result = append(result, sp)
		}
	}

	return result, nil
}

func (r *cmdSubPlanRepo) Create(_ context.Context, sp domain.TaskPlan) error {
	r.subPlans[sp.ID] = sp

	return nil
}

func (r *cmdSubPlanRepo) Update(_ context.Context, sp domain.TaskPlan) error {
	r.subPlans[sp.ID] = sp

	return nil
}

func (r *cmdSubPlanRepo) Delete(_ context.Context, id string) error {
	delete(r.subPlans, id)

	return nil
}

type cmdSessionRepo struct {
	sessions map[string]domain.AgentSession
}

func (r *cmdSessionRepo) Get(_ context.Context, id string) (domain.AgentSession, error) {
	session, ok := r.sessions[id]
	if !ok {
		return domain.AgentSession{}, repository.ErrNotFound
	}

	return session, nil
}

func (r *cmdSessionRepo) ListByWorkItemID(_ context.Context, workItemID string) ([]domain.AgentSession, error) {
	result := make([]domain.AgentSession, 0, len(r.sessions))
	for _, session := range r.sessions {
		if session.WorkItemID == workItemID {
			result = append(result, session)
		}
	}

	return result, nil
}

func (r *cmdSessionRepo) ListBySubPlanID(_ context.Context, subPlanID string) ([]domain.AgentSession, error) {
	result := make([]domain.AgentSession, 0, len(r.sessions))
	for _, session := range r.sessions {
		if session.SubPlanID == subPlanID {
			result = append(result, session)
		}
	}

	return result, nil
}

func (r *cmdSessionRepo) ListByWorkspaceID(_ context.Context, workspaceID string) ([]domain.AgentSession, error) {
	result := make([]domain.AgentSession, 0, len(r.sessions))
	for _, session := range r.sessions {
		if session.WorkspaceID == workspaceID {
			result = append(result, session)
		}
	}

	return result, nil
}

func (r *cmdSessionRepo) ListByOwnerInstanceID(_ context.Context, instanceID string) ([]domain.AgentSession, error) {
	result := make([]domain.AgentSession, 0, len(r.sessions))
	for _, session := range r.sessions {
		if session.OwnerInstanceID != nil && *session.OwnerInstanceID == instanceID {
			result = append(result, session)
		}
	}

	return result, nil
}

func (r *cmdSessionRepo) SearchHistory(_ context.Context, _ domain.SessionHistoryFilter) ([]domain.SessionHistoryEntry, error) {
	return nil, nil
}

func (r *cmdSessionRepo) Create(_ context.Context, session domain.AgentSession) error {
	r.sessions[session.ID] = session

	return nil
}

func (r *cmdSessionRepo) Update(_ context.Context, session domain.AgentSession) error {
	r.sessions[session.ID] = session

	return nil
}

func (r *cmdSessionRepo) Delete(_ context.Context, id string) error {
	delete(r.sessions, id)

	return nil
}

func TestApprovePlanCmd_PublishesPlanApprovedEvent(t *testing.T) {
	workItemRepo := &cmdWorkItemRepo{items: map[string]domain.Session{
		"wi-1": {ID: "wi-1", WorkspaceID: "ws-1", ExternalID: "gh:issue:acme/rocket#42", Source: "github", SourceScope: domain.ScopeIssues, SourceItemIDs: []string{"acme/rocket#42", "acme/rocket#43"}, State: domain.SessionPlanReview},
	}}
	planRepo := &cmdPlanRepo{plans: map[string]domain.Plan{
		"plan-1": {ID: "plan-1", WorkItemID: "wi-1", Status: domain.PlanPendingReview, OrchestratorPlan: "Overall plan text"},
	}}
	subPlanRepo := &cmdSubPlanRepo{subPlans: map[string]domain.TaskPlan{
		"sp-1": {ID: "sp-1", PlanID: "plan-1", RepositoryName: "rocket", Content: "Sub-plan content", Order: 0},
	}}
	workItemSvc := service.NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: workItemRepo}}, NewNoopPublisher())
	bus := event.NewBus(event.BusConfig{})
	defer bus.Close()
	planSvc := service.NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: planRepo, SubPlans: subPlanRepo, Sessions: workItemRepo}}, bus)

	sub, err := bus.Subscribe("approve-test", string(domain.EventPlanApproved))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	cfg := &config.Config{}
	cfg.Adapters.GitHub.IssueCommentContent = config.IssueCommentSubPlan

	msg := ApprovePlanCmd(workItemSvc, planSvc, cfg, bus, "plan-1", "wi-1")()
	if _, ok := msg.(PlanApprovedMsg); !ok {
		t.Fatalf("msg = %T, want PlanApprovedMsg", msg)
	}

	select {
	case evt := <-sub.C:
		// Verify event was emitted with correct type
		if evt.EventType != string(domain.EventPlanApproved) {
			t.Fatalf("event type = %q, want %q", evt.EventType, domain.EventPlanApproved)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for plan.approved event")
	}
}

func TestOverrideAcceptCmd_EmitsWorkItemCompletedEvent(t *testing.T) {
	workItemRepo := &cmdWorkItemRepo{items: map[string]domain.Session{
		"wi-1": {ID: "wi-1", WorkspaceID: "ws-1", ExternalID: "gh:issue:acme/rocket#42", Source: "github", SourceScope: domain.ScopeIssues, SourceItemIDs: []string{"acme/rocket#42"}, State: domain.SessionReviewing},
	}}
	bus := event.NewBus(event.BusConfig{})
	workItemSvc := service.NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: workItemRepo}}, bus)
	defer bus.Close()

	sub, err := bus.Subscribe("complete-test", string(domain.EventWorkItemCompleted))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	msg := OverrideAcceptCmd(workItemSvc, "wi-1")()
	if done, ok := msg.(ActionDoneMsg); !ok || done.Message != "Work item accepted" {
		t.Fatalf("msg = %#v, want successful ActionDoneMsg", msg)
	}

	// EventWorkItemCompleted is emitted by CompleteWorkItem → Transition → emitStateChange.
	var payload struct {
		WorkItemID    string   `json:"work_item_id"`
		ExternalID    string   `json:"external_id"`
		SourceItemIDs []string `json:"source_item_ids"`
		ExternalIDs   []string `json:"external_ids"`
	}
	for {
		select {
		case evt, ok := <-sub.C:
			if !ok {
				t.Fatal("channel closed before finding expected event")
			}
			if err := json.Unmarshal([]byte(evt.Payload), &payload); err != nil {
				t.Fatalf("Unmarshal payload: %v", err)
			}
			if payload.WorkItemID == "wi-1" {
				goto found
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for work_item.completed event")
		}
	}
found:
	if payload.ExternalID != "gh:issue:acme/rocket#42" {
		t.Fatalf("external_id = %q, want gh:issue:acme/rocket#42", payload.ExternalID)
	}
	// Verify the work item is now completed
	updatedItem, err := workItemRepo.Get(context.Background(), "wi-1")
	if err != nil {
		t.Fatalf("Get work item: %v", err)
	}
	if updatedItem.State != domain.SessionCompleted {
		t.Fatalf("work item state = %q, want completed", updatedItem.State)
	}
}

func createReviewContextRepo(t *testing.T, branch string) string {
	t.Helper()
	repoDir := t.TempDir()
	runCmdGit(t, repoDir, "init")
	runCmdGit(t, repoDir, "config", "user.email", "test@example.com")
	runCmdGit(t, repoDir, "config", "user.name", "Test User")
	runCmdGit(t, repoDir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	runCmdGit(t, repoDir, "add", "README.md")
	runCmdGit(t, repoDir, "commit", "-m", "initial commit")
	runCmdGit(t, repoDir, "checkout", "-b", branch)
	runCmdGit(t, repoDir, "remote", "add", "origin", "git@github.com:acme/rocket.git")

	return repoDir
}

func runCmdGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v (output: %s)", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
}

func TestReconcileOrphanedTasksCmd_InterruptsOrphanedRunningSession(t *testing.T) {
	t.Parallel()

	orphanedSessionID := "sess-orphaned"
	ownedSessionID := "sess-owned"
	workspaceID := "ws-1"
	currentInstanceID := "inst-current"

	sessionRepo := &cmdSessionRepo{sessions: map[string]domain.AgentSession{
		orphanedSessionID: {
			ID:          orphanedSessionID,
			WorkspaceID: workspaceID,
			Status:      domain.AgentSessionRunning,
			// OwnerInstanceID is nil — orphaned
		},
		ownedSessionID: {
			ID:              ownedSessionID,
			WorkspaceID:     workspaceID,
			Status:          domain.AgentSessionRunning,
			OwnerInstanceID: &currentInstanceID,
		},
	}}
	instanceRepo := &stubInstanceRepo{byID: map[string]domain.SubstrateInstance{
		currentInstanceID: {
			ID:            currentInstanceID,
			WorkspaceID:   workspaceID,
			LastHeartbeat: time.Now(),
		},
	}}

	sessionSvc := service.NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: sessionRepo}}, NewNoopPublisher())
	instanceSvc := service.NewInstanceService(repository.NoopTransacter{Res: repository.Resources{Instances: instanceRepo}})

	cmd := ReconcileOrphanedTasksCmd(sessionSvc, instanceSvc, workspaceID, currentInstanceID)
	if cmd == nil {
		t.Fatal("ReconcileOrphanedTasksCmd must return a cmd")
	}
	cmd() // Execute the command synchronously.

	// Orphaned session should now be interrupted.
	orphaned := sessionRepo.sessions[orphanedSessionID]
	if orphaned.Status != domain.AgentSessionInterrupted {
		t.Fatalf("orphaned session status = %q, want %q", orphaned.Status, domain.AgentSessionInterrupted)
	}

	// Owned session should remain running.
	owned := sessionRepo.sessions[ownedSessionID]
	if owned.Status != domain.AgentSessionRunning {
		t.Fatalf("owned session status = %q, want %q (should not be interrupted)", owned.Status, domain.AgentSessionRunning)
	}
}
