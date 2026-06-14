package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	githubadapter "github.com/beeemT/substrate/internal/adapter/github"
	gitlabadapter "github.com/beeemT/substrate/internal/adapter/gitlab"
	linearadapter "github.com/beeemT/substrate/internal/adapter/linear"
	sentryadapter "github.com/beeemT/substrate/internal/adapter/sentry"
	"github.com/beeemT/substrate/internal/app"
	"github.com/beeemT/substrate/internal/buildinfo"
	"github.com/beeemT/substrate/internal/config"
	daemonapi "github.com/beeemT/substrate/internal/daemon/api"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/gitwork"
	"github.com/beeemT/substrate/internal/logic"
	"github.com/beeemT/substrate/internal/logic/readmodel"
	"github.com/beeemT/substrate/internal/service"
	"github.com/beeemT/substrate/internal/sessionlog"
	"github.com/beeemT/substrate/internal/tuilog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"gopkg.in/yaml.v3"
)

// Verify API implements the manually registered gRPC services.
var _ interface {
	Health(context.Context, *daemonapi.HealthRequest) (*daemonapi.HealthResponse, error)
	GetInitialSnapshot(context.Context, *daemonapi.GetInitialSnapshotRequest) (*daemonapi.GetInitialSnapshotResponse, error)
	Subscribe(*daemonapi.SubscribeEventsRequest, grpc.ServerStreamingServer[daemonapi.EventBatch]) error
} = (*API)(nil)

type Options struct {
	WorkspaceID       string
	WorkspaceName     string
	WorkspaceDir      string
	InstanceID        string
	Token             string
	Config            *config.Config
	SecretStore       config.SecretStore
	SessionsDir       string
	Harnesses         app.AgentHarnesses
	LogStore          *tuilog.Store
	Autonomous        *AutonomousState
	AutonomousCtl     *AutonomousController
	WorkItems         *service.SessionService
	Workspaces        *service.WorkspaceService
	NewSessionFilters *service.SessionFilterService
	NewSessionLocks   *service.SessionFilterLockService
	WorkItemAdapters  []adapter.WorkItemAdapter
	SettingsReloader  func(context.Context, *config.Config) (*logic.Services, error)
	ShutdownFunc      func()
	Logic             logic.Client
	Events            *service.EventService
	Bus               *event.Bus
	StartedAt         time.Time
}

type API struct {
	opts Options
	// deps is the dynamic service-graph snapshot rebuilt by swapServices.
	// Handlers MUST read the logic client, event bus, services, harnesses,
	// and log store through currentDeps() instead of reaching into opts so
	// they observe a consistent view that survives a settings rebuild.
	deps     atomic.Pointer[apiDeps]
	mu       sync.Mutex
	seen     map[string]any
	inflight sync.Map // map[action+":"+idempotencyKey]*inFlightCall
}

// inFlightCall deduplicates concurrent calls sharing the same idempotency
// key. The first caller stores its result and closes done; followers wait
// for that close and replay the same payload/error so they do not race the
// underlying mutating call.
type inFlightCall struct {
	done chan struct{}
	val  any
	err  error
}

// apiDeps holds the dynamic service-graph fields swapped by swapServices.
// The static fields on Options (WorkspaceID, WorkspaceDir, SessionsDir,
// SecretStore, Config, Token, SettingsReloader, ShutdownFunc, StartedAt,
// Autonomous state, AutonomousCtl handle) are still read directly from opts
// because they are immutable or already guarded by mu.
type apiDeps struct {
	Logic             logic.Client
	Bus               *event.Bus
	Events            *service.EventService
	WorkItems         *service.SessionService
	Workspaces        *service.WorkspaceService
	NewSessionFilters *service.SessionFilterService
	NewSessionLocks   *service.SessionFilterLockService
	WorkItemAdapters  []adapter.WorkItemAdapter
	Harnesses         app.AgentHarnesses
	LogStore          *tuilog.Store
}

// currentDeps returns a copy of the latest service-graph snapshot. The pointer
// load is atomic, so callers observe a consistent view even while
// swapServices is rewriting it.
func (a *API) currentDeps() apiDeps {
	if d := a.deps.Load(); d != nil {
		return *d
	}
	return apiDeps{}
}

// Accessors for the dynamic service-graph fields. Handlers MUST use these
// instead of reading a.opts.X directly so they observe the latest snapshot
// published by swapServices.
func (a *API) logicClient() logic.Client           { return a.currentDeps().Logic }
func (a *API) eventBus() *event.Bus                { return a.currentDeps().Bus }
func (a *API) eventService() *service.EventService { return a.currentDeps().Events }
func (a *API) harnesses() app.AgentHarnesses       { return a.currentDeps().Harnesses }
func (a *API) logStore() *tuilog.Store             { return a.currentDeps().LogStore }
func (a *API) workspaceSvc() *service.WorkspaceService {
	return a.currentDeps().Workspaces
}

func NewAPI(opts Options) *API {
	if opts.StartedAt.IsZero() {
		opts.StartedAt = time.Now()
	}
	if opts.Autonomous == nil {
		opts.Autonomous = NewAutonomousState(time.Now)
	}
	api := &API{opts: opts, seen: map[string]any{}}
	api.deps.Store(&apiDeps{
		Logic:             opts.Logic,
		Bus:               opts.Bus,
		Events:            opts.Events,
		WorkItems:         opts.WorkItems,
		Workspaces:        opts.Workspaces,
		NewSessionFilters: opts.NewSessionFilters,
		NewSessionLocks:   opts.NewSessionLocks,
		WorkItemAdapters:  opts.WorkItemAdapters,
		Harnesses:         opts.Harnesses,
		LogStore:          opts.LogStore,
	})
	if api.opts.AutonomousCtl == nil && api.opts.NewSessionFilters != nil && api.opts.NewSessionLocks != nil && api.opts.WorkItems != nil {
		api.opts.AutonomousCtl = NewAutonomousController(
			api.opts.WorkspaceID,
			api.opts.NewSessionFilters,
			api.opts.NewSessionLocks,
			api.opts.WorkItems,
			api.opts.WorkItemAdapters,
			api.opts.Autonomous,
			&eventBusPublisher{api: api},
		)
	}
	return api
}

func NewGRPCServer(api *API) *grpc.Server {
	server := grpc.NewServer(
		grpc.UnaryInterceptor(api.unaryAuthInterceptor),
		grpc.StreamInterceptor(api.streamAuthInterceptor),
	)
	RegisterSystemAPI(server, api)
	RegisterSessionAPI(server, api)
	RegisterAgentSessionAPI(server, api)
	RegisterReadModelAPI(server, api)
	RegisterAutonomousModeAPI(server, api)
	RegisterEventStreamAPI(server, api)
	RegisterLogAPI(server, api)
	RegisterWorkspaceAPI(server, api)
	RegisterSettingsAPI(server, api)
	return server
}

func ListenUnix(path string) (net.Listener, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("socket path is required")
	}
	return net.Listen("unix", path)
}

func (a *API) Health(ctx context.Context, _ *daemonapi.HealthRequest) (*daemonapi.HealthResponse, error) {
	return &daemonapi.HealthResponse{
		Ready:         true,
		Version:       buildinfo.Version,
		BuildSHA:      buildinfo.BuildSHA,
		WorkspaceID:   a.opts.WorkspaceID,
		UptimeSeconds: int64(time.Since(a.opts.StartedAt).Seconds()),
	}, nil
}

func (a *API) GetRuntimeContext(ctx context.Context, _ *daemonapi.GetRuntimeContextRequest) (*daemonapi.RuntimeContext, error) {
	return &daemonapi.RuntimeContext{
		WorkspaceID:   a.opts.WorkspaceID,
		WorkspaceName: a.opts.WorkspaceName,
		WorkspaceDir:  a.opts.WorkspaceDir,
		InstanceID:    a.opts.InstanceID,
	}, nil
}

