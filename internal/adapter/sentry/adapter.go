package sentry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
)

const defaultBaseURL = config.DefaultSentryBaseURL

const providerSentry = "sentry"

type httpClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type commandRunner func(context.Context, string, []string, []string) ([]byte, error)

type SentryAdapter struct {
	cfg          config.SentryConfig
	client       httpClient
	baseURL      string
	token        string
	organization string
	projects     []string
}

type cliHTTPClient struct {
	runner    commandRunner
	rootURL   string
	apiPrefix string
}

func New(ctx context.Context, cfg config.SentryConfig) (*SentryAdapter, error) {
	return newWithDeps(ctx, cfg, &http.Client{Timeout: 30 * time.Second}, execCommandRunner)
}

func newWithClient(ctx context.Context, cfg config.SentryConfig, client httpClient) (*SentryAdapter, error) {
	return newWithDeps(ctx, cfg, client, execCommandRunner)
}

func newWithDeps(ctx context.Context, cfg config.SentryConfig, client httpClient, runner commandRunner) (*SentryAdapter, error) {
	resolved, err := config.ResolveSentryAuth(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("resolve sentry auth: %w", err)
	}
	organization := strings.TrimSpace(resolved.Organization)
	if organization == "" {
		return nil, errors.New("sentry organization is required")
	}
	transport := client
	token := strings.TrimSpace(resolved.Token)
	if token == "" {
		if !resolved.UseCLI {
			return nil, errors.New("sentry token is required")
		}
		transport = newCLIHTTPClient(resolved.BaseURL, runner)
	}

	return &SentryAdapter{
		cfg:          cfg,
		client:       transport,
		baseURL:      strings.TrimRight(strings.TrimSpace(resolved.BaseURL), "/"),
		token:        token,
		organization: organization,
		projects:     normalizeProjects(resolved.Projects),
	}, nil
}

func execCommandRunner(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // G702: command name comes from trusted config
	cmd.Env = append([]string(nil), env...)

	return cmd.CombinedOutput()
}

func newCLIHTTPClient(baseURL string, runner commandRunner) httpClient {
	parsed, _ := url.Parse(config.NormalizeSentryBaseURL(baseURL))
	apiPrefix := "/api/0"
	if parsed != nil && strings.TrimSpace(parsed.Path) != "" {
		apiPrefix = strings.TrimRight(parsed.Path, "/")
	}

	return &cliHTTPClient{
		runner:    runner,
		rootURL:   config.SentryRootURL(baseURL),
		apiPrefix: apiPrefix,
	}
}

func (c *cliHTTPClient) Do(req *http.Request) (*http.Response, error) {
	if c == nil || c.runner == nil {
		return nil, errors.New("sentry cli runner is not configured")
	}
	endpointPath := req.URL.Path
	if trimmed, ok := strings.CutPrefix(endpointPath, c.apiPrefix); ok {
		endpointPath = trimmed
	}
	if endpointPath == "" {
		endpointPath = "/"
	}
	endpoint := endpointPath
	if req.URL.RawQuery != "" {
		endpoint += "?" + req.URL.RawQuery
	}
	output, err := c.runner(req.Context(), "sentry", []string{"api", endpoint, "--include"}, config.SentryCLIEnvironment(c.rootURL))
	if err != nil {
		return nil, fmt.Errorf("sentry api %s: %w: %s", endpointPath, err, strings.TrimSpace(string(output)))
	}
	resp, parseErr := parseCLIResponse(req, output)
	if parseErr != nil {
		return nil, parseErr
	}

	return resp, nil
}

func parseCLIResponse(req *http.Request, output []byte) (*http.Response, error) {
	raw := strings.ReplaceAll(string(output), "\r\n", "\n")
	headerPart, bodyPart, found := strings.Cut(raw, "\n\n")
	if !found || !strings.HasPrefix(strings.TrimSpace(headerPart), "HTTP/") {
		return nil, fmt.Errorf("parse sentry cli response: unexpected output %q", strings.TrimSpace(raw))
	}
	lines := strings.Split(headerPart, "\n")
	if len(lines) == 0 {
		return nil, errors.New("parse sentry cli response: missing status line")
	}
	statusLine := strings.TrimSpace(lines[0])
	fields := strings.Fields(statusLine)
	if len(fields) < 2 {
		return nil, fmt.Errorf("parse sentry cli response status %q", statusLine)
	}
	statusCode, err := strconv.Atoi(fields[1])
	if err != nil {
		return nil, fmt.Errorf("parse sentry cli response status %q: %w", statusLine, err)
	}
	headers := http.Header{}
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		headers.Add(strings.TrimSpace(key), strings.TrimSpace(value))
	}

	return &http.Response{
		Status:     statusLine,
		StatusCode: statusCode,
		Header:     headers,
		Body:       io.NopCloser(strings.NewReader(bodyPart)),
		Request:    req,
	}, nil
}

