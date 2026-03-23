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
	registry    *SessionRegistry
}

// NewResumption creates a Resumption instance.
func NewResumption(
	harness adapter.AgentHarness,
	sessionSvc *service.TaskService,
	planSvc *service.PlanService,
	sessionRepo repository.TaskRepository,
	eventBus *event.Bus,
	registry *SessionRegistry,
) *Resumption {
	return &Resumption{
		harness:     harness,
		sessionSvc:  sessionSvc,
		planSvc:     planSvc,
		sessionRepo: sessionRepo,
		eventBus:    eventBus,
		registry:    registry,
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

	// Sub-plan provides the task assignment for the resumed agent. Without it the
	// agent has no direction — fail explicitly rather than burning tokens.
	if interrupted.SubPlanID == "" {
		return ResumeSessionResult{}, fmt.Errorf("cannot resume session %s: no sub-plan assigned (session may have been interrupted before planning completed)", interrupted.ID)
	}
	subPlan, err := r.planSvc.GetSubPlan(ctx, interrupted.SubPlanID)
	if err != nil {
		return ResumeSessionResult{}, fmt.Errorf("get sub-plan %s for session %s: %w", interrupted.SubPlanID, interrupted.ID, err)
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
		WorkItemID:      interrupted.WorkItemID,
		WorkspaceID:     interrupted.WorkspaceID,
		Phase:           domain.TaskPhaseImplementation,
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
			Payload: marshalJSONOrEmpty(string(domain.EventAgentSessionResumed), map[string]any{
				"old_session_id": interrupted.ID,
				"new_session_id": newSession.ID,
				"sub_plan_id":    interrupted.SubPlanID,
			}),
			CreatedAt: time.Now(),
		})
	}

	// Register session for steering; deregister when the session finishes.
	if r.registry != nil {
		r.registry.Register(newSession.ID, harnessSession)
		go func() {
			_ = harnessSession.Wait(ctx)
			r.registry.Deregister(newSession.ID)
		}()
	}

	// Transition the old interrupted session to failed now that its replacement
	// is durably running. This clears the interrupted action from the overview.
	if err := r.sessionSvc.Fail(ctx, interrupted.ID, nil); err != nil {
		slog.Warn("failed to fail superseded interrupted session",
			"old_session_id", interrupted.ID,
			"new_session_id", newSession.ID,
			"error", err)
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

// FollowUpSessionResult holds the outputs of a successful follow-up.
type FollowUpSessionResult struct {
	Task           domain.Task
	HarnessSession adapter.AgentSession
}

// FollowUpSession restarts a completed task with a user follow-up message.
// Reuses the same Task row (same ID/log file), transitions completed → running,
// and resumes the native OMP session if available.
func (r *Resumption) FollowUpSession(ctx context.Context, completedTask domain.Task, feedback, currentInstanceID string) (FollowUpSessionResult, error) {
	if completedTask.Status != domain.AgentSessionCompleted {
		return FollowUpSessionResult{}, fmt.Errorf("task %s is not completed (status: %s)", completedTask.ID, completedTask.Status)
	}

	if completedTask.SubPlanID == "" {
		return FollowUpSessionResult{}, fmt.Errorf("cannot follow up on session %s: no sub-plan assigned", completedTask.ID)
	}
	subPlan, err := r.planSvc.GetSubPlan(ctx, completedTask.SubPlanID)
	if err != nil {
		return FollowUpSessionResult{}, fmt.Errorf("get sub-plan %s for session %s: %w", completedTask.SubPlanID, completedTask.ID, err)
	}

	lastLines, err := readLastNLines(completedTask.ID, resumeLogLines)
	if err != nil {
		lastLines = "(prior session log unavailable)"
	}

	systemPrompt := buildFollowUpSystemPrompt(subPlan, lastLines, feedback)

	if err := r.sessionSvc.FollowUpRestart(ctx, completedTask.ID); err != nil {
		return FollowUpSessionResult{}, fmt.Errorf("restart task for follow-up: %w", err)
	}

	now := time.Now()
	completedTask.Status = domain.AgentSessionRunning
	completedTask.CompletedAt = nil
	completedTask.UpdatedAt = now

	opts := adapter.SessionOpts{
		SessionID:         completedTask.ID,
		Mode:              adapter.SessionModeAgent,
		WorkspaceID:       completedTask.WorkspaceID,
		SubPlanID:         completedTask.SubPlanID,
		Repository:        completedTask.RepositoryName,
		WorktreePath:      completedTask.WorktreePath,
		SystemPrompt:      systemPrompt,
		UserPrompt:        "Apply the requested changes to the codebase. Review the current worktree state with `git status` and `git diff` before making any changes.",
		ResumeSessionFile: completedTask.OmpSessionFile,
	}

	harnessSession, err := r.harness.StartSession(ctx, opts)
	if err != nil {
		if revertErr := completeSessionDurably(ctx, r.sessionSvc, completedTask.ID); revertErr != nil {
			slog.Warn("failed to revert task to completed after follow-up harness start error",
				"error", revertErr,
				"task_id", completedTask.ID)
		}
		return FollowUpSessionResult{}, fmt.Errorf("start harness session: %w", err)
	}

	if r.registry != nil {
		r.registry.Register(completedTask.ID, harnessSession)
		go func() {
			_ = harnessSession.Wait(ctx)
			r.registry.Deregister(completedTask.ID)
		}()
	}

	return FollowUpSessionResult{
		Task:           completedTask,
		HarnessSession: harnessSession,
	}, nil
}

// FollowUpFailedSession creates a new agent session to retry a failed task with user feedback.
// The failed task row is preserved as audit trail; a fresh Task row is created for the retry.
func (r *Resumption) FollowUpFailedSession(ctx context.Context, failedTask domain.Task, feedback, currentInstanceID string) (FollowUpSessionResult, error) {
	if failedTask.Status != domain.AgentSessionFailed {
		return FollowUpSessionResult{}, fmt.Errorf("task %s is not failed (status: %s)", failedTask.ID, failedTask.Status)
	}

	if failedTask.SubPlanID == "" {
		return FollowUpSessionResult{}, fmt.Errorf("cannot follow up on failed session %s: no sub-plan assigned", failedTask.ID)
	}
	subPlan, err := r.planSvc.GetSubPlan(ctx, failedTask.SubPlanID)
	if err != nil {
		return FollowUpSessionResult{}, fmt.Errorf("get sub-plan %s for failed session %s: %w", failedTask.SubPlanID, failedTask.ID, err)
	}

	lastLines, err := readLastNLines(failedTask.ID, resumeLogLines)
	if err != nil {
		lastLines = "(prior session log unavailable)"
	}

	systemPrompt := buildFollowUpSystemPrompt(subPlan, lastLines, feedback)

	// Create a fresh task row — the failed task is audit trail and must not be modified.
	now := time.Now()
	newTask := domain.Task{
		ID:              domain.NewID(),
		WorkItemID:      failedTask.WorkItemID,
		WorkspaceID:     failedTask.WorkspaceID,
		Phase:           domain.TaskPhaseImplementation,
		SubPlanID:       failedTask.SubPlanID,
		RepositoryName:  failedTask.RepositoryName,
		WorktreePath:    failedTask.WorktreePath,
		HarnessName:     r.harness.Name(),
		Status:          domain.AgentSessionPending,
		OwnerInstanceID: &currentInstanceID,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := r.sessionSvc.Create(ctx, newTask); err != nil {
		return FollowUpSessionResult{}, fmt.Errorf("create follow-up session for failed task: %w", err)
	}

	if err := r.sessionSvc.Start(ctx, newTask.ID); err != nil {
		deleteOrFailPendingSession(ctx, r.sessionSvc, newTask.ID, nil)
		return FollowUpSessionResult{}, fmt.Errorf("transition follow-up session to running: %w", err)
	}
	now = time.Now()
	newTask.Status = domain.AgentSessionRunning
	newTask.StartedAt = &now
	newTask.UpdatedAt = now

	opts := adapter.SessionOpts{
		SessionID:    newTask.ID,
		Mode:         adapter.SessionModeAgent,
		WorkspaceID:  failedTask.WorkspaceID,
		SubPlanID:    failedTask.SubPlanID,
		Repository:   failedTask.RepositoryName,
		WorktreePath: failedTask.WorktreePath,
		SystemPrompt: systemPrompt,
		UserPrompt:   "Apply the requested changes to the codebase. Review the current worktree state with `git status` and `git diff` before making any changes.",
	}
	if failedTask.OmpSessionFile != "" {
		opts.ResumeSessionFile = failedTask.OmpSessionFile
	}

	harnessSession, err := r.harness.StartSession(ctx, opts)
	if err != nil {
		if failErr := failSessionDurably(ctx, r.sessionSvc, newTask.ID, nil); failErr != nil {
			slog.Warn("failed to fail follow-up session after harness start error",
				"error", failErr,
				"task_id", newTask.ID)
		}
		return FollowUpSessionResult{}, fmt.Errorf("start harness session: %w", err)
	}

	// If resuming a native OMP session, deliver feedback as a follow-up message.
	if failedTask.OmpSessionFile != "" {
		if sendErr := harnessSession.SendMessage(ctx, feedback); sendErr != nil {
			slog.Warn("failed to send follow-up feedback to resumed session",
				"error", sendErr,
				"task_id", newTask.ID)
		}
	}

	if r.eventBus != nil {
		_ = r.eventBus.Publish(ctx, domain.SystemEvent{
			ID:          domain.NewID(),
			EventType:   string(domain.EventAgentSessionResumed),
			WorkspaceID: failedTask.WorkspaceID,
			Payload: marshalJSONOrEmpty(string(domain.EventAgentSessionResumed), map[string]any{
				"old_session_id": failedTask.ID,
				"new_session_id": newTask.ID,
				"sub_plan_id":    failedTask.SubPlanID,
			}),
			CreatedAt: time.Now(),
		})
	}

	if r.registry != nil {
		r.registry.Register(newTask.ID, harnessSession)
		go func() {
			_ = harnessSession.Wait(ctx)
			r.registry.Deregister(newTask.ID)
		}()
	}

	return FollowUpSessionResult{
		Task:           newTask,
		HarnessSession: harnessSession,
	}, nil
}

// buildFollowUpSystemPrompt constructs the system prompt for a follow-up agent session.
func buildFollowUpSystemPrompt(subPlan domain.TaskPlan, lastLogLines, feedback string) string {
	var sb strings.Builder
	sb.WriteString("## Sub-Plan\n\n")
	sb.WriteString(subPlan.Content)
	sb.WriteString("\n\n## Previous Session Summary\n\n")
	sb.WriteString("The previous agent session for this sub-plan has completed. The following is the last output:\n\n")
	sb.WriteString("```\n")
	sb.WriteString(lastLogLines)
	sb.WriteString("\n```\n\n")
	sb.WriteString("## Follow-Up Request\n\n")
	sb.WriteString(feedback)
	sb.WriteString("\n\n## Instructions\n\n")
	sb.WriteString("Review the current worktree state, then apply the requested changes.")
	return sb.String()
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
