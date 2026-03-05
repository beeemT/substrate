package manual_test

import (
	"context"
	"errors"
	"testing"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/adapter/manual"
	"github.com/beeemT/substrate/internal/domain"
)

type mockStore struct{ count int }

func (m *mockStore) CountManualWorkItems(_ context.Context, _ string) (int, error) {
	return m.count, nil
}

func TestResolve_NilManualInput(t *testing.T) {
	a := manual.New(&mockStore{count: 0}, "ws-1")
	_, err := a.Resolve(context.Background(), adapter.Selection{
		Scope: domain.ScopeManual,
	})
	if err == nil {
		t.Fatal("expected error when sel.Manual is nil, got nil")
	}
}

func TestResolve_ValidInput(t *testing.T) {
	a := manual.New(&mockStore{count: 0}, "ws-1")
	item, err := a.Resolve(context.Background(), adapter.Selection{
		Scope: domain.ScopeManual,
		Manual: &adapter.ManualInput{
			Title:       "My task",
			Description: "Do the thing",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item.ExternalID != "MAN-1" {
		t.Errorf("ExternalID = %q, want %q", item.ExternalID, "MAN-1")
	}
	if item.Title != "My task" {
		t.Errorf("Title = %q, want %q", item.Title, "My task")
	}
	if item.Description != "Do the thing" {
		t.Errorf("Description = %q, want %q", item.Description, "Do the thing")
	}
	if item.Source != "manual" {
		t.Errorf("Source = %q, want %q", item.Source, "manual")
	}
	if item.SourceScope != domain.ScopeManual {
		t.Errorf("SourceScope = %q, want %q", item.SourceScope, domain.ScopeManual)
	}
	if item.State != domain.WorkItemIngested {
		t.Errorf("State = %q, want %q", item.State, domain.WorkItemIngested)
	}
	if item.ID == "" {
		t.Error("ID must not be empty")
	}
}

func TestResolve_SequentialIDs(t *testing.T) {
	a := manual.New(&mockStore{count: 41}, "ws-1")
	item, err := a.Resolve(context.Background(), adapter.Selection{
		Scope:  domain.ScopeManual,
		Manual: &adapter.ManualInput{Title: "Next"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item.ExternalID != "MAN-42" {
		t.Errorf("ExternalID = %q, want %q", item.ExternalID, "MAN-42")
	}
}

func TestWatch_ReturnsClosed(t *testing.T) {
	a := manual.New(&mockStore{}, "ws-1")
	ch, err := a.Watch(context.Background(), adapter.WorkItemFilter{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// A closed channel is immediately readable (returns zero value, ok=false).
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("expected channel to be closed, got a value")
		}
	default:
		t.Error("channel was not closed; read would block")
	}
}

func TestUpdateState_NoOp(t *testing.T) {
	a := manual.New(&mockStore{}, "ws-1")
	err := a.UpdateState(context.Background(), "MAN-1", domain.TrackerStateTodo)
	if err != nil {
		t.Errorf("UpdateState returned non-nil error: %v", err)
	}
}

func TestAddComment_NoOp(t *testing.T) {
	a := manual.New(&mockStore{}, "ws-1")
	err := a.AddComment(context.Background(), "MAN-1", "some comment")
	if err != nil {
		t.Errorf("AddComment returned non-nil error: %v", err)
	}
}

func TestListSelectable_NotSupported(t *testing.T) {
	a := manual.New(&mockStore{}, "ws-1")
	_, err := a.ListSelectable(context.Background(), adapter.ListOpts{})
	if !errors.Is(err, adapter.ErrBrowseNotSupported) {
		t.Errorf("ListSelectable error = %v, want ErrBrowseNotSupported", err)
	}
}

func TestFetch_ReturnsErrNotSupported(t *testing.T) {
	a := manual.New(&mockStore{}, "ws-1")
	_, err := a.Fetch(context.Background(), "MAN-1")
	if err == nil {
		t.Fatal("Fetch should return a non-nil error (ErrNotSupported)")
	}
	// Must NOT be ErrBrowseNotSupported — Fetch is not a browse operation.
	if errors.Is(err, adapter.ErrBrowseNotSupported) {
		t.Errorf("Fetch returned ErrBrowseNotSupported; want manual.ErrNotSupported")
	}
}
