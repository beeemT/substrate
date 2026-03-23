package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
)

func TestRenderPlanningPromptIncludesSessionDraftPath(t *testing.T) {
	templates, err := NewPlanningTemplates()
	if err != nil {
		t.Fatalf("NewPlanningTemplates(): %v", err)
	}

	svc := &PlanningService{templates: templates}
	draftPath := "/tmp/workspace/.substrate/sessions/plan-123/plan-draft.md"
	prompt, err := svc.renderPlanningPrompt(&domain.PlanningContext{
		WorkItem: domain.WorkItemSnapshot{
			Title:       "Investigate planning failure",
			ExternalID:  "ISSUE-123",
			Description: "Reproduce and fix planning prompt bugs.",
		},
		Repos: []domain.RepoPointer{{
			Name:     "repo-a",
			Language: "go",
			MainDir:  "/tmp/workspace/repo-a/main",
		}},
		SessionDraftPath: draftPath,
	})
	if err != nil {
		t.Fatalf("renderPlanningPrompt(): %v", err)
	}

	if !strings.Contains(prompt, draftPath) {
		t.Fatalf("planning prompt missing draft path %q\nprompt:\n%s", draftPath, prompt)
	}
}

type planningHarnessSpy struct {
	lastOpts adapter.SessionOpts
	planText string
}

func (h *planningHarnessSpy) Name() string { return "planning-spy" }

