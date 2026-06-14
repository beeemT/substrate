package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/daemon/api"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/service"
)

const (
	autonomousLockConflictWarning = "This filter is already active in another Substrate instance."
	autonomousLeaseDuration       = 30 * time.Second
	autonomousRenewInterval       = 10 * time.Second
)

// AutonomousController owns daemon-side provider watch streams, filter locks,
// lease renewal, deduplication, and persistence of detected work items.
type AutonomousController struct {
	workspaceID string
	filters     *service.SessionFilterService
	locks       *service.SessionFilterLockService
	sessions    *service.SessionService
	state       *AutonomousState
	bus         *eventBusPublisher

	adaptersByProvider map[string]adapter.WorkItemAdapter

	mu       sync.Mutex
	runtimes map[string]*autonomousRuntime
}

type eventBusPublisher struct {
	api *API
}

func (p *eventBusPublisher) status(ctx context.Context, state *AutonomousState, workspaceID, instanceID, level, message string) {
	state.AppendStatus(instanceID, level, message)
	state.publish(ctx, p.api.eventBus(), p.api.eventPublisher(), workspaceID, instanceID, api.AutonomousModeEventStatus, map[string]any{
		"instance_id": instanceID,
		"level":       level,
		"message":     message,
	})
}

func (p *eventBusPublisher) detected(ctx context.Context, state *AutonomousState, workspaceID, instanceID, filterID string, workItem domain.Session) {
	state.RecordDetected(instanceID, filterID, workItem)
	state.publish(ctx, p.api.eventBus(), p.api.eventPublisher(), workspaceID, instanceID, api.AutonomousModeEventDetected, map[string]any{
		"instance_id": instanceID,
		"filter_id":   filterID,
		"work_item":   workItem,
	})
}

func NewAutonomousController(workspaceID string, filters *service.SessionFilterService, locks *service.SessionFilterLockService, sessions *service.SessionService, adapters []adapter.WorkItemAdapter, state *AutonomousState, publisher *eventBusPublisher) *AutonomousController {
	byProvider := make(map[string]adapter.WorkItemAdapter, len(adapters))
	for _, a := range adapters {
		if a == nil {
			continue
		}
		name := strings.TrimSpace(a.Name())
		if name == "" {
			continue
		}
		byProvider[name] = a
	}
	return &AutonomousController{
		workspaceID:        strings.TrimSpace(workspaceID),
		filters:            filters,
		locks:              locks,
		sessions:           sessions,
		state:              state,
		bus:                publisher,
		adaptersByProvider: byProvider,
		runtimes:           make(map[string]*autonomousRuntime),
	}
}

func (c *AutonomousController) Start(ctx context.Context, req api.StartAutonomousModeRequest) error {
	if c == nil {
		return nil
	}
	instanceID := strings.TrimSpace(req.InstanceID)
	if instanceID == "" {
		return errors.New("instance id is required")
	}
	if c.filters == nil || c.locks == nil || c.sessions == nil {
		return errors.New("autonomous mode services are unavailable")
	}
	if len(c.adaptersByProvider) == 0 {
		return errors.New("no provider adapters are available")
	}
	filters, err := c.loadFilters(ctx, req.WorkspaceID, req.SelectedFilterIDs)
	if err != nil {
		return err
	}
	if len(filters) == 0 {
		return errors.New("at least one Filter must be selected")
	}

	runtime := &autonomousRuntime{
		controller:       c,
		workspaceID:      strings.TrimSpace(req.WorkspaceID),
		instanceID:       instanceID,
		filtersByID:      make(map[string]domain.NewSessionFilter, len(filters)),
		activeByProvider: make(map[string][]domain.NewSessionFilter),
		activeLockIDs:    make(map[string]struct{}, len(filters)),
		seenCreated:      make(map[string]struct{}),
	}
	if runtime.workspaceID == "" {
		runtime.workspaceID = c.workspaceID
	}
	for _, filter := range filters {
		runtime.filtersByID[filter.ID] = filter
	}

	c.mu.Lock()
	if _, exists := c.runtimes[instanceID]; exists {
		c.mu.Unlock()
		if strings.TrimSpace(req.IdempotencyKey) != "" {
			return nil
		}
		return fmt.Errorf("autonomous mode is already running for instance %q", instanceID)
	}
	c.runtimes[instanceID] = runtime
	c.mu.Unlock()

	if err := runtime.start(ctx); err != nil {
		c.mu.Lock()
		delete(c.runtimes, instanceID)
		c.mu.Unlock()
		return err
	}
	return nil
}

