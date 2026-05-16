package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os/exec"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
)

// Verify GithubAdapter implements adapter.WorkItemAdapter at compile time.
var _ adapter.WorkItemAdapter = WorkItemAdapter{}

// Verify GithubAdapter implements adapter.RepoLifecycleAdapter at compile time.
var _ adapter.RepoLifecycleAdapter = RepoLifecycleAdapter{}

// Verify GithubAdapter implements adapter.ReviewCommentFetcher at compile time.
var _ adapter.ReviewCommentFetcher = &GithubAdapter{}

// Verify GithubAdapter implements prRefresher interface.
type prRefresher interface {
	StartPRRefresh(ctx context.Context, workspaceID string) func()
}

var _ prRefresher = &GithubAdapter{}

const adapterName = "github"

type httpClient interface {
	Do(req *http.Request) (*http.Response, error)
}
type tokenResolver func(context.Context) (string, error)

type GithubAdapter struct {
	cfg           config.GithubConfig
	client        httpClient
	baseURL       string
	token         string
	defaultBranch string
	assignee      string
	viewer        string
	repos         adapter.ReviewArtifactRepos

	mu      sync.RWMutex
	tracked map[string]githubPull
}

type githubOwner struct {
	Login string `json:"login"`
}

type githubRepository struct {
	FullName      string            `json:"full_name"`
	Owner         *githubOwner      `json:"owner"`
	Name          string            `json:"name"`
	HTMLURL       string            `json:"html_url"`
	Fork          bool              `json:"fork"`
	Parent        *githubRepository `json:"parent,omitempty"`
	DefaultBranch string            `json:"default_branch"`
}

type githubIssue struct {
	Number        int64             `json:"number"`
	Title         string            `json:"title"`
	Body          string            `json:"body"`
	State         string            `json:"state"`
	HTMLURL       string            `json:"html_url"`
	Repository    *githubRepository `json:"repository,omitempty"`
	RepositoryURL string            `json:"repository_url,omitempty"`
	CreatedAt     *time.Time        `json:"created_at"`
	UpdatedAt     *time.Time        `json:"updated_at"`
	Labels        []struct {
		Name string `json:"name"`
	} `json:"labels"`
	PullReq *githubIssuePullRequest `json:"pull_request,omitempty"`
}

type githubIssuePullRequest struct {
	MergedAt *time.Time `json:"merged_at"`
	HTMLURL  string     `json:"html_url"`
	URL      string     `json:"url"`
}

type githubTimelineEvent struct {
	Event  string `json:"event"`
	Source struct {
		Issue githubIssue `json:"issue"`
	} `json:"source"`
}

