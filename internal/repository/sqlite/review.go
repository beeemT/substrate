package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/beeemT/go-atomic/generic"
	"github.com/beeemT/substrate/internal/domain"
)

type reviewCycleRow struct {
	ID              string  `db:"id"`
	AgentSessionID  string  `db:"agent_session_id"`
	CycleNumber     int     `db:"cycle_number"`
	ReviewerHarness string  `db:"reviewer_harness"`
	Status          string  `db:"status"`
	Summary         *string `db:"summary"`
	CreatedAt       string  `db:"created_at"`
	UpdatedAt       string  `db:"updated_at"`
}

func (r *reviewCycleRow) toDomain() (domain.ReviewCycle, error) {
	createdAt, err := parseTime(r.CreatedAt)
	if err != nil {
		return domain.ReviewCycle{}, fmt.Errorf("created_at: %w", err)
	}
	updatedAt, err := parseTime(r.UpdatedAt)
	if err != nil {
		return domain.ReviewCycle{}, fmt.Errorf("updated_at: %w", err)
	}
	return domain.ReviewCycle{
		ID:              r.ID,
		AgentSessionID:  r.AgentSessionID,
		CycleNumber:     r.CycleNumber,
		ReviewerHarness: r.ReviewerHarness,
		Status:          domain.ReviewCycleStatus(r.Status),
		Summary:         derefStr(r.Summary),
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
	}, nil
}

func rowFromReviewCycle(rc domain.ReviewCycle) reviewCycleRow {
	return reviewCycleRow{
		ID:              rc.ID,
		AgentSessionID:  rc.AgentSessionID,
		CycleNumber:     rc.CycleNumber,
		ReviewerHarness: rc.ReviewerHarness,
		Status:          string(rc.Status),
		Summary:         strPtr(rc.Summary),
		CreatedAt:       formatTime(rc.CreatedAt),
		UpdatedAt:       formatTime(rc.UpdatedAt),
	}
}

type critiqueRow struct {
	ID            string  `db:"id"`
	ReviewCycleID string  `db:"review_cycle_id"`
	FilePath      *string `db:"file_path"`
	LineNumber    *int    `db:"line_number"`
	Severity      string  `db:"severity"`
	Description   string  `db:"description"`
	Status        string  `db:"status"`
	CreatedAt     string  `db:"created_at"`
}

func (r *critiqueRow) toDomain() (domain.Critique, error) {
	createdAt, err := parseTime(r.CreatedAt)
	if err != nil {
		return domain.Critique{}, fmt.Errorf("created_at: %w", err)
	}
	return domain.Critique{
		ID:            r.ID,
		ReviewCycleID: r.ReviewCycleID,
		FilePath:      derefStr(r.FilePath),
		LineNumber:    r.LineNumber,
		Severity:      domain.CritiqueSeverity(r.Severity),
		Description:   r.Description,
		Status:        domain.CritiqueStatus(r.Status),
		CreatedAt:     createdAt,
	}, nil
}

func rowFromCritique(c domain.Critique) critiqueRow {
	return critiqueRow{
		ID:            c.ID,
		ReviewCycleID: c.ReviewCycleID,
		FilePath:      strPtr(c.FilePath),
		LineNumber:    c.LineNumber,
		Severity:      string(c.Severity),
		Description:   c.Description,
		Status:        string(c.Status),
		CreatedAt:     formatTime(c.CreatedAt),
	}
}

// ReviewRepo implements repository.ReviewRepository using SQLite.
type ReviewRepo struct{ remote generic.SQLXRemote }

func NewReviewRepo(remote generic.SQLXRemote) ReviewRepo {
	return ReviewRepo{remote: remote}
}

func (r ReviewRepo) GetCycle(ctx context.Context, id string) (domain.ReviewCycle, error) {
	var row reviewCycleRow
	if err := r.remote.GetContext(ctx, &row, `SELECT * FROM review_cycles WHERE id = ?`, id); err != nil {
		return domain.ReviewCycle{}, fmt.Errorf("get review cycle %s: %w", id, err)
	}
	return row.toDomain()
}

