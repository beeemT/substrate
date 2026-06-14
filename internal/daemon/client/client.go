package client

import (
	"context"

	daemonapi "github.com/beeemT/substrate/internal/daemon/api"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/logic"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/metadata"
)

type Client struct {
	conn  *grpc.ClientConn
	token string
}

func Dial(ctx context.Context, target, token string) (*Client, error) {
	conn, err := grpc.NewClient(
		target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(encoding.GetCodec("json"))),
	)
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn, token: token}, nil
}

func (c *Client) Close() error { return c.conn.Close() }

func (c *Client) context(ctx context.Context) context.Context {
	if c.token == "" {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+c.token)
}

func (c *Client) Health(ctx context.Context) (*daemonapi.HealthResponse, error) {
	var out daemonapi.HealthResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.SystemAPI/Health", &daemonapi.HealthRequest{}, &out)
	return &out, err
}

func (c *Client) GetRuntimeContext(ctx context.Context) (*daemonapi.RuntimeContext, error) {
	var out daemonapi.RuntimeContext
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.WorkspaceAPI/GetRuntimeContext", &daemonapi.GetRuntimeContextRequest{}, &out)
	return &out, err
}

func (c *Client) GetSettings(ctx context.Context) (*daemonapi.GetSettingsResponse, error) {
	var out daemonapi.GetSettingsResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.SettingsAPI/GetSettings", &daemonapi.GetSettingsRequest{}, &out)
	return &out, err
}

func (c *Client) SaveSettings(ctx context.Context, rawYAML, idempotencyKey string) (*daemonapi.SaveSettingsResponse, error) {
	var out daemonapi.SaveSettingsResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.SettingsAPI/SaveSettings", &daemonapi.SaveSettingsRequest{RawYAML: rawYAML, IdempotencyKey: idempotencyKey}, &out)
	return &out, err
}

func (c *Client) TestProvider(ctx context.Context, provider, rawYAML string) (*daemonapi.ProviderStatus, error) {
	var out daemonapi.ProviderStatus
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.SettingsAPI/TestProvider", &daemonapi.TestProviderRequest{Provider: provider, RawYAML: rawYAML}, &out)
	return &out, err
}

func (c *Client) LoginProvider(ctx context.Context, provider, harness, rawYAML string) (*daemonapi.LoginProviderResponse, error) {
	var out daemonapi.LoginProviderResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.SettingsAPI/LoginProvider", &daemonapi.LoginProviderRequest{Provider: provider, Harness: harness, RawYAML: rawYAML}, &out)
	return &out, err
}

func (c *Client) RefreshProviderDiagnostics(ctx context.Context, rawYAML string) (*daemonapi.RefreshProviderDiagnosticsResponse, error) {
	var out daemonapi.RefreshProviderDiagnosticsResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.SettingsAPI/RefreshProviderDiagnostics", &daemonapi.RefreshProviderDiagnosticsRequest{RawYAML: rawYAML}, &out)
	return &out, err
}

func (c *Client) InitializeWorkspace(ctx context.Context, dir, name string) (*daemonapi.Workspace, error) {
	var out daemonapi.Workspace
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.WorkspaceAPI/InitializeWorkspace", &daemonapi.InitializeWorkspaceRequest{Dir: dir, Name: name}, &out)
	return &out, err
}

func (c *Client) HealthCheckWorkspace(ctx context.Context, dir string) (*daemonapi.WorkspaceHealth, error) {
	var out daemonapi.WorkspaceHealth
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.WorkspaceAPI/HealthCheckWorkspace", &daemonapi.HealthCheckWorkspaceRequest{Dir: dir}, &out)
	return &out, err
}

func (c *Client) ListManagedRepos(ctx context.Context, workspaceDir string) (*daemonapi.ListManagedReposResponse, error) {
	var out daemonapi.ListManagedReposResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.WorkspaceAPI/ListManagedRepos", &daemonapi.ListManagedReposRequest{WorkspaceDir: workspaceDir}, &out)
	return &out, err
}

