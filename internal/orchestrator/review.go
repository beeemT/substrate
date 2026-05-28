package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/service"
	"github.com/beeemT/substrate/internal/sessionlog"
)

// ReviewPipeline orchestrates the review process for agent sessions.
type ReviewPipeline struct {
	cfg           *config.Config
	harness       adapter.AgentHarness
	reviewSvc     *service.ReviewService
	sessionSvc    *service.AgentSessionService
	planSvc       *service.PlanService
	workItemSvc   *service.SessionService
	eventBus      event.Publisher
	registry      SessionRegistry
	reviewTimeout time.Duration
}

// NewReviewPipeline creates a new ReviewPipeline instance.
func NewReviewPipeline(
	cfg *config.Config,
	harness adapter.AgentHarness,
	reviewSvc *service.ReviewService,
	sessionSvc *service.AgentSessionService,
	planSvc *service.PlanService,
	workItemSvc *service.SessionService,
	eventBus event.Publisher,
	registry SessionRegistry,
) *ReviewPipeline {
	return &ReviewPipeline{
		cfg:           cfg,
		harness:       harness,
		reviewSvc:     reviewSvc,
		sessionSvc:    sessionSvc,
		planSvc:       planSvc,
		workItemSvc:   workItemSvc,
		eventBus:      eventBus,
		registry:      registry,
		reviewTimeout: cfg.Review.ReviewTimeout(),
	}
}

// ReviewResult contains the result of a review.
type ReviewResult struct {
	Passed      bool
	Critiques   []domain.Critique
	CycleNumber int
	NeedsReimpl bool
	Escalated   bool
	SessionID   string // review agent session ID — log at config.SessionsDir()/<SessionID>.log
}

// ReviewSession reviews an agent session's output. If an error is returned
// after the new review cycle has been created, the cycle is durably
// transitioned to `failed` so it does not linger in `reviewing` and mask
// outstanding critiques on prior cycles for the same impl session.
func (p *ReviewPipeline) ReviewSession(ctx context.Context, agentSession domain.AgentSession) (result *ReviewResult, err error) {
	return p.ReviewSessionWithParent(ctx, agentSession, agentSession.ID)
}