type githubIssueSearchResult struct {
	Items []githubIssue `json:"items"`
}
type githubMilestone struct {
	Number      int64      `json:"number"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	State       string     `json:"state"`
	HTMLURL     string     `json:"html_url"`
	CreatedAt   *time.Time `json:"created_at"`
	UpdatedAt   *time.Time `json:"updated_at"`
}
type githubUser struct {
	Login string `json:"login"`
}
type githubPull struct {
	Number   int        `json:"number"`
	Draft    bool       `json:"draft"`
	HTMLURL  string     `json:"html_url"`
	State    string     `json:"state"`
	MergedAt *time.Time `json:"merged_at"`
	Head     struct {
		Ref string `json:"ref"`
	} `json:"head"`
}

type githubReview struct {
	ID          int         `json:"id"`
	User        githubOwner `json:"user"`
	State       string      `json:"state"`
	SubmittedAt *time.Time  `json:"submitted_at"`
}

type githubCheckRun struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

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

type completedPayload struct {
	WorkspaceID   string                    `json:"workspace_id"`
	WorkItemID    string                    `json:"work_item_id"`
	Branch        string                    `json:"branch"`
	ExternalID    string                    `json:"external_id"`
	WorkItemTitle string                    `json:"work_item_title"`
	SubPlan       string                    `json:"sub_plan"`
	TrackerRefs   []domain.TrackerReference `json:"tracker_refs"`
	Review        domain.ReviewRef          `json:"review"`
}

type subPlanPRReadyPayload struct {
	WorkItemID     string                    `json:"work_item_id"`
	WorkspaceID    string                    `json:"workspace_id"`
	PlanID         string                    `json:"plan_id"`
	SubPlanID      string                    `json:"sub_plan_id"`
	Repository     string                    `json:"repository"`
	Branch         string                    `json:"branch"`
	WorktreePath   string                    `json:"worktree_path"`
	WorkItemTitle  string                    `json:"work_item_title"`
	SubPlanContent string                    `json:"sub_plan_content"`
	TrackerRefs    []domain.TrackerReference `json:"tracker_refs"`
	Review         domain.ReviewRef          `json:"review"`
}

const (
	filterAll                = "all"
	defaultBranchMain        = "main"
	stateClosed              = "closed"
	defaultWatchPollInterval = 5 * time.Minute
	minimumWatchPollInterval = 60 * time.Second
)

var defaultStateMappings = map[string]string{
	string(domain.TrackerStateTodo):       "open",
	string(domain.TrackerStateInProgress): "open",
	string(domain.TrackerStateInReview):   "open",
	string(domain.TrackerStateDone):       "closed",
}

// maxResponseBodyBytes limits HTTP response body reads to prevent OOM from
// a malicious or misconfigured API server.
const maxResponseBodyBytes = 50 * 1024 * 1024 // 50 MiB

func New(ctx context.Context, cfg config.GithubConfig) (*GithubAdapter, error) {
	return newWithDeps(ctx, cfg, adapter.ReviewArtifactRepos{}, &http.Client{Timeout: 30 * time.Second}, execTokenResolver)
}

func NewRepoLifecycle(ctx context.Context, cfg config.GithubConfig, repos adapter.ReviewArtifactRepos) (*GithubAdapter, error) {
	return newWithDeps(ctx, cfg, repos, &http.Client{Timeout: 30 * time.Second}, execTokenResolver)
}

func newWithDeps(
	ctx context.Context,
	cfg config.GithubConfig,
	repos adapter.ReviewArtifactRepos,
	client httpClient,
	resolver tokenResolver,
) (*GithubAdapter, error) {
	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		var err error
		token, err = resolver(ctx)
		if err != nil {
			return nil, fmt.Errorf("resolve github token: %w", err)
		}
	}
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	a := &GithubAdapter{
		cfg:           cfg,
		client:        client,
		baseURL:       baseURL,
		token:         token,
		tracked:       make(map[string]githubPull),
		defaultBranch: defaultBranchMain,
		repos:         repos,
	}
	if len(a.cfg.StateMappings) == 0 {
		a.cfg.StateMappings = defaultStateMappings
	}
	viewer, _ := a.viewerLogin(ctx)
	if cfg.Assignee == "" || cfg.Assignee == "me" {
		if viewer != "" {
			a.assignee = viewer
		} else {
			a.assignee = "me"
		}
	} else {
		a.assignee = cfg.Assignee
	}

	return a, nil
}

func (a *GithubAdapter) viewerLogin(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if strings.TrimSpace(a.viewer) != "" {
		return a.viewer, nil
	}
	var user githubUser
	if err := a.getJSON(ctx, "/user", nil, &user); err != nil {
		return "", fmt.Errorf("resolve github viewer: %w", err)
	}
	login := strings.TrimSpace(user.Login)
	if login == "" {
		return "", errors.New("resolve github viewer: empty login")
	}
	a.viewer = login
	if a.assignee == "" || a.assignee == "me" {
		a.assignee = login
	}

	return login, nil
}

func execTokenResolver(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "gh", "auth", "token").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gh auth token: %w: %s", err, strings.TrimSpace(string(out)))
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		return "", errors.New("gh auth token returned empty output")
	}

	return token, nil
}

func (a *GithubAdapter) Name() string { return adapterName }
func (a *GithubAdapter) Capabilities() adapter.AdapterCapabilities {
	return adapter.AdapterCapabilities{
		CanWatch:     true,
		CanBrowse:    true,
		CanMutate:    true,
		BrowseScopes: []domain.SelectionScope{domain.ScopeIssues, domain.ScopeProjects},
		BrowseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues: {
				Views:          []string{"assigned_to_me", "created_by_me", "mentioned", "subscribed", filterAll},
				States:         []string{"open", stateClosed, filterAll},
				SupportsLabels: true,
				SupportsSearch: true,
				SupportsOffset: true,
				SupportsOwner:  true,
				SupportsRepo:   true,
			},
			domain.ScopeProjects: {
				SupportsOffset: true,
				SupportsRepo:   true,
			},
		},
	}
}

func (a *GithubAdapter) ListSelectable(ctx context.Context, opts adapter.ListOpts) (*adapter.ListResult, error) {
	switch opts.Scope {
	case domain.ScopeIssues:
		issues, err := a.listIssues(ctx, opts)
		if err != nil {
			return nil, err
		}
		items := make([]adapter.ListItem, 0, len(issues))
		for _, iss := range issues {
			item := adapter.ListItem{
				ID:          issueSelectionID(iss),
				Title:       issueListTitle(iss),
				Description: iss.Body,
				State:       iss.State,
				Labels:      issueLabels(iss),
				URL:         iss.HTMLURL,
				ParentRef:   issueParentRef(iss),
				CreatedAt:   derefTime(iss.CreatedAt),
				UpdatedAt:   derefTime(iss.UpdatedAt),
			}
			if artifacts := a.issueReviewArtifacts(ctx, iss); len(artifacts) > 0 {
				item.Metadata = map[string]any{adapter.ListItemReviewArtifactsMetadataKey: artifacts}
			}
			items = append(items, item)
		}

		return &adapter.ListResult{Items: items, TotalCount: len(items)}, nil
	case domain.ScopeProjects:
		milestones, err := a.listMilestones(ctx, opts)
		if err != nil {
			return nil, err
		}
		items := make([]adapter.ListItem, 0, len(milestones))
		for _, ms := range milestones {
			items = append(items, adapter.ListItem{
				ID:          strconv.FormatInt(ms.Number, 10),
				Title:       ms.Title + " (repo milestone)",
				Description: ms.Description,
				State:       ms.State,
				CreatedAt:   derefTime(ms.CreatedAt),
				UpdatedAt:   derefTime(ms.UpdatedAt),
			})
		}

		return &adapter.ListResult{Items: items, TotalCount: len(items)}, nil
	case domain.ScopeInitiatives:
		// TODO(phase-N): GitHub Projects v2 via GraphQL
		return nil, adapter.ErrBrowseNotSupported
	default:
		return nil, adapter.ErrBrowseNotSupported
	}
}

func (a *GithubAdapter) Resolve(ctx context.Context, sel adapter.Selection) (domain.Session, error) {
	switch sel.Scope {
	case domain.ScopeIssues:
		issues := make([]githubIssue, 0, len(sel.ItemIDs))
		for _, itemID := range sel.ItemIDs {
			owner, repo, num, err := parseIssueSelectionID("", "", itemID)
			if err != nil {
				return domain.Session{}, fmt.Errorf("parse github issue id %q: %w", itemID, err)
			}
			iss, err := a.fetchIssue(ctx, owner, repo, num)
			if err != nil {
				return domain.Session{}, err
			}
			issues = append(issues, iss)
		}
		if len(issues) == 1 {
			return issueToWorkItem(issues[0]), nil
		}

		return aggregateIssues(issues), nil
	case domain.ScopeProjects:
		metaOwner, _ := sel.Metadata["owner"].(string)
		metaRepo, _ := sel.Metadata["repo"].(string)
		owner := strings.TrimSpace(metaOwner)
		repo := strings.TrimSpace(metaRepo)
		if owner == "" || repo == "" {
			return domain.Session{}, errors.New("github milestone selection requires owner and repo")
		}
		parts := make([]string, 0, len(sel.ItemIDs))
		titles := make([]string, 0, len(sel.ItemIDs))
		milestones := make([]githubMilestone, 0, len(sel.ItemIDs))
		for _, itemID := range sel.ItemIDs {
			num, err := strconv.ParseInt(itemID, 10, 64)
			if err != nil {
				return domain.Session{}, fmt.Errorf("parse milestone number %q: %w", itemID, err)
			}
			ms, err := a.fetchMilestone(ctx, owner, repo, num)
			if err != nil {
				return domain.Session{}, err
			}
			milestones = append(milestones, ms)
			titles = append(titles, ms.Title)
			parts = append(parts, strings.TrimSpace(ms.Title+"\n"+ms.Description))
		}

		return domain.Session{
			ID:            domain.NewID(),
			ExternalID:    fmt.Sprintf("gh:milestone:%s/%s", owner, repo),
			Source:        a.Name(),
			SourceScope:   domain.ScopeProjects,
			SourceItemIDs: append([]string(nil), sel.ItemIDs...),
			Title:         strings.Join(titles, ", "),
			Description:   strings.Join(parts, "\n\n"),
			State:         domain.SessionIngested,
			Metadata: map[string]any{
				"source_summaries": githubMilestoneSourceSummaries(owner, repo, milestones),
			},
			CreatedAt: domain.Now(),
			UpdatedAt: domain.Now(),
		}, nil
	default:
		return domain.Session{}, adapter.ErrBrowseNotSupported
	}
}

func (a *GithubAdapter) Watch(ctx context.Context, filter adapter.WorkItemFilter) (<-chan adapter.WorkItemEvent, error) {
	interval := parsePollInterval(a.cfg.PollInterval)
	ch := make(chan adapter.WorkItemEvent, 16)
	go func() {
		defer close(ch)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		seen := make(map[int64]string)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				issues, err := a.fetchAssignedOpenIssues(ctx)
				if err != nil {
					ch <- adapter.WorkItemEvent{Type: "error", Timestamp: domain.Now()}

					continue
				}
				for _, iss := range issues {
					if len(filter.States) > 0 && !contains(filter.States, iss.State) {
						continue
					}
					prev, ok := seen[iss.Number]
					seen[iss.Number] = iss.State
					if !ok {
						ch <- adapter.WorkItemEvent{Type: "created", WorkItem: issueToWorkItem(iss), Timestamp: domain.Now()}
					} else if prev != iss.State {
						ch <- adapter.WorkItemEvent{Type: "updated", WorkItem: issueToWorkItem(iss), Timestamp: domain.Now()}
					}
				}
			}
		}
	}()

	return ch, nil
}

func parsePollInterval(raw string) time.Duration {
	interval, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return defaultWatchPollInterval
	}
	if interval < minimumWatchPollInterval {
		return minimumWatchPollInterval
	}

	return interval
}

func (a *GithubAdapter) Fetch(ctx context.Context, externalID string) (domain.Session, error) {
	owner, repo, number, err := parseExternalID(externalID)
	if err != nil {
		return domain.Session{}, err
	}
	iss, err := a.fetchIssue(ctx, owner, repo, number)
	if err != nil {
		return domain.Session{}, err
	}

	return issueToWorkItem(iss), nil
}

func (a *GithubAdapter) UpdateState(ctx context.Context, externalID string, state domain.TrackerState) error {
	mapped := a.cfg.StateMappings[string(state)]
	if strings.TrimSpace(mapped) == "" {
		return nil
	}
	owner, repo, number, err := parseExternalID(externalID)
	if err != nil {
		return err
	}

	return a.patchJSON(ctx, fmt.Sprintf("/repos/%s/%s/issues/%d", owner, repo, number), map[string]any{"state": mapped}, nil)
}

func (a *GithubAdapter) AddComment(ctx context.Context, externalID string, body string) error {
	owner, repo, number, err := parseExternalID(externalID)
	if err != nil {
		return err
	}

	return a.postJSON(ctx, fmt.Sprintf("/repos/%s/%s/issues/%d/comments", owner, repo, number), map[string]any{"body": body}, nil)
}

type WorkItemAdapter struct {
	*GithubAdapter
}

type RepoLifecycleAdapter struct {
	*GithubAdapter
}

func (a *GithubAdapter) WorkItemAdapter() WorkItemAdapter {
	return WorkItemAdapter{GithubAdapter: a}
}

func (a *GithubAdapter) RepoLifecycleAdapter() RepoLifecycleAdapter {
	return RepoLifecycleAdapter{GithubAdapter: a}
}

func (a WorkItemAdapter) OnEvent(ctx context.Context, event domain.SystemEvent) error {
	switch domain.EventType(event.EventType) {
	case domain.EventPlanApproved:
		if err := a.onPlanApproved(ctx, event.Payload); err != nil {
			return err
		}
		ids := extractExternalIDs(event.Payload, "gh:")
		for _, id := range ids {
			if updateErr := a.UpdateState(ctx, id, domain.TrackerStateInProgress); updateErr != nil {
				slog.Warn("github: failed to update tracker state to in_progress", "error", updateErr, "external_id", id)
			}
		}
		return nil
	case domain.EventWorkItemCompleted:
		ids := extractExternalIDs(event.Payload, "gh:")
		for _, id := range ids {
			if updateErr := a.UpdateState(ctx, id, domain.TrackerStateInReview); updateErr != nil {
				slog.Warn("github: failed to update tracker state to in_review", "error", updateErr, "external_id", id)
			}
		}
		return nil
	case domain.EventPRMerged:
		if !a.cfg.PostMergeCloseIssue {
			return nil
		}
		if err := a.onPRMerged(ctx, event.Payload); err != nil {
			slog.Warn("github: post-merge issue close failed", "error", err)
		}
		return nil
	default:
		return nil
	}
}

func (a RepoLifecycleAdapter) OnEvent(ctx context.Context, event domain.SystemEvent) error {
	switch domain.EventType(event.EventType) {
	case domain.EventPlanApproved:
		a.syncPRDescriptionsOnApproval(ctx, event.WorkspaceID, event.Payload)
		return nil
	case domain.EventWorktreeCreated:
		if err := a.onWorktreeCreated(ctx, event.Payload); err != nil {
			slog.Warn("github: worktree created handler failed", "err", err)
		}
		return nil
	case domain.EventWorktreeReused:
		if err := a.onWorktreeReused(ctx, event.Payload); err != nil {
			slog.Warn("github: worktree reused handler failed", "err", err)
		}
		return nil
	case domain.EventWorkItemCompleted:
		if err := a.onWorkItemCompleted(ctx, event.Payload); err != nil {
			slog.Warn("github: work-item completed handler failed", "err", err)
		}
		return nil
	case domain.EventSubPlanPRReady:
		if err := a.onSubPlanPRReady(ctx, event.Payload); err != nil {
			slog.Warn("github: sub-plan PR-ready handler failed", "err", err)
		}
		return nil
	default:
		return nil
	}
}

func (a *GithubAdapter) onWorktreeCreated(ctx context.Context, payload string) error {
	var p worktreePayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return fmt.Errorf("unmarshal worktree payload: %w", err)
	}
	if p.Branch == "" {
		return errors.New("missing branch in worktree payload")
	}
	if err := a.resolveForkBase(ctx, &p.Review); err != nil {
		return err
	}
	baseOwner, baseRepo := p.Review.BaseRepo.Owner, p.Review.BaseRepo.Repo
	headOwner := p.Review.HeadRepo.Owner
	baseBranch := strings.TrimSpace(p.Review.BaseBranch)
	if baseOwner == "" || baseRepo == "" || headOwner == "" {
		return errors.New("worktree payload missing review repository coordinates")
	}
	if baseBranch == "" {
		baseBranch = defaultBranchMain
	}
	pull, err := a.findOpenPullByBranch(ctx, baseOwner, baseRepo, baseBranch, headOwner, p.Branch)
	if err != nil {
		return err
	}
	if pull != nil {
		a.mu.Lock()
		a.tracked[p.Branch] = *pull
		a.mu.Unlock()
		a.recordGithubPR(ctx, p.WorkspaceID, p.WorkItemID, domain.ReviewArtifact{
			Provider:  adapterName,
			Kind:      "PR",
			RepoName:  baseOwner + "/" + baseRepo,
			Ref:       fmt.Sprintf("#%d", pull.Number),
			URL:       strings.TrimSpace(pull.HTMLURL),
			State:     githubArtifactState(*pull),
			Branch:    p.Branch,
			Draft:     pull.Draft,
			UpdatedAt: time.Now(),
		}, baseOwner, baseRepo, pull.Number)

		return nil
	}
	title := p.WorkItemTitle
	if title == "" {
		title = p.Branch
	}
	body := appendTrackerFooter(strings.TrimSpace(p.SubPlan), renderGitHubTrackerRefs(p.TrackerRefs, p.Review.BaseRepo))
	var created githubPull
	if err := a.postJSON(
		ctx,
		fmt.Sprintf("/repos/%s/%s/pulls", baseOwner, baseRepo),
		map[string]any{
			"title": title,
			"head":  headOwner + ":" + p.Branch,
			"base":  baseBranch,
			"draft": true,
			"body":  body,
		},
		&created,
	); err != nil {
		// GitHub rejects PR creation with 422 when the branch has no commits
		// beyond the base yet. This is expected at worktree creation time; the
		// PR will be created (non-draft) lazily when the work item completes
		// and commits are present on the branch.
		if strings.Contains(err.Error(), "No commits between") {
			slog.Debug("github: deferred draft PR creation; branch has no commits yet",
				"branch", p.Branch, "base", baseBranch)
			return nil
		}
		return err
	}
	a.mu.Lock()
	a.tracked[p.Branch] = created
	a.mu.Unlock()
	a.recordGithubPR(ctx, p.WorkspaceID, p.WorkItemID, domain.ReviewArtifact{
		Provider:  adapterName,
		Kind:      "PR",
		RepoName:  baseOwner + "/" + baseRepo,
		Ref:       fmt.Sprintf("#%d", created.Number),
		URL:       strings.TrimSpace(created.HTMLURL),
		State:     githubArtifactState(created),
		Branch:    p.Branch,
		Draft:     created.Draft,
		UpdatedAt: time.Now(),
	}, baseOwner, baseRepo, created.Number)

	a.applyPRReviewers(ctx, baseOwner, baseRepo, created.Number)
	a.applyPRLabels(ctx, baseOwner, baseRepo, created.Number)

	return nil
}

func (a *GithubAdapter) onWorktreeReused(ctx context.Context, payload string) error {
	var p worktreePayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return fmt.Errorf("unmarshal worktree reused payload: %w", err)
	}
	if p.Branch == "" {
		return errors.New("missing branch in worktree reused payload")
	}
	if err := a.resolveForkBase(ctx, &p.Review); err != nil {
		return err
	}
	baseOwner, baseRepo := p.Review.BaseRepo.Owner, p.Review.BaseRepo.Repo
	headOwner := p.Review.HeadRepo.Owner
	baseBranch := strings.TrimSpace(p.Review.BaseBranch)
	if baseOwner == "" || baseRepo == "" || headOwner == "" {
		return errors.New("worktree reused payload missing review repository coordinates")
	}
	if baseBranch == "" {
		baseBranch = defaultBranchMain
	}
	pull, err := a.findOpenPullByBranch(ctx, baseOwner, baseRepo, baseBranch, headOwner, p.Branch)
	if err != nil {
		return err
	}
	if pull == nil {
		slog.Debug("github: no open PR found for reused worktree; skipping description update", "branch", p.Branch)
		return nil
	}

	description := appendTrackerFooter(strings.TrimSpace(p.SubPlan), renderGitHubTrackerRefs(p.TrackerRefs, p.Review.BaseRepo))
	if err := a.patchJSON(ctx, fmt.Sprintf("/repos/%s/%s/pulls/%d", baseOwner, baseRepo, pull.Number), map[string]any{"body": description}, nil); err != nil {
		return fmt.Errorf("update PR description: %w", err)
	}

	return nil
}

func (a *GithubAdapter) onPlanApproved(ctx context.Context, payload string) error {
	commentBody, externalIDs, repoScopes := extractPlanCommentPayload(payload)

	for _, externalID := range externalIDs {
		if !strings.HasPrefix(externalID, "gh:") {
			continue
		}
		if !a.shouldPostComment(externalID, repoScopes) {
			continue
		}

		// Existing: post plan comment
		if strings.TrimSpace(commentBody) != "" {
			if err := a.AddComment(ctx, externalID, commentBody); err != nil {
				slog.Warn("github: post plan comment failed", "id", externalID, "error", err)
			}
		}

		// Assign issue to configured user
		if err := a.assignIssueToCurrentUser(ctx, externalID); err != nil {
			slog.Warn("github: assign issue failed", "id", externalID, "error", err)
		}

		// Apply in-progress label
		if err := a.applyInProgressLabel(ctx, externalID); err != nil {
			slog.Warn("github: apply in-progress label failed", "id", externalID, "error", err)
		}
	}
	return nil
}

// assignIssueToCurrentUser assigns the issue to the configured assignee.
// Best-effort: errors are logged but never returned.
func (a *GithubAdapter) assignIssueToCurrentUser(ctx context.Context, externalID string) error {
	assignee := strings.TrimSpace(a.assignee)
	if assignee == "" || assignee == "me" {
		return nil // not yet resolved; skip
	}
	owner, repo, number, err := parseExternalID(externalID)
	if err != nil {
		return err
	}
	return a.postJSON(ctx,
		fmt.Sprintf("/repos/%s/%s/issues/%d/assignees", owner, repo, number),
		map[string]any{"assignees": []string{assignee}},
		nil,
	)
}

// applyInProgressLabel applies the configured label to the issue.
// Best-effort: errors are logged but never returned.
func (a *GithubAdapter) applyInProgressLabel(ctx context.Context, externalID string) error {
	label := strings.TrimSpace(a.cfg.InProgressStatus)
	if label == "" {
		return nil
	}
	owner, repo, number, err := parseExternalID(externalID)
	if err != nil {
		return err
	}
	return a.postJSON(ctx,
		fmt.Sprintf("/repos/%s/%s/issues/%d/labels", owner, repo, number),
		[]string{label},
		nil,
	)
}

// syncPRDescriptionsOnApproval updates the description of all open GitHub PRs
// linked to the work item when a plan is approved. Only runs on the repo-lifecycle
// adapter instance (which has repos populated).
func (a *GithubAdapter) syncPRDescriptionsOnApproval(ctx context.Context, workspaceID, payload string) {
	var p struct {
		WorkItemID  string `json:"work_item_id"`
		CommentBody string `json:"comment_body"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		slog.Warn("github: unmarshal plan.approved payload for description sync", "error", err)
		return
	}
	if p.WorkItemID == "" || strings.TrimSpace(p.CommentBody) == "" {
		return
	}
	links, err := a.repos.SessionArtifacts.ListByWorkspaceID(ctx, workspaceID)
	if err != nil {
		slog.Warn("github: list artifacts for description sync", "error", err)
		return
	}
	for _, link := range links {
		if link.WorkItemID != p.WorkItemID || link.Provider != "github" {
			continue
		}
		pr, err := a.repos.GithubPRs.Get(ctx, link.ProviderArtifactID)
		if err != nil {
			slog.Warn("github: get pr for description sync", "pr_id", link.ProviderArtifactID, "error", err)
			continue
		}
		if pr.State == "merged" || pr.State == "closed" {
			continue
		}
		if err := a.patchJSON(ctx, fmt.Sprintf("/repos/%s/%s/pulls/%d", pr.Owner, pr.Repo, pr.Number), map[string]any{"body": p.CommentBody}, nil); err != nil {
			slog.Warn("github: update PR description on plan approval", "owner", pr.Owner, "repo", pr.Repo, "number", pr.Number, "error", err)
		}
	}
}

