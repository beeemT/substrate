package app

import (
	"context"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/app/remotedetect"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
)

type fakeWorkItemAdapter struct{ events chan domain.SystemEvent }

func (f *fakeWorkItemAdapter) Name() string { return "fake-work" }
func (f *fakeWorkItemAdapter) Capabilities() adapter.AdapterCapabilities {
	return adapter.AdapterCapabilities{}
}

func (f *fakeWorkItemAdapter) ListSelectable(context.Context, adapter.ListOpts) (*adapter.ListResult, error) {
	return nil, adapter.ErrBrowseNotSupported
}

func (f *fakeWorkItemAdapter) Resolve(context.Context, adapter.Selection) (domain.Session, error) {
	return domain.Session{}, nil
}

func (f *fakeWorkItemAdapter) Watch(context.Context, adapter.WorkItemFilter) (<-chan adapter.WorkItemEvent, error) {
	return nil, adapter.ErrWatchNotSupported
}

func (f *fakeWorkItemAdapter) Fetch(context.Context, string) (domain.Session, error) {
	return domain.Session{}, nil
}

func (f *fakeWorkItemAdapter) UpdateState(context.Context, string, domain.TrackerState) error {
	return nil
}
func (f *fakeWorkItemAdapter) AddComment(context.Context, string, string) error { return nil }
func (f *fakeWorkItemAdapter) OnEvent(_ context.Context, evt domain.SystemEvent) error {
	f.events <- evt

	return nil
}

type fakeRepoLifecycleAdapter struct {
	name   string
	events chan domain.SystemEvent
}

func (f *fakeRepoLifecycleAdapter) Name() string { return f.name }
func (f *fakeRepoLifecycleAdapter) OnEvent(_ context.Context, evt domain.SystemEvent) error {
	f.events <- evt

	return nil
}

func wireAdapterSubscriptions(bus *event.Bus, workItemAdapters []adapter.WorkItemAdapter, repoLifecycleAdapters []adapter.RepoLifecycleAdapter) error {
	for _, workItemAdapter := range workItemAdapters {
		sub, err := bus.Subscribe("work-item-adapter:"+workItemAdapter.Name(), string(domain.EventPlanApproved), string(domain.EventWorkItemCompleted))
		if err != nil {
			return err
		}
		go func(a adapter.WorkItemAdapter, events <-chan domain.SystemEvent) {
			for evt := range events {
				_ = a.OnEvent(context.Background(), evt)
			}
		}(workItemAdapter, sub.C)
	}
	for _, lifecycleAdapter := range repoLifecycleAdapters {
		sub, err := bus.Subscribe("repo-lifecycle-adapter:"+lifecycleAdapter.Name(), string(domain.EventWorktreeCreated), string(domain.EventWorkItemCompleted))
		if err != nil {
			return err
		}
		go func(a adapter.RepoLifecycleAdapter, events <-chan domain.SystemEvent) {
			for evt := range events {
				_ = a.OnEvent(context.Background(), evt)
			}
		}(lifecycleAdapter, sub.C)
	}

	return nil
}