func (c *AutonomousController) Stop(ctx context.Context, instanceID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if strings.TrimSpace(instanceID) == "" {
		runtimes := make([]*autonomousRuntime, 0, len(c.runtimes))
		for id, runtime := range c.runtimes {
			delete(c.runtimes, id)
			runtimes = append(runtimes, runtime)
		}
		c.mu.Unlock()
		for _, runtime := range runtimes {
			runtime.stop(ctx)
		}
		return
	}
	runtime := c.runtimes[instanceID]
	delete(c.runtimes, instanceID)
	c.mu.Unlock()
	if runtime != nil {
		runtime.stop(ctx)
	}
}

// UpdateDependencies points the controller at a freshly rebuilt service graph
// so new runtimes pick up the new filters, locks, sessions, and adapters
// instead of the stale ones captured at construction time.
func (c *AutonomousController) UpdateDependencies(filters *service.SessionFilterService, locks *service.SessionFilterLockService, sessions *service.SessionService, adapters []adapter.WorkItemAdapter) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.filters = filters
	c.locks = locks
	c.sessions = sessions
	byProvider := make(map[string]adapter.WorkItemAdapter, len(adapters))
	for _, a := range adapters {
		if a == nil {
			continue
		}
		name := strings.TrimSpace(a.Name())
		if name != "" {
			byProvider[name] = a
		}
	}
	c.adaptersByProvider = byProvider
}

func (c *AutonomousController) loadFilters(ctx context.Context, workspaceID string, ids []string) ([]domain.NewSessionFilter, error) {
	if len(ids) == 0 {
		return c.filters.ListByWorkspaceID(ctx, workspaceID)
	}
	ordered := append([]string(nil), ids...)
	sort.Strings(ordered)
	out := make([]domain.NewSessionFilter, 0, len(ordered))
	seen := make(map[string]struct{}, len(ordered))
	for _, id := range ordered {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		filter, err := c.filters.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if workspaceID != "" && filter.WorkspaceID != workspaceID {
			return nil, fmt.Errorf("filter %q does not belong to workspace %q", id, workspaceID)
		}
		out = append(out, filter)
	}
	return out, nil
}

type autonomousRuntime struct {
	controller  *AutonomousController
	workspaceID string
	instanceID  string

	filtersByID      map[string]domain.NewSessionFilter
	activeByProvider map[string][]domain.NewSessionFilter
	activeLockIDs    map[string]struct{}
	seenCreated      map[string]struct{}

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.Mutex
	done   chan struct{}
}

func (r *autonomousRuntime) start(ctx context.Context) error {
	r.ctx, r.cancel = context.WithCancel(context.Background())
	r.done = make(chan struct{})
	activeByProvider, lockConflict := r.acquireLocks(ctx)
	if len(activeByProvider) == 0 {
		r.cancel()
		if lockConflict {
			return errors.New(autonomousLockConflictWarning)
		}
		return errors.New("no selected Filters could be activated")
	}
	watchers := r.startProviderWatchers(activeByProvider)
	if watchers == 0 {
		r.cancel()
		r.releaseAllLocks()
		return errors.New("no provider watch streams could be started")
	}
	r.wg.Add(1)
	go r.renewLeases()
	go r.shutdownWhenCanceled()
	r.emitStatus(ctx, "info", "New Session autonomous mode started")
	return nil
}

func (r *autonomousRuntime) stop(ctx context.Context) {
	if r.cancel != nil {
		r.cancel()
	}
	if r.done == nil {
		return
	}
	select {
	case <-r.done:
	case <-ctx.Done():
		slog.Warn("autonomous mode stop timed out", "instance_id", r.instanceID, "error", ctx.Err())
	}
}