func (a *GithubAdapter) onWorkItemCompleted(ctx context.Context, payload string) error {
	var p completedPayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return fmt.Errorf("unmarshal completed payload: %w", err)
	}
	// Only act on GitHub-hosted repos. If the review context names a different
	// provider explicitly, this event belongs to another adapter.
	if provider := strings.ToLower(strings.TrimSpace(p.Review.BaseRepo.Provider)); provider != "" && provider != "github" {
		return nil
	}
	if p.Branch == "" {
		slog.Warn("github: work_item.completed payload has no branch; skipping pr update")

		return nil
	}
	if err := a.resolveForkBase(ctx, &p.Review); err != nil {
		return err
	}
	artifacts := a.artifactsForCompletion(ctx, p)
	if len(artifacts) == 0 {
		baseOwner, baseRepo := p.Review.BaseRepo.Owner, p.Review.BaseRepo.Repo
		headOwner := p.Review.HeadRepo.Owner
		if baseOwner == "" || baseRepo == "" || headOwner == "" {
			return errors.New("work item completion payload missing review coordinates")
		}
		pull, err := a.findOpenPullByBranch(ctx, baseOwner, baseRepo, strings.TrimSpace(p.Review.BaseBranch), headOwner, p.Branch)
		if err != nil {
			return err
		}
		if pull == nil {
			// No PR was created at worktree time (branch had no commits then).
			// Create a non-draft PR now that implementation is complete.
			title := p.WorkItemTitle
			if title == "" {
				title = p.Branch
			}
			baseBranch := strings.TrimSpace(p.Review.BaseBranch)
			if baseBranch == "" {
				baseBranch = defaultBranchMain
			}
			var created githubPull
			body := appendTrackerFooter(strings.TrimSpace(p.SubPlan), renderGitHubTrackerRefs(p.TrackerRefs, p.Review.BaseRepo))
			if createErr := a.postJSON(ctx, fmt.Sprintf("/repos/%s/%s/pulls", baseOwner, baseRepo), map[string]any{
				"title": title,
				"head":  headOwner + ":" + p.Branch,
				"base":  baseBranch,
				"draft": false,
				"body":  body,
			}, &created); createErr != nil {
				slog.Warn("github: failed to create PR at work item completion", "branch", p.Branch, "err", createErr)
				return nil
			}
			a.mu.Lock()
			a.tracked[p.Branch] = created
			a.mu.Unlock()
			a.recordGithubPR(ctx, p.WorkspaceID, p.WorkItemID, domain.ReviewArtifact{
				Provider:  adapterName,
				Kind:      "PR",
				RepoName:  baseOwner + "/" + baseRepo,
				Ref:       fmt.Sprintf("#%d", created.Number),
				URL:       strings.TrimSpace(created.HTMLURL),
				State:     githubArtifactState(created),
				Branch:    p.Branch,
				Draft:     created.Draft,
				UpdatedAt: time.Now(),
			}, baseOwner, baseRepo, created.Number)
			a.applyPRReviewers(ctx, baseOwner, baseRepo, created.Number)
			a.applyPRLabels(ctx, baseOwner, baseRepo, created.Number)
			return nil
		}
		artifacts = []domain.ReviewArtifact{{
			Provider:  adapterName,
			Kind:      "PR",
			RepoName:  baseOwner + "/" + baseRepo,
			Ref:       fmt.Sprintf("#%d", pull.Number),
			URL:       strings.TrimSpace(pull.HTMLURL),
			State:     githubArtifactState(*pull),
			Branch:    p.Branch,
			Draft:     pull.Draft,
			UpdatedAt: time.Now(),
		}}
	}
	for _, artifact := range artifacts {
		owner, repo, ok := splitGitHubRepoName(artifact.RepoName)
		if !ok {
			continue
		}
		prNumber, err := parseGitHubPullRef(artifact.Ref)
		if err != nil {
			return err
		}
		if err := a.patchJSON(ctx, fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, repo, prNumber), map[string]any{"draft": false}, nil); err != nil {
			return err
		}
		artifact.Draft = false
		artifact.State = "ready"
		artifact.UpdatedAt = time.Now()
		a.recordGithubPR(ctx, p.WorkspaceID, p.WorkItemID, artifact, owner, repo, prNumber)
	}

	return nil
}

