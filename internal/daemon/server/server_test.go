package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/config"
	daemonapi "github.com/beeemT/substrate/internal/daemon/api"
	daemonclient "github.com/beeemT/substrate/internal/daemon/client"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/gitwork"
	"github.com/beeemT/substrate/internal/logic"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
	"github.com/beeemT/substrate/internal/sessionlog"
	"github.com/beeemT/substrate/internal/tuilog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type memorySecretStore struct {
	values map[string]string
}

func (s *memorySecretStore) Get(key string) (string, error) {
	return s.values[key], nil
}

func (s *memorySecretStore) Set(key, value string) error {
	s.values[key] = value
	return nil
}

func (s *memorySecretStore) Delete(key string) error {
	delete(s.values, key)
	return nil
}

type daemonWorkspaceRepo struct {
	byID map[string]domain.Workspace
}

func (r *daemonWorkspaceRepo) Get(_ context.Context, id string) (domain.Workspace, error) {
	ws, ok := r.byID[id]
	if !ok {
		return domain.Workspace{}, os.ErrNotExist
	}
	return ws, nil
}

func (r *daemonWorkspaceRepo) Create(_ context.Context, ws domain.Workspace) error {
	if r.byID == nil {
		r.byID = make(map[string]domain.Workspace)
	}
	r.byID[ws.ID] = ws
	return nil
}

func (r *daemonWorkspaceRepo) Update(_ context.Context, ws domain.Workspace) error {
	if r.byID == nil {
		r.byID = make(map[string]domain.Workspace)
	}
	r.byID[ws.ID] = ws
	return nil
}

func (r *daemonWorkspaceRepo) Delete(_ context.Context, id string) error {
	delete(r.byID, id)
	return nil
}

func TestGRPCClientGetsRuntimeContext(t *testing.T) {
	api := NewAPI(Options{WorkspaceID: "ws-1", WorkspaceName: "Workspace", WorkspaceDir: "/tmp/workspace", Token: "secret", StartedAt: time.Now()})
	grpcServer := NewGRPCServer(api)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = grpcServer.Serve(listener) }()
	defer grpcServer.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := daemonclient.Dial(ctx, listener.Addr().String(), "secret")
	if err != nil {
		t.Fatalf("dial with token: %v", err)
	}
	defer client.Close()

	runtimeCtx, err := client.GetRuntimeContext(ctx)
	if err != nil {
		t.Fatalf("GetRuntimeContext() error = %v", err)
	}
	if runtimeCtx.WorkspaceID != "ws-1" || runtimeCtx.WorkspaceName != "Workspace" || runtimeCtx.WorkspaceDir != "/tmp/workspace" {
		t.Fatalf("GetRuntimeContext() = %+v", runtimeCtx)
	}
}

func TestGRPCClientInitializesWorkspace(t *testing.T) {
	workspaceDir := t.TempDir()
	repo := &daemonWorkspaceRepo{byID: make(map[string]domain.Workspace)}
	bus := event.NewBus(event.BusConfig{})
	workspaceSvc := service.NewWorkspaceService(repository.NoopTransacter{Res: repository.Resources{Workspaces: repo}}, bus)
	api := NewAPI(Options{WorkspaceID: "ws-1", WorkspaceDir: workspaceDir, Workspaces: workspaceSvc, Token: "secret", StartedAt: time.Now()})
	grpcServer := NewGRPCServer(api)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = grpcServer.Serve(listener) }()
	defer grpcServer.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := daemonclient.Dial(ctx, listener.Addr().String(), "secret")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	workspace, err := client.InitializeWorkspace(ctx, workspaceDir, "Workspace")
	if err != nil {
		t.Fatalf("InitializeWorkspace: %v", err)
	}
	if workspace.ID == "" || workspace.Name != "Workspace" || workspace.Dir != workspaceDir {
		t.Fatalf("workspace = %#v", workspace)
	}
	stored, ok := repo.byID[workspace.ID]
	if !ok {
		t.Fatalf("workspace %q was not stored", workspace.ID)
	}
	if stored.Status != domain.WorkspaceReady || stored.RootPath != workspaceDir {
		t.Fatalf("stored workspace = %#v", stored)
	}
	if _, err := os.Stat(filepath.Join(workspaceDir, gitwork.WorkspaceFileName)); err != nil {
		t.Fatalf("workspace file was not written: %v", err)
	}
}

