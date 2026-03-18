package service

import (
	"context"
	"slices"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

// TaskService provides business logic for repo-scoped tasks.
type TaskService struct {
	repo repository.TaskRepository
}

// NewTaskService creates a new TaskService.
func NewTaskService(repo repository.TaskRepository) *TaskService {
	return &TaskService{repo: repo}
}

// Task state transitions.
var validTaskTransitions = map[domain.TaskStatus][]domain.TaskStatus{
	domain.AgentSessionPending: {domain.AgentSessionRunning, domain.AgentSessionFailed},
	domain.AgentSessionRunning: {
		domain.AgentSessionWaitingForAnswer,
		domain.AgentSessionCompleted,
		domain.AgentSessionInterrupted,
		domain.AgentSessionFailed,
	},
	domain.AgentSessionWaitingForAnswer: {domain.AgentSessionRunning, domain.AgentSessionFailed},
	domain.AgentSessionCompleted:        {},
	domain.AgentSessionInterrupted:      {domain.AgentSessionRunning, domain.AgentSessionFailed},
	domain.AgentSessionFailed:           {},
}

func canTransitionTask(from, to domain.TaskStatus) bool {
	allowed, exists := validTaskTransitions[from]
	if !exists {
		return false
	}
	return slices.Contains(allowed, to)
}

// Get retrieves a task by ID.
func (s *TaskService) Get(ctx context.Context, id string) (domain.Task, error) {
	task, err := s.repo.Get(ctx, id)
	if err != nil {
		return domain.Task{}, newNotFoundError("task", id)
	}

	return task, nil
}

// ListByWorkItemID retrieves all child agent sessions for a work item.
func (s *TaskService) ListByWorkItemID(ctx context.Context, workItemID string) ([]domain.Task, error) {
	return s.repo.ListByWorkItemID(ctx, workItemID)
}

// ListBySubPlanID retrieves all child agent sessions for a sub-plan.
func (s *TaskService) ListBySubPlanID(ctx context.Context, subPlanID string) ([]domain.Task, error) {
	return s.repo.ListBySubPlanID(ctx, subPlanID)
}

// ListByWorkspaceID retrieves all child agent sessions for a workspace.
func (s *TaskService) ListByWorkspaceID(ctx context.Context, workspaceID string) ([]domain.Task, error) {
	return s.repo.ListByWorkspaceID(ctx, workspaceID)
}

// SearchHistory retrieves searchable session-history entries for the requested scope.
func (s *TaskService) SearchHistory(ctx context.Context, filter domain.SessionHistoryFilter) ([]domain.SessionHistoryEntry, error) {
	return s.repo.SearchHistory(ctx, filter)
}

// Create creates a new child agent session in pending status.
func (s *TaskService) Create(ctx context.Context, task domain.Task) error {
	if task.WorkItemID == "" {
		return newInvalidInputError("work item is required", "work_item_id")
	}
	if task.HarnessName == "" {
		return newInvalidInputError("harness name is required", "harness_name")
	}
	if task.Phase == "" {
		return newInvalidInputError("phase is required", "phase")
	}
	switch task.Phase {
	case domain.TaskPhasePlanning:
		// Planning sessions run at the workspace/work-item level and may omit repo-specific fields.
	case domain.TaskPhaseImplementation, domain.TaskPhaseReview:
		if task.SubPlanID == "" {
			return newInvalidInputError("sub-plan is required for this phase", "sub_plan_id")
		}
	default:
		return newInvalidInputError("unknown task phase", "phase")
	}
	if task.Status == "" {
		task.Status = domain.AgentSessionPending
	}
	if task.Status != domain.AgentSessionPending {
		return newInvalidInputError("initial status must be pending", "status")
	}
	now := time.Now()
	task.CreatedAt = now
	task.UpdatedAt = now

	return s.repo.Create(ctx, task)
}

// Transition transitions a task to a new status.
func (s *TaskService) Transition(ctx context.Context, id string, to domain.TaskStatus) error {
	task, err := s.repo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("task", id)
	}

	if !canTransitionTask(task.Status, to) {
		return newInvalidTransitionError(
			sessionStatusName(task.Status),
			sessionStatusName(to),
			"task",
		)
	}

	task.Status = to
	task.UpdatedAt = time.Now()

	return s.repo.Update(ctx, task)
}

