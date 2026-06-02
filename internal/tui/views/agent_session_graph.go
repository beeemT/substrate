package views

import (
	"github.com/beeemT/substrate/internal/domain"
)

func leafAgentSessions(sessions []domain.AgentSession) []domain.AgentSession {
	return domain.LeafAgentSessions(sessions)
}