func (a *SentryAdapter) Name() string { return providerSentry }

func (a *SentryAdapter) Capabilities() adapter.AdapterCapabilities {
	return adapter.AdapterCapabilities{
		CanWatch:     false,
		CanBrowse:    true,
		CanMutate:    false,
		BrowseScopes: []domain.SelectionScope{domain.ScopeIssues},
		BrowseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues: {
				Views:          []string{"assigned_to_me", "all"},
				States:         []string{"unresolved", "for_review", "regressed", "escalating", "resolved", "archived"},
				SupportsSearch: true,
				SupportsCursor: true,
				SupportsRepo:   true,
			},
		},
	}
}

func (a *SentryAdapter) ListSelectable(ctx context.Context, opts adapter.ListOpts) (*adapter.ListResult, error) {
	if opts.Scope != domain.ScopeIssues {
		return nil, adapter.ErrBrowseNotSupported
	}
	query, emptyResult, err := a.buildIssueListQuery(opts)
	if err != nil {
		return nil, err
	}
	if emptyResult {
		return &adapter.ListResult{Items: []adapter.ListItem{}}, nil
	}
	var issues []sentryIssue
	headers, err := a.getJSON(ctx, a.organization, "/issues/", query, &issues)
	if err != nil {
		return nil, err
	}
	items := make([]adapter.ListItem, 0, len(issues))
	for _, issue := range issues {
		items = append(items, issueListItem(a.organization, issue))
	}
	nextCursor, hasMore := parseNextCursor(headers.Get("Link"))

	return &adapter.ListResult{
		Items:      items,
		TotalCount: len(items),
		HasMore:    hasMore,
		NextCursor: nextCursor,
	}, nil
}

func (a *SentryAdapter) Resolve(ctx context.Context, sel adapter.Selection) (domain.Session, error) {
	if sel.Scope != domain.ScopeIssues {
		return domain.Session{}, adapter.ErrBrowseNotSupported
	}
	if len(sel.ItemIDs) == 0 {
		return domain.Session{}, errors.New("sentry resolve requires at least one issue ID")
	}
	issues := make([]sentryIssue, 0, len(sel.ItemIDs))
	for _, itemID := range sel.ItemIDs {
		issueID := strings.TrimSpace(itemID)
		if issueID == "" {
			return domain.Session{}, errors.New("sentry resolve requires non-empty issue IDs")
		}
		issue, err := a.fetchIssue(ctx, a.organization, issueID)
		if err != nil {
			return domain.Session{}, fmt.Errorf("fetch sentry issue %s: %w", issueID, err)
		}
		issues = append(issues, issue)
	}
	if len(issues) == 1 {
		return issueToWorkItem(a.organization, issues[0]), nil
	}

	return aggregateIssues(a.organization, issues), nil
}

func (a *SentryAdapter) Watch(_ context.Context, _ adapter.WorkItemFilter) (<-chan adapter.WorkItemEvent, error) {
	ch := make(chan adapter.WorkItemEvent)
	close(ch)

	return ch, nil
}

func (a *SentryAdapter) Fetch(ctx context.Context, externalID string) (domain.Session, error) {
	organization, issueID, err := parseExternalID(externalID)
	if err != nil {
		return domain.Session{}, err
	}
	issue, err := a.fetchIssue(ctx, organization, issueID)
	if err != nil {
		return domain.Session{}, err
	}

	return issueToWorkItem(organization, issue), nil
}

func (a *SentryAdapter) UpdateState(_ context.Context, _ string, _ domain.TrackerState) error {
	return nil
}

func (a *SentryAdapter) AddComment(_ context.Context, _ string, _ string) error {
	return nil
}

func (a *SentryAdapter) OnEvent(_ context.Context, _ domain.SystemEvent) error {
	return nil
}