func TestWireAdapterSubscriptions(t *testing.T) {
	bus := event.NewBus(event.BusConfig{})
	defer bus.Close()

	work := &fakeWorkItemAdapter{events: make(chan domain.SystemEvent, 4)}
	life := &fakeRepoLifecycleAdapter{name: "glab", events: make(chan domain.SystemEvent, 4)}
	if err := wireAdapterSubscriptions(bus, []adapter.WorkItemAdapter{work}, []adapter.RepoLifecycleAdapter{life}); err != nil {
		t.Fatalf("wireAdapterSubscriptions: %v", err)
	}

	planApproved := domain.SystemEvent{ID: domain.NewID(), EventType: string(domain.EventPlanApproved), CreatedAt: time.Now()}
	if err := bus.Publish(context.Background(), planApproved); err != nil {
		t.Fatalf("publish plan approved: %v", err)
	}
	select {
	case evt := <-work.events:
		if evt.EventType != string(domain.EventPlanApproved) {
			t.Fatalf("work adapter got %s", evt.EventType)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for work item adapter event")
	}
	select {
	case evt := <-life.events:
		t.Fatalf("lifecycle adapter should not receive plan.approved, got %s", evt.EventType)
	default:
	}

	worktreeCreated := domain.SystemEvent{ID: domain.NewID(), EventType: string(domain.EventWorktreeCreated), CreatedAt: time.Now()}
	if err := bus.Publish(context.Background(), worktreeCreated); err != nil {
		t.Fatalf("publish worktree created: %v", err)
	}
	// WorktreeCreated is a repo-lifecycle event, not a work-item event.
	// The work-item adapter must NOT receive it; only the lifecycle adapter should.
	select {
	case evt := <-life.events:
		if evt.EventType != string(domain.EventWorktreeCreated) {
			t.Fatalf("lifecycle adapter got %s, want worktree.created", evt.EventType)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for lifecycle adapter to receive worktree.created")
	}
	select {
	case evt := <-work.events:
		t.Fatalf("work-item adapter must not receive worktree.created, got %s", evt.EventType)
	default:
	}
}

func TestWireAdapterSubscriptions_RoutesLifecycleEventsByProvider(t *testing.T) {
	bus := event.NewBus(event.BusConfig{})
	defer bus.Close()

	work := &fakeWorkItemAdapter{events: make(chan domain.SystemEvent, 8)}
	githubLife := &fakeRepoLifecycleAdapter{name: "github", events: make(chan domain.SystemEvent, 4)}
	gitlabLife := &fakeRepoLifecycleAdapter{name: "glab", events: make(chan domain.SystemEvent, 4)}
	if err := wireAdapterSubscriptions(bus, []adapter.WorkItemAdapter{work}, []adapter.RepoLifecycleAdapter{
		routedRepoLifecycleAdapter{provider: remotedetect.PlatformGitHub, adapter: githubLife},
		routedRepoLifecycleAdapter{provider: remotedetect.PlatformGitLab, adapter: gitlabLife},
	}); err != nil {
		t.Fatalf("wireAdapterSubscriptions: %v", err)
	}

	githubEvent := domain.SystemEvent{
		ID:        domain.NewID(),
		EventType: string(domain.EventWorktreeCreated),
		Payload:   `{"review":{"base_repo":{"provider":"github","owner":"acme","repo":"rocket"},"head_repo":{"provider":"github","owner":"acme","repo":"rocket"}}}`,
		CreatedAt: time.Now(),
	}
	if err := bus.Publish(context.Background(), githubEvent); err != nil {
		t.Fatalf("publish github worktree created: %v", err)
	}
	expectLifecycleEvent(t, githubLife.events, string(domain.EventWorktreeCreated))
	expectNoLifecycleEvent(t, gitlabLife.events)

	gitlabEvent := domain.SystemEvent{
		ID:        domain.NewID(),
		EventType: string(domain.EventWorkItemCompleted),
		Payload:   `{"external_id":"gl:issue:1234#42"}`,
		CreatedAt: time.Now(),
	}
	if err := bus.Publish(context.Background(), gitlabEvent); err != nil {
		t.Fatalf("publish gitlab work item completed: %v", err)
	}
	expectLifecycleEvent(t, gitlabLife.events, string(domain.EventWorkItemCompleted))
	expectNoLifecycleEvent(t, githubLife.events)

	missingReview := domain.SystemEvent{
		ID:        domain.NewID(),
		EventType: string(domain.EventWorktreeCreated),
		Payload:   `{}`,
		CreatedAt: time.Now(),
	}
	if err := bus.Publish(context.Background(), missingReview); err != nil {
		t.Fatalf("publish missing review worktree created: %v", err)
	}
	expectNoLifecycleEvent(t, githubLife.events)
	expectNoLifecycleEvent(t, gitlabLife.events)
}

func expectLifecycleEvent(t *testing.T, ch <-chan domain.SystemEvent, wantType string) {
	t.Helper()
	select {
	case evt := <-ch:
		if evt.EventType != wantType {
			t.Fatalf("event type = %s, want %s", evt.EventType, wantType)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", wantType)
	}
}

func expectNoLifecycleEvent(t *testing.T, ch <-chan domain.SystemEvent) {
	t.Helper()
	select {
	case evt := <-ch:
		t.Fatalf("unexpected lifecycle event %s", evt.EventType)
	case <-time.After(100 * time.Millisecond):
	}
}
