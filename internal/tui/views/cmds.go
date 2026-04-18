package views

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"net/url"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/app/remotedetect"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/gitwork"
	"github.com/beeemT/substrate/internal/orchestrator"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
	"github.com/beeemT/substrate/internal/sessionlog"
	"github.com/beeemT/substrate/internal/tuilog"
)

const (
	pollInterval      = 2 * time.Second
	heartbeatInterval = 5 * time.Second
)

// PollTickCmd returns a Cmd that fires after pollInterval to refresh DB state.
func PollTickCmd() tea.Cmd {
	return tea.Tick(pollInterval, func(t time.Time) tea.Msg {
		return PollTickMsg(t)
	})
}

// HeartbeatTickCmd returns a Cmd that fires after heartbeatInterval.
func HeartbeatTickCmd() tea.Cmd {
	return tea.Tick(heartbeatInterval, func(t time.Time) tea.Msg {
		return HeartbeatTickMsg(t)
	})
}

// LoadSessionsCmd fetches sessions for a workspace from the DB.
func LoadSessionsCmd(svc *service.SessionService, workspaceID string) tea.Cmd {
	return func() tea.Msg {
		filter := repository.SessionFilter{WorkspaceID: &workspaceID}
		items, err := svc.List(context.Background(), filter)
		if err != nil {
			return ErrMsg{Err: err}
		}

		return SessionsLoadedMsg{WorkspaceID: workspaceID, Items: items}
	}
}

// LoadTasksCmd fetches all agent sessions for a workspace.
func LoadTasksCmd(svc *service.TaskService, workspaceID string) tea.Cmd {
	return func() tea.Msg {
		sessions, err := svc.ListByWorkspaceID(context.Background(), workspaceID)
		if err != nil {
			return ErrMsg{Err: err}
		}

		return TasksLoadedMsg{WorkspaceID: workspaceID, Sessions: sessions}
	}
}

// LoadNewSessionFiltersCmd fetches saved New Session Filters for a workspace.
func LoadNewSessionFiltersCmd(svc *service.SessionFilterService, workspaceID string) tea.Cmd {
	return func() tea.Msg {
		if svc == nil {
			return ErrMsg{Err: errors.New("filter service is unavailable")}
		}
		id := strings.TrimSpace(workspaceID)
		if id == "" {
			return ErrMsg{Err: errors.New("workspace ID is required")}
		}
		filters, err := svc.ListByWorkspaceID(context.Background(), id)
		if err != nil {
			return ErrMsg{Err: err}
		}
		sort.SliceStable(filters, func(i, j int) bool {
			if filters[i].Provider != filters[j].Provider {
				return filters[i].Provider < filters[j].Provider
			}
			if filters[i].Name != filters[j].Name {
				return filters[i].Name < filters[j].Name
			}
			return filters[i].ID < filters[j].ID
		})
		return NewSessionFiltersLoadedMsg{WorkspaceID: id, Filters: filters}
	}
}

// SaveNewSessionFilterCmd persists a new saved New Session Filter using the requested name when provided.
func SaveNewSessionFilterCmd(svc *service.SessionFilterService, req SaveNewSessionFilterMsg) tea.Cmd {
	return func() tea.Msg {
		if svc == nil {
			return ErrMsg{Err: errors.New("filter service is unavailable")}
		}
		workspaceID := strings.TrimSpace(req.WorkspaceID)
		if workspaceID == "" {
			return ErrMsg{Err: errors.New("workspace ID is required")}
		}
		provider := strings.TrimSpace(req.Provider)
		if provider == "" {
			return ErrMsg{Err: errors.New("provider is required")}
		}
		name := strings.TrimSpace(req.Name)
		if name == "" {
			name = generatedNewSessionFilterName(provider, req.Criteria)
		}
		created := domain.NewSessionFilter{
			ID:          domain.NewID(),
			WorkspaceID: workspaceID,
			Name:        name,
			Provider:    provider,
			Criteria:    req.Criteria,
		}
		if err := svc.Create(context.Background(), created); err != nil {
			return ErrMsg{Err: err}
		}
		persisted, err := svc.Get(context.Background(), created.ID)
		if err != nil {
			return ErrMsg{Err: err}
		}
		return NewSessionFilterSavedMsg{
			Filter:  persisted,
			Message: "Filter saved: " + persisted.Name,
		}
	}
}

// DeleteNewSessionFilterCmd deletes a saved New Session Filter by ID.
func DeleteNewSessionFilterCmd(svc *service.SessionFilterService, req DeleteNewSessionFilterMsg) tea.Cmd {
	return func() tea.Msg {
		if svc == nil {
			return ErrMsg{Err: errors.New("filter service is unavailable")}
		}
		workspaceID := strings.TrimSpace(req.WorkspaceID)
		if workspaceID == "" {
			return ErrMsg{Err: errors.New("workspace ID is required")}
		}
		filterID := strings.TrimSpace(req.FilterID)
		if filterID == "" {
			return ErrMsg{Err: errors.New("filter ID is required")}
		}
		if err := svc.Delete(context.Background(), filterID); err != nil {
			return ErrMsg{Err: err}
		}
		return NewSessionFilterDeletedMsg{
			FilterID: filterID,
			Message:  "Filter deleted",
		}
	}
}

func generatedNewSessionFilterName(provider string, criteria domain.NewSessionFilterCriteria) string {
	parts := []string{"Filter", strings.TrimSpace(provider)}
	if scope := strings.TrimSpace(string(criteria.Scope)); scope != "" {
		parts = append(parts, scope)
	}
	if view := strings.TrimSpace(criteria.View); view != "" {
		parts = append(parts, view)
	}
	parts = append(parts, time.Now().UTC().Format("20060102-150405"))
	name := strings.Join(parts, " · ")
	return strings.TrimSpace(name)
}

func isAutonomousEligibleFilter(filter domain.NewSessionFilter) bool {
	return strings.EqualFold(strings.TrimSpace(filter.Provider), viewFilterAll) == false
}

