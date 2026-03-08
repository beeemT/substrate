package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
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
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
	"golang.org/x/sync/errgroup"
)

// ImplementationService orchestrates the implementation phase after plan approval.
// It manages wave-based execution of sub-plans, worktree creation, and agent sessions.
type ImplementationService struct {
	cfg          *config.Config
	harness      adapter.AgentHarness
	gitClient    *gitwork.Client
	eventBus     *event.Bus
	planSvc      *service.PlanService
	workItemSvc  *service.WorkItemService
	sessionSvc   *service.SessionService
	subPlanRepo  repository.SubPlanRepository
	sessionRepo  repository.SessionRepository
	eventRepo    repository.EventRepository
	workspaceSvc *service.WorkspaceService
	sessTimeout  time.Duration
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

// NewImplementationService creates a new ImplementationService.
func NewImplementationService(
	cfg *config.Config,
	harness adapter.AgentHarness,
	gitClient *gitwork.Client,
	eventBus *event.Bus,
	planSvc *service.PlanService,
	workItemSvc *service.WorkItemService,
	sessionSvc *service.SessionService,
	subPlanRepo repository.SubPlanRepository,
	sessionRepo repository.SessionRepository,
	eventRepo repository.EventRepository,
	workspaceSvc *service.WorkspaceService,
) *ImplementationService {
	implCfg := DefaultImplementationConfig()
	return &ImplementationService{
		cfg:          cfg,
		harness:      harness,
		gitClient:    gitClient,
		eventBus:     eventBus,
		planSvc:      planSvc,
		workItemSvc:  workItemSvc,
		sessionSvc:   sessionSvc,
		subPlanRepo:  subPlanRepo,
		sessionRepo:  sessionRepo,
		eventRepo:    eventRepo,
		workspaceSvc: workspaceSvc,
		sessTimeout:  implCfg.SessionTimeout,
	}
}

// ImplementResult contains the result of implementation execution.
type ImplementResult struct {
	PlanID      string
	WorkItemID  string
	State       *ExecutionState
	Sessions    []SessionResult
	Warnings    []ImplementationWarning
	CompletedAt time.Time
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
func (s *ImplementationService) Implement(ctx context.Context, planID string) (*ImplementResult, error) {
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
	subPlans, err := s.subPlanRepo.ListByPlanID(ctx, planID)
	if err != nil {
		return nil, fmt.Errorf("get sub-plans: %w", err)
	}

	// 6. Transition work item to implementing
	if err := s.workItemSvc.Transition(ctx, workItem.ID, domain.WorkItemImplementing); err != nil {
		return nil, fmt.Errorf("transition work item to implementing: %w", err)
	}

	// 7. Emit ImplementationStarted event
	if err := s.emitImplementationStarted(ctx, &plan, &workItem, workspace.ID); err != nil {
		slog.Warn("failed to emit implementation started event", "error", err)
	}

	// 8. Generate branch name
	branch := GenerateBranchName(workItem.ExternalID, workItem.Title)

	// 9. Initialize execution state
	state := NewExecutionState(planID, subPlans)

	// 10. Execute waves
	result := &ImplementResult{
		PlanID:     planID,
		WorkItemID: workItem.ID,
		State:      state,
		Sessions:   make([]SessionResult, 0),
		Warnings:   make([]ImplementationWarning, 0),
	}

	// Discover repos to get paths
	repoPaths, err := s.discoverRepoPaths(ctx, workspace.RootPath)
	if err != nil {
		return nil, fmt.Errorf("discover repo paths: %w", err)
	}

	// Pre-create all worktrees sequentially before fan-out to eliminate the
	// TOCTOU race where two sub-plans in the same wave could race to create
	// a worktree for the same repository.
	worktreePaths, err := s.prepareWorktrees(ctx, &workspace, workItem.Title, trackerRefsFromMetadata(workItem.Metadata), subPlans, branch, repoPaths)
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

		// Execute sub-plans in this wave concurrently
		sessionResults, warnings := s.executeWave(ctx, wave, &workspace, &plan, &workItem, branch, worktreePaths, state)
		result.Sessions = append(result.Sessions, sessionResults...)
		result.Warnings = append(result.Warnings, warnings...)

		// Check if wave completed successfully
		waveComplete := true
		for _, sr := range sessionResults {
			if sr.Status == domain.AgentSessionFailed {
				waveComplete = false
				break
			}
		}

		waveEnd := time.Now()
		if waveComplete {
			state.CompleteWave(waveIndex, waveEnd.UnixNano())
		} else {
			state.FailWave(waveIndex, waveEnd.UnixNano())
			// Stop execution on wave failure
			break
		}

		state.AdvanceWave()
	}

	result.CompletedAt = time.Now()

	// Update work item state based on overall result
	if state.AllWavesCompleted() {
		if err := s.workItemSvc.Transition(ctx, workItem.ID, domain.WorkItemReviewing); err != nil {
			slog.Warn("failed to transition work item to reviewing", "error", err)
		}
	} else {
		if err := s.workItemSvc.Transition(ctx, workItem.ID, domain.WorkItemFailed); err != nil {
			slog.Warn("failed to transition work item to failed", "error", err)
		}
	}

	return result, nil
}

// executeWave executes all sub-plans in a wave concurrently.
func (s *ImplementationService) executeWave(
	ctx context.Context,
	wave []domain.SubPlan,
	workspace *domain.Workspace,
	plan *domain.Plan,
	workItem *domain.WorkItem,
	branch string,
	worktreePaths map[string]string,
	state *ExecutionState,
) ([]SessionResult, []ImplementationWarning) {
	var results []SessionResult
	var warnings []ImplementationWarning
	var mu sync.Mutex

	g, ctx := errgroup.WithContext(ctx)

	for _, sp := range wave {
		sp := sp // capture loop variable

		g.Go(func() error {
			result, warning := s.executeSubPlan(ctx, sp, workspace, plan, workItem, branch, worktreePaths, state)

			mu.Lock()
			results = append(results, result)
			if warning != nil {
				warnings = append(warnings, *warning)
			}
			mu.Unlock()

			// If session failed, return error to cancel other goroutines
			if result.Status == domain.AgentSessionFailed {
				return fmt.Errorf("sub-plan %s failed: %s", sp.ID, result.Summary)
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
	subPlan domain.SubPlan,
	workspace *domain.Workspace,
	plan *domain.Plan,
	workItem *domain.WorkItem,
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
	subPlan.Status = domain.SubPlanInProgress
	subPlan.UpdatedAt = time.Now()
	if err := s.subPlanRepo.Update(ctx, subPlan); err != nil {
		slog.Warn("failed to update sub-plan status", "error", err, "sub_plan_id", subPlan.ID)
	}

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

	// Create agent session record
	sessionID := domain.NewID()
	session := domain.AgentSession{
		ID:             sessionID,
		WorkspaceID:    workspace.ID,
		SubPlanID:      subPlan.ID,
		RepositoryName: subPlan.RepositoryName,
		WorktreePath:   worktreePath,
		HarnessName:    s.harness.Name(),
		Status:         domain.AgentSessionPending,
	}
	if err := s.sessionSvc.Create(ctx, session); err != nil {
		result.Status = domain.AgentSessionFailed
		result.Summary = fmt.Sprintf("failed to create session record: %v", err)
		result.CompletedAt = ptrTime(time.Now())
		state.FailSubPlan(subPlan.ID, time.Now().UnixNano(), err)
		return result, &ImplementationWarning{
			Type:      "session_create_failed",
			Message:   result.Summary,
			RepoName:  subPlan.RepositoryName,
			SessionID: sessionID,
		}
	}
	result.SessionID = sessionID

	// Emit AgentSessionStarted event
	if err := s.emitSessionStarted(ctx, &session, workspace.ID); err != nil {
		slog.Warn("failed to emit session started event", "error", err)
	}

	// Transition session to running
	if err := s.sessionSvc.Start(ctx, sessionID); err != nil {
		slog.Warn("failed to start session", "error", err)
	}

	// Build session options
	sessionOpts := s.buildSessionOpts(session, subPlan, plan, workItem, workspace)

	// Start agent session
	harnessSession, err := s.harness.StartSession(ctx, sessionOpts)
	if err != nil {
		result.Status = domain.AgentSessionFailed
		result.Summary = fmt.Sprintf("failed to start agent: %v", err)
		result.CompletedAt = ptrTime(time.Now())
		_ = s.sessionSvc.Fail(ctx, sessionID, ptrInt(1))
		state.FailSubPlan(subPlan.ID, time.Now().UnixNano(), err)
		return result, &ImplementationWarning{
			Type:      "harness_start_failed",
			Message:   result.Summary,
			RepoName:  subPlan.RepositoryName,
			SessionID: sessionID,
		}
	}

	sessionCtx, sessionCancel := context.WithTimeout(ctx, s.sessTimeout)
	defer sessionCancel()

	// Forward events to bus while session runs
	go s.forwardEvents(sessionCtx, harnessSession.Events(), workspace.ID)

	// Wait for session completion
	waitErr := harnessSession.Wait(sessionCtx)

	result.CompletedAt = ptrTime(time.Now())

	if waitErr != nil {
		result.Status = domain.AgentSessionFailed
		result.Summary = waitErr.Error()
		result.ExitCode = ptrInt(1)
		if err := s.sessionSvc.Fail(ctx, sessionID, ptrInt(1)); err != nil {
			slog.Warn("failed to fail session", "error", err)
		}
		state.FailSubPlan(subPlan.ID, time.Now().UnixNano(), waitErr)
	} else {
		result.Status = domain.AgentSessionCompleted
		result.Summary = "Session completed successfully"
		if err := s.sessionSvc.Complete(ctx, sessionID); err != nil {
			slog.Warn("failed to complete session", "error", err)
		}
		state.CompleteSubPlan(subPlan.ID, time.Now().UnixNano())

		// Update sub-plan to completed
		subPlan.Status = domain.SubPlanCompleted
		subPlan.UpdatedAt = time.Now()
		if err := s.subPlanRepo.Update(ctx, subPlan); err != nil {
			slog.Warn("failed to update sub-plan status to completed", "error", err)
		}
	}

	// Emit session completed/failed event
	if result.Status == domain.AgentSessionCompleted {
		if err := s.emitSessionCompleted(ctx, &session, workspace.ID); err != nil {
			slog.Warn("failed to emit session completed event", "error", err)
		}
	} else {
		if err := s.emitSessionFailed(ctx, &session, result.Summary, workspace.ID); err != nil {
			slog.Warn("failed to emit session failed event", "error", err)
		}
	}

	return result, nil
}

// ensureWorktree creates a worktree if it doesn't exist, or returns the existing one.
// Implements idempotency guard by checking git-work list first.
func (s *ImplementationService) ensureWorktree(
	ctx context.Context,
	workspace *domain.Workspace,
	repoName, repoPath, branch, workItemTitle string, trackerRefs []domain.TrackerReference, subPlan string,
) (string, error) {
	// Check if worktree already exists (idempotency guard)
	worktrees, err := s.gitClient.List(ctx, repoPath)
	if err != nil {
		return "", fmt.Errorf("list worktrees: %w", err)
	}

	for _, wt := range worktrees {
		if wt.Branch == branch {
			slog.Info("worktree already exists, skipping creation",
				"repo", repoName,
				"branch", branch,
				"path", wt.Path)
			return wt.Path, nil
		}
	}

	reviewCtx, err := remotedetect.ResolveReviewContext(ctx, repoPath)
	if err != nil {
		return "", fmt.Errorf("resolve review context: %w", err)
	}

	// Emit WorktreeCreating pre-hook event
	creatingPayload := WorktreeCreatingPayload{
		WorkspaceID:   workspace.ID,
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
		Payload:     marshalJSONOrEmpty(creatingPayload),
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

	// Emit WorktreeCreated post-hook event
	createdPayload := WorktreeCreatedPayload{
		WorkspaceID:   workspace.ID,
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
		Payload:     marshalJSONOrEmpty(createdPayload),
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
	workItemTitle string,
	trackerRefs []domain.TrackerReference,
	subPlans []domain.SubPlan,
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
		wt, err := s.ensureWorktree(ctx, workspace, sp.RepositoryName, repoPath, branch, workItemTitle, trackerRefs, sp.Content)
		if err != nil {
			return nil, fmt.Errorf("prepare worktree for %s: %w", sp.RepositoryName, err)
		}
		worktreePaths[sp.RepositoryName] = wt
	}
	return worktreePaths, nil
}

// buildSessionOpts builds session options for an agent session.
func (s *ImplementationService) buildSessionOpts(
	session domain.AgentSession,
	subPlan domain.SubPlan,
	plan *domain.Plan,
	workItem *domain.WorkItem,
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
	systemPrompt := s.buildSystemPrompt(subPlan, plan, workItem, docContext)

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
	}
}

// buildSystemPrompt builds the system prompt for an agent session.
func (s *ImplementationService) buildSystemPrompt(
	subPlan domain.SubPlan,
	plan *domain.Plan,
	workItem *domain.WorkItem,
	docContext string,
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

	prompt.WriteString("## Validation\n")
	prompt.WriteString("Before marking complete: run all relevant formatters, compilation checks, and unit tests.\n")
	prompt.WriteString("All must pass. Refer to AGENTS.md in this repo for tooling specifics.\n")

	return prompt.String()
}

// forwardEvents forwards agent events to the event bus.
func (s *ImplementationService) forwardEvents(ctx context.Context, events <-chan adapter.AgentEvent, workspaceID string) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-events:
			if !ok {
				return
			}
			// Convert agent event to system event and publish to bus
			sysEvent := domain.SystemEvent{
				ID:          domain.NewID(),
				EventType:   string(evt.Type),
				WorkspaceID: workspaceID,
				Payload:     marshalJSONOrEmpty(evt.Payload),
				CreatedAt:   time.Now(),
			}
			if err := s.eventBus.Publish(ctx, sysEvent); err != nil {
				slog.Warn("failed to forward agent event to bus", "error", err, "type", evt.Type)
			}
		}
	}
}

// discoverRepoPaths discovers repo paths in the workspace.
func (s *ImplementationService) discoverRepoPaths(ctx context.Context, workspaceDir string) (map[string]string, error) {
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
	Repository    string           `json:"repository"`
	Branch        string           `json:"branch"`
	WorkItemTitle string           `json:"work_item_title"`
	SubPlan       string           `json:"sub_plan"`
	Review        domain.ReviewRef `json:"review"`
}

type WorktreeCreatedPayload struct {
	WorkspaceID   string                    `json:"workspace_id"`
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

func (s *ImplementationService) emitImplementationStarted(ctx context.Context, plan *domain.Plan, workItem *domain.WorkItem, workspaceID string) error {
	payload := map[string]interface{}{
		"plan_id":   plan.ID,
		"work_item": workItem,
	}
	evt := domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventImplementationStarted),
		WorkspaceID: workspaceID,
		Payload:     marshalJSONOrEmpty(payload),
		CreatedAt:   time.Now(),
	}
	return s.eventRepo.Create(ctx, evt)
}

func (s *ImplementationService) emitSessionStarted(ctx context.Context, session *domain.AgentSession, workspaceID string) error {
	payload := map[string]interface{}{
		"session_id":    session.ID,
		"sub_plan_id":   session.SubPlanID,
		"repository":    session.RepositoryName,
		"worktree_path": session.WorktreePath,
	}
	evt := domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventAgentSessionStarted),
		WorkspaceID: workspaceID,
		Payload:     marshalJSONOrEmpty(payload),
		CreatedAt:   time.Now(),
	}
	return s.eventRepo.Create(ctx, evt)
}

