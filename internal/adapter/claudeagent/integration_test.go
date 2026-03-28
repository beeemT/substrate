//go:build integration

package claudeagent

// Integration test for the claude-agent bridge harness.
// Skipped unless:
//   - "bun" is available in PATH
//   - bridge/claude-agent-bridge.ts is accessible (via standard candidate paths)
//   - the bridge dependencies are installed (node_modules/@anthropic-ai/claude-agent-sdk)
//   - the "claude" binary is available (for the SDK to use)
//
// Run with: go test -run TestIntegration ./internal/adapter/claudeagent/...
// (requires a fully configured development environment)

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
)

func TestIntegrationBasicPrompt(t *testing.T) {
	// Skip if prerequisites are missing.
	if _, err := exec.LookPath("bun"); err != nil {
		t.Skip("bun not in PATH; skipping integration test")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude binary not in PATH; skipping integration test")
	}
	cfg := config.ClaudeCodeConfig{}
	if _, _, err := resolveReadyBridgeRuntime(cfg); err != nil {
		t.Skipf("bridge or dependencies not available: %v", err)
	}

	h := NewHarness(cfg, t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	sess, err := h.StartSession(ctx, adapter.SessionOpts{
		SessionID:    "test-integration-001",
		Mode:         adapter.SessionModeAgent,
		WorktreePath: t.TempDir(),
		UserPrompt:   "What is 1+1? Reply with only the number.",
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Abort(ctx)

	var gotTextDelta, gotDone bool
	drainTimeout := time.After(90 * time.Second)

	for {
		select {
		case evt, ok := <-sess.Events():
			if !ok {
				goto done
			}
			t.Logf("event: type=%s payload=%q", evt.Type, evt.Payload)
			if evt.Type == "text_delta" {
				gotTextDelta = true
			}
			if evt.Type == "done" {
				gotDone = true
				goto done
			}
			if evt.Type == "error" {
				t.Fatalf("session error: %s", evt.Payload)
			}
		case <-drainTimeout:
			t.Fatal("timeout waiting for session to complete")
		case <-ctx.Done():
			t.Fatal("context cancelled")
		}
	}

done:
	if err := sess.Wait(ctx); err != nil {
		t.Logf("Wait: %v (may be expected after abort)", err)
	}

	if !gotTextDelta {
		t.Error("expected at least one text_delta event")
	}
	if !gotDone {
		t.Error("expected a done event")
	}
}