func (a *API) InitializeWorkspace(ctx context.Context, req *daemonapi.InitializeWorkspaceRequest) (*daemonapi.Workspace, error) {
	if a.workspaceSvc() == nil {
		return nil, status.Error(codes.FailedPrecondition, "workspace service is unavailable")
	}
	dir := strings.TrimSpace(req.Dir)
	if dir == "" {
		dir = a.opts.WorkspaceDir
	}
	if dir == "" {
		return nil, status.Error(codes.InvalidArgument, "workspace directory is required")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = filepath.Base(dir)
	}
	wsFile, err := gitwork.InitWorkspace(dir, name)
	createdWorkspaceFile := err == nil
	if err != nil {
		if !gitwork.IsWorkspaceExists(err) {
			return nil, normalizeError(err)
		}
		wsFile, err = gitwork.ReadWorkspaceFile(dir)
		if err != nil {
			return nil, normalizeError(err)
		}
		if _, getErr := a.workspaceSvc().Get(ctx, wsFile.ID); getErr == nil {
			return &daemonapi.Workspace{ID: wsFile.ID, Name: wsFile.Name, Dir: dir}, nil
		}
	}
	ws := domain.Workspace{
		ID:       wsFile.ID,
		Name:     wsFile.Name,
		RootPath: dir,
	}
	if err := a.workspaceSvc().Create(ctx, ws); err != nil {
		if createdWorkspaceFile {
			if removeErr := os.Remove(filepath.Join(dir, gitwork.WorkspaceFileName)); removeErr != nil {
				slog.Warn("failed to remove workspace file on rollback", "error", removeErr)
			}
		}
		return nil, normalizeError(err)
	}
	if err := a.workspaceSvc().MarkReady(ctx, ws.ID); err != nil {
		slog.Warn("failed to mark workspace ready", "workspace_id", ws.ID, "error", err)
	}
	return &daemonapi.Workspace{ID: wsFile.ID, Name: wsFile.Name, Dir: dir}, nil
}

func (a *API) HealthCheckWorkspace(ctx context.Context, req *daemonapi.HealthCheckWorkspaceRequest) (*daemonapi.WorkspaceHealth, error) {
	dir := strings.TrimSpace(req.Dir)
	if dir == "" {
		dir = a.opts.WorkspaceDir
	}
	if dir == "" {
		return nil, status.Error(codes.InvalidArgument, "workspace directory is required")
	}
	if _, err := gitwork.ScanWorkspace(dir); err != nil {
		return &daemonapi.WorkspaceHealth{OK: false, Message: err.Error()}, nil
	}
	return &daemonapi.WorkspaceHealth{OK: true}, nil
}

func (a *API) ListManagedRepos(ctx context.Context, req *daemonapi.ListManagedReposRequest) (*daemonapi.ListManagedReposResponse, error) {
	dir := strings.TrimSpace(req.WorkspaceDir)
	if dir == "" {
		dir = a.opts.WorkspaceDir
	}
	if dir == "" {
		return nil, status.Error(codes.InvalidArgument, "workspace directory is required")
	}
	scan, err := gitwork.ScanWorkspace(dir)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "scan workspace: %v", err)
	}
	type repo struct {
		Path string `json:"path"`
		Name string `json:"name"`
		Kind string `json:"kind"`
	}
	repos := make([]string, 0, len(scan.GitWorkRepos)+len(scan.PlainGitRepos))
	for _, path := range scan.GitWorkRepos {
		encoded, err := json.Marshal(repo{Path: path, Name: filepath.Base(path), Kind: "git-work"})
		if err != nil {
			return nil, status.Errorf(codes.Internal, "encode managed repo: %v", err)
		}
		repos = append(repos, string(encoded))
	}
	for _, path := range scan.PlainGitRepos {
		encoded, err := json.Marshal(repo{Path: path, Name: filepath.Base(path), Kind: "plain-git"})
		if err != nil {
			return nil, status.Errorf(codes.Internal, "encode managed repo: %v", err)
		}
		repos = append(repos, string(encoded))
	}
	return &daemonapi.ListManagedReposResponse{ReposJSON: repos}, nil
}

func (a *API) ListWorktrees(ctx context.Context, req *daemonapi.ListWorktreesRequest) (*daemonapi.ListWorktreesResponse, error) {
	if strings.TrimSpace(req.RepoPath) == "" {
		return nil, status.Error(codes.InvalidArgument, "repo path is required")
	}
	worktrees, err := gitwork.NewClient("").List(ctx, req.RepoPath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list worktrees: %v", err)
	}
	encoded := make([]string, 0, len(worktrees))
	for _, wt := range worktrees {
		data, err := json.Marshal(wt)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "encode worktree: %v", err)
		}
		encoded = append(encoded, string(data))
	}
	return &daemonapi.ListWorktreesResponse{WorktreesJSON: encoded}, nil
}

func (a *API) CloneRepo(ctx context.Context, req *daemonapi.CloneRepoRequest) (*daemonapi.CloneRepoResponse, error) {
	if cached, ok := a.cachedResponse("clone_repo", req.IdempotencyKey); ok {
		return cached.(*daemonapi.CloneRepoResponse), nil
	}
	path, err := gitwork.NewClient("").Clone(ctx, req.CloneDir, req.CloneURL)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "clone repo: %v", err)
	}
	res := &daemonapi.CloneRepoResponse{RepoPath: path}
	a.storeResponse("clone_repo", req.IdempotencyKey, res)
	return res, nil
}

func (a *API) InitRepo(ctx context.Context, req *daemonapi.InitRepoRequest) (*daemonapi.InitRepoResponse, error) {
	if cached, ok := a.cachedResponse("init_repo", req.IdempotencyKey); ok {
		return cached.(*daemonapi.InitRepoResponse), nil
	}
	if err := gitwork.NewClient("").Init(ctx, req.RepoPath); err != nil {
		slog.Error("failed to initialize git-work repo", "path", req.RepoPath, "error", err)
		return nil, status.Errorf(codes.Internal, "init repo: %v", err)
	}
	res := &daemonapi.InitRepoResponse{RepoPath: req.RepoPath}
	a.storeResponse("init_repo", req.IdempotencyKey, res)
	return res, nil
}

func (a *API) RemoveRepo(ctx context.Context, req *daemonapi.RemoveRepoRequest) (*daemonapi.RemoveRepoResponse, error) {
	if cached, ok := a.cachedResponse("remove_repo", req.IdempotencyKey); ok {
		return cached.(*daemonapi.RemoveRepoResponse), nil
	}
	target, err := a.validateRepoRemovalPath(req.RepoPath)
	if err != nil {
		return nil, err
	}
	if err := os.RemoveAll(target); err != nil {
		slog.Error("failed to remove repository", "path", req.RepoPath, "error", err)
		return nil, status.Errorf(codes.Internal, "remove repo: %v", err)
	}
	res := &daemonapi.RemoveRepoResponse{}
	a.storeResponse("remove_repo", req.IdempotencyKey, res)
	return res, nil
}

func (a *API) validateRepoRemovalPath(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", status.Error(codes.InvalidArgument, "repo path is required")
	}
	workspaceDir := strings.TrimSpace(a.opts.WorkspaceDir)
	if workspaceDir == "" {
		return "", status.Error(codes.FailedPrecondition, "workspace directory is unavailable")
	}
	workspace, err := filepath.EvalSymlinks(workspaceDir)
	if err != nil {
		return "", status.Errorf(codes.Internal, "resolve workspace directory: %v", err)
	}
	target, err := filepath.EvalSymlinks(trimmed)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", status.Errorf(codes.NotFound, "repo path not found")
		}
		return "", status.Errorf(codes.InvalidArgument, "resolve repo path: %v", err)
	}
	rel, err := filepath.Rel(workspace, target)
	if err != nil {
		return "", status.Errorf(codes.InvalidArgument, "compare repo path: %v", err)
	}
	if rel == "." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." || filepath.IsAbs(rel) {
		return "", status.Error(codes.InvalidArgument, "repo path must be inside the workspace")
	}
	return target, nil
}

func (a *API) GetSettings(ctx context.Context, _ *daemonapi.GetSettingsRequest) (*daemonapi.GetSettingsResponse, error) {
	a.mu.Lock()
	cfg := a.opts.Config
	a.mu.Unlock()
	if cfg == nil {
		return nil, status.Error(codes.FailedPrecondition, "daemon config is unavailable")
	}
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal settings: %v", err)
	}
	daemons := make([]string, 0, len(cfg.TUI.Daemons))
	for name, entry := range cfg.TUI.Daemons {
		payload := struct {
			Name        string `json:"name"`
			Label       string `json:"label"`
			Kind        string `json:"kind"`
			Address     string `json:"address"`
			TokenRef    string `json:"token_ref"`
			AutoManaged bool   `json:"auto_managed"`
		}{
			Name:        name,
			Label:       entry.Label,
			Kind:        entry.Kind,
			Address:     entry.Address,
			TokenRef:    entry.TokenRef,
			AutoManaged: entry.AutoManaged,
		}
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "encode daemon registry entry: %v", err)
		}
		daemons = append(daemons, string(data))
	}
	return &daemonapi.GetSettingsResponse{RawYAML: string(raw), ActiveDaemon: cfg.TUI.ActiveDaemon, DaemonsJSON: daemons}, nil
}

