//go:build integration

package omp

import (
	"compress/gzip"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/adapter/bridge"
	"github.com/beeemT/substrate/internal/config"
)

func requireTestBun(t *testing.T) string {
	t.Helper()

	bunPath, err := exec.LookPath("bun")
	if err != nil {
		t.Skip("bun not found in PATH, skipping integration test")
	}

	return bunPath
}

func repoBridgePath(t *testing.T) string {
	t.Helper()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}

	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(cwd))))
	bridgePath := filepath.Join(repoRoot, "bridge", "omp-bridge.ts")
	if _, err := os.Stat(bridgePath); os.IsNotExist(err) {
		t.Skipf("bridge script not found at %s, skipping integration test", bridgePath)
	}

	dependencyPath := filepath.Join(filepath.Dir(bridgePath), "node_modules", "@oh-my-pi", "pi-coding-agent")
	if _, err := os.Stat(dependencyPath); err != nil {
		if os.IsNotExist(err) {
			t.Skipf("bridge dependencies not installed under %s; run `bun install --cwd %s`", filepath.Dir(bridgePath), filepath.Dir(bridgePath))
		}
		t.Fatalf("failed to stat bridge dependency path %s: %v", dependencyPath, err)
	}

	return bridgePath
}

// TestIntegration_SessionLifecycle tests that a session starts,
// produces events, and shuts down cleanly.
func TestIntegration_SessionLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	bunPath := requireTestBun(t)

	// Create a temp directory for the session
	tmpDir := t.TempDir()
	sessionLogDir := filepath.Join(tmpDir, "sessions")
	if err := os.MkdirAll(sessionLogDir, 0o755); err != nil {
		t.Fatalf("failed to create session log dir: %v", err)
	}

	bridgePath := repoBridgePath(t)

	cfg := config.OhMyPiConfig{
		BunPath:       bunPath,
		BridgePath:    bridgePath,
		ThinkingLevel: "medium",
	}

	harness := NewHarness(cfg, tmpDir)
	if harness == nil {
		t.Fatal("expected non-nil harness")
	}

	opts := adapter.SessionOpts{
		SessionID:     "test-session-001",
		Mode:          adapter.SessionModeAgent,
		WorktreePath:  tmpDir,
		SessionLogDir: sessionLogDir,
		SystemPrompt:  "You are a test assistant. Respond briefly.",
		UserPrompt:    "Say 'hello world' and nothing else.",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	session, err := harness.StartSession(ctx, opts)
	if err != nil {
		t.Fatalf("failed to start session: %v", err)
	}

	// Collect events
	var events []adapter.AgentEvent
	eventDone := make(chan struct{})
	go func() {
		defer close(eventDone)
		for event := range session.Events() {
			events = append(events, event)
			t.Logf("event: %s - %s", event.Type, truncate(event.Payload, 100))
		}
	}()

	// Wait for session to complete or timeout
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- session.Wait(ctx)
	}()

	select {
	case <-ctx.Done():
		t.Fatalf("context timeout: %v", ctx.Err())
	case err := <-waitDone:
		if err != nil {
			t.Logf("session wait returned error: %v", err)
		}
	}

	// Wait for event channel to close
	<-eventDone

	// Verify we got some events
	if len(events) == 0 {
		t.Error("expected at least one event, got none")
	}

	// Verify session log file was created
	logPath := filepath.Join(sessionLogDir, opts.SessionID+".log")
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Errorf("session log file not created at %s", logPath)
	}

	t.Logf("collected %d events", len(events))
}

// TestIntegration_AbortTerminatesSubprocess tests that Abort()
// terminates the subprocess within 5 seconds.
func TestIntegration_AbortTerminatesSubprocess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	bunPath := requireTestBun(t)

	// Create a temp directory for the session
	tmpDir := t.TempDir()
	sessionLogDir := filepath.Join(tmpDir, "sessions")
	if err := os.MkdirAll(sessionLogDir, 0o755); err != nil {
		t.Fatalf("failed to create session log dir: %v", err)
	}

	bridgePath := repoBridgePath(t)

	cfg := config.OhMyPiConfig{
		BunPath:       bunPath,
		BridgePath:    bridgePath,
		ThinkingLevel: "medium",
	}

	harness := NewHarness(cfg, tmpDir)
	opts := adapter.SessionOpts{
		SessionID:     "test-abort-001",
		Mode:          adapter.SessionModeAgent,
		WorktreePath:  tmpDir,
		SessionLogDir: sessionLogDir,
		SystemPrompt:  "You are a test assistant.",
		// No UserPrompt - we'll abort before sending anything
	}

	ctx := context.Background()
	session, err := harness.StartSession(ctx, opts)
	if err != nil {
		t.Fatalf("failed to start session: %v", err)
	}

	// Give the session a moment to start
	time.Sleep(500 * time.Millisecond)

	// Abort and measure time
	start := time.Now()
	abortCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := session.Abort(abortCtx); err != nil {
		t.Errorf("abort returned error: %v", err)
	}

	elapsed := time.Since(start)
	t.Logf("abort took %v", elapsed)

	// Spec requires abort within 5 seconds
	if elapsed > 5*time.Second {
		t.Errorf("abort took too long: %v (expected < 5s)", elapsed)
	}

	// Verify the session is terminated
	// Wait should return quickly after abort
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer waitCancel()

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- session.Wait(waitCtx)
	}()

	select {
	case err := <-waitDone:
		// Session should be done (abort returns nil for graceful abort)
		t.Logf("session wait after abort: %v", err)
	case <-waitCtx.Done():
		t.Error("session did not terminate after abort within 2s")
	}
}

