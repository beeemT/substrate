package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/beeemT/go-atomic/generic"
	"github.com/beeemT/substrate/internal/domain"
)

type questionRow struct {
	ID             string  `db:"id"`
	AgentSessionID string  `db:"agent_session_id"`
	Stage          *string `db:"stage"`
	Source         *string `db:"source"`
	Content        string  `db:"content"`
	Context        *string `db:"context"`
	Structured     *string `db:"structured"`
	Answer         *string `db:"answer"`
	AnswerData     *string `db:"answer_data"`
	AnsweredBy     *string `db:"answered_by"`
	Status         string  `db:"status"`
	CreatedAt      string  `db:"created_at"`
	AnsweredAt     *string `db:"answered_at"`
	ProposedAnswer *string `db:"proposed_answer"`
}

func (r *questionRow) toDomain() (domain.Question, error) {
	createdAt, err := parseTime(r.CreatedAt)
	if err != nil {
		return domain.Question{}, fmt.Errorf("created_at: %w", err)
	}
	answeredAt, err := parseTimePtr(r.AnsweredAt)
	if err != nil {
		return domain.Question{}, fmt.Errorf("answered_at: %w", err)
	}
	var structured *domain.StructuredQuestionSet
	if r.Structured != nil && *r.Structured != "" {
		var parsed domain.StructuredQuestionSet
		if err := json.Unmarshal([]byte(*r.Structured), &parsed); err != nil {
			return domain.Question{}, fmt.Errorf("structured: %w", err)
		}
		structured = &parsed
	}
	var answerData *domain.AgentQuestionAnswer
	if r.AnswerData != nil && *r.AnswerData != "" {
		var parsed domain.AgentQuestionAnswer
		if err := json.Unmarshal([]byte(*r.AnswerData), &parsed); err != nil {
			return domain.Question{}, fmt.Errorf("answer_data: %w", err)
		}
		answerData = &parsed
	}

	return domain.Question{
		ID:             r.ID,
		AgentSessionID: r.AgentSessionID,
		Stage:          domain.AgentSessionPhase(derefStr(r.Stage)),
		Source:         domain.QuestionSource(derefStr(r.Source)),
		Content:        r.Content,
		Context:        derefStr(r.Context),
		Structured:     structured,
		Answer:         derefStr(r.Answer),
		AnswerData:     answerData,
		AnsweredBy:     derefStr(r.AnsweredBy),
		ProposedAnswer: derefStr(r.ProposedAnswer),
		Status:         domain.QuestionStatus(r.Status),
		CreatedAt:      createdAt,
		AnsweredAt:     answeredAt,
	}, nil
}

func rowFromQuestion(q domain.Question) questionRow {
	structured := ""
	if q.Structured != nil {
		if data, err := json.Marshal(q.Structured); err == nil {
			structured = string(data)
		}
	}
	answerData := ""
	if q.AnswerData != nil {
		if data, err := json.Marshal(q.AnswerData); err == nil {
			answerData = string(data)
		}
	}

	return questionRow{
		ID:             q.ID,
		AgentSessionID: q.AgentSessionID,
		Stage:          strPtr(string(q.Stage)),
		Source:         strPtr(string(q.Source)),
		Content:        q.Content,
		Context:        strPtr(q.Context),
		Structured:     strPtr(structured),
		Answer:         strPtr(q.Answer),
		AnswerData:     strPtr(answerData),
		AnsweredBy:     strPtr(q.AnsweredBy),
		ProposedAnswer: strPtr(q.ProposedAnswer),
		Status:         string(q.Status),
		CreatedAt:      formatTime(q.CreatedAt),
		AnsweredAt:     formatTimePtr(q.AnsweredAt),
	}
}

// QuestionRepo implements repository.QuestionRepository using SQLite.
type QuestionRepo struct{ remote generic.SQLXRemote }

func NewQuestionRepo(remote generic.SQLXRemote) QuestionRepo {
	return QuestionRepo{remote: remote}
}

func (r QuestionRepo) Get(ctx context.Context, id string) (domain.Question, error) {
	var row questionRow
	if err := r.remote.GetContext(ctx, &row, `SELECT * FROM questions WHERE id = ?`, id); err != nil {
		return domain.Question{}, fmt.Errorf("get question %s: %w", id, err)
	}

	return row.toDomain()
}

func (r QuestionRepo) ListBySessionID(ctx context.Context, sessionID string) ([]domain.Question, error) {
	var rows []questionRow
	if err := r.remote.SelectContext(ctx, &rows, `SELECT * FROM questions WHERE agent_session_id = ? ORDER BY created_at`, sessionID); err != nil {
		return nil, fmt.Errorf("list questions for session %s: %w", sessionID, err)
	}
	questions := make([]domain.Question, len(rows))
	for i := range rows {
		q, err := rows[i].toDomain()
		if err != nil {
			return nil, fmt.Errorf("convert question: %w", err)
		}
		questions[i] = q
	}

	return questions, nil
}

func (r QuestionRepo) Create(ctx context.Context, q domain.Question) error {
	row := rowFromQuestion(q)
	_, err := r.remote.NamedExecContext(ctx,
		`INSERT INTO questions (id, agent_session_id, stage, source, content, context, structured, answer, answer_data, proposed_answer, answered_by, status, created_at, answered_at)
		 VALUES (:id, :agent_session_id, :stage, :source, :content, :context, :structured, :answer, :answer_data, :proposed_answer, :answered_by, :status, :created_at, :answered_at)`, row)
	if err != nil {
		return fmt.Errorf("create question %s: %w", q.ID, err)
	}

	return nil
}

func (r QuestionRepo) Update(ctx context.Context, q domain.Question) error {
	row := rowFromQuestion(q)
	res, err := r.remote.NamedExecContext(ctx,
		`UPDATE questions SET agent_session_id = :agent_session_id, stage = :stage, source = :source, content = :content,
		 context = :context, structured = :structured, answer = :answer, answer_data = :answer_data, proposed_answer = :proposed_answer, answered_by = :answered_by, status = :status,
		 answered_at = :answered_at WHERE id = :id`, row)
	if err != nil {
		return fmt.Errorf("update question %s: %w", q.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update question %s: get rows affected: %w", q.ID, err)
	}
	if n == 0 {
		return fmt.Errorf("update question %s: %w", q.ID, sql.ErrNoRows)
	}

	return nil
}

// UpdateProposedAnswer atomically updates proposed_answer only when the question is
// still in the 'escalated' state. If the question was already answered (concurrent
// ResolveEscalated), the conditional WHERE clause makes this a no-op (0 rows affected),
// which is treated as success — the sub-agent is already unblocked.
func (r QuestionRepo) UpdateProposedAnswer(ctx context.Context, id, proposedAnswer string) error {
	type args struct {
		ID             string `db:"id"`
		ProposedAnswer string `db:"proposed_answer"`
		Status         string `db:"status"`
	}
	res, err := r.remote.NamedExecContext(ctx,
		`UPDATE questions SET proposed_answer = :proposed_answer WHERE id = :id AND status = :status`,
		args{ID: id, ProposedAnswer: proposedAnswer, Status: string(domain.QuestionEscalated)},
	)
	if err != nil {
		return fmt.Errorf("update proposed answer %s: %w", id, err)
	}
	if _, err = res.RowsAffected(); err != nil {
		return fmt.Errorf("update proposed answer %s: get rows affected: %w", id, err)
	}
	// 0 rows means the question was already answered by ResolveEscalated — that is fine.
	return nil
}