func (a *API) SaveSettings(ctx context.Context, req *daemonapi.SaveSettingsRequest) (*daemonapi.SaveSettingsResponse, error) {
	if cached, ok := a.cachedResponse("save_settings", req.IdempotencyKey); ok {
		return cached.(*daemonapi.SaveSettingsResponse), nil
	}
	var next config.Config
	if err := yaml.Unmarshal([]byte(req.RawYAML), &next); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "parse settings yaml: %v", err)
	}
	path, err := config.ConfigPath()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "resolve config path: %v", err)
	}
	if err := config.Save(path, &next); err != nil {
		return nil, status.Errorf(codes.Internal, "save settings: %v", err)
	}
	if a.opts.SettingsReloader != nil {
		svcs, err := a.opts.SettingsReloader(ctx, &next)
		if err != nil {
			slog.Error("failed to rebuild daemon settings", "error", err)
			return nil, status.Errorf(codes.Internal, "rebuild settings: %v", err)
		}
		a.swapServices(svcs)
	}
	a.mu.Lock()
	a.opts.Config = &next
	a.mu.Unlock()
	res := &daemonapi.SaveSettingsResponse{Message: "Settings saved"}
	a.storeResponse("save_settings", req.IdempotencyKey, res)
	return res, nil
}

// swapServices points the API at a freshly built service graph so subsequent
// RPC calls reach the new dependencies instead of the stale ones captured at
// construction time. A nil svcs is a no-op so callers that only need the
// rebuild side-effect (e.g. tests) can opt out of the swap.
//
// The dynamic fields are published through an atomic snapshot so concurrent
// handlers always observe a consistent view of the service graph. The
// autonomous controller's service pointers are refreshed in place so
// long-running watch streams pick up the new dependencies without restarting.
func (a *API) swapServices(svcs *logic.Services) {
	if svcs == nil {
		return
	}
	a.deps.Store(&apiDeps{
		Logic:             svcs.Logic,
		Bus:               svcs.Bus,
		Events:            svcs.Events,
		WorkItems:         svcs.Session,
		Workspaces:        svcs.Workspace,
		NewSessionFilters: svcs.NewSessionFilters,
		NewSessionLocks:   svcs.NewSessionFilterLocks,
		WorkItemAdapters:  svcs.Adapters,
		Harnesses:         svcs.Harnesses,
		LogStore:          svcs.LogStore,
	})
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.opts.AutonomousCtl != nil {
		a.opts.AutonomousCtl.UpdateDependencies(svcs.NewSessionFilters, svcs.NewSessionFilterLocks, svcs.Session, svcs.Adapters)
	}
}

func (a *API) TestProvider(ctx context.Context, req *daemonapi.TestProviderRequest) (*daemonapi.ProviderStatus, error) {
	cfg, err := a.settingsConfigFromRaw(req.RawYAML)
	if err != nil {
		return nil, err
	}
	status, err := testProviderConnection(ctx, cfg, req.Provider)
	if err != nil {
		return &status, err
	}
	return &status, nil
}

func (a *API) LoginProvider(ctx context.Context, req *daemonapi.LoginProviderRequest) (*daemonapi.LoginProviderResponse, error) {
	cfg, err := a.settingsConfigFromRaw(req.RawYAML)
	if err != nil {
		return nil, err
	}
	runner := a.harnessActionRunner(req.Harness)
	if runner == nil {
		return nil, status.Errorf(codes.FailedPrecondition, "harness %q does not support login actions", req.Harness)
	}
	result, err := config.RunHarnessAction(ctx, runner, adapter.HarnessActionRequest{
		Action:      "login_provider",
		Provider:    req.Provider,
		HarnessName: req.Harness,
		Inputs:      providerLoginInputs(cfg, req.Provider),
	})
	if err != nil {
		return nil, normalizeError(err)
	}
	if !result.Success {
		return nil, status.Error(codes.FailedPrecondition, result.Message)
	}
	dirty := false
	message := strings.TrimSpace(result.Message)
	switch req.Provider {
	case "github":
		token := strings.TrimSpace(result.Credentials["token"])
		if token == "" {
			return nil, status.Error(codes.FailedPrecondition, "github login did not return a token")
		}
		cfg.Adapters.GitHub.Token = token
		cfg.Adapters.GitHub.TokenRef = "keychain:github.token"
		dirty = true
		if message == "" {
			message = "github login complete"
		}
	case "sentry":
		if message == "" {
			message = "sentry login complete"
		}
	default:
		return nil, status.Errorf(codes.InvalidArgument, "login not implemented for provider %q", req.Provider)
	}
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal login settings: %v", err)
	}
	return &daemonapi.LoginProviderResponse{Message: message, Dirty: dirty, RawYAML: string(raw)}, nil
}

func (a *API) RefreshProviderDiagnostics(ctx context.Context, req *daemonapi.RefreshProviderDiagnosticsRequest) (*daemonapi.RefreshProviderDiagnosticsResponse, error) {
	cfg, err := a.settingsConfigFromRaw(req.RawYAML)
	if err != nil {
		return nil, err
	}
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal settings: %v", err)
	}
	diagnostics := app.DiagnoseHarnesses(cfg, a.opts.WorkspaceDir)
	return &daemonapi.RefreshProviderDiagnosticsResponse{
		RawYAML:        string(raw),
		HarnessWarning: diagnostics.WarningSummary(),
		Providers:      providerStatuses(cfg),
	}, nil
}

func (a *API) Info(ctx context.Context, _ *daemonapi.InfoRequest) (*daemonapi.InfoResponse, error) {
	return &daemonapi.InfoResponse{Version: buildinfo.Version, BuildSHA: buildinfo.BuildSHA, WorkspaceID: a.opts.WorkspaceID}, nil
}

func (a *API) Disconnect(ctx context.Context, _ *daemonapi.DisconnectRequest) (*daemonapi.DisconnectResponse, error) {
	return &daemonapi.DisconnectResponse{}, nil
}

func (a *API) Shutdown(ctx context.Context, _ *daemonapi.ShutdownRequest) (*daemonapi.ShutdownResponse, error) {
	if a.opts.ShutdownFunc != nil {
		a.opts.ShutdownFunc()
	}
	return &daemonapi.ShutdownResponse{}, nil
}

func (a *API) GetAccessToken(ctx context.Context, _ *daemonapi.GetAccessTokenRequest) (*daemonapi.GetAccessTokenResponse, error) {
	a.mu.Lock()
	token := a.opts.Token
	a.mu.Unlock()
	if strings.TrimSpace(token) == "" {
		return nil, status.Error(codes.FailedPrecondition, "daemon access token is not configured")
	}
	return &daemonapi.GetAccessTokenResponse{Token: token}, nil
}

func (a *API) RotateAccessToken(ctx context.Context, _ *daemonapi.RotateAccessTokenRequest) (*daemonapi.RotateAccessTokenResponse, error) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, status.Errorf(codes.Internal, "generate daemon access token: %v", err)
	}
	token := hex.EncodeToString(tokenBytes)
	store := a.opts.SecretStore
	if store == nil {
		store = config.OSKeychainStore{}
	}
	if err := config.SaveDaemonAccessToken(a.opts.Config, store, "local", token); err != nil {
		return nil, status.Errorf(codes.Internal, "save daemon access token: %v", err)
	}
	a.mu.Lock()
	a.opts.Token = token
	a.mu.Unlock()
	return &daemonapi.RotateAccessTokenResponse{Token: token}, nil
}

func (a *API) GetInitialSnapshot(ctx context.Context, req *daemonapi.GetInitialSnapshotRequest) (*daemonapi.GetInitialSnapshotResponse, error) {
	if a.logicClient() == nil {
		return nil, status.Error(codes.FailedPrecondition, "logic client is unavailable")
	}
	snapshot, err := a.logicClient().GetInitialSnapshot(ctx, req.WorkspaceID)
	if err != nil {
		return nil, err
	}
	return &daemonapi.GetInitialSnapshotResponse{Snapshot: snapshot}, nil
}

func (a *API) ListAgentSessions(ctx context.Context, req *daemonapi.ListAgentSessionsRequest) (*daemonapi.ListAgentSessionsResponse, error) {
	if a.logicClient() == nil {
		return nil, status.Error(codes.FailedPrecondition, "logic client is unavailable")
	}
	snapshot, err := a.logicClient().GetInitialSnapshot(ctx, req.WorkspaceID)
	if err != nil {
		return nil, normalizeError(err)
	}
	items := make([]domain.AgentSession, 0, len(snapshot.AgentSessions))
	for _, agentSession := range snapshot.AgentSessions {
		if req.WorkItemID != "" && agentSession.WorkItemID != req.WorkItemID {
			continue
		}
		items = append(items, agentSession)
	}
	return &daemonapi.ListAgentSessionsResponse{AgentSessions: items}, nil
}

func (a *API) SearchHistory(ctx context.Context, req *daemonapi.SearchHistoryRequest) (*daemonapi.SearchHistoryResponse, error) {
	if a.logicClient() == nil {
		return nil, status.Error(codes.FailedPrecondition, "logic client is unavailable")
	}
	entries, err := a.logicClient().SearchSessionHistory(ctx, req.Filter)
	if err != nil {
		return nil, normalizeError(err)
	}
	return &daemonapi.SearchHistoryResponse{Entries: entries}, nil
}

