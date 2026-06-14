package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/beeemT/substrate/internal/daemon/api"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
)

// AutonomousState owns the in-memory autonomous-mode state for one daemon.
//
// In remote TUI mode the controlling client hands off New Session watch
// streams, filter locks, lease renewal, and deduplication to the daemon
// process. The state manager tracks that ownership so a reconnecting client
// can reconstruct the on-screen status from a single GetAutonomousModeStatus
// call rather than reopening locks and watch streams itself.
//
// The state manager is the minimal, Bubble-Tea-free slice. The actual
// provider watch loops and lock leases live behind a follow-up wiring step
// driven by a Controller; this manager exposes the API surface and event
// contract that follow-up slices can build on top of.
type AutonomousState struct {
	mu          sync.Mutex
	runs        map[string]*autonomousRun // keyed by instanceID
	idempotency map[string]cachedAutonomousResponse
	clock       func() time.Time
}

// autonomousRun tracks one autonomous-mode instance.
//
// The struct is private; the public envelope is api.AutonomousModeRun. We keep
// the public envelope stable so we can extend the in-memory model without
// re-plumbing the wire contract.
type autonomousRun struct {
	instanceID       string
	workspaceID      string
	running          bool
	startedAt        time.Time
	stoppedAt        time.Time
	stopReason       string
	activeFilterIDs  map[string]struct{}
	activeByProvider map[string]map[string]struct{}
	recentStatus     []api.AutonomousStatusEntry
	detected         []api.AutonomousStatusEntry
}

// cachedAutonomousResponse memoises the result of a Start/Stop call so that
// retries with the same idempotency key return the same payload.
type cachedAutonomousResponse struct {
	action string
	key    string
	value  any
}

// autonomousStatusRing is the per-run cap on the status/detected ring buffers
// surfaced through the wire contract. Small on purpose: status changes are
// short-lived and a reconnecting client only needs the most recent tail.
const autonomousStatusRing = 32

// NewAutonomousState creates an empty AutonomousState. The clock is fixed in
// tests; in production time.Now is used.
func NewAutonomousState(clock func() time.Time) *AutonomousState {
	if clock == nil {
		clock = time.Now
	}
	return &AutonomousState{
		runs:        make(map[string]*autonomousRun),
		idempotency: make(map[string]cachedAutonomousResponse),
		clock:       clock,
	}
}

// Start records the start of an autonomous-mode run for instanceID. The
// selectedFilterIDs become the initial active filter set; the run is marked
// running immediately and a "started" status entry is appended. Returns the
// wire payload describing the resulting run.
//
// Start is idempotent for retries using the same idempotencyKey against the
// same instance: subsequent calls with the same key return the cached
// response. Without a key, calling Start on an already-running instance is
// reported as an error so the caller can disambiguate.
func (s *AutonomousState) Start(ctx context.Context, bus *event.Bus, events eventServicePublisher, req api.StartAutonomousModeRequest) (api.AutonomousModeRun, error) {
	instanceID := strings.TrimSpace(req.InstanceID)
	if instanceID == "" {
		return api.AutonomousModeRun{}, errors.New("instance id is required")
	}
	workspaceID := strings.TrimSpace(req.WorkspaceID)
	if workspaceID == "" {
		return api.AutonomousModeRun{}, errors.New("workspace id is required")
	}
	if len(req.SelectedFilterIDs) == 0 {
		return api.AutonomousModeRun{}, errors.New("at least one Filter must be selected")
	}
	if cached, ok := s.lookupIdempotent("StartAutonomousMode", instanceID, req.IdempotencyKey); ok {
		return cached.(api.AutonomousModeRun), nil
	}

	s.mu.Lock()
	if run, ok := s.runs[instanceID]; ok && run.running {
		s.mu.Unlock()
		return api.AutonomousModeRun{}, fmt.Errorf("autonomous mode is already running for instance %q", instanceID)
	}
	run := s.newRunLocked(instanceID, workspaceID, req.SelectedFilterIDs)
	s.runs[instanceID] = run
	s.mu.Unlock()

	s.appendStatus(run, api.AutonomousModeEventStarted, "info", "New Session autonomous mode started")
	s.publish(ctx, bus, events, workspaceID, instanceID, api.AutonomousModeEventStarted, map[string]any{
		"instance_id":         instanceID,
		"selected_filter_ids": sortedKeys(run.activeFilterIDs),
	})

	snapshot := s.snapshotRun(run)
	s.storeIdempotent("StartAutonomousMode", instanceID, req.IdempotencyKey, snapshot)
	return snapshot, nil
}

