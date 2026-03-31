package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/gitwork"
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

func (h *planningHarnessSpy) SupportsCompact() bool { return true }
func (h *planningHarnessSpy) Name() string          { return "planning-spy" }

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

func (s *planningHarnessSession) SendAnswer(context.Context, string) error {
	return adapter.ErrSendAnswerNotSupported
}
func (s *planningHarnessSession) Compact(context.Context) error { return nil }
func (s *planningHarnessSession) ResumeInfo() map[string]string { return nil }

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

func (h *scriptedPlanningHarness) SupportsCompact() bool { return true }
func (h *scriptedPlanningHarness) Name() string          { return "planning-scripted" }

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

func (s *scriptedPlanningSession) SendAnswer(context.Context, string) error {
	return adapter.ErrSendAnswerNotSupported
}
func (s *scriptedPlanningSession) Compact(context.Context) error { return nil }
func (s *scriptedPlanningSession) ResumeInfo() map[string]string { return nil }

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
			events := make(chan adapter.AgentEvent, 2)
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

	if _, _, _, planErr := svc.runPlanningWithCorrectionLoop(context.Background(), &domain.PlanningContext{
		WorkItem: domain.WorkItemSnapshot{
			Title:      "Native resume test",
			ExternalID: "ISSUE-999",
		},
		Repos: []domain.RepoPointer{{
			Name:     "repo-a",
			Language: "go",
			MainDir:  filepath.Join(tmpDir, "repo-a", "main"),
		}},
		SessionID:        "plan-rev-1",
		SessionDraftPath: draftPath,
		RevisionFeedback: feedback,
		PriorResumeInfo:  map[string]string{"omp_session_file": resumeFile},
	}, "workspace-1"); planErr != nil {
		t.Fatalf("runPlanningWithCorrectionLoop(): %v", planErr)
	}

	// UserPrompt must be empty when resuming natively.
	if harness.lastOpts.UserPrompt != "" {
		t.Errorf("UserPrompt = %q, want empty when PriorResumeInfo is set", harness.lastOpts.UserPrompt)
	}

	// PriorResumeInfo must be forwarded to the harness via ResumeInfo.
	if harness.lastOpts.ResumeInfo["omp_session_file"] != resumeFile {
		t.Errorf("ResumeInfo[omp_session_file] = %q, want %q", harness.lastOpts.ResumeInfo["omp_session_file"], resumeFile)
	}

	// Feedback must be sent as a follow-up message.
	if len(capturedMessages) == 0 {
		t.Fatal("expected at least one SendMessage call with revision feedback")
	}
	if !strings.Contains(capturedMessages[0], feedback) {
		t.Errorf("first SendMessage = %q, want it to contain feedback %q", capturedMessages[0], feedback)
	}
}

// planningHarnessSessionWithResumeInfo wraps planningHarnessSession and returns
// OMP-specific resume data via the generic ResumeInfo() method.
type planningHarnessSessionWithResumeInfo struct {
	*planningHarnessSession
	resumeInfo map[string]string
}

func (s *planningHarnessSessionWithResumeInfo) ResumeInfo() map[string]string { return s.resumeInfo }

func TestRunPlanningWithCorrectionLoop_StoresResumeInfoOnSuccess(t *testing.T) {
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
	// Seed the planning session record so UpdateResumeInfo can find it.
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
		sessionSvc: service.NewTaskService(repository.NoopTransacter{Res: repository.Resources{Tasks: sessionRepo}}),
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
			return &planningHarnessSessionWithResumeInfo{
				planningHarnessSession: base,
				resumeInfo: map[string]string{
					"omp_session_file": wantOmpFile,
					"omp_session_id":   wantOmpID,
				},
			}, nil
		},
	}
	svc.harness = ompHarness

	if _, _, _, planErr := svc.runPlanningWithCorrectionLoop(context.Background(), &domain.PlanningContext{
		WorkItem: domain.WorkItemSnapshot{Title: "OMP file capture test", ExternalID: "ISSUE-42"},
		Repos: []domain.RepoPointer{{
			Name:     "repo-a",
			Language: "go",
			MainDir:  filepath.Join(tmpDir, "repo-a", "main"),
		}},
		SessionID:        "plan-omp-1",
		SessionDraftPath: draftPath,
	}, "ws-1"); planErr != nil {
		t.Fatalf("runPlanningWithCorrectionLoop(): %v", planErr)
	}

	// Verify that resume info was persisted on the task record.
	updated, err := sessionRepo.Get(context.Background(), "plan-omp-1")
	if err != nil {
		t.Fatalf("Get session: %v", err)
	}
	if updated.ResumeInfo["omp_session_file"] != wantOmpFile {
		t.Errorf("ResumeInfo[omp_session_file] = %q, want %q", updated.ResumeInfo["omp_session_file"], wantOmpFile)
	}
	if updated.ResumeInfo["omp_session_id"] != wantOmpID {
		t.Errorf("ResumeInfo[omp_session_id] = %q, want %q", updated.ResumeInfo["omp_session_id"], wantOmpID)
	}
}