func (a *API) GetInteraction(ctx context.Context, req *daemonapi.GetInteractionRequest) (*daemonapi.GetInteractionResponse, error) {
	if strings.TrimSpace(a.opts.SessionsDir) == "" {
		return nil, status.Error(codes.FailedPrecondition, "session log directory is unavailable")
	}
	entries, _, err := sessionlog.LoadInteractionEntries(a.opts.SessionsDir, req.AgentSessionID, true)
	if err != nil {
		return nil, normalizeError(err)
	}
	return &daemonapi.GetInteractionResponse{Entries: entries}, nil
}
func (a *API) ListSessions(ctx context.Context, req *daemonapi.ListSessionsRequest) (*daemonapi.ListSessionsResponse, error) {
	if a.logicClient() == nil {
		return nil, status.Error(codes.FailedPrecondition, "logic client is unavailable")
	}
	sessions, err := a.logicClient().ListSessions(ctx, req.WorkspaceID)
	if err != nil {
		return nil, err
	}
	return &daemonapi.ListSessionsResponse{Sessions: sessions}, nil
}

func (a *API) GetSessionOverview(ctx context.Context, req *daemonapi.GetSessionOverviewRequest) (*daemonapi.GetSessionOverviewResponse, error) {
	snapshot, err := a.readModelSnapshot(ctx, req.WorkspaceID)
	if err != nil {
		return nil, err
	}
	overview := readmodel.New().SessionOverview(snapshot, req.SessionID)
	return &daemonapi.GetSessionOverviewResponse{Overview: overview}, nil
}

func (a *API) GetSidebar(ctx context.Context, req *daemonapi.GetSidebarRequest) (*daemonapi.GetSidebarResponse, error) {
	snapshot, err := a.readModelSnapshot(ctx, req.WorkspaceID)
	if err != nil {
		return nil, err
	}
	entries := readmodel.New().Sidebar(snapshot, "")
	return &daemonapi.GetSidebarResponse{Entries: entries}, nil
}
func (a *API) GetPlan(ctx context.Context, req *daemonapi.GetPlanRequest) (*daemonapi.PlanView, error) {
	snapshot, err := a.readModelSnapshot(ctx, req.WorkspaceID)
	if err != nil {
		return nil, err
	}
	payload := struct {
		Plan     domain.Plan            `json:"plan"`
		SubPlans []domain.TaskPlan      `json:"sub_plans"`
		Overview readmodel.OverviewPlan `json:"overview"`
	}{
		Plan:     snapshot.Plans[req.SessionID],
		SubPlans: snapshot.SubPlans[req.SessionID],
		Overview: readmodel.New().SessionOverview(snapshot, req.SessionID).Plan,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal plan read model: %v", err)
	}
	return &daemonapi.PlanView{PayloadJSON: string(data)}, nil
}

