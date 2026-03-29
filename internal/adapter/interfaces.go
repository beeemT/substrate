package adapter

import (
	"context"

	"github.com/beeemT/substrate/internal/domain"
)

// WorkItemAdapter provides access to external work item tracking systems.
// Implementations include Linear, GitHub Issues, and manual input.
type WorkItemAdapter interface {
	// Name returns the adapter's identifier (e.g., "linear", "manual").
	Name() string

	// Capabilities describes what this adapter supports.
	Capabilities() AdapterCapabilities

	// ListSelectable returns items available for interactive selection.
	// Used by the New Session wizard to browse available work items.
	// Returns ErrBrowseNotSupported if CanBrowse is false.
	ListSelectable(ctx context.Context, opts ListOpts) (*ListResult, error)

	// Resolve converts a user's selection into a WorkItem.
	// For multi-item selections (e.g., multiple issues), this aggregates
	// them into a single comprehensive WorkItem.
	Resolve(ctx context.Context, selection Selection) (domain.Session, error)

	// Watch returns a channel that emits work item changes.
	// Used for reactive auto-assignment when new work appears.
	// Returns ErrWatchNotSupported if CanWatch is false.
	// The returned channel is closed when the context is canceled.
	Watch(ctx context.Context, filter WorkItemFilter) (<-chan WorkItemEvent, error)
	// Fetch retrieves a work item by its external ID.
	Fetch(ctx context.Context, externalID string) (domain.Session, error)

	// UpdateState updates the work item's state in the external tracker.
	// Maps TrackerState to the tracker's native states.
	// Returns ErrMutateNotSupported if CanMutate is false.
	UpdateState(ctx context.Context, externalID string, state domain.TrackerState) error
	// AddComment adds a comment to the work item in the external tracker.
	// Returns ErrMutateNotSupported if CanMutate is false.
	AddComment(ctx context.Context, externalID string, body string) error
	// OnEvent handles system events, allowing the adapter to react to
	// state changes (e.g., update Linear when PlanApproved fires).
	OnEvent(ctx context.Context, event domain.SystemEvent) error
}

// RepoLifecycleAdapter handles repository-level events like worktree creation.
// Implementations include glab for MR creation, GitHub for PRs, etc.
type RepoLifecycleAdapter interface {
	// Name returns the adapter's identifier (e.g., "glab", "github").
	Name() string

	// OnEvent handles system events for repository lifecycle management.
	// Typically reacts to WorktreeCreated to create MRs/PRs.
	OnEvent(ctx context.Context, event domain.SystemEvent) error
}

// AgentHarness manages agent session lifecycle.
// Implementations wrap external agent systems like oh-my-pi.
type AgentHarness interface {
	// Name returns the harness identifier (e.g., "omp", "mock").
	Name() string

	// StartSession spawns a new agent session with the given options.
	// Returns an AgentSession for interacting with the running agent.
	StartSession(ctx context.Context, opts SessionOpts) (AgentSession, error)

	// SupportsCompact reports whether the harness supports manual compaction.
	// Used by the orchestrator to decide whether to resume a native session
	// (with compact) or start a fresh session for reimplementation.
	SupportsCompact() bool
}

// HarnessActionRunner executes structured harness control-plane actions such as login or auth checks.
type HarnessActionRunner interface {
	RunAction(ctx context.Context, req HarnessActionRequest) (HarnessActionResult, error)
}

// AgentSession represents a running agent interaction.
type AgentSession interface {
	// ID returns the unique identifier for this session.
	ID() string

	// Wait blocks until the session completes (done or error).
	// Returns nil on successful completion, or the error that caused failure.
	Wait(ctx context.Context) error

	// Events returns a channel emitting agent events.
	// The channel closes when the session ends.
	Events() <-chan AgentEvent

	// SendMessage sends a message to the running agent.
	// Used for foreman iteration and critique feedback.
	SendMessage(ctx context.Context, msg string) error

	// Steer sends a steering prompt that interrupts the agent's active streaming turn.
	// Returns ErrSteerNotSupported if the harness does not support mid-stream steering.
	Steer(ctx context.Context, msg string) error

	// SendAnswer sends an answer to resolve a pending ask_foreman tool call.
	// The answer is delivered to the bridge subprocess via stdin.
	SendAnswer(ctx context.Context, answer string) error

	// Abort terminates the agent session gracefully.
	// Returns an error if the session cannot be aborted.
	Abort(ctx context.Context) error

	// ResumeInfo returns harness-specific resume data to persist after session completion.
	// Returns nil if the harness does not support or produce resume data.
	ResumeInfo() map[string]string

	// Compact requests manual context compaction to free up context window space.
	// Returns ErrCompactNotSupported if the harness does not support compaction.
	Compact(ctx context.Context) error
}

// Adapter errors.
var (
	// ErrBrowseNotSupported is returned when ListSelectable is called
	// on an adapter that doesn't support browsing.
	ErrBrowseNotSupported = error(browseNotSupported{})

	// ErrWatchNotSupported is returned when Watch is called
	// on an adapter that doesn't support watching.
	ErrWatchNotSupported = error(watchNotSupported{})

	// ErrMutateNotSupported is returned when mutation methods are called
	// on an adapter that doesn't support mutations.
	ErrMutateNotSupported = error(mutateNotSupported{})

	// ErrSteerNotSupported is returned when Steer is called
	// on a harness that doesn't support mid-stream steering.
	ErrSteerNotSupported = error(steerNotSupported{})

	// ErrSendAnswerNotSupported is returned when SendAnswer is called
	// on a harness that doesn't support answering foreman questions.
	ErrSendAnswerNotSupported = error(sendAnswerNotSupported{})

	// ErrCompactNotSupported is returned when Compact is called
	// on a harness that doesn't support manual compaction.
	ErrCompactNotSupported = error(compactNotSupported{})
)

type (
	browseNotSupported     struct{}
	watchNotSupported      struct{}
	mutateNotSupported     struct{}
	steerNotSupported      struct{}
	sendAnswerNotSupported struct{}
	compactNotSupported    struct{}
)

func (browseNotSupported) Error() string     { return "browse not supported by this adapter" }
func (watchNotSupported) Error() string      { return "watch not supported by this adapter" }
func (mutateNotSupported) Error() string     { return "mutation not supported by this adapter" }
func (steerNotSupported) Error() string      { return "steering not supported by this harness" }
func (sendAnswerNotSupported) Error() string { return "send answer not supported by this harness" }
func (compactNotSupported) Error() string    { return "compact not supported by this harness" }
