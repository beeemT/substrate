package views

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	githubadapter "github.com/beeemT/substrate/internal/adapter/github"
	gitlabadapter "github.com/beeemT/substrate/internal/adapter/gitlab"
	linearadapter "github.com/beeemT/substrate/internal/adapter/linear"
	"github.com/beeemT/substrate/internal/app"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/gitwork"
	"github.com/beeemT/substrate/internal/orchestrator"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
	"github.com/pelletier/go-toml/v2"
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

type SettingsField struct {
	Section     string
	Key         string
	Label       string
	Description string
	Type        SettingsFieldType
	Value       string
	Options     []string
	Sensitive   bool
	Required    bool
	Dirty       bool
	Error       string
	Status      string
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
	Sections  []SettingsSection
	Providers map[string]ProviderStatus
	RawTOML   string
}

type SettingsApplyResult struct {
	Services viewsServicesReload
	Message  string
}

type SettingsService struct {
	workItemRepo  repository.WorkItemRepository
	planRepo      repository.PlanRepository
	subPlanRepo   repository.SubPlanRepository
	workspaceRepo repository.WorkspaceRepository
	sessionRepo   repository.SessionRepository
	questionRepo  repository.QuestionRepository
	instanceRepo  repository.InstanceRepository
	reviewRepo    repository.ReviewRepository
	eventRepo     repository.EventRepository
	secretStore   config.SecretStore
}

type viewsServicesReload struct {
	Services     Services
	ConfigPath   string
	SessionsDir  string
	SettingsData SettingsSnapshot
}

func NewSettingsService(
	workItemRepo repository.WorkItemRepository,
	planRepo repository.PlanRepository,
	subPlanRepo repository.SubPlanRepository,
	workspaceRepo repository.WorkspaceRepository,
	sessionRepo repository.SessionRepository,
	questionRepo repository.QuestionRepository,
	instanceRepo repository.InstanceRepository,
	reviewRepo repository.ReviewRepository,
	eventRepo repository.EventRepository,
	secretStore config.SecretStore,
) *SettingsService {
	return &SettingsService{
		workItemRepo:  workItemRepo,
		planRepo:      planRepo,
		subPlanRepo:   subPlanRepo,
		workspaceRepo: workspaceRepo,
		sessionRepo:   sessionRepo,
		questionRepo:  questionRepo,
		instanceRepo:  instanceRepo,
		reviewRepo:    reviewRepo,
		eventRepo:     eventRepo,
		secretStore:   secretStore,
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
	return SettingsSnapshot{
		Sections:  buildSettingsSections(cfg),
		Providers: buildProviderStatuses(cfg),
		RawTOML:   string(raw),
	}, nil
}

func (s *SettingsService) SaveRaw(raw string) error {
	cfgPath, err := config.ConfigPath()
	if err != nil {
		return err
	}
	return os.WriteFile(cfgPath, []byte(raw), 0o644)
}

func (s *SettingsService) Serialize(sections []SettingsSection) (string, *config.Config, error) {
	cfg := configFromSections(sections)
	if err := validateSettingsConfig(cfg); err != nil {
		return "", nil, err
	}
	if err := config.SaveSecrets(cfg, s.secretStore); err != nil {
		return "", nil, err
	}
	var buf strings.Builder
	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		return "", nil, fmt.Errorf("encode config: %w", err)
	}
	return buf.String(), cfg, nil
}

func (s *SettingsService) Apply(ctx context.Context, raw string, current Services) (SettingsApplyResult, error) {
	if err := s.SaveRaw(raw); err != nil {
		return SettingsApplyResult{}, err
	}
	cfgPath, err := config.ConfigPath()
	if err != nil {
		return SettingsApplyResult{}, err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return SettingsApplyResult{}, err
	}
	if err := config.LoadSecrets(cfg, s.secretStore); err != nil {
		return SettingsApplyResult{}, err
	}
	if current.Foreman != nil {
		_ = current.Foreman.Stop(ctx)
	}
	reloaded, err := s.rebuildServices(ctx, cfg, current)
	if err != nil {
		return SettingsApplyResult{}, err
	}
	return SettingsApplyResult{Services: reloaded, Message: "Settings applied"}, nil
}

