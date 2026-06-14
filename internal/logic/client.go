// Package logic contains daemon-owned product operations and read APIs.
package logic

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/gitwork"
	"github.com/beeemT/substrate/internal/orchestrator"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
	"github.com/beeemT/substrate/internal/sessionlog"
)

// Verify InProcessClient implements Client at compile time.
var _ Client = (*InProcessClient)(nil)

// Operation is the product-level handle returned by long-running daemon actions.
type Operation struct {
	ID              string
	WorkspaceID     string
	SessionID       string
	Status          string
	StartedSequence uint64
}

// ArtifactReview is a daemon-owned read-model projection of a PR/MR review.
type ArtifactReview struct {
	ReviewerLogin string
	State         string
	SubmittedAt   time.Time
}

// ArtifactCheck is a daemon-owned read-model projection of a CI check.
type ArtifactCheck struct {
	Name       string
	Status     string
	Conclusion string
}

// ArtifactItem is a daemon-owned read-model projection of a PR/MR artifact.
type ArtifactItem struct {
	ID           string
	Provider     string
	Kind         string
	RepoName     string
	Ref          string
	URL          string
	State        string
	Branch       string
	Draft        bool
	WorktreePath string
	MergedAt     *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Reviews      []ArtifactReview
	Checks       []ArtifactCheck
}

// ActionResult is the product-level result for synchronous mutations.
type ActionResult struct {
	Message string
}

// InitialSnapshot is the coherent starting read model for a visualization client.
type InitialSnapshot struct {
	Artifacts     map[string][]ArtifactItem
	Sessions      []domain.Session
	AgentSessions []domain.AgentSession
	Plans         map[string]domain.Plan
	SubPlans      map[string][]domain.TaskPlan
	Questions     map[string][]domain.Question
	Reviews       map[string][]domain.ReviewCycle
	// Critiques is keyed by review cycle ID. The latest-review-cycle
	// read-model path uses this to surface critique counts and the first
	// critique excerpt on the action cards.
	Critiques           map[string][]domain.Critique
	Filters             []domain.NewSessionFilter
	LiveInstances       []domain.SubstrateInstance
	ArchivedSessionIDs  []string
	LatestEventSequence uint64
}

// Client exposes product actions and product read models, not repositories or services.
type Client interface {
	GetInitialSnapshot(ctx context.Context, workspaceID string) (InitialSnapshot, error)
	ListSessions(ctx context.Context, workspaceID string) ([]domain.Session, error)
	GetSession(ctx context.Context, sessionID string) (domain.Session, error)
	SearchSessionHistory(ctx context.Context, filter domain.SessionHistoryFilter) ([]domain.SessionHistoryEntry, error)
	ArchiveSession(ctx context.Context, workItemID string) (ActionResult, error)
	UnarchiveSession(ctx context.Context, workItemID string) (ActionResult, error)
	DeleteSession(ctx context.Context, workItemID string) (ActionResult, error)
	OverrideAccept(ctx context.Context, workItemID string) (ActionResult, error)
	FailReview(ctx context.Context, workItemID string) (ActionResult, error)
	ApprovePlan(ctx context.Context, planID, workItemID string) (ActionResult, error)
	RequestPlanChanges(ctx context.Context, workItemID, planID, feedback string) (ActionResult, error)
	SaveReviewedPlan(ctx context.Context, planID, content string) (domain.Plan, []domain.TaskPlan, error)
	RunImplementation(ctx context.Context, planID string) (Operation, error)
	StartPlanning(ctx context.Context, workItemID string) (ActionResult, error)
	CancelPipeline(ctx context.Context, workItemID, agentSessionID string) (ActionResult, error)
	RestartPlanning(ctx context.Context, workItemID, prompt string) (ActionResult, error)
	FollowUpPlan(ctx context.Context, workItemID, feedback string) (ActionResult, error)
	FinalizeWorkItem(ctx context.Context, workItemID string) (ActionResult, error)
	RetryFailedWorkItem(ctx context.Context, planID, workItemID string) (ActionResult, error)
	ResumeAllSessionsForWorkItem(ctx context.Context, workItemID, instanceID string) (ActionResult, error)
	RetryAgentSession(ctx context.Context, sessionID, instanceID string) (ActionResult, error)
	FollowUpAgentSession(ctx context.Context, sessionID, feedback, instanceID string) (ActionResult, error)
	SteerSession(ctx context.Context, sessionID, message string) (ActionResult, error)
	AnswerQuestion(ctx context.Context, questionID, answer, answeredBy string) (ActionResult, error)
	SkipQuestion(ctx context.Context, questionID string) (ActionResult, error)
}

// Dependencies are the concrete daemon-side services used by the in-process client.
type Dependencies struct {
	Sessions             *service.SessionService
	Plans                *service.PlanService
	AgentSessions        *service.AgentSessionService
	Questions            *service.QuestionService
	Reviews              *service.ReviewService
	SessionArtifacts     *service.SessionReviewArtifactService
	GithubPRs            *service.GithubPRService
	GitlabMRs            *service.GitlabMRService
	GithubPRReviews      *service.GithubPRReviewService
	GitlabMRReviews      *service.GitlabMRReviewService
	GithubPRChecks       *service.GithubPRCheckService
	GitlabMRChecks       *service.GitlabMRCheckService
	Planning             *orchestrator.PlanningService
	Events               *service.EventService
	Filters              *service.SessionFilterService
	Instances            *service.InstanceService
	Implementation       *orchestrator.ImplementationService
	AnswerRouter         orchestrator.AnswerRouter
	SessionRegistry      orchestrator.SessionRegistry
	Manual               *orchestrator.ManualSessionService
	GitClient            *gitwork.Client
	SessionsDir          string
	DeleteReviewLogPaths map[string]string
	Config               *config.Config
}

