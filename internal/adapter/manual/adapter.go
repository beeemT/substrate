package manual

import (
	"context"
	"fmt"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

// ErrNotSupported is returned for operations not supported by the manual adapter.
var ErrNotSupported = fmt.Errorf("operation not supported by manual adapter")

// WorkspaceStore provides the minimal DB access needed by ManualAdapter.
// It is typically satisfied by a WorkItemRepository from the enclosing Transact call.
type WorkspaceStore interface {
	// CountManualWorkItems returns the count of work items with source="manual"
	// and workspace_id matching the provided workspace ID.
	CountManualWorkItems(ctx context.Context, workspaceID string) (int, error)
}

// workItemStoreAdapter wraps a SessionRepository to satisfy WorkspaceStore.
type workItemStoreAdapter struct {
	repo        repository.SessionRepository
	workspaceID string
}

// NewWorkspaceStore constructs a WorkspaceStore backed by a SessionRepository.
func NewWorkspaceStore(repo repository.SessionRepository, workspaceID string) WorkspaceStore {
	return &workItemStoreAdapter{repo: repo, workspaceID: workspaceID}
}

func (s *workItemStoreAdapter) CountManualWorkItems(ctx context.Context, workspaceID string) (int, error) {
	src := "manual"
	wsID := workspaceID
	items, err := s.repo.List(ctx, repository.SessionFilter{
		WorkspaceID: &wsID,
		Source:      &src,
		Limit:       10000, // safe upper bound for count
	})
	if err != nil {
		return 0, fmt.Errorf("count manual work items: %w", err)
	}
	return len(items), nil
}

// ManualAdapter implements adapter.WorkItemAdapter for manually entered work items.
type ManualAdapter struct {
	store       WorkspaceStore
	workspaceID string
}

// New constructs a ManualAdapter.
func New(store WorkspaceStore, workspaceID string) *ManualAdapter {
	return &ManualAdapter{store: store, workspaceID: workspaceID}
}

// Name returns the adapter identifier.
func (a *ManualAdapter) Name() string { return "manual" }

// Capabilities returns the manual adapter's capability set.
func (a *ManualAdapter) Capabilities() adapter.AdapterCapabilities {
	return adapter.AdapterCapabilities{
		CanWatch:     false,
		CanBrowse:    false,
		CanMutate:    false,
		BrowseScopes: nil,
	}
}

// ListSelectable is not supported by the manual adapter.
func (a *ManualAdapter) ListSelectable(_ context.Context, _ adapter.ListOpts) (*adapter.ListResult, error) {
	return nil, adapter.ErrBrowseNotSupported
}

// Resolve converts a manual selection into a WorkItem with a stable ExternalID.
func (a *ManualAdapter) Resolve(ctx context.Context, sel adapter.Selection) (domain.Session, error) {
	if sel.Manual == nil {
		return domain.Session{}, fmt.Errorf("manual adapter requires sel.Manual to be set")
	}

	n, err := a.store.CountManualWorkItems(ctx, a.workspaceID)
	if err != nil {
		return domain.Session{}, fmt.Errorf("resolve manual work item: %w", err)
	}

	externalID := fmt.Sprintf("MAN-%d", n+1)
	now := domain.Now()

	return domain.Session{
		ID:          domain.NewID(),
		ExternalID:  externalID,
		Source:      "manual",
		SourceScope: domain.ScopeManual,
		Title:       sel.Manual.Title,
		Description: sel.Manual.Description,
		State:       domain.SessionIngested,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

// Watch returns a closed channel; the manual adapter never auto-discovers work items.
func (a *ManualAdapter) Watch(_ context.Context, _ adapter.WorkItemFilter) (<-chan adapter.WorkItemEvent, error) {
	ch := make(chan adapter.WorkItemEvent)
	close(ch)
	return ch, nil
}

// Fetch is not supported; manual work items have no external tracker to sync from.
func (a *ManualAdapter) Fetch(_ context.Context, _ string) (domain.Session, error) {
	return domain.Session{}, ErrNotSupported
}

// UpdateState is a no-op for the manual adapter.
func (a *ManualAdapter) UpdateState(_ context.Context, _ string, _ domain.TrackerState) error {
	return nil
}

// AddComment is a no-op for the manual adapter.
func (a *ManualAdapter) AddComment(_ context.Context, _ string, _ string) error {
	return nil
}

// OnEvent is a no-op for the manual adapter.
func (a *ManualAdapter) OnEvent(_ context.Context, _ domain.SystemEvent) error {
	return nil
}
