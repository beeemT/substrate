package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/app/remotedetect"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/gitwork"
	"github.com/beeemT/substrate/internal/service"
	"golang.org/x/sync/errgroup"
)

// ImplementationService orchestrates the implementation phase after plan approval.
// It manages wave-based execution of sub-plans, worktree creation, and agent sessions.
type ImplementationService struct {
	cfg            *config.Config
	harness        adapter.AgentHarness
	gitClient      *gitwork.Client
	eventBus       *event.Bus
	planSvc        *service.PlanService
	workItemSvc    *service.SessionService
	sessionSvc     *service.TaskService
	workspaceSvc   *service.WorkspaceService
	registry       *SessionRegistry
	reviewPipeline *ReviewPipeline
	foreman        *Foreman
	questionSvc    *service.QuestionService
	reviewSvc      *service.ReviewService
	sessTimeout    time.Duration
}

// ImplementationConfig contains configuration for the implementation service.
type ImplementationConfig struct {
	SessionTimeout time.Duration
}

// DefaultImplementationConfig returns the default implementation configuration.
func DefaultImplementationConfig() *ImplementationConfig {
	return &ImplementationConfig{
		SessionTimeout: 2 * time.Hour,
	}
}

func NewImplementationService(
	cfg *config.Config,
	harness adapter.AgentHarness,
	gitClient *gitwork.Client,
	eventBus *event.Bus,
	planSvc *service.PlanService,
	workItemSvc *service.SessionService,
	sessionSvc *service.TaskService,
	workspaceSvc *service.WorkspaceService,
	registry *SessionRegistry,
	reviewPipeline *ReviewPipeline,
	foreman *Foreman,
	questionSvc *service.QuestionService,
	reviewSvc *service.ReviewService,
) *ImplementationService {
	implCfg := DefaultImplementationConfig()
	return &ImplementationService{
		cfg:            cfg,
		harness:        harness,
		gitClient:      gitClient,
		eventBus:       eventBus,
		planSvc:        planSvc,
		workItemSvc:    workItemSvc,
		sessionSvc:     sessionSvc,
		workspaceSvc:   workspaceSvc,
		registry:       registry,
		reviewPipeline: reviewPipeline,
		foreman:        foreman,
		questionSvc:    questionSvc,
		reviewSvc:      reviewSvc,
		sessTimeout:    implCfg.SessionTimeout,
	}
}

// ImplementResult contains the result of implementation execution.
type ImplementResult struct {
	PlanID        string
	WorkItemID    string
	State         *ExecutionState
	Sessions      []SessionResult
	Warnings      []ImplementationWarning
	ReviewResults map[string]*SubPlanOutcome // keyed by sub-plan ID
	CompletedAt   time.Time
}

// SubPlanOutcome captures the final state of a sub-plan after the full
// implement→review→reimpl cycle.
type SubPlanOutcome struct {
	SubPlanID    string
	Repository   string
	Passed       bool
	Escalated    bool
	Failed       bool
	ReviewResult *ReviewResult // nil when review was skipped (impl failed or no pipeline)
	Cycles       int           // total impl→review cycles executed
}

// SessionResult contains the result of a single agent session.
type SessionResult struct {
	SessionID    string
	SubPlanID    string
	Repository   string
	WorktreePath string
	Branch       string
	Status       domain.TaskStatus
	StartedAt    time.Time
	CompletedAt  *time.Time
	ExitCode     *int
	Summary      string
	Errors       []string
	Outcome      *SubPlanOutcome // populated after review loop completes
}

// ImplementationWarning represents a non-fatal issue during implementation.
type ImplementationWarning struct {
	Type      string // "worktree_exists", "session_failed", etc.
	Message   string
	RepoName  string
	SessionID string
}

// Implement starts the implementation phase for an approved plan.
// It executes sub-plans in waves, creating worktrees and spawning agent sessions.
func (s *ImplementationService) Implement(ctx context.Context, planID string) (result *ImplementResult, err error) {
	// 1. Get the plan
	plan, err := s.planSvc.GetPlan(ctx, planID)
	if err != nil {
		return nil, fmt.Errorf("get plan: %w", err)
	}

	// 2. Verify plan is approved
	if plan.Status != domain.PlanApproved {
		return nil, fmt.Errorf("plan status is %s, expected %s", plan.Status, domain.PlanApproved)
	}

	// 3. Get the work item
	workItem, err := s.workItemSvc.Get(ctx, plan.WorkItemID)
	if err != nil {
		return nil, fmt.Errorf("get work item: %w", err)
	}

	// 4. Get the workspace
	workspace, err := s.workspaceSvc.Get(ctx, workItem.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("get workspace: %w", err)
	}

	// 5. Get sub-plans
	subPlans, err := s.planSvc.ListSubPlansByPlanID(ctx, planID)
	if err != nil {
		return nil, fmt.Errorf("get sub-plans: %w", err)
	}

	implementingStarted := false
	defer func() {
		if err == nil || !implementingStarted {
			return
		}
		// Context cancellation means the user quit — leave the work item in
		// implementing state so sessions can be resumed on next startup.
		if errors.Is(err, context.Canceled) {
			return
		}
		cleanupCtx, cleanupCancel := durableCleanupContext(ctx)
		defer cleanupCancel()
		if failErr := s.workItemSvc.FailWorkItem(cleanupCtx, workItem.ID); failErr != nil {
			slog.Warn("failed to transition work item to failed after implementation error",
				"error", failErr,
				"plan_id", planID,
				"work_item_id", workItem.ID)
		}
	}()

	// 6. Discover repos before mutating work-item state.
	repoPaths, err := s.discoverRepoPaths(ctx, workspace.RootPath)
	if err != nil {
		return nil, fmt.Errorf("discover repo paths: %w", err)
	}

	// 7. Transition work item to implementing once non-mutating preflight succeeds.
	// On retry (work item already in implementing after RetryFailedWorkItem), skip.
	if workItem.State != domain.SessionImplementing {
		if err := s.workItemSvc.StartImplementation(ctx, workItem.ID); err != nil {
			return nil, fmt.Errorf("transition work item to implementing: %w", err)
		}
	}
	implementingStarted = true

	// 8. Emit ImplementationStarted event
	if err := s.emitImplementationStarted(ctx, &plan, &workItem, workspace.ID); err != nil {
		slog.Warn("failed to emit implementation started event", "error", err)
	}

	// 9. Generate branch name
	branch := GenerateBranchName(workItem.ExternalID, workItem.Title)

	// 10. Initialize execution state
	state := NewExecutionState(planID, subPlans)

	// 11. Execute waves
	result = &ImplementResult{
		PlanID:        planID,
		WorkItemID:    workItem.ID,
		State:         state,
		Sessions:      make([]SessionResult, 0),
		Warnings:      make([]ImplementationWarning, 0),
		ReviewResults: make(map[string]*SubPlanOutcome),
	}

	// Pre-create all worktrees sequentially before fan-out to eliminate the
	// TOCTOU race where two sub-plans in the same wave could race to create
	// a worktree for the same repository.
	worktreePaths, err := s.prepareWorktrees(ctx, &workspace, workItem.ID, workItem.Title, trackerRefsFromMetadata(workItem.Metadata), subPlans, branch, repoPaths)
	if err != nil {
		return nil, fmt.Errorf("prepare worktrees: %w", err)
	}

	// Reset failed and in_progress sub-plans to pending so they are picked up by BuildWaves.
	// Completed sub-plans are left alone. In_progress ones were left stranded by a process crash
	// — treat them as needing a fresh execution just like failed ones.
	for i := range subPlans {
		if subPlans[i].Status == domain.SubPlanFailed || subPlans[i].Status == domain.SubPlanInProgress {
			s.persistSubPlanStatus(ctx, &subPlans[i], domain.SubPlanPending)
		}
	}

	// Execute each wave sequentially
	for waveIndex, wave := range BuildWaves(subPlans) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		waveStart := time.Now()
		state.StartWave(waveIndex, waveStart.UnixNano())

		slog.Info("starting wave",
			"wave", waveIndex,
			"sub_plans", len(wave),
			"plan_id", planID)

		// Execute sub-plans in this wave concurrently
		sessionResults, warnings := s.executeWave(ctx, wave, &workspace, &plan, &workItem, branch, worktreePaths, state)
		result.Sessions = append(result.Sessions, sessionResults...)
		result.Warnings = append(result.Warnings, warnings...)

		// Check if wave completed successfully (impl + review).
		waveComplete := true
		for _, sr := range sessionResults {
			if sr.Outcome != nil {
				result.ReviewResults[sr.SubPlanID] = sr.Outcome
			}
			if sr.Status == domain.AgentSessionFailed || (sr.Outcome != nil && sr.Outcome.Failed) {
				waveComplete = false
				break
			}
		}

		waveEnd := time.Now()
		if !waveComplete {
			state.FailWave(waveIndex, waveEnd.UnixNano())
			// Stop execution on wave failure.
			break
		}
		state.CompleteWave(waveIndex, waveEnd.UnixNano())

		state.AdvanceWave()
	}

	result.CompletedAt = time.Now()

	// Determine final work item state based on review outcomes.
	cleanupCtx, cleanupCancel := durableCleanupContext(ctx)
	defer cleanupCancel()

	hasEscalated := false
	hasFailed := false
	for _, outcome := range result.ReviewResults {
		if outcome.Failed {
			hasFailed = true
		}
		if outcome.Escalated {
			hasEscalated = true
		}
	}

	switch {
	case hasFailed || !state.AllWavesCompleted():
		if failErr := s.workItemSvc.FailWorkItem(cleanupCtx, workItem.ID); failErr != nil {
			slog.Warn("failed to transition work item to failed", "error", failErr)
		}
	case hasEscalated:
		// At least one repo needs human decision. Transition to reviewing.
		if reviewErr := s.workItemSvc.SubmitForReview(cleanupCtx, workItem.ID); reviewErr != nil {
			slog.Warn("failed to transition work item to reviewing", "error", reviewErr)
		}
	default:
		// All repos passed review (or no review pipeline).
		// Commit any residual changes, push to remote, then complete.
		s.commitAndPushRepos(cleanupCtx, result.Sessions, repoPaths, branch)

		if completeErr := s.workItemSvc.CompleteWorkItem(cleanupCtx, workItem.ID); completeErr != nil {
			slog.Warn("failed to transition work item to completed", "error", completeErr)
		}

		if emitErr := s.emitWorkItemCompleted(cleanupCtx, &workItem, workspace.ID, result.Sessions, repoPaths, branch, subPlans); emitErr != nil {
			slog.Warn("failed to emit work item completed event", "error", emitErr)
		}
	}

	return result, nil
}

