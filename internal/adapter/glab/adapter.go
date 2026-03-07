// Package glab implements the glab CLI wrapper adapter.
//
// The GlabAdapter listens to repository lifecycle events and manages GitLab
// Merge Requests via the glab CLI:
//
//   - worktree.created → glab mr create --draft ...
//   - work_item.completed → glab mr update --draft=false ...
//
// All glab failures are logged at WARN and never block the workflow.
// The adapter is always registered regardless of configuration.
package glab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"

	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
)

// commandRunner executes an external command in dir and returns its combined output.
// Injected for testing.
type commandRunner func(ctx context.Context, dir string, name string, args ...string) ([]byte, error)

// execRunner is the real commandRunner that delegates to exec.CommandContext.
func execRunner(ctx context.Context, dir string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

// branchEntry records a repo tracked from a WorktreeCreated event.
type branchEntry struct {
	repo         string
	worktreePath string
}

// GlabAdapter implements adapter.RepoLifecycleAdapter using the glab CLI.
type GlabAdapter struct {
	cfg    config.GlabConfig
	runner commandRunner

	mu      sync.RWMutex
	tracked map[string][]branchEntry // branch → []branchEntry
}

// New creates a GlabAdapter with the given configuration.
func New(cfg config.GlabConfig) *GlabAdapter {
	return newWithRunner(cfg, execRunner)
}

// newWithRunner creates a GlabAdapter with an injectable commandRunner (for tests).
func newWithRunner(cfg config.GlabConfig, runner commandRunner) *GlabAdapter {
	return &GlabAdapter{
		cfg:     cfg,
		runner:  runner,
		tracked: make(map[string][]branchEntry),
	}
}

// Name returns the adapter identifier.
func (a *GlabAdapter) Name() string { return "glab" }

// OnEvent dispatches system events to the appropriate handler.
// All handlers follow the warn-on-failure policy: errors are logged but never
// returned, so glab failures never block the workflow.
func (a *GlabAdapter) OnEvent(ctx context.Context, event domain.SystemEvent) error {
	switch domain.EventType(event.EventType) {
	case domain.EventWorktreeCreated:
		if err := a.onWorktreeCreated(ctx, event.Payload); err != nil {
			slog.Warn("glab: worktree created handler failed", "error", err)
		}
	case domain.EventWorkItemCompleted:
		if err := a.onWorkItemCompleted(ctx, event.Payload); err != nil {
			slog.Warn("glab: work item completed handler failed", "error", err)
		}
	}
	return nil
}

// worktreePayload mirrors orchestrator.WorktreeCreatedPayload.
// Defined locally to avoid cross-package dependency.
type worktreePayload struct {
	WorkspaceID   string `json:"workspace_id"`
	Repository    string `json:"repository"`
	Branch        string `json:"branch"`
	WorktreePath  string `json:"worktree_path"`
	WorkItemTitle string `json:"work_item_title"`
	SubPlan       string `json:"sub_plan"`
}

// completedPayload is the expected shape of EventWorkItemCompleted.
// Emitters must populate Branch so the glab adapter can locate the MRs.
// external_id is included for symmetry with the linear adapter's OnEvent.
type completedPayload struct {
	Branch     string `json:"branch"`
	ExternalID string `json:"external_id"`
}

func (a *GlabAdapter) onWorktreeCreated(ctx context.Context, payload string) error {
	var p worktreePayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return fmt.Errorf("unmarshal worktree payload: %w", err)
	}
	if p.Branch == "" || p.WorktreePath == "" {
		return fmt.Errorf("worktree payload missing branch or worktree_path")
	}

	title := mrTitle(p.WorkItemTitle, p.Branch)
	description := strings.TrimSpace(p.SubPlan)
	if a.mrExists(ctx, p.WorktreePath, p.Branch) {
		slog.Info("glab: MR already exists, skipping create", "branch", p.Branch)
	} else if err := a.createMR(ctx, p.WorktreePath, p.Branch, title, description); err != nil {
		// glab failure — warn but track anyway so un-draft can still be attempted later
		slog.Warn("glab: mr create failed", "repo", p.Repository, "branch", p.Branch, "error", err)
	}

	a.mu.Lock()
	a.tracked[p.Branch] = append(a.tracked[p.Branch], branchEntry{
		repo:         p.Repository,
		worktreePath: p.WorktreePath,
	})
	a.mu.Unlock()

	return nil
}

func (a *GlabAdapter) onWorkItemCompleted(ctx context.Context, payload string) error {
	var p completedPayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return fmt.Errorf("unmarshal completed payload: %w", err)
	}
	if p.Branch == "" {
		// Branch not present in payload — cannot determine which MRs to update.
		slog.Warn("glab: work_item.completed payload has no branch; skipping mr update")
		return nil
	}

	a.mu.RLock()
	entries := append([]branchEntry(nil), a.tracked[p.Branch]...)
	a.mu.RUnlock()

	for _, entry := range entries {
		if err := a.markMRReady(ctx, entry.worktreePath, p.Branch); err != nil {
			slog.Warn("glab: mr update --draft=false failed",
				"repo", entry.repo, "branch", p.Branch, "error", err)
		}
	}
	return nil
}

