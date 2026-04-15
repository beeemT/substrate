package linear

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
)

const (
	providerLinear           = "linear"
	filterAll                = "all"
	defaultWatchPollInterval = 5 * time.Minute
	minWatchPollInterval     = 60 * time.Second
)

// queryIssueByIdentifier fetches a single issue by its display identifier (e.g. "FOO-123").
// The identifier filter matches exactly; no team constraint so it works across team keys.
const queryIssueByIdentifier = `
query IssueByIdentifier($identifier: String!) {
	issues(filter: {
		identifier: { eq: $identifier }
	}) {
		nodes {
			id identifier title description priority url
			state { id name type }
			labels { nodes { name } }
			assignee { id name }
			team { id key }
			createdAt updatedAt
		}
	}
}`

// LinearAdapter implements adapter.WorkItemAdapter against the Linear GraphQL API.
type LinearAdapter struct {
	cfg    config.LinearConfig
	client *gqlClient
	// assigneeID is resolved from cfg.AssigneeFilter ("me" → viewer query) on first Watch.
	// Protected by single-goroutine access inside Watch; callers must call resolveAssigneeID first.
	assigneeID string
}

// New creates a LinearAdapter from the given configuration.
func New(cfg config.LinearConfig) *LinearAdapter {
	return &LinearAdapter{
		cfg:    cfg,
		client: newGQLClient(cfg.APIKey, ""),
	}
}

// Name returns the adapter identifier.
func (a *LinearAdapter) Name() string { return providerLinear }

// Capabilities describes what the Linear adapter supports.
func (a *LinearAdapter) Capabilities() adapter.AdapterCapabilities {
	return adapter.AdapterCapabilities{
		CanWatch:     true,
		CanBrowse:    true,
		CanMutate:    true,
		BrowseScopes: []domain.SelectionScope{domain.ScopeIssues, domain.ScopeProjects, domain.ScopeInitiatives},
		BrowseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{
			domain.ScopeIssues: {
				Views:          []string{"assigned_to_me", "created_by_me", "subscribed", filterAll},
				States:         []string{"open", "closed", filterAll, "triage", "backlog", "started", "unstarted", "completed", "cancelled"},
				SupportsLabels: true,
				SupportsSearch: true,
				SupportsCursor: true,
				SupportsTeam:   true,
			},
			domain.ScopeProjects: {
				States:         []string{"planned", "backlog", "started", "paused", "completed", "canceled", filterAll},
				SupportsSearch: true,
				SupportsCursor: true,
				SupportsTeam:   true,
			},
			domain.ScopeInitiatives: {
				States:         []string{"planned", "backlog", "started", "paused", "completed", "canceled", filterAll},
				SupportsSearch: true,
				SupportsCursor: true,
			},
		},
	}
}

// ListSelectable returns items available for interactive selection, dispatched by scope.
func (a *LinearAdapter) ListSelectable(ctx context.Context, opts adapter.ListOpts) (*adapter.ListResult, error) {
	switch opts.Scope {
	case domain.ScopeIssues:
		return a.listIssues(ctx, opts)
	case domain.ScopeProjects:
		return a.listProjects(ctx, opts)
	case domain.ScopeInitiatives:
		return a.listInitiatives(ctx, opts)
	default:
		return nil, adapter.ErrBrowseNotSupported
	}
}