// StartNewSessionAutonomousModeCmd starts autonomous mode for selected New Session Filters.
func StartNewSessionAutonomousModeCmd(
	workspaceID string,
	instanceID string,
	lockSvc *service.SessionFilterLockService,
	adapters []adapter.WorkItemAdapter,
	loadedFilters []domain.NewSessionFilter,
	selectedFilterIDs []string,
) tea.Cmd {
	return func() tea.Msg {
		if len(selectedFilterIDs) == 0 {
			return ErrMsg{Err: errors.New("at least one Filter must be selected")}
		}
		byID := make(map[string]domain.NewSessionFilter, len(loadedFilters))
		for _, filter := range loadedFilters {
			id := strings.TrimSpace(filter.ID)
			if id == "" {
				continue
			}
			byID[id] = filter
		}
		selected := make([]domain.NewSessionFilter, 0, len(selectedFilterIDs))
		for _, id := range selectedFilterIDs {
			trimmedID := strings.TrimSpace(id)
			filter, ok := byID[trimmedID]
			if !ok {
				return ErrMsg{Err: fmt.Errorf("selected Filter %s was not found", trimmedID)}
			}
			if !isAutonomousEligibleFilter(filter) {
				return ErrMsg{Err: fmt.Errorf("selected Filter %s cannot be used for autonomous mode because provider is all", trimmedID)}
			}
			selected = append(selected, filter)
		}
		runtime := NewNewSessionAutonomousRuntime(workspaceID, instanceID, selected, adapters, lockSvc)
		if err := runtime.Start(); err != nil {
			if strings.TrimSpace(err.Error()) == newSessionAutonomousLockConflictWarning {
				return NewSessionAutonomousStatusMsg{Level: "warning", Message: newSessionAutonomousLockConflictWarning}
			}
			return ErrMsg{Err: err}
		}
		return NewSessionAutonomousStartedMsg{
			Runtime: runtime,
			Events:  runtime.Events(),
			Message: "New Session autonomous mode started",
		}
	}
}

// StopNewSessionAutonomousModeCmd stops a running autonomous mode runtime.
func StopNewSessionAutonomousModeCmd(runtime *NewSessionAutonomousRuntime) tea.Cmd {
	return func() tea.Msg {
		if runtime == nil {
			return NewSessionAutonomousStoppedMsg{Message: "New Session autonomous mode stopped"}
		}
		if err := runtime.Stop(); err != nil {
			return ErrMsg{Err: err}
		}
		return NewSessionAutonomousStoppedMsg{Message: "New Session autonomous mode stopped"}
	}
}

// WaitForNewSessionAutonomousEventCmd waits for one autonomous runtime event.
func WaitForNewSessionAutonomousEventCmd(ch <-chan tea.Msg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return NewSessionAutonomousStoppedMsg{Message: "New Session autonomous mode stopped"}
		}
		return msg
	}
}

// SearchSessionHistoryCmd searches session history within the requested scope.
func SearchSessionHistoryCmd(svc *service.TaskService, filter domain.SessionHistoryFilter) tea.Cmd {
	return func() tea.Msg {
		entries, err := svc.SearchHistory(context.Background(), filter)
		if err != nil {
			return ErrMsg{Err: err}
		}

		return SessionHistoryLoadedMsg{Filter: filter, Entries: entries}
	}
}

// LoadPlanCmd fetches the plan for a work item.
func LoadPlanCmd(svc *service.PlanService, workItemID string) tea.Cmd {
	return func() tea.Msg {
		plan, err := svc.GetPlanByWorkItemID(context.Background(), workItemID)
		if err != nil {
			// No plan yet is not an error worth surfacing.
			return PlanLoadedMsg{WorkItemID: workItemID, Plan: nil}
		}
		subPlans, err := svc.ListSubPlansByPlanID(context.Background(), plan.ID)
		if err != nil {
			return ErrMsg{Err: err}
		}

		return PlanLoadedMsg{WorkItemID: workItemID, Plan: &plan, SubPlans: subPlans}
	}
}

// LoadPlanByIDCmd fetches a plan by its ID and composes a read-only document.
func LoadPlanByIDCmd(svc *service.PlanService, planID string) tea.Cmd {
	return func() tea.Msg {
		plan, err := svc.GetPlan(context.Background(), planID)
		if err != nil {
			return InspectPlanLoadedMsg{PlanID: planID, Err: fmt.Errorf("load plan %s: %w", planID, err)}
		}
		subPlans, err := svc.ListSubPlansByPlanID(context.Background(), plan.ID)
		if err != nil {
			return InspectPlanLoadedMsg{PlanID: planID, Err: fmt.Errorf("load sub-plans for plan %s: %w", planID, err)}
		}
		return InspectPlanLoadedMsg{
			PlanID:   planID,
			Document: domain.ComposePlanDocument(plan, subPlans),
		}
	}
}

// LoadQuestionsCmd fetches open questions for a session.
func LoadQuestionsCmd(svc *service.QuestionService, sessionID string) tea.Cmd {
	return func() tea.Msg {
		questions, err := svc.ListBySessionID(context.Background(), sessionID)
		if err != nil {
			return ErrMsg{Err: err}
		}

		return QuestionsLoadedMsg{SessionID: sessionID, Questions: questions}
	}
}

// LoadReviewsCmd fetches review cycles and critiques for a session.
func LoadReviewsCmd(revSvc *service.ReviewService, sessionID string) tea.Cmd {
	return func() tea.Msg {
		cycles, err := revSvc.ListCyclesBySessionID(context.Background(), sessionID)
		if err != nil {
			return ErrMsg{Err: err}
		}
		critiques := make(map[string][]domain.Critique)
		for _, c := range cycles {
			cc, err := revSvc.ListCritiquesByCycleID(context.Background(), c.ID)
			if err == nil {
				critiques[c.ID] = cc
			}
		}

		return ReviewsLoadedMsg{SessionID: sessionID, Cycles: cycles, Critiques: critiques}
	}
}

// ApprovePlanCmd transitions work item to approved and emits plan approved.
func ApprovePlanCmd(
	workItemSvc *service.SessionService,
	planSvc *service.PlanService,
	bus *event.Bus,
	cfg *config.Config,
	planID, workItemID string,
) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		if err := planSvc.ApprovePlan(ctx, planID); err != nil {
			return ErrMsg{Err: err}
		}
		if err := workItemSvc.ApprovePlan(ctx, workItemID); err != nil {
			return ErrMsg{Err: err}
		}
		if err := emitPlanApproved(ctx, bus, planSvc, workItemSvc, cfg, planID, workItemID); err != nil {
			slog.Warn("failed to emit plan approved event", "plan_id", planID, "work_item_id", workItemID, "err", err)
		}

		return PlanApprovedMsg{PlanID: planID, WorkItemID: workItemID}
	}
}

