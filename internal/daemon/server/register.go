package server

import (
	"context"

	daemonapi "github.com/beeemT/substrate/internal/daemon/api"
	"google.golang.org/grpc"
)

type SystemAPIServer interface {
	Health(context.Context, *daemonapi.HealthRequest) (*daemonapi.HealthResponse, error)
	Info(context.Context, *daemonapi.InfoRequest) (*daemonapi.InfoResponse, error)
	Disconnect(context.Context, *daemonapi.DisconnectRequest) (*daemonapi.DisconnectResponse, error)
	Shutdown(context.Context, *daemonapi.ShutdownRequest) (*daemonapi.ShutdownResponse, error)
	GetAccessToken(context.Context, *daemonapi.GetAccessTokenRequest) (*daemonapi.GetAccessTokenResponse, error)
	RotateAccessToken(context.Context, *daemonapi.RotateAccessTokenRequest) (*daemonapi.RotateAccessTokenResponse, error)
}

type SessionAPIServer interface {
	GetInitialSnapshot(context.Context, *daemonapi.GetInitialSnapshotRequest) (*daemonapi.GetInitialSnapshotResponse, error)
	ListSessions(context.Context, *daemonapi.ListSessionsRequest) (*daemonapi.ListSessionsResponse, error)
	GetSession(context.Context, *daemonapi.GetSessionRequest) (*daemonapi.GetSessionResponse, error)
	SearchSessionHistory(context.Context, *daemonapi.SearchSessionHistoryRequest) (*daemonapi.SearchSessionHistoryResponse, error)
	ArchiveSession(context.Context, *daemonapi.ArchiveSessionRequest) (*daemonapi.ActionResultResponse, error)
	UnarchiveSession(context.Context, *daemonapi.UnarchiveSessionRequest) (*daemonapi.ActionResultResponse, error)
	DeleteSession(context.Context, *daemonapi.DeleteSessionRequest) (*daemonapi.ActionResultResponse, error)
	OverrideAccept(context.Context, *daemonapi.OverrideAcceptRequest) (*daemonapi.ActionResultResponse, error)
	FailReview(context.Context, *daemonapi.FailReviewRequest) (*daemonapi.ActionResultResponse, error)
	ApprovePlan(context.Context, *daemonapi.ApprovePlanRequest) (*daemonapi.ActionResultResponse, error)
	RequestPlanChanges(context.Context, *daemonapi.RequestPlanChangesRequest) (*daemonapi.ActionResultResponse, error)
	SaveReviewedPlan(context.Context, *daemonapi.SaveReviewedPlanRequest) (*daemonapi.SaveReviewedPlanResponse, error)
	RunImplementation(context.Context, *daemonapi.RunImplementationRequest) (*daemonapi.OperationResponse, error)
	StartPlanning(context.Context, *daemonapi.StartPlanningRequest) (*daemonapi.ActionResultResponse, error)
	RestartPlanning(context.Context, *daemonapi.RestartPlanningRequest) (*daemonapi.ActionResultResponse, error)
	FollowUpPlan(context.Context, *daemonapi.FollowUpPlanRequest) (*daemonapi.ActionResultResponse, error)
	FinalizeSession(context.Context, *daemonapi.FinalizeSessionRequest) (*daemonapi.ActionResultResponse, error)
	RetryFailedSession(context.Context, *daemonapi.RetryFailedSessionRequest) (*daemonapi.ActionResultResponse, error)
	ResumeAllForSession(context.Context, *daemonapi.ResumeAllForSessionRequest) (*daemonapi.ActionResultResponse, error)
	RetryAgentSession(context.Context, *daemonapi.RetryAgentSessionRequest) (*daemonapi.ActionResultResponse, error)
	FollowUpAgentSession(context.Context, *daemonapi.FollowUpAgentSessionRequest) (*daemonapi.ActionResultResponse, error)
	SteerSession(context.Context, *daemonapi.SteerSessionRequest) (*daemonapi.ActionResultResponse, error)
	AnswerQuestion(context.Context, *daemonapi.AnswerQuestionRequest) (*daemonapi.ActionResultResponse, error)
	SkipQuestion(context.Context, *daemonapi.SkipQuestionRequest) (*daemonapi.ActionResultResponse, error)
}

