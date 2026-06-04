package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/service"
)

// escalatedEntry holds the answer channel and the agent session ID for a
// question that was escalated to a human. Storing the session ID here avoids
// a round-trip to the DB when ResolveEscalated needs to resume the session.
type escalatedEntry struct {
	answerCh       chan string // bidirectional so we can close it on Stop
	agentSessionID string
}

// pendingQuestion represents a question waiting to be answered.
type pendingQuestion struct {
	question    domain.Question
	answerCh    chan string
	submittedAt time.Time
}

// Foreman manages a persistent oh-my-pi session for answering sub-agent questions.
type Foreman struct {
	cfg             *config.Config
	harness         adapter.AgentHarness
	planSvc         *service.PlanService
	questionSvc     *service.QuestionService
	agentSessionSvc *service.AgentSessionService
	workItemSvc     *service.SessionService
	eventBus        event.Publisher

	mu            sync.Mutex
	sessionMu     sync.Mutex           // serializes SendMessage+waitForAnswer; prevents concurrent Events() readers
	session       adapter.AgentSession // Current harness session
	sessionID     string               // Persisted agent session row ID (stable across restarts)
	workItemID    string
	planID        string
	questionCh    chan pendingQuestion
	questionFront chan pendingQuestion // Priority channel for re-queued questions
	stopCh        chan struct{}
	wg            sync.WaitGroup

	// lastSessionID and lastPlanID preserve the most recent foreman session
	// coordinates across Stop() so the TUI can still show the session log after
	// the foreman shuts down (e.g. when all implementation work completes).
	lastSessionID string
	lastPlanID    string

	// escalatedChs stores answer channels for questions escalated to humans.
	// Keyed by question ID. The TUI calls ResolveEscalated to deliver the answer.
	escalatedMu  sync.Mutex
	escalatedChs map[string]escalatedEntry
}

// NewForeman creates a new Foreman instance.
func NewForeman(
	cfg *config.Config,
	harness adapter.AgentHarness,
	planSvc *service.PlanService,
	questionSvc *service.QuestionService,
	agentSessionSvc *service.AgentSessionService,
	workItemSvc *service.SessionService,
	eventBus event.Publisher,
) *Foreman {
	return &Foreman{
		cfg:             cfg,
		harness:         harness,
		planSvc:         planSvc,
		questionSvc:     questionSvc,
		agentSessionSvc: agentSessionSvc,
		workItemSvc:     workItemSvc,
		eventBus:        eventBus,
		questionCh:      make(chan pendingQuestion, 100),
		questionFront:   make(chan pendingQuestion, 100),
		stopCh:          make(chan struct{}),
		escalatedChs:    make(map[string]escalatedEntry),
	}
}