func (r *autonomousRuntime) acquireLocks(ctx context.Context) (map[string][]domain.NewSessionFilter, bool) {
	activeByProvider := make(map[string][]domain.NewSessionFilter)
	lockConflict := false
	ids := make([]string, 0, len(r.filtersByID))
	for id := range r.filtersByID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		filter := r.filtersByID[id]
		provider := strings.TrimSpace(filter.Provider)
		if provider == "" {
			r.emitStatus(ctx, "warning", fmt.Sprintf("Skipping Filter %q: provider is required.", filter.Name))
			continue
		}
		current, acquired, err := r.controller.locks.Acquire(ctx, filter.ID, r.instanceID, autonomousLeaseDuration)
		if err != nil {
			r.emitStatus(ctx, "warning", fmt.Sprintf("Failed to activate Filter %q: %v", filter.Name, err))
			continue
		}
		if !acquired {
			lockConflict = true
			if strings.TrimSpace(current.InstanceID) != "" && current.InstanceID != r.instanceID {
				r.emitStatus(ctx, "warning", autonomousLockConflictWarning)
			}
			continue
		}
		r.activeLockIDs[filter.ID] = struct{}{}
		activeByProvider[provider] = append(activeByProvider[provider], filter)
	}
	return activeByProvider, lockConflict
}

func (r *autonomousRuntime) startProviderWatchers(activeByProvider map[string][]domain.NewSessionFilter) int {
	providers := make([]string, 0, len(activeByProvider))
	for provider := range activeByProvider {
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	watchers := 0
	for _, provider := range providers {
		filters := activeByProvider[provider]
		watchAdapter := r.controller.adaptersByProvider[provider]
		if watchAdapter == nil {
			r.emitStatus(context.Background(), "warning", fmt.Sprintf("Skipping provider %q: adapter unavailable.", provider))
			r.releaseProviderFilters(filters)
			continue
		}
		if !watchAdapter.Capabilities().CanWatch {
			r.emitStatus(context.Background(), "warning", fmt.Sprintf("Skipping provider %q: watching is not supported.", provider))
			r.releaseProviderFilters(filters)
			continue
		}
		stream, err := watchAdapter.Watch(r.ctx, buildAutonomousProviderWatchFilter(r.workspaceID, filters))
		if err != nil {
			r.emitStatus(context.Background(), "warning", fmt.Sprintf("Failed to watch provider %q: %v", provider, err))
			r.releaseProviderFilters(filters)
			continue
		}
		r.mu.Lock()
		r.activeByProvider[provider] = append([]domain.NewSessionFilter(nil), filters...)
		r.mu.Unlock()
		watchers++
		r.wg.Add(1)
		go r.consumeProviderEvents(provider, watchAdapter, filters, stream)
	}
	return watchers
}

func (r *autonomousRuntime) consumeProviderEvents(provider string, watchAdapter adapter.WorkItemAdapter, filters []domain.NewSessionFilter, stream <-chan adapter.WorkItemEvent) {
	defer r.wg.Done()
	for {
		select {
		case <-r.ctx.Done():
			return
		case evt, ok := <-stream:
			if !ok {
				r.emitStatus(context.Background(), "warning", fmt.Sprintf("Autonomous mode stopped because provider %q watch stream closed.", provider))
				if r.cancel != nil {
					r.cancel()
				}
				return
			}
			if strings.ToLower(strings.TrimSpace(evt.Type)) != "created" {
				continue
			}
			if !r.markCreated(provider, evt.WorkItem.ExternalID) {
				continue
			}
			matchedFilterID, matched := firstAutonomousMatchingFilter(filters, evt.WorkItem)
			if !matched {
				continue
			}
			workItem := evt.WorkItem
			workItem.WorkspaceID = r.workspaceID
			if err := r.controller.sessions.Create(context.Background(), workItem); err != nil {
				slog.Warn("autonomous mode failed to create detected work item", "error", err, "provider", provider, "external_id", workItem.ExternalID)
				r.emitStatus(context.Background(), "warning", fmt.Sprintf("Detected %s but failed to create a session: %v", workItemDisplayLabelForAutonomous(workItem), err))
				continue
			}
			r.controller.bus.detected(context.Background(), r.controller.state, r.workspaceID, r.instanceID, matchedFilterID, workItem)
		}
	}
}

func (r *autonomousRuntime) renewLeases() {
	defer r.wg.Done()
	ticker := time.NewTicker(autonomousRenewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			filterIDs := r.activeFilterIDsSnapshot()
			for _, filterID := range filterIDs {
				current, renewed, err := r.controller.locks.Renew(context.Background(), filterID, r.instanceID, autonomousLeaseDuration)
				if err != nil {
					r.emitStatus(context.Background(), "warning", fmt.Sprintf("Failed to renew Filter lease: %v", err))
					continue
				}
				if renewed {
					continue
				}
				if strings.TrimSpace(current.InstanceID) != "" && current.InstanceID != r.instanceID {
					r.emitStatus(context.Background(), "warning", autonomousLockConflictWarning)
				}
				r.deactivateFilter(filterID)
			}
			if len(r.activeFilterIDsSnapshot()) == 0 {
				r.emitStatus(context.Background(), "warning", "Autonomous mode stopped because no Filters are active.")
				if r.cancel != nil {
					r.cancel()
				}
				return
			}
		}
	}
}

