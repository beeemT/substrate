package orchestrator

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/service"
)

// ReviewFollowup orchestrates a follow-up agent session and the associated
// Foreman lifecycle. It is the single owner of Foreman Start/Stop for
// follow-up sessions.
type ReviewFollowup struct {
	cfg         *config.Config
	harness     adapter.AgentHarness
	registry    SessionRegistry // Interface, not concrete type
	planSvc     *service.PlanService
	questionSvc *service.QuestionService
	sessionSvc  *service.AgentSessionService
	workItemSvc *service.SessionService
	eventBus    event.Publisher
}

// NewReviewFollowup creates a new ReviewFollowup instance.
func NewReviewFollowup(
	cfg *config.Config,
	harness adapter.AgentHarness,
	registry SessionRegistry,
	planSvc *service.PlanService,
	questionSvc *service.QuestionService,
	sessionSvc *service.AgentSessionService,
	workItemSvc *service.SessionService,
	eventBus event.Publisher,
) *ReviewFollowup {
	return &ReviewFollowup{
		cfg:         cfg,
		harness:     harness,
		registry:    registry,
		planSvc:     planSvc,
		questionSvc: questionSvc,
		sessionSvc:  sessionSvc,
		workItemSvc: workItemSvc,
		eventBus:    eventBus,
	}
}

// FollowUp restarts the foreman with follow-up context.
// Gets the current foreman, stops it, starts new one with feedback.
func (r *ReviewFollowup) FollowUp(ctx context.Context, workItemID, feedback string) error {
	// Get plan for this work item
	plan, err := r.planSvc.GetPlanByWorkItemID(ctx, workItemID)
	if err != nil {
		return fmt.Errorf("get plan: %w", err)
	}

	// Get existing foreman to stop it
	existingForeman := r.registry.GetForeman(workItemID)
	if existingForeman != nil {
		if existingForeman.IsRunning() {
			if err := existingForeman.Stop(ctx); err != nil {
				slog.Warn("failed to stop foreman before follow-up", "error", err)
			}
		}
		// Deregister the old foreman to prevent goroutine leaks if Stop()
		// failed or if Stop() returned before the goroutine fully exited.
		r.registry.DeregisterForeman(workItemID)
	}

	// Create new foreman
	newForeman := NewForeman(r.cfg, r.harness, r.planSvc, r.questionSvc, r.sessionSvc, r.workItemSvc, r.eventBus)

	// Start with follow-up context
	if err := newForeman.Start(ctx, plan.ID, feedback); err != nil {
		return fmt.Errorf("start foreman for follow-up: %w", err)
	}

	// Register new foreman
	r.registry.RegisterForeman(workItemID, newForeman)

	return nil
}

// FollowUpFailed handles the failed follow-up case.
// Similar to FollowUp but may include different context about what failed.
func (r *ReviewFollowup) FollowUpFailed(ctx context.Context, workItemID, feedback string) error {
	// Same as FollowUp but with "Failed: " prefix on feedback
	return r.FollowUp(ctx, workItemID, "Failed: "+feedback)
}
