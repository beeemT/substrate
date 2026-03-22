package orchestrator

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/gitwork"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
)

// PlanningConfig contains configuration for the planning pipeline.
type PlanningConfig struct {
	MaxParseRetries int
	SessionTimeout  time.Duration
}

// DefaultPlanningConfig returns the default planning configuration.
func DefaultPlanningConfig() *PlanningConfig {
	return &PlanningConfig{
		MaxParseRetries: 2,
		SessionTimeout:  30 * time.Minute,
	}
}

// PlanningConfigFromConfig extracts planning config from global config.
func PlanningConfigFromConfig(cfg *config.Config) *PlanningConfig {
	pcfg := DefaultPlanningConfig()
	if cfg != nil && cfg.Plan.MaxParseRetries != nil {
		pcfg.MaxParseRetries = *cfg.Plan.MaxParseRetries
	}
	return pcfg
}

// PlanningService orchestrates the planning pipeline.
type PlanningService struct {
	cfg          *PlanningConfig
	discoverer   *Discoverer
	gitClient    *gitwork.Client
	harness      adapter.AgentHarness
	planSvc      *service.PlanService
	workItemSvc  *service.SessionService
	sessionSvc   *service.TaskService
	planRepo     repository.PlanRepository
	subPlanRepo  repository.TaskPlanRepository
	eventRepo    repository.EventRepository
	workspaceSvc *service.WorkspaceService
	registry     *SessionRegistry
	globalCfg    *config.Config
	templates    *PlanningTemplates
}

// PlanningTemplates holds compiled templates.
type PlanningTemplates struct {
	planning   *template.Template
	correction *template.Template
	revision   *template.Template
	followUp   *template.Template
}

// NewPlanningTemplates creates compiled templates.
func NewPlanningTemplates() (*PlanningTemplates, error) {
	planningTmpl, err := template.New("planning").Parse(planningPromptTmpl)
	if err != nil {
		return nil, fmt.Errorf("parse planning template: %w", err)
	}

	correctionTmpl, err := template.New("correction").Parse(correctionPromptTmpl)
	if err != nil {
		return nil, fmt.Errorf("parse correction template: %w", err)
	}

	revisionTmpl, err := template.New("revision").Parse(revisionPromptTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse revision template: %w", err)
	}

	followUpTmpl, err := template.New("followUp").Parse(followUpPromptTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse follow-up template: %w", err)
	}

	return &PlanningTemplates{
		planning:   planningTmpl,
		correction: correctionTmpl,
		revision:   revisionTmpl,
		followUp:   followUpTmpl,
	}, nil
}

func NewPlanningService(
	cfg *PlanningConfig,
	discoverer *Discoverer,
	gitClient *gitwork.Client,
	harness adapter.AgentHarness,
	planSvc *service.PlanService,
	workItemSvc *service.SessionService,
	sessionSvc *service.TaskService,
	planRepo repository.PlanRepository,
	subPlanRepo repository.TaskPlanRepository,
	eventRepo repository.EventRepository,
	workspaceSvc *service.WorkspaceService,
	registry *SessionRegistry,
	globalCfg *config.Config,
) (*PlanningService, error) {
	templates, err := NewPlanningTemplates()
	if err != nil {
		return nil, fmt.Errorf("create templates: %w", err)
	}

	return &PlanningService{
		cfg:          cfg,
		discoverer:   discoverer,
		gitClient:    gitClient,
		harness:      harness,
		planSvc:      planSvc,
		workItemSvc:  workItemSvc,
		sessionSvc:   sessionSvc,
		planRepo:     planRepo,
		subPlanRepo:  subPlanRepo,
		eventRepo:    eventRepo,
		workspaceSvc: workspaceSvc,
		registry:     registry,
		globalCfg:    globalCfg,
		templates:    templates,
	}, nil
}

// Plan executes the planning pipeline for a work item in Ingested state.
func (s *PlanningService) Plan(ctx context.Context, workItemID string) (*domain.PlanningResult, error) {
	if err := s.workItemSvc.StartPlanning(ctx, workItemID); err != nil {
		return nil, fmt.Errorf("transition work item to planning: %w", err)
	}
	return s.planRun(ctx, workItemID, "", "")
}

