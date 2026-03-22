package views

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	githubadapter "github.com/beeemT/substrate/internal/adapter/github"
	gitlabadapter "github.com/beeemT/substrate/internal/adapter/gitlab"
	linearadapter "github.com/beeemT/substrate/internal/adapter/linear"
	sentryadapter "github.com/beeemT/substrate/internal/adapter/sentry"
	"github.com/beeemT/substrate/internal/app"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/gitwork"
	"github.com/beeemT/substrate/internal/orchestrator"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
	"gopkg.in/yaml.v3"
)

type SettingsFieldType int

const (
	SettingsFieldString SettingsFieldType = iota
	SettingsFieldBool
	SettingsFieldEnum
	SettingsFieldPath
	SettingsFieldSecret
	SettingsFieldStringList
	SettingsFieldKeyValue
)

const (
	statusWarning              = "warning"
	statusEmpty                = "empty"
	hintInheritsHarnessDefault = "inherits harness.default"
)

type SettingsField struct {
	Section      string
	Key          string
	Label        string
	Description  string
	DefaultValue string
	Type         SettingsFieldType
	Value        string
	Options      []string
	Sensitive    bool
	Required     bool
	Dirty        bool
	Error        string
	Status       string
}

type SettingsSection struct {
	ID          string
	Title       string
	Description string
	Fields      []SettingsField
	Status      string
	Error       string
}

type ProviderStatus struct {
	Title       string
	Configured  bool
	Connected   bool
	AuthSource  string
	Description string
	LastError   string
}

type SettingsSnapshot struct {
	Sections       []SettingsSection
	Providers      map[string]ProviderStatus
	RawYAML        string
	HarnessWarning string
}

type SettingsApplyResult struct {
	Services viewsServicesReload
	Message  string
}

type SettingsLoginResult struct {
	Snapshot SettingsSnapshot
	Message  string
	Dirty    bool
}

func settingsSnapshotFromConfig(cfg *config.Config) SettingsSnapshot {
	diagnostics := app.DiagnoseHarnesses(cfg, "")

	return SettingsSnapshot{
		Sections:       buildSettingsSections(cfg),
		Providers:      buildProviderStatuses(cfg),
		HarnessWarning: diagnostics.WarningSummary(),
	}
}

type SettingsService struct {
	workItemRepo        repository.SessionRepository
	planRepo            repository.PlanRepository
	subPlanRepo         repository.TaskPlanRepository
	workspaceRepo       repository.WorkspaceRepository
	sessionRepo         repository.TaskRepository
	questionRepo        repository.QuestionRepository
	instanceRepo        repository.InstanceRepository
	reviewRepo          repository.ReviewRepository
	eventRepo           repository.EventRepository
	ghPRRepo            repository.GithubPullRequestRepository
	glMRRepo            repository.GitlabMergeRequestRepository
	sessionArtifactRepo repository.SessionReviewArtifactRepository
	secretStore         config.SecretStore
}

type viewsServicesReload struct {
	Services     Services
	ConfigPath   string
	SessionsDir  string
	SettingsData SettingsSnapshot
}

func NewSettingsService(
	workItemRepo repository.SessionRepository,
	planRepo repository.PlanRepository,
	subPlanRepo repository.TaskPlanRepository,
	workspaceRepo repository.WorkspaceRepository,
	sessionRepo repository.TaskRepository,
	questionRepo repository.QuestionRepository,
	instanceRepo repository.InstanceRepository,
	reviewRepo repository.ReviewRepository,
	eventRepo repository.EventRepository,
	ghPRRepo repository.GithubPullRequestRepository,
	glMRRepo repository.GitlabMergeRequestRepository,
	sessionArtifactRepo repository.SessionReviewArtifactRepository,
	secretStore config.SecretStore,
) *SettingsService {
	return &SettingsService{
		workItemRepo:        workItemRepo,
		planRepo:            planRepo,
		subPlanRepo:         subPlanRepo,
		workspaceRepo:       workspaceRepo,
		sessionRepo:         sessionRepo,
		questionRepo:        questionRepo,
		instanceRepo:        instanceRepo,
		reviewRepo:          reviewRepo,
		eventRepo:           eventRepo,
		ghPRRepo:            ghPRRepo,
		glMRRepo:            glMRRepo,
		sessionArtifactRepo: sessionArtifactRepo,
		secretStore:         secretStore,
	}
}

