package orchestrator

import (
	"context"
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

// pendingQuestion represents a question waiting to be answered.
type pendingQuestion struct {
	question    domain.Question
	answerCh    chan<- string
	submittedAt time.Time
}

// Foreman manages a persistent oh-my-pi session for answering sub-agent questions.
type Foreman struct {
	cfg         *config.Config
	harness     adapter.AgentHarness
	planSvc     *service.PlanService
	questionSvc *service.QuestionService
	sessionSvc  *service.TaskService
	eventBus    *event.Bus

	mu            sync.Mutex
	sessionMu     sync.Mutex           // serializes SendMessage+waitForAnswer; prevents concurrent Events() readers
	session       adapter.AgentSession // Current persistent foreman session
	planID        string
	questionCh    chan pendingQuestion
	questionFront chan pendingQuestion // Priority channel for re-queued questions
	stopCh        chan struct{}
	wg            sync.WaitGroup

	// escalatedChs stores answer channels for questions escalated to humans.
	// Keyed by question ID. The TUI calls ResolveEscalated to deliver the answer.
	escalatedMu  sync.Mutex
	escalatedChs map[string]chan<- string
}

// NewForeman creates a new Foreman instance.
func NewForeman(
	cfg *config.Config,
	harness adapter.AgentHarness,
	planSvc *service.PlanService,
	questionSvc *service.QuestionService,
	sessionSvc *service.TaskService,
	eventBus *event.Bus,
) *Foreman {
	return &Foreman{
		cfg:           cfg,
		harness:       harness,
		planSvc:       planSvc,
		questionSvc:   questionSvc,
		sessionSvc:    sessionSvc,
		eventBus:      eventBus,
		questionCh:    make(chan pendingQuestion, 100),
		questionFront: make(chan pendingQuestion, 100),
		stopCh:        make(chan struct{}),
		escalatedChs:  make(map[string]chan<- string),
	}
}

// Start begins the foreman session for a plan.
func (f *Foreman) Start(ctx context.Context, planID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.session != nil {
		return nil // Already running
	}

	// Re-arm stopCh for this start cycle.
	// Stop() closes it; Start() must allocate a fresh one so the new run() goroutine
	// is not already-exited on the first select.
	f.stopCh = make(chan struct{})

	f.planID = planID

	// Get plan with FAQ
	plan, err := f.planSvc.GetPlan(ctx, planID)
	if err != nil {
		return fmt.Errorf("get plan: %w", err)
	}

	// Build system prompt with plan and FAQ
	systemPrompt := f.buildSystemPrompt(ctx, plan)

	// Start foreman session
	opts := adapter.SessionOpts{
		SessionID:    domain.NewID(),
		Mode:         adapter.SessionModeForeman,
		WorkspaceID:  "", // Foreman doesn't need workspace
		SystemPrompt: systemPrompt,
		UserPrompt:   "You are the Foreman. Answer questions from sub-agents based on the plan and FAQ context.",
	}

	session, err := f.harness.StartSession(ctx, opts)
	if err != nil {
		return fmt.Errorf("start foreman session: %w", err)
	}

	f.session = session

	// Start the question processing loop
	go f.run(ctx)

	return nil
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
func (f *Foreman) run(ctx context.Context) {
	f.wg.Add(1)
	defer f.wg.Done()

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
			case <-f.stopCh:
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
	agentSession, err := f.sessionSvc.Get(ctx, pq.question.AgentSessionID)
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
		f.escalatedMu.Lock()
		f.escalatedChs[pq.question.ID] = pq.answerCh
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

	// Start new session
	opts := adapter.SessionOpts{
		SessionID:    domain.NewID(),
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
	} else {
		for _, sp := range subPlans {
			fmt.Fprintf(&b, "### Repository: %s\n\n%s\n\n", sp.RepositoryName, sp.Content)
		}
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
	if f.session == nil {
		return ""
	}
	return f.session.ID()
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
	ch, ok := f.escalatedChs[questionID]
	if ok {
		delete(f.escalatedChs, questionID)
	}
	f.escalatedMu.Unlock()

	if !ok {
		return fmt.Errorf("%w: %s", ErrQuestionNotEscalated, questionID)
	}

	// Persist the human-approved answer; also appends to FAQ (AnsweredBy="human").
	if err := f.questionSvc.Answer(ctx, questionID, answer, "human"); err != nil {
		return fmt.Errorf("answer escalated question: %w", err)
	}

	// Unblock the sub-agent that was waiting.
	ch <- answer

	return nil
}

// Stop stops the foreman session.
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
	f.session = nil
	f.mu.Unlock()

	close(stopCh)
	f.wg.Wait()

	if err := session.Abort(ctx); err != nil {
		return fmt.Errorf("abort session: %w", err)
	}

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
