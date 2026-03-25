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
	"os/exec"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
)

const minPollInterval = 30 * time.Second

// maxResponseBodyBytes limits HTTP response body reads to prevent OOM from
// a malicious or misconfigured API server.
const maxResponseBodyBytes = 50 * 1024 * 1024 // 50 MiB

const (
	filterAll         = "all"
	filterCreatedByMe = "created_by_me"
	filterClosed      = "closed"
)

type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// tokenResolver resolves a GitLab API token. It is called when the config
// does not contain a token directly. Mirrors the GitHub adapter pattern.
type tokenResolver func(ctx context.Context, host string) (string, error)

type GitlabAdapter struct {
	cfg     config.GitlabConfig
	baseURL string
	client  httpDoer
}

type issue struct {
	ID          int64    `json:"id"`
	IID         int64    `json:"iid"`
	ProjectID   int64    `json:"project_id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	State       string   `json:"state"`
	Labels      []string `json:"labels"`
	WebURL      string   `json:"web_url"`
	References  struct {
		Full string `json:"full"`
	} `json:"references"`
	CreatedAt *time.Time `json:"created_at"`
	UpdatedAt *time.Time `json:"updated_at"`
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
	return newWithDeps(ctx, cfg, &http.Client{Timeout: 30 * time.Second}, execTokenResolver)
}

func newWithClient(ctx context.Context, cfg config.GitlabConfig, client httpDoer) (*GitlabAdapter, error) {
	return newWithDeps(ctx, cfg, client, execTokenResolver)
}

func newWithDeps(ctx context.Context, cfg config.GitlabConfig, client httpDoer, resolver tokenResolver) (*GitlabAdapter, error) {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = "https://gitlab.com"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		host := hostFromBaseURL(baseURL)
		var err error
		token, err = resolver(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("resolve gitlab token: %w", err)
		}
	}
	cfg.Token = token

	a := &GitlabAdapter{cfg: cfg, baseURL: baseURL, client: client}

	return a, nil
}

// hostFromBaseURL extracts the hostname from a base URL for glab token lookup.
func hostFromBaseURL(baseURL string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}

	return parsed.Hostname()
}

// execTokenResolver retrieves a GitLab token from the glab CLI.
// It runs: glab config get token --host <host>
func execTokenResolver(ctx context.Context, host string) (string, error) {
	args := []string{"config", "get", "token"}
	if host != "" {
		args = append(args, "--host", host)
	}
	out, err := exec.CommandContext(ctx, "glab", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("glab config get token: %w: %s", err, strings.TrimSpace(string(out)))
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		return "", errors.New("glab config get token returned empty output")
	}

	return token, nil
}

func (a *GitlabAdapter) Name() string { return "gitlab" }

func (a *GitlabAdapter) Capabilities() adapter.AdapterCapabilities {
	filters := map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
		domain.ScopeIssues: {
			Views:          []string{"assigned_to_me", filterCreatedByMe, filterAll},
			States:         []string{"open", filterClosed, filterAll},
			SupportsLabels: true,
			SupportsSearch: true,
			SupportsOffset: true,
			SupportsRepo:   true,
			SupportsGroup:  true,
		},
		domain.ScopeProjects:    {SupportsOffset: true, SupportsRepo: true},
		domain.ScopeInitiatives: {SupportsOffset: true, SupportsGroup: true},
	}

	return adapter.AdapterCapabilities{
		CanWatch:  true,
		CanBrowse: true,
		CanMutate: true,
		BrowseScopes: []domain.SelectionScope{
			domain.ScopeIssues, domain.ScopeProjects, domain.ScopeInitiatives,
		},
		BrowseFilters: filters,
	}
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
			itemID := gitlabIssueSelectionID(0, iss)
			items = append(items, adapter.ListItem{
				ID:           itemID,
				Identifier:   fmt.Sprintf("#%d", iss.IID),
				Title:        iss.Title,
				Description:  iss.Description,
				State:        iss.State,
				Labels:       append([]string(nil), iss.Labels...),
				ContainerRef: gitlabProjectPath(iss),
				URL:          iss.WebURL,
				CreatedAt:    derefTime(iss.CreatedAt),
				UpdatedAt:    derefTime(iss.UpdatedAt),
			})
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
				ID:          strconv.FormatInt(ms.ID, 10),
				Title:       ms.Title,
				Description: ms.Description,
				State:       ms.State,
				CreatedAt:   derefTime(ms.CreatedAt),
				UpdatedAt:   derefTime(ms.UpdatedAt),
			})
		}

		return &adapter.ListResult{Items: items, TotalCount: len(items)}, nil
	case domain.ScopeInitiatives:
		epics, err := a.listEpics(ctx, opts)
		if err != nil {
			return nil, err
		}
		items := make([]adapter.ListItem, 0, len(epics))
		for _, ep := range epics {
			items = append(items, adapter.ListItem{
				ID:          strconv.FormatInt(ep.IID, 10),
				Title:       ep.Title,
				Description: ep.Description,
				State:       ep.State,
				CreatedAt:   derefTime(ep.CreatedAt),
				UpdatedAt:   derefTime(ep.UpdatedAt),
			})
		}

		return &adapter.ListResult{Items: items, TotalCount: len(items)}, nil
	default:
		return nil, adapter.ErrBrowseNotSupported
	}
}

func (a *GitlabAdapter) Resolve(ctx context.Context, sel adapter.Selection) (domain.Session, error) {
	switch sel.Scope {
	case domain.ScopeIssues:
		issues := make([]issue, 0, len(sel.ItemIDs))
		for _, itemID := range sel.ItemIDs {
			projectID, iid, err := parseIssueSelectionID(0, itemID)
			if err != nil {
				return domain.Session{}, err
			}
			iss, err := a.fetchIssue(ctx, projectID, iid)
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
		projectID := resolveSelectionProjectID(sel)
		if projectID == 0 {
			return domain.Session{}, errors.New("gitlab milestone selection requires project_id metadata")
		}
		parts := make([]string, 0, len(sel.ItemIDs))
		titles := make([]string, 0, len(sel.ItemIDs))
		milestones := make([]milestone, 0, len(sel.ItemIDs))
		for _, itemID := range sel.ItemIDs {
			id, err := strconv.ParseInt(itemID, 10, 64)
			if err != nil {
				return domain.Session{}, fmt.Errorf("parse milestone id %q: %w", itemID, err)
			}
			ms, err := a.fetchMilestone(ctx, projectID, id)
			if err != nil {
				return domain.Session{}, err
			}
			milestones = append(milestones, ms)
			titles = append(titles, ms.Title)
			parts = append(parts, formatMilestone(ms))
		}

		return domain.Session{
			ID:            domain.NewID(),
			ExternalID:    fmt.Sprintf("gl:milestone:%d", projectID),
			Source:        a.Name(),
			SourceScope:   domain.ScopeProjects,
			SourceItemIDs: append([]string(nil), sel.ItemIDs...),
			Title:         strings.Join(titles, ", "),
			Description:   strings.Join(parts, "\n\n"),
			State:         domain.SessionIngested,
			Metadata: map[string]any{
				"project_id":       projectID,
				"source_summaries": gitlabMilestoneSourceSummaries(projectID, milestones),
			},
			CreatedAt: domain.Now(),
			UpdatedAt: domain.Now(),
		}, nil
	case domain.ScopeInitiatives:
		groupID := parseGroupIDFromMetadata(sel.Metadata)
		if groupID == 0 {
			return domain.Session{}, errors.New("gitlab epic selection requires group_id metadata")
		}
		if len(sel.ItemIDs) != 1 {
			return domain.Session{}, errors.New("initiatives scope requires exactly one selection")
		}
		iid, err := strconv.ParseInt(sel.ItemIDs[0], 10, 64)
		if err != nil {
			return domain.Session{}, fmt.Errorf("parse epic iid %q: %w", sel.ItemIDs[0], err)
		}
		ep, err := a.fetchEpic(ctx, groupID, iid)
		if err != nil {
			return domain.Session{}, err
		}

		return domain.Session{
			ID:            domain.NewID(),
			ExternalID:    fmt.Sprintf("gl:epic:%d", ep.IID),
			Source:        a.Name(),
			SourceScope:   domain.ScopeInitiatives,
			SourceItemIDs: append([]string(nil), sel.ItemIDs...),
			Title:         ep.Title,
			Description:   ep.Description,
			State:         domain.SessionIngested,
			Metadata:      map[string]any{"group_id": groupID},
			CreatedAt:     domain.Now(),
			UpdatedAt:     domain.Now(),
		}, nil
	default:
		return domain.Session{}, adapter.ErrBrowseNotSupported
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
					if len(filter.States) > 0 && !slices.Contains(filter.States, iss.State) {
						continue
					}
					prev, ok := seen[iss.IID]
					seen[iss.IID] = iss.State
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

func (a *GitlabAdapter) Fetch(ctx context.Context, externalID string) (domain.Session, error) {
	projectID, iid, err := parseExternalID(externalID)
	if err != nil {
		return domain.Session{}, err
	}
	iss, err := a.fetchIssue(ctx, projectID, iid)
	if err != nil {
		return domain.Session{}, err
	}

	return issueToWorkItem(iss), nil
}

func (a *GitlabAdapter) UpdateState(ctx context.Context, externalID string, state domain.TrackerState) error {
	mapped, ok := a.cfg.StateMappings[string(state)]
	if !ok || strings.TrimSpace(mapped) == "" {
		slog.Warn("gitlab: no state mapping configured; UpdateState is a no-op", "state", state, "external_id", externalID)

		return nil
	}
	projectID, iid, err := parseExternalID(externalID)
	if err != nil {
		return err
	}

	return a.putJSON(ctx, fmt.Sprintf("/api/v4/projects/%d/issues/%d", projectID, iid), map[string]any{"state_event": mapped}, nil)
}

func (a *GitlabAdapter) AddComment(ctx context.Context, externalID string, body string) error {
	projectID, iid, err := parseExternalID(externalID)
	if err != nil {
		return err
	}

	return a.postJSON(ctx, fmt.Sprintf("/api/v4/projects/%d/issues/%d/notes", projectID, iid), map[string]any{"body": body}, nil)
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

func (a *GitlabAdapter) listIssues(ctx context.Context, opts adapter.ListOpts) ([]issue, error) {
	query, err := gitlabIssueListQuery(opts)
	if err != nil {
		return nil, err
	}
	var issues []issue
	if err := a.getJSON(ctx, "/api/v4/issues", query, &issues); err != nil {
		return nil, err
	}

	return issues, nil
}

func parseIntFromMetadata(meta map[string]any, key string) int64 {
	if len(meta) == 0 {
		return 0
	}
	switch value := meta[key].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	case string:
		id, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)

		return id
	default:
		return 0
	}
}

func parseProjectIDFromMetadata(meta map[string]any) int64 {
	return parseIntFromMetadata(meta, "project_id")
}

func parseGroupIDFromMetadata(meta map[string]any) int64 {
	return parseIntFromMetadata(meta, "group_id")
}

func resolveSelectionProjectID(sel adapter.Selection) int64 {
	return parseProjectIDFromMetadata(sel.Metadata)
}

func resolveListProjectID(opts adapter.ListOpts) int64 {
	if id, err := strconv.ParseInt(strings.TrimSpace(opts.Repo), 10, 64); err == nil {
		return id
	}

	return 0
}

func resolveListGroupID(opts adapter.ListOpts) int64 {
	if id, err := strconv.ParseInt(strings.TrimSpace(opts.Group), 10, 64); err == nil {
		return id
	}

	return 0
}

func (a *GitlabAdapter) listMilestones(ctx context.Context, opts adapter.ListOpts) ([]milestone, error) {
	projectID := resolveListProjectID(opts)
	if projectID == 0 {
		return nil, errors.New("gitlab milestones browse requires project_id in repo filter")
	}
	query := url.Values{}
	applyListOpts(query, opts)
	var milestones []milestone
	if err := a.getJSON(ctx, fmt.Sprintf("/api/v4/projects/%d/milestones", projectID), query, &milestones); err != nil {
		return nil, err
	}

	return milestones, nil
}

func (a *GitlabAdapter) listEpics(ctx context.Context, opts adapter.ListOpts) ([]epic, error) {
	groupID := resolveListGroupID(opts)
	if groupID == 0 {
		return nil, adapter.ErrBrowseNotSupported
	}
	query := url.Values{}
	applyListOpts(query, opts)
	var epics []epic
	if err := a.getJSON(ctx, fmt.Sprintf("/api/v4/groups/%d/epics", groupID), query, &epics); err != nil {
		var apiErr *apiError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden {
			return nil, adapter.ErrBrowseNotSupported
		}

		return nil, err
	}

	return epics, nil
}

func (a *GitlabAdapter) fetchIssue(ctx context.Context, projectID, iid int64) (issue, error) {
	var iss issue
	if err := a.getJSON(ctx, fmt.Sprintf("/api/v4/projects/%d/issues/%d", projectID, iid), nil, &iss); err != nil {
		return issue{}, err
	}

	return iss, nil
}

func (a *GitlabAdapter) fetchMilestone(ctx context.Context, projectID, id int64) (milestone, error) {
	var ms milestone
	if err := a.getJSON(ctx, fmt.Sprintf("/api/v4/projects/%d/milestones/%d", projectID, id), nil, &ms); err != nil {
		return milestone{}, err
	}

	return ms, nil
}

func (a *GitlabAdapter) fetchEpic(ctx context.Context, groupID, iid int64) (epic, error) {
	var ep epic
	if err := a.getJSON(ctx, fmt.Sprintf("/api/v4/groups/%d/epics/%d", groupID, iid), nil, &ep); err != nil {
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
	if err := a.getJSON(ctx, "/api/v4/issues", query, &issues); err != nil {
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
	var bodyReader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = strings.NewReader(string(payload))
	}
	req, err := http.NewRequestWithContext(ctx, method, fullURL.String(), bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(a.cfg.Token))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	limitedBody := io.LimitReader(resp.Body, maxResponseBodyBytes)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(limitedBody)

		return &apiError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(data))}
	}
	if dst == nil {
		return nil
	}
	if err := json.NewDecoder(limitedBody).Decode(dst); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	return nil
}

func issueToWorkItem(iss issue) domain.Session {
	projectID := gitlabIssueProjectID(iss)
	selectionID := gitlabIssueSelectionID(projectID, iss)

	return domain.Session{
		ID:            domain.NewID(),
		ExternalID:    formatExternalID(projectID, iss.IID),
		Source:        "gitlab",
		SourceScope:   domain.ScopeIssues,
		SourceItemIDs: []string{selectionID},
		Title:         iss.Title,
		Description:   iss.Description,
		Labels:        append([]string(nil), iss.Labels...),
		State:         domain.SessionIngested,
		Metadata: map[string]any{
			"url":          iss.WebURL,
			"tracker_refs": gitlabTrackerRefs([]issue{iss}),
		},
		CreatedAt: derefTime(iss.CreatedAt),
		UpdatedAt: derefTime(iss.UpdatedAt),
	}
}

func aggregateIssues(issues []issue) domain.Session {
	labels := map[string]struct{}{}
	parts := make([]string, 0, len(issues))
	itemIDs := make([]string, 0, len(issues))
	for _, iss := range issues {
		itemIDs = append(itemIDs, gitlabIssueSelectionID(gitlabIssueProjectID(iss), iss))
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
	projectID := gitlabIssueProjectID(issues[0])

	return domain.Session{
		ID:            domain.NewID(),
		ExternalID:    formatExternalID(projectID, issues[0].IID),
		Source:        "gitlab",
		SourceScope:   domain.ScopeIssues,
		SourceItemIDs: itemIDs,
		Title:         title,
		Description:   strings.Join(parts, "\n\n---\n\n"),
		Labels:        merged,
		State:         domain.SessionIngested,
		Metadata: map[string]any{
			"tracker_refs":     gitlabTrackerRefs(issues),
			"source_summaries": gitlabIssueSourceSummaries(issues),
		},
		CreatedAt: domain.Now(),
		UpdatedAt: domain.Now(),
	}
}

func gitlabTrackerRefs(issues []issue) []domain.TrackerReference {
	refs := make([]domain.TrackerReference, 0, len(issues))
	seen := make(map[string]struct{}, len(issues))
	for _, iss := range issues {
		if iss.IID <= 0 {
			continue
		}
		projectPath := gitlabProjectPath(iss)
		key := fmt.Sprintf("%s#%d", projectPath, iss.IID)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		refs = append(refs, domain.TrackerReference{
			Provider: "gitlab",
			Kind:     "issue",
			ID:       strconv.FormatInt(iss.IID, 10),
			URL:      iss.WebURL,
			Repo:     projectPath,
			Number:   iss.IID,
		})
	}

	return refs
}

func gitlabProjectPath(iss issue) string {
	if strings.TrimSpace(iss.References.Full) != "" {
		parts := strings.SplitN(strings.TrimSpace(iss.References.Full), "#", 2)
		if len(parts) == 2 {
			return strings.TrimSpace(parts[0])
		}
	}
	if strings.TrimSpace(iss.WebURL) == "" {
		return ""
	}
	parsed, err := url.Parse(iss.WebURL)
	if err != nil {
		return ""
	}
	const marker = "/-/issues/"
	idx := strings.Index(parsed.Path, marker)
	if idx == -1 {
		return ""
	}

	return strings.TrimPrefix(parsed.Path[:idx], "/")
}

func gitlabIssueSelectionID(defaultProjectID int64, iss issue) string {
	projectID := gitlabIssueProjectID(iss)
	if projectID == 0 {
		projectID = defaultProjectID
	}
	if projectID > 0 {
		return fmt.Sprintf("%d#%d", projectID, iss.IID)
	}

	return strconv.FormatInt(iss.IID, 10)
}

func parseIssueSelectionID(defaultProjectID int64, raw string) (projectID, iid int64, err error) {
	trimmed := strings.TrimSpace(raw)
	parts := strings.SplitN(trimmed, "#", 2)
	if len(parts) == 1 {
		iid, err = strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("parse issue iid %q: %w", raw, err)
		}
		if defaultProjectID == 0 {
			return 0, 0, fmt.Errorf("gitlab issue selection %q missing project id", raw)
		}

		return defaultProjectID, iid, nil
	}
	projectID, err = strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("parse issue project id %q: %w", raw, err)
	}
	iid, err = strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("parse issue iid %q: %w", raw, err)
	}

	return projectID, iid, nil
}

func gitlabIssueProjectID(iss issue) int64 {
	return iss.ProjectID
}

func formatMilestone(ms milestone) string {
	return strings.TrimSpace(ms.Title + "\n" + ms.Description)
}

func formatExternalID(projectID, iid int64) string {
	return fmt.Sprintf("gl:issue:%d#%d", projectID, iid)
}

func parseExternalID(externalID string) (int64, int64, error) {
	trimmed := strings.TrimSpace(externalID)
	if !strings.HasPrefix(trimmed, "gl:issue:") {
		return 0, 0, fmt.Errorf("invalid gitlab external id %q", externalID)
	}
	raw := strings.TrimPrefix(trimmed, "gl:issue:")
	parts := strings.SplitN(raw, "#", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid gitlab external id %q", externalID)
	}
	projectID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("parse project id: %w", err)
	}
	iid, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("parse issue iid: %w", err)
	}

	return projectID, iid, nil
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

func gitlabIssueListQuery(opts adapter.ListOpts) (url.Values, error) {
	query := url.Values{}
	scope, err := gitlabIssueScopeValue(opts.View)
	if err != nil {
		return nil, err
	}
	state, err := gitlabIssueStateValue(opts.State)
	if err != nil {
		return nil, err
	}
	query.Set("scope", scope)
	query.Set("state", state)
	applyListOpts(query, opts)

	return query, nil
}

func gitlabIssueScopeValue(view string) (string, error) {
	switch strings.TrimSpace(view) {
	case "", "all":
		return filterAll, nil
	case "assigned_to_me":
		return "assigned_to_me", nil
	case "created_by_me":
		return filterCreatedByMe, nil
	case "mentioned", "subscribed":
		return "", fmt.Errorf("gitlab issue view %q not supported", view)
	default:
		return "", fmt.Errorf("gitlab issue view %q not supported", view)
	}
}

func gitlabIssueStateValue(state string) (string, error) {
	switch strings.TrimSpace(state) {
	case "", "open":
		return "opened", nil
	case "closed":
		return filterClosed, nil
	case "all":
		return filterAll, nil
	default:
		return "", fmt.Errorf("gitlab issue state %q not supported", state)
	}
}

func applyListOpts(query url.Values, opts adapter.ListOpts) {
	if opts.Search != "" {
		query.Set("search", opts.Search)
	}
	if len(opts.Labels) > 0 {
		query.Set("labels", strings.Join(opts.Labels, ","))
	}
	if strings.TrimSpace(opts.Repo) != "" {
		query.Set("project_path", strings.TrimSpace(opts.Repo))
	}
	if strings.TrimSpace(opts.Group) != "" {
		query.Set("group_id", strings.TrimSpace(opts.Group))
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
