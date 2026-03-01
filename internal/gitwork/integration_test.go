//go:build integration

package gitwork

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestIntegration_Client_Checkout tests the full checkout flow with git-work CLI.
func TestIntegration_Client_Checkout(t *testing.T) {
	if err := NewClient("").CheckInstalled(); err != nil {
		t.Skipf("git-work not installed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a temporary git-work repo
	repoDir, err := createTestGitWorkRepo(t)
	if err != nil {
		t.Fatalf("create test repo: %v", err)
	}

	client := NewClient("")

	// Checkout a new branch
	branch := "test-branch"
	path, err := client.Checkout(ctx, repoDir, branch)
	if err != nil {
		t.Fatalf("Checkout() error = %v", err)
	}

	// Verify worktree exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Errorf("worktree path %q does not exist", path)
	}

	// Verify the path ends with the branch name
	if filepath.Base(path) != branch {
		t.Errorf("path base = %q, want %q", filepath.Base(path), branch)
	}

	// Cleanup: remove the worktree
	if err := client.Remove(ctx, repoDir, branch); err != nil {
		t.Errorf("Remove() cleanup error = %v", err)
	}
}

// TestIntegration_Client_List tests listing worktrees with git-work CLI.
func TestIntegration_Client_List(t *testing.T) {
	if err := NewClient("").CheckInstalled(); err != nil {
		t.Skipf("git-work not installed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a temporary git-work repo
	repoDir, err := createTestGitWorkRepo(t)
	if err != nil {
		t.Fatalf("create test repo: %v", err)
	}

	client := NewClient("")

	// List worktrees (should have at least main)
	worktrees, err := client.List(ctx, repoDir)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	if len(worktrees) == 0 {
		t.Error("List() returned no worktrees, expected at least main")
	}

	// Find main worktree
	var hasMain bool
	for _, wt := range worktrees {
		if wt.Branch == "main" && wt.IsMain {
			hasMain = true
			break
		}
	}
	if !hasMain {
		t.Error("List() did not include main worktree")
	}
}

// TestIntegration_Client_Remove tests removing a worktree with git-work CLI.
func TestIntegration_Client_Remove(t *testing.T) {
	if err := NewClient("").CheckInstalled(); err != nil {
		t.Skipf("git-work not installed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a temporary git-work repo
	repoDir, err := createTestGitWorkRepo(t)
	if err != nil {
		t.Fatalf("create test repo: %v", err)
	}

	client := NewClient("")

	// First create a worktree
	branch := "test-remove-branch"
	path, err := client.Checkout(ctx, repoDir, branch)
	if err != nil {
		t.Fatalf("Checkout() error = %v", err)
	}

	// Verify it exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatalf("worktree path %q does not exist after checkout", path)
	}

	// Remove it
	if err := client.Remove(ctx, repoDir, branch); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	// Verify it's gone
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("worktree path %q still exists after remove", path)
	}
}

// TestIntegration_WorkspaceInit tests the full workspace init flow.
func TestIntegration_WorkspaceInit(t *testing.T) {
	// Create a temp directory for the workspace
	workspaceDir, err := os.MkdirTemp("", "substrate-integration-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(workspaceDir)

	// Create a mock git-work repo structure
	repoDir := filepath.Join(workspaceDir, "test-repo")
	if err := os.MkdirAll(filepath.Join(repoDir, ".bare"), 0o755); err != nil {
		t.Fatalf("create .bare: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repoDir, "main"), 0o755); err != nil {
		t.Fatalf("create main: %v", err)
	}

	// Initialize workspace
	ws, err := InitWorkspace(workspaceDir, "test-workspace")
	if err != nil {
		t.Fatalf("InitWorkspace() error = %v", err)
	}

	// Verify workspace ID is valid ULID
	if err := ValidateWorkspaceID(ws.ID); err != nil {
		t.Errorf("workspace ID is not valid ULID: %v", err)
	}

	// Discover repos
	repos, err := DiscoverRepos(workspaceDir)
	if err != nil {
		t.Fatalf("DiscoverRepos() error = %v", err)
	}

	if len(repos) != 1 {
		t.Errorf("DiscoverRepos() found %d repos, want 1", len(repos))
	}
	if len(repos) > 0 && repos[0] != repoDir {
		t.Errorf("DiscoverRepos() found %q, want %q", repos[0], repoDir)
	}
}

// createTestGitWorkRepo creates a temporary git-work repository for testing.
func createTestGitWorkRepo(t *testing.T) (string, error) {
	t.Helper()

	// Create temp dir for the repo
	repoDir, err := os.MkdirTemp("", "substrate-gitwork-test-*")
	if err != nil {
		return "", err
	}

	// Initialize a regular git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = repoDir
	if output, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(repoDir)
		return "", fmt.Errorf("git init: %w (output: %s)", err, string(output))
	}

	// Configure git user
	cmd = exec.Command("git", "config", "user.email", "test@test.com")
	cmd.Dir = repoDir
	if output, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(repoDir)
		return "", fmt.Errorf("git config email: %w (output: %s)", err, string(output))
	}

	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = repoDir
	if output, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(repoDir)
		return "", fmt.Errorf("git config name: %w (output: %s)", err, string(output))
	}

	// Create an initial commit (required for git-work init)
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# Test Repo\n"), 0o644); err != nil {
		os.RemoveAll(repoDir)
		return "", fmt.Errorf("create README: %w", err)
	}

	cmd = exec.Command("git", "add", "README.md")
	cmd.Dir = repoDir
	if output, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(repoDir)
		return "", fmt.Errorf("git add: %w (output: %s)", err, string(output))
	}

	cmd = exec.Command("git", "commit", "-m", "Initial commit")
	cmd.Dir = repoDir
	if output, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(repoDir)
		return "", fmt.Errorf("git commit: %w (output: %s)", err, string(output))
	}

	// Convert to git-work layout
	cmd = exec.Command("git-work", "init")
	cmd.Dir = repoDir
	if output, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(repoDir)
		return "", fmt.Errorf("git-work init: %w (output: %s)", err, string(output))
	}

	// Register cleanup
	t.Cleanup(func() { os.RemoveAll(repoDir) })

	return repoDir, nil
}