type AgentSessionAPIServer interface {
	ListAgentSessions(context.Context, *daemonapi.ListAgentSessionsRequest) (*daemonapi.ListAgentSessionsResponse, error)
	SearchHistory(context.Context, *daemonapi.SearchHistoryRequest) (*daemonapi.SearchHistoryResponse, error)
	GetInteraction(context.Context, *daemonapi.GetInteractionRequest) (*daemonapi.GetInteractionResponse, error)
	AnswerQuestion(context.Context, *daemonapi.AnswerQuestionRequest) (*daemonapi.ActionResultResponse, error)
	SkipQuestion(context.Context, *daemonapi.SkipQuestionRequest) (*daemonapi.ActionResultResponse, error)
	SteerSession(context.Context, *daemonapi.SteerSessionRequest) (*daemonapi.ActionResultResponse, error)
	FollowUpAgentSession(context.Context, *daemonapi.FollowUpAgentSessionRequest) (*daemonapi.ActionResultResponse, error)
	RetryAgentSession(context.Context, *daemonapi.RetryAgentSessionRequest) (*daemonapi.ActionResultResponse, error)
	ResumeAllForSession(context.Context, *daemonapi.ResumeAllForSessionRequest) (*daemonapi.ActionResultResponse, error)
	CancelPipeline(context.Context, *daemonapi.CancelPipelineRequest) (*daemonapi.ActionResultResponse, error)
}

type ReadModelAPIServer interface {
	GetSessionOverview(context.Context, *daemonapi.GetSessionOverviewRequest) (*daemonapi.GetSessionOverviewResponse, error)
	GetSidebar(context.Context, *daemonapi.GetSidebarRequest) (*daemonapi.GetSidebarResponse, error)
	GetPlan(context.Context, *daemonapi.GetPlanRequest) (*daemonapi.PlanView, error)
	GetArtifacts(context.Context, *daemonapi.GetArtifactsRequest) (*daemonapi.ArtifactsView, error)
	GetAvailableActions(context.Context, *daemonapi.GetAvailableActionsRequest) (*daemonapi.GetAvailableActionsResponse, error)
}

func RegisterReadModelAPI(s grpc.ServiceRegistrar, srv ReadModelAPIServer) {
	s.RegisterService(&grpc.ServiceDesc{
		ServiceName: "substrate.v1.ReadModelAPI",
		HandlerType: (*ReadModelAPIServer)(nil),
		Methods: []grpc.MethodDesc{
			{MethodName: "GetSessionOverview", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.GetSessionOverviewRequest) (*daemonapi.GetSessionOverviewResponse, error) {
				return srv.GetSessionOverview(ctx, req)
			})},
			{MethodName: "GetSidebar", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.GetSidebarRequest) (*daemonapi.GetSidebarResponse, error) {
				return srv.GetSidebar(ctx, req)
			})},
			{MethodName: "GetPlan", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.GetPlanRequest) (*daemonapi.PlanView, error) {
				return srv.GetPlan(ctx, req)
			})},
			{MethodName: "GetArtifacts", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.GetArtifactsRequest) (*daemonapi.ArtifactsView, error) {
				return srv.GetArtifacts(ctx, req)
			})},
			{MethodName: "GetAvailableActions", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.GetAvailableActionsRequest) (*daemonapi.GetAvailableActionsResponse, error) {
				return srv.GetAvailableActions(ctx, req)
			})},
		},
	}, srv)
}

