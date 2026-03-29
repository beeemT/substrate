package claudeagent

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/adapter/bridge"
)

// claudeAgentSession implements adapter.AgentSession for the Claude Agent SDK bridge.
type claudeAgentSession struct {
	bs              *bridge.BridgeSession
	claudeSessionID string
	sessionMu       sync.Mutex
}

func (s *claudeAgentSession) ID() string { return s.bs.ID }

func (s *claudeAgentSession) ResumeInfo() map[string]string {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	if s.claudeSessionID == "" {
		return nil
	}
	return map[string]string{
		"claude_session_id": s.claudeSessionID,
	}
}
func (s *claudeAgentSession) Wait(ctx context.Context) error    { return s.bs.Wait(ctx) }
func (s *claudeAgentSession) Events() <-chan adapter.AgentEvent { return s.bs.EventsChan() }
func (s *claudeAgentSession) SendMessage(ctx context.Context, msg string) error {
	return s.bs.SendMessage(ctx, msg)
}
func (s *claudeAgentSession) Steer(ctx context.Context, msg string) error {
	return s.bs.Steer(ctx, msg)
}
func (s *claudeAgentSession) Abort(ctx context.Context) error { return s.bs.Abort(ctx) }

// SendAnswer sends an answer to resolve a pending ask_foreman tool call.
func (s *claudeAgentSession) SendAnswer(ctx context.Context, answer string) error {
	return s.bs.SendAnswer(ctx, answer)
}

// sessionMetaCallback is set as bs.ParseSessionMeta. It parses the session_meta
// line and stores the Claude SDK session UUID for later resume.
func (s *claudeAgentSession) sessionMetaCallback(line []byte) {
	var meta struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(line, &meta); err == nil {
		s.sessionMu.Lock()
		s.claudeSessionID = meta.SessionID
		s.sessionMu.Unlock()
	}
}