// Start transitions a task from pending to running.
func (s *TaskService) Start(ctx context.Context, id string) error {
	task, err := s.repo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("task", id)
	}

	if !canTransitionTask(task.Status, domain.AgentSessionRunning) {
		return newInvalidTransitionError(
			sessionStatusName(task.Status),
			sessionStatusName(domain.AgentSessionRunning),
			"task",
		)
	}

	now := time.Now()
	task.Status = domain.AgentSessionRunning
	task.StartedAt = &now
	task.UpdatedAt = now

	return s.repo.Update(ctx, task)
}

// WaitForAnswer transitions a task from running to waiting_for_answer.
func (s *TaskService) WaitForAnswer(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.AgentSessionWaitingForAnswer)
}

// ResumeFromAnswer transitions a task from waiting_for_answer to running.
func (s *TaskService) ResumeFromAnswer(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.AgentSessionRunning)
}

// Complete transitions a task from running to completed.
func (s *TaskService) Complete(ctx context.Context, id string) error {
	task, err := s.repo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("task", id)
	}

	if !canTransitionTask(task.Status, domain.AgentSessionCompleted) {
		return newInvalidTransitionError(
			sessionStatusName(task.Status),
			sessionStatusName(domain.AgentSessionCompleted),
			"task",
		)
	}

	now := time.Now()
	task.Status = domain.AgentSessionCompleted
	task.CompletedAt = &now
	task.UpdatedAt = now

	return s.repo.Update(ctx, task)
}

// Interrupt transitions a task from running to interrupted.
func (s *TaskService) Interrupt(ctx context.Context, id string) error {
	task, err := s.repo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("task", id)
	}

	if !canTransitionTask(task.Status, domain.AgentSessionInterrupted) {
		return newInvalidTransitionError(
			sessionStatusName(task.Status),
			sessionStatusName(domain.AgentSessionInterrupted),
			"task",
		)
	}

	now := time.Now()
	task.Status = domain.AgentSessionInterrupted
	task.ShutdownAt = &now
	task.UpdatedAt = now

	return s.repo.Update(ctx, task)
}

// Resume transitions a task from interrupted to running.
func (s *TaskService) Resume(ctx context.Context, id string) error {
	return s.Transition(ctx, id, domain.AgentSessionRunning)
}

// Fail transitions a task to failed.
func (s *TaskService) Fail(ctx context.Context, id string, exitCode *int) error {
	task, err := s.repo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("task", id)
	}

	if !canTransitionTask(task.Status, domain.AgentSessionFailed) {
		return newInvalidTransitionError(
			sessionStatusName(task.Status),
			sessionStatusName(domain.AgentSessionFailed),
			"task",
		)
	}

	now := time.Now()
	task.Status = domain.AgentSessionFailed
	task.CompletedAt = &now
	task.ExitCode = exitCode
	task.UpdatedAt = now

	return s.repo.Update(ctx, task)
}

// UpdateOwnerInstance updates the owner instance ID for a task.
func (s *TaskService) UpdateOwnerInstance(ctx context.Context, id string, instanceID string) error {
	task, err := s.repo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("task", id)
	}

	task.OwnerInstanceID = &instanceID
	task.UpdatedAt = time.Now()

	return s.repo.Update(ctx, task)
}

// UpdatePID updates the PID for a task.
func (s *TaskService) UpdatePID(ctx context.Context, id string, pid int) error {
	task, err := s.repo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("task", id)
	}

	task.PID = &pid
	task.UpdatedAt = time.Now()

	return s.repo.Update(ctx, task)
}

// Delete deletes a task.
func (s *TaskService) Delete(ctx context.Context, id string) error {
	_, err := s.repo.Get(ctx, id)
	if err != nil {
		return newNotFoundError("task", id)
	}

	return s.repo.Delete(ctx, id)
}

// FindInterruptedByWorkspace finds all interrupted tasks for a workspace.
func (s *TaskService) FindInterruptedByWorkspace(ctx context.Context, workspaceID string) ([]domain.Task, error) {
	tasks, err := s.repo.ListByWorkspaceID(ctx, workspaceID)
	if err != nil {
		return nil, err
	}

	var interrupted []domain.Task
	for _, task := range tasks {
		if task.Status == domain.AgentSessionInterrupted {
			interrupted = append(interrupted, task)
		}
	}

	return interrupted, nil
}

// FindRunningByOwner finds all running tasks owned by an instance.
func (s *TaskService) FindRunningByOwner(ctx context.Context, instanceID string) ([]domain.Task, error) {
	tasks, err := s.repo.ListByOwnerInstanceID(ctx, instanceID)
	if err != nil {
		return nil, err
	}

	var running []domain.Task
	for _, task := range tasks {
		if task.Status == domain.AgentSessionRunning {
			running = append(running, task)
		}
	}

	return running, nil
}
