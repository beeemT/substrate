package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os/exec"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
)

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

	mu      sync.RWMutex
	tracked map[string]int
}

type repoInfo struct {
	DefaultBranch string `json:"default_branch"`
}
type githubIssue struct {
	Number    int64      `json:"number"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	State     string     `json:"state"`
	HTMLURL   string     `json:"html_url"`
	CreatedAt *time.Time `json:"created_at"`
	UpdatedAt *time.Time `json:"updated_at"`
	Labels    []struct {
		Name string `json:"name"`
	} `json:"labels"`
	PullReq *struct{} `json:"pull_request,omitempty"`
}
type githubMilestone struct {
	Number      int64      `json:"number"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	State       string     `json:"state"`
	CreatedAt   *time.Time `json:"created_at"`
	UpdatedAt   *time.Time `json:"updated_at"`
}
type githubUser struct {
	Login string `json:"login"`
}
type githubPull struct {
	Number  int    `json:"number"`
	Draft   bool   `json:"draft"`
	HTMLURL string `json:"html_url"`
}

type worktreePayload struct {
	WorkspaceID   string `json:"workspace_id"`
	Repository    string `json:"repository"`
	Branch        string `json:"branch"`
	WorktreePath  string `json:"worktree_path"`
	WorkItemTitle string `json:"work_item_title"`
	SubPlan       string `json:"sub_plan"`
}
type completedPayload struct {
	Branch     string `json:"branch"`
	ExternalID string `json:"external_id"`
}

func New(ctx context.Context, cfg config.GithubConfig) (*GithubAdapter, error) {
	return newWithDeps(ctx, cfg, &http.Client{Timeout: 30 * time.Second}, execTokenResolver)
}

func newWithDeps(ctx context.Context, cfg config.GithubConfig, client httpClient, resolver tokenResolver) (*GithubAdapter, error) {
	if strings.TrimSpace(cfg.Owner) == "" || strings.TrimSpace(cfg.Repo) == "" {
		return nil, fmt.Errorf("github owner and repo are required")
	}
	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		var err error
		token, err = resolver(ctx)
		if err != nil {
			return nil, fmt.Errorf("resolve github token: %w", err)
		}
	}
	a := &GithubAdapter{cfg: cfg, client: client, baseURL: "https://api.github.com", token: token, tracked: make(map[string]int)}
	var repo repoInfo
	if err := a.getJSON(ctx, "/repos/"+cfg.Owner+"/"+cfg.Repo, nil, &repo); err != nil {
		slog.Warn("github: failed to detect default branch; falling back to main", "owner", cfg.Owner, "repo", cfg.Repo, "err", err)
		a.defaultBranch = "main"
	} else if strings.TrimSpace(repo.DefaultBranch) == "" {
		a.defaultBranch = "main"
	} else {
		a.defaultBranch = repo.DefaultBranch
	}
	if cfg.Assignee == "" || cfg.Assignee == "me" {
		var user githubUser
		if err := a.getJSON(ctx, "/user", nil, &user); err == nil && user.Login != "" {
			a.assignee = user.Login
		} else {
			a.assignee = "me"
		}
	} else {
		a.assignee = cfg.Assignee
	}
	return a, nil
}

func execTokenResolver(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "gh", "auth", "token").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gh auth token: %w: %s", err, strings.TrimSpace(string(out)))
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		return "", fmt.Errorf("gh auth token returned empty output")
	}
	return token, nil
}