func TestRemoveRepoRejectsPathsOutsideWorkspace(t *testing.T) {
	workspaceDir := t.TempDir()
	outsideDir := t.TempDir()
	outsideRepo := filepath.Join(outsideDir, "repo")
	if err := os.Mkdir(outsideRepo, 0o755); err != nil {
		t.Fatalf("mkdir outside repo: %v", err)
	}
	api := NewAPI(Options{WorkspaceID: "ws-1", WorkspaceDir: workspaceDir})

	_, err := api.RemoveRepo(context.Background(), &daemonapi.RemoveRepoRequest{RepoPath: outsideRepo})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("RemoveRepo() status = %v, want InvalidArgument (err=%v)", status.Code(err), err)
	}
	if _, statErr := os.Stat(outsideRepo); statErr != nil {
		t.Fatalf("outside repo was removed or changed: %v", statErr)
	}
}

func TestGRPCClientGetsSettings(t *testing.T) {
	cfg := &config.Config{TUI: config.TUIConfig{
		ActiveDaemon: "local",
		Daemons: map[string]config.DaemonRegistryEntry{
			"local": {Label: "Local", Kind: "local", Address: "unix:///tmp/substrate.sock", TokenRef: "keychain:daemon.local.access_token", AutoManaged: true},
		},
	}}
	api := NewAPI(Options{WorkspaceID: "ws-1", Token: "secret", Config: cfg, StartedAt: time.Now()})
	grpcServer := NewGRPCServer(api)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = grpcServer.Serve(listener) }()
	defer grpcServer.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := daemonclient.Dial(ctx, listener.Addr().String(), "secret")
	if err != nil {
		t.Fatalf("dial with token: %v", err)
	}
	defer client.Close()

	settings, err := client.GetSettings(ctx)
	if err != nil {
		t.Fatalf("GetSettings() error = %v", err)
	}
	if settings.ActiveDaemon != "local" || len(settings.DaemonsJSON) != 1 || !strings.Contains(settings.RawYAML, "daemon:") {
		t.Fatalf("GetSettings() = %+v", settings)
	}
}

func TestGRPCClientRefreshesSettingsDiagnostics(t *testing.T) {
	cfg := &config.Config{Harness: config.HarnessConfig{Default: config.HarnessOhMyPi}}
	api := NewAPI(Options{WorkspaceID: "ws-1", Token: "secret", Config: cfg, StartedAt: time.Now()})
	grpcServer := NewGRPCServer(api)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = grpcServer.Serve(listener) }()
	defer grpcServer.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := daemonclient.Dial(ctx, listener.Addr().String(), "secret")
	if err != nil {
		t.Fatalf("dial with token: %v", err)
	}
	defer client.Close()

	diagnostics, err := client.RefreshProviderDiagnostics(ctx, "")
	if err != nil {
		t.Fatalf("RefreshProviderDiagnostics() error = %v", err)
	}
	if diagnostics.Providers["github"].Title != "GitHub" {
		t.Fatalf("RefreshProviderDiagnostics() providers = %#v", diagnostics.Providers)
	}
}
func TestGRPCClientSaveSettingsRebuildsDaemon(t *testing.T) {
	t.Setenv("SUBSTRATE_HOME", t.TempDir())
	cfg := &config.Config{TUI: config.TUIConfig{ActiveDaemon: "local", Daemons: map[string]config.DaemonRegistryEntry{
		"local": {Label: "Local", Kind: "local", Address: "unix:///tmp/substrate.sock", TokenRef: "keychain:daemon.local.access_token", AutoManaged: true},
	}}}
	var reloaded bool
	api := NewAPI(Options{
		WorkspaceID: "ws-1",
		Token:       "secret",
		Config:      cfg,
		StartedAt:   time.Now(),
		SettingsReloader: func(ctx context.Context, next *config.Config) (*logic.Services, error) {
			reloaded = next.TUI.ActiveDaemon == "local"
			return nil, nil
		},
	})
	grpcServer := NewGRPCServer(api)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = grpcServer.Serve(listener) }()
	defer grpcServer.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := daemonclient.Dial(ctx, listener.Addr().String(), "secret")
	if err != nil {
		t.Fatalf("dial with token: %v", err)
	}
	defer client.Close()

	raw := "tui:\n  active_daemon: local\n  daemons:\n    local:\n      label: Local\n      kind: local\n      address: unix:///tmp/substrate.sock\n      token_ref: keychain:daemon.local.access_token\n      auto_managed: true\n"
	if _, err := client.SaveSettings(ctx, raw, "save-1"); err != nil {
		t.Fatalf("SaveSettings() error = %v", err)
	}
	if !reloaded {
		t.Fatal("settings reloader was not called")
	}
}

