package views

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/app"
	"github.com/beeemT/substrate/internal/config"
	daemonapi "github.com/beeemT/substrate/internal/daemon/api"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/gitwork"
	"github.com/beeemT/substrate/internal/logic"
	"github.com/beeemT/substrate/internal/orchestrator"
	"github.com/beeemT/substrate/internal/service"
	"gopkg.in/yaml.v3"
)

// Verify DaemonProvider implements ServiceProvider at compile time.
var _ ServiceProvider = (*DaemonProvider)(nil)

// Verify remoteSettingsService implements SettingsService at compile time.
var _ SettingsService = (*remoteSettingsService)(nil)

// DaemonProvider is the TUI-side provider for a remote daemon connection.
type DaemonProvider struct {
	logic    logic.Client
	settings *remoteSettingsService
}

func NewDaemonProvider(client logic.Client) *DaemonProvider {
	p := &DaemonProvider{logic: client}
	if settingsClient, ok := client.(SettingsClient); ok {
		p.settings = newRemoteSettingsService(settingsClient)
	} else {
		p.settings = newRemoteSettingsService(nil)
	}
	return p
}
func (p *DaemonProvider) Logic() logic.Client { return p.logic }
func (p *DaemonProvider) EventClient() EventStreamClient {
	if client, ok := p.logic.(EventStreamClient); ok {
		return client
	}
	return nil
}
func (p *DaemonProvider) LogClient() SessionLogClient {
	if client, ok := p.logic.(SessionLogClient); ok {
		return client
	}
	return nil
}
func (p *DaemonProvider) AutonomousClient() AutonomousClient {
	if client, ok := p.logic.(AutonomousClient); ok {
		return client
	}
	return nil
}
func (p *DaemonProvider) WorkspaceClient() WorkspaceClient {
	if client, ok := p.logic.(WorkspaceClient); ok {
		return client
	}
	return nil
}
func (p *DaemonProvider) GetServices() *Services                                   { return &Services{Logic: p.logic} }
func (p *DaemonProvider) Close(context.Context)                                    {}
func (p *DaemonProvider) Session() *service.SessionService                         { return nil }
func (p *DaemonProvider) Plan() *service.PlanService                               { return nil }
func (p *DaemonProvider) Task() *service.AgentSessionService                       { return nil }
func (p *DaemonProvider) Continuation() *service.AgentSessionContinuationService   { return nil }
func (p *DaemonProvider) Question() *service.QuestionService                       { return nil }
func (p *DaemonProvider) Instance() *service.InstanceService                       { return nil }
func (p *DaemonProvider) Workspace() *service.WorkspaceService                     { return nil }
func (p *DaemonProvider) Review() *service.ReviewService                           { return nil }
func (p *DaemonProvider) Events() *service.EventService                            { return nil }
func (p *DaemonProvider) GithubPRs() *service.GithubPRService                      { return nil }
func (p *DaemonProvider) GitlabMRs() *service.GitlabMRService                      { return nil }
func (p *DaemonProvider) SessionArtifacts() *service.SessionReviewArtifactService  { return nil }
func (p *DaemonProvider) GithubPRReviews() *service.GithubPRReviewService          { return nil }
func (p *DaemonProvider) GitlabMRReviews() *service.GitlabMRReviewService          { return nil }
func (p *DaemonProvider) GithubPRChecks() *service.GithubPRCheckService            { return nil }
func (p *DaemonProvider) GitlabMRChecks() *service.GitlabMRCheckService            { return nil }
func (p *DaemonProvider) Settings() SettingsService                                { return p.settings }
func (p *DaemonProvider) NewSessionFilters() *service.SessionFilterService         { return nil }
func (p *DaemonProvider) NewSessionFilterLocks() *service.SessionFilterLockService { return nil }
func (p *DaemonProvider) Planning() *orchestrator.PlanningService                  { return nil }
func (p *DaemonProvider) Implementation() *orchestrator.ImplementationService      { return nil }
func (p *DaemonProvider) ReviewPipeline() *orchestrator.ReviewPipeline             { return nil }
func (p *DaemonProvider) AnswerRouter() orchestrator.AnswerRouter                  { return nil }
func (p *DaemonProvider) ReviewFollowup() *orchestrator.ReviewFollowup             { return nil }
func (p *DaemonProvider) SessionRegistry() orchestrator.SessionRegistry            { return nil }
func (p *DaemonProvider) Manual() *orchestrator.ManualSessionService               { return nil }
func (p *DaemonProvider) Bus() *event.Bus                                          { return nil }
func (p *DaemonProvider) GitClient() *gitwork.Client                               { return nil }
func (p *DaemonProvider) Adapters() []adapter.WorkItemAdapter                      { return nil }
func (p *DaemonProvider) RepoSources() []adapter.RepoSource                        { return nil }
func (p *DaemonProvider) Harnesses() app.AgentHarnesses                            { return app.AgentHarnesses{} }
func (p *DaemonProvider) ReviewComments() *adapter.ReviewCommentDispatcher         { return nil }
func (p *DaemonProvider) StartupWarnings() []string                                { return nil }

