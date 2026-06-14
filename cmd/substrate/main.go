// Package main is the entry point for the Substrate CLI.
package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	atomic "github.com/beeemT/go-atomic"
	"github.com/jmoiron/sqlx"
	"google.golang.org/grpc"
	_ "modernc.org/sqlite"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/app"
	"github.com/beeemT/substrate/internal/buildinfo"
	"github.com/beeemT/substrate/internal/config"
	daemonclient "github.com/beeemT/substrate/internal/daemon/client"
	daemonserver "github.com/beeemT/substrate/internal/daemon/server"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/gitwork"
	"github.com/beeemT/substrate/internal/logic"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/repository/sqlite"
	"github.com/beeemT/substrate/internal/service"
	"github.com/beeemT/substrate/internal/tui/views"
	"github.com/beeemT/substrate/internal/tuilog"
	"github.com/beeemT/substrate/migrations"
)

func printUsage() {
	fmt.Println("substrate - AI-powered work item orchestration")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  substrate              Start/connect local daemon and launch TUI")
	fmt.Println("  substrate tui          Connect to selected daemon and launch TUI")
	fmt.Println("  substrate daemon       Run daemon")
	fmt.Println("  substrate daemon stop  Stop local daemon")
	fmt.Println("  substrate serve        Alias for daemon")
	fmt.Println("  substrate --version    Show version")
}

func main() {
	if err := run(); err != nil {
		// Write error to stderr before logging (which goes to TUI log store)
		fmt.Fprintln(os.Stderr, "error:", err)
		log.Fatal(err)
	}
}

type workspaceContext struct {
	ID   string
	Name string
	Dir  string
}

type coreServices struct {
	workItem             *service.SessionService
	plan                 *service.PlanService
	workspace            *service.WorkspaceService
	session              *service.AgentSessionService
	question             *service.QuestionService
	instance             *service.InstanceService
	review               *service.ReviewService
	event                *service.EventService
	githubPR             *service.GithubPRService
	gitlabMR             *service.GitlabMRService
	sessionArtifact      *service.SessionReviewArtifactService
	ghPRReview           *service.GithubPRReviewService
	glMRReview           *service.GitlabMRReviewService
	ghPRCheck            *service.GithubPRCheckService
	glMRCheck            *service.GitlabMRCheckService
	newSessionFilter     *service.SessionFilterService
	newSessionFilterLock *service.SessionFilterLockService
	settings             views.SettingsService
}

type adapterSetup struct {
	workItem       []adapter.WorkItemAdapter
	repoLifecycle  []adapter.RepoLifecycleAdapter
	repoSources    []adapter.RepoSource
	reviewComments *adapter.ReviewCommentDispatcher
	warnings       []string
	adapterErrors  chan views.AdapterErrorMsg
}

func run() error {
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "daemon", "serve":
			return runDaemon(args[1:])
		case "tui", "--tui":
			return runTUIClient(args[1:])
		}
	}
	if handleCLIArgs(args) {
		return nil
	}

	return runDefaultTUI(args)
}

func runDefaultTUI(args []string) error {
	cfg, err := loadRuntimeConfig()
	if err != nil {
		return err
	}
	daemonName, _, err := parseDaemonSelectionArg(args)
	if err != nil {
		return err
	}
	active := daemonName
	if active == "" {
		active = cfg.TUI.ActiveDaemon
	}
	if active == "" {
		active = "local"
	}
	if active == "local" {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := ensureLocalDaemon(ctx, cfg); err != nil {
			return err
		}
	}

	return runTUIClient(args)
}