func (a *API) GetArtifacts(ctx context.Context, req *daemonapi.GetArtifactsRequest) (*daemonapi.ArtifactsView, error) {
	snapshot, err := a.readModelSnapshot(ctx, req.WorkspaceID)
	if err != nil {
		return nil, err
	}
	payload := struct {
		Items           []logic.ArtifactItem `json:"items"`
		AggregateReview string               `json:"aggregate_review"`
		AggregateCI     string               `json:"aggregate_ci"`
	}{Items: snapshot.Artifacts[req.SessionID]}
	for _, action := range readmodel.New().Sidebar(snapshot, req.SessionID) {
		if action.WorkItemID == req.SessionID && action.Kind == readmodel.SidebarTaskArtifacts {
			payload.AggregateReview = action.ArtifactAggregateReview
			payload.AggregateCI = action.ArtifactAggregateCI
			break
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal artifacts read model: %v", err)
	}
	return &daemonapi.ArtifactsView{PayloadJSON: string(data)}, nil
}

func (a *API) GetAvailableActions(ctx context.Context, req *daemonapi.GetAvailableActionsRequest) (*daemonapi.GetAvailableActionsResponse, error) {
	snapshot, err := a.readModelSnapshot(ctx, req.WorkspaceID)
	if err != nil {
		return nil, err
	}
	actions := readmodel.New().AvailableActions(snapshot, req.SessionID)
	return &daemonapi.GetAvailableActionsResponse{Actions: actions}, nil
}

func (a *API) DeleteSession(ctx context.Context, req *daemonapi.DeleteSessionRequest) (*daemonapi.ActionResultResponse, error) {
	if a.logicClient() == nil {
		return nil, status.Error(codes.FailedPrecondition, "logic client is unavailable")
	}
	if cached, ok := a.cachedResponse("DeleteSession", req.IdempotencyKey); ok {
		return cached.(*daemonapi.ActionResultResponse), nil
	}
	result, err := a.logicClient().DeleteSession(ctx, req.SessionID)
	if err != nil {
		return nil, normalizeError(err)
	}
	res := &daemonapi.ActionResultResponse{Message: result.Message}
	a.storeResponse("DeleteSession", req.IdempotencyKey, res)
	return res, nil
}

func (a *API) readModelSnapshot(ctx context.Context, workspaceID string) (logic.InitialSnapshot, error) {
	if a.logicClient() == nil {
		return logic.InitialSnapshot{}, status.Error(codes.FailedPrecondition, "logic client is unavailable")
	}
	snapshot, err := a.logicClient().GetInitialSnapshot(ctx, workspaceID)
	if err != nil {
		return logic.InitialSnapshot{}, normalizeError(err)
	}
	return snapshot, nil
}

func (a *API) GetSession(ctx context.Context, req *daemonapi.GetSessionRequest) (*daemonapi.GetSessionResponse, error) {
	if a.logicClient() == nil {
		return nil, status.Error(codes.FailedPrecondition, "logic client is unavailable")
	}
	session, err := a.logicClient().GetSession(ctx, req.SessionID)
	if err != nil {
		return nil, normalizeError(err)
	}
	return &daemonapi.GetSessionResponse{Session: session}, nil
}

func (a *API) SearchSessionHistory(ctx context.Context, req *daemonapi.SearchSessionHistoryRequest) (*daemonapi.SearchSessionHistoryResponse, error) {
	if a.logicClient() == nil {
		return nil, status.Error(codes.FailedPrecondition, "logic client is unavailable")
	}
	entries, err := a.logicClient().SearchSessionHistory(ctx, req.Filter)
	if err != nil {
		return nil, normalizeError(err)
	}
	return &daemonapi.SearchSessionHistoryResponse{Entries: entries}, nil
}

func (a *API) ArchiveSession(ctx context.Context, req *daemonapi.ArchiveSessionRequest) (*daemonapi.ActionResultResponse, error) {
	if a.logicClient() == nil {
		return nil, status.Error(codes.FailedPrecondition, "logic client is unavailable")
	}
	if cached, ok := a.cachedResponse("ArchiveSession", req.IdempotencyKey); ok {
		return cached.(*daemonapi.ActionResultResponse), nil
	}
	result, err := a.logicClient().ArchiveSession(ctx, req.SessionID)
	if err != nil {
		return nil, normalizeError(err)
	}
	res := &daemonapi.ActionResultResponse{Message: result.Message}
	a.storeResponse("ArchiveSession", req.IdempotencyKey, res)
	return res, nil
}

func (a *API) UnarchiveSession(ctx context.Context, req *daemonapi.UnarchiveSessionRequest) (*daemonapi.ActionResultResponse, error) {
	if a.logicClient() == nil {
		return nil, status.Error(codes.FailedPrecondition, "logic client is unavailable")
	}
	if cached, ok := a.cachedResponse("UnarchiveSession", req.IdempotencyKey); ok {
		return cached.(*daemonapi.ActionResultResponse), nil
	}
	result, err := a.logicClient().UnarchiveSession(ctx, req.SessionID)
	if err != nil {
		return nil, normalizeError(err)
	}
	res := &daemonapi.ActionResultResponse{Message: result.Message}
	a.storeResponse("UnarchiveSession", req.IdempotencyKey, res)
	return res, nil
}

func (a *API) OverrideAccept(ctx context.Context, req *daemonapi.OverrideAcceptRequest) (*daemonapi.ActionResultResponse, error) {
	if a.logicClient() == nil {
		return nil, status.Error(codes.FailedPrecondition, "logic client is unavailable")
	}
	if cached, ok := a.cachedResponse("OverrideAccept", req.IdempotencyKey); ok {
		return cached.(*daemonapi.ActionResultResponse), nil
	}
	result, err := a.logicClient().OverrideAccept(ctx, req.WorkItemID)
	if err != nil {
		return nil, normalizeError(err)
	}
	res := &daemonapi.ActionResultResponse{Message: result.Message}
	a.storeResponse("OverrideAccept", req.IdempotencyKey, res)
	return res, nil
}

func (a *API) FailReview(ctx context.Context, req *daemonapi.FailReviewRequest) (*daemonapi.ActionResultResponse, error) {
	if a.logicClient() == nil {
		return nil, status.Error(codes.FailedPrecondition, "logic client is unavailable")
	}
	if cached, ok := a.cachedResponse("FailReview", req.IdempotencyKey); ok {
		return cached.(*daemonapi.ActionResultResponse), nil
	}
	result, err := a.logicClient().FailReview(ctx, req.WorkItemID)
	if err != nil {
		return nil, normalizeError(err)
	}
	res := &daemonapi.ActionResultResponse{Message: result.Message}
	a.storeResponse("FailReview", req.IdempotencyKey, res)
	return res, nil
}
func (a *API) StartAutonomousMode(ctx context.Context, req *daemonapi.StartAutonomousModeRequest) (*daemonapi.AutonomousModeRun, error) {
	if a.opts.Autonomous == nil {
		return nil, status.Error(codes.FailedPrecondition, "autonomous mode state is unavailable")
	}
	bus := a.eventBus()
	publisher := a.eventPublisher()
	run, err := a.opts.Autonomous.Start(ctx, bus, publisher, *req)
	if err != nil {
		return nil, normalizeError(err)
	}
	if a.opts.AutonomousCtl != nil {
		if err := a.opts.AutonomousCtl.Start(ctx, *req); err != nil {
			// Drop the idempotency cache so a retry with the same key re-creates
			// the run instead of returning the stale running snapshot captured
			// before the controller failed. MarkStopped already transitions the
			// in-memory run to the stopped state.
			a.opts.Autonomous.ForgetIdempotent("StartAutonomousMode", req.InstanceID, req.IdempotencyKey)
			a.opts.Autonomous.MarkStopped(ctx, bus, publisher, req.InstanceID, "controller start failed")
			return nil, normalizeError(err)
		}
	}
	return &run, nil
}

func (a *API) StopAutonomousMode(ctx context.Context, req *daemonapi.StopAutonomousModeRequest) (*daemonapi.AutonomousModeStatusResponse, error) {
	if a.opts.Autonomous == nil {
		return nil, status.Error(codes.FailedPrecondition, "autonomous mode state is unavailable")
	}
	if a.opts.AutonomousCtl != nil {
		a.opts.AutonomousCtl.Stop(ctx, req.InstanceID)
	}
	if _, err := a.opts.Autonomous.Stop(ctx, a.eventBus(), a.eventPublisher(), *req); err != nil {
		return nil, normalizeError(err)
	}
	status := a.opts.Autonomous.Status(req.InstanceID)
	return &status, nil
}

func (a *API) GetAutonomousModeStatus(ctx context.Context, req *daemonapi.GetAutonomousModeStatusRequest) (*daemonapi.AutonomousModeStatusResponse, error) {
	if a.opts.Autonomous == nil {
		return nil, status.Error(codes.FailedPrecondition, "autonomous mode state is unavailable")
	}
	status := a.opts.Autonomous.Status(req.InstanceID)
	return &status, nil
}

func (a *API) eventPublisher() eventServicePublisher {
	if events := a.eventService(); events != nil {
		return events
	}
	return nil
}

func (a *API) ApprovePlan(ctx context.Context, req *daemonapi.ApprovePlanRequest) (*daemonapi.ActionResultResponse, error) {
	if a.logicClient() == nil {
		return nil, status.Error(codes.FailedPrecondition, "logic client is unavailable")
	}
	res, err := idempotent(a, "ApprovePlan", req.IdempotencyKey, func() (*daemonapi.ActionResultResponse, error) {
		result, err := a.logicClient().ApprovePlan(ctx, req.PlanID, req.WorkItemID)
		if err != nil {
			return nil, normalizeError(err)
		}
		return &daemonapi.ActionResultResponse{Message: result.Message}, nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (a *API) RequestPlanChanges(ctx context.Context, req *daemonapi.RequestPlanChangesRequest) (*daemonapi.ActionResultResponse, error) {
	if a.logicClient() == nil {
		return nil, status.Error(codes.FailedPrecondition, "logic client is unavailable")
	}
	if cached, ok := a.cachedResponse("RequestPlanChanges", req.IdempotencyKey); ok {
		return cached.(*daemonapi.ActionResultResponse), nil
	}
	result, err := a.logicClient().RequestPlanChanges(ctx, req.WorkItemID, req.PlanID, req.Feedback)
	if err != nil {
		return nil, normalizeError(err)
	}
	res := &daemonapi.ActionResultResponse{Message: result.Message}
	a.storeResponse("RequestPlanChanges", req.IdempotencyKey, res)
	return res, nil
}

func (a *API) SaveReviewedPlan(ctx context.Context, req *daemonapi.SaveReviewedPlanRequest) (*daemonapi.SaveReviewedPlanResponse, error) {
	if a.logicClient() == nil {
		return nil, status.Error(codes.FailedPrecondition, "logic client is unavailable")
	}
	if cached, ok := a.cachedResponse("SaveReviewedPlan", req.IdempotencyKey); ok {
		return cached.(*daemonapi.SaveReviewedPlanResponse), nil
	}
	plan, subPlans, err := a.logicClient().SaveReviewedPlan(ctx, req.PlanID, req.Content)
	if err != nil {
		return nil, normalizeError(err)
	}
	res := &daemonapi.SaveReviewedPlanResponse{Plan: plan, SubPlans: subPlans, Message: "Plan updated"}
	a.storeResponse("SaveReviewedPlan", req.IdempotencyKey, res)
	return res, nil
}

func (a *API) RunImplementation(ctx context.Context, req *daemonapi.RunImplementationRequest) (*daemonapi.OperationResponse, error) {
	if a.logicClient() == nil {
		return nil, status.Error(codes.FailedPrecondition, "logic client is unavailable")
	}
	if cached, ok := a.cachedResponse("RunImplementation", req.IdempotencyKey); ok {
		return cached.(*daemonapi.OperationResponse), nil
	}
	operation, err := a.logicClient().RunImplementation(ctx, req.PlanID)
	if err != nil {
		return nil, normalizeError(err)
	}
	res := &daemonapi.OperationResponse{Operation: operation}
	a.storeResponse("RunImplementation", req.IdempotencyKey, res)
	return res, nil
}

func (a *API) StartPlanning(ctx context.Context, req *daemonapi.StartPlanningRequest) (*daemonapi.ActionResultResponse, error) {
	if a.logicClient() == nil {
		return nil, status.Error(codes.FailedPrecondition, "logic client is unavailable")
	}
	if cached, ok := a.cachedResponse("StartPlanning", req.IdempotencyKey); ok {
		return cached.(*daemonapi.ActionResultResponse), nil
	}
	result, err := a.logicClient().StartPlanning(ctx, req.WorkItemID)
	if err != nil {
		return nil, normalizeError(err)
	}
	res := &daemonapi.ActionResultResponse{Message: result.Message}
	a.storeResponse("StartPlanning", req.IdempotencyKey, res)
	return res, nil
}

func (a *API) RestartPlanning(ctx context.Context, req *daemonapi.RestartPlanningRequest) (*daemonapi.ActionResultResponse, error) {
	if a.logicClient() == nil {
		return nil, status.Error(codes.FailedPrecondition, "logic client is unavailable")
	}
	if cached, ok := a.cachedResponse("RestartPlanning", req.IdempotencyKey); ok {
		return cached.(*daemonapi.ActionResultResponse), nil
	}
	result, err := a.logicClient().RestartPlanning(ctx, req.WorkItemID, req.Prompt)
	if err != nil {
		return nil, normalizeError(err)
	}
	res := &daemonapi.ActionResultResponse{Message: result.Message}
	a.storeResponse("RestartPlanning", req.IdempotencyKey, res)
	return res, nil
}

func (a *API) FollowUpPlan(ctx context.Context, req *daemonapi.FollowUpPlanRequest) (*daemonapi.ActionResultResponse, error) {
	if a.logicClient() == nil {
		return nil, status.Error(codes.FailedPrecondition, "logic client is unavailable")
	}
	if cached, ok := a.cachedResponse("FollowUpPlan", req.IdempotencyKey); ok {
		return cached.(*daemonapi.ActionResultResponse), nil
	}
	result, err := a.logicClient().FollowUpPlan(ctx, req.WorkItemID, req.Feedback)
	if err != nil {
		return nil, normalizeError(err)
	}
	res := &daemonapi.ActionResultResponse{Message: result.Message}
	a.storeResponse("FollowUpPlan", req.IdempotencyKey, res)
	return res, nil
}

func (a *API) FinalizeSession(ctx context.Context, req *daemonapi.FinalizeSessionRequest) (*daemonapi.ActionResultResponse, error) {
	if a.logicClient() == nil {
		return nil, status.Error(codes.FailedPrecondition, "logic client is unavailable")
	}
	if cached, ok := a.cachedResponse("FinalizeSession", req.IdempotencyKey); ok {
		return cached.(*daemonapi.ActionResultResponse), nil
	}
	result, err := a.logicClient().FinalizeWorkItem(ctx, req.WorkItemID)
	if err != nil {
		return nil, normalizeError(err)
	}
	res := &daemonapi.ActionResultResponse{Message: result.Message}
	a.storeResponse("FinalizeSession", req.IdempotencyKey, res)
	return res, nil
}

func (a *API) RetryFailedSession(ctx context.Context, req *daemonapi.RetryFailedSessionRequest) (*daemonapi.ActionResultResponse, error) {
	if a.logicClient() == nil {
		return nil, status.Error(codes.FailedPrecondition, "logic client is unavailable")
	}
	if cached, ok := a.cachedResponse("RetryFailedSession", req.IdempotencyKey); ok {
		return cached.(*daemonapi.ActionResultResponse), nil
	}
	result, err := a.logicClient().RetryFailedWorkItem(ctx, req.PlanID, req.WorkItemID)
	if err != nil {
		return nil, normalizeError(err)
	}
	res := &daemonapi.ActionResultResponse{Message: result.Message}
	a.storeResponse("RetryFailedSession", req.IdempotencyKey, res)
	return res, nil
}

func (a *API) ResumeAllForSession(ctx context.Context, req *daemonapi.ResumeAllForSessionRequest) (*daemonapi.ActionResultResponse, error) {
	if a.logicClient() == nil {
		return nil, status.Error(codes.FailedPrecondition, "logic client is unavailable")
	}
	if cached, ok := a.cachedResponse("ResumeAllForSession", req.IdempotencyKey); ok {
		return cached.(*daemonapi.ActionResultResponse), nil
	}
	result, err := a.logicClient().ResumeAllSessionsForWorkItem(ctx, req.WorkItemID, req.InstanceID)
	if err != nil {
		return nil, normalizeError(err)
	}
	res := &daemonapi.ActionResultResponse{Message: result.Message}
	a.storeResponse("ResumeAllForSession", req.IdempotencyKey, res)
	return res, nil
}
func (a *API) CancelPipeline(ctx context.Context, req *daemonapi.CancelPipelineRequest) (*daemonapi.ActionResultResponse, error) {
	if a.logicClient() == nil {
		return nil, status.Error(codes.FailedPrecondition, "logic client is unavailable")
	}
	if cached, ok := a.cachedResponse("CancelPipeline", req.IdempotencyKey); ok {
		return cached.(*daemonapi.ActionResultResponse), nil
	}
	result, err := a.logicClient().CancelPipeline(ctx, req.WorkItemID, req.AgentSessionID)
	if err != nil {
		return nil, normalizeError(err)
	}
	res := &daemonapi.ActionResultResponse{Message: result.Message}
	a.storeResponse("CancelPipeline", req.IdempotencyKey, res)
	return res, nil
}

func (a *API) RetryAgentSession(ctx context.Context, req *daemonapi.RetryAgentSessionRequest) (*daemonapi.ActionResultResponse, error) {
	if a.logicClient() == nil {
		return nil, status.Error(codes.FailedPrecondition, "logic client is unavailable")
	}
	if cached, ok := a.cachedResponse("RetryAgentSession", req.IdempotencyKey); ok {
		return cached.(*daemonapi.ActionResultResponse), nil
	}
	result, err := a.logicClient().RetryAgentSession(ctx, req.AgentSessionID, req.InstanceID)
	if err != nil {
		return nil, normalizeError(err)
	}
	res := &daemonapi.ActionResultResponse{Message: result.Message}
	a.storeResponse("RetryAgentSession", req.IdempotencyKey, res)
	return res, nil
}

func (a *API) FollowUpAgentSession(ctx context.Context, req *daemonapi.FollowUpAgentSessionRequest) (*daemonapi.ActionResultResponse, error) {
	if a.logicClient() == nil {
		return nil, status.Error(codes.FailedPrecondition, "logic client is unavailable")
	}
	if cached, ok := a.cachedResponse("FollowUpAgentSession", req.IdempotencyKey); ok {
		return cached.(*daemonapi.ActionResultResponse), nil
	}
	result, err := a.logicClient().FollowUpAgentSession(ctx, req.AgentSessionID, req.Feedback, req.InstanceID)
	if err != nil {
		return nil, normalizeError(err)
	}
	res := &daemonapi.ActionResultResponse{Message: result.Message}
	a.storeResponse("FollowUpAgentSession", req.IdempotencyKey, res)
	return res, nil
}

func (a *API) SteerSession(ctx context.Context, req *daemonapi.SteerSessionRequest) (*daemonapi.ActionResultResponse, error) {
	if a.logicClient() == nil {
		return nil, status.Error(codes.FailedPrecondition, "logic client is unavailable")
	}
	if cached, ok := a.cachedResponse("SteerSession", req.IdempotencyKey); ok {
		return cached.(*daemonapi.ActionResultResponse), nil
	}
	result, err := a.logicClient().SteerSession(ctx, req.AgentSessionID, req.Message)
	if err != nil {
		return nil, normalizeError(err)
	}
	res := &daemonapi.ActionResultResponse{Message: result.Message}
	a.storeResponse("SteerSession", req.IdempotencyKey, res)
	return res, nil
}

func (a *API) AnswerQuestion(ctx context.Context, req *daemonapi.AnswerQuestionRequest) (*daemonapi.ActionResultResponse, error) {
	if a.logicClient() == nil {
		return nil, status.Error(codes.FailedPrecondition, "logic client is unavailable")
	}
	if cached, ok := a.cachedResponse("AnswerQuestion", req.IdempotencyKey); ok {
		return cached.(*daemonapi.ActionResultResponse), nil
	}
	result, err := a.logicClient().AnswerQuestion(ctx, req.QuestionID, req.Answer, req.AnsweredBy)
	if err != nil {
		return nil, normalizeError(err)
	}
	res := &daemonapi.ActionResultResponse{Message: result.Message}
	a.storeResponse("AnswerQuestion", req.IdempotencyKey, res)
	return res, nil
}

func (a *API) SkipQuestion(ctx context.Context, req *daemonapi.SkipQuestionRequest) (*daemonapi.ActionResultResponse, error) {
	if a.logicClient() == nil {
		return nil, status.Error(codes.FailedPrecondition, "logic client is unavailable")
	}
	if cached, ok := a.cachedResponse("SkipQuestion", req.IdempotencyKey); ok {
		return cached.(*daemonapi.ActionResultResponse), nil
	}
	result, err := a.logicClient().SkipQuestion(ctx, req.QuestionID)
	if err != nil {
		return nil, normalizeError(err)
	}
	res := &daemonapi.ActionResultResponse{Message: result.Message}
	a.storeResponse("SkipQuestion", req.IdempotencyKey, res)
	return res, nil
}

func idempotent[T any](a *API, action, key string, fn func() (T, error)) (T, error) {
	var zero T
	key = strings.TrimSpace(key)
	if key == "" {
		return fn()
	}
	cacheKey := action + ":" + key
	if cached, ok := a.cachedResponse(action, key); ok {
		if value, ok := cached.(T); ok {
			return value, nil
		}
		return zero, status.Errorf(codes.Internal, "cached idempotent response for %s has unexpected type", action)
	}
	call := &inFlightCall{done: make(chan struct{})}
	actual, loaded := a.inflight.LoadOrStore(cacheKey, call)
	if loaded {
		prior := actual.(*inFlightCall)
		<-prior.done
		if prior.err != nil {
			return zero, prior.err
		}
		if value, ok := prior.val.(T); ok {
			return value, nil
		}
		return zero, status.Errorf(codes.Internal, "in-flight idempotent response for %s has unexpected type", action)
	}
	defer a.inflight.Delete(cacheKey)
	defer close(call.done)
	value, err := fn()
	call.val = value
	call.err = err
	if err != nil {
		return zero, err
	}
	a.storeResponse(action, key, value)
	return value, nil
}

func (a *API) cachedResponse(action, key string) (any, bool) {
	if strings.TrimSpace(key) == "" {
		return nil, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	value, ok := a.seen[action+":"+key]
	return value, ok
}

func (a *API) storeResponse(action, key string, value any) {
	if strings.TrimSpace(key) == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.seen[action+":"+key] = value
}

func (a *API) Subscribe(req *daemonapi.SubscribeEventsRequest, stream grpc.ServerStreamingServer[daemonapi.EventBatch]) error {
	deps := a.currentDeps()
	var sub *event.Subscriber
	if deps.Bus != nil {
		var err error
		sub, err = deps.Bus.Subscribe("grpc-event-stream:"+domain.NewID(), req.EventTypes...)
		if err != nil {
			return err
		}
		defer deps.Bus.Unsubscribe(sub.ID)
	}

	seenSequence := req.AfterSequence
	if deps.Events != nil {
		limit := replayLimit(req.ReplayWindow)
		replay, err := deps.Events.ListByWorkspaceIDAfterSequence(stream.Context(), req.WorkspaceID, req.AfterSequence, limit+1)
		if err != nil {
			return normalizeError(err)
		}
		// Filter by EventTypes before enforcing the replay window so a narrow
		// subscription is not rejected just because the workspace accumulated
		// more events than fit in the window for unrelated types.
		if len(req.EventTypes) > 0 {
			filtered := replay[:0]
			for _, event := range replay {
				if eventMatchesAnyType(event, req.EventTypes) {
					filtered = append(filtered, event)
				}
			}
			replay = filtered
		}
		if len(replay) > limit {
			return status.Errorf(codes.ResourceExhausted, "event replay window exceeded; reload snapshot and resubscribe")
		}
		batch := daemonapi.EventBatch{Events: make([]daemonapi.SystemEventEnvelope, 0, len(replay)), CaughtUp: true}
		for _, event := range replay {
			batch.Events = append(batch.Events, daemonapi.EventEnvelope(event))
			batch.LatestSequence = event.Sequence
			seenSequence = event.Sequence
		}
		if len(batch.Events) > 0 || req.IncludeSnapshotMarker {
			if err := stream.Send(&batch); err != nil {
				return err
			}
		}
	}
	if sub == nil {
		return nil
	}
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case event, ok := <-sub.C:
			if !ok {
				return nil
			}
			if req.WorkspaceID != "" && event.WorkspaceID != req.WorkspaceID {
				continue
			}
			if !eventMatchesAnyType(event, req.EventTypes) {
				continue
			}
			if event.Sequence <= seenSequence {
				continue
			}
			batch := daemonapi.EventBatch{Events: []daemonapi.SystemEventEnvelope{daemonapi.EventEnvelope(event)}, LatestSequence: event.Sequence, CaughtUp: true}
			if err := sendWithSlowConsumerPolicy(stream, batch); err != nil {
				return err
			}
		}
	}
}

// eventMatchesAnyType reports whether event.EventType matches one of the
// supplied types. An empty types slice matches every event so callers that
// pass no filter do not need a special case.
func eventMatchesAnyType(event domain.SystemEvent, types []string) bool {
	if len(types) == 0 {
		return true
	}
	for _, t := range types {
		if strings.EqualFold(strings.TrimSpace(event.EventType), strings.TrimSpace(t)) {
			return true
		}
	}
	return false
}

func (a *API) SnapshotAgentSessionLog(ctx context.Context, req *daemonapi.SnapshotAgentSessionLogRequest) (*daemonapi.SessionLogSnapshot, error) {
	entries, nextOffset, err := a.loadSessionLog(req.AgentSessionID, true)
	if err != nil {
		return nil, err
	}
	encoded, err := encodeSessionLogEntries(entries)
	if err != nil {
		return nil, err
	}
	return &daemonapi.SessionLogSnapshot{AgentSessionID: req.AgentSessionID, EntriesJSON: encoded, NextOffset: nextOffset}, nil
}

func (a *API) TailAgentSessionLog(req *daemonapi.TailAgentSessionLogRequest, stream grpc.ServerStreamingServer[daemonapi.SessionLogBatch]) error {
	since := req.Since
	if since == 0 {
		entries, nextOffset, err := a.loadSessionLog(req.AgentSessionID, false)
		if err != nil {
			return err
		}
		encoded, err := encodeSessionLogEntries(entries)
		if err != nil {
			return err
		}
		if err := stream.Send(&daemonapi.SessionLogBatch{AgentSessionID: req.AgentSessionID, EntriesJSON: encoded, NextOffset: nextOffset}); err != nil {
			return err
		}
		since = nextOffset
	}
	activePath, err := a.sessionLogPath(req.AgentSessionID)
	if err != nil {
		return err
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-ticker.C:
			entries, nextOffset, err := tailActiveSessionLog(activePath, since)
			if err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					slog.Warn("tail agent session log", "error", err, "agent_session_id", req.AgentSessionID)
				}
				continue
			}
			if nextOffset == since && len(entries) == 0 {
				continue
			}
			encoded, err := encodeSessionLogEntries(entries)
			if err != nil {
				return err
			}
			since = nextOffset
			if err := stream.Send(&daemonapi.SessionLogBatch{AgentSessionID: req.AgentSessionID, EntriesJSON: encoded, NextOffset: nextOffset}); err != nil {
				return err
			}
		}
	}
}