// PlanWithFeedback runs a revision planning session for a work item in plan_review state.
// It rejects the existing plan and re-plans with the human's feedback embedded in the prompt.
func (s *PlanningService) PlanWithFeedback(ctx context.Context, workItemID, oldPlanID, feedback string) (*domain.PlanningResult, error) {
	// Capture plan text before rejecting so the revision prompt has context.
	currentPlanText := s.buildPlanText(ctx, oldPlanID)
	if err := s.planSvc.RejectPlan(ctx, oldPlanID); err != nil {
		return nil, fmt.Errorf("reject old plan: %w", err)
	}
	if err := s.workItemSvc.RejectPlan(ctx, workItemID); err != nil {
		return nil, fmt.Errorf("transition work item to planning: %w", err)
	}
	return s.planRun(ctx, workItemID, feedback, currentPlanText)
}


// FollowUpPlan transitions a completed work item back to planning with differential context.
// It captures the current plan, implementation results, and user feedback to produce an updated plan.
func (s *PlanningService) FollowUpPlan(ctx context.Context, workItemID, feedback string) (*domain.PlanningResult, error) {
	// 1. Verify work item is completed
	workItem, err := s.workItemSvc.Get(ctx, workItemID)
	if err != nil {
		return nil, fmt.Errorf("get work item: %w", err)
	}
	if workItem.State != domain.SessionCompleted {
		return nil, fmt.Errorf("work item %s is in state %s, expected completed", workItemID, workItem.State)
	}

	// 2. Find the current approved plan
	currentPlan, err := s.planSvc.GetPlanByWorkItemID(ctx, workItemID)
	if err != nil {
		return nil, fmt.Errorf("get plan for work item: %w", err)
	}
	if currentPlan.Status != domain.PlanApproved {
		return nil, fmt.Errorf("no approved plan found for work item %s (current status: %s)", workItemID, currentPlan.Status)
	}

	// 3. Capture plan text before transitioning
	currentPlanText := s.buildPlanText(ctx, currentPlan.ID)

	// 4. Build repo result summaries from session logs
	repoResults := s.buildRepoResultSummaries(ctx, currentPlan.ID)

	// 5. Render the follow-up prompt
	var buf bytes.Buffer
	if err := s.templates.followUp.Execute(&buf, FollowUpData{
		Feedback:    feedback,
		CurrentPlan: currentPlanText,
		RepoResults: repoResults,
	}); err != nil {
		return nil, fmt.Errorf("render follow-up template: %w", err)
	}
	followUpContext := buf.String()

	// 6. Transition work item to planning for follow-up
	if err := s.workItemSvc.StartFollowUpPlanning(ctx, workItemID); err != nil {
		return nil, fmt.Errorf("transition work item to planning: %w", err)
	}

	// 7. Run planning with the follow-up context as the revision feedback.
	// Pass empty currentPlanText to planRun since the follow-up template already
	// embeds the plan; this avoids double-rendering via the revision template.
	return s.planRun(ctx, workItemID, followUpContext, "")
}

// buildRepoResultSummaries collects implementation results for each repo in the plan.
func (s *PlanningService) buildRepoResultSummaries(ctx context.Context, planID string) []RepoResultSummary {
	subPlans, err := s.subPlanRepo.ListByPlanID(ctx, planID)
	if err != nil {
		slog.Warn("failed to list sub-plans for follow-up summaries", "error", err, "plan_id", planID)
		return nil
	}

	var results []RepoResultSummary
	for _, sp := range subPlans {
		result := RepoResultSummary{
			RepoName: sp.RepositoryName,
			Status:   string(sp.Status),
		}

		// Try to get the most recent session log for this sub-plan
		sessions, err := s.sessionSvc.ListBySubPlanID(ctx, sp.ID)
		if err == nil && len(sessions) > 0 {
			// Use the most recent session (last in list)
			latestSession := sessions[len(sessions)-1]
			logPath := sessionLogPath(latestSession.ID)
			if logPath != "" {
				result.LogTail = readLogTail(logPath, 50)
			}
		}

		results = append(results, result)
	}

	return results
}