func (a *GithubAdapter) onSubPlanPRReady(ctx context.Context, payload string) error {
	var p subPlanPRReadyPayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return fmt.Errorf("unmarshal sub-plan PR-ready payload: %w", err)
	}
	// Only act on GitHub-hosted repos.
	if provider := strings.ToLower(strings.TrimSpace(p.Review.BaseRepo.Provider)); provider != "" && provider != "github" {
		return nil
	}
	if p.Branch == "" {
		slog.Warn("github: sub-plan PR-ready payload has no branch; skipping pr update")
		return nil
	}
	if err := a.resolveForkBase(ctx, &p.Review); err != nil {
		// Fork resolution is a best-effort enrichment. If it fails (e.g. transient
		// network error), log and continue with the coordinates from the payload.
		slog.Warn("github: fork-resolution failed for sub-plan PR-ready, continuing with payload coordinates",
			"work_item_id", p.WorkItemID, "branch", p.Branch, "error", err)
	}
	baseOwner, baseRepo := p.Review.BaseRepo.Owner, p.Review.BaseRepo.Repo
	headOwner := p.Review.HeadRepo.Owner
	if baseOwner == "" || baseRepo == "" || headOwner == "" {
		return errors.New("sub-plan PR-ready payload missing review coordinates")
	}
	baseBranch := strings.TrimSpace(p.Review.BaseBranch)
	if baseBranch == "" {
		baseBranch = defaultBranchMain
	}

	// Check if we have existing artifacts for this work item.
	artifacts := a.artifactsForCompletion(ctx, completedPayload{
		WorkspaceID:   p.WorkspaceID,
		WorkItemID:    p.WorkItemID,
		Branch:        p.Branch,
		WorkItemTitle: p.WorkItemTitle,
		SubPlan:       p.SubPlanContent,
		TrackerRefs:   p.TrackerRefs,
		Review:        p.Review,
	})

	if len(artifacts) > 0 {
		// Update existing artifacts: mark PR as non-draft.
		// Only touch artifacts for this sub-plan's repo to avoid undrafting
		// unrelated PRs in multi-repo work items that share a branch.
		targetRepo := baseOwner + "/" + baseRepo
		found := false
		for _, artifact := range artifacts {
			if artifact.RepoName != targetRepo {
				continue
			}
			found = true
			owner, repo, ok := splitGitHubRepoName(artifact.RepoName)
			if !ok {
				continue
			}
			prNumber, err := parseGitHubPullRef(artifact.Ref)
			if err != nil {
				return err
			}
			if err := a.patchJSON(ctx, fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, repo, prNumber), map[string]any{"draft": false}, nil); err != nil {
				return err
			}
			artifact.Draft = false
			artifact.State = "ready"
			artifact.UpdatedAt = time.Now()
			a.recordGithubPR(ctx, p.WorkspaceID, p.WorkItemID, artifact, owner, repo, prNumber)
		}
		// If we found and updated an artifact for this repo, we're done.
		// Otherwise, fall through to search for existing PRs via API.
		if found {
			return nil
		}
	}

	// No artifacts found via event repo. Try to find existing PR via API.
	pull, err := a.findOpenPullByBranch(ctx, baseOwner, baseRepo, baseBranch, headOwner, p.Branch)
	if err != nil {
		return err
	}
	if pull != nil {
		// PR exists - mark it ready.
		a.mu.Lock()
		a.tracked[p.Branch] = *pull
		a.mu.Unlock()
		if err := a.patchJSON(ctx, fmt.Sprintf("/repos/%s/%s/pulls/%d", baseOwner, baseRepo, pull.Number), map[string]any{"draft": false}, nil); err != nil {
			return err
		}
		// Record state as "ready" since we just undrafted; githubArtifactState
		// would still return "draft" from the pre-patch pull value.
		a.recordGithubPR(ctx, p.WorkspaceID, p.WorkItemID, domain.ReviewArtifact{
			Provider:  adapterName,
			Kind:      "PR",
			RepoName:  baseOwner + "/" + baseRepo,
			Ref:       fmt.Sprintf("#%d", pull.Number),
			URL:       strings.TrimSpace(pull.HTMLURL),
			State:     "ready",
			Branch:    p.Branch,
			Draft:     false,
			UpdatedAt: time.Now(),
		}, baseOwner, baseRepo, pull.Number)
		return nil
	}

	// No PR found - create a non-draft PR.
	title := p.WorkItemTitle
	if title == "" {
		title = p.Branch
	}
	body := appendTrackerFooter(strings.TrimSpace(p.SubPlanContent), renderGitHubTrackerRefs(p.TrackerRefs, p.Review.BaseRepo))
	var created githubPull
	if createErr := a.postJSON(ctx, fmt.Sprintf("/repos/%s/%s/pulls", baseOwner, baseRepo), map[string]any{
		"title": title,
		"head":  headOwner + ":" + p.Branch,
		"base":  baseBranch,
		"draft": false,
		"body":  body,
	}, &created); createErr != nil {
		slog.Warn("github: failed to create PR at sub-plan PR-ready", "branch", p.Branch, "err", createErr)
		return nil
	}
	a.mu.Lock()
	a.tracked[p.Branch] = created
	a.mu.Unlock()
	a.recordGithubPR(ctx, p.WorkspaceID, p.WorkItemID, domain.ReviewArtifact{
		Provider:  adapterName,
		Kind:      "PR",
		RepoName:  baseOwner + "/" + baseRepo,
		Ref:       fmt.Sprintf("#%d", created.Number),
		URL:       strings.TrimSpace(created.HTMLURL),
		State:     githubArtifactState(created),
		Branch:    p.Branch,
		Draft:     created.Draft,
		UpdatedAt: time.Now(),
	}, baseOwner, baseRepo, created.Number)
	a.applyPRReviewers(ctx, baseOwner, baseRepo, created.Number)
	a.applyPRLabels(ctx, baseOwner, baseRepo, created.Number)
	return nil
}

// applyPRReviewers adds configured reviewers to a PR via the GitHub API.
// Failures are logged at Warn and never returned — reviewer assignment is
// a best-effort side effect that must not block the PR lifecycle.
func (a *GithubAdapter) applyPRReviewers(ctx context.Context, owner, repo string, prNumber int) {
	if len(a.cfg.Reviewers) == 0 {
		return
	}
	if err := a.postJSON(ctx,
		fmt.Sprintf("/repos/%s/%s/pulls/%d/requested_reviewers", owner, repo, prNumber),
		map[string]any{"reviewers": a.cfg.Reviewers},
		nil,
	); err != nil {
		slog.Warn("github: failed to add reviewers to PR", "pr", prNumber, "error", err)
	}
}

// applyPRLabels adds configured labels to a PR via the GitHub issues API.
// PRs share the issues namespace in GitHub's REST API.
// Failures are logged at Warn and never returned — label application is
// a best-effort side effect that must not block the PR lifecycle.
func (a *GithubAdapter) applyPRLabels(ctx context.Context, owner, repo string, prNumber int) {
	if len(a.cfg.Labels) == 0 {
		return
	}
	if err := a.postJSON(ctx,
		fmt.Sprintf("/repos/%s/%s/issues/%d/labels", owner, repo, prNumber),
		map[string]any{"labels": a.cfg.Labels},
		nil,
	); err != nil {
		slog.Warn("github: failed to add labels to PR", "pr", prNumber, "error", err)
	}
}

func (a *GithubAdapter) listIssues(ctx context.Context, opts adapter.ListOpts) ([]githubIssue, error) {
	if strings.TrimSpace(opts.View) == "created_by_me" {
		return a.listCreatedIssues(ctx, opts)
	}

	return a.listInboxIssues(ctx, opts)
}

func (a *GithubAdapter) listInboxIssues(ctx context.Context, opts adapter.ListOpts) ([]githubIssue, error) {
	query, err := githubIssueListQuery(opts)
	if err != nil {
		return nil, err
	}
	var issues []githubIssue
	if err := a.getJSON(ctx, "/issues", query, &issues); err != nil {
		return nil, err
	}
	normalizeGitHubIssueRepositories(issues)
	filtered := filterIssues(issues)
	if owner := strings.TrimSpace(opts.Owner); owner != "" {
		repo := strings.TrimSpace(opts.Repo)
		filtered = filterGitHubIssuesByContainer(filtered, owner, repo)
	} else if o, r, ok := splitGitHubRepoName(opts.Repo); ok {
		// Repo supplied without Owner: treat "owner/repo" as a combined path.
		filtered = filterGitHubIssuesByContainer(filtered, o, r)
	}
	if len(opts.Labels) > 0 {
		filtered = filterGitHubIssuesByLabels(filtered, opts.Labels)
	}
	if opts.Limit > 0 && opts.Offset > 0 {
		pageStart := opts.Offset % opts.Limit
		pageStart = min(pageStart, len(filtered))
		filtered = filtered[pageStart:]
	}
	sort.Slice(filtered, func(i, j int) bool {
		if left, right := issueRepoFullName(filtered[i]), issueRepoFullName(filtered[j]); left != right {
			return left < right
		}

		return filtered[i].Number < filtered[j].Number
	})

	return filtered, nil
}

func (a *GithubAdapter) issueReviewArtifacts(ctx context.Context, iss githubIssue) []domain.ReviewArtifact {
	owner, repo := issueOwnerRepo(iss)
	if owner == "" || repo == "" || iss.Number <= 0 {
		return nil
	}
	events, err := a.fetchIssueTimeline(ctx, owner, repo, iss.Number)
	if err != nil {
		slog.Warn("github: fetch issue timeline failed", "owner", owner, "repo", repo, "issue", iss.Number, "error", err)
		return nil
	}

	return githubReviewArtifactsFromTimeline(events)
}

func (a *GithubAdapter) fetchIssueTimeline(ctx context.Context, owner, repo string, issueNumber int64) ([]githubTimelineEvent, error) {
	query := url.Values{}
	query.Set("per_page", "100")
	var events []githubTimelineEvent
	endpoint := fmt.Sprintf("/repos/%s/%s/issues/%d/timeline", url.PathEscape(owner), url.PathEscape(repo), issueNumber)
	for page := 1; ; page++ {
		var pageEvents []githubTimelineEvent
		header, err := a.getJSONWithHeaders(ctx, endpoint, query, &pageEvents)
		if err != nil {
			return nil, err
		}
		events = append(events, pageEvents...)
		if !githubLinkHasRelNext(header.Get("Link")) {
			break
		}
		query.Set("page", strconv.Itoa(page+1))
	}

	return events, nil
}

func githubReviewArtifactsFromTimeline(events []githubTimelineEvent) []domain.ReviewArtifact {
	artifacts := make([]domain.ReviewArtifact, 0)
	seen := make(map[string]struct{})
	for _, event := range events {
		if event.Event != "cross-referenced" || event.Source.Issue.PullReq == nil {
			continue
		}
		issue := event.Source.Issue
		normalizeGitHubIssueRepository(&issue, "", "")
		owner, repo := issueOwnerRepo(issue)
		if owner == "" || repo == "" || issue.Number <= 0 {
			continue
		}
		key := formatIssueSelectionID(owner, repo, issue.Number)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		state := strings.TrimSpace(issue.State)
		if issue.PullReq.MergedAt != nil {
			state = "merged"
		}
		rawURL := strings.TrimSpace(issue.HTMLURL)
		if rawURL == "" {
			rawURL = strings.TrimSpace(issue.PullReq.HTMLURL)
		}
		artifacts = append(artifacts, domain.ReviewArtifact{
			Provider:  adapterName,
			Kind:      "PR",
			RepoName:  owner + "/" + repo,
			Ref:       fmt.Sprintf("#%d", issue.Number),
			URL:       rawURL,
			State:     state,
			UpdatedAt: derefTime(issue.UpdatedAt),
		})
	}

	return artifacts
}

