// Package tuilog provides a custom slog.Handler that captures log entries
// into an in-memory ring buffer and optionally sends warn/error entries to a
// channel for TUI toast display.
package tuilog

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

const toastAttrKey = "toast"

const defaultMaxEntries = 1000

// Entry is a single captured log record.
type Entry struct {
	Time    time.Time
	Level   slog.Level
	Message string
	Attrs   string // pre-formatted key=value pairs
}

// String renders the entry as a single human-readable line.
func (e Entry) String() string {
	ts := e.Time.Format("15:04:05")
	level := e.Level.String()
	if e.Attrs != "" {
		return fmt.Sprintf("%s %s %s %s", ts, level, e.Message, e.Attrs)
	}
	return fmt.Sprintf("%s %s %s", ts, level, e.Message)
}

// Store is a thread-safe ring buffer of log entries.
type Store struct {
	mu      sync.Mutex
	entries []Entry
	max     int
}

// NewStore creates a Store with the default capacity.
func NewStore() *Store {
	return &Store{max: defaultMaxEntries}
}

// Append adds an entry to the store, evicting the oldest if at capacity.
func (s *Store) Append(e Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.entries) >= s.max {
		s.entries = s.entries[1:]
	}
	s.entries = append(s.entries, e)
}

// Snapshot returns a copy of all entries (oldest first).
func (s *Store) Snapshot() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, len(s.entries))
	copy(out, s.entries)
	return out
}

// Len returns the current number of stored entries.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// ToastEntry is sent over the toast channel for warn/error log entries.
type ToastEntry struct {
	Level   slog.Level
	Message string
}

// Handler is a slog.Handler that writes to a Store and pushes warn/error
// entries onto a channel for TUI toast display.
type Handler struct {
	store  *Store
	toasts chan<- ToastEntry
	level  slog.Level
	group  string
	attrs  []slog.Attr
}

// NewHandler creates a Handler that captures to the given store.
// Entries at or above toastLevel are also sent to the toasts channel.
// The channel should be buffered to avoid blocking the caller.
func NewHandler(store *Store, toasts chan<- ToastEntry) *Handler {
	return &Handler{
		store:  store,
		toasts: toasts,
		level:  slog.LevelDebug,
	}
}

func (h *Handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *Handler) Handle(_ context.Context, r slog.Record) error {
	var sb strings.Builder
	if h.group != "" {
		sb.WriteString(h.group)
		sb.WriteByte('.')
	}
	for _, a := range h.attrs {
		writeAttr(&sb, a)
	}
	r.Attrs(func(a slog.Attr) bool {
		writeAttr(&sb, a)
		return true
	})

	entry := Entry{
		Time:    r.Time,
		Level:   r.Level,
		Message: r.Message,
		Attrs:   strings.TrimSpace(sb.String()),
	}
	h.store.Append(entry)

	if r.Level >= slog.LevelWarn && h.toasts != nil && recordToastEnabled(h.attrs, r) {
		select {
		case h.toasts <- ToastEntry{Level: r.Level, Message: r.Message}:
		default:
			// Drop if channel is full to avoid blocking.
		}
	}

	return nil
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newAttrs := make([]slog.Attr, len(h.attrs)+len(attrs))
	copy(newAttrs, h.attrs)
	copy(newAttrs[len(h.attrs):], attrs)
	return &Handler{
		store:  h.store,
		toasts: h.toasts,
		level:  h.level,
		group:  h.group,
		attrs:  newAttrs,
	}
}

func (h *Handler) WithGroup(name string) slog.Handler {
	newGroup := name
	if h.group != "" {
		newGroup = h.group + "." + name
	}
	return &Handler{
		store:  h.store,
		toasts: h.toasts,
		level:  h.level,
		group:  newGroup,
		attrs:  h.attrs,
	}
}

func writeAttr(sb *strings.Builder, a slog.Attr) {
	if a.Equal(slog.Attr{}) {
		return
	}
	if sb.Len() > 0 {
		sb.WriteByte(' ')
	}
	fmt.Fprintf(sb, "%s=%s", a.Key, a.Value.String())
}

func recordToastEnabled(handlerAttrs []slog.Attr, r slog.Record) bool {
	enabled := true

	apply := func(a slog.Attr) {
		if a.Equal(slog.Attr{}) || a.Key != toastAttrKey {
			return
		}
		value := a.Value.Resolve()
		if value.Kind() == slog.KindBool {
			enabled = value.Bool()
		}
	}

	for _, a := range handlerAttrs {
		apply(a)
	}
	r.Attrs(func(a slog.Attr) bool {
		apply(a)
		return true
	})

	return enabled
}
