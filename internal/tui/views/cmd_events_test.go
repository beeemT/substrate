package views

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/orchestrator"
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

func (r *cmdSessionRepo) ListActiveChildrenByParentID(_ context.Context, parentID string) ([]domain.AgentSession, error) {
	result := make([]domain.AgentSession, 0, len(r.sessions))
	for _, session := range r.sessions {
		if session.ParentAgentSessionID != parentID {
			continue
		}
		switch session.Status {
		case domain.AgentSessionPending, domain.AgentSessionRunning, domain.AgentSessionWaitingForAnswer:
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

type resumeAllHarness struct {
	starts    []adapter.SessionOpts
	failSubID map[string]bool
}

func (h *resumeAllHarness) Name() string          { return "resume-test" }
func (h *resumeAllHarness) SupportsCompact() bool { return false }
func (h *resumeAllHarness) StartSession(_ context.Context, opts adapter.SessionOpts) (adapter.AgentSession, error) {
	h.starts = append(h.starts, opts)
	if h.failSubID[opts.SubPlanID] {
		return nil, errors.New("start failed")
	}
	return resumeAllAgentSession{id: opts.SessionID}, nil
}

type resumeAllAgentSession struct{ id string }

func (s resumeAllAgentSession) ID() string                                { return s.id }
func (s resumeAllAgentSession) Wait(context.Context) error                { return nil }
func (s resumeAllAgentSession) Events() <-chan adapter.AgentEvent         { return nil }
func (s resumeAllAgentSession) SendMessage(context.Context, string) error { return nil }
func (s resumeAllAgentSession) Steer(context.Context, string) error {
	return adapter.ErrSteerNotSupported
}

func (s resumeAllAgentSession) SendAnswer(context.Context, string) error {
	return adapter.ErrSendAnswerNotSupported
}
func (s resumeAllAgentSession) Abort(context.Context) error   { return nil }
func (s resumeAllAgentSession) ResumeInfo() map[string]string { return nil }
func (s resumeAllAgentSession) Compact(context.Context) error { return adapter.ErrCompactNotSupported }
func (s resumeAllAgentSession) Done() <-chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}

type resumeAllWorkspaceRepo struct{}

func (resumeAllWorkspaceRepo) Get(context.Context, string) (domain.Workspace, error) {
	return domain.Workspace{}, repository.ErrNotFound
}
func (resumeAllWorkspaceRepo) Create(context.Context, domain.Workspace) error { return nil }
func (resumeAllWorkspaceRepo) Update(context.Context, domain.Workspace) error { return nil }
func (resumeAllWorkspaceRepo) Delete(context.Context, string) error           { return nil }

type resumeAllCmdFixture struct {
	sessionRepo    *cmdSessionRepo
	workItemRepo   *cmdWorkItemRepo
	harness        *resumeAllHarness
	foremanHarness *resumeAllHarness
	workItemSvc    *service.SessionService
	planningSvc    *orchestrator.PlanningService
	resumption     *orchestrator.Resumption
	sessionSvc     *service.AgentSessionService
	planSvc        *service.PlanService
	registry       orchestrator.SessionRegistry
}

func newResumeAllCmdFixture(t *testing.T, sessions []domain.AgentSession) resumeAllCmdFixture {
	t.Helper()

	sessionRepo := &cmdSessionRepo{sessions: make(map[string]domain.AgentSession)}
	subPlans := make(map[string]domain.TaskPlan)
	for _, session := range sessions {
		sessionRepo.sessions[session.ID] = session
		if session.SubPlanID != "" {
			subPlans[session.SubPlanID] = domain.TaskPlan{ID: session.SubPlanID, PlanID: "plan-1", RepositoryName: session.RepositoryName, Content: "Do work"}
		}
	}
	workItemRepo := &cmdWorkItemRepo{items: map[string]domain.Session{
		"wi-1": {ID: "wi-1", WorkspaceID: "ws-1", State: domain.SessionImplementing, Title: "Work item", Source: "manual"},
	}}
	planRepo := &cmdPlanRepo{plans: map[string]domain.Plan{"plan-1": {ID: "plan-1", WorkItemID: "wi-1", Status: domain.PlanApproved}}}
	subPlanRepo := &cmdSubPlanRepo{subPlans: subPlans}
	publisher := NewNoopPublisher()
	workItemSvc := service.NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: workItemRepo}}, publisher)
	sessionSvc := service.NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: sessionRepo}}, publisher)
	planSvc := service.NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: planRepo, SubPlans: subPlanRepo, Sessions: workItemRepo}}, publisher)
	registry := orchestrator.NewSessionRegistry()
	harness := &resumeAllHarness{failSubID: make(map[string]bool)}
	resumption := orchestrator.NewResumption(harness, sessionSvc, planSvc, workItemSvc, publisher, registry, nil)
	workspaceSvc := service.NewWorkspaceService(repository.NoopTransacter{Res: repository.Resources{Workspaces: resumeAllWorkspaceRepo{}}}, publisher)
	planningSvc, err := orchestrator.NewPlanningService(orchestrator.DefaultPlanningConfig(), nil, nil, harness, planSvc, workItemSvc, sessionSvc, publisher, workspaceSvc, registry, nil, &config.Config{})
	if err != nil {
		t.Fatalf("NewPlanningService: %v", err)
	}

	return resumeAllCmdFixture{sessionRepo: sessionRepo, workItemRepo: workItemRepo, harness: harness, workItemSvc: workItemSvc, planningSvc: planningSvc, resumption: resumption, sessionSvc: sessionSvc, planSvc: planSvc, registry: registry}
}

