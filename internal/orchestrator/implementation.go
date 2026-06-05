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
	"github.com/beeemT/substrate/internal/worktree"
)

// ImplementationService orchestrates the implementation phase after plan approval.
// It manages wave-based execution of sub-plans, worktree creation, and agent sessions.
type ImplementationService struct {
	cfg              *config.Config
	harness          adapter.AgentHarness
	gitClient        *gitwork.Client
	eventBus         event.Publisher
	planSvc          *service.PlanService
	workItemSvc      *service.SessionService
	sessionSvc       *service.AgentSessionService
	continuationSvc  *service.AgentSessionContinuationService
	workspaceSvc     *service.WorkspaceService
	registry         SessionRegistry
	reviewPipeline   *ReviewPipeline
	foremanHarness   adapter.AgentHarness
	questionSvc      *service.QuestionService
	reviewSvc        *service.ReviewService
	questionRouter   *QuestionRouter // session-scoped; recreated per Implement call
	sessTimeout      time.Duration
	hookRegistry     *worktree.HookRegistry
	planningSvc      *PlanningService
	questionRouterMu sync.Mutex // guards questionRouter read in forwardEvents
}
type AgentGraphTrigger string

const (
	AgentGraphTriggerResumeInterrupted AgentGraphTrigger = "resume_interrupted"
	AgentGraphTriggerRetryFailed       AgentGraphTrigger = "retry_failed"
	AgentGraphTriggerFollowUpCompleted AgentGraphTrigger = "follow_up_completed"
	AgentGraphTriggerFollowUpFailed    AgentGraphTrigger = "follow_up_failed"
	AgentGraphTriggerAutoReimpl        AgentGraphTrigger = "auto_reimpl"
)

const implementationReviewContinuationKind = "implementation_review"

type ContinuationContext struct {
	CompletedImplementationID string
	SupersededLeafID          string
	Trigger                   AgentGraphTrigger
	FirstReviewParentID       string
}

type AgentGraphIntent struct {
	SourceSessionID   string
	WorkItemID        string
	SubPlanID         string
	Trigger           AgentGraphTrigger
	Feedback          string
	CurrentInstanceID string
}

type AgentGraphRunResult struct {
	SourceSession domain.AgentSession
	NewSession    domain.AgentSession
	Trigger       AgentGraphTrigger
}

type ResumeRetryMode string

const (
	ResumeRetryModeResumeInterrupted ResumeRetryMode = "resume_interrupted"
	ResumeRetryModeRetryFailed       ResumeRetryMode = "retry_failed"
)

type ResumeRetrySkippedLeaf struct {
	SessionID string
	Kind      domain.AgentSessionKind
	Status    domain.AgentSessionStatus
	Reason    string
}

type ResumeRetryDispatchResult struct {
	Accepted int
	Skipped  []ResumeRetrySkippedLeaf
}

type ContinuationRecoverySkipped struct {
	ContinuationID string
	SessionID      string
	Status         domain.AgentSessionContinuationStatus
	Reason         string
}

type ContinuationRecoveryResult struct {
	Recovered int
	Skipped   []ContinuationRecoverySkipped
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
	eventBus event.Publisher,
	planSvc *service.PlanService,
	workItemSvc *service.SessionService,
	sessionSvc *service.AgentSessionService,
	continuationSvc *service.AgentSessionContinuationService,
	workspaceSvc *service.WorkspaceService,
	registry SessionRegistry,
	reviewPipeline *ReviewPipeline,
	foremanHarness adapter.AgentHarness,
	questionSvc *service.QuestionService,
	reviewSvc *service.ReviewService,
	hookRegistry *worktree.HookRegistry,
) *ImplementationService {
	implCfg := DefaultImplementationConfig()
	// questionRouter is created fresh per Implement call; foreman is looked up dynamically per question.
	questionRouter := NewQuestionRouter(questionSvc, sessionSvc, registry, eventBus)
	return &ImplementationService{
		cfg:             cfg,
		harness:         harness,
		gitClient:       gitClient,
		eventBus:        eventBus,
		planSvc:         planSvc,
		workItemSvc:     workItemSvc,
		sessionSvc:      sessionSvc,
		continuationSvc: continuationSvc,
		workspaceSvc:    workspaceSvc,
		registry:        registry,
		reviewPipeline:  reviewPipeline,
		foremanHarness:  foremanHarness,
		questionSvc:     questionSvc,
		reviewSvc:       reviewSvc,
		questionRouter:  questionRouter,
		sessTimeout:     implCfg.SessionTimeout,
		hookRegistry:    hookRegistry,
	}
}

func (s *ImplementationService) SetPlanningService(planningSvc *PlanningService) {
	s.planningSvc = planningSvc
}

// BeginForeman starts a foreman for the work item, tied to the plan.
// Called when implementation starts (from TUI after plan approval, before Implement()).
// Creates a fresh *Foreman instance registered in SessionRegistry.
func (s *ImplementationService) BeginForeman(ctx context.Context, workItemID, planID string) error {
	// Check if foreman already exists for this work item
	if existing := s.registry.GetForeman(workItemID); existing != nil {
		if existing.IsRunning() {
			// Already running for this work item - no-op
			return nil
		}
	}

	// Create new foreman instance
	foreman := NewForeman(s.cfg, s.foremanHarness, s.planSvc, s.questionSvc, s.sessionSvc, s.workItemSvc, s.eventBus)

	// Start the foreman
	if err := foreman.Start(ctx, planID, ""); err != nil {
		return fmt.Errorf("start foreman: %w", err)
	}

	// Register in session registry
	s.registry.RegisterForeman(workItemID, foreman)

	return nil
}

// EndForeman stops the foreman for the work item.
// Called when implementation completes, pauses for review, or is abandoned.
func (s *ImplementationService) EndForeman(ctx context.Context, workItemID string) error {
	foreman := s.registry.GetForeman(workItemID)
	if foreman == nil {
		return nil
	}

	// Stop with durable context to ensure completion
	stopCtx, stopCancel := durableCleanupContext(ctx)
	defer stopCancel()

	if err := foreman.Stop(stopCtx); err != nil {
		slog.Warn("failed to stop foreman", "error", err, "work_item_id", workItemID)
		// Continue with deregistration even on error
	}

	// Deregister from session registry
	s.registry.DeregisterForeman(workItemID)

	return nil
}

func (s *ImplementationService) submitForHumanReview(ctx context.Context, workItemID string) error {
	// No agents are running while the operator decides from the review action
	// card. Stop the Foreman before publishing the reviewing state so the
	// persisted Foreman session is not left running until the next extension.
	if err := s.EndForeman(ctx, workItemID); err != nil {
		slog.Warn("failed to stop foreman before human review",
			"error", err,
			"work_item_id", workItemID)
	}
	if err := s.workItemSvc.SubmitForReview(ctx, workItemID); err != nil {
		return fmt.Errorf("transition work item to reviewing: %w", err)
	}
	return nil
}