// executeWave executes all sub-plans in a wave concurrently.
func (s *ImplementationService) executeWave(
	ctx context.Context,
	wave []domain.TaskPlan,
	workspace *domain.Workspace,
	plan *domain.Plan,
	workItem *domain.Session,
	branch string,
	worktreePaths map[string]string,
	state *ExecutionState,
) ([]SessionResult, []ImplementationWarning) {
	var results []SessionResult
	var warnings []ImplementationWarning
	var mu sync.Mutex

	g, ctx := errgroup.WithContext(ctx)

	for _, sp := range wave {
		spCopy := sp

		g.Go(func() error {
			result, warning := s.executeSubPlan(ctx, spCopy, workspace, plan, workItem, branch, worktreePaths, state)

			mu.Lock()
			results = append(results, result)
			if warning != nil {
				warnings = append(warnings, *warning)
			}
			mu.Unlock()

			// Cancel other goroutines only on hard failure (impl or review).
			// Review escalation is not a failure — it's a human-decision pause.
			if result.Status == domain.AgentSessionFailed {
				return fmt.Errorf("sub-plan %s failed: %s", spCopy.ID, result.Summary)
			}
			if result.Outcome != nil && result.Outcome.Failed {
				return fmt.Errorf("sub-plan %s review failed", spCopy.ID)
			}

			return nil
		})
	}

	// Wait for all sub-plans in the wave
	_ = g.Wait() // Error is handled via results

	return results, warnings
}

// executeSubPlan executes a single sub-plan.
func (s *ImplementationService) executeSubPlan(
	ctx context.Context,
	subPlan domain.TaskPlan,
	workspace *domain.Workspace,
	plan *domain.Plan,
	workItem *domain.Session,
	branch string,
	worktreePaths map[string]string,
	state *ExecutionState,
) (SessionResult, *ImplementationWarning) {
	result := SessionResult{
		SubPlanID:  subPlan.ID,
		Repository: subPlan.RepositoryName,
		Branch:     branch,
		StartedAt:  time.Now(),
	}

	// Mark sub-plan as in progress
	state.StartSubPlan(subPlan.ID, time.Now().UnixNano())

	// Update sub-plan status using the full struct
	s.persistSubPlanStatus(ctx, &subPlan, domain.SubPlanInProgress)

	// Look up pre-created worktree path (created by prepareWorktrees before fan-out).
	worktreePath, ok := worktreePaths[subPlan.RepositoryName]
	if !ok {
		// Defensive: should not happen if prepareWorktrees succeeded.
		result.Status = domain.AgentSessionFailed
		result.Summary = fmt.Sprintf("worktree for repository %s not found", subPlan.RepositoryName)
		result.CompletedAt = ptrTime(time.Now())
		state.FailSubPlan(subPlan.ID, time.Now().UnixNano(), fmt.Errorf("%s", result.Summary))
		return result, &ImplementationWarning{
			Type:     "worktree_not_found",
			Message:  result.Summary,
			RepoName: subPlan.RepositoryName,
		}
	}
	result.WorktreePath = worktreePath

	// Crash recovery: if the most recent session for this sub-plan is a review session,
	// the review agent crashed — skip re-implementation and retry the review directly.
	if s.reviewPipeline != nil {
		if last := s.lastSessionForSubPlan(ctx, subPlan.ID); last != nil && last.Phase == domain.TaskPhaseReview {
			prevImpl := s.latestCompletedImplSession(ctx, subPlan.ID)
			if prevImpl != nil {
				slog.Info("skipping implementation, retrying review for sub-plan",
					"sub_plan_id", subPlan.ID, "prev_impl_session_id", prevImpl.ID)
				result.Status = domain.AgentSessionCompleted
				result.SessionID = prevImpl.ID
				result.WorktreePath = prevImpl.WorktreePath
				result.Summary = "Retrying review with existing implementation"
				result.CompletedAt = ptrTime(time.Now())
				outcome := s.reviewLoop(ctx, *prevImpl, subPlan, workspace, plan, workItem, worktreePaths)
				result.Outcome = outcome
				if outcome.Passed {
					state.CompleteSubPlan(subPlan.ID, time.Now().UnixNano())
					s.persistSubPlanStatus(ctx, &subPlan, domain.SubPlanCompleted)
				} else if outcome.Failed {
					state.FailSubPlan(subPlan.ID, time.Now().UnixNano(), fmt.Errorf("review failed for %s", subPlan.RepositoryName))
					s.persistSubPlanStatus(ctx, &subPlan, domain.SubPlanFailed)
				} else if outcome.Escalated {
					state.FailSubPlan(subPlan.ID, time.Now().UnixNano(), fmt.Errorf("review escalated for %s — requires human intervention", subPlan.RepositoryName))
					s.persistSubPlanStatus(ctx, &subPlan, domain.SubPlanFailed)
				}
				return result, nil
			}
			slog.Warn("review retry needed but no completed impl session found, falling back to full implementation",
				"sub_plan_id", subPlan.ID, "last_session_id", last.ID)
		}
	}

	// Load any outstanding review critique context (empty for first-time implementations).
	critiqueFeedback := s.loadCritiqueFeedback(ctx, subPlan.ID)
	prevImpl := s.latestCompletedImplSession(ctx, subPlan.ID)

	// Run implementation (fresh or with critique context from prior review).
	implSession, err := s.runImplementation(ctx, subPlan, workspace, plan, workItem, worktreePath, critiqueFeedback, prevImpl)
	if err != nil {
		result.Status = domain.AgentSessionFailed
		result.Summary = err.Error()
		result.CompletedAt = ptrTime(time.Now())
		state.FailSubPlan(subPlan.ID, time.Now().UnixNano(), err)
		s.persistSubPlanStatus(ctx, &subPlan, domain.SubPlanFailed)
		return result, nil
	}

	result.SessionID = implSession.ID
	result.Status = domain.AgentSessionCompleted
	result.Summary = "Session completed successfully"
	result.CompletedAt = ptrTime(time.Now())

	// Run review loop if pipeline is configured.
	if s.reviewPipeline != nil {
		outcome := s.reviewLoop(ctx, implSession, subPlan, workspace, plan, workItem, worktreePaths)
		result.Outcome = outcome
		if outcome.Passed {
			state.CompleteSubPlan(subPlan.ID, time.Now().UnixNano())
			s.persistSubPlanStatus(ctx, &subPlan, domain.SubPlanCompleted)
		} else if outcome.Failed {
			state.FailSubPlan(subPlan.ID, time.Now().UnixNano(), fmt.Errorf("review failed for %s", subPlan.RepositoryName))
			s.persistSubPlanStatus(ctx, &subPlan, domain.SubPlanFailed)
		} else if outcome.Escalated {
			state.FailSubPlan(subPlan.ID, time.Now().UnixNano(), fmt.Errorf("review escalated for %s \u2014 requires human intervention", subPlan.RepositoryName))
			s.persistSubPlanStatus(ctx, &subPlan, domain.SubPlanFailed)
		}
		return result, nil
	}

	// No review pipeline — mark sub-plan completed immediately.
	state.CompleteSubPlan(subPlan.ID, time.Now().UnixNano())
	s.persistSubPlanStatus(ctx, &subPlan, domain.SubPlanCompleted)

	return result, nil
}