// sessionLogPath derives the log file path for a session. Best-effort.
func sessionLogPath(sessionID string) string {
	dir, err := config.SessionsDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, sessionID+".log")
}

// readLogTail reads the last n lines from a log file. Best-effort.
func readLogTail(path string, n int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}

	return strings.Join(lines, "\n")
}
// buildPlanText reconstructs the full persisted plan document for review revisions.
// Returns empty string on any error (best-effort; revision can proceed without prior context).
func (s *PlanningService) buildPlanText(ctx context.Context, planID string) string {
	plan, err := s.planSvc.GetPlan(ctx, planID)
	if err != nil {
		return ""
	}
	subPlans, err := s.planSvc.ListSubPlansByPlanID(ctx, planID)
	if err != nil {
		return domain.ComposePlanDocument(plan, nil)
	}
	return domain.ComposePlanDocument(plan, subPlans)
}

// UpdateReviewedPlan validates and persists a full reviewed plan document in place.
func (s *PlanningService) UpdateReviewedPlan(ctx context.Context, planID, rawContent string) (domain.Plan, []domain.TaskPlan, error) {
	plan, err := s.planSvc.GetPlan(ctx, planID)
	if err != nil {
		return domain.Plan{}, nil, fmt.Errorf("get plan: %w", err)
	}
	workItem, err := s.workItemSvc.Get(ctx, plan.WorkItemID)
	if err != nil {
		return domain.Plan{}, nil, fmt.Errorf("get work item: %w", err)
	}
	workspace, err := s.workspaceSvc.Get(ctx, workItem.WorkspaceID)
	if err != nil {
		return domain.Plan{}, nil, fmt.Errorf("get workspace: %w", err)
	}
	healthCheck, err := s.discoverer.PreflightCheck(ctx, workspace.RootPath)
	if err != nil {
		return domain.Plan{}, nil, fmt.Errorf("preflight check: %w", err)
	}
	repos, err := s.discoverer.DiscoverRepos(ctx, workspace.RootPath, healthCheck.GitWorkRepos)
	if err != nil {
		return domain.Plan{}, nil, fmt.Errorf("discover repos: %w", err)
	}
	parser := NewPlanParser()
	rawOutput, parseErrors := parser.ParseAndValidate(rawContent, repos)
	if parseErrors.HasErrors() {
		return domain.Plan{}, nil, fmt.Errorf("plan parsing failed: %w", &parseErrors)
	}
	updatedPlan, updatedSubPlans, err := s.planSvc.ApplyReviewedPlanOutput(ctx, planID, rawOutput)
	if err != nil {
		return domain.Plan{}, nil, fmt.Errorf("apply reviewed plan: %w", err)
	}
	return updatedPlan, updatedSubPlans, nil
}

