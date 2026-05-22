package views

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/adapter"
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
func LoadTasksCmd(svc *service.AgentSessionService, workspaceID string) tea.Cmd {
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
func SearchSessionHistoryCmd(svc *service.AgentSessionService, filter domain.SessionHistoryFilter) tea.Cmd {
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

// LoadSessionCmd fetches a single work item by ID (event-driven).
func LoadSessionCmd(svc *service.SessionService, workItemID string) tea.Cmd {
	return func() tea.Msg {
		workItem, err := svc.Get(context.Background(), workItemID)
		if err != nil {
			return ErrMsg{Err: err}
		}

		return SessionLoadedMsg{WorkItem: workItem}
	}
}

// LoadTasksForSessionCmd fetches all tasks for a work item (event-driven).
func LoadTasksForSessionCmd(svc *service.AgentSessionService, workItemID string) tea.Cmd {
	return func() tea.Msg {
		sessions, err := svc.ListByWorkItemID(context.Background(), workItemID)
		if err != nil {
			return ErrMsg{Err: err}
		}

		return TasksForSessionLoadedMsg{WorkItemID: workItemID, Sessions: sessions}
	}
}

// LoadPlanForSessionCmd fetches the plan and sub-plans for a work item (event-driven).
func LoadPlanForSessionCmd(svc *service.PlanService, workItemID string) tea.Cmd {
	return func() tea.Msg {
		plan, err := svc.GetPlanByWorkItemID(context.Background(), workItemID)
		if err != nil {
			// No plan yet is not an error worth surfacing.
			return PlanForSessionLoadedMsg{WorkItemID: workItemID, Plan: nil}
		}
		subPlans, err := svc.ListSubPlansByPlanID(context.Background(), plan.ID)
		if err != nil {
			return ErrMsg{Err: err}
		}

		return PlanForSessionLoadedMsg{WorkItemID: workItemID, Plan: &plan, SubPlans: subPlans}
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

// ApprovePlanCmd transitions work item to approved.
// Note: PlanService.ApprovePlan emits EventPlanApproved with adapter context via the event bus.
func ApprovePlanCmd(
	workItemSvc *service.SessionService,
	planSvc *service.PlanService,
	cfg *config.Config,
	bus *event.Bus,
	planID, workItemID string,
) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()

		// Build adapter context for plan approval event
		approvalCtx := buildPlanApprovalEventContext(ctx, planSvc, workItemSvc, cfg, planID, workItemID)

		if err := planSvc.ApprovePlan(ctx, planID, service.WithPlanApprovalEventContext(approvalCtx)); err != nil {
			return ErrMsg{Err: err}
		}
		if err := workItemSvc.ApprovePlan(ctx, workItemID); err != nil {
			return ErrMsg{Err: err}
		}

		return PlanApprovedMsg{PlanID: planID, WorkItemID: workItemID}
	}
}

// buildPlanApprovalEventContext builds adapter-specific context for plan approval events.
func buildPlanApprovalEventContext(ctx context.Context, planSvc *service.PlanService, workItemSvc *service.SessionService, cfg *config.Config, planID, workItemID string) service.PlanApprovalEventContext {
	approvalCtx := service.PlanApprovalEventContext{}

	workItem, err := workItemSvc.Get(ctx, workItemID)
	if err != nil {
		slog.Warn("failed to get work item for plan approval context", "work_item_id", workItemID, "error", err)
		return approvalCtx
	}

	approvalCtx.ExternalID = workItem.ExternalID
	approvalCtx.ExternalIDs = service.WorkItemEventExternalIDs(workItem)

	plan, err := planSvc.GetPlan(ctx, planID)
	if err != nil {
		slog.Warn("failed to get plan for approval context", "plan_id", planID, "error", err)
		return approvalCtx
	}

	var commentMode config.IssueCommentContent
	if cfg != nil {
		commentMode = cfg.IssueCommentContentForSource(workItem.Source)
	} else {
		commentMode = config.IssueCommentSubPlan
	}
	if commentBody := buildIssueCommentBody(ctx, planSvc, commentMode, plan); commentBody != "" {
		approvalCtx.CommentBody = commentBody
	}

	if cfg != nil {
		approvalCtx.RepoCommentScopes = cfg.IssueActionScopesForWorkItem(workItem)
	}

	return approvalCtx
}

// AnswerQuestionCmd delivers a human answer through the route required by the question's stage.
func AnswerQuestionCmd(svc *service.QuestionService, sessionSvc *service.AgentSessionService, registry *orchestrator.SessionRegistry, foreman *orchestrator.Foreman, bus *event.Bus, questionID, answer, answeredBy string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		q, err := svc.Get(ctx, questionID)
		if err != nil {
			return ErrMsg{Err: err}
		}
		switch q.Stage {
		case domain.AgentSessionPhasePlanning:
			if registry != nil {
				if err := registry.SendAnswer(ctx, q.AgentSessionID, answer); err != nil && !errors.Is(err, orchestrator.ErrSessionNotRunning) {
					return ErrMsg{Err: fmt.Errorf("answer planning question %s: %w", questionID, err)}
				}
			}
			if err := answerPlanningQuestion(ctx, svc, sessionSvc, bus, q, answer, answeredBy); err != nil {
				return ErrMsg{Err: err}
			}
			return ActionDoneMsg{Message: "Answer sent to planner"}

		case domain.AgentSessionPhaseImplementation, domain.AgentSessionPhaseReview, "":
			if foreman != nil {
				err := foreman.ResolveEscalated(context.Background(), questionID, answer)
				if err == nil {
					return ActionDoneMsg{Message: "Answer submitted"}
				}
				if !errors.Is(err, orchestrator.ErrQuestionNotEscalated) {
					return ErrMsg{Err: err}
				}
			}
			if sessionSvc != nil && q.AgentSessionID != "" {
				if err := sessionSvc.ResumeFromAnswer(context.Background(), q.AgentSessionID); err != nil {
					slog.Warn("failed to resume session on answer fallback", "error", err, "question_id", questionID)
				}
			}
			if err := svc.Answer(context.Background(), questionID, answer, answeredBy); err != nil {
				return ErrMsg{Err: err}
			}
			return ActionDoneMsg{Message: "Answer submitted"}

		case domain.AgentSessionPhaseManual:
			// Manual sessions receive answers directly via the registry.
			if registry != nil {
				if err := registry.SendAnswer(ctx, q.AgentSessionID, answer); err != nil && !errors.Is(err, orchestrator.ErrSessionNotRunning) {
					return ErrMsg{Err: fmt.Errorf("answer manual question %s: %w", questionID, err)}
				}
			}
			if sessionSvc != nil {
				if err := sessionSvc.ResumeFromAnswer(ctx, q.AgentSessionID); err != nil {
					slog.Warn("failed to resume manual session from answer", "error", err, "question_id", questionID)
				}
			}
			if err := svc.Answer(ctx, questionID, answer, answeredBy); err != nil {
				return ErrMsg{Err: err}
			}
			return ActionDoneMsg{Message: "Answer submitted"}

		default:
			return ErrMsg{Err: fmt.Errorf("answer question %s: unsupported stage %q", questionID, q.Stage)}
		}
	}
}

func answerPlanningQuestion(ctx context.Context, svc *service.QuestionService, sessionSvc *service.AgentSessionService, bus *event.Bus, q domain.Question, answer, answeredBy string) error {
	if err := svc.Answer(ctx, q.ID, answer, answeredBy); err != nil {
		return err
	}
	if sessionSvc != nil && q.AgentSessionID != "" {
		if err := sessionSvc.ResumeFromAnswer(ctx, q.AgentSessionID); err != nil {
			return fmt.Errorf("resume planning session after answer: %w", err)
		}
	}
	if err := orchestrator.PublishQuestionAnswered(ctx, bus, q.ID, q.AgentSessionID); err != nil {
		return fmt.Errorf("publish planning question answered event: %w", err)
	}
	return nil
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
// initializeWorkspaceServicesCmd rebuilds the service graph for a new workspace
// so adapters, harnesses, and instance registration use the new workspace root.
func initializeWorkspaceServicesCmd(provider ServiceProvider, runtimeCtx RuntimeContext, workspaceID, workspaceName, workspaceDir string) tea.Cmd {
	return func() tea.Msg {
		serviceMgr, ok := provider.(*ServiceManager)
		if !ok {
			return ErrMsg{Err: errors.New("service manager is unavailable")}
		}
		if runtimeCtx.Cfg == nil {
			return ErrMsg{Err: errors.New("config is unavailable")}
		}

		services := provider.GetServices()
		if services == nil {
			services = &Services{}
		}
		current := *services
		reloaded, err := serviceMgr.InitWorkspace(context.Background(), runtimeCtx.Cfg, current, workspaceID, workspaceName, workspaceDir)
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
		if err := reloaded.Instance.Create(context.Background(), inst); err != nil {
			slog.Warn("failed to register instance after workspace initialization", "workspace_id", workspaceID, "err", err)
		} else {
			reloaded.InstanceID = inst.ID
		}

		sessionsDir, _ := config.SessionsDir()

		return WorkspaceServicesReloadedMsg{Reload: viewsServicesReload{
			Services:    *reloaded,
			SessionsDir: sessionsDir,
			Cfg:         runtimeCtx.Cfg,
		}, Message: "Workspace initialized"}
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
			slog.Warn("agent session log scanner error", "error", scanErr, "agent_session_id", sessionID)
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
	taskSvc *service.AgentSessionService,
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

		for _, agentSession := range tasks {
			if agentSession.Status != domain.AgentSessionRunning && agentSession.Status != domain.AgentSessionWaitingForAnswer {
				continue
			}
			// Never interrupt sessions owned by the current instance.
			if agentSession.OwnerInstanceID != nil && *agentSession.OwnerInstanceID == currentInstanceID {
				continue
			}
			ownerAlive := agentSession.OwnerInstanceID != nil && liveSet[*agentSession.OwnerInstanceID]
			if ownerAlive {
				continue
			}
			if err := taskSvc.Interrupt(ctx, agentSession.ID); err != nil {
				slog.Error("reconcile: failed to interrupt orphaned agent session",
					"agent_session_id", agentSession.ID, "workspace_id", workspaceID, "error", err)
			}
		}

		return nil
	}
}

// RestartPlanningCmd resumes an interrupted planning session with native harness
// resume data when available, falling back to a fresh planning start when no
// interrupted session exists. Prompt is optional operator guidance delivered as
// revision feedback when native resume is used.
func RestartPlanningCmd(ctx context.Context, workItemSvc *service.SessionService, planningSvc *orchestrator.PlanningService, sessionSvc *service.AgentSessionService, workItemID string, prompt string) tea.Cmd {
	return func() tea.Msg {
		// Roll work item back from interrupted state so the resume can transition it
		// back to planning (ResumeInterruptedPlanning requires SessionPlanning state).
		if err := workItemSvc.RollbackPlanningInterrupt(ctx, workItemID); err != nil {
			return ErrMsg{Err: err}
		}

		// Find the interrupted planning session for this work item to resume it.
		sessions, listErr := sessionSvc.ListByWorkItemID(ctx, workItemID)
		if listErr != nil {
			return ErrMsg{Err: fmt.Errorf("list sessions for work item: %w", listErr)}
		}
		var interrupted *domain.AgentSession
		for i := range sessions {
			if sessions[i].Status == domain.AgentSessionInterrupted &&
				sessions[i].Phase == domain.AgentSessionPhasePlanning {
				interrupted = &sessions[i]
				break
			}
		}

		if interrupted != nil {
			// Resume the interrupted planning session with native resume data.
			// Fail the old interrupted session so the overview clears the action card.
			if failErr := sessionSvc.Fail(ctx, interrupted.ID, nil); failErr != nil {
				slog.Warn("failed to fail interrupted planning session before resume",
					"agent_session_id", interrupted.ID, "error", failErr)
			}
			_, err := planningSvc.ResumeInterruptedPlanning(ctx, *interrupted, prompt)
			if err != nil {
				return ErrMsg{Err: err}
			}
			return PlanningRestartedMsg{WorkItemID: workItemID, Message: "Planning resumed"}
		}

		// No interrupted session found — fall back to fresh planning.
		if _, err := planningSvc.Plan(ctx, workItemID); err != nil {
			return ErrMsg{Err: err}
		}
		return PlanningRestartedMsg{WorkItemID: workItemID, Message: "Planning restarted"}
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

// RunImplementationCmd dispatches the implementation pipeline for an approved plan.
// It runs asynchronously - completion is signaled via EventWorkItemCompleted.
func RunImplementationCmd(ctx context.Context, svc *orchestrator.ImplementationService, planID string) tea.Cmd {
	return func() tea.Msg {
		go func() {
			_, err := svc.Implement(ctx, planID)
			if err != nil {
				slog.Error("implementation failed", "error", err, "plan_id", planID)
			}
			// On success, EventWorkItemCompleted is emitted by the service.
		}()
		return nil // Don't block - let TUI continue processing events
	}
}

// FinalizeWorkItemCmd retries final commit/push/completion for an implementing work item whose repo tasks are complete.
// FinalizeWorkItemCmd retries final commit/push/completion for an implementing work item whose repo tasks are complete.
// It runs asynchronously - completion is signaled via EventWorkItemCompleted.
func FinalizeWorkItemCmd(ctx context.Context, svc *orchestrator.ImplementationService, workItemID string) tea.Cmd {
	return func() tea.Msg {
		go func() {
			_, err := svc.FinalizeWorkItem(ctx, workItemID)
			if err != nil {
				slog.Error("finalize work item failed", "error", err, "work_item_id", workItemID)
			}
			// On success, EventWorkItemCompleted is emitted by the service.
		}()
		return nil // Don't block
	}
}

// ResumeAllSessionsForWorkItemCmd resumes all non-superseded, non-planning-phase
// interrupted sessions for a work item. Planning-phase interruptions are handled
// by RestartPlanningCmd instead.
func ResumeAllSessionsForWorkItemCmd(
	ctx context.Context,
	workItemSvc *service.SessionService,
	planningSvc *orchestrator.PlanningService,
	resumption *orchestrator.Resumption,
	sessionSvc *service.AgentSessionService,
	planSvc *service.PlanService,
	foreman *orchestrator.Foreman,
	workItemID string,
	instanceID string,
) tea.Cmd {
	return func() tea.Msg {
		sessions, err := sessionSvc.ListByWorkItemID(ctx, workItemID)
		if err != nil {
			return ErrMsg{Err: err}
		}

		activeSubPlans := make(map[string]bool)
		hasPlanningActive := false
		for _, s := range sessions {
			if s.Status == domain.AgentSessionRunning || s.Status == domain.AgentSessionPending ||
				s.Status == domain.AgentSessionCompleted || s.Status == domain.AgentSessionWaitingForAnswer {
				if s.Phase == domain.AgentSessionPhasePlanning {
					hasPlanningActive = true
				} else if s.SubPlanID != "" {
					activeSubPlans[s.SubPlanID] = true
				}
			}
		}

		toResume := make([]domain.AgentSession, 0, len(sessions))
		var planningInterrupted *domain.AgentSession
		for _, s := range sessions {
			if s.Status != domain.AgentSessionInterrupted {
				continue
			}
			if s.Phase == domain.AgentSessionPhasePlanning {
				if !hasPlanningActive {
					planningInterrupted = &s
				}
				continue
			}
			if s.SubPlanID != "" && activeSubPlans[s.SubPlanID] {
				continue
			}
			toResume = append(toResume, s)
		}

		if planningInterrupted != nil {
			if err := workItemSvc.RollbackPlanningInterrupt(ctx, workItemID); err != nil {
				return ErrMsg{Err: err}
			}
			// Fail the old interrupted session so the overview clears the action card.
			if failErr := sessionSvc.Fail(ctx, planningInterrupted.ID, nil); failErr != nil {
				slog.Warn("failed to fail interrupted planning session before resume",
					"agent_session_id", planningInterrupted.ID, "error", failErr)
			}
			_, err := planningSvc.ResumeInterruptedPlanning(ctx, *planningInterrupted, "")
			if err != nil {
				return ErrMsg{Err: err}
			}
			return PlanningRestartedMsg{WorkItemID: workItemID, Message: "Planning resumed"}
		}

		if len(toResume) == 0 {
			return SessionResumedMsg{WorkItemID: workItemID, Message: "No resumable tasks"}
		}

		foremanPlanID := ""
		if foreman != nil && planSvc != nil {
			plan, err := planSvc.GetPlanByWorkItemID(ctx, workItemID)
			if err != nil {
				return ErrMsg{Err: fmt.Errorf("get plan for foreman resume: %w", err)}
			}
			if plan.Status == domain.PlanApproved {
				if err := foreman.Start(ctx, plan.ID, ""); err != nil {
					return ErrMsg{Err: fmt.Errorf("start foreman for resume: %w", err)}
				}
				foremanPlanID = plan.ID
			}
		}

		succeeded := 0
		for _, s := range toResume {
			if _, err := resumption.ResumeSession(ctx, s, instanceID); err != nil {
				slog.Warn("resume all: failed to resume session",
					"agent_session_id", s.ID, "error", err)
				continue
			}
			succeeded++
		}

		msg := "Resumed 1 task"
		if succeeded != 1 {
			msg = fmt.Sprintf("Resumed %d tasks", succeeded)
		}
		return SessionResumedMsg{WorkItemID: workItemID, ForemanPlanID: foremanPlanID, Message: msg}
	}
}

// OverrideAcceptCmd marks a work item completed despite outstanding critiques.
// EventWorkItemCompleted is emitted by CompleteWorkItem → Transition → emitStateChange.
func OverrideAcceptCmd(
	workItemSvc *service.SessionService,
	workItemID string,
) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		if err := workItemSvc.CompleteWorkItem(ctx, workItemID); err != nil {
			return ErrMsg{Err: err}
		}

		return ActionDoneMsg{Message: "Work item accepted"}
	}
}

// RetryFailedCmd transitions a failed work item back to implementing and re-runs
// the implementation pipeline for failed sub-plans.
// It runs asynchronously - completion is signaled via EventWorkItemCompleted.
func RetryFailedCmd(ctx context.Context, workItemSvc *service.SessionService, implSvc *orchestrator.ImplementationService, planID, workItemID string) tea.Cmd {
	return func() tea.Msg {
		go func() {
			if err := workItemSvc.RetryFailedWorkItem(ctx, workItemID); err != nil {
				slog.Error("retry failed work item failed", "error", err, "work_item_id", workItemID)
				return
			}
			// Re-run implementation — Implement() will reset failed sub-plans
			// to pending and only execute those.
			_, err := implSvc.Implement(ctx, planID)
			if err != nil {
				slog.Error("retry implementation failed", "error", err, "plan_id", planID)
			}
			// On success, EventWorkItemCompleted is emitted by the service.
		}()
		return nil // Don't block
	}
}

// archiveSessionCmd archives a work item and returns a completion message.
func archiveSessionCmd(svc *service.SessionService, workItemID string, focusAfterArchive bool, focusWorkItemID string) tea.Cmd {
	return func() tea.Msg {
		if err := svc.Archive(context.Background(), workItemID); err != nil {
			return ErrMsg{Err: fmt.Errorf("archive session: %w", err)}
		}
		return SessionArchivedMsg{
			WorkItemID:        workItemID,
			Message:           "Session archived",
			FocusAfterArchive: focusAfterArchive,
			FocusWorkItemID:   focusWorkItemID,
		}
	}
}

// unarchiveSessionCmd unarchives a work item and returns a completion message.
func unarchiveSessionCmd(svc *service.SessionService, workItemID string) tea.Cmd {
	return func() tea.Msg {
		if err := svc.Unarchive(context.Background(), workItemID); err != nil {
			return ErrMsg{Err: fmt.Errorf("unarchive session: %w", err)}
		}
		return SessionUnarchivedMsg{WorkItemID: workItemID, Message: "Session unarchived"}
	}
}

// buildRepoCommentScopes builds a map of repo identifiers to comment scopes for plan-approved events.
func buildRepoCommentScopes(workItem domain.Session, cfg *config.Config) map[string]string {
	scopes := make(map[string]string)
	for _, itemID := range workItem.SourceItemIDs {
		repoKey := extractRepoKey(workItem.Source, itemID)
		if repoKey == "" {
			continue
		}
		if scope := cfg.IssueActionScopeForRepo(repoKey); scope != "" {
			scopes[repoKey] = string(scope)
		}
	}
	return scopes
}

// extractRepoKey extracts the repository identifier from a source item ID.
func extractRepoKey(source, itemID string) string {
	switch source {
	case "github":
		// GitHub item IDs are in format "owner/repo#number"
		if idx := strings.IndexByte(itemID, '#'); idx > 0 {
			return strings.TrimSpace(itemID[:idx])
		}
	case "gitlab":
		// GitLab item IDs are in format "projectPath#number"
		if idx := strings.IndexByte(itemID, '#'); idx > 0 {
			return strings.TrimSpace(itemID[:idx])
		}
	}
	return ""
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

// publishSystemEvent publishes a system event to the event bus.
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

// SkipQuestionCmd marks a question as skipped and unblocks the pending live harness when possible.
func SkipQuestionCmd(svc *service.QuestionService, sessionSvc *service.AgentSessionService, registry *orchestrator.SessionRegistry, foreman *orchestrator.Foreman, bus *event.Bus, questionID string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		q, err := svc.Get(ctx, questionID)
		if err != nil {
			return ErrMsg{Err: err}
		}
		if q.Stage == domain.AgentSessionPhasePlanning {
			if registry != nil {
				if err := registry.SendAnswer(ctx, q.AgentSessionID, ""); err != nil && !errors.Is(err, orchestrator.ErrSessionNotRunning) {
					return ErrMsg{Err: fmt.Errorf("skip planning question %s: %w", questionID, err)}
				}
			}
			if err := answerPlanningQuestion(ctx, svc, sessionSvc, bus, q, "", "human"); err != nil {
				return ErrMsg{Err: err}
			}
			return ActionDoneMsg{Message: "Question skipped"}
		} else if foreman != nil {
			err := foreman.ResolveEscalated(ctx, questionID, "")
			if err == nil {
				return ActionDoneMsg{Message: "Question skipped"}
			}
			if !errors.Is(err, orchestrator.ErrQuestionNotEscalated) {
				return ErrMsg{Err: err}
			}
		}
		if sessionSvc != nil && q.AgentSessionID != "" {
			if err := sessionSvc.ResumeFromAnswer(ctx, q.AgentSessionID); err != nil {
				slog.Warn("failed to resume session on skip fallback", "error", err, "question_id", questionID)
			}
		}
		if err := svc.Answer(ctx, questionID, "", "human"); err != nil {
			return ErrMsg{Err: err}
		}

		return ActionDoneMsg{Message: "Question skipped"}
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

// shellEscape escapes a string for safe use in osascript strings.
func shellEscape(s string) string {
	return strings.ReplaceAll(s, "\"", "\\\"")
}

// OpenTerminalCmd opens a new Terminal.app window in the specified directory.
func OpenTerminalCmd(dir string) tea.Cmd {
	return func() tea.Msg {
		script := fmt.Sprintf(`tell application "Terminal"
		do script "cd %s"
		activate
	end tell`, shellEscape(dir))
		cmd := exec.CommandContext(context.TODO(), "osascript", "-e", script)
		if err := cmd.Run(); err != nil {
			slog.Warn("failed to open terminal", "path", dir, "error", err)
		}
		return nil
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

// FollowUpSessionCmd starts a follow-up agent session on a completed task and
// blocks until the session finishes. Returns FollowUpSessionCompleteMsg when done.
func FollowUpSessionCmd(ctx context.Context, resumption *orchestrator.Resumption, svc *service.AgentSessionService, taskID, feedback, instanceID string) tea.Cmd {
	return func() tea.Msg {
		task, err := svc.Get(ctx, taskID)
		if err != nil {
			return ErrMsg{Err: fmt.Errorf("get task for follow-up: %w", err)}
		}
		result, err := resumption.FollowUpSession(ctx, task, feedback, instanceID)
		if err != nil {
			return ErrMsg{Err: fmt.Errorf("start follow-up session: %w", err)}
		}
		resumption.WaitAndComplete(ctx, result.Session.ID, result.HarnessSession)
		return FollowUpSessionCompleteMsg{WorkItemID: task.WorkItemID}
	}
}

// FollowUpFailedSessionCmd starts a follow-up agent session on a failed task and
// blocks until the session finishes. Returns FollowUpSessionCompleteMsg when done.
func FollowUpFailedSessionCmd(ctx context.Context, resumption *orchestrator.Resumption, svc *service.AgentSessionService, taskID, feedback, instanceID string) tea.Cmd {
	return func() tea.Msg {
		task, err := svc.Get(ctx, taskID)
		if err != nil {
			return ErrMsg{Err: fmt.Errorf("get task for failed follow-up: %w", err)}
		}
		result, err := resumption.FollowUpFailedSession(ctx, task, feedback, instanceID)
		if err != nil {
			return ErrMsg{Err: fmt.Errorf("start failed follow-up session: %w", err)}
		}
		resumption.WaitAndComplete(ctx, result.Session.ID, result.HarnessSession)
		return FollowUpSessionCompleteMsg{WorkItemID: task.WorkItemID}
	}
}

// FollowUpPlanCmd starts a follow-up re-planning cycle for a completed work item.
func FollowUpPlanCmd(ctx context.Context, svc *orchestrator.PlanningService, workItemID, feedback string) tea.Cmd {
	return func() tea.Msg {
		_, err := svc.FollowUpPlan(ctx, workItemID, feedback)
		return FollowUpPlanResultMsg{WorkItemID: workItemID, Err: err}
	}
}

// ResolveReviewAddressDispatchCmd looks up completed tasks for the given work
// item and matches each entry in perRepo (repoName -> feedback) to the most
// recently updated completed Task for that repository. Runs off the UI thread
// so a slow DB query does not block Bubble Tea's Update loop. The returned
// message carries Task.ID -> feedback for matched repos and a list of skipped
// repo names with no completed task.
func ResolveReviewAddressDispatchCmd(ctx context.Context, svc *service.AgentSessionService, workItemID string, perRepo map[string]string) tea.Cmd {
	return func() tea.Msg {
		total := len(perRepo)
		tasks, err := svc.ListByWorkItemID(ctx, workItemID)
		if err != nil {
			return ReviewAddressDispatchResultMsg{WorkItemID: workItemID, Total: total, Err: err}
		}
		// Build repoName -> most-recently-updated completed session.
		completedByRepo := make(map[string]domain.AgentSession)
		for _, t := range tasks {
			if t.Status != domain.AgentSessionCompleted || t.RepositoryName == "" {
				continue
			}
			existing, ok := completedByRepo[t.RepositoryName]
			if !ok || t.UpdatedAt.After(existing.UpdatedAt) {
				completedByRepo[t.RepositoryName] = t
			}
		}
		dispatched := make(map[string]string, len(perRepo))
		var skipped []string
		for repoName, feedback := range perRepo {
			task, ok := completedByRepo[repoName]
			if !ok {
				skipped = append(skipped, repoName)
				continue
			}
			dispatched[task.ID] = feedback
		}
		return ReviewAddressDispatchResultMsg{
			WorkItemID: workItemID,
			Dispatched: dispatched,
			Skipped:    skipped,
			Total:      total,
		}
	}
}

// FetchReviewCommentsCmd fetches unresolved review comments for the given PRs/MRs
// in parallel. mode is either "" (initial fetch → ReviewCommentsFetchedMsg) or
// "address"/"replan" (silent re-fetch → ReviewCommentsRefetchedMsg carrying the
// dispatch intent). generation tags the resulting message with the overlay
// generation captured at launch so the App handler can drop stale results from
// a cancelled/replaced overlay session.
//
// Per-item failures are captured in the message's Err but partial successes are
// preserved in Result; callers MUST check len(Result) before treating Err as
// fatal so a transient failure on one repo does not discard data from others.
func FetchReviewCommentsCmd(fetcher *adapter.ReviewCommentDispatcher, workItemID string, items []ArtifactItem, mode string, generation int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		type fetchOutcome struct {
			itemID   string
			repoName string
			ref      string
			comments []adapter.ReviewComment
			err      error
		}
		outcomes := make(chan fetchOutcome, len(items))
		var wg sync.WaitGroup
		for _, it := range items {
			identifier, number, ok := parseArtifactFetchArgs(it)
			if !ok {
				slog.Warn("skipping artifact with unparsable ref", "ref", it.Ref, "repo", it.RepoName)
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				comments, err := fetcher.FetchReviewCommentsForTarget(ctx, adapter.ReviewCommentTarget{
					Provider:       it.Provider,
					RepoIdentifier: identifier,
					Number:         number,
					WorktreePath:   it.WorktreePath,
				})
				outcomes <- fetchOutcome{
					itemID:   it.ID,
					repoName: it.RepoName,
					ref:      it.Ref,
					comments: comments,
					err:      err,
				}
			}()
		}
		go func() {
			wg.Wait()
			close(outcomes)
		}()
		result := make(map[string][]adapter.ReviewComment, len(items))
		var firstErr error
		for o := range outcomes {
			if o.err != nil {
				slog.Warn("fetch review comments failed", "repo", o.repoName, "ref", o.ref, "err", o.err)
				if firstErr == nil {
					firstErr = fmt.Errorf("%s %s: %w", o.repoName, o.ref, o.err)
				}
				continue
			}
			if len(o.comments) > 0 {
				result[o.itemID] = o.comments
			}
		}
		fetchedAt := time.Now()
		if mode == "" {
			return ReviewCommentsFetchedMsg{
				WorkItemID: workItemID,
				Generation: generation,
				Result:     result,
				FetchedAt:  fetchedAt,
				Err:        firstErr,
			}
		}
		return ReviewCommentsRefetchedMsg{
			WorkItemID: workItemID,
			Generation: generation,
			Result:     result,
			FetchedAt:  fetchedAt,
			Mode:       mode,
			Err:        firstErr,
		}
	}
}

// parseArtifactFetchArgs extracts (repoIdentifier, number) from an ArtifactItem.
// For GitHub: returns ("owner/repo", number); the ref is `#<n>`.
// For GitLab: returns (projectPath, iid); the ref is `!<n>`.
// Returns ok=false when the ref does not have the expected provider sigil or
// the trailing digits do not parse as an integer; callers MUST log/skip.
func parseArtifactFetchArgs(it ArtifactItem) (string, int, bool) {
	var trimmed string
	switch it.Provider {
	case "github":
		var ok bool
		trimmed, ok = strings.CutPrefix(it.Ref, "#")
		if !ok {
			return "", 0, false
		}
		if repoName := gitHubRepoNameFromPullURL(it.URL); repoName != "" {
			it.RepoName = repoName
		}
	case "gitlab":
		var ok bool
		trimmed, ok = strings.CutPrefix(it.Ref, "!")
		if !ok {
			return "", 0, false
		}
		projectPath := gitLabProjectPathFromMRURL(it.URL)
		if projectPath != "" {
			it.RepoName = projectPath
		}
	default:
		return "", 0, false
	}
	n, err := strconv.Atoi(trimmed)
	if err != nil {
		return "", 0, false
	}
	return it.RepoName, n, true
}

func gitHubRepoNameFromPullURL(rawURL string) string {
	s := strings.TrimSpace(rawURL)
	if s == "" {
		return ""
	}

	u, err := url.Parse(s)
	if err != nil {
		slog.Debug("falling back to artifact repo name after parsing GitHub PR URL failed", "url", rawURL, "error", err)
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 4 || parts[0] == "" || parts[1] == "" || parts[2] != "pull" {
		return ""
	}

	return parts[0] + "/" + parts[1]
}

func gitLabProjectPathFromMRURL(rawURL string) string {
	s := strings.TrimSpace(rawURL)
	if s == "" {
		return ""
	}

	u, err := url.Parse(s)
	if err != nil {
		slog.Debug("falling back to artifact repo name after parsing GitLab MR URL failed", "url", rawURL, "error", err)
		return ""
	}

	path, unescapeErr := url.PathUnescape(strings.TrimPrefix(u.EscapedPath(), "/"))
	if unescapeErr != nil {
		slog.Debug("falling back to escaped GitLab MR URL path", "url", rawURL, "error", unescapeErr)
		path = strings.TrimPrefix(u.Path, "/")
	}
	projectPath, _, ok := strings.Cut(path, "/-/merge_requests/")
	if !ok || projectPath == "" {
		return ""
	}

	return projectPath
}

// StartupIntegrationsStartCmd defers heavy startup integrations until after the first frame.
func StartupIntegrationsStartCmd() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg {
		return StartupIntegrationsStartMsg{}
	})
}

// StartupIntegrationsCmd completes the deferred startup service graph in the background.
func StartupIntegrationsCmd(provider ServiceProvider, runtimeCtx RuntimeContext) tea.Cmd {
	return func() tea.Msg {
		serviceMgr, ok := provider.(*ServiceManager)
		if !ok {
			return StartupIntegrationsReadyMsg{Err: errors.New("service manager is unavailable")}
		}
		if runtimeCtx.Cfg == nil {
			return StartupIntegrationsReadyMsg{Err: errors.New("config is unavailable")}
		}
		services := provider.GetServices()
		if services == nil {
			return StartupIntegrationsReadyMsg{Err: errors.New("services are unavailable")}
		}
		current := *services
		reloaded, err := serviceMgr.Rebuild(context.Background(), runtimeCtx.Cfg, current)
		if err != nil {
			return StartupIntegrationsReadyMsg{Err: err}
		}

		// Refresh settings diagnostics after rebuild.
		if err := reloaded.Settings.RefreshWithDiagnostics(context.Background(), runtimeCtx.Cfg); err != nil {
			return StartupIntegrationsReadyMsg{Err: err}
		}

		sessionsDir, _ := config.SessionsDir()
		return StartupIntegrationsReadyMsg{Reload: viewsServicesReload{
			Services:    *reloaded,
			SessionsDir: sessionsDir,
			Cfg:         runtimeCtx.Cfg,
		}}
	}
}

// SettingsDiagnosticsStartCmd fires after the first frame to schedule diagnostics.
func SettingsDiagnosticsStartCmd() tea.Cmd {
	return func() tea.Msg {
		return SettingsDiagnosticsStartMsg{}
	}
}

// SettingsDiagnosticsCmd runs harness diagnostics asynchronously and delivers a completion message.
func SettingsDiagnosticsCmd(settings SettingsService, cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		err := settings.RefreshWithDiagnostics(context.Background(), cfg)
		return SettingsDiagnosticsReadyMsg{Err: err}
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

// LoadReposCmd fetches repos from the repo source at sourceIndex, walking
// pagination up to maxPages or until the source signals no more results.
// requestID is stamped onto the response so the overlay can discard stale results.
func LoadReposCmd(sources []adapter.RepoSource, sourceIndex int, search string, pageSize, maxPages, requestID int, ownedOnly bool) tea.Cmd {
	return func() tea.Msg {
		if len(sources) == 0 {
			return RepoListLoadedMsg{RequestID: requestID, Repos: []adapter.RepoItem{}}
		}
		idx := sourceIndex
		if idx < 0 || idx >= len(sources) {
			idx = 0
		}
		if pageSize <= 0 {
			pageSize = 30
		}
		if maxPages <= 0 {
			maxPages = 1
		}
		// Allow more wall-clock time when paginating across multiple pages.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		var aggregated []adapter.RepoItem
		hasMore := false
		for page := 1; page <= maxPages; page++ {
			opts := adapter.RepoListOpts{Search: search, Limit: pageSize, Page: page, OwnedOnly: ownedOnly}
			result, err := sources[idx].ListRepos(ctx, opts)
			if err != nil {
				// Surface the error but keep any results gathered so far.
				return RepoListLoadedMsg{RequestID: requestID, Repos: aggregated, Errs: []error{err}}
			}
			if result == nil {
				break
			}
			aggregated = append(aggregated, result.Repos...)
			hasMore = result.HasMore
			if !result.HasMore {
				break
			}
		}
		return RepoListLoadedMsg{RequestID: requestID, Repos: aggregated, HasMore: hasMore}
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
	// SCP-style SSH form: git@github.com:owner/repo (no scheme prefix).
	if !strings.Contains(s, "://") && strings.Contains(s, "@") && strings.Contains(s, ":") {
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
// Emits RepoInitProgressMsg after each repo and NewReposInitDoneMsg on full success.
func initNewReposCmd(client *gitwork.Client, repos []string) tea.Cmd {
	if client == nil {
		client = gitwork.NewClient("")
	}
	total := len(repos)
	cmds := make([]tea.Cmd, 0, total*2+1)

	for i, repoPath := range repos {
		repoPath := repoPath // capture loop var
		cmds = append(cmds, func() tea.Msg {
			if err := client.Init(context.Background(), repoPath); err != nil {
				slog.Error("failed to initialize new git-work repo", "path", repoPath, "error", err)
				return ErrMsg{Err: fmt.Errorf("initialize git-work repo %s: %w", filepath.Base(repoPath), err)}
			}
			return RepoInitProgressMsg{Initialized: i + 1, Total: total}
		})
	}

	cmds = append(cmds, func() tea.Msg {
		return NewReposInitDoneMsg{Count: total}
	})

	return tea.Sequence(cmds...)
}