func (a *GithubAdapter) listCreatedIssues(ctx context.Context, opts adapter.ListOpts) ([]githubIssue, error) {
	viewer, err := a.viewerLogin(ctx)
	if err != nil {
		return nil, err
	}
	query, err := githubCreatedIssueSearchQuery(viewer, opts)
	if err != nil {
		return nil, err
	}
	var result githubIssueSearchResult
	if err := a.getJSON(ctx, "/search/issues", query, &result); err != nil {
		return nil, err
	}
	issues := filterIssues(result.Items)
	normalizeGitHubIssueRepositories(issues)
	if owner := strings.TrimSpace(opts.Owner); owner != "" {
		repo := strings.TrimSpace(opts.Repo)
		issues = filterGitHubIssuesByContainer(issues, owner, repo)
	} else if o, r, ok := splitGitHubRepoName(opts.Repo); ok {
		// Repo supplied without Owner: treat "owner/repo" as a combined path.
		issues = filterGitHubIssuesByContainer(issues, o, r)
	}
	if len(opts.Labels) > 0 {
		issues = filterGitHubIssuesByLabels(issues, opts.Labels)
	}
	sort.Slice(issues, func(i, j int) bool {
		if left, right := issueRepoFullName(issues[i]), issueRepoFullName(issues[j]); left != right {
			return left < right
		}

		return issues[i].Number < issues[j].Number
	})

	return issues, nil
}

func githubIssueListQuery(opts adapter.ListOpts) (url.Values, error) {
	query := url.Values{}
	filter, err := githubIssueFilterValue(opts.View)
	if err != nil {
		return nil, err
	}
	state, err := githubIssueStateValue(opts.State)
	if err != nil {
		return nil, err
	}
	query.Set("filter", filter)
	query.Set("state", state)
	if opts.Search != "" {
		query.Set("q", opts.Search)
	}
	if opts.Limit > 0 {
		query.Set("per_page", strconv.Itoa(opts.Limit))
	}
	if opts.Limit > 0 && opts.Offset > 0 {
		query.Set("page", strconv.Itoa((opts.Offset/opts.Limit)+1))
	}

	return query, nil
}

func githubCreatedIssueSearchQuery(viewer string, opts adapter.ListOpts) (url.Values, error) {
	state, err := githubIssueStateValue(opts.State)
	if err != nil {
		return nil, err
	}
	terms := []string{"is:issue", "author:" + strings.TrimSpace(viewer)}
	if state != filterAll {
		terms = append(terms, "state:"+state)
	}
	if owner := strings.TrimSpace(opts.Owner); owner != "" {
		if repo := strings.TrimSpace(opts.Repo); repo != "" {
			terms = append(terms, fmt.Sprintf("repo:%s/%s", owner, repo))
		}
	} else if _, _, ok := splitGitHubRepoName(opts.Repo); ok {
		// Repo supplied without Owner: the user typed "owner/repo" in the Repo
		// field, which GitHub's search API accepts directly as a repo: qualifier.
		terms = append(terms, "repo:"+strings.TrimSpace(opts.Repo))
	}
	for _, label := range opts.Labels {
		trimmed := strings.TrimSpace(label)
		if trimmed == "" {
			continue
		}
		terms = append(terms, fmt.Sprintf("label:%q", trimmed))
	}
	if search := strings.TrimSpace(opts.Search); search != "" {
		terms = append(terms, search)
	}
	query := url.Values{}
	query.Set("q", strings.Join(terms, " "))
	if opts.Limit > 0 {
		query.Set("per_page", strconv.Itoa(opts.Limit))
	}
	if opts.Limit > 0 && opts.Offset > 0 {
		query.Set("page", strconv.Itoa((opts.Offset/opts.Limit)+1))
	}

	return query, nil
}

func githubIssueFilterValue(view string) (string, error) {
	switch strings.TrimSpace(view) {
	case "", "assigned_to_me":
		return "assigned", nil
	case "mentioned":
		return "mentioned", nil
	case "subscribed":
		return "subscribed", nil
	case filterAll:
		return filterAll, nil
	default:
		return "", fmt.Errorf("github issue view %q not supported", view)
	}
}

func githubIssueStateValue(state string) (string, error) {
	switch strings.TrimSpace(state) {
	case "", "open":
		return "open", nil
	case stateClosed:
		return stateClosed, nil
	case filterAll:
		return filterAll, nil
	default:
		return "", fmt.Errorf("github issue state %q not supported", state)
	}
}

func filterGitHubIssuesByContainer(issues []githubIssue, owner, repo string) []githubIssue {
	filtered := make([]githubIssue, 0, len(issues))
	for _, iss := range issues {
		issueOwner, issueRepo := issueOwnerRepo(iss)
		if issueOwner != owner {
			continue
		}
		if repo != "" && issueRepo != repo {
			continue
		}
		filtered = append(filtered, iss)
	}

	return filtered
}

func filterGitHubIssuesByLabels(issues []githubIssue, labels []string) []githubIssue {
	if len(labels) == 0 {
		return issues
	}
	want := make(map[string]struct{}, len(labels))
	for _, label := range labels {
		trimmed := strings.TrimSpace(label)
		if trimmed == "" {
			continue
		}
		want[trimmed] = struct{}{}
	}
	filtered := make([]githubIssue, 0, len(issues))
	for _, iss := range issues {
		have := make(map[string]struct{}, len(iss.Labels))
		for _, label := range iss.Labels {
			have[label.Name] = struct{}{}
		}
		matched := true
		for label := range want {
			if _, ok := have[label]; !ok {
				matched = false

				break
			}
		}
		if matched {
			filtered = append(filtered, iss)
		}
	}

	return filtered
}

func (a *GithubAdapter) resolveForkBase(ctx context.Context, review *domain.ReviewRef) error {
	if review == nil {
		return nil
	}
	baseOwner := strings.TrimSpace(review.BaseRepo.Owner)
	baseRepo := strings.TrimSpace(review.BaseRepo.Repo)
	headOwner := strings.TrimSpace(review.HeadRepo.Owner)
	headRepo := strings.TrimSpace(review.HeadRepo.Repo)
	if baseOwner == "" || baseRepo == "" || headOwner == "" || headRepo == "" {
		return nil
	}
	if !strings.EqualFold(baseOwner, headOwner) || !strings.EqualFold(baseRepo, headRepo) {
		return nil
	}
	if provider := strings.TrimSpace(review.BaseRepo.Provider); provider != "" && !strings.EqualFold(provider, adapterName) {
		return nil
	}

	repo, err := a.fetchRepository(ctx, headOwner, headRepo)
	if err != nil {
		return fmt.Errorf("resolve github fork parent for %s/%s: %w", headOwner, headRepo, err)
	}
	if !repo.Fork || repo.Parent == nil {
		return nil
	}
	parentOwner, parentRepo := githubRepositoryOwnerRepo(*repo.Parent)
	if parentOwner == "" || parentRepo == "" {
		return nil
	}

	review.BaseRepo.Provider = adapterName
	review.BaseRepo.Owner = parentOwner
	review.BaseRepo.Repo = parentRepo
	review.BaseRepo.Host = review.HeadRepo.Host
	review.BaseRepo.URL = strings.TrimSpace(repo.Parent.HTMLURL)
	if branch := strings.TrimSpace(repo.Parent.DefaultBranch); branch != "" {
		review.BaseBranch = branch
	}
	return nil
}

func (a *GithubAdapter) fetchRepository(ctx context.Context, owner, repo string) (githubRepository, error) {
	var out githubRepository
	if err := a.getJSON(ctx, fmt.Sprintf("/repos/%s/%s", owner, repo), nil, &out); err != nil {
		return githubRepository{}, err
	}
	return out, nil
}

func githubRepositoryOwnerRepo(repo githubRepository) (string, string) {
	if repo.Owner != nil && repo.Owner.Login != "" && repo.Name != "" {
		return repo.Owner.Login, repo.Name
	}
	if repo.FullName == "" {
		return "", ""
	}
	parts := strings.SplitN(repo.FullName, "/", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

func (a *GithubAdapter) listMilestones(ctx context.Context, opts adapter.ListOpts) ([]githubMilestone, error) {
	owner := strings.TrimSpace(opts.Owner)
	repo := strings.TrimSpace(opts.Repo)
	if owner == "" || repo == "" {
		return nil, errors.New("github milestones browse requires owner and repo filters")
	}
	query := url.Values{}
	if opts.Limit > 0 {
		query.Set("per_page", strconv.Itoa(opts.Limit))
	}
	var milestones []githubMilestone
	if err := a.getJSON(ctx, fmt.Sprintf("/repos/%s/%s/milestones", owner, repo), query, &milestones); err != nil {
		return nil, err
	}

	return milestones, nil
}

func (a *GithubAdapter) fetchIssue(ctx context.Context, owner, repo string, number int64) (githubIssue, error) {
	var iss githubIssue
	if err := a.getJSON(ctx, fmt.Sprintf("/repos/%s/%s/issues/%d", owner, repo, number), nil, &iss); err != nil {
		return githubIssue{}, err
	}
	if iss.PullReq != nil {
		return githubIssue{}, fmt.Errorf("github issue %s/%s#%d is a pull request", owner, repo, number)
	}
	normalizeGitHubIssueRepository(&iss, owner, repo)

	return iss, nil
}

func (a *GithubAdapter) fetchMilestone(ctx context.Context, owner, repo string, number int64) (githubMilestone, error) {
	var ms githubMilestone
	if err := a.getJSON(ctx, fmt.Sprintf("/repos/%s/%s/milestones/%d", owner, repo, number), nil, &ms); err != nil {
		return githubMilestone{}, err
	}

	return ms, nil
}

func (a *GithubAdapter) fetchAssignedOpenIssues(ctx context.Context) ([]githubIssue, error) {
	query := url.Values{"state": []string{"open"}}
	if strings.TrimSpace(a.assignee) != "" {
		query.Set("assignee", a.assignee)
	}
	var issues []githubIssue
	if err := a.getJSON(ctx, "/issues", query, &issues); err != nil {
		return nil, err
	}
	filtered := filterIssues(issues)
	sort.Slice(filtered, func(i, j int) bool {
		if left, right := issueRepoFullName(filtered[i]), issueRepoFullName(filtered[j]); left != right {
			return left < right
		}

		return filtered[i].Number < filtered[j].Number
	})

	return filtered, nil
}

func (a *GithubAdapter) artifactsForCompletion(ctx context.Context, p completedPayload) []domain.ReviewArtifact {
	if a.repos.Events == nil || strings.TrimSpace(p.WorkspaceID) == "" || strings.TrimSpace(p.WorkItemID) == "" {
		return nil
	}
	events, err := a.repos.Events.ListByWorkspaceID(ctx, p.WorkspaceID, 0)
	if err != nil {
		slog.Warn("github: list review artifact events for completion failed", "workspace_id", p.WorkspaceID, "error", err)
		return nil
	}
	latest := make(map[string]domain.ReviewArtifact)
	for _, event := range events {
		if domain.EventType(event.EventType) != domain.EventReviewArtifactRecorded {
			continue
		}
		var payload domain.ReviewArtifactEventPayload
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			slog.Warn("github: unmarshal review artifact event for completion failed", "event_id", event.ID, "error", err)
			continue
		}
		artifact := payload.Artifact
		if repoName := githubRepoNameFromPullURL(artifact.URL); repoName != "" {
			artifact.RepoName = repoName
		}
		if payload.WorkItemID != p.WorkItemID ||
			artifact.Provider != adapterName ||
			strings.TrimSpace(artifact.Branch) != strings.TrimSpace(p.Branch) ||
			strings.TrimSpace(artifact.Ref) == "" ||
			strings.TrimSpace(artifact.RepoName) == "" {
			continue
		}
		key := artifact.RepoName + "|" + artifact.Branch
		if current, ok := latest[key]; ok && !artifact.UpdatedAt.After(current.UpdatedAt) {
			continue
		}
		latest[key] = artifact
	}
	artifacts := make([]domain.ReviewArtifact, 0, len(latest))
	for _, artifact := range latest {
		artifacts = append(artifacts, artifact)
	}
	sort.SliceStable(artifacts, func(i, j int) bool {
		if artifacts[i].RepoName != artifacts[j].RepoName {
			return artifacts[i].RepoName < artifacts[j].RepoName
		}

		return artifacts[i].Ref < artifacts[j].Ref
	})

	return artifacts
}

func githubRepoNameFromPullURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Path == "" {
		return ""
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 4 || parts[0] == "" || parts[1] == "" || parts[2] != "pull" {
		return ""
	}

	return parts[0] + "/" + parts[1]
}

func splitGitHubRepoName(raw string) (string, string, bool) {
	parts := strings.Split(strings.TrimSpace(raw), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}

	return parts[0], parts[1], true
}

func parseGitHubPullRef(ref string) (int, error) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(ref), "#")
	if trimmed == "" {
		return 0, errors.New("github pull ref is required")
	}
	value, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, fmt.Errorf("parse github pull ref %q: %w", ref, err)
	}

	return value, nil
}