// Start begins the foreman session for a plan. If followUpContext is non-empty,
// it is included in the initial user prompt so the foreman is aware of follow-up
// feedback from the operator.
//
// Start persists an AgentSessionKindForeman row if one does not already exist for
// this work item. Follow-up and restart reuse the same row so the sidebar always
// shows a single continuous Foreman entry.
func (f *Foreman) Start(ctx context.Context, planID string, followUpContext string) error {
	f.mu.Lock()

	if f.session != nil {
		f.mu.Unlock()
		return nil // Already running
	}

	// Re-arm stopCh for this start cycle.
	// Stop() closes it; Start() must allocate a fresh one so the new run() goroutine
	// is not already-exited on the first select.
	f.stopCh = make(chan struct{})

	f.planID = planID
	f.lastSessionID = ""
	f.lastPlanID = ""

	// Get plan with FAQ
	plan, err := f.planSvc.GetPlan(ctx, planID)
	if err != nil {
		f.mu.Unlock()
		return fmt.Errorf("get plan: %w", err)
	}
	f.workItemID = plan.WorkItemID

	// Look up existing foreman session row for this work item.
	// Reuse the same row across restarts so the sidebar shows one continuous Foreman.
	sessions, err := f.agentSessionSvc.ListByWorkItemID(ctx, f.workItemID)
	if err != nil {
		f.mu.Unlock()
		return fmt.Errorf("list sessions for foreman row lookup: %w", err)
	}
	var foremanSessionID string
	for _, s := range sessions {
		if s.Kind == domain.AgentSessionKindForeman {
			foremanSessionID = s.ID
			break
		}
	}
	if foremanSessionID == "" {
		// No existing foreman row: allocate a new ID. The harness will receive this ID.
		foremanSessionID = domain.NewID()
	}

	// Build system prompt with plan and FAQ
	systemPrompt := f.buildSystemPrompt(ctx, plan)

	userPrompt := "You are the Foreman. Answer questions from sub-agents based on the plan and FAQ context."
	if followUpContext != "" {
		userPrompt += "\n\nThe operator has requested a follow-up with this context:\n" + followUpContext
	}

	// Start harness with the persisted session ID (reused or newly allocated above).
	opts := adapter.SessionOpts{
		SessionID:    foremanSessionID,
		Mode:         adapter.SessionModeForeman,
		WorkspaceID:  "", // Foreman doesn't need workspace
		SystemPrompt: systemPrompt,
		UserPrompt:   userPrompt,
	}

	session, err := f.harness.StartSession(ctx, opts)
	if err != nil {
		f.mu.Unlock()
		return fmt.Errorf("start foreman session: %w", err)
	}

	f.session = session
	f.sessionID = foremanSessionID

	// Persist the foreman agent session row if it doesn't already exist.
	// WorkspaceID requires loading the work item.
	workItem, err := f.workItemSvc.Get(ctx, f.workItemID)
	if err != nil {
		slog.Warn("failed to load work item for foreman session WorkspaceID", "error", err, "work_item_id", f.workItemID)
	}
	workspaceID := ""
	if workItem.ID != "" {
		workspaceID = workItem.WorkspaceID
	}

	// Determine whether the row already exists by checking the sessions list.
	rowExists := false
	for _, s := range sessions {
		if s.ID == foremanSessionID {
			rowExists = true
			break
		}
	}

	if !rowExists {
		// No existing foreman row — create one in pending status.
		now := time.Now()
		foremanSession := domain.AgentSession{
			ID:          foremanSessionID,
			WorkItemID:  f.workItemID,
			WorkspaceID: workspaceID,
			Kind:        domain.AgentSessionKindForeman,
			PlanID:      planID,
			HarnessName: f.harness.Name(),
			Status:      domain.AgentSessionPending,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := f.agentSessionSvc.Create(ctx, foremanSession); err != nil {
			f.mu.Unlock()
			return fmt.Errorf("create foreman session row: %w", err)
		}
	}

	// Transition the row to running. If the row was already running (restart case),
	// this returns an error which is acceptable — the harness session is live.
	if err := f.agentSessionSvc.Start(ctx, foremanSessionID); err != nil {
		slog.Warn("foreman session row transition to running", "error", err, "session_id", foremanSessionID)
	}

	// Start the question processing loop.
	f.wg.Add(1)
	go f.run(ctx, f.stopCh)
	// Watch for harness exit and transition the persisted row to failed if the process
	// dies unexpectedly. This prevents the row from being stuck in 'running' state.
	go f.watchHarnessExit(ctx)
	f.mu.Unlock()

	// Publish EventForemanStarted (after unlock; event publishing is thread-safe)
	f.publishEvent(ctx, domain.EventForemanStarted, domain.ForemanEventPayload{
		WorkItemID: f.workItemID,
		PlanID:     planID,
		SessionID:  f.sessionID,
	})

	return nil
}

// watchHarnessExit monitors the harness session and transitions the persisted row
// to failed if the process exits unexpectedly (not via Stop()).
func (f *Foreman) watchHarnessExit(ctx context.Context) {
	// Wait for the harness session to exit.
	// Guard against nil session (already stopped).
	f.mu.Lock()
	session := f.session
	if session == nil {
		f.mu.Unlock()
		return
	}
	sessionID := f.sessionID
	f.mu.Unlock()

	<-session.Done()

	// The harness exited. If sessionID is still non-empty, Stop() has not yet run
	// (or is running concurrently). Stop() transitions the row itself; if it clears
	// sessionID, we skip the failed transition.
	f.mu.Lock()
	// Re-check: if Stop() already handled the transition, sessionID may be stale or
	// the session field may have been cleared. We rely on f.session being non-nil
	// to indicate this watcher won the race.
	if f.session == nil {
		// Stop() ran after our initial nil check but before Done() fired.
		// It already transitioned the row; nothing to do.
		f.mu.Unlock()
		return
	}
	f.mu.Unlock()

	// Harness exited unexpectedly; transition the persisted row to failed.
	if err := f.agentSessionSvc.Fail(context.WithoutCancel(ctx), sessionID, nil); err != nil {
		slog.Warn("failed to transition foreman session to failed after harness exit",
			"error", err, "session_id", sessionID)
	}
}

// Ask sends a question to the foreman and returns a channel for the answer.
func (f *Foreman) Ask(ctx context.Context, q domain.Question) <-chan string {
	answerCh := make(chan string, 1)

	pq := pendingQuestion{
		question:    q,
		answerCh:    answerCh,
		submittedAt: time.Now(),
	}

	select {
	case f.questionCh <- pq:
	default:
		// Queue is full, try async
		go func() {
			select {
			case f.questionCh <- pq:
			case <-ctx.Done():
			}
		}()
	}

	return answerCh
}

// run processes questions from the queue.
func (f *Foreman) run(ctx context.Context, stopCh <-chan struct{}) {
	defer f.wg.Done()

	// The session and stop channel were captured by Start before this goroutine
	// was launched. Avoid reading mutable Foreman fields here; Stop may clear or
	// replace them during restart.

	for {
		// Non-blocking priority check: drain questionFront before blocking.
		var pq pendingQuestion
		var ok bool
		select {
		case pq, ok = <-f.questionFront:
		default:
			// Block on both channels; Go select is pseudo-random when both ready,
			// so we checked questionFront non-blocking first to give it priority.
			select {
			case pq, ok = <-f.questionFront:
			case pq, ok = <-f.questionCh:
			case <-ctx.Done():
				return
			case <-stopCh:
				return
			}
		}

		if !ok {
			return
		}

		// Process the question.
		if err := f.answerOne(ctx, pq); err != nil {
			slog.Error("failed to answer question", "error", err, "question_id", pq.question.ID)
			// Re-queue on failure
			f.requeueQuestion(ctx, pq)
			// Restart session
			if restartErr := f.restartSession(ctx); restartErr != nil {
				slog.Error("failed to restart foreman session", "error", restartErr)
			}
		}
	}
}

// answerOne processes a single question.
func (f *Foreman) answerOne(ctx context.Context, pq pendingQuestion) error {
	f.mu.Lock()
	session := f.session
	f.mu.Unlock()

	if session == nil {
		return errors.New("foreman session not started")
	}

	// Get session to retrieve repository name.
	agentSession, err := f.agentSessionSvc.Get(ctx, pq.question.AgentSessionID)
	if err != nil {
		return fmt.Errorf("get agent session: %w", err)
	}

	// Send question to foreman.
	questionText := fmt.Sprintf("Question from %s: %s\nContext: %s",
		agentSession.RepositoryName, pq.question.Content, pq.question.Context)
	f.sessionMu.Lock()
	if err := session.SendMessage(ctx, questionText); err != nil {
		f.sessionMu.Unlock()

		return fmt.Errorf("send message: %w", err)
	}
	// Wait for the foreman_proposed event.
	answer, uncertain, err := f.waitForAnswer(ctx, session)
	f.sessionMu.Unlock()
	if err != nil {
		return err
	}

	if uncertain {
		// Escalate to human: persist proposed answer for TUI pre-fill, keep answerCh open.
		if err := f.questionSvc.EscalateWithProposal(ctx, pq.question.ID, answer); err != nil {
			return fmt.Errorf("escalate question: %w", err)
		}
		// Register the answer channel so ResolveEscalated can unblock the sub-agent later.
		// Also transition the session to waiting_for_answer so the TUI surfaces the overlay.
		if err := f.agentSessionSvc.WaitForAnswer(ctx, pq.question.AgentSessionID); err != nil {
			// Log but do not abort: the escalation is persisted; the TUI will still show the
			// question via DB poll even if the state transition fails.
			slog.Warn("failed to transition agent session to waiting_for_answer",
				"error", err, "agent_session_id", pq.question.AgentSessionID)
		}
		f.escalatedMu.Lock()
		f.escalatedChs[pq.question.ID] = escalatedEntry{
			answerCh:       pq.answerCh,
			agentSessionID: pq.question.AgentSessionID,
		}
		f.escalatedMu.Unlock()
	} else {
		// Auto-answer: persist, append to FAQ, unblock the sub-agent.
		if err := f.questionSvc.Answer(ctx, pq.question.ID, answer, "foreman"); err != nil {
			return fmt.Errorf("answer question: %w", err)
		}

		faqEntry := domain.FAQEntry{
			ID:             domain.NewID(),
			PlanID:         f.planID,
			AgentSessionID: pq.question.AgentSessionID,
			RepoName:       agentSession.RepositoryName,
			Question:       pq.question.Content,
			Answer:         answer,
			AnsweredBy:     "foreman",
			CreatedAt:      time.Now(),
		}
		if err := f.planSvc.AppendFAQ(ctx, faqEntry); err != nil {
			return fmt.Errorf("append faq: %w", err)
		}

		// Unblock the waiting sub-agent.
		pq.answerCh <- answer
	}

	return nil
}

// waitForAnswer waits for the foreman's answer from the session's Events channel.
// It looks for foreman_proposed events and extracts the answer text and confidence marker.
// If no response is received within the timeout, it returns an error.
func (f *Foreman) waitForAnswer(ctx context.Context, session adapter.AgentSession) (string, bool, error) {
	// Parse timeout from config (string like "30s", "1m", or "0" for indefinite).
	// Default is 0 (no timeout) — the foreman waits indefinitely for an answer.
	var timeout time.Duration
	if f.cfg.Foreman.QuestionTimeout != "" && f.cfg.Foreman.QuestionTimeout != "0" {
		if d, err := time.ParseDuration(f.cfg.Foreman.QuestionTimeout); err == nil {
			timeout = d
		}
	}

	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// Read events from the session's Events channel
	eventsCh := session.Events()
	for {
		select {
		case <-ctx.Done():
			return "", true, fmt.Errorf("timeout waiting for foreman response: %w", ctx.Err())
		case evt, ok := <-eventsCh:
			if !ok {
				// Channel closed, session ended
				return "", true, errors.New("foreman session ended without response")
			}

			// Check for foreman_proposed event
			if evt.Type == "foreman_proposed" {
				answer := evt.Payload
				uncertain := false
				if evt.Metadata != nil {
					if u, ok := evt.Metadata["uncertain"].(bool); ok {
						uncertain = u
					}
				}

				return answer, uncertain, nil
			}

			// Ignore other event types (progress, done, etc.)
			// but continue waiting for foreman_proposed
		}
	}
}

// requeueQuestion puts a question back on the priority queue.
func (f *Foreman) requeueQuestion(ctx context.Context, pq pendingQuestion) {
	go func() {
		select {
		case f.questionFront <- pq:
		case <-ctx.Done():
		}
	}()
}

// restartSession restarts the foreman session with current plan from DB.
func (f *Foreman) restartSession(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Get current plan with FAQ
	plan, err := f.planSvc.GetPlan(ctx, f.planID)
	if err != nil {
		return fmt.Errorf("get plan: %w", err)
	}

	// Abort current session if exists
	if f.session != nil {
		if abortErr := f.session.Abort(ctx); abortErr != nil {
			slog.Warn("failed to abort foreman session before restart", "error", abortErr)
		}
	}

	// Build system prompt with updated plan
	systemPrompt := f.buildSystemPrompt(ctx, plan)

	// Restart with the same persisted session ID so the sidebar and log path are unchanged.
	// f.sessionID is set by Start() and is stable across restarts.
	sessionID := f.sessionID
	if sessionID == "" {
		// sessionID should always be set if the foreman was started via Start().
		// If somehow restartSession is called without a prior Start(), fall back to new ID.
		sessionID = domain.NewID()
	}

	// Start new session with the same session ID
	opts := adapter.SessionOpts{
		SessionID:    sessionID,
		Mode:         adapter.SessionModeForeman,
		WorkspaceID:  "",
		SystemPrompt: systemPrompt,
		UserPrompt:   "You are the Foreman. Your session was restarted. Continue answering questions.",
	}

	session, err := f.harness.StartSession(ctx, opts)
	if err != nil {
		return fmt.Errorf("start foreman session: %w", err)
	}

	f.session = session

	return nil
}

// buildSystemPrompt builds the system prompt for the foreman session.
func (f *Foreman) buildSystemPrompt(ctx context.Context, plan domain.Plan) string {
	var b strings.Builder

	b.WriteString("You are the Foreman. You are the sole arbiter between sub-agent questions and the human operator.\n\n")
	b.WriteString("Sub-agents are executing the plan in their respective repositories. When they cannot resolve\n")
	b.WriteString("a question from the codebase or plan alone, they ask you. Your job is to answer them from the\n")
	b.WriteString("plan and accumulated FAQ so they can keep working — without interrupting the human unless you\n")
	b.WriteString("have no choice.\n\n")

	b.WriteString("## Your authority\n\n")
	b.WriteString("You hold the complete cross-repo plan (goals, constraints, acceptance criteria, per-repo\n")
	b.WriteString("work). Treat it as the ground truth for intent. If the answer is clearly derivable from the\n")
	b.WriteString("plan, derive it. If the same question was already answered in the FAQ, use that answer verbatim.\n\n")

	b.WriteString("## Current Plan\n\n")

	subPlans, err := f.planSvc.ListSubPlansByPlanID(ctx, plan.ID)
	if err != nil {
		slog.Warn("failed to get sub-plans for system prompt", "error", err)
		// Continue without sub-plans — the orchestrator plan and FAQ are still useful.
	} else {
		b.WriteString(domain.ComposePlanDocument(plan, subPlans))
		b.WriteString("\n\n")
	}

	if len(plan.FAQ) > 0 {
		b.WriteString("## FAQ (previously answered questions — re-use these answers exactly)\n\n")
		for _, entry := range plan.FAQ {
			fmt.Fprintf(&b, "Q: %s\nA: %s (answered by %s)\n\n",
				entry.Question, entry.Answer, entry.AnsweredBy)
		}
	}

	b.WriteString("## Instructions\n\n")
	b.WriteString("Answer the question concisely and precisely. Use only information from the plan and FAQ above.\n\n")
	b.WriteString("**When to answer with CONFIDENCE: high**\n")
	b.WriteString("Use this ONLY when the answer is explicitly stated in the plan or is a verbatim FAQ match.\n")
	b.WriteString("Do not use it for inferences, interpretations, or anything not directly written in the plan.\n\n")
	b.WriteString("**When to answer with CONFIDENCE: uncertain**\n")
	b.WriteString("Use this when the answer requires interpretation, the plan is ambiguous, the question touches\n")
	b.WriteString("something not covered by the plan, or you are not fully certain. In this case the human\n")
	b.WriteString("operator will review your proposed answer before it reaches the sub-agent.\n\n")
	b.WriteString("Always end your response with exactly one of:\n")
	b.WriteString("CONFIDENCE: high\n")
	b.WriteString("CONFIDENCE: uncertain\n\n")
	b.WriteString("Do not fabricate facts about the codebase. Do not guess at implementation details\n")
	b.WriteString("not described in the plan. If in doubt, use CONFIDENCE: uncertain.\n")

	return b.String()
}

// SessionID returns the ID of the currently running foreman session.
// Returns "" if the Foreman has not been started or has been stopped.
func (f *Foreman) SessionID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Prefer the stored session ID (set by Start(), stable across restarts).
	if f.sessionID != "" {
		return f.sessionID
	}
	if f.session == nil {
		return ""
	}
	return f.session.ID()
}

