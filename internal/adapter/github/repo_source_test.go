package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
)

func newTestRepoSource(t *testing.T, handler http.HandlerFunc) (*GithubRepoSource, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	cfg := config.GithubConfig{
		Token:   "test-token",
		BaseURL: server.URL,
	}
	src, err := NewRepoSource(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewRepoSource: %v", err)
	}
	return src, server
}

func TestGithubRepoSource_Name(t *testing.T) {
	src, _ := newTestRepoSource(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("no HTTP request expected")
	})

	if got := src.Name(); got != "github" {
		t.Errorf("Name() = %q, want %q", got, "github")
	}
}

func TestGithubRepoSource_ListUserRepos(t *testing.T) {
	wantRepos := []map[string]any{{
		"name": "repo1", "full_name": "user/repo1", "description": "A repo",
		"clone_url": "https://github.com/user/repo1.git",
		"ssh_url": "git@github.com:user/repo1.git",
		"default_branch": "main", "private": false,
		"owner": map[string]string{"login": "user"},
	}}

	src, _ := newTestRepoSource(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/repos" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer test-token")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(wantRepos)
	})

	result, err := src.ListRepos(context.Background(), adapter.RepoListOpts{})
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(result.Repos) != 1 {
		t.Fatalf("len(Repos) = %d, want 1", len(result.Repos))
	}

	repo := result.Repos[0]
	if repo.Name != "repo1" {
		t.Errorf("Name = %q, want %q", repo.Name, "repo1")
	}
	if repo.FullName != "user/repo1" {
		t.Errorf("FullName = %q, want %q", repo.FullName, "user/repo1")
	}
	if repo.Description != "A repo" {
		t.Errorf("Description = %q, want %q", repo.Description, "A repo")
	}
	if repo.URL != "https://github.com/user/repo1.git" {
		t.Errorf("URL = %q, want %q", repo.URL, "https://github.com/user/repo1.git")
	}
	if repo.SSHURL != "git@github.com:user/repo1.git" {
		t.Errorf("SSHURL = %q, want %q", repo.SSHURL, "git@github.com:user/repo1.git")
	}
	if repo.DefaultBranch != "main" {
		t.Errorf("DefaultBranch = %q, want %q", repo.DefaultBranch, "main")
	}
	if repo.IsPrivate {
		t.Errorf("IsPrivate = true, want false")
	}
	if repo.Source != "github" {
		t.Errorf("Source = %q, want %q", repo.Source, "github")
	}
	if repo.Owner != "user" {
		t.Errorf("Owner = %q, want %q", repo.Owner, "user")
	}
}

func TestGithubRepoSource_SearchRepos(t *testing.T) {
	searchResp := map[string]any{
		"total_count": 1,
		"items": []map[string]any{{
			"name": "repo1", "full_name": "user/repo1", "description": "A repo",
			"clone_url": "https://github.com/user/repo1.git",
			"ssh_url": "git@github.com:user/repo1.git",
			"default_branch": "main", "private": false,
			"owner": map[string]string{"login": "user"},
		}},
	}

	src, _ := newTestRepoSource(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/repositories" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if q := r.URL.Query().Get("q"); q != "testquery" {
			t.Errorf("search q = %q, want %q", q, "testquery")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(searchResp)
	})

	result, err := src.ListRepos(context.Background(), adapter.RepoListOpts{Search: "testquery"})
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(result.Repos) != 1 {
		t.Fatalf("len(Repos) = %d, want 1", len(result.Repos))
	}
	if result.Repos[0].Name != "repo1" {
		t.Errorf("Name = %q, want %q", result.Repos[0].Name, "repo1")
	}
}

func TestGithubRepoSource_ListRepos_Pagination(t *testing.T) {
	t.Run("full page returns HasMore true", func(t *testing.T) {
		// Default limit is 30, so returning 30 items means HasMore=true
		repos := make([]map[string]any, 30)
		for i := range repos {
			repos[i] = map[string]any{
				"name": "repo", "full_name": "user/repo", "description": nil,
				"clone_url": "https://github.com/user/repo.git",
				"ssh_url": "git@github.com:user/repo.git",
				"default_branch": "main", "private": false,
				"owner": map[string]string{"login": "user"},
			}
		}

		src, _ := newTestRepoSource(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(repos)
		})

		result, err := src.ListRepos(context.Background(), adapter.RepoListOpts{})
		if err != nil {
			t.Fatalf("ListRepos: %v", err)
		}
		if !result.HasMore {
			t.Error("HasMore = false, want true when returned items == limit")
		}
	})

	t.Run("partial page returns HasMore false", func(t *testing.T) {
		repos := make([]map[string]any, 5)
		for i := range repos {
			repos[i] = map[string]any{
				"name": "repo", "full_name": "user/repo", "description": nil,
				"clone_url": "https://github.com/user/repo.git",
				"ssh_url": "git@github.com:user/repo.git",
				"default_branch": "main", "private": false,
				"owner": map[string]string{"login": "user"},
			}
		}

		src, _ := newTestRepoSource(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(repos)
		})

		result, err := src.ListRepos(context.Background(), adapter.RepoListOpts{})
		if err != nil {
			t.Fatalf("ListRepos: %v", err)
		}
		if result.HasMore {
			t.Error("HasMore = true, want false when returned items < limit")
		}
	})
}

func TestGithubRepoSource_ListRepos_AuthError(t *testing.T) {
	src, _ := newTestRepoSource(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"Bad credentials"}`))
	})

	_, err := src.ListRepos(context.Background(), adapter.RepoListOpts{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var permErr *adapter.PermissionError
	if !errors.As(err, &permErr) {
		t.Fatalf("expected PermissionError, got %T: %v", err, err)
	}
	if permErr.Adapter != "github" {
		t.Errorf("Adapter = %q, want %q", permErr.Adapter, "github")
	}
	if permErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want %d", permErr.StatusCode, http.StatusUnauthorized)
	}
}

func TestGithubRepoSource_ListRepos_EmptyResult(t *testing.T) {
	src, _ := newTestRepoSource(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	})

	result, err := src.ListRepos(context.Background(), adapter.RepoListOpts{})
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(result.Repos) != 0 {
		t.Errorf("len(Repos) = %d, want 0", len(result.Repos))
	}
	if result.HasMore {
		t.Error("HasMore = true, want false for empty result")
	}
}