// createMR runs `glab mr create --draft --source-branch <branch> --title <title> [--description ...] [--reviewer ...] [--label ...] --yes`.
func (a *GlabAdapter) createMR(ctx context.Context, dir, branch, title, description string) error {
	args := []string{
		"mr", "create",
		"--draft",
		"--source-branch", branch,
		"--title", title,
		"--yes",
	}
	if strings.TrimSpace(description) != "" {
		args = append(args, "--description", description)
	}
	for _, r := range a.cfg.Reviewers {
		args = append(args, "--reviewer", r)
	}
	for _, l := range a.cfg.Labels {
		args = append(args, "--label", l)
	}

	out, err := a.runner(ctx, dir, "glab", args...)
	if err != nil {
		return fmt.Errorf("glab mr create: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	url := parseMRURL(out)
	if url != "" {
		slog.Info("glab: MR created", "branch", branch, "url", url)
	}
	return nil
}

// glabMRView is the minimal subset of `glab mr view --output json` we need.
type glabMRView struct {
	IID    int    `json:"iid"`
	State  string `json:"state"`
	WebURL string `json:"web_url"`
}

// mrExists returns true when an open or merged MR for the given source branch
// already exists in the repo at dir. It calls `glab mr view --source-branch <branch>
// --output json` and parses the JSON response; a non-zero exit code means no MR.
// Errors (including glab not-found) are treated as "no MR" — we always prefer to
// attempt creation over silently skipping.
func (a *GlabAdapter) mrExists(ctx context.Context, dir, branch string) bool {
	out, err := a.runner(ctx, dir, "glab",
		"mr", "view",
		"--source-branch", branch,
		"--output", "json",
	)
	if err != nil {
		// Non-zero exit = MR not found or glab error; treat as absent.
		return false
	}
	var mr glabMRView
	if err := json.Unmarshal(out, &mr); err != nil {
		return false
	}
	return mr.IID > 0
}

// markMRReady runs `glab mr update --source-branch <branch> --draft=false --yes`.
func (a *GlabAdapter) markMRReady(ctx context.Context, dir, branch string) error {
	out, err := a.runner(ctx, dir, "glab",
		"mr", "update",
		"--source-branch", branch,
		"--draft=false",
		"--yes",
	)
	if err != nil {
		return fmt.Errorf("glab mr update: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// parseMRURL scans command output for the first line containing a GitLab MR URL.
func parseMRURL(output []byte) string {
	for _, line := range bytes.Split(output, []byte("\n")) {
		s := strings.TrimSpace(string(line))
		if strings.Contains(s, "/-/merge_requests/") {
			return s
		}
	}
	return ""
}

// mrTitle derives the MR title using the following priority:
//  1. workItemTitle if non-empty
//  2. Parsed from branch slug: "sub-LIN-FOO-123-fix-auth-flow" → "Fix auth flow [LIN-FOO-123]"
//  3. Branch name as fallback
func mrTitle(workItemTitle, branch string) string {
	if workItemTitle != "" {
		return workItemTitle
	}
	return titleFromBranch(branch)
}

// titleFromBranch converts a branch slug to a human-readable MR title.
//
//	"sub-LIN-FOO-123-fix-auth-flow"  → "Fix auth flow [LIN-FOO-123]"
//	"sub-MAN-42-add-new-feature"     → "Add new feature [MAN-42]"
//	"some-other-branch"              → "Some other branch"
func titleFromBranch(branch string) string {
	// Strip "sub-" prefix
	slug := strings.TrimPrefix(branch, "sub-")
	if slug == branch {
		// No sub- prefix — replace hyphens with spaces and capitalize
		return capitalize(strings.ReplaceAll(branch, "-", " "))
	}

	// Try to extract a known external-ID prefix
	var externalID, remainder string

	switch {
	case strings.HasPrefix(slug, "LIN-"):
		// LIN-{KEY}-{NUM}-rest  e.g. LIN-FOO-123-fix-auth-flow
		// KEY and NUM are separated by hyphens, so match LIN-\w+-\d+-
		parts := strings.SplitN(slug, "-", 4) // ["LIN", "KEY", "NUM", "rest"]
		if len(parts) >= 3 {
			externalID = strings.Join(parts[:3], "-") // LIN-KEY-NUM
			if len(parts) == 4 {
				remainder = parts[3]
			}
		}
	case strings.HasPrefix(slug, "MAN-"):
		// MAN-{N}-rest  e.g. MAN-42-add-new-feature
		parts := strings.SplitN(slug, "-", 3) // ["MAN", "N", "rest"]
		if len(parts) >= 2 {
			externalID = strings.Join(parts[:2], "-") // MAN-N
			if len(parts) == 3 {
				remainder = parts[2]
			}
		}
	}

	if externalID == "" {
		// Unrecognised format — use the full slug as title
		return capitalize(strings.ReplaceAll(slug, "-", " "))
	}

	if remainder == "" {
		return externalID
	}

	human := capitalize(strings.ReplaceAll(remainder, "-", " "))
	return fmt.Sprintf("%s [%s]", human, externalID)
}

// capitalize uppercases the first rune of s and leaves the rest unchanged.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	if runes[0] >= 'a' && runes[0] <= 'z' {
		runes[0] -= 32
	}
	return string(runes)
}
