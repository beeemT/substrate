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
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

// Verify GitlabAdapter implements adapter.WorkItemAdapter at compile time.
var _ adapter.WorkItemAdapter = &GitlabAdapter{}

// Verify GitlabAdapter implements statusRefresher interface.
type statusRefresher interface {
	StartStatusRefresh(ctx context.Context, workspaceID string) func()
}

var _ statusRefresher = &GitlabAdapter{}

const (
	defaultWatchPollInterval = 5 * time.Minute
	minPollInterval          = 60 * time.Second
)

// maxResponseBodyBytes limits HTTP response body reads to prevent OOM from
// a malicious or misconfigured API server.
const maxResponseBodyBytes = 50 * 1024 * 1024 // 50 MiB

const (
	adapterName       = "gitlab"
	filterAll         = "all"
	filterCreatedByMe = "created_by_me"
	filterClosed      = "closed"
)

// defaultStateMappings maps domain TrackerStates to GitLab state_event values.
// GitLab's issue state_event API accepts "close" and "reopen".
var defaultStateMappings = map[string]string{
	string(domain.TrackerStateTodo):       "reopen",
	string(domain.TrackerStateInProgress): "reopen",
	string(domain.TrackerStateInReview):   "reopen",
	string(domain.TrackerStateDone):       "close",
}

type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// tokenResolver resolves a GitLab API token. It is called when the config
// does not contain a token directly. Mirrors the GitHub adapter pattern.
type tokenResolver func(ctx context.Context, host string) (string, error)

type GitlabAdapter struct {
	cfg                  config.GitlabConfig
	baseURL              string
	client               httpDoer
	username             string                      // cached authenticated username for "mine" scope checks
	usernameMu           sync.RWMutex                // protects username cache
	graphqlMu            sync.RWMutex                // protects graphqlSupported
	graphqlSupported     bool                        // true once we confirm /api/graphql responds with valid JSON
	graphqlSupportedUint uint32                      // atomic guard: set to 1 once warming has been launched; prevents duplicate warm-up passes
	statusCache          statusCache                 // projectPath -> workItemType -> statusName -> statusID
	statusCacheMu        sync.RWMutex                // protects statusCache
	repos                adapter.ReviewArtifactRepos // for background status refresh
}

type statusCache struct {
	byProject map[string]map[string]map[string]string // projectPath -> workItemType -> statusName -> statusID
	mu        sync.RWMutex
}

type issue struct {
	ID                 int64    `json:"id"`
	IID                int64    `json:"iid"`
	ProjectID          int64    `json:"project_id"`
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	State              string   `json:"state"`
	Status             string   `json:"-"` // GitLab Work Item status from GraphQL (not in REST API)
	Labels             []string `json:"labels"`
	WebURL             string   `json:"web_url"`
	MergeRequestsCount int64    `json:"merge_requests_count"`
	References         struct {
		Full string `json:"full"`
	} `json:"references"`
	CreatedAt *time.Time `json:"created_at"`
	UpdatedAt *time.Time `json:"updated_at"`
}

type project struct {
	ID                int64  `json:"id"`
	PathWithNamespace string `json:"path_with_namespace"`
	Namespace         struct {
		ID   int64  `json:"id"`
		Path string `json:"path"`
		Kind string `json:"kind"` // "user" or "group"
	} `json:"namespace"`
}

