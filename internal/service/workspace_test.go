package service

import (
	"context"
	"testing"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

func TestWorkspaceService_Create(t *testing.T) {
	ctx := context.Background()
	repo := NewMockWorkspaceRepository()
	svc := NewWorkspaceService(repository.NoopTransacter{Res: repository.Resources{Workspaces: repo}})

	t.Run("creates workspace with creating status", func(t *testing.T) {
		ws := domain.Workspace{
			ID:       "ws-1",
			Name:     "Test Workspace",
			RootPath: "/path/to/workspace",
		}
		if err := svc.Create(ctx, ws); err != nil {
			t.Fatalf("Create failed: %v", err)
		}

		got, err := svc.Get(ctx, "ws-1")
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if got.Status != domain.WorkspaceCreating {
			t.Errorf("Status = %q, want %q", got.Status, domain.WorkspaceCreating)
		}
	})
}

func TestWorkspaceService_ValidTransitions(t *testing.T) {
	ctx := context.Background()

	validTransitions := []struct {
		from domain.WorkspaceStatus
		to   domain.WorkspaceStatus
		name string
	}{
		{domain.WorkspaceCreating, domain.WorkspaceReady, "creating -> ready"},
		{domain.WorkspaceCreating, domain.WorkspaceError, "creating -> error"},
		{domain.WorkspaceReady, domain.WorkspaceArchived, "ready -> archived"},
		{domain.WorkspaceError, domain.WorkspaceReady, "error -> ready"},
	}

	for _, tc := range validTransitions {
		t.Run(tc.name, func(t *testing.T) {
			repo := NewMockWorkspaceRepository()
			svc := NewWorkspaceService(repository.NoopTransacter{Res: repository.Resources{Workspaces: repo}})

			ws := domain.Workspace{
				ID:       "ws-test",
				Name:     "Test",
				RootPath: "/path",
				Status:   tc.from,
			}
			repo.workspaces["ws-test"] = ws

			if err := svc.Transition(ctx, "ws-test", tc.to); err != nil {
				t.Fatalf("Transition from %s to %s failed: %v", tc.from, tc.to, err)
			}

			got, err := svc.Get(ctx, "ws-test")
			if err != nil {
				t.Fatalf("Get failed: %v", err)
			}
			if got.Status != tc.to {
				t.Errorf("Status = %q, want %q", got.Status, tc.to)
			}
		})
	}
}

func TestWorkspaceService_InvalidTransitions(t *testing.T) {
	ctx := context.Background()

	invalidTransitions := []struct {
		from domain.WorkspaceStatus
		to   domain.WorkspaceStatus
		name string
	}{
		{domain.WorkspaceCreating, domain.WorkspaceArchived, "creating -> archived"},
		{domain.WorkspaceReady, domain.WorkspaceCreating, "ready -> creating"},
		{domain.WorkspaceReady, domain.WorkspaceError, "ready -> error"},
		{domain.WorkspaceArchived, domain.WorkspaceReady, "archived -> ready"},
		{domain.WorkspaceArchived, domain.WorkspaceCreating, "archived -> creating"},
		{domain.WorkspaceError, domain.WorkspaceArchived, "error -> archived"},
		{domain.WorkspaceError, domain.WorkspaceCreating, "error -> creating"},
	}

	for _, tc := range invalidTransitions {
		t.Run(tc.name, func(t *testing.T) {
			repo := NewMockWorkspaceRepository()
			svc := NewWorkspaceService(repository.NoopTransacter{Res: repository.Resources{Workspaces: repo}})

			ws := domain.Workspace{
				ID:       "ws-test",
				Name:     "Test",
				RootPath: "/path",
				Status:   tc.from,
			}
			repo.workspaces["ws-test"] = ws

			err := svc.Transition(ctx, "ws-test", tc.to)
			if err == nil {
				t.Fatalf("expected error for transition from %s to %s", tc.from, tc.to)
			}
			if _, ok := err.(ErrInvalidTransition); !ok {
				t.Errorf("error type = %T, want ErrInvalidTransition", err)
			}
		})
	}
}

func TestWorkspaceService_ConvenienceMethods(t *testing.T) {
	ctx := context.Background()
	repo := NewMockWorkspaceRepository()
	svc := NewWorkspaceService(repository.NoopTransacter{Res: repository.Resources{Workspaces: repo}})

	t.Run("MarkReady", func(t *testing.T) {
		ws := domain.Workspace{ID: "ws-1", Status: domain.WorkspaceCreating}
		repo.workspaces["ws-1"] = ws
		if err := svc.MarkReady(ctx, "ws-1"); err != nil {
			t.Fatalf("MarkReady failed: %v", err)
		}
		got, _ := svc.Get(ctx, "ws-1")
		if got.Status != domain.WorkspaceReady {
			t.Errorf("Status = %q, want %q", got.Status, domain.WorkspaceReady)
		}
	})

	t.Run("MarkError", func(t *testing.T) {
		ws := domain.Workspace{ID: "ws-2", Status: domain.WorkspaceCreating}
		repo.workspaces["ws-2"] = ws
		if err := svc.MarkError(ctx, "ws-2"); err != nil {
			t.Fatalf("MarkError failed: %v", err)
		}
		got, _ := svc.Get(ctx, "ws-2")
		if got.Status != domain.WorkspaceError {
			t.Errorf("Status = %q, want %q", got.Status, domain.WorkspaceError)
		}
	})

	t.Run("Archive", func(t *testing.T) {
		ws := domain.Workspace{ID: "ws-3", Status: domain.WorkspaceReady}
		repo.workspaces["ws-3"] = ws
		if err := svc.Archive(ctx, "ws-3"); err != nil {
			t.Fatalf("Archive failed: %v", err)
		}
		got, _ := svc.Get(ctx, "ws-3")
		if got.Status != domain.WorkspaceArchived {
			t.Errorf("Status = %q, want %q", got.Status, domain.WorkspaceArchived)
		}
	})

	t.Run("Recover", func(t *testing.T) {
		ws := domain.Workspace{ID: "ws-4", Status: domain.WorkspaceError}
		repo.workspaces["ws-4"] = ws
		if err := svc.Recover(ctx, "ws-4"); err != nil {
			t.Fatalf("Recover failed: %v", err)
		}
		got, _ := svc.Get(ctx, "ws-4")
		if got.Status != domain.WorkspaceReady {
			t.Errorf("Status = %q, want %q", got.Status, domain.WorkspaceReady)
		}
	})
}

func TestWorkspaceService_NotFound(t *testing.T) {
	ctx := context.Background()
	repo := NewMockWorkspaceRepository()
	svc := NewWorkspaceService(repository.NoopTransacter{Res: repository.Resources{Workspaces: repo}})

	t.Run("Get not found", func(t *testing.T) {
		_, err := svc.Get(ctx, "nonexistent")
		if err == nil {
			t.Fatal("expected error for nonexistent workspace")
		}
		_, ok := err.(ErrNotFound)
		if !ok {
			t.Errorf("error type = %T, want ErrNotFound", err)
		}
	})

	t.Run("Delete not found", func(t *testing.T) {
		err := svc.Delete(ctx, "nonexistent")
		if err == nil {
			t.Fatal("expected error for nonexistent workspace")
		}
		_, ok := err.(ErrNotFound)
		if !ok {
			t.Errorf("error type = %T, want ErrNotFound", err)
		}
	})
}