func (s *SettingsService) Snapshot(cfg *config.Config) (SettingsSnapshot, error) {
	cfgPath, err := config.ConfigPath()
	if err != nil {
		return SettingsSnapshot{}, err
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil && !os.IsNotExist(err) {
		return SettingsSnapshot{}, err
	}
	if err := config.LoadSecrets(cfg, s.secretStore); err != nil {
		return SettingsSnapshot{}, err
	}
	diagnostics := app.DiagnoseHarnesses(cfg, "")

	return SettingsSnapshot{
		Sections:       buildSettingsSections(cfg),
		Providers:      buildProviderStatuses(cfg),
		RawYAML:        string(raw),
		HarnessWarning: diagnostics.WarningSummary(),
	}, nil
}

func (s *SettingsService) SaveRaw(raw string) error {
	cfgPath, err := config.ConfigPath()
	if err != nil {
		return err
	}

	return os.WriteFile(cfgPath, []byte(raw), 0o600)
}

func (s *SettingsService) loadConfigFromRaw(raw string) (*config.Config, error) {
	tmp, err := os.CreateTemp("", "substrate-settings-*.yaml")
	if err != nil {
		return nil, fmt.Errorf("create temp YAML config: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.WriteString(raw); err != nil {
		_ = tmp.Close()

		return nil, fmt.Errorf("write temp YAML config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("close temp YAML config: %w", err)
	}
	cfg, err := config.Load(tmpPath)
	if err != nil {
		return nil, err
	}
	if err := config.LoadSecrets(cfg, s.secretStore); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (s *SettingsService) Serialize(sections []SettingsSection) (string, *config.Config, error) {
	cfg, err := configFromSections(sections)
	if err != nil {
		return "", nil, err
	}
	if err := validateSettingsConfig(cfg); err != nil {
		return "", nil, err
	}
	if err := config.SaveSecrets(cfg, s.secretStore); err != nil {
		return "", nil, err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return "", nil, fmt.Errorf("encode YAML config: %w", err)
	}

	return string(data), cfg, nil
}

func (s *SettingsService) Apply(ctx context.Context, raw string, current Services) (SettingsApplyResult, error) {
	cfg, err := s.loadConfigFromRaw(raw)
	if err != nil {
		return SettingsApplyResult{}, err
	}
	reloaded, err := s.rebuildServices(ctx, cfg, current)
	if err != nil {
		return SettingsApplyResult{}, err
	}
	if err := s.SaveRaw(raw); err != nil {
		return SettingsApplyResult{}, err
	}
	if current.Foreman != nil {
		_ = current.Foreman.Stop(ctx)
	}
	reloaded.SettingsData.RawYAML = raw

	return SettingsApplyResult{Services: reloaded, Message: "Settings applied"}, nil
}

func (s *SettingsService) TestProvider(ctx context.Context, provider string, sections []SettingsSection) (ProviderStatus, error) {
	cfg, err := configFromSections(sections)
	if err != nil {
		return ProviderStatus{}, err
	}
	if err := validateSettingsConfig(cfg); err != nil {
		return ProviderStatus{}, err
	}
	switch provider {
	case "linear":
		status := buildProviderStatuses(cfg)[provider]
		if strings.TrimSpace(cfg.Adapters.Linear.APIKey) == "" {
			return status, errors.New("linear api key is required")
		}
		client := linearadapter.New(cfg.Adapters.Linear)
		_, err := client.ListSelectable(ctx, adapter.ListOpts{Scope: domain.ScopeIssues, Limit: 1})
		if err != nil {
			status.Connected = false
			status.LastError = err.Error()

			return status, err
		}
		status.Connected = true
		status.LastError = ""

		return status, nil
	case providerGitlab:
		status := buildProviderStatuses(cfg)[provider]
		client, err := gitlabadapter.New(ctx, cfg.Adapters.GitLab)
		if err != nil {
			status.Connected = false
			status.LastError = err.Error()

			return status, err
		}
		_, err = client.ListSelectable(ctx, adapter.ListOpts{Scope: domain.ScopeIssues, Limit: 1})
		if err != nil {
			status.Connected = false
			status.LastError = err.Error()

			return status, err
		}
		status.Connected = true
		status.LastError = ""

		return status, nil
	case providerSentry:
		status := buildProviderStatuses(cfg)[provider]
		client, err := sentryadapter.New(ctx, cfg.Adapters.Sentry)
		if err != nil {
			status.Connected = false
			status.LastError = err.Error()

			return status, err
		}
		_, err = client.ListSelectable(ctx, adapter.ListOpts{Scope: domain.ScopeIssues, Limit: 1})
		if err != nil {
			status.Connected = false
			status.LastError = err.Error()

			return status, err
		}
		status.Connected = true
		status.LastError = ""

		return status, nil
	case providerGithub:
		status := buildProviderStatuses(cfg)[provider]
		client, err := githubadapter.New(ctx, cfg.Adapters.GitHub)
		if err != nil {
			status.Connected = false
			status.LastError = err.Error()

			return status, err
		}
		_, err = client.ListSelectable(ctx, adapter.ListOpts{Scope: domain.ScopeIssues, Limit: 1})
		if err != nil {
			status.Connected = false
			status.LastError = err.Error()

			return status, err
		}
		status.Connected = true
		status.LastError = ""

		return status, nil
	default:
		return ProviderStatus{}, fmt.Errorf("unknown provider %q", provider)
	}
}

func (s *SettingsService) LoginProvider(ctx context.Context, provider, harness string, sections []SettingsSection, svcs Services) (SettingsLoginResult, error) {
	cfg, err := configFromSections(sections)
	if err != nil {
		return SettingsLoginResult{}, err
	}
	req := adapter.HarnessActionRequest{
		Action:      "login_provider",
		Provider:    provider,
		HarnessName: harness,
		Inputs:      providerLoginInputs(cfg, provider),
	}
	runner := harnessRunnerForProvider(harness, svcs)
	if runner == nil {
		return SettingsLoginResult{}, fmt.Errorf("harness %q does not support login actions", harness)
	}
	result, err := config.RunHarnessAction(ctx, runner, req)
	if err != nil {
		return SettingsLoginResult{}, err
	}
	if !result.Success {
		return SettingsLoginResult{}, fmt.Errorf("%s", result.Message)
	}
	dirty := false
	message := strings.TrimSpace(result.Message)
	switch provider {
	case providerGithub:
		token := strings.TrimSpace(result.Credentials["token"])
		if token == "" {
			return SettingsLoginResult{}, errors.New("github login did not return a token")
		}
		cfg.Adapters.GitHub.Token = token
		cfg.Adapters.GitHub.TokenRef = secretRef("github.token")
		dirty = true
		if message == "" {
			message = "github login complete"
		}
	case providerSentry:
		if message == "" {
			message = "sentry login complete"
		}
	default:
		return SettingsLoginResult{}, fmt.Errorf("login not implemented for provider %q", provider)
	}

	return SettingsLoginResult{Snapshot: settingsSnapshotFromConfig(cfg), Message: message, Dirty: dirty}, nil
}

func providerLoginInputs(cfg *config.Config, provider string) map[string]string {
	if cfg == nil {
		return nil
	}
	switch provider {
	case providerSentry:
		if rawBaseURL := strings.TrimSpace(cfg.Adapters.Sentry.BaseURL); rawBaseURL != "" {
			return map[string]string{"base_url": strings.TrimSpace(config.SentryRootURL(rawBaseURL))}
		}
		resolved := config.ResolveSentryContext(cfg.Adapters.Sentry)
		rootURL := strings.TrimSpace(config.SentryRootURL(resolved.BaseURL))
		if rootURL == "" {
			return nil
		}

		return map[string]string{"base_url": rootURL}
	default:
		return nil
	}
}

func sentryBaseURLFieldValue(cfg config.SentryConfig) string {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if !cfg.BaseURLExplicit && config.NormalizeSentryBaseURL(baseURL) == config.DefaultSentryBaseURL {
		return ""
	}

	return baseURL
}

func (s *SettingsService) rebuildServices(ctx context.Context, cfg *config.Config, current Services) (viewsServicesReload, error) {
	workItemSvc := service.NewSessionService(s.workItemRepo)
	planSvc := service.NewPlanService(s.planRepo, s.subPlanRepo)
	workspaceSvc := service.NewWorkspaceService(s.workspaceRepo)
	sessionSvc := service.NewTaskService(s.sessionRepo)
	questionSvc := service.NewQuestionService(s.questionRepo)
	instanceSvc := service.NewInstanceService(s.instanceRepo)
	reviewSvc := service.NewReviewService(s.reviewRepo)
	bus := event.NewBus(event.BusConfig{EventRepo: s.eventRepo})
	gitClient := current.GitClient
	if gitClient == nil {
		gitClient = gitwork.NewClient("")
	}
	var adapters []adapter.WorkItemAdapter
	if current.WorkspaceID != "" {
		adapters = app.BuildWorkItemAdapters(cfg, current.WorkspaceID, s.workItemRepo)
	}
	repoLifecycleAdapters := app.BuildRepoLifecycleAdapters(ctx, cfg, current.WorkspaceDir, adapter.ReviewArtifactRepos{
		Events:           s.eventRepo,
		GithubPRs:        s.ghPRRepo,
		GitlabMRs:        s.glMRRepo,
		SessionArtifacts: s.sessionArtifactRepo,
	})
	for _, workItemAdapter := range adapters {
		sub, subErr := bus.Subscribe("work-item-adapter:" + workItemAdapter.Name())
		if subErr != nil {
			return viewsServicesReload{}, fmt.Errorf("subscribe work item adapter %s: %w", workItemAdapter.Name(), subErr)
		}
		go func(a adapter.WorkItemAdapter, events <-chan domain.SystemEvent) {
			for evt := range events {
				_ = a.OnEvent(context.Background(), evt)
			}
		}(workItemAdapter, sub.C)
	}
	for _, lifecycleAdapter := range repoLifecycleAdapters {
		sub, subErr := bus.Subscribe("repo-lifecycle-adapter:"+lifecycleAdapter.Name(), string(domain.EventWorktreeCreated), string(domain.EventWorkItemCompleted))
		if subErr != nil {
			return viewsServicesReload{}, fmt.Errorf("subscribe repo lifecycle adapter %s: %w", lifecycleAdapter.Name(), subErr)
		}
		go func(a adapter.RepoLifecycleAdapter, events <-chan domain.SystemEvent) {
			for evt := range events {
				_ = a.OnEvent(context.Background(), evt)
			}
		}(lifecycleAdapter, sub.C)
	}

	// Start PR/MR state refresh for lifecycle adapters.
	for _, la := range repoLifecycleAdapters {
		type prRefresher interface {
			StartPRRefresh(ctx context.Context, workspaceID string)
		}
		type mrRefresher interface {
			StartMRRefresh(ctx context.Context, workspaceID string)
		}
		if r, ok := la.(prRefresher); ok && current.WorkspaceID != "" {
			r.StartPRRefresh(ctx, current.WorkspaceID)
		}
		if r, ok := la.(mrRefresher); ok && current.WorkspaceID != "" {
			r.StartMRRefresh(ctx, current.WorkspaceID)
		}
	}
	discoverer := orchestrator.NewDiscoverer(gitClient, cfg)
	harnesses, err := app.BuildAgentHarnesses(cfg, current.WorkspaceDir)
	if err != nil {
		return viewsServicesReload{}, fmt.Errorf("building agent harnesses: %w", err)
	}
	planningCfg := orchestrator.PlanningConfigFromConfig(cfg)
	registry := orchestrator.NewSessionRegistry()
	var planningSvc *orchestrator.PlanningService
	if harnesses.Planning != nil {
		planningSvc, err = orchestrator.NewPlanningService(planningCfg, discoverer, gitClient, harnesses.Planning, planSvc, workItemSvc, sessionSvc, s.planRepo, s.subPlanRepo, s.eventRepo, workspaceSvc, registry, cfg)
		if err != nil {
			return viewsServicesReload{}, fmt.Errorf("build planning service: %w", err)
		}
	}
	var reviewPipeline *orchestrator.ReviewPipeline
	if harnesses.Review != nil {
		reviewPipeline = orchestrator.NewReviewPipeline(cfg, harnesses.Review, reviewSvc, sessionSvc, planSvc, workItemSvc, s.sessionRepo, s.planRepo, bus, registry)
	}
	var implSvc *orchestrator.ImplementationService
	if harnesses.Implementation != nil {
		implSvc = orchestrator.NewImplementationService(cfg, harnesses.Implementation, gitClient, bus, planSvc, workItemSvc, sessionSvc, s.subPlanRepo, s.sessionRepo, workspaceSvc, registry, reviewPipeline)
	}
	var resumption *orchestrator.Resumption
	if harnesses.Resume != nil {
		resumption = orchestrator.NewResumption(harnesses.Resume, sessionSvc, planSvc, s.sessionRepo, bus, registry)
	}
	var foreman *orchestrator.Foreman
	if harnesses.Foreman != nil {
		foreman = orchestrator.NewForeman(cfg, harnesses.Foreman, planSvc, questionSvc, sessionSvc, s.planRepo, bus)
	}
	cfgPath, err := config.ConfigPath()
	if err != nil {
		return viewsServicesReload{}, err
	}
	sessionsDir, err := config.SessionsDir()
	if err != nil {
		return viewsServicesReload{}, err
	}
	snapshot, err := s.Snapshot(cfg)
	if err != nil {
		return viewsServicesReload{}, err
	}

	return viewsServicesReload{
		ConfigPath:   cfgPath,
		SessionsDir:  sessionsDir,
		SettingsData: snapshot,
		Services: Services{
			Session:          workItemSvc,
			Plan:             planSvc,
			TaskPlan:         s.subPlanRepo,
			Task:             sessionSvc,
			Question:         questionSvc,
			Instance:         instanceSvc,
			Workspace:        workspaceSvc,
			Review:           reviewSvc,
			Events:           s.eventRepo,
			GithubPRs:        s.ghPRRepo,
			GitlabMRs:        s.glMRRepo,
			SessionArtifacts: s.sessionArtifactRepo,
			Planning:         planningSvc,
			Implementation:   implSvc,
			ReviewPipeline:   reviewPipeline,
			Resumption:       resumption,
			Foreman:          foreman,
			SessionRegistry:  registry,
			Cfg:              cfg,
			Adapters:         adapters,
			Harnesses:        harnesses,
			GitClient:        gitClient,
			Bus:              bus,
			InstanceID:       current.InstanceID,
			WorkspaceID:      current.WorkspaceID,
			WorkspaceDir:     current.WorkspaceDir,
			WorkspaceName:    current.WorkspaceName,
		},
	}, nil
}

func harnessRunnerForProvider(harness string, svcs Services) adapter.HarnessActionRunner {
	var agentHarness adapter.AgentHarness
	switch strings.TrimSpace(harness) {
	case string(config.HarnessOhMyPi), string(config.HarnessClaudeCode), string(config.HarnessCodex):
		agentHarness = svcs.ForemanHarness()
	default:
		agentHarness = svcs.ForemanHarness()
	}
	runner, _ := agentHarness.(adapter.HarnessActionRunner)

	return runner
}

func buildSettingsSections(cfg *config.Config) []SettingsSection {
	if cfg == nil {
		return nil
	}
	sections := []SettingsSection{
		{
			ID:          "commit",
			Title:       "Commit",
			Description: "Agent commit behavior",
			Fields: []SettingsField{
				{Section: "commit", Key: "strategy", Label: "Strategy", Type: SettingsFieldEnum, Value: string(cfg.Commit.Strategy), Options: []string{"granular", "semi-regular", "single"}, Required: true},
				{Section: "commit", Key: "message_format", Label: "Message Format", Type: SettingsFieldEnum, Value: string(cfg.Commit.MessageFormat), Options: []string{"ai-generated", "conventional", "custom"}, Required: true},
				{Section: "commit", Key: "message_template", Label: "Message Template", Type: SettingsFieldString, Value: cfg.Commit.MessageTemplate},
			},
		},
		{
			ID:          "plan",
			Title:       "Planning",
			Description: "Planning pipeline settings",
			Fields:      []SettingsField{{Section: "plan", Key: "max_parse_retries", Label: "Max Parse Retries", Type: SettingsFieldString, Value: intPtrStr(cfg.Plan.MaxParseRetries)}},
		},
		{
			ID:          "review",
			Title:       "Review",
			Description: "Review pipeline settings",
			Fields: []SettingsField{
				{Section: "review", Key: "pass_threshold", Label: "Pass Threshold", Type: SettingsFieldEnum, Value: string(cfg.Review.PassThreshold), Options: []string{"nit_only", "minor_ok", "no_critiques"}, Required: true},
				{Section: "review", Key: "max_cycles", Label: "Max Cycles", Type: SettingsFieldString, Value: intPtrStr(cfg.Review.MaxCycles)},
			},
		},
		{
			ID:          "foreman",
			Title:       "Foreman",
			Description: "Foreman session settings",
			Fields:      []SettingsField{{Section: "foreman", Key: "question_timeout", Label: "Question Timeout", Type: SettingsFieldString, Value: cfg.Foreman.QuestionTimeout}},
		},
		{
			ID:          settingHarness,
			Title:       "Harness Routing",
			Description: "Select which harness runs each phase",
			Fields: []SettingsField{
				{Section: settingHarness, Key: "default", Label: "Default Harness", Type: SettingsFieldEnum, Value: string(cfg.Harness.Default), Options: []string{"ohmypi", "claude-code", "codex"}, Required: true},
				{Section: "harness.phase", Key: "planning", Label: "Planning Harness", Type: SettingsFieldEnum, Value: string(cfg.Harness.Phase.Planning), Options: []string{"ohmypi", "claude-code", "codex"}},
				{Section: "harness.phase", Key: "implementation", Label: "Implementation Harness", Type: SettingsFieldEnum, Value: string(cfg.Harness.Phase.Implementation), Options: []string{"ohmypi", "claude-code", "codex"}},
				{Section: "harness.phase", Key: "review", Label: "Review Harness", Type: SettingsFieldEnum, Value: string(cfg.Harness.Phase.Review), Options: []string{"ohmypi", "claude-code", "codex"}},
				{Section: "harness.phase", Key: "foreman", Label: "Foreman Harness", Type: SettingsFieldEnum, Value: string(cfg.Harness.Phase.Foreman), Options: []string{"ohmypi", "claude-code", "codex"}},
			},
		},
		{
			ID:          "harness.ohmypi",
			Title:       "Harness · Oh My Pi",
			Description: "Bridge-based harness configuration",
			Fields: []SettingsField{
				{Section: "adapters.ohmypi", Key: "bun_path", Label: "Bun Path", Type: SettingsFieldPath, Value: cfg.Adapters.OhMyPi.BunPath},
				{Section: "adapters.ohmypi", Key: "bridge_path", Label: "Bridge Path", Type: SettingsFieldPath, Value: cfg.Adapters.OhMyPi.BridgePath},
				{Section: "adapters.ohmypi", Key: "thinking_level", Label: "Thinking Level", Type: SettingsFieldString, Value: cfg.Adapters.OhMyPi.ThinkingLevel},
			},
		},
		{
			ID:          "harness.claude",
			Title:       "Harness · Claude Code",
			Description: "Claude Code CLI configuration",
			Fields: []SettingsField{
				{Section: "adapters.claude_code", Key: "binary_path", Label: "Binary Path", Type: SettingsFieldPath, Value: cfg.Adapters.ClaudeCode.BinaryPath},
				{Section: "adapters.claude_code", Key: "model", Label: "Model", Type: SettingsFieldString, Value: cfg.Adapters.ClaudeCode.Model},
				{Section: "adapters.claude_code", Key: "permission_mode", Label: "Permission Mode", Type: SettingsFieldString, Value: cfg.Adapters.ClaudeCode.PermissionMode},
				{Section: "adapters.claude_code", Key: "max_turns", Label: "Max Turns", Type: SettingsFieldString, Value: strconv.Itoa(cfg.Adapters.ClaudeCode.MaxTurns)},
				{Section: "adapters.claude_code", Key: "max_budget_usd", Label: "Max Budget USD", Type: SettingsFieldString, Value: formatFloat(cfg.Adapters.ClaudeCode.MaxBudgetUSD)},
			},
		},
		{
			ID:          "harness.codex",
			Title:       "Harness · Codex",
			Description: "Codex CLI configuration",
			Fields: []SettingsField{
				{Section: "adapters.codex", Key: "binary_path", Label: "Binary Path", Type: SettingsFieldPath, Value: cfg.Adapters.Codex.BinaryPath},
				{Section: "adapters.codex", Key: "model", Label: "Model", Type: SettingsFieldString, Value: cfg.Adapters.Codex.Model},
				{Section: "adapters.codex", Key: "approval_mode", Label: "Approval Mode", Type: SettingsFieldString, Value: cfg.Adapters.Codex.ApprovalMode},
				{Section: "adapters.codex", Key: "full_auto", Label: "Full Auto", Type: SettingsFieldBool, Value: boolStr(cfg.Adapters.Codex.FullAuto)},
				{Section: "adapters.codex", Key: "quiet", Label: "Quiet", Type: SettingsFieldBool, Value: boolStr(cfg.Adapters.Codex.Quiet)},
			},
		},
		{
			ID:          "provider.linear",
			Title:       "Provider · Linear",
			Description: "Linear work item source configuration",
			Fields: []SettingsField{
				{Section: "adapters.linear", Key: "api_key_ref", Label: "API Key", Type: SettingsFieldSecret, Value: secretDisplayValue(cfg.Adapters.Linear.APIKeyRef, cfg.Adapters.Linear.APIKey), Sensitive: true, Status: secretStatus(cfg.Adapters.Linear.APIKeyRef, cfg.Adapters.Linear.APIKey)},
				{Section: "adapters.linear", Key: "team_id", Label: "Team ID", Type: SettingsFieldString, Value: cfg.Adapters.Linear.TeamID},
				{Section: "adapters.linear", Key: "assignee_filter", Label: "Assignee Filter", Type: SettingsFieldString, Value: cfg.Adapters.Linear.AssigneeFilter},
				{Section: "adapters.linear", Key: "poll_interval", Label: "Poll Interval", Type: SettingsFieldString, Value: cfg.Adapters.Linear.PollInterval},
				{Section: "adapters.linear", Key: "state_mappings", Label: "State Mappings", Type: SettingsFieldKeyValue, Value: formatMap(cfg.Adapters.Linear.StateMappings)},
			},
		},
		{
			ID:          "provider.gitlab",
			Title:       "Provider · GitLab",
			Description: "GitLab issue and MR integration",
			Fields: []SettingsField{
				{Section: "adapters.gitlab", Key: "token_ref", Label: "Token", Type: SettingsFieldSecret, Value: secretDisplayValue(cfg.Adapters.GitLab.TokenRef, cfg.Adapters.GitLab.Token), Sensitive: true, Status: secretStatus(cfg.Adapters.GitLab.TokenRef, cfg.Adapters.GitLab.Token)},
				{Section: "adapters.gitlab", Key: "base_url", Label: "Base URL", Type: SettingsFieldString, Value: cfg.Adapters.GitLab.BaseURL},
				{Section: "adapters.gitlab", Key: "assignee", Label: "Assignee", Type: SettingsFieldString, Value: cfg.Adapters.GitLab.Assignee},
				{Section: "adapters.gitlab", Key: "poll_interval", Label: "Poll Interval", Type: SettingsFieldString, Value: cfg.Adapters.GitLab.PollInterval},
				{Section: "adapters.gitlab", Key: "state_mappings", Label: "State Mappings", Type: SettingsFieldKeyValue, Value: formatMap(cfg.Adapters.GitLab.StateMappings)},
			},
		},
		{
			ID:          "provider.sentry",
			Title:       "Provider · Sentry",
			Description: "Sentry issue source configuration",
			Fields: []SettingsField{
				{Section: "adapters.sentry", Key: "token_ref", Label: "Token", Type: SettingsFieldSecret, Value: secretDisplayValue(cfg.Adapters.Sentry.TokenRef, cfg.Adapters.Sentry.Token), Sensitive: true, Status: config.SentryAuthSource(cfg.Adapters.Sentry)},
				{Section: "adapters.sentry", Key: "base_url", Label: "Base URL", Type: SettingsFieldString, Value: sentryBaseURLFieldValue(cfg.Adapters.Sentry)},
				{Section: "adapters.sentry", Key: "organization", Label: "Organization", Type: SettingsFieldString, Value: cfg.Adapters.Sentry.Organization},
				{Section: "adapters.sentry", Key: "projects", Label: "Projects", Type: SettingsFieldStringList, Value: strings.Join(cfg.Adapters.Sentry.Projects, ",")},
			},
		},
		{
			ID:          "provider.github",
			Title:       "Provider · GitHub",
			Description: "GitHub issues and PR integration",
			Fields: []SettingsField{
				{Section: "adapters.github", Key: "token_ref", Label: "Token", Type: SettingsFieldSecret, Value: secretDisplayValue(cfg.Adapters.GitHub.TokenRef, cfg.Adapters.GitHub.Token), Sensitive: true, Status: config.GitHubAuthSource(cfg.Adapters.GitHub)},
				{Section: "adapters.github", Key: "assignee", Label: "Assignee", Type: SettingsFieldString, Value: cfg.Adapters.GitHub.Assignee},
				{Section: "adapters.github", Key: "poll_interval", Label: "Poll Interval", Type: SettingsFieldString, Value: cfg.Adapters.GitHub.PollInterval},
				{Section: "adapters.github", Key: "reviewers", Label: "Reviewers", Type: SettingsFieldStringList, Value: strings.Join(cfg.Adapters.GitHub.Reviewers, ",")},
				{Section: "adapters.github", Key: "labels", Label: "Labels", Type: SettingsFieldStringList, Value: strings.Join(cfg.Adapters.GitHub.Labels, ",")},
				{Section: "adapters.github", Key: "state_mappings", Label: "State Mappings", Type: SettingsFieldKeyValue, Value: formatMap(cfg.Adapters.GitHub.StateMappings)},
			},
		},
		{
			ID:          "repo.glab",
			Title:       "Repo Lifecycle · glab",
			Description: "GitLab MR automation",
			Fields: []SettingsField{
				{Section: "adapters.glab", Key: "reviewers", Label: "Reviewers", Type: SettingsFieldStringList, Value: strings.Join(cfg.Adapters.Glab.Reviewers, ",")},
				{Section: "adapters.glab", Key: "labels", Label: "Labels", Type: SettingsFieldStringList, Value: strings.Join(cfg.Adapters.Glab.Labels, ",")},
			},
		},
		{
			ID:          "repos",
			Title:       "Repo Overrides",
			Description: "Per-repo documentation paths",
			Fields:      []SettingsField{{Section: "repos", Key: "doc_paths", Label: "Repo Doc Paths", Type: SettingsFieldKeyValue, Value: formatRepos(cfg.Repos)}},
		},
	}
	for i := range sections {
		annotateFieldPresentation(&sections[i])
		sections[i].Status = sectionStatus(sections[i])
	}
	annotateHarnessWarnings(sections, cfg, app.DiagnoseHarnesses(cfg, ""))

	return sections
}

func annotateHarnessWarnings(sections []SettingsSection, cfg *config.Config, diagnostics app.HarnessDiagnostics) {
	if !diagnostics.HasWarnings() {
		return
	}
	harnessWarnings := diagnostics.HarnessWarnings()
	routedHarnesses := configuredPhaseHarnesses(cfg)
	for i := range sections {
		switch sections[i].ID {
		case settingHarness:
			setSectionWarning(&sections[i], diagnostics.PhaseWarnings())
		case "harness.ohmypi":
			setHarnessSectionWarning(&sections[i], config.HarnessOhMyPi, routedHarnesses, harnessWarnings[config.HarnessOhMyPi])
		case "harness.claude":
			setHarnessSectionWarning(&sections[i], config.HarnessClaudeCode, routedHarnesses, harnessWarnings[config.HarnessClaudeCode])
		case "harness.codex":
			setHarnessSectionWarning(&sections[i], config.HarnessCodex, routedHarnesses, harnessWarnings[config.HarnessCodex])
		}
	}
}

func configuredPhaseHarnesses(cfg *config.Config) map[config.HarnessName]bool {
	harnesses := make(map[config.HarnessName]bool, 4)
	if cfg == nil {
		return harnesses
	}
	for _, harness := range []config.HarnessName{
		cfg.Harness.Phase.Planning,
		cfg.Harness.Phase.Implementation,
		cfg.Harness.Phase.Review,
		cfg.Harness.Phase.Foreman,
	} {
		if harness == "" {
			continue
		}
		harnesses[harness] = true
	}

	return harnesses
}

func setHarnessSectionWarning(section *SettingsSection, harness config.HarnessName, routedHarnesses map[config.HarnessName]bool, warnings []string) {
	if !routedHarnesses[harness] {
		return
	}
	setSectionWarning(section, warnings)
}

func setSectionWarning(section *SettingsSection, warnings []string) {
	if section == nil || len(warnings) == 0 {
		return
	}
	section.Status = statusWarning
	section.Error = strings.Join(warnings, "\n")
}

func buildProviderStatuses(cfg *config.Config) map[string]ProviderStatus {
	statuses := map[string]ProviderStatus{
		"linear": {
			Title:       "Linear",
			Configured:  cfg.Adapters.Linear.APIKeyRef != "" || strings.TrimSpace(cfg.Adapters.Linear.APIKey) != "",
			Connected:   false,
			AuthSource:  secretStatus(cfg.Adapters.Linear.APIKeyRef, cfg.Adapters.Linear.APIKey),
			Description: "Uses OS keychain-backed API key",
		},
		providerGitlab: {
			Title:       "GitLab",
			Configured:  cfg.Adapters.GitLab.TokenRef != "" || strings.TrimSpace(cfg.Adapters.GitLab.Token) != "",
			Connected:   false,
			AuthSource:  secretStatus(cfg.Adapters.GitLab.TokenRef, cfg.Adapters.GitLab.Token),
			Description: "Uses OS keychain-backed token",
		},
		providerSentry: {
			Title:       "Sentry",
			Configured:  config.SentryAuthConfigured(cfg.Adapters.Sentry),
			Connected:   false,
			AuthSource:  config.SentryAuthSource(cfg.Adapters.Sentry),
			Description: "Uses keychain, environment, or sentry CLI authentication",
		},
		providerGithub: {
			Title:       "GitHub",
			Configured:  config.GitHubAuthConfigured(cfg.Adapters.GitHub),
			Connected:   false,
			AuthSource:  config.GitHubAuthSource(cfg.Adapters.GitHub),
			Description: "Uses OS keychain-backed token or gh CLI fallback",
		},
	}

	return statuses
}

func configFromSections(sections []SettingsSection) (*config.Config, error) {
	cfg := &config.Config{}
	for _, sec := range sections {
		for _, field := range sec.Fields {
			if err := applyField(cfg, field); err != nil {
				return nil, err
			}
		}
	}

	return cfg, nil
}

func applyField(cfg *config.Config, field SettingsField) error {
	value := strings.TrimSpace(field.Value)
	fieldPath := field.Section + "." + field.Key
	switch fieldPath {
	case "commit.strategy":
		cfg.Commit.Strategy = config.CommitStrategy(value)
	case "commit.message_format":
		cfg.Commit.MessageFormat = config.CommitMessageFormat(value)
	case "commit.message_template":
		cfg.Commit.MessageTemplate = value
	case "plan.max_parse_retries":
		parsed, err := parseOptionalInt(fieldPath, value)
		if err != nil {
			return err
		}
		cfg.Plan.MaxParseRetries = parsed
	case "review.pass_threshold":
		cfg.Review.PassThreshold = config.PassThreshold(value)
	case "review.max_cycles":
		parsed, err := parseOptionalInt(fieldPath, value)
		if err != nil {
			return err
		}
		cfg.Review.MaxCycles = parsed
	case "foreman.question_timeout":
		cfg.Foreman.QuestionTimeout = value
	case "harness.default":
		cfg.Harness.Default = config.HarnessName(value)
	case "harness.phase.planning":
		cfg.Harness.Phase.Planning = config.HarnessName(value)
	case "harness.phase.implementation":
		cfg.Harness.Phase.Implementation = config.HarnessName(value)
	case "harness.phase.review":
		cfg.Harness.Phase.Review = config.HarnessName(value)
	case "harness.phase.foreman":
		cfg.Harness.Phase.Foreman = config.HarnessName(value)
	case "adapters.ohmypi.bun_path":
		cfg.Adapters.OhMyPi.BunPath = value
	case "adapters.ohmypi.bridge_path":
		cfg.Adapters.OhMyPi.BridgePath = value
	case "adapters.ohmypi.thinking_level":
		cfg.Adapters.OhMyPi.ThinkingLevel = value
	case "adapters.claude_code.binary_path":
		cfg.Adapters.ClaudeCode.BinaryPath = value
	case "adapters.claude_code.model":
		cfg.Adapters.ClaudeCode.Model = value
	case "adapters.claude_code.permission_mode":
		cfg.Adapters.ClaudeCode.PermissionMode = value
	case "adapters.claude_code.max_turns":
		parsed, err := parseInt(fieldPath, value)
		if err != nil {
			return err
		}
		cfg.Adapters.ClaudeCode.MaxTurns = parsed
	case "adapters.claude_code.max_budget_usd":
		parsed, err := parseFloat(fieldPath, value)
		if err != nil {
			return err
		}
		cfg.Adapters.ClaudeCode.MaxBudgetUSD = parsed
	case "adapters.codex.binary_path":
		cfg.Adapters.Codex.BinaryPath = value
	case "adapters.codex.model":
		cfg.Adapters.Codex.Model = value
	case "adapters.codex.approval_mode":
		cfg.Adapters.Codex.ApprovalMode = value
	case "adapters.codex.full_auto":
		parsed, err := parseFieldBool(fieldPath, value)
		if err != nil {
			return err
		}
		cfg.Adapters.Codex.FullAuto = parsed
	case "adapters.codex.quiet":
		parsed, err := parseFieldBool(fieldPath, value)
		if err != nil {
			return err
		}
		cfg.Adapters.Codex.Quiet = parsed
	case "adapters.linear.api_key_ref":
		cfg.Adapters.Linear.APIKey, cfg.Adapters.Linear.APIKeyRef = applySecretField(value, "linear.api_key")
	case "adapters.linear.team_id":
		cfg.Adapters.Linear.TeamID = value
	case "adapters.linear.assignee_filter":
		cfg.Adapters.Linear.AssigneeFilter = value
	case "adapters.linear.poll_interval":
		cfg.Adapters.Linear.PollInterval = value
	case "adapters.linear.state_mappings":
		cfg.Adapters.Linear.StateMappings = parseMap(value)
	case "adapters.gitlab.token_ref":
		cfg.Adapters.GitLab.Token, cfg.Adapters.GitLab.TokenRef = applySecretField(value, "gitlab.token")
	case "adapters.gitlab.base_url":
		cfg.Adapters.GitLab.BaseURL = value
	case "adapters.gitlab.assignee":
		cfg.Adapters.GitLab.Assignee = value
	case "adapters.gitlab.poll_interval":
		cfg.Adapters.GitLab.PollInterval = value
	case "adapters.gitlab.state_mappings":
		cfg.Adapters.GitLab.StateMappings = parseMap(value)
	case "adapters.sentry.token_ref":
		cfg.Adapters.Sentry.Token, cfg.Adapters.Sentry.TokenRef = applySecretField(value, "sentry.token")
	case "adapters.sentry.base_url":
		cfg.Adapters.Sentry.BaseURL = value
		cfg.Adapters.Sentry.BaseURLExplicit = value != ""
	case "adapters.sentry.organization":
		cfg.Adapters.Sentry.Organization = value
	case "adapters.sentry.projects":
		cfg.Adapters.Sentry.Projects = parseList(value)
	case "adapters.github.token_ref":
		cfg.Adapters.GitHub.Token, cfg.Adapters.GitHub.TokenRef = applySecretField(value, "github.token")
	case "adapters.github.assignee":
		cfg.Adapters.GitHub.Assignee = value
	case "adapters.github.poll_interval":
		cfg.Adapters.GitHub.PollInterval = value
	case "adapters.github.reviewers":
		cfg.Adapters.GitHub.Reviewers = parseList(value)
	case "adapters.github.labels":
		cfg.Adapters.GitHub.Labels = parseList(value)
	case "adapters.github.state_mappings":
		cfg.Adapters.GitHub.StateMappings = parseMap(value)
	case "adapters.glab.reviewers":
		cfg.Adapters.Glab.Reviewers = parseList(value)
	case "adapters.glab.labels":
		cfg.Adapters.Glab.Labels = parseList(value)
	case "repos.doc_paths":
		cfg.Repos = parseRepos(value)
	}

	return nil
}

func validateSettingsConfig(cfg *config.Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", "substrate-settings-validate-*.yaml")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()

		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if _, err := config.Load(tmpPath); err != nil {
		return err
	}
	for _, durationValue := range []string{cfg.Adapters.Linear.PollInterval, cfg.Adapters.GitLab.PollInterval, cfg.Adapters.GitHub.PollInterval, cfg.Foreman.QuestionTimeout} {
		if strings.TrimSpace(durationValue) == "" {
			continue
		}
		if _, err := time.ParseDuration(durationValue); err != nil {
			return fmt.Errorf("invalid duration %q: %w", durationValue, err)
		}
	}
	if cfg.Adapters.GitLab.BaseURL != "" {
		if _, err := url.ParseRequestURI(cfg.Adapters.GitLab.BaseURL); err != nil {
			return fmt.Errorf("invalid gitlab base_url: %w", err)
		}
	}

	return nil
}

func findSection(sections []SettingsSection, id string) SettingsSection {
	for _, sec := range sections {
		if sec.ID == id {
			return sec
		}
	}

	return SettingsSection{}
}

func sectionStatus(section SettingsSection) string {
	for _, field := range section.Fields {
		if field.Required && strings.TrimSpace(field.Value) == "" {
			return "incomplete"
		}
	}

	return "configured"
}

func annotateFieldPresentation(section *SettingsSection) {
	for i := range section.Fields {
		description, defaultValue := fieldPresentation(section.Fields[i].Section, section.Fields[i].Key)
		section.Fields[i].Description = description
		section.Fields[i].DefaultValue = defaultValue
	}
}

func fieldPresentation(section, key string) (description string, defaultValue string) {
	switch section + "." + key {
	case "commit.strategy":
		return "Controls how often implementation work is committed while an agent is running.", "semi-regular"
	case "commit.message_format":
		return "Chooses how commit messages are generated for agent-authored commits.", "ai-generated"
	case "commit.message_template":
		return "Custom commit message template used only when the message format is set to custom.", statusEmpty
	case "plan.max_parse_retries":
		return "Maximum retries for repairing malformed plan output before planning fails.", "2"
	case "review.pass_threshold":
		return "Sets how strict the review pipeline is before a change is accepted.", "minor_ok"
	case "review.max_cycles":
		return "Maximum review and re-implementation cycles before escalation to a human.", "3"
	case "foreman.question_timeout":
		return "How long Foreman waits before timing out a question; 0 disables the timeout.", "0"
	case "harness.default":
		return "Primary harness used whenever a phase-specific override is not set.", "ohmypi"
	case "harness.phase.planning":
		return "Overrides the harness used for the planning phase.", hintInheritsHarnessDefault
	case "harness.phase.implementation":
		return "Overrides the harness used for the implementation phase.", hintInheritsHarnessDefault
	case "harness.phase.review":
		return "Overrides the harness used for the review phase.", hintInheritsHarnessDefault
	case "harness.phase.foreman":
		return "Overrides the harness used for the Foreman coordination phase.", hintInheritsHarnessDefault
	case "adapters.ohmypi.bun_path":
		return "Optional override for the Bun executable used only when Substrate launches a source bridge script instead of the packaged compiled bridge.", "auto-detect on PATH when needed"
	case "adapters.ohmypi.bridge_path":
		return "Optional override for the oh-my-pi bridge binary or script; leave empty to use the packaged compiled bridge.", "packaged compiled bridge"
	case "adapters.ohmypi.thinking_level":
		return "Reasoning depth hint forwarded to the oh-my-pi bridge harness.", statusEmpty
	case "adapters.claude_code.binary_path":
		return "Path to the Claude Code CLI binary.", statusEmpty
	case "adapters.claude_code.model":
		return "Claude model name passed to the CLI for new sessions.", statusEmpty
	case "adapters.claude_code.permission_mode":
		return "Permission or sandbox mode requested from Claude Code.", statusEmpty
	case "adapters.claude_code.max_turns":
		return "Upper bound on Claude Code turns for a single session.", "0"
	case "adapters.claude_code.max_budget_usd":
		return "Optional USD budget ceiling passed to Claude Code sessions.", "0"
	case "adapters.codex.binary_path":
		return "Path to the Codex CLI binary.", statusEmpty
	case "adapters.codex.model":
		return "Codex model name used for new sessions.", statusEmpty
	case "adapters.codex.approval_mode":
		return "Approval mode passed to Codex for command execution.", statusEmpty
	case "adapters.codex.full_auto":
		return "Allows Codex to run in full-auto mode when the CLI supports it.", settingFalse
	case "adapters.codex.quiet":
		return "Reduces Codex CLI verbosity in session output.", settingFalse
	case "adapters.linear.api_key_ref":
		return "Linear API credential stored in config or the OS keychain.", statusEmpty
	case "adapters.linear.team_id":
		return "Default Linear team used for scoped browsing and identifier resolution.", statusEmpty
	case "adapters.linear.assignee_filter":
		return "Watcher assignee filter; use 'me' or a specific Linear user identifier.", statusEmpty
	case "adapters.linear.poll_interval":
		return "Polling interval for Linear watch updates.", "30s"
	case "adapters.linear.state_mappings":
		return "Maps Substrate tracker states to Linear workflow states.", statusEmpty
	case "adapters.gitlab.token_ref":
		return "GitLab token stored in config or the OS keychain for issue and MR APIs.", statusEmpty
	case "adapters.gitlab.base_url":
		return "Base URL for the GitLab instance used by the adapter.", "https://gitlab.com"
	case "adapters.gitlab.assignee":
		return "GitLab assignee username filter used by watch polling.", statusEmpty
	case "adapters.gitlab.poll_interval":
		return "Polling interval for GitLab watch updates.", "60s"
	case "adapters.gitlab.state_mappings":
		return "Maps Substrate tracker states to GitLab issue states.", statusEmpty
	case "adapters.github.token_ref":
		return "GitHub token stored in config or the OS keychain; runtime may also fall back to gh auth.", statusEmpty
	case "adapters.github.assignee":
		return "GitHub assignee filter used by watch polling.", statusEmpty
	case "adapters.github.poll_interval":
		return "Polling interval for GitHub watch updates.", "60s"
	case "adapters.github.reviewers":
		return "Default reviewers requested when Substrate opens GitHub pull requests.", statusEmpty
	case "adapters.github.labels":
		return "Default labels applied to GitHub pull requests created by Substrate.", statusEmpty
	case "adapters.github.state_mappings":
		return "Maps Substrate tracker states to GitHub issue states.", statusEmpty
	case "adapters.sentry.token_ref":
		return "Sentry token stored in config or the OS keychain; runtime may also use SENTRY_AUTH_TOKEN or authenticated sentry CLI.", statusEmpty
	case "adapters.sentry.base_url":
		return "Base URL for the Sentry API used by the adapter and sentry CLI fallback.", config.DefaultSentryBaseURL
	case "adapters.sentry.organization":
		return "Sentry organization slug required for browsing and resolving issues.", statusEmpty
	case "adapters.sentry.projects":
		return "Optional comma-separated Sentry project allowlist used to scope browsing.", statusEmpty
	case "adapters.glab.reviewers":
		return "Default GitLab merge request reviewers added by the glab lifecycle adapter.", statusEmpty
	case "adapters.glab.labels":
		return "Default GitLab merge request labels added by the glab lifecycle adapter.", statusEmpty
	case "repos.doc_paths":
		return "Per-repository documentation paths injected into planning context.", statusEmpty
	default:
		return "", ""
	}
}

func intPtrStr(p *int) string {
	if p == nil {
		return ""
	}

	return strconv.Itoa(*p)
}

func boolStr(v bool) string {
	if v {
		return "true"
	}

	return settingFalse
}

func formatFloat(v float64) string {
	if v == 0 {
		return ""
	}

	return strconv.FormatFloat(v, 'f', -1, 64)
}

func parseOptionalInt(fieldPath, v string) (*int, error) {
	if strings.TrimSpace(v) == "" {
		return nil, nil
	}
	parsed, err := parseInt(fieldPath, v)
	if err != nil {
		return nil, err
	}

	return &parsed, nil
}

func parseInt(fieldPath, v string) (int, error) {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid integer %q: %w", fieldPath, trimmed, err)
	}

	return n, nil
}

func parseFloat(fieldPath, v string) (float64, error) {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return 0, nil
	}
	f, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid number %q: %w", fieldPath, trimmed, err)
	}

	return f, nil
}

func parseBool(v string) bool {
	b, _ := strconv.ParseBool(strings.TrimSpace(v))

	return b
}

func parseFieldBool(fieldPath, v string) (bool, error) {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(trimmed)
	if err != nil {
		return false, fmt.Errorf("%s: invalid boolean %q: %w", fieldPath, trimmed, err)
	}

	return b, nil
}

func parseList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}

	return out
}

