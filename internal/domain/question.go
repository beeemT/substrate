package domain

import "time"

// Question is a normalized agent question surfaced by any harness question mechanism.
type Question struct {
	ID             string
	AgentSessionID string
	Stage          AgentSessionKind
	Source         QuestionSource
	Content        string
	Context        string
	Structured     *StructuredQuestionSet
	Answer         string
	AnswerData     *AgentQuestionAnswer
	ProposedAnswer string
	AnsweredBy     string
	Status         QuestionStatus
	CreatedAt      time.Time
	AnsweredAt     *time.Time
}

// QuestionSource identifies the harness mechanism that produced a normalized question.
type QuestionSource string

const (
	QuestionSourceAskForeman            QuestionSource = "ask_foreman"
	QuestionSourceAskUser               QuestionSource = "ask_user"
	QuestionSourceClaudeAsk             QuestionSource = "claude_ask"
	QuestionSourceOMPAsk                QuestionSource = "omp_ask"
	QuestionSourceOpenCodeQuestion      QuestionSource = "opencode_question"
	QuestionSourceFutureHarnessQuestion QuestionSource = "future_harness_question"
)

// IsHumanDirected reports whether this source expects an operator answer sent
// directly back into the live agent session.
func (s QuestionSource) IsHumanDirected() bool {
	switch s {
	case QuestionSourceAskUser,
		QuestionSourceClaudeAsk,
		QuestionSourceOMPAsk,
		QuestionSourceOpenCodeQuestion,
		QuestionSourceFutureHarnessQuestion:
		return true
	default:
		return false
	}
}

// IsForemanDirected reports whether this source expects Foreman to produce or
// escalate the answer before the live agent session is unblocked.
func (s QuestionSource) IsForemanDirected() bool {
	return s == QuestionSourceAskForeman
}

// QuestionOption is one selectable option in a structured agent question.
type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Preview     string `json:"preview,omitempty"`
}

// StructuredQuestion is one prompt inside a structured question set.
type StructuredQuestion struct {
	ID               string           `json:"id,omitempty"`
	Question         string           `json:"question"`
	Header           string           `json:"header,omitempty"`
	Options          []QuestionOption `json:"options,omitempty"`
	MultiSelect      bool             `json:"multi_select"`
	RecommendedIndex *int             `json:"recommended_index,omitempty"`
}

// StructuredQuestionSet preserves native harness ask-question semantics.
type StructuredQuestionSet struct {
	Questions            []StructuredQuestion `json:"questions"`
	SupportsCustomAnswer bool                 `json:"supports_custom_answer"`
	SupportsAnnotations  bool                 `json:"supports_annotations"`
	NativeResponseFormat string               `json:"native_response_format,omitempty"`
}

// AgentQuestionAnnotation carries optional notes/previews for structured answers.
type AgentQuestionAnnotation struct {
	Preview string `json:"preview,omitempty"`
	Notes   string `json:"notes,omitempty"`
}

// StructuredQuestionAnswer is the user's answer to one structured question.
type StructuredQuestionAnswer struct {
	QuestionID      string   `json:"question_id,omitempty"`
	Question        string   `json:"question"`
	SelectedOptions []string `json:"selected_options,omitempty"`
	CustomAnswer    string   `json:"custom_answer,omitempty"`
}

// AgentQuestionAnswer is the normalized answer delivered back to a live harness.
type AgentQuestionAnswer struct {
	Text              string                             `json:"text,omitempty"`
	StructuredAnswers []StructuredQuestionAnswer         `json:"structured_answers,omitempty"`
	Annotations       map[string]AgentQuestionAnnotation `json:"annotations,omitempty"`
}

// QuestionStatus represents the lifecycle state of a question.
type QuestionStatus string

const (
	QuestionPending   QuestionStatus = "pending"
	QuestionAnswered  QuestionStatus = "answered"
	QuestionEscalated QuestionStatus = "escalated"
)