func resumeAllSession(id, subPlanID string, status domain.AgentSessionStatus) domain.AgentSession {
	return domain.AgentSession{ID: id, WorkItemID: "wi-1", WorkspaceID: "ws-1", Kind: domain.AgentSessionKindImplementation, SubPlanID: subPlanID, RepositoryName: subPlanID + "-repo", WorktreePath: "/tmp/" + subPlanID, Status: status}
}

func TestResumeAllSessionsForWorkItemCmd_ResumesAll(t *testing.T) {
	fix := newResumeAllCmdFixture(t, []domain.AgentSession{
		resumeAllSession("sess-1", "sp-1", domain.AgentSessionInterrupted),
		resumeAllSession("sess-2", "sp-2", domain.AgentSessionInterrupted),
	})

	msg := ResumeAllSessionsForWorkItemCmd(context.Background(), fix.workItemSvc, fix.planningSvc, fix.resumption, fix.sessionSvc, fix.planSvc, nil, "wi-1", "inst-1")()
	resumed, ok := msg.(SessionResumedMsg)
	if !ok {
		t.Fatalf("msg = %T, want SessionResumedMsg", msg)
	}
	if resumed.Message != "Resumed 2 tasks" {
		t.Fatalf("message = %q, want %q", resumed.Message, "Resumed 2 tasks")
	}
	if len(fix.harness.starts) != 2 {
		t.Fatalf("starts = %d, want 2", len(fix.harness.starts))
	}
}

func TestResumeAllSessionsForWorkItemCmd_SkipsSuperseded(t *testing.T) {
	fix := newResumeAllCmdFixture(t, []domain.AgentSession{
		resumeAllSession("sess-old", "sp-1", domain.AgentSessionInterrupted),
		resumeAllSession("sess-new", "sp-1", domain.AgentSessionRunning),
	})

	msg := ResumeAllSessionsForWorkItemCmd(context.Background(), fix.workItemSvc, fix.planningSvc, fix.resumption, fix.sessionSvc, fix.planSvc, nil, "wi-1", "inst-1")()
	resumed, ok := msg.(SessionResumedMsg)
	if !ok {
		t.Fatalf("msg = %T, want SessionResumedMsg", msg)
	}
	if resumed.Message != "No resumable tasks" {
		t.Fatalf("message = %q, want %q", resumed.Message, "No resumable tasks")
	}
	if len(fix.harness.starts) != 0 {
		t.Fatalf("starts = %d, want 0", len(fix.harness.starts))
	}
}