func (a *GithubAdapter) recordGithubPR(ctx context.Context, workspaceID, workItemID string, artifact domain.ReviewArtifact, owner, repo string, number int) {
	if err := adapter.PersistGithubPR(ctx, a.repos, workspaceID, workItemID, artifact, owner, repo, number); err != nil {
		slog.Warn("github: persist review artifact failed", "repo", artifact.RepoName, "branch", artifact.Branch, "error", err)
	}
}

func githubArtifactState(pull githubPull) string {
	if pull.Draft {
		return "draft"
	}

	return "ready"
}

func githubPRState(pull githubPull) string {
	if pull.MergedAt != nil {
		return "merged"
	}
	if pull.State == stateClosed {
		return stateClosed
	}
	if pull.Draft {
		return "draft"
	}
	return "ready"
}

func (a *GithubAdapter) findOpenPullByBranch(ctx context.Context, baseOwner, baseRepo, baseBranch, headOwner, branch string) (*githubPull, error) {
	query := url.Values{"state": []string{"open"}, "head": []string{headOwner + ":" + branch}}
	if trimmedBase := strings.TrimSpace(baseBranch); trimmedBase != "" {
		query.Set("base", trimmedBase)
	}
	var pulls []githubPull
	if err := a.getJSON(ctx, fmt.Sprintf("/repos/%s/%s/pulls", baseOwner, baseRepo), query, &pulls); err != nil {
		return nil, err
	}
	if len(pulls) == 0 {
		return nil, nil
	}

	return &pulls[0], nil
}

func githubLinkHasRelNext(linkHeader string) bool {
	for _, link := range strings.Split(linkHeader, ",") {
		for _, part := range strings.Split(link, ";") {
			if strings.EqualFold(strings.TrimSpace(part), `rel="next"`) {
				return true
			}
		}
	}

	return false
}

func (a *GithubAdapter) getJSON(ctx context.Context, endpoint string, query url.Values, dst any) error {
	_, err := a.getJSONWithHeaders(ctx, endpoint, query, dst)

	return err
}

func (a *GithubAdapter) getJSONWithHeaders(ctx context.Context, endpoint string, query url.Values, dst any) (http.Header, error) {
	return a.doJSONWithHeaders(ctx, http.MethodGet, endpoint, query, nil, dst)
}

func (a *GithubAdapter) postJSON(ctx context.Context, endpoint string, body any, dst any) error {
	return a.doJSON(ctx, http.MethodPost, endpoint, nil, body, dst)
}

func (a *GithubAdapter) patchJSON(ctx context.Context, endpoint string, body any, dst any) error {
	return a.doJSON(ctx, http.MethodPatch, endpoint, nil, body, dst)
}

func (a *GithubAdapter) doJSON(ctx context.Context, method, endpoint string, query url.Values, body any, dst any) error {
	_, err := a.doJSONWithHeaders(ctx, method, endpoint, query, body, dst)

	return err
}

func (a *GithubAdapter) doJSONWithHeaders(ctx context.Context, method, endpoint string, query url.Values, body any, dst any) (http.Header, error) {
	fullURL, err := url.Parse(a.baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base url: %w", err)
	}
	fullURL.Path = path.Join(fullURL.Path, endpoint)
	fullURL.RawQuery = query.Encode()
	var bodyReader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, fullURL.String(), bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	limitedBody := io.LimitReader(resp.Body, maxResponseBodyBytes)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(limitedBody)
		body := strings.TrimSpace(string(data))
		switch {
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			return nil, adapter.NewPermissionError(adapterName, resp.StatusCode, body)
		case resp.StatusCode == http.StatusNotFound:
			return nil, adapter.NewNotFoundError(adapterName, adapter.ResourceGeneric, body)
		default:
			return nil, fmt.Errorf("github api status %d: %s", resp.StatusCode, body)
		}
	}
	if dst == nil {
		return resp.Header.Clone(), nil
	}
	if err := json.NewDecoder(limitedBody).Decode(dst); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return resp.Header.Clone(), nil
}

func filterIssues(issues []githubIssue) []githubIssue {
	filtered := make([]githubIssue, 0, len(issues))
	for _, iss := range issues {
		if iss.PullReq == nil {
			filtered = append(filtered, iss)
		}
	}

	return filtered
}

func issueLabels(iss githubIssue) []string {
	labels := make([]string, 0, len(iss.Labels))
	for _, label := range iss.Labels {
		labels = append(labels, label.Name)
	}
	sort.Strings(labels)

	return labels
}

func issueToWorkItem(iss githubIssue) domain.Session {
	owner, repo := issueOwnerRepo(iss)

	return domain.Session{
		ID:            domain.NewID(),
		ExternalID:    formatExternalID(owner, repo, iss.Number),
		Source:        adapterName,
		SourceScope:   domain.ScopeIssues,
		SourceItemIDs: []string{issueSelectionID(iss)},
		Title:         iss.Title,
		Description:   iss.Body,
		Labels:        issueLabels(iss),
		State:         domain.SessionIngested,
		Metadata: map[string]any{
			"url":           iss.HTMLURL,
			"tracker_refs":  githubTrackerRefs([]githubIssue{iss}),
			"tracker_state": strings.TrimSpace(iss.State),
		},
		CreatedAt: derefTime(iss.CreatedAt),
		UpdatedAt: derefTime(iss.UpdatedAt),
	}
}

func aggregateIssues(issues []githubIssue) domain.Session {
	labels := map[string]struct{}{}
	parts := make([]string, 0, len(issues))
	itemIDs := make([]string, 0, len(issues))
	for _, iss := range issues {
		owner, repo := issueOwnerRepo(iss)
		itemIDs = append(itemIDs, issueSelectionID(iss))
		parts = append(parts, fmt.Sprintf("[%s/%s] #%d %s\n%s", owner, repo, iss.Number, iss.Title, strings.TrimSpace(iss.Body)))
		for _, label := range issueLabels(iss) {
			labels[label] = struct{}{}
		}
	}
	merged := make([]string, 0, len(labels))
	for label := range labels {
		merged = append(merged, label)
	}
	sort.Strings(merged)
	title := issues[0].Title
	if len(issues) > 1 {
		title = fmt.Sprintf("%s (+%d more)", issues[0].Title, len(issues)-1)
	}
	owner, repo := issueOwnerRepo(issues[0])

	return domain.Session{
		ID:            domain.NewID(),
		ExternalID:    formatExternalID(owner, repo, issues[0].Number),
		Source:        adapterName,
		SourceScope:   domain.ScopeIssues,
		SourceItemIDs: itemIDs,
		Title:         title,
		Description:   strings.Join(parts, "\n\n---\n\n"),
		Labels:        merged,
		State:         domain.SessionIngested,
		Metadata: map[string]any{
			"tracker_refs":     githubTrackerRefs(issues),
			"source_summaries": githubIssueSourceSummaries(issues),
			"tracker_state":    strings.TrimSpace(issues[0].State),
		},
		CreatedAt: domain.Now(),
		UpdatedAt: domain.Now(),
	}
}

func githubTrackerRefs(issues []githubIssue) []domain.TrackerReference {
	refs := make([]domain.TrackerReference, 0, len(issues))
	seen := make(map[string]struct{}, len(issues))
	for _, iss := range issues {
		owner, repo := issueOwnerRepo(iss)
		if iss.Number <= 0 || owner == "" || repo == "" {
			continue
		}
		key := fmt.Sprintf("%s/%s#%d", owner, repo, iss.Number)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		refs = append(refs, domain.TrackerReference{
			Provider: adapterName,
			Kind:     "issue",
			ID:       strconv.FormatInt(iss.Number, 10),
			URL:      iss.HTMLURL,
			Owner:    owner,
			Repo:     repo,
			Number:   iss.Number,
		})
	}

	return refs
}

func normalizeGitHubIssueRepositories(issues []githubIssue) {
	for i := range issues {
		normalizeGitHubIssueRepository(&issues[i], "", "")
	}
}

func normalizeGitHubIssueRepository(iss *githubIssue, fallbackOwner, fallbackRepo string) {
	if iss == nil || iss.Repository != nil {
		return
	}
	owner, repo := githubIssueRepositoryIdentity(*iss)
	if owner == "" || repo == "" {
		owner, repo = fallbackOwner, fallbackRepo
	}
	if owner == "" || repo == "" {
		return
	}
	iss.Repository = &githubRepository{FullName: owner + "/" + repo, Owner: &githubOwner{Login: owner}, Name: repo}
}

func githubIssueRepositoryIdentity(iss githubIssue) (string, string) {
	if owner, repo := parseGitHubRepositoryURL(iss.RepositoryURL); owner != "" && repo != "" {
		return owner, repo
	}

	return parseGitHubRepositoryURL(iss.HTMLURL)
}

func parseGitHubRepositoryURL(rawURL string) (string, string) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", ""
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	switch {
	case len(parts) >= 3 && parts[0] == "repos":
		return parts[1], parts[2]
	case len(parts) >= 2:
		return parts[0], parts[1]
	default:
		return "", ""
	}
}

func issueSelectionID(iss githubIssue) string {
	owner, repo := issueOwnerRepo(iss)

	return formatIssueSelectionID(owner, repo, iss.Number)
}

func issueListTitle(iss githubIssue) string {
	owner, repo := issueOwnerRepo(iss)

	return fmt.Sprintf("[%s/%s] #%d: %s", owner, repo, iss.Number, iss.Title)
}