func ensureLocalDaemon(ctx context.Context, cfg *config.Config) error {
	if compatible, err := localDaemonMatchesWorkspace(ctx, cfg); compatible {
		return nil
	} else if err == nil {
		slog.Info("local daemon is bound to a different workspace; restarting")
	} else {
		slog.Debug("local daemon health check failed; starting daemon", "error", err)
	}

	if err := stopStaleLocalDaemon(cfg); err != nil {
		slog.Debug("stopping stale local daemon", "error", err)
	}

	if err := registerLocalDaemonAddress(cfg); err != nil {
		return err
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	cmd := exec.Command(exe, "daemon")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start substrate daemon: %w", err)
	}
	if err := cmd.Process.Release(); err != nil {
		slog.Warn("release daemon process handle", "error", err)
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		healthy, err := localDaemonHealthy(ctx, cfg)
		if healthy {
			return nil
		}
		if err != nil {
			slog.Debug("waiting for local daemon readiness", "error", err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for local daemon readiness: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func localDaemonHealthy(ctx context.Context, cfg *config.Config) (bool, error) {
	token, err := config.DaemonAccessToken(cfg, config.OSKeychainStore{}, "local")
	if err != nil {
		return false, err
	}
	address, err := localDaemonAddress(cfg)
	if err != nil {
		return false, err
	}
	client, err := daemonclient.Dial(ctx, address, token)
	if err != nil {
		return false, err
	}
	defer client.Close()
	healthCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if _, err := client.Health(healthCtx); err != nil {
		return false, err
	}
	return true, nil
}

// localDaemonMatchesWorkspace reports whether the running local daemon is
// healthy AND bound to the same workspace the current process is operating
// in. A healthy daemon bound to a different workspace must be restarted so
// we don't reuse it across workspace boundaries.
func localDaemonMatchesWorkspace(ctx context.Context, cfg *config.Config) (bool, error) {
	if healthy, err := localDaemonHealthy(ctx, cfg); !healthy {
		return false, err
	}
	expectedDir, expectedID, err := currentWorkspaceIdentity()
	if err != nil {
		// Outside a workspace: trust the running daemon as long as it is
		// healthy; it can serve the workspace-init flow.
		slog.Debug("skipping workspace identity check (no workspace)", "error", err)
		return true, nil
	}
	token, err := config.DaemonAccessToken(cfg, config.OSKeychainStore{}, "local")
	if err != nil {
		return false, err
	}
	address, err := localDaemonAddress(cfg)
	if err != nil {
		return false, err
	}
	runtimeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	client, err := daemonclient.Dial(runtimeCtx, address, token)
	if err != nil {
		return false, err
	}
	defer client.Close()
	runtime, err := client.GetRuntimeContext(runtimeCtx)
	if err != nil {
		return false, err
	}
	if expectedID != "" && runtime.WorkspaceID != "" && runtime.WorkspaceID != expectedID {
		slog.Info("local daemon workspace mismatch", "expected", expectedID, "actual", runtime.WorkspaceID)
		return false, nil
	}
	if expectedDir != "" && runtime.WorkspaceDir != "" && runtime.WorkspaceDir != expectedDir {
		slog.Info("local daemon workspace dir mismatch", "expected", expectedDir, "actual", runtime.WorkspaceDir)
		return false, nil
	}
	return true, nil
}

// currentWorkspaceIdentity resolves the workspace dir and ID of the current
// process's working directory by reading the workspace file Substrate writes
// when initializing a workspace. Returns ("", "", nil) when no workspace is
// detected so callers can short-circuit cross-workspace checks.
func currentWorkspaceIdentity() (dir, id string, err error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("getwd: %w", err)
	}
	wsDir, wsFile, wsErr := gitwork.FindWorkspace(cwd)
	if wsErr != nil {
		if gitwork.IsNotInWorkspace(wsErr) {
			return "", "", nil
		}
		return "", "", wsErr
	}
	return wsDir, wsFile.ID, nil
}

// stopStaleLocalDaemon terminates the running local daemon so the caller can
// spawn a fresh one. It is a best-effort helper: errors are returned for
// logging but never block the restart path.
func stopStaleLocalDaemon(cfg *config.Config) error {
	socketPath, err := daemonSocketPath(cfg)
	if err != nil {
		return err
	}
	metadata, err := readDaemonMetadata(socketPath)
	if err != nil {
		return err
	}
	if metadata.PID <= 0 {
		return fmt.Errorf("daemon metadata has invalid pid %d", metadata.PID)
	}
	if err := syscall.Kill(metadata.PID, 0); err != nil {
		return fmt.Errorf("daemon process %d not reachable: %w", metadata.PID, err)
	}
	if err := syscall.Kill(metadata.PID, syscall.SIGTERM); err != nil {
		return fmt.Errorf("stop daemon process %d: %w", metadata.PID, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(metadata.PID, 0); err != nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("daemon process %d did not exit after SIGTERM", metadata.PID)
}

func localDaemonAddress(cfg *config.Config) (string, error) {
	socketPath, err := daemonSocketPath(cfg)
	if err != nil {
		return "", err
	}
	return "unix://" + socketPath, nil
}

func registerLocalDaemonAddress(cfg *config.Config) error {
	socketPath, err := daemonSocketPath(cfg)
	if err != nil {
		return err
	}
	entry := cfg.TUI.Daemons["local"]
	entry.Label = "Local"
	entry.Kind = "local"
	entry.Address = "unix://" + socketPath
	if strings.TrimSpace(entry.TokenRef) == "" {
		entry.TokenRef = "keychain:daemon.local.access_token"
	}
	entry.AutoManaged = true
	if cfg.TUI.Daemons == nil {
		cfg.TUI.Daemons = map[string]config.DaemonRegistryEntry{}
	}
	cfg.TUI.Daemons["local"] = entry
	if cfg.TUI.ActiveDaemon == "" {
		cfg.TUI.ActiveDaemon = "local"
	}
	cfgPath, err := config.ConfigPath()
	if err != nil {
		return err
	}
	if err := config.Save(cfgPath, cfg); err != nil {
		return fmt.Errorf("save local daemon registry entry: %w", err)
	}
	return nil
}

func runTUIClient(args []string) error {
	daemonName, remainingArgs, err := parseDaemonSelectionArg(args)
	if err != nil {
		return err
	}
	if handleCLIArgs(remainingArgs) {
		return nil
	}
	logStore, logToasts := initTUILogging()
	cfg, err := loadRuntimeConfig()
	if err != nil {
		return err
	}
	tuilog.SetDefaultLevelFromConfig(cfg.TUI.UI.LogLevel)
	active := daemonName
	if active == "" {
		active = cfg.TUI.ActiveDaemon
	}
	if active == "" {
		active = "local"
	}
	entry, ok := cfg.TUI.Daemons[active]
	if !ok {
		return fmt.Errorf("active daemon %q is not configured", active)
	}
	token, err := config.DaemonAccessToken(cfg, config.OSKeychainStore{}, active)
	if err != nil {
		return err
	}
	address := entry.Address
	if strings.TrimSpace(address) == "" && entry.Kind == "local" {
		socketPath, socketErr := daemonSocketPath(cfg)
		if socketErr != nil {
			return socketErr
		}
		address = "unix://" + socketPath
	}
	client, err := daemonclient.Dial(context.Background(), address, token)
	if err != nil {
		return fmt.Errorf("dial daemon %q: %w", active, err)
	}
	defer client.Close()
	if _, err := client.Health(context.Background()); err != nil {
		return fmt.Errorf("daemon health: %w", err)
	}
	runtime, err := client.GetRuntimeContext(context.Background())
	if err != nil {
		return fmt.Errorf("daemon runtime context: %w", err)
	}
	runtimeCtx := views.RuntimeContext{
		Cfg:           cfg,
		LogStore:      logStore,
		LogToasts:     logToasts,
		InstanceID:    runtime.InstanceID,
		WorkspaceID:   runtime.WorkspaceID,
		WorkspaceName: runtime.WorkspaceName,
		WorkspaceDir:  runtime.WorkspaceDir,
	}
	return views.RunTUI(views.NewDaemonProvider(client), runtimeCtx)
}

func parseDaemonSelectionArg(args []string) (string, []string, error) {
	remaining := make([]string, 0, len(args))
	daemonName := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--daemon" {
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("--daemon requires a daemon name")
			}
			if daemonName != "" {
				return "", nil, fmt.Errorf("--daemon specified more than once")
			}
			daemonName = strings.TrimSpace(args[i+1])
			if daemonName == "" {
				return "", nil, fmt.Errorf("--daemon requires a daemon name")
			}
			i++
			continue
		}
		if strings.HasPrefix(arg, "--daemon=") {
			if daemonName != "" {
				return "", nil, fmt.Errorf("--daemon specified more than once")
			}
			daemonName = strings.TrimSpace(strings.TrimPrefix(arg, "--daemon="))
			if daemonName == "" {
				return "", nil, fmt.Errorf("--daemon requires a daemon name")
			}
			continue
		}
		remaining = append(remaining, arg)
	}
	return daemonName, remaining, nil
}

func runDaemon(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "stop":
			return stopLocalDaemon()
		default:
			return fmt.Errorf("unknown daemon command %q", args[0])
		}
	}
	cfg, err := loadRuntimeConfig()
	if err != nil {
		return err
	}

	// Install the TUI-aware slog handler before any subsequent slog calls so
	// daemon startup logs are captured in the LogStore and available to the
	// TUI's logs overlay.
	logStore, _ := initTUILogging()
	tuilog.SetDefaultLevelFromConfig(cfg.Daemon.Runtime.LogLevel)

	ctx := context.Background()
	db, err := openDatabase(ctx, cfg.Daemon.Runtime.DatabasePath)
	if err != nil {
		return err
	}
	defer db.Close()

	remote := dbRemote{db}
	eventRepo := sqlite.NewEventRepo(remote)
	transacter := sqlite.NewTransacter(db)

	if err := registerLocalDaemonAddress(cfg); err != nil {
		return err
	}

	if err := config.LoadSecrets(cfg, config.OSKeychainStore{}); err != nil {
		return fmt.Errorf("load config secrets: %w", err)
	}

	startupBus := event.NewBus(event.BusConfig{EventRepo: eventRepo})
	defer startupBus.Close()
	workspaceSvc := service.NewWorkspaceService(transacter, startupBus)
	workspace, markWorkspaceReady, err := inspectStartupWorkspace(ctx, workspaceSvc)
	if err != nil {
		return err
	}

	serviceMgr := logic.NewServiceManager(transacter, eventRepo)
	initialServices := logic.Services{
		WorkspaceID:   workspace.ID,
		WorkspaceName: workspace.Name,
		WorkspaceDir:  workspace.Dir,
	}
	if err := serviceMgr.InitWithServices(ctx, cfg, initialServices); err != nil {
		return fmt.Errorf("init service manager: %w", err)
	}
	instanceID := registerInstance(ctx, serviceMgr.Instance(), workspace.ID)
	if services := serviceMgr.GetServices(); services != nil {
		services.InstanceID = instanceID
	}
	defer serviceMgr.Close(context.Background())
	if markWorkspaceReady {
		if transitionErr := serviceMgr.Workspace().MarkReady(ctx, workspace.ID); transitionErr != nil {
			slog.Warn("workspace found with status creating; failed to transition to ready", "id", workspace.ID, "err", transitionErr)
		}
	}

	token, err := daemonToken(cfg)
	if err != nil {
		return err
	}
	socketPath, err := daemonSocketPath(cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		return fmt.Errorf("create daemon socket directory: %w", err)
	}
	live, err := isSocketLive(socketPath)
	if err != nil {
		return fmt.Errorf("check daemon socket liveness: %w", err)
	}
	if live {
		return fmt.Errorf("refusing to unlink live daemon socket %q: another daemon is listening", socketPath)
	}
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale daemon socket: %w", err)
	}

	listener, err := daemonserver.ListenUnix(socketPath)
	if err != nil {
		return err
	}
	metadata := daemonMetadata{
		SocketPath:    socketPath,
		PID:           os.Getpid(),
		Version:       buildinfo.Version,
		BuildSHA:      buildinfo.BuildSHA,
		BuildTime:     buildinfo.BuildTime,
		SchemaVersion: 1,
		WorkspaceID:   workspace.ID,
		StartedAt:     time.Now().Format(time.RFC3339Nano),
		TokenRef:      cfg.TUI.Daemons["local"].TokenRef,
	}
	if err := writeDaemonMetadata(socketPath, metadata); err != nil {
		return err
	}
	defer os.Remove(metadataPath(socketPath))
	defer listener.Close()

	sessionsDir, err := config.SessionsDir()
	if err != nil {
		return err
	}
	shutdownCh := make(chan struct{})
	var shutdownOnce sync.Once

	api := daemonserver.NewAPI(daemonserver.Options{
		WorkspaceID: workspace.ID,
		InstanceID:  instanceID,
		ShutdownFunc: func() {
			shutdownOnce.Do(func() {
				close(shutdownCh)
			})
		},
		WorkspaceName: workspace.Name,
		WorkspaceDir:  workspace.Dir,
		Token:         token,
		Config:        cfg,
		Harnesses:     serviceMgr.GetServices().Harnesses,
		LogStore:      logStore,
		SessionsDir:   sessionsDir,
		SettingsReloader: func(ctx context.Context, next *config.Config) (*logic.Services, error) {
			current := serviceMgr.GetServices()
			if current == nil {
				return nil, fmt.Errorf("service graph is not initialized")
			}
			return serviceMgr.Rebuild(ctx, next, *current)
		},
		WorkItems:         serviceMgr.Session(),
		Workspaces:        serviceMgr.Workspace(),
		NewSessionFilters: serviceMgr.NewSessionFilters(),
		NewSessionLocks:   serviceMgr.NewSessionFilterLocks(),
		WorkItemAdapters:  serviceMgr.Adapters(),
		Logic:             serviceMgr.Logic(),
		Events:            serviceMgr.Events(),
		Bus:               serviceMgr.Bus(),
		StartedAt:         time.Now(),
	})
	grpcServer := daemonserver.NewGRPCServer(api)
	errCh := make(chan error, 1)
	go func() {
		errCh <- grpcServer.Serve(listener)
	}()
	slog.Info("substrate daemon listening", "socket", socketPath, "workspace_id", workspace.ID)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigCh)
	select {
	case sig := <-sigCh:
		slog.Info("substrate daemon stopping", "signal", sig.String())
		stopGRPCServer(grpcServer)
	case <-shutdownCh:
		slog.Info("substrate daemon stopping", "reason", "shutdown rpc")
		stopGRPCServer(grpcServer)
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("serve daemon grpc: %w", err)
		}
	}
	return nil
}