type AutonomousModeAPIServer interface {
	StartAutonomousMode(context.Context, *daemonapi.StartAutonomousModeRequest) (*daemonapi.AutonomousModeRun, error)
	StopAutonomousMode(context.Context, *daemonapi.StopAutonomousModeRequest) (*daemonapi.AutonomousModeStatusResponse, error)
	GetAutonomousModeStatus(context.Context, *daemonapi.GetAutonomousModeStatusRequest) (*daemonapi.AutonomousModeStatusResponse, error)
}

func RegisterAutonomousModeAPI(s grpc.ServiceRegistrar, srv AutonomousModeAPIServer) {
	s.RegisterService(&grpc.ServiceDesc{
		ServiceName: "substrate.v1.AutonomousModeAPI",
		HandlerType: (*AutonomousModeAPIServer)(nil),
		Methods: []grpc.MethodDesc{
			{MethodName: "StartAutonomousMode", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.StartAutonomousModeRequest) (*daemonapi.AutonomousModeRun, error) {
				return srv.StartAutonomousMode(ctx, req)
			})},
			{MethodName: "StopAutonomousMode", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.StopAutonomousModeRequest) (*daemonapi.AutonomousModeStatusResponse, error) {
				return srv.StopAutonomousMode(ctx, req)
			})},
			{MethodName: "GetAutonomousModeStatus", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.GetAutonomousModeStatusRequest) (*daemonapi.AutonomousModeStatusResponse, error) {
				return srv.GetAutonomousModeStatus(ctx, req)
			})},
		},
	}, srv)
}

type EventStreamAPIServer interface {
	Subscribe(*daemonapi.SubscribeEventsRequest, grpc.ServerStreamingServer[daemonapi.EventBatch]) error
}

type LogAPIServer interface {
	TailAgentSessionLog(*daemonapi.TailAgentSessionLogRequest, grpc.ServerStreamingServer[daemonapi.SessionLogBatch]) error
	SnapshotAgentSessionLog(context.Context, *daemonapi.SnapshotAgentSessionLogRequest) (*daemonapi.SessionLogSnapshot, error)
	TailAppLog(*daemonapi.TailAppLogRequest, grpc.ServerStreamingServer[daemonapi.AppLogBatch]) error
	SnapshotAppLog(context.Context, *daemonapi.SnapshotAppLogRequest) (*daemonapi.AppLogSnapshot, error)
}

type WorkspaceAPIServer interface {
	GetRuntimeContext(context.Context, *daemonapi.GetRuntimeContextRequest) (*daemonapi.RuntimeContext, error)
	InitializeWorkspace(context.Context, *daemonapi.InitializeWorkspaceRequest) (*daemonapi.Workspace, error)
	HealthCheckWorkspace(context.Context, *daemonapi.HealthCheckWorkspaceRequest) (*daemonapi.WorkspaceHealth, error)
	ListManagedRepos(context.Context, *daemonapi.ListManagedReposRequest) (*daemonapi.ListManagedReposResponse, error)
	ListWorktrees(context.Context, *daemonapi.ListWorktreesRequest) (*daemonapi.ListWorktreesResponse, error)
	CloneRepo(context.Context, *daemonapi.CloneRepoRequest) (*daemonapi.CloneRepoResponse, error)
	InitRepo(context.Context, *daemonapi.InitRepoRequest) (*daemonapi.InitRepoResponse, error)
	RemoveRepo(context.Context, *daemonapi.RemoveRepoRequest) (*daemonapi.RemoveRepoResponse, error)
}

type SettingsAPIServer interface {
	GetSettings(context.Context, *daemonapi.GetSettingsRequest) (*daemonapi.GetSettingsResponse, error)
	SaveSettings(context.Context, *daemonapi.SaveSettingsRequest) (*daemonapi.SaveSettingsResponse, error)
	TestProvider(context.Context, *daemonapi.TestProviderRequest) (*daemonapi.ProviderStatus, error)
	LoginProvider(context.Context, *daemonapi.LoginProviderRequest) (*daemonapi.LoginProviderResponse, error)
	RefreshProviderDiagnostics(context.Context, *daemonapi.RefreshProviderDiagnosticsRequest) (*daemonapi.RefreshProviderDiagnosticsResponse, error)
}

