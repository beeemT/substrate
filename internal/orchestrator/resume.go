package orchestrator

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
)

const resumeLogLines = 50

// Resumption handles Resume and Abandon workflows for interrupted agent sessions.
type Resumption struct {
	harness     adapter.AgentHarness
	sessionSvc  *service.TaskService
	planSvc     *service.PlanService
	sessionRepo repository.TaskRepository
	eventBus    *event.Bus
}

// NewResumption creates a Resumption instance.
func NewResumption(
	harness adapter.AgentHarness,
	sessionSvc *service.TaskService,
	planSvc *service.PlanService,
	sessionRepo repository.TaskRepository,
	eventBus *event.Bus,
) *Resumption {
	return &Resumption{
		harness:     harness,
		sessionSvc:  sessionSvc,
		planSvc:     planSvc,
		sessionRepo: sessionRepo,
		eventBus:    eventBus,
	}
}

// ResumeSessionResult holds the outputs of a successful resume.
type ResumeSessionResult struct {
	NewSession     domain.Task
	HarnessSession adapter.AgentSession
}

// ResumeSession starts a new agent session to continue work from an interrupted one.
// The interrupted session remains in the DB as interrupted (audit trail).
// The new session links to the same SubPlan and reuses the existing worktree.
// currentInstanceID becomes the owner of the new session.
func (r *Resumption) ResumeSession(ctx context.Context, interrupted domain.Task, currentInstanceID string) (ResumeSessionResult, error) {
	if interrupted.Status != domain.AgentSessionInterrupted {
		return ResumeSessionResult{}, fmt.Errorf("session %s is not interrupted (status: %s)", interrupted.ID, interrupted.Status)
	}

	// Claim the interrupted session for audit — marks who is resuming it.
	if err := r.sessionSvc.UpdateOwnerInstance(ctx, interrupted.ID, currentInstanceID); err != nil {
		return ResumeSessionResult{}, fmt.Errorf("claim interrupted session: %w", err)
	}

	// Sub-plan provides implementation context for the system prompt.
	subPlan, err := r.planSvc.GetSubPlan(ctx, interrupted.SubPlanID)
	if err != nil {
		return ResumeSessionResult{}, fmt.Errorf("get sub-plan: %w", err)
	}

	// Last N lines from the interrupted session's log give the agent orientation.
	lastLines, err := readLastNLines(interrupted.ID, resumeLogLines)
	if err != nil {
		// Non-fatal: resume without prior log context.
		lastLines = "(prior session log unavailable)"
	}

	systemPrompt := buildResumeSystemPrompt(subPlan, lastLines)

	// Create the new session record (pending).
	now := time.Now()
	newSession := domain.Task{
		ID:              domain.NewID(),
		WorkspaceID:     interrupted.WorkspaceID,
		SubPlanID:       interrupted.SubPlanID,
		RepositoryName:  interrupted.RepositoryName,
		WorktreePath:    interrupted.WorktreePath,
		HarnessName:     r.harness.Name(),
		Status:          domain.AgentSessionPending,
		OwnerInstanceID: &currentInstanceID,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := r.sessionSvc.Create(ctx, newSession); err != nil {
		return ResumeSessionResult{}, fmt.Errorf("create resumed session: %w", err)
	}

	// Transition the new session to running before launching the harness so the
	// durable session row never lags external state.
	if err := r.sessionSvc.Start(ctx, newSession.ID); err != nil {
		deleteOrFailPendingSession(ctx, r.sessionSvc, newSession.ID, nil)
		return ResumeSessionResult{}, fmt.Errorf("transition resumed session to running: %w", err)
	}
	now = time.Now()
	newSession.Status = domain.AgentSessionRunning
	newSession.StartedAt = &now
	newSession.UpdatedAt = now

	// Start the harness session once the row is durably running.
	opts := adapter.SessionOpts{
		SessionID:    newSession.ID,
		Mode:         adapter.SessionModeAgent,
		WorkspaceID:  interrupted.WorkspaceID,
		SubPlanID:    interrupted.SubPlanID,
		Repository:   interrupted.RepositoryName,
		WorktreePath: interrupted.WorktreePath,
		SystemPrompt: systemPrompt,
		UserPrompt:   "You are continuing work on this sub-plan. The worktree may contain partial changes from a previous session. Run `git status` and `git diff` to understand current state, then continue implementing remaining items.",
	}
	harnessSession, err := r.harness.StartSession(ctx, opts)
	if err != nil {
		if failErr := failSessionDurably(ctx, r.sessionSvc, newSession.ID, nil); failErr != nil {
			slog.Warn("failed to fail resumed session after harness start error",
				"error", failErr,
				"session_id", newSession.ID)
		}
		return ResumeSessionResult{}, fmt.Errorf("start harness session: %w", err)
	}

	if r.eventBus != nil {
		_ = r.eventBus.Publish(ctx, domain.SystemEvent{
			ID:          domain.NewID(),
			EventType:   string(domain.EventAgentSessionResumed),
			WorkspaceID: interrupted.WorkspaceID,
			Payload: marshalJSONOrEmpty(map[string]any{
				"old_session_id": interrupted.ID,
				"new_session_id": newSession.ID,
				"sub_plan_id":    interrupted.SubPlanID,
			}),
			CreatedAt: time.Now(),
		})
	}

	return ResumeSessionResult{
		NewSession:     newSession,
		HarnessSession: harnessSession,
	}, nil
}

// AbandonSession transitions an interrupted session to failed. Terminal operation.
func (r *Resumption) AbandonSession(ctx context.Context, id string) error {
	session, err := r.sessionSvc.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}
	if session.Status != domain.AgentSessionInterrupted {
		return fmt.Errorf("can only abandon interrupted sessions (status: %s)", session.Status)
	}
	return r.sessionSvc.Fail(ctx, id, nil)
}

// buildResumeSystemPrompt constructs the system prompt for a resumed agent session.
func buildResumeSystemPrompt(subPlan domain.TaskPlan, lastLogLines string) string {
	var sb strings.Builder
	sb.WriteString("## Sub-Plan\n\n")
	sb.WriteString(subPlan.Content)
	sb.WriteString("\n\n## Resume Context\n\n")
	sb.WriteString("Your previous session was interrupted. The following is the last output from that session:\n\n")
	sb.WriteString("```\n")
	sb.WriteString(lastLogLines)
	sb.WriteString("\n```\n\n")
	sb.WriteString("## Instructions\n\n")
	sb.WriteString("You are continuing work on this sub-plan. The worktree may contain partial changes.\n")
	sb.WriteString("Run `git status` and `git diff` to understand current state, then continue implementing remaining items.")
	return sb.String()
}

// readLastNLines reads the last n lines from a session's JSONL log file.
// Lines are returned as-is (raw JSONL), suitable for embedding in a system prompt.
func readLastNLines(sessionID string, n int) (string, error) {
	globalDir, err := config.GlobalDir()
	if err != nil {
		return "", fmt.Errorf("get global dir: %w", err)
	}
	logPath := filepath.Join(globalDir, "sessions", sessionID+".log")
	f, err := os.Open(logPath)
	if err != nil {
		return "", fmt.Errorf("open log: %w", err)
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scan log: %w", err)
	}

	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n"), nil
}
