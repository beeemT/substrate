package orchestrator

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
)

// mockSessionSvc implements *service.AgentSessionService by wrapping a mock repo.
type mockSessionSvcWrapper struct {
	realSvc *service.AgentSessionService
}

func newMockSessionSvcWrapper() (*service.AgentSessionService, *mockSessionRepo) {
	sessionRepo := newMockSessionRepo()
	return service.NewAgentSessionService(
		repository.NoopTransacter{Res: repository.Resources{AgentSessions: sessionRepo}},
		&mockPublisher{},
	), sessionRepo
}

// TestRouteManual_RoutesQuestionToHuman verifies that routeManual persists the question
// and marks the session waiting for answer.
func TestRouteManual_RoutesQuestionToHuman(t *testing.T) {
	t.Parallel()

	sessionSvc, sessionRepo := newMockSessionSvcWrapper()
	sessionRepo.sessions["manual-session"] = domain.AgentSession{
		ID:             "manual-session",
		WorkItemID:     "wi-1",
		WorkspaceID:    "ws-1",
		Kind:           domain.AgentSessionKindManual,
		HarnessName:    "mock",
		Status:         domain.AgentSessionRunning,
		RepositoryName: "repo1",
		WorktreePath:   "/tmp/worktree",
	}

	questionSvc, _ := newServiceAndRepoForQuestions()
	registry := NewSessionRegistry()
	bus := &mockPublisher{}
	router := NewQuestionRouter(questionSvc, sessionSvc, registry, bus)

	evt := adapter.AgentEvent{
		Type:    "question",
		Payload: "Proceed with the change?",
		Metadata: map[string]any{
			"id":     "q-1",
			"source": string(adapter.AgentQuestionSourceAskForeman),
		},
	}

	err := router.Route(context.Background(), domain.AgentSessionKindManual, evt, "manual-session")
	if err != nil {
		t.Fatalf("Route returned error: %v", err)
	}

	// Verify question was persisted
	questions, err := questionSvc.ListBySessionID(context.Background(), "manual-session")
	if err != nil {
		t.Fatalf("ListBySessionID returned error: %v", err)
	}
	if len(questions) != 1 {
		t.Fatalf("questions len = %d, want 1", len(questions))
	}

	q := questions[0]
	if q.Stage != domain.AgentSessionKindManual {
		t.Errorf("question stage = %s, want %s", q.Stage, domain.AgentSessionKindManual)
	}
	if q.Source != domain.QuestionSourceAskForeman {
		t.Errorf("question source = %s, want %s", q.Source, domain.QuestionSourceAskForeman)
	}
}

// TestSessionWorkItemID_ErrorPropagation verifies that sessionWorkItemID returns
// an error when the session is not found, but the caller handles it gracefully.
func TestSessionWorkItemID_ErrorPropagation(t *testing.T) {
	t.Parallel()

	sessionSvc, _ := newMockSessionSvcWrapper() // No sessions registered
	questionSvc, _ := newServiceAndRepoForQuestions()
	registry := NewSessionRegistry()
	bus := &mockPublisher{}
	router := NewQuestionRouter(questionSvc, sessionSvc, registry, bus)

	workItemID, err := router.sessionWorkItemID(context.Background(), "non-existent-session")
	if err == nil {
		t.Fatal("sessionWorkItemID returned nil error for non-existent session")
	}
	var notFoundErr service.ErrNotFound
	if !errors.As(err, &notFoundErr) {
		t.Errorf("error = %v, want service.ErrNotFound", err)
	}
	if workItemID != "" {
		t.Errorf("workItemID = %q, want empty string", workItemID)
	}
}

// TestQuestionRouter_Route_UnsupportedStage verifies that unsupported stages return an error.
func TestQuestionRouter_Route_UnsupportedStage(t *testing.T) {
	t.Parallel()

	sessionSvc, _ := newMockSessionSvcWrapper()
	questionSvc, _ := newServiceAndRepoForQuestions()
	registry := NewSessionRegistry()
	bus := &mockPublisher{}
	router := NewQuestionRouter(questionSvc, sessionSvc, registry, bus)

	evt := adapter.AgentEvent{
		Type:    "question",
		Payload: "Continue?",
		Metadata: map[string]any{
			"id":     "q-3",
			"source": string(adapter.AgentQuestionSourceAskForeman),
		},
	}

	err := router.Route(context.Background(), domain.AgentSessionKind("unsupported"), evt, "session")
	if err == nil {
		t.Fatal("Route returned nil error for unsupported stage")
	}
}

// TestRouteManual_WaitForAnswer verifies that routeManual calls WaitForAnswer on the session service.
func TestRouteManual_WaitForAnswer(t *testing.T) {
	t.Parallel()

	sessionSvc, sessionRepo := newMockSessionSvcWrapper()
	sessionRepo.sessions["manual-session"] = domain.AgentSession{
		ID:             "manual-session",
		WorkItemID:     "wi-1",
		WorkspaceID:    "ws-1",
		Kind:           domain.AgentSessionKindManual,
		HarnessName:    "mock",
		Status:         domain.AgentSessionRunning,
		RepositoryName: "repo1",
		WorktreePath:   "/tmp/worktree",
	}

	questionSvc, _ := newServiceAndRepoForQuestions()
	registry := NewSessionRegistry()
	bus := &mockPublisher{}
	router := NewQuestionRouter(questionSvc, sessionSvc, registry, bus)

	evt := adapter.AgentEvent{
		Type:    "question",
		Payload: "Ready to proceed?",
		Metadata: map[string]any{
			"id":     "q-4",
			"source": string(adapter.AgentQuestionSourceAskForeman),
		},
	}

	err := router.Route(context.Background(), domain.AgentSessionKindManual, evt, "manual-session")
	if err != nil {
		t.Fatalf("Route returned error: %v", err)
	}

	// Verify session status was updated to waiting_for_answer
	session, err := sessionSvc.Get(context.Background(), "manual-session")
	if err != nil {
		t.Fatalf("Get session returned error: %v", err)
	}
	if session.Status != domain.AgentSessionWaitingForAnswer {
		t.Errorf("session status = %s, want %s", session.Status, domain.AgentSessionWaitingForAnswer)
	}
}

