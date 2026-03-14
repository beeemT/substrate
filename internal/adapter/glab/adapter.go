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
	"sort"
	"strings"
	"sync"
	"time"

	coreadapter "github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
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

// branchEntry records durable repo/MR context for one branch.
type branchEntry struct {
	repo         string
	worktreePath string
	ref          string
	url          string
}

// GlabAdapter implements adapter.RepoLifecycleAdapter using the glab CLI.
type GlabAdapter struct {
	cfg       config.GlabConfig
	runner    commandRunner
	eventRepo repository.EventRepository

	mu      sync.RWMutex
	tracked map[string][]branchEntry // branch → []branchEntry
}

// New creates a GlabAdapter with the given configuration.
func New(cfg config.GlabConfig) *GlabAdapter {
	return newWithRunner(cfg, nil, execRunner)
}

// NewWithEventRepo creates a GlabAdapter that persists durable MR metadata.
func NewWithEventRepo(cfg config.GlabConfig, eventRepo repository.EventRepository) *GlabAdapter {
	return newWithRunner(cfg, eventRepo, execRunner)
}

// newWithRunner creates a GlabAdapter with an injectable commandRunner (for tests).
func newWithRunner(cfg config.GlabConfig, eventRepo repository.EventRepository, runner commandRunner) *GlabAdapter {
	return &GlabAdapter{
		cfg:       cfg,
		runner:    runner,
		eventRepo: eventRepo,
		tracked:   make(map[string][]branchEntry),
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
	WorkspaceID   string                    `json:"workspace_id"`
	WorkItemID    string                    `json:"work_item_id"`
	Repository    string                    `json:"repository"`
	Branch        string                    `json:"branch"`
	WorktreePath  string                    `json:"worktree_path"`
	WorkItemTitle string                    `json:"work_item_title"`
	SubPlan       string                    `json:"sub_plan"`
	TrackerRefs   []domain.TrackerReference `json:"tracker_refs"`
}

// completedPayload is the expected shape of EventWorkItemCompleted.
// Emitters must populate Branch so the glab adapter can locate the MRs.
// external_id is included for symmetry with the linear adapter's OnEvent.
type completedPayload struct {
	WorkspaceID string `json:"workspace_id"`
	WorkItemID  string `json:"work_item_id"`
	Branch      string `json:"branch"`
	ExternalID  string `json:"external_id"`
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
	description := appendTrackerFooter(strings.TrimSpace(p.SubPlan), renderGitLabTrackerRefs(p.TrackerRefs))
	var artifact *domain.ReviewArtifact
	if mr, ok := a.mrView(ctx, p.WorktreePath, p.Branch); ok {
		slog.Info("glab: MR already exists, skipping create", "branch", p.Branch)
		artifact = &domain.ReviewArtifact{
			Provider:     "gitlab",
			Kind:         "MR",
			RepoName:     p.Repository,
			Ref:          glabArtifactRef(strings.TrimSpace(mr.WebURL), mr.IID),
			URL:          strings.TrimSpace(mr.WebURL),
			State:        glabArtifactState(mr),
			Branch:       p.Branch,
			WorktreePath: p.WorktreePath,
			UpdatedAt:    time.Now(),
		}
	} else if url, err := a.createMR(ctx, p.WorktreePath, p.Branch, title, description); err != nil {
		slog.Warn("glab: mr create failed", "repo", p.Repository, "branch", p.Branch, "error", err)
	} else {
		artifact = &domain.ReviewArtifact{
			Provider:     "gitlab",
			Kind:         "MR",
			RepoName:     p.Repository,
			Ref:          glabArtifactRef(url, 0),
			URL:          url,
			State:        "draft",
			Branch:       p.Branch,
			WorktreePath: p.WorktreePath,
			Draft:        true,
			UpdatedAt:    time.Now(),
		}
	}

	trackedRef := ""
	trackedURL := ""
	if artifact != nil {
		trackedRef = artifact.Ref
		trackedURL = artifact.URL
	}
	a.mu.Lock()
	a.tracked[p.Branch] = append(a.tracked[p.Branch], branchEntry{
		repo:         p.Repository,
		worktreePath: p.WorktreePath,
		ref:          trackedRef,
		url:          trackedURL,
	})
	a.mu.Unlock()
	if artifact != nil {
		a.recordReviewArtifact(ctx, p.WorkspaceID, p.WorkItemID, *artifact)
	}
	return nil
}

func (a *GlabAdapter) onWorkItemCompleted(ctx context.Context, payload string) error {
	var p completedPayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return fmt.Errorf("unmarshal completed payload: %w", err)
	}
	if p.Branch == "" {
		slog.Warn("glab: work_item.completed payload has no branch; skipping mr update")
		return nil
	}

	entries := a.entriesForCompletion(ctx, p)

	for _, entry := range entries {
		if err := a.markMRReady(ctx, entry.worktreePath, p.Branch); err != nil {
			slog.Warn("glab: mr update --draft=false failed", "repo", entry.repo, "branch", p.Branch, "error", err)
			continue
		}
		if mr, ok := a.mrView(ctx, entry.worktreePath, p.Branch); ok {
			a.recordReviewArtifact(ctx, p.WorkspaceID, p.WorkItemID, domain.ReviewArtifact{
				Provider:     "gitlab",
				Kind:         "MR",
				RepoName:     entry.repo,
				Ref:          glabArtifactRef(strings.TrimSpace(mr.WebURL), mr.IID),
				URL:          strings.TrimSpace(mr.WebURL),
				State:        glabArtifactState(mr),
				Branch:       p.Branch,
				WorktreePath: entry.worktreePath,
				UpdatedAt:    time.Now(),
			})
			continue
		}
		a.recordReviewArtifact(ctx, p.WorkspaceID, p.WorkItemID, domain.ReviewArtifact{
			Provider:     "gitlab",
			Kind:         "MR",
			RepoName:     entry.repo,
			Ref:          entry.ref,
			URL:          entry.url,
			State:        "ready",
			Branch:       p.Branch,
			WorktreePath: entry.worktreePath,
			UpdatedAt:    time.Now(),
		})
	}
	return nil
}