func (r ReviewRepo) ListCyclesBySessionID(ctx context.Context, sessionID string) ([]domain.ReviewCycle, error) {
	var rows []reviewCycleRow
	if err := r.remote.SelectContext(ctx, &rows, `SELECT * FROM review_cycles WHERE agent_session_id = ? ORDER BY cycle_number`, sessionID); err != nil {
		return nil, fmt.Errorf("list review cycles for session %s: %w", sessionID, err)
	}
	cycles := make([]domain.ReviewCycle, len(rows))
	for i := range rows {
		rc, err := rows[i].toDomain()
		if err != nil {
			return nil, fmt.Errorf("convert review cycle: %w", err)
		}
		cycles[i] = rc
	}
	return cycles, nil
}

func (r ReviewRepo) CreateCycle(ctx context.Context, rc domain.ReviewCycle) error {
	row := rowFromReviewCycle(rc)
	_, err := r.remote.NamedExecContext(ctx,
		`INSERT INTO review_cycles (id, agent_session_id, cycle_number, reviewer_harness, status, summary, created_at, updated_at)
		 VALUES (:id, :agent_session_id, :cycle_number, :reviewer_harness, :status, :summary, :created_at, :updated_at)`, row)
	if err != nil {
		return fmt.Errorf("create review cycle %s: %w", rc.ID, err)
	}
	return nil
}

func (r ReviewRepo) UpdateCycle(ctx context.Context, rc domain.ReviewCycle) error {
	row := rowFromReviewCycle(rc)
	res, err := r.remote.NamedExecContext(ctx,
		`UPDATE review_cycles SET agent_session_id = :agent_session_id, cycle_number = :cycle_number,
		 reviewer_harness = :reviewer_harness, status = :status, summary = :summary,
		 updated_at = :updated_at WHERE id = :id`, row)
	if err != nil {
		return fmt.Errorf("update review cycle %s: %w", rc.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update review cycle %s: get rows affected: %w", rc.ID, err)
	}
	if n == 0 {
		return fmt.Errorf("update review cycle %s: %w", rc.ID, sql.ErrNoRows)
	}
	return nil
}

func (r ReviewRepo) GetCritique(ctx context.Context, id string) (domain.Critique, error) {
	var row critiqueRow
	if err := r.remote.GetContext(ctx, &row, `SELECT * FROM critiques WHERE id = ?`, id); err != nil {
		return domain.Critique{}, fmt.Errorf("get critique %s: %w", id, err)
	}
	return row.toDomain()
}

func (r ReviewRepo) ListCritiquesByReviewCycleID(ctx context.Context, cycleID string) ([]domain.Critique, error) {
	var rows []critiqueRow
	if err := r.remote.SelectContext(ctx, &rows, `SELECT * FROM critiques WHERE review_cycle_id = ? ORDER BY created_at`, cycleID); err != nil {
		return nil, fmt.Errorf("list critiques for review cycle %s: %w", cycleID, err)
	}
	critiques := make([]domain.Critique, len(rows))
	for i := range rows {
		c, err := rows[i].toDomain()
		if err != nil {
			return nil, fmt.Errorf("convert critique: %w", err)
		}
		critiques[i] = c
	}
	return critiques, nil
}

func (r ReviewRepo) CreateCritique(ctx context.Context, c domain.Critique) error {
	row := rowFromCritique(c)
	_, err := r.remote.NamedExecContext(ctx,
		`INSERT INTO critiques (id, review_cycle_id, file_path, line_number, severity, description, status, created_at)
		 VALUES (:id, :review_cycle_id, :file_path, :line_number, :severity, :description, :status, :created_at)`, row)
	if err != nil {
		return fmt.Errorf("create critique %s: %w", c.ID, err)
	}
	return nil
}

func (r ReviewRepo) UpdateCritique(ctx context.Context, c domain.Critique) error {
	row := rowFromCritique(c)
	res, err := r.remote.NamedExecContext(ctx,
		`UPDATE critiques SET review_cycle_id = :review_cycle_id, file_path = :file_path,
		 line_number = :line_number, severity = :severity, description = :description,
		 status = :status WHERE id = :id`, row)
	if err != nil {
		return fmt.Errorf("update critique %s: %w", c.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update critique %s: get rows affected: %w", c.ID, err)
	}
	if n == 0 {
		return fmt.Errorf("update critique %s: %w", c.ID, sql.ErrNoRows)
	}
	return nil
}
