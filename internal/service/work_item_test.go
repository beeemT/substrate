package service

import (
	"context"
	"testing"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

func TestWorkItemService_Create(t *testing.T) {
	ctx := context.Background()

	t.Run("creates item with ingested state", func(t *testing.T) {
		repo := NewMockWorkItemRepository()
		svc := NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: repo}})

		item := domain.Session{
			ID:          "wi-1",
			WorkspaceID: "ws-1",
			Title:       "Test Item",
			Source:      "manual",
		}
		if err := svc.Create(ctx, item); err != nil {
			t.Fatalf("Create failed: %v", err)
		}

		got, err := svc.Get(ctx, "wi-1")
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if got.State != domain.SessionIngested {
			t.Errorf("State = %q, want %q", got.State, domain.SessionIngested)
		}
	})

	t.Run("allows items without external id", func(t *testing.T) {
		repo := NewMockWorkItemRepository()
		svc := NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: repo}})

		first := domain.Session{ID: "wi-no-ext-1", WorkspaceID: "ws-1", Title: "First", Source: "manual"}
		second := domain.Session{ID: "wi-no-ext-2", WorkspaceID: "ws-1", Title: "Second", Source: "manual"}

		if err := svc.Create(ctx, first); err != nil {
			t.Fatalf("Create first without external id: %v", err)
		}
		if err := svc.Create(ctx, second); err != nil {
			t.Fatalf("Create second without external id: %v", err)
		}
	})

	t.Run("rejects duplicate external id in same workspace", func(t *testing.T) {
		repo := NewMockWorkItemRepository()
		svc := NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: repo}})

		first := domain.Session{ID: "wi-dup-1", WorkspaceID: "ws-1", ExternalID: "EXT-1", Title: "First", Source: "manual"}
		second := domain.Session{ID: "wi-dup-2", WorkspaceID: "ws-1", ExternalID: "EXT-1", Title: "Second", Source: "manual"}

		if err := svc.Create(ctx, first); err != nil {
			t.Fatalf("Create first duplicate candidate: %v", err)
		}
		err := svc.Create(ctx, second)
		if err == nil {
			t.Fatal("expected duplicate create error")
		}
		if _, ok := err.(ErrAlreadyExists); !ok {
			t.Errorf("error type = %T, want ErrAlreadyExists", err)
		}
	})

	t.Run("rejects overlapping source item ids in same workspace", func(t *testing.T) {
		repo := NewMockWorkItemRepository()
		svc := NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: repo}})

		existing := domain.Session{
			ID:            "wi-existing",
			WorkspaceID:   "ws-1",
			ExternalID:    "gh:issue:acme/rocket#42",
			Title:         "Issue 42",
			Source:        "github",
			SourceScope:   domain.ScopeIssues,
			SourceItemIDs: []string{"acme/rocket#42"},
		}
		aggregate := domain.Session{
			ID:            "wi-aggregate",
			WorkspaceID:   "ws-1",
			ExternalID:    "gh:issue:acme/rocket#7",
			Title:         "Issue 7 (+1 more)",
			Source:        "github",
			SourceScope:   domain.ScopeIssues,
			SourceItemIDs: []string{"acme/rocket#7", "acme/rocket#42"},
		}

		if err := svc.Create(ctx, existing); err != nil {
			t.Fatalf("Create existing issue: %v", err)
		}
		err := svc.Create(ctx, aggregate)
		if err == nil {
			t.Fatal("expected overlap create error")
		}
		if _, ok := err.(ErrAlreadyExists); !ok {
			t.Errorf("error type = %T, want ErrAlreadyExists", err)
		}
	})

	t.Run("allows distinct source item ids in same workspace", func(t *testing.T) {
		repo := NewMockWorkItemRepository()
		svc := NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: repo}})

		existing := domain.Session{
			ID:            "wi-existing-distinct",
			WorkspaceID:   "ws-1",
			ExternalID:    "gh:issue:acme/rocket#42",
			Title:         "Issue 42",
			Source:        "github",
			SourceScope:   domain.ScopeIssues,
			SourceItemIDs: []string{"acme/rocket#42"},
		}
		aggregate := domain.Session{
			ID:            "wi-aggregate-distinct",
			WorkspaceID:   "ws-1",
			ExternalID:    "gh:issue:acme/rocket#7",
			Title:         "Issue 7 (+1 more)",
			Source:        "github",
			SourceScope:   domain.ScopeIssues,
			SourceItemIDs: []string{"acme/rocket#7", "acme/rocket#8"},
		}

		if err := svc.Create(ctx, existing); err != nil {
			t.Fatalf("Create existing issue: %v", err)
		}
		if err := svc.Create(ctx, aggregate); err != nil {
			t.Fatalf("Create distinct aggregate: %v", err)
		}
	})

	t.Run("allows github milestones with same number in different repos", func(t *testing.T) {
		repo := NewMockWorkItemRepository()
		svc := NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: repo}})

		existing := domain.Session{
			ID:            "Session-1",
			WorkspaceID:   "ws-1",
			ExternalID:    "gh:milestone:acme/rocket",
			Title:         "Rocket v1",
			Source:        "github",
			SourceScope:   domain.ScopeProjects,
			SourceItemIDs: []string{"7"},
		}
		candidate := domain.Session{
			ID:            "wi-gh-ms-2",
			WorkspaceID:   "ws-1",
			ExternalID:    "gh:milestone:acme/booster",
			Title:         "Rocket v1",
			Source:        "github",
			SourceScope:   domain.ScopeProjects,
			SourceItemIDs: []string{"7"},
		}

		if err := svc.Create(ctx, existing); err != nil {
			t.Fatalf("Create existing milestone: %v", err)
		}
		if err := svc.Create(ctx, candidate); err != nil {
			t.Fatalf("Create cross-repo milestone: %v", err)
		}
	})

	t.Run("allows gitlab milestones with same id in different projects", func(t *testing.T) {
		repo := NewMockWorkItemRepository()
		svc := NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: repo}})

		existing := domain.Session{
			ID:            "wi-gl-ms-1",
			WorkspaceID:   "ws-1",
			ExternalID:    "gl:milestone:1234",
			Title:         "Platform",
			Source:        "gitlab",
			SourceScope:   domain.ScopeProjects,
			SourceItemIDs: []string{"77"},
			Metadata:      map[string]any{"project_id": int64(1234)},
		}
		candidate := domain.Session{
			ID:            "wi-gl-ms-2",
			WorkspaceID:   "ws-1",
			ExternalID:    "gl:milestone:5678",
			Title:         "Platform",
			Source:        "gitlab",
			SourceScope:   domain.ScopeProjects,
			SourceItemIDs: []string{"77"},
			Metadata:      map[string]any{"project_id": int64(5678)},
		}

		if err := svc.Create(ctx, existing); err != nil {
			t.Fatalf("Create existing GitLab milestone: %v", err)
		}
		if err := svc.Create(ctx, candidate); err != nil {
			t.Fatalf("Create cross-project GitLab milestone: %v", err)
		}
	})

	t.Run("rejects gitlab milestones with same id in same project", func(t *testing.T) {
		repo := NewMockWorkItemRepository()
		svc := NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: repo}})

		existing := domain.Session{
			ID:            "wi-gl-ms-same-1",
			WorkspaceID:   "ws-1",
			Title:         "Platform",
			Source:        "gitlab",
			SourceScope:   domain.ScopeProjects,
			SourceItemIDs: []string{"77"},
			Metadata:      map[string]any{"project_id": int64(1234)},
		}
		candidate := domain.Session{
			ID:            "wi-gl-ms-same-2",
			WorkspaceID:   "ws-1",
			Title:         "Platform duplicate",
			Source:        "gitlab",
			SourceScope:   domain.ScopeProjects,
			SourceItemIDs: []string{"77"},
			Metadata:      map[string]any{"project_id": int64(1234)},
		}

		if err := svc.Create(ctx, existing); err != nil {
			t.Fatalf("Create existing same-project GitLab milestone: %v", err)
		}
		err := svc.Create(ctx, candidate)
		if err == nil {
			t.Fatal("expected same-project GitLab milestone duplicate error")
		}
		if _, ok := err.(ErrAlreadyExists); !ok {
			t.Errorf("error type = %T, want ErrAlreadyExists", err)
		}
	})

	t.Run("allows gitlab epics with same iid in different groups", func(t *testing.T) {
		repo := NewMockWorkItemRepository()
		svc := NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: repo}})

		existing := domain.Session{
			ID:            "wi-gl-epic-1",
			WorkspaceID:   "ws-1",
			ExternalID:    "gl:epic:9",
			Title:         "Epic 9",
			Source:        "gitlab",
			SourceScope:   domain.ScopeInitiatives,
			SourceItemIDs: []string{"9"},
			Metadata:      map[string]any{"group_id": int64(11)},
		}
		candidate := domain.Session{
			ID:            "wi-gl-epic-2",
			WorkspaceID:   "ws-1",
			ExternalID:    "gl:epic:9",
			Title:         "Epic 9",
			Source:        "gitlab",
			SourceScope:   domain.ScopeInitiatives,
			SourceItemIDs: []string{"9"},
			Metadata:      map[string]any{"group_id": int64(22)},
		}

		if err := svc.Create(ctx, existing); err != nil {
			t.Fatalf("Create existing GitLab epic: %v", err)
		}
		if err := svc.Create(ctx, candidate); err != nil {
			t.Fatalf("Create cross-group GitLab epic: %v", err)
		}
	})

	t.Run("allows same external id in different workspaces", func(t *testing.T) {
		repo := NewMockWorkItemRepository()
		svc := NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: repo}})

		first := domain.Session{ID: "wi-cross-1", WorkspaceID: "ws-1", ExternalID: "EXT-1", Title: "First", Source: "manual"}
		second := domain.Session{ID: "wi-cross-2", WorkspaceID: "ws-2", ExternalID: "EXT-1", Title: "Second", Source: "manual"}

		if err := svc.Create(ctx, first); err != nil {
			t.Fatalf("Create first workspace item: %v", err)
		}
		if err := svc.Create(ctx, second); err != nil {
			t.Fatalf("Create second workspace item: %v", err)
		}
	})

	t.Run("rejects missing workspace id", func(t *testing.T) {
		repo := NewMockWorkItemRepository()
		svc := NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: repo}})

		item := domain.Session{
			ID:     "wi-missing-workspace",
			Title:  "Test Item",
			Source: "manual",
		}
		err := svc.Create(ctx, item)
		if err == nil {
			t.Fatal("expected error for missing workspace id")
		}
		_, ok := err.(ErrInvalidInput)
		if !ok {
			t.Errorf("error type = %T, want ErrInvalidInput", err)
		}
	})

	t.Run("rejects non-ingested initial state", func(t *testing.T) {
		repo := NewMockWorkItemRepository()
		svc := NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: repo}})

		item := domain.Session{
			ID:          "wi-2",
			WorkspaceID: "ws-1",
			Title:       "Test Item",
			Source:      "manual",
			State:       domain.SessionPlanning,
		}
		err := svc.Create(ctx, item)
		if err == nil {
			t.Fatal("expected error for non-ingested initial state")
		}
		if _, ok := err.(ErrInvalidInput); !ok {
			t.Errorf("error type = %T, want ErrInvalidInput", err)
		}
	})
}