type relatedMergeRequest struct {
	IID            int64      `json:"iid"`
	ProjectID      int64      `json:"project_id"`
	State          string     `json:"state"`
	WebURL         string     `json:"web_url"`
	SourceBranch   string     `json:"source_branch"`
	Draft          bool       `json:"draft"`
	WorkInProgress bool       `json:"work_in_progress"`
	UpdatedAt      *time.Time `json:"updated_at"`
	References     struct {
		Short string `json:"short"`
		Full  string `json:"full"`
	} `json:"references"`
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

// gitlabGraphQLRequest is the GraphQL query for fetching Work Item status via the workItems widget.
type gitlabGraphQLRequest struct {
	Query               string                 `json:"query"`
	Variables           gitlabGraphQLVariables `json:"variables"`
	gitlabGraphQLErrors `json:"errors,omitempty"`
}

type gitlabGraphQLVariables struct {
	FullPath string `json:"fullPath"`
	IID      string `json:"iid"`
}

type gitlabGraphQLErrors struct {
	Message   string   `json:"message"`
	Locations []any    `json:"locations,omitempty"`
	Path      []string `json:"path,omitempty"`
}

// graphqlStatusResponse is the top-level response wrapper.
type graphqlStatusResponse struct {
	Data struct {
		Project struct {
			WorkItems struct {
				Nodes []graphqlWorkItem `json:"nodes"`
			} `json:"workItems"`
		} `json:"project"`
	} `json:"data"`
	Errors []gitlabGraphQLErrors `json:"errors,omitempty"`
}

// graphqlWorkItem is a lightweight Work Item for status queries.
type graphqlWorkItem struct {
	IID     string `json:"iid"`
	Widgets []struct {
		Type   string `json:"type"`
		Status struct {
			Name string `json:"name"`
		} `json:"status"`
	} `json:"widgets"`
}
type graphqlStatusWidget struct {
	Type   string `json:"type"`
	Status struct {
		Name string `json:"name"`
	} `json:"status"`
}

// statusEnrichment holds status per issue reference for batch enrichment.
type statusEnrichment map[string]string // key: "group/project#123", value: status string

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

	if len(a.cfg.StateMappings) == 0 {
		a.cfg.StateMappings = defaultStateMappings
	}

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

func (a *GitlabAdapter) Name() string { return adapterName }

func (a *GitlabAdapter) Capabilities() adapter.AdapterCapabilities {
	filters := map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
		domain.ScopeIssues: {
			Views: []string{"assigned_to_me", filterCreatedByMe, filterAll},

			States:         []string{"open", filterClosed, filterAll},
			SupportsLabels: true,
			SupportsSearch: true,
			SupportsOffset: true,
			SupportsRepo:   true,
			SupportsGroup:  true,
			SupportsStatus: a.checkGraphQLSupport(),
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

// graphqlSupported returns true if this GitLab instance supports the GraphQL API.
// The check is performed once on first access and cached thereafter.
func (a *GitlabAdapter) checkGraphQLSupport() bool {
	a.graphqlMu.Lock()
	defer a.graphqlMu.Unlock()
	if a.graphqlSupported {
		return true
	}
	// No token means GraphQL check is not possible.
	if strings.TrimSpace(a.cfg.Token) == "" {
		return false
	}
	// Ping /api/graphql with a minimal introspection query. We only check that the
	// endpoint returns valid JSON (even an empty object) — any JSON response means
	// the GraphQL endpoint is present. This is a cheap check that avoids parsing
	// the full schema.
	body, err := a.doJSONRaw(context.Background(), http.MethodPost, "/api/graphql", nil,
		gitlabGraphQLRequest{Query: "{ __typename }", Variables: gitlabGraphQLVariables{}})
	if err != nil {
		slog.Debug("gitlab: /api/graphql not available", "baseURL", a.baseURL, "error", err)
		return false
	}
	if len(body) == 0 || (body[0] != '{' && body[0] != '[') {
		slog.Debug("gitlab: /api/graphql returned non-JSON", "baseURL", a.baseURL)
		return false
	}
	a.graphqlSupported = true
	slog.Debug("gitlab: /api/graphql confirmed available", "baseURL", a.baseURL)
	// Attempt atomic set: if another goroutine already set graphqlSupported
	// to true and kicked off warming, skip to avoid a duplicate warm-up pass.
	if !atomic.CompareAndSwapUint32(&a.graphqlSupportedUint, 0, 1) {
		return true
	}
	// Warm the status cache for all known projects asynchronously now that we
	// know GraphQL is available. This speeds up status transitions on plan approval.
	go a.warmStatusCacheForAllProjects(context.Background())
	return true
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
			item := adapter.ListItem{
				ID:           itemID,
				Identifier:   fmt.Sprintf("#%d", iss.IID),
				Title:        iss.Title,
				Description:  iss.Description,
				State:        iss.State,
				Status:       strings.TrimSpace(iss.Status),
				Labels:       append([]string(nil), iss.Labels...),
				ContainerRef: gitlabProjectPath(iss),
				URL:          iss.WebURL,
				CreatedAt:    derefTime(iss.CreatedAt),
				UpdatedAt:    derefTime(iss.UpdatedAt),
			}
			// Populate metadata with tracker_state from GraphQL enrichment for session creation.
			if iss.Status != "" {
				item.Metadata = map[string]any{"tracker_state": iss.Status}
			}
			if artifacts := a.issueReviewArtifacts(ctx, iss); len(artifacts) > 0 {
				if item.Metadata == nil {
					item.Metadata = make(map[string]any)
				}
				item.Metadata[adapter.ListItemReviewArtifactsMetadataKey] = artifacts
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
			// Support both numeric project IDs (legacy) and path-based external IDs.
			projectID, iid, err := parseIssueSelectionID(0, itemID)
			if err != nil {
				return domain.Session{}, err
			}
			// If we got a numeric project ID, resolve it to the current project.
			if projectID == 0 && strings.Contains(itemID, "/") {
				// Path-based external ID: resolve project path to numeric ID.
				parts := strings.SplitN(itemID, ":", 2)
				if len(parts) == 2 && parts[0] == "gl" {
					pathParts := strings.Split(parts[1], "#")[0]
					pid, err := a.resolveNumericProjectID(ctx, pathParts)
					if err != nil {
						return domain.Session{}, err
					}
					projectID = pid
				}
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
	projectPath, iid, err := parseExternalID(externalID)
	if err != nil {
		return domain.Session{}, err
	}
	numericID, err := a.resolveNumericProjectID(ctx, projectPath)
	if err != nil {
		return domain.Session{}, err
	}
	iss, err := a.fetchIssue(ctx, numericID, iid)
	if err != nil {
		return domain.Session{}, err
	}

	return issueToWorkItem(iss), nil
}

func (a *GitlabAdapter) UpdateState(ctx context.Context, externalID string, state domain.TrackerState) error {
	mapped := a.cfg.StateMappings[string(state)]
	if strings.TrimSpace(mapped) == "" {
		return nil
	}
	projectPath, iid, err := parseExternalID(externalID)
	if err != nil {
		return err
	}
	numericID, err := a.resolveNumericProjectID(ctx, projectPath)
	if err != nil {
		return err
	}

	return a.putJSON(ctx, fmt.Sprintf("/api/v4/projects/%d/issues/%d", numericID, iid), map[string]any{"state_event": mapped}, nil)
}

func (a *GitlabAdapter) AddComment(ctx context.Context, externalID string, body string) error {
	projectPath, iid, err := parseExternalID(externalID)
	if err != nil {
		return err
	}
	numericID, err := a.resolveNumericProjectID(ctx, projectPath)
	if err != nil {
		return err
	}

	return a.postJSON(ctx, fmt.Sprintf("/api/v4/projects/%d/issues/%d/notes", numericID, iid), map[string]any{"body": body}, nil)
}

func (a *GitlabAdapter) OnEvent(ctx context.Context, event domain.SystemEvent) error {
	switch domain.EventType(event.EventType) {
	case domain.EventPlanApproved:
		if err := a.onPlanApproved(ctx, event.Payload); err != nil {
			return err
		}
		ids := extractExternalIDs(event.Payload, "gl:")
		for _, id := range ids {
			if updateErr := a.UpdateState(ctx, id, domain.TrackerStateInProgress); updateErr != nil {
				slog.Warn("gitlab: failed to update tracker state to in_progress", "error", updateErr, "external_id", id)
			}
		}
		return nil
	case domain.EventWorkItemCompleted:
		ids := extractExternalIDs(event.Payload, "gl:")
		for _, id := range ids {
			if updateErr := a.UpdateState(ctx, id, domain.TrackerStateInReview); updateErr != nil {
				slog.Warn("gitlab: failed to update tracker state to in_review", "error", updateErr, "external_id", id)
			}
		}
		return nil
	case domain.EventSubPlanPRReady:
		// EventSubPlanPRReady is handled by the glab adapter via repo-lifecycle routing.
		// The gitlab adapter itself does not have a handler since it relies on the glab
		// adapter (registered as a repo-lifecycle adapter) for MR lifecycle management.
		return nil
	default:
		return nil
	}
}

func (a *GitlabAdapter) onPlanApproved(ctx context.Context, payload string) error {
	commentBody, externalIDs, repoScopes := extractPlanCommentPayload(payload)

	for _, externalID := range externalIDs {
		if !strings.HasPrefix(externalID, "gl:") {
			continue
		}
		if !a.shouldPerformIssueAction(ctx, externalID, repoScopes) {
			continue
		}

		// Existing: post plan comment
		if strings.TrimSpace(commentBody) != "" {
			if err := a.AddComment(ctx, externalID, commentBody); err != nil {
				slog.Warn("gitlab: post plan comment failed", "id", externalID, "error", err)
			}
		}

		// Assign issue to configured user
		if err := a.assignIssueToCurrentUser(ctx, externalID); err != nil {
			slog.Warn("gitlab: assign issue failed", "id", externalID, "error", err)
		}

		// Transition to in-progress status via GraphQL
		if strings.TrimSpace(a.cfg.InProgressStatus) != "" && a.checkGraphQLSupport() {
			if err := a.transitionIssueStatus(ctx, externalID, a.cfg.InProgressStatus); err != nil {
				slog.Warn("gitlab: transition issue status failed", "id", externalID, "error", err)
			}
		}
	}
	return nil
}

// shouldPerformIssueAction returns true if plan-approval actions (comment, assign,
// status transition) should be applied to the given external ID.
func (a *GitlabAdapter) shouldPerformIssueAction(ctx context.Context, externalID string, repoScopes map[string]string) bool {
	repoKey := extractProjectPathFromExternalID(externalID)
	if repoKey == "" {
		return true
	}
	scopeStr, ok := repoScopes[repoKey]
	if !ok {
		return true // Default to performing actions
	}
	switch config.IssueActionScope(scopeStr) {
	case config.IssueActionScopeNone:
		return false
	case config.IssueActionScopeMine:
		return a.isOwnNamespace(ctx, externalID)
	default:
		return true
	}
}

// extractProjectPathFromExternalID extracts the project path from a GitLab external ID.
func extractProjectPathFromExternalID(externalID string) string {
	projectPath, _, err := parseExternalID(externalID)
	if err != nil {
		return ""
	}
	return projectPath
}

// assignIssueToCurrentUser assigns the issue to the configured assignee.
// Best-effort: errors are logged but never returned.
func (a *GitlabAdapter) assignIssueToCurrentUser(ctx context.Context, externalID string) error {
	assignee := strings.TrimSpace(a.cfg.Assignee)
	if assignee == "" || assignee == "me" {
		return nil
	}
	projectPath, iid, err := parseExternalID(externalID)
	if err != nil {
		return err
	}
	proj, err := a.fetchProjectByPath(ctx, projectPath)
	if err != nil {
		return err
	}
	var users []struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
	}
	if err := a.getJSON(ctx, "/api/v4/users", url.Values{"username": {assignee}}, &users); err != nil {
		return err
	}
	if len(users) == 0 {
		return fmt.Errorf("gitlab: user %q not found", assignee)
	}
	return a.putJSON(ctx,
		fmt.Sprintf("/api/v4/projects/%d/issues/%d", proj.ID, iid),
		map[string]any{"assignee_ids": []int64{users[0].ID}},
		nil,
	)
}

// transitionIssueStatus transitions the GitLab Work Item to the named status via GraphQL.
// It resolves the status name to a Global ID using the pre-fetched status cache.
// Best-effort: errors are logged but never returned.
func (a *GitlabAdapter) transitionIssueStatus(ctx context.Context, externalID, statusName string) error {
	projectPath, iid, err := parseExternalID(externalID)
	if err != nil {
		return err
	}
	// Skip legacy numeric project IDs; GraphQL requires a path like "group/project".
	if _, parseErr := strconv.ParseInt(projectPath, 10, 64); parseErr == nil {
		slog.Debug("gitlab: transitionIssueStatus: skipping legacy numeric project ID", "projectPath", projectPath)
		return nil
	}

	// Resolve status name to Global ID from cache
	statusID := a.resolveStatusID(projectPath, statusName)
	if statusID == "" {
		// Cache miss: resolve asynchronously for next time, but try to build a quick query
		go func() {
			a.resolveAndCacheStatusIDs(context.Background(), projectPath)
		}()
		return fmt.Errorf("gitlab: status %q not cached for project %s", statusName, projectPath)
	}

	workItemID, err := a.resolveWorkItemID(ctx, projectPath, iid)
	if err != nil {
		return err
	}

	mutation := `mutation updateWorkItemStatus($id: WorkItemID!, $statusID: WorkItemsStatusesStatusID!) {
		workItemUpdate(input: {id: $id, statusWidget: {status: $statusID}}) {
			errors
		}
	}`
	vars := map[string]any{
		"id":       workItemID,
		"statusID": statusID,
	}

	type respType struct {
		Data struct {
			WorkItemUpdate struct {
				Errors []string `json:"errors"`
			} `json:"workItemUpdate"`
		} `json:"data"`
	}
	var resp respType
	if err := a.doJSON(ctx, http.MethodPost, "/api/graphql", nil, map[string]any{
		"query":     mutation,
		"variables": vars,
	}, &resp); err != nil {
		return err
	}
	if len(resp.Data.WorkItemUpdate.Errors) > 0 {
		return fmt.Errorf("gitlab graphql: %s", resp.Data.WorkItemUpdate.Errors[0])
	}
	return nil
}

// resolveWorkItemID fetches the Global ID for a work item given its project path and IID.
func (a *GitlabAdapter) resolveWorkItemID(ctx context.Context, projectPath string, iid int64) (string, error) {
	query := `query workItemID($fullPath: ID!, $iid: String!) {
		project(fullPath: $fullPath) {
			workItems(iids: [$iid]) {
				nodes { id }
			}
		}
	}`
	type respType struct {
		Data struct {
			Project struct {
				WorkItems struct {
					Nodes []struct {
						ID string `json:"id"`
					} `json:"nodes"`
				} `json:"workItems"`
			} `json:"project"`
		} `json:"data"`
	}
	var resp respType
	if err := a.doJSON(ctx, http.MethodPost, "/api/graphql", nil, map[string]any{
		"query": query,
		"variables": map[string]any{
			"fullPath": projectPath,
			"iid":      strconv.FormatInt(iid, 10),
		},
	}, &resp); err != nil {
		return "", err
	}
	if len(resp.Data.Project.WorkItems.Nodes) == 0 || resp.Data.Project.WorkItems.Nodes[0].ID == "" {
		return "", fmt.Errorf("gitlab: work item not found: %s#%d", projectPath, iid)
	}
	return resp.Data.Project.WorkItems.Nodes[0].ID, nil
}

// resolveStatusID looks up the Global ID for a status name in the cache.
// Returns "" if not yet cached.
func (a *GitlabAdapter) resolveStatusID(projectPath, statusName string) string {
	a.statusCacheMu.RLock()
	defer a.statusCacheMu.RUnlock()
	if a.statusCache.byProject == nil {
		return ""
	}
	byType, ok := a.statusCache.byProject[projectPath]
	if !ok {
		return ""
	}
	// Search all work item types for the status name
	for _, byName := range byType {
		if id, ok := byName[statusName]; ok {
			return id
		}
	}
	return ""
}

// warmStatusCacheForAllProjects fetches status IDs for all projects that have
// sessions in the workspace. Called on startup and whenever GraphQL becomes available.
func (a *GitlabAdapter) warmStatusCacheForAllProjects(ctx context.Context) {
	if a.repos.Sessions == nil {
		return
	}
	if !a.checkGraphQLSupport() {
		return
	}
	// List all sessions to discover unique project paths.
	sessions, err := a.repos.Sessions.List(ctx, repository.SessionFilter{})
	if err != nil {
		slog.Warn("gitlab: warmStatusCache: list sessions failed", "error", err)
		return
	}
	// Collect unique project paths from external IDs.
	projectPaths := make([]string, 0, len(sessions))
	seen := make(map[string]struct{})
	for _, sess := range sessions {
		if !strings.HasPrefix(sess.ExternalID, "gl:") {
			continue
		}
		path, _, err := parseExternalID(sess.ExternalID)
		if err != nil || path == "" || path == "0" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		projectPaths = append(projectPaths, path)
	}
	if len(projectPaths) == 0 {
		return
	}
	var wg sync.WaitGroup
	for _, pPath := range projectPaths {
		wg.Add(1)
		go func(projectPath string) {
			defer wg.Done()
			a.resolveAndCacheStatusIDs(context.Background(), projectPath)
		}(pPath)
	}
	go func() {
		wg.Wait()
		slog.Debug("gitlab: status cache warm complete", "projects", len(projectPaths))
	}()
}

// resolveAndCacheStatusIDs fetches all available statuses for all work item types
// in a project and caches them for later use.
func (a *GitlabAdapter) resolveAndCacheStatusIDs(ctx context.Context, projectPath string) {
	if !a.checkGraphQLSupport() {
		return
	}

	query := `query projectWorkItemStatuses($fullPath: ID!) {
		project(fullPath: $fullPath) {
			workItems(first: 20) {
				nodes {
					workItemType { name }
					widgets {
						type
						... on WorkItemWidgetStatus {
							status { id name }
						}
					}
				}
			}
		}
	}`

	type respType struct {
		Data struct {
			Project struct {
				WorkItems struct {
					Nodes []struct {
						WorkItemType struct {
							Name string `json:"name"`
						} `json:"workItemType"`
						Widgets []struct {
							Type   string `json:"type"`
							Status *struct {
								ID   string `json:"id"`
								Name string `json:"name"`
							} `json:"status"`
						} `json:"widgets"`
					} `json:"nodes"`
				} `json:"workItems"`
			} `json:"project"`
		} `json:"data"`
	}

	var resp respType
	if err := a.doJSON(ctx, http.MethodPost, "/api/graphql", nil, map[string]any{
		"query":     query,
		"variables": map[string]any{"fullPath": projectPath},
	}, &resp); err != nil {
		slog.Warn("gitlab: failed to resolve status IDs", "project", projectPath, "error", err)
		return
	}

	a.statusCacheMu.Lock()
	defer a.statusCacheMu.Unlock()
	// Initialize the outer map inside the lock to avoid racing with concurrent readers.
	if a.statusCache.byProject == nil {
		a.statusCache.byProject = make(map[string]map[string]map[string]string)
	}
	// Merge into any existing cache entry for this project so that repeated
	// calls (e.g., concurrent project warm-ups) do not clobber previously
	// discovered work-item types and statuses.
	byType, ok := a.statusCache.byProject[projectPath]
	if !ok {
		byType = make(map[string]map[string]string)
	} else {
		// Make a shallow copy so the merged result is written back atomically.
		copied := make(map[string]map[string]string, len(byType))
		for k, v := range byType {
			innerCopy := make(map[string]string, len(v))
			for kk, vv := range v {
				innerCopy[kk] = vv
			}
			copied[k] = innerCopy
		}
		byType = copied
	}
	for _, node := range resp.Data.Project.WorkItems.Nodes {
		wtype := node.WorkItemType.Name
		if wtype == "" {
			continue
		}
		if _, ok := byType[wtype]; !ok {
			byType[wtype] = make(map[string]string)
		}
		for _, w := range node.Widgets {
			if w.Type == "STATUS" && w.Status != nil && w.Status.Name != "" {
				byType[wtype][w.Status.Name] = w.Status.ID
			}
		}
	}
	a.statusCache.byProject[projectPath] = byType
	slog.Debug("gitlab: cached status IDs", "project", projectPath,
		"types", len(byType),
		"totalStatuses", sumValues(byType))
}

func sumValues(m map[string]map[string]string) int {
	n := 0
	for _, inner := range m {
		n += len(inner)
	}
	return n
}

// isOwnNamespace returns true if the issue's project is owned by the authenticated user.
func (a *GitlabAdapter) isOwnNamespace(ctx context.Context, externalID string) bool {
	projectPath, _, err := parseExternalID(externalID)
	if err != nil || projectPath == "" {
		return false
	}
	// Fetch project to get namespace info
	proj, err := a.fetchProjectByPath(ctx, projectPath)
	if err != nil || proj == nil {
		return false
	}
	// For user namespaces, check if it matches the authenticated user
	if proj.Namespace.Kind == "user" {
		if err := a.resolveUsername(ctx); err != nil {
			// On error, conservatively return false rather than suppressing comments
			return false
		}
		a.usernameMu.RLock()
		username := a.username
		a.usernameMu.RUnlock()
		return proj.Namespace.Path == username
	}
	// For group namespaces, we'd need to check group membership - conservatively return false
	return false
}

// fetchProjectByPath fetches project details by path for scope checking.
func (a *GitlabAdapter) fetchProjectByPath(ctx context.Context, projectPath string) (*project, error) {
	var proj project
	encodedPath := url.PathEscape(projectPath)
	if err := a.getJSON(ctx, fmt.Sprintf("/api/v4/projects/%s", encodedPath), nil, &proj); err != nil {
		return nil, err
	}
	return &proj, nil
}

// resolveUsername fetches and caches the authenticated user's username.
func (a *GitlabAdapter) resolveUsername(ctx context.Context) error {
	// Check cache first with read lock
	a.usernameMu.RLock()
	if a.username != "" {
		a.usernameMu.RUnlock()
		return nil
	}
	a.usernameMu.RUnlock()

	// Fetch username with write lock
	a.usernameMu.Lock()
	defer a.usernameMu.Unlock()
	// Double-check after acquiring write lock
	if a.username != "" {
		return nil
	}

	var user struct {
		Username string `json:"username"`
		ID       int64  `json:"id"`
	}
	if err := a.getJSON(ctx, "/api/v4/user", nil, &user); err != nil {
		return err
	}
	a.username = user.Username
	return nil
}

// graphqlStatusEnrichment fetches Work Item status for a batch of issues and returns a map
// keyed by full reference (e.g. "group/project#123"). Returns nil on any GraphQL error so the
// caller falls back to the REST response without status. GraphQL errors are logged to the
// standard logger rather than surfaced as user-facing toasts.
func (a *GitlabAdapter) graphqlStatusEnrichment(ctx context.Context, issues []issue) statusEnrichment {
	if len(issues) == 0 || strings.TrimSpace(a.cfg.Token) == "" {
		return nil
	}
	if !a.checkGraphQLSupport() {
		return nil
	}
	enrich := make(statusEnrichment)

	// Group issues by project
	projectIssues := make(map[int64][]issue)
	for _, iss := range issues {
		if iss.References.Full == "" || iss.ProjectID == 0 {
			continue
		}
		projectIssues[iss.ProjectID] = append(projectIssues[iss.ProjectID], iss)
	}

	// Query each project in batches
	for projectID, projIssues := range projectIssues {
		iids := make([]string, len(projIssues))
		for i, iss := range projIssues {
			iids[i] = strconv.FormatInt(iss.IID, 10)
		}
		// Extract project path from first issue's references (e.g., "group/project#123" -> "group/project")
		projectPath := ""
		if len(projIssues) > 0 && projIssues[0].References.Full != "" {
			if idx := strings.LastIndex(projIssues[0].References.Full, "#"); idx > 0 {
				projectPath = projIssues[0].References.Full[:idx]
			}
		}
		statuses := a.graphqlBatchFetchStatus(ctx, projectPath, projectID, iids)
		for _, iss := range projIssues {
			if s, ok := statuses[iss.IID]; ok {
				enrich[iss.References.Full] = s
			}
		}
	}

	if len(enrich) == 0 {
		return nil
	}
	return enrich
}

// graphqlStatusFilterIssues uses the GraphQL API to enrich issues with Work Item status.
// If status is non-empty, filters to only issues matching that status.
// Returns the original slice if GraphQL fails (degrades gracefully).
// Batches requests by project for efficiency.
func (a *GitlabAdapter) graphqlStatusFilterIssues(ctx context.Context, issues []issue, status string) []issue {
	if !a.checkGraphQLSupport() {
		return issues
	}
	filterStatus := strings.TrimSpace(status)

	// Group issues by project
	projectIssues := make(map[int64][]int)
	issueMap := make(map[int64]map[int64]*issue) // projectID -> (iid -> issue pointer)
	for i, iss := range issues {
		if iss.References.Full == "" || iss.ProjectID == 0 {
			continue
		}
		projectIssues[iss.ProjectID] = append(projectIssues[iss.ProjectID], i)
		if issueMap[iss.ProjectID] == nil {
			issueMap[iss.ProjectID] = make(map[int64]*issue)
		}
		issueMap[iss.ProjectID][iss.IID] = &issues[i]
	}

	// Query each project's issues in a single batch
	for projectID, indices := range projectIssues {
		iids := make([]string, len(indices))
		for j, idx := range indices {
			iids[j] = strconv.FormatInt(issues[idx].IID, 10)
		}
		// Extract project path from first issue's references
		projectPath := ""
		if len(indices) > 0 && issues[indices[0]].References.Full != "" {
			if idx := strings.LastIndex(issues[indices[0]].References.Full, "#"); idx > 0 {
				projectPath = issues[indices[0]].References.Full[:idx]
			}
		}
		statuses := a.graphqlBatchFetchStatus(ctx, projectPath, projectID, iids)
		for _, idx := range indices {
			iss := &issues[idx]
			if s, ok := statuses[iss.IID]; ok {
				iss.Status = s
			}
		}
	}

	// Filter if needed
	if filterStatus == "" {
		return issues
	}
	filtered := make([]issue, 0, len(issues))
	for _, iss := range issues {
		if iss.Status == "" || strings.EqualFold(iss.Status, filterStatus) {
			filtered = append(filtered, iss)
		}
	}
	return filtered
}

// graphqlBatchFetchStatus fetches status for multiple IIDs in a single GraphQL query.
// projectPath is like "group/project" (e.g., "justtrack/frontend/paket").
func (a *GitlabAdapter) graphqlBatchFetchStatus(ctx context.Context, projectPath string, projectID int64, iids []string) map[int64]string {
	if len(iids) == 0 {
		return nil
	}
	query := `query ProjectWorkItems($fullPath: ID!, $iids: [String!]!) {
		project(fullPath: $fullPath) {
			workItems(iids: $iids) {
				nodes {
					iid
					widgets {
						type
						... on WorkItemWidgetStatus {
							status { name }
						}
					}
				}
			}
		}
	}`
	// Use a custom request struct for the batch query
	type batchResponse struct {
		Data struct {
			Project struct {
				WorkItems struct {
					Nodes []struct {
						IID     string `json:"iid"`
						Widgets []struct {
							Type   string `json:"type"`
							Status struct {
								Name string `json:"name"`
							} `json:"status"`
						} `json:"widgets"`
					} `json:"nodes"`
				} `json:"workItems"`
			} `json:"project"`
		} `json:"data"`
		Errors []gitlabGraphQLErrors `json:"errors,omitempty"`
	}

	var resp batchResponse
	endpoint := "/api/graphql"
	req := struct {
		Query     string `json:"query"`
		Variables struct {
			FullPath string   `json:"fullPath"`
			IIDs     []string `json:"iids"`
		} `json:"variables"`
	}{
		Query: query,
		Variables: struct {
			FullPath string   `json:"fullPath"`
			IIDs     []string `json:"iids"`
		}{
			FullPath: projectPath,
			IIDs:     iids,
		},
	}
	if err := a.doJSON(ctx, http.MethodPost, endpoint, nil, req, &resp); err != nil {
		slog.Warn("graphql: batch status fetch failed", "projectID", projectID, "error", err)
		return nil
	}
	if len(resp.Errors) > 0 {
		for _, e := range resp.Errors {
			slog.Warn("graphql: batch errors", "projectID", projectID, "message", e.Message)
		}
	}
	result := make(map[int64]string)
	for _, node := range resp.Data.Project.WorkItems.Nodes {
		iid, _ := strconv.ParseInt(node.IID, 10, 64)
		for _, w := range node.Widgets {
			if w.Type == "STATUS" {
				result[iid] = w.Status.Name
				break
			}
		}
	}
	return result
}

func (a *GitlabAdapter) listIssues(ctx context.Context, opts adapter.ListOpts) ([]issue, error) {
	query, err := gitlabIssueListQuery(opts)
	if err != nil {
		return nil, err
	}
	// Route to the project-specific endpoint when a repo is provided; the global
	// /api/v4/issues endpoint does not support filtering by project path.
	endpoint := "/api/v4/issues"
	if repo := strings.TrimSpace(opts.Repo); repo != "" {
		endpoint = "/api/v4/projects/" + url.PathEscape(repo) + "/issues"
	}
	var issues []issue
	if err := a.getJSON(ctx, endpoint, query, &issues); err != nil {
		return nil, err
	}

	// Enrich all issues with Work Item status from GraphQL for display.
	// If a status filter is requested, also filter to matching statuses.
	filterStatus := strings.TrimSpace(opts.Status)
	issues = a.graphqlStatusFilterIssues(ctx, issues, filterStatus)

	// Enrich issues with Work Item status for UI display (subtitle, details, etc.)
	enrich := a.graphqlStatusEnrichment(ctx, issues)
	for i := range issues {
		if s, ok := enrich[issues[i].References.Full]; ok {
			issues[i].Status = s
		}
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

// resolveNumericProjectID looks up the numeric project ID from a path.
// Returns the path as-is if it's already numeric (legacy format).
func (a *GitlabAdapter) resolveNumericProjectID(ctx context.Context, projectPath string) (int64, error) {
	// If already numeric, return as-is
	if numericID, err := strconv.ParseInt(projectPath, 10, 64); err == nil {
		return numericID, nil
	}

	// Otherwise, fetch project by path to get numeric ID
	var proj project
	encodedPath := url.PathEscape(projectPath)
	if err := a.getJSON(ctx, fmt.Sprintf("/api/v4/projects/%s", encodedPath), nil, &proj); err != nil {
		return 0, fmt.Errorf("resolve project %s: %w", projectPath, err)
	}
	return proj.ID, nil
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
		var permErr *adapter.PermissionError
		if errors.As(err, &permErr) && permErr.StatusCode == http.StatusForbidden {
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

	// Enrich with Work Item status from GraphQL for sidebar and source details display.
	// For fetchIssue we can't easily get the project path, so we use the project ID.
	// GraphQL should accept the numeric ID or the GID.
	if a.checkGraphQLSupport() {
		statuses := a.graphqlBatchFetchStatus(ctx, strconv.FormatInt(projectID, 10), projectID, []string{strconv.FormatInt(iid, 10)})
		if s, ok := statuses[iid]; ok {
			iss.Status = s
		}
	}

	return iss, nil
}

func (a *GitlabAdapter) issueReviewArtifacts(ctx context.Context, iss issue) []domain.ReviewArtifact {
	if iss.MergeRequestsCount <= 0 || iss.ProjectID == 0 || iss.IID == 0 {
		return nil
	}
	mrs, err := a.fetchRelatedMergeRequests(ctx, iss.ProjectID, iss.IID)
	if err != nil {
		slog.Warn("gitlab: fetch related merge requests failed", "project_id", iss.ProjectID, "issue_iid", iss.IID, "error", err)
		return nil
	}

	return gitlabReviewArtifactsFromRelatedMRs(mrs, gitlabProjectPath(iss))
}

func (a *GitlabAdapter) fetchRelatedMergeRequests(ctx context.Context, projectID, issueIID int64) ([]relatedMergeRequest, error) {
	var mrs []relatedMergeRequest
	endpoint := fmt.Sprintf("/api/v4/projects/%d/issues/%d/related_merge_requests", projectID, issueIID)
	if err := a.getJSON(ctx, endpoint, nil, &mrs); err != nil {
		return nil, err
	}

	return mrs, nil
}

func gitlabReviewArtifactsFromRelatedMRs(mrs []relatedMergeRequest, fallbackProjectPath string) []domain.ReviewArtifact {
	artifacts := make([]domain.ReviewArtifact, 0, len(mrs))
	seen := make(map[string]struct{}, len(mrs))
	for _, mr := range mrs {
		if mr.IID <= 0 {
			continue
		}
		projectPath := gitlabMergeRequestProjectPath(mr, fallbackProjectPath)
		key := projectPath + "!" + strconv.FormatInt(mr.IID, 10)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		state := gitlabRelatedMRArtifactState(mr)
		artifacts = append(artifacts, domain.ReviewArtifact{
			Provider:  adapterName,
			Kind:      "MR",
			RepoName:  projectPath,
			Ref:       fmt.Sprintf("!%d", mr.IID),
			URL:       strings.TrimSpace(mr.WebURL),
			State:     state,
			Branch:    strings.TrimSpace(mr.SourceBranch),
			Draft:     mr.Draft || mr.WorkInProgress,
			UpdatedAt: derefTime(mr.UpdatedAt),
		})
	}

	return artifacts
}

func gitlabRelatedMRArtifactState(mr relatedMergeRequest) string {
	state := strings.TrimSpace(mr.State)
	if (mr.Draft || mr.WorkInProgress) && state != "merged" && state != "closed" {
		return "draft"
	}

	return state
}

func gitlabMergeRequestProjectPath(mr relatedMergeRequest, fallback string) string {
	if strings.TrimSpace(mr.References.Full) != "" {
		parts := strings.SplitN(strings.TrimSpace(mr.References.Full), "!", 2)
		if len(parts) == 2 {
			return strings.TrimSpace(parts[0])
		}
	}

	return strings.TrimSpace(fallback)
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

// doJSONRaw performs an HTTP request and returns the raw response body bytes.
// It is used for lightweight probes where we only care about whether the endpoint
// returns JSON (any JSON), not the actual content.
func (a *GitlabAdapter) doJSONRaw(ctx context.Context, method, endpoint string, query url.Values, body any) ([]byte, error) {
	rawURL := strings.TrimRight(a.baseURL, "/") + endpoint
	fullURL, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	if query != nil {
		fullURL.RawQuery = query.Encode()
	}
	var bodyReader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = strings.NewReader(string(payload))
	}
	req, err := http.NewRequestWithContext(ctx, method, fullURL.String(), bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(a.cfg.Token))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	return data, nil
}

func (a *GitlabAdapter) doJSON(ctx context.Context, method, endpoint string, query url.Values, body any, dst any) error {
	// Build the full URL by string-concatenating base and endpoint so that
	// pre-encoded path segments (e.g. owner%2Frepo) survive intact.
	// url.Parse populates RawPath when the escaped form differs from Path,
	// and url.URL.String() uses RawPath verbatim.
	rawURL := strings.TrimRight(a.baseURL, "/") + endpoint
	fullURL, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse url: %w", err)
	}
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
		body := strings.TrimSpace(string(data))
		switch {
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			return adapter.NewPermissionError(adapterName, resp.StatusCode, body)
		case resp.StatusCode == http.StatusNotFound:
			resource := adapter.DetectGitLabResource(body)
			return adapter.NewNotFoundError(adapterName, resource, body)
		default:
			return &apiError{StatusCode: resp.StatusCode, Body: body}
		}
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
	projectPath := gitlabProjectPath(iss)
	selectionID := gitlabIssueSelectionID(gitlabIssueProjectID(iss), iss)

	// Prefer GraphQL status over REST state when available (e.g., "in_progress" vs "opened").
	trackerState := strings.TrimSpace(iss.State)
	if s := strings.TrimSpace(iss.Status); s != "" {
		trackerState = s
	}

	return domain.Session{
		ID:            domain.NewID(),
		ExternalID:    formatExternalID(projectPath, iss.IID),
		Source:        adapterName,
		SourceScope:   domain.ScopeIssues,
		SourceItemIDs: []string{selectionID},
		Title:         iss.Title,
		Description:   iss.Description,
		Labels:        append([]string(nil), iss.Labels...),
		State:         domain.SessionIngested,
		Metadata: map[string]any{
			"url":           iss.WebURL,
			"tracker_refs":  gitlabTrackerRefs([]issue{iss}),
			"tracker_state": trackerState,
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
	projectPath := gitlabProjectPath(issues[0])

	return domain.Session{
		ID:            domain.NewID(),
		ExternalID:    formatExternalID(projectPath, issues[0].IID),
		Source:        adapterName,
		SourceScope:   domain.ScopeIssues,
		SourceItemIDs: itemIDs,
		Title:         title,
		Description:   strings.Join(parts, "\n\n---\n\n"),
		Labels:        merged,
		State:         domain.SessionIngested,
		Metadata: map[string]any{
			"tracker_refs":     gitlabTrackerRefs(issues),
			"source_summaries": gitlabIssueSourceSummaries(issues),
			// Prefer GraphQL status over REST state when available.
			"tracker_state": func() string {
				for _, iss := range issues {
					if s := strings.TrimSpace(iss.Status); s != "" {
						return s
					}
				}
				return strings.TrimSpace(issues[0].State)
			}(),
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
			Provider: adapterName,
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

	return gitlabProjectPathFromIssueURL(iss.WebURL)
}

func gitlabProjectPathFromIssueURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
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

func refreshIssueProjectPath(sess domain.Session, externalProjectPath string) string {
	if path := gitlabIssueProjectPathFromSessionMetadata(sess.Metadata); path != "" {
		return path
	}
	projectPath := strings.TrimSpace(externalProjectPath)
	if strings.Contains(projectPath, "/") {
		return projectPath
	}

	return projectPath
}

func gitlabIssueProjectPathFromSessionMetadata(metadata map[string]any) string {
	if len(metadata) == 0 {
		return ""
	}
	if path := gitlabProjectPathFromIssueURL(metadataString(metadata, "url")); path != "" {
		return path
	}
	if path := metadataString(metadata, "project_path"); strings.Contains(path, "/") {
		return strings.TrimSpace(path)
	}
	if path := gitlabIssueProjectPathFromTrackerRefs(metadata["tracker_refs"]); path != "" {
		return path
	}
	if path := gitlabIssueProjectPathFromSourceSummaries(metadata["source_summaries"]); path != "" {
		return path
	}

	return ""
}

func metadataString(metadata map[string]any, key string) string {
	value, ok := metadata[key].(string)
	if !ok {
		return ""
	}

	return strings.TrimSpace(value)
}

func gitlabIssueProjectPathFromTrackerRefs(value any) string {
	switch refs := value.(type) {
	case []domain.TrackerReference:
		for _, ref := range refs {
			if ref.Provider == adapterName && ref.Kind == "issue" {
				if path := gitlabProjectPathFromIssueURL(ref.URL); path != "" {
					return path
				}
				if path := strings.TrimSpace(ref.Repo); strings.Contains(path, "/") {
					return path
				}
			}
		}
	case []any:
		for _, raw := range refs {
			ref, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if provider := metadataString(ref, "provider"); provider != "" && provider != adapterName {
				continue
			}
			if kind := metadataString(ref, "kind"); kind != "" && kind != "issue" {
				continue
			}
			if path := gitlabProjectPathFromIssueURL(metadataString(ref, "url")); path != "" {
				return path
			}
			if path := metadataString(ref, "repo"); strings.Contains(path, "/") {
				return path
			}
		}
	}

	return ""
}

func gitlabIssueProjectPathFromSourceSummaries(value any) string {
	summaries, ok := value.([]any)
	if !ok {
		return ""
	}
	for _, raw := range summaries {
		summary, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if provider := metadataString(summary, "provider"); provider != "" && provider != adapterName {
			continue
		}
		if kind := metadataString(summary, "kind"); kind != "" && kind != "issue" {
			continue
		}
		if path := gitlabProjectPathFromIssueURL(metadataString(summary, "url")); path != "" {
			return path
		}
		if path := metadataString(summary, "container"); strings.Contains(path, "/") {
			return path
		}
	}

	return ""
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

// formatExternalID creates a GitLab external ID from a project path and issue IID.
// Format: "gl:issue:{path}#{iid}"
func formatExternalID(projectPath string, iid int64) string {
	return fmt.Sprintf("gl:issue:%s#%d", projectPath, iid)
}

// parseExternalID parses a GitLab external ID and returns the project path and issue IID.
// Accepts "gl:issue:{path}#{iid}", "gl:{path}#{iid}", "gl:issue:{numeric}#{iid}", and "gl:{numeric}#{iid}".
func parseExternalID(externalID string) (projectPath string, iid int64, err error) {
	trimmed := strings.TrimSpace(externalID)
	prefix := "gl:issue:"
	if !strings.HasPrefix(trimmed, prefix) {
		prefix = "gl:"
		if !strings.HasPrefix(trimmed, prefix) {
			return "", 0, fmt.Errorf("invalid gitlab external id %q", externalID)
		}
	}
	raw := strings.TrimPrefix(trimmed, prefix)
	parts := strings.SplitN(raw, "#", 2)
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("invalid gitlab external id %q", externalID)
	}

	iid, err = strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", 0, fmt.Errorf("parse issue iid: %w", err)
	}

	// Check if parts[0] is numeric (legacy format) or path (new format)
	if numericID, parseErr := strconv.ParseInt(parts[0], 10, 64); parseErr == nil {
		// Legacy format - return the numeric ID as string for backward compatibility
		return strconv.FormatInt(numericID, 10), iid, nil
	}

	// New format - parts[0] is already a path
	return strings.TrimSpace(parts[0]), iid, nil
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
	interval, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return defaultWatchPollInterval
	}
	if interval < minPollInterval {
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

// StartStatusRefresh starts a background goroutine that periodically refreshes
// Work Item statuses for all sessions in the workspace. It runs an immediate
// refresh on startup and then repeats at the configured interval.
func (a *GitlabAdapter) StartStatusRefresh(ctx context.Context, workspaceID string) func() {
	if a.repos.Sessions == nil {
		return nil
	}
	interval := parsePollInterval(a.cfg.StatusRefreshInterval)
	// Derive a cancellable context so the goroutine is isolated from the
	// caller's lifecycle.
	refreshCtx, cancel := context.WithCancel(context.Background())
	go func() {
		// Immediate refresh on startup.
		a.refreshWorkItemStatuses(refreshCtx, workspaceID)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.refreshWorkItemStatuses(refreshCtx, workspaceID)
			}
		}
	}()
	return cancel
}

// refreshWorkItemStatuses fetches fresh Work Item status from GraphQL for all
// non-terminal sessions in the workspace and updates their metadata.
func (a *GitlabAdapter) refreshWorkItemStatuses(ctx context.Context, workspaceID string) {
	if a.repos.Sessions == nil {
		return
	}
	if !a.checkGraphQLSupport() {
		return
	}

	// List all sessions for this workspace.
	sessions, err := a.repos.Sessions.List(ctx, repository.SessionFilter{
		WorkspaceID: &workspaceID,
	})
	if err != nil {
		slog.Warn("gitlab: refresh status: list sessions failed", "workspaceID", workspaceID, "error", err)
		return
	}

	// Skip truly terminal sessions.
	skipStates := map[domain.SessionState]bool{
		domain.SessionMerged:   true,
		domain.SessionArchived: true,
	}

	// Group sessions by project for batch fetching.
	// Map structure: projectPath -> map of sessionID -> (session, iid)
	type sessionRef struct {
		session       domain.Session
		iid           string
		parsedProject string
	}
	projectSessions := make(map[string]map[string]sessionRef)
	for _, sess := range sessions {
		if skipStates[sess.State] {
			continue
		}

		// Extract projectPath and IID from ExternalID.
		projectPath, iid, err := parseExternalID(sess.ExternalID)
		if err != nil || projectPath == "" || iid == 0 {
			continue
		}
		resolvedProjectPath := refreshIssueProjectPath(sess, projectPath)
		if resolvedProjectPath == "" {
			continue
		}
		if resolvedProjectPath != projectPath {
			slog.Info("gitlab: refresh status: corrected project path", "sessionID", sess.ID, "old", projectPath, "new", resolvedProjectPath)
		}

		if projectSessions[resolvedProjectPath] == nil {
			projectSessions[resolvedProjectPath] = make(map[string]sessionRef)
		}
		projectSessions[resolvedProjectPath][sess.ID] = sessionRef{session: sess, iid: strconv.FormatInt(iid, 10), parsedProject: projectPath}
	}

	// Fetch fresh status for each project.
	for projectPath, sessions := range projectSessions {
		iids := make([]string, 0, len(sessions))
		for _, ref := range sessions {
			iids = append(iids, ref.iid)
		}

		// Fetch status using projectPath. For legacy numeric IDs, graphqlBatchFetchStatus
		// will handle conversion internally if needed.
		statuses := a.graphqlBatchFetchStatus(ctx, projectPath, 0, iids)
		if len(statuses) == 0 {
			continue
		}

		// Update session metadata with fresh status.
		for _, ref := range sessions {
			iid, _ := strconv.ParseInt(ref.iid, 10, 64)
			if status, ok := statuses[iid]; ok {
				if ref.session.Metadata == nil {
					ref.session.Metadata = map[string]any{}
				}
				ref.session.Metadata["tracker_state"] = status
				if projectPath != ref.parsedProject {
					ref.session.ExternalID = formatExternalID(projectPath, iid)
				}
				if err := a.repos.Sessions.Update(ctx, ref.session); err != nil {
					slog.Warn("gitlab: refresh status: update session failed", "sessionID", ref.session.ID, "project", projectPath, "error", err)
				}
			}
		}
	}
}