func (c *Client) GetSessionOverview(ctx context.Context, workspaceID, sessionID string) (*daemonapi.GetSessionOverviewResponse, error) {
	var out daemonapi.GetSessionOverviewResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.ReadModelAPI/GetSessionOverview", &daemonapi.GetSessionOverviewRequest{WorkspaceID: workspaceID, SessionID: sessionID}, &out)
	return &out, err
}

func (c *Client) GetSidebar(ctx context.Context, workspaceID string) (*daemonapi.GetSidebarResponse, error) {
	var out daemonapi.GetSidebarResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.ReadModelAPI/GetSidebar", &daemonapi.GetSidebarRequest{WorkspaceID: workspaceID}, &out)
	return &out, err
}
func (c *Client) GetPlan(ctx context.Context, workspaceID, sessionID string) (*daemonapi.PlanView, error) {
	var out daemonapi.PlanView
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.ReadModelAPI/GetPlan", &daemonapi.GetPlanRequest{WorkspaceID: workspaceID, SessionID: sessionID}, &out)
	return &out, err
}

func (c *Client) GetArtifacts(ctx context.Context, workspaceID, sessionID string) (*daemonapi.ArtifactsView, error) {
	var out daemonapi.ArtifactsView
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.ReadModelAPI/GetArtifacts", &daemonapi.GetArtifactsRequest{WorkspaceID: workspaceID, SessionID: sessionID}, &out)
	return &out, err
}

func (c *Client) GetAvailableActions(ctx context.Context, workspaceID, sessionID string) (*daemonapi.GetAvailableActionsResponse, error) {
	var out daemonapi.GetAvailableActionsResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.ReadModelAPI/GetAvailableActions", &daemonapi.GetAvailableActionsRequest{WorkspaceID: workspaceID, SessionID: sessionID}, &out)
	return &out, err
}

func (c *Client) StartAutonomousMode(ctx context.Context, req daemonapi.StartAutonomousModeRequest) (*daemonapi.AutonomousModeRun, error) {
	var out daemonapi.AutonomousModeRun
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.AutonomousModeAPI/StartAutonomousMode", &req, &out)
	return &out, err
}

func (c *Client) StopAutonomousMode(ctx context.Context, req daemonapi.StopAutonomousModeRequest) (*daemonapi.AutonomousModeStatusResponse, error) {
	var out daemonapi.AutonomousModeStatusResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.AutonomousModeAPI/StopAutonomousMode", &req, &out)
	return &out, err
}

func (c *Client) GetAutonomousModeStatus(ctx context.Context, instanceID string) (*daemonapi.AutonomousModeStatusResponse, error) {
	var out daemonapi.AutonomousModeStatusResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.AutonomousModeAPI/GetAutonomousModeStatus", &daemonapi.GetAutonomousModeStatusRequest{InstanceID: instanceID}, &out)
	return &out, err
}

func (c *Client) ListWorktrees(ctx context.Context, repoPath string) (*daemonapi.ListWorktreesResponse, error) {
	var out daemonapi.ListWorktreesResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.WorkspaceAPI/ListWorktrees", &daemonapi.ListWorktreesRequest{RepoPath: repoPath}, &out)
	return &out, err
}

func (c *Client) CloneRepo(ctx context.Context, cloneDir, cloneURL string) (*daemonapi.CloneRepoResponse, error) {
	var out daemonapi.CloneRepoResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.WorkspaceAPI/CloneRepo", &daemonapi.CloneRepoRequest{CloneDir: cloneDir, CloneURL: cloneURL}, &out)
	return &out, err
}

func (c *Client) InitRepo(ctx context.Context, repoPath string) (*daemonapi.InitRepoResponse, error) {
	var out daemonapi.InitRepoResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.WorkspaceAPI/InitRepo", &daemonapi.InitRepoRequest{RepoPath: repoPath}, &out)
	return &out, err
}

func (c *Client) RemoveRepo(ctx context.Context, repoPath string) (*daemonapi.RemoveRepoResponse, error) {
	var out daemonapi.RemoveRepoResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.WorkspaceAPI/RemoveRepo", &daemonapi.RemoveRepoRequest{RepoPath: repoPath}, &out)
	return &out, err
}