func (a *LinearAdapter) listIssues(ctx context.Context, opts adapter.ListOpts) (*adapter.ListResult, error) {
	teamID := opts.TeamID
	if teamID == "" {
		teamID = a.cfg.TeamID
	}
	first := opts.Limit
	if first <= 0 {
		first = 50
	}
	if first > 250 {
		first = 250
	}
	vars := map[string]any{
		"teamId":     optionalString(teamID),
		"search":     optionalString(opts.Search),
		"labelNames": optionalStrings(opts.Labels),
		"stateTypes": optionalStrings(linearIssueStateTypes(opts.State)),
		"stateNames": optionalStrings(linearIssueStateNames(opts.State)),
		"first":      first,
		"after":      optionalString(opts.Cursor),
	}
	queryName := "TeamIssues"
	switch opts.View {
	case "assigned_to_me":
		assigneeID, err := a.assigneeIDForBrowse(ctx)
		if err != nil {
			return nil, err
		}
		vars["assigneeId"] = assigneeID
		queryName = "AssignedIssues"
	case "created_by_me":
		creatorID, err := a.assigneeIDForBrowse(ctx)
		if err != nil {
			return nil, err
		}
		vars["creatorId"] = creatorID
		queryName = "CreatorIssues"
	case "subscribed":
		subscriberID, err := a.assigneeIDForBrowse(ctx)
		if err != nil {
			return nil, err
		}
		vars["subscriberId"] = subscriberID
		queryName = "SubscribedIssues"
	case "", filterAll:
	default:
		return nil, fmt.Errorf("linear issue view %q is not supported", opts.View)
	}
	query := buildIssueQuery(queryName, vars)
	var resp issuesResponse
	if err := a.client.do(ctx, query, stripNilVars(vars), &resp); err != nil {
		return nil, err
	}
	items := make([]adapter.ListItem, len(resp.Issues.Nodes))
	for i, issue := range resp.Issues.Nodes {
		items[i] = adapter.ListItem{
			ID:           issue.ID,
			Title:        issue.Identifier + ": " + issue.Title,
			Description:  issue.Description,
			State:        issue.State.Name,
			Labels:       labelNames(issue.Labels),
			Identifier:   issue.Identifier,
			ContainerRef: issue.Team.Key,
			URL:          issue.URL,
			CreatedAt:    derefTime(issue.CreatedAt),
			UpdatedAt:    derefTime(issue.UpdatedAt),
		}
	}

	return &adapter.ListResult{
		Items:      items,
		TotalCount: len(items),
		HasMore:    resp.Issues.PageInfo.HasNextPage,
		NextCursor: resp.Issues.PageInfo.EndCursor,
	}, nil
}

func (a *LinearAdapter) listProjects(ctx context.Context, opts adapter.ListOpts) (*adapter.ListResult, error) {
	teamID := opts.TeamID
	if teamID == "" {
		teamID = a.cfg.TeamID
	}
	first := opts.Limit
	if first <= 0 {
		first = 50
	}
	if first > 250 {
		first = 250
	}
	var resp projectsResponse
	vars := map[string]any{
		"teamId": optionalString(teamID),
		"search": optionalString(opts.Search),
		"states": optionalStrings(linearProjectStates(opts.State)),
		"first":  first,
		"after":  optionalString(opts.Cursor),
	}
	query := buildProjectQuery(vars)
	if err := a.client.do(ctx, query, stripNilVars(vars), &resp); err != nil {
		return nil, err
	}
	items := make([]adapter.ListItem, len(resp.Projects.Nodes))
	for i, proj := range resp.Projects.Nodes {
		items[i] = adapter.ListItem{
			ID:          proj.ID,
			Title:       proj.Name,
			Description: proj.Description,
			State:       proj.State,
			CreatedAt:   derefTime(proj.CreatedAt),
			UpdatedAt:   derefTime(proj.UpdatedAt),
		}
	}

	return &adapter.ListResult{
		Items:      items,
		TotalCount: len(items),
		HasMore:    resp.Projects.PageInfo.HasNextPage,
		NextCursor: resp.Projects.PageInfo.EndCursor,
	}, nil
}