func issueParentRef(iss githubIssue) *adapter.ParentRef {
	owner, repo := issueOwnerRepo(iss)
	if owner == "" || repo == "" {
		return nil
	}

	return &adapter.ParentRef{ID: owner + "/" + repo, Type: "repository", Title: owner + "/" + repo}
}

func issueOwnerRepo(iss githubIssue) (string, string) {
	if iss.Repository == nil {
		return "", ""
	}
	if iss.Repository.Owner != nil && iss.Repository.Owner.Login != "" && iss.Repository.Name != "" {
		return iss.Repository.Owner.Login, iss.Repository.Name
	}
	if iss.Repository.FullName == "" {
		return "", ""
	}
	parts := strings.SplitN(iss.Repository.FullName, "/", 2)
	if len(parts) != 2 {
		return "", ""
	}

	return parts[0], parts[1]
}

func issueRepoFullName(iss githubIssue) string {
	owner, repo := issueOwnerRepo(iss)
	if owner == "" || repo == "" {
		return ""
	}

	return owner + "/" + repo
}

func formatIssueSelectionID(owner, repo string, number int64) string {
	return fmt.Sprintf("%s/%s#%d", owner, repo, number)
}

func parseIssueSelectionID(defaultOwner, defaultRepo, itemID string) (string, string, int64, error) {
	if strings.Contains(itemID, "/") && strings.Contains(itemID, "#") {
		parts := strings.SplitN(itemID, "#", 2)
		if len(parts) != 2 || parts[0] == "" {
			return "", "", 0, errors.New("invalid repo-scoped issue id")
		}
		repoParts := strings.SplitN(parts[0], "/", 2)
		if len(repoParts) != 2 || repoParts[0] == "" || repoParts[1] == "" {
			return "", "", 0, errors.New("invalid repo-scoped issue id")
		}
		number, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return "", "", 0, err
		}

		return repoParts[0], repoParts[1], number, nil
	}
	number, err := strconv.ParseInt(itemID, 10, 64)
	if err != nil {
		return "", "", 0, err
	}

	return defaultOwner, defaultRepo, number, nil
}

func formatExternalID(owner, repo string, number int64) string {
	return fmt.Sprintf("gh:issue:%s/%s#%d", owner, repo, number)
}

func parseExternalID(externalID string) (string, string, int64, error) {
	trimmed := strings.TrimSpace(externalID)
	// Accept both "gh:issue:owner/repo#number" and "gh:owner/repo#number".
	prefix := "gh:issue:"
	if !strings.HasPrefix(trimmed, prefix) {
		prefix = "gh:"
		if !strings.HasPrefix(trimmed, prefix) {
			return "", "", 0, fmt.Errorf("invalid github external id %q", externalID)
		}
	}
	raw := strings.TrimPrefix(trimmed, prefix)
	parts := strings.SplitN(raw, "#", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
		return "", "", 0, fmt.Errorf("invalid github external id %q", externalID)
	}
	owner, repo, _, err := parseIssueSelectionID("", "", parts[0]+"#0")
	if err != nil {
		return "", "", 0, fmt.Errorf("invalid github external id %q: %w", externalID, err)
	}
	number, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", "", 0, fmt.Errorf("parse issue number: %w", err)
	}

	return owner, repo, number, nil
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
	for _, id := range parsed.ExternalIDs {
		if strings.HasPrefix(id, prefix) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 && parsed.ExternalID != "" && strings.HasPrefix(parsed.ExternalID, prefix) {
		ids = append(ids, parsed.ExternalID)
	}
	return ids
}

func extractPlanCommentPayload(payload string) (string, []string, map[string]string) {
	var parsed struct {
		CommentBody       string            `json:"comment_body"`
		ExternalIDs       []string          `json:"external_ids"`
		RepoCommentScopes map[string]string `json:"repo_comment_scopes"`
	}
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		return "", nil, nil
	}

	return parsed.CommentBody, parsed.ExternalIDs, parsed.RepoCommentScopes
}

// shouldPostComment returns true if a plan comment should be posted for the given external ID.
func (a *GithubAdapter) shouldPostComment(externalID string, repoScopes map[string]string) bool {
	repo := extractRepoFromExternalID(externalID)
	if repo == "" {
		return true
	}
	scopeStr, ok := repoScopes[repo]
	if !ok {
		return true // Default to posting
	}
	switch config.IssueActionScope(scopeStr) {
	case config.IssueActionScopeNone:
		return false
	case config.IssueActionScopeMine:
		return a.isOwnRepo(repo)
	default:
		return true
	}
}

// extractRepoFromExternalID extracts the "owner/repo" portion from a GitHub external ID.
func extractRepoFromExternalID(externalID string) string {
	trimmed := strings.TrimSpace(externalID)
	if !strings.HasPrefix(trimmed, "gh:issue:") {
		return ""
	}
	raw := strings.TrimPrefix(trimmed, "gh:issue:")
	parts := strings.SplitN(raw, "#", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
		return ""
	}
	return strings.TrimSpace(parts[0])
}

// isOwnRepo returns true if the repository is owned by the authenticated user.
func (a *GithubAdapter) isOwnRepo(repo string) bool {
	a.mu.RLock()
	viewer := a.viewer
	a.mu.RUnlock()
	if idx := strings.IndexByte(repo, '/'); idx > 0 {
		return repo[:idx] == viewer
	}
	return false
}

func derefTime(t *time.Time) time.Time {
	if t == nil {
		return domain.Now()
	}

	return t.UTC()
}