func TestGRPCServerHealthRequiresToken(t *testing.T) {
	api := NewAPI(Options{WorkspaceID: "ws-1", Token: "secret", StartedAt: time.Now()})
	grpcServer := NewGRPCServer(api)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = grpcServer.Serve(listener) }()
	defer grpcServer.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	withoutToken, err := daemonclient.Dial(ctx, listener.Addr().String(), "")
	if err != nil {
		t.Fatalf("dial without token: %v", err)
	}
	defer withoutToken.Close()
	if _, err := withoutToken.Health(ctx); err == nil {
		t.Fatal("Health without token succeeded, want authentication error")
	}

	withToken, err := daemonclient.Dial(ctx, listener.Addr().String(), "secret")
	if err != nil {
		t.Fatalf("dial with token: %v", err)
	}
	defer withToken.Close()
	res, err := withToken.Health(ctx)
	if err != nil {
		t.Fatalf("Health with token: %v", err)
	}
	if !res.Ready || res.WorkspaceID != "ws-1" {
		t.Fatalf("Health() = %+v", res)
	}
}

func TestGRPCServerAccessTokenRevealAndRotate(t *testing.T) {
	store := &memorySecretStore{values: map[string]string{}}
	cfg := &config.Config{TUI: config.TUIConfig{Daemons: map[string]config.DaemonRegistryEntry{
		"local": {TokenRef: "keychain:daemon.local.access_token"},
	}}}
	api := NewAPI(Options{
		WorkspaceID: "ws-1",
		Token:       "secret",
		Config:      cfg,
		SecretStore: store,
		StartedAt:   time.Now(),
	})
	grpcServer := NewGRPCServer(api)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = grpcServer.Serve(listener) }()
	defer grpcServer.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := daemonclient.Dial(ctx, listener.Addr().String(), "secret")
	if err != nil {
		t.Fatalf("dial with token: %v", err)
	}
	defer client.Close()

	revealed, err := client.GetAccessToken(ctx)
	if err != nil {
		t.Fatalf("GetAccessToken() error = %v", err)
	}
	if revealed != "secret" {
		t.Fatalf("GetAccessToken() = %q, want old token", revealed)
	}

	rotated, err := client.RotateAccessToken(ctx)
	if err != nil {
		t.Fatalf("RotateAccessToken() error = %v", err)
	}
	if rotated == "" || rotated == "secret" {
		t.Fatalf("rotated token = %q", rotated)
	}
	if got := store.values["daemon.local.access_token"]; got != rotated {
		t.Fatalf("stored token = %q, want rotated token", got)
	}
	if _, err := client.Health(ctx); err != nil {
		t.Fatalf("Health with rotated client token: %v", err)
	}

	oldClient, err := daemonclient.Dial(ctx, listener.Addr().String(), "secret")
	if err != nil {
		t.Fatalf("dial old token client: %v", err)
	}
	defer oldClient.Close()
	if _, err := oldClient.Health(ctx); err == nil {
		t.Fatal("Health with old token succeeded after rotation")
	}
}

