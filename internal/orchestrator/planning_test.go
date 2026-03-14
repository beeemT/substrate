package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/domain"
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

func TestRunPlanningWithCorrectionLoopIncludesSessionDraftPathInUserPrompt(t *testing.T) {
	templates, err := NewPlanningTemplates()
	if err != nil {
		t.Fatalf("NewPlanningTemplates(): %v", err)
	}

	tmpDir := t.TempDir()
	draftPath := filepath.Join(tmpDir, ".substrate", "sessions", "plan-123", "plan-draft.md")
	harness := &planningHarnessSpy{planText: "```substrate-plan\nexecution_groups:\n  - [repo-a]\n```\n\n## Orchestration\nKeep repo-a isolated.\n\n## SubPlan: repo-a\nUpdate the planner.\n"}
	svc := &PlanningService{
		cfg:       &PlanningConfig{MaxParseRetries: 0, SessionTimeout: time.Minute},
		harness:   harness,
		templates: templates,
	}

	_, _, _, planErr := svc.runPlanningWithCorrectionLoop(context.Background(), &domain.PlanningContext{
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

func TestRunPlanningWithCorrectionLoopWaitsForPlannerDoneBeforeAcceptingDraft(t *testing.T) {
	templates, err := NewPlanningTemplates()
	if err != nil {
		t.Fatalf("NewPlanningTemplates(): %v", err)
	}

	tmpDir := t.TempDir()
	draftPath := filepath.Join(tmpDir, ".substrate", "sessions", "plan-123", "plan-draft.md")
	intermediatePlan := "```substrate-plan\nexecution_groups:\n  - [repo-a]\n```\n\n## Orchestration\nStop after the first draft.\n\n## SubPlan: repo-a\nInitial sketch.\n"
	finalPlan := "```substrate-plan\nexecution_groups:\n  - [repo-a]\n```\n\n## Orchestration\nFinish the full orchestration after reviewing the workspace.\n\n## SubPlan: repo-a\nFinal implementation details.\n"
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
	finalPlan := "```substrate-plan\nexecution_groups:\n  - [repo-a]\n```\n\n## Orchestration\nRecovered after the planner was asked to rewrite the missing draft.\n\n## SubPlan: repo-a\nProduce the final repo plan.\n"
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
