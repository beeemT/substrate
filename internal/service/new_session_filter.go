package service

import (
	"context"
	"strings"
	"time"

	"github.com/beeemT/go-atomic"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

const defaultNewSessionFilterLease = 30 * time.Second

// SessionFilterService provides business logic for saved New Session filters.
type SessionFilterService struct {
	transacter atomic.Transacter[repository.Resources]
}

// NewSessionFilterService creates a SessionFilterService.
func NewSessionFilterService(transacter atomic.Transacter[repository.Resources]) *SessionFilterService {
	return &SessionFilterService{transacter: transacter}
}

func (s *SessionFilterService) Get(ctx context.Context, id string) (domain.NewSessionFilter, error) {
	var result domain.NewSessionFilter
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		filter, err := res.NewSessionFilters.Get(ctx, id)
		if err != nil {
			return newNotFoundError("new session filter", id)
		}
		result = filter
		return nil
	})
	return result, err
}

func (s *SessionFilterService) GetByWorkspaceProviderName(ctx context.Context, workspaceID, provider, name string) (domain.NewSessionFilter, error) {
	var result domain.NewSessionFilter
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		filter, err := res.NewSessionFilters.GetByWorkspaceProviderName(ctx, workspaceID, provider, name)
		if err != nil {
			return newNotFoundError("new session filter", workspaceID+"/"+provider+"/"+name)
		}
		result = filter
		return nil
	})
	return result, err
}

func (s *SessionFilterService) ListByWorkspaceID(ctx context.Context, workspaceID string) ([]domain.NewSessionFilter, error) {
	var result []domain.NewSessionFilter
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		filters, err := res.NewSessionFilters.ListByWorkspaceID(ctx, workspaceID)
		if err != nil {
			return err
		}
		result = filters
		return nil
	})
	return result, err
}

func (s *SessionFilterService) ListByWorkspaceProvider(ctx context.Context, workspaceID, provider string) ([]domain.NewSessionFilter, error) {
	var result []domain.NewSessionFilter
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		filters, err := res.NewSessionFilters.ListByWorkspaceProvider(ctx, workspaceID, provider)
		if err != nil {
			return err
		}
		result = filters
		return nil
	})
	return result, err
}

func (s *SessionFilterService) Create(ctx context.Context, filter domain.NewSessionFilter) error {
	if strings.TrimSpace(filter.ID) == "" {
		return newInvalidInputError("id is required", "id")
	}
	if strings.TrimSpace(filter.WorkspaceID) == "" {
		return newInvalidInputError("workspace_id is required", "workspace_id")
	}
	if strings.TrimSpace(filter.Name) == "" {
		return newInvalidInputError("name is required", "name")
	}
	if strings.TrimSpace(filter.Provider) == "" {
		return newInvalidInputError("provider is required", "provider")
	}
	now := time.Now().UTC()
	filter.CreatedAt = now
	filter.UpdatedAt = now
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		return res.NewSessionFilters.Create(ctx, filter)
	})
}

func (s *SessionFilterService) Update(ctx context.Context, filter domain.NewSessionFilter) error {
	if strings.TrimSpace(filter.ID) == "" {
		return newInvalidInputError("id is required", "id")
	}
	if strings.TrimSpace(filter.WorkspaceID) == "" {
		return newInvalidInputError("workspace_id is required", "workspace_id")
	}
	if strings.TrimSpace(filter.Name) == "" {
		return newInvalidInputError("name is required", "name")
	}
	if strings.TrimSpace(filter.Provider) == "" {
		return newInvalidInputError("provider is required", "provider")
	}
	filter.UpdatedAt = time.Now().UTC()
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		return res.NewSessionFilters.Update(ctx, filter)
	})
}

func (s *SessionFilterService) Delete(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return newInvalidInputError("id is required", "id")
	}
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		return res.NewSessionFilters.Delete(ctx, id)
	})
}

// SessionFilterLockService coordinates lock leases for active New Session filters.
type SessionFilterLockService struct {
	transacter atomic.Transacter[repository.Resources]
}

// NewSessionFilterLockService creates a SessionFilterLockService.
func NewSessionFilterLockService(transacter atomic.Transacter[repository.Resources]) *SessionFilterLockService {
	return &SessionFilterLockService{transacter: transacter}
}

func (s *SessionFilterLockService) Acquire(ctx context.Context, filterID, instanceID string, leaseDuration time.Duration) (domain.NewSessionFilterLock, bool, error) {
	if strings.TrimSpace(filterID) == "" {
		return domain.NewSessionFilterLock{}, false, newInvalidInputError("filter_id is required", "filter_id")
	}
	if strings.TrimSpace(instanceID) == "" {
		return domain.NewSessionFilterLock{}, false, newInvalidInputError("instance_id is required", "instance_id")
	}
	if leaseDuration <= 0 {
		leaseDuration = defaultNewSessionFilterLease
	}
	now := time.Now().UTC()
	request := domain.NewSessionFilterLock{
		FilterID:       filterID,
		InstanceID:     instanceID,
		LeaseExpiresAt: now.Add(leaseDuration),
		AcquiredAt:     now,
		UpdatedAt:      now,
	}
	var (
		current  domain.NewSessionFilterLock
		acquired bool
	)
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		var err error
		current, acquired, err = res.NewSessionFilterLocks.Acquire(ctx, request)
		return err
	})
	return current, acquired, err
}

func (s *SessionFilterLockService) Renew(ctx context.Context, filterID, instanceID string, leaseDuration time.Duration) (domain.NewSessionFilterLock, bool, error) {
	if strings.TrimSpace(filterID) == "" {
		return domain.NewSessionFilterLock{}, false, newInvalidInputError("filter_id is required", "filter_id")
	}
	if strings.TrimSpace(instanceID) == "" {
		return domain.NewSessionFilterLock{}, false, newInvalidInputError("instance_id is required", "instance_id")
	}
	if leaseDuration <= 0 {
		leaseDuration = defaultNewSessionFilterLease
	}
	now := time.Now().UTC()
	request := domain.NewSessionFilterLock{
		FilterID:       filterID,
		InstanceID:     instanceID,
		LeaseExpiresAt: now.Add(leaseDuration),
		UpdatedAt:      now,
	}
	var (
		current domain.NewSessionFilterLock
		renewed bool
	)
	err := s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		var err error
		current, renewed, err = res.NewSessionFilterLocks.Renew(ctx, request)
		return err
	})
	return current, renewed, err
}

func (s *SessionFilterLockService) Release(ctx context.Context, filterID, instanceID string) error {
	if strings.TrimSpace(filterID) == "" {
		return newInvalidInputError("filter_id is required", "filter_id")
	}
	if strings.TrimSpace(instanceID) == "" {
		return newInvalidInputError("instance_id is required", "instance_id")
	}
	return s.transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
		return res.NewSessionFilterLocks.Release(ctx, filterID, instanceID)
	})
}