func validPlanningPlan(orchestration, goal string) string {
	return "```substrate-plan\nexecution_groups:\n  - [repo-a]\n```\n\n## Orchestration\n" + orchestration + "\n\n## SubPlan: repo-a\n" + validPlanningSubPlan(goal) + "\n"
}

func validPlanningSubPlan(goal string) string {
	return "### Goal\n" + goal + "\n\n### Scope\n- internal/repo_a.go\n\n### Changes\n1. Update implementation details.\n2. Add or refresh tests.\n3. Wire the affected callers.\n\n### Validation\n- go test ./...\n\n### Risks\n- Preserve current repo behavior.\n"
}

// uniqueWorkItemPlanRepo is an in-memory PlanRepository that enforces the
// plans.work_item_id UNIQUE constraint, reproducing the SQLite behaviour that
// caused the production failure. Using it in unit tests catches regressions
// where buildAndPersistPlan tries to INSERT without first clearing the slot.
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

// TestBuildAndPersistPlanAtomicReplace is the regression test for the UNIQUE
// constraint bug. It verifies that buildAndPersistPlan atomically removes the
// old plan and inserts the new one in the same transaction, so the constraint
// on plans.work_item_id is never violated.
func TestBuildAndPersistPlanAtomicReplace(t *testing.T) {
	const (
		workItemID = "wi-atomic-replace"
		oldPlanID  = "plan-old"
	)

	planRepo := newUniqueWorkItemPlanRepo()
	subPlanRepo := newMockSubPlanRepo()
	planSvc := service.NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: planRepo, SubPlans: subPlanRepo}})

	ctx := context.Background()

	// Seed a rejected plan that occupies the unique slot.
	oldPlan := domain.Plan{
		ID:         oldPlanID,
		WorkItemID: workItemID,
		Status:     domain.PlanRejected,
		Version:    1,
	}
	if err := planRepo.Create(ctx, oldPlan); err != nil {
		t.Fatalf("seed old plan: %v", err)
	}

	templates, err := NewPlanningTemplates()
	if err != nil {
		t.Fatalf("NewPlanningTemplates: %v", err)
	}

	svc := &PlanningService{
		planSvc:   planSvc,
		templates: templates,
		cfg:       DefaultPlanningConfig(),
	}

	rawOutput := domain.RawPlanOutput{
		Orchestration:   "## Goal\ntest\n",
		SubPlans:        []domain.RawSubPlan{{RepoName: "repo-a", Content: "do stuff"}},
		ExecutionGroups: [][]string{{"repo-a"}},
	}
	workItem := domain.Session{ID: workItemID}

	plan, subPlans, err := svc.buildAndPersistPlan(ctx, rawOutput, workItem, oldPlanID)
	if err != nil {
		t.Fatalf("buildAndPersistPlan with replacePlanID: %v", err)
	}

	// Old plan must be gone — the unique slot was released.
	if _, exists := planRepo.plans[oldPlanID]; exists {
		t.Error("old plan still present after atomic replace")
	}
	if planRepo.taken[workItemID] == oldPlanID {
		t.Error("unique slot still points to old plan after atomic replace")
	}

	// New plan must occupy the slot.
	if planRepo.taken[workItemID] != plan.ID {
		t.Errorf("unique slot = %q, want new plan ID %q", planRepo.taken[workItemID], plan.ID)
	}
	if plan.WorkItemID != workItemID {
		t.Errorf("plan.WorkItemID = %q, want %q", plan.WorkItemID, workItemID)
	}

	// Sub-plans must be created.
	if len(subPlans) != 1 {
		t.Errorf("len(subPlans) = %d, want 1", len(subPlans))
	} else if subPlans[0].RepositoryName != "repo-a" {
		t.Errorf("subPlans[0].RepositoryName = %q, want \"repo-a\"", subPlans[0].RepositoryName)
	}

	// A second call without replacePlanID must fail — the slot is taken.
	_, _, err2 := svc.buildAndPersistPlan(ctx, rawOutput, workItem, "")
	if err2 == nil {
		t.Error("expected UNIQUE constraint error when creating without replacePlanID; got nil")
	}
}