// IsRunning reports whether the Foreman has an active session.
// Safe for concurrent use.
func (f *Foreman) IsRunning() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.session != nil
}

// LastSessionID returns the session ID of the most recently stopped foreman
// session. This allows the TUI to display the foreman's session log even after
// the foreman has been stopped. Returns "" if no session has ever run.
func (f *Foreman) LastSessionID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastSessionID
}

// LastPlanID returns the plan ID associated with the most recently stopped
// foreman session.
func (f *Foreman) LastPlanID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastPlanID
}

// ErrQuestionNotEscalated is returned by ResolveEscalated and SendUserMessage
// when no in-flight answer channel exists for the given question ID.
// This happens if the Foreman was restarted after escalation, or if the question
// was not escalated through the Foreman at all.
var ErrQuestionNotEscalated = errors.New("question not in escalated channels")

// ResolveEscalated delivers a human-approved answer for a previously escalated question.
// Called by the TUI after the human iterates with the Foreman and presses [A]pprove.
// Records the answer in the DB and unblocks the waiting sub-agent.
func (f *Foreman) ResolveEscalated(ctx context.Context, questionID, answer string) error {
	f.escalatedMu.Lock()
	entry, ok := f.escalatedChs[questionID]
	if ok {
		delete(f.escalatedChs, questionID)
	}
	f.escalatedMu.Unlock()

	if !ok {
		return fmt.Errorf("%w: %s", ErrQuestionNotEscalated, questionID)
	}

	// Persist the human-approved answer.
	if err := f.questionSvc.Answer(ctx, questionID, answer, "human"); err != nil {
		return fmt.Errorf("answer escalated question: %w", err)
	}

	// Append to FAQ so the foreman can reuse human-answered questions.
	q, err := f.questionSvc.Get(ctx, questionID)
	if err != nil {
		slog.Warn("failed to fetch question for FAQ append", "error", err, "question_id", questionID)
	} else {
		agentSession, err := f.agentSessionSvc.Get(ctx, entry.agentSessionID)
		if err != nil {
			slog.Warn("failed to fetch agent session for FAQ append", "error", err, "agent_session_id", entry.agentSessionID)
		} else {
			faqEntry := domain.FAQEntry{
				ID:             domain.NewID(),
				PlanID:         f.planID,
				AgentSessionID: entry.agentSessionID,
				RepoName:       agentSession.RepositoryName,
				Question:       q.Content,
				Answer:         answer,
				AnsweredBy:     "human",
				CreatedAt:      time.Now(),
			}
			if err := f.planSvc.AppendFAQ(ctx, faqEntry); err != nil {
				slog.Warn("failed to append human-answered FAQ", "error", err, "question_id", questionID)
			}
		}
	}

	// Transition the session back to running before unblocking the sub-agent so the
	// TUI clears the action-required state before the agent receives the answer.
	if err := f.agentSessionSvc.ResumeFromAnswer(ctx, entry.agentSessionID); err != nil {
		slog.Warn("failed to resume agent session from waiting_for_answer",
			"error", err, "agent_session_id", entry.agentSessionID)
	}

	// Unblock the sub-agent that was waiting.
	entry.answerCh <- answer

	return nil
}