func (a *GithubAdapter) Name() string { return "github" }
func (a *GithubAdapter) Capabilities() adapter.AdapterCapabilities {
	return adapter.AdapterCapabilities{CanWatch: true, CanBrowse: true, CanMutate: true, BrowseScopes: []domain.SelectionScope{domain.ScopeIssues, domain.ScopeProjects}}
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
			items = append(items, adapter.ListItem{ID: strconv.FormatInt(iss.Number, 10), Title: fmt.Sprintf("#%d: %s", iss.Number, iss.Title), Description: iss.Body, State: iss.State, Labels: issueLabels(iss), CreatedAt: derefTime(iss.CreatedAt), UpdatedAt: derefTime(iss.UpdatedAt)})
		}
		return &adapter.ListResult{Items: items, TotalCount: len(items)}, nil
	case domain.ScopeProjects:
		milestones, err := a.listMilestones(ctx, opts)
		if err != nil {
			return nil, err
		}
		items := make([]adapter.ListItem, 0, len(milestones))
		for _, ms := range milestones {
			items = append(items, adapter.ListItem{ID: strconv.FormatInt(ms.Number, 10), Title: ms.Title, Description: ms.Description, State: ms.State, CreatedAt: derefTime(ms.CreatedAt), UpdatedAt: derefTime(ms.UpdatedAt)})
		}
		return &adapter.ListResult{Items: items, TotalCount: len(items)}, nil
	case domain.ScopeInitiatives:
		// TODO(phase-N): GitHub Projects v2 via GraphQL
		return nil, adapter.ErrBrowseNotSupported
	default:
		return nil, adapter.ErrBrowseNotSupported
	}
}

func (a *GithubAdapter) Resolve(ctx context.Context, sel adapter.Selection) (domain.WorkItem, error) {
	switch sel.Scope {
	case domain.ScopeIssues:
		issues := make([]githubIssue, 0, len(sel.ItemIDs))
		for _, itemID := range sel.ItemIDs {
			num, err := strconv.ParseInt(itemID, 10, 64)
			if err != nil {
				return domain.WorkItem{}, fmt.Errorf("parse github issue number %q: %w", itemID, err)
			}
			iss, err := a.fetchIssue(ctx, num)
			if err != nil {
				return domain.WorkItem{}, err
			}
			issues = append(issues, iss)
		}
		if len(issues) == 1 {
			return issueToWorkItem(a.cfg.Owner, a.cfg.Repo, issues[0]), nil
		}
		return aggregateIssues(a.cfg.Owner, a.cfg.Repo, issues), nil
	case domain.ScopeProjects:
		parts := make([]string, 0, len(sel.ItemIDs))
		titles := make([]string, 0, len(sel.ItemIDs))
		for _, itemID := range sel.ItemIDs {
			num, err := strconv.ParseInt(itemID, 10, 64)
			if err != nil {
				return domain.WorkItem{}, fmt.Errorf("parse milestone number %q: %w", itemID, err)
			}
			ms, err := a.fetchMilestone(ctx, num)
			if err != nil {
				return domain.WorkItem{}, err
			}
			titles = append(titles, ms.Title)
			parts = append(parts, strings.TrimSpace(ms.Title+"\n"+ms.Description))
		}
		return domain.WorkItem{ID: domain.NewID(), ExternalID: fmt.Sprintf("GH-%s-%s-MILESTONE", a.cfg.Owner, a.cfg.Repo), Source: a.Name(), SourceScope: domain.ScopeProjects, SourceItemIDs: append([]string(nil), sel.ItemIDs...), Title: strings.Join(titles, ", "), Description: strings.Join(parts, "\n\n"), State: domain.WorkItemIngested, CreatedAt: domain.Now(), UpdatedAt: domain.Now()}, nil
	default:
		return domain.WorkItem{}, adapter.ErrBrowseNotSupported
	}
}

func (a *GithubAdapter) Watch(ctx context.Context, filter adapter.WorkItemFilter) (<-chan adapter.WorkItemEvent, error) {
	interval := 60 * time.Second
	if parsed, err := time.ParseDuration(a.cfg.PollInterval); err == nil && parsed > 0 {
		interval = parsed
	}
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
						ch <- adapter.WorkItemEvent{Type: "created", WorkItem: issueToWorkItem(a.cfg.Owner, a.cfg.Repo, iss), Timestamp: domain.Now()}
					} else if prev != iss.State {
						ch <- adapter.WorkItemEvent{Type: "updated", WorkItem: issueToWorkItem(a.cfg.Owner, a.cfg.Repo, iss), Timestamp: domain.Now()}
					}
				}
			}
		}
	}()
	return ch, nil
}