// InProcessClient adapts the current service graph to the product client boundary.
type InProcessClient struct {
	deps Dependencies
}

// NewInProcessClient creates a product client backed by the current daemon service graph.
func NewInProcessClient(deps Dependencies) *InProcessClient {
	return &InProcessClient{deps: deps}
}

func (c *InProcessClient) ListSessions(ctx context.Context, workspaceID string) ([]domain.Session, error) {
	if c.deps.Sessions == nil {
		return nil, fmt.Errorf("session service is required")
	}
	return c.deps.Sessions.List(ctx, repository.SessionFilter{WorkspaceID: &workspaceID})
}

func (c *InProcessClient) GetSession(ctx context.Context, sessionID string) (domain.Session, error) {
	if c.deps.Sessions == nil {
		return domain.Session{}, fmt.Errorf("session service is required")
	}
	return c.deps.Sessions.Get(ctx, sessionID)
}

func (c *InProcessClient) SearchSessionHistory(ctx context.Context, filter domain.SessionHistoryFilter) ([]domain.SessionHistoryEntry, error) {
	if c.deps.AgentSessions == nil {
		return nil, fmt.Errorf("agent session service is required")
	}
	return c.deps.AgentSessions.SearchHistory(ctx, filter)
}

func (c *InProcessClient) ArchiveSession(ctx context.Context, workItemID string) (ActionResult, error) {
	if c.deps.Sessions == nil {
		return ActionResult{}, fmt.Errorf("session service is required")
	}
	if err := c.deps.Sessions.Archive(ctx, workItemID); err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Message: "Session archived"}, nil
}

func (c *InProcessClient) UnarchiveSession(ctx context.Context, workItemID string) (ActionResult, error) {
	if c.deps.Sessions == nil {
		return ActionResult{}, fmt.Errorf("session service is required")
	}
	if err := c.deps.Sessions.Unarchive(ctx, workItemID); err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Message: "Session unarchived"}, nil
}

func (c *InProcessClient) DeleteSession(ctx context.Context, workItemID string) (ActionResult, error) {
	if c.deps.Sessions == nil || c.deps.Plans == nil || c.deps.AgentSessions == nil {
		return ActionResult{}, fmt.Errorf("session, plan, and agent session services are required")
	}
	result, err := c.deleteSessionGraph(ctx, workItemID)
	if err != nil {
		return ActionResult{}, err
	}
	message := "Session deleted"
	if result.CleanupWarning != nil {
		message += ", but some session artifacts could not be removed: " + result.CleanupWarning.Error()
	}
	return ActionResult{Message: message}, nil
}
func deleteTaskArtifacts(sessionsDir, taskID, reviewLogPath string) error {
	var errs []error
	for _, deleteID := range deleteTaskArtifactIDs(sessionsDir, taskID, reviewLogPath) {
		paths, err := sessionlog.InteractionPaths(sessionsDir, deleteID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, path := range paths {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				errs = append(errs, fmt.Errorf("remove session log %s: %w", path, err))
			}
		}
	}
	return errors.Join(errs...)
}

func deleteTaskArtifactIDs(sessionsDir, taskID, reviewLogPath string) []string {
	if strings.TrimSpace(sessionsDir) == "" || strings.TrimSpace(taskID) == "" {
		return nil
	}
	ids := []string{taskID}
	if strings.TrimSpace(reviewLogPath) == "" {
		return ids
	}
	reviewLogPath = filepath.Clean(reviewLogPath)
	if filepath.Dir(reviewLogPath) != filepath.Clean(sessionsDir) {
		return ids
	}
	base := filepath.Base(reviewLogPath)
	if reviewID, ok := strings.CutSuffix(base, ".log"); ok {
		if reviewID != "" && reviewID != taskID {
			ids = append(ids, reviewID)
		}
	}
	return ids
}

type sessionDeleteResult struct {
	TaskIDs        []string
	CleanupWarning error
}

func (c *InProcessClient) deleteSessionGraph(ctx context.Context, workItemID string) (sessionDeleteResult, error) {
	result := sessionDeleteResult{TaskIDs: make([]string, 0)}
	artifactDeletes := make([]struct {
		taskID        string
		reviewLogPath string
	}, 0)
	worktreeByRepo := map[string]string{}

	plan, err := c.deps.Plans.GetPlanByWorkItemID(ctx, workItemID)
	var notFound service.ErrNotFound
	if err != nil && !errors.As(err, &notFound) {
		return sessionDeleteResult{}, err
	}
	if err == nil {
		taskPlans, err := c.deps.Plans.ListSubPlansByPlanID(ctx, plan.ID)
		if err != nil {
			return sessionDeleteResult{}, err
		}
		for _, taskPlan := range taskPlans {
			tasks, err := c.deps.AgentSessions.ListBySubPlanID(ctx, taskPlan.ID)
			if err != nil {
				return sessionDeleteResult{}, err
			}
			for _, agentSession := range tasks {
				result.TaskIDs = append(result.TaskIDs, agentSession.ID)
				artifactDeletes = append(artifactDeletes, struct {
					taskID        string
					reviewLogPath string
				}{taskID: agentSession.ID, reviewLogPath: c.deps.DeleteReviewLogPaths[agentSession.ID]})
				if agentSession.WorktreePath != "" {
					worktreeByRepo[agentSession.RepositoryName] = agentSession.WorktreePath
				}
				if c.deps.SessionRegistry != nil {
					c.deps.SessionRegistry.AbortAndDeregister(ctx, agentSession.ID)
				}
				if err := c.deps.AgentSessions.Delete(ctx, agentSession.ID); err != nil {
					return sessionDeleteResult{}, err
				}
			}
			if err := c.deps.Plans.DeleteSubPlan(ctx, taskPlan.ID); err != nil {
				return sessionDeleteResult{}, err
			}
		}
		if err := c.deps.Plans.DeletePlan(ctx, plan.ID); err != nil {
			return sessionDeleteResult{}, err
		}
	}

	if err := c.deleteSessionReviewArtifacts(ctx, workItemID); err != nil {
		return sessionDeleteResult{}, fmt.Errorf("delete session review artifacts: %w", err)
	}
	if err := c.deps.Sessions.Delete(ctx, workItemID); err != nil {
		return sessionDeleteResult{}, err
	}

	var cleanupErrs []error
	for _, artifactDelete := range artifactDeletes {
		if err := deleteTaskArtifacts(c.deps.SessionsDir, artifactDelete.taskID, artifactDelete.reviewLogPath); err != nil {
			cleanupErrs = append(cleanupErrs, err)
		}
	}
	for repo, worktreePath := range worktreeByRepo {
		if worktreePath == "" || c.deps.GitClient == nil {
			continue
		}
		repoDir := filepath.Dir(worktreePath)
		branch := filepath.Base(worktreePath)
		if err := c.deps.GitClient.Remove(ctx, repoDir, branch); err != nil {
			slog.Warn("failed to remove worktree during session delete", "worktree", worktreePath, "repo", repo, "error", err)
			cleanupErrs = append(cleanupErrs, fmt.Errorf("remove worktree %s: %w", worktreePath, err))
		} else {
			slog.Debug("removed worktree during session delete", "worktree", worktreePath, "repo", repo)
		}
	}
	result.CleanupWarning = errors.Join(cleanupErrs...)
	return result, nil
}