func (s *SettingsService) TestProvider(ctx context.Context, provider string, sections []SettingsSection) (ProviderStatus, error) {
	cfg := configFromSections(sections)
	if err := validateSettingsConfig(cfg); err != nil {
		return ProviderStatus{}, err
	}
	switch provider {
	case "linear":
		status := buildProviderStatuses(cfg)[provider]
		if strings.TrimSpace(cfg.Adapters.Linear.APIKey) == "" {
			return status, fmt.Errorf("linear api key is required")
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
	case "gitlab":
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
	case "github":
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

func (s *SettingsService) LoginProvider(ctx context.Context, provider, harness string, sections []SettingsSection, svcs Services) (SettingsSection, error) {
	req := adapter.HarnessActionRequest{Action: "login_provider", Provider: provider, HarnessName: harness}
	runner := harnessRunnerForProvider(harness, svcs)
	if runner == nil {
		return SettingsSection{}, fmt.Errorf("harness %q does not support login actions", harness)
	}
	result, err := config.RunHarnessAction(ctx, runner, req)
	if err != nil {
		return SettingsSection{}, err
	}
	if !result.Success {
		return SettingsSection{}, fmt.Errorf("%s", result.Message)
	}
	cfg := configFromSections(sections)
	switch provider {
	case "github":
		cfg.Adapters.GitHub.Token = result.Credentials["token"]
		cfg.Adapters.GitHub.TokenRef = secretRef("github.token")
		return findSection(buildSettingsSections(cfg), "provider.github"), nil
	default:
		return SettingsSection{}, fmt.Errorf("login not implemented for provider %q", provider)
	}
}

func (s *SettingsService) rebuildServices(ctx context.Context, cfg *config.Config, current Services) (viewsServicesReload, error) {
	workItemSvc := service.NewWorkItemService(s.workItemRepo)
	planSvc := service.NewPlanService(s.planRepo, s.subPlanRepo)
	workspaceSvc := service.NewWorkspaceService(s.workspaceRepo)
	sessionSvc := service.NewSessionService(s.sessionRepo)
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
	repoLifecycleAdapters := app.BuildRepoLifecycleAdapters(ctx, cfg, current.WorkspaceDir)
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
	discoverer := orchestrator.NewDiscoverer(gitClient, cfg)
	harnesses, err := app.BuildAgentHarnesses(cfg, current.WorkspaceDir)
	if err != nil {
		return viewsServicesReload{}, fmt.Errorf("building agent harnesses: %w", err)
	}
	planningCfg := orchestrator.PlanningConfigFromConfig(cfg)
	planningSvc, err := orchestrator.NewPlanningService(planningCfg, discoverer, gitClient, harnesses.Planning, planSvc, workItemSvc, s.planRepo, s.subPlanRepo, s.eventRepo, workspaceSvc, cfg)
	if err != nil {
		return viewsServicesReload{}, fmt.Errorf("build planning service: %w", err)
	}
	implSvc := orchestrator.NewImplementationService(cfg, harnesses.Implementation, gitClient, bus, planSvc, workItemSvc, sessionSvc, s.subPlanRepo, s.sessionRepo, s.eventRepo, workspaceSvc)
	reviewPipeline := orchestrator.NewReviewPipeline(cfg, harnesses.Review, reviewSvc, sessionSvc, planSvc, workItemSvc, s.sessionRepo, s.planRepo, bus)
	resumption := orchestrator.NewResumption(harnesses.Resume, sessionSvc, planSvc, s.sessionRepo, bus)
	foreman := orchestrator.NewForeman(cfg, harnesses.Foreman, planSvc, questionSvc, sessionSvc, s.planRepo, bus)
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
			WorkItem:       workItemSvc,
			Plan:           planSvc,
			SubPlan:        s.subPlanRepo,
			Session:        sessionSvc,
			Question:       questionSvc,
			Instance:       instanceSvc,
			Workspace:      workspaceSvc,
			Review:         reviewSvc,
			Planning:       planningSvc,
			Implementation: implSvc,
			ReviewPipeline: reviewPipeline,
			Resumption:     resumption,
			Foreman:        foreman,
			Cfg:            cfg,
			Adapters:       adapters,
			Harnesses:      harnesses,
			GitClient:      gitClient,
			Bus:            bus,
			InstanceID:     current.InstanceID,
			WorkspaceID:    current.WorkspaceID,
			WorkspaceDir:   current.WorkspaceDir,
			WorkspaceName:  current.WorkspaceName,
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
			Fields: []SettingsField{
				{Section: "foreman", Key: "enabled", Label: "Enabled", Type: SettingsFieldBool, Value: boolStr(cfg.Foreman.Enabled)},
				{Section: "foreman", Key: "question_timeout", Label: "Question Timeout", Type: SettingsFieldString, Value: cfg.Foreman.QuestionTimeout},
			},
		},
		{
			ID:          "harness",
			Title:       "Harness Routing",
			Description: "Select which harness runs each phase",
			Fields: []SettingsField{
				{Section: "harness", Key: "default", Label: "Default Harness", Type: SettingsFieldEnum, Value: string(cfg.Harness.Default), Options: []string{"ohmypi", "claude-code", "codex"}, Required: true},
				{Section: "harness", Key: "fallback", Label: "Fallback Harnesses", Type: SettingsFieldStringList, Value: strings.Join(harnessNamesToStrings(cfg.Harness.Fallback), ",")},
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
				{Section: "adapters.gitlab", Key: "project_id", Label: "Project ID", Type: SettingsFieldString, Value: int64Str(cfg.Adapters.GitLab.ProjectID)},
				{Section: "adapters.gitlab", Key: "assignee", Label: "Assignee", Type: SettingsFieldString, Value: cfg.Adapters.GitLab.Assignee},
				{Section: "adapters.gitlab", Key: "poll_interval", Label: "Poll Interval", Type: SettingsFieldString, Value: cfg.Adapters.GitLab.PollInterval},
				{Section: "adapters.gitlab", Key: "state_mappings", Label: "State Mappings", Type: SettingsFieldKeyValue, Value: formatMap(cfg.Adapters.GitLab.StateMappings)},
			},
		},
		{
			ID:          "provider.github",
			Title:       "Provider · GitHub",
			Description: "GitHub issues and PR integration",
			Fields: []SettingsField{
				{Section: "adapters.github", Key: "token_ref", Label: "Token", Type: SettingsFieldSecret, Value: secretDisplayValue(cfg.Adapters.GitHub.TokenRef, cfg.Adapters.GitHub.Token), Sensitive: true, Status: githubAuthStatus(cfg)},
				{Section: "adapters.github", Key: "owner", Label: "Owner", Type: SettingsFieldString, Value: cfg.Adapters.GitHub.Owner},
				{Section: "adapters.github", Key: "repo", Label: "Repo", Type: SettingsFieldString, Value: cfg.Adapters.GitHub.Repo},
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
		sections[i].Status = sectionStatus(sections[i])
	}
	return sections
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
		"gitlab": {
			Title:       "GitLab",
			Configured:  cfg.Adapters.GitLab.TokenRef != "" || strings.TrimSpace(cfg.Adapters.GitLab.Token) != "",
			Connected:   false,
			AuthSource:  secretStatus(cfg.Adapters.GitLab.TokenRef, cfg.Adapters.GitLab.Token),
			Description: "Uses OS keychain-backed token",
		},
		"github": {
			Title:       "GitHub",
			Configured:  cfg.Adapters.GitHub.TokenRef != "" || strings.TrimSpace(cfg.Adapters.GitHub.Token) != "" || hasGhCLI(),
			Connected:   false,
			AuthSource:  githubAuthStatus(cfg),
			Description: "Uses OS keychain-backed token or gh CLI fallback",
		},
	}
	return statuses
}

func configFromSections(sections []SettingsSection) *config.Config {
	cfg := &config.Config{}
	for _, sec := range sections {
		for _, field := range sec.Fields {
			applyField(cfg, field)
		}
	}
	return cfg
}

func applyField(cfg *config.Config, field SettingsField) {
	value := strings.TrimSpace(field.Value)
	switch field.Section + "." + field.Key {
	case "commit.strategy":
		cfg.Commit.Strategy = config.CommitStrategy(value)
	case "commit.message_format":
		cfg.Commit.MessageFormat = config.CommitMessageFormat(value)
	case "commit.message_template":
		cfg.Commit.MessageTemplate = value
	case "plan.max_parse_retries":
		cfg.Plan.MaxParseRetries = parseOptionalInt(value)
	case "review.pass_threshold":
		cfg.Review.PassThreshold = config.PassThreshold(value)
	case "review.max_cycles":
		cfg.Review.MaxCycles = parseOptionalInt(value)
	case "foreman.enabled":
		cfg.Foreman.Enabled = parseBool(value)
	case "foreman.question_timeout":
		cfg.Foreman.QuestionTimeout = value
	case "harness.default":
		cfg.Harness.Default = config.HarnessName(value)
	case "harness.fallback":
		cfg.Harness.Fallback = parseHarnessList(value)
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
		cfg.Adapters.ClaudeCode.MaxTurns = parseInt(value)
	case "adapters.claude_code.max_budget_usd":
		cfg.Adapters.ClaudeCode.MaxBudgetUSD = parseFloat(value)
	case "adapters.codex.binary_path":
		cfg.Adapters.Codex.BinaryPath = value
	case "adapters.codex.model":
		cfg.Adapters.Codex.Model = value
	case "adapters.codex.approval_mode":
		cfg.Adapters.Codex.ApprovalMode = value
	case "adapters.codex.full_auto":
		cfg.Adapters.Codex.FullAuto = parseBool(value)
	case "adapters.codex.quiet":
		cfg.Adapters.Codex.Quiet = parseBool(value)
	case "adapters.linear.api_key_ref":
		cfg.Adapters.Linear.APIKey = value
		cfg.Adapters.Linear.APIKeyRef = secretRef("linear.api_key")
	case "adapters.linear.team_id":
		cfg.Adapters.Linear.TeamID = value
	case "adapters.linear.assignee_filter":
		cfg.Adapters.Linear.AssigneeFilter = value
	case "adapters.linear.poll_interval":
		cfg.Adapters.Linear.PollInterval = value
	case "adapters.linear.state_mappings":
		cfg.Adapters.Linear.StateMappings = parseMap(value)
	case "adapters.gitlab.token_ref":
		cfg.Adapters.GitLab.Token = value
		cfg.Adapters.GitLab.TokenRef = secretRef("gitlab.token")
	case "adapters.gitlab.base_url":
		cfg.Adapters.GitLab.BaseURL = value
	case "adapters.gitlab.project_id":
		cfg.Adapters.GitLab.ProjectID = parseInt64(value)
	case "adapters.gitlab.assignee":
		cfg.Adapters.GitLab.Assignee = value
	case "adapters.gitlab.poll_interval":
		cfg.Adapters.GitLab.PollInterval = value
	case "adapters.gitlab.state_mappings":
		cfg.Adapters.GitLab.StateMappings = parseMap(value)
	case "adapters.github.token_ref":
		cfg.Adapters.GitHub.Token = value
		cfg.Adapters.GitHub.TokenRef = secretRef("github.token")
	case "adapters.github.owner":
		cfg.Adapters.GitHub.Owner = value
	case "adapters.github.repo":
		cfg.Adapters.GitHub.Repo = value
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
}

func validateSettingsConfig(cfg *config.Config) error {
	cfgPath, err := config.ConfigPath()
	if err != nil {
		return err
	}
	var buf strings.Builder
	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		return err
	}
	tmp := cfgPath + ".settings-validate"
	if err := os.WriteFile(tmp, []byte(buf.String()), 0o644); err != nil {
		return err
	}
	defer os.Remove(tmp)
	if _, err := config.Load(tmp); err != nil {
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

func authSource(configured bool, fallback bool, configuredLabel, fallbackLabel string) string {
	if configured {
		return configuredLabel
	}
	if fallback {
		return fallbackLabel
	}
	return "unset"
}

func githubAuthStatus(cfg *config.Config) string {
	if strings.TrimSpace(cfg.Adapters.GitHub.Token) != "" {
		return "config token"
	}
	if hasGhCLI() {
		return "gh cli"
	}
	return "unset"
}

func hasGhCLI() bool {
	_, err := exec.LookPath("gh")
	return err == nil
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
	return "false"
}

func harnessNamesToStrings(names []config.HarnessName) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, string(n))
	}
	return out
}

func int64Str(v int64) string {
	if v == 0 {
		return ""
	}
	return strconv.FormatInt(v, 10)
}

func formatFloat(v float64) string {
	if v == 0 {
		return ""
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func parseOptionalInt(v string) *int {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parsed := parseInt(v)
	return &parsed
}

func parseInt(v string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(v))
	return n
}

func parseInt64(v string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	return n
}

func parseFloat(v string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(v), 64)
	return f
}

func parseBool(v string) bool {
	b, _ := strconv.ParseBool(strings.TrimSpace(v))
	return b
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

func parseHarnessList(v string) []config.HarnessName {
	parts := parseList(v)
	out := make([]config.HarnessName, 0, len(parts))
	for _, part := range parts {
		out = append(out, config.HarnessName(part))
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
	for _, entry := range strings.Split(strings.TrimSpace(v), ";") {
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