// planRun executes the planning pipeline for a work item already in the planning state.
// revisionFeedback and currentPlanText are empty for initial planning.
func (s *PlanningService) planRun(ctx context.Context, workItemID, revisionFeedback, currentPlanText string) (*domain.PlanningResult, error) {
	// 1. Get the work item
	workItem, err := s.workItemSvc.Get(ctx, workItemID)
	if err != nil {
		return nil, fmt.Errorf("get work item: %w", err)
	}

	// 2. Get the workspace
	workspace, err := s.workspaceSvc.Get(ctx, workItem.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("get workspace: %w", err)
	}

	// 3. Perform preflight check and pull main worktrees
	healthCheck, err := s.discoverer.PreflightCheck(ctx, workspace.RootPath)
	if err != nil {
		return nil, fmt.Errorf("preflight check: %w", err)
	}
	pullFailures := s.discoverer.PullMainWorktrees(ctx, healthCheck.GitWorkRepos)
	healthCheck.PullFailures = pullFailures

	// 4. Discover repos with metadata
	repos, err := s.discoverer.DiscoverRepos(ctx, workspace.RootPath, healthCheck.GitWorkRepos)
	if err != nil {
		return nil, fmt.Errorf("discover repos: %w", err)
	}

	// 5. Read workspace AGENTS.md
	workspaceAgentsMd, err := ReadWorkspaceAgentsMd(workspace.RootPath)
	if err != nil {
		slog.Warn("failed to read workspace AGENTS.md", "error", err)
	}

	// 6. Allocate and persist the planning session before launching the harness.
	sessionID := domain.NewID()
	planningSession := domain.Task{
		ID:          sessionID,
		WorkItemID:  workItem.ID,
		WorkspaceID: workspace.ID,
		Phase:       domain.TaskPhasePlanning,
		HarnessName: s.harness.Name(),
		Status:      domain.AgentSessionPending,
	}
	if err := s.sessionSvc.Create(ctx, planningSession); err != nil {
		_ = s.workItemSvc.Transition(ctx, workItemID, domain.SessionIngested)
		return nil, fmt.Errorf("create planning session: %w", err)
	}

	// 7. Create session directory.
	sessionDir, err := EnsureSessionDir(workspace.RootPath, sessionID)
	if err != nil {
		deleteOrFailPendingSession(ctx, s.sessionSvc, sessionID, ptrInt(1))
		_ = s.workItemSvc.Transition(ctx, workItemID, domain.SessionIngested)
		return nil, fmt.Errorf("create session directory: %w", err)
	}

	// 8. Transition the planning session to running before launching the harness.
	if err := s.sessionSvc.Start(ctx, sessionID); err != nil {
		deleteOrFailPendingSession(ctx, s.sessionSvc, sessionID, ptrInt(1))
		_ = s.workItemSvc.Transition(ctx, workItemID, domain.SessionIngested)
		return nil, fmt.Errorf("transition planning session to running: %w", err)
	}

	// 9. Build planning context.
	planningCtx := &domain.PlanningContext{
		WorkItem: domain.WorkItemSnapshot{
			ID:          workItem.ID,
			ExternalID:  workItem.ExternalID,
			Title:       workItem.Title,
			Description: workItem.Description,
			Labels:      workItem.Labels,
			Source:      workItem.Source,
		},
		WorkspaceAgentsMd: workspaceAgentsMd,
		Repos:             repos,
		SessionID:         sessionID,
		SessionDraftPath:  sessionDir.DraftPath,
		MaxParseRetries:   s.cfg.MaxParseRetries,
		RevisionFeedback:  revisionFeedback,
		CurrentPlanText:   currentPlanText,
	}

	// 10. Emit PlanningStarted event.
	if err := s.emitPlanningStartedEvent(ctx, workItemID, sessionID, workspace.ID); err != nil {
		slog.Warn("failed to emit planning started event", "error", err)
	}

	rawContent, retries, warnings, planErr := s.runPlanningWithCorrectionLoop(ctx, planningCtx, workItem.WorkspaceID)
	if planErr != nil {
		if failErr := failSessionDurably(ctx, s.sessionSvc, sessionID, ptrInt(1)); failErr != nil {
			slog.Warn("failed to fail planning session", "error", failErr, "session_id", sessionID)
		}
		if emitErr := s.emitPlanFailedEvent(ctx, workItemID, planningCtx.SessionID, workspace.ID, planErr.ParseErrors); emitErr != nil {
			slog.Warn("failed to emit plan failed event", "error", emitErr)
		}
		_ = s.workItemSvc.Transition(ctx, workItemID, domain.SessionIngested)
		return &domain.PlanningResult{
			Warnings:    append(warnings, healthCheck.ToPlanningWarnings()...),
			ParseErrors: planErr.ParseErrors,
			Retries:     retries,
		}, planErr
	}

	// 11. Parse and validate the final plan.
	parser := NewPlanParser()
	rawOutput, parseErrors := parser.ParseAndValidate(rawContent, repos)
	if parseErrors.HasErrors() {
		slog.Error("plan parsing failed after correction loop", "errors", parseErrors.Error())
		if failErr := failSessionDurably(ctx, s.sessionSvc, sessionID, ptrInt(1)); failErr != nil {
			slog.Warn("failed to fail planning session after parse failure", "error", failErr, "session_id", sessionID)
		}
		if emitErr := s.emitPlanFailedEvent(ctx, workItemID, planningCtx.SessionID, workspace.ID, &parseErrors); emitErr != nil {
			slog.Warn("failed to emit plan failed event", "error", emitErr)
		}
		_ = s.workItemSvc.Transition(ctx, workItemID, domain.SessionIngested)
		return &domain.PlanningResult{
			Warnings:    append(warnings, healthCheck.ToPlanningWarnings()...),
			ParseErrors: &parseErrors,
			Retries:     retries,
		}, fmt.Errorf("plan parsing failed: %w", &parseErrors)
	}

	// 12. Build and persist plan + sub-plans.
	plan, subPlans, err := s.buildAndPersistPlan(ctx, rawOutput, workItem)
	if err != nil {
		if failErr := failSessionDurably(ctx, s.sessionSvc, sessionID, ptrInt(1)); failErr != nil {
			slog.Warn("failed to fail planning session after persistence error", "error", failErr, "session_id", sessionID)
		}
		if emitErr := s.emitPlanFailedEvent(ctx, workItemID, planningCtx.SessionID, workspace.ID, nil); emitErr != nil {
			slog.Warn("failed to emit plan failed event", "error", emitErr)
		}
		_ = s.workItemSvc.Transition(ctx, workItemID, domain.SessionIngested)
		return nil, fmt.Errorf("persist plan: %w", err)
	}

	// 13. Transition work item to plan_review.
	if err := s.workItemSvc.SubmitPlanForReview(ctx, workItemID); err != nil {
		if failErr := failSessionDurably(ctx, s.sessionSvc, sessionID, ptrInt(1)); failErr != nil {
			slog.Warn("failed to fail planning session after state transition error", "error", failErr, "session_id", sessionID)
		}
		if emitErr := s.emitPlanFailedEvent(ctx, workItemID, planningCtx.SessionID, workspace.ID, nil); emitErr != nil {
			slog.Warn("failed to emit plan failed event", "error", emitErr)
		}
		return nil, fmt.Errorf("transition work item to plan review: %w", err)
	}

	if completeErr := completeSessionDurably(ctx, s.sessionSvc, sessionID); completeErr != nil {
		slog.Warn("failed to complete planning session", "error", completeErr, "session_id", sessionID)
	}

	// 14. Emit PlanGenerated event.
	if err := s.emitPlanGeneratedEvent(ctx, plan.ID, workItemID, plan.Version, workspace.ID); err != nil {
		slog.Warn("failed to emit plan generated event", "error", err)
	}

	return &domain.PlanningResult{
		Plan:     plan,
		SubPlans: subPlans,
		Warnings: append(warnings, healthCheck.ToPlanningWarnings()...),
		Retries:  retries,
	}, nil
}