func (r *autonomousRuntime) emitStatus(ctx context.Context, level, message string) {
	r.controller.bus.status(ctx, r.controller.state, r.workspaceID, r.instanceID, level, message)
}

func (r *autonomousRuntime) markCreated(provider, externalID string) bool {
	externalID = strings.TrimSpace(externalID)
	if externalID == "" {
		return true
	}
	key := provider + "\x00" + externalID
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.seenCreated[key]; exists {
		return false
	}
	r.seenCreated[key] = struct{}{}
	return true
}

func (r *autonomousRuntime) activeFilterIDsSnapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.activeLockIDs))
	for id := range r.activeLockIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (r *autonomousRuntime) deactivateFilter(filterID string) {
	r.mu.Lock()
	delete(r.activeLockIDs, filterID)
	for provider, filters := range r.activeByProvider {
		next := make([]domain.NewSessionFilter, 0, len(filters))
		for _, filter := range filters {
			if filter.ID == filterID {
				continue
			}
			next = append(next, filter)
		}
		if len(next) == 0 {
			delete(r.activeByProvider, provider)
			continue
		}
		r.activeByProvider[provider] = next
	}
	r.mu.Unlock()
	r.releaseFilterLock(filterID)
}

func (r *autonomousRuntime) releaseProviderFilters(filters []domain.NewSessionFilter) {
	for _, filter := range filters {
		r.deactivateFilter(filter.ID)
	}
}

func (r *autonomousRuntime) releaseFilterLock(filterID string) {
	if strings.TrimSpace(filterID) == "" || r.controller.locks == nil {
		return
	}
	if err := r.controller.locks.Release(context.Background(), filterID, r.instanceID); err != nil {
		slog.Warn("failed to release New Session Filter lock", "filter_id", filterID, "error", err)
	}
}

func (r *autonomousRuntime) releaseAllLocks() {
	ids := r.activeFilterIDsSnapshot()
	r.mu.Lock()
	r.activeLockIDs = make(map[string]struct{})
	r.activeByProvider = make(map[string][]domain.NewSessionFilter)
	r.mu.Unlock()
	for _, id := range ids {
		r.releaseFilterLock(id)
	}
}

func (r *autonomousRuntime) shutdownWhenCanceled() {
	defer close(r.done)
	<-r.ctx.Done()
	r.wg.Wait()
	r.releaseAllLocks()
	r.controller.mu.Lock()
	if current := r.controller.runtimes[r.instanceID]; current == r {
		delete(r.controller.runtimes, r.instanceID)
	}
	r.controller.state.MarkStopped(context.Background(), r.controller.bus.api.eventBus(), r.controller.bus.api.eventPublisher(), r.instanceID, "autonomous mode stopped")
}

