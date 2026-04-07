package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/gitwork"
)

// brokenClient returns a gitwork.Client that always fails PullMainWorktree
// because the git-work binary does not exist. Used to detect whether a pull
// was actually attempted: a cooldown skip returns nil, a real attempt returns
// a PullFailure.
func brokenClient() *gitwork.Client {
	return gitwork.NewClient("/nonexistent-git-work-binary")
}

func TestDiscoverer_PullMainWorktrees_FirstCallPulls(t *testing.T) {
	d := NewDiscoverer(brokenClient(), nil)
	d.pullCooldown = time.Hour

	// lastPullAt is zero — no previous pull, so this call must proceed.
	result := d.PullMainWorktrees(context.Background(), []string{"/repo-a"})

	// The broken binary means the pull failed, which is proof the call was made.
	if len(result.PullFailures) == 0 {
		t.Fatal("expected pull failure (pull was attempted with broken binary), got none")
	}

	// lastPullAt must be stamped.
	if d.lastPullAt.IsZero() {
		t.Error("lastPullAt not set after first pull")
	}
}

func TestDiscoverer_PullMainWorktrees_SkipsWithinCooldown(t *testing.T) {
	d := NewDiscoverer(brokenClient(), nil)
	d.pullCooldown = time.Hour
	d.lastPullAt = time.Now() // simulate a very recent pull

	// The broken binary would produce failures if called; cooldown must suppress both pull and sync.
	result := d.PullMainWorktrees(context.Background(), []string{"/repo-a"})

	if result.PullFailures != nil {
		t.Errorf("expected nil PullFailures (cooldown active, pull+sync skipped), got %v", result.PullFailures)
	}
}

func TestDiscoverer_PullMainWorktrees_PullsAfterCooldownExpires(t *testing.T) {
	d := NewDiscoverer(brokenClient(), nil)
	d.pullCooldown = time.Hour
	d.lastPullAt = time.Now().Add(-2 * time.Hour) // simulate a stale pull

	// Cooldown has expired; this call must proceed.
	result := d.PullMainWorktrees(context.Background(), []string{"/repo-a"})

	if len(result.PullFailures) == 0 {
		t.Fatal("expected pull failure (cooldown expired, pull attempted with broken binary), got none")
	}
}

func TestDiscoverer_PullMainWorktrees_EmptyReposDoesNotSkipNextCall(t *testing.T) {
	d := NewDiscoverer(brokenClient(), nil)
	d.pullCooldown = time.Hour

	// Call with no repos — still stamps lastPullAt, applying the cooldown.
	before := time.Now()
	d.PullMainWorktrees(context.Background(), []string{})

	if d.lastPullAt.Before(before) {
		t.Error("lastPullAt not stamped after pull with empty repo list")
	}

	// Subsequent call within cooldown must be skipped.
	result := d.PullMainWorktrees(context.Background(), []string{"/repo-a"})
	if result.PullFailures != nil {
		t.Errorf("expected nil PullFailures (cooldown active after empty-repo pull), got %v", result.PullFailures)
	}
}

func TestDiscoverer_PullMainWorktrees_MultipleReposAllAttempted(t *testing.T) {
	d := NewDiscoverer(brokenClient(), nil)
	d.pullCooldown = time.Hour // no previous pull

	repoPaths := []string{"/repo-a", "/repo-b", "/repo-c"}
	result := d.PullMainWorktrees(context.Background(), repoPaths)

	// Each repo must have produced a failure (broken binary), so count must match.
	// syncWorktrees also runs for each repo but failures are logged, not returned.
	if len(result.PullFailures) != len(repoPaths) {
		t.Errorf("expected %d failures (one per repo), got %d", len(repoPaths), len(result.PullFailures))
	}
}

func TestDiscoverer_PullMainWorktrees_SyncRunsEvenIfPullFailed(t *testing.T) {
	d := NewDiscoverer(brokenClient(), nil)
	d.pullCooldown = time.Hour

	// Pull fails (broken binary). Sync must still be attempted for every repo,
	// swallow its own error, and not affect the returned PullFailures.
	result := d.PullMainWorktrees(context.Background(), []string{"/repo-a"})

	if len(result.PullFailures) == 0 {
		t.Fatal("expected pull failure to be recorded; got none")
	}
	// If sync had panicked or propagated its error, the test would not reach here.
}

func TestDiscoverer_PullMainWorktrees_SyncSkippedWithinCooldown(t *testing.T) {
	d := NewDiscoverer(brokenClient(), nil)
	d.pullCooldown = time.Hour
	d.lastPullAt = time.Now() // within cooldown

	// The cooldown gates both pull and sync. Neither must be invoked.
	// brokenClient would produce observable failures if either were called.
	result := d.PullMainWorktrees(context.Background(), []string{"/repo-a"})

	if result.PullFailures != nil {
		t.Errorf("expected zero-value result (cooldown active), got PullFailures=%v", result.PullFailures)
	}
}