// Idempotent: safe to call multiple times (e.g. re-implementation cycles where
// ImplementationCompleteMsg fires StopForemanCmd each time).
func (f *Foreman) Stop(ctx context.Context) error {
	// Capture session and stopCh under the lock, then nil f.session atomically.
	// A concurrent Stop() will see f.session == nil and return early,
	// preventing double-close of stopCh (which would panic).
	f.mu.Lock()
	if f.session == nil {
		f.mu.Unlock()

		return nil
	}
	session := f.session
	stopCh := f.stopCh
	lastSessionID := session.ID()
	lastPlanID := f.planID
	f.lastSessionID = lastSessionID
	f.lastPlanID = lastPlanID
	f.session = nil
	f.sessionID = "" // Prevent watchHarnessExit from also transitioning

	close(stopCh)
	f.wg.Wait()
	f.mu.Unlock()
	if err := session.Abort(ctx); err != nil {
		slog.Warn("abort foreman session on stop", "error", err, "session_id", lastSessionID)
	}

	// Transition the persisted row to the appropriate terminal state.
	// Interrupted if context was cancelled (e.g., app shutdown, pipeline interrupt);
	// completed otherwise (normal stop after implementation finishes).
	if ctx.Err() != nil {
		if err := f.agentSessionSvc.Interrupt(ctx, lastSessionID); err != nil {
			slog.Warn("failed to transition foreman session to interrupted", "error", err, "session_id", lastSessionID)
		}
	} else {
		if err := f.agentSessionSvc.Complete(ctx, lastSessionID); err != nil {
			slog.Warn("failed to transition foreman session to completed", "error", err, "session_id", lastSessionID)
		}
	}

	// Drain and close any open entries in escalatedChs.
	// This prevents goroutine leaks for questions that were escalated to humans
	// but not yet resolved when the foreman is stopped.
	f.escalatedMu.Lock()
	for questionID, entry := range f.escalatedChs {
		select {
		case <-entry.answerCh:
		default:
			// Channel not received yet; close it to unblock any waiting goroutines.
			close(entry.answerCh)
		}
		delete(f.escalatedChs, questionID)
	}
	f.escalatedMu.Unlock()

	// Publish EventForemanStopped
	f.publishEvent(ctx, domain.EventForemanStopped, domain.ForemanEventPayload{
		WorkItemID:    f.workItemID,
		LastPlanID:    lastPlanID,
		LastSessionID: lastSessionID,
	})

	return nil
}

