// Package orchestrator provides session orchestration services.
package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/gitwork"
	"github.com/beeemT/substrate/internal/service"
)

// ManualSessionService orchestrates manual agent sessions within an existing work item.
// Unlike implementation sessions, manual sessions have no sub-plan context, no automatic
// commit/push, and no Foreman involvement. Questions are routed to the operator inline.
type ManualSessionService struct {
	cfg            *config.Config
	harness        adapter.AgentHarness
	gitClient      *gitwork.Client
	sessionSvc     *service.AgentSessionService
	workItemSvc    *service.SessionService
	workspaceSvc   *service.WorkspaceService
	registry       *SessionRegistry
	questionRouter *QuestionRouter
	eventBus       event.Publisher
}

// NewManualSessionService creates a new ManualSessionService.
func NewManualSessionService(
	cfg *config.Config,
	harness adapter.AgentHarness,
	gitClient *gitwork.Client,
	sessionSvc *service.AgentSessionService,
	workItemSvc *service.SessionService,
	workspaceSvc *service.WorkspaceService,
	registry *SessionRegistry,
	questionRouter *QuestionRouter,
	eventBus event.Publisher,
) *ManualSessionService {
	return &ManualSessionService{
		cfg:            cfg,
		harness:        harness,
		gitClient:      gitClient,
		sessionSvc:     sessionSvc,
		workItemSvc:    workItemSvc,
		workspaceSvc:   workspaceSvc,
		registry:       registry,
		questionRouter: questionRouter,
		eventBus:       eventBus,
	}
}

// StartManualSessionRequest contains the parameters for starting a manual session.
type StartManualSessionRequest struct {
	WorkItemID      string
	WorkspaceID     string
	RepositoryName  string
	InitialMessage  string
	SubPlanID       string // optional context only
	OwnerInstanceID *string
}