func (a *API) SnapshotAppLog(ctx context.Context, _ *daemonapi.SnapshotAppLogRequest) (*daemonapi.AppLogSnapshot, error) {
	entries := a.appLogEntriesSince(0)
	encoded, err := encodeAppLogEntries(entries)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "encode app log: %v", err)
	}
	return &daemonapi.AppLogSnapshot{EntriesJSON: encoded, NextOffset: int64(len(entries))}, nil
}

func (a *API) TailAppLog(req *daemonapi.TailAppLogRequest, stream grpc.ServerStreamingServer[daemonapi.AppLogBatch]) error {
	// Send any backlog first, then poll for future entries until the stream
	// context is cancelled. This mirrors TailAgentSessionLog so callers do
	// not have to re-issue Tail every time they want new lines.
	since := req.Since
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		entries := a.appLogEntriesSince(since)
		if len(entries) > 0 {
			encoded, err := encodeAppLogEntries(entries)
			if err != nil {
				return status.Errorf(codes.Internal, "encode app log: %v", err)
			}
			next := since + int64(len(entries))
			if err := stream.Send(&daemonapi.AppLogBatch{EntriesJSON: encoded, NextOffset: next}); err != nil {
				return err
			}
			since = next
		}
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-ticker.C:
		}
	}
}

