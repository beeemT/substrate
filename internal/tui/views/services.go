package views

import (
	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/app"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/gitwork"
	"github.com/beeemT/substrate/internal/orchestrator"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
)

// Services aggregates all dependencies needed by the TUI.
type Services struct {
	Session   *service.SessionService
	Plan      *service.PlanService
	TaskPlan  repository.TaskPlanRepository
	Task      *service.TaskService
	Question  *service.QuestionService
	Instance  *service.InstanceService
	Workspace *service.WorkspaceService
	Review    *service.ReviewService
	Events    repository.EventRepository
	// Orchestration pipelines backed by the configured agent harnesses.
	Planning       *orchestrator.PlanningService
	Implementation *orchestrator.ImplementationService
	ReviewPipeline *orchestrator.ReviewPipeline
	Resumption     *orchestrator.Resumption
	Foreman        *orchestrator.Foreman
	Cfg            *config.Config
	Adapters       []adapter.WorkItemAdapter
	Harnesses      app.AgentHarnesses
	Settings       *SettingsService
	SettingsData   SettingsSnapshot
	GitClient      *gitwork.Client
	Bus            *event.Bus
	// Instance identity
	InstanceID    string
	WorkspaceID   string
	WorkspaceDir  string
	WorkspaceName string
}

func (s Services) ForemanHarness() adapter.AgentHarness {
	if s.Harnesses.Foreman != nil {
		return s.Harnesses.Foreman
	}
	return nil
}