// StartManualSession starts a new manual agent session in the deterministic worktree
// for the given work item and repository.
func (s *ManualSessionService) StartManualSession(ctx context.Context, req StartManualSessionRequest) (domain.AgentSession, error) {
	// 1. Validate work item exists and belongs to workspace.
	workItem, err := s.workItemSvc.Get(ctx, req.WorkItemID)
	if err != nil {
		return domain.AgentSession{}, fmt.Errorf("get work item: %w", err)
	}
	if workItem.WorkspaceID != req.WorkspaceID {
		return domain.AgentSession{}, fmt.Errorf("work item does not belong to workspace")
	}

	// 2. Validate workspace exists and get root path for repo discovery.
	workspace, err := s.workspaceSvc.Get(ctx, req.WorkspaceID)
	if err != nil {
		return domain.AgentSession{}, fmt.Errorf("get workspace: %w", err)
	}

	// 3. Discover repo paths from workspace root.
	repoPaths, err := s.discoverRepoPaths(ctx, workspace.RootPath)
	if err != nil {
		return domain.AgentSession{}, fmt.Errorf("discover repo paths: %w", err)
	}

	// 4. Validate repository is in the discovered repos.
	repoRoot, ok := repoPaths[req.RepositoryName]
	if !ok {
		return domain.AgentSession{}, fmt.Errorf("repository %q is not in workspace", req.RepositoryName)
	}

	// 5. Resolve deterministic worktree path from work item branch and repo worktree list.
	branch := GenerateBranchName(workItem.ExternalID, workItem.Title)
	worktrees, err := s.gitClient.List(ctx, repoRoot)
	if err != nil {
		return domain.AgentSession{}, fmt.Errorf("list worktrees for repo %q: %w", req.RepositoryName, err)
	}
	var worktreePath string
	for _, wt := range worktrees {
		if wt.Branch == branch {
			worktreePath = wt.Path
			break
		}
	}
	if worktreePath == "" {
		return domain.AgentSession{}, fmt.Errorf("worktree for branch %q not found in repository %q", branch, req.RepositoryName)
	}

	// 6. Check for concurrent sessions on the same worktree.
	activeSessions, err := s.sessionSvc.ListByWorkspaceID(ctx, req.WorkspaceID)
	if err != nil {
		return domain.AgentSession{}, fmt.Errorf("check active sessions: %w", err)
	}
	for _, session := range activeSessions {
		if session.WorktreePath == worktreePath && isActiveStatus(session.Status) {
			return domain.AgentSession{}, fmt.Errorf("active session %q already exists on worktree %q", session.ID, worktreePath)
		}
	}

	// 7. Create domain.AgentSession with manual phase.
	now := time.Now()
	agentSession := domain.AgentSession{
		ID:              domain.NewID(),
		WorkItemID:      req.WorkItemID,
		WorkspaceID:     req.WorkspaceID,
		Phase:           domain.AgentSessionPhaseManual,
		SubPlanID:       req.SubPlanID, // optional; may be empty
		RepositoryName:  req.RepositoryName,
		WorktreePath:    worktreePath,
		HarnessName:     s.harness.Name(),
		OwnerInstanceID: req.OwnerInstanceID,
		Status:          domain.AgentSessionPending,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	// 8. Persist the session.
	if err := s.sessionSvc.Create(ctx, agentSession); err != nil {
		return domain.AgentSession{}, fmt.Errorf("create manual session: %w", err)
	}

	// 9. Transition to running.
	if err := s.sessionSvc.Start(ctx, agentSession.ID); err != nil {
		return domain.AgentSession{}, fmt.Errorf("start manual session: %w", err)
	}

	// 10. Start harness session.
	opts := adapter.SessionOpts{
		SessionID:          agentSession.ID,
		Mode:               adapter.SessionModeAgent,
		WorkspaceID:        agentSession.WorkspaceID,
		SubPlanID:          agentSession.SubPlanID,
		Repository:         agentSession.RepositoryName,
		WorktreePath:       agentSession.WorktreePath,
		SystemPrompt:       "", // Use harness default for manual sessions
		UserPrompt:         req.InitialMessage,
		CommitConfig:       adapter.CommitConfig{},
		AllowPush:          false, // Manual sessions never auto-push
		AnswerTimeoutMs:    0,
		QuestionToolPolicy: adapter.QuestionToolPolicyHuman,
	}

	harnessSession, err := s.harness.StartSession(ctx, opts)
	if err != nil {
		// Mark session as failed with a detached context so DB write completes even if caller cancels.
		_ = failSessionDurably(context.WithoutCancel(ctx), s.sessionSvc, agentSession.ID, nil)
		return domain.AgentSession{}, fmt.Errorf("start harness session: %w", err)
	}

	// 11. Register in session registry.
	s.registry.Register(agentSession.ID, harnessSession)

	// 12. Start event forwarding and completion waiter goroutines.
	go s.forwardEvents(context.WithoutCancel(ctx), harnessSession.Events(), agentSession.ID)
	go s.waitForCompletion(context.WithoutCancel(ctx), harnessSession, agentSession.ID, agentSession.WorkItemID)

	// Refresh agent session state from DB (StartedAt is now set).
	agentSession, _ = s.sessionSvc.Get(ctx, agentSession.ID)
	return agentSession, nil
}

// SendMessage sends a follow-up message to a running manual session.
func (s *ManualSessionService) SendMessage(ctx context.Context, sessionID, message string) error {
	return s.registry.SendMessage(ctx, sessionID, message)
}

// Steer sends a steering prompt to interrupt a running manual session's active streaming turn.
func (s *ManualSessionService) Steer(ctx context.Context, sessionID, message string) error {
	return s.registry.Steer(ctx, sessionID, message)
}

// SendAnswer sends an operator answer to resolve a pending question in a manual session.
func (s *ManualSessionService) SendAnswer(ctx context.Context, sessionID, answer string) error {
	if err := s.registry.SendAnswer(ctx, sessionID, answer); err != nil {
		return fmt.Errorf("send answer to manual session: %w", err)
	}
	// Transition session back to running after answer is sent.
	if err := s.sessionSvc.ResumeFromAnswer(ctx, sessionID); err != nil {
		return fmt.Errorf("resume manual session from answer: %w", err)
	}
	return nil
}

// Abort aborts a running manual session.
func (s *ManualSessionService) Abort(ctx context.Context, sessionID string) error {
	// AbortAndDeregister handles both abort and deregistration.
	s.registry.AbortAndDeregister(ctx, sessionID)

	// Mark as interrupted using a durable context so the DB write completes even if ctx is cancelled.
	return interruptSessionDurably(context.WithoutCancel(ctx), s.sessionSvc, sessionID)
}

// ResumeManualSession resumes an interrupted manual session with a new harness instance.
// It creates a new DB row in pending/running state and links to the interrupted session's
// resume info for native conversation resumption.
func (s *ManualSessionService) ResumeManualSession(ctx context.Context, interrupted domain.AgentSession, initialMessage string, ownerInstanceID *string) (domain.AgentSession, error) {
	if interrupted.Phase != domain.AgentSessionPhaseManual {
		return domain.AgentSession{}, fmt.Errorf("can only resume manual sessions, got phase %q", interrupted.Phase)
	}

	// Create new manual session row preserving manual phase and worktree.
	now := time.Now()
	newSession := domain.AgentSession{
		ID:              domain.NewID(),
		WorkItemID:      interrupted.WorkItemID,
		WorkspaceID:     interrupted.WorkspaceID,
		Phase:           domain.AgentSessionPhaseManual,
		SubPlanID:       interrupted.SubPlanID,
		RepositoryName:  interrupted.RepositoryName,
		WorktreePath:    interrupted.WorktreePath,
		HarnessName:     s.harness.Name(),
		OwnerInstanceID: ownerInstanceID,
		Status:          domain.AgentSessionPending,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if err := s.sessionSvc.Create(ctx, newSession); err != nil {
		return domain.AgentSession{}, fmt.Errorf("create resumed manual session: %w", err)
	}

	if err := s.sessionSvc.Start(ctx, newSession.ID); err != nil {
		return domain.AgentSession{}, fmt.Errorf("start resumed manual session: %w", err)
	}

	// Start harness with resume info from the interrupted session.
	opts := adapter.SessionOpts{
		SessionID:           newSession.ID,
		Mode:                adapter.SessionModeAgent,
		WorkspaceID:         newSession.WorkspaceID,
		SubPlanID:           newSession.SubPlanID,
		Repository:          newSession.RepositoryName,
		WorktreePath:        newSession.WorktreePath,
		SystemPrompt:        "", // Use harness default
		UserPrompt:          initialMessage,
		CommitConfig:        adapter.CommitConfig{},
		AllowPush:           false,
		AnswerTimeoutMs:     0,
		QuestionToolPolicy:  adapter.QuestionToolPolicyHuman,
		ResumeFromSessionID: interrupted.ID,
		ResumeInfo:          interrupted.ResumeInfo,
	}

	harnessSession, err := s.harness.StartSession(ctx, opts)
	if err != nil {
		_ = failSessionDurably(context.WithoutCancel(ctx), s.sessionSvc, newSession.ID, nil)
		return domain.AgentSession{}, fmt.Errorf("start resumed harness session: %w", err)
	}

	s.registry.Register(newSession.ID, harnessSession)

	go s.forwardEvents(context.WithoutCancel(ctx), harnessSession.Events(), newSession.ID)
	go s.waitForCompletion(context.WithoutCancel(ctx), harnessSession, newSession.ID, newSession.WorkItemID)

	newSession, _ = s.sessionSvc.Get(ctx, newSession.ID)
	return newSession, nil
}

// FollowUpManualSession sends a follow-up message to a completed manual session.
// It reuses the same session row (completed → running) when native resume is available,
// otherwise starts a new session and links old→new via EventAgentSessionResumed.
func (s *ManualSessionService) FollowUpManualSession(ctx context.Context, completed domain.AgentSession, message string) (domain.AgentSession, error) {
	if completed.Phase != domain.AgentSessionPhaseManual {
		return domain.AgentSession{}, fmt.Errorf("can only follow up manual sessions, got phase %q", completed.Phase)
	}

	// If the harness supports native resume, reuse the existing row.
	if len(completed.ResumeInfo) > 0 {
		if err := s.sessionSvc.FollowUpRestart(ctx, completed.ID, completed.OwnerInstanceID); err != nil {
			return domain.AgentSession{}, fmt.Errorf("follow up restart manual session: %w", err)
		}

		opts := adapter.SessionOpts{
			SessionID:           completed.ID,
			Mode:                adapter.SessionModeAgent,
			WorkspaceID:         completed.WorkspaceID,
			SubPlanID:           completed.SubPlanID,
			Repository:          completed.RepositoryName,
			WorktreePath:        completed.WorktreePath,
			SystemPrompt:        "", // Use harness default
			UserPrompt:          message,
			CommitConfig:        adapter.CommitConfig{},
			AllowPush:           false,
			AnswerTimeoutMs:     0,
			QuestionToolPolicy:  adapter.QuestionToolPolicyHuman,
			ResumeFromSessionID: completed.ID,
			ResumeInfo:          completed.ResumeInfo,
		}

		harnessSession, err := s.harness.StartSession(ctx, opts)
		if err != nil {
			// Transition back to completed and start a new session instead.
			if failErr := failSessionDurably(context.WithoutCancel(ctx), s.sessionSvc, completed.ID, nil); failErr != nil {
				slog.Warn("failed to fail manual follow-up session after harness start error", "error", failErr, "agent_session_id", completed.ID)
			}
			return s.startNewFollowUpSession(ctx, completed, message)
		}

		s.registry.Register(completed.ID, harnessSession)

		go s.forwardEvents(context.WithoutCancel(ctx), harnessSession.Events(), completed.ID)
		go s.waitForCompletion(context.WithoutCancel(ctx), harnessSession, completed.ID, completed.WorkItemID)

		updated, _ := s.sessionSvc.Get(ctx, completed.ID)
		return updated, nil
	}

	// No resume info: start a new session and link old→new.
	return s.startNewFollowUpSession(ctx, completed, message)
}

// startNewFollowUpSession creates a new manual session as a follow-up to a completed one
// that cannot be natively resumed.
func (s *ManualSessionService) startNewFollowUpSession(ctx context.Context, completed domain.AgentSession, message string) (domain.AgentSession, error) {
	now := time.Now()
	newSession := domain.AgentSession{
		ID:              domain.NewID(),
		WorkItemID:      completed.WorkItemID,
		WorkspaceID:     completed.WorkspaceID,
		Phase:           domain.AgentSessionPhaseManual,
		SubPlanID:       completed.SubPlanID,
		RepositoryName:  completed.RepositoryName,
		WorktreePath:    completed.WorktreePath,
		HarnessName:     s.harness.Name(),
		OwnerInstanceID: completed.OwnerInstanceID,
		Status:          domain.AgentSessionPending,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if err := s.sessionSvc.Create(ctx, newSession); err != nil {
		return domain.AgentSession{}, fmt.Errorf("create follow-up manual session: %w", err)
	}

	if err := s.sessionSvc.Start(ctx, newSession.ID); err != nil {
		return domain.AgentSession{}, fmt.Errorf("start follow-up manual session: %w", err)
	}

	opts := adapter.SessionOpts{
		SessionID:          newSession.ID,
		Mode:               adapter.SessionModeAgent,
		WorkspaceID:        newSession.WorkspaceID,
		SubPlanID:          newSession.SubPlanID,
		Repository:         newSession.RepositoryName,
		WorktreePath:       newSession.WorktreePath,
		SystemPrompt:       "", // Use harness default
		UserPrompt:         message,
		CommitConfig:       adapter.CommitConfig{},
		AllowPush:          false,
		AnswerTimeoutMs:    0,
		QuestionToolPolicy: adapter.QuestionToolPolicyHuman,
	}

	harnessSession, err := s.harness.StartSession(ctx, opts)
	if err != nil {
		_ = failSessionDurably(context.WithoutCancel(ctx), s.sessionSvc, newSession.ID, nil)
		return domain.AgentSession{}, fmt.Errorf("start follow-up harness session: %w", err)
	}

	s.registry.Register(newSession.ID, harnessSession)

	go s.forwardEvents(context.WithoutCancel(ctx), harnessSession.Events(), newSession.ID)
	go s.waitForCompletion(context.WithoutCancel(ctx), harnessSession, newSession.ID, newSession.WorkItemID)

	// Emit EventAgentSessionResumed with old→new linkage so TUI can link the sessions.
	updated, _ := s.sessionSvc.Get(ctx, newSession.ID)
	s.eventBus.Publish(ctx, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventAgentSessionResumed),
		WorkspaceID: updated.WorkspaceID,
		Payload:     marshalManualSessionPayloadWithOld(updated, completed.ID),
		CreatedAt:   time.Now(),
	})

	newSession, _ = s.sessionSvc.Get(ctx, newSession.ID)
	return newSession, nil
}

// forwardEvents drains harness events so producer channels cannot fill.
// Detailed transcript events stay in session logs; only questions need live routing.
func (s *ManualSessionService) forwardEvents(ctx context.Context, events <-chan adapter.AgentEvent, sessionID string) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-events:
			if !ok {
				return
			}

			if evt.Type == "question" {
				if s.questionRouter != nil {
					if err := s.questionRouter.Route(ctx, domain.AgentSessionPhaseManual, evt, sessionID); err != nil {
						slog.Error("failed to route manual question", "error", err, "agent_session_id", sessionID)
					}
				}
				continue
			}

		}
	}
}