func TestWorkItemService_ValidTransitions(t *testing.T) {
	ctx := context.Background()

	// Define all valid transitions based on the state machine
	validTransitions := []struct {
		from domain.SessionState
		to   domain.SessionState
		name string
	}{
		{domain.SessionIngested, domain.SessionPlanning, "ingested -> planning"},
		{domain.SessionPlanning, domain.SessionIngested, "planning -> ingested rollback"},
		{domain.SessionPlanning, domain.SessionPlanReview, "planning -> plan_review"},
		{domain.SessionPlanning, domain.SessionFailed, "planning -> failed"},
		{domain.SessionPlanReview, domain.SessionApproved, "plan_review -> approved"},
		{domain.SessionPlanReview, domain.SessionPlanning, "plan_review -> planning"},
		{domain.SessionPlanReview, domain.SessionFailed, "plan_review -> failed"},
		{domain.SessionApproved, domain.SessionImplementing, "approved -> implementing"},
		{domain.SessionApproved, domain.SessionFailed, "approved -> failed"},
		{domain.SessionImplementing, domain.SessionReviewing, "implementing -> reviewing"},
		{domain.SessionImplementing, domain.SessionFailed, "implementing -> failed"},
		{domain.SessionImplementing, domain.SessionCompleted, "implementing -> completed"},
		{domain.SessionReviewing, domain.SessionCompleted, "reviewing -> completed"},
		{domain.SessionReviewing, domain.SessionImplementing, "reviewing -> implementing"},
		{domain.SessionReviewing, domain.SessionFailed, "reviewing -> failed"},
		{domain.SessionFailed, domain.SessionImplementing, "failed -> implementing"},
	}

	for _, tc := range validTransitions {
		t.Run(tc.name, func(t *testing.T) {
			repo := NewMockWorkItemRepository()
			svc := NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: repo}})

			// Create item in the 'from' state directly in repo
			item := domain.Session{
				ID:          "wi-test",
				WorkspaceID: "ws-1",
				Title:       "Test",
				Source:      "manual",
				State:       tc.from,
			}
			repo.items["wi-test"] = item

			if err := svc.Transition(ctx, "wi-test", tc.to); err != nil {
				t.Fatalf("Transition from %s to %s failed: %v", tc.from, tc.to, err)
			}

			got, err := svc.Get(ctx, "wi-test")
			if err != nil {
				t.Fatalf("Get failed: %v", err)
			}
			if got.State != tc.to {
				t.Errorf("State = %q, want %q", got.State, tc.to)
			}
		})
	}
}

