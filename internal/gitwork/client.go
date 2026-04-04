package gitwork

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Client wraps the git-work CLI for worktree management.
type Client struct {
	// BinPath is the path to the git-work binary.
	// If empty, "git-work" is looked up in PATH.
	BinPath string
}

// NewClient creates a new git-work client.
func NewClient(binPath string) *Client {
	return &Client{BinPath: binPath}
}

// bin returns the path to the git-work binary.
func (c *Client) bin() string {
	if c.BinPath != "" {
		return c.BinPath
	}

	return "git-work"
}

// Init converts a plain git repository into the git-work layout.
func (c *Client) Init(ctx context.Context, repoDir string) error {
	cmd := exec.CommandContext(ctx, c.bin(), "init")
	cmd.Dir = repoDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git-work init in %s: %w (output: %s)", repoDir, err, strings.TrimSpace(string(output)))
	}

	return nil
}

// Checkout creates a new worktree for the given branch and returns its path.
// The branch is created from the current HEAD if it doesn't exist.
// The worktree is created in a subdirectory named after the branch.
func (c *Client) Checkout(ctx context.Context, repoDir, branch string) (string, error) {
	// Use -b to create a new worktree for the branch.
	args := []string{"checkout", "-b", branch}
	cmd := exec.CommandContext(ctx, c.bin(), args...)
	cmd.Dir = repoDir
	// git-work checkout -b outputs the path to stdout
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("git-work checkout -b %s: %w (stderr: %s)", branch, err, string(exitErr.Stderr))
		}

		return "", fmt.Errorf("git-work checkout -b %s: %w", branch, err)
	}

	path := strings.TrimSpace(string(output))
	if path == "" {
		return "", fmt.Errorf("git-work checkout -b %s: empty output", branch)
	}

	return path, nil
}

// Clone clones a remote repository into the workspace using git-work clone.
// It runs git-work clone <remoteURL> with cmd.Dir = parentDir.
// git-work clone creates <parentDir>/<repo-name>/.bare/ layout and a worktree for HEAD.
// It parses stdout for the directory path and validates the path is under parentDir.
// Returns the absolute path to the created repository root.
func (c *Client) Clone(ctx context.Context, parentDir, remoteURL string) (string, error) {
	args := []string{"clone", remoteURL}
	cmd := exec.CommandContext(ctx, c.bin(), args...)
	cmd.Dir = parentDir

	// git-work clone outputs the repository path to stdout
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			slog.Error("git-work clone failed", "url", remoteURL, "dir", parentDir, "err", err, "stderr", string(exitErr.Stderr))
			return "", fmt.Errorf("git-work clone %s: %w (stderr: %s)", remoteURL, err, string(exitErr.Stderr))
		}

		slog.Error("git-work clone failed", "url", remoteURL, "dir", parentDir, "err", err)
		return "", fmt.Errorf("git-work clone %s: %w", remoteURL, err)
	}

	path := strings.TrimSpace(string(output))
	if path == "" {
		slog.Error("git-work clone returned empty output", "url", remoteURL, "dir", parentDir)
		return "", fmt.Errorf("git-work clone %s: empty output", remoteURL)
	}

	// Validate the returned path is under parentDir (path traversal guard)
	rel, err := filepath.Rel(parentDir, path)
	if err != nil {
		slog.Error("failed to resolve clone path", "path", path, "parent", parentDir, "err", err)
		return "", fmt.Errorf("resolve clone path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, "../") {
		slog.Error("clone path escapes parent directory", "path", path, "parent", parentDir)
		return "", fmt.Errorf("invalid clone path %q: escapes parent directory", path)
	}

	return path, nil
}

// Context cancellation is honored: if ctx is cancelled, the command will be
// terminated and an error returned. Partial JSON output on cancellation is
// handled by parseListJSON which returns an empty slice for empty input.
func (c *Client) List(ctx context.Context, repoDir string) ([]Worktree, error) {
	args := []string{"list", "--format=json"}
	cmd := exec.CommandContext(ctx, c.bin(), args...)
	cmd.Dir = repoDir

	// git-work outputs JSON to stderr with --format=json
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git-work list: %w (output: %s)", err, string(output))
	}

	return parseListJSON(output, repoDir)
}

// gitWorkListResponse represents the top-level JSON structure from git-work list --format=json.
type gitWorkListResponse struct {
	Data struct {
		Worktrees []worktreeJSON `json:"worktrees"`
	} `json:"data"`
	Messages []struct {
		Level string `json:"level"`
		Text  string `json:"text"`
	} `json:"messages"`
}