func stopGRPCServer(server *grpc.Server) {
	done := make(chan struct{})
	go func() {
		server.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		slog.Warn("daemon graceful stop timed out; forcing stop")
		server.Stop()
		<-done
	}
}

func daemonSocketPath(cfg *config.Config) (string, error) {
	if cfg != nil && strings.TrimSpace(cfg.Daemon.Runtime.Bind.SocketPath) != "" {
		return cfg.Daemon.Runtime.Bind.SocketPath, nil
	}
	globalDir, err := config.GlobalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(globalDir, "run", "local.sock"), nil
}

func daemonToken(cfg *config.Config) (string, error) {
	store := config.OSKeychainStore{}
	token, err := config.DaemonAccessToken(cfg, store, "local")
	if err == nil && strings.TrimSpace(token) != "" {
		return token, nil
	}
	tokenBytes := make([]byte, 32)
	if _, readErr := rand.Read(tokenBytes); readErr != nil {
		return "", fmt.Errorf("generate daemon access token: %w", readErr)
	}
	token = hex.EncodeToString(tokenBytes)
	if saveErr := config.SaveDaemonAccessToken(cfg, store, "local", token); saveErr != nil {
		return "", saveErr
	}
	return token, nil
}
func buildInfoString() string {
	parts := []string{buildinfo.Version}
	if strings.TrimSpace(buildinfo.BuildSHA) != "" {
		parts = append(parts, "sha="+buildinfo.BuildSHA)
	}
	if strings.TrimSpace(buildinfo.BuildTime) != "" {
		parts = append(parts, "built="+buildinfo.BuildTime)
	}
	return strings.Join(parts, " ")
}