// PlanningError represents a planning failure with optional parse errors.
type PlanningError struct {
	Err         error
	ParseErrors *domain.ParseErrors
}

func (e *PlanningError) Error() string {
	return e.Err.Error()
}

func (e *PlanningError) Unwrap() error {
	return e.Err
}

// runPlanningWithCorrectionLoop runs the planning session and handles retries.
func (s *PlanningService) runPlanningWithCorrectionLoop(
	ctx context.Context,
	planningCtx *domain.PlanningContext,
	workspaceID string,
) (string, int, []domain.PlanningWarning, *PlanningError) {
	var warnings []domain.PlanningWarning
	maxRetries := s.cfg.MaxParseRetries
	draftPath := planningCtx.SessionDraftPath

	// Build discovered repo names list
	discoveredRepoNames := make([]string, 0, len(planningCtx.Repos))
	for _, repo := range planningCtx.Repos {
		discoveredRepoNames = append(discoveredRepoNames, repo.Name)
	}

	// Build system prompt
	systemPrompt, err := s.renderPlanningPrompt(planningCtx)
	if err != nil {
		return "", 0, warnings, &PlanningError{Err: fmt.Errorf("render planning prompt: %w", err)}
	}

	// Create session options
	sessionOpts := adapter.SessionOpts{
		SessionID:    planningCtx.SessionID,
		Mode:         adapter.SessionModeAgent,
		WorkspaceID:  workspaceID,
		DraftPath:    draftPath,
		SystemPrompt: systemPrompt,
		UserPrompt: fmt.Sprintf(
			"Your role is to plan this change. You are not implementing anything.\n\n"+
				"Begin planning. Write the plan progressively to %s. "+
				"Explore the workspace and determine which repos need changes.",
			draftPath,
		),
		WorktreePath: "", // Planning uses workspace root
	}

	// Apply session timeout — bounds the entire planning session lifetime.
	sessionCtx, sessionCancel := context.WithTimeout(ctx, s.cfg.SessionTimeout)
	defer sessionCancel()

	// Start the session
	session, err := s.harness.StartSession(sessionCtx, sessionOpts)
	if err != nil {
		return "", 0, warnings, &PlanningError{Err: fmt.Errorf("start planning session: %w", err)}
	}
	defer session.Abort(sessionCtx)

	// Register session for steering.
	if s.registry != nil {
		s.registry.Register(planningCtx.SessionID, session)
		defer s.registry.Deregister(planningCtx.SessionID)
	}

	parser := NewPlanParser()
	attempt := 0

	for attempt <= maxRetries {
		// Wait for the current planning turn to finish before treating the draft as final.
		if err := s.waitForPlanningTurn(sessionCtx, session); err != nil {
			return "", attempt, warnings, &PlanningError{Err: fmt.Errorf("wait for planner turn: %w", err)}
		}

		// Read the latest draft file after the planner signals that its turn is complete.
		draftContent, err := os.ReadFile(draftPath)
		if err != nil {
			if os.IsNotExist(err) {
				// Draft file doesn't exist - send correction.
				if attempt < maxRetries {
					correctionMsg := s.buildCorrectionMessage(domain.ParseErrors{MissingBlock: true}, discoveredRepoNames, draftPath)
					if sendErr := session.SendMessage(sessionCtx, correctionMsg); sendErr != nil {
						slog.Warn("failed to send correction message", "error", sendErr)
					}
					attempt++
					continue
				}
				return "", attempt, warnings, &PlanningError{
					Err:         fmt.Errorf("plan draft file not created after %d attempts", attempt),
					ParseErrors: &domain.ParseErrors{MissingBlock: true},
				}
			}
			return "", attempt, warnings, &PlanningError{Err: fmt.Errorf("read draft file: %w", err)}
		}

		// Parse and validate the draft.
		_, parseErrors := parser.ParseAndValidate(string(draftContent), planningCtx.Repos)
		if !parseErrors.HasErrors() {
			return string(draftContent), attempt, warnings, nil
		}

		// Parse errors - send correction if we have retries left.
		if attempt < maxRetries {
			warnings = append(warnings, domain.PlanningWarning{
				Type:    "parse_error",
				Message: parseErrors.Error(),
				Path:    draftPath,
			})

			correctionMsg := s.buildCorrectionMessage(parseErrors, discoveredRepoNames, draftPath)
			if sendErr := session.SendMessage(sessionCtx, correctionMsg); sendErr != nil {
				slog.Warn("failed to send correction message", "error", sendErr)
			}
			attempt++
			continue
		}

		// Exhausted retries.
		return "", attempt, warnings, &PlanningError{
			Err:         fmt.Errorf("plan parsing failed after %d attempts: %s", attempt, parseErrors.Error()),
			ParseErrors: &parseErrors,
		}
	}

	return "", attempt, warnings, &PlanningError{Err: errors.New("max retries exceeded")}
}

