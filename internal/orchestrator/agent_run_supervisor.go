package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/service"
)

type AgentHarnessSelector interface {
	HarnessFor(kind domain.AgentSessionKind) adapter.AgentHarness
}

type AgentRunSupervisor struct {
	harnesses  AgentHarnessSelector
	sessionSvc *service.AgentSessionService
	registry   SessionRegistry
	forward    func(context.Context, <-chan adapter.AgentEvent, string)
	timeout    time.Duration
}

type AgentRunRequest struct {
	Session                  domain.AgentSession
	Opts                     adapter.SessionOpts
	CompleteContinuationKind string
	AfterStart               func(context.Context, adapter.AgentSession) error
	OnCompleted              func(context.Context, domain.AgentSession) error
	OnFailed                 func(context.Context, domain.AgentSession, error) error
	OnInterrupted            func(context.Context, domain.AgentSession) error
}

func (s *AgentRunSupervisor) Start(ctx context.Context, req AgentRunRequest) (domain.AgentSession, error) {
	if req.Session.ID == "" {
		return domain.AgentSession{}, fmt.Errorf("agent session id is required")
	}
	if s.harnesses == nil {
		return domain.AgentSession{}, fmt.Errorf("agent harness selector is required")
	}
	if s.sessionSvc == nil {
		return domain.AgentSession{}, fmt.Errorf("agent session service is required")
	}
	if s.registry == nil {
		return domain.AgentSession{}, fmt.Errorf("session registry is required")
	}
	harness := s.harnesses.HarnessFor(req.Session.Kind)
	if harness == nil {
		return domain.AgentSession{}, fmt.Errorf("no harness configured for %s sessions", req.Session.Kind)
	}

	harnessSession, err := harness.StartSession(ctx, req.Opts)
	if err != nil {
		if failErr := failSessionDurably(ctx, s.sessionSvc, req.Session.ID, ptrInt(1)); failErr != nil {
			slog.Warn("failed to fail session after harness start error", "error", failErr, "agent_session_id", req.Session.ID)
		}
		return domain.AgentSession{}, fmt.Errorf("start agent: %w", err)
	}
	if req.AfterStart != nil {
		if err := req.AfterStart(ctx, harnessSession); err != nil {
			if failErr := failSessionDurably(ctx, s.sessionSvc, req.Session.ID, ptrInt(1)); failErr != nil {
				slog.Warn("failed to fail session after post-start error", "error", failErr, "agent_session_id", req.Session.ID)
			}
			return domain.AgentSession{}, err
		}
	}

	s.registry.Register(req.Session.ID, harnessSession)
	sessionCtx := ctx
	cancel := func() {}
	if s.timeout > 0 {
		sessionCtx, cancel = context.WithTimeout(ctx, s.timeout)
	}
	go s.wait(sessionCtx, cancel, req, harnessSession)
	return req.Session, nil
}

func (s *AgentRunSupervisor) wait(ctx context.Context, cancel context.CancelFunc, req AgentRunRequest, harnessSession adapter.AgentSession) {
	defer cancel()
	defer s.registry.Deregister(req.Session.ID)
	if s.forward != nil {
		go s.forward(ctx, harnessSession.Events(), req.Session.ID)
	}

	waitErr := harnessSession.Wait(ctx)
	if info := harnessSession.ResumeInfo(); len(info) > 0 {
		updateCtx, updateCancel := durableCleanupContext(ctx)
		if err := s.sessionSvc.UpdateResumeInfo(updateCtx, req.Session.ID, info); err != nil {
			slog.Warn("failed to store resume info", "error", err, "agent_session_id", req.Session.ID)
		}
		updateCancel()
	}

	callbackCtx, callbackCancel := durableCleanupContext(ctx)
	defer callbackCancel()
	completed := req.Session
	completed.ResumeInfo = harnessSession.ResumeInfo()

	if waitErr != nil {
		if errors.Is(waitErr, context.Canceled) {
			if err := interruptSessionDurably(callbackCtx, s.sessionSvc, req.Session.ID); err != nil {
				slog.Warn("failed to interrupt session on context cancellation", "error", err, "agent_session_id", req.Session.ID)
			}
			if req.OnInterrupted != nil {
				if err := req.OnInterrupted(callbackCtx, completed); err != nil {
					slog.Error("agent run interrupted continuation failed", "error", err, "agent_session_id", req.Session.ID)
				}
			}
			return
		}
		if !agentSessionAlreadyInterrupted(callbackCtx, s.sessionSvc, req.Session.ID) {
			if err := failSessionDurably(callbackCtx, s.sessionSvc, req.Session.ID, ptrInt(1)); err != nil {
				slog.Warn("failed to fail session", "error", err, "agent_session_id", req.Session.ID)
			}
		}
		if req.OnFailed != nil {
			if err := req.OnFailed(callbackCtx, completed, waitErr); err != nil {
				slog.Error("agent run failure continuation failed", "error", err, "agent_session_id", req.Session.ID)
			}
		}
		return
	}

	var completeErr error
	if req.CompleteContinuationKind != "" {
		_, completeErr = s.sessionSvc.CompleteWithPendingContinuation(callbackCtx, req.Session.ID, req.CompleteContinuationKind)
		if completeErr != nil {
			completeErr = fmt.Errorf("complete session with pending continuation: %w", completeErr)
		}
	} else if err := completeSessionDurably(callbackCtx, s.sessionSvc, req.Session.ID); err != nil {
		completeErr = fmt.Errorf("complete session: %w", err)
	}
	if completeErr != nil {
		slog.Error("agent run durable completion failed", "error", completeErr, "agent_session_id", req.Session.ID)
		if req.OnFailed != nil {
			if err := req.OnFailed(callbackCtx, completed, completeErr); err != nil {
				slog.Error("agent run durable completion failure callback failed", "error", err, "agent_session_id", req.Session.ID)
			}
		}
		return
	}
	if req.OnCompleted != nil {
		if err := req.OnCompleted(callbackCtx, completed); err != nil {
			slog.Error("agent run completion continuation failed", "error", err, "agent_session_id", req.Session.ID)
		}
	}
}

type staticHarnessSelector struct {
	harness adapter.AgentHarness
}

func (s staticHarnessSelector) HarnessFor(domain.AgentSessionKind) adapter.AgentHarness {
	return s.harness
}
