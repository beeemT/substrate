package service

import (
	"context"
	"testing"

	"github.com/beeemT/substrate/internal/domain"
)

func TestReviewService_CreateCycle(t *testing.T) {
	ctx := context.Background()
	repo := NewMockReviewRepository()
	svc := NewReviewService(repo)

	t.Run("creates cycle with reviewing status", func(t *testing.T) {
		cycle := domain.ReviewCycle{
			ID:              "cycle-1",
			AgentSessionID:  "session-1",
			CycleNumber:     1,
			ReviewerHarness: "omp",
		}
		if err := svc.CreateCycle(ctx, cycle); err != nil {
			t.Fatalf("CreateCycle failed: %v", err)
		}

		got, err := svc.GetCycle(ctx, "cycle-1")
		if err != nil {
			t.Fatalf("GetCycle failed: %v", err)
		}
		if got.Status != domain.ReviewCycleReviewing {
			t.Errorf("Status = %q, want %q", got.Status, domain.ReviewCycleReviewing)
		}
	})

	t.Run("rejects non-reviewing initial status", func(t *testing.T) {
		cycle := domain.ReviewCycle{
			ID:             "cycle-2",
			AgentSessionID: "session-1",
			Status:         domain.ReviewCyclePassed,
		}
		err := svc.CreateCycle(ctx, cycle)
		if err == nil {
			t.Fatal("expected error for non-reviewing initial status")
		}
		_, ok := err.(ErrInvalidInput)
		if !ok {
			t.Errorf("error type = %T, want ErrInvalidInput", err)
		}
	})
}

func TestReviewCycleService_ValidTransitions(t *testing.T) {
	ctx := context.Background()

	validTransitions := []struct {
		from domain.ReviewCycleStatus
		to   domain.ReviewCycleStatus
		name string
	}{
		{domain.ReviewCycleReviewing, domain.ReviewCycleCritiquesFound, "reviewing -> critiques_found"},
		{domain.ReviewCycleReviewing, domain.ReviewCyclePassed, "reviewing -> passed"},
		{domain.ReviewCycleReviewing, domain.ReviewCycleFailed, "reviewing -> failed"},
		{domain.ReviewCycleCritiquesFound, domain.ReviewCycleReimplementing, "critiques_found -> reimplementing"},
		{domain.ReviewCycleCritiquesFound, domain.ReviewCycleFailed, "critiques_found -> failed"},
		{domain.ReviewCycleReimplementing, domain.ReviewCycleReviewing, "reimplementing -> reviewing"},
		{domain.ReviewCycleReimplementing, domain.ReviewCycleFailed, "reimplementing -> failed"},
	}

	for _, tc := range validTransitions {
		t.Run(tc.name, func(t *testing.T) {
			repo := NewMockReviewRepository()
			svc := NewReviewService(repo)

			cycle := domain.ReviewCycle{
				ID:             "cycle-test",
				AgentSessionID: "session-1",
				Status:         tc.from,
			}
			repo.cycles["cycle-test"] = cycle

			if err := svc.TransitionCycle(ctx, "cycle-test", tc.to); err != nil {
				t.Fatalf("Transition from %s to %s failed: %v", tc.from, tc.to, err)
			}

			got, err := svc.GetCycle(ctx, "cycle-test")
			if err != nil {
				t.Fatalf("GetCycle failed: %v", err)
			}
			if got.Status != tc.to {
				t.Errorf("Status = %q, want %q", got.Status, tc.to)
			}
		})
	}
}

func TestReviewCycleService_InvalidTransitions(t *testing.T) {
	ctx := context.Background()

	invalidTransitions := []struct {
		from domain.ReviewCycleStatus
		to   domain.ReviewCycleStatus
		name string
	}{
		{domain.ReviewCycleReviewing, domain.ReviewCycleReimplementing, "reviewing -> reimplementing"},
		{domain.ReviewCycleCritiquesFound, domain.ReviewCycleReviewing, "critiques_found -> reviewing"},
		{domain.ReviewCycleCritiquesFound, domain.ReviewCyclePassed, "critiques_found -> passed"},
		{domain.ReviewCycleReimplementing, domain.ReviewCyclePassed, "reimplementing -> passed"},
		{domain.ReviewCycleReimplementing, domain.ReviewCycleCritiquesFound, "reimplementing -> critiques_found"},
		{domain.ReviewCyclePassed, domain.ReviewCycleReviewing, "passed -> reviewing"},
		{domain.ReviewCyclePassed, domain.ReviewCycleFailed, "passed -> failed"},
		{domain.ReviewCycleFailed, domain.ReviewCycleReviewing, "failed -> reviewing"},
		{domain.ReviewCycleFailed, domain.ReviewCyclePassed, "failed -> passed"},
	}

	for _, tc := range invalidTransitions {
		t.Run(tc.name, func(t *testing.T) {
			repo := NewMockReviewRepository()
			svc := NewReviewService(repo)

			cycle := domain.ReviewCycle{
				ID:             "cycle-test",
				AgentSessionID: "session-1",
				Status:         tc.from,
			}
			repo.cycles["cycle-test"] = cycle

			err := svc.TransitionCycle(ctx, "cycle-test", tc.to)
			if err == nil {
				t.Fatalf("expected error for transition from %s to %s", tc.from, tc.to)
			}
			if _, ok := err.(ErrInvalidTransition); !ok {
				t.Errorf("error type = %T, want ErrInvalidTransition", err)
			}
		})
	}
}

