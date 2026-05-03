// Package worktree provides worktree lifecycle management and hooks.
package worktree

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// DefaultHookTimeout is the default timeout for pre-hook execution.
const DefaultHookTimeout = 30 * time.Second

// CheckoutRequest describes a requested worktree checkout.
type CheckoutRequest struct {
	WorkspaceID   string
	WorkItemID    string
	Repository    string
	Branch        string
	WorkItemTitle string
}

// PreHook is a synchronous hook called before a worktree checkout.
// If any hook returns an error, the checkout is aborted.
type PreHook func(ctx context.Context, req CheckoutRequest) error

// HookConfig configures a hook's behavior.
type HookConfig struct {
	Name    string
	Timeout time.Duration // 0 means DefaultHookTimeout
}

type hookEntry struct {
	config HookConfig
	hook   PreHook
}

// HookRegistry manages pre-checkout hooks.
type HookRegistry struct {
	mu    sync.RWMutex
	hooks []hookEntry
}

// NewHookRegistry creates a new empty hook registry.
func NewHookRegistry() *HookRegistry {
	return &HookRegistry{}
}

// Register adds a pre-hook to the registry. Hooks are called in registration order.
func (r *HookRegistry) Register(cfg HookConfig, hook PreHook) {
	if hook == nil {
		panic("worktree: Register called with nil hook")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultHookTimeout
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks = append(r.hooks, hookEntry{config: cfg, hook: hook})
}

// Run executes all registered pre-hooks against the given request.
// Returns the first non-nil error from a hook, or nil if all hooks pass.
func (r *HookRegistry) Run(ctx context.Context, req CheckoutRequest) error {
	r.mu.RLock()
	hooks := r.hooks
	r.mu.RUnlock()

	for _, entry := range hooks {
		hookCtx, cancel := context.WithTimeout(ctx, entry.config.Timeout)
		err := entry.hook(hookCtx, req)
		cancel()
		if err != nil {
			slog.Warn("pre-checkout hook rejected",
				"name", entry.config.Name,
				"error", err,
				"workspace_id", req.WorkspaceID,
				"work_item_id", req.WorkItemID,
				"repository", req.Repository,
				"branch", req.Branch,
			)
			return err
		}
	}
	return nil
}