// reviewLoop runs the implement→review→reimpl cycle for a single sub-plan.
// It returns the final SubPlanOutcome. The initial implementation session is
// already completed; this method starts with the first review.
func (s *ImplementationService) reviewLoop(
	ctx context.Context,
	implSession domain.Task,
	subPlan domain.TaskPlan,
	workspace *domain.Workspace,
	plan *domain.Plan,
	workItem *domain.Session,
	worktreePaths map[string]string,
) *SubPlanOutcome {
	outcome := &SubPlanOutcome{
		SubPlanID:  subPlan.ID,
		Repository: subPlan.RepositoryName,
	}

	currentSession := implSession
	autoLoop := s.cfg.Review.AutoFeedbackLoop != nil && *s.cfg.Review.AutoFeedbackLoop

	for {
		outcome.Cycles++

		// Safety bound: the per-session max-cycles check inside ReviewSession
		// resets after each reimplementation (new session). This outer guard
		// ensures the total cycle count across all sessions is bounded.
		if s.cfg.Review.MaxCycles != nil && outcome.Cycles > *s.cfg.Review.MaxCycles {
			outcome.Escalated = true
			return outcome
		}

		reviewResult, err := s.reviewPipeline.ReviewSession(ctx, currentSession)
		if err != nil {
			slog.Warn("review session failed", "error", err,
				"session_id", currentSession.ID, "sub_plan", subPlan.ID)
			outcome.Failed = true
			return outcome
		}
		outcome.ReviewResult = reviewResult

		if reviewResult.Passed {
			outcome.Passed = true
			return outcome
		}

		if reviewResult.Escalated {
			outcome.Escalated = true
			return outcome
		}

		if !reviewResult.NeedsReimpl || !autoLoop {
			// Needs reimpl but auto-loop disabled — escalate for human decision.
			outcome.Escalated = true
			return outcome
		}

		// Auto-reimpl: resume the previous session with critique feedback.
		feedback := buildCritiqueFeedback(reviewResult.Critiques)
		worktreePath, ok := worktreePaths[subPlan.RepositoryName]
		if !ok {
			slog.Warn("worktree path not found for auto-reimpl",
				"sub_plan_id", subPlan.ID, "repo", subPlan.RepositoryName)
			outcome.Failed = true
			return outcome
		}
		newSession, err := s.runImplementation(ctx, subPlan, workspace, plan, workItem, worktreePath, feedback, &currentSession)
		if err != nil {
			slog.Warn("reimplementation failed", "error", err,
				"sub_plan", subPlan.ID, "cycle", outcome.Cycles)
			outcome.Failed = true
			return outcome
		}
		currentSession = newSession
	}
}

