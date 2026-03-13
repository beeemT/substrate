package omp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
)

func writeHarnessExecutable(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestRunAction_SentryLoginPassesSelfHostedURL(t *testing.T) {
	binDir := t.TempDir()
	seenPath := filepath.Join(binDir, "seen-url.txt")
	writeHarnessExecutable(t, binDir, "sentry", "#!/bin/sh\nif [ \"$1\" = \"auth\" ] && [ \"$2\" = \"login\" ]; then\n  printf '%s' \"$SENTRY_URL\" > \"$SEEN_PATH\"\n  exit 0\nfi\nexit 1\n")
	t.Setenv("PATH", binDir)
	t.Setenv("SEEN_PATH", seenPath)

	h := NewHarness(config.OhMyPiConfig{}, "")
	result, err := h.RunAction(context.Background(), adapter.HarnessActionRequest{Action: "login_provider", Provider: "sentry", Inputs: map[string]string{"base_url": "https://sentry.example.com/self-hosted"}})
	if err != nil {
		t.Fatalf("RunAction() error = %v", err)
	}
	if !result.Success || result.Message != "sentry login succeeded" {
		t.Fatalf("result = %+v, want sentry login success", result)
	}
	seen, err := os.ReadFile(seenPath)
	if err != nil {
		t.Fatalf("read seen url: %v", err)
	}
	if strings.TrimSpace(string(seen)) != "https://sentry.example.com/self-hosted" {
		t.Fatalf("SENTRY_URL = %q, want self-hosted root URL", strings.TrimSpace(string(seen)))
	}
}

func TestRunAction_SentryLoginSurfacesFailure(t *testing.T) {
	binDir := t.TempDir()
	writeHarnessExecutable(t, binDir, "sentry", "#!/bin/sh\necho denied\nexit 1\n")
	t.Setenv("PATH", binDir)

	h := NewHarness(config.OhMyPiConfig{}, "")
	_, err := h.RunAction(context.Background(), adapter.HarnessActionRequest{Action: "login_provider", Provider: "sentry"})
	if err == nil {
		t.Fatal("RunAction() error = nil, want sentry auth login failure")
	}
	if !strings.Contains(err.Error(), "sentry auth login") || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("RunAction() error = %q, want sentry auth login stderr", err)
	}
}

func TestRunAction_SentryLoginClearsInheritedURLWhenUnset(t *testing.T) {
	binDir := t.TempDir()
	seenPath := filepath.Join(binDir, "seen-empty-url.txt")
	writeHarnessExecutable(t, binDir, "sentry", "#!/bin/sh\nif [ \"$1\" = \"auth\" ] && [ \"$2\" = \"login\" ]; then\n  printf '%s' \"$SENTRY_URL\" > \"$SEEN_PATH\"\n  exit 0\nfi\nexit 1\n")
	t.Setenv("PATH", binDir)
	t.Setenv("SEEN_PATH", seenPath)
	t.Setenv("SENTRY_URL", "https://ambient.example.com")

	h := NewHarness(config.OhMyPiConfig{}, "")
	result, err := h.RunAction(context.Background(), adapter.HarnessActionRequest{Action: "login_provider", Provider: "sentry"})
	if err != nil {
		t.Fatalf("RunAction() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("result = %+v, want success", result)
	}
	seen, err := os.ReadFile(seenPath)
	if err != nil {
		t.Fatalf("read seen url: %v", err)
	}
	if strings.TrimSpace(string(seen)) != "" {
		t.Fatalf("SENTRY_URL = %q, want empty for default host login", strings.TrimSpace(string(seen)))
	}
}
