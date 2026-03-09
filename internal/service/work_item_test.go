package service

import (
	"context"
	"testing"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

func TestWorkItemService_Create(t *testing.T) {
	ctx := context.Background()
	repo := NewMockWorkItemRepository()
	svc := NewWorkItemService(repo)

	t.Run("creates item with ingested state", func(t *testing.T) {
		item := domain.WorkItem{
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
		if got.State != domain.WorkItemIngested {
			t.Errorf("State = %q, want %q", got.State, domain.WorkItemIngested)
		}
	})

	t.Run("rejects missing workspace id", func(t *testing.T) {
		item := domain.WorkItem{
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
		item := domain.WorkItem{
			ID:          "wi-2",
			WorkspaceID: "ws-1",
			Title:       "Test Item",
			Source:      "manual",
			State:       domain.WorkItemPlanning,
		}
		err := svc.Create(ctx, item)
		if err == nil {
			t.Fatal("expected error for non-ingested initial state")
		}
		_, ok := err.(ErrInvalidInput)
		if !ok {
			t.Errorf("error type = %T, want ErrInvalidInput", err)
		}
	})
}

func TestWorkItemService_ValidTransitions(t *testing.T) {
	ctx := context.Background()

	// Define all valid transitions based on the state machine
	validTransitions := []struct {
		from domain.WorkItemState
		to   domain.WorkItemState
		name string
	}{
		{domain.WorkItemIngested, domain.WorkItemPlanning, "ingested -> planning"},
		{domain.WorkItemPlanning, domain.WorkItemPlanReview, "planning -> plan_review"},
		{domain.WorkItemPlanning, domain.WorkItemFailed, "planning -> failed"},
		{domain.WorkItemPlanReview, domain.WorkItemApproved, "plan_review -> approved"},
		{domain.WorkItemPlanReview, domain.WorkItemPlanning, "plan_review -> planning"},
		{domain.WorkItemPlanReview, domain.WorkItemFailed, "plan_review -> failed"},
		{domain.WorkItemApproved, domain.WorkItemImplementing, "approved -> implementing"},
		{domain.WorkItemApproved, domain.WorkItemFailed, "approved -> failed"},
		{domain.WorkItemImplementing, domain.WorkItemReviewing, "implementing -> reviewing"},
		{domain.WorkItemImplementing, domain.WorkItemFailed, "implementing -> failed"},
		{domain.WorkItemReviewing, domain.WorkItemCompleted, "reviewing -> completed"},
		{domain.WorkItemReviewing, domain.WorkItemImplementing, "reviewing -> implementing"},
		{domain.WorkItemReviewing, domain.WorkItemFailed, "reviewing -> failed"},
	}

	for _, tc := range validTransitions {
		t.Run(tc.name, func(t *testing.T) {
			repo := NewMockWorkItemRepository()
			svc := NewWorkItemService(repo)

			// Create item in the 'from' state directly in repo
			item := domain.WorkItem{
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

	// Define invalid transitions - at least one per state
	invalidTransitions := []struct {
		from domain.WorkItemState
		to   domain.WorkItemState
		name string
	}{
		// From ingested - can't skip to approved
		{domain.WorkItemIngested, domain.WorkItemApproved, "ingested -> approved"},
		{domain.WorkItemIngested, domain.WorkItemCompleted, "ingested -> completed"},
		{domain.WorkItemIngested, domain.WorkItemFailed, "ingested -> failed"},
		// From planning - can't go directly to implementing
		{domain.WorkItemPlanning, domain.WorkItemImplementing, "planning -> implementing"},
		{domain.WorkItemPlanning, domain.WorkItemCompleted, "planning -> completed"},
		// From plan_review - can't go directly to implementing
		{domain.WorkItemPlanReview, domain.WorkItemImplementing, "plan_review -> implementing"},
		{domain.WorkItemPlanReview, domain.WorkItemCompleted, "plan_review -> completed"},
		// From approved - can't go back to planning
		{domain.WorkItemApproved, domain.WorkItemPlanning, "approved -> planning"},
		{domain.WorkItemApproved, domain.WorkItemCompleted, "approved -> completed"},
		// From implementing - can't go directly to completed
		{domain.WorkItemImplementing, domain.WorkItemCompleted, "implementing -> completed"},
		{domain.WorkItemImplementing, domain.WorkItemPlanning, "implementing -> planning"},
		// From reviewing - can't go back to planning
		{domain.WorkItemReviewing, domain.WorkItemPlanning, "reviewing -> planning"},
		{domain.WorkItemReviewing, domain.WorkItemApproved, "reviewing -> approved"},
		// From completed - terminal state
		{domain.WorkItemCompleted, domain.WorkItemPlanning, "completed -> planning"},
		{domain.WorkItemCompleted, domain.WorkItemImplementing, "completed -> implementing"},
		// From failed - terminal state
		{domain.WorkItemFailed, domain.WorkItemPlanning, "failed -> planning"},
		{domain.WorkItemFailed, domain.WorkItemIngested, "failed -> ingested"},
	}

	for _, tc := range invalidTransitions {
		t.Run(tc.name, func(t *testing.T) {
			repo := NewMockWorkItemRepository()
			svc := NewWorkItemService(repo)

			// Create item in the 'from' state
			item := domain.WorkItem{
				ID:          "wi-test",
				WorkspaceID: "ws-1",
				Title:       "Test",
				Source:      "manual",
				State:       tc.from,
			}
			repo.items["wi-test"] = item

			err := svc.Transition(ctx, "wi-test", tc.to)
			if err == nil {
				t.Fatalf("expected error for transition from %s to %s", tc.from, tc.to)
			}

			var transitionErr ErrInvalidTransition
			if err == nil {
				t.Fatalf("expected ErrInvalidTransition, got nil")
			}
			_ = transitionErr // Just check we got an error
			if _, ok := err.(ErrInvalidTransition); !ok {
				t.Errorf("error type = %T, want ErrInvalidTransition", err)
			}
		})
	}
}

func TestWorkItemService_ConvenienceMethods(t *testing.T) {
	ctx := context.Background()
	repo := NewMockWorkItemRepository()
	svc := NewWorkItemService(repo)

	t.Run("StartPlanning", func(t *testing.T) {
		item := domain.WorkItem{ID: "wi-1", WorkspaceID: "ws-1", Title: "T", Source: "manual", State: domain.WorkItemIngested}
		repo.items["wi-1"] = item
		if err := svc.StartPlanning(ctx, "wi-1"); err != nil {
			t.Fatalf("StartPlanning failed: %v", err)
		}
		got, _ := svc.Get(ctx, "wi-1")
		if got.State != domain.WorkItemPlanning {
			t.Errorf("State = %q, want %q", got.State, domain.WorkItemPlanning)
		}
	})

	t.Run("SubmitPlanForReview", func(t *testing.T) {
		item := domain.WorkItem{ID: "wi-2", WorkspaceID: "ws-1", Title: "T", Source: "manual", State: domain.WorkItemPlanning}
		repo.items["wi-2"] = item
		if err := svc.SubmitPlanForReview(ctx, "wi-2"); err != nil {
			t.Fatalf("SubmitPlanForReview failed: %v", err)
		}
		got, _ := svc.Get(ctx, "wi-2")
		if got.State != domain.WorkItemPlanReview {
			t.Errorf("State = %q, want %q", got.State, domain.WorkItemPlanReview)
		}
	})

	t.Run("ApprovePlan", func(t *testing.T) {
		item := domain.WorkItem{ID: "wi-3", WorkspaceID: "ws-1", Title: "T", Source: "manual", State: domain.WorkItemPlanReview}
		repo.items["wi-3"] = item
		if err := svc.ApprovePlan(ctx, "wi-3"); err != nil {
			t.Fatalf("ApprovePlan failed: %v", err)
		}
		got, _ := svc.Get(ctx, "wi-3")
		if got.State != domain.WorkItemApproved {
			t.Errorf("State = %q, want %q", got.State, domain.WorkItemApproved)
		}
	})

	t.Run("RejectPlan", func(t *testing.T) {
		item := domain.WorkItem{ID: "wi-4", WorkspaceID: "ws-1", Title: "T", Source: "manual", State: domain.WorkItemPlanReview}
		repo.items["wi-4"] = item
		if err := svc.RejectPlan(ctx, "wi-4"); err != nil {
			t.Fatalf("RejectPlan failed: %v", err)
		}
		got, _ := svc.Get(ctx, "wi-4")
		if got.State != domain.WorkItemPlanning {
			t.Errorf("State = %q, want %q", got.State, domain.WorkItemPlanning)
		}
	})

	t.Run("StartImplementation", func(t *testing.T) {
		item := domain.WorkItem{ID: "wi-5", WorkspaceID: "ws-1", Title: "T", Source: "manual", State: domain.WorkItemApproved}
		repo.items["wi-5"] = item
		if err := svc.StartImplementation(ctx, "wi-5"); err != nil {
			t.Fatalf("StartImplementation failed: %v", err)
		}
		got, _ := svc.Get(ctx, "wi-5")
		if got.State != domain.WorkItemImplementing {
			t.Errorf("State = %q, want %q", got.State, domain.WorkItemImplementing)
		}
	})

	t.Run("SubmitForReview", func(t *testing.T) {
		item := domain.WorkItem{ID: "wi-6", WorkspaceID: "ws-1", Title: "T", Source: "manual", State: domain.WorkItemImplementing}
		repo.items["wi-6"] = item
		if err := svc.SubmitForReview(ctx, "wi-6"); err != nil {
			t.Fatalf("SubmitForReview failed: %v", err)
		}
		got, _ := svc.Get(ctx, "wi-6")
		if got.State != domain.WorkItemReviewing {
			t.Errorf("State = %q, want %q", got.State, domain.WorkItemReviewing)
		}
	})

	t.Run("CompleteWorkItem", func(t *testing.T) {
		item := domain.WorkItem{ID: "wi-7", WorkspaceID: "ws-1", Title: "T", Source: "manual", State: domain.WorkItemReviewing}
		repo.items["wi-7"] = item
		if err := svc.CompleteWorkItem(ctx, "wi-7"); err != nil {
			t.Fatalf("CompleteWorkItem failed: %v", err)
		}
		got, _ := svc.Get(ctx, "wi-7")
		if got.State != domain.WorkItemCompleted {
			t.Errorf("State = %q, want %q", got.State, domain.WorkItemCompleted)
		}
	})

	t.Run("RequestReimplementation", func(t *testing.T) {
		item := domain.WorkItem{ID: "wi-8", WorkspaceID: "ws-1", Title: "T", Source: "manual", State: domain.WorkItemReviewing}
		repo.items["wi-8"] = item
		if err := svc.RequestReimplementation(ctx, "wi-8"); err != nil {
			t.Fatalf("RequestReimplementation failed: %v", err)
		}
		got, _ := svc.Get(ctx, "wi-8")
		if got.State != domain.WorkItemImplementing {
			t.Errorf("State = %q, want %q", got.State, domain.WorkItemImplementing)
		}
	})

	t.Run("FailWorkItem", func(t *testing.T) {
		item := domain.WorkItem{ID: "wi-9", WorkspaceID: "ws-1", Title: "T", Source: "manual", State: domain.WorkItemImplementing}
		repo.items["wi-9"] = item
		if err := svc.FailWorkItem(ctx, "wi-9"); err != nil {
			t.Fatalf("FailWorkItem failed: %v", err)
		}
		got, _ := svc.Get(ctx, "wi-9")
		if got.State != domain.WorkItemFailed {
			t.Errorf("State = %q, want %q", got.State, domain.WorkItemFailed)
		}
	})
}

func TestWorkItemService_NotFound(t *testing.T) {
	ctx := context.Background()
	repo := NewMockWorkItemRepository()
	svc := NewWorkItemService(repo)

	t.Run("Get not found", func(t *testing.T) {
		_, err := svc.Get(ctx, "nonexistent")
		if err == nil {
			t.Fatal("expected error for nonexistent item")
		}
		_, ok := err.(ErrNotFound)
		if !ok {
			t.Errorf("error type = %T, want ErrNotFound", err)
		}
	})

	t.Run("Transition not found", func(t *testing.T) {
		err := svc.Transition(ctx, "nonexistent", domain.WorkItemPlanning)
		if err == nil {
			t.Fatal("expected error for nonexistent item")
		}
		_, ok := err.(ErrNotFound)
		if !ok {
			t.Errorf("error type = %T, want ErrNotFound", err)
		}
	})

	t.Run("Delete not found", func(t *testing.T) {
		err := svc.Delete(ctx, "nonexistent")
		if err == nil {
			t.Fatal("expected error for nonexistent item")
		}
		_, ok := err.(ErrNotFound)
		if !ok {
			t.Errorf("error type = %T, want ErrNotFound", err)
		}
	})
}

func TestWorkItemService_List(t *testing.T) {
	ctx := context.Background()
	repo := NewMockWorkItemRepository()
	svc := NewWorkItemService(repo)

	// Create test items
	ws1 := "ws-1"
	ws2 := "ws-2"
	statePlanning := domain.WorkItemPlanning

	items := []domain.WorkItem{
		{ID: "wi-1", WorkspaceID: ws1, Title: "Item 1", Source: "manual", State: domain.WorkItemIngested},
		{ID: "wi-2", WorkspaceID: ws1, Title: "Item 2", Source: "linear", State: domain.WorkItemPlanning},
		{ID: "wi-3", WorkspaceID: ws2, Title: "Item 3", Source: "manual", State: domain.WorkItemPlanning},
	}
	for _, item := range items {
		repo.items[item.ID] = item
	}

	t.Run("filter by workspace", func(t *testing.T) {
		got, err := svc.List(ctx, repository.WorkItemFilter{WorkspaceID: &ws1})
		if err != nil {
			t.Fatalf("List failed: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("got %d items, want 2", len(got))
		}
	})

	t.Run("filter by state", func(t *testing.T) {
		got, err := svc.List(ctx, repository.WorkItemFilter{State: &statePlanning})
		if err != nil {
			t.Fatalf("List failed: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("got %d items, want 2", len(got))
		}
	})

	t.Run("filter by source", func(t *testing.T) {
		source := "manual"
		got, err := svc.List(ctx, repository.WorkItemFilter{Source: &source})
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
	svc := NewWorkItemService(repo)

	// Create initial item
	item := domain.WorkItem{
		ID:          "wi-1",
		WorkspaceID: "ws-1",
		Title:       "Original Title",
		Source:      "manual",
		State:       domain.WorkItemIngested,
	}
	repo.items["wi-1"] = item

	t.Run("updates mutable fields", func(t *testing.T) {
		updated := domain.WorkItem{
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
		if got.State != domain.WorkItemIngested {
			t.Errorf("State should be preserved, got %q", got.State)
		}
	})

	t.Run("update nonexistent returns error", func(t *testing.T) {
		updated := domain.WorkItem{ID: "nonexistent"}
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