func TestCritiqueService_ValidTransitions(t *testing.T) {
	ctx := context.Background()

	validTransitions := []struct {
		from domain.CritiqueStatus
		to   domain.CritiqueStatus
		name string
	}{
		{domain.CritiqueOpen, domain.CritiqueResolved, "open -> resolved"},
	}

	for _, tc := range validTransitions {
		t.Run(tc.name, func(t *testing.T) {
			repo := NewMockReviewRepository()
			svc := NewReviewService(repo)

			critique := domain.Critique{
				ID:            "critique-test",
				ReviewCycleID: "cycle-1",
				FilePath:      "test.go",
				Description:   "Test critique",
				Severity:      domain.CritiqueMajor,
				Status:        tc.from,
			}
			repo.critiques["critique-test"] = critique

			if err := svc.TransitionCritique(ctx, "critique-test", tc.to); err != nil {
				t.Fatalf("Transition from %s to %s failed: %v", tc.from, tc.to, err)
			}

			got, err := svc.GetCritique(ctx, "critique-test")
			if err != nil {
				t.Fatalf("GetCritique failed: %v", err)
			}
			if got.Status != tc.to {
				t.Errorf("Status = %q, want %q", got.Status, tc.to)
			}
		})
	}
}

func TestCritiqueService_InvalidTransitions(t *testing.T) {
	ctx := context.Background()

	invalidTransitions := []struct {
		from domain.CritiqueStatus
		to   domain.CritiqueStatus
		name string
	}{
		{domain.CritiqueResolved, domain.CritiqueOpen, "resolved -> open"},
	}

	for _, tc := range invalidTransitions {
		t.Run(tc.name, func(t *testing.T) {
			repo := NewMockReviewRepository()
			svc := NewReviewService(repo)

			critique := domain.Critique{
				ID:            "critique-test",
				ReviewCycleID: "cycle-1",
				FilePath:      "test.go",
				Status:        tc.from,
			}
			repo.critiques["critique-test"] = critique

			err := svc.TransitionCritique(ctx, "critique-test", tc.to)
			if err == nil {
				t.Fatalf("expected error for transition from %s to %s", tc.from, tc.to)
			}
			if _, ok := err.(ErrInvalidTransition); !ok {
				t.Errorf("error type = %T, want ErrInvalidTransition", err)
			}
		})
	}
}

func TestReviewService_CountMajorCritiques(t *testing.T) {
	ctx := context.Background()
	repo := NewMockReviewRepository()
	svc := NewReviewService(repo)

	// Create critiques with different severities
	repo.critiques["c-1"] = domain.Critique{ID: "c-1", ReviewCycleID: "cycle-1", Severity: domain.CritiqueCritical, Status: domain.CritiqueOpen}
	repo.critiques["c-2"] = domain.Critique{ID: "c-2", ReviewCycleID: "cycle-1", Severity: domain.CritiqueMajor, Status: domain.CritiqueOpen}
	repo.critiques["c-3"] = domain.Critique{ID: "c-3", ReviewCycleID: "cycle-1", Severity: domain.CritiqueMinor, Status: domain.CritiqueOpen}
	repo.critiques["c-4"] = domain.Critique{ID: "c-4", ReviewCycleID: "cycle-1", Severity: domain.CritiqueMajor, Status: domain.CritiqueResolved}
	repo.byCycle["cycle-1"] = []string{"c-1", "c-2", "c-3", "c-4"}

	count, err := svc.CountMajorCritiques(ctx, "cycle-1")
	if err != nil {
		t.Fatalf("CountMajorCritiques failed: %v", err)
	}

	// Should count critical + major that are open = 2
	if count != 2 {
		t.Errorf("got %d major critiques, want 2", count)
	}
}

func TestReviewService_HasUnresolvedCritiques(t *testing.T) {
	ctx := context.Background()

	t.Run("has unresolved", func(t *testing.T) {
		repo := NewMockReviewRepository()
		svc := NewReviewService(repo)

		repo.critiques["c-1"] = domain.Critique{ID: "c-1", ReviewCycleID: "cycle-1", Status: domain.CritiqueOpen}
		repo.critiques["c-2"] = domain.Critique{ID: "c-2", ReviewCycleID: "cycle-1", Status: domain.CritiqueResolved}
		repo.byCycle["cycle-1"] = []string{"c-1", "c-2"}

		hasUnresolved, err := svc.HasUnresolvedCritiques(ctx, "cycle-1")
		if err != nil {
			t.Fatalf("HasUnresolvedCritiques failed: %v", err)
		}
		if !hasUnresolved {
			t.Error("expected true for unresolved critiques")
		}
	})

	t.Run("all resolved", func(t *testing.T) {
		repo := NewMockReviewRepository()
		svc := NewReviewService(repo)

		repo.critiques["c-1"] = domain.Critique{ID: "c-1", ReviewCycleID: "cycle-2", Status: domain.CritiqueResolved}
		repo.byCycle["cycle-2"] = []string{"c-1"}

		hasUnresolved, err := svc.HasUnresolvedCritiques(ctx, "cycle-2")
		if err != nil {
			t.Fatalf("HasUnresolvedCritiques failed: %v", err)
		}
		if hasUnresolved {
			t.Error("expected false for all resolved critiques")
		}
	})
}
