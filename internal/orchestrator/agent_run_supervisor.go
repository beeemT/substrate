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

type AgentRunMode int

const (
	AgentRunModeWait AgentRunMode = iota
	AgentRunModeReviewEvents
)

type AgentRunRequest struct {
	Session                  domain.AgentSession
	Opts                     adapter.SessionOpts
	Mode                     AgentRunMode
	CompleteContinuationKind string
	AfterStart               func(context.Context, adapter.AgentSession) error
	ReadOutput               func(context.Context, string) (string, error)
	OnCompleted              func(context.Context, domain.AgentSession) error
	OnReviewCompleted        func(context.Context, domain.AgentSession, string) error
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
	if req.Mode == AgentRunModeReviewEvents {
		s.waitReviewEvents(ctx, req, harnessSession)
		return
	}
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

func (s *AgentRunSupervisor) waitReviewEvents(ctx context.Context, req AgentRunRequest, harnessSession adapter.AgentSession) {
	events := harnessSession.Events()
	completed := req.Session
	completed.ResumeInfo = harnessSession.ResumeInfo()
	for {
		select {
		case <-ctx.Done():
			callbackCtx, callbackCancel := durableCleanupContext(ctx)
			defer callbackCancel()
			if errors.Is(ctx.Err(), context.Canceled) {
				if err := interruptSessionDurably(callbackCtx, s.sessionSvc, req.Session.ID); err != nil {
					slog.Warn("failed to interrupt review session on context cancellation", "error", err, "agent_session_id", req.Session.ID)
				}
				if req.OnInterrupted != nil {
					if err := req.OnInterrupted(callbackCtx, completed); err != nil {
						slog.Error("review run interrupted continuation failed", "error", err, "agent_session_id", req.Session.ID)
					}
				}
				s.abortReviewSession(callbackCtx, harnessSession, req.Session.ID)
				return
			}
			err := fmt.Errorf("review session timed out: %w", ctx.Err())
			s.failReviewRun(callbackCtx, req, completed, err)
			s.abortReviewSession(callbackCtx, harnessSession, req.Session.ID)
			return
		case evt, ok := <-events:
			if !ok {
				callbackCtx, callbackCancel := durableCleanupContext(ctx)
				defer callbackCancel()
				err := errors.New("review session events channel closed unexpectedly")
				s.failReviewRun(callbackCtx, req, completed, err)
				s.abortReviewSession(callbackCtx, harnessSession, req.Session.ID)
				return
			}
			switch evt.Type {
			case "done":
				callbackCtx, callbackCancel := durableCleanupContext(ctx)
				defer callbackCancel()
				if err := completeSessionDurably(callbackCtx, s.sessionSvc, req.Session.ID); err != nil {
					completeErr := fmt.Errorf("complete review session: %w", err)
					slog.Error("review run durable completion failed", "error", completeErr, "agent_session_id", req.Session.ID)
					if req.OnFailed != nil {
						if callbackErr := req.OnFailed(callbackCtx, completed, completeErr); callbackErr != nil {
							slog.Error("review run durable completion failure callback failed", "error", callbackErr, "agent_session_id", req.Session.ID)
						}
					}
					s.abortReviewSession(callbackCtx, harnessSession, req.Session.ID)
					return
				}
				output := ""
				if req.ReadOutput != nil {
					var err error
					output, err = req.ReadOutput(callbackCtx, req.Session.ID)
					if err != nil {
						readErr := fmt.Errorf("read review session output: %w", err)
						if req.OnFailed != nil {
							if callbackErr := req.OnFailed(callbackCtx, completed, readErr); callbackErr != nil {
								slog.Error("review output failure callback failed", "error", callbackErr, "agent_session_id", req.Session.ID)
							}
						}
						s.abortReviewSession(callbackCtx, harnessSession, req.Session.ID)
						return
					}
				}
				if req.OnReviewCompleted != nil {
					if err := req.OnReviewCompleted(callbackCtx, completed, output); err != nil {
						slog.Error("review run completion continuation failed", "error", err, "agent_session_id", req.Session.ID)
					}
				}
				if req.OnCompleted != nil {
					if err := req.OnCompleted(callbackCtx, completed); err != nil {
						slog.Error("review run completion callback failed", "error", err, "agent_session_id", req.Session.ID)
					}
				}
				s.abortReviewSession(callbackCtx, harnessSession, req.Session.ID)
				return
			case "error":
				callbackCtx, callbackCancel := durableCleanupContext(ctx)
				defer callbackCancel()
				err := fmt.Errorf("review session error: %s", evt.Payload)
				s.failReviewRun(callbackCtx, req, completed, err)
				s.abortReviewSession(callbackCtx, harnessSession, req.Session.ID)
				return
			}
		}
	}
}

func (s *AgentRunSupervisor) failReviewRun(ctx context.Context, req AgentRunRequest, completed domain.AgentSession, err error) {
	if failErr := failSessionDurably(ctx, s.sessionSvc, req.Session.ID, ptrInt(1)); failErr != nil {
		slog.Warn("failed to fail review session", "error", failErr, "agent_session_id", req.Session.ID)
	}
	if req.OnFailed != nil {
		if callbackErr := req.OnFailed(ctx, completed, err); callbackErr != nil {
			slog.Error("review run failure continuation failed", "error", callbackErr, "agent_session_id", req.Session.ID)
		}
	}
}

func (s *AgentRunSupervisor) abortReviewSession(ctx context.Context, harnessSession adapter.AgentSession, sessionID string) {
	if err := harnessSession.Abort(ctx); err != nil {
		slog.Warn("failed to abort review session after terminal event", "error", err, "agent_session_id", sessionID)
	}
}

type staticHarnessSelector struct {
	harness adapter.AgentHarness
}

func (s staticHarnessSelector) HarnessFor(domain.AgentSessionKind) adapter.AgentHarness {
	return s.harness
}