type remoteSettingsService struct {
	client SettingsClient
	mu     sync.RWMutex
	snap   SettingsSnapshot
}

func newRemoteSettingsService(client SettingsClient) *remoteSettingsService {
	svc := &remoteSettingsService{client: client}
	svc.snap = SettingsSnapshot{
		Sections:         []SettingsSection{{ID: "daemon", Title: "Daemon", Description: "Connected daemon settings are loaded from the selected daemon.", Fields: nil}},
		Providers:        map[string]ProviderStatus{},
		DiagnosticsState: SettingsDiagnosticsPending,
	}
	return svc
}

func (s *remoteSettingsService) Snapshot() SettingsSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snap.defensiveCopy()
}

func (s *remoteSettingsService) RefreshConfigOnly(ctx context.Context, _ *config.Config) error {
	return s.refresh(ctx)
}

func (s *remoteSettingsService) RefreshWithDiagnostics(ctx context.Context, _ *config.Config) error {
	if s.client == nil {
		return errors.New("daemon settings client is unavailable")
	}
	s.mu.RLock()
	raw := s.snap.RawYAML
	s.mu.RUnlock()
	res, err := s.client.RefreshProviderDiagnostics(ctx, raw)
	if err != nil {
		return err
	}
	var cfg config.Config
	if err := yaml.Unmarshal([]byte(res.RawYAML), &cfg); err != nil {
		return err
	}
	snapshot := SettingsSnapshot{
		Sections:         buildSettingsSectionsConfigOnly(&cfg),
		Providers:        remoteProviderStatuses(res.Providers),
		RawYAML:          res.RawYAML,
		HarnessWarning:   res.HarnessWarning,
		DiagnosticsState: SettingsDiagnosticsReady,
	}
	s.mu.Lock()
	s.snap = snapshot
	s.mu.Unlock()
	return nil
}

func (s *remoteSettingsService) refresh(ctx context.Context) error {
	if s.client == nil {
		return errors.New("daemon settings client is unavailable")
	}
	res, err := s.client.GetSettings(ctx)
	if err != nil {
		return err
	}
	var cfg config.Config
	if err := yaml.Unmarshal([]byte(res.RawYAML), &cfg); err != nil {
		return err
	}
	snapshot := SettingsSnapshot{
		Sections:         buildSettingsSectionsConfigOnly(&cfg),
		Providers:        buildProviderStatuses(&cfg),
		RawYAML:          res.RawYAML,
		DiagnosticsState: SettingsDiagnosticsReady,
	}
	s.mu.Lock()
	s.snap = snapshot
	s.mu.Unlock()
	return nil
}