// TestPlan_ReplacesExistingRejectedPlanOnRestart is the integration regression test
// for the UNIQUE constraint failure on plans.work_item_id.
//
// Production failure sequence:
//  1. User clicked Reject on a plan in the plan_review UI (PlanRejectMsg path).
//     → plan row stays in DB with status=rejected
//     → work item transitions plan_review → ingested
//  2. User triggered re-planning (StartPlanMsg path).
//     → svc.Plan(ctx, workItemID) called without a replacePlanID
//     → agent ran successfully and wrote a valid plan draft
//     → buildAndPersistPlan tried to INSERT a new plan for the same work_item_id
//     → UNIQUE constraint fired because the rejected plan still occupied the slot
//     → session failed with exit_code=1; work item returned to ingested
//     Steps 1-2 repeated three times (sessions 01KMDVM6, 01KME1D5, 01KME3JY).
//
// Fix: Plan() now looks up any existing plan (regardless of status) and passes its ID as
// replacePlanID, so CreatePlanAtomic can delete it atomically before inserting.
func TestPlan_ReplacesExistingRejectedPlanOnRestart(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspace")

	// Fake gitwork repo: ScanWorkspace detects .bare/, buildRepoPointer calls
	// the fake git-work binary to get the main worktree path.
	repoName := "test-repo"
	repoDir := filepath.Join(workspaceRoot, repoName)
	mainDir := filepath.Join(repoDir, "main")
	for _, d := range []string{filepath.Join(repoDir, ".bare"), mainDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("create dir %s: %v", d, err)
		}
	}

	// Fake git-work binary: returns a JSON worktree list with mainDir as the main.
	wtJSON, _ := json.Marshal(map[string]any{
		"data": map[string]any{
			"worktrees": []map[string]any{
				{"dir": mainDir, "branch": "main", "current": true},
			},
		},
	})
	fakeGitWork := filepath.Join(tmpDir, "git-work")
	if err := os.WriteFile(
		fakeGitWork,
		fmt.Appendf(nil, "#!/bin/sh\nprintf '%%s\\n' %q\n", string(wtJSON)),
		0o755,
	); err != nil {
		t.Fatalf("write fake git-work: %v", err)
	}

	const (
		workItemID  = "wi-replan-test"
		workspaceID = "ws-replan-test"
		oldPlanID   = "plan-rejected-test"
	)

	// Plan repo with UNIQUE enforcement (mirrors SQLite behaviour).
	planRepo := newUniqueWorkItemPlanRepo()
	subPlanRepo := newMockSubPlanRepo()
	planSvc := service.NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: planRepo, SubPlans: subPlanRepo}})

	// Seed the rejected plan that occupies the unique slot.
	if err := planRepo.Create(context.Background(), domain.Plan{
		ID:         oldPlanID,
		WorkItemID: workItemID,
		Status:     domain.PlanRejected,
		Version:    1,
	}); err != nil {
		t.Fatalf("seed rejected plan: %v", err)
	}

	// Work item and workspace repos.
	workItemRepo := &planTestWorkItemRepo{items: map[string]domain.Session{
		workItemID: {ID: workItemID, WorkspaceID: workspaceID, State: domain.SessionIngested},
	}}
	workItemSvc := service.NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: workItemRepo}})

	workspaceRepo := &planTestWorkspaceRepo{workspaces: map[string]domain.Workspace{
		workspaceID: {ID: workspaceID, RootPath: workspaceRoot},
	}}
	workspaceSvc := service.NewWorkspaceService(repository.NoopTransacter{Res: repository.Resources{Workspaces: workspaceRepo}})

	sessionRepo := newMockSessionRepo()
	sessionSvc := service.NewTaskService(repository.NoopTransacter{Res: repository.Resources{Tasks: sessionRepo}})

	eventRepo := &planTestEventRepo{}
	eventSvc := service.NewEventService(repository.NoopTransacter{Res: repository.Resources{Events: eventRepo}})

	// Discoverer uses the fake binary so buildRepoPointer succeeds for test-repo.
	gitClient := gitwork.NewClient(fakeGitWork)
	globalCfg := &config.Config{}
	globalCfg.Plan.MaxParseRetries = ptrInt(0)
	discoverer := NewDiscoverer(gitClient, globalCfg)

	// Harness writes a valid plan referencing repoName.
	planText := validPlanningPlanWithRepo(repoName, "Keep test-repo isolated.", "Implement the resolver.")
	harness := &planningHarnessSpy{planText: planText}

	pcfg := DefaultPlanningConfig()
	pcfg.MaxParseRetries = 0
	svc, err := NewPlanningService(pcfg, discoverer, gitClient, harness, planSvc, workItemSvc, sessionSvc, eventSvc, workspaceSvc, nil, globalCfg)
	if err != nil {
		t.Fatalf("NewPlanningService: %v", err)
	}

	// Call Plan() — should atomically replace the rejected plan.
	result, planErr := svc.Plan(context.Background(), workItemID)
	if planErr != nil {
		t.Fatalf("Plan(): %v", planErr)
	}
	if result == nil {
		t.Fatal("Plan() returned nil result")
	}

	// Old plan must be gone: the unique slot was released and re-taken.
	if _, exists := planRepo.plans[oldPlanID]; exists {
		t.Error("old rejected plan still present after Plan(): replacePlanID was not passed to buildAndPersistPlan")
	}

	// New plan must now occupy the slot.
	if planRepo.taken[workItemID] == oldPlanID || planRepo.taken[workItemID] == "" {
		t.Errorf("unique slot not updated: still points to %q", planRepo.taken[workItemID])
	}
}