func (a *GithubAdapter) Fetch(ctx context.Context, externalID string) (domain.WorkItem, error) {
	number, err := parseExternalID(a.cfg.Owner, a.cfg.Repo, externalID)
	if err != nil {
		return domain.WorkItem{}, err
	}
	iss, err := a.fetchIssue(ctx, number)
	if err != nil {
		return domain.WorkItem{}, err
	}
	return issueToWorkItem(a.cfg.Owner, a.cfg.Repo, iss), nil
}

func (a *GithubAdapter) UpdateState(ctx context.Context, externalID string, state domain.TrackerState) error {
	mapped, ok := a.cfg.StateMappings[string(state)]
	if !ok || strings.TrimSpace(mapped) == "" {
		slog.Warn("github: no state mapping configured; UpdateState is a no-op", "state", state, "external_id", externalID)
		return nil
	}
	number, err := parseExternalID(a.cfg.Owner, a.cfg.Repo, externalID)
	if err != nil {
		return err
	}
	return a.patchJSON(ctx, fmt.Sprintf("/repos/%s/%s/issues/%d", a.cfg.Owner, a.cfg.Repo, number), map[string]any{"state": mapped}, nil)
}

func (a *GithubAdapter) AddComment(ctx context.Context, externalID string, body string) error {
	number, err := parseExternalID(a.cfg.Owner, a.cfg.Repo, externalID)
	if err != nil {
		return err
	}
	return a.postJSON(ctx, fmt.Sprintf("/repos/%s/%s/issues/%d/comments", a.cfg.Owner, a.cfg.Repo, number), map[string]any{"body": body}, nil)
}

func (a *GithubAdapter) OnEvent(ctx context.Context, event domain.SystemEvent) error {
	switch domain.EventType(event.EventType) {
	case domain.EventPlanApproved:
		if err := a.onPlanApproved(ctx, event.Payload); err != nil {
			return err
		}
		externalID := extractExternalID(event.Payload)
		if externalID == "" {
			return nil
		}
		return a.UpdateState(ctx, externalID, domain.TrackerStateInProgress)
	case domain.EventWorktreeCreated:
		if err := a.onWorktreeCreated(ctx, event.Payload); err != nil {
			slog.Warn("github: worktree created handler failed", "err", err)
		}
		return nil
	case domain.EventWorkItemCompleted:
		externalID := extractExternalID(event.Payload)
		if externalID != "" {
			_ = a.UpdateState(ctx, externalID, domain.TrackerStateDone)
		}
		if err := a.onWorkItemCompleted(ctx, event.Payload); err != nil {
			slog.Warn("github: work item completed handler failed", "err", err)
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
		return fmt.Errorf("missing branch in worktree payload")
	}
	exists, prNumber, err := a.findOpenPullByBranch(ctx, p.Branch)
	if err != nil {
		return err
	}
	if exists {
		a.mu.Lock()
		a.tracked[p.Branch] = prNumber
		a.mu.Unlock()
		return nil
	}
	title := p.WorkItemTitle
	if title == "" {
		title = p.Branch
	}
	body := strings.TrimSpace(p.SubPlan)
	var created githubPull
	if err := a.postJSON(ctx, fmt.Sprintf("/repos/%s/%s/pulls", a.cfg.Owner, a.cfg.Repo), map[string]any{"title": title, "head": p.Branch, "base": a.defaultBranch, "draft": true, "body": body}, &created); err != nil {
		return err
	}

	a.mu.Lock()
	a.tracked[p.Branch] = created.Number
	a.mu.Unlock()
	return nil
}

func (a *GithubAdapter) onPlanApproved(ctx context.Context, payload string) error {
	commentBody, externalIDs := extractPlanCommentPayload(payload)
	if strings.TrimSpace(commentBody) == "" {
		return nil
	}
	for _, externalID := range externalIDs {
		if err := a.AddComment(ctx, externalID, commentBody); err != nil {
			return err
		}
	}
	return nil
}

func (a *GithubAdapter) onWorkItemCompleted(ctx context.Context, payload string) error {
	var p completedPayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return fmt.Errorf("unmarshal completed payload: %w", err)
	}
	if p.Branch == "" {
		slog.Warn("github: work_item.completed payload has no branch; skipping pr update")
		return nil
	}
	a.mu.RLock()
	prNumber, ok := a.tracked[p.Branch]
	a.mu.RUnlock()
	if !ok {
		_, foundNumber, err := a.findOpenPullByBranch(ctx, p.Branch)
		if err != nil {
			return err
		}
		prNumber = foundNumber
		if prNumber == 0 {
			return nil
		}
	}
	return a.patchJSON(ctx, fmt.Sprintf("/repos/%s/%s/pulls/%d", a.cfg.Owner, a.cfg.Repo, prNumber), map[string]any{"draft": false}, nil)
}