func RegisterSettingsAPI(s grpc.ServiceRegistrar, srv SettingsAPIServer) {
	s.RegisterService(&grpc.ServiceDesc{
		ServiceName: "substrate.v1.SettingsAPI",
		HandlerType: (*SettingsAPIServer)(nil),
		Methods: []grpc.MethodDesc{
			{MethodName: "GetSettings", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.GetSettingsRequest) (*daemonapi.GetSettingsResponse, error) {
				return srv.GetSettings(ctx, req)
			})},
			{MethodName: "SaveSettings", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.SaveSettingsRequest) (*daemonapi.SaveSettingsResponse, error) {
				return srv.SaveSettings(ctx, req)
			})},
			{MethodName: "TestProvider", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.TestProviderRequest) (*daemonapi.ProviderStatus, error) {
				return srv.TestProvider(ctx, req)
			})},
			{MethodName: "LoginProvider", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.LoginProviderRequest) (*daemonapi.LoginProviderResponse, error) {
				return srv.LoginProvider(ctx, req)
			})},
			{MethodName: "RefreshProviderDiagnostics", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.RefreshProviderDiagnosticsRequest) (*daemonapi.RefreshProviderDiagnosticsResponse, error) {
				return srv.RefreshProviderDiagnostics(ctx, req)
			})},
		},
	}, srv)
}

func RegisterWorkspaceAPI(s grpc.ServiceRegistrar, srv WorkspaceAPIServer) {
	s.RegisterService(&grpc.ServiceDesc{
		ServiceName: "substrate.v1.WorkspaceAPI",
		HandlerType: (*WorkspaceAPIServer)(nil),
		Methods: []grpc.MethodDesc{
			{MethodName: "GetRuntimeContext", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.GetRuntimeContextRequest) (*daemonapi.RuntimeContext, error) {
				return srv.GetRuntimeContext(ctx, req)
			})},
			{MethodName: "InitializeWorkspace", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.InitializeWorkspaceRequest) (*daemonapi.Workspace, error) {
				return srv.InitializeWorkspace(ctx, req)
			})},
			{MethodName: "HealthCheckWorkspace", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.HealthCheckWorkspaceRequest) (*daemonapi.WorkspaceHealth, error) {
				return srv.HealthCheckWorkspace(ctx, req)
			})},
			{MethodName: "ListManagedRepos", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.ListManagedReposRequest) (*daemonapi.ListManagedReposResponse, error) {
				return srv.ListManagedRepos(ctx, req)
			})},
			{MethodName: "ListWorktrees", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.ListWorktreesRequest) (*daemonapi.ListWorktreesResponse, error) {
				return srv.ListWorktrees(ctx, req)
			})},
			{MethodName: "CloneRepo", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.CloneRepoRequest) (*daemonapi.CloneRepoResponse, error) {
				return srv.CloneRepo(ctx, req)
			})},
			{MethodName: "InitRepo", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.InitRepoRequest) (*daemonapi.InitRepoResponse, error) {
				return srv.InitRepo(ctx, req)
			})},
			{MethodName: "RemoveRepo", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.RemoveRepoRequest) (*daemonapi.RemoveRepoResponse, error) {
				return srv.RemoveRepo(ctx, req)
			})},
		},
	}, srv)
}