// Stop terminates the autonomous-mode run for instanceID. With an empty
// instanceID every active run is stopped (operator override). Returns the
// snapshots of the runs that were stopped so the caller can confirm the
// state transition.
func (s *AutonomousState) Stop(ctx context.Context, bus *event.Bus, events eventServicePublisher, req api.StopAutonomousModeRequest) ([]api.AutonomousModeRun, error) {
	instanceID := strings.TrimSpace(req.InstanceID)
	if cached, ok := s.lookupIdempotent("StopAutonomousMode", instanceID, req.IdempotencyKey); ok {
		return cached.([]api.AutonomousModeRun), nil
	}
	stopped := s.stopRuns(instanceID, "user requested stop")
	for _, snap := range stopped {
		s.publish(ctx, bus, events, snap.WorkspaceID, snap.InstanceID, api.AutonomousModeEventStopped, map[string]any{
			"instance_id": snap.InstanceID,
			"stop_reason": snap.StopReason,
		})
	}
	s.storeIdempotent("StopAutonomousMode", instanceID, req.IdempotencyKey, stopped)
	return stopped, nil
}

// Status returns a snapshot of the current autonomous-mode state. When
// instanceID is empty, every tracked run is returned; otherwise only the run
// for that instance. The response envelope is the same wire type returned by
// the gRPC handler.
func (s *AutonomousState) Status(instanceID string) api.AutonomousModeStatusResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	filter := strings.TrimSpace(instanceID)
	out := api.AutonomousModeStatusResponse{Runs: make([]api.AutonomousModeRun, 0, len(s.runs))}
	for id, run := range s.runs {
		if filter != "" && filter != id {
			continue
		}
		out.Runs = append(out.Runs, s.snapshotRunLocked(run))
	}
	sort.Slice(out.Runs, func(i, j int) bool { return out.Runs[i].InstanceID < out.Runs[j].InstanceID })
	for _, run := range out.Runs {
		if run.Running {
			out.ActiveCount++
		}
	}
	out.Running = out.ActiveCount > 0
	return out
}

// AppendStatus records a status entry on a tracked run. Returns false when
// the instance is unknown so the caller can decide how to react.
func (s *AutonomousState) AppendStatus(instanceID, level, message string) bool {
	s.mu.Lock()
	run, ok := s.runs[strings.TrimSpace(instanceID)]
	s.mu.Unlock()
	if !ok {
		return false
	}
	s.appendStatus(run, api.AutonomousModeEventStatus, level, message)
	return true
}

// RecordDetected records a work item detection on a tracked run.
func (s *AutonomousState) RecordDetected(instanceID, filterID string, payload any) bool {
	s.mu.Lock()
	run, ok := s.runs[strings.TrimSpace(instanceID)]
	s.mu.Unlock()
	if !ok {
		return false
	}
	s.appendDetected(run, filterID, payload)
	return true
}

// MarkStopped transitions a run out of the running state with the given
// reason. The reason is published as a stop event so reconnecting clients
// observe the cause.
func (s *AutonomousState) MarkStopped(ctx context.Context, bus *event.Bus, events eventServicePublisher, instanceID, reason string) bool {
	stopped := s.stopRuns(instanceID, reason)
	if len(stopped) == 0 {
		return false
	}
	for _, snap := range stopped {
		s.publish(ctx, bus, events, snap.WorkspaceID, snap.InstanceID, api.AutonomousModeEventStopped, map[string]any{
			"instance_id": snap.InstanceID,
			"stop_reason": snap.StopReason,
		})
	}
	return true
}