// RestartForemanWithPlan stops and starts the foreman with the current plan.
// Called after replanning to update foreman's context with new plan/FAQ.
func (s *ImplementationService) RestartForemanWithPlan(ctx context.Context, workItemID, planID string) error {
	plan, err := s.planSvc.GetPlan(ctx, planID)
	if err != nil || plan.WorkItemID != workItemID {
		if err != nil {
			slog.Warn("failed to load requested plan for foreman restart; falling back to active work item plan",
				"error", err, "work_item_id", workItemID, "plan_id", planID)
		} else {
			slog.Warn("requested plan belongs to a different work item; falling back to active work item plan",
				"work_item_id", workItemID, "plan_id", planID, "plan_work_item_id", plan.WorkItemID)
		}
		plan, err = s.planSvc.GetPlanByWorkItemID(ctx, workItemID)
		if err != nil {
			return fmt.Errorf("load current plan for work item: %w", err)
		}
	}

	// End existing foreman only after resolving the replacement plan so a stale
	// TUI cache cannot leave running agents without their question router.
	if err := s.EndForeman(ctx, workItemID); err != nil {
		slog.Warn("error ending foreman before restart", "error", err)
	}

	return s.BeginForeman(ctx, workItemID, plan.ID)
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
	Status       domain.AgentSessionStatus
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

// RepoFinalizationResult contains the result of finalizing a single repo's branch.
// Used by the orchestrator to emit EventSubPlanPRReady per sub-plan after successful push.
type RepoFinalizationResult struct {
	SubPlanID    string
	Repository   string
	WorktreePath string
	Branch       string
	Review       domain.ReviewRef
	Err          error
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

	// Note: Foreman lifecycle is managed externally via BeginForeman/EndForeman.
	// The foreman (if any) is registered in the session registry and looked up
	// dynamically by the question router.

	// 7. Transition work item to implementing once non-mutating preflight succeeds.
	// On retry (work item already in implementing after RetryFailedWorkItem), skip.
	if workItem.State != domain.SessionImplementing {
		if err := s.workItemSvc.StartImplementation(ctx, workItem.ID); err != nil {
			return nil, fmt.Errorf("transition work item to implementing: %w", err)
		}
	}
	implementingStarted = true
	// EventWorkItemImplementing is emitted by StartImplementation → Transition → emitStateChange

	// 9. Generate branch name
	branch := GenerateBranchName(workItem.ExternalID, workItem.Title)

	// Reset retryable sub-plans before building execution state so retry waves
	// reflect the statuses that will actually be executed.
	s.resetRetryableSubPlans(ctx, subPlans)

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

		// Execute sub-plans in this wave concurrently. A sub-plan failure must
		// not cancel siblings; the parent context still interrupts all sessions
		// when the operator quits or the app shuts down.
		sessionResults, warnings := s.executeWave(ctx, wave, &workspace, &plan, &workItem, branch, worktreePaths, state)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
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

	// Determine final work item state based on persisted sub-plan statuses.
	// Reading from DB ensures correctness even if the in-memory ReviewResults
	// map is incomplete (e.g. after crash recovery or focused retry).
	hasEscalated := false
	hasFailed := false
	freshSubPlans, err := s.planSvc.ListSubPlansByPlanID(ctx, planID)
	if err != nil {
		return result, fmt.Errorf("list sub-plans for final state derivation: %w", err)
	}
	for _, sp := range freshSubPlans {
		switch sp.Status {
		case domain.SubPlanFailed:
			hasFailed = true
		case domain.SubPlanEscalated:
			hasEscalated = true
		}
	}

	switch {
	case hasFailed || !state.AllWavesCompleted():
		cleanupCtx, cleanupCancel := durableCleanupContext(ctx)
		if failErr := s.workItemSvc.FailWorkItem(cleanupCtx, workItem.ID); failErr != nil {
			slog.Warn("failed to transition work item to failed", "error", failErr)
		}
		cleanupCancel()
	case hasEscalated:
		// At least one repo needs human decision. Stop Foreman and transition to reviewing.
		cleanupCtx, cleanupCancel := durableCleanupContext(ctx)
		if reviewErr := s.submitForHumanReview(cleanupCtx, workItem.ID); reviewErr != nil {
			slog.Warn("failed to submit work item for human review", "error", reviewErr)
		}
		cleanupCancel()
	default:
		// All repos passed review (or no review pipeline).
		// Commit any residual changes, push to remote, then complete.
		if err := s.finalizeCompletedWorkItem(ctx, &workItem, workspace.ID, result.Sessions, repoPaths, branch, subPlans); err != nil {
			slog.Warn("failed to finalize completed work item", "error", err)
		}
	}

	return result, nil
}

// resetRetryableSubPlans resets sub-plans that should be picked up by a bulk
// retry. In addition to persisted failed/in-progress/escalated sub-plan states,
// it treats failed or interrupted graph leaves as retryable even when the
// sub-plan status itself is stale.
func (s *ImplementationService) resetRetryableSubPlans(ctx context.Context, subPlans []domain.TaskPlan) {
	for i := range subPlans {
		if s.subPlanNeedsRetry(ctx, subPlans[i]) {
			s.resetSubPlanForRetry(ctx, &subPlans[i])
		}
	}
}

func (s *ImplementationService) subPlanNeedsRetry(ctx context.Context, subPlan domain.TaskPlan) bool {
	switch subPlan.Status {
	case domain.SubPlanFailed, domain.SubPlanInProgress, domain.SubPlanEscalated:
		return true
	}

	sessions, err := s.sessionSvc.ListBySubPlanID(ctx, subPlan.ID)
	if err != nil {
		slog.Warn("failed to list sessions for retry reconciliation",
			"error", err,
			"sub_plan_id", subPlan.ID)
		return false
	}
	for _, leaf := range domain.LeafAgentSessions(sessions) {
		if leaf.Status == domain.AgentSessionFailed || leaf.Status == domain.AgentSessionInterrupted {
			return true
		}
	}
	return false
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
	var wg sync.WaitGroup

	for _, sp := range wave {
		spCopy := sp
		wg.Add(1)

		go func() {
			defer wg.Done()
			result, warning := s.executeSubPlan(ctx, spCopy, workspace, plan, workItem, branch, worktreePaths, state)

			mu.Lock()
			results = append(results, result)
			if warning != nil {
				warnings = append(warnings, *warning)
			}
			mu.Unlock()
		}()
	}

	// Wait for all sub-plans in the wave. Individual failures are represented
	// in results; they do not cancel sibling goroutines.
	wg.Wait()

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

	// Crash recovery: if the most recent session for this sub-plan is a
	// non-completed review session, the review agent crashed or was interrupted
	// before producing a terminal decision — skip re-implementation and retry the
	// review directly. A completed review session with critiques is different:
	// the human "continue reviewing" action must resume with an implementation
	// child that addresses those critiques, especially when the automatic review
	// loop exhausted its cycle budget.
	if s.reviewPipeline != nil {
		if last := s.lastSessionForSubPlan(ctx, subPlan.ID); last != nil && last.Kind == domain.AgentSessionKindReview && last.Status != domain.AgentSessionCompleted {
			prevImpl := s.latestCompletedImplSession(ctx, subPlan.ID)
			if prevImpl != nil {
				slog.Info("skipping implementation, retrying review for sub-plan",
					"sub_plan_id", subPlan.ID, "prev_impl_session_id", prevImpl.ID)
				return s.runReviewOnExistingImpl(ctx, subPlan, workspace, plan, workItem, worktreePaths, *prevImpl, result, state, last.ID,
					"Retrying review with existing implementation")
			}
			slog.Warn("review retry needed but no completed impl session found, falling back to full implementation",
				"sub_plan_id", subPlan.ID, "last_session_id", last.ID)
		}
	}

	// Tier 3: extend the crash-recovery rule. Even when the most recent session
	// is NOT a review (e.g. a failed/interrupted impl from yesterday's bulk
	// retry, or a fresh InProgress retry of a previously-completed work item),
	// if there is already a completed impl session for this sub-plan and:
	//   - it has no successful review yet, AND
	//   - there is no outstanding critique to feed back into a re-impl,
	// then the right next step is to review that existing impl, not run a
	// fresh one. This closes the architectural gap that caused the bulk-retry
	// hang on 2026-05-28: Implement() entering runImplementation with prevImpl
	// + ResumeInfo + empty critique → bridge resumed but had nothing to do.
	//
	// This mirrors the graph continuation choice: if a completed implementation
	// can be reviewed directly, prefer continuation over starting another
	// implementation child with no new feedback.
	if s.reviewPipeline != nil {
		last := s.lastSessionForSubPlan(ctx, subPlan.ID)
		if prevImpl := s.latestCompletedImplSession(ctx, subPlan.ID); prevImpl != nil {
			outstandingCritique := s.implHasOutstandingCritique(ctx, prevImpl.ID)
			passedReview := s.implHasPassedReview(ctx, prevImpl.ID)
			switch {
			case passedReview:
				result.Status = domain.AgentSessionCompleted
				result.SessionID = prevImpl.ID
				result.WorktreePath = prevImpl.WorktreePath
				result.Summary = "Existing implementation already passed review"
				result.CompletedAt = ptrTime(time.Now())
				state.CompleteSubPlan(subPlan.ID, time.Now().UnixNano())
				s.persistSubPlanStatus(ctx, &subPlan, domain.SubPlanCompleted)
				return result, nil
			case outstandingCritique:
				// Auto-reimpl path: critique feedback exists; runImplementation
				// will resume the impl session and feed the critique back in.
				// Nothing to short-circuit here — fall through.
			default:
				// Completed impl with no successful review and no outstanding
				// critique. Skip the re-impl and review the existing impl. If this
				// is replacing a stale failed/interrupted leaf, link the review to
				// that leaf so graph-derived labels stop surfacing it.
				reviewParentSessionID := prevImpl.ID
				if last != nil {
					reviewParentSessionID = last.ID
				}
				slog.Info("skipping implementation, reviewing existing completed impl with no outstanding review",
					"sub_plan_id", subPlan.ID, "prev_impl_session_id", prevImpl.ID)
				return s.runReviewOnExistingImpl(ctx, subPlan, workspace, plan, workItem, worktreePaths, *prevImpl, result, state, reviewParentSessionID,
					"Reviewing existing completed implementation")
			}
		}
	}

	// Load any outstanding review critique context (empty for first-time implementations).
	critiqueFeedback := s.loadCritiqueFeedback(ctx, subPlan.ID)
	prevImpl := s.latestCompletedImplSession(ctx, subPlan.ID)

	// Determine the parent session ID for the agent-session graph:
	//   - First implementation: empty (no parent).
	//   - Retry after a failed/interrupted impl OR reimplementation that follows
	//     a review (e.g. after orchestrator restart that loaded saved critique
	//     feedback): the latest existing session for this sub-plan is the
	//     parent. lastSessionForSubPlan returns the most recent session
	//     regardless of kind/status, which captures both the "retry failed
	//     impl" and "reimpl after review" cases.
	parentSessionID := ""
	if last := s.lastSessionForSubPlan(ctx, subPlan.ID); last != nil {
		parentSessionID = last.ID
	}

	// Run implementation (fresh or with critique context from prior review).
	implSession, err := s.runImplementation(ctx, subPlan, workspace, plan, workItem, worktreePath, critiqueFeedback, prevImpl, parentSessionID, false)
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
			s.persistSubPlanStatus(ctx, &subPlan, domain.SubPlanEscalated)
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
	implSession domain.AgentSession,
	subPlan domain.TaskPlan,
	workspace *domain.Workspace,
	plan *domain.Plan,
	workItem *domain.Session,
	worktreePaths map[string]string,
) *SubPlanOutcome {
	return s.reviewLoopWithFirstReviewParent(ctx, implSession, subPlan, workspace, plan, workItem, worktreePaths, "")
}

func (s *ImplementationService) reviewLoopWithFirstReviewParent(
	ctx context.Context,
	implSession domain.AgentSession,
	subPlan domain.TaskPlan,
	workspace *domain.Workspace,
	plan *domain.Plan,
	workItem *domain.Session,
	worktreePaths map[string]string,
	firstReviewParentSessionID string,
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
		// resets after each reimplementation (new implementation session). This
		// pre-review guard handles resumed loops that are already over budget
		// before starting another review.
		if s.cfg.Review.MaxCycles != nil && outcome.Cycles > *s.cfg.Review.MaxCycles {
			outcome.Escalated = true
			return outcome
		}

		var reviewResult *ReviewResult
		var err error
		if outcome.Cycles == 1 && firstReviewParentSessionID != "" {
			reviewResult, err = s.reviewPipeline.ReviewSessionWithParent(ctx, currentSession, firstReviewParentSessionID)
		} else {
			reviewResult, err = s.reviewPipeline.ReviewSession(ctx, currentSession)
		}
		if err != nil {
			slog.Warn("review agent session failed", "error", err,
				"agent_session_id", currentSession.ID, "sub_plan", subPlan.ID)
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

		// The review budget counts completed review cycles. If the last allowed
		// review still found critiques, escalate immediately on that review
		// result. Starting another implementation would spend work the system has
		// already decided cannot be auto-reviewed, leaving the leaf implementation
		// with no review result and no useful human decision context.
		if s.cfg.Review.MaxCycles != nil && outcome.Cycles >= *s.cfg.Review.MaxCycles {
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
		// The reimplementation is created from the review's critique, so the
		// new impl's parent in the agent-session graph is the review session
		// that produced the critique (not the impl that was reviewed).
		newSession, err := s.runImplementation(ctx, subPlan, workspace, plan, workItem, worktreePath, feedback, &currentSession, reviewResult.SessionID, true)
		if err != nil {
			slog.Warn("reimplementation failed", "error", err,
				"sub_plan", subPlan.ID, "cycle", outcome.Cycles)
			outcome.Failed = true
			return outcome
		}
		currentSession = newSession
	}
}

// ResumeRetryLeavesForWorkItem dispatches graph-managed resume or retry work
// for current leaves in a work item. Implementation and review leaves use the
// agent-session graph. Planning leaves are routed to PlanningService when it is
// wired. Foreman is restarted once for the current plan before implementation or
// review work so resumed agents have question routing available.
func (s *ImplementationService) ResumeRetryLeavesForWorkItem(ctx context.Context, workItemID string, mode ResumeRetryMode, instanceID string) (ResumeRetryDispatchResult, error) {
	if workItemID == "" {
		return ResumeRetryDispatchResult{}, fmt.Errorf("work item id is required")
	}
	sessions, err := s.sessionSvc.ListByWorkItemID(ctx, workItemID)
	if err != nil {
		return ResumeRetryDispatchResult{}, fmt.Errorf("list work item sessions: %w", err)
	}

	var leaves []domain.AgentSession
	var trigger AgentGraphTrigger
	switch mode {
	case ResumeRetryModeResumeInterrupted:
		leaves = domain.ResumableAgentSessionLeaves(sessions)
		trigger = AgentGraphTriggerResumeInterrupted
	case ResumeRetryModeRetryFailed:
		leaves = domain.RetryableAgentSessionLeaves(sessions)
		trigger = AgentGraphTriggerRetryFailed
	default:
		return ResumeRetryDispatchResult{}, fmt.Errorf("unsupported resume/retry mode %q", mode)
	}

	result := ResumeRetryDispatchResult{}
	foremanStarted := false
	needsForeman := false
	for _, leaf := range leaves {
		if leaf.Kind == domain.AgentSessionKindImplementation || leaf.Kind == domain.AgentSessionKindReview || leaf.Kind == domain.AgentSessionKindForeman {
			needsForeman = true
			break
		}
	}
	if needsForeman {
		plan, err := s.planSvc.GetPlanByWorkItemID(ctx, workItemID)
		if err != nil {
			return result, fmt.Errorf("load work item plan before graph resume: %w", err)
		}
		if plan.Status == domain.PlanApproved {
			if err := s.BeginForeman(ctx, workItemID, plan.ID); err != nil {
				return result, fmt.Errorf("start foreman before graph resume: %w", err)
			}
			foremanStarted = true
		}
	}

	for _, leaf := range leaves {
		intent := AgentGraphIntent{
			SourceSessionID:   leaf.ID,
			WorkItemID:        leaf.WorkItemID,
			SubPlanID:         leaf.SubPlanID,
			Trigger:           trigger,
			CurrentInstanceID: instanceID,
		}
		switch leaf.Kind {
		case domain.AgentSessionKindImplementation:
			go func(intent AgentGraphIntent, sessionID string) {
				if _, err := s.StartImplementationGraphRun(context.WithoutCancel(ctx), intent); err != nil {
					slog.Error("dispatch implementation graph run failed", "error", err, "agent_session_id", sessionID)
				}
			}(intent, leaf.ID)
			result.Accepted++
		case domain.AgentSessionKindReview:
			go func(intent AgentGraphIntent, sessionID string) {
				if _, err := s.RetryReviewLeaf(context.WithoutCancel(ctx), intent); err != nil {
					slog.Error("dispatch review graph run failed", "error", err, "agent_session_id", sessionID)
				}
			}(intent, leaf.ID)
			result.Accepted++
		case domain.AgentSessionKindPlanning:
			if mode != ResumeRetryModeResumeInterrupted || leaf.Status != domain.AgentSessionInterrupted {
				result.Skipped = append(result.Skipped, ResumeRetrySkippedLeaf{SessionID: leaf.ID, Kind: leaf.Kind, Status: leaf.Status, Reason: "planning retry is not supported by bulk graph orchestration"})
				continue
			}
			if s.planningSvc == nil {
				result.Skipped = append(result.Skipped, ResumeRetrySkippedLeaf{SessionID: leaf.ID, Kind: leaf.Kind, Status: leaf.Status, Reason: "planning service is not configured"})
				continue
			}
			if _, err := s.planningSvc.ResumeInterruptedPlanning(ctx, leaf, ""); err != nil {
				return result, fmt.Errorf("resume interrupted planning session %s: %w", leaf.ID, err)
			}
			result.Accepted++
		case domain.AgentSessionKindForeman:
			if !foremanStarted {
				result.Skipped = append(result.Skipped, ResumeRetrySkippedLeaf{SessionID: leaf.ID, Kind: leaf.Kind, Status: leaf.Status, Reason: "foreman restart was not required"})
				continue
			}
			result.Accepted++
		case domain.AgentSessionKindManual:
			result.Skipped = append(result.Skipped, ResumeRetrySkippedLeaf{SessionID: leaf.ID, Kind: leaf.Kind, Status: leaf.Status, Reason: "manual sessions are not graph-managed"})
		default:
			result.Skipped = append(result.Skipped, ResumeRetrySkippedLeaf{SessionID: leaf.ID, Kind: leaf.Kind, Status: leaf.Status, Reason: "unsupported agent session kind"})
		}
	}
	return result, nil
}

// StartImplementationGraphRun starts an implementation child from a current
// implementation graph leaf, waits for that child, then runs the graph
// continuation. It is the graph-aware entry point for implementation
// resume/retry/follow-up paths; callers that must not block should dispatch it
// from a background goroutine.
func (s *ImplementationService) StartImplementationGraphRun(ctx context.Context, intent AgentGraphIntent) (AgentGraphRunResult, error) {
	if intent.SourceSessionID == "" {
		return AgentGraphRunResult{}, fmt.Errorf("source session id is required")
	}
	source, err := s.sessionSvc.Get(ctx, intent.SourceSessionID)
	if err != nil {
		return AgentGraphRunResult{}, fmt.Errorf("get source session: %w", err)
	}
	if intent.WorkItemID != "" && source.WorkItemID != intent.WorkItemID {
		return AgentGraphRunResult{}, fmt.Errorf("agent session %s belongs to work item %s, not %s", source.ID, source.WorkItemID, intent.WorkItemID)
	}
	if intent.SubPlanID != "" && source.SubPlanID != intent.SubPlanID {
		return AgentGraphRunResult{}, fmt.Errorf("agent session %s belongs to sub-plan %s, not %s", source.ID, source.SubPlanID, intent.SubPlanID)
	}
	trigger := intent.Trigger
	if trigger == "" {
		trigger = defaultImplementationGraphTrigger(source.Status)
	}
	sessions, err := s.sessionSvc.ListByWorkItemID(ctx, source.WorkItemID)
	if err != nil {
		return AgentGraphRunResult{}, fmt.Errorf("list work item sessions: %w", err)
	}
	if !domain.IsLeafAgentSessionID(sessions, source.ID) {
		return AgentGraphRunResult{}, service.ErrAgentSessionNotLeaf
	}

	switch source.Kind {
	case domain.AgentSessionKindImplementation:
		if err := validateImplementationGraphTrigger(source, trigger); err != nil {
			return AgentGraphRunResult{}, err
		}
		return s.startImplementationGraphRunFromSource(ctx, intent, source, source, trigger)
	case domain.AgentSessionKindReview:
		if trigger != AgentGraphTriggerFollowUpCompleted {
			return AgentGraphRunResult{}, fmt.Errorf("review session %s only supports completed follow-up through implementation graph (trigger: %s)", source.ID, trigger)
		}
		if source.Status != domain.AgentSessionCompleted {
			return AgentGraphRunResult{}, fmt.Errorf("review session %s is not completed (status: %s)", source.ID, source.Status)
		}
		impl, err := nearestCompletedImplementationAncestor(source, sessions)
		if err != nil {
			return AgentGraphRunResult{}, err
		}
		if impl.WorkItemID != source.WorkItemID || impl.SubPlanID != source.SubPlanID {
			return AgentGraphRunResult{}, fmt.Errorf("implementation ancestor %s does not match review leaf %s", impl.ID, source.ID)
		}
		return s.startImplementationGraphRunFromSource(ctx, intent, source, impl, trigger)
	default:
		return AgentGraphRunResult{}, fmt.Errorf("session %s is not an implementation or completed review session (kind: %s)", source.ID, source.Kind)
	}
}

func (s *ImplementationService) startImplementationGraphRunFromSource(ctx context.Context, intent AgentGraphIntent, source domain.AgentSession, implementation domain.AgentSession, trigger AgentGraphTrigger) (AgentGraphRunResult, error) {
	subPlan, err := s.planSvc.GetSubPlan(ctx, implementation.SubPlanID)
	if err != nil {
		return AgentGraphRunResult{}, fmt.Errorf("get sub-plan: %w", err)
	}
	plan, err := s.planSvc.GetPlan(ctx, subPlan.PlanID)
	if err != nil {
		return AgentGraphRunResult{}, fmt.Errorf("get plan: %w", err)
	}
	workItem, err := s.workItemSvc.Get(ctx, plan.WorkItemID)
	if err != nil {
		return AgentGraphRunResult{}, fmt.Errorf("get work item: %w", err)
	}
	workspace, err := s.workspaceSvc.Get(ctx, workItem.WorkspaceID)
	if err != nil {
		return AgentGraphRunResult{}, fmt.Errorf("get workspace: %w", err)
	}
	if err := s.prepareImplementationGraphState(ctx, &workItem, &subPlan, trigger); err != nil {
		return AgentGraphRunResult{}, err
	}

	worktreePath := implementation.WorktreePath
	if worktreePath == "" {
		worktreePath = source.WorktreePath
	}
	if worktreePath == "" {
		return AgentGraphRunResult{}, fmt.Errorf("implementation session %s has no worktree path", implementation.ID)
	}
	feedback := implementationGraphFeedback(intent, trigger)
	newSession, err := s.runImplementation(ctx, subPlan, &workspace, &plan, &workItem, worktreePath, feedback, &implementation, source.ID, true)
	if err != nil {
		return AgentGraphRunResult{SourceSession: source, Trigger: trigger}, fmt.Errorf("run implementation graph child: %w", err)
	}
	if err := s.ContinueImplementationGraph(ctx, ContinuationContext{
		CompletedImplementationID: newSession.ID,
		SupersededLeafID:          source.ID,
		Trigger:                   trigger,
	}); err != nil {
		return AgentGraphRunResult{SourceSession: source, NewSession: newSession, Trigger: trigger}, fmt.Errorf("continue implementation graph child: %w", err)
	}
	return AgentGraphRunResult{SourceSession: source, NewSession: newSession, Trigger: trigger}, nil
}

func defaultImplementationGraphTrigger(status domain.AgentSessionStatus) AgentGraphTrigger {
	switch status {
	case domain.AgentSessionInterrupted:
		return AgentGraphTriggerResumeInterrupted
	case domain.AgentSessionFailed:
		return AgentGraphTriggerRetryFailed
	case domain.AgentSessionCompleted:
		return AgentGraphTriggerFollowUpCompleted
	default:
		return ""
	}
}

func validateImplementationGraphTrigger(source domain.AgentSession, trigger AgentGraphTrigger) error {
	switch trigger {
	case AgentGraphTriggerResumeInterrupted:
		if source.Status != domain.AgentSessionInterrupted {
			return fmt.Errorf("implementation session %s is not interrupted (status: %s)", source.ID, source.Status)
		}
	case AgentGraphTriggerRetryFailed, AgentGraphTriggerFollowUpFailed:
		if source.Status != domain.AgentSessionFailed {
			return fmt.Errorf("implementation session %s is not failed (status: %s)", source.ID, source.Status)
		}
	case AgentGraphTriggerFollowUpCompleted:
		if source.Status != domain.AgentSessionCompleted {
			return fmt.Errorf("implementation session %s is not completed (status: %s)", source.ID, source.Status)
		}
	case AgentGraphTriggerAutoReimpl:
		if source.Status != domain.AgentSessionCompleted {
			return fmt.Errorf("implementation session %s is not completed for auto reimplementation (status: %s)", source.ID, source.Status)
		}
	default:
		return fmt.Errorf("unsupported implementation graph trigger %q", trigger)
	}
	return nil
}

func implementationGraphFeedback(intent AgentGraphIntent, trigger AgentGraphTrigger) string {
	if intent.Feedback != "" {
		return intent.Feedback
	}
	if trigger == AgentGraphTriggerResumeInterrupted || trigger == AgentGraphTriggerRetryFailed {
		return resumeContinuationMessage
	}
	return ""
}

func (s *ImplementationService) prepareImplementationGraphState(ctx context.Context, workItem *domain.Session, subPlan *domain.TaskPlan, trigger AgentGraphTrigger) error {
	switch workItem.State {
	case domain.SessionFailed:
		if err := s.workItemSvc.RetryFailedWorkItem(ctx, workItem.ID); err != nil {
			return fmt.Errorf("retry failed work item: %w", err)
		}
		workItem.State = domain.SessionImplementing
	case domain.SessionReviewing:
		if err := s.workItemSvc.StartImplementation(ctx, workItem.ID); err != nil {
			return fmt.Errorf("transition reviewing work item to implementing: %w", err)
		}
		workItem.State = domain.SessionImplementing
	case domain.SessionImplementing:
	case domain.SessionCompleted:
		if trigger != AgentGraphTriggerFollowUpCompleted {
			return fmt.Errorf("work item %s is completed; trigger %s cannot start implementation graph run", workItem.ID, trigger)
		}
		if err := s.workItemSvc.StartImplementation(ctx, workItem.ID); err != nil {
			return fmt.Errorf("transition completed work item to implementing: %w", err)
		}
		workItem.State = domain.SessionImplementing
	default:
		return fmt.Errorf("work item %s is in state %s, cannot start implementation graph run", workItem.ID, workItem.State)
	}
	switch subPlan.Status {
	case domain.SubPlanPending, domain.SubPlanFailed, domain.SubPlanEscalated:
		if err := s.persistSubPlanStatusStrict(ctx, subPlan, domain.SubPlanInProgress); err != nil {
			return fmt.Errorf("mark sub-plan in progress: %w", err)
		}
		subPlan.Status = domain.SubPlanInProgress
	case domain.SubPlanCompleted:
		if trigger != AgentGraphTriggerFollowUpCompleted {
			return fmt.Errorf("sub-plan %s is completed; trigger %s cannot start implementation graph run", subPlan.ID, trigger)
		}
		if err := s.persistSubPlanStatusStrict(ctx, subPlan, domain.SubPlanInProgress); err != nil {
			return fmt.Errorf("mark completed sub-plan in progress: %w", err)
		}
		subPlan.Status = domain.SubPlanInProgress
	case domain.SubPlanInProgress:
	default:
		return fmt.Errorf("sub-plan %s is in status %s, cannot start implementation graph run", subPlan.ID, subPlan.Status)
	}
	return nil
}

// RetryReviewLeaf reruns review continuation from a failed or interrupted review
// graph leaf while preserving the graph edge from that leaf to the replacement
// review session. The reviewed implementation is discovered by walking ancestors
// rather than assuming the review's direct parent is the implementation.
func (s *ImplementationService) RetryReviewLeaf(ctx context.Context, intent AgentGraphIntent) (AgentGraphRunResult, error) {
	if intent.SourceSessionID == "" {
		return AgentGraphRunResult{}, fmt.Errorf("source session id is required")
	}
	source, err := s.sessionSvc.Get(ctx, intent.SourceSessionID)
	if err != nil {
		return AgentGraphRunResult{}, fmt.Errorf("get source session: %w", err)
	}
	if source.Kind != domain.AgentSessionKindReview {
		return AgentGraphRunResult{}, fmt.Errorf("session %s is not a review session (kind: %s)", source.ID, source.Kind)
	}
	switch source.Status {
	case domain.AgentSessionFailed, domain.AgentSessionInterrupted:
	default:
		return AgentGraphRunResult{}, fmt.Errorf("review session %s is not retryable (status: %s)", source.ID, source.Status)
	}
	if intent.WorkItemID != "" && source.WorkItemID != intent.WorkItemID {
		return AgentGraphRunResult{}, fmt.Errorf("review session %s belongs to work item %s, not %s", source.ID, source.WorkItemID, intent.WorkItemID)
	}
	if intent.SubPlanID != "" && source.SubPlanID != intent.SubPlanID {
		return AgentGraphRunResult{}, fmt.Errorf("review session %s belongs to sub-plan %s, not %s", source.ID, source.SubPlanID, intent.SubPlanID)
	}

	sessions, err := s.sessionSvc.ListByWorkItemID(ctx, source.WorkItemID)
	if err != nil {
		return AgentGraphRunResult{}, fmt.Errorf("list work item sessions: %w", err)
	}
	if !isAgentSessionLeaf(source.ID, sessions) {
		return AgentGraphRunResult{}, service.ErrAgentSessionNotLeaf
	}
	impl, err := nearestCompletedImplementationAncestor(source, sessions)
	if err != nil {
		return AgentGraphRunResult{}, err
	}
	if impl.WorkItemID != source.WorkItemID || impl.SubPlanID != source.SubPlanID {
		return AgentGraphRunResult{}, fmt.Errorf("implementation ancestor %s does not match review leaf %s", impl.ID, source.ID)
	}

	trigger := intent.Trigger
	if trigger == "" {
		trigger = AgentGraphTriggerRetryFailed
	}
	if err := s.ContinueImplementationGraph(ctx, ContinuationContext{
		CompletedImplementationID: impl.ID,
		SupersededLeafID:          source.ID,
		FirstReviewParentID:       source.ID,
		Trigger:                   trigger,
	}); err != nil {
		return AgentGraphRunResult{}, fmt.Errorf("continue review retry: %w", err)
	}
	return AgentGraphRunResult{SourceSession: source, Trigger: trigger}, nil
}

func isAgentSessionLeaf(id string, sessions []domain.AgentSession) bool {
	for _, leaf := range domain.LeafAgentSessions(sessions) {
		if leaf.ID == id {
			return true
		}
	}
	return false
}

func nearestCompletedImplementationAncestor(source domain.AgentSession, sessions []domain.AgentSession) (domain.AgentSession, error) {
	byID := make(map[string]domain.AgentSession, len(sessions))
	for i := range sessions {
		byID[sessions[i].ID] = sessions[i]
	}
	seen := map[string]bool{source.ID: true}
	parentID := source.ParentAgentSessionID
	for parentID != "" {
		if seen[parentID] {
			return domain.AgentSession{}, fmt.Errorf("agent-session graph cycle at %s", parentID)
		}
		seen[parentID] = true
		parent, ok := byID[parentID]
		if !ok {
			return domain.AgentSession{}, fmt.Errorf("parent session %s for %s not found", parentID, source.ID)
		}
		if parent.Kind == domain.AgentSessionKindImplementation {
			if parent.Status != domain.AgentSessionCompleted {
				return domain.AgentSession{}, fmt.Errorf("implementation ancestor %s is not completed (status: %s)", parent.ID, parent.Status)
			}
			return parent, nil
		}
		parentID = parent.ParentAgentSessionID
	}
	return domain.AgentSession{}, fmt.Errorf("review session %s has no implementation ancestor", source.ID)
}

// RecoverContinuationsForWorkItem resumes interrupted implementation
// continuation work for one work item after an explicit operator action.
func (s *ImplementationService) RecoverContinuationsForWorkItem(ctx context.Context, workItemID string) (ContinuationRecoveryResult, error) {
	if workItemID == "" {
		return ContinuationRecoveryResult{}, fmt.Errorf("work item id is required")
	}
	workItem, err := s.workItemSvc.Get(ctx, workItemID)
	if err != nil {
		return ContinuationRecoveryResult{}, fmt.Errorf("get work item: %w", err)
	}
	return s.recoverContinuations(ctx, workItem.WorkspaceID, workItemID)
}

// RecoverContinuationsForWorkspace resumes interrupted implementation
// continuation work that was durably left pending or running by a prior process.
// Failed continuations are returned as skipped so UI/retry surfaces can expose
// the recorded error instead of silently replaying a known-bad continuation.
func (s *ImplementationService) RecoverContinuationsForWorkspace(ctx context.Context, workspaceID string) (ContinuationRecoveryResult, error) {
	return s.recoverContinuations(ctx, workspaceID, "")
}

func (s *ImplementationService) recoverContinuations(ctx context.Context, workspaceID, workItemID string) (ContinuationRecoveryResult, error) {
	if workspaceID == "" {
		return ContinuationRecoveryResult{}, fmt.Errorf("workspace id is required")
	}
	continuations, err := s.continuationSvc.ListRecoverable(ctx, workspaceID)
	if err != nil {
		return ContinuationRecoveryResult{}, fmt.Errorf("list recoverable continuations: %w", err)
	}

	result := ContinuationRecoveryResult{}
	for _, continuation := range continuations {
		if workItemID != "" && continuation.WorkItemID != workItemID {
			continue
		}
		if continuation.Kind != implementationReviewContinuationKind {
			result.Skipped = append(result.Skipped, ContinuationRecoverySkipped{
				ContinuationID: continuation.ID,
				SessionID:      continuation.AgentSessionID,
				Status:         continuation.Status,
				Reason:         "unsupported continuation kind",
			})
			continue
		}
		switch continuation.Status {
		case domain.AgentSessionContinuationPending, domain.AgentSessionContinuationRunning:
			if err := s.ContinueImplementationGraph(ctx, ContinuationContext{CompletedImplementationID: continuation.AgentSessionID}); err != nil {
				return result, fmt.Errorf("recover continuation %s for session %s: %w", continuation.ID, continuation.AgentSessionID, err)
			}
			result.Recovered++
		case domain.AgentSessionContinuationFailed:
			result.Skipped = append(result.Skipped, ContinuationRecoverySkipped{
				ContinuationID: continuation.ID,
				SessionID:      continuation.AgentSessionID,
				Status:         continuation.Status,
				Reason:         "continuation previously failed",
			})
		default:
			result.Skipped = append(result.Skipped, ContinuationRecoverySkipped{
				ContinuationID: continuation.ID,
				SessionID:      continuation.AgentSessionID,
				Status:         continuation.Status,
				Reason:         "continuation status is not recoverable",
			})
		}
	}
	return result, nil
}

// ContinueImplementationGraph resumes the per-sub-plan pipeline starting from a
// completed implementation session and records durable continuation state for
// review, sub-plan, work-item, and finalization work.
func (s *ImplementationService) ContinueImplementationGraph(ctx context.Context, cc ContinuationContext) error {
	return s.continueImplementationGraph(ctx, cc)
}

func (s *ImplementationService) continueImplementationGraph(ctx context.Context, cc ContinuationContext) (err error) {
	completedSessionID := cc.CompletedImplementationID
	firstReviewParentSessionID := cc.FirstReviewParentID
	var session domain.AgentSession
	session, err = s.sessionSvc.Get(ctx, completedSessionID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}
	if session.Status != domain.AgentSessionCompleted {
		return fmt.Errorf("session %s is not completed (status: %s)", completedSessionID, session.Status)
	}
	if session.Kind != domain.AgentSessionKindImplementation {
		return fmt.Errorf("session %s is not an implementation session (kind: %s)", completedSessionID, session.Kind)
	}
	if session.SubPlanID == "" {
		return fmt.Errorf("session %s has no sub-plan assigned", completedSessionID)
	}

	continuation, err := s.continuationSvc.CreatePending(ctx, session.ID, implementationReviewContinuationKind)
	if err != nil {
		return fmt.Errorf("create pending continuation: %w", err)
	}
	defer func() {
		if err != nil {
			if _, failErr := s.continuationSvc.Fail(ctx, continuation.ID, err); failErr != nil {
				slog.Error("record continuation failure failed", "continuation_id", continuation.ID, "error", failErr)
			}
			s.emitContinuationFailed(ctx, session, err)
		}
	}()

	if _, err := s.continuationSvc.Start(ctx, continuation.ID); err != nil {
		return fmt.Errorf("start continuation: %w", err)
	}

	subPlan, err := s.planSvc.GetSubPlan(ctx, session.SubPlanID)
	if err != nil {
		return fmt.Errorf("get sub-plan: %w", err)
	}

	plan, err := s.planSvc.GetPlan(ctx, subPlan.PlanID)
	if err != nil {
		return fmt.Errorf("get plan: %w", err)
	}
	workItem, err := s.workItemSvc.Get(ctx, plan.WorkItemID)
	if err != nil {
		return fmt.Errorf("get work item: %w", err)
	}
	workspace, err := s.workspaceSvc.Get(ctx, workItem.WorkspaceID)
	if err != nil {
		return fmt.Errorf("get workspace: %w", err)
	}

	switch workItem.State {
	case domain.SessionFailed:
		if err := s.workItemSvc.RetryFailedWorkItem(ctx, workItem.ID); err != nil {
			return fmt.Errorf("retry failed work item: %w", err)
		}
	case domain.SessionReviewing:
		if err := s.workItemSvc.StartImplementation(ctx, workItem.ID); err != nil {
			return fmt.Errorf("transition reviewing work item to implementing: %w", err)
		}
	case domain.SessionImplementing:
	default:
		return fmt.Errorf("work item %s is in state %s, cannot continue retry", workItem.ID, workItem.State)
	}

	if subPlan.Status == domain.SubPlanFailed || subPlan.Status == domain.SubPlanEscalated {
		if err := s.persistSubPlanStatusStrict(ctx, &subPlan, domain.SubPlanInProgress); err != nil {
			return fmt.Errorf("mark sub-plan in progress: %w", err)
		}
	}

	worktreePaths := map[string]string{subPlan.RepositoryName: session.WorktreePath}
	if s.reviewPipeline != nil {
		outcome := s.reviewLoopWithFirstReviewParent(ctx, session, subPlan, &workspace, &plan, &workItem, worktreePaths, firstReviewParentSessionID)
		switch {
		case outcome.Passed:
			if err := s.persistSubPlanStatusStrict(ctx, &subPlan, domain.SubPlanCompleted); err != nil {
				return fmt.Errorf("mark sub-plan completed: %w", err)
			}
		case outcome.Failed:
			if err := s.persistSubPlanStatusStrict(ctx, &subPlan, domain.SubPlanFailed); err != nil {
				return fmt.Errorf("mark sub-plan failed: %w", err)
			}
		case outcome.Escalated:
			if err := s.persistSubPlanStatusStrict(ctx, &subPlan, domain.SubPlanEscalated); err != nil {
				return fmt.Errorf("mark sub-plan escalated: %w", err)
			}
		}
	} else if err := s.persistSubPlanStatusStrict(ctx, &subPlan, domain.SubPlanCompleted); err != nil {
		return fmt.Errorf("mark sub-plan completed without review: %w", err)
	}

	allSubPlans, err := s.planSvc.ListSubPlansByPlanID(ctx, plan.ID)
	if err != nil {
		return fmt.Errorf("list sub-plans for state derivation: %w", err)
	}

	hasFailed := false
	hasEscalated := false
	allCompleted := true
	for _, sp := range allSubPlans {
		switch sp.Status {
		case domain.SubPlanFailed:
			hasFailed = true
			allCompleted = false
		case domain.SubPlanEscalated:
			hasEscalated = true
			allCompleted = false
		case domain.SubPlanCompleted:
		default:
			allCompleted = false
		}
	}

	switch {
	case hasFailed:
		if err := s.workItemSvc.FailWorkItem(ctx, workItem.ID); err != nil {
			return fmt.Errorf("transition work item to failed: %w", err)
		}
	case hasEscalated:
		if err := s.submitForHumanReview(ctx, workItem.ID); err != nil {
			return err
		}
	case allCompleted:
		repoPaths, err := s.discoverRepoPaths(ctx, workspace.RootPath)
		if err != nil {
			return fmt.Errorf("discover repo paths for finalization: %w", err)
		}
		branch := GenerateBranchName(workItem.ExternalID, workItem.Title)
		tasks, err := s.sessionSvc.ListByWorkItemID(ctx, workItem.ID)
		if err != nil {
			return fmt.Errorf("list sessions for finalization: %w", err)
		}
		sessions, err := completedSessionResultsForSubPlans(allSubPlans, tasks, branch)
		if err != nil {
			return fmt.Errorf("build session results for finalization: %w", err)
		}
		if err := s.finalizeCompletedWorkItem(ctx, &workItem, workspace.ID, sessions, repoPaths, branch, allSubPlans); err != nil {
			return fmt.Errorf("finalize completed work item: %w", err)
		}
	}

	if _, err := s.continuationSvc.Complete(ctx, continuation.ID); err != nil {
		return fmt.Errorf("complete continuation: %w", err)
	}
	return nil
}

// runImplementation creates and runs a new agent session for a sub-plan.
// It handles both fresh implementations and re-implementations with review
// critique context. When prevSession is non-nil and has ResumeInfo, the harness
// is asked to resume the previous conversation; critique feedback is then sent
// as a follow-up message to preserve conversation context. When prevSession is
// nil or has no ResumeInfo, critique feedback is appended to the system prompt.
//
// parentSessionID, when non-empty, is recorded on the new session as
// ParentAgentSessionID so the agent-session graph captures the lifecycle edge
// from the prior session to this new one (failed-impl retry, reimplementation
// after review critique, ...). The first implementation in a sub-plan passes
// an empty parentSessionID.
func (s *ImplementationService) runImplementation(
	ctx context.Context,
	subPlan domain.TaskPlan,
	workspace *domain.Workspace,
	plan *domain.Plan,
	workItem *domain.Session,
	worktreePath string,
	critiqueFeedback string,
	prevSession *domain.AgentSession,
	parentSessionID string,
	createCompletionContinuation bool,
) (domain.AgentSession, error) {
	sessionID := domain.NewID()
	agentSession := domain.AgentSession{
		ID:                   sessionID,
		WorkItemID:           workItem.ID,
		WorkspaceID:          workspace.ID,
		Kind:                 domain.AgentSessionKindImplementation,
		SubPlanID:            subPlan.ID,
		RepositoryName:       subPlan.RepositoryName,
		WorktreePath:         worktreePath,
		HarnessName:          s.harness.Name(),
		ParentAgentSessionID: parentSessionID,
	}
	if err := s.sessionSvc.Create(ctx, agentSession); err != nil {
		return domain.AgentSession{}, fmt.Errorf("create agent session: %w", err)
	}
	if err := s.sessionSvc.Start(ctx, sessionID); err != nil {
		if transitionErr := s.sessionSvc.Transition(ctx, sessionID, domain.AgentSessionFailed); transitionErr != nil {
			slog.Warn("failed to transition agent session to failed", "error", transitionErr, "agent_session_id", sessionID)
		}
		return domain.AgentSession{}, fmt.Errorf("start agent session: %w", err)
	}

	opts := s.buildSessionOpts(agentSession, subPlan, plan, workItem, workspace)

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

	completeContinuationKind := ""
	if createCompletionContinuation {
		completeContinuationKind = implementationReviewContinuationKind
	}
	done := make(chan error, 1)
	supervisor := &AgentRunSupervisor{
		harnesses:  staticHarnessSelector{harness: s.harness},
		sessionSvc: s.sessionSvc,
		registry:   s.registry,
		forward:    s.forwardEvents,
		timeout:    s.sessTimeout,
	}
	_, err := supervisor.Start(ctx, AgentRunRequest{
		Session:                  agentSession,
		Opts:                     opts,
		CompleteContinuationKind: completeContinuationKind,
		AfterStart: func(ctx context.Context, harnessSession adapter.AgentSession) error {
			// a clean summary of its prior work rather than the full transcript.
			if canCompact {
				if err := harnessSession.Compact(ctx); err != nil {
					slog.Warn("failed to compact resumed session, continuing without compact", "error", err,
						"agent_session_id", sessionID)
				}
			}

			// Deliver guidance to the resumed session. Without an explicit user-turn
			// after resume + compact-skip, the bridge sits idle until sessTimeout —
			// the SDK does not auto-prompt on its own. Send either the critique
			// feedback (auto-reimpl path) or a generic continuation orientation
			// (bulk retry / escalation reset / any other path that lands here with
			// no critique).
			switch {
			case canCompact && critiqueFeedback != "":
				// Auto-reimpl after critique: the critique is the user's turn.
				if err := harnessSession.SendMessage(ctx, critiqueFeedback); err != nil {
					slog.Warn("failed to send critique feedback to resumed session", "error", err,
						"agent_session_id", sessionID)
				}
			case canCompact:
				// Resumed with no critique to deliver — bulk retry of a failed work
				// item, escalation reset, or any other path that reaches us here.
				if err := harnessSession.SendMessage(ctx, resumeContinuationMessage); err != nil {
					slog.Warn("failed to send orientation to resumed session", "error", err,
						"agent_session_id", sessionID)
				}
			}
			return nil
		},
		OnCompleted: func(context.Context, domain.AgentSession) error {
			done <- nil
			return nil
		},
		OnFailed: func(_ context.Context, _ domain.AgentSession, err error) error {
			done <- fmt.Errorf("agent session failed: %w", err)
			return nil
		},
		OnInterrupted: func(context.Context, domain.AgentSession) error {
			done <- fmt.Errorf("agent session failed: %w", context.Canceled)
			return nil
		},
	})
	if err != nil {
		return domain.AgentSession{}, err
	}
	if err := <-done; err != nil {
		return domain.AgentSession{}, err
	}
	if info, err := s.sessionSvc.Get(ctx, sessionID); err == nil {
		agentSession.ResumeInfo = info.ResumeInfo
	} else {
		slog.Warn("failed to reload completed implementation session", "error", err, "agent_session_id", sessionID)
	}
	return agentSession, nil
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

// implHasOutstandingCritique reports whether any review cycle for the given
// implementation session is in `critiques_found` or `reimplementing`. This is
// the same condition `loadCritiqueFeedback` uses to decide whether there is
// critique to feed back into a re-impl. Used by Tier 3 (executeSubPlan
// review-existing-impl branch) to decide between reviewing the existing impl
// versus running a re-impl.
func (s *ImplementationService) implHasOutstandingCritique(ctx context.Context, implSessionID string) bool {
	cycles, err := s.reviewSvc.ListCyclesBySessionID(ctx, implSessionID)
	if err != nil {
		slog.Warn("failed to list review cycles checking for outstanding critique",
			"error", err, "impl_session_id", implSessionID)
		return false
	}
	for _, c := range cycles {
		if c.Status == domain.ReviewCycleCritiquesFound || c.Status == domain.ReviewCycleReimplementing {
			return true
		}
	}
	return false
}

// implHasPassedReview reports whether any review cycle for the given
// implementation session has reached the `passed` terminal status.
func (s *ImplementationService) implHasPassedReview(ctx context.Context, implSessionID string) bool {
	cycles, err := s.reviewSvc.ListCyclesBySessionID(ctx, implSessionID)
	if err != nil {
		slog.Warn("failed to list review cycles checking for passed review",
			"error", err, "impl_session_id", implSessionID)
		return false
	}
	for _, c := range cycles {
		if c.Status == domain.ReviewCyclePassed {
			return true
		}
	}
	return false
}

// runReviewOnExistingImpl runs the review pipeline against an existing completed
// implementation session and translates the outcome into SessionResult /
// ExecutionState transitions. Shared between the original crash-recovery branch
// (latest session is a review) and the Tier 3 branch (latest session is anything
// but the impl is already complete with no outstanding work).
//
// `summary` becomes the SessionResult.Summary so the caller can describe why
// the re-impl was skipped.
func (s *ImplementationService) runReviewOnExistingImpl(
	ctx context.Context,
	subPlan domain.TaskPlan,
	workspace *domain.Workspace,
	plan *domain.Plan,
	workItem *domain.Session,
	worktreePaths map[string]string,
	prevImpl domain.AgentSession,
	result SessionResult,
	state *ExecutionState,
	reviewParentSessionID string,
	summary string,
) (SessionResult, *ImplementationWarning) {
	result.Status = domain.AgentSessionCompleted
	result.SessionID = prevImpl.ID
	result.WorktreePath = prevImpl.WorktreePath
	result.Summary = summary
	result.CompletedAt = ptrTime(time.Now())

	outcome := s.reviewLoopWithFirstReviewParent(ctx, prevImpl, subPlan, workspace, plan, workItem, worktreePaths, reviewParentSessionID)
	result.Outcome = outcome
	switch {
	case outcome.Passed:
		state.CompleteSubPlan(subPlan.ID, time.Now().UnixNano())
		s.persistSubPlanStatus(ctx, &subPlan, domain.SubPlanCompleted)
	case outcome.Failed:
		state.FailSubPlan(subPlan.ID, time.Now().UnixNano(), fmt.Errorf("review failed for %s", subPlan.RepositoryName))
		s.persistSubPlanStatus(ctx, &subPlan, domain.SubPlanFailed)
	case outcome.Escalated:
		state.FailSubPlan(subPlan.ID, time.Now().UnixNano(), fmt.Errorf("review escalated for %s — requires human intervention", subPlan.RepositoryName))
		s.persistSubPlanStatus(ctx, &subPlan, domain.SubPlanEscalated)
	}
	return result, nil
}

// loadCritiqueFeedback looks up outstanding review critiques for a sub-plan
// and formats them for injection into the next implementation session.
// Returns "" when no prior implementation session exists, or when no review cycle
// with critiques is found.
func (s *ImplementationService) loadCritiqueFeedback(ctx context.Context, subPlanID string) string {
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

	// Run pre-checkout hooks (synchronous; aborts checkout on rejection)
	if err := s.hookRegistry.Run(ctx, worktree.CheckoutRequest{
		WorkspaceID:   workspace.ID,
		WorkItemID:    workItemID,
		Repository:    repoName,
		Branch:        branch,
		WorkItemTitle: workItemTitle,
	}); err != nil {
		return "", fmt.Errorf("worktree pre-hook rejected: %w", err)
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
	agentSession domain.AgentSession,
	subPlan domain.TaskPlan,
	plan *domain.Plan,
	workItem *domain.Session,
	workspace *domain.Workspace,
) adapter.SessionOpts {
	// Read AGENTS.md if it exists
	agentsMdPath := filepath.Join(agentSession.WorktreePath, "AGENTS.md")
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
		SessionID:            agentSession.ID,
		Mode:                 adapter.SessionModeAgent,
		WorkspaceID:          workspace.ID,
		SubPlanID:            subPlan.ID,
		Repository:           subPlan.RepositoryName,
		WorktreePath:         agentSession.WorktreePath,
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
	if s.cfg.Foreman.QuestionTimeout == "" || s.cfg.Foreman.QuestionTimeout == "0" {
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
	prompt.WriteString("Commit all changes before finishing. If a Commit Strategy section is present above, follow it; otherwise use a single commit: \x60git add -A && git commit\x60.\n")

	return prompt.String()
}

// buildCommitSection returns a prompt section instructing the agent how to commit
// its work based on the configured strategy and message format. Returns empty
// string when no meaningful strategy is set.
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

	if section := buildMessageFormatSection(cfg); section != "" {
		sb.WriteString("\n\n")
		sb.WriteString(section)
	}

	return sb.String()
}

// buildMessageFormatSection returns instructions for commit message format based on the
// configured format. Returns empty string when using the default ai-generated format.
func buildMessageFormatSection(cfg adapter.CommitConfig) string {
	switch cfg.MessageFormat {
	case "conventional":
		return "### Commit Message Format\n\n" +
			"Use Conventional Commits format: \x60type(scope): description\x60\n" +
			"Common types: feat, fix, refactor, docs, test, chore, perf, build, ci.\n" +
			"Keep the subject line under 72 characters. Use imperative mood (\"add feature\" not \"added feature\").\n" +
			"Separate subject from body with a blank line when the body adds context."
	case "custom":
		if cfg.MessageTemplate == "" {
			return ""
		}
		return "### Commit Message Format\n\n" +
			"Follow this template for every commit message:\n\n" +
			"\x60\x60\x60\n" + cfg.MessageTemplate + "\n\x60\x60\x60"
	default:
		// ai-generated or unrecognized: no extra format instructions needed.
		return ""
	}
}

// buildCommitAgentSystemPrompt constructs the system prompt for the short-lived commit
// agent that handles residual uncommitted changes after an implementation session.
// The prompt is strategy-aware: it instructs the agent how to group and commit
// the leftover changes based on the configured strategy.
func buildCommitAgentSystemPrompt(cfg adapter.CommitConfig) string {
	var sb strings.Builder
	sb.WriteString("The implementation session is complete but there are residual uncommitted changes in the worktree.\n\n")
	sb.WriteString("Do not modify any files. Your only job is to stage and commit existing changes.\n\n")

	switch cfg.Strategy {
	case "granular":
		sb.WriteString("Review each changed file and group them into self-contained logical units. ")
		sb.WriteString("Stage and commit each unit separately with a descriptive message for that specific change. ")
		sb.WriteString("For example, a new function and its test should be one commit; an unrelated refactor in another file should be a separate commit.")
	case "semi-regular":
		sb.WriteString("Review the changes and group them into a few logical commits (e.g. by feature area or concern). ")
		sb.WriteString("Stage and commit each group with a message that describes what the group accomplishes.")
	default: // "single" or unknown
		sb.WriteString("Stage all changes and make a single commit with a comprehensive message summarizing the full scope of residual changes.")
	}

	return sb.String()
}

// forwardEvents drains harness events so producer channels cannot fill.
// Detailed transcript events stay in session logs; only questions need live routing.
func (s *ImplementationService) forwardEvents(ctx context.Context, events <-chan adapter.AgentEvent, sessionID string) {
	// Snapshot the session-scoped router so we read a consistent pair (router + foreman)
	// even if a concurrent Implement call swaps them.
	s.questionRouterMu.Lock()
	router := s.questionRouter
	s.questionRouterMu.Unlock()
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-events:
			if !ok {
				return
			}

			if evt.Type == "question" {
				if err := router.Route(ctx, domain.AgentSessionKindImplementation, evt, sessionID); err != nil {
					slog.Error("failed to route implementation question", "error", err, "agent_session_id", sessionID)
				}
				continue
			}

		}
	}
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
// Nil-safe: if the event bus is not configured, the event is silently dropped.
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
	SessionID    string                  `json:"agent_session_id"`
	WorkItemID   string                  `json:"work_item_id"`
	Phase        domain.AgentSessionKind `json:"phase"`
	SubPlanID    string                  `json:"sub_plan_id"`
	Repository   string                  `json:"repository"`
	WorktreePath string                  `json:"worktree_path"`
}

func newSessionEventPayload(agentSession *domain.AgentSession) sessionEventPayload {
	return sessionEventPayload{
		SessionID:    agentSession.ID,
		WorkItemID:   agentSession.WorkItemID,
		Phase:        agentSession.Kind,
		SubPlanID:    agentSession.SubPlanID,
		Repository:   agentSession.RepositoryName,
		WorktreePath: agentSession.WorktreePath,
	}
}

// FinalizeWorkItem retries the final commit/push/completion step for a work item whose repo tasks already finished.
func (s *ImplementationService) FinalizeWorkItem(ctx context.Context, workItemID string) (*ImplementResult, error) {
	workItem, err := s.workItemSvc.Get(ctx, workItemID)
	if err != nil {
		return nil, fmt.Errorf("get work item: %w", err)
	}
	if workItem.State != domain.SessionImplementing {
		return nil, fmt.Errorf("work item %s is %s, expected %s", workItemID, workItem.State, domain.SessionImplementing)
	}

	workspace, err := s.workspaceSvc.Get(ctx, workItem.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("get workspace: %w", err)
	}

	plan, err := s.planSvc.GetPlanByWorkItemID(ctx, workItemID)
	if err != nil {
		return nil, fmt.Errorf("get plan for work item: %w", err)
	}
	subPlans, err := s.planSvc.ListSubPlansByPlanID(ctx, plan.ID)
	if err != nil {
		return nil, fmt.Errorf("get sub-plans: %w", err)
	}
	if len(subPlans) == 0 {
		return nil, fmt.Errorf("work item %s has no sub-plans to finalize", workItemID)
	}

	for _, subPlan := range subPlans {
		if subPlan.Status != domain.SubPlanCompleted {
			return nil, fmt.Errorf("work item %s is not ready to finalize: sub-plan %s is %s", workItemID, subPlan.ID, subPlan.Status)
		}
	}

	tasks, err := s.sessionSvc.ListByWorkItemID(ctx, workItemID)
	if err != nil {
		return nil, fmt.Errorf("list agent sessions: %w", err)
	}
	for _, agentSession := range tasks {
		if isActiveAgentSession(agentSession.Status) {
			return nil, fmt.Errorf("work item %s still has active agent session %s (%s)", workItemID, agentSession.ID, agentSession.Status)
		}
	}

	repoPaths, err := s.discoverRepoPaths(ctx, workspace.RootPath)
	if err != nil {
		return nil, fmt.Errorf("discover repo paths: %w", err)
	}

	branch := GenerateBranchName(workItem.ExternalID, workItem.Title)
	sessions, err := completedSessionResultsForSubPlans(subPlans, tasks, branch)
	if err != nil {
		return nil, err
	}

	if err := s.finalizeCompletedWorkItem(ctx, &workItem, workspace.ID, sessions, repoPaths, branch, subPlans); err != nil {
		return nil, err
	}

	return &ImplementResult{
		PlanID:     plan.ID,
		WorkItemID: workItemID,
		Sessions:   sessions,
	}, nil
}

func (s *ImplementationService) finalizeCompletedWorkItem(ctx context.Context, workItem *domain.Session, workspaceID string, sessions []SessionResult, repoPaths map[string]string, branch string, subPlans []domain.TaskPlan) error {
	finalizationCtx, finalizationCancel := durableFinalizationContext(ctx)
	finalizationResults := s.commitAndPushRepos(finalizationCtx, sessions, repoPaths, branch)
	finalizationCancel()

	// Build subPlan map for lookup
	subPlanByID := make(map[string]domain.TaskPlan, len(subPlans))
	for _, sp := range subPlans {
		subPlanByID[sp.ID] = sp
	}

	// Emit PR-ready events for each successful finalization and collect failures.
	// Finalization errors and PR-ready emission errors are tracked separately so
	// the error message accurately reflects what failed.
	var finalizationErrs, prReadyErrs []error
	for _, result := range finalizationResults {
		if result.Err != nil {
			finalizationErrs = append(finalizationErrs, result.Err)
			continue
		}

		sp, ok := subPlanByID[result.SubPlanID]
		if !ok {
			slog.Warn("finalization result has no matching sub-plan, skipping PR-ready",
				"sub_plan_id", result.SubPlanID, "repo", result.Repository)
			continue
		}

		// Use a durable context so the PR-ready event is emitted even if the
		// caller's context is canceled after finalization succeeds.
		prReadyCtx, prReadyCancel := durablePRReadyContext(ctx)
		defer prReadyCancel()
		if markErr := s.planSvc.MarkSubPlanPRReady(prReadyCtx, result.SubPlanID, service.SubPlanPRReadyContext{
			Repository:     result.Repository,
			Branch:         result.Branch,
			WorktreePath:   result.WorktreePath,
			WorkItemTitle:  workItem.Title,
			SubPlanContent: sp.Content,
			TrackerRefs:    trackerRefsFromMetadata(workItem.Metadata),
			Review:         result.Review,
		}); markErr != nil {
			prReadyErrs = append(prReadyErrs, fmt.Errorf("sub-plan %s PR-ready: %w", result.SubPlanID, markErr))
		}
	}

	if len(finalizationErrs) > 0 {
		return fmt.Errorf("finalize repos: %w", errors.Join(finalizationErrs...))
	}
	if len(prReadyErrs) > 0 {
		return fmt.Errorf("emit PR-ready events: %w", errors.Join(prReadyErrs...))
	}

	completeCtx, completeCancel := durableCleanupContext(ctx)
	if completeErr := s.workItemSvc.CompleteWorkItem(completeCtx, workItem.ID); completeErr != nil {
		completeCancel()
		return fmt.Errorf("complete work item: %w", completeErr)
	}
	completeCancel()
	// EventWorkItemCompleted is emitted by CompleteWorkItem → Transition → emitStateChange.
	// EventSubPlanPRReady is emitted above for each sub-plan after successful push.

	return nil
}

func completedSessionResultsForSubPlans(subPlans []domain.TaskPlan, sessions []domain.AgentSession, branch string) ([]SessionResult, error) {
	latestBySubPlan := make(map[string]domain.AgentSession, len(subPlans))
	for _, agentSession := range sessions {
		if agentSession.Kind != domain.AgentSessionKindImplementation || agentSession.Status != domain.AgentSessionCompleted || agentSession.SubPlanID == "" {
			continue
		}
		previous, ok := latestBySubPlan[agentSession.SubPlanID]
		if !ok || sessionSortTime(agentSession).After(sessionSortTime(previous)) {
			latestBySubPlan[agentSession.SubPlanID] = agentSession
		}
	}

	results := make([]SessionResult, 0, len(subPlans))
	for _, subPlan := range subPlans {
		agentSession, ok := latestBySubPlan[subPlan.ID]
		if !ok {
			return nil, fmt.Errorf("no completed implementation agent session found for sub-plan %s", subPlan.ID)
		}
		if strings.TrimSpace(agentSession.WorktreePath) == "" {
			return nil, fmt.Errorf("completed implementation agent session %s has no worktree path", agentSession.ID)
		}
		results = append(results, SessionResult{
			SubPlanID:    subPlan.ID,
			Repository:   subPlan.RepositoryName,
			Branch:       branch,
			Status:       domain.AgentSessionCompleted,
			SessionID:    agentSession.ID,
			WorktreePath: agentSession.WorktreePath,
		})
	}

	return results, nil
}

func sessionSortTime(agentSession domain.AgentSession) time.Time {
	if !agentSession.UpdatedAt.IsZero() {
		return agentSession.UpdatedAt
	}
	return agentSession.CreatedAt
}

func isActiveAgentSession(status domain.AgentSessionStatus) bool {
	switch status {
	case domain.AgentSessionPending, domain.AgentSessionRunning, domain.AgentSessionWaitingForAnswer:
		return true
	default:
		return false
	}
}

// commitAndPushRepos ensures all agent changes are committed and pushed to remote.
// It runs after all repos pass review. For each unique repo, it checks for residual
// uncommitted changes, commits them as a safety net if present, then pushes the branch.
// Agent sessions are already completed and deregistered by this point, so we
// commit directly rather than sending a follow-up message to the agent.
// Returns per-sub-plan results so the caller can emit PR-ready events for each sub-plan.
// When multiple sub-plans share a repository, commit/push is done once but PR-ready is
// emitted for each sub-plan.
func (s *ImplementationService) commitAndPushRepos(ctx context.Context, sessions []SessionResult, repoPaths map[string]string, branch string) []RepoFinalizationResult {
	results := make([]RepoFinalizationResult, 0, len(sessions))
	// Track the successful result per repo so we can emit PR-ready for each sub-plan.
	successfulByRepo := make(map[string]RepoFinalizationResult, len(repoPaths))
	for _, sess := range sessions {
		repo := sess.Repository
		if prevResult, ok := successfulByRepo[repo]; ok {
			// Another sub-plan already pushed this repo; emit PR-ready for this one too.
			results = append(results, RepoFinalizationResult{
				SubPlanID:    sess.SubPlanID,
				Repository:   repo,
				WorktreePath: prevResult.WorktreePath,
				Branch:       branch,
				Review:       prevResult.Review,
			})
			continue
		}

		bareRepo, ok := repoPaths[repo]
		if !ok {
			err := fmt.Errorf("repo %s: bare repo path missing", repo)
			slog.Warn("no bare repo path for repository, skipping commit/push", "repo", repo, "error", err)
			results = append(results, RepoFinalizationResult{
				SubPlanID:    sess.SubPlanID,
				Repository:   repo,
				WorktreePath: sess.WorktreePath,
				Branch:       branch,
				Err:          err,
			})
			continue
		}

		// Resolve review context from the bare repo for remote name and review info.
		reviewCtx, err := remotedetect.ResolveReviewContext(ctx, bareRepo)
		if err != nil {
			err := fmt.Errorf("repo %s: resolve review context: %w", repo, err)
			slog.Error("failed to resolve review context for push", "repo", repo, "error", err)
			results = append(results, RepoFinalizationResult{
				SubPlanID:    sess.SubPlanID,
				Repository:   repo,
				WorktreePath: sess.WorktreePath,
				Branch:       branch,
				Err:          err,
			})
			continue
		}

		// Build review ref for PR-ready event from resolved context.
		reviewRef := reviewCtx.Review
		reviewRef.HeadBranch = branch

		// Check for residual uncommitted changes in the worktree.
		if sess.WorktreePath != "" {
			dirty, statusErr := gitStatusDirty(ctx, sess.WorktreePath)
			if statusErr != nil {
				err := fmt.Errorf("repo %s: check worktree status: %w", repo, statusErr)
				slog.Error("failed to check worktree status before finalization", "repo", repo, "worktree", sess.WorktreePath, "error", err)
				results = append(results, RepoFinalizationResult{
					SubPlanID:    sess.SubPlanID,
					Repository:   repo,
					WorktreePath: sess.WorktreePath,
					Branch:       branch,
					Review:       reviewRef,
					Err:          err,
				})
				continue
			}
			if dirty {
				slog.Warn("agent left uncommitted changes in worktree; spinning up commit session",
					"repo", repo, "worktree", sess.WorktreePath, "agent_session_id", sess.SessionID)
				if commitErr := s.commitViaAgent(ctx, sess.WorktreePath, repo, sess.SessionID); commitErr != nil {
					err := fmt.Errorf("repo %s: commit residual changes: %w", repo, commitErr)
					slog.Error("failed to commit residual changes", "repo", repo, "error", err)
					results = append(results, RepoFinalizationResult{
						SubPlanID:    sess.SubPlanID,
						Repository:   repo,
						WorktreePath: sess.WorktreePath,
						Branch:       branch,
						Review:       reviewRef,
						Err:          err,
					})
					continue
				}

				dirtyAfterCommit, statusErr := gitStatusDirty(ctx, sess.WorktreePath)
				if statusErr != nil {
					err := fmt.Errorf("repo %s: check worktree status after commit: %w", repo, statusErr)
					slog.Error("failed to check worktree status after residual commit", "repo", repo, "worktree", sess.WorktreePath, "error", err)
					results = append(results, RepoFinalizationResult{
						SubPlanID:    sess.SubPlanID,
						Repository:   repo,
						WorktreePath: sess.WorktreePath,
						Branch:       branch,
						Review:       reviewRef,
						Err:          err,
					})
					continue
				}
				if dirtyAfterCommit {
					err := fmt.Errorf("repo %s: residual changes remain after commit finalization", repo)
					slog.Error("residual changes remain after commit finalization", "repo", repo, "worktree", sess.WorktreePath, "error", err)
					results = append(results, RepoFinalizationResult{
						SubPlanID:    sess.SubPlanID,
						Repository:   repo,
						WorktreePath: sess.WorktreePath,
						Branch:       branch,
						Review:       reviewRef,
						Err:          err,
					})
					continue
				}
			}
		}

		// Push branch to remote.
		pushErr := func() error {
			if reviewCtx.RemoteName == "" {
				return nil
			}
			return gitPushBranch(ctx, bareRepo, reviewCtx.RemoteName, branch)
		}()

		if pushErr != nil {
			if isPreReceiveRejection(pushErr.Error()) {
				// A server-side hook declined the push (commit message policy,
				// secrets scan, etc.). Attempt a local fix and retry.
				if sess.WorktreePath == "" {
					err := fmt.Errorf("repo %s: push rejected by pre-receive hook and no worktree available: %w", repo, pushErr)
					slog.Error("push rejected by pre-receive hook; no worktree available to fix",
						"repo", repo, "branch", branch, "error", err)
					results = append(results, RepoFinalizationResult{
						SubPlanID:    sess.SubPlanID,
						Repository:   repo,
						WorktreePath: sess.WorktreePath,
						Branch:       branch,
						Review:       reviewRef,
						Err:          err,
					})
				} else {
					slog.Warn("push rejected by pre-receive hook; attempting local fix",
						"repo", repo, "branch", branch, "error", pushErr)
					if fixErr := s.fixPreReceiveRejectionViaAgent(ctx, sess.WorktreePath, pushErr.Error()); fixErr != nil {
						err := fmt.Errorf("repo %s: fix pre-receive rejection after push failure: %w", repo, errors.Join(pushErr, fixErr))
						slog.Error("failed to fix pre-receive hook rejection",
							"repo", repo, "branch", branch, "error", err)
						results = append(results, RepoFinalizationResult{
							SubPlanID:    sess.SubPlanID,
							Repository:   repo,
							WorktreePath: sess.WorktreePath,
							Branch:       branch,
							Review:       reviewRef,
							Err:          err,
						})
					} else if retryErr := gitPushBranch(ctx, bareRepo, reviewCtx.RemoteName, branch); retryErr != nil {
						err := fmt.Errorf("repo %s: push after pre-receive fix: %w", repo, errors.Join(pushErr, retryErr))
						slog.Error("push still failed after fixing pre-receive hook rejection",
							"repo", repo, "branch", branch, "error", err)
						results = append(results, RepoFinalizationResult{
							SubPlanID:    sess.SubPlanID,
							Repository:   repo,
							WorktreePath: sess.WorktreePath,
							Branch:       branch,
							Review:       reviewRef,
							Err:          err,
						})
					} else {
						// Push succeeded after fix
						result := RepoFinalizationResult{
							SubPlanID:    sess.SubPlanID,
							Repository:   repo,
							WorktreePath: sess.WorktreePath,
							Branch:       branch,
							Review:       reviewRef,
						}
						results = append(results, result)
						successfulByRepo[repo] = result
					}
				}
			} else {
				err := fmt.Errorf("repo %s: push branch %s: %w", repo, branch, pushErr)
				slog.Warn("failed to push branch to remote after review pass",
					"repo", repo, "branch", branch, "error", err)
				results = append(results, RepoFinalizationResult{
					SubPlanID:    sess.SubPlanID,
					Repository:   repo,
					WorktreePath: sess.WorktreePath,
					Branch:       branch,
					Review:       reviewRef,
					Err:          err,
				})
			}
		} else {
			// Push succeeded
			result := RepoFinalizationResult{
				SubPlanID:    sess.SubPlanID,
				Repository:   repo,
				WorktreePath: sess.WorktreePath,
				Branch:       branch,
				Review:       reviewRef,
			}
			results = append(results, result)
			successfulByRepo[repo] = result
		}
	}

	return results
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
// using the configured commit strategy and message format.
// Falls back to gitStageAndCommit with a generic message if the agent session fails.
func (s *ImplementationService) commitViaAgent(ctx context.Context, worktreePath, repo, _ string) error {
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

	systemPrompt := buildCommitAgentSystemPrompt(commitCfg) + "\n\n" + commitInstructions

	opts := adapter.SessionOpts{
		SessionID:    domain.NewID(),
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

	go s.forwardEvents(ctx, sess.Events(), "")

	commitCtx, cancel := context.WithTimeout(ctx, commitAgentTimeout)
	defer cancel()
	if err := sess.Wait(commitCtx); err != nil {
		slog.Warn("commit agent session failed, falling back to generic message",
			"repo", repo, "error", err)
		// kill agent before touching worktree ourselves; deferred Abort is idempotent
		if abortErr := sess.Abort(ctx); abortErr != nil {
			slog.Warn("implementation: commit agent abort failed", "error", abortErr)
		}
		return gitStageAndCommit(ctx, worktreePath, fallbackCommitMsg)
	}
	return nil
}

// lastSessionForSubPlan returns the most recent session for a sub-plan,
// regardless of phase or status. Used by the two-stage retry model:
// the last session's phase determines whether to retry implementation
// or review.
func (s *ImplementationService) lastSessionForSubPlan(ctx context.Context, subPlanID string) *domain.AgentSession {
	sessions, err := s.sessionSvc.ListBySubPlanID(ctx, subPlanID)
	if err != nil {
		slog.Warn("failed to list sessions for sub-plan",
			"error", err, "sub_plan_id", subPlanID)
		return nil
	}

	var latest *domain.AgentSession
	for i := range sessions {
		if latest == nil || sessions[i].CreatedAt.After(latest.CreatedAt) {
			t := sessions[i]
			latest = &t
		}
	}
	return latest
}

// latestCompletedImplSession returns the most recent completed implementation
// session for a sub-plan, or nil if none exists. Used by the review-retry
// path to find the impl session whose output should be reviewed.
func (s *ImplementationService) latestCompletedImplSession(ctx context.Context, subPlanID string) *domain.AgentSession {
	sessions, err := s.sessionSvc.ListBySubPlanID(ctx, subPlanID)
	if err != nil {
		slog.Warn("failed to list sessions for sub-plan, treating as no completed impl session",
			"error", err, "sub_plan_id", subPlanID)
		return nil
	}

	var latest *domain.AgentSession
	for i := range sessions {
		t := sessions[i]
		if t.Kind == domain.AgentSessionKindImplementation && t.Status == domain.AgentSessionCompleted {
			if latest == nil || t.CreatedAt.After(latest.CreatedAt) {
				latest = &t
			}
		}
	}
	return latest
}

func (s *ImplementationService) emitContinuationFailed(ctx context.Context, session domain.AgentSession, cause error) {
	if s.eventBus == nil {
		return
	}
	payload := fmt.Sprintf(`{"agent_session_id":%q,"work_item_id":%q,"sub_plan_id":%q,"error":%q}`,
		session.ID,
		session.WorkItemID,
		session.SubPlanID,
		cause.Error(),
	)
	service.Emit(s.eventBus, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventAgentSessionContinuationFailed),
		WorkspaceID: session.WorkspaceID,
		Payload:     payload,
		CreatedAt:   time.Now(),
	})
}

// persistSubPlanStatusStrict persists a sub-plan transition and returns the
// error to callers that cannot safely continue with stale durable state.
func (s *ImplementationService) persistSubPlanStatusStrict(ctx context.Context, sp *domain.TaskPlan, status domain.TaskPlanStatus) error {
	if err := s.planSvc.TransitionSubPlan(ctx, sp.ID, status); err != nil {
		return err
	}
	sp.Status = status
	sp.UpdatedAt = time.Now()
	return nil
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

func (s *ImplementationService) resetSubPlanForRetry(ctx context.Context, sp *domain.TaskPlan) {
	if err := s.planSvc.ResetSubPlanForRetry(ctx, sp.ID); err != nil {
		slog.Warn("failed to reset sub-plan for retry",
			"error", err,
			"sub_plan_id", sp.ID)
	}
	sp.Status = domain.SubPlanPending
	sp.UpdatedAt = time.Now()
}

// Helper functions

const (
	durableCleanupTimeout      = 30 * time.Second
	commitAgentTimeout         = 5 * time.Minute
	preReceiveFixAgentTimeout  = 15 * time.Minute
	durableFinalizationTimeout = commitAgentTimeout + preReceiveFixAgentTimeout + time.Minute
)

// resumeContinuationMessage is sent to a resumed harness session when there is
// no critique feedback to deliver and no explicit operator prompt. Without an
// initial user-turn after resume + compact-skip, the SDK sits idle: there is
// nothing for it to respond to, and the bridge process therefore never reaches
// its post-prompt exit path.
// This matches the graph retry/resume orientation used when a resumed harness
// session has no more specific critique or operator feedback to handle.
const resumeContinuationMessage = "You are continuing work on this sub-plan. " +
	"The worktree may contain partial changes from a previous session. " +
	"Run `git status` and `git diff` to understand current state, then " +
	"continue implementing remaining items."

// durablePRReadyContext returns a context derived from parent but insulated from
// cancellation, with a generous timeout for PR-ready marking and DB persistence.
// This ensures EventSubPlanPRReady is emitted even if the caller's context is
// canceled after finalization completes.
func durablePRReadyContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), durableCleanupTimeout)
}

func durableCleanupContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), durableCleanupTimeout)
}

func durableFinalizationContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), durableFinalizationTimeout)
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

// isPreReceiveRejection reports whether the push error output indicates that a
// server-side pre-receive hook declined the push.  This is a git-standard
// signal emitted regardless of hosting platform (GitLab, GitHub, Gitea, …).
func isPreReceiveRejection(errOutput string) bool {
	return strings.Contains(errOutput, "pre-receive hook declined")
}

// fixPreReceiveRejectionViaAgent spins up a short-lived agent session to
// attempt to fix whatever caused the remote's pre-receive hook to decline the
// push.  hookOutput is the verbatim combined output from the failed push; the
// agent uses it to diagnose the rejection and take corrective action.
//
// The agent is explicitly constrained to straightforward local fixes —
// rewording commit messages, removing a violating commit — so that it does not
// attempt complex out-of-scope actions such as opening issues or merge
// requests, changing branch-protection rules, or interacting with the remote
// hosting platform in any way.
func (s *ImplementationService) fixPreReceiveRejectionViaAgent(ctx context.Context, worktreePath, hookOutput string) error {
	prompt := "A branch push was rejected by the remote's pre-receive hook.\n\n" +
		"Your task:\n" +
		"  1. Identify which commits don't match the required commit message pattern.\n" +
		"  2. Fix the commit messages using 'git filter-branch --msg-filter' or 'git rebase -i'.\n" +
		"  3. Verify the fix by checking the commit messages match the pattern.\n\n" +
		"You MUST stop and report without making any changes if the rejection requires anything beyond " +
		"rewording or removing a commit — for example: branch protection that requires a merge request, " +
		"repository access controls, missing GPG signing keys, secrets detected in file content, or any " +
		"action that involves the remote hosting platform, CI systems, issues, or pull/merge requests. " +
		"Those cases cannot be fixed here.\n\n" +
		"Do NOT modify file content. Do NOT push. Do NOT create issues or merge requests.\n\n" +
		"Hook output:\n" + hookOutput

	opts := adapter.SessionOpts{
		SessionID:    domain.NewID(),
		Mode:         adapter.SessionModeAgent,
		WorktreePath: worktreePath,
		SystemPrompt: "You are a git expert. Fix the pre-receive hook rejection using only local commit history edits; never modify file content or interact with the remote.",
		UserPrompt:   prompt,
		AllowPush:    false,
	}
	sess, err := s.harness.StartSession(ctx, opts)
	if err != nil {
		return fmt.Errorf("start pre-receive-fix agent: %w", err)
	}
	defer sess.Abort(ctx)

	go s.forwardEvents(ctx, sess.Events(), "")
	fixCtx, cancel := context.WithTimeout(ctx, preReceiveFixAgentTimeout)
	defer cancel()
	if err := sess.Wait(fixCtx); err != nil {
		if abortErr := sess.Abort(ctx); abortErr != nil {
			slog.Warn("implementation: pre-receive-fix agent abort failed", "error", abortErr)
		}
		return fmt.Errorf("pre-receive-fix agent: %w", err)
	}
	return nil
}
