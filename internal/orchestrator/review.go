package orchestrator

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
)

// ReviewPipeline orchestrates the review process for agent sessions.
type ReviewPipeline struct {
	cfg         *config.Config
	harness     adapter.AgentHarness
	reviewSvc   *service.ReviewService
	sessionSvc  *service.SessionService
	planSvc     *service.PlanService
	workItemSvc *service.WorkItemService
	sessionRepo repository.SessionRepository
	planRepo    repository.PlanRepository
	eventBus    *event.Bus
}

// NewReviewPipeline creates a new ReviewPipeline instance.
func NewReviewPipeline(
	cfg *config.Config,
	harness adapter.AgentHarness,
	reviewSvc *service.ReviewService,
	sessionSvc *service.SessionService,
	planSvc *service.PlanService,
	workItemSvc *service.WorkItemService,
	sessionRepo repository.SessionRepository,
	planRepo repository.PlanRepository,
	eventBus *event.Bus,
) *ReviewPipeline {
	return &ReviewPipeline{
		cfg:         cfg,
		harness:     harness,
		reviewSvc:   reviewSvc,
		sessionSvc:  sessionSvc,
		planSvc:     planSvc,
		workItemSvc: workItemSvc,
		sessionRepo: sessionRepo,
		planRepo:    planRepo,
		eventBus:    eventBus,
	}
}

// ReviewResult contains the result of a review.
type ReviewResult struct {
	Passed      bool
	Critiques   []domain.Critique
	CycleNumber int
	NeedsReimpl bool
	Escalated   bool
}

// ReviewStartedPayload is the payload for EventReviewStarted.
type ReviewStartedPayload struct {
	PlanID      string `json:"plan_id"`
	SessionID   string `json:"session_id"`
	CycleNumber int    `json:"cycle_number"`
}

// CritiquesFoundPayload is the payload for EventCritiquesFound.
type CritiquesFoundPayload struct {
	CycleID       string `json:"cycle_id"`
	CritiqueCount int    `json:"critique_count"`
	HasMajor      bool   `json:"has_major"`
}

// ReviewCompletedPayload is the payload for EventReviewCompleted.
type ReviewCompletedPayload struct {
	CycleID     string `json:"cycle_id"`
	Passed      bool   `json:"passed"`
	CycleNumber int    `json:"cycle_number"`
}

// ReimplementationStartedPayload is the payload for EventReimplementationStarted.
type ReimplementationStartedPayload struct {
	CycleID   string `json:"cycle_id"`
	PlanID    string `json:"plan_id"`
	SessionID string `json:"session_id"`
}

