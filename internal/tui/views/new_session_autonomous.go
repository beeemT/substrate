package views

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/service"
)

const (
	newSessionAutonomousLockConflictWarning = "This filter is already active in another Substrate instance."
	newSessionAutonomousLeaseDuration       = 30 * time.Second
	newSessionAutonomousRenewInterval       = 10 * time.Second
)

// NewSessionAutonomousRuntime coordinates provider watch streams and lock leases.
type NewSessionAutonomousRuntime struct {
	workspaceID string
	instanceID  string
	lockSvc     *service.SessionFilterLockService

	adaptersByProvider map[string]adapter.WorkItemAdapter
	activeByProvider   map[string][]domain.NewSessionFilter
	activeFilterByID   map[string]domain.NewSessionFilter
	activeLockIDs      map[string]struct{}

	events chan tea.Msg
	done   chan struct{}

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu          sync.Mutex
	seenCreated map[string]struct{}
	started     bool
}

func NewNewSessionAutonomousRuntime(
	workspaceID string,
	instanceID string,
	filters []domain.NewSessionFilter,
	adapters []adapter.WorkItemAdapter,
	lockSvc *service.SessionFilterLockService,
) *NewSessionAutonomousRuntime {
	adaptersByProvider := make(map[string]adapter.WorkItemAdapter, len(adapters))
	for _, a := range adapters {
		if a == nil {
			continue
		}
		name := strings.TrimSpace(a.Name())
		if name == "" {
			continue
		}
		adaptersByProvider[name] = a
	}

	activeFilterByID := make(map[string]domain.NewSessionFilter, len(filters))
	for _, filter := range filters {
		id := strings.TrimSpace(filter.ID)
		if id == "" {
			continue
		}
		activeFilterByID[id] = filter
	}

	return &NewSessionAutonomousRuntime{
		workspaceID:        strings.TrimSpace(workspaceID),
		instanceID:         strings.TrimSpace(instanceID),
		lockSvc:            lockSvc,
		adaptersByProvider: adaptersByProvider,
		activeByProvider:   make(map[string][]domain.NewSessionFilter),
		activeFilterByID:   activeFilterByID,
		activeLockIDs:      make(map[string]struct{}),
		events:             make(chan tea.Msg, 64),
		done:               make(chan struct{}),
		seenCreated:        make(map[string]struct{}),
	}
}

func (r *NewSessionAutonomousRuntime) Events() <-chan tea.Msg { return r.events }

func (r *NewSessionAutonomousRuntime) Start() error {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return errors.New("autonomous mode is already running")
	}
	r.mu.Unlock()

	if strings.TrimSpace(r.instanceID) == "" {
		return errors.New("instance ID is required")
	}
	if r.lockSvc == nil {
		return errors.New("filter lock service is unavailable")
	}
	if len(r.activeFilterByID) == 0 {
		return errors.New("at least one Filter must be selected")
	}

	ctx, cancel := context.WithCancel(context.Background())
	r.ctx = ctx
	r.cancel = cancel

	activeByProvider, lockConflict := r.acquireLocks()
	if len(activeByProvider) == 0 {
		cancel()
		if lockConflict {
			return errors.New(newSessionAutonomousLockConflictWarning)
		}
		return errors.New("no selected Filters could be activated")
	}

	watchers := r.startProviderWatchers(ctx, activeByProvider)
	if watchers == 0 {
		cancel()
		r.wg.Wait()
		r.releaseAllLocks()
		return errors.New("no provider watch streams could be started")
	}

	r.wg.Add(1)
	go r.renewLeases(ctx)

	go r.shutdownWhenCanceled()

	r.mu.Lock()
	r.started = true
	r.mu.Unlock()

	r.emitStatus("info", "New Session autonomous mode started")
	return nil
}

func (r *NewSessionAutonomousRuntime) Stop() error {
	r.mu.Lock()
	cancel := r.cancel
	done := r.done
	started := r.started
	r.mu.Unlock()

	if !started {
		return nil
	}
	if cancel != nil {
		cancel()
	}
	<-done
	return nil
}