func TestGRPCClientSnapshotsAndTailsSessionLog(t *testing.T) {
	sessionsDir := t.TempDir()
	logPath := filepath.Join(sessionsDir, "agent-1.log")
	if err := os.WriteFile(logPath, []byte(`{"type":"event","event":{"type":"assistant_output","text":"alpha"}}`+"\n"), 0o600); err != nil {
		t.Fatalf("write session log: %v", err)
	}
	api := NewAPI(Options{WorkspaceID: "ws-1", Token: "secret", SessionsDir: sessionsDir, StartedAt: time.Now()})
	grpcServer := NewGRPCServer(api)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = grpcServer.Serve(listener) }()
	defer grpcServer.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := daemonclient.Dial(ctx, listener.Addr().String(), "secret")
	if err != nil {
		t.Fatalf("dial with token: %v", err)
	}
	defer client.Close()

	snapshot, err := client.SnapshotAgentSessionLog(ctx, "agent-1")
	if err != nil {
		t.Fatalf("SnapshotAgentSessionLog() error = %v", err)
	}
	if len(snapshot.EntriesJSON) != 1 || snapshot.NextOffset == 0 {
		t.Fatalf("SnapshotAgentSessionLog() = %+v", snapshot)
	}
	var entry sessionlog.Entry
	if err := json.Unmarshal([]byte(snapshot.EntriesJSON[0]), &entry); err != nil {
		t.Fatalf("decode snapshot entry: %v", err)
	}
	if entry.Text != "alpha" {
		t.Fatalf("snapshot entry text = %q, want alpha", entry.Text)
	}

	stream, err := client.TailAgentSessionLog(ctx, daemonapi.TailAgentSessionLogRequest{AgentSessionID: "agent-1", Since: snapshot.NextOffset})
	if err != nil {
		t.Fatalf("TailAgentSessionLog() error = %v", err)
	}
	if err := os.WriteFile(logPath, []byte(`{"type":"event","event":{"type":"assistant_output","text":"alpha"}}`+"\n"+`{"type":"event","event":{"type":"assistant_output","text":"beta"}}`+"\n"), 0o600); err != nil {
		t.Fatalf("append session log: %v", err)
	}
	var batch daemonapi.SessionLogBatch
	if err := stream.RecvMsg(&batch); err != nil {
		t.Fatalf("recv tail batch: %v", err)
	}
	if len(batch.EntriesJSON) != 1 {
		t.Fatalf("tail batch entries = %+v, want one entry", batch.EntriesJSON)
	}
	if err := json.Unmarshal([]byte(batch.EntriesJSON[0]), &entry); err != nil {
		t.Fatalf("decode tail entry: %v", err)
	}
	if entry.Text != "beta" {
		t.Fatalf("tail entry text = %q, want beta", entry.Text)
	}
}

func TestGRPCClientSnapshotsAndTailsAppLog(t *testing.T) {
	store := tuilog.NewStore()
	store.Append(tuilog.Entry{Time: time.Unix(1, 0), Level: slog.LevelInfo, Message: "daemon started"})
	api := NewAPI(Options{WorkspaceID: "ws-1", Token: "secret", LogStore: store, StartedAt: time.Now()})
	grpcServer := NewGRPCServer(api)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = grpcServer.Serve(listener) }()
	defer grpcServer.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := daemonclient.Dial(ctx, listener.Addr().String(), "secret")
	if err != nil {
		t.Fatalf("dial with token: %v", err)
	}
	defer client.Close()

	snapshot, err := client.SnapshotAppLog(ctx)
	if err != nil {
		t.Fatalf("SnapshotAppLog() error = %v", err)
	}
	if len(snapshot.EntriesJSON) != 1 || snapshot.NextOffset != 1 {
		t.Fatalf("SnapshotAppLog() = %+v", snapshot)
	}
	store.Append(tuilog.Entry{Time: time.Unix(2, 0), Level: slog.LevelWarn, Message: "second"})
	stream, err := client.TailAppLog(ctx, daemonapi.TailAppLogRequest{Since: snapshot.NextOffset})
	if err != nil {
		t.Fatalf("TailAppLog() error = %v", err)
	}
	var batch daemonapi.AppLogBatch
	if err := stream.RecvMsg(&batch); err != nil {
		t.Fatalf("recv app log batch: %v", err)
	}
	if len(batch.EntriesJSON) != 1 || batch.NextOffset != 2 {
		t.Fatalf("TailAppLog() = %+v", batch)
	}
}

type countingLogic struct {
	approveCalls int
	sessions     []domain.Session
}

func (c *countingLogic) GetInitialSnapshot(context.Context, string) (logic.InitialSnapshot, error) {
	return logic.InitialSnapshot{Sessions: c.sessions}, nil
}

func (c *countingLogic) ListSessions(context.Context, string) ([]domain.Session, error) {
	return c.sessions, nil
}

