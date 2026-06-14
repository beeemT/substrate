package server

import (
	"context"
	"testing"
	"time"

	daemonapi "github.com/beeemT/substrate/internal/daemon/api"
	"github.com/beeemT/substrate/internal/domain"
)

type autonomousEventRecorder struct {
	events []domain.SystemEvent
}

func (r *autonomousEventRecorder) Create(_ context.Context, event domain.SystemEvent) (domain.SystemEvent, error) {
	r.events = append(r.events, event)
	return event, nil
}

func TestAutonomousStateStartStatusStop(t *testing.T) {
	clock := func() time.Time { return time.Unix(10, 0).UTC() }
	state := NewAutonomousState(clock)
	recorder := &autonomousEventRecorder{}
	ctx := context.Background()

	run, err := state.Start(ctx, nil, recorder, daemonapi.StartAutonomousModeRequest{
		WorkspaceID:       "ws-1",
		InstanceID:        "instance-1",
		SelectedFilterIDs: []string{"github:foo", "linear:bar"},
		IdempotencyKey:    "start-1",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if !run.Running || run.InstanceID != "instance-1" || len(run.ActiveFilterIDs) != 2 {
		t.Fatalf("Start() = %#v", run)
	}
	status := state.Status("instance-1")
	if !status.Running || status.ActiveCount != 1 || len(status.Runs) != 1 {
		t.Fatalf("Status() = %#v", status)
	}
	if len(recorder.events) != 1 || recorder.events[0].EventType != daemonapi.AutonomousModeEventStarted {
		t.Fatalf("events after start = %#v", recorder.events)
	}

	stopped, err := state.Stop(ctx, nil, recorder, daemonapi.StopAutonomousModeRequest{InstanceID: "instance-1", IdempotencyKey: "stop-1"})
	if err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if len(stopped) != 1 || stopped[0].Running || stopped[0].StopReason == "" {
		t.Fatalf("Stop() = %#v", stopped)
	}
	status = state.Status("instance-1")
	if status.Running || status.ActiveCount != 0 {
		t.Fatalf("Status() after stop = %#v", status)
	}
	if len(recorder.events) != 2 || recorder.events[1].EventType != daemonapi.AutonomousModeEventStopped {
		t.Fatalf("events after stop = %#v", recorder.events)
	}
}

func TestAutonomousStateIdempotentStart(t *testing.T) {
	state := NewAutonomousState(func() time.Time { return time.Unix(1, 0).UTC() })
	ctx := context.Background()
	req := daemonapi.StartAutonomousModeRequest{
		WorkspaceID:       "ws-1",
		InstanceID:        "instance-1",
		SelectedFilterIDs: []string{"filter-1"},
		IdempotencyKey:    "start-1",
	}
	first, err := state.Start(ctx, nil, nil, req)
	if err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	second, err := state.Start(ctx, nil, nil, req)
	if err != nil {
		t.Fatalf("second Start() error = %v", err)
	}
	if first.InstanceID != second.InstanceID || len(second.ActiveFilterIDs) != 1 {
		t.Fatalf("idempotent start mismatch: first=%#v second=%#v", first, second)
	}
	_, err = state.Start(ctx, nil, nil, daemonapi.StartAutonomousModeRequest{
		WorkspaceID:       "ws-1",
		InstanceID:        "instance-1",
		SelectedFilterIDs: []string{"filter-1"},
	})
	if err == nil {
		t.Fatal("Start() without idempotency key on running instance error = nil")
	}
}

func TestAutonomousStateStatusRingIsCapped(t *testing.T) {
	state := NewAutonomousState(func() time.Time { return time.Unix(1, 0).UTC() })
	ctx := context.Background()
	_, err := state.Start(ctx, nil, nil, daemonapi.StartAutonomousModeRequest{
		WorkspaceID:       "ws-1",
		InstanceID:        "instance-1",
		SelectedFilterIDs: []string{"filter-1"},
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	for i := 0; i < autonomousStatusRing+5; i++ {
		state.AppendStatus("instance-1", "info", "tick")
	}
	status := state.Status("instance-1")
	if len(status.Runs) != 1 || len(status.Runs[0].RecentStatusJSON) != autonomousStatusRing {
		t.Fatalf("status ring len = %#v", status)
	}
}