// runImplementation creates and runs a new agent session for a sub-plan.
// It handles both fresh implementations and re-implementations with review
// critique context. When prevSession is non-nil and has ResumeInfo, the harness
// is asked to resume the previous conversation; critique feedback is then sent
// as a follow-up message to preserve conversation context. When prevSession is
// nil or has no ResumeInfo, critique feedback is appended to the system prompt.
func (s *ImplementationService) runImplementation(
	ctx context.Context,
	subPlan domain.TaskPlan,
	workspace *domain.Workspace,
	plan *domain.Plan,
	workItem *domain.Session,
	worktreePath string,
	critiqueFeedback string,
	prevSession *domain.Task,
) (domain.Task, error) {
	sessionID := domain.NewID()
	session := domain.Task{
		ID:             sessionID,
		WorkItemID:     workItem.ID,
		WorkspaceID:    workspace.ID,
		Phase:          domain.TaskPhaseImplementation,
		SubPlanID:      subPlan.ID,
		RepositoryName: subPlan.RepositoryName,
		WorktreePath:   worktreePath,
		HarnessName:    s.harness.Name(),
		Status:         domain.AgentSessionPending,
	}
	if err := s.sessionSvc.Create(ctx, session); err != nil {
		return domain.Task{}, fmt.Errorf("create session: %w", err)
	}
	if err := s.sessionSvc.Start(ctx, sessionID); err != nil {
		deleteOrFailPendingSession(ctx, s.sessionSvc, sessionID, ptrInt(1))
		return domain.Task{}, fmt.Errorf("start session: %w", err)
	}
	now := time.Now()
	session.Status = domain.AgentSessionRunning
	session.StartedAt = &now
	session.UpdatedAt = now

	if err := s.emitSessionStarted(ctx, &session, workspace.ID); err != nil {
		slog.Warn("failed to emit session started event", "error", err)
	}

	opts := s.buildSessionOpts(session, subPlan, plan, workItem, workspace)

	// Decide how to deliver critique feedback.
	// When resuming a prior session (prevSession has ResumeInfo), send critique
	// as a follow-up message after the harness starts so the model sees it in
	// conversation context. When not resuming, bake critique into the system prompt.
	hasResume := prevSession != nil && len(prevSession.ResumeInfo) > 0
	canCompact := hasResume && s.harness.SupportsCompact()
	if canCompact {
		opts.ResumeFromSessionID = prevSession.ID
		opts.ResumeInfo = prevSession.ResumeInfo
		opts.UserPrompt = "" // harness resumes; no new prompt turn needed
	} else if critiqueFeedback != "" {
		opts.SystemPrompt += "\n\n" + critiqueFeedback
	}

	harnessSession, err := s.harness.StartSession(ctx, opts)
	if err != nil {
		if failErr := failSessionDurably(ctx, s.sessionSvc, sessionID, ptrInt(1)); failErr != nil {
			slog.Warn("failed to fail session after harness start error", "error", failErr,
				"session_id", sessionID)
		}
		return domain.Task{}, fmt.Errorf("start agent: %w", err)
	}

	// Compact the conversation before sending critique so the model starts with
	// a clean summary of its prior work rather than the full transcript.
	if canCompact {
		if err := harnessSession.Compact(ctx); err != nil {
			slog.Warn("failed to compact resumed session, continuing without compact", "error", err,
				"session_id", sessionID)
		}
	}

	// Send critique feedback as a follow-up message when resuming a prior session.
	// This preserves the model's conversation history — it knows what it implemented
	// and can see the critique in context.
	if canCompact && critiqueFeedback != "" {
		if err := harnessSession.SendMessage(ctx, critiqueFeedback); err != nil {
			slog.Warn("failed to send critique feedback to resumed session", "error", err,
				"session_id", sessionID)
			// Non-fatal: session continues without the explicit critique prompt.
		}
	}

	sessionCtx, sessionCancel := context.WithTimeout(ctx, s.sessTimeout)
	defer sessionCancel()

	if s.registry != nil {
		s.registry.Register(sessionID, harnessSession)
		defer s.registry.Deregister(sessionID)
	}

	go s.forwardEvents(sessionCtx, harnessSession.Events(), workspace.ID, sessionID)

	waitErr := harnessSession.Wait(sessionCtx)

	if waitErr != nil {
		if errors.Is(waitErr, context.Canceled) {
			// Pipeline was cancelled (user quit) — mark as interrupted for resume on next startup.
			if interruptErr := interruptSessionDurably(ctx, s.sessionSvc, sessionID); interruptErr != nil {
				slog.Warn("failed to interrupt session on context cancellation",
					"error", interruptErr, "session_id", sessionID)
			}
			if err := s.emitSessionInterrupted(ctx, &session, workspace.ID); err != nil {
				slog.Warn("failed to emit session interrupted event",
					"error", err, "session_id", sessionID)
			}
		} else {
			if failErr := failSessionDurably(ctx, s.sessionSvc, sessionID, ptrInt(1)); failErr != nil {
				slog.Warn("failed to fail session", "error", failErr, "session_id", sessionID)
			}
			if err := s.emitSessionFailed(ctx, &session, waitErr.Error(), workspace.ID); err != nil {
				slog.Warn("failed to emit session failed event", "error", err, "session_id", sessionID)
			}
		}
		return domain.Task{}, fmt.Errorf("agent session failed: %w", waitErr)
	}

	if completeErr := completeSessionDurably(ctx, s.sessionSvc, sessionID); completeErr != nil {
		slog.Warn("failed to complete session", "error", completeErr, "session_id", sessionID)
	}

	// Persist harness-specific resume data generically — no harness-specific knowledge here.
	if info := harnessSession.ResumeInfo(); len(info) > 0 {
		session.ResumeInfo = info
		if err := s.sessionSvc.UpdateResumeInfo(ctx, sessionID, info); err != nil {
			slog.Warn("failed to store resume info", "error", err, "session_id", sessionID)
		}
	}

	if err := s.emitSessionCompleted(ctx, &session, workspace.ID); err != nil {
		slog.Warn("failed to emit session completed event", "error", err)
	}

	return session, nil
}

// buildCritiqueFeedback formats review critiques into a prompt section
// that instructs the implementation agent to address them.
func buildCritiqueFeedback(critiques []domain.Critique) string {
	if len(critiques) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Review Critiques\n\n")
	b.WriteString("The following issues were found during code review. Address each one:\n\n")
	for i, c := range critiques {
		fmt.Fprintf(&b, "%d. [%s] %s", i+1, c.Severity, c.Description)
		if c.FilePath != "" {
			fmt.Fprintf(&b, " (file: %s", c.FilePath)
			if c.LineNumber != nil && *c.LineNumber > 0 {
				fmt.Fprintf(&b, ", line %d", *c.LineNumber)
			}
			b.WriteString(")")
		}
		if c.Suggestion != "" {
			fmt.Fprintf(&b, "\n   Suggestion: %s", c.Suggestion)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// loadCritiqueFeedback looks up outstanding review critiques for a sub-plan
// and formats them for injection into the next implementation session.
// Returns "" when no reviewSvc is configured, when no prior implementation
// session exists, or when no review cycle with critiques is found.
func (s *ImplementationService) loadCritiqueFeedback(ctx context.Context, subPlanID string) string {
	if s.reviewSvc == nil {
		return ""
	}
	prev := s.latestCompletedImplSession(ctx, subPlanID)
	if prev == nil {
		return ""
	}
	cycles, err := s.reviewSvc.ListCyclesBySessionID(ctx, prev.ID)
	if err != nil {
		slog.Warn("failed to list review cycles for critique lookup",
			"error", err, "sub_plan_id", subPlanID)
		return ""
	}
	// Find the latest cycle (highest CycleNumber) with critiques_found or reimplementing status.
	var latest *domain.ReviewCycle
	for i := range cycles {
		c := &cycles[i]
		if c.Status != domain.ReviewCycleCritiquesFound && c.Status != domain.ReviewCycleReimplementing {
			continue
		}
		if latest == nil || c.CycleNumber > latest.CycleNumber {
			latest = c
		}
	}
	if latest == nil {
		return ""
	}
	critiques, err := s.reviewSvc.ListCritiquesByCycleID(ctx, latest.ID)
	if err != nil {
		slog.Warn("failed to list critiques for review cycle",
			"error", err, "cycle_id", latest.ID)
		return ""
	}
	return buildCritiqueFeedback(critiques)
}

// ensureWorktree creates a worktree if it doesn't exist, or returns the existing one.
// Implements idempotency guard by checking git-work list first.
func (s *ImplementationService) ensureWorktree(
	ctx context.Context,
	workspace *domain.Workspace,
	workItemID, repoName, repoPath, branch, workItemTitle string, trackerRefs []domain.TrackerReference, subPlan string,
) (string, error) {
	// Check if worktree already exists (idempotency guard)
	worktrees, err := s.gitClient.List(ctx, repoPath)
	if err != nil {
		return "", fmt.Errorf("list worktrees: %w", err)
	}

	reviewCtx, err := remotedetect.ResolveReviewContext(ctx, repoPath)
	if err != nil {
		slog.Warn("failed to resolve review context", "repo", repoName, "error", err)
		reviewCtx = remotedetect.ReviewContext{}
	}

	for _, wt := range worktrees {
		if wt.Branch == branch {
			slog.Info("worktree already exists, reusing",
				"repo", repoName,
				"branch", branch,
				"path", wt.Path)

			// Emit WorktreeReused event so lifecycle adapters can update MR/PR descriptions
			reusedPayload := WorktreeCreatedPayload{
				WorkspaceID:   workspace.ID,
				WorkItemID:    workItemID,
				Repository:    repoName,
				Branch:        branch,
				WorktreePath:  wt.Path,
				WorkItemTitle: workItemTitle,
				SubPlan:       subPlan,
				TrackerRefs:   trackerRefs,
				Review:        reviewCtx.Review,
			}
			reusedEvent := domain.SystemEvent{
				ID:          domain.NewID(),
				EventType:   string(domain.EventWorktreeReused),
				WorkspaceID: workspace.ID,
				Payload:     marshalJSONOrEmpty(string(domain.EventWorktreeReused), reusedPayload),
				CreatedAt:   time.Now(),
			}
			if err := s.eventBus.Publish(ctx, reusedEvent); err != nil {
				slog.Warn("failed to emit worktree reused event", "error", err)
			}

			return wt.Path, nil
		}
	}

	// Emit WorktreeCreating pre-hook event
	creatingPayload := WorktreeCreatingPayload{
		WorkspaceID:   workspace.ID,
		WorkItemID:    workItemID,
		Repository:    repoName,
		Branch:        branch,
		WorkItemTitle: workItemTitle,
		SubPlan:       subPlan,
		Review:        reviewCtx.Review,
	}
	creatingEvent := domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventWorktreeCreating),
		WorkspaceID: workspace.ID,
		Payload:     marshalJSONOrEmpty(string(domain.EventWorktreeCreating), creatingPayload),
		CreatedAt:   time.Now(),
	}
	if err := s.eventBus.Publish(ctx, creatingEvent); err != nil {
		return "", fmt.Errorf("worktree creating pre-hook rejected: %w", err)
	}

	// Create worktree
	worktreePath, err := s.gitClient.Checkout(ctx, repoPath, branch)
	if err != nil {
		return "", fmt.Errorf("git-work checkout: %w", err)
	}

	// Push branch to remote so lifecycle adapters (GitHub, GitLab) can create
	// a draft PR/MR immediately. The remote must know the branch before the API
	// will accept it as a PR head. Best-effort: a failed push is logged but does
	// not abort worktree creation; the PR creation attempt will warn later.
	if reviewCtx.RemoteName != "" {
		if pushErr := gitPushBranch(ctx, repoPath, reviewCtx.RemoteName, branch); pushErr != nil {
			slog.Warn("failed to push branch to remote; draft PR creation may fail",
				"repo", repoName, "branch", branch, "err", pushErr)
		}
	}

	// Emit WorktreeCreated post-hook event
	createdPayload := WorktreeCreatedPayload{
		WorkspaceID:   workspace.ID,
		WorkItemID:    workItemID,
		Repository:    repoName,
		Branch:        branch,
		WorktreePath:  worktreePath,
		WorkItemTitle: workItemTitle,
		SubPlan:       subPlan,
		TrackerRefs:   trackerRefs,
		Review:        reviewCtx.Review,
	}
	createdEvent := domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventWorktreeCreated),
		WorkspaceID: workspace.ID,
		Payload:     marshalJSONOrEmpty(string(domain.EventWorktreeCreated), createdPayload),
		CreatedAt:   time.Now(),
	}
	if err := s.eventBus.Publish(ctx, createdEvent); err != nil {
		slog.Warn("failed to emit worktree created event", "error", err)
	}

	return worktreePath, nil
}