func (a *LinearAdapter) listInitiatives(ctx context.Context, opts adapter.ListOpts) (*adapter.ListResult, error) {
	first := opts.Limit
	if first <= 0 {
		first = 50
	}
	if first > 250 {
		first = 250
	}
	var resp initiativesResponse
	vars := map[string]any{
		"search":   optionalString(opts.Search),
		"statuses": optionalStrings(linearInitiativeStates(opts.State)),
		"first":    first,
		"after":    optionalString(opts.Cursor),
	}
	query := buildInitiativeQuery(vars)
	if err := a.client.do(ctx, query, stripNilVars(vars), &resp); err != nil {
		return nil, err
	}
	items := make([]adapter.ListItem, len(resp.Initiatives.Nodes))
	for i, init := range resp.Initiatives.Nodes {
		items[i] = adapter.ListItem{
			ID:          init.ID,
			Title:       init.Name,
			Description: init.Description,
			State:       init.Status,
			Labels:      initiativeProjectNames(init),
			CreatedAt:   derefTime(init.CreatedAt),
			UpdatedAt:   derefTime(init.UpdatedAt),
		}
	}

	return &adapter.ListResult{
		Items:      items,
		TotalCount: len(items),
		HasMore:    resp.Initiatives.PageInfo.HasNextPage,
		NextCursor: resp.Initiatives.PageInfo.EndCursor,
	}, nil
}

// Resolve converts a user selection into a WorkItem, aggregating multiple items when needed.
func (a *LinearAdapter) Resolve(ctx context.Context, sel adapter.Selection) (domain.Session, error) {
	switch sel.Scope {
	case domain.ScopeIssues:
		issues, err := a.fetchIssuesByIDs(ctx, sel.ItemIDs)
		if err != nil {
			return domain.Session{}, fmt.Errorf("fetch issues: %w", err)
		}
		if len(issues) == 0 {
			return domain.Session{}, fmt.Errorf("no issues found for IDs: %v", sel.ItemIDs)
		}
		if len(issues) == 1 {
			return issueToWorkItem(issues[0]), nil
		}

		return aggregateIssues(issues), nil

	case domain.ScopeProjects:
		projects := make([]linearProject, 0, len(sel.ItemIDs))
		for _, id := range sel.ItemIDs {
			proj, err := a.fetchProjectWithIssues(ctx, id)
			if err != nil {
				return domain.Session{}, fmt.Errorf("fetch project %s: %w", id, err)
			}
			projects = append(projects, proj)
		}
		names := make([]string, len(projects))
		sections := make([]string, len(projects))
		for i, proj := range projects {
			names[i] = proj.Name
			sections[i] = formatProjectSection(proj)
		}
		firstID := sel.ItemIDs[0]
		extSuffix := firstID
		if len(firstID) > 8 {
			extSuffix = firstID[:8]
		}
		metadata := linearProjectMetadata(projects, domain.TrackerReference{
			Provider: providerLinear,
			Kind:     "project",
			ID:       projects[0].ID,
		})
		metadata["source_summaries"] = linearProjectSourceSummaries(projects)

		return domain.Session{
			ID:            domain.NewID(),
			ExternalID:    "LIN-PRJ-" + extSuffix,
			Source:        providerLinear,
			SourceScope:   domain.ScopeProjects,
			SourceItemIDs: sel.ItemIDs,
			Title:         strings.Join(names, ", "),
			Description:   strings.Join(sections, "\n\n"),
			State:         domain.SessionIngested,
			Metadata:      metadata,
			CreatedAt:     domain.Now(),
			UpdatedAt:     domain.Now(),
		}, nil

	case domain.ScopeInitiatives:
		if len(sel.ItemIDs) != 1 {
			return domain.Session{}, fmt.Errorf("initiatives scope requires exactly one ID, got %d", len(sel.ItemIDs))
		}
		id := sel.ItemIDs[0]
		init, err := a.fetchInitiativeDeep(ctx, id)
		if err != nil {
			return domain.Session{}, fmt.Errorf("fetch initiative %s: %w", id, err)
		}
		extSuffix := id
		if len(id) > 8 {
			extSuffix = id[:8]
		}

		return domain.Session{
			ID:            domain.NewID(),
			ExternalID:    "LIN-INIT-" + extSuffix,
			Source:        providerLinear,
			SourceScope:   domain.ScopeInitiatives,
			SourceItemIDs: []string{id},
			Title:         init.Name,
			Description:   formatInitiativeWorkItem(init),
			State:         domain.SessionIngested,
			Metadata: linearInitiativeMetadata(init, domain.TrackerReference{
				Provider: providerLinear,
				Kind:     "initiative",
				ID:       init.ID,
			}),
			CreatedAt: domain.Now(),
			UpdatedAt: domain.Now(),
		}, nil

	default:
		return domain.Session{}, fmt.Errorf("unsupported scope: %s", sel.Scope)
	}
}