func (h *planningHarnessSpy) StartSession(_ context.Context, opts adapter.SessionOpts) (adapter.AgentSession, error) {
	h.lastOpts = opts
	if err := os.MkdirAll(filepath.Dir(opts.DraftPath), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(opts.DraftPath, []byte(h.planText), 0o644); err != nil {
		return nil, err
	}
	events := make(chan adapter.AgentEvent, 1)
	events <- adapter.AgentEvent{Type: "done", Timestamp: time.Now()}
	close(events)

	return &planningHarnessSession{id: opts.SessionID, events: events}, nil
}

type planningHarnessSession struct {
	id     string
	events <-chan adapter.AgentEvent
}

func (s *planningHarnessSession) ID() string                        { return s.id }
func (s *planningHarnessSession) Wait(context.Context) error        { return nil }
func (s *planningHarnessSession) Events() <-chan adapter.AgentEvent { return s.events }
func (s *planningHarnessSession) SendMessage(context.Context, string) error {
	return nil
}
func (s *planningHarnessSession) Abort(context.Context) error { return nil }
func (s *planningHarnessSession) Steer(context.Context, string) error {
	return adapter.ErrSteerNotSupported
}

func TestRunPlanningWithCorrectionLoopIncludesSessionDraftPathInUserPrompt(t *testing.T) {
	templates, err := NewPlanningTemplates()
	if err != nil {
		t.Fatalf("NewPlanningTemplates(): %v", err)
	}

	tmpDir := t.TempDir()
	draftPath := filepath.Join(tmpDir, ".substrate", "sessions", "plan-123", "plan-draft.md")
	harness := &planningHarnessSpy{planText: validPlanningPlan("Keep repo-a isolated.", "Update the planner.")}
	svc := &PlanningService{
		cfg:       &PlanningConfig{MaxParseRetries: 0, SessionTimeout: time.Minute},
		harness:   harness,
		templates: templates,
	}

	rawContent, retries, warnings, planErr := svc.runPlanningWithCorrectionLoop(context.Background(), &domain.PlanningContext{
		WorkItem: domain.WorkItemSnapshot{
			Title:       "Investigate planning failure",
			ExternalID:  "ISSUE-123",
			Description: "Reproduce and fix planning prompt bugs.",
		},
		Repos: []domain.RepoPointer{{
			Name:     "repo-a",
			Language: "go",
			MainDir:  filepath.Join(tmpDir, "repo-a", "main"),
		}},
		SessionID:        "plan-123",
		SessionDraftPath: draftPath,
	}, "workspace-123")
	_ = rawContent
	_ = retries
	_ = warnings
	if planErr != nil {
		t.Fatalf("runPlanningWithCorrectionLoop(): %v", planErr)
	}

	if harness.lastOpts.DraftPath != draftPath {
		t.Fatalf("StartSession DraftPath = %q, want %q", harness.lastOpts.DraftPath, draftPath)
	}
	if !strings.Contains(harness.lastOpts.UserPrompt, draftPath) {
		t.Fatalf("user prompt missing draft path %q\nprompt:\n%s", draftPath, harness.lastOpts.UserPrompt)
	}
	if !strings.Contains(harness.lastOpts.SystemPrompt, draftPath) {
		t.Fatalf("system prompt missing draft path %q\nprompt:\n%s", draftPath, harness.lastOpts.SystemPrompt)
	}
}

type scriptedPlanningHarness struct {
	lastOpts     adapter.SessionOpts
	startSession func(adapter.SessionOpts) (adapter.AgentSession, error)
}

func (h *scriptedPlanningHarness) Name() string { return "planning-scripted" }

func (h *scriptedPlanningHarness) StartSession(_ context.Context, opts adapter.SessionOpts) (adapter.AgentSession, error) {
	h.lastOpts = opts

	return h.startSession(opts)
}

type scriptedPlanningSession struct {
	id          string
	events      chan adapter.AgentEvent
	sendMessage func(context.Context, string) error
}

func (s *scriptedPlanningSession) ID() string                        { return s.id }
func (s *scriptedPlanningSession) Wait(context.Context) error        { return nil }
func (s *scriptedPlanningSession) Events() <-chan adapter.AgentEvent { return s.events }
func (s *scriptedPlanningSession) SendMessage(ctx context.Context, msg string) error {
	if s.sendMessage != nil {
		return s.sendMessage(ctx, msg)
	}

	return nil
}
func (s *scriptedPlanningSession) Abort(context.Context) error { return nil }
func (s *scriptedPlanningSession) Steer(context.Context, string) error {
	return adapter.ErrSteerNotSupported
}

func TestRunPlanningWithCorrectionLoopWaitsForPlannerDoneBeforeAcceptingDraft(t *testing.T) {
	templates, err := NewPlanningTemplates()
	if err != nil {
		t.Fatalf("NewPlanningTemplates(): %v", err)
	}

	tmpDir := t.TempDir()
	draftPath := filepath.Join(tmpDir, ".substrate", "sessions", "plan-123", "plan-draft.md")
	intermediatePlan := validPlanningPlan("Stop after the first draft.", "Initial sketch.")
	finalPlan := validPlanningPlan("Finish the full orchestration after reviewing the workspace.", "Final implementation details.")
	writeErrCh := make(chan error, 1)
	harness := &scriptedPlanningHarness{
		startSession: func(opts adapter.SessionOpts) (adapter.AgentSession, error) {
			if err := os.MkdirAll(filepath.Dir(opts.DraftPath), 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(opts.DraftPath, []byte(intermediatePlan), 0o644); err != nil {
				return nil, err
			}
			events := make(chan adapter.AgentEvent, 2)
			go func() {
				events <- adapter.AgentEvent{Type: "started", Timestamp: time.Now()}
				time.Sleep(20 * time.Millisecond)
				if err := os.WriteFile(opts.DraftPath, []byte(finalPlan), 0o644); err != nil {
					writeErrCh <- err

					return
				}
				events <- adapter.AgentEvent{Type: "done", Timestamp: time.Now()}
			}()

			return &scriptedPlanningSession{id: opts.SessionID, events: events}, nil
		},
	}
	svc := &PlanningService{
		cfg:       &PlanningConfig{MaxParseRetries: 0, SessionTimeout: time.Minute},
		harness:   harness,
		templates: templates,
	}

	rawContent, retries, _, planErr := svc.runPlanningWithCorrectionLoop(context.Background(), &domain.PlanningContext{
		WorkItem: domain.WorkItemSnapshot{
			Title:       "Investigate planning completion boundary",
			ExternalID:  "ISSUE-456",
			Description: "Ensure progressive plan writes do not finalize early.",
		},
		Repos: []domain.RepoPointer{{
			Name:     "repo-a",
			Language: "go",
			MainDir:  filepath.Join(tmpDir, "repo-a", "main"),
		}},
		SessionID:        "plan-123",
		SessionDraftPath: draftPath,
	}, "workspace-123")
	if planErr != nil {
		t.Fatalf("runPlanningWithCorrectionLoop(): %v", planErr)
	}
	select {
	case err := <-writeErrCh:
		t.Fatalf("write final draft: %v", err)
	default:
	}
	if retries != 0 {
		t.Fatalf("retries = %d, want 0", retries)
	}
	if rawContent != finalPlan {
		t.Fatalf("runPlanningWithCorrectionLoop() returned intermediate draft:\n%s", rawContent)
	}
}

func TestRunPlanningWithCorrectionLoopRequestsRewriteAfterPlannerDoneWithoutDraft(t *testing.T) {
	templates, err := NewPlanningTemplates()
	if err != nil {
		t.Fatalf("NewPlanningTemplates(): %v", err)
	}

	tmpDir := t.TempDir()
	draftPath := filepath.Join(tmpDir, ".substrate", "sessions", "plan-456", "plan-draft.md")
	finalPlan := validPlanningPlan("Recovered after the planner was asked to rewrite the missing draft.", "Produce the final repo plan.")
	correctionMessages := make([]string, 0, 1)
	harness := &scriptedPlanningHarness{
		startSession: func(opts adapter.SessionOpts) (adapter.AgentSession, error) {
			events := make(chan adapter.AgentEvent, 2)
			session := &scriptedPlanningSession{
				id:     opts.SessionID,
				events: events,
			}
			session.sendMessage = func(_ context.Context, msg string) error {
				correctionMessages = append(correctionMessages, msg)
				if err := os.MkdirAll(filepath.Dir(opts.DraftPath), 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(opts.DraftPath, []byte(finalPlan), 0o644); err != nil {
					return err
				}
				events <- adapter.AgentEvent{Type: "done", Timestamp: time.Now()}

				return nil
			}
			events <- adapter.AgentEvent{Type: "done", Timestamp: time.Now()}

			return session, nil
		},
	}
	svc := &PlanningService{
		cfg:       &PlanningConfig{MaxParseRetries: 1, SessionTimeout: time.Minute},
		harness:   harness,
		templates: templates,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	rawContent, retries, _, planErr := svc.runPlanningWithCorrectionLoop(ctx, &domain.PlanningContext{
		WorkItem: domain.WorkItemSnapshot{
			Title:       "Recover missing planning draft",
			ExternalID:  "ISSUE-789",
			Description: "Rewrite the draft after the first turn completed without a file.",
		},
		Repos: []domain.RepoPointer{{
			Name:     "repo-a",
			Language: "go",
			MainDir:  filepath.Join(tmpDir, "repo-a", "main"),
		}},
		SessionID:        "plan-456",
		SessionDraftPath: draftPath,
	}, "workspace-123")
	if planErr != nil {
		t.Fatalf("runPlanningWithCorrectionLoop(): %v", planErr)
	}
	if retries != 1 {
		t.Fatalf("retries = %d, want 1", retries)
	}
	if len(correctionMessages) != 1 {
		t.Fatalf("correction messages = %d, want 1", len(correctionMessages))
	}
	if !strings.Contains(correctionMessages[0], draftPath) {
		t.Fatalf("correction message missing draft path %q\nmessage:\n%s", draftPath, correctionMessages[0])
	}
	if rawContent != finalPlan {
		t.Fatalf("runPlanningWithCorrectionLoop() returned %q, want final rewritten plan", rawContent)
	}
}

func TestRunPlanningWithCorrectionLoop_NativeResume_ClearsUserPromptAndSendsFeedbackAsMessage(t *testing.T) {
	templates, err := NewPlanningTemplates()
	if err != nil {
		t.Fatalf("NewPlanningTemplates(): %v", err)
	}

	tmpDir := t.TempDir()
	draftPath := filepath.Join(tmpDir, ".substrate", "sessions", "plan-rev-1", "plan-draft.md")
	feedback := "Add error-handling section to repo-a sub-plan."
	const resumeFile = "/some/prior-session.jsonl"

	var capturedMessages []string
	harness := &scriptedPlanningHarness{
		startSession: func(opts adapter.SessionOpts) (adapter.AgentSession, error) {
			if err := os.MkdirAll(filepath.Dir(opts.DraftPath), 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(opts.DraftPath, []byte(validPlanningPlan("Revised orchestration.", "Add error handling.")), 0o644); err != nil {
				return nil, err
			}
			events := make(chan adapter.AgentEvent, 1)
			sess := &scriptedPlanningSession{
				id:     opts.SessionID,
				events: events,
			}
			sess.sendMessage = func(_ context.Context, msg string) error {
				capturedMessages = append(capturedMessages, msg)
				events <- adapter.AgentEvent{Type: "done", Timestamp: time.Now()}
				return nil
			}
			// Emit done after a tick to simulate the model completing a turn after SendMessage.
			// Without resume, the harness emits done immediately; with resume the trigger
			// message is what starts the turn.
			go func() { events <- adapter.AgentEvent{Type: "done", Timestamp: time.Now()} }()
			return sess, nil
		},
	}

	svc := &PlanningService{
		cfg:       &PlanningConfig{MaxParseRetries: 0, SessionTimeout: time.Minute},
		harness:   harness,
		templates: templates,
	}

	_, _, _, planErr := svc.runPlanningWithCorrectionLoop(context.Background(), &domain.PlanningContext{
		WorkItem: domain.WorkItemSnapshot{
			Title:      "Native resume test",
			ExternalID: "ISSUE-999",
		},
		Repos: []domain.RepoPointer{{
			Name:     "repo-a",
			Language: "go",
			MainDir:  filepath.Join(tmpDir, "repo-a", "main"),
		}},
		SessionID:         "plan-rev-1",
		SessionDraftPath:  draftPath,
		RevisionFeedback:  feedback,
		ResumeSessionFile: resumeFile,
	}, "workspace-1")
	if planErr != nil {
		t.Fatalf("runPlanningWithCorrectionLoop(): %v", planErr)
	}

	// UserPrompt must be empty when resuming natively.
	if harness.lastOpts.UserPrompt != "" {
		t.Errorf("UserPrompt = %q, want empty when ResumeSessionFile is set", harness.lastOpts.UserPrompt)
	}

	// ResumeSessionFile must be forwarded to the harness.
	if harness.lastOpts.ResumeSessionFile != resumeFile {
		t.Errorf("ResumeSessionFile = %q, want %q", harness.lastOpts.ResumeSessionFile, resumeFile)
	}

	// Feedback must be sent as a follow-up message.
	if len(capturedMessages) == 0 {
		t.Fatal("expected at least one SendMessage call with revision feedback")
	}
	if !strings.Contains(capturedMessages[0], feedback) {
		t.Errorf("first SendMessage = %q, want it to contain feedback %q", capturedMessages[0], feedback)
	}
}

// planningHarnessSessionWithOmpFile wraps planningHarnessSession and exposes OMP metadata,
// enabling tests to verify that storeOmpSessionFile is called after a successful plan.
type planningHarnessSessionWithOmpFile struct {
	*planningHarnessSession
	ompFile string
	ompID   string
}

func (s *planningHarnessSessionWithOmpFile) OmpSessionFile() string { return s.ompFile }
func (s *planningHarnessSessionWithOmpFile) OmpSessionID() string   { return s.ompID }

func TestRunPlanningWithCorrectionLoop_StoresOmpSessionFileOnSuccess(t *testing.T) {
	templates, err := NewPlanningTemplates()
	if err != nil {
		t.Fatalf("NewPlanningTemplates(): %v", err)
	}

	tmpDir := t.TempDir()
	draftPath := filepath.Join(tmpDir, ".substrate", "sessions", "plan-omp-1", "plan-draft.md")
	const wantOmpFile = "/home/user/.omp/sessions/abc123.jsonl"
	const wantOmpID = "abc123"

	planText := validPlanningPlan("Orchestration section.", "Implement feature.")
	harness := &planningHarnessSpy{planText: planText}

	sessionRepo := newMockSessionRepo()
	// Seed the planning session record so UpdateOmpSessionFile can find it.
	sessionRepo.sessions["plan-omp-1"] = domain.Task{
		ID:          "plan-omp-1",
		WorkItemID:  "wi-1",
		WorkspaceID: "ws-1",
		Phase:       domain.TaskPhasePlanning,
		HarnessName: "mock",
		Status:      domain.AgentSessionRunning,
	}

	svc := &PlanningService{
		cfg:        &PlanningConfig{MaxParseRetries: 0, SessionTimeout: time.Minute},
		harness:    harness,
		templates:  templates,
		sessionSvc: service.NewTaskService(sessionRepo),
	}

	// Override StartSession to return a session that exposes OMP metadata.
	ompHarness := &scriptedPlanningHarness{
		startSession: func(opts adapter.SessionOpts) (adapter.AgentSession, error) {
			if err := os.MkdirAll(filepath.Dir(opts.DraftPath), 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(opts.DraftPath, []byte(planText), 0o644); err != nil {
				return nil, err
			}
			events := make(chan adapter.AgentEvent, 1)
			events <- adapter.AgentEvent{Type: "done", Timestamp: time.Now()}
			close(events)
			base := &planningHarnessSession{id: opts.SessionID, events: events}
			return &planningHarnessSessionWithOmpFile{
				planningHarnessSession: base,
				ompFile:                wantOmpFile,
				ompID:                  wantOmpID,
			}, nil
		},
	}
	svc.harness = ompHarness

	_, _, _, planErr := svc.runPlanningWithCorrectionLoop(context.Background(), &domain.PlanningContext{
		WorkItem: domain.WorkItemSnapshot{Title: "OMP file capture test", ExternalID: "ISSUE-42"},
		Repos: []domain.RepoPointer{{
			Name:     "repo-a",
			Language: "go",
			MainDir:  filepath.Join(tmpDir, "repo-a", "main"),
		}},
		SessionID:        "plan-omp-1",
		SessionDraftPath: draftPath,
	}, "ws-1")
	if planErr != nil {
		t.Fatalf("runPlanningWithCorrectionLoop(): %v", planErr)
	}

	// Verify that the OMP session file was persisted on the task record.
	updated, err := sessionRepo.Get(context.Background(), "plan-omp-1")
	if err != nil {
		t.Fatalf("Get session: %v", err)
	}
	if updated.OmpSessionFile != wantOmpFile {
		t.Errorf("OmpSessionFile = %q, want %q", updated.OmpSessionFile, wantOmpFile)
	}
	if updated.OmpSessionID != wantOmpID {
		t.Errorf("OmpSessionID = %q, want %q", updated.OmpSessionID, wantOmpID)
	}
}

func validPlanningPlan(orchestration, goal string) string {
	return "```substrate-plan\nexecution_groups:\n  - [repo-a]\n```\n\n## Orchestration\n" + orchestration + "\n\n## SubPlan: repo-a\n" + validPlanningSubPlan(goal) + "\n"
}

func validPlanningSubPlan(goal string) string {
	return "### Goal\n" + goal + "\n\n### Scope\n- internal/repo_a.go\n\n### Changes\n1. Update implementation details.\n2. Add or refresh tests.\n3. Wire the affected callers.\n\n### Validation\n- go test ./...\n\n### Risks\n- Preserve current repo behavior.\n"
}

// ============================================================
// uniqueWorkItemPlanRepo: enforces work_item_id uniqueness like SQLite
// ============================================================

// uniqueWorkItemPlanRepo wraps the in-memory plan mock with a work_item_id UNIQUE
// constraint, reproducing the SQLite constraint that caused the production failure
// when PlanWithFeedback tried to INSERT a new plan without first deleting the old one.
type uniqueWorkItemPlanRepo struct {
	plans map[string]domain.Plan // planID → Plan
	taken map[string]string      // workItemID → planID holding the slot
}

func newUniqueWorkItemPlanRepo() *uniqueWorkItemPlanRepo {
	return &uniqueWorkItemPlanRepo{
		plans: make(map[string]domain.Plan),
		taken: make(map[string]string),
	}
}

func (r *uniqueWorkItemPlanRepo) Get(_ context.Context, id string) (domain.Plan, error) {
	if p, ok := r.plans[id]; ok {
		return p, nil
	}
	return domain.Plan{}, fmt.Errorf("plan not found: %s", id)
}

func (r *uniqueWorkItemPlanRepo) GetByWorkItemID(_ context.Context, workItemID string) (domain.Plan, error) {
	if id, ok := r.taken[workItemID]; ok {
		return r.plans[id], nil
	}
	return domain.Plan{}, fmt.Errorf("plan not found for work item: %s", workItemID)
}

func (r *uniqueWorkItemPlanRepo) Create(_ context.Context, plan domain.Plan) error {
	if existingID, ok := r.taken[plan.WorkItemID]; ok {
		return fmt.Errorf("UNIQUE constraint failed: plans.work_item_id (slot held by %s)", existingID)
	}
	r.plans[plan.ID] = plan
	r.taken[plan.WorkItemID] = plan.ID
	return nil
}

func (r *uniqueWorkItemPlanRepo) Update(_ context.Context, plan domain.Plan) error {
	r.plans[plan.ID] = plan
	return nil
}

func (r *uniqueWorkItemPlanRepo) Delete(_ context.Context, id string) error {
	if p, ok := r.plans[id]; ok {
		delete(r.taken, p.WorkItemID)
		delete(r.plans, id)
	}
	return nil
}

func (r *uniqueWorkItemPlanRepo) AppendFAQ(_ context.Context, _ domain.FAQEntry) error {
	return nil
}

// errorOnUpdateWorkItemRepo is a minimal work-item repo whose Update always fails.
// Get returns the pre-seeded item; all other methods are no-ops.
type errorOnUpdateWorkItemRepo struct {
	item domain.Session
}

func (r *errorOnUpdateWorkItemRepo) Get(_ context.Context, _ string) (domain.Session, error) {
	return r.item, nil
}

func (r *errorOnUpdateWorkItemRepo) List(_ context.Context, _ repository.SessionFilter) ([]domain.Session, error) {
	return nil, nil
}

func (r *errorOnUpdateWorkItemRepo) Create(_ context.Context, _ domain.Session) error {
	return nil
}

func (r *errorOnUpdateWorkItemRepo) Update(_ context.Context, _ domain.Session) error {
	return fmt.Errorf("simulated Update error: stopping before planRun")
}

func (r *errorOnUpdateWorkItemRepo) Delete(_ context.Context, _ string) error {
	return nil
}

// ============================================================
// TestPlanWithFeedbackDeletesOldPlanBeforeReplanning
// ============================================================

// TestPlanWithFeedbackDeletesOldPlanBeforeReplanning is a regression test for the
// bug where PlanWithFeedback failed with a SQLite UNIQUE constraint violation on
// plans.work_item_id. The old rejected plan was not deleted before planRun tried to
// INSERT a new one, hitting the unique constraint.
func TestPlanWithFeedbackDeletesOldPlanBeforeReplanning(t *testing.T) {
	const (
		workItemID = "wi-feedback-regression"
		oldPlanID  = "plan-feedback-old"
	)

	// Plan repo that enforces work_item_id uniqueness like SQLite.
	planRepo := newUniqueWorkItemPlanRepo()
	subPlanRepo := newMockSubPlanRepo()

	// Seed the old plan in pending_review state (the state when a user requests changes).
	oldPlan := domain.Plan{
		ID:         oldPlanID,
		WorkItemID: workItemID,
		Status:     domain.PlanPendingReview,
		Version:    1,
	}
	if err := planRepo.Create(context.Background(), oldPlan); err != nil {
		t.Fatalf("seed old plan: %v", err)
	}

	planSvc := service.NewPlanService(
		planRepo, subPlanRepo,
		service.NoopPlanTransacter{PlanRepo: planRepo, SubPlanRepo: subPlanRepo},
	)

	// workItemSvc.RejectPlan must see the work item in plan_review so the state
	// machine allows the transition. Update fails to stop execution before planRun.
	workItemSvc := service.NewSessionService(&errorOnUpdateWorkItemRepo{
		item: domain.Session{
			ID:          workItemID,
			WorkspaceID: "ws-1",
			State:       domain.SessionPlanReview,
		},
	})

	templates, err := NewPlanningTemplates()
	if err != nil {
		t.Fatalf("NewPlanningTemplates: %v", err)
	}

	svc := &PlanningService{
		cfg:         DefaultPlanningConfig(),
		templates:   templates,
		planSvc:     planSvc,
		planRepo:    planRepo,
		workItemSvc: workItemSvc,
		// sessionSvc nil: findPriorPlanningSessionFile is nil-safe, returns "".
		// workspaceSvc, discoverer, harness nil: planRun is never reached.
	}

	_, feedbackErr := svc.PlanWithFeedback(context.Background(), workItemID, oldPlanID, "fix the scope")

	// We expect exactly the error from workItemSvc.RejectPlan (Update fails).
	// If we get a different error (e.g., UNIQUE constraint), Delete was not called.
	if feedbackErr == nil {
		t.Fatal("expected error from workItemSvc.RejectPlan (Update), got nil")
	}
	if !strings.Contains(feedbackErr.Error(), "transition work item to planning") {
		t.Errorf("unexpected error path: %v\nwant error mentioning \"transition work item to planning\"", feedbackErr)
	}

	// The unique slot must be cleared: old plan deleted before workItemSvc.RejectPlan was called.
	if _, exists := planRepo.taken[workItemID]; exists {
		t.Error("PlanWithFeedback did not delete the old plan: unique slot still occupied")
	}
	if _, exists := planRepo.plans[oldPlanID]; exists {
		t.Error("PlanWithFeedback did not remove the old plan from the repo")
	}

	// A new Create for the same work item must succeed (the slot is clear).
	newPlan := domain.Plan{ID: "plan-feedback-new", WorkItemID: workItemID, Status: domain.PlanDraft}
	if err := planRepo.Create(context.Background(), newPlan); err != nil {
		t.Errorf("Create after Delete should succeed; got: %v", err)
	}
}