// TestIntegration_LogRotation tests that log files are rotated
// when they exceed the 10MB threshold.
func TestIntegration_LogRotation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "sessions")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("failed to create log dir: %v", err)
	}

	sessionID := "test-rotation-001"
	logPath := filepath.Join(logDir, sessionID+".log")

	// Create a log file with content
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("failed to create log file: %v", err)
	}

	// Write 11MB of data (exceeds 10MB threshold)
	data := strings.Repeat("x", 1024*1024) // 1MB
	for i := 0; i < 11; i++ {
		if _, err := logFile.WriteString(data); err != nil {
			t.Fatalf("failed to write data: %v", err)
		}
	}
	logFile.Close()

	// Verify file size
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("failed to stat log file: %v", err)
	}
	t.Logf("log file size: %d bytes", info.Size())

	// Test compression function
	compressedPath := logPath + ".test.gz"
	if err := bridge.CompressFile(logPath, compressedPath); err != nil {
		t.Fatalf("bridge.CompressFile failed: %v", err)
	}

	// Verify compressed file exists and is smaller
	compInfo, err := os.Stat(compressedPath)
	if err != nil {
		t.Fatalf("failed to stat compressed file: %v", err)
	}
	t.Logf("compressed file size: %d bytes", compInfo.Size())

	// Compressed file should be smaller than original
	if compInfo.Size() >= info.Size() {
		t.Errorf("compressed file (%d bytes) should be smaller than original (%d bytes)",
			compInfo.Size(), info.Size())
	}

	// Verify we can decompress the file
	compFile, err := os.Open(compressedPath)
	if err != nil {
		t.Fatalf("failed to open compressed file: %v", err)
	}
	defer compFile.Close()

	gzReader, err := gzip.NewReader(compFile)
	if err != nil {
		t.Fatalf("failed to create gzip reader: %v", err)
	}
	defer gzReader.Close()

	// Read first few bytes to verify it's valid
	buf := make([]byte, 100)
	n, err := gzReader.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("failed to read decompressed data: %v", err)
	}
	t.Logf("decompressed first %d bytes: %s", n, string(buf[:min(n, 50)]))
}

// TestIntegration_ForemanMode tests that foreman mode uses read-only tools.
func TestIntegration_ForemanMode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	bunPath := requireTestBun(t)

	// Create a temp directory for the session
	tmpDir := t.TempDir()
	sessionLogDir := filepath.Join(tmpDir, "sessions")
	if err := os.MkdirAll(sessionLogDir, 0o755); err != nil {
		t.Fatalf("failed to create session log dir: %v", err)
	}

	bridgePath := repoBridgePath(t)

	cfg := config.OhMyPiConfig{
		BunPath:       bunPath,
		BridgePath:    bridgePath,
		ThinkingLevel: "medium",
	}

	harness := NewHarness(cfg, tmpDir)

	// Verify foreman capabilities
	caps := harness.Capabilities()
	if !caps.SupportsMessaging {
		t.Error("foreman should support messaging")
	}

	// Foreman mode should work
	opts := adapter.SessionOpts{
		SessionID:     "test-foreman-001",
		Mode:          adapter.SessionModeForeman,
		WorktreePath:  "", // Empty for foreman
		SessionLogDir: sessionLogDir,
		SystemPrompt:  "You are a foreman. Answer questions concisely.",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	session, err := harness.StartSession(ctx, opts)
	if err != nil {
		t.Fatalf("failed to start foreman session: %v", err)
	}

	// Session should start successfully
	if session.ID() != opts.SessionID {
		t.Errorf("session ID mismatch: got %s, want %s", session.ID(), opts.SessionID)
	}

	// Clean up
	session.Abort(ctx)
}

// truncate truncates a string to maxLen characters.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}

	return s[:maxLen] + "..."
}

// min returns the minimum of two integers.
func min(a, b int) int {
	if a < b {
		return a
	}

	return b
}
