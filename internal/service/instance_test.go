package service

import (
	"context"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/domain"
)

func TestInstanceService_Create(t *testing.T) {
	ctx := context.Background()
	repo := NewMockInstanceRepository()
	svc := NewInstanceService(repo)

	inst := domain.SubstrateInstance{
		ID:          "inst-1",
		WorkspaceID: "ws-1",
		PID:         12345,
		Hostname:    "localhost",
	}
	if err := svc.Create(ctx, inst); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := svc.Get(ctx, "inst-1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.PID != 12345 {
		t.Errorf("PID = %d, want 12345", got.PID)
	}
	if got.LastHeartbeat.IsZero() {
		t.Error("LastHeartbeat should be set")
	}
	if got.StartedAt.IsZero() {
		t.Error("StartedAt should be set")
	}
}

func TestInstanceService_Get(t *testing.T) {
	ctx := context.Background()
	repo := NewMockInstanceRepository()
	svc := NewInstanceService(repo)

	inst := domain.SubstrateInstance{
		ID:          "inst-1",
		WorkspaceID: "ws-1",
		PID:         12345,
	}
	repo.instances["inst-1"] = inst

	t.Run("gets existing instance", func(t *testing.T) {
		got, err := svc.Get(ctx, "inst-1")
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if got.ID != "inst-1" {
			t.Errorf("ID = %q, want %q", got.ID, "inst-1")
		}
	})

	t.Run("returns error for nonexistent", func(t *testing.T) {
		_, err := svc.Get(ctx, "nonexistent")
		if err == nil {
			t.Fatal("expected error for nonexistent instance")
		}
		_, ok := err.(ErrNotFound)
		if !ok {
			t.Errorf("error type = %T, want ErrNotFound", err)
		}
	})
}

func TestInstanceService_ListByWorkspaceID(t *testing.T) {
	ctx := context.Background()
	repo := NewMockInstanceRepository()
	svc := NewInstanceService(repo)

	repo.instances["inst-1"] = domain.SubstrateInstance{ID: "inst-1", WorkspaceID: "ws-1"}
	repo.instances["inst-2"] = domain.SubstrateInstance{ID: "inst-2", WorkspaceID: "ws-1"}
	repo.instances["inst-3"] = domain.SubstrateInstance{ID: "inst-3", WorkspaceID: "ws-2"}
	repo.byWorkspace["ws-1"] = []string{"inst-1", "inst-2"}
	repo.byWorkspace["ws-2"] = []string{"inst-3"}

	instances, err := svc.ListByWorkspaceID(ctx, "ws-1")
	if err != nil {
		t.Fatalf("ListByWorkspaceID failed: %v", err)
	}

	if len(instances) != 2 {
		t.Errorf("got %d instances, want 2", len(instances))
	}
}

func TestInstanceService_UpdateHeartbeat(t *testing.T) {
	ctx := context.Background()
	repo := NewMockInstanceRepository()
	svc := NewInstanceService(repo)

	inst := domain.SubstrateInstance{
		ID:            "inst-1",
		WorkspaceID:   "ws-1",
		PID:           12345,
		LastHeartbeat: time.Now().Add(-10 * time.Second),
	}
	repo.instances["inst-1"] = inst

	oldHeartbeat := inst.LastHeartbeat
	time.Sleep(10 * time.Millisecond) // Small delay to ensure time difference

	if err := svc.UpdateHeartbeat(ctx, "inst-1"); err != nil {
		t.Fatalf("UpdateHeartbeat failed: %v", err)
	}

	got, _ := svc.Get(ctx, "inst-1")
	if !got.LastHeartbeat.After(oldHeartbeat) {
		t.Error("LastHeartbeat should be updated")
	}
}

func TestInstanceService_Delete(t *testing.T) {
	ctx := context.Background()
	repo := NewMockInstanceRepository()
	svc := NewInstanceService(repo)

	inst := domain.SubstrateInstance{
		ID:          "inst-1",
		WorkspaceID: "ws-1",
	}
	repo.instances["inst-1"] = inst

	t.Run("deletes existing instance", func(t *testing.T) {
		if err := svc.Delete(ctx, "inst-1"); err != nil {
			t.Fatalf("Delete failed: %v", err)
		}

		_, err := svc.Get(ctx, "inst-1")
		if err == nil {
			t.Fatal("expected error after deletion")
		}
	})

	t.Run("returns error for nonexistent", func(t *testing.T) {
		err := svc.Delete(ctx, "nonexistent")
		if err == nil {
			t.Fatal("expected error for nonexistent instance")
		}
		_, ok := err.(ErrNotFound)
		if !ok {
			t.Errorf("error type = %T, want ErrNotFound", err)
		}
	})
}