// AnswerQuestionCmd approves a human answer for an escalated question.
// When foreman is non-nil, ResolveEscalated is used so the blocked sub-agent goroutine
// is unblocked via its answer channel in addition to the DB write.
// Falls back to direct questionSvc.Answer on ErrQuestionNotEscalated (Foreman was
// restarted and cleared in-flight channels, or no Foreman is configured).
// sessionSvc is used in the fallback path to resume the session from waiting_for_answer.
func AnswerQuestionCmd(svc *service.QuestionService, sessionSvc *service.TaskService, foreman *orchestrator.Foreman, questionID, answer, answeredBy string) tea.Cmd {
	return func() tea.Msg {
		if foreman != nil {
			err := foreman.ResolveEscalated(context.Background(), questionID, answer)
			if err == nil {
				return ActionDoneMsg{Message: "Answer submitted"}
			}
			// If the channel is gone (Foreman restarted), fall through to direct write.
			if !errors.Is(err, orchestrator.ErrQuestionNotEscalated) {
				return ErrMsg{Err: err}
			}
		}
		// Foreman was restarted or not configured: resume the session directly so the TUI
		// clears the action-required state before persisting the answer.
		if sessionSvc != nil {
			if q, err := svc.Get(context.Background(), questionID); err == nil && q.AgentSessionID != "" {
				if err := sessionSvc.ResumeFromAnswer(context.Background(), q.AgentSessionID); err != nil {
					slog.Warn("failed to resume session on answer fallback", "error", err, "question_id", questionID)
				}
			}
		}
		if err := svc.Answer(context.Background(), questionID, answer, answeredBy); err != nil {
			return ErrMsg{Err: err}
		}

		return ActionDoneMsg{Message: "Answer submitted"}
	}
}

// HeartbeatCmd sends a heartbeat for the current instance.
func HeartbeatCmd(svc *service.InstanceService, instanceID string) tea.Cmd {
	return func() tea.Msg {
		if heartbeatErr := svc.UpdateHeartbeat(context.Background(), instanceID); heartbeatErr != nil {
			slog.Warn("failed to update heartbeat", "error", heartbeatErr, "instance_id", instanceID)
		}

		return nil
	}
}

// DeleteInstanceCmd removes the instance record on clean shutdown.
func DeleteInstanceCmd(svc *service.InstanceService, instanceID string) tea.Cmd {
	return func() tea.Msg {
		if deleteErr := svc.Delete(context.Background(), instanceID); deleteErr != nil {
			slog.Warn("failed to delete instance record", "error", deleteErr, "instance_id", instanceID)
		}

		return nil
	}
}

// initializeWorkspaceServicesCmd rebuilds services after workspace initialization
// so adapters, harnesses, and instance registration use the new workspace root.
func initializeWorkspaceServicesCmd(settings *SettingsService, current Services, workspaceID, workspaceName, workspaceDir string) tea.Cmd {
	return func() tea.Msg {
		if settings == nil {
			return ErrMsg{Err: errors.New("settings service is unavailable")}
		}
		if current.Cfg == nil {
			return ErrMsg{Err: errors.New("config is unavailable")}
		}
		current.WorkspaceID = workspaceID
		current.WorkspaceName = workspaceName
		current.WorkspaceDir = workspaceDir

		reloaded, err := settings.rebuildServices(context.Background(), current.Cfg, current)
		if err != nil {
			return ErrMsg{Err: err}
		}

		host, _ := os.Hostname()
		inst := domain.SubstrateInstance{
			ID:          domain.NewID(),
			WorkspaceID: workspaceID,
			PID:         os.Getpid(),
			Hostname:    host,
		}
		if err := reloaded.Services.Instance.Create(context.Background(), inst); err != nil {
			slog.Warn("failed to register instance after workspace initialization", "workspace_id", workspaceID, "err", err)
		} else {
			reloaded.Services.InstanceID = inst.ID
		}

		return WorkspaceServicesReloadedMsg{Reload: reloaded, Message: "Workspace initialized"}
	}
}

// WorkspaceHealthCheckCmd scans the current directory for repos.
func WorkspaceHealthCheckCmd(dir string) tea.Cmd {
	return func() tea.Msg {
		scan, err := gitwork.ScanWorkspace(dir)
		if err != nil {
			return WorkspaceHealthCheckMsg{Error: err}
		}

		return WorkspaceHealthCheckMsg{Check: domain.WorkspaceHealthCheck{
			GitWorkRepos:   scan.GitWorkRepos,
			PlainGitClones: scan.PlainGitRepos,
		}}
	}
}

func sessionInteractionPaths(sessionsDir, sessionID string) ([]string, error) {
	pattern := filepath.Join(sessionsDir, sessionID+".log.*.gz")
	compressed, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob session logs: %w", err)
	}
	sort.Strings(compressed)
	paths := append([]string(nil), compressed...)
	active := filepath.Join(sessionsDir, sessionID+".log")
	if _, err := os.Stat(active); err == nil {
		paths = append(paths, active)
	} else if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("stat session log: %w", err)
	}

	return paths, nil
}

func readSessionInteractionFile(path string) ([]sessionlog.Entry, error) {
	return sessionlog.ReadFile(path)
}

func LoadSessionInteractionCmd(sessionsDir, sessionID string) tea.Cmd {
	return func() tea.Msg {
		paths, err := sessionInteractionPaths(sessionsDir, sessionID)
		if err != nil {
			return ErrMsg{Err: err}
		}
		var entries []sessionlog.Entry
		for _, path := range paths {
			chunk, err := readSessionInteractionFile(path)
			if err != nil {
				return ErrMsg{Err: err}
			}
			entries = append(entries, chunk...)
		}

		return SessionInteractionLoadedMsg{SessionID: sessionID, Entries: entries}
	}
}