type daemonMetadata struct {
	SocketPath    string `json:"socket_path"`
	PID           int    `json:"pid"`
	Version       string `json:"version"`
	BuildSHA      string `json:"build_sha"`
	BuildTime     string `json:"build_time"`
	SchemaVersion uint32 `json:"schema_version"`
	WorkspaceID   string `json:"workspace_id"`
	StartedAt     string `json:"started_at"`
	TokenRef      string `json:"token_ref"`
}

func writeDaemonMetadata(socketPath string, metadata daemonMetadata) error {
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("encode daemon metadata: %w", err)
	}
	if err := os.WriteFile(metadataPath(socketPath), data, 0o600); err != nil {
		return fmt.Errorf("write daemon metadata: %w", err)
	}
	return nil
}
func readDaemonMetadata(socketPath string) (daemonMetadata, error) {
	data, err := os.ReadFile(metadataPath(socketPath))
	if err != nil {
		return daemonMetadata{}, fmt.Errorf("read daemon metadata: %w", err)
	}
	var metadata daemonMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return daemonMetadata{}, fmt.Errorf("decode daemon metadata: %w", err)
	}
	return metadata, nil
}

func stopLocalDaemon() error {
	cfg, err := loadRuntimeConfig()
	if err != nil {
		return err
	}
	socketPath, err := daemonSocketPath(cfg)
	if err != nil {
		return err
	}
	metadata, err := readDaemonMetadata(socketPath)
	if err != nil {
		return err
	}
	if metadata.PID <= 0 {
		return fmt.Errorf("daemon metadata has invalid pid %d", metadata.PID)
	}
	if metadata.SocketPath != "" && metadata.SocketPath != socketPath {
		return fmt.Errorf("daemon metadata socket %q does not match configured socket %q", metadata.SocketPath, socketPath)
	}
	if err := syscall.Kill(metadata.PID, 0); err != nil {
		return fmt.Errorf("daemon process %d is not reachable: %w", metadata.PID, err)
	}
	identityCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	token, err := config.DaemonAccessToken(cfg, config.OSKeychainStore{}, "local")
	if err != nil {
		return err
	}
	address := "unix://" + socketPath
	client, err := daemonclient.Dial(identityCtx, address, token)
	if err != nil {
		return fmt.Errorf("verify daemon identity before stop: %w", err)
	}
	defer client.Close()
	runtime, err := client.GetRuntimeContext(identityCtx)
	if err != nil {
		return fmt.Errorf("verify daemon runtime before stop: %w", err)
	}
	if metadata.WorkspaceID != "" && runtime.WorkspaceID != "" && metadata.WorkspaceID != runtime.WorkspaceID {
		return fmt.Errorf("daemon metadata workspace %q does not match live daemon workspace %q", metadata.WorkspaceID, runtime.WorkspaceID)
	}
	if err := syscall.Kill(metadata.PID, syscall.SIGTERM); err != nil {
		return fmt.Errorf("stop daemon process %d: %w", metadata.PID, err)
	}
	return nil
}

