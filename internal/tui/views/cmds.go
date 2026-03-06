package views

import (
	"bufio"
	"context"
	"io"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/gitwork"
	"github.com/beeemT/substrate/internal/orchestrator"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
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

// LoadWorkItemsCmd fetches work items for a workspace from the DB.
func LoadWorkItemsCmd(svc *service.WorkItemService, workspaceID string) tea.Cmd {
	return func() tea.Msg {
		filter := repository.WorkItemFilter{WorkspaceID: &workspaceID}
		items, err := svc.List(context.Background(), filter)
		if err != nil {
			return ErrMsg{Err: err}
		}
		return WorkItemsLoadedMsg{Items: items}
	}
}

// LoadSessionsCmd fetches all agent sessions for a workspace.
func LoadSessionsCmd(svc *service.SessionService, workspaceID string) tea.Cmd {
	return func() tea.Msg {
		sessions, err := svc.ListByWorkspaceID(context.Background(), workspaceID)
		if err != nil {
			return ErrMsg{Err: err}
		}
		return SessionsLoadedMsg{Sessions: sessions}
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
func ApprovePlanCmd(workItemSvc *service.WorkItemService, planSvc *service.PlanService, planID, workItemID string) tea.Cmd {
	return func() tea.Msg {
		if err := planSvc.ApprovePlan(context.Background(), planID); err != nil {
			return ErrMsg{Err: err}
		}
		if err := workItemSvc.ApprovePlan(context.Background(), workItemID); err != nil {
			return ErrMsg{Err: err}
		}
		return ActionDoneMsg{Message: "Plan approved"}
	}
}

// AnswerQuestionCmd records a human answer for a question.
func AnswerQuestionCmd(svc *service.QuestionService, questionID, answer, answeredBy string) tea.Cmd {
	return func() tea.Msg {
		if err := svc.Answer(context.Background(), questionID, answer, answeredBy); err != nil {
			return ErrMsg{Err: err}
		}
		return ActionDoneMsg{Message: "Answer submitted"}
	}
}

// HeartbeatCmd sends a heartbeat for the current instance.
func HeartbeatCmd(svc *service.InstanceService, instanceID string) tea.Cmd {
	return func() tea.Msg {
		_ = svc.UpdateHeartbeat(context.Background(), instanceID)
		return nil
	}
}

// DeleteInstanceCmd removes the instance record on clean shutdown.
func DeleteInstanceCmd(svc *service.InstanceService, instanceID string) tea.Cmd {
	return func() tea.Msg {
		_ = svc.Delete(context.Background(), instanceID)
		return nil
	}
}

// WorkspaceHealthCheckCmd scans the current directory for repos.
func WorkspaceHealthCheckCmd(dir string) tea.Cmd {
	return func() tea.Msg {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return WorkspaceHealthCheckMsg{Error: err}
		}
		check := domain.WorkspaceHealthCheck{}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			path := filepath.Join(dir, e.Name())
			if gitwork.IsGitWorkRepo(path) {
				check.GitWorkRepos = append(check.GitWorkRepos, path)
			} else if isPlainGitClone(path) {
				check.PlainGitClones = append(check.PlainGitClones, path)
			}
		}
		return WorkspaceHealthCheckMsg{Check: check}
	}
}

// isPlainGitClone reports whether dir is a regular git clone (has .git but not .bare).
func isPlainGitClone(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	if err != nil {
		return false
	}
	_, bareErr := os.Stat(filepath.Join(dir, ".bare"))
	return os.IsNotExist(bareErr)
}

// TailSessionLogCmd reads new lines from a session log file since the given byte offset.
// It handles log rotation by detecting size regression.
func TailSessionLogCmd(logPath string, sessionID string, since int64) tea.Cmd {
	return func() tea.Msg {
		stat, err := os.Stat(logPath)
		if err != nil {
			return SessionLogLinesMsg{SessionID: sessionID, NextOffset: since}
		}
		offset := since
		if stat.Size() < since {
			offset = 0 // rotation detected
		}
		if stat.Size() == offset {
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
		var lines []string
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		// Use actual FD position, not stat size, to avoid skipping bytes
		// written between EOF detection and the stat call.
		pos, seekErr := f.Seek(0, io.SeekCurrent)
		newOffset := offset
		if seekErr == nil {
			newOffset = pos
		}
		// scanner.Err() is non-nil on I/O error or line > 1 MiB.
		// Return whatever lines were collected; next call resumes from pos.
		_ = scanner.Err()
		return SessionLogLinesMsg{SessionID: sessionID, Lines: lines, NextOffset: newOffset}
	}
}

// SavePlanCmd persists updated plan content to the DB after $EDITOR edit.
func SavePlanCmd(planSvc *service.PlanService, planID, content string) tea.Cmd {
	return func() tea.Msg {
		if err := planSvc.UpdatePlanContent(context.Background(), planID, content); err != nil {
			return ErrMsg{Err: err}
		}
		return ActionDoneMsg{Message: "Plan updated"}
	}
}

// RejectPlanCmd transitions a work item and its plan back to the Ingested state.
func RejectPlanCmd(workItemSvc *service.WorkItemService, planSvc *service.PlanService, workItemID, planID, reason string) tea.Cmd {
	return func() tea.Msg {
		if err := planSvc.RejectPlan(context.Background(), planID); err != nil {
			return ErrMsg{Err: err}
		}
		if err := workItemSvc.RejectPlan(context.Background(), workItemID); err != nil {
			return ErrMsg{Err: err}
		}
		return ActionDoneMsg{Message: "Plan rejected"}
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
func StartPlanningCmd(svc *orchestrator.PlanningService, workItemID string) tea.Cmd {
	return func() tea.Msg {
		if _, err := svc.Plan(context.Background(), workItemID); err != nil {
			return ErrMsg{Err: err}
		}
		return ActionDoneMsg{Message: "Planning complete"}
	}
}

// PlanWithFeedbackCmd rejects the old plan and starts a revision session.
func PlanWithFeedbackCmd(svc *orchestrator.PlanningService, workItemID, planID, feedback string) tea.Cmd {
	return func() tea.Msg {
		if _, err := svc.PlanWithFeedback(context.Background(), workItemID, planID, feedback); err != nil {
			return ErrMsg{Err: err}
		}
		return ActionDoneMsg{Message: "Plan revised"}
	}
}

// RunImplementationCmd executes the implementation pipeline for an approved plan.
func RunImplementationCmd(svc *orchestrator.ImplementationService, planID string) tea.Cmd {
	return func() tea.Msg {
		if _, err := svc.Implement(context.Background(), planID); err != nil {
			return ErrMsg{Err: err}
		}
		return ActionDoneMsg{Message: "Implementation complete"}
	}
}