func (r *NewSessionAutonomousRuntime) acquireLocks() (map[string][]domain.NewSessionFilter, bool) {
	activeByProvider := make(map[string][]domain.NewSessionFilter)
	lockConflict := false
	ids := make([]string, 0, len(r.activeFilterByID))
	for id := range r.activeFilterByID {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		filter := r.activeFilterByID[id]
		provider := strings.TrimSpace(filter.Provider)
		if provider == "" {
			r.emitStatus("warning", fmt.Sprintf("Skipping Filter %q: provider is required.", filter.Name))
			continue
		}

		current, acquired, err := r.lockSvc.Acquire(context.Background(), filter.ID, r.instanceID, newSessionAutonomousLeaseDuration)
		if err != nil {
			r.emitStatus("warning", fmt.Sprintf("Failed to activate Filter %q: %v", filter.Name, err))
			continue
		}
		if !acquired && strings.TrimSpace(current.InstanceID) != "" && current.InstanceID != r.instanceID {
			lockConflict = true
			r.emitStatus("warning", newSessionAutonomousLockConflictWarning)
			continue
		}

		r.mu.Lock()
		r.activeLockIDs[filter.ID] = struct{}{}
		r.mu.Unlock()
		activeByProvider[provider] = append(activeByProvider[provider], filter)
	}

	for provider := range activeByProvider {
		sort.SliceStable(activeByProvider[provider], func(i, j int) bool {
			return activeByProvider[provider][i].ID < activeByProvider[provider][j].ID
		})
	}
	return activeByProvider, lockConflict
}

func (r *NewSessionAutonomousRuntime) startProviderWatchers(ctx context.Context, activeByProvider map[string][]domain.NewSessionFilter) int {
	watchers := 0

	providers := make([]string, 0, len(activeByProvider))
	for provider := range activeByProvider {
		providers = append(providers, provider)
	}
	sort.Strings(providers)

	for _, provider := range providers {
		filters := activeByProvider[provider]
		watchAdapter, ok := r.adaptersByProvider[provider]
		if !ok || watchAdapter == nil {
			r.emitStatus("warning", fmt.Sprintf("No adapter configured for provider %q; skipping autonomous watch.", provider))
			r.releaseProviderFilters(filters)
			continue
		}
		if !watchAdapter.Capabilities().CanWatch {
			r.emitStatus("warning", fmt.Sprintf("Adapter %q does not support watch; skipping autonomous watch.", provider))
			r.releaseProviderFilters(filters)
			continue
		}

		watchFilter := buildProviderWatchFilter(r.workspaceID, filters)
		stream, err := watchAdapter.Watch(ctx, watchFilter)
		if err != nil {
			if errors.Is(err, adapter.ErrWatchNotSupported) {
				r.emitStatus("warning", fmt.Sprintf("Adapter %q does not support watch; skipping autonomous watch.", provider))
			} else {
				r.emitStatus("warning", fmt.Sprintf("Failed to start watch for provider %q: %v", provider, err))
			}
			r.releaseProviderFilters(filters)
			continue
		}

		r.mu.Lock()
		r.activeByProvider[provider] = append([]domain.NewSessionFilter(nil), filters...)
		r.mu.Unlock()

		watchers++
		r.wg.Add(1)
		go r.consumeProviderEvents(ctx, provider, watchAdapter, filters, stream)
	}

	return watchers
}

func (r *NewSessionAutonomousRuntime) consumeProviderEvents(
	ctx context.Context,
	provider string,
	watchAdapter adapter.WorkItemAdapter,
	filters []domain.NewSessionFilter,
	stream <-chan adapter.WorkItemEvent,
) {
	defer r.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-stream:
			if !ok {
				return
			}
			if strings.ToLower(strings.TrimSpace(evt.Type)) != "created" {
				continue
			}
			if !r.markCreated(provider, evt.WorkItem.ExternalID) {
				continue
			}

			matchedFilterID, matched := firstMatchingFilter(filters, evt.WorkItem)
			if !matched {
				continue
			}

			r.emit(NewSessionAutonomousDetectedWorkItemMsg{
				Adapter:  watchAdapter,
				FilterID: matchedFilterID,
				WorkItem: evt.WorkItem,
			})
		}
	}
}