// prepareWorktrees creates worktrees for all unique repositories referenced by
// the sub-plans before any goroutine fan-out. Sequential execution here
// eliminates the TOCTOU race that would arise if two sub-plans in the same
// wave targeted the same repository and called ensureWorktree concurrently.
func (s *ImplementationService) prepareWorktrees(
	ctx context.Context,
	workspace *domain.Workspace,
	workItemID string,
	workItemTitle string,
	trackerRefs []domain.TrackerReference,
	subPlans []domain.TaskPlan,
	branch string,
	repoPaths map[string]string,
) (map[string]string, error) {
	seen := make(map[string]bool)
	worktreePaths := make(map[string]string, len(repoPaths))
	for _, sp := range subPlans {
		if seen[sp.RepositoryName] {
			continue
		}
		seen[sp.RepositoryName] = true
		repoPath, ok := repoPaths[sp.RepositoryName]
		if !ok {
			return nil, fmt.Errorf("repository %s not found in workspace", sp.RepositoryName)
		}
		wt, err := s.ensureWorktree(ctx, workspace, workItemID, sp.RepositoryName, repoPath, branch, workItemTitle, trackerRefs, sp.Content)
		if err != nil {
			return nil, fmt.Errorf("prepare worktree for %s: %w", sp.RepositoryName, err)
		}
		worktreePaths[sp.RepositoryName] = wt
	}
	return worktreePaths, nil
}

// buildSessionOpts builds session options for an agent session.
func (s *ImplementationService) buildSessionOpts(
	session domain.Task,
	subPlan domain.TaskPlan,
	plan *domain.Plan,
	workItem *domain.Session,
	workspace *domain.Workspace,
) adapter.SessionOpts {
	// Read AGENTS.md if it exists
	agentsMdPath := filepath.Join(session.WorktreePath, "AGENTS.md")
	docContext := ""
	if content, err := os.ReadFile(agentsMdPath); err == nil {
		docContext = string(content)
	}

	// Build commit config from global config
	commitConfig := adapter.CommitConfig{
		Strategy:      "semi-regular",
		MessageFormat: "ai-generated",
	}
	if s.cfg != nil {
		commitConfig.Strategy = string(s.cfg.Commit.Strategy)
		commitConfig.MessageFormat = string(s.cfg.Commit.MessageFormat)
		commitConfig.MessageTemplate = s.cfg.Commit.MessageTemplate
	}
	// Build system prompt
	systemPrompt := s.buildSystemPrompt(subPlan, plan, workItem, docContext, commitConfig)

	return adapter.SessionOpts{
		SessionID:            session.ID,
		Mode:                 adapter.SessionModeAgent,
		WorkspaceID:          workspace.ID,
		SubPlanID:            subPlan.ID,
		Repository:           subPlan.RepositoryName,
		WorktreePath:         session.WorktreePath,
		CrossRepoPlan:        plan.OrchestratorPlan,
		DocumentationContext: docContext,
		SystemPrompt:         systemPrompt,
		UserPrompt:           "Begin implementing the sub-plan. Follow the instructions and validate your changes.",
		CommitConfig:         commitConfig,
		AllowPush:            false, // Push only after review passes
		AnswerTimeoutMs:      s.foremanAnswerTimeoutMs(),
	}
}

// foremanAnswerTimeoutMs returns the configured foreman question timeout in
// milliseconds for the bridge's ask_foreman answer wait. Returns 0 when no
// timeout is configured (0 = wait indefinitely).
func (s *ImplementationService) foremanAnswerTimeoutMs() int64 {
	if s.cfg == nil || s.cfg.Foreman.QuestionTimeout == "" || s.cfg.Foreman.QuestionTimeout == "0" {
		return 0
	}
	if d, err := time.ParseDuration(s.cfg.Foreman.QuestionTimeout); err == nil && d > 0 {
		return d.Milliseconds()
	}
	return 0
}

// buildSystemPrompt builds the system prompt for an agent session.
func (s *ImplementationService) buildSystemPrompt(
	subPlan domain.TaskPlan,
	plan *domain.Plan,
	workItem *domain.Session,
	docContext string,
	commitCfg adapter.CommitConfig,
) string {
	var prompt strings.Builder

	prompt.WriteString("# Task\n\n")
	prompt.WriteString("Implement the following sub-plan for the work item.\n\n")

	prompt.WriteString("## Work Item\n")
	fmt.Fprintf(&prompt, "Title: %s\n", workItem.Title)
	fmt.Fprintf(&prompt, "ID: %s\n\n", workItem.ExternalID)

	prompt.WriteString("## Cross-Repo Orchestration\n")
	prompt.WriteString(plan.OrchestratorPlan)
	prompt.WriteString("\n\n")

	prompt.WriteString("## Sub-Plan for ")
	prompt.WriteString(subPlan.RepositoryName)
	prompt.WriteString("\n\n")
	prompt.WriteString(subPlan.Content)
	prompt.WriteString("\n\n")

	if docContext != "" {
		prompt.WriteString("## Repository Guidance (from AGENTS.md)\n\n")
		prompt.WriteString(docContext)
		prompt.WriteString("\n\n")
	}

	if section := buildCommitSection(commitCfg); section != "" {
		prompt.WriteString(section)
		prompt.WriteString("\n\n")
	}

	prompt.WriteString("## Validation\n")
	prompt.WriteString("Before marking complete: run all relevant formatters, compilation checks, and unit tests.\n")
	prompt.WriteString("All must pass. Refer to AGENTS.md in this repo for tooling specifics.\n")

	return prompt.String()
}

