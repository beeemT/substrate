package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/beeemT/go-atomic/generic"
	"github.com/beeemT/substrate/internal/domain"
)

type questionRow struct {
	ID             string  `db:"id"`
	AgentSessionID string  `db:"agent_session_id"`
	Content        string  `db:"content"`
	Context        *string `db:"context"`
	Answer         *string `db:"answer"`
	AnsweredBy     *string `db:"answered_by"`
	Status         string  `db:"status"`
	CreatedAt      string  `db:"created_at"`
	AnsweredAt     *string `db:"answered_at"`
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
	return domain.Question{
		ID:             r.ID,
		AgentSessionID: r.AgentSessionID,
		Content:        r.Content,
		Context:        derefStr(r.Context),
		Answer:         derefStr(r.Answer),
		AnsweredBy:     derefStr(r.AnsweredBy),
		Status:         domain.QuestionStatus(r.Status),
		CreatedAt:      createdAt,
		AnsweredAt:     answeredAt,
	}, nil
}

func rowFromQuestion(q domain.Question) questionRow {
	return questionRow{
		ID:             q.ID,
		AgentSessionID: q.AgentSessionID,
		Content:        q.Content,
		Context:        strPtr(q.Context),
		Answer:         strPtr(q.Answer),
		AnsweredBy:     strPtr(q.AnsweredBy),
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
		`INSERT INTO questions (id, agent_session_id, content, context, answer, answered_by, status, created_at, answered_at)
		 VALUES (:id, :agent_session_id, :content, :context, :answer, :answered_by, :status, :created_at, :answered_at)`, row)
	if err != nil {
		return fmt.Errorf("create question %s: %w", q.ID, err)
	}
	return nil
}

func (r QuestionRepo) Update(ctx context.Context, q domain.Question) error {
	row := rowFromQuestion(q)
	res, err := r.remote.NamedExecContext(ctx,
		`UPDATE questions SET agent_session_id = :agent_session_id, content = :content,
		 context = :context, answer = :answer, answered_by = :answered_by, status = :status,
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