func (c *InProcessClient) deleteSessionReviewArtifacts(ctx context.Context, workItemID string) error {
	if c.deps.SessionArtifacts == nil {
		return nil
	}
	artifacts, err := c.deps.SessionArtifacts.ListByWorkItemID(ctx, workItemID)
	if err != nil {
		return fmt.Errorf("list session review artifacts: %w", err)
	}
	for _, artifact := range artifacts {
		switch artifact.Provider {
		case "gitlab":
			if c.deps.GitlabMRReviews != nil {
				if err := c.deps.GitlabMRReviews.DeleteByMRID(ctx, artifact.ProviderArtifactID); err != nil {
					slog.Warn("failed to delete MR reviews during session delete", "artifact_id", artifact.ID, "mr_id", artifact.ProviderArtifactID, "error", err)
				}
			}
			if c.deps.GitlabMRChecks != nil {
				if err := c.deps.GitlabMRChecks.DeleteByMRID(ctx, artifact.ProviderArtifactID); err != nil {
					slog.Warn("failed to delete MR checks during session delete", "artifact_id", artifact.ID, "mr_id", artifact.ProviderArtifactID, "error", err)
				}
			}
			if c.deps.GitlabMRs != nil {
				if err := c.deps.GitlabMRs.Delete(ctx, artifact.ProviderArtifactID); err != nil {
					slog.Warn("failed to delete MR during session delete", "artifact_id", artifact.ID, "mr_id", artifact.ProviderArtifactID, "error", err)
				}
			}
		case "github":
			if c.deps.GithubPRReviews != nil {
				if err := c.deps.GithubPRReviews.DeleteByPRID(ctx, artifact.ProviderArtifactID); err != nil {
					slog.Warn("failed to delete PR reviews during session delete", "artifact_id", artifact.ID, "pr_id", artifact.ProviderArtifactID, "error", err)
				}
			}
			if c.deps.GithubPRChecks != nil {
				if err := c.deps.GithubPRChecks.DeleteByPRID(ctx, artifact.ProviderArtifactID); err != nil {
					slog.Warn("failed to delete PR checks during session delete", "artifact_id", artifact.ID, "pr_id", artifact.ProviderArtifactID, "error", err)
				}
			}
			if c.deps.GithubPRs != nil {
				if err := c.deps.GithubPRs.Delete(ctx, artifact.ProviderArtifactID); err != nil {
					slog.Warn("failed to delete PR during session delete", "artifact_id", artifact.ID, "pr_id", artifact.ProviderArtifactID, "error", err)
				}
			}
		}
	}
	if err := c.deps.SessionArtifacts.DeleteByWorkItemID(ctx, workItemID); err != nil {
		return fmt.Errorf("delete session review artifacts: %w", err)
	}
	return nil
}

func (c *InProcessClient) OverrideAccept(ctx context.Context, workItemID string) (ActionResult, error) {
	if c.deps.Sessions == nil {
		return ActionResult{}, fmt.Errorf("session service is required")
	}
	if err := c.deps.Sessions.CompleteWorkItem(ctx, workItemID); err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Message: "Work item accepted"}, nil
}

func (c *InProcessClient) FailReview(ctx context.Context, workItemID string) (ActionResult, error) {
	if c.deps.Sessions == nil {
		return ActionResult{}, fmt.Errorf("session service is required")
	}
	if err := c.deps.Sessions.FailWorkItem(ctx, workItemID); err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Message: "Work item failed"}, nil
}

func (c *InProcessClient) ApprovePlan(ctx context.Context, planID, workItemID string) (ActionResult, error) {
	if c.deps.Plans == nil || c.deps.Sessions == nil {
		return ActionResult{}, fmt.Errorf("plan and session services are required")
	}
	approvalCtx := c.planApprovalEventContext(ctx, planID, workItemID)
	if err := c.deps.Plans.ApprovePlan(ctx, planID, service.WithPlanApprovalEventContext(approvalCtx)); err != nil {
		return ActionResult{}, err
	}
	if err := c.deps.Sessions.ApprovePlan(ctx, workItemID); err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Message: "Plan approved"}, nil
}