func (c *Client) GetAccessToken(ctx context.Context) (string, error) {
	var out daemonapi.GetAccessTokenResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.SystemAPI/GetAccessToken", &daemonapi.GetAccessTokenRequest{}, &out)
	return out.Token, err
}

func (c *Client) SnapshotAppLog(ctx context.Context) (*daemonapi.AppLogSnapshot, error) {
	var out daemonapi.AppLogSnapshot
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.LogAPI/SnapshotAppLog", &daemonapi.SnapshotAppLogRequest{}, &out)
	return &out, err
}

func (c *Client) TailAppLog(ctx context.Context, req daemonapi.TailAppLogRequest) (grpc.ClientStream, error) {
	desc := &grpc.StreamDesc{ServerStreams: true}
	stream, err := c.conn.NewStream(c.context(ctx), desc, "/substrate.v1.LogAPI/TailAppLog")
	if err != nil {
		return nil, err
	}
	if err := stream.SendMsg(&req); err != nil {
		return nil, err
	}
	if err := stream.CloseSend(); err != nil {
		return nil, err
	}
	return stream, nil
}

func (c *Client) RotateAccessToken(ctx context.Context) (string, error) {
	var out daemonapi.RotateAccessTokenResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.SystemAPI/RotateAccessToken", &daemonapi.RotateAccessTokenRequest{}, &out)
	if err == nil {
		c.token = out.Token
	}
	return out.Token, err
}

func (c *Client) SnapshotAgentSessionLog(ctx context.Context, agentSessionID string) (*daemonapi.SessionLogSnapshot, error) {
	var out daemonapi.SessionLogSnapshot
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.LogAPI/SnapshotAgentSessionLog", &daemonapi.SnapshotAgentSessionLogRequest{AgentSessionID: agentSessionID}, &out)
	return &out, err
}

func (c *Client) TailAgentSessionLog(ctx context.Context, req daemonapi.TailAgentSessionLogRequest) (grpc.ClientStream, error) {
	desc := &grpc.StreamDesc{ServerStreams: true}
	stream, err := c.conn.NewStream(c.context(ctx), desc, "/substrate.v1.LogAPI/TailAgentSessionLog")
	if err != nil {
		return nil, err
	}
	if err := stream.SendMsg(&req); err != nil {
		return nil, err
	}
	if err := stream.CloseSend(); err != nil {
		return nil, err
	}
	return stream, nil
}

func (c *Client) SnapshotResponse(ctx context.Context, workspaceID string) (*daemonapi.GetInitialSnapshotResponse, error) {
	return c.snapshot(ctx, workspaceID)
}

func (c *Client) SubscribeEvents(ctx context.Context, req daemonapi.SubscribeEventsRequest) (grpc.ClientStream, error) {
	desc := &grpc.StreamDesc{ServerStreams: true}
	stream, err := c.conn.NewStream(c.context(ctx), desc, "/substrate.v1.EventStreamAPI/Subscribe")
	if err != nil {
		return nil, err
	}
	if err := stream.SendMsg(&req); err != nil {
		return nil, err
	}
	if err := stream.CloseSend(); err != nil {
		return nil, err
	}
	return stream, nil
}

// Verify Client implements the product logic client over gRPC.
var _ logic.Client = (*Client)(nil)