func TailSessionLogCmd(logPath string, sessionID string, since int64) tea.Cmd {
	return func() tea.Msg {
		if since == 0 {
			// Initial call: load all archived content (gzipped rotations) plus the
			// active log file so the viewport starts with the full session history.
			// Subsequent polls use the continuation path below (since > 0).
			sessionsDir := filepath.Dir(logPath)
			paths, err := sessionInteractionPaths(sessionsDir, sessionID)
			if err == nil {
				var entries []sessionlog.Entry
				for _, path := range paths {
					chunk, readErr := readSessionInteractionFile(path)
					if readErr == nil {
						entries = append(entries, chunk...)
					}
				}
				// nextOffset = current size of the active file so the first
				// continuation poll reads only bytes written after this load.
				// When the active file does not exist (only rotated archives
				// remain), use a sentinel offset of 1 so the next call enters
				// the continuation path instead of re-triggering a full archive
				// reload. The continuation path handles a missing file gracefully
				// (sleeps, retries). When the file eventually appears, the
				// rotation-detection check (stat.Size < since) resets offset to 0
				// so we read from the beginning.
				nextOffset := int64(1)
				if stat, statErr := os.Stat(logPath); statErr == nil {
					nextOffset = max(1, stat.Size())
				}

				return SessionLogLinesMsg{SessionID: sessionID, Entries: entries, NextOffset: nextOffset}
			}
			// Path discovery failed (should be rare); fall through to single-file read.
		}
		// Continuation poll: read only new bytes from the active log file.
		// Sleep on the no-new-data and error paths to avoid a tight spin loop that
		// would flood the Bubble Tea message queue and starve scroll event processing.
		const tailPollInterval = 250 * time.Millisecond
		stat, err := os.Stat(logPath)
		if err != nil {
			time.Sleep(tailPollInterval)

			return SessionLogLinesMsg{SessionID: sessionID, NextOffset: since}
		}
		offset := since
		if stat.Size() < since {
			offset = 0 // rotation detected
		}
		if stat.Size() == offset {
			time.Sleep(tailPollInterval)

			return SessionLogLinesMsg{SessionID: sessionID, NextOffset: offset}
		}
		f, err := os.Open(logPath)
		if err != nil {
			return SessionLogLinesMsg{SessionID: sessionID, NextOffset: offset}
		}
		defer f.Close()
		if _, err := f.Seek(offset, 0); err != nil {
			return SessionLogLinesMsg{SessionID: sessionID, NextOffset: offset}
		}
		var entries []sessionlog.Entry
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)
		for scanner.Scan() {
			if entry, ok := sessionlog.ParseLine(scanner.Text()); ok {
				entries = append(entries, entry)
			}
		}
		pos, seekErr := f.Seek(0, io.SeekCurrent)
		newOffset := offset
		if seekErr == nil {
			newOffset = pos
		}
		if scanErr := scanner.Err(); scanErr != nil {
			slog.Warn("session log scanner error", "error", scanErr, "session_id", sessionID)
		}

		return SessionLogLinesMsg{SessionID: sessionID, Entries: entries, NextOffset: newOffset}
	}
}

// SaveReviewedPlanCmd validates and persists a full reviewed plan document after $EDITOR edits.
func SaveReviewedPlanCmd(planningSvc *orchestrator.PlanningService, planID, content string) tea.Cmd {
	return func() tea.Msg {
		if planningSvc == nil {
			return ErrMsg{Err: errors.New("planning service not configured")}
		}
		plan, subPlans, err := planningSvc.UpdateReviewedPlan(context.Background(), planID, content)
		if err != nil {
			return ErrMsg{Err: err}
		}

		return PlanSavedMsg{
			WorkItemID: plan.WorkItemID,
			Plan:       plan,
			SubPlans:   subPlans,
			Message:    "Plan updated",
		}
	}
}

// LoadLiveInstancesCmd fetches all substrate instances for a workspace and
// returns the set of IDs whose heartbeat is fresher than 15 seconds ago.
func LoadLiveInstancesCmd(svc *service.InstanceService, workspaceID string) tea.Cmd {
	return func() tea.Msg {
		const staleness = 15 * time.Second
		instances, err := svc.ListByWorkspaceID(context.Background(), workspaceID)
		if err != nil {
			return ErrMsg{Err: err}
		}
		alive := make(map[string]bool, len(instances))
		threshold := time.Now().Add(-staleness)
		for _, inst := range instances {
			if inst.LastHeartbeat.After(threshold) {
				alive[inst.ID] = true
			}
		}

		return LiveInstancesLoadedMsg{AliveIDs: alive}
	}
}

// StartPlanningCmd runs the planning pipeline for a work item in the background.
// On completion it fires ActionDoneMsg; the 2 s poll loop picks up the new plan_review state.
func StartPlanningCmd(ctx context.Context, svc *orchestrator.PlanningService, workItemID string) tea.Cmd {
	return func() tea.Msg {
		if _, err := svc.Plan(ctx, workItemID); err != nil {
			return ErrMsg{Err: err}
		}

		return ActionDoneMsg{Message: "Planning complete"}
	}
}

// ReconcileOrphanedTasksCmd interrupts any running or waiting tasks whose owner
// instance is absent or has a stale heartbeat (>15s). It is called on workspace
// initialization to clean up tasks left in a running state by a previous substrate process.
// Errors are logged but never surfaced to the user — the 2s poll loop picks up the
// updated task statuses.
func ReconcileOrphanedTasksCmd(
	taskSvc *service.TaskService,
	instanceSvc *service.InstanceService,
	workspaceID, currentInstanceID string,
) tea.Cmd {
	return func() tea.Msg {
		const staleness = 15 * time.Second
		ctx := context.Background()

		tasks, err := taskSvc.ListByWorkspaceID(ctx, workspaceID)
		if err != nil {
			slog.Warn("reconcile: failed to list tasks", "workspace_id", workspaceID, "error", err)
			return nil
		}
		instances, err := instanceSvc.ListByWorkspaceID(ctx, workspaceID)
		if err != nil {
			slog.Warn("reconcile: failed to list instances", "workspace_id", workspaceID, "error", err)
			return nil
		}

		liveSet := make(map[string]bool, len(instances))
		for _, inst := range instances {
			if time.Since(inst.LastHeartbeat) <= staleness {
				liveSet[inst.ID] = true
			}
		}

		for _, task := range tasks {
			if task.Status != domain.AgentSessionRunning && task.Status != domain.AgentSessionWaitingForAnswer {
				continue
			}
			// Never interrupt tasks owned by the current instance.
			if task.OwnerInstanceID != nil && *task.OwnerInstanceID == currentInstanceID {
				continue
			}
			ownerAlive := task.OwnerInstanceID != nil && liveSet[*task.OwnerInstanceID]
			if ownerAlive {
				continue
			}
			if err := taskSvc.Interrupt(ctx, task.ID); err != nil {
				slog.Error("reconcile: failed to interrupt orphaned task",
					"task_id", task.ID, "workspace_id", workspaceID, "error", err)
			}
		}

		return nil
	}
}

// RestartPlanningCmd rolls a work item back from planning to ingested (if needed)
// and then re-runs the planning pipeline from the beginning. This handles the case
// where a planning task was interrupted and the user wants to start fresh.
func RestartPlanningCmd(ctx context.Context, workItemSvc *service.SessionService, planningSvc *orchestrator.PlanningService, sessionSvc *service.TaskService, workItemID string) tea.Cmd {
	return func() tea.Msg {
		if err := workItemSvc.RollbackPlanningInterrupt(ctx, workItemID); err != nil {
			return ErrMsg{Err: err}
		}

		// Fail any interrupted sessions so the overview clears the action card.
		sessions, listErr := sessionSvc.ListByWorkItemID(ctx, workItemID)
		if listErr == nil {
			for _, s := range sessions {
				if s.Status == domain.AgentSessionInterrupted {
					if failErr := sessionSvc.Fail(ctx, s.ID, nil); failErr != nil {
						slog.Warn("failed to fail interrupted session during planning restart",
							"session_id", s.ID, "error", failErr)
					}
				}
			}
		}

		if _, err := planningSvc.Plan(ctx, workItemID); err != nil {
			return ErrMsg{Err: err}
		}

		return PlanningRestartedMsg{Message: "Planning restarted"}
	}
}