func isSocketLive(socketPath string) (bool, error) {
	if _, err := os.Stat(socketPath); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err == nil {
		if closeErr := conn.Close(); closeErr != nil {
			slog.Warn("close daemon socket probe", "error", closeErr)
		}
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return false, nil
	}
	return false, err
}

func metadataPath(socketPath string) string {
	return socketPath + ".json"
}

func handleCLIArgs(args []string) bool {
	if len(args) == 0 {
		return false
	}

	switch args[0] {
	case "--help", "-h", "help":
		printUsage()
		return true
	case "--version", "-v", "version":
		fmt.Println(buildInfoString())
		return true
	default:
		return false
	}
}

func initTUILogging() (*tuilog.Store, chan tuilog.ToastEntry) {
	// Install a TUI-aware slog handler that captures log entries into an
	// in-memory buffer and sends warn/error entries to a channel for toast
	// display. This replaces the default text handler, which would write to
	// stderr and corrupt the bubbletea rendering.
	logStore := tuilog.NewStore()
	logToasts := make(chan tuilog.ToastEntry, 64)
	slog.SetDefault(slog.New(tuilog.NewHandler(logStore, logToasts)))

	return logStore, logToasts
}

func loadRuntimeConfig() (*config.Config, error) {
	globalDir, err := config.GlobalDir()
	if err != nil {
		return nil, fmt.Errorf("getting global directory: %w", err)
	}

	if err := os.MkdirAll(globalDir, 0o750); err != nil {
		return nil, fmt.Errorf("creating global directory: %w", err)
	}

	cfgPath, err := config.ConfigPath()
	if err != nil {
		return nil, fmt.Errorf("getting config path: %w", err)
	}

	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		if err := initializeGlobalConfig(cfgPath); err != nil {
			return nil, fmt.Errorf("initializing global config: %w", err)
		}
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}

	if err := ensureSessionsDir(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func ensureSessionsDir() error {
	// Ensure sessions directory exists. The TUI tails logs from this location at runtime.
	sessionsDir, err := config.SessionsDir()
	if err != nil {
		return fmt.Errorf("getting sessions directory: %w", err)
	}
	if err := os.MkdirAll(sessionsDir, 0o750); err != nil {
		return fmt.Errorf("creating sessions directory: %w", err)
	}

	return nil
}

func openDatabase(ctx context.Context, configuredPath string) (*sqlx.DB, error) {
	dbPath := strings.TrimSpace(configuredPath)
	if dbPath == "" {
		// Fall back to the global default only when the operator has not
		// explicitly configured a per-daemon database path.
		defaultPath, err := config.GlobalDBPath()
		if err != nil {
			return nil, fmt.Errorf("getting database path: %w", err)
		}
		dbPath = defaultPath
	} else {
		// Make sure the parent directory exists so the SQLite open below
		// does not fail on a bare user-supplied path.
		if err := os.MkdirAll(filepath.Dir(dbPath), 0o750); err != nil {
			return nil, fmt.Errorf("creating database directory: %w", err)
		}
	}

	db, err := sqlx.Open("sqlite", dbPath+"?_foreign_keys=1&_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	if err := repository.Migrate(ctx, db, migrations.FS); err != nil {
		db.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	// Harden database file permissions — the file may have been created with
	// the process umask (often 0644); restrict to owner-only access.
	// Must run after migrations so the file exists on first launch.
	if err := os.Chmod(dbPath, 0o600); err != nil {
		slog.Warn("failed to set database permissions", "path", dbPath, "err", err)
	}

	return db, nil
}

func buildCoreServices(
	transacter atomic.Transacter[repository.Resources],
	eventRepo repository.EventRepository,
) coreServices {
	workItemSvc := service.NewSessionService(transacter, nil)
	planSvc := service.NewPlanService(transacter, nil)
	workspaceSvc := service.NewWorkspaceService(transacter, nil)
	sessionSvc := service.NewAgentSessionService(transacter, nil)
	questionSvc := service.NewQuestionService(transacter, nil)
	instanceSvc := service.NewInstanceService(transacter)
	reviewSvc := service.NewReviewService(transacter, nil)
	eventSvc := service.NewEventService(transacter)
	ghPRSvc := service.NewGithubPRService(transacter)
	glMRSvc := service.NewGitlabMRService(transacter)
	sessionArtifactSvc := service.NewSessionReviewArtifactService(transacter)
	ghPRReviewSvc := service.NewGithubPRReviewService(transacter)
	glMRReviewSvc := service.NewGitlabMRReviewService(transacter)
	ghPRCheckSvc := service.NewGithubPRCheckService(transacter)
	glMRCheckSvc := service.NewGitlabMRCheckService(transacter)
	newSessionFilterSvc := service.NewSessionFilterService(transacter)
	newSessionFilterLockSvc := service.NewSessionFilterLockService(transacter)

	// Create ServiceManager for service graph lifecycle
	serviceMgr := views.NewServiceManager(transacter, eventRepo)

	return coreServices{
		workItem:             workItemSvc,
		plan:                 planSvc,
		workspace:            workspaceSvc,
		session:              sessionSvc,
		question:             questionSvc,
		instance:             instanceSvc,
		review:               reviewSvc,
		event:                eventSvc,
		githubPR:             ghPRSvc,
		gitlabMR:             glMRSvc,
		sessionArtifact:      sessionArtifactSvc,
		ghPRReview:           ghPRReviewSvc,
		glMRReview:           glMRReviewSvc,
		ghPRCheck:            ghPRCheckSvc,
		glMRCheck:            glMRCheckSvc,
		newSessionFilter:     newSessionFilterSvc,
		newSessionFilterLock: newSessionFilterLockSvc,
		settings: views.NewSettingsService(
			transacter, config.OSKeychainStore{}, serviceMgr,
		),
	}
}

func inspectStartupWorkspace(ctx context.Context, workspaceSvc *service.WorkspaceService) (workspaceContext, bool, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return workspaceContext{}, false, fmt.Errorf("getting working directory: %w", err)
	}

	wsDir, wsFile, wsErr := gitwork.FindWorkspace(cwd)
	if wsErr != nil {
		if gitwork.IsNotInWorkspace(wsErr) {
			return workspaceContext{}, false, nil
		}
		return workspaceContext{}, false, fmt.Errorf("detecting workspace: %w", wsErr)
	}

	workspace := workspaceContext{
		Dir:  wsDir,
		Name: wsFile.Name,
	}

	ws, err := workspaceSvc.Get(ctx, wsFile.ID)
	if err != nil {
		slog.Warn("workspace file found but not in DB; will prompt init", "id", wsFile.ID, "err", err)
		return workspace, false, nil
	}

	workspace.ID = ws.ID
	workspace.Name = ws.Name

	return workspace, ws.Status == domain.WorkspaceCreating, nil
}

func registerInstance(ctx context.Context, instanceSvc *service.InstanceService, workspaceID string) string {
	if workspaceID == "" {
		return ""
	}

	host, _ := os.Hostname()
	inst := domain.SubstrateInstance{
		ID:          domain.NewID(),
		WorkspaceID: workspaceID,
		PID:         os.Getpid(),
		Hostname:    host,
	}

	if err := instanceSvc.Create(ctx, inst); err != nil {
		slog.Warn("failed to register instance", "err", err)
		return ""
	}

	return inst.ID
}

func buildAdapterSetup(
	ctx context.Context,
	cfg *config.Config,
	workspace workspaceContext,
	services coreServices,
	bus *event.Bus,
) (adapterSetup, error) {
	repos := adapter.ReviewArtifactRepos{
		Events:           services.event,
		GithubPRs:        services.githubPR,
		GitlabMRs:        services.gitlabMR,
		SessionArtifacts: services.sessionArtifact,
		Sessions:         services.workItem,
		GithubPRReviews:  services.ghPRReview,
		GitlabMRReviews:  services.glMRReview,
		GithubPRChecks:   services.ghPRCheck,
		GitlabMRChecks:   services.glMRCheck,
		Bus:              bus,
	}
	githubAdapter, githubWarning := app.BuildGithubAdapter(ctx, cfg, repos)

	var workItemAdapters []adapter.WorkItemAdapter
	var adapterWarnings []string
	if workspace.ID != "" {
		workItemAdapters, adapterWarnings = app.BuildWorkItemAdapters(cfg, workspace.ID, services.workItem, githubAdapter)
	}
	if githubWarning != "" {
		adapterWarnings = append(adapterWarnings, githubWarning)
	}

	repoLifecycleAdapters := app.BuildRepoLifecycleAdapters(
		ctx,
		cfg,
		workspace.Dir,
		repos,
		githubAdapter,
	)
	repoSources := app.BuildRepoSources(ctx, cfg)
	reviewCommentDispatcher := app.BuildReviewCommentFetcher(cfg, workspace.Dir, githubAdapter)

	adapterErrors := make(chan views.AdapterErrorMsg, 16)

	if err := subscribeWorkItemAdapters(bus, workItemAdapters, adapterErrors); err != nil {
		return adapterSetup{}, err
	}
	if err := subscribeRepoLifecycleAdapters(bus, repoLifecycleAdapters, adapterErrors); err != nil {
		return adapterSetup{}, err
	}

	startRepoLifecycleRefresh(ctx, repoLifecycleAdapters, workspace.ID)

	return adapterSetup{
		workItem:       workItemAdapters,
		repoLifecycle:  repoLifecycleAdapters,
		repoSources:    repoSources,
		reviewComments: reviewCommentDispatcher,
		warnings:       adapterWarnings,
		adapterErrors:  adapterErrors,
	}, nil
}

func subscribeWorkItemAdapters(
	bus *event.Bus,
	adapters []adapter.WorkItemAdapter,
	adapterErrors chan<- views.AdapterErrorMsg,
) error {
	return subscribeAdapters(
		bus,
		"work-item-adapter",
		[]domain.EventType{domain.EventPlanApproved, domain.EventWorkItemCompleted, domain.EventPRMerged},
		adapters,
		adapterErrors,
	)
}

func subscribeRepoLifecycleAdapters(
	bus *event.Bus,
	adapters []adapter.RepoLifecycleAdapter,
	adapterErrors chan<- views.AdapterErrorMsg,
) error {
	return subscribeAdapters(
		bus,
		"repo-lifecycle-adapter",
		[]domain.EventType{
			domain.EventWorktreeCreated,
			domain.EventWorktreeReused,
			domain.EventWorkItemCompleted,
			domain.EventSubPlanPRReady,
			domain.EventPRMerged,
			domain.EventPlanApproved,
		},
		adapters,
		adapterErrors,
	)
}

type eventDrivenAdapter interface {
	Name() string
	OnEvent(context.Context, domain.SystemEvent) error
}

func subscribeAdapters[T eventDrivenAdapter](
	bus *event.Bus,
	subscriberPrefix string,
	eventTypes []domain.EventType,
	adapters []T,
	adapterErrors chan<- views.AdapterErrorMsg,
) error {
	for _, candidate := range adapters {
		sub, err := bus.Subscribe(
			subscriberPrefix+":"+candidate.Name(),
			eventTypeStrings(eventTypes)...,
		)
		if err != nil {
			return fmt.Errorf("subscribe %s %s: %w", subscriberPrefix, candidate.Name(), err)
		}

		go runAdapterEventLoop(candidate, bus, sub.C, adapterErrors)
	}

	return nil
}

func eventTypeStrings(eventTypes []domain.EventType) []string {
	values := make([]string, 0, len(eventTypes))
	for _, eventType := range eventTypes {
		values = append(values, string(eventType))
	}

	return values
}

func runAdapterEventLoop[T eventDrivenAdapter](
	adapterInstance T,
	bus *event.Bus,
	events <-chan domain.SystemEvent,
	adapterErrors chan<- views.AdapterErrorMsg,
) {
	for evt := range events {
		if err := handleAdapterEvent(adapterInstance, evt); err != nil {
			slog.Warn(
				"adapter event failed after retries",
				"adapter", adapterInstance.Name(),
				"event", evt.EventType,
				"err", err,
				"retries", 2,
			)
			publishAdapterError(context.Background(), bus, adapterInstance.Name(), evt, err)
			reportAdapterError(adapterErrors, adapterInstance.Name(), evt.EventType, err)
		}
	}
}

func handleAdapterEvent[T eventDrivenAdapter](adapterInstance T, evt domain.SystemEvent) error {
	var lastErr error
	for attempt := range 3 {
		if err := adapterInstance.OnEvent(context.Background(), evt); err != nil {
			lastErr = err
			if attempt < 2 {
				// PermissionError is permanent; retrying will not help.
				if errors.As(lastErr, new(*adapter.PermissionError)) {
					break
				}
				time.Sleep(time.Duration(attempt+1) * time.Second)
			}
			continue
		}
		return nil
	}

	return lastErr
}

func publishAdapterError(ctx context.Context, bus *event.Bus, adapterName string, evt domain.SystemEvent, err error) {
	errPayload := fmt.Sprintf(`{"adapter":%q,"event_type":%q,"error":%q}`, adapterName, evt.EventType, err.Error())
	if pubErr := bus.Publish(ctx, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventAdapterError),
		WorkspaceID: evt.WorkspaceID,
		Payload:     errPayload,
		CreatedAt:   time.Now(),
	}); pubErr != nil {
		slog.Warn("failed to publish adapter error event", "adapter", adapterName, "err", pubErr)
	}
}