func (c *InProcessClient) planApprovalEventContext(ctx context.Context, planID, workItemID string) service.PlanApprovalEventContext {
	approvalCtx := service.PlanApprovalEventContext{}
	workItem, err := c.deps.Sessions.Get(ctx, workItemID)
	if err != nil {
		slog.Warn("failed to get work item for plan approval context", "work_item_id", workItemID, "error", err)
		return approvalCtx
	}
	approvalCtx.ExternalID = workItem.ExternalID
	approvalCtx.ExternalIDs = service.WorkItemEventExternalIDs(workItem)

	plan, err := c.deps.Plans.GetPlan(ctx, planID)
	if err != nil {
		slog.Warn("failed to get plan for plan approval context", "plan_id", planID, "error", err)
		return approvalCtx
	}
	commentMode := config.IssueCommentSubPlan
	if c.deps.Config != nil {
		commentMode = c.deps.Config.IssueCommentContentForSource(workItem.Source)
	}
	if commentBody := c.issueCommentBody(ctx, commentMode, plan); commentBody != "" {
		approvalCtx.CommentBody = commentBody
	}
	if c.deps.Config != nil {
		approvalCtx.RepoCommentScopes = c.deps.Config.IssueActionScopesForWorkItem(workItem)
	}
	return approvalCtx
}

func (c *InProcessClient) issueCommentBody(ctx context.Context, mode config.IssueCommentContent, plan domain.Plan) string {
	switch mode {
	case config.IssueCommentNone:
		return ""
	case config.IssueCommentOrchestratorPlan:
		return strings.TrimSpace(plan.OrchestratorPlan)
	case config.IssueCommentSubPlan:
		subPlans, err := c.deps.Plans.ListSubPlansByPlanID(ctx, plan.ID)
		if err != nil {
			slog.Warn("failed to list sub-plans for issue comment", "plan_id", plan.ID, "error", err)
			return ""
		}
		return domain.ComposeSubPlansContent(subPlans)
	case config.IssueCommentOrchestratorAndSubPlan:
		subPlans, err := c.deps.Plans.ListSubPlansByPlanID(ctx, plan.ID)
		if err != nil {
			slog.Warn("failed to list sub-plans for issue comment", "plan_id", plan.ID, "error", err)
			return strings.TrimSpace(plan.OrchestratorPlan)
		}
		orchestration := strings.TrimSpace(plan.OrchestratorPlan)
		subContent := domain.ComposeSubPlansContent(subPlans)
		switch {
		case orchestration == "":
			return subContent
		case subContent == "":
			return orchestration
		default:
			return orchestration + "\n\n" + subContent
		}
	case config.IssueCommentFullPlan:
		subPlans, err := c.deps.Plans.ListSubPlansByPlanID(ctx, plan.ID)
		if err != nil {
			slog.Warn("failed to list sub-plans for issue comment", "plan_id", plan.ID, "error", err)
			return strings.TrimSpace(plan.OrchestratorPlan)
		}
		return domain.ComposePlanDocument(plan, subPlans)
	default:
		slog.Warn("unknown issue_comment_content mode; comment suppressed", "mode", mode)
		return ""
	}
}

func (c *InProcessClient) CancelPipeline(ctx context.Context, workItemID, agentSessionID string) (ActionResult, error) {
	if c.deps.AgentSessions == nil {
		return ActionResult{}, fmt.Errorf("agent session service is required")
	}
	var sessions []domain.AgentSession
	if strings.TrimSpace(agentSessionID) != "" {
		session, err := c.deps.AgentSessions.Get(ctx, agentSessionID)
		if err != nil {
			return ActionResult{}, fmt.Errorf("get agent session for cancellation: %w", err)
		}
		sessions = []domain.AgentSession{session}
	} else {
		list, err := c.deps.AgentSessions.ListByWorkItemID(ctx, workItemID)
		if err != nil {
			return ActionResult{}, fmt.Errorf("list agent sessions for cancellation: %w", err)
		}
		sessions = list
	}
	for _, session := range sessions {
		switch session.Status {
		case domain.AgentSessionRunning, domain.AgentSessionWaitingForAnswer:
			if err := c.interruptAgentSession(ctx, session); err != nil {
				return ActionResult{}, err
			}
		}
	}
	return ActionResult{Message: "Pipeline cancelled"}, nil
}

func (c *InProcessClient) interruptAgentSession(ctx context.Context, session domain.AgentSession) error {
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cleanupCancel()
	if c.deps.SessionRegistry != nil {
		if harnessSession, ok := c.deps.SessionRegistry.Registered(session.ID); ok {
			if info := harnessSession.ResumeInfo(); len(info) > 0 {
				if err := c.deps.AgentSessions.UpdateResumeInfo(cleanupCtx, session.ID, info); err != nil {
					return fmt.Errorf("update resume info for %s: %w", session.ID, err)
				}
			}
		}
	}
	if err := c.deps.AgentSessions.Interrupt(cleanupCtx, session.ID); err != nil {
		return fmt.Errorf("interrupt agent session %s: %w", session.ID, err)
	}
	if c.deps.SessionRegistry != nil {
		c.deps.SessionRegistry.AbortAndDeregister(cleanupCtx, session.ID)
	}
	return nil
}

func (c *InProcessClient) RequestPlanChanges(ctx context.Context, workItemID, planID, feedback string) (ActionResult, error) {
	if c.deps.Planning == nil {
		return ActionResult{}, fmt.Errorf("planning service is required")
	}
	if _, err := c.deps.Planning.PlanWithFeedback(ctx, workItemID, planID, feedback); err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Message: "Plan revised"}, nil
}

func (c *InProcessClient) SaveReviewedPlan(ctx context.Context, planID, content string) (domain.Plan, []domain.TaskPlan, error) {
	if c.deps.Planning == nil {
		return domain.Plan{}, nil, fmt.Errorf("planning service is required")
	}
	plan, subPlans, err := c.deps.Planning.UpdateReviewedPlan(ctx, planID, content)
	if err != nil {
		return domain.Plan{}, nil, err
	}
	return plan, subPlans, nil
}

