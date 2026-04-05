package opencode

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
)

func TestName(t *testing.T) {
	h := NewHarness(config.OpenCodeConfig{}, "/tmp")
	if got := h.Name(); got != "opencode" {
		t.Errorf("Name() = %q, want %q", got, "opencode")
	}
}

func TestCapabilities(t *testing.T) {
	h := NewHarness(config.OpenCodeConfig{}, "/tmp")
	caps := h.Capabilities()

	if !caps.SupportsStreaming {
		t.Error("expected SupportsStreaming == true")
	}
	if !caps.SupportsMessaging {
		t.Error("expected SupportsMessaging == true")
	}
	if !caps.SupportsNativeResume {
		t.Error("expected SupportsNativeResume == true")
	}

	expectedTools := []string{
		"Read", "Write", "Edit", "Bash", "Glob", "Grep",
		"mcp__substrate-foreman__ask_foreman",
	}
	for _, tool := range expectedTools {
		if !slices.Contains(caps.SupportedTools, tool) {
			t.Errorf("SupportedTools missing %q; got %v", tool, caps.SupportedTools)
		}
	}
}

func TestSupportsCompact(t *testing.T) {
	h := NewHarness(config.OpenCodeConfig{}, "/tmp")
	if !h.SupportsCompact() {
		t.Error("expected SupportsCompact() == true")
	}
}

func TestValidateReadiness_BinaryNotFound(t *testing.T) {
	// Use a BinaryPath pointing to a binary that definitely doesn't exist.
	err := ValidateReadiness(config.OpenCodeConfig{
		BinaryPath: "nonexistent_opencode_binary_test",
	})
	if err == nil {
		t.Fatal("expected error for missing binary, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found', got: %v", err)
	}
}

func TestRunAction_UnsupportedAction(t *testing.T) {
	h := NewHarness(config.OpenCodeConfig{}, "/tmp")
	_, err := h.RunAction(context.Background(), adapter.HarnessActionRequest{
		Action: "bogus",
	})
	if err == nil {
		t.Fatal("expected error for unsupported action, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported opencode action") {
		t.Errorf("error should mention 'unsupported opencode action', got: %v", err)
	}
}
