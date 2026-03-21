package sqlite

import (
	"context"
	"fmt"

	"github.com/beeemT/go-atomic/generic"
	"github.com/beeemT/substrate/internal/domain"
)

type gitlabMRRow struct {
	ID           string `db:"id"`
	ProjectPath  string `db:"project_path"`
	IID          int    `db:"iid"`
	State        string `db:"state"`
	Draft        int    `db:"draft"`
	SourceBranch string `db:"source_branch"`
	WebURL       string `db:"web_url"`
	CreatedAt    string `db:"created_at"`
	UpdatedAt    string `db:"updated_at"`
}

func (r *gitlabMRRow) toDomain() (domain.GitlabMergeRequest, error) {
	createdAt, err := parseTime(r.CreatedAt)
	if err != nil {
		return domain.GitlabMergeRequest{}, fmt.Errorf("created_at: %w", err)
	}
	updatedAt, err := parseTime(r.UpdatedAt)
	if err != nil {
		return domain.GitlabMergeRequest{}, fmt.Errorf("updated_at: %w", err)
	}

	return domain.GitlabMergeRequest{
		ID:           r.ID,
		ProjectPath:  r.ProjectPath,
		IID:          r.IID,
		State:        r.State,
		Draft:        r.Draft != 0,
		SourceBranch: r.SourceBranch,
		WebURL:       r.WebURL,
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
	}, nil
}

func rowFromGitlabMR(mr domain.GitlabMergeRequest) gitlabMRRow {
	draft := 0
	if mr.Draft {
		draft = 1
	}

	return gitlabMRRow{
		ID:           mr.ID,
		ProjectPath:  mr.ProjectPath,
		IID:          mr.IID,
		State:        mr.State,
		Draft:        draft,
		SourceBranch: mr.SourceBranch,
		WebURL:       mr.WebURL,
		CreatedAt:    formatTime(mr.CreatedAt),
		UpdatedAt:    formatTime(mr.UpdatedAt),
	}
}

// GitlabMRRepo implements repository.GitlabMergeRequestRepository using SQLite.
type GitlabMRRepo struct{ remote generic.SQLXRemote }

func NewGitlabMRRepo(remote generic.SQLXRemote) GitlabMRRepo {
	return GitlabMRRepo{remote: remote}
}

func (r GitlabMRRepo) Upsert(ctx context.Context, mr domain.GitlabMergeRequest) error {
	row := rowFromGitlabMR(mr)
	_, err := r.remote.NamedExecContext(ctx,
		`INSERT INTO gitlab_merge_requests (id, project_path, iid, state, draft, source_branch, web_url, created_at, updated_at)
		 VALUES (:id, :project_path, :iid, :state, :draft, :source_branch, :web_url, :created_at, :updated_at)
		 ON CONFLICT(project_path, iid) DO UPDATE SET
		   state = excluded.state,
		   draft = excluded.draft,
		   source_branch = excluded.source_branch,
		   web_url = excluded.web_url,
		   updated_at = excluded.updated_at`, row)
	if err != nil {
		return fmt.Errorf("upsert gitlab mr %s: %w", mr.ID, err)
	}

	return nil
}

func (r GitlabMRRepo) Get(ctx context.Context, id string) (domain.GitlabMergeRequest, error) {
	var row gitlabMRRow
	if err := r.remote.GetContext(ctx, &row, `SELECT * FROM gitlab_merge_requests WHERE id = ?`, id); err != nil {
		return domain.GitlabMergeRequest{}, fmt.Errorf("get gitlab mr %s: %w", id, err)
	}

	return row.toDomain()
}

func (r GitlabMRRepo) GetByIID(ctx context.Context, projectPath string, iid int) (domain.GitlabMergeRequest, error) {
	var row gitlabMRRow
	if err := r.remote.GetContext(ctx, &row,
		`SELECT * FROM gitlab_merge_requests WHERE project_path = ? AND iid = ?`,
		projectPath, iid); err != nil {
		return domain.GitlabMergeRequest{}, fmt.Errorf("get gitlab mr %s!%d: %w", projectPath, iid, err)
	}

	return row.toDomain()
}

func (r GitlabMRRepo) ListByWorkspaceID(ctx context.Context, workspaceID string) ([]domain.GitlabMergeRequest, error) {
	var rows []gitlabMRRow
	if err := r.remote.SelectContext(ctx, &rows,
		`SELECT g.* FROM gitlab_merge_requests g
		 JOIN session_review_artifacts s ON s.provider_artifact_id = g.id
		 WHERE s.workspace_id = ? AND s.provider = 'gitlab'
		 ORDER BY g.updated_at DESC`, workspaceID); err != nil {
		return nil, fmt.Errorf("list gitlab mrs for workspace %s: %w", workspaceID, err)
	}

	return r.toDomainSlice(rows)
}

func (r GitlabMRRepo) ListNonTerminal(ctx context.Context, workspaceID string) ([]domain.GitlabMergeRequest, error) {
	var rows []gitlabMRRow
	if err := r.remote.SelectContext(ctx, &rows,
		`SELECT g.* FROM gitlab_merge_requests g
		 JOIN session_review_artifacts s ON s.provider_artifact_id = g.id
		 WHERE s.workspace_id = ? AND s.provider = 'gitlab'
		   AND g.state NOT IN ('merged', 'closed')
		 ORDER BY g.updated_at DESC`, workspaceID); err != nil {
		return nil, fmt.Errorf("list non-terminal gitlab mrs for workspace %s: %w", workspaceID, err)
	}

	return r.toDomainSlice(rows)
}

func (r GitlabMRRepo) toDomainSlice(rows []gitlabMRRow) ([]domain.GitlabMergeRequest, error) {
	mrs := make([]domain.GitlabMergeRequest, len(rows))
	for i := range rows {
		mr, err := rows[i].toDomain()
		if err != nil {
			return nil, fmt.Errorf("convert gitlab mr: %w", err)
		}
		mrs[i] = mr
	}

	return mrs, nil
}