func (a *API) appLogEntriesSince(since int64) []tuilog.Entry {
	if a.logStore() == nil {
		return nil
	}
	entries := a.logStore().Snapshot()
	if since <= 0 {
		return entries
	}
	if since >= int64(len(entries)) {
		return nil
	}
	return entries[int(since):]
}

func encodeAppLogEntries(entries []tuilog.Entry) ([]string, error) {
	encoded := make([]string, 0, len(entries))
	for _, entry := range entries {
		data, err := json.Marshal(entry)
		if err != nil {
			return nil, err
		}
		encoded = append(encoded, string(data))
	}
	return encoded, nil
}

func (a *API) loadSessionLog(agentSessionID string, includePartial bool) ([]sessionlog.Entry, int64, error) {
	if err := sessionlog.ValidateSessionID(agentSessionID); err != nil {
		return nil, 0, status.Error(codes.InvalidArgument, err.Error())
	}
	if strings.TrimSpace(a.opts.SessionsDir) == "" {
		return nil, 0, status.Error(codes.FailedPrecondition, "session log directory is unavailable")
	}
	entries, nextOffset, err := sessionlog.LoadInteractionEntries(a.opts.SessionsDir, agentSessionID, includePartial)
	if err != nil {
		return nil, 0, status.Errorf(codes.Internal, "load session log: %v", err)
	}
	return entries, nextOffset, nil
}

func (a *API) sessionLogPath(agentSessionID string) (string, error) {
	if err := sessionlog.ValidateSessionID(agentSessionID); err != nil {
		return "", status.Error(codes.InvalidArgument, err.Error())
	}
	if strings.TrimSpace(a.opts.SessionsDir) == "" {
		return "", status.Error(codes.FailedPrecondition, "session log directory is unavailable")
	}
	return filepath.Join(a.opts.SessionsDir, agentSessionID+".log"), nil
}

func encodeSessionLogEntries(entries []sessionlog.Entry) ([]string, error) {
	encoded := make([]string, 0, len(entries))
	for _, entry := range entries {
		data, err := json.Marshal(entry)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "encode session log entry: %v", err)
		}
		encoded = append(encoded, string(data))
	}
	return encoded, nil
}