func (c *countingLogic) GetSession(_ context.Context, id string) (domain.Session, error) {
	for _, session := range c.sessions {
		if session.ID == id {
			return session, nil
		}
	}
	return domain.Session{}, nil
}
func (c *countingLogic) SearchSessionHistory(context.Context, domain.SessionHistoryFilter) ([]domain.SessionHistoryEntry, error) {
	return nil, nil
}
func (c *countingLogic) ArchiveSession(context.Context, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "archived"}, nil
}

func (c *countingLogic) UnarchiveSession(context.Context, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "unarchived"}, nil
}
func (c *countingLogic) DeleteSession(context.Context, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "deleted"}, nil
}
func (c *countingLogic) OverrideAccept(context.Context, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "accepted"}, nil
}

func (c *countingLogic) FailReview(context.Context, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "failed"}, nil
}

func (c *countingLogic) ApprovePlan(context.Context, string, string) (logic.ActionResult, error) {
	c.approveCalls++
	return logic.ActionResult{Message: "approved"}, nil
}
func (c *countingLogic) RequestPlanChanges(context.Context, string, string, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "revised"}, nil
}

func (c *countingLogic) SaveReviewedPlan(context.Context, string, string) (domain.Plan, []domain.TaskPlan, error) {
	return domain.Plan{}, nil, nil
}

func (c *countingLogic) RunImplementation(context.Context, string) (logic.Operation, error) {
	return logic.Operation{}, nil
}
func (c *countingLogic) StartPlanning(context.Context, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "planning"}, nil
}
func (c *countingLogic) CancelPipeline(context.Context, string, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "cancelled"}, nil
}

func (c *countingLogic) RestartPlanning(context.Context, string, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "planning restarted"}, nil
}
func (c *countingLogic) FollowUpPlan(context.Context, string, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "follow-up plan"}, nil
}

func (c *countingLogic) FinalizeWorkItem(context.Context, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "finalize"}, nil
}

func (c *countingLogic) RetryFailedWorkItem(context.Context, string, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "retry failed"}, nil
}

func (c *countingLogic) ResumeAllSessionsForWorkItem(context.Context, string, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "resume all"}, nil
}

func (c *countingLogic) RetryAgentSession(context.Context, string, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "retry"}, nil
}
func (c *countingLogic) FollowUpAgentSession(context.Context, string, string, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "follow-up"}, nil
}

func (c *countingLogic) SteerSession(context.Context, string, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "steered"}, nil
}

func (c *countingLogic) AnswerQuestion(context.Context, string, string, string) (logic.ActionResult, error) {
	return logic.ActionResult{}, nil
}
func (c *countingLogic) SkipQuestion(context.Context, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "skipped"}, nil
}

func TestMutatingRPCIdempotencyKeyCachesResponse(t *testing.T) {
	logicClient := &countingLogic{}
	api := NewAPI(Options{Logic: logicClient})
	req := &daemonapi.ApprovePlanRequest{PlanID: "plan-1", WorkItemID: "work-1", IdempotencyKey: "same-click"}
	if _, err := api.ApprovePlan(context.Background(), req); err != nil {
		t.Fatalf("first ApprovePlan: %v", err)
	}
	if _, err := api.ApprovePlan(context.Background(), req); err != nil {
		t.Fatalf("second ApprovePlan: %v", err)
	}
	if logicClient.approveCalls != 1 {
		t.Fatalf("approveCalls = %d, want 1", logicClient.approveCalls)
	}
}

func TestGRPCClientGetsSnapshotSessions(t *testing.T) {
	logicClient := &countingLogic{sessions: []domain.Session{{ID: "work-1", WorkspaceID: "ws-1", Title: "Fix bug"}}}
	api := NewAPI(Options{WorkspaceID: "ws-1", Token: "secret", Logic: logicClient})
	grpcServer := NewGRPCServer(api)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = grpcServer.Serve(listener) }()
	defer grpcServer.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := daemonclient.Dial(ctx, listener.Addr().String(), "secret")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()
	sessions, err := client.ListSessions(ctx, "ws-1")
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != "work-1" {
		t.Fatalf("sessions = %#v", sessions)
	}
}