// waitForPlanningTurn waits for the planner to signal that its current turn is complete.
// It returns nil on a done event.
// It returns an error if the context is cancelled, the session emits an error, or the
// session closes before signaling completion.
func (s *PlanningService) waitForPlanningTurn(ctx context.Context, session adapter.AgentSession) error {
	events := session.Events()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case evt, ok := <-events:
			if !ok {
				return errors.New("agent session ended before planner signaled completion")
			}
			switch evt.Type {
			case "done":
				return nil
			case "error":
				if strings.TrimSpace(evt.Payload) == "" {
					return errors.New("planner session failed")
				}
				return fmt.Errorf("planner session failed: %s", evt.Payload)
			}
		}
	}
}

// buildCorrectionMessage builds a correction message for the agent.
func (s *PlanningService) buildCorrectionMessage(errors domain.ParseErrors, discoveredRepos []string, draftPath string) string {
	var buf bytes.Buffer
	data := CorrectionTemplateData{
		Errors:           errors.Error(),
		DiscoveredRepos:  discoveredRepos,
		SessionDraftPath: draftPath,
	}
	if err := s.templates.correction.Execute(&buf, data); err != nil {
		slog.Warn("failed to render correction template", "error", err)
		return fmt.Sprintf("Your plan had errors: %s. Valid repos: %v. Rewrite %s.", errors.Error(), discoveredRepos, draftPath)
	}
	return buf.String()
}

