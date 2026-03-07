package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
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

const minPollInterval = 30 * time.Second

type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type GitlabAdapter struct {
	cfg      config.GitlabConfig
	baseURL  string
	client   httpDoer
	groupID  int64
	hasEpics bool

	mu sync.RWMutex
}

type projectMeta struct {
	Namespace struct {
		ID   int64  `json:"id"`
		Kind string `json:"kind"`
	} `json:"namespace"`
}

type issue struct {
	ID          int64      `json:"id"`
	IID         int64      `json:"iid"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	State       string     `json:"state"`
	Labels      []string   `json:"labels"`
	WebURL      string     `json:"web_url"`
	CreatedAt   *time.Time `json:"created_at"`
	UpdatedAt   *time.Time `json:"updated_at"`
}

type milestone struct {
	ID          int64      `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	State       string     `json:"state"`
	CreatedAt   *time.Time `json:"created_at"`
	UpdatedAt   *time.Time `json:"updated_at"`
}

type epic struct {
	ID          int64      `json:"id"`
	IID         int64      `json:"iid"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	State       string     `json:"state"`
	WebURL      string     `json:"web_url"`
	CreatedAt   *time.Time `json:"created_at"`
	UpdatedAt   *time.Time `json:"updated_at"`
}

func New(ctx context.Context, cfg config.GitlabConfig) (*GitlabAdapter, error) {
	return newWithClient(ctx, cfg, &http.Client{Timeout: 30 * time.Second})
}

func newWithClient(ctx context.Context, cfg config.GitlabConfig, client httpDoer) (*GitlabAdapter, error) {
	if cfg.ProjectID == 0 {
		return nil, fmt.Errorf("gitlab project_id is required")
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, fmt.Errorf("gitlab token is required")
	}
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = "https://gitlab.com"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	a := &GitlabAdapter{cfg: cfg, baseURL: baseURL, client: client}
	meta, err := a.getProject(ctx)
	if err != nil {
		return nil, fmt.Errorf("load gitlab project metadata: %w", err)
	}
	if meta.Namespace.Kind == "group" {
		a.groupID = meta.Namespace.ID
		a.hasEpics = true
	} else {
		slog.Info("gitlab: epics unavailable for personal namespace", "project_id", cfg.ProjectID)
	}
	return a, nil
}

func (a *GitlabAdapter) Name() string { return "gitlab" }

func (a *GitlabAdapter) Capabilities() adapter.AdapterCapabilities {
	return adapter.AdapterCapabilities{CanWatch: true, CanBrowse: true, CanMutate: true, BrowseScopes: []domain.SelectionScope{domain.ScopeIssues, domain.ScopeProjects, domain.ScopeInitiatives}}
}

func (a *GitlabAdapter) ListSelectable(ctx context.Context, opts adapter.ListOpts) (*adapter.ListResult, error) {
	switch opts.Scope {
	case domain.ScopeIssues:
		issues, err := a.listIssues(ctx, opts)
		if err != nil {
			return nil, err
		}
		items := make([]adapter.ListItem, 0, len(issues))
		for _, iss := range issues {
			items = append(items, adapter.ListItem{ID: strconv.FormatInt(iss.IID, 10), Title: fmt.Sprintf("#%d: %s", iss.IID, iss.Title), Description: iss.Description, State: iss.State, Labels: append([]string(nil), iss.Labels...), CreatedAt: derefTime(iss.CreatedAt), UpdatedAt: derefTime(iss.UpdatedAt)})
		}
		return &adapter.ListResult{Items: items, TotalCount: len(items)}, nil
	case domain.ScopeProjects:
		milestones, err := a.listMilestones(ctx, opts)
		if err != nil {
			return nil, err
		}
		items := make([]adapter.ListItem, 0, len(milestones))
		for _, ms := range milestones {
			items = append(items, adapter.ListItem{ID: strconv.FormatInt(ms.ID, 10), Title: ms.Title, Description: ms.Description, State: ms.State, CreatedAt: derefTime(ms.CreatedAt), UpdatedAt: derefTime(ms.UpdatedAt)})
		}
		return &adapter.ListResult{Items: items, TotalCount: len(items)}, nil
	case domain.ScopeInitiatives:
		if !a.hasEpics {
			return nil, adapter.ErrBrowseNotSupported
		}
		epics, err := a.listEpics(ctx, opts)
		if err != nil {
			if errors.Is(err, adapter.ErrBrowseNotSupported) {
				return nil, err
			}
			return nil, err
		}
		items := make([]adapter.ListItem, 0, len(epics))
		for _, ep := range epics {
			items = append(items, adapter.ListItem{ID: strconv.FormatInt(ep.IID, 10), Title: ep.Title, Description: ep.Description, State: ep.State, CreatedAt: derefTime(ep.CreatedAt), UpdatedAt: derefTime(ep.UpdatedAt)})
		}
		return &adapter.ListResult{Items: items, TotalCount: len(items)}, nil
	default:
		return nil, adapter.ErrBrowseNotSupported
	}
}

func (a *GitlabAdapter) Resolve(ctx context.Context, sel adapter.Selection) (domain.WorkItem, error) {
	switch sel.Scope {
	case domain.ScopeIssues:
		issues := make([]issue, 0, len(sel.ItemIDs))
		for _, itemID := range sel.ItemIDs {
			iid, err := strconv.ParseInt(itemID, 10, 64)
			if err != nil {
				return domain.WorkItem{}, fmt.Errorf("parse issue iid %q: %w", itemID, err)
			}
			iss, err := a.fetchIssue(ctx, iid)
			if err != nil {
				return domain.WorkItem{}, err
			}
			issues = append(issues, iss)
		}
		if len(issues) == 1 {
			return issueToWorkItem(a.cfg.ProjectID, issues[0]), nil
		}
		return aggregateIssues(a.cfg.ProjectID, issues), nil
	case domain.ScopeProjects:
		parts := make([]string, 0, len(sel.ItemIDs))
		titles := make([]string, 0, len(sel.ItemIDs))
		for _, itemID := range sel.ItemIDs {
			id, err := strconv.ParseInt(itemID, 10, 64)
			if err != nil {
				return domain.WorkItem{}, fmt.Errorf("parse milestone id %q: %w", itemID, err)
			}
			ms, err := a.fetchMilestone(ctx, id)
			if err != nil {
				return domain.WorkItem{}, err
			}
			titles = append(titles, ms.Title)
			parts = append(parts, formatMilestone(ms))
		}
		return domain.WorkItem{ID: domain.NewID(), ExternalID: fmt.Sprintf("GL-%d-MILESTONE", a.cfg.ProjectID), Source: a.Name(), SourceScope: domain.ScopeProjects, SourceItemIDs: append([]string(nil), sel.ItemIDs...), Title: strings.Join(titles, ", "), Description: strings.Join(parts, "\n\n"), State: domain.WorkItemIngested, CreatedAt: domain.Now(), UpdatedAt: domain.Now()}, nil
	case domain.ScopeInitiatives:
		if len(sel.ItemIDs) != 1 {
			return domain.WorkItem{}, fmt.Errorf("initiatives scope requires exactly one selection")
		}
		iid, err := strconv.ParseInt(sel.ItemIDs[0], 10, 64)
		if err != nil {
			return domain.WorkItem{}, fmt.Errorf("parse epic iid %q: %w", sel.ItemIDs[0], err)
		}
		ep, err := a.fetchEpic(ctx, iid)
		if err != nil {
			return domain.WorkItem{}, err
		}
		return domain.WorkItem{ID: domain.NewID(), ExternalID: fmt.Sprintf("GL-%d-EPIC-%d", a.cfg.ProjectID, ep.IID), Source: a.Name(), SourceScope: domain.ScopeInitiatives, SourceItemIDs: append([]string(nil), sel.ItemIDs...), Title: ep.Title, Description: ep.Description, State: domain.WorkItemIngested, CreatedAt: domain.Now(), UpdatedAt: domain.Now()}, nil
	default:
		return domain.WorkItem{}, adapter.ErrBrowseNotSupported
	}
}

func (a *GitlabAdapter) Watch(ctx context.Context, filter adapter.WorkItemFilter) (<-chan adapter.WorkItemEvent, error) {
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
				issues, err := a.fetchAssignedOpenedIssues(ctx)
				if err != nil {
					ch <- adapter.WorkItemEvent{Type: "error", Timestamp: domain.Now()}
					continue
				}
				for _, iss := range issues {
					if len(filter.States) > 0 && !contains(filter.States, iss.State) {
						continue
					}
					prev, ok := seen[iss.IID]
					seen[iss.IID] = iss.State
					if !ok {
						ch <- adapter.WorkItemEvent{Type: "created", WorkItem: issueToWorkItem(a.cfg.ProjectID, iss), Timestamp: domain.Now()}
					} else if prev != iss.State {
						ch <- adapter.WorkItemEvent{Type: "updated", WorkItem: issueToWorkItem(a.cfg.ProjectID, iss), Timestamp: domain.Now()}
					}
				}
			}
		}
	}()
	return ch, nil
}

func (a *GitlabAdapter) Fetch(ctx context.Context, externalID string) (domain.WorkItem, error) {
	iid, err := parseExternalID(a.cfg.ProjectID, externalID)
	if err != nil {
		return domain.WorkItem{}, err
	}
	iss, err := a.fetchIssue(ctx, iid)
	if err != nil {
		return domain.WorkItem{}, err
	}
	return issueToWorkItem(a.cfg.ProjectID, iss), nil
}

func (a *GitlabAdapter) UpdateState(ctx context.Context, externalID string, state domain.TrackerState) error {
	mapped, ok := a.cfg.StateMappings[string(state)]
	if !ok || strings.TrimSpace(mapped) == "" {
		slog.Warn("gitlab: no state mapping configured; UpdateState is a no-op", "state", state, "external_id", externalID)
		return nil
	}
	iid, err := parseExternalID(a.cfg.ProjectID, externalID)
	if err != nil {
		return err
	}
	return a.putJSON(ctx, fmt.Sprintf("/api/v4/projects/%d/issues/%d", a.cfg.ProjectID, iid), map[string]any{"state_event": mapped}, nil)
}

func (a *GitlabAdapter) AddComment(ctx context.Context, externalID string, body string) error {
	iid, err := parseExternalID(a.cfg.ProjectID, externalID)
	if err != nil {
		return err
	}
	return a.postJSON(ctx, fmt.Sprintf("/api/v4/projects/%d/issues/%d/notes", a.cfg.ProjectID, iid), map[string]any{"body": body}, nil)
}

func (a *GitlabAdapter) OnEvent(ctx context.Context, event domain.SystemEvent) error {
	externalID := extractExternalID(event.Payload)
	switch domain.EventType(event.EventType) {
	case domain.EventPlanApproved:
		if err := a.onPlanApproved(ctx, event.Payload); err != nil {
			return err
		}
		if externalID == "" {
			return nil
		}
		return a.UpdateState(ctx, externalID, domain.TrackerStateInProgress)
	case domain.EventWorkItemCompleted:
		if externalID == "" {
			return nil
		}
		return a.UpdateState(ctx, externalID, domain.TrackerStateDone)
	default:
		return nil
	}
}

func (a *GitlabAdapter) onPlanApproved(ctx context.Context, payload string) error {
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

func (a *GitlabAdapter) getProject(ctx context.Context) (projectMeta, error) {
	var meta projectMeta
	err := a.getJSON(ctx, fmt.Sprintf("/api/v4/projects/%d", a.cfg.ProjectID), nil, &meta)
	return meta, err
}

func (a *GitlabAdapter) listIssues(ctx context.Context, opts adapter.ListOpts) ([]issue, error) {
	query := url.Values{}
	query.Set("state", "opened")
	applyListOpts(query, opts)
	var issues []issue
	if err := a.getJSON(ctx, fmt.Sprintf("/api/v4/projects/%d/issues", a.cfg.ProjectID), query, &issues); err != nil {
		return nil, err
	}
	return issues, nil
}

func (a *GitlabAdapter) listMilestones(ctx context.Context, opts adapter.ListOpts) ([]milestone, error) {
	query := url.Values{}
	applyListOpts(query, opts)
	var milestones []milestone
	if err := a.getJSON(ctx, fmt.Sprintf("/api/v4/projects/%d/milestones", a.cfg.ProjectID), query, &milestones); err != nil {
		return nil, err
	}
	return milestones, nil
}

func (a *GitlabAdapter) listEpics(ctx context.Context, opts adapter.ListOpts) ([]epic, error) {
	query := url.Values{}
	applyListOpts(query, opts)
	var epics []epic
	if err := a.getJSON(ctx, fmt.Sprintf("/api/v4/groups/%d/epics", a.groupID), query, &epics); err != nil {
		var apiErr *apiError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden {
			slog.Warn("gitlab: epics unsupported for this project/group", "project_id", a.cfg.ProjectID, "group_id", a.groupID)
			return nil, adapter.ErrBrowseNotSupported
		}
		return nil, err
	}
	return epics, nil
}

func (a *GitlabAdapter) fetchIssue(ctx context.Context, iid int64) (issue, error) {
	var iss issue
	if err := a.getJSON(ctx, fmt.Sprintf("/api/v4/projects/%d/issues/%d", a.cfg.ProjectID, iid), nil, &iss); err != nil {
		return issue{}, err
	}
	return iss, nil
}

func (a *GitlabAdapter) fetchMilestone(ctx context.Context, id int64) (milestone, error) {
	var ms milestone
	if err := a.getJSON(ctx, fmt.Sprintf("/api/v4/projects/%d/milestones/%d", a.cfg.ProjectID, id), nil, &ms); err != nil {
		return milestone{}, err
	}
	return ms, nil
}

func (a *GitlabAdapter) fetchEpic(ctx context.Context, iid int64) (epic, error) {
	var ep epic
	if err := a.getJSON(ctx, fmt.Sprintf("/api/v4/groups/%d/epics/%d", a.groupID, iid), nil, &ep); err != nil {
		return epic{}, err
	}
	return ep, nil
}

func (a *GitlabAdapter) fetchAssignedOpenedIssues(ctx context.Context) ([]issue, error) {
	query := url.Values{}
	query.Set("state", "opened")
	if strings.TrimSpace(a.cfg.Assignee) != "" {
		query.Set("assignee_username", a.cfg.Assignee)
	}
	var issues []issue
	if err := a.getJSON(ctx, fmt.Sprintf("/api/v4/projects/%d/issues", a.cfg.ProjectID), query, &issues); err != nil {
		return nil, err
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].IID < issues[j].IID })
	return issues, nil
}

func (a *GitlabAdapter) getJSON(ctx context.Context, endpoint string, query url.Values, dst any) error {
	return a.doJSON(ctx, http.MethodGet, endpoint, query, nil, dst)
}

func (a *GitlabAdapter) postJSON(ctx context.Context, endpoint string, body any, dst any) error {
	return a.doJSON(ctx, http.MethodPost, endpoint, nil, body, dst)
}

func (a *GitlabAdapter) putJSON(ctx context.Context, endpoint string, body any, dst any) error {
	return a.doJSON(ctx, http.MethodPut, endpoint, nil, body, dst)
}

type apiError struct {
	StatusCode int
	Body       string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("gitlab api status %d: %s", e.StatusCode, e.Body)
}

func (a *GitlabAdapter) doJSON(ctx context.Context, method, endpoint string, query url.Values, body any, dst any) error {
	fullURL, err := url.Parse(a.baseURL)
	if err != nil {
		return fmt.Errorf("parse base url: %w", err)
	}
	fullURL.Path = path.Join(fullURL.Path, endpoint)
	if query != nil {
		fullURL.RawQuery = query.Encode()
	}
	var reader strings.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		reader = *strings.NewReader(string(payload))
	} else {
		reader = *strings.NewReader("")
	}
	req, err := http.NewRequestWithContext(ctx, method, fullURL.String(), &reader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.cfg.Token)
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
		return &apiError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(data))}
	}
	if dst == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func issueToWorkItem(projectID int64, iss issue) domain.WorkItem {
	return domain.WorkItem{ID: domain.NewID(), ExternalID: formatExternalID(projectID, iss.IID), Source: "gitlab", SourceScope: domain.ScopeIssues, SourceItemIDs: []string{strconv.FormatInt(iss.IID, 10)}, Title: iss.Title, Description: iss.Description, Labels: append([]string(nil), iss.Labels...), State: domain.WorkItemIngested, Metadata: map[string]any{"url": iss.WebURL}, CreatedAt: derefTime(iss.CreatedAt), UpdatedAt: derefTime(iss.UpdatedAt)}
}

func aggregateIssues(projectID int64, issues []issue) domain.WorkItem {
	labels := map[string]struct{}{}
	parts := make([]string, 0, len(issues))
	itemIDs := make([]string, 0, len(issues))
	for _, iss := range issues {
		itemIDs = append(itemIDs, strconv.FormatInt(iss.IID, 10))
		parts = append(parts, fmt.Sprintf("#%d %s\n%s", iss.IID, iss.Title, strings.TrimSpace(iss.Description)))
		for _, label := range iss.Labels {
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
	return domain.WorkItem{ID: domain.NewID(), ExternalID: formatExternalID(projectID, issues[0].IID), Source: "gitlab", SourceScope: domain.ScopeIssues, SourceItemIDs: itemIDs, Title: title, Description: strings.Join(parts, "\n\n---\n\n"), Labels: merged, State: domain.WorkItemIngested, CreatedAt: domain.Now(), UpdatedAt: domain.Now()}
}

func formatMilestone(ms milestone) string {
	return strings.TrimSpace(ms.Title + "\n" + ms.Description)
}

func formatExternalID(projectID, iid int64) string {
	return fmt.Sprintf("GL-%d-%d", projectID, iid)
}

func parseExternalID(projectID int64, externalID string) (int64, error) {
	parts := strings.Split(externalID, "-")
	if len(parts) != 3 || parts[0] != "GL" {
		return 0, fmt.Errorf("invalid gitlab external id %q", externalID)
	}
	gotProjectID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse project id: %w", err)
	}
	if gotProjectID != projectID {
		return 0, fmt.Errorf("gitlab external id project mismatch: got %d want %d", gotProjectID, projectID)
	}
	iid, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse issue iid: %w", err)
	}
	return iid, nil
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

func applyListOpts(query url.Values, opts adapter.ListOpts) {
	if opts.Search != "" {
		query.Set("search", opts.Search)
	}
	if opts.Limit > 0 {
		query.Set("per_page", strconv.Itoa(opts.Limit))
	}
	if opts.Offset > 0 && opts.Limit > 0 {
		query.Set("page", strconv.Itoa((opts.Offset/opts.Limit)+1))
	}
}

func parsePollInterval(raw string) time.Duration {
	interval, err := time.ParseDuration(raw)
	if err != nil || interval < minPollInterval {
		return minPollInterval
	}
	return interval
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