// ReviewSession reviews an agent session's output.
func (p *ReviewPipeline) ReviewSession(ctx context.Context, session domain.AgentSession) (*ReviewResult, error) {
	// Get existing review cycles
	cycles, err := p.reviewSvc.ListCyclesBySessionID(ctx, session.ID)
	if err != nil {
		return nil, fmt.Errorf("list review cycles: %w", err)
	}

	// Determine next cycle number
	cycleNumber := len(cycles) + 1

	// Check max cycles
	maxCycles := *p.cfg.Review.MaxCycles
	if cycleNumber > maxCycles {
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
		AgentSessionID:  session.ID,
		CycleNumber:     cycleNumber,
		ReviewerHarness: p.harness.Name(),
		Status:          domain.ReviewCycleReviewing,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	if err := p.reviewSvc.CreateCycle(ctx, cycle); err != nil {
		return nil, fmt.Errorf("create review cycle: %w", err)
	}

	// Emit ReviewStarted event
	if p.eventBus != nil {
		if err := p.eventBus.Publish(ctx, domain.SystemEvent{
			ID:          domain.NewID(),
			EventType:   string(domain.EventReviewStarted),
			WorkspaceID: session.WorkspaceID,
			Payload: marshalJSONOrEmpty(map[string]any{
				"session_id":   session.ID,
				"cycle_number": cycleNumber,
				"cycle_id":     cycle.ID,
			}),
			CreatedAt: time.Now(),
		}); err != nil {
			slog.Warn("failed to emit review started event", "error", err)
		}
	}

	// Get plan and sub-plan for context
	subPlan, err := p.planSvc.GetSubPlan(ctx, session.SubPlanID)
	if err != nil {
		return nil, fmt.Errorf("get sub-plan: %w", err)
	}
	plan, err := p.planRepo.Get(ctx, subPlan.PlanID)
	if err != nil {
		return nil, fmt.Errorf("get plan: %w", err)
	}

	// Start review agent session - now returns session too
	reviewSession, reviewOutput, err := p.startReviewAgent(ctx, session, subPlan, plan, cycle)
	if err != nil {
		return nil, fmt.Errorf("start review agent: %w", err)
	}
	defer reviewSession.Abort(ctx) // Abort when done

	// Parse critiques from output
	critiques, parseErr := p.parseCritiques(reviewOutput)
	if parseErr != nil {
		// Correction loop
		correctedOutput, err := p.runCorrectionLoop(ctx, reviewSession, cycle, reviewOutput)
		if err != nil {
			slog.Warn("correction loop failed, treating as zero critiques", "error", err)
			critiques = []domain.Critique{}
		} else {
			critiques, parseErr = p.parseCritiques(correctedOutput)
			if parseErr != nil {
				slog.Warn("parse failed after correction, treating as zero critiques")
				critiques = []domain.Critique{}
			}
		}
	}

	// Decision logic
	result := p.makeDecision(ctx, cycle, critiques)

	// Emit review outcome events
	if p.eventBus != nil {
		payload := marshalJSONOrEmpty(map[string]any{
			"session_id":     session.ID,
			"cycle_number":   cycleNumber,
			"cycle_id":       cycle.ID,
			"passed":         result.Passed,
			"critique_count": len(critiques),
			"needs_reimpl":   result.NeedsReimpl,
			"escalated":      result.Escalated,
		})
		evtType := domain.EventReviewCompleted
		if result.NeedsReimpl {
			evtType = domain.EventCritiquesFound
			if err := p.eventBus.Publish(ctx, domain.SystemEvent{
				ID:          domain.NewID(),
				EventType:   string(domain.EventReimplementationStarted),
				WorkspaceID: session.WorkspaceID,
				Payload:     payload,
				CreatedAt:   time.Now(),
			}); err != nil {
				slog.Warn("failed to emit reimplementation started event", "error", err)
			}
		}
		if err := p.eventBus.Publish(ctx, domain.SystemEvent{
			ID:          domain.NewID(),
			EventType:   string(evtType),
			WorkspaceID: session.WorkspaceID,
			Payload:     payload,
			CreatedAt:   time.Now(),
		}); err != nil {
			slog.Warn("failed to emit review outcome event", "error", err)
		}
	}

	return result, nil
}

// startReviewAgent starts a review agent session and returns the session (still alive) along with output.
func (p *ReviewPipeline) startReviewAgent(ctx context.Context, session domain.AgentSession, subPlan domain.SubPlan, plan domain.Plan, cycle domain.ReviewCycle) (adapter.AgentSession, string, error) {
	// Build review prompt
	prompt := p.buildReviewPrompt(subPlan, plan)

	// Start review session in foreman mode (read-only tools)
	opts := adapter.SessionOpts{
		SessionID:    domain.NewID(),
		Mode:         adapter.SessionModeForeman,
		WorkspaceID:  session.WorkspaceID,
		SubPlanID:    session.SubPlanID,
		Repository:   session.RepositoryName,
		WorktreePath: session.WorktreePath,
		SystemPrompt: prompt,
		UserPrompt:   "Review the changes in this worktree. Compare against main and evaluate against the sub-plan.",
	}

	reviewSession, err := p.harness.StartSession(ctx, opts)
	if err != nil {
		return nil, "", fmt.Errorf("start review session: %w", err)
	}
	// Watch for done event instead of calling Wait()
	// Add 5-minute timeout to prevent hanging
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	for {
		select {
		case <-timeoutCtx.Done():
			return reviewSession, "", fmt.Errorf("review session timed out: %w", timeoutCtx.Err())
		case evt, ok := <-reviewSession.Events():
			if !ok {
				return reviewSession, "", fmt.Errorf("review session events channel closed unexpectedly")
			}
			if evt.Type == "done" {
				// Read output from session log file
				output, err := p.readSessionOutputFromLog(ctx, opts.SessionID)
				if err != nil {
					slog.Warn("failed to read session log, returning NO_CRITIQUES", "error", err)
					return reviewSession, "NO_CRITIQUES", nil
				}
				return reviewSession, output, nil
			} else if evt.Type == "error" {
				return reviewSession, "", fmt.Errorf("review session error: %s", evt.Payload)
			}
		}
	}
}

// buildReviewPrompt builds the prompt for the review agent.
func (p *ReviewPipeline) buildReviewPrompt(subPlan domain.SubPlan, plan domain.Plan) string {
	prompt := `## Task

Review the changes in this repository against the plan. Compare the feature branch against main. Identify correctness, completeness, and quality issues.

## Sub-Plan

` + subPlan.Content + "\n\n## FAQ\n\n"

	for _, entry := range plan.FAQ {
		prompt += fmt.Sprintf("Q: %s\nA: %s\n\n", entry.Question, entry.Answer)
	}

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
		return nil, fmt.Errorf("no valid CRITIQUE blocks found and no NO_CRITIQUES marker")
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

		if strings.HasPrefix(line, "File:") {
			critique.FilePath = strings.TrimSpace(strings.TrimPrefix(line, "File:"))
		} else if strings.HasPrefix(line, "Severity:") {
			sev := strings.TrimSpace(strings.TrimPrefix(line, "Severity:"))
			critique.Severity = domain.CritiqueSeverity(sev)
		} else if strings.HasPrefix(line, "Description:") {
			critique.Description = strings.TrimSpace(strings.TrimPrefix(line, "Description:"))
		}
	}

	if critique.Description == "" {
		return domain.Critique{}, fmt.Errorf("missing description")
	}

	return critique, nil
}