func TestWorkItemService_InvalidTransitions(t *testing.T) {
	ctx := context.Background()

	invalidTransitions := []struct {
		from domain.SessionState
		to   domain.SessionState
		name string
	}{
		{domain.SessionIngested, domain.SessionApproved, "ingested -> approved"},
		{domain.SessionIngested, domain.SessionCompleted, "ingested -> completed"},
		{domain.SessionIngested, domain.SessionFailed, "ingested -> failed"},
		{domain.SessionPlanning, domain.SessionImplementing, "planning -> implementing"},
		{domain.SessionPlanning, domain.SessionCompleted, "planning -> completed"},
		{domain.SessionPlanReview, domain.SessionImplementing, "plan_review -> implementing"},
		{domain.SessionPlanReview, domain.SessionCompleted, "plan_review -> completed"},
		{domain.SessionApproved, domain.SessionPlanning, "approved -> planning"},
		{domain.SessionApproved, domain.SessionCompleted, "approved -> completed"},
		{domain.SessionImplementing, domain.SessionPlanning, "implementing -> planning"},
		{domain.SessionReviewing, domain.SessionPlanning, "reviewing -> planning"},
		{domain.SessionReviewing, domain.SessionApproved, "reviewing -> approved"},
		{domain.SessionCompleted, domain.SessionImplementing, "completed -> implementing"},
		{domain.SessionFailed, domain.SessionPlanning, "failed -> planning"},
		{domain.SessionFailed, domain.SessionIngested, "failed -> ingested"},
	}

	for _, tc := range invalidTransitions {
		t.Run(tc.name, func(t *testing.T) {
			repo := NewMockWorkItemRepository()
			svc := NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: repo}})

			item := domain.Session{
				ID:          "wi-test",
				WorkspaceID: "ws-1",
				Title:       "Test",
				Source:      "manual",
				State:       tc.from,
			}
			repo.items["wi-test"] = item

			err := svc.Transition(ctx, "wi-test", tc.to)
			if err == nil {
				t.Fatalf("expected ErrInvalidTransition, got nil")
			}
			if _, ok := err.(ErrInvalidTransition); !ok {
				t.Errorf("error type = %T, want ErrInvalidTransition", err)
			}
		})
	}
}