// worktreeJSON represents a single worktree entry from git-work list --format=json.
type worktreeJSON struct {
	Dir     string `json:"dir"`
	Branch  string `json:"branch"`
	Current bool   `json:"current"`
}

// parseListJSON parses the JSON output from git-work list --format=json.
func parseListJSON(data []byte, repoDir string) ([]Worktree, error) {
	if len(data) == 0 {
		return []Worktree{}, nil
	}

	var resp gitWorkListResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse git-work list JSON: %w", err)
	}

	entries := resp.Data.Worktrees
	worktrees := make([]Worktree, len(entries))
	for i, e := range entries {
		// Convert to full path if not already absolute
		path := e.Dir
		if !filepath.IsAbs(path) {
			path = filepath.Join(repoDir, path)
		}

		// Validate that resolved path is under repoDir (prevent path traversal)
		// Only check relative paths - absolute paths are used as-is
		if !filepath.IsAbs(e.Dir) {
			rel, err := filepath.Rel(repoDir, path)
			if err != nil {
				return nil, fmt.Errorf("resolve worktree path: %w", err)
			}
			if rel == ".." || strings.HasPrefix(rel, "../") {
				return nil, fmt.Errorf("invalid worktree path %q: escapes repo directory", e.Dir)
			}
		}

		worktrees[i] = Worktree{
			Path:   path,
			Branch: e.Branch,
			// Current worktree is main if it's the default branch (typically 'main' or 'master')
			// We also check if the branch is named 'main' or 'master' as a fallback
			IsMain: e.Current || e.Branch == "main" || e.Branch == "master",
		}
	}

	return worktrees, nil
}

// Remove deletes the worktree for the given branch.
func (c *Client) Remove(ctx context.Context, repoDir, branch string) error {
	// Use -- to prevent flag injection from branch names starting with -
	args := []string{"rm", "--yes", "--", branch}
	cmd := exec.CommandContext(ctx, c.bin(), args...)
	cmd.Dir = repoDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git-work rm %s: %w (output: %s)", branch, err, string(output))
	}

	return nil
}

// Sync synchronizes worktrees with remote state, pruning deleted branches.
func (c *Client) Sync(ctx context.Context, repoDir string) error {
	args := []string{"sync"}
	cmd := exec.CommandContext(ctx, c.bin(), args...)
	cmd.Dir = repoDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git-work sync: %w (output: %s)", err, string(output))
	}

	return nil
}

// CheckInstalled verifies that git-work is available in PATH or at BinPath.
func (c *Client) CheckInstalled() error {
	bin := c.bin()
	if _, err := exec.LookPath(bin); err != nil {
		return fmt.Errorf("git-work not found in PATH: %w", err)
	}

	return nil
}

// GetMainWorktree returns the path to the main worktree for a repository.
func (c *Client) GetMainWorktree(ctx context.Context, repoDir string) (string, error) {
	worktrees, err := c.List(ctx, repoDir)
	if err != nil {
		return "", err
	}

	for _, wt := range worktrees {
		if wt.IsMain {
			return wt.Path, nil
		}
	}

	return "", fmt.Errorf("no main worktree found in %s", repoDir)
}

// IsGitWorkRepo checks if the given directory is a git-work repository
// by looking for a .bare subdirectory.
func IsGitWorkRepo(dir string) bool {
	barePath := filepath.Join(dir, ".bare")
	info, err := os.Stat(barePath)

	return err == nil && info.IsDir()
}

// PullMainWorktree runs git pull --ff-only in the main worktree of the given repo.
// It returns the output and any error. Unlike other methods, this does not wrap
// the error - the caller should handle it appropriately (as a warning, not a failure).
func (c *Client) PullMainWorktree(ctx context.Context, repoDir string) (string, error) {
	// First get the main worktree path
	mainDir, err := c.GetMainWorktree(ctx, repoDir)
	if err != nil {
		return "", fmt.Errorf("get main worktree: %w", err)
	}

	// Run git pull --ff-only in the main worktree
	cmd := exec.CommandContext(ctx, "git", "pull", "--ff-only")
	cmd.Dir = mainDir

	output, err := cmd.CombinedOutput()

	return string(output), err
}

// PullResult represents the result of pulling a single repo's main worktree.
type PullResult struct {
	RepoName string
	Success  bool
	Output   string
	Error    error
}
