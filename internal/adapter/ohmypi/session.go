package omp

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/adapter/bridge"
)

// ohMyPiSession implements adapter.AgentSession for oh-my-pi.
type ohMyPiSession struct {
	bs             *bridge.BridgeSession
	ompSessionFile string
	ompSessionID   string
	sessionMu      sync.Mutex
}

func (s *ohMyPiSession) ID() string { return s.bs.ID }

// OmpSessionFile returns the OMP native session file path captured from session_meta.
func (s *ohMyPiSession) OmpSessionFile() string {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	return s.ompSessionFile
}

// OmpSessionID returns the OMP native session ID captured from session_meta.
func (s *ohMyPiSession) OmpSessionID() string {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	return s.ompSessionID
}

func (s *ohMyPiSession) Wait(ctx context.Context) error { return s.bs.Wait(ctx) }
func (s *ohMyPiSession) Events() <-chan adapter.AgentEvent { return s.bs.EventsChan() }
func (s *ohMyPiSession) SendMessage(ctx context.Context, msg string) error {
	return s.bs.SendMessage(ctx, msg)
}
func (s *ohMyPiSession) Steer(ctx context.Context, msg string) error { return s.bs.Steer(ctx, msg) }
func (s *ohMyPiSession) Abort(ctx context.Context) error             { return s.bs.Abort(ctx) }
func (s *ohMyPiSession) SendAnswer(ctx context.Context, answer string) error {
	return s.bs.SendAnswer(ctx, answer)
}

// sessionMetaCallback is set as bs.ParseSessionMeta. It parses the session_meta
// line and stores the OMP session file path and session ID for later resume.
func (s *ohMyPiSession) sessionMetaCallback(line []byte) {
	var meta struct {
		OmpSessionFile string `json:"omp_session_file"`
		OmpSessionID   string `json:"omp_session_id"`
	}
	if err := json.Unmarshal(line, &meta); err == nil {
		s.sessionMu.Lock()
		s.ompSessionFile = meta.OmpSessionFile
		s.ompSessionID = meta.OmpSessionID
		s.sessionMu.Unlock()
	}
}
