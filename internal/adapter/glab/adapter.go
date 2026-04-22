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
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	coreadapter "github.com/beeemT/substrate/internal/adapter"
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

// branchEntry records durable repo/MR context for one branch.
type branchEntry struct {
	repo         string
	worktreePath string
	ref          string
	url          string
}

// GlabAdapter implements adapter.RepoLifecycleAdapter using the glab CLI.
type GlabAdapter struct {
	cfg          config.GlabConfig
	runner       commandRunner
	repos        coreadapter.ReviewArtifactRepos
	workspaceDir string

	workspaceID string

	mu      sync.RWMutex
	tracked map[string][]branchEntry // branch → []branchEntry
}

// New creates a GlabAdapter with the given configuration.
func New(cfg config.GlabConfig) *GlabAdapter {
	return newWithRunner(cfg, coreadapter.ReviewArtifactRepos{}, "", execRunner)
}

// NewWithEventRepo creates a GlabAdapter that persists durable MR metadata.
func NewWithEventRepo(cfg config.GlabConfig, repos coreadapter.ReviewArtifactRepos, workspaceDir string) *GlabAdapter {
	return newWithRunner(cfg, repos, workspaceDir, execRunner)
}

// newWithRunner creates a GlabAdapter with an injectable commandRunner (for tests).
func newWithRunner(cfg config.GlabConfig, repos coreadapter.ReviewArtifactRepos, workspaceDir string, runner commandRunner) *GlabAdapter {
	return &GlabAdapter{
		cfg:          cfg,
		runner:       runner,
		repos:        repos,
		workspaceDir: workspaceDir,
		tracked:      make(map[string][]branchEntry),
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
	case domain.EventWorktreeReused:
		if err := a.onWorktreeReused(ctx, event.Payload); err != nil {
			slog.Warn("glab: worktree reused handler failed", "error", err)
		}
	case domain.EventWorkItemCompleted:
		if err := a.onWorkItemCompleted(ctx, event.Payload); err != nil {
			slog.Warn("glab: work item completed handler failed", "error", err)
		}
	case domain.EventPRMerged:
		if err := a.onPRMerged(ctx, event.Payload); err != nil {
			slog.Warn("glab: post-merge issue close failed", "error", err)
		}
	case domain.EventPlanApproved:
		a.syncMRDescriptionsOnApproval(ctx, event.WorkspaceID, event.Payload)
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
	WorkspaceID   string           `json:"workspace_id"`
	WorkItemID    string           `json:"work_item_id"`
	Branch        string           `json:"branch"`
	ExternalID    string           `json:"external_id"`
	WorkItemTitle string           `json:"work_item_title"`
	SubPlan       string           `json:"sub_plan"`
	Review        domain.ReviewRef `json:"review"`
}

func (a *GlabAdapter) onWorktreeCreated(ctx context.Context, payload string) error {
	var p worktreePayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return fmt.Errorf("unmarshal worktree payload: %w", err)
	}
	if p.Branch == "" || p.WorktreePath == "" {
		return errors.New("worktree payload missing branch or worktree_path")
	}

	title := mrTitle(p.WorkItemTitle, p.Branch)
	description := appendTrackerFooter(strings.TrimSpace(p.SubPlan), renderGitLabTrackerRefs(p.TrackerRefs))
	var artifact *domain.ReviewArtifact
	var iid int
	if mr, ok := a.mrView(ctx, p.WorktreePath, p.Branch); ok {
		slog.Info("glab: MR already exists, skipping create", "branch", p.Branch)
		iid = mr.IID
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
		// Try to get the IID of the newly created MR.
		if created, ok := a.mrView(ctx, p.WorktreePath, p.Branch); ok {
			iid = created.IID
		}
		artifact = &domain.ReviewArtifact{
			Provider:     "gitlab",
			Kind:         "MR",
			RepoName:     p.Repository,
			Ref:          glabArtifactRef(url, iid),
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
		a.recordGitlabMR(ctx, p.WorkspaceID, p.WorkItemID, *artifact, p.Repository, iid)
	}

	return nil
}

func (a *GlabAdapter) onWorktreeReused(ctx context.Context, payload string) error {
	var p worktreePayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return fmt.Errorf("unmarshal worktree reused payload: %w", err)
	}
	if p.Branch == "" || p.WorktreePath == "" {
		return errors.New("worktree reused payload missing branch or worktree_path")
	}

	// Update MR description with new sub-plan content
	description := appendTrackerFooter(strings.TrimSpace(p.SubPlan), renderGitLabTrackerRefs(p.TrackerRefs))
	if err := a.updateMRDescription(ctx, p.WorktreePath, p.Branch, description); err != nil {
		slog.Warn("glab: failed to update MR description on reuse", "repo", p.Repository, "branch", p.Branch, "error", err)
	}

	return nil
}