func reportAdapterError(
	adapterErrors chan<- views.AdapterErrorMsg,
	adapterName, eventType string,
	err error,
) {
	select {
	case adapterErrors <- views.AdapterErrorMsg{
		Adapter:   adapterName,
		EventType: eventType,
		Err:       err,
		Retries:   2,
	}:
	default:
	}
}

func startRepoLifecycleRefresh(
	ctx context.Context,
	adapters []adapter.RepoLifecycleAdapter,
	workspaceID string,
) {
	if workspaceID == "" {
		return
	}

	type prRefresher interface {
		StartPRRefresh(ctx context.Context, workspaceID string)
	}
	type mrRefresher interface {
		StartMRRefresh(ctx context.Context, workspaceID string)
	}

	for _, lifecycleAdapter := range adapters {
		if refresher, ok := lifecycleAdapter.(prRefresher); ok {
			refresher.StartPRRefresh(ctx, workspaceID)
		}
		if refresher, ok := lifecycleAdapter.(mrRefresher); ok {
			refresher.StartMRRefresh(ctx, workspaceID)
		}
	}
}

// initializeGlobalConfig creates the default config.yaml file.
func initializeGlobalConfig(cfgPath string) error {
	defaultConfig := strings.Join([]string{
		"# Substrate Configuration",
		"# This file was auto-generated with default values.",
		"# All settings have sensible defaults - customize as needed.",
		"#",
		"# commit:",
		"#   strategy: semi-regular",
		"#   message_format: ai-generated",
		"#   message_template: \"feat({{.Scope}}): {{.Description}}\"",
		"#",
		"# plan:",
		"#   max_parse_retries: 2",
		"#",
		"# review:",
		"#   pass_threshold: minor_ok",
		"#   max_cycles: 3",
		"#",
		"# harness:",
		"#   default: ohmypi",
		"#   phase:",
		"#     planning: ohmypi",
		"#     implementation: ohmypi",
		"#     review: ohmypi",
		"#     foreman: ohmypi",
		"#",
		"# foreman:",
		"#   question_timeout: \"0\"",
		"#",
		"# adapters:",
		"#   claude_code:",
		"#     binary_path: claude",
		"#     model: sonnet",
		"#   codex:",
		"#     binary_path: codex",
		"#     model: o4",
		"#     reasoning_effort: high",
		"#   ohmypi:",
		"#     thinking_level: high",
		"#     bun_path: /opt/homebrew/bin/bun",
		"#     bridge_path: /custom/path/to/omp-bridge",
		"#   acp:",
		"#     question_bridge_path: /custom/path/to/question-mcp/index.ts",
		"",
	}, "\n")
	if err := os.WriteFile(cfgPath, []byte(defaultConfig), 0o600); err != nil {
		return fmt.Errorf("writing default config: %w", err)
	}

	fmt.Printf("substrate: created default config at %s\n", cfgPath)

	return nil
}