func firstAutonomousMatchingFilter(filters []domain.NewSessionFilter, workItem domain.Session) (string, bool) {
	ordered := append([]domain.NewSessionFilter(nil), filters...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	for _, filter := range ordered {
		if matchesAutonomousFilterCriteria(filter.Criteria, workItem) {
			return filter.ID, true
		}
	}
	return "", false
}

func matchesAutonomousFilterCriteria(criteria domain.NewSessionFilterCriteria, workItem domain.Session) bool {
	if criteria.Scope != "" && (workItem.SourceScope == "" || workItem.SourceScope != criteria.Scope) {
		return false
	}
	view := strings.ToLower(strings.TrimSpace(criteria.View))
	if view != "" && view != "all" {
		return false
	}
	if state := strings.TrimSpace(criteria.State); state != "" {
		actual, ok := extractAutonomousState(workItem)
		if !ok || !strings.EqualFold(actual, state) {
			return false
		}
	}
	search := strings.ToLower(strings.TrimSpace(criteria.Search))
	if search != "" {
		haystack := strings.ToLower(strings.TrimSpace(workItem.ExternalID + " " + workItem.Title + " " + workItem.Description))
		if !strings.Contains(haystack, search) {
			return false
		}
	}
	if !containsAllAutonomousLabelsFold(workItem.Labels, criteria.Labels) {
		return false
	}
	if owner := strings.TrimSpace(criteria.Owner); owner != "" {
		actual, ok := extractAutonomousOwner(workItem)
		if !ok || !strings.EqualFold(actual, owner) {
			return false
		}
	}
	if repo := strings.TrimSpace(criteria.Repository); repo != "" {
		actual, ok := extractAutonomousRepo(workItem)
		if !ok || !strings.EqualFold(actual, repo) {
			return false
		}
	}
	if group := strings.TrimSpace(criteria.Group); group != "" {
		actual, ok := extractAutonomousStringMetadata(workItem.Metadata, "group", "group_id", "groupID")
		if !ok || !strings.EqualFold(actual, group) {
			return false
		}
	}
	if teamID := strings.TrimSpace(criteria.TeamID); teamID != "" {
		actual, ok := extractAutonomousStringMetadata(workItem.Metadata, "team_id", "teamID", "team")
		if !ok || !strings.EqualFold(actual, teamID) {
			return false
		}
	}
	return true
}

func containsAllAutonomousLabelsFold(actual []string, expected []string) bool {
	if len(expected) == 0 {
		return true
	}
	if len(actual) == 0 {
		return false
	}
	actualSet := make(map[string]struct{}, len(actual))
	for _, label := range actual {
		trimmed := strings.ToLower(strings.TrimSpace(label))
		if trimmed != "" {
			actualSet[trimmed] = struct{}{}
		}
	}
	for _, label := range expected {
		trimmed := strings.ToLower(strings.TrimSpace(label))
		if trimmed == "" {
			continue
		}
		if _, ok := actualSet[trimmed]; !ok {
			return false
		}
	}
	return true
}

func buildAutonomousProviderWatchFilter(workspaceID string, filters []domain.NewSessionFilter) adapter.WorkItemFilter {
	stateSet := make(map[string]struct{})
	labelSet := make(map[string]struct{})
	teamSet := make(map[string]struct{})
	for _, filter := range filters {
		if state := strings.TrimSpace(filter.Criteria.State); state != "" {
			stateSet[state] = struct{}{}
		}
		for _, label := range filter.Criteria.Labels {
			trimmed := strings.TrimSpace(label)
			if trimmed != "" {
				labelSet[trimmed] = struct{}{}
			}
		}
		if teamID := strings.TrimSpace(filter.Criteria.TeamID); teamID != "" {
			teamSet[teamID] = struct{}{}
		}
	}
	states := make([]string, 0, len(stateSet))
	for state := range stateSet {
		states = append(states, state)
	}
	sort.Strings(states)
	labels := make([]string, 0, len(labelSet))
	for label := range labelSet {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	teamID := ""
	if len(teamSet) == 1 {
		for id := range teamSet {
			teamID = id
		}
	}
	return adapter.WorkItemFilter{WorkspaceID: strings.TrimSpace(workspaceID), TeamID: teamID, States: states, Labels: labels}
}

func extractAutonomousState(workItem domain.Session) (string, bool) {
	return extractAutonomousStringMetadata(workItem.Metadata, "tracker_state", "state", "status", "linear_state_name", "linear_state_type", "sentry_status", "sentry_issue_status")
}

func extractAutonomousOwner(workItem domain.Session) (string, bool) {
	for _, ref := range autonomousTrackerRefsFromMetadata(workItem.Metadata) {
		if owner := strings.TrimSpace(ref.Owner); owner != "" {
			return owner, true
		}
		if owner := strings.TrimSpace(ref.Repository.Owner); owner != "" {
			return owner, true
		}
	}
	return "", false
}

func extractAutonomousRepo(workItem domain.Session) (string, bool) {
	for _, ref := range autonomousTrackerRefsFromMetadata(workItem.Metadata) {
		if repo := strings.TrimSpace(ref.Repo); repo != "" {
			return repo, true
		}
		if repo := strings.TrimSpace(ref.Repository.Repo); repo != "" {
			return repo, true
		}
	}
	return "", false
}

func autonomousTrackerRefsFromMetadata(metadata map[string]any) []domain.TrackerReference {
	if len(metadata) == 0 {
		return nil
	}
	raw, ok := metadata["tracker_refs"]
	if !ok {
		return nil
	}
	switch refs := raw.(type) {
	case []domain.TrackerReference:
		return append([]domain.TrackerReference(nil), refs...)
	case []any:
		parsed := make([]domain.TrackerReference, 0, len(refs))
		for _, ref := range refs {
			switch value := ref.(type) {
			case domain.TrackerReference:
				parsed = append(parsed, value)
			case map[string]any:
				if trackerRef, ok := autonomousTrackerRefFromMap(value); ok {
					parsed = append(parsed, trackerRef)
				}
			}
		}
		return parsed
	default:
		return nil
	}
}

func autonomousTrackerRefFromMap(value map[string]any) (domain.TrackerReference, bool) {
	id, ok := autonomousAnyToString(value["id"])
	if !ok || strings.TrimSpace(id) == "" {
		return domain.TrackerReference{}, false
	}
	ref := domain.TrackerReference{ID: strings.TrimSpace(id)}
	if provider, ok := autonomousAnyToString(value["provider"]); ok {
		ref.Provider = strings.TrimSpace(provider)
	}
	if kind, ok := autonomousAnyToString(value["kind"]); ok {
		ref.Kind = strings.TrimSpace(kind)
	}
	if owner, ok := autonomousAnyToString(value["owner"]); ok {
		ref.Owner = strings.TrimSpace(owner)
	}
	if repo, ok := autonomousAnyToString(value["repo"]); ok {
		ref.Repo = strings.TrimSpace(repo)
	}
	if url, ok := autonomousAnyToString(value["url"]); ok {
		ref.URL = strings.TrimSpace(url)
	}
	if projectID, ok := autonomousAnyToInt64(value["project_id"]); ok {
		ref.ProjectID = projectID
	}
	if number, ok := autonomousAnyToInt64(value["number"]); ok {
		ref.Number = number
	}
	if repositoryRaw, ok := value["repository"].(map[string]any); ok {
		if provider, ok := autonomousAnyToString(repositoryRaw["provider"]); ok {
			ref.Repository.Provider = strings.TrimSpace(provider)
		}
		if owner, ok := autonomousAnyToString(repositoryRaw["owner"]); ok {
			ref.Repository.Owner = strings.TrimSpace(owner)
		}
		if repo, ok := autonomousAnyToString(repositoryRaw["repo"]); ok {
			ref.Repository.Repo = strings.TrimSpace(repo)
		}
		if projectID, ok := autonomousAnyToInt64(repositoryRaw["project_id"]); ok {
			ref.Repository.ProjectID = projectID
		}
		if host, ok := autonomousAnyToString(repositoryRaw["host"]); ok {
			ref.Repository.Host = strings.TrimSpace(host)
		}
		if url, ok := autonomousAnyToString(repositoryRaw["url"]); ok {
			ref.Repository.URL = strings.TrimSpace(url)
		}
	}
	return ref, true
}

func autonomousAnyToString(v any) (string, bool) {
	switch value := v.(type) {
	case string:
		return value, true
	case fmt.Stringer:
		return value.String(), true
	default:
		return "", false
	}
}

func autonomousAnyToInt64(v any) (int64, bool) {
	switch value := v.(type) {
	case int:
		return int64(value), true
	case int64:
		return value, true
	case int32:
		return int64(value), true
	case float64:
		return int64(value), true
	case float32:
		return int64(value), true
	default:
		return 0, false
	}
}

func extractAutonomousStringMetadata(metadata map[string]any, keys ...string) (string, bool) {
	if len(metadata) == 0 {
		return "", false
	}
	for _, key := range keys {
		raw, ok := metadata[key]
		if !ok {
			continue
		}
		value, ok := autonomousAnyToString(raw)
		if !ok {
			continue
		}
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed, true
		}
	}
	return "", false
}

func workItemDisplayLabelForAutonomous(wi domain.Session) string {
	if title := strings.TrimSpace(wi.Title); title != "" {
		return title
	}
	if external := strings.TrimSpace(wi.ExternalID); external != "" {
		return external
	}
	return "work item"
}
