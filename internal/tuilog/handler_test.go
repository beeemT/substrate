package tuilog

import (
	"log/slog"
	"testing"
)

func TestHandlerWarnErrorEmitToastsByDefault(t *testing.T) {
	t.Parallel()

	store := NewStore()
	toasts := make(chan ToastEntry, 2)
	logger := slog.New(NewHandler(store, toasts))

	logger.Warn("warn message")
	logger.Error("error message")

	gotWarn := <-toasts
	if gotWarn.Level != slog.LevelWarn || gotWarn.Message != "warn message" {
		t.Fatalf("first toast = %#v, want warn message", gotWarn)
	}
	gotErr := <-toasts
	if gotErr.Level != slog.LevelError || gotErr.Message != "error message" {
		t.Fatalf("second toast = %#v, want error message", gotErr)
	}

	if got := store.Len(); got != 2 {
		t.Fatalf("store len = %d, want 2", got)
	}
}

func TestHandlerSuppressesToastWhenToastAttrFalse(t *testing.T) {
	t.Parallel()

	store := NewStore()
	toasts := make(chan ToastEntry, 1)
	logger := slog.New(NewHandler(store, toasts))

	logger.Error("suppressed", "toast", false)

	select {
	case msg := <-toasts:
		t.Fatalf("unexpected toast entry: %#v", msg)
	default:
	}

	entries := store.Snapshot()
	if len(entries) != 1 {
		t.Fatalf("entries len = %d, want 1", len(entries))
	}
	if entries[0].Message != "suppressed" {
		t.Fatalf("stored message = %q, want suppressed", entries[0].Message)
	}
}

func TestHandlerSuppressesToastWhenLoggerWithToastFalse(t *testing.T) {
	t.Parallel()

	store := NewStore()
	toasts := make(chan ToastEntry, 1)
	logger := slog.New(NewHandler(store, toasts)).With("toast", false)

	logger.Error("suppressed via with")

	select {
	case msg := <-toasts:
		t.Fatalf("unexpected toast entry: %#v", msg)
	default:
	}

	if got := store.Len(); got != 1 {
		t.Fatalf("store len = %d, want 1", got)
	}
}

func TestHandlerIgnoresNonBoolToastAttr(t *testing.T) {
	t.Parallel()

	store := NewStore()
	toasts := make(chan ToastEntry, 1)
	logger := slog.New(NewHandler(store, toasts))

	logger.Error("non-bool", "toast", "false")

	select {
	case msg := <-toasts:
		if msg.Level != slog.LevelError || msg.Message != "non-bool" {
			t.Fatalf("toast = %#v, want error non-bool", msg)
		}
	default:
		t.Fatal("expected toast entry")
	}

	if got := store.Len(); got != 1 {
		t.Fatalf("store len = %d, want 1", got)
	}
}