func TestGRPCClientGetsSession(t *testing.T) {
	logicClient := &countingLogic{sessions: []domain.Session{{ID: "work-1", WorkspaceID: "ws-1", Title: "Fix bug"}}}
	api := NewAPI(Options{WorkspaceID: "ws-1", Token: "secret", Logic: logicClient})
	grpcServer := NewGRPCServer(api)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = grpcServer.Serve(listener) }()
	defer grpcServer.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := daemonclient.Dial(ctx, listener.Addr().String(), "secret")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()
	session, err := client.GetSession(ctx, "work-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if session.ID != "work-1" {
		t.Fatalf("session = %#v", session)
	}
}

func TestGRPCClientGetsReadModels(t *testing.T) {
	logicClient := &countingLogic{sessions: []domain.Session{{ID: "work-1", WorkspaceID: "ws-1", Title: "Fix bug", State: domain.SessionPlanReview, UpdatedAt: time.Unix(1, 0)}}}
	api := NewAPI(Options{WorkspaceID: "ws-1", Token: "secret", Logic: logicClient})
	grpcServer := NewGRPCServer(api)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = grpcServer.Serve(listener) }()
	defer grpcServer.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := daemonclient.Dial(ctx, listener.Addr().String(), "secret")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()
	sidebar, err := client.GetSidebar(ctx, "ws-1")
	if err != nil {
		t.Fatalf("GetSidebar: %v", err)
	}
	if len(sidebar.Entries) == 0 || sidebar.Entries[0].WorkItemID != "work-1" {
		t.Fatalf("sidebar = %#v", sidebar.Entries)
	}
	overview, err := client.GetSessionOverview(ctx, "ws-1", "work-1")
	if err != nil {
		t.Fatalf("GetSessionOverview: %v", err)
	}
	if overview.Overview.WorkItemID != "work-1" {
		t.Fatalf("overview = %#v", overview.Overview)
	}
	actions, err := client.GetAvailableActions(ctx, "ws-1", "work-1")
	if err != nil {
		t.Fatalf("GetAvailableActions: %v", err)
	}
	if actions.Actions == nil {
		t.Fatalf("actions = nil")
	}
	planView, err := client.GetPlan(ctx, "ws-1", "work-1")
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if planView.PayloadJSON == "" {
		t.Fatal("GetPlan payload is empty")
	}
	artifacts, err := client.GetArtifacts(ctx, "ws-1", "work-1")
	if err != nil {
		t.Fatalf("GetArtifacts: %v", err)
	}
	if artifacts.PayloadJSON == "" {
		t.Fatal("GetArtifacts payload is empty")
	}

	approve, err := client.ApprovePlan(ctx, "plan-1", "work-1")
	if err != nil {
		t.Fatalf("ApprovePlan: %v", err)
	}
	if approve.Message != "approved" || logicClient.approveCalls != 1 {
		t.Fatalf("ApprovePlan result = %#v calls=%d", approve, logicClient.approveCalls)
	}
}

func TestGRPCClientControlsAutonomousMode(t *testing.T) {
	api := NewAPI(Options{WorkspaceID: "ws-1", Token: "secret", StartedAt: time.Now()})
	grpcServer := NewGRPCServer(api)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = grpcServer.Serve(listener) }()
	defer grpcServer.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := daemonclient.Dial(ctx, listener.Addr().String(), "secret")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()
	run, err := client.StartAutonomousMode(ctx, daemonapi.StartAutonomousModeRequest{
		WorkspaceID:       "ws-1",
		InstanceID:        "instance-1",
		SelectedFilterIDs: []string{"filter-1"},
		IdempotencyKey:    "start-1",
	})
	if err != nil {
		t.Fatalf("StartAutonomousMode: %v", err)
	}
	if !run.Running || run.InstanceID != "instance-1" || len(run.ActiveFilterIDs) != 1 {
		t.Fatalf("run = %#v", run)
	}
	status, err := client.GetAutonomousModeStatus(ctx, "instance-1")
	if err != nil {
		t.Fatalf("GetAutonomousModeStatus: %v", err)
	}
	if !status.Running || status.ActiveCount != 1 {
		t.Fatalf("status = %#v", status)
	}
	status, err = client.StopAutonomousMode(ctx, daemonapi.StopAutonomousModeRequest{InstanceID: "instance-1", IdempotencyKey: "stop-1"})
	if err != nil {
		t.Fatalf("StopAutonomousMode: %v", err)
	}
	if status.Running || status.ActiveCount != 0 {
		t.Fatalf("stopped status = %#v", status)
	}
}