func TestWorkItemService_ConvenienceMethods(t *testing.T) {
	ctx := context.Background()
	repo := NewMockWorkItemRepository()
	svc := NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: repo}})

	t.Run("StartPlanning", func(t *testing.T) {
		repo.items["wi-1"] = domain.Session{ID: "wi-1", WorkspaceID: "ws-1", Title: "T", Source: "manual", State: domain.SessionIngested}
		if err := svc.StartPlanning(ctx, "wi-1"); err != nil {
			t.Fatalf("StartPlanning failed: %v", err)
		}
		got, _ := svc.Get(ctx, "wi-1")
		if got.State != domain.SessionPlanning {
			t.Errorf("State = %q, want %q", got.State, domain.SessionPlanning)
		}
	})

	t.Run("SubmitPlanForReview", func(t *testing.T) {
		repo.items["wi-2"] = domain.Session{ID: "wi-2", WorkspaceID: "ws-1", Title: "T", Source: "manual", State: domain.SessionPlanning}
		if err := svc.SubmitPlanForReview(ctx, "wi-2"); err != nil {
			t.Fatalf("SubmitPlanForReview failed: %v", err)
		}
		got, _ := svc.Get(ctx, "wi-2")
		if got.State != domain.SessionPlanReview {
			t.Errorf("State = %q, want %q", got.State, domain.SessionPlanReview)
		}
	})

	t.Run("ApprovePlan", func(t *testing.T) {
		repo.items["wi-3"] = domain.Session{ID: "wi-3", WorkspaceID: "ws-1", Title: "T", Source: "manual", State: domain.SessionPlanReview}
		if err := svc.ApprovePlan(ctx, "wi-3"); err != nil {
			t.Fatalf("ApprovePlan failed: %v", err)
		}
		got, _ := svc.Get(ctx, "wi-3")
		if got.State != domain.SessionApproved {
			t.Errorf("State = %q, want %q", got.State, domain.SessionApproved)
		}
	})

	t.Run("RejectPlan", func(t *testing.T) {
		repo.items["wi-4"] = domain.Session{ID: "wi-4", WorkspaceID: "ws-1", Title: "T", Source: "manual", State: domain.SessionPlanReview}
		if err := svc.RejectPlan(ctx, "wi-4"); err != nil {
			t.Fatalf("RejectPlan failed: %v", err)
		}
		got, _ := svc.Get(ctx, "wi-4")
		if got.State != domain.SessionPlanning {
			t.Errorf("State = %q, want %q", got.State, domain.SessionPlanning)
		}
	})

	t.Run("StartImplementation", func(t *testing.T) {
		repo.items["wi-5"] = domain.Session{ID: "wi-5", WorkspaceID: "ws-1", Title: "T", Source: "manual", State: domain.SessionApproved}
		if err := svc.StartImplementation(ctx, "wi-5"); err != nil {
			t.Fatalf("StartImplementation failed: %v", err)
		}
		got, _ := svc.Get(ctx, "wi-5")
		if got.State != domain.SessionImplementing {
			t.Errorf("State = %q, want %q", got.State, domain.SessionImplementing)
		}
	})

	t.Run("SubmitForReview", func(t *testing.T) {
		repo.items["wi-6"] = domain.Session{ID: "wi-6", WorkspaceID: "ws-1", Title: "T", Source: "manual", State: domain.SessionImplementing}
		if err := svc.SubmitForReview(ctx, "wi-6"); err != nil {
			t.Fatalf("SubmitForReview failed: %v", err)
		}
		got, _ := svc.Get(ctx, "wi-6")
		if got.State != domain.SessionReviewing {
			t.Errorf("State = %q, want %q", got.State, domain.SessionReviewing)
		}
	})

	t.Run("CompleteWorkItem", func(t *testing.T) {
		repo.items["wi-7"] = domain.Session{ID: "wi-7", WorkspaceID: "ws-1", Title: "T", Source: "manual", State: domain.SessionReviewing}
		if err := svc.CompleteWorkItem(ctx, "wi-7"); err != nil {
			t.Fatalf("CompleteWorkItem failed: %v", err)
		}
		got, _ := svc.Get(ctx, "wi-7")
		if got.State != domain.SessionCompleted {
			t.Errorf("State = %q, want %q", got.State, domain.SessionCompleted)
		}
	})

	t.Run("RequestReimplementation", func(t *testing.T) {
		repo.items["wi-8"] = domain.Session{ID: "wi-8", WorkspaceID: "ws-1", Title: "T", Source: "manual", State: domain.SessionReviewing}
		if err := svc.RequestReimplementation(ctx, "wi-8"); err != nil {
			t.Fatalf("RequestReimplementation failed: %v", err)
		}
		got, _ := svc.Get(ctx, "wi-8")
		if got.State != domain.SessionImplementing {
			t.Errorf("State = %q, want %q", got.State, domain.SessionImplementing)
		}
	})

	t.Run("FailWorkItem", func(t *testing.T) {
		repo.items["wi-9"] = domain.Session{ID: "wi-9", WorkspaceID: "ws-1", Title: "T", Source: "manual", State: domain.SessionImplementing}
		if err := svc.FailWorkItem(ctx, "wi-9"); err != nil {
			t.Fatalf("FailWorkItem failed: %v", err)
		}
		got, _ := svc.Get(ctx, "wi-9")
		if got.State != domain.SessionFailed {
			t.Errorf("State = %q, want %q", got.State, domain.SessionFailed)
		}
	})
}

