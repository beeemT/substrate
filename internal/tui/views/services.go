package views

import (
	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/gitwork"
	"github.com/beeemT/substrate/internal/orchestrator"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
)

// Services aggregates all dependencies needed by the TUI.
type Services struct {
	WorkItem  *service.WorkItemService
	Plan      *service.PlanService
	SubPlan   repository.SubPlanRepository
	Session   *service.SessionService
	Question  *service.QuestionService
	Instance  *service.InstanceService
	Workspace *service.WorkspaceService
	Review    *service.ReviewService
	// Orchestration pipelines. Non-nil only when the oh-my-pi harness is configured.
	Planning       *orchestrator.PlanningService
	Implementation *orchestrator.ImplementationService
	ReviewPipeline *orchestrator.ReviewPipeline
	Resumption     *orchestrator.Resumption
	Foreman        *orchestrator.Foreman
	Cfg            *config.Config
	Adapters       []adapter.WorkItemAdapter
	GitClient      *gitwork.Client
	Bus            *event.Bus
	// Instance identity
	InstanceID    string
	WorkspaceID   string
	WorkspaceDir  string
	WorkspaceName string
}