func (c *InProcessClient) RunImplementation(ctx context.Context, planID string) (Operation, error) {
	if c.deps.Implementation == nil {
		return Operation{}, fmt.Errorf("implementation service is required")
	}
	// Detach from the unary RPC context so the background work survives
	// request cancellation. Completion and failures are surfaced through
	// domain events by the orchestrator.
	dispatchCtx := context.WithoutCancel(ctx)
	go func() {
		// Start the Foreman before the implementation pipeline so the
		// question router can route sub-agent questions to it. This mirrors
		// the legacy TUI path, which dispatched BeginForemanOrchestratedCmd
		// alongside RunImplementationCmd.
		plan, planErr := c.deps.Plans.GetPlan(dispatchCtx, planID)
		if planErr != nil {
			slog.Error("implementation aborted: failed to load plan for foreman", "error", planErr, "plan_id", planID)
			return
		}
		workItemID := plan.WorkItemID
		if err := c.deps.Implementation.BeginForeman(dispatchCtx, workItemID, planID); err != nil {
			slog.Warn("failed to begin foreman before implementation", "error", err, "plan_id", planID, "work_item_id", workItemID)
		}
		if _, err := c.deps.Implementation.Implement(dispatchCtx, planID); err != nil {
			slog.Error("implementation operation failed", "error", err, "plan_id", planID)
			return
		}
	}()
	return Operation{ID: domain.NewID(), Status: "running"}, nil
}

func (c *InProcessClient) StartPlanning(ctx context.Context, workItemID string) (ActionResult, error) {
	if c.deps.Planning == nil {
		return ActionResult{}, fmt.Errorf("planning service is required")
	}
	if _, err := c.deps.Planning.Plan(ctx, workItemID); err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Message: "Planning complete"}, nil
}

func (c *InProcessClient) RestartPlanning(ctx context.Context, workItemID, prompt string) (ActionResult, error) {
	if c.deps.Sessions == nil || c.deps.AgentSessions == nil || c.deps.Planning == nil {
		return ActionResult{}, fmt.Errorf("session, agent session, and planning services are required")
	}
	dispatchCtx := context.WithoutCancel(ctx)
	go func() {
		if err := c.deps.Sessions.RollbackPlanningInterrupt(dispatchCtx, workItemID); err != nil {
			slog.Error("rollback interrupted planning failed", "error", err, "work_item_id", workItemID)
			return
		}
		sessions, err := c.deps.AgentSessions.ListByWorkItemID(dispatchCtx, workItemID)
		if err != nil {
			slog.Error("list sessions for planning restart failed", "error", err, "work_item_id", workItemID)
			return
		}
		var interrupted *domain.AgentSession
		for i := range sessions {
			if sessions[i].Status == domain.AgentSessionInterrupted && sessions[i].Kind == domain.AgentSessionKindPlanning {
				interrupted = &sessions[i]
				break
			}
		}
		if interrupted != nil {
			if failErr := c.deps.AgentSessions.Fail(dispatchCtx, interrupted.ID, nil); failErr != nil {
				slog.Warn("failed to fail interrupted planning session before resume", "agent_session_id", interrupted.ID, "error", failErr)
			}
			if _, err := c.deps.Planning.ResumeInterruptedPlanning(dispatchCtx, *interrupted, prompt); err != nil {
				slog.Error("resume interrupted planning failed", "error", err, "work_item_id", workItemID, "agent_session_id", interrupted.ID)
			}
			return
		}
		if _, err := c.deps.Planning.Plan(dispatchCtx, workItemID); err != nil {
			slog.Error("restart planning failed", "error", err, "work_item_id", workItemID)
		}
	}()
	return ActionResult{Message: "Planning restart dispatched"}, nil
}

func (c *InProcessClient) FollowUpPlan(ctx context.Context, workItemID, feedback string) (ActionResult, error) {
	if c.deps.Planning == nil {
		return ActionResult{}, fmt.Errorf("planning service is required")
	}
	if _, err := c.deps.Planning.FollowUpPlan(ctx, workItemID, feedback); err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Message: "Follow-up plan ready for review"}, nil
}

func (c *InProcessClient) FinalizeWorkItem(ctx context.Context, workItemID string) (ActionResult, error) {
	if c.deps.Implementation == nil {
		return ActionResult{}, fmt.Errorf("implementation service is required")
	}
	dispatchCtx := context.WithoutCancel(ctx)
	go func() {
		if _, err := c.deps.Implementation.FinalizeWorkItem(dispatchCtx, workItemID); err != nil {
			slog.Error("finalize work item failed", "error", err, "work_item_id", workItemID)
		}
	}()
	return ActionResult{Message: "Finalize dispatched"}, nil
}

func (c *InProcessClient) RetryFailedWorkItem(ctx context.Context, planID, workItemID string) (ActionResult, error) {
	if c.deps.Sessions == nil || c.deps.Implementation == nil {
		return ActionResult{}, fmt.Errorf("session and implementation services are required")
	}
	dispatchCtx := context.WithoutCancel(ctx)
	go func() {
		if err := c.deps.Sessions.RetryFailedWorkItem(dispatchCtx, workItemID); err != nil {
			slog.Error("retry failed work item failed", "error", err, "work_item_id", workItemID)
			return
		}
		result, err := c.deps.Implementation.ResumeRetryLeavesForWorkItem(dispatchCtx, workItemID, orchestrator.ResumeRetryModeRetryFailed, "")
		if err != nil {
			slog.Error("retry graph leaves failed", "error", err, "work_item_id", workItemID)
			return
		}
		for _, skipped := range result.Skipped {
			slog.Warn("skipped graph retry leaf", "work_item_id", workItemID, "agent_session_id", skipped.SessionID, "kind", skipped.Kind, "status", skipped.Status, "reason", skipped.Reason)
		}
	}()
	return ActionResult{Message: "Retry dispatched"}, nil
}

