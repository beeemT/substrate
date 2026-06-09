package opencode

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

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

func TestBuildInitialPromptFoldsSystemAndUser(t *testing.T) {
	if got := buildInitialPrompt("system", "user"); got != "system\n\nuser" {
		t.Fatalf("buildInitialPrompt with system = %q", got)
	}
	if got := buildInitialPrompt("", "user"); got != "user" {
		t.Fatalf("buildInitialPrompt without system = %q", got)
	}
}

func TestShouldRegisterQuestionMCPRespectsQuestionPolicy(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		mode   adapter.SessionMode
		policy adapter.QuestionToolPolicy
		want   bool
	}{
		{name: "default agent", mode: adapter.SessionModeAgent, policy: adapter.QuestionToolPolicyDefault, want: true},
		{name: "foreman policy", mode: adapter.SessionModeAgent, policy: adapter.QuestionToolPolicyForeman, want: true},
		{name: "human policy", mode: adapter.SessionModeAgent, policy: adapter.QuestionToolPolicyHuman, want: false},
		{name: "none policy", mode: adapter.SessionModeAgent, policy: adapter.QuestionToolPolicyNone, want: false},
		{name: "foreman session", mode: adapter.SessionModeForeman, policy: adapter.QuestionToolPolicyDefault, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := shouldRegisterQuestionMCP(tc.mode, tc.policy); got != tc.want {
				t.Fatalf("shouldRegisterQuestionMCP = %v, want %v", got, tc.want)
			}
		})
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

func TestDetectServerURL_MatchesPattern(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Simulate stdout with the expected line among noise.
	pr, pw := io.Pipe()
	go func() {
		fmt.Fprintln(pw, "loading config...")
		fmt.Fprintln(pw, "Server running on http://127.0.0.1:42187")
		fmt.Fprintln(pw, "ready")
		pw.Close()
	}()

	url, err := detectServerURL(ctx, pr)
	if err != nil {
		t.Fatalf("detectServerURL: %v", err)
	}
	if url != "http://127.0.0.1:42187" {
		t.Fatalf("url = %q, want %q", url, "http://127.0.0.1:42187")
	}
}

func TestDetectServerURL_ContextCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe()

	// Cancel after a short delay without writing the expected line.
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
		pw.Close()
	}()

	_, err := detectServerURL(ctx, pr)
	if err == nil {
		t.Fatal("expected error on context cancel, got nil")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("error = %v, want context canceled", err)
	}
}

func TestDetectServerURL_StreamEnd(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Close stdout without ever writing the pattern.
	pr, pw := io.Pipe()
	pw.Close()

	_, err := detectServerURL(ctx, pr)
	if err == nil {
		t.Fatal("expected error when stream ends without URL, got nil")
	}
	if !strings.Contains(err.Error(), "server URL not found") {
		t.Fatalf("error = %v, want 'server URL not found'", err)
	}
}