func RegisterSystemAPI(s grpc.ServiceRegistrar, srv SystemAPIServer) {
	s.RegisterService(&grpc.ServiceDesc{
		ServiceName: "substrate.v1.SystemAPI",
		HandlerType: (*SystemAPIServer)(nil),
		Methods: []grpc.MethodDesc{
			{MethodName: "Health", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.HealthRequest) (*daemonapi.HealthResponse, error) {
				return srv.Health(ctx, req)
			})},
			{MethodName: "Info", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.InfoRequest) (*daemonapi.InfoResponse, error) {
				return srv.Info(ctx, req)
			})},
			{MethodName: "Disconnect", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.DisconnectRequest) (*daemonapi.DisconnectResponse, error) {
				return srv.Disconnect(ctx, req)
			})},
			{MethodName: "Shutdown", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.ShutdownRequest) (*daemonapi.ShutdownResponse, error) {
				return srv.Shutdown(ctx, req)
			})},
			{MethodName: "GetAccessToken", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.GetAccessTokenRequest) (*daemonapi.GetAccessTokenResponse, error) {
				return srv.GetAccessToken(ctx, req)
			})},
			{MethodName: "RotateAccessToken", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.RotateAccessTokenRequest) (*daemonapi.RotateAccessTokenResponse, error) {
				return srv.RotateAccessToken(ctx, req)
			})},
		},
	}, srv)
}

func RegisterSessionAPI(s grpc.ServiceRegistrar, srv SessionAPIServer) {
	s.RegisterService(&grpc.ServiceDesc{
		ServiceName: "substrate.v1.SessionAPI",
		HandlerType: (*SessionAPIServer)(nil),
		Methods: []grpc.MethodDesc{
			{MethodName: "GetInitialSnapshot", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.GetInitialSnapshotRequest) (*daemonapi.GetInitialSnapshotResponse, error) {
				return srv.GetInitialSnapshot(ctx, req)
			})},
			{MethodName: "ListSessions", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.ListSessionsRequest) (*daemonapi.ListSessionsResponse, error) {
				return srv.ListSessions(ctx, req)
			})},
			{MethodName: "GetSession", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.GetSessionRequest) (*daemonapi.GetSessionResponse, error) {
				return srv.GetSession(ctx, req)
			})},
			{MethodName: "ArchiveSession", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.ArchiveSessionRequest) (*daemonapi.ActionResultResponse, error) {
				return srv.ArchiveSession(ctx, req)
			})},
			{MethodName: "DeleteSession", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.DeleteSessionRequest) (*daemonapi.ActionResultResponse, error) {
				return srv.DeleteSession(ctx, req)
			})},
			{MethodName: "OverrideAccept", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.OverrideAcceptRequest) (*daemonapi.ActionResultResponse, error) {
				return srv.OverrideAccept(ctx, req)
			})},
			{MethodName: "FailReview", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.FailReviewRequest) (*daemonapi.ActionResultResponse, error) {
				return srv.FailReview(ctx, req)
			})},
			{MethodName: "UnarchiveSession", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.UnarchiveSessionRequest) (*daemonapi.ActionResultResponse, error) {
				return srv.UnarchiveSession(ctx, req)
			})},
			{MethodName: "SearchSessionHistory", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.SearchSessionHistoryRequest) (*daemonapi.SearchSessionHistoryResponse, error) {
				return srv.SearchSessionHistory(ctx, req)
			})},
			{MethodName: "RequestPlanChanges", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.RequestPlanChangesRequest) (*daemonapi.ActionResultResponse, error) {
				return srv.RequestPlanChanges(ctx, req)
			})},
			{MethodName: "SaveReviewedPlan", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.SaveReviewedPlanRequest) (*daemonapi.SaveReviewedPlanResponse, error) {
				return srv.SaveReviewedPlan(ctx, req)
			})},
			{MethodName: "StartPlanning", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.StartPlanningRequest) (*daemonapi.ActionResultResponse, error) {
				return srv.StartPlanning(ctx, req)
			})},
			{MethodName: "RestartPlanning", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.RestartPlanningRequest) (*daemonapi.ActionResultResponse, error) {
				return srv.RestartPlanning(ctx, req)
			})},
			{MethodName: "FollowUpPlan", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.FollowUpPlanRequest) (*daemonapi.ActionResultResponse, error) {
				return srv.FollowUpPlan(ctx, req)
			})},
			{MethodName: "FinalizeSession", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.FinalizeSessionRequest) (*daemonapi.ActionResultResponse, error) {
				return srv.FinalizeSession(ctx, req)
			})},
			{MethodName: "RetryFailedSession", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.RetryFailedSessionRequest) (*daemonapi.ActionResultResponse, error) {
				return srv.RetryFailedSession(ctx, req)
			})},
			{MethodName: "ResumeAllForSession", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.ResumeAllForSessionRequest) (*daemonapi.ActionResultResponse, error) {
				return srv.ResumeAllForSession(ctx, req)
			})},
			{MethodName: "ApprovePlan", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.ApprovePlanRequest) (*daemonapi.ActionResultResponse, error) {
				return srv.ApprovePlan(ctx, req)
			})},
			{MethodName: "RunImplementation", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.RunImplementationRequest) (*daemonapi.OperationResponse, error) {
				return srv.RunImplementation(ctx, req)
			})},
			{MethodName: "RetryAgentSession", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.RetryAgentSessionRequest) (*daemonapi.ActionResultResponse, error) {
				return srv.RetryAgentSession(ctx, req)
			})},
			{MethodName: "FollowUpAgentSession", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.FollowUpAgentSessionRequest) (*daemonapi.ActionResultResponse, error) {
				return srv.FollowUpAgentSession(ctx, req)
			})},
			{MethodName: "SteerSession", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.SteerSessionRequest) (*daemonapi.ActionResultResponse, error) {
				return srv.SteerSession(ctx, req)
			})},
			{MethodName: "AnswerQuestion", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.AnswerQuestionRequest) (*daemonapi.ActionResultResponse, error) {
				return srv.AnswerQuestion(ctx, req)
			})},
			{MethodName: "SkipQuestion", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.SkipQuestionRequest) (*daemonapi.ActionResultResponse, error) {
				return srv.SkipQuestion(ctx, req)
			})},
		},
	}, srv)
}