func (c *InProcessClient) ResumeAllSessionsForWorkItem(ctx context.Context, workItemID, instanceID string) (ActionResult, error) {
	if c.deps.Implementation == nil {
		return ActionResult{}, fmt.Errorf("implementation service is required for graph resume")
	}
	dispatchCtx := context.WithoutCancel(ctx)
	go func() {
		recovery, err := c.deps.Implementation.RecoverContinuationsForWorkItem(dispatchCtx, workItemID)
		if err != nil {
			slog.Error("recover continuation work failed", "error", err, "work_item_id", workItemID)
			return
		}
		if recovery.Recovered > 0 || len(recovery.Skipped) > 0 {
			slog.Debug("manual continuation recovery completed", "work_item_id", workItemID, "recovered", recovery.Recovered, "skipped", len(recovery.Skipped))
		}
		result, err := c.deps.Implementation.ResumeRetryLeavesForWorkItem(dispatchCtx, workItemID, orchestrator.ResumeRetryModeResumeInterrupted, instanceID)
		if err != nil {
			slog.Error("resume graph leaves failed", "error", err, "work_item_id", workItemID)
			return
		}
		for _, skipped := range result.Skipped {
			slog.Warn("skipped graph resume leaf", "work_item_id", workItemID, "agent_session_id", skipped.SessionID, "kind", skipped.Kind, "status", skipped.Status, "reason", skipped.Reason)
		}
	}()
	return ActionResult{Message: "Resume dispatched"}, nil
}

func (c *InProcessClient) RetryAgentSession(ctx context.Context, sessionID, instanceID string) (ActionResult, error) {
	if c.deps.AgentSessions == nil {
		return ActionResult{}, fmt.Errorf("agent session service is required")
	}
	if c.deps.Implementation == nil {
		return ActionResult{}, fmt.Errorf("implementation service is required")
	}
	task, err := c.deps.AgentSessions.Get(ctx, sessionID)
	if err != nil {
		return ActionResult{}, fmt.Errorf("get session for retry: %w", err)
	}
	trigger := orchestrator.AgentGraphTriggerRetryFailed
	if task.Status == domain.AgentSessionInterrupted {
		trigger = orchestrator.AgentGraphTriggerResumeInterrupted
	}
	dispatchCtx := context.WithoutCancel(ctx)
	go func() {
		intent := orchestrator.AgentGraphIntent{
			SourceSessionID:   task.ID,
			WorkItemID:        task.WorkItemID,
			SubPlanID:         task.SubPlanID,
			Trigger:           trigger,
			CurrentInstanceID: instanceID,
		}
		var err error
		switch task.Kind {
		case domain.AgentSessionKindImplementation:
			_, err = c.deps.Implementation.StartImplementationGraphRun(dispatchCtx, intent)
		case domain.AgentSessionKindReview:
			_, err = c.deps.Implementation.RetryReviewLeaf(dispatchCtx, intent)
		default:
			err = fmt.Errorf("retry not supported for kind %s", task.Kind)
		}
		if err != nil {
			slog.Error("retry graph run failed", "error", err, "agent_session_id", task.ID)
		}
	}()
	return ActionResult{Message: "Retry dispatched"}, nil
}

func (c *InProcessClient) FollowUpAgentSession(ctx context.Context, sessionID, feedback, instanceID string) (ActionResult, error) {
	if c.deps.AgentSessions == nil {
		return ActionResult{}, fmt.Errorf("agent session service is required")
	}
	task, err := c.deps.AgentSessions.Get(ctx, sessionID)
	if err != nil {
		return ActionResult{}, fmt.Errorf("get session for follow-up: %w", err)
	}
	if task.Kind == domain.AgentSessionKindManual {
		if c.deps.Manual == nil {
			return ActionResult{}, fmt.Errorf("manual session service is required")
		}
		if _, err := c.deps.Manual.FollowUpManualSession(context.WithoutCancel(ctx), task, feedback); err != nil {
			return ActionResult{}, fmt.Errorf("follow up manual session %s: %w", sessionID, err)
		}
		return ActionResult{Message: "Follow-up session started"}, nil
	}
	if c.deps.Implementation == nil {
		return ActionResult{}, fmt.Errorf("implementation service is required")
	}
	trigger := orchestrator.AgentGraphTriggerFollowUpCompleted
	if task.Status == domain.AgentSessionFailed {
		trigger = orchestrator.AgentGraphTriggerFollowUpFailed
	}
	dispatchCtx := context.WithoutCancel(ctx)
	go func() {
		if _, err := c.deps.Implementation.StartImplementationGraphRun(dispatchCtx, orchestrator.AgentGraphIntent{
			SourceSessionID:   task.ID,
			WorkItemID:        task.WorkItemID,
			SubPlanID:         task.SubPlanID,
			Trigger:           trigger,
			Feedback:          feedback,
			CurrentInstanceID: instanceID,
		}); err != nil {
			slog.Error("follow-up graph run failed", "error", err, "agent_session_id", task.ID)
		}
	}()
	return ActionResult{Message: "Follow-up dispatched"}, nil
}