// PlanWithFeedbackCmd rejects the old plan and starts a revision session.
func PlanWithFeedbackCmd(ctx context.Context, svc *orchestrator.PlanningService, workItemID, planID, feedback string) tea.Cmd {
	return func() tea.Msg {
		if _, err := svc.PlanWithFeedback(ctx, workItemID, planID, feedback); err != nil {
			return ErrMsg{Err: err}
		}

		return ActionDoneMsg{Message: "Plan revised"}
	}
}

// RunImplementationCmd executes the implementation pipeline for an approved plan.
// On success it returns ImplementationCompleteMsg so the caller can trigger review.
func RunImplementationCmd(ctx context.Context, svc *orchestrator.ImplementationService, planID string) tea.Cmd {
	return func() tea.Msg {
		result, err := svc.Implement(ctx, planID)
		if err != nil {
			return ErrMsg{Err: err}
		}
		var sessionIDs []string
		for _, s := range result.Sessions {
			if s.Status == domain.AgentSessionCompleted {
				sessionIDs = append(sessionIDs, s.SessionID)
			}
		}

		return ImplementationCompleteMsg{
			PlanID:     planID,
			WorkItemID: result.WorkItemID,
			SessionIDs: sessionIDs,
		}
	}
}

// ResumeSessionCmd resumes an interrupted agent session.
func ResumeSessionCmd(ctx context.Context, resumption *orchestrator.Resumption, sessionSvc *service.TaskService, oldSessionID, instanceID string) tea.Cmd {
	return func() tea.Msg {
		session, err := sessionSvc.Get(ctx, oldSessionID)
		if err != nil {
			return ErrMsg{Err: err}
		}
		if _, err := resumption.ResumeSession(ctx, session, instanceID); err != nil {
			return ErrMsg{Err: err}
		}

		return SessionResumedMsg{Message: "Session resumed"}
	}
}

// OverrideAcceptCmd marks a work item completed despite outstanding critiques.
func OverrideAcceptCmd(
	workItemSvc *service.SessionService,
	planSvc *service.PlanService,
	sessionSvc *service.TaskService,
	bus *event.Bus,
	workItemID string,
) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		if err := workItemSvc.CompleteWorkItem(ctx, workItemID); err != nil {
			return ErrMsg{Err: err}
		}
		if err := emitWorkItemCompleted(ctx, bus, planSvc, sessionSvc, workItemSvc, workItemID); err != nil {
			slog.Warn("failed to emit work item completed event", "work_item_id", workItemID, "err", err)
		}

		return ActionDoneMsg{Message: "Work item accepted"}
	}
}

// RetryFailedCmd transitions a failed work item back to implementing and re-runs
// the implementation pipeline for failed sub-plans.
func RetryFailedCmd(ctx context.Context, workItemSvc *service.SessionService, implSvc *orchestrator.ImplementationService, planID, workItemID string) tea.Cmd {
	return func() tea.Msg {
		if err := workItemSvc.RetryFailedWorkItem(ctx, workItemID); err != nil {
			return ErrMsg{Err: fmt.Errorf("retry failed work item: %w", err)}
		}
		// Re-run implementation — Implement() will reset failed sub-plans
		// to pending and only execute those.
		result, err := implSvc.Implement(ctx, planID)
		if err != nil {
			return ErrMsg{Err: fmt.Errorf("retry implementation: %w", err)}
		}
		var sessionIDs []string
		for _, s := range result.Sessions {
			if s.Status == domain.AgentSessionCompleted {
				sessionIDs = append(sessionIDs, s.SessionID)
			}
		}
		return ImplementationCompleteMsg{
			PlanID:     planID,
			WorkItemID: workItemID,
			SessionIDs: sessionIDs,
		}
	}
}

func emitPlanApproved(ctx context.Context, bus *event.Bus, planSvc *service.PlanService, workItemSvc *service.SessionService, cfg *config.Config, planID, workItemID string) error {
	if bus == nil {
		return nil
	}
	plan, err := planSvc.GetPlan(ctx, planID)
	if err != nil {
		return err
	}
	workItem, err := workItemSvc.Get(ctx, workItemID)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"plan_id":      planID,
		"work_item_id": workItemID,
		"external_id":  workItem.ExternalID,
	}
	var commentMode config.IssueCommentContent
	if cfg != nil {
		commentMode = cfg.IssueCommentContentForSource(workItem.Source)
	} else {
		commentMode = config.IssueCommentSubPlan
	}
	if commentBody := buildIssueCommentBody(ctx, planSvc, commentMode, plan); commentBody != "" {
		payload["comment_body"] = commentBody
	}
	if externalIDs := workItemEventExternalIDs(workItem); len(externalIDs) > 0 {
		payload["external_ids"] = externalIDs
	}

	return publishSystemEvent(ctx, bus, domain.EventPlanApproved, workItem.WorkspaceID, payload)
}

// buildIssueCommentBody assembles the comment body for plan-approved issue comments
// according to the configured IssueCommentContent mode. Returns empty string for
// IssueCommentNone or when the selected content is empty.
func buildIssueCommentBody(ctx context.Context, planSvc *service.PlanService, mode config.IssueCommentContent, plan domain.Plan) string {
	switch mode {
	case config.IssueCommentNone:
		return ""
	case config.IssueCommentOrchestratorPlan:
		return strings.TrimSpace(plan.OrchestratorPlan)
	case config.IssueCommentSubPlan:
		subPlans, err := planSvc.ListSubPlansByPlanID(ctx, plan.ID)
		if err != nil {
			slog.Warn("failed to list sub-plans for issue comment", "plan_id", plan.ID, "err", err)
			return ""
		}
		return domain.ComposeSubPlansContent(subPlans)
	case config.IssueCommentOrchestratorAndSubPlan:
		subPlans, err := planSvc.ListSubPlansByPlanID(ctx, plan.ID)
		if err != nil {
			slog.Warn("failed to list sub-plans for issue comment", "plan_id", plan.ID, "err", err)
			return strings.TrimSpace(plan.OrchestratorPlan)
		}
		orchestration := strings.TrimSpace(plan.OrchestratorPlan)
		subContent := domain.ComposeSubPlansContent(subPlans)
		switch {
		case orchestration == "":
			return subContent
		case subContent == "":
			return orchestration
		default:
			return orchestration + "\n\n" + subContent
		}
	case config.IssueCommentFullPlan:
		subPlans, err := planSvc.ListSubPlansByPlanID(ctx, plan.ID)
		if err != nil {
			slog.Warn("failed to list sub-plans for issue comment", "plan_id", plan.ID, "err", err)
			return strings.TrimSpace(plan.OrchestratorPlan)
		}
		return domain.ComposePlanDocument(plan, subPlans)
	default:
		// Unknown mode — treat as none to avoid leaking partial content.
		slog.Warn("unknown issue_comment_content mode; comment suppressed", "mode", mode)
		return ""
	}
}

