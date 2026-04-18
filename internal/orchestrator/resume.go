package orchestrator

import (
	"bufio"
	"context"
	"errors"
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
	"github.com/beeemT/substrate/internal/service"
)

const resumeLogLines = 50

// Resumption handles Resume and Abandon workflows for interrupted agent sessions.
type Resumption struct {
	harness    adapter.AgentHarness
	sessionSvc *service.TaskService
	planSvc    *service.PlanService
	eventBus   *event.Bus
	registry   *SessionRegistry
}

// NewResumption creates a Resumption instance.
func NewResumption(
	harness adapter.AgentHarness,
	sessionSvc *service.TaskService,
	planSvc *service.PlanService,
	eventBus *event.Bus,
	registry *SessionRegistry,
) *Resumption {
	return &Resumption{
		harness:    harness,
		sessionSvc: sessionSvc,
		planSvc:    planSvc,
		eventBus:   eventBus,
		registry:   registry,
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
		return ResumeSessionResult{}, fmt.Errorf(
			"cannot resume session %s: no sub-plan assigned (session may have been interrupted before planning completed)",
			interrupted.ID,
		)
	}
	subPlan, err := r.planSvc.GetSubPlan(ctx, interrupted.SubPlanID)
	if err != nil {
		return ResumeSessionResult{}, fmt.Errorf("get sub-plan %s for session %s: %w", interrupted.SubPlanID, interrupted.ID, err)
	}

	// Last N lines from the interrupted session's log give the agent orientation.
	// This is used as fallback context when native resume is unavailable.
	lastLines, err := readLastNLines(interrupted.ID, resumeLogLines)
	if err != nil {
		// Non-fatal: resume without prior log context.
		lastLines = "(prior session log unavailable)"
	}

	hasResume := len(interrupted.ResumeInfo) > 0
	systemPrompt := buildResumeSystemPrompt(subPlan, lastLines, loadGlobalCommitConfig())

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
	}
	if hasResume {
		opts.ResumeFromSessionID = interrupted.ID
		opts.ResumeInfo = interrupted.ResumeInfo
		// Harness resumes the native conversation; no synthetic prompt turn needed.
	} else {
		opts.UserPrompt = "You are continuing work on this sub-plan. The worktree may contain partial changes from a previous session. Run `git status` and `git diff` to understand current state, then continue implementing remaining items."
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

	// When resuming a native session, send orientation as a follow-up message
	// so the model sees it in conversation context rather than a stale system prompt.
	if hasResume {
		resumeMsg := "Your previous session was interrupted. The worktree may contain partial changes. Run `git status` and `git diff` to understand current state, then continue implementing remaining items."
		if sendErr := harnessSession.SendMessage(ctx, resumeMsg); sendErr != nil {
			slog.Warn("failed to send orientation to resumed session",
				"error", sendErr,
				"session_id", newSession.ID)
		}
	}

	if r.eventBus != nil {
		if pubErr := r.eventBus.Publish(ctx, domain.SystemEvent{
			ID:          domain.NewID(),
			EventType:   string(domain.EventAgentSessionResumed),
			WorkspaceID: interrupted.WorkspaceID,
			Payload: marshalJSONOrEmpty(string(domain.EventAgentSessionResumed), map[string]any{
				"old_session_id": interrupted.ID,
				"new_session_id": newSession.ID,
				"sub_plan_id":    interrupted.SubPlanID,
			}),
			CreatedAt: time.Now(),
		}); pubErr != nil {
			slog.Warn("failed to publish session resumed event", "error", pubErr, "new_session_id", newSession.ID)
		}
	}

	// Register session for steering; deregister when the session finishes.
	if r.registry != nil {
		r.registry.Register(newSession.ID, harnessSession)
		go func() {
			if waitErr := harnessSession.Wait(ctx); waitErr != nil {
				slog.Warn("harness session wait failed", "error", waitErr)
			}
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
func (r *Resumption) FollowUpSession(ctx context.Context, completedTask domain.Task, feedback, _ string) (FollowUpSessionResult, error) {
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

	systemPrompt := buildFollowUpSystemPrompt(subPlan, lastLines, feedback, loadGlobalCommitConfig())

	if err := r.sessionSvc.FollowUpRestart(ctx, completedTask.ID); err != nil {
		return FollowUpSessionResult{}, fmt.Errorf("restart task for follow-up: %w", err)
	}

	now := time.Now()
	completedTask.Status = domain.AgentSessionRunning
	completedTask.CompletedAt = nil
	completedTask.UpdatedAt = now

	opts := adapter.SessionOpts{
		SessionID:           completedTask.ID,
		Mode:                adapter.SessionModeAgent,
		WorkspaceID:         completedTask.WorkspaceID,
		SubPlanID:           completedTask.SubPlanID,
		Repository:          completedTask.RepositoryName,
		WorktreePath:        completedTask.WorktreePath,
		SystemPrompt:        systemPrompt,
		UserPrompt:          feedback,
		ResumeFromSessionID: completedTask.ID,
		ResumeInfo:          completedTask.ResumeInfo,
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

	systemPrompt := buildFollowUpSystemPrompt(subPlan, lastLines, feedback, loadGlobalCommitConfig())

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
		UserPrompt:   feedback,
	}
	opts.ResumeFromSessionID = failedTask.ID
	opts.ResumeInfo = failedTask.ResumeInfo

	harnessSession, err := r.harness.StartSession(ctx, opts)
	if err != nil {
		if failErr := failSessionDurably(ctx, r.sessionSvc, newTask.ID, nil); failErr != nil {
			slog.Warn("failed to fail follow-up session after harness start error",
				"error", failErr,
				"task_id", newTask.ID)
		}
		return FollowUpSessionResult{}, fmt.Errorf("start harness session: %w", err)
	}

	if r.eventBus != nil {
		if pubErr := r.eventBus.Publish(ctx, domain.SystemEvent{
			ID:          domain.NewID(),
			EventType:   string(domain.EventAgentSessionResumed),
			WorkspaceID: failedTask.WorkspaceID,
			Payload: marshalJSONOrEmpty(string(domain.EventAgentSessionResumed), map[string]any{
				"old_session_id": failedTask.ID,
				"new_session_id": newTask.ID,
				"sub_plan_id":    failedTask.SubPlanID,
			}),
			CreatedAt: time.Now(),
		}); pubErr != nil {
			slog.Warn("failed to publish session resumed event", "error", pubErr, "new_session_id", newTask.ID)
		}
	}

	if r.registry != nil {
		r.registry.Register(newTask.ID, harnessSession)
	}

	return FollowUpSessionResult{
		Task:           newTask,
		HarnessSession: harnessSession,
	}, nil
}

// buildFollowUpSystemPrompt constructs the system prompt for a follow-up agent session.
func buildFollowUpSystemPrompt(subPlan domain.TaskPlan, lastLogLines, feedback string, commitCfg adapter.CommitConfig) string {
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

	if section := buildCommitSection(commitCfg); section != "" {
		sb.WriteString("\n\n")
		sb.WriteString(section)
	}

	return sb.String()
}

// buildResumeSystemPrompt constructs the system prompt for a resumed agent session.
func buildResumeSystemPrompt(subPlan domain.TaskPlan, lastLogLines string, commitCfg adapter.CommitConfig) string {
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

	if section := buildCommitSection(commitCfg); section != "" {
		sb.WriteString("\n\n")
		sb.WriteString(section)
	}

	return sb.String()
}

// loadGlobalCommitConfig reads the global substrate config and returns the commit
// configuration. Returns a sensible default when the config cannot be loaded.
func loadGlobalCommitConfig() adapter.CommitConfig {
	globalDir, err := config.GlobalDir()
	if err != nil {
		return adapter.CommitConfig{Strategy: "semi-regular", MessageFormat: "ai-generated"}
	}
	cfg, err := config.Load(filepath.Join(globalDir, "config.yaml"))
	if err != nil {
		return adapter.CommitConfig{Strategy: "semi-regular", MessageFormat: "ai-generated"}
	}
	return adapter.CommitConfig{
		Strategy:        string(cfg.Commit.Strategy),
		MessageFormat:   string(cfg.Commit.MessageFormat),
		MessageTemplate: cfg.Commit.MessageTemplate,
	}
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

// WaitAndComplete blocks until harnessSession finishes, then transitions
// sessionID to the appropriate terminal state in the DB and saves resume info.
// It deregisters the session from the registry on completion. Callers must
// have previously registered the session via registry.Register.
// Intended for follow-up sessions where the TUI command goroutine drives the wait.
func (r *Resumption) WaitAndComplete(ctx context.Context, sessionID string, harnessSession adapter.AgentSession) {
	if r.registry != nil {
		defer r.registry.Deregister(sessionID)
	}

	waitErr := harnessSession.Wait(ctx)
	if waitErr != nil {
		if errors.Is(waitErr, context.Canceled) {
			if err := interruptSessionDurably(ctx, r.sessionSvc, sessionID); err != nil {
				slog.Warn("failed to interrupt follow-up session after cancellation",
					"error", err, "session_id", sessionID)
			}
		} else {
			if err := failSessionDurably(ctx, r.sessionSvc, sessionID, nil); err != nil {
				slog.Warn("failed to fail follow-up session",
					"error", err, "session_id", sessionID)
			}
		}
		return
	}

	if err := completeSessionDurably(ctx, r.sessionSvc, sessionID); err != nil {
		slog.Warn("failed to complete follow-up session",
			"error", err, "session_id", sessionID)
	}

	if info := harnessSession.ResumeInfo(); len(info) > 0 {
		if err := r.sessionSvc.UpdateResumeInfo(ctx, sessionID, info); err != nil {
			slog.Warn("failed to update resume info for follow-up session",
				"error", err, "session_id", sessionID)
		}
	}
}