// dbRemote wraps *sqlx.DB to implement generic.SQLXRemote.
// The repos use generic.SQLXRemote (designed for *sqlx.Tx usage), but for the
// TUI's direct reads/writes outside explicit transactions we delegate all methods
// to *sqlx.DB, which supports them identically except NamedStmtContext (a Tx-only
// method in sqlx that the repos never actually call).
type dbRemote struct{ db *sqlx.DB }

func (r dbRemote) BindNamed(query string, arg any) (string, []any, error) {
	return r.db.BindNamed(query, arg)
}
func (r dbRemote) DriverName() string { return r.db.DriverName() }
func (r dbRemote) GetContext(ctx context.Context, dest any, query string, args ...any) error {
	return r.db.GetContext(ctx, dest, query, args...)
}

func (r dbRemote) MustExecContext(ctx context.Context, query string, args ...any) sql.Result {
	return r.db.MustExecContext(ctx, query, args...)
}

func (r dbRemote) NamedExecContext(ctx context.Context, query string, arg any) (sql.Result, error) {
	return r.db.NamedExecContext(ctx, query, arg)
}

func (r dbRemote) NamedQuery(query string, arg any) (*sqlx.Rows, error) {
	return r.db.NamedQuery(query, arg)
}

// NamedStmtContext returns stmt unchanged. *sqlx.DB has no transaction to bind
// the stmt to; the repos never call this method in practice.
func (r dbRemote) NamedStmtContext(_ context.Context, stmt *sqlx.NamedStmt) *sqlx.NamedStmt {
	return stmt
}

func (r dbRemote) PrepareNamedContext(ctx context.Context, query string) (*sqlx.NamedStmt, error) {
	return r.db.PrepareNamedContext(ctx, query)
}

func (r dbRemote) PreparexContext(ctx context.Context, query string) (*sqlx.Stmt, error) {
	return r.db.PreparexContext(ctx, query)
}

func (r dbRemote) QueryRowxContext(ctx context.Context, query string, args ...any) *sqlx.Row {
	return r.db.QueryRowxContext(ctx, query, args...)
}

func (r dbRemote) QueryxContext(ctx context.Context, query string, args ...any) (*sqlx.Rows, error) {
	return r.db.QueryxContext(ctx, query, args...)
}
func (r dbRemote) Rebind(query string) string { return r.db.Rebind(query) }
func (r dbRemote) SelectContext(ctx context.Context, dest any, query string, args ...any) error {
	return r.db.SelectContext(ctx, dest, query, args...)
}
