package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/service"
)

const (
	heartbeatInterval = 5 * time.Second
	staleThreshold    = 15 * time.Second
)

// InstanceManager registers the current Substrate process in the DB,
// maintains a liveness heartbeat, and reconciles orphaned sessions on startup.
type InstanceManager struct {
	instanceSvc *service.InstanceService
	sessionSvc  *service.TaskService
	eventBus    *event.Bus

	instanceID  string
	workspaceID string
	stopCh      chan struct{}
	wg          sync.WaitGroup
}

// NewInstanceManager creates a new InstanceManager.
func NewInstanceManager(
	instanceSvc *service.InstanceService,
	sessionSvc *service.TaskService,
	eventBus *event.Bus,
) *InstanceManager {
	return &InstanceManager{
		instanceSvc: instanceSvc,
		sessionSvc:  sessionSvc,
		eventBus:    eventBus,
		stopCh:      make(chan struct{}),
	}
}

// InstanceID returns the DB-registered ID for this process.
func (m *InstanceManager) InstanceID() string { return m.instanceID }

// Start registers this process as a substrate instance, runs startup reconciliation
// to mark orphaned sessions as interrupted, and launches the heartbeat goroutine.
// Must be paired with GracefulShutdown.
func (m *InstanceManager) Start(ctx context.Context, workspaceID string) error {
	hostname, _ := os.Hostname()
	m.workspaceID = workspaceID
	m.instanceID = domain.NewID()

	inst := domain.SubstrateInstance{
		ID:          m.instanceID,
		WorkspaceID: workspaceID,
		PID:         os.Getpid(),
		Hostname:    hostname,
	}
	if err := m.instanceSvc.Create(ctx, inst); err != nil {
		return fmt.Errorf("register instance: %w", err)
	}

	// Non-fatal: log reconciliation errors and continue.
	if err := m.Reconcile(ctx); err != nil {
		slog.Warn("startup reconciliation had errors", "error", err)
	}

	m.wg.Add(1)
	go m.heartbeatLoop()

	return nil
}

// Reconcile scans all running/waiting sessions in this workspace. Any session whose
// owner instance is absent from the DB or has a stale heartbeat (>15s) is
// transitioned to interrupted and EventAgentSessionInterrupted is emitted.
// The current instance's own sessions are never interrupted.
func (m *InstanceManager) Reconcile(ctx context.Context) error {
	sessions, err := m.sessionSvc.ListByWorkspaceID(ctx, m.workspaceID)
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}

	// Build a set of live instance IDs (heartbeat within threshold).
	instances, err := m.instanceSvc.ListByWorkspaceID(ctx, m.workspaceID)
	if err != nil {
		return fmt.Errorf("list instances: %w", err)
	}
	liveSet := make(map[string]bool, len(instances))
	for _, inst := range instances {
		if time.Since(inst.LastHeartbeat) <= staleThreshold {
			liveSet[inst.ID] = true
		}
	}

	var firstErr error
	for _, s := range sessions {
		if s.Status != domain.AgentSessionRunning && s.Status != domain.AgentSessionWaitingForAnswer {
			continue
		}
		// Skip sessions owned by this (freshly started) instance.
		if s.OwnerInstanceID != nil && *s.OwnerInstanceID == m.instanceID {
			continue
		}
		// Owner absent or stale → orphaned.
		ownerAlive := s.OwnerInstanceID != nil && liveSet[*s.OwnerInstanceID]
		if ownerAlive {
			continue
		}

		if err := m.sessionSvc.Interrupt(ctx, s.ID); err != nil {
			slog.Error("failed to interrupt orphaned session", "session_id", s.ID, "error", err)
			if firstErr == nil {
				firstErr = err
			}

			continue
		}
		slog.Info("reconciled orphaned session as interrupted", "session_id", s.ID)

		m.publishInterrupted(ctx, s.ID)
	}

	return firstErr
}

// GracefulShutdown marks all running sessions owned by this instance as interrupted,
// stops the heartbeat goroutine, and deletes the instance record from the DB.
// Call this on SIGINT/SIGTERM before the process exits.
func (m *InstanceManager) GracefulShutdown(ctx context.Context) error {
	// Signal the heartbeat goroutine to stop and wait for it.
	close(m.stopCh)
	m.wg.Wait()

	// Interrupt owned running sessions before removing the instance row.
	running, err := m.sessionSvc.FindRunningByOwner(ctx, m.instanceID)
	if err != nil {
		slog.Warn("failed to list owned sessions during shutdown", "error", err)
	}
	for _, s := range running {
		if err := m.sessionSvc.Interrupt(ctx, s.ID); err != nil {
			slog.Error("failed to interrupt session during shutdown", "session_id", s.ID, "error", err)

			continue
		}
		m.publishInterrupted(ctx, s.ID)
	}

	// Remove the instance row so other instances do not wait for our heartbeat.
	if err := m.instanceSvc.Delete(ctx, m.instanceID); err != nil {
		return fmt.Errorf("delete instance on shutdown: %w", err)
	}

	return nil
}

// heartbeatLoop ticks every heartbeatInterval and updates LastHeartbeat in the DB.
func (m *InstanceManager) heartbeatLoop() {
	defer m.wg.Done()
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := m.instanceSvc.UpdateHeartbeat(ctx, m.instanceID); err != nil {
				slog.Warn("heartbeat update failed", "instance_id", m.instanceID, "error", err)
			}
			cancel()
		}
	}
}

func (m *InstanceManager) publishInterrupted(ctx context.Context, sessionID string) {
	if m.eventBus == nil {
		return
	}
	_ = m.eventBus.Publish(ctx, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventAgentSessionInterrupted),
		WorkspaceID: m.workspaceID,
		Payload:     marshalJSONOrEmpty(string(domain.EventAgentSessionInterrupted), map[string]any{"session_id": sessionID}),
		CreatedAt:   time.Now(),
	})
}
