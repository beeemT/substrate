package service

import (
	"context"
	"testing"

	"github.com/beeemT/substrate/internal/domain"
)

func TestDocumentationService_Create(t *testing.T) {
	ctx := context.Background()
	repo := NewMockDocumentationRepository()
	svc := NewDocumentationService(repo)

	t.Run("creates repo embedded source", func(t *testing.T) {
		ds := domain.DocumentationSource{
			ID:             "ds-1",
			WorkspaceID:    "ws-1",
			RepositoryName: "repo1",
			Type:           domain.DocSourceRepoEmbedded,
			Path:           "docs/",
		}
		if err := svc.Create(ctx, ds); err != nil {
			t.Fatalf("Create failed: %v", err)
		}

		got, err := svc.Get(ctx, "ds-1")
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if got.Type != domain.DocSourceRepoEmbedded {
			t.Errorf("Type = %q, want %q", got.Type, domain.DocSourceRepoEmbedded)
		}
	})

	t.Run("creates dedicated repo source", func(t *testing.T) {
		ds := domain.DocumentationSource{
			ID:          "ds-2",
			WorkspaceID: "ws-1",
			Type:        domain.DocSourceDedicatedRepo,
			RepoURL:     "https://github.com/org/docs",
			Branch:      "main",
		}
		if err := svc.Create(ctx, ds); err != nil {
			t.Fatalf("Create failed: %v", err)
		}
	})

	t.Run("rejects invalid type", func(t *testing.T) {
		ds := domain.DocumentationSource{
			ID:          "ds-3",
			WorkspaceID: "ws-1",
			Type:        "invalid",
		}
		err := svc.Create(ctx, ds)
		if err == nil {
			t.Fatal("expected error for invalid type")
		}
		_, ok := err.(ErrInvalidInput)
		if !ok {
			t.Errorf("error type = %T, want ErrInvalidInput", err)
		}
	})
}

func TestDocumentationService_Get(t *testing.T) {
	ctx := context.Background()
	repo := NewMockDocumentationRepository()
	svc := NewDocumentationService(repo)

	ds := domain.DocumentationSource{
		ID:          "ds-1",
		WorkspaceID: "ws-1",
		Type:        domain.DocSourceRepoEmbedded,
	}
	repo.sources["ds-1"] = ds

	t.Run("gets existing source", func(t *testing.T) {
		got, err := svc.Get(ctx, "ds-1")
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if got.ID != "ds-1" {
			t.Errorf("ID = %q, want %q", got.ID, "ds-1")
		}
	})

	t.Run("returns error for nonexistent", func(t *testing.T) {
		_, err := svc.Get(ctx, "nonexistent")
		if err == nil {
			t.Fatal("expected error for nonexistent source")
		}
		_, ok := err.(ErrNotFound)
		if !ok {
			t.Errorf("error type = %T, want ErrNotFound", err)
		}
	})
}

func TestDocumentationService_ListByWorkspaceID(t *testing.T) {
	ctx := context.Background()
	repo := NewMockDocumentationRepository()
	svc := NewDocumentationService(repo)

	repo.sources["ds-1"] = domain.DocumentationSource{ID: "ds-1", WorkspaceID: "ws-1"}
	repo.sources["ds-2"] = domain.DocumentationSource{ID: "ds-2", WorkspaceID: "ws-1"}
	repo.sources["ds-3"] = domain.DocumentationSource{ID: "ds-3", WorkspaceID: "ws-2"}
	repo.byWorkspace["ws-1"] = []string{"ds-1", "ds-2"}
	repo.byWorkspace["ws-2"] = []string{"ds-3"}

	sources, err := svc.ListByWorkspaceID(ctx, "ws-1")
	if err != nil {
		t.Fatalf("ListByWorkspaceID failed: %v", err)
	}

	if len(sources) != 2 {
		t.Errorf("got %d sources, want 2", len(sources))
	}
}

func TestDocumentationService_Update(t *testing.T) {
	ctx := context.Background()
	repo := NewMockDocumentationRepository()
	svc := NewDocumentationService(repo)

	ds := domain.DocumentationSource{
		ID:          "ds-1",
		WorkspaceID: "ws-1",
		Type:        domain.DocSourceRepoEmbedded,
		Path:        "docs/",
	}
	repo.sources["ds-1"] = ds

	t.Run("updates existing source", func(t *testing.T) {
		updated := domain.DocumentationSource{
			ID:          "ds-1",
			Path:        "documentation/",
			Description: "Updated description",
		}
		if err := svc.Update(ctx, updated); err != nil {
			t.Fatalf("Update failed: %v", err)
		}

		got, _ := svc.Get(ctx, "ds-1")
		if got.Path != "documentation/" {
			t.Errorf("Path = %q, want %q", got.Path, "documentation/")
		}
	})

	t.Run("returns error for nonexistent", func(t *testing.T) {
		updated := domain.DocumentationSource{ID: "nonexistent"}
		err := svc.Update(ctx, updated)
		if err == nil {
			t.Fatal("expected error for nonexistent source")
		}
		_, ok := err.(ErrNotFound)
		if !ok {
			t.Errorf("error type = %T, want ErrNotFound", err)
		}
	})
}

func TestDocumentationService_UpdateLastSynced(t *testing.T) {
	ctx := context.Background()
	repo := NewMockDocumentationRepository()
	svc := NewDocumentationService(repo)

	ds := domain.DocumentationSource{
		ID:          "ds-1",
		WorkspaceID: "ws-1",
		Type:        domain.DocSourceRepoEmbedded,
	}
	repo.sources["ds-1"] = ds

	if err := svc.UpdateLastSynced(ctx, "ds-1"); err != nil {
		t.Fatalf("UpdateLastSynced failed: %v", err)
	}

	got, _ := svc.Get(ctx, "ds-1")
	if got.LastSyncedAt == nil {
		t.Error("LastSyncedAt should be set")
	}
}

func TestDocumentationService_Delete(t *testing.T) {
	ctx := context.Background()
	repo := NewMockDocumentationRepository()
	svc := NewDocumentationService(repo)

	ds := domain.DocumentationSource{
		ID:          "ds-1",
		WorkspaceID: "ws-1",
		Type:        domain.DocSourceRepoEmbedded,
	}
	repo.sources["ds-1"] = ds

	t.Run("deletes existing source", func(t *testing.T) {
		if err := svc.Delete(ctx, "ds-1"); err != nil {
			t.Fatalf("Delete failed: %v", err)
		}

		_, err := svc.Get(ctx, "ds-1")
		if err == nil {
			t.Fatal("expected error after deletion")
		}
	})

	t.Run("returns error for nonexistent", func(t *testing.T) {
		err := svc.Delete(ctx, "nonexistent")
		if err == nil {
			t.Fatal("expected error for nonexistent source")
		}
		_, ok := err.(ErrNotFound)
		if !ok {
			t.Errorf("error type = %T, want ErrNotFound", err)
		}
	})
}