func emitWorkItemCompleted(ctx context.Context, bus *event.Bus, planSvc *service.PlanService, sessionSvc *service.TaskService, workItemSvc *service.SessionService, workItemID string) error {
	if bus == nil {
		return nil
	}
	workItem, err := workItemSvc.Get(ctx, workItemID)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"workspace_id":    workItem.WorkspaceID,
		"work_item_id":    workItemID,
		"external_id":     workItem.ExternalID,
		"work_item_title": workItem.Title,
	}
	if review, branch, subPlanContent, err := completionReviewContext(ctx, planSvc, sessionSvc, workItemID); err == nil {
		if branch != "" {
			payload["branch"] = branch
		}
		if review.BaseRepo.Owner != "" || review.BaseRepo.Repo != "" || review.HeadRepo.Owner != "" || review.HeadRepo.Repo != "" {
			payload["review"] = review
		}
		if subPlanContent != "" {
			payload["sub_plan"] = subPlanContent
		}
	} else {
		slog.Warn("failed to derive work item completion context", "work_item_id", workItemID, "err", err)
	}
	if externalIDs := workItemEventExternalIDs(workItem); len(externalIDs) > 0 {
		payload["external_ids"] = externalIDs
	}
	if trackerRefs := sessionTrackerRefs(workItem.Metadata); len(trackerRefs) > 0 {
		payload["tracker_refs"] = trackerRefs
	}

	return publishSystemEvent(ctx, bus, domain.EventWorkItemCompleted, workItem.WorkspaceID, payload)
}

func publishSystemEvent(ctx context.Context, bus *event.Bus, eventType domain.EventType, workspaceID string, payload map[string]any) error {
	serialized, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal %s payload: %w", eventType, err)
	}

	return bus.Publish(ctx, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(eventType),
		WorkspaceID: workspaceID,
		Payload:     string(serialized),
		CreatedAt:   time.Now(),
	})
}

func completionReviewContext(ctx context.Context, planSvc *service.PlanService, sessionSvc *service.TaskService, workItemID string) (domain.ReviewRef, string, string, error) {
	plan, err := planSvc.GetPlanByWorkItemID(ctx, workItemID)
	if err != nil {
		return domain.ReviewRef{}, "", "", err
	}
	subPlans, err := planSvc.ListSubPlansByPlanID(ctx, plan.ID)
	if err != nil {
		return domain.ReviewRef{}, "", "", err
	}
	for _, subPlan := range subPlans {
		sessions, err := sessionSvc.ListBySubPlanID(ctx, subPlan.ID)
		if err != nil {
			return domain.ReviewRef{}, "", "", err
		}
		for _, session := range sessions {
			if strings.TrimSpace(session.WorktreePath) == "" {
				continue
			}
			reviewCtx, err := remotedetect.ResolveReviewContext(ctx, session.WorktreePath)
			if err != nil {
				slog.Warn("failed to resolve review context for session", "session_id", session.ID, "worktree", session.WorktreePath, "error", err)
				continue
			}
			branch := strings.TrimSpace(reviewCtx.Review.HeadBranch)
			if branch == "" {
				branch = strings.TrimSpace(reviewCtx.Review.BaseBranch)
			}
			if branch == "" {
				continue
			}

			return reviewCtx.Review, branch, subPlan.Content, nil
		}
	}

	return domain.ReviewRef{}, "", "", fmt.Errorf("no session worktree context found for work item %s", workItemID)
}

func workItemEventExternalIDs(workItem domain.Session) []string {
	seen := make(map[string]struct{})
	ids := make([]string, 0, len(workItem.SourceItemIDs)+1)
	appendID := func(id string) {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			return
		}
		if _, ok := seen[trimmed]; ok {
			return
		}
		seen[trimmed] = struct{}{}
		ids = append(ids, trimmed)
	}
	switch {
	case workItem.Source == "github" && workItem.SourceScope == domain.ScopeIssues:
		for _, id := range workItem.SourceItemIDs {
			appendID("gh:issue:" + id)
		}
	case workItem.Source == "gitlab" && workItem.SourceScope == domain.ScopeIssues:
		for _, id := range workItem.SourceItemIDs {
			appendID("gl:issue:" + id)
		}
	}
	appendID(workItem.ExternalID)

	return ids
}

// SkipQuestionCmd marks a question as skipped — the sub-agent continues without an answer.
// Calls ResolveEscalated with an empty string so the blocked goroutine is unblocked.
// Falls back to direct questionSvc.Answer when Foreman channels are not available.
// sessionSvc is used in the fallback path to resume the session from waiting_for_answer.
func SkipQuestionCmd(svc *service.QuestionService, sessionSvc *service.TaskService, foreman *orchestrator.Foreman, questionID string) tea.Cmd {
	return func() tea.Msg {
		if foreman != nil {
			err := foreman.ResolveEscalated(context.Background(), questionID, "")
			if err == nil {
				return ActionDoneMsg{Message: "Question skipped"}
			}
			if !errors.Is(err, orchestrator.ErrQuestionNotEscalated) {
				return ErrMsg{Err: err}
			}
		}
		// Foreman was restarted or not configured: resume the session directly so the TUI
		// clears the action-required state before persisting the skip.
		if sessionSvc != nil {
			if q, err := svc.Get(context.Background(), questionID); err == nil && q.AgentSessionID != "" {
				if err := sessionSvc.ResumeFromAnswer(context.Background(), q.AgentSessionID); err != nil {
					slog.Warn("failed to resume session on skip fallback", "error", err, "question_id", questionID)
				}
			}
		}
		if err := svc.Answer(context.Background(), questionID, "", "human"); err != nil {
			return ErrMsg{Err: err}
		}

		return ActionDoneMsg{Message: "Question skipped"}
	}
}