// Watch polls Linear for assigned issues and emits events when they appear or change state.
// It respects context cancellation and applies exponential backoff on rate limiting.
func (a *LinearAdapter) Watch(ctx context.Context, filter adapter.WorkItemFilter) (<-chan adapter.WorkItemEvent, error) {
	interval := resolveWatchPollInterval(a.cfg.PollInterval)
	ch := make(chan adapter.WorkItemEvent, 16)
	go func() {
		defer close(ch)
		if err := a.resolveAssigneeID(ctx); err != nil {
			ch <- adapter.WorkItemEvent{Type: "error", Timestamp: domain.Now()}

			return
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		seen := make(map[string]string) // Linear internal ID -> state ID
		backoff := interval
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				issues, err := a.fetchAssignedIssues(ctx)
				if err != nil {
					if err == ErrRateLimited {
						backoff = nextWatchBackoff(backoff, interval)
						ticker.Reset(backoff)
					} else {
						ch <- adapter.WorkItemEvent{Type: "error", WorkItem: domain.Session{}, Timestamp: domain.Now()}
					}

					continue
				}
				backoff = interval // reset on success
				ticker.Reset(backoff)
				for _, issue := range issues {
					if len(filter.States) > 0 {
						matched := false
						for _, s := range filter.States {
							if s == issue.State.Type || s == issue.State.Name {
								matched = true

								break
							}
						}
						if !matched {
							continue
						}
					}
					prev, wasSeen := seen[issue.ID]
					seen[issue.ID] = issue.State.ID
					if !wasSeen {
						ch <- adapter.WorkItemEvent{Type: "created", WorkItem: issueToWorkItem(issue), Timestamp: domain.Now()}
					} else if prev != issue.State.ID {
						ch <- adapter.WorkItemEvent{Type: "updated", WorkItem: issueToWorkItem(issue), Timestamp: domain.Now()}
					}
				}
			}
		}
	}()

	return ch, nil
}

func resolveWatchPollInterval(raw string) time.Duration {
	interval, err := time.ParseDuration(raw)
	if err != nil {
		slog.Warn("linear: invalid poll interval; using default",
			"poll_interval", raw,
			"default", defaultWatchPollInterval,
			"error", err,
		)

		return defaultWatchPollInterval
	}
	if interval < minWatchPollInterval {
		return minWatchPollInterval
	}

	return interval
}

func nextWatchBackoff(current, base time.Duration) time.Duration {
	backoff := current * 2
	maxBackoff := 10 * base
	if backoff > maxBackoff {
		return maxBackoff
	}

	return backoff
}

// Fetch retrieves a work item by its Substrate ExternalID (e.g. "LIN-FOO-123").
// It reconstructs the Linear identifier and queries by identifier.
func (a *LinearAdapter) Fetch(ctx context.Context, externalID string) (domain.Session, error) {
	identifier, err := substrateToLinearIdentifier(externalID)
	if err != nil {
		return domain.Session{}, fmt.Errorf("parse external ID: %w", err)
	}
	issue, err := a.fetchIssueByIdentifier(ctx, identifier)
	if err != nil {
		return domain.Session{}, fmt.Errorf("fetch issue by identifier %q: %w", identifier, err)
	}

	return issueToWorkItem(issue), nil
}

