package codex

import (
	"slices"
	"testing"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
)

func TestHarnessNameAndCapabilities(t *testing.T) {
	h := NewHarness(config.CodexConfig{})
	if got := h.Name(); got != "codex" {
		t.Fatalf("Name() = %q, want codex", got)
	}
	caps := h.Capabilities()
	if !caps.SupportsStreaming {
		t.Fatal("SupportsStreaming = false, want true")
	}
	if caps.SupportsMessaging {
		t.Fatal("SupportsMessaging = true, want false")
	}
}

func TestBuildArgs(t *testing.T) {
	h := NewHarness(config.CodexConfig{Model: "o4", ApprovalMode: "full-auto", FullAuto: true, Quiet: true})
	args := h.buildArgs(adapter.SessionOpts{WorktreePath: "/tmp/work", SystemPrompt: "sys", UserPrompt: "user"})
	for _, want := range []string{"-w", "/tmp/work", "-m", "o4", "--approval-mode", "full-auto", "--full-auto", "-q"} {
		if !slices.Contains(args, want) {
			t.Fatalf("args missing %q: %v", want, args)
		}
	}
}