// renderPlanningPrompt renders the planning prompt.
// When ctx.RevisionFeedback is set, it renders the revision prompt instead.
func (s *PlanningService) renderPlanningPrompt(ctx *domain.PlanningContext) (string, error) {
	if ctx.RevisionFeedback != "" {
		var buf bytes.Buffer
		if err := s.templates.revision.Execute(&buf, RevisionData{
			Feedback:            ctx.RevisionFeedback,
			CurrentPlan:         ctx.CurrentPlanText,
			NewSessionDraftPath: ctx.SessionDraftPath,
		}); err != nil {
			return "", err
		}
		return buf.String(), nil
	}
	var buf bytes.Buffer
	if err := s.templates.planning.Execute(&buf, ctx); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// buildAndPersistPlan creates and persists the plan and sub-plans.
func (s *PlanningService) buildAndPersistPlan(
	ctx context.Context,
	rawOutput domain.RawPlanOutput,
	workItem domain.Session,
) (*domain.Plan, []domain.TaskPlan, error) {
	now := time.Now().UTC()
	planID := domain.NewID()

	// Create plan
	plan := &domain.Plan{
		ID:               planID,
		WorkItemID:       workItem.ID,
		Status:           domain.PlanDraft,
		OrchestratorPlan: rawOutput.Orchestration,
		Version:          1,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	if err := s.planRepo.Create(ctx, *plan); err != nil {
		return nil, nil, fmt.Errorf("create plan: %w", err)
	}

	// Create sub-plans
	var subPlans []domain.TaskPlan
	for _, sp := range rawOutput.SubPlans {
		subPlan := domain.TaskPlan{
			ID:             domain.NewID(),
			PlanID:         planID,
			RepositoryName: sp.RepoName,
			Content:        sp.Content,
			Order:          findOrderForRepo(sp.RepoName, rawOutput.ExecutionGroups),
			Status:         domain.SubPlanPending,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := s.subPlanRepo.Create(ctx, subPlan); err != nil {
			return nil, nil, fmt.Errorf("create sub-plan for %s: %w", sp.RepoName, err)
		}
		subPlans = append(subPlans, subPlan)
	}

	return plan, subPlans, nil
}

// emitPlanningStartedEvent emits a PlanningStarted event.
func (s *PlanningService) emitPlanningStartedEvent(ctx context.Context, workItemID, sessionID, workspaceID string) error {
	evt := domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventWorkItemPlanning),
		WorkspaceID: workspaceID,
		Payload:     fmt.Sprintf(`{"work_item_id":"%s","session_id":"%s"}`, workItemID, sessionID),
		CreatedAt:   time.Now().UTC(),
	}
	return s.eventRepo.Create(ctx, evt)
}

// emitPlanGeneratedEvent emits a PlanGenerated event.
func (s *PlanningService) emitPlanGeneratedEvent(ctx context.Context, planID, workItemID string, version int, workspaceID string) error {
	evt := domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventPlanGenerated),
		WorkspaceID: workspaceID,
		Payload:     fmt.Sprintf(`{"plan_id":"%s","work_item_id":"%s","version":%d}`, planID, workItemID, version),
		CreatedAt:   time.Now().UTC(),
	}
	return s.eventRepo.Create(ctx, evt)
}

// emitPlanFailedEvent emits a PlanFailed event.
func (s *PlanningService) emitPlanFailedEvent(ctx context.Context, workItemID, sessionID, workspaceID string, parseErrors *domain.ParseErrors) error {
	type planFailedPayload struct {
		WorkItemID  string  `json:"work_item_id"`
		SessionID   string  `json:"session_id"`
		ParseErrors *string `json:"parse_errors,omitempty"`
	}
	p := planFailedPayload{WorkItemID: workItemID, SessionID: sessionID}
	if parseErrors != nil {
		errStr := parseErrors.Error()
		p.ParseErrors = &errStr
	}
	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshal plan failed payload: %w", err)
	}
	evt := domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventPlanFailed),
		WorkspaceID: workspaceID,
		Payload:     string(data),
		CreatedAt:   time.Now().UTC(),
	}
	return s.eventRepo.Create(ctx, evt)
}