func contains(values []string, want string) bool {
	return slices.Contains(values, want)
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

func renderGitHubTrackerRefs(refs []domain.TrackerReference, baseRepo domain.RepoRef) string {
	parts := make([]string, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		rendered := renderGitHubTrackerRef(ref, baseRepo)
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

func renderGitHubTrackerRef(ref domain.TrackerReference, baseRepo domain.RepoRef) string {
	switch ref.Provider {
	case adapterName:
		if ref.Kind != "issue" || ref.Number <= 0 {
			return ""
		}
		refOwner := ref.Owner
		if refOwner == "" {
			refOwner = ref.Repository.Owner
		}
		refRepo := ref.Repo
		if refRepo == "" {
			refRepo = ref.Repository.Repo
		}
		if refOwner == baseRepo.Owner && refRepo == baseRepo.Repo {
			return fmt.Sprintf("#%d", ref.Number)
		}
		if refOwner != "" && refRepo != "" {
			return fmt.Sprintf("%s/%s#%d", refOwner, refRepo, ref.Number)
		}

		return ""
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

func (a *GithubAdapter) refreshPRs(ctx context.Context, workspaceID string) {
	if a.repos.GithubPRs == nil {
		return
	}
	prs, err := a.repos.GithubPRs.ListNonTerminal(ctx, workspaceID)
	if err != nil {
		slog.Warn("github: refresh prs list failed", "error", err)
		return
	}
	for _, pr := range prs {
		var freshPull githubPull
		if err := a.getJSON(ctx, fmt.Sprintf("/repos/%s/%s/pulls/%d", pr.Owner, pr.Repo, pr.Number), nil, &freshPull); err != nil {
			slog.Warn("github: refresh pr failed", "pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number), "error", err)
			continue
		}
		updated := domain.GithubPullRequest{
			ID:         pr.ID,
			Owner:      pr.Owner,
			Repo:       pr.Repo,
			Number:     pr.Number,
			State:      githubPRState(freshPull),
			Draft:      freshPull.Draft,
			HeadBranch: pr.HeadBranch,
			HTMLURL:    freshPull.HTMLURL,
			MergedAt:   freshPull.MergedAt,
			CreatedAt:  pr.CreatedAt,
			UpdatedAt:  time.Now(),
		}
		if err := a.repos.GithubPRs.Upsert(ctx, updated); err != nil {
			slog.Warn("github: refresh pr upsert failed", "error", err)
		}
		// Fetch and upsert PR reviews.
		if a.repos.GithubPRReviews != nil {
			state := githubPRState(freshPull)
			if state == "merged" || state == "closed" {
				// Terminal state: clean up stale review rows.
				if err := a.repos.GithubPRReviews.DeleteByPRID(ctx, pr.ID); err != nil {
					slog.Warn("github: delete pr reviews on terminal state failed", "pr", pr.ID, "error", err)
				}
			} else {
				var apiReviews []githubReview
				if err := a.getJSON(ctx, fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews", pr.Owner, pr.Repo, pr.Number), nil, &apiReviews); err != nil {
					slog.Warn("github: refresh pr reviews failed", "pr", pr.Number, "error", err)
				} else {
					a.upsertPRReviews(ctx, pr.ID, apiReviews)
				}
			}
		}
		// Fetch and upsert PR check runs.
		if a.repos.GithubPRChecks != nil && pr.HeadBranch != "" {
			state := githubPRState(freshPull)
			if state == "merged" || state == "closed" {
				if err := a.repos.GithubPRChecks.DeleteByPRID(ctx, pr.ID); err != nil {
					slog.Warn("github: delete pr checks on terminal state failed", "pr", pr.ID, "error", err)
				}
			} else {
				var checkResp struct {
					CheckRuns []githubCheckRun `json:"check_runs"`
				}
				if err := a.getJSON(ctx, fmt.Sprintf("/repos/%s/%s/commits/%s/check-runs", pr.Owner, pr.Repo, pr.HeadBranch), nil, &checkResp); err != nil {
					slog.Warn("github: refresh pr checks failed", "pr", pr.Number, "error", err)
				} else {
					a.upsertPRChecks(ctx, pr.ID, checkResp.CheckRuns)
				}
			}
		}

		// Detect merge transition: PR just became merged.
		if githubPRState(freshPull) == "merged" && pr.State != "merged" {
			a.checkAllMerged(ctx, workspaceID, pr.ID)
		}
	}
}

// upsertPRReviews deduplicates GitHub reviews by reviewer (keeping latest non-PENDING per user)
// and upserts each into the database.
func (a *GithubAdapter) upsertPRReviews(ctx context.Context, prID string, apiReviews []githubReview) {
	// Deduplicate: keep the latest non-PENDING review per reviewer.
	// The API returns reviews chronologically, so iterate forward and overwrite.
	latest := make(map[string]githubReview)
	for _, r := range apiReviews {
		if strings.EqualFold(r.State, "PENDING") {
			continue
		}
		if r.User.Login == "" {
			continue
		}
		latest[r.User.Login] = r
	}

	now := time.Now()
	for login, r := range latest {
		submittedAt := now
		if r.SubmittedAt != nil {
			submittedAt = *r.SubmittedAt
		}
		review := domain.GithubPRReview{
			ID:            domain.NewID(),
			PRID:          prID,
			ReviewerLogin: login,
			State:         strings.ToLower(r.State),
			SubmittedAt:   submittedAt,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		if err := a.repos.GithubPRReviews.Upsert(ctx, review); err != nil {
			slog.Warn("github: upsert pr review failed", "pr", prID, "reviewer", login, "error", err)
		}
	}
}

// upsertPRChecks stores the latest check-run state per check name.
func (a *GithubAdapter) upsertPRChecks(ctx context.Context, prID string, runs []githubCheckRun) {
	now := time.Now()
	for _, run := range runs {
		if run.Name == "" {
			continue
		}
		check := domain.GithubPRCheck{
			ID:         domain.NewID(),
			PRID:       prID,
			Name:       run.Name,
			Status:     strings.ToLower(run.Status),
			Conclusion: strings.ToLower(run.Conclusion),
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		if err := a.repos.GithubPRChecks.Upsert(ctx, check); err != nil {
			slog.Warn("github: upsert pr check failed", "pr", prID, "check", run.Name, "error", err)
		}
	}
}

// StartPRRefresh starts a background goroutine that periodically refreshes
// non-terminal GitHub pull requests from the API. It runs an immediate refresh
// on startup and then repeats every 120 seconds.
func (a *GithubAdapter) StartPRRefresh(ctx context.Context, workspaceID string) func() {
	if a.repos.GithubPRs == nil {
		return nil
	}
	refreshCtx, cancel := context.WithCancel(context.Background())
	go func() {
		// Immediate refresh on startup.
		a.refreshPRs(refreshCtx, workspaceID)

		ticker := time.NewTicker(120 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.refreshPRs(refreshCtx, workspaceID)
			}
		}
	}()
	return cancel
}

func (a *GithubAdapter) checkAllMerged(ctx context.Context, workspaceID, prID string) {
	if a.repos.SessionArtifacts == nil || a.repos.Sessions == nil || a.repos.Bus == nil {
		return
	}
	links, err := a.repos.SessionArtifacts.ListByWorkspaceID(ctx, workspaceID)
	if err != nil {
		slog.Warn("github: list artifacts for merge check failed", "error", err)
		return
	}
	// Find which work item this PR belongs to.
	var workItemID string
	for _, link := range links {
		if link.ProviderArtifactID == prID {
			workItemID = link.WorkItemID
			break
		}
	}
	if workItemID == "" {
		return
	}
	// Check work item is in completed state.
	wi, err := a.repos.Sessions.Get(ctx, workItemID)
	if err != nil {
		slog.Warn("github: get work item for merge check failed", "work_item_id", workItemID, "error", err)
		return
	}
	if wi.State != domain.SessionCompleted {
		return
	}
	// Collect all links for this work item and check each provider row.
	for _, link := range links {
		if link.WorkItemID != workItemID {
			continue
		}
		switch link.Provider {
		case "github":
			ghPR, err := a.repos.GithubPRs.Get(ctx, link.ProviderArtifactID)
			if err != nil {
				slog.Warn("github: get pr for merge check failed", "pr_id", link.ProviderArtifactID, "error", err)
				return
			}
			if ghPR.State != "merged" {
				return
			}
		case "gitlab":
			if a.repos.GitlabMRs == nil {
				return
			}
			glMR, err := a.repos.GitlabMRs.Get(ctx, link.ProviderArtifactID)
			if err != nil {
				slog.Warn("github: get gitlab mr for merge check failed", "mr_id", link.ProviderArtifactID, "error", err)
				return
			}
			if glMR.State != "merged" {
				return
			}
		default:
			return // Unknown provider, can't verify
		}
	}
	// All merged — transition work item and emit event.
	if err := a.repos.Sessions.MergeWorkItem(ctx, workItemID); err != nil {
		slog.Warn("github: merge work item failed", "work_item_id", workItemID, "error", err)
		return
	}
	payload, err := json.Marshal(map[string]string{
		"workspace_id": workspaceID,
		"work_item_id": workItemID,
		"external_id":  wi.ExternalID,
	})
	if err != nil {
		slog.Warn("github: marshal pr.merged payload failed", "error", err)
		return
	}
	if err := a.repos.Bus.Publish(ctx, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventPRMerged),
		WorkspaceID: workspaceID,
		Payload:     string(payload),
		CreatedAt:   time.Now(),
	}); err != nil {
		slog.Warn("github: publish pr.merged event failed", "error", err)
	}
}

func (a *GithubAdapter) onPRMerged(ctx context.Context, payload string) error {
	ids := extractExternalIDs(payload, "gh:")
	if len(ids) == 0 {
		return nil
	}
	// Only close the primary (first) issue.
	externalID := ids[0]
	owner, repo, number, err := parseExternalID(externalID)
	if err != nil {
		return fmt.Errorf("parse external id for issue close: %w", err)
	}
	return a.patchJSON(ctx, fmt.Sprintf("/repos/%s/%s/issues/%d", owner, repo, number), map[string]any{"state": "closed"}, nil)
}

func (a *GithubAdapter) Provider() string { return adapterName }

const reviewThreadsPageSize = 100

// reviewThreadsQuery paginates over review threads using GraphQL cursors.
// Inner comments(first:1) is sufficient — only the opening comment of each
// thread is surfaced, so requesting more would be wasted work.
const reviewThreadsQuery = `query($owner:String!,$name:String!,$number:Int!,$cursor:String) {
  repository(owner:$owner,name:$name) {
    pullRequest(number:$number) {
      reviewThreads(first:100, after:$cursor) {
        pageInfo { hasNextPage endCursor }
        nodes {
          isResolved
          comments(first:1) {
            nodes {
              id
              body
              path
              line
              url
              createdAt
              author { login }
            }
          }
        }
      }
    }
  }
}`

type githubReviewCommentNode struct {
	ID        string  `json:"id"`
	Body      string  `json:"body"`
	Path      *string `json:"path"`
	Line      *int    `json:"line"`
	URL       string  `json:"url"`
	CreatedAt string  `json:"createdAt"`
	Author    *struct {
		Login string `json:"login"`
	} `json:"author"`
}

type githubReviewThreadNode struct {
	IsResolved bool `json:"isResolved"`
	Comments   struct {
		Nodes []githubReviewCommentNode `json:"nodes"`
	} `json:"comments"`
}

type githubReviewThreadsResponse struct {
	Repository *struct {
		PullRequest *struct {
			ReviewThreads struct {
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
				Nodes []githubReviewThreadNode `json:"nodes"`
			} `json:"reviewThreads"`
		} `json:"pullRequest"`
	} `json:"repository"`
}

// FetchReviewComments returns the unresolved review comment threads for the
// given PR. Only the opening (first) comment of each unresolved thread is
// surfaced. The query paginates through all review threads via GraphQL cursors
// so PRs with more than reviewThreadsPageSize threads are fully covered.
func (a *GithubAdapter) FetchReviewComments(ctx context.Context, target adapter.ReviewCommentTarget) ([]adapter.ReviewComment, error) {
	repoIdentifier := target.RepoIdentifier
	owner, repo, ok := strings.Cut(repoIdentifier, "/")
	if !ok || owner == "" || repo == "" {
		return nil, fmt.Errorf("invalid github repo identifier %q (want owner/repo)", repoIdentifier)
	}
	number := target.Number
	var (
		out    []adapter.ReviewComment
		cursor *string
	)
	for {
		var resp githubReviewThreadsResponse
		vars := map[string]any{"owner": owner, "name": repo, "number": number, "cursor": cursor}
		if err := a.graphql(ctx, reviewThreadsQuery, vars, &resp); err != nil {
			return nil, fmt.Errorf("fetch review comments: %w", err)
		}
		if resp.Repository == nil || resp.Repository.PullRequest == nil {
			return out, nil
		}
		threads := resp.Repository.PullRequest.ReviewThreads.Nodes
		for _, th := range threads {
			if th.IsResolved || len(th.Comments.Nodes) == 0 {
				continue
			}
			out = append(out, githubThreadToReviewComment(th.Comments.Nodes[0]))
		}
		pageInfo := resp.Repository.PullRequest.ReviewThreads.PageInfo
		if !pageInfo.HasNextPage || pageInfo.EndCursor == "" {
			return out, nil
		}
		next := pageInfo.EndCursor
		cursor = &next
	}
}

// githubThreadToReviewComment normalizes a GraphQL comment node into the
// shared adapter.ReviewComment type.
func githubThreadToReviewComment(c githubReviewCommentNode) adapter.ReviewComment {
	var createdAt time.Time
	if c.CreatedAt != "" {
		parsed, err := time.Parse(time.RFC3339, c.CreatedAt)
		if err != nil {
			slog.Warn("github: invalid review comment createdAt", "value", c.CreatedAt, "error", err)
		} else {
			createdAt = parsed
		}
	}
	var pathStr string
	if c.Path != nil {
		pathStr = *c.Path
	}
	var line int
	if c.Line != nil && pathStr != "" {
		line = *c.Line
	}
	var reviewer string
	if c.Author != nil {
		reviewer = c.Author.Login
	}
	return adapter.ReviewComment{
		ID:            c.ID,
		ReviewerLogin: reviewer,
		Body:          c.Body,
		Path:          pathStr,
		Line:          line,
		URL:           c.URL,
		CreatedAt:     createdAt,
	}
}

// maxGraphQLErrorBodyBytes caps the body slice embedded in error strings to
// keep log/UI surfaces sane when an upstream proxy returns a large HTML page.
const maxGraphQLErrorBodyBytes = 4 * 1024

// graphql posts a GraphQL query to GitHub's /graphql endpoint, decoding the
// `data` payload into dst. Returns a wrapped error containing all GraphQL
// error messages when the response includes any.
func (a *GithubAdapter) graphql(ctx context.Context, query string, variables map[string]any, dst any) error {
	endpoint := strings.TrimRight(a.baseURL, "/") + "/graphql"
	payload, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return fmt.Errorf("marshal graphql request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create graphql request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("graphql request: %w", err)
	}
	defer resp.Body.Close()
	limitedBody := io.LimitReader(resp.Body, maxResponseBodyBytes)
	data, readErr := io.ReadAll(limitedBody)
	if readErr != nil {
		return fmt.Errorf("read graphql response: %w", readErr)
	}
	trimmed := strings.TrimSpace(string(data))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return &adapter.PermissionError{Adapter: adapterName, StatusCode: resp.StatusCode, Body: truncateForError(trimmed)}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("github graphql status %d: %s", resp.StatusCode, truncateForError(trimmed))
	}
	var wrapper struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return fmt.Errorf("decode graphql response: %w", err)
	}
	if len(wrapper.Errors) > 0 {
		msgs := make([]string, 0, len(wrapper.Errors))
		for _, e := range wrapper.Errors {
			msgs = append(msgs, e.Message)
		}
		return fmt.Errorf("graphql error: %s", strings.Join(msgs, "; "))
	}
	if dst == nil || len(wrapper.Data) == 0 {
		return nil
	}
	if err := json.Unmarshal(wrapper.Data, dst); err != nil {
		return fmt.Errorf("decode graphql data: %w", err)
	}
	return nil
}

// truncateForError clamps a string to maxGraphQLErrorBodyBytes so error values
// embedded in slog/UI surfaces do not balloon when an upstream proxy returns
// a large HTML payload.
func truncateForError(s string) string {
	if len(s) <= maxGraphQLErrorBodyBytes {
		return s
	}
	return s[:maxGraphQLErrorBodyBytes] + "…(truncated)"
}