// buildCommitSection returns a prompt section instructing the agent how to commit
// its work based on the configured strategy. Returns empty string when no
// meaningful strategy is set.
func buildCommitSection(cfg adapter.CommitConfig) string {
	var sb strings.Builder
	sb.WriteString("## Commit Strategy\n\n")

	switch cfg.Strategy {
	case "granular":
		sb.WriteString("Commit frequently: after every self-contained change (a function added, a test fixed, a refactor step). Use \x60git add -A && git commit\x60 for each logical unit of work. Write concise, descriptive commit messages summarizing what changed and why. Do not batch unrelated changes into a single commit.")
	case "semi-regular":
		sb.WriteString("Commit at meaningful checkpoints: after completing a logical group of related changes (a feature component, a refactored module, a passing test suite). Use \x60git add -A && git commit\x60. Write clear commit messages that describe the group of changes. Do not leave uncommitted work at session end.")
	case "single":
		sb.WriteString("Make a single commit at the end of the session containing all your changes. Use \x60git add -A && git commit\x60. Write a comprehensive commit message summarizing the full scope of work.")
	default:
		return ""
	}

	return sb.String()
}

// forwardEvents forwards agent events to the event bus.
// When a "question" event is received and a foreman is available,
// it routes the question to the foreman and delivers the answer back
// to the originating agent session.
func (s *ImplementationService) forwardEvents(ctx context.Context, events <-chan adapter.AgentEvent, workspaceID, sessionID string) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-events:
			if !ok {
				return
			}

			// Route question events to the foreman when available.
			if evt.Type == "question" && s.foreman != nil {
				s.routeQuestionToForeman(ctx, evt, sessionID)
				continue
			}

			// Convert agent event to system event and publish to bus
			sysEvent := domain.SystemEvent{
				ID:          domain.NewID(),
				EventType:   evt.Type,
				WorkspaceID: workspaceID,
				Payload:     marshalJSONOrEmpty(evt.Type, evt.Payload),
				CreatedAt:   time.Now(),
			}
			if err := s.eventBus.Publish(ctx, sysEvent); err != nil {
				slog.Warn("failed to forward agent event to bus", "error", err, "type", evt.Type)
			}
		}
	}
}

// routeQuestionToForeman persists a question, submits it to the foreman,
// and delivers the answer back to the originating agent session via SendAnswer.
func (s *ImplementationService) routeQuestionToForeman(ctx context.Context, evt adapter.AgentEvent, sessionID string) {
	questionText := evt.Payload
	questionCtx := ""
	if evt.Metadata != nil {
		if c, ok := evt.Metadata["context"].(string); ok {
			questionCtx = c
		}
	}

	q := domain.Question{
		ID:             domain.NewID(),
		AgentSessionID: sessionID,
		Content:        questionText,
		Context:        questionCtx,
		Status:         domain.QuestionPending,
	}

	// Persist the question.
	if err := s.questionSvc.Create(ctx, q); err != nil {
		slog.Error("failed to persist question for foreman", "error", err, "session_id", sessionID)
		return
	}

	// Also publish the canonical event so the TUI can display the question.
	if err := s.eventBus.Publish(ctx, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventAgentQuestionRaised),
		WorkspaceID: "",
		Payload:     marshalJSONOrEmpty("agent_question.raised", map[string]string{"id": q.ID, "session_id": sessionID, "question": questionText}),
		CreatedAt:   time.Now(),
	}); err != nil {
		slog.Warn("failed to publish question raised event", "error", err)
	}

	// Submit to the foreman and wait for the answer in a goroutine.
	// This must not block forwardEvents — other events keep flowing.
	answerCh := s.foreman.Ask(ctx, q)
	go func() {
		select {
		case answer, ok := <-answerCh:
			if !ok || answer == "" {
				slog.Warn("foreman answer channel closed without answer", "question_id", q.ID)
				return
			}
			// Deliver the answer back to the agent session via stdin.
			if s.registry != nil {
				if err := s.registry.SendAnswer(ctx, sessionID, answer); err != nil {
					slog.Error("failed to send foreman answer to agent session",
						"error", err, "question_id", q.ID, "session_id", sessionID)
					return
				}
			}
			// Publish the canonical answered event.
			if pubErr := s.eventBus.Publish(ctx, domain.SystemEvent{
				ID:          domain.NewID(),
				EventType:   string(domain.EventAgentQuestionAnswered),
				WorkspaceID: "",
				Payload:     marshalJSONOrEmpty("agent_question.answered", map[string]string{"id": q.ID, "session_id": sessionID}),
				CreatedAt:   time.Now(),
			}); pubErr != nil {
				slog.Warn("failed to publish question answered event", "error", pubErr)
			}
		case <-ctx.Done():
			slog.Warn("context cancelled while waiting for foreman answer", "question_id", q.ID)
		}
	}()
}

// discoverRepoPaths discovers repo paths in the workspace.
func (s *ImplementationService) discoverRepoPaths(_ context.Context, workspaceDir string) (map[string]string, error) {
	entries, err := os.ReadDir(workspaceDir)
	if err != nil {
		return nil, fmt.Errorf("read workspace directory: %w", err)
	}

	paths := make(map[string]string)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		repoPath := filepath.Join(workspaceDir, entry.Name())
		if gitwork.IsGitWorkRepo(repoPath) {
			paths[entry.Name()] = repoPath
		}
	}

	return paths, nil
}

// Event emission helpers

type WorktreeCreatingPayload struct {
	WorkspaceID   string           `json:"workspace_id"`
	WorkItemID    string           `json:"work_item_id"`
	Repository    string           `json:"repository"`
	Branch        string           `json:"branch"`
	WorkItemTitle string           `json:"work_item_title"`
	SubPlan       string           `json:"sub_plan"`
	Review        domain.ReviewRef `json:"review"`
}

type WorktreeCreatedPayload struct {
	WorkspaceID   string                    `json:"workspace_id"`
	WorkItemID    string                    `json:"work_item_id"`
	Repository    string                    `json:"repository"`
	Branch        string                    `json:"branch"`
	WorktreePath  string                    `json:"worktree_path"`
	WorkItemTitle string                    `json:"work_item_title"`
	SubPlan       string                    `json:"sub_plan"`
	TrackerRefs   []domain.TrackerReference `json:"tracker_refs"`
	Review        domain.ReviewRef          `json:"review"`
}

func trackerRefsFromMetadata(metadata map[string]any) []domain.TrackerReference {
	if len(metadata) == 0 {
		return nil
	}
	raw, ok := metadata["tracker_refs"]
	if !ok || raw == nil {
		return nil
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var refs []domain.TrackerReference
	if err := json.Unmarshal(payload, &refs); err != nil {
		return nil
	}
	return refs
}

// publishEvent constructs a SystemEvent and publishes it to the event bus.
func (s *ImplementationService) publishEvent(ctx context.Context, eventType domain.EventType, workspaceID string, payload any) error {
	evt := domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(eventType),
		WorkspaceID: workspaceID,
		Payload:     marshalJSONOrEmpty(string(eventType), payload),
		CreatedAt:   time.Now(),
	}
	return s.eventBus.Publish(ctx, evt)
}