func (a *SentryAdapter) fetchIssue(ctx context.Context, organization, issueID string) (sentryIssue, error) {
	var issue sentryIssue
	_, err := a.getJSON(ctx, organization, "/issues/"+url.PathEscape(issueID)+"/", nil, &issue)
	if err != nil {
		return sentryIssue{}, err
	}
	if strings.TrimSpace(issue.ID) == "" {
		issue.ID = strings.TrimSpace(issueID)
	}

	return issue, nil
}

func (a *SentryAdapter) getJSON(ctx context.Context, organization, path string, query url.Values, out any) (http.Header, error) {
	endpoint := a.baseURL + "/organizations/" + url.PathEscape(strings.TrimSpace(organization)) + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.token) != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))

		return nil, fmt.Errorf("sentry api %s: %s", req.URL.Path, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return nil, fmt.Errorf("decode sentry response: %w", err)
	}

	return resp.Header.Clone(), nil
}

func (a *SentryAdapter) buildIssueListQuery(opts adapter.ListOpts) (url.Values, bool, error) {
	projects, ok := scopedProjects(a.projects, opts.Repo)
	if !ok {
		return nil, true, nil
	}

	terms := make([]string, 0, 4)
	switch strings.TrimSpace(opts.View) {
	case "", "all":
	case "assigned_to_me":
		terms = append(terms, "assigned:me")
	default:
		return nil, false, fmt.Errorf("sentry issue view %q is not supported", opts.View)
	}

	switch state := strings.TrimSpace(opts.State); state {
	case "", "unresolved", "for_review", "regressed", "escalating", "resolved", "archived":
		if state != "" {
			terms = append(terms, "is:"+state)
		}
	default:
		return nil, false, fmt.Errorf("sentry issue state %q is not supported", opts.State)
	}

	if projectQuery := issueProjectQuery(projects); projectQuery != "" {
		terms = append(terms, projectQuery)
	}
	if search := strings.TrimSpace(opts.Search); search != "" {
		terms = append(terms, search)
	}

	values := url.Values{}
	if len(terms) > 0 {
		values.Set("query", strings.Join(terms, " "))
	}
	if cursor := strings.TrimSpace(opts.Cursor); cursor != "" {
		values.Set("cursor", cursor)
	}
	if limit := normalizeLimit(opts.Limit); limit > 0 {
		values.Set("limit", strconv.Itoa(limit))
	}

	return values, false, nil
}

func issueProjectQuery(projects []string) string {
	switch len(projects) {
	case 0:
		return ""
	case 1:
		return "project:" + projects[0]
	default:
		return "project:[" + strings.Join(projects, ",") + "]"
	}
}

func normalizeProjects(projects []string) []string {
	if len(projects) == 0 {
		return nil
	}
	out := make([]string, 0, len(projects))
	seen := make(map[string]struct{}, len(projects))
	for _, project := range projects {
		project = strings.TrimSpace(project)
		if project == "" {
			continue
		}
		if _, ok := seen[project]; ok {
			continue
		}
		seen[project] = struct{}{}
		out = append(out, project)
	}

	return out
}

func scopedProjects(allowlist []string, repo string) ([]string, bool) {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return append([]string(nil), allowlist...), true
	}
	if len(allowlist) == 0 {
		return []string{repo}, true
	}
	if slices.Contains(allowlist, repo) {
		return []string{repo}, true
	}

	return nil, false
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 100 {
		return 100
	}

	return limit
}

func issueListItem(organization string, issue sentryIssue) adapter.ListItem {
	return adapter.ListItem{
		ID:           issue.ID,
		Title:        strings.TrimSpace(issue.Title),
		Description:  listDescription(issue),
		State:        strings.TrimSpace(issue.Status),
		Provider:     providerSentry,
		Identifier:   issueIdentifier(issue),
		ContainerRef: issueProject(issue),
		URL:          strings.TrimSpace(issue.Permalink),
		Metadata:     issueMetadata(organization, []sentryIssue{issue}),
		CreatedAt:    issueFirstSeen(issue),
		UpdatedAt:    issueUpdatedAt(issue),
	}
}

func issueToWorkItem(organization string, issue sentryIssue) domain.Session {
	issueID := strings.TrimSpace(issue.ID)

	return domain.Session{
		ID:            domain.NewID(),
		ExternalID:    formatExternalID(organization, issueID),
		Source:        providerSentry,
		SourceScope:   domain.ScopeIssues,
		SourceItemIDs: []string{issueID},
		Title:         strings.TrimSpace(issue.Title),
		Description:   issueSection(issue),
		State:         domain.SessionIngested,
		Metadata:      issueMetadata(organization, []sentryIssue{issue}),
		CreatedAt:     issueFirstSeen(issue),
		UpdatedAt:     issueUpdatedAt(issue),
	}
}

