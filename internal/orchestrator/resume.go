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
	harness        adapter.AgentHarness
	sessionSvc     *service.AgentSessionService
	planSvc        *service.PlanService
	eventBus       event.Publisher
	registry       *SessionRegistry
	questionRouter *QuestionRouter
}

// NewResumption creates a Resumption instance.
func NewResumption(
	harness adapter.AgentHarness,
	sessionSvc *service.AgentSessionService,
	planSvc *service.PlanService,
	eventBus event.Publisher,
	registry *SessionRegistry,
	questionRouter *QuestionRouter,
) *Resumption {
	return &Resumption{
		harness:        harness,
		sessionSvc:     sessionSvc,
		planSvc:        planSvc,
		eventBus:       eventBus,
		registry:       registry,
		questionRouter: questionRouter,
	}
}

// ResumeSessionResult holds the outputs of a successful resume.
type ResumeSessionResult struct {
	NewSession     domain.AgentSession
	HarnessSession adapter.AgentSession
}

// ResumeSession starts a new agent session to continue work from an interrupted one.
// The interrupted session remains in the DB as interrupted (audit trail).
// The new session links to the same SubPlan and reuses the existing worktree.
// currentInstanceID becomes the owner of the new session.
// EventAgentSessionResumed is emitted by AgentSessionService.Resume().
func (r *Resumption) ResumeSession(ctx context.Context, interrupted domain.AgentSession, currentInstanceID string) (ResumeSessionResult, error) {
	return r.ResumeSessionWithPrompt(ctx, interrupted, "", currentInstanceID)
}

// ResumeSessionWithPrompt starts a resumed agent session and optionally delivers
// operator guidance as the first resumed user message.
func (r *Resumption) ResumeSessionWithPrompt(ctx context.Context, interrupted domain.AgentSession, prompt, currentInstanceID string) (ResumeSessionResult, error) {
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

	// Create the new session, transition to running, and emit EventAgentSessionResumed
	// in a single service call. This ensures the event is always emitted with both
	// old and new session IDs.
	newSession, err := r.sessionSvc.Resume(ctx, interrupted, r.harness.Name(), &currentInstanceID)
	if err != nil {
		return ResumeSessionResult{}, fmt.Errorf("create resumed session: %w", err)
	}

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
	trimmedPrompt := strings.TrimSpace(prompt)
	if hasResume {
		opts.ResumeFromSessionID = interrupted.ID
		opts.ResumeInfo = interrupted.ResumeInfo
		// Harness resumes the native conversation; optional operator guidance is the first resumed user turn.
		opts.UserPrompt = trimmedPrompt
	} else if trimmedPrompt != "" {
		opts.UserPrompt = "You are continuing work on this sub-plan. The worktree may contain partial changes from a previous session. Run `git status` and `git diff` to understand current state, then continue implementing remaining items.\n\nOperator guidance:\n" + trimmedPrompt
	} else {
		opts.UserPrompt = "You are continuing work on this sub-plan. The worktree may contain partial changes from a previous session. Run `git status` and `git diff` to understand current state, then continue implementing remaining items."
	}
	harnessSession, err := r.harness.StartSession(ctx, opts)
	if err != nil {
		if failErr := failSessionDurably(ctx, r.sessionSvc, newSession.ID, nil); failErr != nil {
			slog.Warn("failed to fail resumed agent session after harness start error",
				"error", failErr,
				"agent_session_id", newSession.ID)
		}

		return ResumeSessionResult{}, fmt.Errorf("start harness session: %w", err)
	}

	// When resuming a native session, send orientation as a follow-up message
	// so the model sees it in conversation context rather than a stale system prompt.
	if hasResume {
		resumeMsg := "Your previous session was interrupted. The worktree may contain partial changes. Run `git status` and `git diff` to understand current state, then continue implementing remaining items."
		if sendErr := harnessSession.SendMessage(ctx, resumeMsg); sendErr != nil {
			slog.Warn("failed to send orientation to resumed agent session",
				"error", sendErr,
				"agent_session_id", newSession.ID)
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
		slog.Warn("failed to fail superseded interrupted agent session",
			"old_agent_session_id", interrupted.ID,
			"new_agent_session_id", newSession.ID,
			"error", err)
	}

	return ResumeSessionResult{
		NewSession:     newSession,
		HarnessSession: harnessSession,
	}, nil
}

// AbandonSession transitions an interrupted session to failed. Terminal operation.
func (r *Resumption) AbandonSession(ctx context.Context, id string) error {
	agentSession, err := r.sessionSvc.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("get agent session: %w", err)
	}
	if agentSession.Status != domain.AgentSessionInterrupted {
		return fmt.Errorf("can only abandon interrupted sessions (status: %s)", agentSession.Status)
	}

	return r.sessionSvc.Fail(ctx, id, nil)
}

