package components

import (
	"strings"
	"testing"

	"github.com/beeemT/substrate/internal/tui/styles"
)

func TestToastModelCoalescesDuplicateToast(t *testing.T) {
	t.Parallel()

	m := NewToastModel(styles.NewStyles(styles.DefaultTheme))
	m.SetWidth(80)
	m.AddToast("event channel full, dropping event", ToastWarning)
	firstExpiry := m.toasts[0].Expires
	m.AddToast("other warning", ToastWarning)
	m.AddToast("event channel full, dropping event", ToastWarning)

	if got := len(m.toasts); got != 2 {
		t.Fatalf("toast count = %d, want 2", got)
	}
	latest := m.toasts[len(m.toasts)-1]
	if latest.Message != "event channel full, dropping event" {
		t.Fatalf("latest message = %q, want duplicate moved to top", latest.Message)
	}
	if latest.Count != 2 {
		t.Fatalf("duplicate count = %d, want 2", latest.Count)
	}
	if !latest.Expires.After(firstExpiry) {
		t.Fatalf("duplicate expiry was not reset")
	}
	if !strings.Contains(latest.rendered, "2x") {
		t.Fatalf("rendered toast %q does not include duplicate count", latest.rendered)
	}
}

func TestToastModelDoesNotCoalesceDifferentLevels(t *testing.T) {
	t.Parallel()

	m := NewToastModel(styles.NewStyles(styles.DefaultTheme))
	m.SetWidth(80)
	m.AddToast("same text", ToastWarning)
	m.AddToast("same text", ToastError)

	if got := len(m.toasts); got != 2 {
		t.Fatalf("toast count = %d, want 2", got)
	}
	if m.toasts[0].Count != 1 || m.toasts[1].Count != 1 {
		t.Fatalf("counts = %d,%d, want 1,1", m.toasts[0].Count, m.toasts[1].Count)
	}
}