func aggregateIssues(organization string, issues []sentryIssue) domain.Session {
	sourceIDs := make([]string, 0, len(issues))
	sections := make([]string, 0, len(issues))
	projects := make(map[string]struct{}, len(issues))
	for _, issue := range issues {
		sourceIDs = append(sourceIDs, strings.TrimSpace(issue.ID))
		sections = append(sections, issueSection(issue))
		if project := issueProject(issue); project != "" {
			projects[project] = struct{}{}
		}
	}
	title := strings.TrimSpace(issues[0].Title)
	if len(issues) > 1 {
		title = fmt.Sprintf("%s (+%d more)", title, len(issues)-1)
	}
	projectList := make([]string, 0, len(projects))
	for project := range projects {
		projectList = append(projectList, project)
	}
	sort.Strings(projectList)
	metadata := issueMetadata(organization, issues)
	if len(projectList) > 0 {
		metadata["sentry_projects"] = projectList
	}

	return domain.Session{
		ID:            domain.NewID(),
		ExternalID:    formatExternalID(organization, strings.TrimSpace(issues[0].ID)),
		Source:        providerSentry,
		SourceScope:   domain.ScopeIssues,
		SourceItemIDs: sourceIDs,
		Title:         title,
		Description:   strings.Join(sections, "\n\n---\n\n"),
		State:         domain.SessionIngested,
		Metadata:      metadata,
		CreatedAt:     issueFirstSeen(issues[0]),
		UpdatedAt:     issueUpdatedAt(issues[0]),
	}
}

func issueMetadata(organization string, issues []sentryIssue) map[string]any {
	issueIDs := make([]string, 0, len(issues))
	identifiers := make([]string, 0, len(issues))
	projectSlugs := make([]string, 0, len(issues))
	permalinks := make([]string, 0, len(issues))
	seenProjects := map[string]struct{}{}
	for _, issue := range issues {
		issueIDs = append(issueIDs, strings.TrimSpace(issue.ID))
		identifiers = append(identifiers, issueIdentifier(issue))
		if slug := strings.TrimSpace(issue.Project.Slug); slug != "" {
			if _, ok := seenProjects[slug]; !ok {
				seenProjects[slug] = struct{}{}
				projectSlugs = append(projectSlugs, slug)
			}
		}
		if permalink := strings.TrimSpace(issue.Permalink); permalink != "" {
			permalinks = append(permalinks, permalink)
		}
	}
	metadata := map[string]any{
		"sentry_organization": organization,
		"sentry_issue_ids":    issueIDs,
		"sentry_identifiers":  identifiers,
		"tracker_refs":        sentryTrackerRefs(issues),
		"source_summaries":    sentrySourceSummaries(issues),
	}
	if len(projectSlugs) > 0 {
		metadata["sentry_project_slugs"] = projectSlugs
	}
	if len(permalinks) > 0 {
		metadata["sentry_permalinks"] = permalinks
	}
	if len(issues) == 1 {
		metadata["sentry_issue_id"] = issueIDs[0]
		metadata["sentry_identifier"] = identifiers[0]
		metadata["sentry_permalink"] = strings.TrimSpace(issues[0].Permalink)
		metadata["sentry_project"] = issueProject(issues[0])
	}

	return metadata
}

func sentryTrackerRefs(issues []sentryIssue) []domain.TrackerReference {
	refs := make([]domain.TrackerReference, 0, len(issues))
	seen := make(map[string]struct{}, len(issues))
	for _, issue := range issues {
		issueID := strings.TrimSpace(issue.ID)
		if issueID == "" {
			continue
		}
		if _, ok := seen[issueID]; ok {
			continue
		}
		seen[issueID] = struct{}{}
		refs = append(refs, domain.TrackerReference{
			Provider: providerSentry,
			Kind:     "issue",
			ID:       issueIdentifier(issue),
			URL:      strings.TrimSpace(issue.Permalink),
			Repo:     strings.TrimSpace(issue.Project.Slug),
		})
	}

	return refs
}