func (a *GithubAdapter) listIssues(ctx context.Context, opts adapter.ListOpts) ([]githubIssue, error) {
	query := url.Values{"state": []string{"open"}}
	if opts.Search != "" {
		query.Set("q", opts.Search)
	}
	if opts.Limit > 0 {
		query.Set("per_page", strconv.Itoa(opts.Limit))
	}
	var issues []githubIssue
	if err := a.getJSON(ctx, fmt.Sprintf("/repos/%s/%s/issues", a.cfg.Owner, a.cfg.Repo), query, &issues); err != nil {
		return nil, err
	}
	return filterIssues(issues), nil
}

func (a *GithubAdapter) listMilestones(ctx context.Context, opts adapter.ListOpts) ([]githubMilestone, error) {
	query := url.Values{}
	if opts.Limit > 0 {
		query.Set("per_page", strconv.Itoa(opts.Limit))
	}
	var milestones []githubMilestone
	if err := a.getJSON(ctx, fmt.Sprintf("/repos/%s/%s/milestones", a.cfg.Owner, a.cfg.Repo), query, &milestones); err != nil {
		return nil, err
	}
	return milestones, nil
}

func (a *GithubAdapter) fetchIssue(ctx context.Context, number int64) (githubIssue, error) {
	var iss githubIssue
	if err := a.getJSON(ctx, fmt.Sprintf("/repos/%s/%s/issues/%d", a.cfg.Owner, a.cfg.Repo, number), nil, &iss); err != nil {
		return githubIssue{}, err
	}
	if iss.PullReq != nil {
		return githubIssue{}, fmt.Errorf("github issue %d is a pull request", number)
	}
	return iss, nil
}

func (a *GithubAdapter) fetchMilestone(ctx context.Context, number int64) (githubMilestone, error) {
	var ms githubMilestone
	if err := a.getJSON(ctx, fmt.Sprintf("/repos/%s/%s/milestones/%d", a.cfg.Owner, a.cfg.Repo, number), nil, &ms); err != nil {
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
	if err := a.getJSON(ctx, fmt.Sprintf("/repos/%s/%s/issues", a.cfg.Owner, a.cfg.Repo), query, &issues); err != nil {
		return nil, err
	}
	filtered := filterIssues(issues)
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].Number < filtered[j].Number })
	return filtered, nil
}

func (a *GithubAdapter) findOpenPullByBranch(ctx context.Context, branch string) (bool, int, error) {
	query := url.Values{"state": []string{"open"}, "head": []string{a.cfg.Owner + ":" + branch}}
	var pulls []githubPull
	if err := a.getJSON(ctx, fmt.Sprintf("/repos/%s/%s/pulls", a.cfg.Owner, a.cfg.Repo), query, &pulls); err != nil {
		return false, 0, err
	}
	if len(pulls) == 0 {
		return false, 0, nil
	}
	return true, pulls[0].Number, nil
}