type sessionEventPayload struct {
	SessionID    string           `json:"session_id"`
	WorkItemID   string           `json:"work_item_id"`
	Phase        domain.TaskPhase `json:"phase"`
	SubPlanID    string           `json:"sub_plan_id"`
	Repository   string           `json:"repository"`
	WorktreePath string           `json:"worktree_path"`
}

func newSessionEventPayload(session *domain.Task) sessionEventPayload {
	return sessionEventPayload{
		SessionID:    session.ID,
		WorkItemID:   session.WorkItemID,
		Phase:        session.Phase,
		SubPlanID:    session.SubPlanID,
		Repository:   session.RepositoryName,
		WorktreePath: session.WorktreePath,
	}
}

// commitAndPushRepos ensures all agent changes are committed and pushed to remote.
// It runs after all repos pass review. For each unique repo, it checks for residual
// uncommitted changes, commits them as a safety net if present, then pushes the branch.
// Agent sessions are already completed and deregistered by this point, so we
// commit directly rather than sending a follow-up message to the agent.
func (s *ImplementationService) commitAndPushRepos(ctx context.Context, sessions []SessionResult, repoPaths map[string]string, branch string) {
	seen := make(map[string]bool, len(repoPaths))
	for _, sess := range sessions {
		repo := sess.Repository
		if seen[repo] {
			continue
		}
		seen[repo] = true

		bareRepo, ok := repoPaths[repo]
		if !ok {
			slog.Warn("no bare repo path for repository, skipping commit/push", "repo", repo)
			continue
		}

		// Resolve review context from the bare repo for remote name.
		reviewCtx, err := remotedetect.ResolveReviewContext(ctx, bareRepo)
		if err != nil {
			slog.Warn("failed to resolve review context for push", "repo", repo, "error", err)
		}

		// Check for residual uncommitted changes in the worktree.
		if sess.WorktreePath != "" {
			if dirty, _ := gitStatusDirty(ctx, sess.WorktreePath); dirty {
				slog.Warn("agent left uncommitted changes in worktree; spinning up commit session",
					"repo", repo, "worktree", sess.WorktreePath, "session_id", sess.SessionID)
				if commitErr := s.commitViaAgent(ctx, sess.WorktreePath, repo, sess.SessionID); commitErr != nil {
					slog.Error("failed to commit residual changes", "repo", repo, "error", commitErr)
				}
			}
		}

		// Push branch to remote.
		if reviewCtx.RemoteName != "" {
			if pushErr := gitPushBranch(ctx, bareRepo, reviewCtx.RemoteName, branch); pushErr != nil {
				slog.Warn("failed to push branch to remote after review pass",
					"repo", repo, "branch", branch, "err", pushErr)
			}
		}
	}
}

// gitStatusDirty returns true if the working tree at dir has uncommitted changes.
func gitStatusDirty(ctx context.Context, dir string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("git status --porcelain: %w", err)
	}
	return len(strings.TrimSpace(string(out))) > 0, nil
}

// gitStageAndCommit stages all changes and commits them with the given message.
func gitStageAndCommit(ctx context.Context, dir, message string) error {
	// Stage all changes.
	addCmd := exec.CommandContext(ctx, "git", "add", "-A")
	addCmd.Dir = dir
	if out, err := addCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git add -A: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}

	// Commit.
	commitCmd := exec.CommandContext(ctx, "git", "commit", "-m", message)
	commitCmd.Dir = dir
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// commitViaAgent spins up a short-lived agent session to commit residual changes
// using the configured commit message format instead of a hardcoded generic message.
// Falls back to gitStageAndCommit with a generic message if the agent session fails.
func (s *ImplementationService) commitViaAgent(ctx context.Context, worktreePath, repo, sessionID string) error {
	const fallbackCommitMsg = "chore: commit residual changes before push"

	commitCfg := adapter.CommitConfig{
		Strategy:      "single",
		MessageFormat: "ai-generated",
	}
	if s.cfg != nil {
		commitCfg.Strategy = string(s.cfg.Commit.Strategy)
		commitCfg.MessageFormat = string(s.cfg.Commit.MessageFormat)
		commitCfg.MessageTemplate = s.cfg.Commit.MessageTemplate
	}

	commitInstructions := buildCommitSection(commitCfg)
	if commitInstructions == "" {
		return gitStageAndCommit(ctx, worktreePath, fallbackCommitMsg)
	}

	systemPrompt := "The implementation session is complete but there are residual uncommitted changes in the worktree.\n\n" +
		"Stage all changes and make a single commit with an appropriate message. Do not modify any files.\n\n" +
		commitInstructions

	opts := adapter.SessionOpts{
		Mode:         adapter.SessionModeAgent,
		WorktreePath: worktreePath,
		SystemPrompt: systemPrompt,
		UserPrompt:   "Commit the residual changes now.",
		AllowPush:    false,
	}
	sess, err := s.harness.StartSession(ctx, opts)
	if err != nil {
		slog.Warn("failed to start commit agent session, falling back to generic message",
			"repo", repo, "error", err)
		return gitStageAndCommit(ctx, worktreePath, fallbackCommitMsg)
	}
	defer sess.Abort(ctx)

	go s.forwardEvents(ctx, sess.Events(), "", "")

	commitCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := sess.Wait(commitCtx); err != nil {
		slog.Warn("commit agent session failed, falling back to generic message",
			"repo", repo, "error", err)
		sess.Abort(ctx) // kill agent before touching worktree ourselves; deferred Abort is idempotent
		return gitStageAndCommit(ctx, worktreePath, fallbackCommitMsg)
	}
	return nil
}

// emitWorkItemCompleted publishes a work_item.completed event to the event bus.
// Adapters use this to transition draft PRs to ready, update tracker state, etc.
func (s *ImplementationService) emitWorkItemCompleted(
	ctx context.Context,
	workItem *domain.Session,
	workspaceID string,
	sessions []SessionResult,
	repoPaths map[string]string,
	branch string,
	subPlans []domain.TaskPlan,
) error {
	payload := map[string]any{
		"workspace_id":    workspaceID,
		"work_item_id":    workItem.ID,
		"external_id":     workItem.ExternalID,
		"work_item_title": workItem.Title,
		"branch":          branch,
	}

	// Resolve review context from the first session's worktree.
	for _, sess := range sessions {
		if sess.WorktreePath == "" {
			continue
		}
		// Use the bare repo path for review context resolution (same as ensureWorktree).
		if bareRepo, ok := repoPaths[sess.Repository]; ok {
			reviewCtx, err := remotedetect.ResolveReviewContext(ctx, bareRepo)
			if err != nil {
				slog.Warn("failed to resolve review context for completion event", "repo", sess.Repository, "error", err)
				continue
			}
			if reviewCtx.Review.BaseRepo.Owner != "" || reviewCtx.Review.BaseRepo.Repo != "" || reviewCtx.Review.HeadRepo.Owner != "" || reviewCtx.Review.HeadRepo.Repo != "" {
				payload["review"] = reviewCtx.Review
			}
			break
		}
	}

	// Include sub-plan content from the first sub-plan.
	if len(subPlans) > 0 {
		payload["sub_plan"] = subPlans[0].Content
	}

	// Include external IDs (source item IDs prefixed by provider).
	if externalIDs := workItemEventExternalIDs(*workItem); len(externalIDs) > 0 {
		payload["external_ids"] = externalIDs
	}

	// Include tracker references.
	if trackerRefs := trackerRefsFromMetadata(workItem.Metadata); len(trackerRefs) > 0 {
		payload["tracker_refs"] = trackerRefs
	}

	return s.publishEvent(ctx, domain.EventWorkItemCompleted, workspaceID, payload)
}