// findOrderForRepo finds the execution order for a repo from the execution groups.
func findOrderForRepo(repoName string, groups [][]string) int {
	for i, group := range groups {
		for _, name := range group {
			if strings.EqualFold(name, repoName) {
				return i
			}
		}
	}
	return 0
}

// CorrectionTemplateData is data for the correction template.
type CorrectionTemplateData struct {
	Errors           string
	DiscoveredRepos  []string
	SessionDraftPath string
}

// planningPromptTmpl is the planning prompt template.
const planningPromptTmpl = `{{if .WorkspaceAgentsMd}}## Workspace Guidance
{{.WorkspaceAgentsMd}}

{{end}}## Work Item
Title: {{.WorkItem.Title}}
ID: {{.WorkItem.ExternalID}}
Description:
{{.WorkItem.Description}}

## Repos
{{range .Repos}}- {{.Name}} ({{.Language}}{{if .Framework}}/{{.Framework}}{{end}}) — {{.MainDir}}{{if .AgentsMdPath}}
  guidance: {{.AgentsMdPath}}{{end}}{{if .DocPaths}}
  docs: {{range .DocPaths}}{{.}} {{end}}{{end}}
{{end}}## Instructions
Your role in this session is to plan this change. Exploration, analysis, and writing
the plan to {{.SessionDraftPath}} are your only work. You are not implementing anything.

If {{.SessionDraftPath}} already exists, read it first to orient yourself before exploring.
Write every draft update to {{.SessionDraftPath}}.
Explore the workspace before finalising your plan. After each significant decision or
exploration finding, update that file. Substrate reads it as your plan output —
your final message is not used. The last complete version present when the session
ends is what gets executed by a separate implementation session.

Begin the file with a fenced code block tagged substrate-plan containing YAML:

` + "```" + `substrate-plan
execution_groups:
  - [<repo-name>, ...]   # group 1: no dependencies, run first (parallel within group)
  - [<repo-name>, ...]   # group 2: run after group 1 completes (parallel within group)
  # add further groups as needed; list only repos that require changes
` + "```" + `

Then write exactly this structure:

## Orchestration
<cross-repo coordination, shared contracts, execution order rationale, and shared risks only>

## SubPlan: <repo-name>
### Goal
<repo-specific end state>

### Scope
- concrete files, modules, interfaces, migrations, or commands expected to change

### Changes
1. concrete implementation step
2. concrete implementation step
3. concrete implementation step

### Validation
- exact tests, checks, or commands to run

### Risks
- edge cases, sequencing constraints, invariants, or failure modes to watch

One ## SubPlan section per repo listed in execution_groups. Omit repos requiring no changes.
Every sub-plan must be implementation-ready for a later coding session.
### Scope, ### Validation, and ### Risks must each contain at least one list item.
### Changes must contain at least three concrete list items.
The Orchestration section must stay separate from repo-specific implementation details.

## Before Concluding
Confirm {{.SessionDraftPath}} contains a syntactically complete substrate-plan YAML block,
all required ## SubPlan sections are present, and every SubPlan has populated ### Goal,
### Scope, ### Changes, ### Validation, and ### Risks sections.
`

// correctionPromptTmpl is the correction prompt template.
const correctionPromptTmpl = `Your role is still to plan only. Your plan had structural errors that prevent execution:
{{.Errors}}

Valid repos in this workspace:
{{range .DiscoveredRepos}}  - {{.}}
{{end}}

Re-read {{.SessionDraftPath}} to see your current plan, then address the errors above.
Rewrite {{.SessionDraftPath}} with your complete revised plan. The substrate-plan YAML
block must appear first, before any prose.
Each repo sub-plan must include ### Goal, ### Scope, ### Changes, ### Validation, and ### Risks.
### Scope, ### Validation, and ### Risks need list items; ### Changes needs at least three concrete steps.
`
