package domain

import "time"

// Question is a question surfaced by a sub-agent, routed through the foreman.
type Question struct {
	ID             string
	AgentSessionID string
	Content        string
	Context        string
	Answer         string
	ProposedAnswer string
	AnsweredBy     string
	Status         QuestionStatus
	CreatedAt      time.Time
	AnsweredAt     *time.Time
}

// QuestionStatus represents the lifecycle state of a question.
type QuestionStatus string

const (
	QuestionPending   QuestionStatus = "pending"
	QuestionAnswered  QuestionStatus = "answered"
	QuestionEscalated QuestionStatus = "escalated"
)
