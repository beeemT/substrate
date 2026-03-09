package views

import (
	"strings"
	"testing"

	"github.com/beeemT/substrate/internal/tui/styles"
)

func TestSessionLogViewRespectsRequestedHeightWithMeta(t *testing.T) {
	t.Parallel()

	m := NewSessionLogModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(48, 10)
	m.SetTitle("SUB-1 · Investigate overflow")
	m.SetMeta("workspace · repo-1 · sess-1")
	m.SetStaticContent([]string{"line 1", "line 2", "line 3"})

	lines := strings.Split(m.View(), "\n")
	if got := len(lines); got != 10 {
		t.Fatalf("line count = %d, want 10", got)
	}
	if !strings.Contains(lines[len(lines)-1], "Pause/unpause") {
		t.Fatalf("last line = %q, want session log hints", lines[len(lines)-1])
	}
}