func tailActiveSessionLog(path string, since int64) ([]sessionlog.Entry, int64, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return nil, since, err
	}
	offset := since
	if stat.Size() < since {
		offset = 0
	}
	if stat.Size() == offset {
		return nil, offset, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, offset, err
	}
	defer file.Close()
	if _, err := file.Seek(offset, 0); err != nil {
		return nil, offset, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, offset, err
	}
	if lastNewline := bytes.LastIndexByte(data, '\n'); lastNewline >= 0 {
		data = data[:lastNewline+1]
	} else {
		return nil, offset, nil
	}
	entries, err := sessionlog.ScanEntries(bytes.NewReader(data))
	if err != nil {
		return nil, offset + int64(len(data)), err
	}
	return entries, offset + int64(len(data)), nil
}
func (a *API) settingsConfigFromRaw(raw string) (*config.Config, error) {
	if strings.TrimSpace(raw) == "" {
		a.mu.Lock()
		cfg := a.opts.Config
		a.mu.Unlock()
		if cfg == nil {
			return nil, status.Error(codes.FailedPrecondition, "daemon config is unavailable")
		}
		return cfg, nil
	}
	tmp, err := os.CreateTemp("", "substrate-daemon-settings-*.yaml")
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create temp settings: %v", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.WriteString(raw); err != nil {
		if closeErr := tmp.Close(); closeErr != nil {
			slog.Warn("failed to close temp settings file after write error", "error", closeErr)
		}
		return nil, status.Errorf(codes.Internal, "write temp settings: %v", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, status.Errorf(codes.Internal, "close temp settings: %v", err)
	}
	cfg, err := config.Load(tmpPath)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "parse settings yaml: %v", err)
	}
	if err := config.LoadSecrets(cfg, a.opts.SecretStore); err != nil {
		return nil, status.Errorf(codes.Internal, "load settings secrets: %v", err)
	}
	return cfg, nil
}

func (a *API) harnessActionRunner(harness string) adapter.HarnessActionRunner {
	runner, ok := a.harnesses().Implementation.(adapter.HarnessActionRunner)
	if !ok {
		return nil
	}
	switch config.HarnessName(strings.TrimSpace(harness)) {
	case config.HarnessOhMyPi, config.HarnessClaudeCode, config.HarnessCodex, config.HarnessOpenCode, config.HarnessACP:
		return runner
	default:
		return nil
	}
}

func providerLoginInputs(cfg *config.Config, provider string) map[string]string {
	switch provider {
	case "sentry":
		if rawBaseURL := strings.TrimSpace(cfg.Adapters.Sentry.BaseURL); rawBaseURL != "" {
			return map[string]string{"base_url": strings.TrimSpace(config.SentryRootURL(rawBaseURL))}
		}
	}
	return nil
}

func testProviderConnection(ctx context.Context, cfg *config.Config, provider string) (daemonapi.ProviderStatus, error) {
	statuses := providerStatuses(cfg)
	providerStatus := statuses[provider]
	switch provider {
	case "linear":
		if strings.TrimSpace(cfg.Adapters.Linear.APIKey) == "" {
			return providerStatus, statusError(codes.InvalidArgument, "linear api key is required", &providerStatus)
		}
		client := linearadapter.New(cfg.Adapters.Linear)
		_, err := client.ListSelectable(ctx, adapter.ListOpts{Scope: domain.ScopeIssues, Limit: 1})
		return providerConnectionResult(providerStatus, err)
	case "gitlab":
		client, err := gitlabadapter.New(ctx, cfg.Adapters.GitLab)
		if err != nil {
			return providerConnectionResult(providerStatus, err)
		}
		_, err = client.ListSelectable(ctx, adapter.ListOpts{Scope: domain.ScopeIssues, Limit: 1})
		return providerConnectionResult(providerStatus, err)
	case "sentry":
		client, err := sentryadapter.New(ctx, cfg.Adapters.Sentry)
		if err != nil {
			return providerConnectionResult(providerStatus, err)
		}
		_, err = client.ListSelectable(ctx, adapter.ListOpts{Scope: domain.ScopeIssues, Limit: 1})
		return providerConnectionResult(providerStatus, err)
	case "github":
		client, err := githubadapter.New(ctx, cfg.Adapters.GitHub)
		if err != nil {
			return providerConnectionResult(providerStatus, err)
		}
		_, err = client.ListSelectable(ctx, adapter.ListOpts{Scope: domain.ScopeIssues, Limit: 1})
		return providerConnectionResult(providerStatus, err)
	default:
		return daemonapi.ProviderStatus{}, status.Errorf(codes.InvalidArgument, "unknown provider %q", provider)
	}
}

func providerConnectionResult(status daemonapi.ProviderStatus, err error) (daemonapi.ProviderStatus, error) {
	if err != nil {
		status.Connected = false
		status.LastError = err.Error()
		return status, normalizeError(err)
	}
	status.Connected = true
	status.LastError = ""
	return status, nil
}

func statusError(code codes.Code, message string, providerStatus *daemonapi.ProviderStatus) error {
	if providerStatus != nil {
		providerStatus.Connected = false
		providerStatus.LastError = message
	}
	return status.Error(code, message)
}

func providerStatuses(cfg *config.Config) map[string]daemonapi.ProviderStatus {
	return map[string]daemonapi.ProviderStatus{
		"linear": {
			Title:       "Linear",
			Configured:  cfg.Adapters.Linear.APIKeyRef != "" || strings.TrimSpace(cfg.Adapters.Linear.APIKey) != "",
			AuthSource:  secretStatus(cfg.Adapters.Linear.APIKeyRef, cfg.Adapters.Linear.APIKey),
			Description: "Uses OS keychain-backed API key",
		},
		"gitlab": {
			Title:       "GitLab",
			Configured:  cfg.Adapters.GitLab.TokenRef != "" || strings.TrimSpace(cfg.Adapters.GitLab.Token) != "",
			AuthSource:  secretStatus(cfg.Adapters.GitLab.TokenRef, cfg.Adapters.GitLab.Token),
			Description: "Uses OS keychain-backed token",
		},
		"sentry": {
			Title:       "Sentry",
			Configured:  config.SentryAuthConfigured(cfg.Adapters.Sentry),
			AuthSource:  config.SentryAuthSource(cfg.Adapters.Sentry),
			Description: "Uses keychain, environment, or sentry CLI authentication",
		},
		"github": {
			Title:       "GitHub",
			Configured:  config.GitHubAuthConfigured(cfg.Adapters.GitHub),
			AuthSource:  config.GitHubAuthSource(cfg.Adapters.GitHub),
			Description: "Uses OS keychain-backed token or gh CLI fallback",
		},
	}
}

func secretStatus(ref, value string) string {
	switch {
	case strings.TrimSpace(ref) != "":
		return "keychain"
	case strings.TrimSpace(value) != "":
		return "inline"
	default:
		return "missing"
	}
}

func replayLimit(window int) int {
	if window <= 0 {
		return 500
	}
	if window > 5000 {
		return 5000
	}
	return window
}

func sendWithSlowConsumerPolicy(stream grpc.ServerStreamingServer[daemonapi.EventBatch], batch daemonapi.EventBatch) error {
	done := make(chan error, 1)
	go func() { done <- stream.Send(&batch) }()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		slog.Warn("closing slow event stream client", "latest_sequence", batch.LatestSequence)
		return status.Error(codes.ResourceExhausted, "slow event stream consumer")
	case <-stream.Context().Done():
		return stream.Context().Err()
	}
}

func (a *API) unaryAuthInterceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	if err := a.authorize(ctx); err != nil {
		return nil, err
	}
	return handler(ctx, req)
}

func (a *API) streamAuthInterceptor(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	if err := a.authorize(stream.Context()); err != nil {
		return err
	}
	return handler(srv, stream)
}

func (a *API) authorize(ctx context.Context) error {
	a.mu.Lock()
	expected := a.opts.Token
	a.mu.Unlock()
	if strings.TrimSpace(expected) == "" {
		return status.Error(codes.FailedPrecondition, "daemon access token is not configured")
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "missing authorization metadata")
	}
	values := md.Get("authorization")
	for _, value := range values {
		token := strings.TrimSpace(strings.TrimPrefix(value, "Bearer "))
		if subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1 {
			return nil
		}
	}
	return status.Error(codes.Unauthenticated, "invalid authorization token")
}

func normalizeError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
		return status.Error(codes.Canceled, err.Error())
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return status.Error(codes.DeadlineExceeded, err.Error())
	}
	var notFound service.ErrNotFound
	if errors.As(err, &notFound) {
		return status.Error(codes.NotFound, err.Error())
	}
	var invalidTransition service.ErrInvalidTransition
	if errors.As(err, &invalidTransition) {
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	var invalidInput service.ErrInvalidInput
	if errors.As(err, &invalidInput) {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	var alreadyExists service.ErrAlreadyExists
	if errors.As(err, &alreadyExists) {
		return status.Error(codes.AlreadyExists, err.Error())
	}
	var constraint service.ErrConstraintViolation
	if errors.As(err, &constraint) {
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	return status.Error(codes.Internal, err.Error())
}