func TestResumeAllSessionsForWorkItemCmd_SecondDispatchDoesNotDuplicateResume(t *testing.T) {
	fix := newResumeAllCmdFixture(t, []domain.AgentSession{
		resumeAllSession("sess-old", "sp-1", domain.AgentSessionInterrupted),
	})

	first := ResumeAllSessionsForWorkItemCmd(context.Background(), fix.workItemSvc, fix.planningSvc, fix.resumption, fix.sessionSvc, fix.planSvc, nil, "wi-1", "inst-1")()
	if _, ok := first.(SessionResumedMsg); !ok {
		t.Fatalf("first msg = %T, want SessionResumedMsg", first)
	}
	second := ResumeAllSessionsForWorkItemCmd(context.Background(), fix.workItemSvc, fix.planningSvc, fix.resumption, fix.sessionSvc, fix.planSvc, nil, "wi-1", "inst-1")()
	resumed, ok := second.(SessionResumedMsg)
	if !ok {
		t.Fatalf("second msg = %T, want SessionResumedMsg", second)
	}
	if resumed.Message != "No resumable tasks" {
		t.Fatalf("second message = %q, want %q", resumed.Message, "No resumable tasks")
	}
	if len(fix.harness.starts) != 1 {
		t.Fatalf("starts = %d, want 1", len(fix.harness.starts))
	}
}

func TestResumeAllSessionsForWorkItemCmd_ResumesInterruptedLeafWithCompletedParent(t *testing.T) {
	impl := resumeAllSession("impl-completed", "sp-1", domain.AgentSessionCompleted)
	review := resumeAllSession("review-interrupted", "sp-1", domain.AgentSessionInterrupted)
	review.Kind = domain.AgentSessionKindReview
	review.ParentAgentSessionID = impl.ID
	fix := newResumeAllCmdFixture(t, []domain.AgentSession{impl, review})

	msg := ResumeAllSessionsForWorkItemCmd(context.Background(), fix.workItemSvc, fix.planningSvc, fix.resumption, fix.sessionSvc, fix.planSvc, nil, "wi-1", "inst-1")()
	resumed, ok := msg.(SessionResumedMsg)
	if !ok {
		t.Fatalf("msg = %T, want SessionResumedMsg", msg)
	}
	if resumed.Message != "Resumed 1 task" {
		t.Fatalf("message = %q, want %q", resumed.Message, "Resumed 1 task")
	}
	if len(fix.harness.starts) != 1 {
		t.Fatalf("starts = %d, want 1", len(fix.harness.starts))
	}
}

func TestResumeAllSessionsForWorkItemCmd_PlanningTriggersRestart(t *testing.T) {
	planning := domain.AgentSession{ID: "sess-plan", WorkItemID: "wi-1", WorkspaceID: "ws-1", Kind: domain.AgentSessionKindPlanning, Status: domain.AgentSessionInterrupted}
	implementation := resumeAllSession("sess-impl", "sp-1", domain.AgentSessionInterrupted)
	fix := newResumeAllCmdFixture(t, []domain.AgentSession{planning, implementation})
	item := fix.workItemRepo.items["wi-1"]
	item.State = domain.SessionPlanning
	fix.workItemRepo.items["wi-1"] = item

	msg := ResumeAllSessionsForWorkItemCmd(context.Background(), fix.workItemSvc, fix.planningSvc, fix.resumption, fix.sessionSvc, fix.planSvc, nil, "wi-1", "inst-1")()
	if _, ok := msg.(ErrMsg); !ok {
		t.Fatalf("msg = %T, want ErrMsg from planning restart attempt", msg)
	}
	if len(fix.harness.starts) != 0 {
		t.Fatalf("implementation resume starts = %d, want 0", len(fix.harness.starts))
	}
}

func TestResumeAllSessionsForWorkItemCmd_ReportsCorrectCount(t *testing.T) {
	fix := newResumeAllCmdFixture(t, []domain.AgentSession{
		resumeAllSession("sess-1", "sp-1", domain.AgentSessionInterrupted),
		resumeAllSession("sess-2", "sp-2", domain.AgentSessionInterrupted),
		resumeAllSession("sess-3", "sp-3", domain.AgentSessionInterrupted),
	})

	msg := ResumeAllSessionsForWorkItemCmd(context.Background(), fix.workItemSvc, fix.planningSvc, fix.resumption, fix.sessionSvc, fix.planSvc, nil, "wi-1", "inst-1")()
	resumed, ok := msg.(SessionResumedMsg)
	if !ok {
		t.Fatalf("msg = %T, want SessionResumedMsg", msg)
	}
	if resumed.Message != "Resumed 3 tasks" {
		t.Fatalf("message = %q, want %q", resumed.Message, "Resumed 3 tasks")
	}
}