func (s *remoteSettingsService) Save(ctx context.Context, sections []SettingsSection, current Services) (SettingsApplyResult, error) {
	if s.client == nil {
		return SettingsApplyResult{}, errors.New("daemon settings client is unavailable")
	}
	previous := current.Cfg
	if previous == nil {
		s.mu.RLock()
		raw := s.snap.RawYAML
		s.mu.RUnlock()
		if strings.TrimSpace(raw) != "" {
			var cfg config.Config
			if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
				return SettingsApplyResult{}, err
			}
			previous = &cfg
		}
	}
	cfg, err := configFromSectionsWithPrevious(sections, previous)
	if err != nil {
		return SettingsApplyResult{}, err
	}
	if err := validateSettingsConfig(cfg); err != nil {
		return SettingsApplyResult{}, err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return SettingsApplyResult{}, err
	}
	res, err := s.client.SaveSettings(ctx, string(data), domain.NewID())
	if err != nil {
		return SettingsApplyResult{}, err
	}
	if err := s.refresh(ctx); err != nil {
		return SettingsApplyResult{}, err
	}
	// The DaemonProvider's GetServices() returns a struct populated only with
	// the Logic client; everything else (workspace identity, runtime fields,
	// instance ID, sessions dir) lives on the App's runtime context. Preserve
	// those fields from the caller's `current` so applyServicesReload does
	// not blank out the active workspace after a settings save over the
	// daemon boundary.
	preserved := Services{
		Logic:         current.Logic,
		InstanceID:    current.InstanceID,
		WorkspaceID:   current.WorkspaceID,
		WorkspaceDir:  current.WorkspaceDir,
		WorkspaceName: current.WorkspaceName,
		Settings:      current.Settings,
	}
	cfgPath, err := config.ConfigPath()
	if err != nil {
		return SettingsApplyResult{}, err
	}
	sessionsDir, err := config.SessionsDir()
	if err != nil {
		return SettingsApplyResult{}, err
	}
	return SettingsApplyResult{Services: viewsServicesReload{Services: preserved, Cfg: cfg, ConfigPath: cfgPath, SessionsDir: sessionsDir}, Message: firstNonEmptyString(res.Message, "Settings saved")}, nil
}

func (s *remoteSettingsService) TestProvider(ctx context.Context, provider string, sections []SettingsSection) (ProviderStatus, error) {
	if s.client == nil {
		return ProviderStatus{}, errors.New("daemon settings client is unavailable")
	}
	raw, err := rawYAMLFromSettingsSections(sections)
	if err != nil {
		return ProviderStatus{}, err
	}
	status, err := s.client.TestProvider(ctx, provider, raw)
	if status == nil {
		return ProviderStatus{}, err
	}
	return remoteProviderStatus(*status), err
}

func (s *remoteSettingsService) LoginProvider(ctx context.Context, provider, harness string, sections []SettingsSection, svcs Services) (SettingsLoginResult, error) {
	if s.client == nil {
		return SettingsLoginResult{}, errors.New("daemon settings client is unavailable")
	}
	raw, err := rawYAMLFromSettingsSections(sections)
	if err != nil {
		return SettingsLoginResult{}, err
	}
	res, err := s.client.LoginProvider(ctx, provider, harness, raw)
	if err != nil {
		return SettingsLoginResult{}, err
	}
	if strings.TrimSpace(res.RawYAML) != "" {
		var cfg config.Config
		if err := yaml.Unmarshal([]byte(res.RawYAML), &cfg); err != nil {
			return SettingsLoginResult{}, err
		}
		s.mu.Lock()
		s.snap = SettingsSnapshot{
			Sections:         buildSettingsSectionsConfigOnly(&cfg),
			Providers:        buildProviderStatuses(&cfg),
			RawYAML:          res.RawYAML,
			DiagnosticsState: SettingsDiagnosticsReady,
		}
		s.mu.Unlock()
	}
	return SettingsLoginResult{Message: res.Message, Dirty: res.Dirty}, nil
}

func (s *remoteSettingsService) RefreshLoginSnapshot(ctx context.Context, sections []SettingsSection) error {
	return s.refresh(ctx)
}

func (s *remoteSettingsService) RefreshLoginSnapshotFromConfig(ctx context.Context, cfg *config.Config) error {
	return s.refresh(ctx)
}

func (s *remoteSettingsService) SetDiagnosticsState(state SettingsDiagnosticsState) {
	s.mu.Lock()
	s.snap.DiagnosticsState = state
	s.mu.Unlock()
}

func rawYAMLFromSettingsSections(sections []SettingsSection) (string, error) {
	cfg, err := configFromSections(sections)
	if err != nil {
		return "", err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func remoteProviderStatuses(statuses map[string]daemonapi.ProviderStatus) map[string]ProviderStatus {
	out := make(map[string]ProviderStatus, len(statuses))
	for key, status := range statuses {
		out[key] = remoteProviderStatus(status)
	}
	return out
}

func remoteProviderStatus(status daemonapi.ProviderStatus) ProviderStatus {
	return ProviderStatus{
		Title:       status.Title,
		Configured:  status.Configured,
		Connected:   status.Connected,
		AuthSource:  status.AuthSource,
		Description: status.Description,
		LastError:   status.LastError,
	}
}