func TestSessionService_RetryFailedWorkItem(t *testing.T) {
	ctx := context.Background()
	repo := NewMockWorkItemRepository()
	svc := NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: repo}})
	repo.items["wi-1"] = domain.Session{ID: "wi-1", WorkspaceID: "ws-1", Title: "T", Source: "manual", State: domain.SessionFailed}
	if err := svc.RetryFailedWorkItem(ctx, "wi-1"); err != nil {
		t.Fatalf("RetryFailedWorkItem: %v", err)
	}
	got, _ := svc.Get(ctx, "wi-1")
	if got.State != domain.SessionImplementing {
		t.Errorf("state = %v, want implementing", got.State)
	}
}

func TestSessionService_RetryFailedWorkItem_NotFailed(t *testing.T) {
	ctx := context.Background()
	repo := NewMockWorkItemRepository()
	svc := NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: repo}})
	repo.items["wi-1"] = domain.Session{ID: "wi-1", WorkspaceID: "ws-1", Title: "T", Source: "manual", State: domain.SessionImplementing}
	if err := svc.RetryFailedWorkItem(ctx, "wi-1"); err == nil {
		t.Fatal("expected error for non-failed work item")
	}
}

func TestWorkItemService_NotFound(t *testing.T) {
	ctx := context.Background()
	repo := NewMockWorkItemRepository()
	svc := NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: repo}})

	t.Run("Get not found", func(t *testing.T) {
		_, err := svc.Get(ctx, "nonexistent")
		if err == nil {
			t.Fatal("expected error for nonexistent item")
		}
		if _, ok := err.(ErrNotFound); !ok {
			t.Errorf("error type = %T, want ErrNotFound", err)
		}
	})

	t.Run("Transition not found", func(t *testing.T) {
		err := svc.Transition(ctx, "nonexistent", domain.SessionPlanning)
		if err == nil {
			t.Fatal("expected error for nonexistent item")
		}
		if _, ok := err.(ErrNotFound); !ok {
			t.Errorf("error type = %T, want ErrNotFound", err)
		}
	})

	t.Run("Delete not found", func(t *testing.T) {
		err := svc.Delete(ctx, "nonexistent")
		if err == nil {
			t.Fatal("expected error for nonexistent item")
		}
		if _, ok := err.(ErrNotFound); !ok {
			t.Errorf("error type = %T, want ErrNotFound", err)
		}
	})
}