// ReviewSessionWithParent reviews an implementation session's output while
// allowing retry/recovery callers to link the new review agent session to the
// graph leaf it supersedes. The reviewed implementation remains agentSession;
// reviewParentSessionID controls only the agent-session graph edge.
func (p *ReviewPipeline) ReviewSessionWithParent(ctx context.Context, agentSession domain.AgentSession, reviewParentSessionID string) (result *ReviewResult, err error) {
	if reviewParentSessionID == "" {
		reviewParentSessionID = agentSession.ID
	}
	// Get existing review cycles
	cycles, err := p.reviewSvc.ListCyclesBySessionID(ctx, agentSession.ID)
	if err != nil {
		return nil, fmt.Errorf("list review cycles: %w", err)
	}

	// Count only terminal-decision cycles toward the budget. Stale cycles
	// left in reviewing/reimplementing by harness crashes do not consume it,
	// but still reserve their cycle_number in the database.
	terminalCount := 0
	maxCycleNumber := 0
	for _, c := range cycles {
		if c.CycleNumber > maxCycleNumber {
			maxCycleNumber = c.CycleNumber
		}
		switch c.Status {
		case domain.ReviewCyclePassed, domain.ReviewCycleCritiquesFound, domain.ReviewCycleFailed:
			terminalCount++
		}
	}
	budgetCycleNumber := terminalCount + 1
	cycleNumber := maxCycleNumber + 1

	// Check max cycles
	maxCycles := *p.cfg.Review.MaxCycles
	if budgetCycleNumber > maxCycles {
		// Exceeded max cycles - escalate
		if len(cycles) > 0 {
			if err := p.reviewSvc.FailReviewCycle(ctx, cycles[len(cycles)-1].ID); err != nil {
				slog.Error("failed to fail review cycle", "error", err)
			}
		}
		return &ReviewResult{
			Passed:      false,
			Escalated:   true,
			CycleNumber: cycleNumber,
		}, nil
	}

	// Create review cycle
	cycle := domain.ReviewCycle{
		ID:              domain.NewID(),
		AgentSessionID:  agentSession.ID,
		CycleNumber:     cycleNumber,
		ReviewerHarness: p.harness.Name(),
		Status:          domain.ReviewCycleReviewing,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	if err := p.reviewSvc.CreateCycle(ctx, cycle); err != nil {
		return nil, fmt.Errorf("create review cycle: %w", err)
	}

	// From here on, any error must transition the cycle to a terminal state.
	// Otherwise it stays in `reviewing` forever — `loadCritiqueFeedback` then
	// silently treats it as "no outstanding critiques", and the next retry can
	// resume the prior impl session with no work to do (see investigation in
	// internal/orchestrator/review.go around 2026-05-28). makeDecision moves
	// the cycle to passed/critiques_found on the happy path, in which case
	// the FailReviewCycle call below is a no-op (terminal → terminal is
	// rejected by the transition table; we just log the rejection).
	defer func() {
		if err == nil {
			return
		}
		cleanupCtx, cancel := durableCleanupContext(ctx)
		defer cancel()
		if failErr := p.reviewSvc.FailReviewCycle(cleanupCtx, cycle.ID); failErr != nil {
			slog.Warn("failed to transition review cycle to failed after review error",
				"error", failErr,
				"cycle_id", cycle.ID,
				"agent_session_id", agentSession.ID,
				"review_error", err)
		}
	}()

	// Emit ReviewStarted event with the full cycle so TUI can upsert without a reload
	service.Emit(p.eventBus, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventReviewStarted),
		WorkspaceID: agentSession.WorkspaceID,
		Payload: marshalJSONOrEmpty(string(domain.EventReviewStarted), map[string]any{
			"agent_session_id": agentSession.ID,
			"work_item_id":     agentSession.WorkItemID,
			"cycle_number":     cycleNumber,
			"cycle":            cycle,
		}),
		CreatedAt: time.Now(),
	})

	// Get plan and sub-plan for context
	subPlan, err := p.planSvc.GetSubPlan(ctx, agentSession.SubPlanID)
	if err != nil {
		return nil, fmt.Errorf("get sub-plan: %w", err)
	}
	plan, err := p.planSvc.GetPlan(ctx, subPlan.PlanID)
	if err != nil {
		return nil, fmt.Errorf("get plan: %w", err)
	}

	// Start review agent session - now returns (session, output, sessionID, error)
	reviewSession, reviewOutput, reviewSessionID, err := p.startReviewAgent(ctx, agentSession, subPlan, plan, reviewParentSessionID)
	if err != nil {
		return nil, fmt.Errorf("start review agent: %w", err)
	}
	defer reviewSession.Abort(ctx) // Abort when done

	// Parse critiques from output. If the output is not parseable, log a warning and
	// treat it as no critiques — the review agent runs in agent mode (bridge exits after
	// first response), so multi-turn correction is not possible.
	critiques, parseErr := p.parseCritiques(reviewOutput)
	if parseErr != nil {
		slog.Warn("review output not parseable, treating as no critiques", "error", parseErr, "agent_session_id", reviewSessionID)
		critiques = []domain.Critique{}
	}

	// Decision logic
	result = p.makeDecision(ctx, cycle, critiques)
	result.SessionID = reviewSessionID

	// Emit review outcome events (async)
	payload := marshalJSONOrEmpty("review.outcome", map[string]any{
		"agent_session_id": agentSession.ID,
		"work_item_id":     agentSession.WorkItemID,
		"cycle_number":     cycleNumber,
		"cycle_id":         cycle.ID,
		"passed":           result.Passed,
		"critique_count":   len(critiques),
		"needs_reimpl":     result.NeedsReimpl,
		"escalated":        result.Escalated,
	})
	now := time.Now()
	// Always emit ReviewCompleted as the base outcome event.
	service.Emit(p.eventBus, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventReviewCompleted),
		WorkspaceID: agentSession.WorkspaceID,
		Payload:     payload,
		CreatedAt:   now,
	})
	if result.NeedsReimpl {
		// Additionally emit critique-specific events so consumers know why review ended.
		service.Emit(p.eventBus, domain.SystemEvent{
			ID:          domain.NewID(),
			EventType:   string(domain.EventCritiquesFound),
			WorkspaceID: agentSession.WorkspaceID,
			Payload:     payload,
			CreatedAt:   now,
		})
		service.Emit(p.eventBus, domain.SystemEvent{
			ID:          domain.NewID(),
			EventType:   string(domain.EventReimplementationStarted),
			WorkspaceID: agentSession.WorkspaceID,
			Payload:     payload,
			CreatedAt:   now,
		})
	}

	return result, nil
}