// UpdateState maps the Substrate TrackerState to a Linear workflow state via StateMappings
// and applies the update. If no mapping is configured for the state, the call is a no-op.
func (a *LinearAdapter) UpdateState(ctx context.Context, externalID string, state domain.TrackerState) error {
	stateID, ok := a.cfg.StateMappings[string(state)]
	if !ok {
		slog.Warn("linear: no state mapping configured; UpdateState is a no-op",
			"state", state, "external_id", externalID)

		return nil
	}
	linearID, err := a.externalIDToLinearID(ctx, externalID)
	if err != nil {
		return fmt.Errorf("resolve internal ID for %q: %w", externalID, err)
	}
	var result updateIssueStateResponse

	return a.client.do(ctx, mutationUpdateIssueState, map[string]any{
		"issueId": linearID,
		"stateId": stateID,
	}, &result)
}

// AddComment posts a comment to the Linear issue identified by the Substrate ExternalID.
// The externalID ("LIN-FOO-123") is resolved to the Linear internal UUID before mutation.
func (a *LinearAdapter) AddComment(ctx context.Context, externalID string, body string) error {
	linearID, err := a.externalIDToLinearID(ctx, externalID)
	if err != nil {
		return fmt.Errorf("resolve internal ID for %q: %w", externalID, err)
	}
	var result addCommentResponse

	return a.client.do(ctx, mutationAddComment, map[string]any{
		"issueId": linearID,
		"body":    body,
	}, &result)
}

// OnEvent reacts to system events: plan.approved → set in_progress; work_item.completed → set done.
// Malformed payloads or missing external_id are silently ignored to avoid disrupting the workflow.
func (a *LinearAdapter) OnEvent(ctx context.Context, event domain.SystemEvent) error {
	switch domain.EventType(event.EventType) {
	case domain.EventPlanApproved:
		id := extractExternalID(event.Payload)
		if id == "" || !strings.HasPrefix(id, "LIN-") {
			return nil
		}

		return a.UpdateState(ctx, id, domain.TrackerStateInProgress)
	case domain.EventWorkItemCompleted:
		id := extractExternalID(event.Payload)
		if id == "" || !strings.HasPrefix(id, "LIN-") {
			return nil
		}

		return a.UpdateState(ctx, id, domain.TrackerStateDone)
	default:
		return nil
	}
}

