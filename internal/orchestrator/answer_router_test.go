package orchestrator

import (
	"context"
	"testing"

	"github.com/beeemT/substrate/internal/domain"
)

func TestAnswerImplementationHumanDirectedQuestionSendsLiveAnswer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	sessionSvc, sessionRepo := newMockSessionSvcWrapper()
	sessionRepo.sessions["impl-session"] = domain.AgentSession{
		ID:             "impl-session",
		WorkItemID:     "wi-1",
		WorkspaceID:    "ws-1",
		Kind:           domain.AgentSessionKindImplementation,
		HarnessName:    "mock",
		Status:         domain.AgentSessionWaitingForAnswer,
		RepositoryName: "repo1",
		WorktreePath:   "/tmp/worktree",
	}
	questionSvc, _ := newServiceAndRepoForQuestions()
	question := domain.Question{
		ID:             "q-human",
		AgentSessionID: "impl-session",
		Stage:          domain.AgentSessionKindImplementation,
		Source:         domain.QuestionSourceAskUser,
		Content:        "Which branch should I use?",
		Status:         domain.QuestionPending,
	}
	if err := questionSvc.Create(ctx, question); err != nil {
		t.Fatalf("Create question: %v", err)
	}

	registry := NewSessionRegistry()
	liveSession := &registryMockSession{id: "impl-session"}
	registry.Register("impl-session", liveSession)
	router := NewAnswerRouter(registry, questionSvc, sessionSvc, &mockPublisher{})

	if err := router.Answer(ctx, "q-human", "Use main.", "human"); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if len(liveSession.answers) != 1 || liveSession.answers[0] != "Use main." {
		t.Fatalf("answers = %#v, want one live answer", liveSession.answers)
	}
	updatedQuestion, err := questionSvc.Get(ctx, "q-human")
	if err != nil {
		t.Fatalf("Get question: %v", err)
	}
	if updatedQuestion.Status != domain.QuestionAnswered || updatedQuestion.Answer != "Use main." {
		t.Fatalf("question = %#v, want answered with text", updatedQuestion)
	}
	updatedSession, err := sessionSvc.Get(ctx, "impl-session")
	if err != nil {
		t.Fatalf("Get session: %v", err)
	}
	if updatedSession.Status != domain.AgentSessionRunning {
		t.Fatalf("session status = %s, want %s", updatedSession.Status, domain.AgentSessionRunning)
	}
}
