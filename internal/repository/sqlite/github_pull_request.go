package sqlite

import (
	"context"
	"fmt"

	"github.com/beeemT/go-atomic/generic"
	"github.com/beeemT/substrate/internal/domain"
)

type githubPRRow struct {
	ID         string  `db:"id"`
	Owner      string  `db:"owner"`
	Repo       string  `db:"repo"`
	Number     int     `db:"number"`
	State      string  `db:"state"`
	Draft      int     `db:"draft"`
	HeadBranch string  `db:"head_branch"`
	HTMLURL    string  `db:"html_url"`
	MergedAt   *string `db:"merged_at"`
	CreatedAt  string  `db:"created_at"`
	UpdatedAt  string  `db:"updated_at"`
}

func (r *githubPRRow) toDomain() (domain.GithubPullRequest, error) {
	mergedAt, err := parseTimePtr(r.MergedAt)
	if err != nil {
		return domain.GithubPullRequest{}, fmt.Errorf("merged_at: %w", err)
	}
	createdAt, err := parseTime(r.CreatedAt)
	if err != nil {
		return domain.GithubPullRequest{}, fmt.Errorf("created_at: %w", err)
	}
	updatedAt, err := parseTime(r.UpdatedAt)
	if err != nil {
		return domain.GithubPullRequest{}, fmt.Errorf("updated_at: %w", err)
	}

	return domain.GithubPullRequest{
		ID:         r.ID,
		Owner:      r.Owner,
		Repo:       r.Repo,
		Number:     r.Number,
		State:      r.State,
		Draft:      r.Draft != 0,
		HeadBranch: r.HeadBranch,
		HTMLURL:    r.HTMLURL,
		MergedAt:   mergedAt,
		CreatedAt:  createdAt,
		UpdatedAt:  updatedAt,
	}, nil
}

func rowFromGithubPR(pr domain.GithubPullRequest) githubPRRow {
	draft := 0
	if pr.Draft {
		draft = 1
	}

	return githubPRRow{
		ID:         pr.ID,
		Owner:      pr.Owner,
		Repo:       pr.Repo,
		Number:     pr.Number,
		State:      pr.State,
		Draft:      draft,
		HeadBranch: pr.HeadBranch,
		HTMLURL:    pr.HTMLURL,
		MergedAt:   formatTimePtr(pr.MergedAt),
		CreatedAt:  formatTime(pr.CreatedAt),
		UpdatedAt:  formatTime(pr.UpdatedAt),
	}
}

// GithubPRRepo implements repository.GithubPullRequestRepository using SQLite.
type GithubPRRepo struct{ remote generic.SQLXRemote }

func NewGithubPRRepo(remote generic.SQLXRemote) GithubPRRepo {
	return GithubPRRepo{remote: remote}
}

func (r GithubPRRepo) Upsert(ctx context.Context, pr domain.GithubPullRequest) error {
	row := rowFromGithubPR(pr)
	_, err := r.remote.NamedExecContext(ctx,
		`INSERT INTO github_pull_requests (id, owner, repo, number, state, draft, head_branch, html_url, merged_at, created_at, updated_at)
		 VALUES (:id, :owner, :repo, :number, :state, :draft, :head_branch, :html_url, :merged_at, :created_at, :updated_at)
		 ON CONFLICT(owner, repo, number) DO UPDATE SET
		   state = excluded.state,
		   draft = excluded.draft,
		   head_branch = excluded.head_branch,
		   html_url = excluded.html_url,
		   merged_at = excluded.merged_at,
		   updated_at = excluded.updated_at`, row)
	if err != nil {
		return fmt.Errorf("upsert github pr %s: %w", pr.ID, err)
	}

	return nil
}

func (r GithubPRRepo) Get(ctx context.Context, id string) (domain.GithubPullRequest, error) {
	var row githubPRRow
	if err := r.remote.GetContext(ctx, &row, `SELECT * FROM github_pull_requests WHERE id = ?`, id); err != nil {
		return domain.GithubPullRequest{}, fmt.Errorf("get github pr %s: %w", id, err)
	}

	return row.toDomain()
}

func (r GithubPRRepo) GetByNumber(ctx context.Context, owner, repo string, number int) (domain.GithubPullRequest, error) {
	var row githubPRRow
	if err := r.remote.GetContext(ctx, &row,
		`SELECT * FROM github_pull_requests WHERE owner = ? AND repo = ? AND number = ?`,
		owner, repo, number); err != nil {
		return domain.GithubPullRequest{}, fmt.Errorf("get github pr %s/%s#%d: %w", owner, repo, number, err)
	}

	return row.toDomain()
}

func (r GithubPRRepo) ListByWorkspaceID(ctx context.Context, workspaceID string) ([]domain.GithubPullRequest, error) {
	var rows []githubPRRow
	if err := r.remote.SelectContext(ctx, &rows,
		`SELECT g.* FROM github_pull_requests g
		 JOIN session_review_artifacts s ON s.provider_artifact_id = g.id
		 WHERE s.workspace_id = ? AND s.provider = 'github'
		 ORDER BY g.updated_at DESC`, workspaceID); err != nil {
		return nil, fmt.Errorf("list github prs for workspace %s: %w", workspaceID, err)
	}

	return r.toDomainSlice(rows)
}

func (r GithubPRRepo) ListNonTerminal(ctx context.Context, workspaceID string) ([]domain.GithubPullRequest, error) {
	var rows []githubPRRow
	if err := r.remote.SelectContext(ctx, &rows,
		`SELECT g.* FROM github_pull_requests g
		 JOIN session_review_artifacts s ON s.provider_artifact_id = g.id
		 WHERE s.workspace_id = ? AND s.provider = 'github'
		   AND g.state NOT IN ('merged', 'closed')
		 ORDER BY g.updated_at DESC`, workspaceID); err != nil {
		return nil, fmt.Errorf("list non-terminal github prs for workspace %s: %w", workspaceID, err)
	}

	return r.toDomainSlice(rows)
}

func (r GithubPRRepo) toDomainSlice(rows []githubPRRow) ([]domain.GithubPullRequest, error) {
	prs := make([]domain.GithubPullRequest, len(rows))
	for i := range rows {
		pr, err := rows[i].toDomain()
		if err != nil {
			return nil, fmt.Errorf("convert github pr: %w", err)
		}
		prs[i] = pr
	}

	return prs, nil
}