// Close stops every active run with the given reason. Used during daemon
// shutdown so reconnects do not see ghost runs.
func (s *AutonomousState) Close(ctx context.Context, bus *event.Bus, events eventServicePublisher, reason string) {
	stopped := s.stopRuns("", reason)
	for _, snap := range stopped {
		s.publish(ctx, bus, events, snap.WorkspaceID, snap.InstanceID, api.AutonomousModeEventStopped, map[string]any{
			"instance_id": snap.InstanceID,
			"stop_reason": snap.StopReason,
		})
	}
}

func (s *AutonomousState) newRunLocked(instanceID, workspaceID string, filterIDs []string) *autonomousRun {
	activeFilterIDs := make(map[string]struct{}, len(filterIDs))
	activeByProvider := make(map[string]map[string]struct{})
	for _, id := range filterIDs {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		activeFilterIDs[trimmed] = struct{}{}
		// The provider is resolved by the Controller when filters are mapped
		// to adapters. We seed the empty-provider bucket so the snapshot
		// shape is stable even before the controller is wired.
		if _, ok := activeByProvider[""]; !ok {
			activeByProvider[""] = make(map[string]struct{})
		}
		activeByProvider[""][trimmed] = struct{}{}
	}
	return &autonomousRun{
		instanceID:       instanceID,
		workspaceID:      workspaceID,
		running:          true,
		startedAt:        s.clock(),
		activeFilterIDs:  activeFilterIDs,
		activeByProvider: activeByProvider,
		recentStatus:     make([]api.AutonomousStatusEntry, 0, autonomousStatusRing),
		detected:         make([]api.AutonomousStatusEntry, 0, autonomousStatusRing),
	}
}

func (s *AutonomousState) stopRuns(instanceID, reason string) []api.AutonomousModeRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	filter := strings.TrimSpace(instanceID)
	out := make([]api.AutonomousModeRun, 0, len(s.runs))
	for id, run := range s.runs {
		if filter != "" && filter != id {
			continue
		}
		if !run.running {
			continue
		}
		run.running = false
		run.stoppedAt = s.clock()
		run.stopReason = reason
		s.appendStatusLocked(run, api.AutonomousModeEventStopped, "info", reason)
		out = append(out, s.snapshotRunLocked(run))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].InstanceID < out[j].InstanceID })
	return out
}

func (s *AutonomousState) appendStatus(run *autonomousRun, kind, level, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendStatusLocked(run, kind, level, message)
}

func (s *AutonomousState) appendStatusLocked(run *autonomousRun, kind, level, message string) {
	entry := api.AutonomousStatusEntry{
		Kind:      kind,
		Level:     level,
		Message:   message,
		Timestamp: s.clock(),
	}
	run.recentStatus = appendRing(run.recentStatus, entry, autonomousStatusRing)
}

func (s *AutonomousState) appendDetected(run *autonomousRun, filterID string, payload any) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		encoded = []byte(fmt.Sprintf(`{"error":%q}`, err.Error()))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := api.AutonomousStatusEntry{
		Kind:      api.AutonomousModeEventDetected,
		Message:   strings.TrimSpace(filterID),
		Payload:   string(encoded),
		Timestamp: s.clock(),
	}
	run.detected = appendRing(run.detected, entry, autonomousStatusRing)
}

func (s *AutonomousState) snapshotRun(run *autonomousRun) api.AutonomousModeRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotRunLocked(run)
}