// FollowUpSessionResult holds the outputs of a successful follow-up.
type FollowUpSessionResult struct {
	Session        domain.AgentSession
	HarnessSession adapter.AgentSession
}

// FollowUpSession restarts a completed session with a user follow-up message.
// Reuses the same AgentSession row (same ID/log file), transitions completed → running,
// and resumes the native OMP session if available.
func (r *Resumption) FollowUpSession(ctx context.Context, completedSession domain.AgentSession, feedback, currentInstanceID string) (FollowUpSessionResult, error) {
	if completedSession.Status != domain.AgentSessionCompleted {
		return FollowUpSessionResult{}, fmt.Errorf("session %s is not completed (status: %s)", completedSession.ID, completedSession.Status)
	}

	if completedSession.SubPlanID == "" {
		return FollowUpSessionResult{}, fmt.Errorf("cannot follow up on session %s: no sub-plan assigned", completedSession.ID)
	}
	subPlan, err := r.planSvc.GetSubPlan(ctx, completedSession.SubPlanID)
	if err != nil {
		return FollowUpSessionResult{}, fmt.Errorf("get sub-plan %s for session %s: %w", completedSession.SubPlanID, completedSession.ID, err)
	}

	lastLines, err := readLastNLines(completedSession.ID, resumeLogLines)
	if err != nil {
		lastLines = "(prior session log unavailable)"
	}

	systemPrompt := buildFollowUpSystemPrompt(subPlan, lastLines, feedback, loadGlobalCommitConfig())

	ownerInstanceID := (*string)(nil)
	if currentInstanceID != "" {
		ownerInstanceID = &currentInstanceID
	}
	if err := r.sessionSvc.FollowUpRestart(ctx, completedSession.ID, ownerInstanceID); err != nil {
		return FollowUpSessionResult{}, fmt.Errorf("restart task for follow-up: %w", err)
	}

	now := time.Now()
	completedSession.Status = domain.AgentSessionRunning
	completedSession.CompletedAt = nil
	completedSession.OwnerInstanceID = ownerInstanceID
	completedSession.UpdatedAt = now

	opts := adapter.SessionOpts{
		SessionID:           completedSession.ID,
		Mode:                adapter.SessionModeAgent,
		WorkspaceID:         completedSession.WorkspaceID,
		SubPlanID:           completedSession.SubPlanID,
		Repository:          completedSession.RepositoryName,
		WorktreePath:        completedSession.WorktreePath,
		SystemPrompt:        systemPrompt,
		UserPrompt:          feedback,
		ResumeFromSessionID: completedSession.ID,
		ResumeInfo:          completedSession.ResumeInfo,
	}

	harnessSession, err := r.harness.StartSession(ctx, opts)
	if err != nil {
		if revertErr := completeSessionDurably(ctx, r.sessionSvc, completedSession.ID); revertErr != nil {
			slog.Warn("failed to revert agent session to completed after follow-up harness start error",
				"error", revertErr,
				"agent_session_id", completedSession.ID)
		}
		return FollowUpSessionResult{}, fmt.Errorf("start harness session: %w", err)
	}

	if r.registry != nil {
		r.registry.Register(completedSession.ID, harnessSession)
	}

	return FollowUpSessionResult{
		Session:        completedSession,
		HarnessSession: harnessSession,
	}, nil
}