// workItemEventExternalIDs builds the external_ids list for the completion event payload.
// It prefixes source item IDs with the provider namespace and deduplicates.
func workItemEventExternalIDs(workItem domain.Session) []string {
	seen := make(map[string]struct{})
	ids := make([]string, 0, len(workItem.SourceItemIDs)+1)
	appendID := func(id string) {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			return
		}
		if _, ok := seen[trimmed]; ok {
			return
		}
		seen[trimmed] = struct{}{}
		ids = append(ids, trimmed)
	}
	switch {
	case workItem.Source == "github" && workItem.SourceScope == domain.ScopeIssues:
		for _, id := range workItem.SourceItemIDs {
			appendID("gh:issue:" + id)
		}
	case workItem.Source == "gitlab" && workItem.SourceScope == domain.ScopeIssues:
		for _, id := range workItem.SourceItemIDs {
			appendID("gl:issue:" + id)
		}
	}
	appendID(workItem.ExternalID)
	return ids
}

func (s *ImplementationService) emitImplementationStarted(ctx context.Context, plan *domain.Plan, workItem *domain.Session, workspaceID string) error {
	return s.publishEvent(ctx, domain.EventImplementationStarted, workspaceID, struct {
		PlanID   string          `json:"plan_id"`
		WorkItem *domain.Session `json:"work_item"`
	}{
		PlanID:   plan.ID,
		WorkItem: workItem,
	})
}

func (s *ImplementationService) emitSessionStarted(ctx context.Context, session *domain.Task, workspaceID string) error {
	return s.publishEvent(ctx, domain.EventAgentSessionStarted, workspaceID, newSessionEventPayload(session))
}

func (s *ImplementationService) emitSessionCompleted(ctx context.Context, session *domain.Task, workspaceID string) error {
	return s.publishEvent(ctx, domain.EventAgentSessionCompleted, workspaceID, newSessionEventPayload(session))
}

func (s *ImplementationService) emitSessionFailed(ctx context.Context, session *domain.Task, errMsg string, workspaceID string) error {
	payload := struct {
		sessionEventPayload
		Error string `json:"error"`
	}{
		sessionEventPayload: newSessionEventPayload(session),
		Error:               errMsg,
	}
	return s.publishEvent(ctx, domain.EventAgentSessionFailed, workspaceID, payload)
}

func (s *ImplementationService) emitSessionInterrupted(ctx context.Context, session *domain.Task, workspaceID string) error {
	return s.publishEvent(ctx, domain.EventAgentSessionInterrupted, workspaceID, newSessionEventPayload(session))
}

// lastSessionForSubPlan returns the most recent session for a sub-plan,
// regardless of phase or status. Used by the two-stage retry model:
// the last session's phase determines whether to retry implementation
// or review.
func (s *ImplementationService) lastSessionForSubPlan(ctx context.Context, subPlanID string) *domain.Task {
	tasks, err := s.sessionSvc.ListBySubPlanID(ctx, subPlanID)
	if err != nil {
		slog.Warn("failed to list sessions for sub-plan",
			"error", err, "sub_plan_id", subPlanID)
		return nil
	}

	var latest *domain.Task
	for i := range tasks {
		if latest == nil || tasks[i].CreatedAt.After(latest.CreatedAt) {
			t := tasks[i]
			latest = &t
		}
	}
	return latest
}

// latestCompletedImplSession returns the most recent completed implementation
// session for a sub-plan, or nil if none exists. Used by the review-retry
// path to find the impl session whose output should be reviewed.
func (s *ImplementationService) latestCompletedImplSession(ctx context.Context, subPlanID string) *domain.Task {
	tasks, err := s.sessionSvc.ListBySubPlanID(ctx, subPlanID)
	if err != nil {
		slog.Warn("failed to list sessions for sub-plan, treating as no completed impl session",
			"error", err, "sub_plan_id", subPlanID)
		return nil
	}

	var latest *domain.Task
	for i := range tasks {
		t := tasks[i]
		if t.Phase == domain.TaskPhaseImplementation && t.Status == domain.AgentSessionCompleted {
			if latest == nil || t.CreatedAt.After(latest.CreatedAt) {
				latest = &t
			}
		}
	}
	return latest
}

// persistSubPlanStatus sets the sub-plan status, timestamps the update, and
// persists it. Errors are logged as warnings since the in-memory state is
// already consistent and the next Implement call will reconcile.
func (s *ImplementationService) persistSubPlanStatus(ctx context.Context, sp *domain.TaskPlan, status domain.TaskPlanStatus) {
	if err := s.planSvc.TransitionSubPlan(ctx, sp.ID, status); err != nil {
		slog.Warn("failed to persist sub-plan status",
			"error", err,
			"sub_plan_id", sp.ID,
			"status", status)
	}
	// Always update in-memory state so the orchestrator can continue.
	sp.Status = status
	sp.UpdatedAt = time.Now()
}

// Helper functions

const durableCleanupTimeout = 30 * time.Second

func durableCleanupContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), durableCleanupTimeout)
}

func deleteOrFailPendingSession(parent context.Context, sessionSvc *service.TaskService, sessionID string, exitCode *int) {
	cleanupCtx, cleanupCancel := durableCleanupContext(parent)
	defer cleanupCancel()

	err := sessionSvc.Delete(cleanupCtx, sessionID)
	if err == nil {
		return
	}
	slog.Warn("failed to delete pending session during cleanup", "error", err, "session_id", sessionID)

	if err := sessionSvc.Fail(cleanupCtx, sessionID, exitCode); err != nil {
		slog.Warn("failed to terminalize pending session during cleanup", "error", err, "session_id", sessionID)
	}
}

func failSessionDurably(parent context.Context, sessionSvc *service.TaskService, sessionID string, exitCode *int) error {
	cleanupCtx, cleanupCancel := durableCleanupContext(parent)
	defer cleanupCancel()
	return sessionSvc.Fail(cleanupCtx, sessionID, exitCode)
}

// interruptSessionDurably marks a session as interrupted using a context detached from
// parent cancellation, ensuring the DB write completes even if the pipeline is shutting down.
func interruptSessionDurably(parent context.Context, sessionSvc *service.TaskService, sessionID string) error {
	cleanupCtx, cleanupCancel := durableCleanupContext(parent)
	defer cleanupCancel()
	return sessionSvc.Interrupt(cleanupCtx, sessionID)
}

func completeSessionDurably(parent context.Context, sessionSvc *service.TaskService, sessionID string) error {
	cleanupCtx, cleanupCancel := durableCleanupContext(parent)
	defer cleanupCancel()
	return sessionSvc.Complete(cleanupCtx, sessionID)
}

func marshalJSONOrEmpty(eventType string, v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		slog.Warn("failed to marshal event payload",
			"event_type", eventType,
			"payload_type", fmt.Sprintf("%T", v),
			"error", err)
		return "{}"
	}
	return string(b)
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

func ptrInt(i int) *int {
	return &i
}

// gitPushBranch pushes branch to remote using plain git. It is used immediately
// after worktree creation to establish the branch on the remote so that lifecycle
// adapters (GitHub, GitLab) can create a draft PR/MR without a 422 "head invalid".
func gitPushBranch(ctx context.Context, repoDir, remote, branch string) error {
	cmd := exec.CommandContext(ctx, "git", "push", remote, branch)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git push %s %s: %w (output: %s)", remote, branch, err, strings.TrimSpace(string(out)))
	}
	return nil
}
