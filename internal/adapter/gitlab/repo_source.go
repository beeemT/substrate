package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
)

// GitlabRepoSource lists the user's GitLab projects for repository selection.
type GitlabRepoSource struct {
	cfg     config.GitlabConfig
	baseURL string
	client  httpDoer
}

type gitlabProjectItem struct {
	ID                int64   `json:"id"`
	Name              string  `json:"name"`
	PathWithNamespace string  `json:"path_with_namespace"`
	Description       *string `json:"description"`
	WebURL            string  `json:"web_url"`
	HTTPURL           string  `json:"http_url_to_repo"`
	SSHURL            string  `json:"ssh_url_to_repo"`
	DefaultBranch     string  `json:"default_branch"`
	Visibility        string  `json:"visibility"`
	Owner             struct {
		Username string `json:"username"`
	} `json:"owner"`
}

// NewRepoSource creates a GitLab repo source with token resolution.
// If cfg.Token is empty, it falls back to the glab CLI token.
func NewRepoSource(ctx context.Context, cfg config.GitlabConfig) (*GitlabRepoSource, error) {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = "https://gitlab.com"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		host := hostFromBaseURL(baseURL)
		var err error
		token, err = execTokenResolver(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("resolve gitlab token: %w", err)
		}
	}
	cfg.Token = token

	return &GitlabRepoSource{
		cfg:     cfg,
		baseURL: baseURL,
		client:  &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// Name returns the source identifier.
func (s *GitlabRepoSource) Name() string { return adapterName }

// ListRepos returns a page of GitLab projects.
// When opts.Search is non-empty, the GitLab search API is used.
// Pagination is driven by the Link response header.
func (s *GitlabRepoSource) ListRepos(ctx context.Context, opts adapter.RepoListOpts) (*adapter.RepoListResult, error) {
	if opts.Limit <= 0 {
		opts.Limit = 30
	}
	if opts.Page <= 0 {
		opts.Page = 1
	}

	query := url.Values{
		"order_by": {"last_activity_at"},
		"per_page": {strconv.Itoa(opts.Limit)},
		"page":     {strconv.Itoa(opts.Page)},
	}

	if opts.Search != "" {
		query.Set("search", opts.Search)
	} else {
		// membership=true returns all projects the user is a member of, including group projects.
		// owned=true additionally restricts to only projects in the user's personal namespace;
		// callers opt in via OwnedOnly (the TUI default).
		query.Set("membership", "true")
		if opts.OwnedOnly {
			query.Set("owned", "true")
		}
	}

	endpoint := path.Join("/api/v4", "projects")

	fullURL, err := url.Parse(s.baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base url: %w", err)
	}
	fullURL.Path = path.Join(fullURL.Path, endpoint)
	fullURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(s.cfg.Token))

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list gitlab repos: %w", err)
	}
	defer resp.Body.Close()

	limitedBody := io.LimitReader(resp.Body, maxResponseBodyBytes)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(limitedBody)
		body := strings.TrimSpace(string(data))
		slog.Error("gitlab list repos failed", "status", resp.StatusCode, "body", body)
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, &adapter.PermissionError{Adapter: adapterName, StatusCode: resp.StatusCode, Body: body}
		}
		return nil, fmt.Errorf("gitlab list repos: %s", resp.Status)
	}

	var items []gitlabProjectItem
	if err := json.NewDecoder(limitedBody).Decode(&items); err != nil {
		return nil, fmt.Errorf("decode gitlab projects: %w", err)
	}

	repos := make([]adapter.RepoItem, 0, len(items))
	for _, p := range items {
		desc := ""
		if p.Description != nil {
			desc = *p.Description
		}
		repos = append(repos, adapter.RepoItem{
			Name:          p.Name,
			FullName:      p.PathWithNamespace,
			Description:   desc,
			URL:           p.HTTPURL,
			SSHURL:        p.SSHURL,
			DefaultBranch: p.DefaultBranch,
			IsPrivate:     p.Visibility == "private" || p.Visibility == "internal",
			Source:        adapterName,
			Owner:         p.Owner.Username,
		})
	}

	return &adapter.RepoListResult{
		Repos:   repos,
		HasMore: parseLinkHeaderNext(resp.Header.Get("Link")),
	}, nil
}

// parseLinkHeaderNext checks a GitLab Link header for a rel="next" entry.
// GitLab format: <url>; rel="next", <url>; rel="last"
func parseLinkHeaderNext(link string) bool {
	if link == "" {
		return false
	}
	for part := range strings.SplitSeq(link, ",") {
		part = strings.TrimSpace(part)
		segments := strings.Split(part, ";")
		if len(segments) < 2 {
			continue
		}
		rel := strings.TrimSpace(segments[1])
		if strings.HasPrefix(rel, `rel="next"`) {
			return true
		}
	}
	return false
}

var _ adapter.RepoSource = (*GitlabRepoSource)(nil)