// FollowUpFailedSession creates a new agent session to retry a failed session with user feedback.
// The failed session row is preserved as audit trail; a fresh AgentSession row is created for the retry.
func (r *Resumption) FollowUpFailedSession(ctx context.Context, failedSession domain.AgentSession, feedback, currentInstanceID string) (FollowUpSessionResult, error) {
	if failedSession.Status != domain.AgentSessionFailed {
		return FollowUpSessionResult{}, fmt.Errorf("session %s is not failed (status: %s)", failedSession.ID, failedSession.Status)
	}

	if failedSession.SubPlanID == "" {
		return FollowUpSessionResult{}, fmt.Errorf("cannot follow up on failed session %s: no sub-plan assigned", failedSession.ID)
	}
	subPlan, err := r.planSvc.GetSubPlan(ctx, failedSession.SubPlanID)
	if err != nil {
		return FollowUpSessionResult{}, fmt.Errorf("get sub-plan %s for failed session %s: %w", failedSession.SubPlanID, failedSession.ID, err)
	}

	lastLines, err := readLastNLines(failedSession.ID, resumeLogLines)
	if err != nil {
		lastLines = "(prior session log unavailable)"
	}

	systemPrompt := buildFollowUpSystemPrompt(subPlan, lastLines, feedback, loadGlobalCommitConfig())

	// Create, transition to running, and emit EventAgentSessionResumed in one service call.
	// The failed session row is preserved as audit trail. EventAgentSessionResumed is emitted
	// by AgentSessionService.FollowUpFailed with the full new session and old session ID.
	newSession, err := r.sessionSvc.FollowUpFailed(ctx, failedSession, r.harness.Name(), &currentInstanceID)
	if err != nil {
		return FollowUpSessionResult{}, fmt.Errorf("create and start follow-up session: %w", err)
	}
	opts := adapter.SessionOpts{
		SessionID:    newSession.ID,
		Mode:         adapter.SessionModeAgent,
		WorkspaceID:  failedSession.WorkspaceID,
		SubPlanID:    failedSession.SubPlanID,
		Repository:   failedSession.RepositoryName,
		WorktreePath: failedSession.WorktreePath,
		SystemPrompt: systemPrompt,
		UserPrompt:   feedback,
	}
	opts.ResumeFromSessionID = failedSession.ID
	opts.ResumeInfo = failedSession.ResumeInfo

	harnessSession, err := r.harness.StartSession(ctx, opts)
	if err != nil {
		if failErr := failSessionDurably(ctx, r.sessionSvc, newSession.ID, nil); failErr != nil {
			slog.Warn("failed to fail follow-up session after harness start error",
				"error", failErr,
				"agent_session_id", newSession.ID)
		}
		return FollowUpSessionResult{}, fmt.Errorf("start harness session: %w", err)
	}

	if r.registry != nil {
		r.registry.Register(newSession.ID, harnessSession)
	}

	return FollowUpSessionResult{
		Session:        newSession,
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

// forwardEvents drains follow-up harness events so terminal bridge events cannot block
// BridgeSession.Wait. Detailed transcript events stay in session logs; questions are
// routed with implementation semantics because follow-up sessions continue implementation work.
func (r *Resumption) forwardEvents(ctx context.Context, events <-chan adapter.AgentEvent, sessionID string) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-events:
			if !ok {
				return
			}

			if evt.Type == "question" {
				if r.questionRouter != nil {
					if err := r.questionRouter.Route(ctx, domain.AgentSessionPhaseImplementation, evt, sessionID); err != nil {
						slog.Error("failed to route follow-up question", "error", err, "agent_session_id", sessionID)
					}
				}
				continue
			}

		}
	}
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

	drainCtx, drainCancel := context.WithCancel(context.WithoutCancel(ctx))
	defer drainCancel()
	go r.forwardEvents(drainCtx, harnessSession.Events(), sessionID)

	waitErr := harnessSession.Wait(ctx)
	if waitErr != nil {
		if errors.Is(waitErr, context.Canceled) {
			if err := interruptSessionDurably(ctx, r.sessionSvc, sessionID); err != nil {
				slog.Warn("failed to interrupt follow-up session after cancellation",
					"error", err, "agent_session_id", sessionID)
			}
		} else if !agentSessionAlreadyInterrupted(ctx, r.sessionSvc, sessionID) {
			if err := failSessionDurably(ctx, r.sessionSvc, sessionID, nil); err != nil {
				slog.Warn("failed to fail follow-up session",
					"error", err, "agent_session_id", sessionID)
			}
		}
		return
	}

	if err := completeSessionDurably(ctx, r.sessionSvc, sessionID); err != nil {
		slog.Warn("failed to complete follow-up session",
			"error", err, "agent_session_id", sessionID)
	}

	if info := harnessSession.ResumeInfo(); len(info) > 0 {
		if err := r.sessionSvc.UpdateResumeInfo(ctx, sessionID, info); err != nil {
			slog.Warn("failed to update resume info for follow-up session",
				"error", err, "agent_session_id", sessionID)
		}
	}
}