func TestWorkItemService_List(t *testing.T) {
	ctx := context.Background()
	repo := NewMockWorkItemRepository()
	svc := NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: repo}})

	// Create test items
	ws1 := "ws-1"
	ws2 := "ws-2"
	statePlanning := domain.SessionPlanning

	items := []domain.Session{
		{ID: "wi-1", WorkspaceID: ws1, Title: "Item 1", Source: "manual", State: domain.SessionIngested},
		{ID: "wi-2", WorkspaceID: ws1, Title: "Item 2", Source: "linear", State: domain.SessionPlanning},
		{ID: "wi-3", WorkspaceID: ws2, Title: "Item 3", Source: "manual", State: domain.SessionPlanning},
	}
	for _, item := range items {
		repo.items[item.ID] = item
	}

	t.Run("filter by workspace", func(t *testing.T) {
		got, err := svc.List(ctx, repository.SessionFilter{WorkspaceID: &ws1})
		if err != nil {
			t.Fatalf("List failed: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("got %d items, want 2", len(got))
		}
	})

	t.Run("filter by state", func(t *testing.T) {
		got, err := svc.List(ctx, repository.SessionFilter{State: &statePlanning})
		if err != nil {
			t.Fatalf("List failed: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("got %d items, want 2", len(got))
		}
	})

	t.Run("filter by source", func(t *testing.T) {
		source := "manual"
		got, err := svc.List(ctx, repository.SessionFilter{Source: &source})
		if err != nil {
			t.Fatalf("List failed: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("got %d items, want 2", len(got))
		}
	})
}

func TestWorkItemService_Update(t *testing.T) {
	ctx := context.Background()
	repo := NewMockWorkItemRepository()
	svc := NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: repo}})

	// Create initial item
	item := domain.Session{
		ID:          "wi-1",
		WorkspaceID: "ws-1",
		Title:       "Original Title",
		Source:      "manual",
		State:       domain.SessionIngested,
	}
	repo.items["wi-1"] = item

	t.Run("updates mutable fields", func(t *testing.T) {
		updated := domain.Session{
			ID:          "wi-1",
			Title:       "Updated Title",
			Description: "New description",
		}
		if err := svc.Update(ctx, updated); err != nil {
			t.Fatalf("Update failed: %v", err)
		}

		got, _ := svc.Get(ctx, "wi-1")
		if got.Title != "Updated Title" {
			t.Errorf("Title = %q, want %q", got.Title, "Updated Title")
		}
		if got.Description != "New description" {
			t.Errorf("Description = %q, want %q", got.Description, "New description")
		}
		// State should be preserved
		if got.State != domain.SessionIngested {
			t.Errorf("State should be preserved, got %q", got.State)
		}
	})

	t.Run("update nonexistent returns error", func(t *testing.T) {
		updated := domain.Session{ID: "nonexistent"}
		err := svc.Update(ctx, updated)
		if err == nil {
			t.Fatal("expected error for nonexistent item")
		}
		_, ok := err.(ErrNotFound)
		if !ok {
			t.Errorf("error type = %T, want ErrNotFound", err)
		}
	})
}