func TestRouteImplementationHumanDirectedQuestionWaitsForHuman(t *testing.T) {
	t.Parallel()

	sessionSvc, sessionRepo := newMockSessionSvcWrapper()
	sessionRepo.sessions["impl-session"] = domain.AgentSession{
		ID:             "impl-session",
		WorkItemID:     "wi-1",
		WorkspaceID:    "ws-1",
		Kind:           domain.AgentSessionKindImplementation,
		HarnessName:    "mock",
		Status:         domain.AgentSessionRunning,
		RepositoryName: "repo1",
		WorktreePath:   "/tmp/worktree",
	}

	questionSvc, _ := newServiceAndRepoForQuestions()
	router := NewQuestionRouter(questionSvc, sessionSvc, NewSessionRegistry(), &mockPublisher{})
	evt := adapter.AgentEvent{
		Type:    "question",
		Payload: "Which branch should I use?",
		Question: &adapter.AgentQuestion{
			Source:   adapter.AgentQuestionSourceAskUser,
			FreeText: "Which branch should I use?",
		},
	}

	if err := router.Route(context.Background(), domain.AgentSessionKindImplementation, evt, "impl-session"); err != nil {
		t.Fatalf("Route: %v", err)
	}
	session, err := sessionSvc.Get(context.Background(), "impl-session")
	if err != nil {
		t.Fatalf("Get session: %v", err)
	}
	if session.Status != domain.AgentSessionWaitingForAnswer {
		t.Fatalf("session status = %s, want %s", session.Status, domain.AgentSessionWaitingForAnswer)
	}
	questions, err := questionSvc.ListBySessionID(context.Background(), "impl-session")
	if err != nil {
		t.Fatalf("ListBySessionID: %v", err)
	}
	if len(questions) != 1 || questions[0].Source != domain.QuestionSourceAskUser {
		t.Fatalf("questions = %#v, want one ask_user question", questions)
	}
}

// newServiceAndRepoForQuestions creates a QuestionService with in-memory storage.
func newServiceAndRepoForQuestions() (*service.QuestionService, *inMemQuestionRepo) {
	repo := &inMemQuestionRepo{questions: make(map[string]domain.Question)}
	svc := service.NewQuestionService(
		repository.NoopTransacter{Res: repository.Resources{Questions: repo}},
		&mockPublisher{},
	)
	return svc, repo
}

type inMemQuestionRepo struct {
	mu        sync.Mutex
	questions map[string]domain.Question
}

func (r *inMemQuestionRepo) Get(_ context.Context, id string) (domain.Question, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if q, ok := r.questions[id]; ok {
		return q, nil
	}
	return domain.Question{}, repository.ErrNotFound
}

func (r *inMemQuestionRepo) Create(_ context.Context, q domain.Question) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.questions[q.ID] = q
	return nil
}

func (r *inMemQuestionRepo) Update(_ context.Context, q domain.Question) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.questions[q.ID] = q
	return nil
}

func (r *inMemQuestionRepo) ListBySessionID(_ context.Context, _ string) ([]domain.Question, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]domain.Question, 0, len(r.questions))
	for _, q := range r.questions {
		result = append(result, q)
	}
	return result, nil
}

func (r *inMemQuestionRepo) ListByWorkItemID(_ context.Context, _ string) ([]domain.Question, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]domain.Question, 0, len(r.questions))
	for _, q := range r.questions {
		result = append(result, q)
	}
	return result, nil
}

func (r *inMemQuestionRepo) ListPending(_ context.Context) ([]domain.Question, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]domain.Question, 0, len(r.questions))
	for _, q := range r.questions {
		result = append(result, q)
	}
	return result, nil
}

func (r *inMemQuestionRepo) Delete(_ context.Context, _ string) error {
	return nil
}

func (r *inMemQuestionRepo) UpdateProposedAnswer(_ context.Context, _, _ string) error {
	return nil
}

// TestRoute_ForemanKindRejected verifies that Route() rejects foreman kind questions.
func TestRoute_ForemanKindRejected(t *testing.T) {
	t.Parallel()

	sessionSvc, _ := newMockSessionSvcWrapper()
	questionSvc, _ := newServiceAndRepoForQuestions()
	registry := NewSessionRegistry()
	bus := &mockPublisher{}
	router := NewQuestionRouter(questionSvc, sessionSvc, registry, bus)

	evt := adapter.AgentEvent{
		Type:    "question",
		Payload: "Should I proceed?",
		Metadata: map[string]any{
			"id":     "q-foreman",
			"source": string(adapter.AgentQuestionSourceAskForeman),
		},
	}

	err := router.Route(context.Background(), domain.AgentSessionKindForeman, evt, "foreman-session")
	if err == nil {
		t.Fatal("Route returned nil error for foreman kind, want non-nil error")
	}
	if !strings.Contains(err.Error(), "foreman") {
		t.Errorf("error = %q, want message containing \"foreman\"", err.Error())
	}
}