// SendUserMessage sends human follow-up text to the running Foreman session,
// waits for an updated proposed answer, persists it with UpdateProposal, and
// returns the new proposal so the TUI can refresh the question UI.
// The answer channel remains registered — the human must still call
// ResolveEscalated (or SkipQuestion) to actually unblock the sub-agent.
func (f *Foreman) SendUserMessage(ctx context.Context, questionID, text string) (newProposal string, uncertain bool, err error) {
	// Verify the question is still tracked before touching the session.
	f.escalatedMu.Lock()
	_, ok := f.escalatedChs[questionID]
	f.escalatedMu.Unlock()
	if !ok {
		return "", false, fmt.Errorf("%w: %s", ErrQuestionNotEscalated, questionID)
	}

	f.mu.Lock()
	session := f.session
	f.mu.Unlock()

	if session == nil {
		return "", false, errors.New("foreman session not started")
	}

	f.sessionMu.Lock()
	if err := session.SendMessage(ctx, text); err != nil {
		f.sessionMu.Unlock()

		return "", false, fmt.Errorf("send user message to foreman: %w", err)
	}
	newProposal, uncertain, err = f.waitForAnswer(ctx, session)
	f.sessionMu.Unlock()
	if err != nil {
		return "", false, err
	}

	if updateErr := f.questionSvc.UpdateProposal(ctx, questionID, newProposal); updateErr != nil {
		// Log but don't fail: the proposal is in-memory and the TUI will display it.
		slog.Warn("failed to persist updated foreman proposal",
			"question_id", questionID, "error", updateErr)
	}

	return newProposal, uncertain, nil
}

// publishEvent constructs a SystemEvent and publishes it to the event bus.
func (f *Foreman) publishEvent(ctx context.Context, eventType domain.EventType, payload domain.ForemanEventPayload) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal foreman event: %w", err)
	}
	return f.eventBus.Publish(ctx, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(eventType),
		WorkspaceID: "",
		Payload:     string(data),
		CreatedAt:   time.Now(),
	})
}

// RefineAnswer sends human follow-up text to get a revised answer proposal.
// The escalation remains open; human must still call ResolveEscalated to finalize.
// This is the ForemanHandler interface implementation; it delegates to SendUserMessage.
func (f *Foreman) RefineAnswer(ctx context.Context, questionID, text string) (newProposal string, uncertain bool, err error) {
	return f.SendUserMessage(ctx, questionID, text)
}