// SendToForemanCmd sends human follow-up text to the running Foreman session and
// returns a ForemanReplyMsg carrying the refreshed proposed answer.
func SendToForemanCmd(foreman *orchestrator.Foreman, questionID, text string) tea.Cmd {
	return func() tea.Msg {
		newProposal, uncertain, err := foreman.SendUserMessage(context.Background(), questionID, text)
		if err != nil {
			return ErrMsg{Err: err}
		}

		return ForemanReplyMsg{QuestionID: questionID, NewProposal: newProposal, Uncertain: uncertain}
	}
}

// StartForemanCmd starts the Foreman session for a given plan.
// Uses a background context; Stop() is the proper shutdown mechanism.
// followUpContext is optional; when non-empty it is forwarded to the foreman's
// initial prompt so it is aware of follow-up feedback.
func StartForemanCmd(foreman *orchestrator.Foreman, planID string, followUpContext string) tea.Cmd {
	return func() tea.Msg {
		if err := foreman.Start(context.Background(), planID, followUpContext); err != nil {
			return ErrMsg{Err: err}
		}

		return ActionDoneMsg{Message: "Foreman started"}
	}
}

// OpenBrowserCmd opens the provided URL in the system browser.
func OpenBrowserCmd(rawURL string) tea.Cmd {
	url := strings.TrimSpace(rawURL)
	if url == "" {
		return func() tea.Msg { return ErrMsg{Err: errors.New("browser URL is required")} }
	}

	return tea.ExecProcess(browserOpenExecCmd(url), func(err error) tea.Msg {
		if err != nil {
			return ErrMsg{Err: fmt.Errorf("open browser for %q: %w", url, err)}
		}

		return nil
	})
}

func browserOpenExecCmd(url string) *exec.Cmd {
	switch runtime.GOOS {
	case "darwin":
		return exec.CommandContext(context.TODO(), "open", url)
	case "windows":
		return exec.CommandContext(context.TODO(), "rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return exec.CommandContext(context.TODO(), "xdg-open", url)
	}
}

// StopForemanCmd stops the Foreman session after implementation ends.
func StopForemanCmd(foreman *orchestrator.Foreman) tea.Cmd {
	return func() tea.Msg {
		// Error is logged but not surfaced: a failed Stop should not block the TUI.
		if err := foreman.Stop(context.Background()); err != nil {
			slog.Warn("foreman stop returned an error", "err", err)
		}

		return nil
	}
}

// SteerSessionCmd sends a steering/follow-up message to a running agent session.
func SteerSessionCmd(registry *orchestrator.SessionRegistry, sessionID, message string) tea.Cmd {
	return func() tea.Msg {
		if err := registry.Steer(context.Background(), sessionID, message); err != nil {
			return ErrMsg{Err: fmt.Errorf("steer session %s: %w", sessionID, err)}
		}

		return SteerSessionSentMsg{SessionID: sessionID}
	}
}

// FollowUpSessionCmd starts a follow-up agent session on a completed task.
func FollowUpSessionCmd(ctx context.Context, resumption *orchestrator.Resumption, svc *service.TaskService, taskID, feedback, instanceID string) tea.Cmd {
	return func() tea.Msg {
		task, err := svc.Get(ctx, taskID)
		if err != nil {
			return ErrMsg{Err: fmt.Errorf("get task for follow-up: %w", err)}
		}
		if _, err := resumption.FollowUpSession(ctx, task, feedback, instanceID); err != nil {
			return ErrMsg{Err: fmt.Errorf("start follow-up session: %w", err)}
		}
		return FollowUpSessionSentMsg{TaskID: taskID}
	}
}

// FollowUpFailedSessionCmd starts a follow-up agent session on a failed task.
func FollowUpFailedSessionCmd(ctx context.Context, resumption *orchestrator.Resumption, svc *service.TaskService, taskID, feedback, instanceID string) tea.Cmd {
	return func() tea.Msg {
		task, err := svc.Get(ctx, taskID)
		if err != nil {
			return ErrMsg{Err: fmt.Errorf("get task for failed follow-up: %w", err)}
		}
		if _, err := resumption.FollowUpFailedSession(ctx, task, feedback, instanceID); err != nil {
			return ErrMsg{Err: fmt.Errorf("start failed follow-up session: %w", err)}
		}
		return FollowUpFailedSessionSentMsg{TaskID: taskID}
	}
}

// FollowUpPlanCmd starts a follow-up re-planning cycle for a completed work item.
func FollowUpPlanCmd(ctx context.Context, svc *orchestrator.PlanningService, workItemID, feedback string) tea.Cmd {
	return func() tea.Msg {
		_, err := svc.FollowUpPlan(ctx, workItemID, feedback)
		return FollowUpPlanResultMsg{WorkItemID: workItemID, Err: err}
	}
}

// WaitForAdapterErrorCmd listens for adapter errors and converts them to TUI messages.
// It reads one error from the channel and returns it as an AdapterErrorMsg.
// The caller should re-invoke this command after handling the message to continue listening.
func WaitForAdapterErrorCmd(ch <-chan AdapterErrorMsg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		err, ok := <-ch
		if !ok {
			return nil
		}
		return err
	}
}

// StartupWarningsCmd returns a Cmd that fires a StartupWarningsMsg.
func StartupWarningsCmd(warnings []string) tea.Cmd {
	if len(warnings) == 0 {
		return nil
	}
	return func() tea.Msg {
		return StartupWarningsMsg{Warnings: warnings}
	}
}

// WaitForLogToastCmd listens for slog warn/error entries and converts them to TUI messages.
// The caller should re-invoke this command after handling the message to continue listening.
func WaitForLogToastCmd(ch <-chan tuilog.ToastEntry) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		entry, ok := <-ch
		if !ok {
			return nil
		}
		return LogToastMsg{
			Level:   entry.Level.String(),
			Message: entry.Message,
		}
	}
}

// LoadReposCmd fetches repos from the repo source at sourceIndex.
// requestID is stamped onto the response so the overlay can discard stale results.
func LoadReposCmd(sources []adapter.RepoSource, sourceIndex int, search string, limit int, requestID int, ownedOnly bool) tea.Cmd {
	return func() tea.Msg {
		if len(sources) == 0 {
			return RepoListLoadedMsg{RequestID: requestID, Repos: []adapter.RepoItem{}}
		}
		idx := sourceIndex
		if idx < 0 || idx >= len(sources) {
			idx = 0
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		opts := adapter.RepoListOpts{Search: search, Limit: limit, Page: 1, OwnedOnly: ownedOnly}
		result, err := sources[idx].ListRepos(ctx, opts)
		if err != nil {
			return RepoListLoadedMsg{RequestID: requestID, Errs: []error{err}}
		}
		return RepoListLoadedMsg{RequestID: requestID, Repos: result.Repos, HasMore: result.HasMore}
	}
}

// CloneRepoCmd runs git-work clone and returns the result.
func CloneRepoCmd(gitClient *gitwork.Client, workspaceDir, cloneURL string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		path, err := gitClient.Clone(ctx, workspaceDir, cloneURL)
		return RepoClonedMsg{RepoPath: path, Err: err}
	}
}