func (s *AutonomousState) snapshotRunLocked(run *autonomousRun) api.AutonomousModeRun {
	activeByProvider := make(map[string][]string, len(run.activeByProvider))
	for provider, ids := range run.activeByProvider {
		out := make([]string, 0, len(ids))
		for id := range ids {
			out = append(out, id)
		}
		sort.Strings(out)
		activeByProvider[provider] = out
	}
	recentStatus := make([]string, 0, len(run.recentStatus))
	for _, entry := range run.recentStatus {
		recentStatus = append(recentStatus, encodeStatusEntry(entry))
	}
	detected := make([]string, 0, len(run.detected))
	for _, entry := range run.detected {
		detected = append(detected, encodeStatusEntry(entry))
	}
	return api.AutonomousModeRun{
		InstanceID:       run.instanceID,
		WorkspaceID:      run.workspaceID,
		Running:          run.running,
		StartedAt:        run.startedAt,
		StoppedAt:        run.stoppedAt,
		StopReason:       run.stopReason,
		ActiveFilterIDs:  sortedKeys(run.activeFilterIDs),
		ActiveByProvider: activeByProvider,
		RecentStatusJSON: recentStatus,
		LastDetectedJSON: detected,
	}
}

func (s *AutonomousState) publish(ctx context.Context, bus *event.Bus, events eventServicePublisher, workspaceID, instanceID, kind string, payload map[string]any) {
	if bus == nil && events == nil {
		return
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["instance_id"] = instanceID
	raw, err := json.Marshal(payload)
	if err != nil {
		slog.Warn("autonomous: marshal event payload", "kind", kind, "err", err)
		return
	}
	evt := domain.SystemEvent{
		WorkspaceID: workspaceID,
		EventType:   kind,
		CreatedAt:   s.clock(),
		Payload:     string(raw),
	}
	if events != nil {
		persisted, err := events.Create(ctx, evt)
		if err != nil {
			slog.Warn("autonomous: persist event", "kind", kind, "err", err)
		} else {
			evt = persisted
		}
	}
	if bus != nil {
		if err := bus.Publish(ctx, evt); err != nil {
			slog.Warn("autonomous: publish event", "kind", kind, "err", err)
		}
	}
}

func (s *AutonomousState) lookupIdempotent(action, instanceID, key string) (any, bool) {
	if strings.TrimSpace(key) == "" {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cached, ok := s.idempotency[action+":"+strings.TrimSpace(instanceID)+":"+strings.TrimSpace(key)]
	if !ok {
		return nil, false
	}
	return cached.value, true
}

func (s *AutonomousState) storeIdempotent(action, instanceID, key string, value any) {
	if strings.TrimSpace(key) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.idempotency[action+":"+strings.TrimSpace(instanceID)+":"+strings.TrimSpace(key)] = cachedAutonomousResponse{action: action, key: strings.TrimSpace(key), value: value}
}

// ForgetIdempotent drops the cached response for (action, instanceID, key) so a
// retry with the same idempotency key re-executes the underlying call. The
// StartAutonomousMode retry path uses it to avoid surfacing a stale running
// snapshot when the prior controller start failed and the run is no longer
// running.
func (s *AutonomousState) ForgetIdempotent(action, instanceID, key string) {
	if strings.TrimSpace(key) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.idempotency, action+":"+strings.TrimSpace(instanceID)+":"+strings.TrimSpace(key))
}

// eventServicePublisher narrows the API surface AutonomousState needs from
// the daemon's event service. Decoupling lets us pass either a real
// *service.EventService (production) or a stub in tests.
type eventServicePublisher interface {
	Create(ctx context.Context, e domain.SystemEvent) (domain.SystemEvent, error)
}

func appendRing[T any](ring []T, entry T, capacity int) []T {
	ring = append(ring, entry)
	if len(ring) > capacity {
		ring = ring[len(ring)-capacity:]
	}
	return ring
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func encodeStatusEntry(entry api.AutonomousStatusEntry) string {
	raw, err := json.Marshal(entry)
	if err != nil {
		return fmt.Sprintf(`{"kind":%q,"level":%q,"message":%q,"timestamp":%q}`, entry.Kind, entry.Level, entry.Message, entry.Timestamp.Format(time.RFC3339Nano))
	}
	return string(raw)
}
