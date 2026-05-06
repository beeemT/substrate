package views

import (
	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/app"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/gitwork"
	"github.com/beeemT/substrate/internal/orchestrator"
	"github.com/beeemT/substrate/internal/service"
)

// testProvider is a ServiceProvider implementation for tests.
type testProvider struct {
	svcs Services
}

func (tp *testProvider) GetServices() *Services               { return &tp.svcs }
func (tp *testProvider) Session() *service.SessionService     { return tp.svcs.Session }
func (tp *testProvider) Plan() *service.PlanService           { return tp.svcs.Plan }
func (tp *testProvider) Task() *service.TaskService           { return tp.svcs.Task }
func (tp *testProvider) Question() *service.QuestionService   { return tp.svcs.Question }
func (tp *testProvider) Instance() *service.InstanceService   { return tp.svcs.Instance }
func (tp *testProvider) Workspace() *service.WorkspaceService { return tp.svcs.Workspace }
func (tp *testProvider) Review() *service.ReviewService       { return tp.svcs.Review }
func (tp *testProvider) Events() *service.EventService        { return tp.svcs.Events }
func (tp *testProvider) GithubPRs() *service.GithubPRService  { return tp.svcs.GithubPRs }
func (tp *testProvider) GitlabMRs() *service.GitlabMRService  { return tp.svcs.GitlabMRs }
func (tp *testProvider) SessionArtifacts() *service.SessionReviewArtifactService {
	return tp.svcs.SessionArtifacts
}

func (tp *testProvider) GithubPRReviews() *service.GithubPRReviewService {
	return tp.svcs.GithubPRReviews
}

func (tp *testProvider) GitlabMRReviews() *service.GitlabMRReviewService {
	return tp.svcs.GitlabMRReviews
}
func (tp *testProvider) GithubPRChecks() *service.GithubPRCheckService { return tp.svcs.GithubPRChecks }
func (tp *testProvider) GitlabMRChecks() *service.GitlabMRCheckService { return tp.svcs.GitlabMRChecks }
func (tp *testProvider) NewSessionFilters() *service.SessionFilterService {
	return tp.svcs.NewSessionFilters
}

func (tp *testProvider) NewSessionFilterLocks() *service.SessionFilterLockService {
	return tp.svcs.NewSessionFilterLocks
}
func (tp *testProvider) Settings() *SettingsService              { return tp.svcs.Settings }
func (tp *testProvider) Planning() *orchestrator.PlanningService { return tp.svcs.Planning }
func (tp *testProvider) Implementation() *orchestrator.ImplementationService {
	return tp.svcs.Implementation
}
func (tp *testProvider) ReviewPipeline() *orchestrator.ReviewPipeline { return tp.svcs.ReviewPipeline }
func (tp *testProvider) Resumption() *orchestrator.Resumption         { return tp.svcs.Resumption }
func (tp *testProvider) Foreman() *orchestrator.Foreman               { return tp.svcs.Foreman }
func (tp *testProvider) SessionRegistry() *orchestrator.SessionRegistry {
	return tp.svcs.SessionRegistry
}
func (tp *testProvider) Bus() *event.Bus                     { return tp.svcs.Bus }
func (tp *testProvider) GitClient() *gitwork.Client          { return tp.svcs.GitClient }
func (tp *testProvider) Adapters() []adapter.WorkItemAdapter { return tp.svcs.Adapters }
func (tp *testProvider) RepoSources() []adapter.RepoSource   { return tp.svcs.RepoSources }
func (tp *testProvider) Harnesses() app.AgentHarnesses       { return tp.svcs.Harnesses }
func (tp *testProvider) ReviewComments() *adapter.ReviewCommentDispatcher {
	return tp.svcs.ReviewComments
}
func (tp *testProvider) StartupWarnings() []string { return tp.svcs.StartupWarnings }

// newTestApp creates an App for testing with a testProvider wrapping svcs.
func newTestApp(svcs Services) *App {
	return NewApp(&testProvider{svcs: svcs}, RuntimeContext{})
}

// servicesToProvider wraps a Services struct in a testProvider.
func servicesToProvider(svcs Services) ServiceProvider {
	return &testProvider{svcs: svcs}
}