// manualSessionEventPayload holds the payload for manual session lifecycle events.
type manualSessionEventPayload struct {
	Session      domain.AgentSession `json:"session"`
	WorkItemID   string              `json:"work_item_id"`
	SessionID    string              `json:"agent_session_id"`
	OldSessionID string              `json:"old_session_id,omitempty"`
}

// marshalManualSessionPayloadWithOld serializes a resumed manual session event payload.
func marshalManualSessionPayloadWithOld(agentSession domain.AgentSession, oldSessionID string) string {
	payload := manualSessionEventPayload{
		Session:      agentSession,
		WorkItemID:   agentSession.WorkItemID,
		SessionID:    agentSession.ID,
		OldSessionID: oldSessionID,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		slog.Warn("failed to marshal manual session payload", "error", err)
		return "{}"
	}
	return string(b)
}

// waitForCompletion blocks until the harness session finishes, then transitions
// the session to the appropriate terminal state in the DB and saves resume info.
func (s *ManualSessionService) waitForCompletion(ctx context.Context, harnessSession adapter.AgentSession, sessionID, workItemID string) {
	err := harnessSession.Wait(ctx)
	if err != nil {
		slog.Warn("manual harness session wait returned error", "error", err, "agent_session_id", sessionID)
	}

	cleanupCtx, cleanupCancel := durableCleanupContext(ctx)
	defer cleanupCancel()

	// Persist resume info if available.
	if resumeInfo := harnessSession.ResumeInfo(); len(resumeInfo) > 0 {
		if updateErr := s.sessionSvc.UpdateResumeInfo(cleanupCtx, sessionID, resumeInfo); updateErr != nil {
			slog.Error("failed to update resume info for manual session", "error", updateErr, "agent_session_id", sessionID)
		}
	}

	// Transition to terminal state based on how the session ended.
	if ctx.Err() != nil {
		// Context cancelled: user aborted.
		if interruptErr := s.sessionSvc.Interrupt(cleanupCtx, sessionID); interruptErr != nil {
			slog.Error("failed to interrupt manual session", "error", interruptErr, "agent_session_id", sessionID)
		}
	} else if err != nil {
		// Harness error.
		if !agentSessionAlreadyInterrupted(cleanupCtx, s.sessionSvc, sessionID) {
			if failErr := s.sessionSvc.Fail(cleanupCtx, sessionID, nil); failErr != nil {
				slog.Error("failed to fail manual session", "error", failErr, "agent_session_id", sessionID)
			}
		}
	} else {
		// Natural completion.
		if completeErr := s.sessionSvc.Complete(ctx, sessionID); completeErr != nil {
			slog.Error("failed to complete manual session", "error", completeErr, "agent_session_id", sessionID)
		}
	}

	// Always deregister.
	s.registry.Deregister(sessionID)
}

// discoverRepoPaths discovers repo paths in the workspace.
func (s *ManualSessionService) discoverRepoPaths(_ context.Context, workspaceDir string) (map[string]string, error) {
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

// isActiveStatus reports whether a status represents an active (non-terminal) session.
func isActiveStatus(status domain.AgentSessionStatus) bool {
	return status == domain.AgentSessionPending ||
		status == domain.AgentSessionRunning ||
		status == domain.AgentSessionWaitingForAnswer
}