// startReviewAgent starts a review agent session and returns the session (still alive) along with output.
func (p *ReviewPipeline) startReviewAgent(
	ctx context.Context,
	agentSession domain.AgentSession,
	subPlan domain.TaskPlan,
	plan domain.Plan,
	reviewParentSessionID string,
) (adapter.AgentSession, string, string, error) {
	// Build review prompt
	prompt := p.buildReviewPrompt(subPlan, plan)

	// Persist the review session before launching the harness.
	reviewSessionID := domain.NewID()
	reviewTask := domain.AgentSession{
		ID:                   reviewSessionID,
		WorkItemID:           agentSession.WorkItemID,
		WorkspaceID:          agentSession.WorkspaceID,
		Kind:                 domain.AgentSessionKindReview,
		SubPlanID:            agentSession.SubPlanID,
		RepositoryName:       agentSession.RepositoryName,
		WorktreePath:         agentSession.WorktreePath,
		HarnessName:          p.harness.Name(),
		ParentAgentSessionID: reviewParentSessionID,
	}
	if err := p.sessionSvc.Create(ctx, reviewTask); err != nil {
		return nil, "", "", fmt.Errorf("create review session: %w", err)
	}
	if err := p.sessionSvc.Start(ctx, reviewSessionID); err != nil {
		if transitionErr := p.sessionSvc.Transition(ctx, reviewSessionID, domain.AgentSessionFailed); transitionErr != nil {
			slog.Warn("failed to transition session to failed", "error", transitionErr, "agent_session_id", reviewSessionID)
		}
		return nil, "", "", fmt.Errorf("transition review session to running: %w", err)
	}

	// Start review session in agent mode so UserPrompt is delivered automatically
	// by the harness and the bridge emits lifecycle.completed ("done") on finish.
	opts := adapter.SessionOpts{
		SessionID:    reviewSessionID,
		Mode:         adapter.SessionModeAgent,
		WorkspaceID:  agentSession.WorkspaceID,
		SubPlanID:    agentSession.SubPlanID,
		Repository:   agentSession.RepositoryName,
		WorktreePath: agentSession.WorktreePath,
		SystemPrompt: prompt,
		UserPrompt:   "Review the changes in this worktree. Compare against main and evaluate against the sub-plan.",
	}

	reviewSession, err := p.harness.StartSession(ctx, opts)
	if err != nil {
		if failErr := failSessionDurably(ctx, p.sessionSvc, reviewSessionID, ptrInt(1)); failErr != nil {
			slog.Warn("failed to fail review session after harness start error", "error", failErr, "agent_session_id", reviewSessionID)
		}
		return nil, "", "", fmt.Errorf("start review session: %w", err)
	}

	// In agent mode the harness sends UserPrompt automatically — no manual prompt needed.
	// Watch for done event instead of calling Wait().
	// Apply configured review timeout.
	timeoutCtx, cancel := context.WithTimeout(ctx, p.reviewTimeout)
	defer cancel()

	// Register session for steering.
	p.registry.Register(reviewSessionID, reviewSession)
	defer p.registry.Deregister(reviewSessionID)
	for {
		select {
		case <-timeoutCtx.Done():
			if failErr := failSessionDurably(ctx, p.sessionSvc, reviewSessionID, ptrInt(1)); failErr != nil {
				slog.Warn("failed to fail timed out review session", "error", failErr, "agent_session_id", reviewSessionID)
			}
			return reviewSession, "", reviewSessionID, fmt.Errorf("review session timed out: %w", timeoutCtx.Err())
		case evt, ok := <-reviewSession.Events():
			if !ok {
				if failErr := failSessionDurably(ctx, p.sessionSvc, reviewSessionID, ptrInt(1)); failErr != nil {
					slog.Warn("failed to fail closed review session", "error", failErr, "agent_session_id", reviewSessionID)
				}
				return reviewSession, "", reviewSessionID, errors.New("review session events channel closed unexpectedly")
			}
			switch evt.Type {
			case "done":
				// "done" is the normal agent-mode completion signal (lifecycle.completed).
				if completeErr := completeSessionDurably(ctx, p.sessionSvc, reviewSessionID); completeErr != nil {
					slog.Warn("failed to complete review session", "error", completeErr, "agent_session_id", reviewSessionID)
				}
				output, err := p.readSessionOutputFromLog(ctx, reviewSessionID)
				if err != nil {
					return reviewSession, "", reviewSessionID, fmt.Errorf("read review session output: %w", err)
				}
				return reviewSession, output, reviewSessionID, nil
			case "error":
				if failErr := failSessionDurably(ctx, p.sessionSvc, reviewSessionID, ptrInt(1)); failErr != nil {
					slog.Warn("failed to fail review session after agent error", "error", failErr, "agent_session_id", reviewSessionID)
				}
				return reviewSession, "", reviewSessionID, fmt.Errorf("review session error: %s", evt.Payload)
			}
		}
	}
}