func (a *GithubAdapter) getJSON(ctx context.Context, endpoint string, query url.Values, dst any) error {
	return a.doJSON(ctx, http.MethodGet, endpoint, query, nil, dst)
}

func (a *GithubAdapter) postJSON(ctx context.Context, endpoint string, body any, dst any) error {
	return a.doJSON(ctx, http.MethodPost, endpoint, nil, body, dst)
}

func (a *GithubAdapter) patchJSON(ctx context.Context, endpoint string, body any, dst any) error {
	return a.doJSON(ctx, http.MethodPatch, endpoint, nil, body, dst)
}

func (a *GithubAdapter) doJSON(ctx context.Context, method, endpoint string, query url.Values, body any, dst any) error {
	fullURL, err := url.Parse(a.baseURL)
	if err != nil {
		return fmt.Errorf("parse base url: %w", err)
	}
	fullURL.Path = path.Join(fullURL.Path, endpoint)
	fullURL.RawQuery = query.Encode()
	var bodyReader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, fullURL.String(), bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("github api status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if dst == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
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

func issueToWorkItem(owner, repo string, iss githubIssue) domain.WorkItem {
	return domain.WorkItem{ID: domain.NewID(), ExternalID: formatExternalID(owner, repo, iss.Number), Source: "github", SourceScope: domain.ScopeIssues, SourceItemIDs: []string{strconv.FormatInt(iss.Number, 10)}, Title: iss.Title, Description: iss.Body, Labels: issueLabels(iss), State: domain.WorkItemIngested, Metadata: map[string]any{"url": iss.HTMLURL}, CreatedAt: derefTime(iss.CreatedAt), UpdatedAt: derefTime(iss.UpdatedAt)}
}

func aggregateIssues(owner, repo string, issues []githubIssue) domain.WorkItem {
	labels := map[string]struct{}{}
	parts := make([]string, 0, len(issues))
	itemIDs := make([]string, 0, len(issues))
	for _, iss := range issues {
		itemIDs = append(itemIDs, strconv.FormatInt(iss.Number, 10))
		parts = append(parts, fmt.Sprintf("#%d %s\n%s", iss.Number, iss.Title, strings.TrimSpace(iss.Body)))
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
	return domain.WorkItem{ID: domain.NewID(), ExternalID: formatExternalID(owner, repo, issues[0].Number), Source: "github", SourceScope: domain.ScopeIssues, SourceItemIDs: itemIDs, Title: title, Description: strings.Join(parts, "\n\n---\n\n"), Labels: merged, State: domain.WorkItemIngested, CreatedAt: domain.Now(), UpdatedAt: domain.Now()}
}

func formatExternalID(owner, repo string, number int64) string {
	return fmt.Sprintf("GH-%s-%s-%d", owner, repo, number)
}

func parseExternalID(owner, repo, externalID string) (int64, error) {
	prefix := fmt.Sprintf("GH-%s-%s-", owner, repo)
	if !strings.HasPrefix(externalID, prefix) {
		return 0, fmt.Errorf("github external id repo mismatch: got %q want prefix %q", externalID, prefix)
	}
	num, err := strconv.ParseInt(strings.TrimPrefix(externalID, prefix), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse issue number: %w", err)
	}
	return num, nil
}

func extractExternalID(payload string) string {
	var parsed struct {
		ExternalID string `json:"external_id"`
	}
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		return ""
	}
	return parsed.ExternalID
}

func extractPlanCommentPayload(payload string) (string, []string) {
	var parsed struct {
		CommentBody string   `json:"comment_body"`
		ExternalIDs []string `json:"external_ids"`
	}
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		return "", nil
	}
	return parsed.CommentBody, parsed.ExternalIDs
}

func derefTime(t *time.Time) time.Time {
	if t == nil {
		return domain.Now()
	}
	return t.UTC()
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