// extractExternalID unmarshals a JSON payload and returns the "external_id" field.
// Returns empty string if the payload is malformed or the field is absent.
func extractExternalID(payload string) string {
	var p struct {
		ExternalID string `json:"external_id"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return ""
	}

	return p.ExternalID
}

// --- Data access helpers ---

func (a *LinearAdapter) resolveAssigneeID(ctx context.Context) error {
	if a.assigneeID != "" {
		return nil // already resolved; idempotent
	}
	if a.cfg.AssigneeFilter == "" || a.cfg.AssigneeFilter == "me" {
		var resp viewerResponse
		if err := a.client.do(ctx, queryViewer, nil, &resp); err != nil {
			return fmt.Errorf("resolve viewer: %w", err)
		}
		a.assigneeID = resp.Viewer.ID
	} else {
		a.assigneeID = a.cfg.AssigneeFilter
	}

	return nil
}

func (a *LinearAdapter) fetchAssignedIssues(ctx context.Context) ([]linearIssue, error) {
	vars := map[string]any{
		"teamId":     optionalString(a.cfg.TeamID),
		"assigneeId": a.assigneeID,
		"first":      250,
	}
	query := buildIssueQuery("AssignedIssues", vars)
	var resp issuesResponse
	if err := a.client.do(ctx, query, stripNilVars(vars), &resp); err != nil {
		return nil, err
	}

	return resp.Issues.Nodes, nil
}

func (a *LinearAdapter) assigneeIDForBrowse(ctx context.Context) (string, error) {
	if a.assigneeID != "" {
		return a.assigneeID, nil
	}
	if err := a.resolveAssigneeID(ctx); err != nil {
		return "", err
	}

	return a.assigneeID, nil
}

func linearIssueStateTypes(state string) []string {
	switch state {
	case "", filterAll:
		return nil
	case "open":
		return []string{"triage", "backlog", "unstarted", "started"}
	case "closed":
		return []string{"completed", "canceled"}
	case "cancelled":
		return []string{"canceled"}
	case "triage", "backlog", "unstarted", "started", "completed", "canceled":
		return []string{state}
	default:
		return nil
	}
}

func linearIssueStateNames(state string) []string {
	if state == "" || state == filterAll || len(linearIssueStateTypes(state)) > 0 {
		return nil
	}

	return []string{state}
}

func linearProjectStates(state string) []string {
	switch state {
	case "", filterAll:
		return nil
	case "closed":
		return []string{"completed", "canceled"}
	case "open":
		return []string{"planned", "backlog", "started", "paused"}
	default:
		return []string{state}
	}
}

func linearInitiativeStates(state string) []string {
	return linearProjectStates(state)
}

func optionalString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	return value
}

func optionalStrings(values []string) any {
	if len(values) == 0 {
		return nil
	}

	return values
}

func (a *LinearAdapter) fetchIssuesByIDs(ctx context.Context, ids []string) ([]linearIssue, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var resp issuesResponse
	if err := a.client.do(ctx, queryIssuesByIDs, map[string]any{"ids": ids}, &resp); err != nil {
		return nil, err
	}

	return resp.Issues.Nodes, nil
}

func (a *LinearAdapter) fetchProjectWithIssues(ctx context.Context, id string) (linearProject, error) {
	var resp projectResponse
	if err := a.client.do(ctx, queryProjectWithIssues, map[string]any{"id": id}, &resp); err != nil {
		return linearProject{}, err
	}
	if resp.Project == nil {
		return linearProject{}, fmt.Errorf("project %q not found", id)
	}

	return *resp.Project, nil
}

func (a *LinearAdapter) fetchInitiativeDeep(ctx context.Context, id string) (linearInitiative, error) {
	var resp struct {
		Initiative *linearInitiative `json:"initiative"`
	}
	if err := a.client.do(ctx, querySingleInitiative, map[string]any{"id": id}, &resp); err != nil {
		return linearInitiative{}, err
	}
	if resp.Initiative == nil {
		return linearInitiative{}, fmt.Errorf("initiative %q not found", id)
	}

	return *resp.Initiative, nil
}

func (a *LinearAdapter) fetchIssueByIdentifier(ctx context.Context, identifier string) (linearIssue, error) {
	var resp issuesResponse
	if err := a.client.do(ctx, queryIssueByIdentifier, map[string]any{"identifier": identifier}, &resp); err != nil {
		return linearIssue{}, err
	}
	if len(resp.Issues.Nodes) == 0 {
		return linearIssue{}, fmt.Errorf("issue with identifier %q not found", identifier)
	}

	return resp.Issues.Nodes[0], nil
}

// externalIDToLinearID converts a Substrate ExternalID ("LIN-FOO-123") to the
// Linear internal UUID by reconstructing the identifier and fetching the issue.
func (a *LinearAdapter) externalIDToLinearID(ctx context.Context, externalID string) (string, error) {
	identifier, err := substrateToLinearIdentifier(externalID)
	if err != nil {
		return "", err
	}
	issue, err := a.fetchIssueByIdentifier(ctx, identifier)
	if err != nil {
		return "", err
	}

	return issue.ID, nil
}

// --- WorkItem construction ---

// issueToWorkItem converts a linearIssue to a domain.WorkItem.
// A new Substrate ID is generated on each call.
func issueToWorkItem(issue linearIssue) domain.Session {
	assigneeID := ""
	if issue.Assignee != nil {
		assigneeID = issue.Assignee.ID
	}

	return domain.Session{
		ID:            domain.NewID(),
		ExternalID:    linearExternalID(issue),
		Source:        providerLinear,
		SourceScope:   domain.ScopeIssues,
		SourceItemIDs: []string{issue.ID},
		Title:         issue.Title,
		Description:   issue.Description,
		Labels:        labelNames(issue.Labels),
		State:         domain.SessionIngested,
		AssigneeID:    assigneeID,
		Metadata: linearIssueMetadata(issue, []domain.TrackerReference{{
			Provider: providerLinear,
			Kind:     "issue",
			ID:       issue.Identifier,
			URL:      issue.URL,
		}}),
		CreatedAt: derefTime(issue.CreatedAt),
		UpdatedAt: derefTime(issue.UpdatedAt),
	}
}

// aggregateIssues merges multiple issues into a single WorkItem.
// Must be called with at least 2 issues.
func aggregateIssues(issues []linearIssue) domain.Session {
	// Deduplicate labels across all issues.
	labelSet := make(map[string]struct{})
	for _, issue := range issues {
		for _, l := range issue.Labels.Nodes {
			labelSet[l.Name] = struct{}{}
		}
	}
	labels := make([]string, 0, len(labelSet))
	for l := range labelSet {
		labels = append(labels, l)
	}

	// Concatenate non-empty descriptions.
	descs := make([]string, 0, len(issues))
	for _, issue := range issues {
		if issue.Description != "" {
			descs = append(descs, issue.Description)
		}
	}

	sourceIDs := make([]string, len(issues))
	for i, issue := range issues {
		sourceIDs[i] = issue.ID
	}

	metadata := linearIssueMetadata(issues[0], linearTrackerRefs(issues))
	metadata["source_summaries"] = linearIssueSourceSummaries(issues)

	return domain.Session{
		ID:            domain.NewID(),
		ExternalID:    linearExternalID(issues[0]),
		Source:        providerLinear,
		SourceScope:   domain.ScopeIssues,
		SourceItemIDs: sourceIDs,
		Title:         issues[0].Title + fmt.Sprintf(" (+%d more)", len(issues)-1),
		Description:   strings.Join(descs, "\n\n---\n\n"),
		Labels:        labels,
		State:         domain.SessionIngested,
		Metadata:      metadata,
		CreatedAt:     derefTime(issues[0].CreatedAt),
		UpdatedAt:     derefTime(issues[0].UpdatedAt),
	}
}

func linearTrackerRefs(issues []linearIssue) []domain.TrackerReference {
	refs := make([]domain.TrackerReference, 0, len(issues))
	seen := make(map[string]struct{}, len(issues))
	for _, issue := range issues {
		if issue.Identifier == "" {
			continue
		}
		if _, ok := seen[issue.Identifier]; ok {
			continue
		}
		seen[issue.Identifier] = struct{}{}
		refs = append(refs, domain.TrackerReference{
			Provider: providerLinear,
			Kind:     "issue",
			ID:       issue.Identifier,
			URL:      issue.URL,
		})
	}

	return refs
}

func linearIssueMetadata(issue linearIssue, trackerRefs []domain.TrackerReference) map[string]any {
	metadata := map[string]any{
		"linear_id":         issue.ID,
		"linear_url":        issue.URL,
		"linear_state_id":   issue.State.ID,
		"linear_state_name": issue.State.Name,
		"linear_state_type": issue.State.Type,
		"linear_identifier": issue.Identifier,
		"linear_team_id":    issue.Team.ID,
		"linear_team_key":   issue.Team.Key,
		"tracker_refs":      trackerRefs,
	}
	if issue.Assignee != nil {
		metadata["linear_assignee_id"] = issue.Assignee.ID
		metadata["linear_assignee_name"] = issue.Assignee.Name
	}

	return metadata
}

func linearProjectMetadata(projects []linearProject, trackerRefs ...domain.TrackerReference) map[string]any {
	projectIDs := make([]string, 0, len(projects))
	projectNames := make([]string, 0, len(projects))
	for _, proj := range projects {
		projectIDs = append(projectIDs, proj.ID)
		projectNames = append(projectNames, proj.Name)
	}
	metadata := map[string]any{
		"linear_project_ids":   projectIDs,
		"linear_project_names": projectNames,
		"tracker_refs":         trackerRefs,
	}
	if len(projects) > 0 {
		metadata["linear_project_id"] = projects[0].ID
		metadata["linear_project_name"] = projects[0].Name
		metadata["linear_project_state"] = projects[0].State
	}

	return metadata
}

func linearInitiativeMetadata(init linearInitiative, trackerRefs ...domain.TrackerReference) map[string]any {
	projectIDs := make([]string, 0, len(init.Projects.Nodes))
	projectNames := make([]string, 0, len(init.Projects.Nodes))
	for _, proj := range init.Projects.Nodes {
		projectIDs = append(projectIDs, proj.ID)
		projectNames = append(projectNames, proj.Name)
	}

	return map[string]any{
		"linear_initiative_id":     init.ID,
		"linear_initiative_name":   init.Name,
		"linear_initiative_status": init.Status,
		"linear_project_ids":       projectIDs,
		"linear_project_names":     projectNames,
		"tracker_refs":             trackerRefs,
	}
}

func initiativeProjectNames(init linearInitiative) []string {
	names := make([]string, 0, len(init.Projects.Nodes))
	for _, proj := range init.Projects.Nodes {
		if proj.Name == "" {
			continue
		}
		names = append(names, proj.Name)
	}
	if len(names) == 0 {
		return nil
	}

	return names
}

// --- Formatters ---

// formatProjectSection renders a project and its open issues as a Markdown section.
func formatProjectSection(proj linearProject) string {
	var sb strings.Builder
	sb.WriteString("## " + proj.Name + "\n\n")
	if proj.Description != "" {
		sb.WriteString(proj.Description + "\n\n")
	}
	sb.WriteString("**State:** " + proj.State + "\n\n")
	if len(proj.Issues.Nodes) > 0 {
		sb.WriteString("### Issues\n\n")
		for _, issue := range proj.Issues.Nodes {
			sb.WriteString("- " + issue.Identifier + ": " + issue.Title + " (" + issue.State.Name + ")\n")
		}
	}

	return sb.String()
}

// formatInitiativeWorkItem renders an initiative and all its projects/issues as Markdown.
func formatInitiativeWorkItem(init linearInitiative) string {
	var sb strings.Builder
	sb.WriteString("# " + init.Name + "\n\n")
	if init.Description != "" {
		sb.WriteString(init.Description + "\n\n")
	}
	sb.WriteString("**Status:** " + init.Status + "\n\n")
	if len(init.Projects.Nodes) > 0 {
		sb.WriteString("## Projects\n\n")
		for _, proj := range init.Projects.Nodes {
			sb.WriteString(formatProjectSection(proj))
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

// --- Identifier helpers ---

// linearExternalID builds the Substrate ExternalID for an issue: "LIN-{teamKey}-{num}".
func linearExternalID(issue linearIssue) string {
	return "LIN-" + issue.Team.Key + "-" + numericSuffix(issue.Identifier)
}

// numericSuffix returns the numeric part of a Linear identifier (e.g. "FOO-123" → "123").
func numericSuffix(identifier string) string {
	parts := strings.SplitN(identifier, "-", 2)
	if len(parts) < 2 {
		return identifier
	}

	return parts[1]
}

// substrateToLinearIdentifier converts "LIN-FOO-123" → "FOO-123".
// Returns an error if the format is not recognised.
func substrateToLinearIdentifier(externalID string) (string, error) {
	remainder := strings.TrimPrefix(externalID, "LIN-")
	if remainder == externalID {
		return "", fmt.Errorf("invalid external ID %q: expected LIN-TEAM-NUM", externalID)
	}
	parts := strings.SplitN(remainder, "-", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("invalid external ID %q: expected LIN-TEAM-NUM", externalID)
	}

	return parts[0] + "-" + parts[1], nil
}

// --- Misc helpers ---

// labelNames extracts label name strings from the GraphQL labels connection.
func labelNames(labels linearLabels) []string {
	if len(labels.Nodes) == 0 {
		return nil
	}
	names := make([]string, len(labels.Nodes))
	for i, l := range labels.Nodes {
		names[i] = l.Name
	}

	return names
}

// derefTime dereferences a time pointer; returns zero value if nil.
func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}

	return *t
}