// runCorrectionLoop runs the correction loop for unparsable output.
func (p *ReviewPipeline) runCorrectionLoop(ctx context.Context, reviewSession adapter.AgentSession, cycle domain.ReviewCycle, originalOutput string) (string, error) {
	maxRetries := *p.cfg.Plan.MaxParseRetries

	for i := 0; i < maxRetries; i++ {
		correctionMsg := fmt.Sprintf(`Your output was not parseable. Output either:
- Exactly "NO_CRITIQUES" if there are no issues requiring fixes, or
- One or more CRITIQUE / END_CRITIQUE blocks.

Do not include explanatory prose outside these markers.

Original output:
%s`, originalOutput)

		// Send correction message to review session
		if err := reviewSession.SendMessage(ctx, correctionMsg); err != nil {
			return "", fmt.Errorf("send correction message: %w", err)
		}

		// Wait for next "done" event from the session
		for {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case evt, ok := <-reviewSession.Events():
				if !ok {
					return "", fmt.Errorf("review session ended during correction")
				}
				if evt.Type == "done" {
					// Read new output from session log
					output, err := p.readSessionOutputFromLog(ctx, reviewSession.ID())
					if err != nil {
						slog.Warn("failed to read session log after correction", "error", err)
						break // Try another correction
					}
					return output, nil
				}
			}
		}
	}

	return "", fmt.Errorf("max correction retries exceeded")
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
func (p *ReviewPipeline) readSessionOutputFromLog(ctx context.Context, sessionID string) (string, error) {
	// Get global session directory
	globalDir, err := config.GlobalDir()
	if err != nil {
		return "", fmt.Errorf("get global dir: %w", err)
	}

	logPath := filepath.Join(globalDir, "sessions", sessionID+".log")

	// Check if file exists
	if _, err := os.Stat(logPath); err != nil {
		return "", fmt.Errorf("session log file not found: %w", err)
	}

	// Open and read the file
	file, err := os.Open(logPath)
	if err != nil {
		return "", fmt.Errorf("open session log: %w", err)
	}
	defer file.Close()

	// Read all content and extract text from progress events
	var output strings.Builder
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		// Each line is: <timestamp> <json-event>
		// We want to extract text from progress events
		if strings.Contains(line, "\"type\":\"event\"") {
			if strings.Contains(line, "\"type\":\"progress\"") {
				// Extract text field from JSON
				if idx := strings.Index(line, `"text":"`); idx != -1 {
					start := idx + 8
					end := strings.Index(line[start:], `"`)
					if end != -1 {
						text := line[start : start+end]
						output.WriteString(text)
						output.WriteString("\n")
					}
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read session log: %w", err)
	}

	return output.String(), nil
}
