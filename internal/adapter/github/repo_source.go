package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
)

// GithubRepoSource lists the user's GitHub repositories and supports search.
type GithubRepoSource struct {
	client  httpClient
	baseURL string
	token   string
}

// NewRepoSource creates a GitHub repo source, resolving the token from config
// or falling back to the gh CLI.
func NewRepoSource(ctx context.Context, cfg config.GithubConfig) (*GithubRepoSource, error) {
	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		var err error
		token, err = execTokenResolver(ctx)
		if err != nil {
			return nil, fmt.Errorf("resolve github token: %w", err)
		}
	}
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	return &GithubRepoSource{
		client:  &http.Client{Timeout: 30 * time.Second},
		baseURL: baseURL,
		token:   token,
	}, nil
}

// Name returns the source identifier.
func (s *GithubRepoSource) Name() string { return adapterName }

// ListRepos returns repositories available for cloning.
func (s *GithubRepoSource) ListRepos(ctx context.Context, opts adapter.RepoListOpts) (*adapter.RepoListResult, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 30
	}
	page := opts.Page
	if page <= 0 {
		page = 1
	}

	if opts.Search != "" {
		return s.searchRepos(ctx, opts.Search, limit, page)
	}
	return s.listUserRepos(ctx, limit, page)
}

func (s *GithubRepoSource) listUserRepos(ctx context.Context, limit, page int) (*adapter.RepoListResult, error) {
	query := url.Values{
		"type":     {"owner"},
		"sort":     {"updated"},
		"per_page": {strconv.Itoa(limit)},
		"page":     {strconv.Itoa(page)},
	}

	var repos []githubRepoItem
	if err := s.getJSON(ctx, "/user/repos", query, &repos); err != nil {
		return nil, fmt.Errorf("list github repos: %w", err)
	}

	return &adapter.RepoListResult{
		Repos:   mapGithubRepos(repos),
		HasMore: len(repos) == limit,
	}, nil
}

func (s *GithubRepoSource) searchRepos(ctx context.Context, search string, limit, page int) (*adapter.RepoListResult, error) {
	query := url.Values{
		"q":        {search},
		"sort":     {"updated"},
		"per_page": {strconv.Itoa(limit)},
		"page":     {strconv.Itoa(page)},
	}

	var result githubSearchResult
	if err := s.getJSON(ctx, "/search/repositories", query, &result); err != nil {
		return nil, fmt.Errorf("search github repos: %w", err)
	}

	return &adapter.RepoListResult{
		Repos:   mapGithubRepos(result.Items),
		HasMore: len(result.Items) == limit,
	}, nil
}

// getJSON performs an authenticated GET and decodes the JSON response.
func (s *GithubRepoSource) getJSON(ctx context.Context, endpoint string, query url.Values, dst any) error {
	fullURL, err := url.Parse(s.baseURL)
	if err != nil {
		return fmt.Errorf("parse base url: %w", err)
	}
	fullURL.Path = path.Join(fullURL.Path, endpoint)
	fullURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL.String(), nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	limitedBody := io.LimitReader(resp.Body, maxResponseBodyBytes)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(limitedBody)
		body := strings.TrimSpace(string(data))
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return &adapter.PermissionError{Adapter: adapterName, StatusCode: resp.StatusCode, Body: body}
		}
		return fmt.Errorf("github api status %d: %s", resp.StatusCode, body)
	}
	if dst == nil {
		return nil
	}
	if err := json.NewDecoder(limitedBody).Decode(dst); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// -- JSON structs for GitHub API responses --

type githubRepoItem struct {
	Name          string  `json:"name"`
	FullName      string  `json:"full_name"`
	Description   *string `json:"description"`
	HTMLURL       string  `json:"html_url"`
	CloneURL      string  `json:"clone_url"`
	SSHURL        string  `json:"ssh_url"`
	DefaultBranch string  `json:"default_branch"`
	Private       bool    `json:"private"`
	Owner         struct {
		Login string `json:"login"`
	} `json:"owner"`
}

type githubSearchResult struct {
	TotalCount int              `json:"total_count"`
	Items      []githubRepoItem `json:"items"`
}

// -- helpers --

func mapGithubRepos(items []githubRepoItem) []adapter.RepoItem {
	repos := make([]adapter.RepoItem, 0, len(items))
	for _, item := range items {
		desc := ""
		if item.Description != nil {
			desc = *item.Description
		}
		repos = append(repos, adapter.RepoItem{
			Name:          item.Name,
			FullName:      item.FullName,
			Description:   desc,
			URL:           item.CloneURL,
			SSHURL:        item.SSHURL,
			DefaultBranch: item.DefaultBranch,
			IsPrivate:     item.Private,
			Source:        adapterName,
			Owner:         item.Owner.Login,
		})
	}
	return repos
}