// buildReviewPrompt builds the prompt for the review agent.
func (p *ReviewPipeline) buildReviewPrompt(subPlan domain.TaskPlan, plan domain.Plan) string {
	var faqBuilder strings.Builder
	for _, entry := range plan.FAQ {
		fmt.Fprintf(&faqBuilder, "Q: %s\nA: %s\n\n", entry.Question, entry.Answer)
	}

	prompt := `## Role
` + "You are a code reviewer. Your sole responsibility is to review the changes — do NOT edit, fix, or write any code. Produce only the structured output described below." + `

## Task

Review the changes in this repository against the plan. Compare the feature branch against main. Identify correctness, completeness, and quality issues.

## Sub-Plan

` + subPlan.Content + "\n\n## FAQ\n\n" + faqBuilder.String()

	prompt += `
## Output Format

If no issues (or only nit-level issues that do not require fixing): output exactly "NO_CRITIQUES".
Otherwise, for each issue output:

CRITIQUE
File: <path or "general">
Severity: critical | major | minor | nit
Description: <what is wrong and what to do>
END_CRITIQUE
`

	return prompt
}

// parseCritiques parses critiques from review output.
func (p *ReviewPipeline) parseCritiques(output string) ([]domain.Critique, error) {
	// Check for NO_CRITIQUES
	if strings.Contains(output, "NO_CRITIQUES") {
		return []domain.Critique{}, nil
	}

	// Parse CRITIQUE blocks
	re := regexp.MustCompile(`(?s)CRITIQUE\s*(.*?)\s*END_CRITIQUE`)
	matches := re.FindAllStringSubmatch(output, -1)

	if len(matches) == 0 {
		return nil, errors.New("no valid CRITIQUE blocks found and no NO_CRITIQUES marker")
	}

	critiques := make([]domain.Critique, 0, len(matches))
	for _, match := range matches {
		block := match[1]
		critique, err := p.parseCritiqueBlock(block)
		if err != nil {
			slog.Warn("failed to parse critique block", "error", err)
			continue
		}
		critiques = append(critiques, critique)
	}

	return critiques, nil
}