// createMR runs `glab mr create --draft --source-branch <branch> --title <title> [--description ...] [--label ...] --yes`.
func (a *GlabAdapter) createMR(ctx context.Context, dir, branch, title, description string) (string, error) {
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
		return "", fmt.Errorf("glab mr create: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	url := parseMRURL(out)
	if url != "" {
		slog.Info("glab: MR created", "branch", branch, "url", url)
	}
	return url, nil
}

// glabMRView is the minimal subset of `glab mr view --output json` we need.
type glabMRView struct {
	IID            int    `json:"iid"`
	State          string `json:"state"`
	WebURL         string `json:"web_url"`
	Draft          bool   `json:"draft"`
	WorkInProgress bool   `json:"work_in_progress"`
}

// mrView returns the current MR metadata for the branch when one exists.
func (a *GlabAdapter) mrView(ctx context.Context, dir, branch string) (glabMRView, bool) {
	out, err := a.runner(ctx, dir, "glab",
		"mr", "view",
		"--source-branch", branch,
		"--output", "json",
	)
	if err != nil {
		return glabMRView{}, false
	}
	var mr glabMRView
	if err := json.Unmarshal(out, &mr); err != nil {
		return glabMRView{}, false
	}
	if mr.IID <= 0 {
		return glabMRView{}, false
	}
	return mr, true
}

func (a *GlabAdapter) mrExists(ctx context.Context, dir, branch string) bool {
	_, ok := a.mrView(ctx, dir, branch)
	return ok
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

func glabArtifactRef(rawURL string, iid int) string {
	if iid > 0 {
		return fmt.Sprintf("!%d", iid)
	}
	marker := "/-/merge_requests/"
	idx := strings.LastIndex(strings.TrimSpace(rawURL), marker)
	if idx == -1 {
		return ""
	}
	return "!" + strings.TrimSpace(rawURL[idx+len(marker):])
}

func glabArtifactState(mr glabMRView) string {
	switch strings.TrimSpace(mr.State) {
	case "opened":
		if mr.Draft || mr.WorkInProgress {
			return "draft"
		}
		return "ready"
	case "merged":
		return "merged"
	default:
		return strings.TrimSpace(mr.State)
	}
}

func (a *GlabAdapter) entriesForCompletion(ctx context.Context, p completedPayload) []branchEntry {
	a.mu.RLock()
	tracked := append([]branchEntry(nil), a.tracked[p.Branch]...)
	a.mu.RUnlock()
	if a.eventRepo == nil || strings.TrimSpace(p.WorkspaceID) == "" || strings.TrimSpace(p.WorkItemID) == "" {
		return tracked
	}
	events, err := a.eventRepo.ListByWorkspaceID(ctx, p.WorkspaceID, 0)
	if err != nil {
		return tracked
	}
	seen := make(map[string]branchEntry)
	for _, event := range events {
		if domain.EventType(event.EventType) != domain.EventReviewArtifactRecorded {
			continue
		}
		var payload domain.ReviewArtifactEventPayload
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			continue
		}
		artifact := payload.Artifact
		if payload.WorkItemID != p.WorkItemID || artifact.Provider != "gitlab" || strings.TrimSpace(artifact.Branch) != strings.TrimSpace(p.Branch) || strings.TrimSpace(artifact.WorktreePath) == "" {
			continue
		}
		seen[artifact.RepoName+"|"+artifact.WorktreePath] = branchEntry{repo: artifact.RepoName, worktreePath: artifact.WorktreePath, ref: artifact.Ref, url: artifact.URL}
	}
	if len(seen) == 0 {
		return tracked
	}
	entries := make([]branchEntry, 0, len(seen))
	for _, entry := range seen {
		entries = append(entries, entry)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].repo != entries[j].repo {
			return entries[i].repo < entries[j].repo
		}
		return entries[i].worktreePath < entries[j].worktreePath
	})
	return entries
}

