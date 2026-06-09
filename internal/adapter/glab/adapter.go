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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	coreadapter "github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
)

// Verify GlabAdapter implements adapter.RepoLifecycleAdapter at compile time.
var _ coreadapter.RepoLifecycleAdapter = &GlabAdapter{}

// Verify GlabAdapter implements adapter.ReviewCommentFetcher at compile time.
var _ coreadapter.ReviewCommentFetcher = &GlabAdapter{}

// Verify GlabAdapter implements mrRefresher interface.
type mrRefresher interface {
	StartMRRefresh(ctx context.Context, workspaceID string) func()
}

var _ mrRefresher = &GlabAdapter{}

const (
	gitlabMRTitleMaxRunes       = 245
	gitlabMRTitleEllipsisSuffix = "…"
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
	case domain.EventSubPlanPRReady:
		if err := a.onSubPlanPRReady(ctx, event.Payload); err != nil {
			slog.Warn("glab: sub-plan PR-ready handler failed", "error", err)
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
	Review        domain.ReviewRef          `json:"review"`
}

// completedPayload is the legacy shape for the old EventWorkItemCompleted MR lifecycle.
// MR finalization now uses EventSubPlanPRReady because branches are per sub-plan,
// not work-item-level data. This remains only for the old completion helper code.
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

	projectPath := gitlabProjectPathFromReview(p.Review, p.Repository)
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
			RepoName:     projectPath,
			Ref:          glabArtifactRef(strings.TrimSpace(mr.WebURL), mr.IID),
			URL:          strings.TrimSpace(mr.WebURL),
			State:        glabArtifactState(mr),
			Branch:       p.Branch,
			WorktreePath: p.WorktreePath,
			UpdatedAt:    time.Now(),
		}
	} else if url, err := a.createMR(ctx, p.WorktreePath, p.Branch, title, description); err != nil {
		slog.Warn("glab: mr create failed", "repo", projectPath, "branch", p.Branch, "error", err)
	} else {
		// Try to get the IID of the newly created MR.
		if created, ok := a.mrView(ctx, p.WorktreePath, p.Branch); ok {
			iid = created.IID
		}
		artifact = &domain.ReviewArtifact{
			Provider:     "gitlab",
			Kind:         "MR",
			RepoName:     projectPath,
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
		repo:         projectPath,
		worktreePath: p.WorktreePath,
		ref:          trackedRef,
		url:          trackedURL,
	})
	a.mu.Unlock()
	if artifact != nil {
		a.recordGitlabMR(ctx, p.WorkspaceID, p.WorkItemID, *artifact, projectPath, iid)
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

	projectPath := gitlabProjectPathFromReview(p.Review, p.Repository)
	iid := 0
	if mr, ok := a.trackedMRForWorkItem(ctx, p.WorkItemID, projectPath, p.Branch, p.WorktreePath); ok {
		iid = mr.IID
	}
	description := appendTrackerFooter(strings.TrimSpace(p.SubPlan), renderGitLabTrackerRefs(p.TrackerRefs))
	if err := a.updateMRDescription(ctx, p.WorktreePath, p.Branch, iid, description); err != nil {
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
			if err := a.markMRReady(ctx, entry.worktreePath, p.Branch, 0); err != nil {
				slog.Warn("glab: mr update --draft=false failed", "repo", entry.repo, "branch", p.Branch, "error", err)
				continue
			}
		} else if a.mrExists(ctx, entry.worktreePath, p.Branch) {
			// MR exists on remote but wasn't tracked (adapter restart). Mark ready.
			if err := a.markMRReady(ctx, entry.worktreePath, p.Branch, 0); err != nil {
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
			if err := a.markMRReady(ctx, entry.worktreePath, p.Branch, 0); err != nil {
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

// subPlanPRReadyPayload is the expected shape of EventSubPlanPRReady for glab.
type subPlanPRReadyPayload struct {
	WorkItemID     string                    `json:"work_item_id"`
	WorkspaceID    string                    `json:"workspace_id"`
	PlanID         string                    `json:"plan_id"`
	Repository     string                    `json:"repository"`
	Branch         string                    `json:"branch"`
	WorktreePath   string                    `json:"worktree_path"`
	WorkItemTitle  string                    `json:"work_item_title"`
	SubPlanContent string                    `json:"sub_plan_content"`
	TrackerRefs    []domain.TrackerReference `json:"tracker_refs"`
	Review         domain.ReviewRef          `json:"review"`
}

func (a *GlabAdapter) onSubPlanPRReady(ctx context.Context, payload string) error {
	var p subPlanPRReadyPayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return fmt.Errorf("unmarshal sub-plan PR-ready payload: %w", err)
	}
	// Only act on GitLab-hosted repos.
	if provider := strings.ToLower(strings.TrimSpace(p.Review.BaseRepo.Provider)); provider != "" && provider != "gitlab" {
		return nil
	}
	if p.Branch == "" {
		return fmt.Errorf("glab: sub-plan PR-ready payload has no branch")
	}
	// Validate review coordinates are present.
	// Require at least BaseRepo.Provider or BaseRepo.Repo to identify the GitLab project.
	// HeadRepo fields are optional since glab uses --source-branch directly without owner:branch format.
	if p.Review.BaseRepo.Provider == "" && p.Review.BaseRepo.Repo == "" {
		return fmt.Errorf("glab: sub-plan PR-ready payload missing review coordinates")
	}

	projectPath := gitlabProjectPathFromReview(p.Review, p.Repository)
	worktreePath := p.WorktreePath
	if worktreePath == "" {
		worktreePath = a.workspaceDir
	}

	// Prefer the durable artifact link created when the draft MR was first recorded.
	// `glab mr view` can fail when local worktree/remote discovery is stale, but the
	// tracked artifact is the product-owned source of truth for an MR we already know.
	if mr, ok := a.trackedMRForSubPlanPRReady(ctx, p, projectPath); ok {
		a.handleExistingMR(ctx, mr, p, projectPath, worktreePath)
		return nil
	}

	// Check if MR exists via glab.
	if mr, ok := a.mrView(ctx, worktreePath, p.Branch); ok {
		a.handleExistingMR(ctx, mr, p, projectPath, worktreePath)
		return nil
	}

	// No existing tracked or discoverable MR — create one non-draft
	title := mrTitle(p.WorkItemTitle, p.Branch)
	description := appendTrackerFooter(strings.TrimSpace(p.SubPlanContent), renderGitLabTrackerRefs(p.TrackerRefs))
	mrURL, err := a.createMRNonDraft(ctx, worktreePath, p.Branch, title, description)
	if err != nil {
		slog.Warn("glab: sub-plan PR-ready MR creation failed", "repo", projectPath, "branch", p.Branch, "error", err)
		return nil
	}

	// Persist the MR. Prefer the follow-up mrView for full state; fall back to
	// the URL from create output if mrView fails.
	if mr, ok := a.mrView(ctx, worktreePath, p.Branch); ok {
		a.recordGitlabMR(ctx, p.WorkspaceID, p.WorkItemID, domain.ReviewArtifact{
			Provider:     "gitlab",
			Kind:         "MR",
			RepoName:     projectPath,
			Ref:          glabArtifactRef(strings.TrimSpace(mr.WebURL), mr.IID),
			URL:          strings.TrimSpace(mr.WebURL),
			State:        glabArtifactState(mr),
			Branch:       p.Branch,
			WorktreePath: worktreePath,
			UpdatedAt:    time.Now(),
		}, projectPath, mr.IID)
	} else if mrURL != "" {
		// mrView failed but create succeeded — extract IID from the returned URL.
		iid := 0
		if parsed, err := parseGlabMRNumber(mrURL); err == nil {
			iid = parsed
		} else {
			slog.Warn("glab: failed to parse IID from create-MR URL", "url", mrURL, "error", err)
		}
		a.recordGitlabMR(ctx, p.WorkspaceID, p.WorkItemID, domain.ReviewArtifact{
			Provider:     "gitlab",
			Kind:         "MR",
			RepoName:     projectPath,
			Ref:          mrURL,
			URL:          mrURL,
			State:        "opened",
			Branch:       p.Branch,
			WorktreePath: worktreePath,
			UpdatedAt:    time.Now(),
		}, projectPath, iid)
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

// createMRNonDraft runs `glab mr create --source-branch <branch> --title <title> [--description ...] [--label ...] --yes`.
// Used for PR-ready events where the MR should not be a draft.
func (a *GlabAdapter) createMRNonDraft(ctx context.Context, dir, branch, title, description string) (string, error) {
	args := []string{
		"mr", "create",
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
		return "", fmt.Errorf("glab mr create (non-draft): %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	url := parseMRURL(out)
	if url != "" {
		slog.Info("glab: MR created (non-draft)", "branch", branch, "url", url)
	}

	return url, nil
}

// glabMRView is the minimal subset of `glb mr view --output json` we need.
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

// mrViewRemote fetches MR metadata using glab api (no local repo required).
func (a *GlabAdapter) mrViewRemote(ctx context.Context, projectPath string, iid int) (glabMRView, bool) {
	// URL-encode the project path for the API
	encodedPath := url.PathEscape(projectPath)
	endpoint := fmt.Sprintf("/projects/%s/merge_requests/%d", encodedPath, iid)
	slog.Debug("glab: mrViewRemote endpoint", "endpoint", endpoint)

	hostname := glabHostname(a.cfg.BaseURL)

	args := []string{"api", endpoint}
	if hostname != "" {
		args = append(args, "--hostname", hostname)
	}
	out, err := a.runner(ctx, a.workspaceDir, "glab", args...)
	slog.Debug("glab: mrViewRemote result", "endpoint", endpoint, "iid", iid, "hasError", err != nil, "outputLen", len(out))
	if err != nil {
		slog.Warn("glab: mrViewRemote failed", "endpoint", endpoint, "iid", iid, "error", err, "output", string(out))
		return glabMRView{}, false
	}
	var mr glabMRView
	if err := json.Unmarshal(out, &mr); err != nil {
		slog.Warn("glab: mrViewRemote unmarshal failed", "endpoint", endpoint, "iid", iid, "error", err, "output", string(out))
		return glabMRView{}, false
	}
	slog.Debug("glab: mrViewRemote parsed", "endpoint", endpoint, "iid", iid, "mr.IID", mr.IID, "raw_state", strings.TrimSpace(mr.State), "artifact_state", glabArtifactState(mr), "draft", mr.Draft, "work_in_progress", mr.WorkInProgress, "web_url", strings.TrimSpace(mr.WebURL))
	if mr.IID <= 0 {
		slog.Warn("glab: mrViewRemote invalid IID", "endpoint", endpoint, "iid", iid, "mr.IID", mr.IID)
		return glabMRView{}, false
	}

	return mr, true
}

// runGlabApi executes a glab api command. If repoDir is empty, uses --hostname.
func (a *GlabAdapter) runGlabApi(ctx context.Context, repoDir string, endpoint string) ([]byte, error) {
	args := []string{"api", endpoint}
	if repoDir == "" {
		hostname := glabHostname(a.cfg.BaseURL)
		if hostname != "" {
			args = append([]string{"--hostname", hostname}, args...)
		}
		return a.runner(ctx, a.workspaceDir, "glab", args...)
	}
	return a.runner(ctx, repoDir, "glab", args...)
}

func (a *GlabAdapter) mrExists(ctx context.Context, dir, branch string) bool {
	_, ok := a.mrView(ctx, dir, branch)

	return ok
}

func (a *GlabAdapter) trackedMRForSubPlanPRReady(ctx context.Context, p subPlanPRReadyPayload, projectPath string) (glabMRView, bool) {
	return a.trackedMRForWorkItem(ctx, p.WorkItemID, projectPath, p.Branch, p.WorktreePath)
}

func (a *GlabAdapter) trackedMRForWorkItem(ctx context.Context, workItemID, projectPath, branch, worktreePath string) (glabMRView, bool) {
	if a.repos.SessionArtifacts == nil || a.repos.GitlabMRs == nil || strings.TrimSpace(workItemID) == "" {
		return glabMRView{}, false
	}
	links, err := a.repos.SessionArtifacts.ListByWorkItemID(ctx, workItemID)
	if err != nil {
		slog.Warn("glab: list tracked MR artifacts failed", "work_item_id", workItemID, "error", err)
		return glabMRView{}, false
	}
	projectPath = strings.TrimSpace(projectPath)
	requireProjectMatch := strings.Contains(projectPath, "/")
	branch = strings.TrimSpace(branch)
	worktreePath = strings.TrimSpace(worktreePath)
	for _, link := range links {
		if link.Provider != "gitlab" || strings.TrimSpace(link.ProviderArtifactID) == "" {
			continue
		}
		mr, err := a.repos.GitlabMRs.Get(ctx, link.ProviderArtifactID)
		if err != nil {
			slog.Warn("glab: get tracked MR artifact failed", "work_item_id", workItemID, "artifact_id", link.ProviderArtifactID, "error", err)
			continue
		}
		if requireProjectMatch && strings.TrimSpace(resolveProjectPath(mr)) != projectPath {
			continue
		}
		if worktreePath != "" && strings.TrimSpace(mr.WorktreePath) != "" && strings.TrimSpace(mr.WorktreePath) != worktreePath {
			continue
		}
		if strings.TrimSpace(mr.SourceBranch) != branch || mr.IID <= 0 {
			continue
		}
		return glabMRView{
			IID:            mr.IID,
			State:          mr.State,
			WebURL:         mr.WebURL,
			Draft:          mr.Draft,
			WorkInProgress: mr.Draft,
		}, true
	}
	return glabMRView{}, false
}

// handleExistingMR undrafts an MR that already exists and records it in the artifact store.
// This is the canonical path for both the initial mrView check and the 409-conflict recovery path.
//
// Recording is skipped if a link already exists for this work item. This avoids duplicate
// session_review_artifacts links when the MR was already recorded as a draft during
// onWorktreeCreated — the link from that initial recording is preserved and updated
// via the MR row upsert in PersistGitlabMR.
func (a *GlabAdapter) handleExistingMR(ctx context.Context, mr glabMRView, p subPlanPRReadyPayload, projectPath, worktreePath string) {
	// Check if a link already exists for this work item. If so, skip re-recording
	// to avoid duplicate links. The MR row itself is already upserted (ON CONFLICT updates
	// the state/draft/URL), so the existing link will reflect the updated state.
	if a.linkExistsForWorkItem(ctx, p.WorkItemID) {
		slog.Debug("glab: MR link already exists for work item, skipping re-record",
			"workItemID", p.WorkItemID, "iid", mr.IID)
	}

	// Mark the MR ready (undraft it) so it's ready for review.
	if err := a.markMRReady(ctx, worktreePath, p.Branch, mr.IID); err != nil {
		slog.Warn("glab: mr update --ready failed", "repo", projectPath, "branch", p.Branch, "error", err)
	}
	// Refresh MR state after undrafting to capture the post-update draft flag.
	// Preserve the original MR data if refresh fails, since we already have valid metadata.
	if refreshed, refreshOk := a.mrView(ctx, worktreePath, p.Branch); refreshOk {
		mr = refreshed
	}

	// Only record if no link exists for this work item.
	if !a.linkExistsForWorkItem(ctx, p.WorkItemID) {
		a.recordGitlabMR(ctx, p.WorkspaceID, p.WorkItemID, domain.ReviewArtifact{
			Provider:     "gitlab",
			Kind:         "MR",
			RepoName:     projectPath,
			Ref:          glabArtifactRef(strings.TrimSpace(mr.WebURL), mr.IID),
			URL:          strings.TrimSpace(mr.WebURL),
			State:        glabArtifactState(mr),
			Branch:       p.Branch,
			WorktreePath: worktreePath,
			UpdatedAt:    time.Now(),
		}, projectPath, mr.IID)
	}
}

// linkExistsForWorkItem returns true if a session_review_artifacts link already exists
// for the given work item with provider "gitlab".
func (a *GlabAdapter) linkExistsForWorkItem(ctx context.Context, workItemID string) bool {
	if a.repos.SessionArtifacts == nil || strings.TrimSpace(workItemID) == "" {
		return false
	}
	links, err := a.repos.SessionArtifacts.ListByWorkItemID(ctx, workItemID)
	if err != nil {
		slog.Warn("glab: check link exists failed", "workItemID", workItemID, "error", err)
		return false
	}
	for _, link := range links {
		if link.Provider == "gitlab" {
			return true
		}
	}
	return false
}

// markMRReady runs `glab mr update <iid> --ready --yes` against a resolved MR IID.
func (a *GlabAdapter) markMRReady(ctx context.Context, dir, branch string, iid int) error {
	target, ok := a.mrUpdateTarget(ctx, dir, branch, iid)
	if !ok {
		return fmt.Errorf("resolve GitLab MR update target for branch %q", branch)
	}
	out, err := a.runner(ctx, dir, "glab",
		"mr", "update", target,
		"--ready",
		"--yes",
	)
	if err != nil {
		return fmt.Errorf("glab mr update: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}

	return nil
}

// updateMRDescription updates the description of an existing MR.
func (a *GlabAdapter) updateMRDescription(ctx context.Context, dir, branch string, iid int, description string) error {
	target, ok := a.mrUpdateTarget(ctx, dir, branch, iid)
	if !ok {
		return fmt.Errorf("resolve GitLab MR update target for branch %q", branch)
	}
	out, err := a.runner(ctx, dir, "glab",
		"mr", "update", target,
		"--description", description,
		"--yes",
	)
	if err != nil {
		return fmt.Errorf("glab mr update description: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}

	return nil
}

func (a *GlabAdapter) mrUpdateTarget(ctx context.Context, dir, branch string, iid int) (string, bool) {
	if iid > 0 {
		return strconv.Itoa(iid), true
	}
	if mr, ok := a.mrView(ctx, dir, branch); ok {
		return strconv.Itoa(mr.IID), true
	}
	if mr, ok := a.mrViewBySourceBranchAPI(ctx, dir, branch); ok {
		return strconv.Itoa(mr.IID), true
	}
	return "", false
}

func (a *GlabAdapter) mrViewBySourceBranchAPI(ctx context.Context, dir, branch string) (glabMRView, bool) {
	branch = strings.TrimSpace(branch)
	if branch == "" || strings.TrimSpace(dir) == "" {
		return glabMRView{}, false
	}
	projectPath, err := a.projectPathFromLocalRepo(ctx, dir)
	if err != nil {
		slog.Debug("glab: resolve project path for source branch MR lookup failed", "dir", dir, "branch", branch, "error", err)
		return glabMRView{}, false
	}
	endpoint := fmt.Sprintf(
		"/projects/%s/merge_requests?source_branch=%s&state=opened&order_by=updated_at&sort=desc&per_page=2",
		url.PathEscape(projectPath),
		url.QueryEscape(branch),
	)
	out, err := a.runGlabApi(ctx, dir, endpoint)
	if err != nil {
		slog.Debug("glab: source branch MR lookup failed", "project", projectPath, "branch", branch, "error", err)
		return glabMRView{}, false
	}
	var mrs []glabMRView
	if err := json.Unmarshal(out, &mrs); err != nil {
		slog.Warn("glab: unmarshal source branch MR lookup failed", "project", projectPath, "branch", branch, "error", err)
		return glabMRView{}, false
	}
	for _, mr := range mrs {
		if mr.IID > 0 {
			if len(mrs) > 1 {
				slog.Debug("glab: multiple MRs share source branch; using most recently updated", "project", projectPath, "branch", branch, "iid", mr.IID)
			}
			return mr, true
		}
	}
	return glabMRView{}, false
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

// parseGlabMRNumber extracts the IID from a GitLab MR URL.
func parseGlabMRNumber(url string) (int, error) {
	marker := "/-/merge_requests/"
	idx := strings.LastIndex(url, marker)
	if idx == -1 {
		return 0, fmt.Errorf("no %q marker in MR URL %q", marker, url)
	}
	s := strings.TrimPrefix(url[idx+len(marker):], "!")
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("parse IID from MR URL %q: %w", url, err)
	}
	return n, nil
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
	if projectPath := gitlabProjectPathFromReview(p.Review, ""); projectPath != "" && len(tracked) == 1 {
		tracked[0].repo = projectPath
	}
	if a.repos.Events == nil || strings.TrimSpace(p.WorkspaceID) == "" || strings.TrimSpace(p.WorkItemID) == "" {
		return tracked
	}
	events, err := a.repos.Events.ListByWorkspaceID(ctx, p.WorkspaceID, 0)
	if err != nil {
		slog.Warn("glab: list review artifact events for completion failed", "workspace_id", p.WorkspaceID, "error", err)
		return tracked
	}
	seen := make(map[string]branchEntry)
	for _, event := range events {
		if domain.EventType(event.EventType) != domain.EventReviewArtifactRecorded {
			continue
		}
		var payload domain.ReviewArtifactEventPayload
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			slog.Warn("glab: unmarshal review artifact event for completion failed", "event_id", event.ID, "error", err)
			continue
		}
		artifact := payload.Artifact
		if payload.WorkItemID != p.WorkItemID || artifact.Provider != "gitlab" ||
			strings.TrimSpace(artifact.Branch) != strings.TrimSpace(p.Branch) ||
			strings.TrimSpace(artifact.WorktreePath) == "" {
			continue
		}
		projectPath := gitlabProjectPathFromMRURL(artifact.URL)
		if projectPath == "" {
			projectPath = strings.TrimSpace(artifact.RepoName)
		}
		seen[projectPath+"|"+artifact.WorktreePath] = branchEntry{
			repo: projectPath, worktreePath: artifact.WorktreePath,
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
		slog.Warn("glab: list worktree events for completion failed", "workspace_id", p.WorkspaceID, "error", err)
		return nil
	}
	var entries []branchEntry
	for _, event := range events {
		if domain.EventType(event.EventType) != domain.EventWorktreeCreated {
			continue
		}
		var wt worktreePayload
		if err := json.Unmarshal([]byte(event.Payload), &wt); err != nil {
			slog.Warn("glab: unmarshal worktree event for completion failed", "event_id", event.ID, "error", err)
			continue
		}
		if wt.WorkItemID != p.WorkItemID || wt.Branch != p.Branch || wt.WorktreePath == "" {
			continue
		}
		projectPath := gitlabProjectPathFromReview(wt.Review, wt.Repository)
		entries = append(entries, branchEntry{
			repo:         projectPath,
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

func gitlabProjectPathFromReview(review domain.ReviewRef, fallback string) string {
	if projectPath := gitlabProjectPathFromRepoRef(review.BaseRepo); projectPath != "" {
		return projectPath
	}
	if projectPath := gitlabProjectPathFromRepoRef(review.HeadRepo); projectPath != "" {
		return projectPath
	}

	return strings.TrimSpace(fallback)
}

func gitlabProjectPathFromRepoRef(ref domain.RepoRef) string {
	owner := strings.Trim(strings.TrimSpace(ref.Owner), "/")
	repo := strings.Trim(strings.TrimSpace(ref.Repo), "/")
	switch {
	case owner != "" && repo != "":
		return owner + "/" + repo
	case owner == "" && strings.Contains(repo, "/"):
		return repo
	default:
		return ""
	}
}

func gitlabProjectPathFromMRURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Path == "" {
		return ""
	}
	path, err := url.PathUnescape(strings.Trim(parsed.EscapedPath(), "/"))
	if err != nil {
		path = strings.Trim(parsed.Path, "/")
	}
	projectPath, _, ok := strings.Cut(path, "/-/merge_requests/")
	if !ok {
		return ""
	}

	return strings.Trim(projectPath, "/")
}

func gitlabProjectPathFromRemoteURL(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.TrimSuffix(trimmed, ".git")
	if strings.Contains(trimmed, "://") {
		parsed, err := url.Parse(trimmed)
		if err != nil {
			return ""
		}

		return projectPathFromRemotePath(parsed.Path)
	}
	if strings.HasPrefix(trimmed, "git@") || strings.HasPrefix(trimmed, "ssh://") {
		trimmed = strings.TrimPrefix(trimmed, "ssh://")
		if at := strings.Index(trimmed, "@"); at >= 0 {
			trimmed = trimmed[at+1:]
		}
		if colon := strings.Index(trimmed, ":"); colon >= 0 {
			trimmed = trimmed[colon+1:]
		} else if slash := strings.Index(trimmed, "/"); slash >= 0 {
			trimmed = trimmed[slash+1:]
		}

		return projectPathFromRemotePath(trimmed)
	}

	return ""
}

func projectPathFromRemotePath(path string) string {
	projectPath := strings.Trim(path, "/")
	if !strings.Contains(projectPath, "/") {
		return ""
	}

	return projectPath
}

func glabHostname(baseURL string) string {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err == nil && parsed.Host != "" {
		return parsed.Host
	}
	trimmed = strings.TrimPrefix(trimmed, "https://")
	trimmed = strings.TrimPrefix(trimmed, "http://")
	if host, _, ok := strings.Cut(trimmed, "/"); ok {
		return host
	}

	return trimmed
}

// resolveProjectPath returns the canonical GitLab project path for an MR.
// If the stored path looks wrong (no "/", suggesting a local folder name),
// it extracts the correct path from the stored WebURL.
func resolveProjectPath(mr domain.GitlabMergeRequest) string {
	// A valid GitLab project path always contains at least one "/" (group/repo or group/subgroup/repo).
	if strings.Contains(mr.ProjectPath, "/") {
		return mr.ProjectPath
	}

	// Stored path looks like a local folder name. Try to extract the real path from WebURL.
	if path := gitlabProjectPathFromMRURL(mr.WebURL); path != "" {
		return path
	}

	// Last resort: return the stored path unchanged.
	return mr.ProjectPath
}

func (a *GlabAdapter) refreshProjectPath(ctx context.Context, mr domain.GitlabMergeRequest, repoDir string, hasLocalRepo bool) (string, bool) {
	if projectPath := gitlabProjectPathFromMRURL(mr.WebURL); projectPath != "" {
		return projectPath, true
	}
	projectPath := strings.TrimSpace(mr.ProjectPath)
	if strings.Contains(projectPath, "/") {
		return projectPath, true
	}
	if hasLocalRepo {
		localProjectPath, err := a.projectPathFromLocalRepo(ctx, repoDir)
		if err != nil {
			slog.Warn("glab: resolve project path from local repo failed", "iid", mr.IID, "repoDir", repoDir, "error", err)
		} else if localProjectPath != "" {
			return localProjectPath, true
		}
	}

	return projectPath, false
}

func (a *GlabAdapter) projectPathFromLocalRepo(ctx context.Context, repoDir string) (string, error) {
	out, err := a.runner(ctx, repoDir, "git", "remote", "get-url", "origin")
	if err != nil {
		return "", fmt.Errorf("git remote get-url origin: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	projectPath := gitlabProjectPathFromRemoteURL(string(out))
	if projectPath == "" {
		return "", fmt.Errorf("parse origin remote url %q", strings.TrimSpace(string(out)))
	}

	return projectPath, nil
}

func (a *GlabAdapter) recordGitlabMR(ctx context.Context, workspaceID, workItemID string, artifact domain.ReviewArtifact, projectPath string, iid int) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" || !strings.Contains(projectPath, "/") {
		if fromURL := gitlabProjectPathFromMRURL(artifact.URL); fromURL != "" {
			projectPath = fromURL
			artifact.RepoName = fromURL
		}
	}
	if err := coreadapter.PersistGitlabMR(ctx, a.repos, workspaceID, workItemID, artifact, projectPath, iid); err != nil {
		slog.Warn("glab: persist review artifact failed", "repo", artifact.RepoName, "branch", artifact.Branch, "error", err)
	}
}

// mrTitle derives the MR title using the following priority:
//  1. workItemTitle if non-empty
//  2. Parsed from branch slug: "sub-LIN-FOO-123-fix-auth-flow" → "Fix auth flow [LIN-FOO-123]"
//  3. Branch name as fallback
func mrTitle(workItemTitle, branch string) string {
	title := workItemTitle
	if title == "" {
		title = titleFromBranch(branch)
	}

	return clampMRTitle(title)
}

// clampMRTitle keeps titles within GitLab's 255-character MR title limit.
func clampMRTitle(title string) string {
	cut := -1
	runeCount := 0
	for i := range title {
		if runeCount == gitlabMRTitleMaxRunes-1 {
			cut = i
		}
		runeCount++
		if runeCount > gitlabMRTitleMaxRunes {
			return strings.TrimRightFunc(title[:cut], unicode.IsSpace) + gitlabMRTitleEllipsisSuffix
		}
	}

	return title
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
// call is a no-op (safe for tests). Returns a stop function that cancels
// the refresh loop; callers MUST invoke it on adapter teardown.
func (a *GlabAdapter) StartMRRefresh(ctx context.Context, workspaceID string) func() {
	if a.repos.GitlabMRs == nil {
		return nil
	}
	a.workspaceID = workspaceID
	refreshCtx, cancel := context.WithCancel(context.Background())
	go a.refreshMRLoop(refreshCtx)
	return cancel
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
		if err := ctx.Err(); err != nil {
			slog.Debug("glab: refresh mrs context canceled", "error", err)
			return
		}
		a.refreshSingleMR(ctx, mr)
	}
}

func (a *GlabAdapter) refreshSingleMR(ctx context.Context, mr domain.GitlabMergeRequest) {
	started := time.Now()
	repoDir, hasLocalRepo := a.refreshMRRepoDir(mr)
	projectPath, hasProjectPath := a.refreshProjectPath(ctx, mr, repoDir, hasLocalRepo)
	if !hasProjectPath {
		slog.Warn("glab: refresh mr project path unresolved", "iid", mr.IID, "stored_project", mr.ProjectPath, "web_url", mr.WebURL, "repoDir", repoDir, "hasLocalRepo", hasLocalRepo)
		return
	}
	if projectPath != mr.ProjectPath {
		slog.Info("glab: corrected project path", "iid", mr.IID, "old", mr.ProjectPath, "new", projectPath)
	}
	slog.Debug("glab: refreshSingleMR", "iid", mr.IID, "project", projectPath, "repoDir", repoDir, "hasLocalRepo", hasLocalRepo, "workspaceDir", a.workspaceDir)
	var fresh glabMRView
	var ok bool

	slog.Debug("glab: refreshSingleMR local lookup", "iid", mr.IID, "hasLocalRepo", hasLocalRepo)

	// Try local repo first, fall back to remote lookup.
	if hasLocalRepo {
		fresh, ok = a.mrView(ctx, repoDir, mr.SourceBranch)
		slog.Debug("glab: refreshSingleMR local result", "iid", mr.IID, "ok", ok)
	}
	if !ok {
		fresh, ok = a.mrViewRemote(ctx, projectPath, mr.IID)
		slog.Debug("glab: refreshSingleMR remote result", "iid", mr.IID, "ok", ok, "raw_state", strings.TrimSpace(fresh.State), "artifact_state", glabArtifactState(fresh), "draft", fresh.Draft, "work_in_progress", fresh.WorkInProgress)
		if !ok {
			if err := ctx.Err(); err != nil {
				slog.Warn("glab: refresh mr lookup canceled", "iid", mr.IID, "project", projectPath, "error", err)
				return
			}
			slog.Warn("glab: refresh mr lookup failed", "iid", mr.IID, "project", projectPath, "elapsed", time.Since(started))
			return
		}
		// Remote lookup succeeded without repository-local glab state; use
		// hostname-based API calls for follow-up review/check refreshes too.
		repoDir = ""
	}

	// If the remote response contains a WebURL, extract the canonical path from it.
	// This handles cases where the stored path was partially wrong but passed the
	// initial slash check (e.g., a different subgroup structure).
	if fresh.WebURL != "" {
		if remotePath := gitlabProjectPathFromMRURL(fresh.WebURL); remotePath != "" {
			projectPath = remotePath
		}
	}

	// If the project path changed, we need to either update the existing row or
	// replace the old row with the corrected one. First, check if a canonical MR
	// already exists with the correct path.
	var canonicalID string
	if projectPath != mr.ProjectPath {
		if canonical, err := a.repos.GitlabMRs.GetByIID(ctx, projectPath, mr.IID); err == nil {
			canonicalID = canonical.ID
		}
	}

	// If a canonical MR exists, transfer session_review_artifacts from the old
	// MR to the canonical one before deleting the old row. This preserves the
	// work item -> MR link.
	if canonicalID != "" {
		slog.Info("glab: transferring session links to canonical MR",
			"iid", mr.IID, "old_id", mr.ID, "canonical_id", canonicalID)
		if a.repos.SessionArtifacts != nil {
			if err := a.repos.SessionArtifacts.TransferArtifactLinks(ctx, mr.ID, canonicalID); err != nil {
				slog.Warn("glab: failed to transfer session links",
					"iid", mr.IID, "error", err)
			}
		}
		if err := a.repos.GitlabMRs.Delete(ctx, mr.ID); err != nil {
			slog.Warn("glab: failed to delete old MR row after transfer",
				"iid", mr.IID, "id", mr.ID, "error", err)
		}
	}

	state := glabArtifactState(fresh)
	slog.Debug("glab: refreshSingleMR updating", "iid", mr.IID, "raw_state", strings.TrimSpace(fresh.State), "state", state, "draft", fresh.Draft, "work_in_progress", fresh.WorkInProgress)
	updated := domain.GitlabMergeRequest{
		ID:           mr.ID,
		ProjectPath:  projectPath,
		IID:          fresh.IID,
		State:        state,
		Draft:        fresh.Draft || fresh.WorkInProgress,
		SourceBranch: mr.SourceBranch,
		WebURL:       strings.TrimSpace(fresh.WebURL),
		WorktreePath: mr.WorktreePath,
		CreatedAt:    mr.CreatedAt,
		UpdatedAt:    time.Now(),
	}
	if err := a.repos.GitlabMRs.Upsert(ctx, updated); err != nil {
		slog.Warn("glab: refresh mr upsert failed", "iid", mr.IID, "project", projectPath, "state", state, "error", err)
		return
	}
	slog.Debug("glab: refreshSingleMR upserted", "iid", mr.IID, "project", projectPath, "state", state, "elapsed", time.Since(started))

	// Fetch and upsert MR reviews.
	slog.Debug("glab: refreshSingleMR terminal state check", "iid", mr.IID, "state", state)
	if state == "merged" || state == "closed" {
		slog.Debug("glab: refreshSingleMR deleting reviews/checks", "iid", mr.IID, "state", state)
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
		// Build a corrected MR copy for review/check refresh (they use mr.ProjectPath in API calls).
		corrected := mr
		corrected.ProjectPath = projectPath
		corrected.WebURL = strings.TrimSpace(fresh.WebURL)
		slog.Debug("glab: refreshSingleMR refreshing reviews/checks", "iid", mr.IID)
		a.refreshMRReviews(ctx, corrected, repoDir)
		slog.Debug("glab: refreshSingleMR reviews done", "iid", mr.IID)
		a.refreshMRChecks(ctx, corrected, repoDir)
		slog.Debug("glab: refreshSingleMR checks done", "iid", mr.IID)
	}

	// Detect merge transition: MR just became merged.
	if state == "merged" && mr.State != "merged" {
		a.checkAllMerged(ctx, mr.ID)
	}
}

func (a *GlabAdapter) refreshMRRepoDir(mr domain.GitlabMergeRequest) (string, bool) {
	dir := strings.TrimSpace(mr.WorktreePath)
	if dir == "" {
		return "", false
	}
	if _, err := os.Stat(dir); err == nil {
		return dir, true
	} else {
		slog.Warn("glab: refresh mr repo dir not found", "dir", dir, "iid", mr.IID, "error", err)
	}

	return "", false
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
	approvalOut, err := a.runGlabApi(ctx, repoDir,
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
	discOut, err := a.runGlabApi(ctx, repoDir,
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
	pipelinesOut, err := a.runGlabApi(ctx, repoDir,
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
	jobsOut, err := a.runGlabApi(ctx, repoDir,
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
		if coreadapter.IsWorkItemNotFound(err) {
			slog.Debug("glab: skip merge check for stale review artifact", "work_item_id", workItemID, "mr_id", mrID, "error", err)
			return
		}
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
	ids := extractExternalIDs(payload, "gl:issue:")
	if len(ids) == 0 {
		return nil
	}
	if a.workspaceDir == "" {
		return fmt.Errorf("glab: no workspace dir for issue close")
	}
	var lastErr error
	for _, extID := range ids {
		raw := strings.TrimPrefix(extID, "gl:issue:")
		parts := strings.SplitN(raw, "#", 2)
		if len(parts) != 2 {
			lastErr = fmt.Errorf("invalid gitlab external id %q", extID)
			continue
		}
		projectID := parts[0]
		issueIID := parts[1]
		if projectID == "" || issueIID == "" {
			lastErr = fmt.Errorf("invalid gitlab external id %q", extID)
			continue
		}
		_, err := a.runner(ctx, a.workspaceDir, "glab", "api", "-X", "PUT",
			fmt.Sprintf("/projects/%s/issues/%s", url.PathEscape(projectID), url.PathEscape(issueIID)),
			"--field", "state_event=close")
		if err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// extractExternalIDs returns all external IDs matching the given prefix from the payload.
// Falls back to the legacy single external_id field if external_ids is absent.
func extractExternalIDs(payload string, prefix string) []string {
	var parsed struct {
		ExternalID  string   `json:"external_id"`
		ExternalIDs []string `json:"external_ids"`
	}
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		return nil
	}
	var ids []string
	if len(parsed.ExternalIDs) > 0 {
		for _, id := range parsed.ExternalIDs {
			if strings.HasPrefix(id, prefix) {
				ids = append(ids, id)
			}
		}
	} else if strings.HasPrefix(parsed.ExternalID, prefix) {
		ids = append(ids, parsed.ExternalID)
	}
	return ids
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
// target.RepoIdentifier is the GitLab project path (e.g. "group/sub/repo");
// it is URL-escaped here. target.WorktreePath, when set, is used as the glab
// working directory so glab resolves the correct GitLab host and credentials.
// The endpoint is paginated explicitly so MRs with more than
// glabDiscussionsPageSize discussions are fully covered.
func (a *GlabAdapter) FetchReviewComments(ctx context.Context, target coreadapter.ReviewCommentTarget) ([]coreadapter.ReviewComment, error) {
	projectPath := target.RepoIdentifier
	iid := target.Number
	commandDir := a.reviewCommentCommandDir(target.WorktreePath)
	webURL, err := a.fetchMRWebURL(ctx, commandDir, projectPath, iid)
	if err != nil {
		// Non-fatal: URL field is best-effort. Continue with empty webURL.
		slog.Warn("glab: fetch MR web_url failed", "project", projectPath, "iid", iid, "error", err)
	}

	var comments []coreadapter.ReviewComment
	for page := 1; ; page++ {
		endpoint := fmt.Sprintf("/projects/%s/merge_requests/%d/discussions?per_page=%d&page=%d",
			url.PathEscape(projectPath), iid, glabDiscussionsPageSize, page)
		output, runErr := a.runner(ctx, commandDir, "glab", "api", endpoint)

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

func (a *GlabAdapter) reviewCommentCommandDir(worktreePath string) string {
	if dir := strings.TrimSpace(worktreePath); dir != "" {
		return dir
	}

	return a.workspaceDir
}

// fetchMRWebURL queries the merge_request endpoint to recover web_url, used for
// constructing per-note links. Empty result is treated as "unknown" by callers.
func (a *GlabAdapter) fetchMRWebURL(ctx context.Context, commandDir, projectPath string, iid int) (string, error) {
	endpoint := fmt.Sprintf("/projects/%s/merge_requests/%d", url.PathEscape(projectPath), iid)
	output, err := a.runner(ctx, commandDir, "glab", "api", endpoint)
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