func TestResumeAllSessionsForWorkItemCmd_NoResumableTasks(t *testing.T) {
	fix := newResumeAllCmdFixture(t, []domain.AgentSession{
		resumeAllSession("sess-done", "sp-1", domain.AgentSessionCompleted),
		resumeAllSession("sess-running", "sp-2", domain.AgentSessionRunning),
	})

	msg := ResumeAllSessionsForWorkItemCmd(context.Background(), fix.workItemSvc, fix.planningSvc, fix.resumption, fix.sessionSvc, fix.planSvc, nil, "wi-1", "inst-1")()
	resumed, ok := msg.(SessionResumedMsg)
	if !ok {
		t.Fatalf("msg = %T, want SessionResumedMsg", msg)
	}
	if resumed.Message != "No resumable tasks" {
		t.Fatalf("message = %q, want %q", resumed.Message, "No resumable tasks")
	}
}

func TestResumeAllSessionsForWorkItemCmd_PartialFailure(t *testing.T) {
	fix := newResumeAllCmdFixture(t, []domain.AgentSession{
		resumeAllSession("sess-1", "sp-1", domain.AgentSessionInterrupted),
		resumeAllSession("sess-2", "sp-2", domain.AgentSessionInterrupted),
		resumeAllSession("sess-3", "sp-3", domain.AgentSessionInterrupted),
	})
	fix.harness.failSubID["sp-2"] = true
	fix.harness.failSubID["sp-3"] = true

	msg := ResumeAllSessionsForWorkItemCmd(context.Background(), fix.workItemSvc, fix.planningSvc, fix.resumption, fix.sessionSvc, fix.planSvc, nil, "wi-1", "inst-1")()
	resumed, ok := msg.(SessionResumedMsg)
	if !ok {
		t.Fatalf("msg = %T, want SessionResumedMsg", msg)
	}
	if resumed.Message != "Resumed 1 task" {
		t.Fatalf("message = %q, want %q", resumed.Message, "Resumed 1 task")
	}
	if len(fix.harness.starts) != 3 {
		t.Fatalf("starts = %d, want 3", len(fix.harness.starts))
	}
}

// TestResumeAllSessionsForWorkItemCmd_SkipsManualSessions verifies that the
// bulk-retry/resume path ignores manual sessions: an interrupted manual
// session must NOT be resumed, and a running manual session must NOT prevent
// an interrupted impl session in the same sub-plan from being resumed.
func TestResumeAllSessionsForWorkItemCmd_SkipsManualSessions(t *testing.T) {
	manualInterrupted := domain.AgentSession{
		ID:             "manual-interrupted",
		WorkItemID:     "wi-1",
		WorkspaceID:    "ws-1",
		Kind:           domain.AgentSessionKindManual,
		SubPlanID:      "sp-2",
		RepositoryName: "sp-2-repo",
		WorktreePath:   "/tmp/sp-2",
		Status:         domain.AgentSessionInterrupted,
	}
	manualRunning := domain.AgentSession{
		ID:             "manual-running",
		WorkItemID:     "wi-1",
		WorkspaceID:    "ws-1",
		Kind:           domain.AgentSessionKindManual,
		SubPlanID:      "sp-1",
		RepositoryName: "sp-1-repo",
		WorktreePath:   "/tmp/sp-1",
		Status:         domain.AgentSessionRunning,
	}
	implInterrupted := resumeAllSession("impl-interrupted", "sp-1", domain.AgentSessionInterrupted)

	fix := newResumeAllCmdFixture(t, []domain.AgentSession{manualInterrupted, manualRunning, implInterrupted})

	msg := ResumeAllSessionsForWorkItemCmd(context.Background(), fix.workItemSvc, fix.planningSvc, fix.resumption, fix.sessionSvc, fix.planSvc, nil, "wi-1", "inst-1")()
	resumed, ok := msg.(SessionResumedMsg)
	if !ok {
		t.Fatalf("msg = %T, want SessionResumedMsg", msg)
	}
	// Only the impl session should be resumed; manual sessions are out of scope.
	if resumed.Message != "Resumed 1 task" {
		t.Fatalf("message = %q, want %q", resumed.Message, "Resumed 1 task")
	}
	if len(fix.harness.starts) != 1 {
		t.Fatalf("starts = %d, want 1 (manual sessions must not be resumed)", len(fix.harness.starts))
	}
	for _, opts := range fix.harness.starts {
		if opts.SubPlanID != "sp-1" {
			t.Errorf("resumed sub-plan = %q, want sp-1 (impl) only", opts.SubPlanID)
		}
	}
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