func RegisterAgentSessionAPI(s grpc.ServiceRegistrar, srv AgentSessionAPIServer) {
	s.RegisterService(&grpc.ServiceDesc{
		ServiceName: "substrate.v1.AgentSessionAPI",
		HandlerType: (*AgentSessionAPIServer)(nil),
		Methods: []grpc.MethodDesc{
			{MethodName: "ListAgentSessions", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.ListAgentSessionsRequest) (*daemonapi.ListAgentSessionsResponse, error) {
				return srv.ListAgentSessions(ctx, req)
			})},
			{MethodName: "SearchHistory", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.SearchHistoryRequest) (*daemonapi.SearchHistoryResponse, error) {
				return srv.SearchHistory(ctx, req)
			})},
			{MethodName: "GetInteraction", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.GetInteractionRequest) (*daemonapi.GetInteractionResponse, error) {
				return srv.GetInteraction(ctx, req)
			})},
			{MethodName: "AnswerQuestion", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.AnswerQuestionRequest) (*daemonapi.ActionResultResponse, error) {
				return srv.AnswerQuestion(ctx, req)
			})},
			{MethodName: "SkipQuestion", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.SkipQuestionRequest) (*daemonapi.ActionResultResponse, error) {
				return srv.SkipQuestion(ctx, req)
			})},
			{MethodName: "SteerSession", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.SteerSessionRequest) (*daemonapi.ActionResultResponse, error) {
				return srv.SteerSession(ctx, req)
			})},
			{MethodName: "FollowUpSession", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.FollowUpAgentSessionRequest) (*daemonapi.ActionResultResponse, error) {
				return srv.FollowUpAgentSession(ctx, req)
			})},
			{MethodName: "RetrySession", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.RetryAgentSessionRequest) (*daemonapi.ActionResultResponse, error) {
				return srv.RetryAgentSession(ctx, req)
			})},
			{MethodName: "ResumeAllForSession", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.ResumeAllForSessionRequest) (*daemonapi.ActionResultResponse, error) {
				return srv.ResumeAllForSession(ctx, req)
			})},
			{MethodName: "CancelPipeline", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.CancelPipelineRequest) (*daemonapi.ActionResultResponse, error) {
				return srv.CancelPipeline(ctx, req)
			})},
		},
	}, srv)
}