func (a *GlabAdapter) onWorkItemCompleted(ctx context.Context, payload string) error {
	var p completedPayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return fmt.Errorf("unmarshal completed payload: %w", err)
	}
	// Only act on GitLab-hosted repos. If the review context names a different
	// provider explicitly, this event belongs to another adapter.
	if provider := strings.ToLower(strings.TrimSpace(p.Review.BaseRepo.Provider)); provider != "" && provider != "gitlab" {
		return nil
	}
	if p.Branch == "" {
		slog.Warn("glab: work_item.completed payload has no branch; skipping mr update")

		return nil
	}

	entries := a.entriesForCompletion(ctx, p)
	if len(entries) == 0 {
		entries = a.worktreePathsForCompletion(ctx, p)
	}

	for _, entry := range entries {
		hasPreviousTracking := entry.ref != "" || entry.url != ""

		if hasPreviousTracking {
			// Previously tracked MR — mark ready (existing flow).
			if err := a.markMRReady(ctx, entry.worktreePath, p.Branch); err != nil {
				slog.Warn("glab: mr update --draft=false failed", "repo", entry.repo, "branch", p.Branch, "error", err)
				continue
			}
		} else if a.mrExists(ctx, entry.worktreePath, p.Branch) {
			// MR exists on remote but wasn't tracked (adapter restart). Mark ready.
			if err := a.markMRReady(ctx, entry.worktreePath, p.Branch); err != nil {
				slog.Warn("glab: mr update --draft=false failed", "repo", entry.repo, "branch", p.Branch, "error", err)
				continue
			}
		} else {
			// No MR at all — create one, then mark it ready (review already passed).
			title := mrTitle(p.WorkItemTitle, p.Branch)
			description := strings.TrimSpace(p.SubPlan)
			if _, err := a.createMR(ctx, entry.worktreePath, p.Branch, title, description); err != nil {
				slog.Warn("glab: completion-time MR creation failed", "repo", entry.repo, "branch", p.Branch, "error", err)
				continue
			}
			if err := a.markMRReady(ctx, entry.worktreePath, p.Branch); err != nil {
				slog.Warn("glab: mr update --draft=false failed after completion-time create", "repo", entry.repo, "branch", p.Branch, "error", err)
			}
		}

		// Record the artifact.
		if mr, ok := a.mrView(ctx, entry.worktreePath, p.Branch); ok {
			a.recordGitlabMR(ctx, p.WorkspaceID, p.WorkItemID, domain.ReviewArtifact{
				Provider:     "gitlab",
				Kind:         "MR",
				RepoName:     entry.repo,
				Ref:          glabArtifactRef(strings.TrimSpace(mr.WebURL), mr.IID),
				URL:          strings.TrimSpace(mr.WebURL),
				State:        glabArtifactState(mr),
				Branch:       p.Branch,
				WorktreePath: entry.worktreePath,
				UpdatedAt:    time.Now(),
			}, entry.repo, mr.IID)

			continue
		}
		a.recordGitlabMR(ctx, p.WorkspaceID, p.WorkItemID, domain.ReviewArtifact{
			Provider:     "gitlab",
			Kind:         "MR",
			RepoName:     entry.repo,
			Ref:          entry.ref,
			URL:          entry.url,
			State:        "ready",
			Branch:       p.Branch,
			WorktreePath: entry.worktreePath,
			UpdatedAt:    time.Now(),
		}, entry.repo, 0)
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

type glabApprovalState struct {
	Rules []glabApprovalRule `json:"rules"`
}

type glabApprovalRule struct {
	ApprovedBy        []glabApprovalUser `json:"approved_by"`
	EligibleApprovers []glabApprovalUser `json:"eligible_approvers"`
}

type glabApprovalUser struct {
	Username string `json:"username"`
}

type glabDiscussion struct {
	Notes []glabNote `json:"notes"`
}

type glabNote struct {
	Resolvable bool `json:"resolvable"`
	Resolved   bool `json:"resolved"`
}

type glabPipeline struct {
	ID     int    `json:"id"`
	Status string `json:"status"`
	Ref    string `json:"ref"`
}

type glabJob struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
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

// markMRReady runs `glab mr update <branch> --ready --yes`.
func (a *GlabAdapter) markMRReady(ctx context.Context, dir, branch string) error {
	out, err := a.runner(ctx, dir, "glab",
		"mr", "update", branch,
		"--ready",
		"--yes",
	)
	if err != nil {
		return fmt.Errorf("glab mr update: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}

	return nil
}

// updateMRDescription updates the description of an existing MR.
func (a *GlabAdapter) updateMRDescription(ctx context.Context, dir, branch, description string) error {
	out, err := a.runner(ctx, dir, "glab",
		"mr", "update", branch,
		"--description", description,
		"--yes",
	)
	if err != nil {
		return fmt.Errorf("glab mr update description: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}

	return nil
}

// parseMRURL scans command output for the first line containing a GitLab MR URL.
func parseMRURL(output []byte) string {
	for line := range bytes.SplitSeq(output, []byte("\n")) {
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
	if a.repos.Events == nil || strings.TrimSpace(p.WorkspaceID) == "" || strings.TrimSpace(p.WorkItemID) == "" {
		return tracked
	}
	events, err := a.repos.Events.ListByWorkspaceID(ctx, p.WorkspaceID, 0)
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
		if payload.WorkItemID != p.WorkItemID || artifact.Provider != "gitlab" ||
			strings.TrimSpace(artifact.Branch) != strings.TrimSpace(p.Branch) ||
			strings.TrimSpace(artifact.WorktreePath) == "" {
			continue
		}
		seen[artifact.RepoName+"|"+artifact.WorktreePath] = branchEntry{
			repo: artifact.RepoName, worktreePath: artifact.WorktreePath,
			ref: artifact.Ref, url: artifact.URL,
		}
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

// worktreePathsForCompletion is a fallback that scans persisted EventWorktreeCreated
// events to find worktree paths when no tracked entries or recorded artifacts exist.
// This handles the case where the adapter restarted before MR creation, or MR
// creation failed during onWorktreeCreated.
func (a *GlabAdapter) worktreePathsForCompletion(ctx context.Context, p completedPayload) []branchEntry {
	if a.repos.Events == nil || strings.TrimSpace(p.WorkspaceID) == "" || strings.TrimSpace(p.WorkItemID) == "" {
		return nil
	}
	events, err := a.repos.Events.ListByWorkspaceID(ctx, p.WorkspaceID, 0)
	if err != nil {
		return nil
	}
	var entries []branchEntry
	for _, event := range events {
		if domain.EventType(event.EventType) != domain.EventWorktreeCreated {
			continue
		}
		var wt worktreePayload
		if err := json.Unmarshal([]byte(event.Payload), &wt); err != nil {
			continue
		}
		if wt.WorkItemID != p.WorkItemID || wt.Branch != p.Branch || wt.WorktreePath == "" {
			continue
		}
		entries = append(entries, branchEntry{
			repo:         wt.Repository,
			worktreePath: wt.WorktreePath,
		})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].repo != entries[j].repo {
			return entries[i].repo < entries[j].repo
		}
		return entries[i].worktreePath < entries[j].worktreePath
	})

	return entries
}

func (a *GlabAdapter) recordGitlabMR(ctx context.Context, workspaceID, workItemID string, artifact domain.ReviewArtifact, projectPath string, iid int) {
	if err := coreadapter.PersistGitlabMR(ctx, a.repos, workspaceID, workItemID, artifact, projectPath, iid); err != nil {
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

// StartMRRefresh begins a background goroutine that periodically refreshes
// non-terminal MR state from GitLab. If no MR repository is configured the
// call is a no-op (safe for tests).
func (a *GlabAdapter) StartMRRefresh(ctx context.Context, workspaceID string) {
	if a.repos.GitlabMRs == nil {
		return
	}
	a.workspaceID = workspaceID
	go a.refreshMRLoop(ctx)
}

func (a *GlabAdapter) refreshMRLoop(ctx context.Context) {
	a.refreshMRs(ctx)
	ticker := time.NewTicker(120 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.refreshMRs(ctx)
		}
	}
}

func (a *GlabAdapter) refreshMRs(ctx context.Context) {
	mrs, err := a.repos.GitlabMRs.ListNonTerminal(ctx, a.workspaceID)
	if err != nil {
		slog.Warn("glab: refresh mrs list failed", "error", err)
		return
	}
	for _, mr := range mrs {
		a.refreshSingleMR(ctx, mr)
	}
}

func (a *GlabAdapter) refreshSingleMR(ctx context.Context, mr domain.GitlabMergeRequest) {
	if a.workspaceDir == "" {
		return
	}
	repoDir := filepath.Join(a.workspaceDir, mr.ProjectPath)
	if _, err := os.Stat(repoDir); err != nil {
		slog.Warn("glab: refresh mr repo dir not found", "dir", repoDir, "iid", mr.IID)
		return
	}
	fresh, ok := a.mrView(ctx, repoDir, mr.SourceBranch)
	if !ok {
		return
	}
	updated := domain.GitlabMergeRequest{
		ID:           mr.ID,
		ProjectPath:  mr.ProjectPath,
		IID:          fresh.IID,
		State:        glabArtifactState(fresh),
		Draft:        fresh.Draft || fresh.WorkInProgress,
		SourceBranch: mr.SourceBranch,
		WebURL:       strings.TrimSpace(fresh.WebURL),
		CreatedAt:    mr.CreatedAt,
		UpdatedAt:    time.Now(),
	}
	if err := a.repos.GitlabMRs.Upsert(ctx, updated); err != nil {
		slog.Warn("glab: refresh mr upsert failed", "iid", mr.IID, "error", err)
	}

	// Fetch and upsert MR reviews.
	state := glabArtifactState(fresh)
	if state == "merged" || state == "closed" {
		if a.repos.GitlabMRReviews != nil {
			if err := a.repos.GitlabMRReviews.DeleteByMRID(ctx, mr.ID); err != nil {
				slog.Warn("glab: delete mr reviews on terminal state failed", "iid", mr.IID, "error", err)
			}
		}
		if a.repos.GitlabMRChecks != nil {
			if err := a.repos.GitlabMRChecks.DeleteByMRID(ctx, mr.ID); err != nil {
				slog.Warn("glab: delete mr checks on terminal state failed", "iid", mr.IID, "error", err)
			}
		}
	} else {
		a.refreshMRReviews(ctx, mr, repoDir)
		a.refreshMRChecks(ctx, mr, repoDir)
	}

	// Detect merge transition: MR just became merged.
	if glabArtifactState(fresh) == "merged" && mr.State != "merged" {
		a.checkAllMerged(ctx, mr.ID)
	}
}

// refreshMRReviews fetches approval state and unresolved discussions for a GitLab MR
// and upserts review rows.
func (a *GlabAdapter) refreshMRReviews(ctx context.Context, mr domain.GitlabMergeRequest, repoDir string) {
	if a.repos.GitlabMRReviews == nil {
		return
	}

	now := time.Now()
	var reviews []domain.GitlabMRReview

	// Fetch approval state via glab api.
	approvalOut, err := a.runner(ctx, repoDir, "glab", "api",
		fmt.Sprintf("/projects/%s/merge_requests/%d/approval_state",
			url.PathEscape(mr.ProjectPath), mr.IID))
	if err != nil {
		slog.Warn("glab: fetch mr approval state failed", "iid", mr.IID, "error", err)
	} else {
		var approvalState glabApprovalState
		if err := json.Unmarshal(approvalOut, &approvalState); err != nil {
			slog.Warn("glab: parse mr approval state failed", "iid", mr.IID, "error", err)
		} else {
			approved := make(map[string]bool)
			for _, rule := range approvalState.Rules {
				for _, u := range rule.ApprovedBy {
					if u.Username != "" {
						approved[u.Username] = true
					}
				}
			}
			for login := range approved {
				reviews = append(reviews, domain.GitlabMRReview{
					ID:            domain.NewID(),
					MRID:          mr.ID,
					ReviewerLogin: login,
					State:         "approved",
					SubmittedAt:   now,
					CreatedAt:     now,
					UpdatedAt:     now,
				})
			}
		}
	}

	// Fetch discussions to detect unresolved threads.
	discOut, err := a.runner(ctx, repoDir, "glab", "api",
		fmt.Sprintf("/projects/%s/merge_requests/%d/discussions",
			url.PathEscape(mr.ProjectPath), mr.IID))
	if err != nil {
		slog.Warn("glab: fetch mr discussions failed", "iid", mr.IID, "error", err)
	} else {
		var discussions []glabDiscussion
		if err := json.Unmarshal(discOut, &discussions); err != nil {
			slog.Warn("glab: parse mr discussions failed", "iid", mr.IID, "error", err)
		} else {
			unresolved := 0
			for _, d := range discussions {
				for _, n := range d.Notes {
					if n.Resolvable && !n.Resolved {
						unresolved++
						break
					}
				}
			}
			if unresolved > 0 {
				reviews = append(reviews, domain.GitlabMRReview{
					ID:            domain.NewID(),
					MRID:          mr.ID,
					ReviewerLogin: "__unresolved_threads__",
					State:         "changes_requested",
					SubmittedAt:   now,
					CreatedAt:     now,
					UpdatedAt:     now,
				})
			}
		}
	}

	// Delete existing and re-insert fresh state.
	if err := a.repos.GitlabMRReviews.DeleteByMRID(ctx, mr.ID); err != nil {
		slog.Warn("glab: delete mr reviews failed", "iid", mr.IID, "error", err)
	}
	for _, review := range reviews {
		if err := a.repos.GitlabMRReviews.Upsert(ctx, review); err != nil {
			slog.Warn("glab: upsert mr review failed", "iid", mr.IID, "reviewer", review.ReviewerLogin, "error", err)
		}
	}
}

// refreshMRChecks fetches the latest pipeline jobs for a GitLab MR
// and upserts them as check rows.
func (a *GlabAdapter) refreshMRChecks(ctx context.Context, mr domain.GitlabMergeRequest, repoDir string) {
	if a.repos.GitlabMRChecks == nil {
		return
	}

	// Fetch the latest pipeline for the MR source branch.
	pipelinesOut, err := a.runner(ctx, repoDir, "glab", "api",
		fmt.Sprintf("/projects/%s/pipelines?ref=%s&per_page=1&order_by=updated_at",
			url.PathEscape(mr.ProjectPath), url.QueryEscape(mr.SourceBranch)))
	if err != nil {
		slog.Warn("glab: fetch mr pipelines failed", "iid", mr.IID, "error", err)
		return
	}

	var pipelines []glabPipeline
	if err := json.Unmarshal(pipelinesOut, &pipelines); err != nil {
		slog.Warn("glab: parse mr pipelines failed", "iid", mr.IID, "error", err)
		return
	}
	if len(pipelines) == 0 {
		return
	}

	// Fetch jobs for the latest pipeline.
	jobsOut, err := a.runner(ctx, repoDir, "glab", "api",
		fmt.Sprintf("/projects/%s/pipelines/%d/jobs",
			url.PathEscape(mr.ProjectPath), pipelines[0].ID))
	if err != nil {
		slog.Warn("glab: fetch pipeline jobs failed", "iid", mr.IID, "pipeline", pipelines[0].ID, "error", err)
		return
	}

	var jobs []glabJob
	if err := json.Unmarshal(jobsOut, &jobs); err != nil {
		slog.Warn("glab: parse pipeline jobs failed", "iid", mr.IID, "error", err)
		return
	}

	now := time.Now()
	// Delete existing and re-insert fresh state.
	if err := a.repos.GitlabMRChecks.DeleteByMRID(ctx, mr.ID); err != nil {
		slog.Warn("glab: delete mr checks failed", "iid", mr.IID, "error", err)
	}
	for _, job := range jobs {
		if job.Name == "" {
			continue
		}
		status, conclusion := glabJobStatusMap(job.Status)
		check := domain.GitlabMRCheck{
			ID:         domain.NewID(),
			MRID:       mr.ID,
			Name:       job.Name,
			Status:     status,
			Conclusion: conclusion,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		if err := a.repos.GitlabMRChecks.Upsert(ctx, check); err != nil {
			slog.Warn("glab: upsert mr check failed", "iid", mr.IID, "job", job.Name, "error", err)
		}
	}
}

// glabJobStatusMap maps a GitLab CI job status to (status, conclusion) pair.
func glabJobStatusMap(jobStatus string) (string, string) {
	switch jobStatus {
	case "success":
		return "completed", "success"
	case "failed":
		return "completed", "failure"
	case "canceled":
		return "completed", "cancelled"
	case "skipped":
		return "completed", "skipped"
	case "running", "pending":
		return "in_progress", ""
	case "manual":
		return "queued", ""
	default:
		return "queued", ""
	}
}

// checkAllMerged transitions a work item to SessionMerged when all linked
// review artifacts are merged, then publishes EventPRMerged.
func (a *GlabAdapter) checkAllMerged(ctx context.Context, mrID string) {
	if a.repos.SessionArtifacts == nil || a.repos.Sessions == nil || a.repos.Bus == nil {
		return
	}
	links, err := a.repos.SessionArtifacts.ListByWorkspaceID(ctx, a.workspaceID)
	if err != nil {
		slog.Warn("glab: list artifacts for merge check failed", "error", err)
		return
	}
	var workItemID string
	for _, link := range links {
		if link.ProviderArtifactID == mrID {
			workItemID = link.WorkItemID
			break
		}
	}
	if workItemID == "" {
		return
	}
	wi, err := a.repos.Sessions.Get(ctx, workItemID)
	if err != nil {
		slog.Warn("glab: get work item for merge check failed", "work_item_id", workItemID, "error", err)
		return
	}
	if wi.State != domain.SessionCompleted {
		return
	}
	for _, link := range links {
		if link.WorkItemID != workItemID {
			continue
		}
		switch link.Provider {
		case "github":
			if a.repos.GithubPRs == nil {
				return
			}
			ghPR, err := a.repos.GithubPRs.Get(ctx, link.ProviderArtifactID)
			if err != nil {
				slog.Warn("glab: get github pr for merge check failed", "pr_id", link.ProviderArtifactID, "error", err)
				return
			}
			if ghPR.State != "merged" {
				return
			}
		case "gitlab":
			glMR, err := a.repos.GitlabMRs.Get(ctx, link.ProviderArtifactID)
			if err != nil {
				slog.Warn("glab: get mr for merge check failed", "mr_id", link.ProviderArtifactID, "error", err)
				return
			}
			if glMR.State != "merged" {
				return
			}
		default:
			return
		}
	}
	if err := a.repos.Sessions.MergeWorkItem(ctx, workItemID); err != nil {
		slog.Warn("glab: merge work item failed", "work_item_id", workItemID, "error", err)
		return
	}
	payload, err := json.Marshal(map[string]string{
		"workspace_id": a.workspaceID,
		"work_item_id": workItemID,
		"external_id":  wi.ExternalID,
	})
	if err != nil {
		slog.Warn("glab: marshal pr.merged payload failed", "error", err)
		return
	}
	if err := a.repos.Bus.Publish(ctx, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventPRMerged),
		WorkspaceID: a.workspaceID,
		Payload:     string(payload),
		CreatedAt:   time.Now(),
	}); err != nil {
		slog.Warn("glab: publish pr.merged event failed", "error", err)
	}
}

// syncMRDescriptionsOnApproval updates the description of all open GitLab MRs
// linked to the work item when a plan is approved.
func (a *GlabAdapter) syncMRDescriptionsOnApproval(ctx context.Context, workspaceID, payload string) {
	if a.repos.SessionArtifacts == nil || a.repos.GitlabMRs == nil || a.workspaceDir == "" {
		return
	}
	var p struct {
		WorkItemID  string `json:"work_item_id"`
		CommentBody string `json:"comment_body"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		slog.Warn("glab: unmarshal plan.approved payload for description sync", "error", err)
		return
	}
	if p.WorkItemID == "" || strings.TrimSpace(p.CommentBody) == "" {
		return
	}
	links, err := a.repos.SessionArtifacts.ListByWorkspaceID(ctx, workspaceID)
	if err != nil {
		slog.Warn("glab: list artifacts for description sync", "error", err)
		return
	}
	for _, link := range links {
		if link.WorkItemID != p.WorkItemID || link.Provider != "gitlab" {
			continue
		}
		mr, err := a.repos.GitlabMRs.Get(ctx, link.ProviderArtifactID)
		if err != nil {
			slog.Warn("glab: get mr for description sync", "mr_id", link.ProviderArtifactID, "error", err)
			continue
		}
		if mr.State == "merged" || mr.State == "closed" {
			continue
		}
		if _, err := a.runner(ctx, a.workspaceDir, "glab", "api", "-X", "PUT",
			fmt.Sprintf("/projects/%s/merge_requests/%d", url.PathEscape(mr.ProjectPath), mr.IID),
			"--field", "description="+p.CommentBody); err != nil {
			slog.Warn("glab: update MR description on plan approval", "project", mr.ProjectPath, "iid", mr.IID, "error", err)
		}
	}
}

// onPRMerged closes the linked GitLab issue when PostMergeCloseIssue is enabled.
func (a *GlabAdapter) onPRMerged(ctx context.Context, payload string) error {
	if !a.cfg.PostMergeCloseIssue {
		return nil
	}
	var parsed struct {
		ExternalID string `json:"external_id"`
	}
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		return fmt.Errorf("parse pr.merged payload: %w", err)
	}
	if parsed.ExternalID == "" || !strings.HasPrefix(parsed.ExternalID, "gl:issue:") {
		return nil
	}
	raw := strings.TrimPrefix(parsed.ExternalID, "gl:issue:")
	parts := strings.SplitN(raw, "#", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid gitlab external id %q", parsed.ExternalID)
	}
	projectID := parts[0]
	issueIID := parts[1]
	if projectID == "" || issueIID == "" {
		return fmt.Errorf("invalid gitlab external id %q", parsed.ExternalID)
	}
	if a.workspaceDir == "" {
		return fmt.Errorf("glab: no workspace dir for issue close")
	}
	_, err := a.runner(ctx, a.workspaceDir, "glab", "api", "-X", "PUT",
		fmt.Sprintf("/projects/%s/issues/%s", url.PathEscape(projectID), url.PathEscape(issueIID)),
		"--field", "state_event=close")
	return err
}

type glabDiscussionFull struct {
	ID    string         `json:"id"`
	Notes []glabNoteFull `json:"notes"`
}

type glabNoteFull struct {
	ID         int64              `json:"id"`
	Body       string             `json:"body"`
	Author     glabNoteFullAuthor `json:"author"`
	CreatedAt  string             `json:"created_at"` // ISO8601
	System     bool               `json:"system"`
	Resolvable bool               `json:"resolvable"`
	Resolved   bool               `json:"resolved"`
	Position   *glabNotePosition  `json:"position,omitempty"`
}

type glabNoteFullAuthor struct {
	Username string `json:"username"`
}

// glabNotePosition includes both new_* and old_* anchors. Reviewer notes on
// removed lines populate only old_path/old_line; new_line is null in that case.
type glabNotePosition struct {
	NewPath string `json:"new_path"`
	NewLine int    `json:"new_line"`
	OldPath string `json:"old_path"`
	OldLine int    `json:"old_line"`
}

// glabMRRef is the minimal MR payload used to recover web_url for URL building.
type glabMRRef struct {
	WebURL string `json:"web_url"`
}

// glabDiscussionsPageSize controls per-page size for the discussions endpoint.
// 100 is the GitLab API maximum.
const glabDiscussionsPageSize = 100

// Provider returns the provider identifier for ReviewCommentFetcher.
func (a *GlabAdapter) Provider() string { return "gitlab" }

// FetchReviewComments returns unresolved review comments for the given MR.
// projectPath is the GitLab project path (e.g. "group/sub/repo"); it is URL-escaped here.
// iid is the merge request IID. The endpoint is paginated explicitly so MRs
// with more than glabDiscussionsPageSize discussions are fully covered.
func (a *GlabAdapter) FetchReviewComments(ctx context.Context, projectPath string, iid int) ([]coreadapter.ReviewComment, error) {
	webURL, err := a.fetchMRWebURL(ctx, projectPath, iid)
	if err != nil {
		// Non-fatal: URL field is best-effort. Continue with empty webURL.
		slog.Warn("glab: fetch MR web_url failed", "project", projectPath, "iid", iid, "error", err)
	}

	var comments []coreadapter.ReviewComment
	for page := 1; ; page++ {
		endpoint := fmt.Sprintf("/projects/%s/merge_requests/%d/discussions?per_page=%d&page=%d",
			url.PathEscape(projectPath), iid, glabDiscussionsPageSize, page)
		output, runErr := a.runner(ctx, a.workspaceDir, "glab", "api", endpoint)

		if runErr != nil {
			return nil, fmt.Errorf("glab discussions for !%d page %d: %w (output: %s)", iid, page, runErr, strings.TrimSpace(string(output)))
		}
		var discussions []glabDiscussionFull
		if jsonErr := json.Unmarshal(output, &discussions); jsonErr != nil {
			return nil, fmt.Errorf("parse glab discussions page %d: %w", page, jsonErr)
		}
		if len(discussions) == 0 {
			break
		}
		comments = appendGlabDiscussionComments(comments, discussions, webURL)
		slog.Info("DEBUG glab discussions page", "page", page, "got", len(discussions), "comments_so_far", len(comments))
		if len(discussions) < glabDiscussionsPageSize {
			break
		}
	}
	return comments, nil
}

// fetchMRWebURL queries the merge_request endpoint to recover web_url, used for
// constructing per-note links. Empty result is treated as "unknown" by callers.
func (a *GlabAdapter) fetchMRWebURL(ctx context.Context, projectPath string, iid int) (string, error) {
	endpoint := fmt.Sprintf("/projects/%s/merge_requests/%d", url.PathEscape(projectPath), iid)
	output, err := a.runner(ctx, a.workspaceDir, "glab", "api", endpoint)
	if err != nil {
		return "", fmt.Errorf("glab api %s: %w (output: %s)", endpoint, err, strings.TrimSpace(string(output)))
	}
	var ref glabMRRef
	if jsonErr := json.Unmarshal(output, &ref); jsonErr != nil {
		return "", fmt.Errorf("parse glab mr ref: %w", jsonErr)
	}
	return strings.TrimSpace(ref.WebURL), nil
}

// appendGlabDiscussionComments converts unresolved discussions on a page into
// shared adapter.ReviewComment values and appends them to dst.
func appendGlabDiscussionComments(dst []coreadapter.ReviewComment, discussions []glabDiscussionFull, webURL string) []coreadapter.ReviewComment {
	for _, d := range discussions {
		// Resolution rule: a discussion is fully resolved iff it has at least one
		// resolvable note and every resolvable note has Resolved==true. Skip those.
		// Discussions with no resolvable notes (e.g. a lone system reply) are kept.
		var resolvable, anyUnresolved bool
		for i := range d.Notes {
			if !d.Notes[i].Resolvable {
				continue
			}
			resolvable = true
			if !d.Notes[i].Resolved {
				anyUnresolved = true
			}
		}
		if resolvable && !anyUnresolved {
			continue
		}
		// Take the first non-system note as the discussion's anchor comment.
		var note *glabNoteFull
		for i := range d.Notes {
			if d.Notes[i].System {
				continue
			}
			note = &d.Notes[i]
			break
		}
		if note == nil {
			continue
		}
		dst = append(dst, glabNoteToReviewComment(*note, webURL))
	}
	return dst
}

// glabNoteToReviewComment normalizes a single non-system, non-resolved note.
// Position fields prefer new_path/new_line; when those are absent (note anchors
// to a removed line) it falls back to old_path/old_line.
func glabNoteToReviewComment(note glabNoteFull, webURL string) coreadapter.ReviewComment {
	createdAt, parseErr := time.Parse(time.RFC3339Nano, note.CreatedAt)
	if parseErr != nil {
		createdAt, parseErr = time.Parse(time.RFC3339, note.CreatedAt)
	}
	if parseErr != nil {
		slog.Warn("glab: failed to parse review comment timestamp", "created_at", note.CreatedAt, "error", parseErr)
		createdAt = time.Time{}
	}

	path, line := "", 0
	if note.Position != nil {
		switch {
		case note.Position.NewPath != "" && note.Position.NewLine != 0:
			path = note.Position.NewPath
			line = note.Position.NewLine
		case note.Position.OldPath != "":
			path = note.Position.OldPath
			line = note.Position.OldLine
		case note.Position.NewPath != "":
			// new_line is zero/null but a new_path is present (rare).
			path = note.Position.NewPath
		}
	}

	noteURL := ""
	if webURL != "" {
		noteURL = webURL + "#note_" + strconv.FormatInt(note.ID, 10)
	}

	return coreadapter.ReviewComment{
		ID:            strconv.FormatInt(note.ID, 10),
		ReviewerLogin: note.Author.Username,
		Body:          note.Body,
		Path:          path,
		Line:          line,
		URL:           noteURL,
		CreatedAt:     createdAt,
	}
}