func TestInstanceService_IsAlive(t *testing.T) {
	ctx := context.Background()
	repo := NewMockInstanceRepository()
	svc := NewInstanceService(repo)

	threshold := 15 * time.Second

	t.Run("returns true for recent heartbeat", func(t *testing.T) {
		inst := domain.SubstrateInstance{
			ID:            "inst-1",
			WorkspaceID:   "ws-1",
			LastHeartbeat: time.Now(),
		}
		repo.instances["inst-1"] = inst

		alive, err := svc.IsAlive(ctx, "inst-1", threshold)
		if err != nil {
			t.Fatalf("IsAlive failed: %v", err)
		}
		if !alive {
			t.Error("expected true for recent heartbeat")
		}
	})

	t.Run("returns false for stale heartbeat", func(t *testing.T) {
		inst := domain.SubstrateInstance{
			ID:            "inst-2",
			WorkspaceID:   "ws-1",
			LastHeartbeat: time.Now().Add(-30 * time.Second),
		}
		repo.instances["inst-2"] = inst

		alive, err := svc.IsAlive(ctx, "inst-2", threshold)
		if err != nil {
			t.Fatalf("IsAlive failed: %v", err)
		}
		if alive {
			t.Error("expected false for stale heartbeat")
		}
	})
}

func TestInstanceService_FindStaleInstances(t *testing.T) {
	ctx := context.Background()
	repo := NewMockInstanceRepository()
	svc := NewInstanceService(repo)

	threshold := 15 * time.Second

	repo.instances["inst-1"] = domain.SubstrateInstance{ID: "inst-1", WorkspaceID: "ws-1", LastHeartbeat: time.Now()}
	repo.instances["inst-2"] = domain.SubstrateInstance{ID: "inst-2", WorkspaceID: "ws-1", LastHeartbeat: time.Now().Add(-30 * time.Second)}
	repo.instances["inst-3"] = domain.SubstrateInstance{ID: "inst-3", WorkspaceID: "ws-1", LastHeartbeat: time.Now().Add(-60 * time.Second)}
	repo.byWorkspace["ws-1"] = []string{"inst-1", "inst-2", "inst-3"}

	stale, err := svc.FindStaleInstances(ctx, "ws-1", threshold)
	if err != nil {
		t.Fatalf("FindStaleInstances failed: %v", err)
	}

	if len(stale) != 2 {
		t.Errorf("got %d stale instances, want 2", len(stale))
	}
}

func TestInstanceService_CleanupStaleInstances(t *testing.T) {
	ctx := context.Background()
	repo := NewMockInstanceRepository()
	svc := NewInstanceService(repo)

	threshold := 15 * time.Second

	repo.instances["inst-1"] = domain.SubstrateInstance{ID: "inst-1", WorkspaceID: "ws-1", LastHeartbeat: time.Now()}
	repo.instances["inst-2"] = domain.SubstrateInstance{ID: "inst-2", WorkspaceID: "ws-1", LastHeartbeat: time.Now().Add(-30 * time.Second)}
	repo.instances["inst-3"] = domain.SubstrateInstance{ID: "inst-3", WorkspaceID: "ws-1", LastHeartbeat: time.Now().Add(-60 * time.Second)}
	repo.byWorkspace["ws-1"] = []string{"inst-1", "inst-2", "inst-3"}

	count, err := svc.CleanupStaleInstances(ctx, "ws-1", threshold)
	if err != nil {
		t.Fatalf("CleanupStaleInstances failed: %v", err)
	}

	if count != 2 {
		t.Errorf("cleaned up %d instances, want 2", count)
	}

	// Verify only alive instance remains
	remaining, _ := svc.ListByWorkspaceID(ctx, "ws-1")
	if len(remaining) != 1 {
		t.Errorf("got %d remaining instances, want 1", len(remaining))
	}
	if remaining[0].ID != "inst-1" {
		t.Errorf("remaining instance ID = %q, want %q", remaining[0].ID, "inst-1")
	}
}