func RegisterEventStreamAPI(s grpc.ServiceRegistrar, srv EventStreamAPIServer) {
	s.RegisterService(&grpc.ServiceDesc{
		ServiceName: "substrate.v1.EventStreamAPI",
		HandlerType: (*EventStreamAPIServer)(nil),
		Streams: []grpc.StreamDesc{{
			StreamName:    "Subscribe",
			ServerStreams: true,
			Handler: func(service any, stream grpc.ServerStream) error {
				req := new(daemonapi.SubscribeEventsRequest)
				if err := stream.RecvMsg(req); err != nil {
					return err
				}
				return srv.Subscribe(req, &serverStreamingAdapter[daemonapi.EventBatch]{ServerStream: stream})
			},
		}},
	}, srv)
}

func RegisterLogAPI(s grpc.ServiceRegistrar, srv LogAPIServer) {
	s.RegisterService(&grpc.ServiceDesc{
		ServiceName: "substrate.v1.LogAPI",
		HandlerType: (*LogAPIServer)(nil),
		Methods: []grpc.MethodDesc{
			{MethodName: "SnapshotAgentSessionLog", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.SnapshotAgentSessionLogRequest) (*daemonapi.SessionLogSnapshot, error) {
				return srv.SnapshotAgentSessionLog(ctx, req)
			})},
			{MethodName: "SnapshotAppLog", Handler: unaryHandler(func(ctx context.Context, req *daemonapi.SnapshotAppLogRequest) (*daemonapi.AppLogSnapshot, error) {
				return srv.SnapshotAppLog(ctx, req)
			})},
		},
		Streams: []grpc.StreamDesc{
			{
				StreamName:    "TailAgentSessionLog",
				ServerStreams: true,
				Handler: func(service any, stream grpc.ServerStream) error {
					req := new(daemonapi.TailAgentSessionLogRequest)
					if err := stream.RecvMsg(req); err != nil {
						return err
					}
					return srv.TailAgentSessionLog(req, &serverStreamingAdapter[daemonapi.SessionLogBatch]{ServerStream: stream})
				},
			},
			{
				StreamName:    "TailAppLog",
				ServerStreams: true,
				Handler: func(service any, stream grpc.ServerStream) error {
					req := new(daemonapi.TailAppLogRequest)
					if err := stream.RecvMsg(req); err != nil {
						return err
					}
					return srv.TailAppLog(req, &serverStreamingAdapter[daemonapi.AppLogBatch]{ServerStream: stream})
				},
			},
		},
	}, srv)
}

type unaryMethod[Req any, Resp any] func(context.Context, *Req) (*Resp, error)

func unaryHandler[Req any, Resp any](method unaryMethod[Req, Resp]) grpc.MethodHandler {
	return func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
		req := new(Req)
		if err := dec(req); err != nil {
			return nil, err
		}
		if interceptor == nil {
			return method(ctx, req)
		}
		info := &grpc.UnaryServerInfo{Server: srv}
		handler := func(ctx context.Context, request any) (any, error) {
			return method(ctx, request.(*Req))
		}
		return interceptor(ctx, req, info, handler)
	}
}

type serverStreamingAdapter[T any] struct {
	grpc.ServerStream
}

func (s *serverStreamingAdapter[T]) Send(msg *T) error {
	return s.ServerStream.SendMsg(msg)
}