func (c *InProcessClient) SteerSession(ctx context.Context, sessionID, message string) (ActionResult, error) {
	if c.deps.AgentSessions != nil {
		task, err := c.deps.AgentSessions.Get(ctx, sessionID)
		if err == nil && task.Kind == domain.AgentSessionKindManual {
			if c.deps.Manual == nil {
				return ActionResult{}, fmt.Errorf("manual session service is required")
			}
			if err := c.deps.Manual.SendMessage(ctx, sessionID, message); err != nil {
				return ActionResult{}, fmt.Errorf("prompt manual session %s: %w", sessionID, err)
			}
			return ActionResult{Message: "Steering prompt sent"}, nil
		}
		if err != nil {
			slog.Debug("agent session lookup before steering failed", "agent_session_id", sessionID, "error", err)
		}
	}
	if c.deps.SessionRegistry == nil {
		return ActionResult{}, fmt.Errorf("session registry is required")
	}
	if err := c.deps.SessionRegistry.Steer(ctx, sessionID, message); err != nil {
		return ActionResult{}, fmt.Errorf("steer session %s: %w", sessionID, err)
	}
	return ActionResult{Message: "Steering prompt sent"}, nil
}

func (c *InProcessClient) AnswerQuestion(ctx context.Context, questionID, answer, answeredBy string) (ActionResult, error) {
	if c.deps.AnswerRouter == nil {
		return ActionResult{}, fmt.Errorf("answer router is required")
	}
	if err := c.deps.AnswerRouter.Answer(ctx, questionID, answer, answeredBy); err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Message: "Answer submitted"}, nil
}

func (c *InProcessClient) SkipQuestion(ctx context.Context, questionID string) (ActionResult, error) {
	if c.deps.AnswerRouter == nil {
		return ActionResult{}, fmt.Errorf("answer router is required")
	}
	if err := c.deps.AnswerRouter.Skip(ctx, questionID); err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Message: "Question skipped"}, nil
}

func (c *InProcessClient) artifactItems(ctx context.Context, workspaceID string) (map[string][]ArtifactItem, error) {
	out := make(map[string][]ArtifactItem)
	if c.deps.SessionArtifacts == nil {
		return out, nil
	}
	links, err := c.deps.SessionArtifacts.ListByWorkspaceID(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list session review artifacts: %w", err)
	}
	for _, link := range links {
		switch link.Provider {
		case "github":
			item, ok, err := c.githubArtifactItem(ctx, link.ProviderArtifactID)
			if err != nil {
				slog.Warn("failed to build github artifact read model", "artifact_id", link.ProviderArtifactID, "error", err)
				continue
			}
			if ok {
				out[link.WorkItemID] = append(out[link.WorkItemID], item)
			}
		case "gitlab":
			item, ok, err := c.gitlabArtifactItem(ctx, link.ProviderArtifactID)
			if err != nil {
				slog.Warn("failed to build gitlab artifact read model", "artifact_id", link.ProviderArtifactID, "error", err)
				continue
			}
			if ok {
				out[link.WorkItemID] = append(out[link.WorkItemID], item)
			}
		}
	}
	for workItemID := range out {
		sort.SliceStable(out[workItemID], func(i, j int) bool {
			if out[workItemID][i].RepoName != out[workItemID][j].RepoName {
				return out[workItemID][i].RepoName < out[workItemID][j].RepoName
			}
			return out[workItemID][i].Ref < out[workItemID][j].Ref
		})
	}
	return out, nil
}

func (c *InProcessClient) githubArtifactItem(ctx context.Context, id string) (ArtifactItem, bool, error) {
	if c.deps.GithubPRs == nil {
		return ArtifactItem{}, false, nil
	}
	pr, err := c.deps.GithubPRs.Get(ctx, id)
	if err != nil {
		return ArtifactItem{}, false, err
	}
	state := pr.State
	if pr.Draft && state != "merged" && state != "closed" {
		state = "draft"
	}
	var reviews []ArtifactReview
	if c.deps.GithubPRReviews != nil {
		ghReviews, err := c.deps.GithubPRReviews.ListByPRID(ctx, pr.ID)
		if err != nil {
			return ArtifactItem{}, false, err
		}
		reviews = make([]ArtifactReview, 0, len(ghReviews))
		for _, review := range ghReviews {
			reviews = append(reviews, ArtifactReview{ReviewerLogin: review.ReviewerLogin, State: review.State, SubmittedAt: review.SubmittedAt})
		}
	}
	var checks []ArtifactCheck
	if c.deps.GithubPRChecks != nil {
		ghChecks, err := c.deps.GithubPRChecks.ListByPRID(ctx, pr.ID)
		if err != nil {
			return ArtifactItem{}, false, err
		}
		checks = make([]ArtifactCheck, 0, len(ghChecks))
		for _, check := range ghChecks {
			checks = append(checks, ArtifactCheck{Name: check.Name, Status: check.Status, Conclusion: check.Conclusion})
		}
	}
	return ArtifactItem{
		ID:        fmt.Sprintf("github:%s/%s:#%d", pr.Owner, pr.Repo, pr.Number),
		Provider:  "github",
		Kind:      "PR",
		RepoName:  pr.Owner + "/" + pr.Repo,
		Ref:       fmt.Sprintf("#%d", pr.Number),
		URL:       pr.HTMLURL,
		State:     state,
		Branch:    pr.HeadBranch,
		Draft:     pr.Draft,
		MergedAt:  pr.MergedAt,
		CreatedAt: pr.CreatedAt,
		UpdatedAt: pr.UpdatedAt,
		Reviews:   reviews,
		Checks:    checks,
	}, true, nil
}