func issueIdentifier(issue sentryIssue) string {
	if shortID := strings.TrimSpace(issue.ShortID); shortID != "" {
		return shortID
	}

	return strings.TrimSpace(issue.ID)
}

func issueProject(issue sentryIssue) string {
	if slug := strings.TrimSpace(issue.Project.Slug); slug != "" {
		return slug
	}

	return strings.TrimSpace(issue.Project.Name)
}

func listDescription(issue sentryIssue) string {
	parts := make([]string, 0, 5)
	if culprit := strings.TrimSpace(issue.Culprit); culprit != "" {
		parts = append(parts, "culprit: "+culprit)
	}
	if level := strings.TrimSpace(issue.Level); level != "" {
		parts = append(parts, "level: "+level)
	}
	if status := strings.TrimSpace(issue.Status); status != "" {
		parts = append(parts, "status: "+status)
	}
	if lastSeen := formatTime(issueUpdatedAt(issue)); lastSeen != "" {
		parts = append(parts, "last_seen: "+lastSeen)
	}
	if permalink := strings.TrimSpace(issue.Permalink); permalink != "" {
		parts = append(parts, "url: "+permalink)
	}

	return strings.Join(parts, " | ")
}

func issueSection(issue sentryIssue) string {
	lines := []string{strings.TrimSpace(issueIdentifier(issue)) + " - " + strings.TrimSpace(issue.Title)}
	if project := issueProject(issue); project != "" {
		lines = append(lines, "project: "+project)
	}
	if status := strings.TrimSpace(issue.Status); status != "" {
		lines = append(lines, "status: "+status)
	}
	if culprit := strings.TrimSpace(issue.Culprit); culprit != "" {
		lines = append(lines, "culprit: "+culprit)
	}
	if count := strings.TrimSpace(issue.Count.String()); count != "" {
		lines = append(lines, "events: "+count)
	}
	if users := strings.TrimSpace(issue.UserCount.String()); users != "" {
		lines = append(lines, "users: "+users)
	}
	if level := strings.TrimSpace(issue.Level); level != "" {
		lines = append(lines, "level: "+level)
	}
	if permalink := strings.TrimSpace(issue.Permalink); permalink != "" {
		lines = append(lines, "url: "+permalink)
	}

	return strings.Join(lines, "\n")
}

func issueFirstSeen(issue sentryIssue) time.Time {
	if issue.FirstSeen != nil {
		return issue.FirstSeen.UTC()
	}

	return time.Time{}
}

func issueUpdatedAt(issue sentryIssue) time.Time {
	if issue.LastSeen != nil {
		return issue.LastSeen.UTC()
	}

	return issueFirstSeen(issue)
}

func parseNextCursor(link string) (string, bool) {
	for part := range strings.SplitSeq(link, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		params := map[string]string{}
		for field := range strings.SplitSeq(part, ";") {
			field = strings.TrimSpace(field)
			if field == "" || strings.HasPrefix(field, "<") {
				continue
			}
			key, value, ok := strings.Cut(field, "=")
			if !ok {
				continue
			}
			params[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"`)
		}
		if params["rel"] != "next" {
			continue
		}
		if params["results"] != "true" {
			return "", false
		}
		if cursor := strings.TrimSpace(params["cursor"]); cursor != "" {
			return cursor, true
		}
	}

	return "", false
}

func formatExternalID(organization, issueID string) string {
	return "SEN-" + strings.TrimSpace(organization) + "-" + strings.TrimSpace(issueID)
}

func parseExternalID(externalID string) (string, string, error) {
	if !strings.HasPrefix(externalID, "SEN-") {
		return "", "", fmt.Errorf("invalid sentry external id %q", externalID)
	}
	rest := strings.TrimPrefix(externalID, "SEN-")
	idx := strings.LastIndex(rest, "-")
	if idx <= 0 || idx == len(rest)-1 {
		return "", "", fmt.Errorf("invalid sentry external id %q", externalID)
	}
	organization := strings.TrimSpace(rest[:idx])
	issueID := strings.TrimSpace(rest[idx+1:])
	if organization == "" || issueID == "" {
		return "", "", fmt.Errorf("invalid sentry external id %q", externalID)
	}

	return organization, issueID, nil
}

func parseSentryTime(raw string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.UTC(), nil
		}
	}

	return time.Time{}, fmt.Errorf("parse sentry time %q", raw)
}

func formatTime(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}

	return ts.UTC().Format(time.RFC3339)
}