func firstMatchingFilter(filters []domain.NewSessionFilter, workItem domain.Session) (string, bool) {
	ordered := append([]domain.NewSessionFilter(nil), filters...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	for _, filter := range ordered {
		if matchesNewSessionFilterCriteria(filter.Criteria, workItem) {
			return filter.ID, true
		}
	}
	return "", false
}

func matchesNewSessionFilterCriteria(criteria domain.NewSessionFilterCriteria, workItem domain.Session) bool {
	if criteria.Scope != "" {
		if workItem.SourceScope == "" || workItem.SourceScope != criteria.Scope {
			return false
		}
	}

	view := strings.ToLower(strings.TrimSpace(criteria.View))
	if view != "" && view != "all" {
		// Deterministic+conservative: watch events do not carry enough context to
		// prove inbox-style views such as assigned_to_me/mentioned.
		return false
	}

	if state := strings.TrimSpace(criteria.State); state != "" {
		actual, ok := extractState(workItem)
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

	if !containsAllLabelsFold(workItem.Labels, criteria.Labels) {
		return false
	}

	if owner := strings.TrimSpace(criteria.Owner); owner != "" {
		actual, ok := extractOwner(workItem)
		if !ok || !strings.EqualFold(actual, owner) {
			return false
		}
	}
	if repo := strings.TrimSpace(criteria.Repository); repo != "" {
		actual, ok := extractRepo(workItem)
		if !ok || !strings.EqualFold(actual, repo) {
			return false
		}
	}
	if group := strings.TrimSpace(criteria.Group); group != "" {
		actual, ok := extractStringMetadata(workItem.Metadata, "group", "group_id", "groupID")
		if !ok || !strings.EqualFold(actual, group) {
			return false
		}
	}
	if teamID := strings.TrimSpace(criteria.TeamID); teamID != "" {
		actual, ok := extractStringMetadata(workItem.Metadata, "team_id", "teamID", "team")
		if !ok || !strings.EqualFold(actual, teamID) {
			return false
		}
	}

	return true
}

func containsAllLabelsFold(actual []string, expected []string) bool {
	if len(expected) == 0 {
		return true
	}
	if len(actual) == 0 {
		return false
	}
	actualSet := make(map[string]struct{}, len(actual))
	for _, label := range actual {
		trimmed := strings.ToLower(strings.TrimSpace(label))
		if trimmed == "" {
			continue
		}
		actualSet[trimmed] = struct{}{}
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

func buildProviderWatchFilter(workspaceID string, filters []domain.NewSessionFilter) adapter.WorkItemFilter {
	stateSet := make(map[string]struct{})
	labelSet := make(map[string]struct{})
	teamSet := make(map[string]struct{})
	for _, filter := range filters {
		if state := strings.TrimSpace(filter.Criteria.State); state != "" {
			stateSet[state] = struct{}{}
		}
		for _, label := range filter.Criteria.Labels {
			trimmed := strings.TrimSpace(label)
			if trimmed == "" {
				continue
			}
			labelSet[trimmed] = struct{}{}
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
	return adapter.WorkItemFilter{
		WorkspaceID: strings.TrimSpace(workspaceID),
		TeamID:      teamID,
		States:      states,
		Labels:      labels,
	}
}

func extractState(workItem domain.Session) (string, bool) {
	return extractStringMetadata(workItem.Metadata,
		"tracker_state", "state", "status", "linear_state_name", "linear_state_type", "sentry_status", "sentry_issue_status",
	)
}

func extractOwner(workItem domain.Session) (string, bool) {
	for _, ref := range trackerRefsFromMetadata(workItem.Metadata) {
		if owner := strings.TrimSpace(ref.Owner); owner != "" {
			return owner, true
		}
		if owner := strings.TrimSpace(ref.Repository.Owner); owner != "" {
			return owner, true
		}
	}
	return "", false
}

func extractRepo(workItem domain.Session) (string, bool) {
	for _, ref := range trackerRefsFromMetadata(workItem.Metadata) {
		if repo := strings.TrimSpace(ref.Repo); repo != "" {
			return repo, true
		}
		if repo := strings.TrimSpace(ref.Repository.Repo); repo != "" {
			return repo, true
		}
	}
	return "", false
}

func trackerRefsFromMetadata(metadata map[string]any) []domain.TrackerReference {
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
				if trackerRef, ok := trackerRefFromMap(value); ok {
					parsed = append(parsed, trackerRef)
				}
			}
		}
		return parsed
	default:
		return nil
	}
}

func trackerRefFromMap(value map[string]any) (domain.TrackerReference, bool) {
	id, ok := anyToString(value["id"])
	if !ok || strings.TrimSpace(id) == "" {
		return domain.TrackerReference{}, false
	}
	ref := domain.TrackerReference{ID: strings.TrimSpace(id)}
	if provider, ok := anyToString(value["provider"]); ok {
		ref.Provider = strings.TrimSpace(provider)
	}
	if kind, ok := anyToString(value["kind"]); ok {
		ref.Kind = strings.TrimSpace(kind)
	}
	if owner, ok := anyToString(value["owner"]); ok {
		ref.Owner = strings.TrimSpace(owner)
	}
	if repo, ok := anyToString(value["repo"]); ok {
		ref.Repo = strings.TrimSpace(repo)
	}
	if url, ok := anyToString(value["url"]); ok {
		ref.URL = strings.TrimSpace(url)
	}
	if projectID, ok := anyToInt64(value["project_id"]); ok {
		ref.ProjectID = projectID
	}
	if number, ok := anyToInt64(value["number"]); ok {
		ref.Number = number
	}
	if repositoryRaw, ok := value["repository"].(map[string]any); ok {
		if provider, ok := anyToString(repositoryRaw["provider"]); ok {
			ref.Repository.Provider = strings.TrimSpace(provider)
		}
		if owner, ok := anyToString(repositoryRaw["owner"]); ok {
			ref.Repository.Owner = strings.TrimSpace(owner)
		}
		if repo, ok := anyToString(repositoryRaw["repo"]); ok {
			ref.Repository.Repo = strings.TrimSpace(repo)
		}
		if projectID, ok := anyToInt64(repositoryRaw["project_id"]); ok {
			ref.Repository.ProjectID = projectID
		}
		if host, ok := anyToString(repositoryRaw["host"]); ok {
			ref.Repository.Host = strings.TrimSpace(host)
		}
		if url, ok := anyToString(repositoryRaw["url"]); ok {
			ref.Repository.URL = strings.TrimSpace(url)
		}
	}
	return ref, true
}

func anyToString(v any) (string, bool) {
	switch value := v.(type) {
	case string:
		return value, true
	case fmt.Stringer:
		return value.String(), true
	default:
		return "", false
	}
}

func anyToInt64(v any) (int64, bool) {
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

func extractStringMetadata(metadata map[string]any, keys ...string) (string, bool) {
	if len(metadata) == 0 {
		return "", false
	}
	for _, key := range keys {
		raw, ok := metadata[key]
		if !ok {
			continue
		}
		value, ok := anyToString(raw)
		if !ok {
			continue
		}
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		return trimmed, true
	}
	return "", false
}

func (r *NewSessionAutonomousRuntime) renewLeases(ctx context.Context) {
	defer r.wg.Done()
	ticker := time.NewTicker(newSessionAutonomousRenewInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			filterIDs := r.activeFilterIDsSnapshot()
			for _, filterID := range filterIDs {
				current, renewed, err := r.lockSvc.Renew(context.Background(), filterID, r.instanceID, newSessionAutonomousLeaseDuration)
				if err != nil {
					r.emitStatus("warning", fmt.Sprintf("Failed to renew Filter lease: %v", err))
					continue
				}
				if renewed {
					continue
				}
				if strings.TrimSpace(current.InstanceID) != "" && current.InstanceID != r.instanceID {
					r.emitStatus("warning", newSessionAutonomousLockConflictWarning)
				}
				r.deactivateFilter(filterID)
			}
			if len(r.activeFilterIDsSnapshot()) == 0 {
				r.emitStatus("warning", "Autonomous mode stopped because no Filters are active.")
				if r.cancel != nil {
					r.cancel()
				}
				return
			}
		}
	}
}

func (r *NewSessionAutonomousRuntime) markCreated(provider, externalID string) bool {
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

func (r *NewSessionAutonomousRuntime) activeFilterIDsSnapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.activeLockIDs))
	for id := range r.activeLockIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (r *NewSessionAutonomousRuntime) deactivateFilter(filterID string) {
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

func (r *NewSessionAutonomousRuntime) releaseProviderFilters(filters []domain.NewSessionFilter) {
	for _, filter := range filters {
		r.deactivateFilter(filter.ID)
	}
}

func (r *NewSessionAutonomousRuntime) releaseFilterLock(filterID string) {
	if strings.TrimSpace(filterID) == "" || r.lockSvc == nil {
		return
	}
	if err := r.lockSvc.Release(context.Background(), filterID, r.instanceID); err != nil {
		slog.Warn("failed to release New Session Filter lock", "filter_id", filterID, "err", err)
	}
}

func (r *NewSessionAutonomousRuntime) releaseAllLocks() {
	ids := r.activeFilterIDsSnapshot()
	r.mu.Lock()
	r.activeLockIDs = make(map[string]struct{})
	r.activeByProvider = make(map[string][]domain.NewSessionFilter)
	r.mu.Unlock()
	for _, id := range ids {
		r.releaseFilterLock(id)
	}
}

func (r *NewSessionAutonomousRuntime) shutdownWhenCanceled() {
	defer close(r.done)
	defer close(r.events)

	<-r.ctx.Done()
	r.wg.Wait()
	r.releaseAllLocks()

	r.mu.Lock()
	r.started = false
	r.mu.Unlock()
}

func (r *NewSessionAutonomousRuntime) emitStatus(level, message string) {
	r.emit(NewSessionAutonomousStatusMsg{Level: level, Message: message})
}

func (r *NewSessionAutonomousRuntime) emit(msg tea.Msg) {
	if msg == nil {
		return
	}
	if r.ctx == nil {
		return
	}
	select {
	case <-r.ctx.Done():
		return
	case r.events <- msg:
	}
}