func (a *GlabAdapter) recordReviewArtifact(ctx context.Context, workspaceID, workItemID string, artifact domain.ReviewArtifact) {
	if err := coreadapter.PersistReviewArtifact(ctx, a.eventRepo, workspaceID, workItemID, artifact); err != nil {
		slog.Warn("glab: persist review artifact failed", "repo", artifact.RepoName, "branch", artifact.Branch, "error", err)
	}
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

func appendTrackerFooter(body, footer string) string {
	body = strings.TrimSpace(body)
	footer = strings.TrimSpace(footer)
	switch {
	case body == "":
		return footer
	case footer == "":
		return body
	default:
		return body + "\n\n" + footer
	}
}

func renderGitLabTrackerRefs(refs []domain.TrackerReference) string {
	parts := make([]string, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		rendered := renderGitLabTrackerRef(ref)
		if rendered == "" {
			continue
		}
		if _, ok := seen[rendered]; ok {
			continue
		}
		seen[rendered] = struct{}{}
		parts = append(parts, rendered)
	}
	if len(parts) == 0 {
		return ""
	}
	return "Resolves " + strings.Join(parts, ", ")
}

func renderGitLabTrackerRef(ref domain.TrackerReference) string {
	switch ref.Provider {
	case "gitlab":
		if ref.Kind != "issue" || ref.Number <= 0 {
			return ""
		}
		if ref.Repo == "" {
			return fmt.Sprintf("#%d", ref.Number)
		}
		if ref.URL != "" {
			return fmt.Sprintf("[%s#%d](%s)", ref.Repo, ref.Number, ref.URL)
		}
		return fmt.Sprintf("%s#%d", ref.Repo, ref.Number)
	case "linear":
		if ref.ID == "" {
			return ""
		}
		if ref.URL != "" {
			return fmt.Sprintf("[%s](%s)", ref.ID, ref.URL)
		}
		return ref.ID
	default:
		return ""
	}
}