// TestPlan_ReplacesExistingApprovedPlanFromCompleted is the regression test for
// the UNIQUE constraint failure when a completed work item (which retains its
// approved plan) is re-entered into planning via the duplicate-session dialog.
//
// Production failure sequence:
//  1. Work item completed successfully — plan row has status=approved.
//  2. User picked the same item in the new-session browser.
//  3. Duplicate dialog fired; user chose "Start planning with existing item".
//  4. TUI called StartPlanningCmd → Plan() — completed→planning is a valid transition.
//  5. Plan() only checked for PlanRejected; the approved plan was invisible.
//  6. Agent succeeded, buildAndPersistPlan tried to INSERT →
//     UNIQUE constraint failed: plans.work_item_id.
//
// Fix: Plan() now passes any existing plan's ID as replacePlanID, regardless of status.
func TestPlan_ReplacesExistingApprovedPlanFromCompleted(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspace")

	// Fake gitwork repo.
	repoName := "test-repo"
	repoDir := filepath.Join(workspaceRoot, repoName)
	mainDir := filepath.Join(repoDir, "main")
	for _, d := range []string{filepath.Join(repoDir, ".bare"), mainDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("create dir %s: %v", d, err)
		}
	}

	wtJSON, _ := json.Marshal(map[string]any{
		"data": map[string]any{
			"worktrees": []map[string]any{
				{"dir": mainDir, "branch": "main", "current": true},
			},
		},
	})
	fakeGitWork := filepath.Join(tmpDir, "git-work")
	if err := os.WriteFile(
		fakeGitWork,
		fmt.Appendf(nil, "#!/bin/sh\nprintf '%%s\\n' %q\n", string(wtJSON)),
		0o755,
	); err != nil {
		t.Fatalf("write fake git-work: %v", err)
	}

	const (
		workItemID  = "wi-completed-replan"
		workspaceID = "ws-completed-replan"
		oldPlanID   = "plan-approved-completed"
	)

	planRepo := newUniqueWorkItemPlanRepo()
	subPlanRepo := newMockSubPlanRepo()
	planSvc := service.NewPlanService(repository.NoopTransacter{Res: repository.Resources{Plans: planRepo, SubPlans: subPlanRepo}})

	// Seed an approved plan — the artifact of a completed work item.
	if err := planRepo.Create(context.Background(), domain.Plan{
		ID:         oldPlanID,
		WorkItemID: workItemID,
		Status:     domain.PlanApproved,
		Version:    1,
	}); err != nil {
		t.Fatalf("seed approved plan: %v", err)
	}

	// Work item starts in completed state — the transition table allows completed→planning.
	workItemRepo := &planTestWorkItemRepo{items: map[string]domain.Session{
		workItemID: {ID: workItemID, WorkspaceID: workspaceID, State: domain.SessionCompleted},
	}}
	workItemSvc := service.NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: workItemRepo}})

	workspaceRepo := &planTestWorkspaceRepo{workspaces: map[string]domain.Workspace{
		workspaceID: {ID: workspaceID, RootPath: workspaceRoot},
	}}
	workspaceSvc := service.NewWorkspaceService(repository.NoopTransacter{Res: repository.Resources{Workspaces: workspaceRepo}})

	sessionRepo := newMockSessionRepo()
	sessionSvc := service.NewTaskService(repository.NoopTransacter{Res: repository.Resources{Tasks: sessionRepo}})

	eventRepo := &planTestEventRepo{}
	eventSvc := service.NewEventService(repository.NoopTransacter{Res: repository.Resources{Events: eventRepo}})

	gitClient := gitwork.NewClient(fakeGitWork)
	globalCfg := &config.Config{}
	globalCfg.Plan.MaxParseRetries = ptrInt(0)
	discoverer := NewDiscoverer(gitClient, globalCfg)

	planText := validPlanningPlanWithRepo(repoName, "Re-plan after completion.", "Implement the next iteration.")
	harness := &planningHarnessSpy{planText: planText}

	pcfg := DefaultPlanningConfig()
	pcfg.MaxParseRetries = 0
	svc, err := NewPlanningService(pcfg, discoverer, gitClient, harness, planSvc, workItemSvc, sessionSvc, eventSvc, workspaceSvc, nil, globalCfg)
	if err != nil {
		t.Fatalf("NewPlanningService: %v", err)
	}

	result, planErr := svc.Plan(context.Background(), workItemID)
	if planErr != nil {
		t.Fatalf("Plan(): %v", planErr)
	}
	if result == nil {
		t.Fatal("Plan() returned nil result")
	}

	// Old approved plan must be gone.
	if _, exists := planRepo.plans[oldPlanID]; exists {
		t.Error("old approved plan still present after Plan(): replacePlanID was not passed to buildAndPersistPlan")
	}

	// New plan must occupy the unique slot.
	if planRepo.taken[workItemID] == oldPlanID || planRepo.taken[workItemID] == "" {
		t.Errorf("unique slot not updated: still points to %q", planRepo.taken[workItemID])
	}
}