func parseMap(v string) map[string]string {
	result := map[string]string{}
	for _, part := range parseList(v) {
		pieces := strings.SplitN(part, "=", 2)
		if len(pieces) != 2 {
			continue
		}
		result[strings.TrimSpace(pieces[0])] = strings.TrimSpace(pieces[1])
	}
	if len(result) == 0 {
		return nil
	}

	return result
}

func formatMap(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+m[k])
	}

	return strings.Join(parts, ",")
}

func secretRef(name string) string {
	return "keychain:" + name
}

func applySecretField(value, defaultRef string) (string, string) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", ""
	}
	if strings.HasPrefix(trimmed, "keychain:") {
		return "", trimmed
	}

	return trimmed, secretRef(defaultRef)
}

func secretDisplayValue(ref, value string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	if strings.TrimSpace(ref) != "" {
		return ref
	}

	return ""
}

func secretStatus(ref, value string) string {
	if strings.TrimSpace(value) != "" {
		return "pending save"
	}
	if strings.TrimSpace(ref) != "" {
		return "keychain"
	}

	return "unset"
}

func parseRepos(v string) map[string]config.RepoConfig {
	result := map[string]config.RepoConfig{}
	for entry := range strings.SplitSeq(strings.TrimSpace(v), ";") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			continue
		}
		repo := strings.TrimSpace(parts[0])
		paths := parseList(strings.ReplaceAll(parts[1], "|", ","))
		result[repo] = config.RepoConfig{DocPaths: paths}
	}
	if len(result) == 0 {
		return nil
	}

	return result
}

func formatRepos(repos map[string]config.RepoConfig) string {
	if len(repos) == 0 {
		return ""
	}
	keys := make([]string, 0, len(repos))
	for k := range repos {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+strings.Join(repos[k].DocPaths, "|"))
	}

	return strings.Join(parts, ";")
}
