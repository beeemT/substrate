//go:build integration

package opencode

// Integration test for the opencode HTTP harness.
// Skipped unless:
//   - "opencode" binary is available in PATH
//   - the binary responds to --version
//
// Run with: go test -run TestIntegration ./internal/adapter/opencode/...
// (requires opencode installed and configured with API credentials)

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
)

func TestIntegrationBasicPrompt(t *testing.T) {
	if _, err := exec.LookPath("opencode"); err != nil {
		t.Skip("opencode binary not in PATH; skipping integration test")
	}
	if err := ValidateReadiness(config.OpenCodeConfig{}); err != nil {
		t.Skipf("opencode not ready: %v", err)
	}

	h := NewHarness(config.OpenCodeConfig{}, t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	sess, err := h.StartSession(ctx, adapter.SessionOpts{
		SessionID:  "test-integration-001",
		Mode:       adapter.SessionModeAgent,
		UserPrompt: "What is 1+1? Reply with only the number.",
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
