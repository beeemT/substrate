package service

import (
	"context"
	"testing"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

func TestQuestionService_Create(t *testing.T) {
	ctx := context.Background()
	repo := NewMockQuestionRepository()
	svc := NewQuestionService(repository.NoopTransacter{Res: repository.Resources{Questions: repo}})

	t.Run("creates question with pending status", func(t *testing.T) {
		q := domain.Question{
			ID:             "q-1",
			AgentSessionID: "session-1",
			Content:        "What should I do?",
			Context:        "Some context",
		}
		if err := svc.Create(ctx, q); err != nil {
			t.Fatalf("Create failed: %v", err)
		}

		got, err := svc.Get(ctx, "q-1")
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if got.Status != domain.QuestionPending {
			t.Errorf("Status = %q, want %q", got.Status, domain.QuestionPending)
		}
	})

	t.Run("rejects non-pending initial status", func(t *testing.T) {
		q := domain.Question{
			ID:             "q-2",
			AgentSessionID: "session-1",
			Content:        "What?",
			Status:         domain.QuestionAnswered,
		}
		err := svc.Create(ctx, q)
		if err == nil {
			t.Fatal("expected error for non-pending initial status")
		}
		_, ok := err.(ErrInvalidInput)
		if !ok {
			t.Errorf("error type = %T, want ErrInvalidInput", err)
		}
	})
}

func TestQuestionService_ValidTransitions(t *testing.T) {
	ctx := context.Background()

	validTransitions := []struct {
		from domain.QuestionStatus
		to   domain.QuestionStatus
		name string
	}{
		{domain.QuestionPending, domain.QuestionAnswered, "pending -> answered"},
		{domain.QuestionPending, domain.QuestionEscalated, "pending -> escalated"},
		{domain.QuestionEscalated, domain.QuestionAnswered, "escalated -> answered (human approval)"},
	}

	for _, tc := range validTransitions {
		t.Run(tc.name, func(t *testing.T) {
			repo := NewMockQuestionRepository()
			svc := NewQuestionService(repository.NoopTransacter{Res: repository.Resources{Questions: repo}})

			q := domain.Question{
				ID:             "q-test",
				AgentSessionID: "session-1",
				Content:        "Test",
				Status:         tc.from,
			}
			repo.questions["q-test"] = q

			if err := svc.Transition(ctx, "q-test", tc.to); err != nil {
				t.Fatalf("Transition from %s to %s failed: %v", tc.from, tc.to, err)
			}

			got, err := svc.Get(ctx, "q-test")
			if err != nil {
				t.Fatalf("Get failed: %v", err)
			}
			if got.Status != tc.to {
				t.Errorf("Status = %q, want %q", got.Status, tc.to)
			}
		})
	}
}

func TestQuestionService_InvalidTransitions(t *testing.T) {
	ctx := context.Background()

	invalidTransitions := []struct {
		from domain.QuestionStatus
		to   domain.QuestionStatus
		name string
	}{
		{domain.QuestionPending, domain.QuestionPending, "pending -> pending"},
		{domain.QuestionAnswered, domain.QuestionPending, "answered -> pending"},
		{domain.QuestionAnswered, domain.QuestionEscalated, "answered -> escalated"},
		{domain.QuestionEscalated, domain.QuestionPending, "escalated -> pending"},
	}

	for _, tc := range invalidTransitions {
		t.Run(tc.name, func(t *testing.T) {
			repo := NewMockQuestionRepository()
			svc := NewQuestionService(repository.NoopTransacter{Res: repository.Resources{Questions: repo}})

			q := domain.Question{
				ID:             "q-test",
				AgentSessionID: "session-1",
				Content:        "Test",
				Status:         tc.from,
			}
			repo.questions["q-test"] = q

			err := svc.Transition(ctx, "q-test", tc.to)
			if err == nil {
				t.Fatalf("expected error for transition from %s to %s", tc.from, tc.to)
			}
			if _, ok := err.(ErrInvalidTransition); !ok {
				t.Errorf("error type = %T, want ErrInvalidTransition", err)
			}
		})
	}
}

func TestQuestionService_Answer(t *testing.T) {
	ctx := context.Background()
	repo := NewMockQuestionRepository()
	svc := NewQuestionService(repository.NoopTransacter{Res: repository.Resources{Questions: repo}})

	q := domain.Question{
		ID:             "q-1",
		AgentSessionID: "session-1",
		Content:        "What should I do?",
		Status:         domain.QuestionPending,
	}
	repo.questions["q-1"] = q

	if err := svc.Answer(ctx, "q-1", "Do this", "foreman"); err != nil {
		t.Fatalf("Answer failed: %v", err)
	}

	got, _ := svc.Get(ctx, "q-1")
	if got.Status != domain.QuestionAnswered {
		t.Errorf("Status = %q, want %q", got.Status, domain.QuestionAnswered)
	}
	if got.Answer != "Do this" {
		t.Errorf("Answer = %q, want %q", got.Answer, "Do this")
	}
	if got.AnsweredBy != "foreman" {
		t.Errorf("AnsweredBy = %q, want %q", got.AnsweredBy, "foreman")
	}
	if got.AnsweredAt == nil {
		t.Error("AnsweredAt should be set")
	}
}

func TestQuestionService_Escalate(t *testing.T) {
	ctx := context.Background()
	repo := NewMockQuestionRepository()
	svc := NewQuestionService(repository.NoopTransacter{Res: repository.Resources{Questions: repo}})

	q := domain.Question{
		ID:             "q-1",
		AgentSessionID: "session-1",
		Content:        "What should I do?",
		Status:         domain.QuestionPending,
	}
	repo.questions["q-1"] = q

	if err := svc.Escalate(ctx, "q-1"); err != nil {
		t.Fatalf("Escalate failed: %v", err)
	}

	got, _ := svc.Get(ctx, "q-1")
	if got.Status != domain.QuestionEscalated {
		t.Errorf("Status = %q, want %q", got.Status, domain.QuestionEscalated)
	}
}

func TestQuestionService_UpdateContext(t *testing.T) {
	ctx := context.Background()
	repo := NewMockQuestionRepository()
	svc := NewQuestionService(repository.NoopTransacter{Res: repository.Resources{Questions: repo}})

	t.Run("updates context for pending question", func(t *testing.T) {
		q := domain.Question{
			ID:             "q-1",
			AgentSessionID: "session-1",
			Content:        "What?",
			Context:        "Old context",
			Status:         domain.QuestionPending,
		}
		repo.questions["q-1"] = q

		if err := svc.UpdateContext(ctx, "q-1", "New context"); err != nil {
			t.Fatalf("UpdateContext failed: %v", err)
		}

		got, _ := svc.Get(ctx, "q-1")
		if got.Context != "New context" {
			t.Errorf("Context = %q, want %q", got.Context, "New context")
		}
	})

	t.Run("rejects update for non-pending question", func(t *testing.T) {
		q := domain.Question{
			ID:             "q-2",
			AgentSessionID: "session-1",
			Content:        "What?",
			Status:         domain.QuestionAnswered,
		}
		repo.questions["q-2"] = q

		err := svc.UpdateContext(ctx, "q-2", "New context")
		if err == nil {
			t.Fatal("expected error for updating non-pending question")
		}
		_, ok := err.(ErrConstraintViolation)
		if !ok {
			t.Errorf("error type = %T, want ErrConstraintViolation", err)
		}
	})
}

func TestQuestionService_HasPendingQuestions(t *testing.T) {
	ctx := context.Background()

	t.Run("has pending", func(t *testing.T) {
		repo := NewMockQuestionRepository()
		svc := NewQuestionService(repository.NoopTransacter{Res: repository.Resources{Questions: repo}})

		repo.questions["q-1"] = domain.Question{ID: "q-1", AgentSessionID: "session-1", Status: domain.QuestionPending}
		repo.questions["q-2"] = domain.Question{ID: "q-2", AgentSessionID: "session-1", Status: domain.QuestionAnswered}
		repo.bySession["session-1"] = []string{"q-1", "q-2"}

		hasPending, err := svc.HasPendingQuestions(ctx, "session-1")
		if err != nil {
			t.Fatalf("HasPendingQuestions failed: %v", err)
		}
		if !hasPending {
			t.Error("expected true for pending questions")
		}
	})

	t.Run("no pending", func(t *testing.T) {
		repo := NewMockQuestionRepository()
		svc := NewQuestionService(repository.NoopTransacter{Res: repository.Resources{Questions: repo}})

		repo.questions["q-1"] = domain.Question{ID: "q-1", AgentSessionID: "session-2", Status: domain.QuestionAnswered}
		repo.bySession["session-2"] = []string{"q-1"}

		hasPending, err := svc.HasPendingQuestions(ctx, "session-2")
		if err != nil {
			t.Fatalf("HasPendingQuestions failed: %v", err)
		}
		if hasPending {
			t.Error("expected false for no pending questions")
		}
	})
}