func (c *InProcessClient) gitlabArtifactItem(ctx context.Context, id string) (ArtifactItem, bool, error) {
	if c.deps.GitlabMRs == nil {
		return ArtifactItem{}, false, nil
	}
	mr, err := c.deps.GitlabMRs.Get(ctx, id)
	if err != nil {
		return ArtifactItem{}, false, err
	}
	state := mr.State
	if mr.Draft && state != "merged" && state != "closed" {
		state = "draft"
	}
	var reviews []ArtifactReview
	if c.deps.GitlabMRReviews != nil {
		glReviews, err := c.deps.GitlabMRReviews.ListByMRID(ctx, mr.ID)
		if err != nil {
			return ArtifactItem{}, false, err
		}
		reviews = make([]ArtifactReview, 0, len(glReviews))
		for _, review := range glReviews {
			reviews = append(reviews, ArtifactReview{ReviewerLogin: review.ReviewerLogin, State: review.State, SubmittedAt: review.SubmittedAt})
		}
	}
	var checks []ArtifactCheck
	if c.deps.GitlabMRChecks != nil {
		glChecks, err := c.deps.GitlabMRChecks.ListByMRID(ctx, mr.ID)
		if err != nil {
			return ArtifactItem{}, false, err
		}
		checks = make([]ArtifactCheck, 0, len(glChecks))
		for _, check := range glChecks {
			checks = append(checks, ArtifactCheck{Name: check.Name, Status: check.Status, Conclusion: check.Conclusion})
		}
	}
	return ArtifactItem{
		ID:           fmt.Sprintf("gitlab:%s:!%d", mr.ProjectPath, mr.IID),
		Provider:     "gitlab",
		Kind:         "MR",
		RepoName:     mr.ProjectPath,
		Ref:          fmt.Sprintf("!%d", mr.IID),
		URL:          mr.WebURL,
		State:        state,
		Branch:       mr.SourceBranch,
		Draft:        mr.Draft,
		WorktreePath: mr.WorktreePath,
		CreatedAt:    mr.CreatedAt,
		UpdatedAt:    mr.UpdatedAt,
		Reviews:      reviews,
		Checks:       checks,
	}, true, nil
}

func (c *InProcessClient) GetInitialSnapshot(ctx context.Context, workspaceID string) (InitialSnapshot, error) {
	// Capture the replay cursor BEFORE any other reads so events committed
	// during snapshot building have a sequence > latestSequence and are
	// replayed by the event stream after the client consumes this snapshot.
	// Reading LatestSequence last would race with concurrent event commits
	// and silently drop them on the floor.
	var latestSequence uint64
	if c.deps.Events != nil {
		var err error
		latestSequence, err = c.deps.Events.LatestSequence(ctx, workspaceID)
		if err != nil {
			return InitialSnapshot{}, err
		}
	}
	sessions, err := c.ListSessions(ctx, workspaceID)
	if err != nil {
		return InitialSnapshot{}, err
	}
	var agentSessions []domain.AgentSession
	if c.deps.AgentSessions != nil {
		agentSessions, err = c.deps.AgentSessions.ListByWorkspaceID(ctx, workspaceID)
		if err != nil {
			return InitialSnapshot{}, err
		}
	}
	plans := make(map[string]domain.Plan, len(sessions))
	subPlans := make(map[string][]domain.TaskPlan, len(sessions))
	if c.deps.Plans != nil {
		for _, session := range sessions {
			plan, planErr := c.deps.Plans.GetPlanByWorkItemID(ctx, session.ID)
			if planErr != nil {
				var notFound service.ErrNotFound
				if errors.As(planErr, &notFound) {
					continue
				}
				return InitialSnapshot{}, fmt.Errorf("get plan for work item %s: %w", session.ID, planErr)
			}
			plans[session.ID] = plan
			children, childrenErr := c.deps.Plans.ListSubPlansByPlanID(ctx, plan.ID)
			if childrenErr != nil {
				return InitialSnapshot{}, childrenErr
			}
			subPlans[session.ID] = children
		}
	}
	questions := make(map[string][]domain.Question)
	if c.deps.Questions != nil {
		for _, agentSession := range agentSessions {
			open, qErr := c.deps.Questions.ListBySessionID(ctx, agentSession.ID)
			if qErr != nil {
				return InitialSnapshot{}, qErr
			}
			if len(open) > 0 {
				questions[agentSession.ID] = open
			}
		}
	}
	reviews := make(map[string][]domain.ReviewCycle)
	critiques := make(map[string][]domain.Critique)
	if c.deps.Reviews != nil {
		for _, agentSession := range agentSessions {
			cycles, reviewErr := c.deps.Reviews.ListCyclesBySessionID(ctx, agentSession.ID)
			if reviewErr != nil {
				return InitialSnapshot{}, reviewErr
			}
			if len(cycles) > 0 {
				reviews[agentSession.ID] = cycles
			}
			for _, cycle := range cycles {
				cycleCritiques, critErr := c.deps.Reviews.ListCritiquesByCycleID(ctx, cycle.ID)
				if critErr != nil {
					return InitialSnapshot{}, critErr
				}
				if len(cycleCritiques) > 0 {
					critiques[cycle.ID] = cycleCritiques
				}
			}
		}
	}
	var filters []domain.NewSessionFilter
	if c.deps.Filters != nil {
		filters, err = c.deps.Filters.ListByWorkspaceID(ctx, workspaceID)
		if err != nil {
			return InitialSnapshot{}, err
		}
	}
	var instances []domain.SubstrateInstance
	if c.deps.Instances != nil {
		instances, err = c.deps.Instances.ListByWorkspaceID(ctx, workspaceID)
		if err != nil {
			return InitialSnapshot{}, err
		}
	}
	artifacts, err := c.artifactItems(ctx, workspaceID)
	if err != nil {
		return InitialSnapshot{}, err
	}
	return InitialSnapshot{
		Artifacts:           artifacts,
		Sessions:            sessions,
		AgentSessions:       agentSessions,
		Plans:               plans,
		SubPlans:            subPlans,
		Questions:           questions,
		Filters:             filters,
		Reviews:             reviews,
		Critiques:           critiques,
		LiveInstances:       instances,
		ArchivedSessionIDs:  archivedSessionIDs(sessions),
		LatestEventSequence: latestSequence,
	}, nil
}

func archivedSessionIDs(sessions []domain.Session) []string {
	ids := make([]string, 0)
	for _, session := range sessions {
		if session.State == domain.SessionArchived {
			ids = append(ids, session.ID)
		}
	}
	return ids
}