// validPlanningPlanWithRepo returns a complete valid substrate plan for the named repo.
func validPlanningPlanWithRepo(repoName, orchestration, goal string) string {
	return "```substrate-plan\nexecution_groups:\n  - [" + repoName + "]\n```\n\n## Orchestration\n" +
		orchestration + "\n\n## SubPlan: " + repoName + "\n" + validPlanningSubPlan(goal) + "\n"
}

// planTestWorkItemRepo is a minimal in-memory SessionRepository for planning tests.
type planTestWorkItemRepo struct {
	items map[string]domain.Session
}

func (r *planTestWorkItemRepo) Get(_ context.Context, id string) (domain.Session, error) {
	if item, ok := r.items[id]; ok {
		return item, nil
	}
	return domain.Session{}, repository.ErrNotFound
}

func (r *planTestWorkItemRepo) List(_ context.Context, _ repository.SessionFilter) ([]domain.Session, error) {
	return nil, nil
}

func (r *planTestWorkItemRepo) Create(_ context.Context, item domain.Session) error {
	r.items[item.ID] = item
	return nil
}

func (r *planTestWorkItemRepo) Update(_ context.Context, item domain.Session) error {
	r.items[item.ID] = item
	return nil
}

func (r *planTestWorkItemRepo) Delete(_ context.Context, id string) error {
	delete(r.items, id)
	return nil
}

// planTestWorkspaceRepo is a minimal in-memory WorkspaceRepository for planning tests.
type planTestWorkspaceRepo struct {
	workspaces map[string]domain.Workspace
}

func (r *planTestWorkspaceRepo) Get(_ context.Context, id string) (domain.Workspace, error) {
	if ws, ok := r.workspaces[id]; ok {
		return ws, nil
	}
	return domain.Workspace{}, repository.ErrNotFound
}

func (r *planTestWorkspaceRepo) Create(_ context.Context, ws domain.Workspace) error {
	r.workspaces[ws.ID] = ws
	return nil
}

func (r *planTestWorkspaceRepo) Update(_ context.Context, ws domain.Workspace) error {
	r.workspaces[ws.ID] = ws
	return nil
}

func (r *planTestWorkspaceRepo) Delete(_ context.Context, id string) error {
	delete(r.workspaces, id)
	return nil
}

// planTestEventRepo is a no-op EventRepository for planning tests.
type planTestEventRepo struct{}

func (r *planTestEventRepo) Create(_ context.Context, _ domain.SystemEvent) error { return nil }
func (r *planTestEventRepo) ListByType(_ context.Context, _ string, _ int) ([]domain.SystemEvent, error) {
	return nil, nil
}

func (r *planTestEventRepo) ListByWorkspaceID(_ context.Context, _ string, _ int) ([]domain.SystemEvent, error) {
	return nil, nil
}