// parseCritiqueBlock parses a single critique block.
func (p *ReviewPipeline) parseCritiqueBlock(block string) (domain.Critique, error) {
	lines := strings.Split(block, "\n")

	var critique domain.Critique
	critique.ID = domain.NewID()
	critique.Status = domain.CritiqueOpen
	critique.CreatedAt = time.Now()

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if after, ok := strings.CutPrefix(line, "File:"); ok {
			critique.FilePath = strings.TrimSpace(after)
		} else if after, ok := strings.CutPrefix(line, "Severity:"); ok {
			sev := strings.TrimSpace(after)
			critique.Severity = domain.CritiqueSeverity(sev)
		} else if after, ok := strings.CutPrefix(line, "Description:"); ok {
			critique.Description = strings.TrimSpace(after)
		}
	}

	if critique.Description == "" {
		return domain.Critique{}, errors.New("missing description")
	}

	return critique, nil
}

// makeDecision makes the pass/fail decision based on critiques.
func (p *ReviewPipeline) makeDecision(ctx context.Context, cycle domain.ReviewCycle, critiques []domain.Critique) *ReviewResult {
	// Check pass threshold
	threshold := p.cfg.Review.PassThreshold

	hasMajor := false
	for _, c := range critiques {
		if c.Severity == domain.CritiqueMajor || c.Severity == domain.CritiqueCritical {
			hasMajor = true
			break
		}
	}

	// Decision logic based on threshold
	switch threshold {
	case config.PassThresholdNoCritiques:
		if len(critiques) > 0 {
			return p.needsReimplementation(ctx, cycle, critiques)
		}
	case config.PassThresholdNitOnly:
		for _, c := range critiques {
			if c.Severity != domain.CritiqueNit {
				return p.needsReimplementation(ctx, cycle, critiques)
			}
		}
	case config.PassThresholdMinorOK:
		if hasMajor {
			return p.needsReimplementation(ctx, cycle, critiques)
		}
	}

	// Passed - save critiques and mark as passed
	for _, c := range critiques {
		c.ReviewCycleID = cycle.ID
		if err := p.reviewSvc.CreateCritique(ctx, c); err != nil {
			slog.Warn("failed to create critique", "error", err)
		}
	}

	if err := p.reviewSvc.PassReview(ctx, cycle.ID); err != nil {
		slog.Warn("failed to pass review", "error", err)
	}

	return &ReviewResult{
		Passed:      true,
		Critiques:   critiques,
		CycleNumber: cycle.CycleNumber,
	}
}

// needsReimplementation returns a result indicating re-implementation is needed.
func (p *ReviewPipeline) needsReimplementation(ctx context.Context, cycle domain.ReviewCycle, critiques []domain.Critique) *ReviewResult {
	// Save critiques
	for _, c := range critiques {
		c.ReviewCycleID = cycle.ID
		if err := p.reviewSvc.CreateCritique(ctx, c); err != nil {
			slog.Warn("failed to create critique", "error", err)
		}
	}

	// Mark cycle as critiques found
	if err := p.reviewSvc.RecordCritiques(ctx, cycle.ID); err != nil {
		slog.Warn("failed to record critiques", "error", err)
	}

	return &ReviewResult{
		Passed:      false,
		Critiques:   critiques,
		CycleNumber: cycle.CycleNumber,
		NeedsReimpl: true,
	}
}

// readSessionOutputFromLog reads the session output from the log file.
// The log file is stored at ~/.substrate/sessions/<session-id>.log
func (p *ReviewPipeline) readSessionOutputFromLog(_ context.Context, sessionID string) (string, error) {
	globalDir, err := config.GlobalDir()
	if err != nil {
		return "", fmt.Errorf("get global dir: %w", err)
	}

	logPath := filepath.Join(globalDir, "sessions", sessionID+".log")

	entries, err := sessionlog.ReadFile(logPath)
	if err != nil {
		return "", fmt.Errorf("read session log: %w", err)
	}

	return sessionlog.FlattenAssistantOutput(entries), nil
}