// gitConfigPath returns the path to the git config file for the given repo kind.
// Returns "" for unrecognized kinds.
func gitConfigPath(repoPath string, kind repoKind) string {
	switch kind {
	case repoKindGitWork:
		return filepath.Join(repoPath, ".bare", "config")
	case repoKindPlainGit:
		return filepath.Join(repoPath, ".git", "config")
	default:
		return ""
	}
}

// readOriginRemoteURL opens the git config file at configPath and returns the
// url value from the [remote "origin"] section. Returns "" on any error or if
// the section or key is not found. Failure is intentional/silent.
func readOriginRemoteURL(configPath string) string {
	if configPath == "" {
		return ""
	}
	f, err := os.Open(configPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	inOrigin := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == `[remote "origin"]` {
			inOrigin = true
			continue
		}
		if inOrigin {
			if strings.HasPrefix(line, "[") {
				break
			}
			if idx := strings.IndexByte(line, '='); idx > 0 {
				key := strings.TrimSpace(line[:idx])
				if key == "url" {
					return strings.TrimSpace(line[idx+1:])
				}
			}
		}
	}
	return ""
}

// repoSlugFromURL normalizes a git remote URL to a lowercase owner/repo slug.
// Returns "" on any error or if the result is empty.
func repoSlugFromURL(rawURL string) string {
	s := strings.TrimSpace(rawURL)
	if s == "" {
		return ""
	}
	// Strip .git suffix (case-insensitive).
	lo := strings.ToLower(s)
	if strings.HasSuffix(lo, ".git") {
		s = s[:len(s)-4]
	}
	// SSH form: git@github.com:owner/repo
	if strings.Contains(s, "@") && strings.Contains(s, ":") {
		if idx := strings.LastIndexByte(s, ':'); idx >= 0 {
			return strings.ToLower(s[idx+1:])
		}
		return ""
	}
	// HTTPS form.
	u, err := url.Parse(s)
	if err != nil || u.Path == "" {
		return ""
	}
	result := strings.TrimPrefix(u.Path, "/")
	if result == "" {
		return ""
	}
	return strings.ToLower(result)
}

// --- Repo Manager ---

// LoadManagedReposCmd scans the workspace for git-work and plain git repositories.
func LoadManagedReposCmd(workspaceDir string) tea.Cmd {
	return func() tea.Msg {
		scan, err := gitwork.ScanWorkspace(workspaceDir)
		if err != nil {
			return ManagedReposLoadedMsg{Err: err}
		}
		repos := make([]managedRepo, 0, len(scan.GitWorkRepos)+len(scan.PlainGitRepos))
		for _, p := range scan.GitWorkRepos {
			repos = append(repos, managedRepo{
				Path:      p,
				Name:      filepath.Base(p),
				Kind:      repoKindGitWork,
				RemoteURL: readOriginRemoteURL(gitConfigPath(p, repoKindGitWork)),
			})
		}
		for _, p := range scan.PlainGitRepos {
			repos = append(repos, managedRepo{
				Path:      p,
				Name:      filepath.Base(p),
				Kind:      repoKindPlainGit,
				RemoteURL: readOriginRemoteURL(gitConfigPath(p, repoKindPlainGit)),
			})
		}
		sort.Slice(repos, func(i, j int) bool { return repos[i].Name < repos[j].Name })
		return ManagedReposLoadedMsg{Repos: repos}
	}
}

// LoadWorktreesCmd lists worktrees for a git-work repository.
// requestID guards against stale responses when selection changes quickly.
// For plain git repos, returns an empty WorktreesLoadedMsg immediately (no worktrees to show).
func LoadWorktreesCmd(client *gitwork.Client, repo managedRepo, requestID int) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return WorktreesLoadedMsg{RequestID: requestID, RepoPath: repo.Path, Err: errors.New("git-work client is unavailable")}
		}
		if repo.Kind != repoKindGitWork {
			return WorktreesLoadedMsg{RequestID: requestID, RepoPath: repo.Path}
		}
		wts, err := client.List(context.Background(), repo.Path)
		return WorktreesLoadedMsg{RequestID: requestID, RepoPath: repo.Path, Worktrees: wts, Err: err}
	}
}

// RemoveRepoCmd permanently deletes the repository directory tree from the workspace.
// This is irreversible; the caller must obtain explicit user confirmation before invoking this.
func RemoveRepoCmd(repoPath string) tea.Cmd {
	return func() tea.Msg {
		if err := os.RemoveAll(repoPath); err != nil {
			slog.Error("failed to remove repository", "path", repoPath, "error", err)
			return RepoRemovedMsg{RepoPath: repoPath, Err: err}
		}
		return RepoRemovedMsg{RepoPath: repoPath}
	}
}

// InitRepoCmd converts a plain git repository into a git-work managed repository
// by running git-work init. On success, the repo will appear as a git-work repo
// on the next scan.
func InitRepoCmd(client *gitwork.Client, repoPath string) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return RepoInitializedMsg{RepoPath: repoPath, Err: errors.New("git-work client is unavailable")}
		}
		if err := client.Init(context.Background(), repoPath); err != nil {
			slog.Error("failed to initialize git-work repo", "path", repoPath, "error", err)
			return RepoInitializedMsg{RepoPath: repoPath, Err: err}
		}
		return RepoInitializedMsg{RepoPath: repoPath}
	}
}

// initNewReposCmd converts all given plain-git repos to the git-work layout.
// It runs sequentially so failures are attributed to individual repos.
// Returns NewReposInitDoneMsg on full success, ErrMsg on first failure.
func initNewReposCmd(client *gitwork.Client, repos []string) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			client = gitwork.NewClient("")
		}
		for _, repoPath := range repos {
			if err := client.Init(context.Background(), repoPath); err != nil {
				slog.Error("failed to initialize new git-work repo", "path", repoPath, "error", err)
				return ErrMsg{Err: fmt.Errorf("initialize git-work repo %s: %w", filepath.Base(repoPath), err)}
			}
		}
		return NewReposInitDoneMsg{Count: len(repos)}
	}
}