func (c *Client) ListSessions(ctx context.Context, workspaceID string) ([]domain.Session, error) {
	snapshot, err := c.GetInitialSnapshot(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	return snapshot.Sessions, nil
}

func (c *Client) GetSession(ctx context.Context, sessionID string) (domain.Session, error) {
	var out daemonapi.GetSessionResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.SessionAPI/GetSession", &daemonapi.GetSessionRequest{SessionID: sessionID}, &out)
	return out.Session, err
}

func (c *Client) SearchSessionHistory(ctx context.Context, filter domain.SessionHistoryFilter) ([]domain.SessionHistoryEntry, error) {
	var out daemonapi.SearchSessionHistoryResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.SessionAPI/SearchSessionHistory", &daemonapi.SearchSessionHistoryRequest{Filter: filter}, &out)
	return out.Entries, err
}

func (c *Client) ArchiveSession(ctx context.Context, workItemID string) (logic.ActionResult, error) {
	var out daemonapi.ActionResultResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.SessionAPI/ArchiveSession", &daemonapi.ArchiveSessionRequest{SessionID: workItemID}, &out)
	return logic.ActionResult{Message: out.Message}, err
}

func (c *Client) UnarchiveSession(ctx context.Context, workItemID string) (logic.ActionResult, error) {
	var out daemonapi.ActionResultResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.SessionAPI/UnarchiveSession", &daemonapi.UnarchiveSessionRequest{SessionID: workItemID}, &out)
	return logic.ActionResult{Message: out.Message}, err
}

func (c *Client) DeleteSession(ctx context.Context, workItemID string) (logic.ActionResult, error) {
	var out daemonapi.ActionResultResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.SessionAPI/DeleteSession", &daemonapi.DeleteSessionRequest{SessionID: workItemID}, &out)
	return logic.ActionResult{Message: out.Message}, err
}

func (c *Client) OverrideAccept(ctx context.Context, workItemID string) (logic.ActionResult, error) {
	var out daemonapi.ActionResultResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.SessionAPI/OverrideAccept", &daemonapi.OverrideAcceptRequest{WorkItemID: workItemID}, &out)
	return logic.ActionResult{Message: out.Message}, err
}

func (c *Client) FailReview(ctx context.Context, workItemID string) (logic.ActionResult, error) {
	var out daemonapi.ActionResultResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.SessionAPI/FailReview", &daemonapi.FailReviewRequest{WorkItemID: workItemID}, &out)
	return logic.ActionResult{Message: out.Message}, err
}

func (c *Client) ApprovePlan(ctx context.Context, planID, workItemID string) (logic.ActionResult, error) {
	var out daemonapi.ActionResultResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.SessionAPI/ApprovePlan", &daemonapi.ApprovePlanRequest{PlanID: planID, WorkItemID: workItemID}, &out)
	return logic.ActionResult{Message: out.Message}, err
}

func (c *Client) RequestPlanChanges(ctx context.Context, workItemID, planID, feedback string) (logic.ActionResult, error) {
	var out daemonapi.ActionResultResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.SessionAPI/RequestPlanChanges", &daemonapi.RequestPlanChangesRequest{WorkItemID: workItemID, PlanID: planID, Feedback: feedback}, &out)
	return logic.ActionResult{Message: out.Message}, err
}

func (c *Client) SaveReviewedPlan(ctx context.Context, planID, content string) (domain.Plan, []domain.TaskPlan, error) {
	var out daemonapi.SaveReviewedPlanResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.SessionAPI/SaveReviewedPlan", &daemonapi.SaveReviewedPlanRequest{PlanID: planID, Content: content}, &out)
	return out.Plan, out.SubPlans, err
}

func (c *Client) RunImplementation(ctx context.Context, planID string) (logic.Operation, error) {
	var out daemonapi.OperationResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.SessionAPI/RunImplementation", &daemonapi.RunImplementationRequest{PlanID: planID}, &out)
	return out.Operation, err
}

func (c *Client) StartPlanning(ctx context.Context, workItemID string) (logic.ActionResult, error) {
	var out daemonapi.ActionResultResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.SessionAPI/StartPlanning", &daemonapi.StartPlanningRequest{WorkItemID: workItemID}, &out)
	return logic.ActionResult{Message: out.Message}, err
}
func (c *Client) CancelPipeline(ctx context.Context, workItemID, agentSessionID string) (logic.ActionResult, error) {
	var out daemonapi.ActionResultResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.AgentSessionAPI/CancelPipeline", &daemonapi.CancelPipelineRequest{WorkItemID: workItemID, AgentSessionID: agentSessionID}, &out)
	return logic.ActionResult{Message: out.Message}, err
}

func (c *Client) RestartPlanning(ctx context.Context, workItemID, prompt string) (logic.ActionResult, error) {
	var out daemonapi.ActionResultResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.SessionAPI/RestartPlanning", &daemonapi.RestartPlanningRequest{WorkItemID: workItemID, Prompt: prompt}, &out)
	return logic.ActionResult{Message: out.Message}, err
}

func (c *Client) FollowUpPlan(ctx context.Context, workItemID, feedback string) (logic.ActionResult, error) {
	var out daemonapi.ActionResultResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.SessionAPI/FollowUpPlan", &daemonapi.FollowUpPlanRequest{WorkItemID: workItemID, Feedback: feedback}, &out)
	return logic.ActionResult{Message: out.Message}, err
}

func (c *Client) FinalizeWorkItem(ctx context.Context, workItemID string) (logic.ActionResult, error) {
	var out daemonapi.ActionResultResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.SessionAPI/FinalizeSession", &daemonapi.FinalizeSessionRequest{WorkItemID: workItemID}, &out)
	return logic.ActionResult{Message: out.Message}, err
}

func (c *Client) RetryFailedWorkItem(ctx context.Context, planID, workItemID string) (logic.ActionResult, error) {
	var out daemonapi.ActionResultResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.SessionAPI/RetryFailedSession", &daemonapi.RetryFailedSessionRequest{PlanID: planID, WorkItemID: workItemID}, &out)
	return logic.ActionResult{Message: out.Message}, err
}

func (c *Client) ResumeAllSessionsForWorkItem(ctx context.Context, workItemID, instanceID string) (logic.ActionResult, error) {
	var out daemonapi.ActionResultResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.SessionAPI/ResumeAllForSession", &daemonapi.ResumeAllForSessionRequest{WorkItemID: workItemID, InstanceID: instanceID}, &out)
	return logic.ActionResult{Message: out.Message}, err
}

func (c *Client) RetryAgentSession(ctx context.Context, sessionID, instanceID string) (logic.ActionResult, error) {
	var out daemonapi.ActionResultResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.SessionAPI/RetryAgentSession", &daemonapi.RetryAgentSessionRequest{AgentSessionID: sessionID, InstanceID: instanceID}, &out)
	return logic.ActionResult{Message: out.Message}, err
}

func (c *Client) FollowUpAgentSession(ctx context.Context, sessionID, feedback, instanceID string) (logic.ActionResult, error) {
	var out daemonapi.ActionResultResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.SessionAPI/FollowUpAgentSession", &daemonapi.FollowUpAgentSessionRequest{AgentSessionID: sessionID, Feedback: feedback, InstanceID: instanceID}, &out)
	return logic.ActionResult{Message: out.Message}, err
}

func (c *Client) SteerSession(ctx context.Context, sessionID, message string) (logic.ActionResult, error) {
	var out daemonapi.ActionResultResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.SessionAPI/SteerSession", &daemonapi.SteerSessionRequest{AgentSessionID: sessionID, Message: message}, &out)
	return logic.ActionResult{Message: out.Message}, err
}

func (c *Client) AnswerQuestion(ctx context.Context, questionID, answer, answeredBy string) (logic.ActionResult, error) {
	var out daemonapi.ActionResultResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.SessionAPI/AnswerQuestion", &daemonapi.AnswerQuestionRequest{QuestionID: questionID, Answer: answer, AnsweredBy: answeredBy}, &out)
	return logic.ActionResult{Message: out.Message}, err
}

func (c *Client) SkipQuestion(ctx context.Context, questionID string) (logic.ActionResult, error) {
	var out daemonapi.ActionResultResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.SessionAPI/SkipQuestion", &daemonapi.SkipQuestionRequest{QuestionID: questionID}, &out)
	return logic.ActionResult{Message: out.Message}, err
}

func (c *Client) GetInitialSnapshot(ctx context.Context, workspaceID string) (logic.InitialSnapshot, error) {
	res, err := c.snapshot(ctx, workspaceID)
	if err != nil {
		return logic.InitialSnapshot{}, err
	}
	return res.Snapshot, nil
}

func (c *Client) snapshot(ctx context.Context, workspaceID string) (*daemonapi.GetInitialSnapshotResponse, error) {
	var out daemonapi.GetInitialSnapshotResponse
	err := c.conn.Invoke(c.context(ctx), "/substrate.v1.SessionAPI/GetInitialSnapshot", &daemonapi.GetInitialSnapshotRequest{WorkspaceID: workspaceID}, &out)
	return &out, err
}