func (s *ImplementationService) emitSessionCompleted(ctx context.Context, session *domain.AgentSession, workspaceID string) error {
	payload := map[string]interface{}{
		"session_id":    session.ID,
		"sub_plan_id":   session.SubPlanID,
		"repository":    session.RepositoryName,
		"worktree_path": session.WorktreePath,
	}
	evt := domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventAgentSessionCompleted),
		WorkspaceID: workspaceID,
		Payload:     marshalJSONOrEmpty(payload),
		CreatedAt:   time.Now(),
	}
	return s.eventRepo.Create(ctx, evt)
}

func (s *ImplementationService) emitSessionFailed(ctx context.Context, session *domain.AgentSession, errMsg string, workspaceID string) error {
	payload := map[string]interface{}{
		"session_id":    session.ID,
		"sub_plan_id":   session.SubPlanID,
		"repository":    session.RepositoryName,
		"worktree_path": session.WorktreePath,
		"error":         errMsg,
	}
	evt := domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventAgentSessionFailed),
		WorkspaceID: workspaceID,
		Payload:     marshalJSONOrEmpty(payload),
		CreatedAt:   time.Now(),
	}
	return s.eventRepo.Create(ctx, evt)
}

// Helper functions

func marshalJSONOrEmpty(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		slog.Error("marshalJSONOrEmpty: failed to marshal event payload",
			"type", fmt.Sprintf("%T", v),
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
