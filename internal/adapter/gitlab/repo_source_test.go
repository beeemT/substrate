package gitlab

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

func newTestRepoSource(t *testing.T, handler http.HandlerFunc) (*GitlabRepoSource, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	cfg := config.GitlabConfig{
		Token:   "test-token",
		BaseURL: server.URL,
	}
	src, err := NewRepoSource(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewRepoSource: %v", err)
	}
	return src, server
}

func sampleProjectsJSON(visibility string) string {
	type owner struct {
		Username string `json:"username"`
	}
	type project struct {
		ID                int64   `json:"id"`
		Name              string  `json:"name"`
		PathWithNamespace string  `json:"path_with_namespace"`
		Description       *string `json:"description"`
		HTTPURL           string  `json:"http_url_to_repo"`
		SSHURL            string  `json:"ssh_url_to_repo"`
		DefaultBranch     string  `json:"default_branch"`
		Visibility        string  `json:"visibility"`
		Owner             owner   `json:"owner"`
	}
	desc := "A repo"
	projects := []project{{
		ID:                1,
		Name:              "repo1",
		PathWithNamespace: "group/repo1",
		Description:       &desc,
		HTTPURL:           "https://gitlab.com/group/repo1.git",
		SSHURL:            "git@gitlab.com:group/repo1.git",
		DefaultBranch:     "main",
		Visibility:        visibility,
		Owner:             owner{Username: "user"},
	}}
	b, _ := json.Marshal(projects)
	return string(b)
}

func TestGitlabRepoSource_Name(t *testing.T) {
	t.Parallel()
	handler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("unexpected HTTP request")
	})
	src, _ := newTestRepoSource(t, handler)

	if got := src.Name(); got != "gitlab" {
		t.Fatalf("Name() = %q, want %q", got, "gitlab")
	}
}

func TestGitlabRepoSource_ListProjects(t *testing.T) {
	t.Parallel()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Browse with OwnedOnly=true must send both membership=true and owned=true.
		if got := r.URL.Query().Get("membership"); got != "true" {
			t.Errorf("membership param = %q, want %q", got, "true")
		}
		if got := r.URL.Query().Get("owned"); got != "true" {
			t.Errorf("owned param = %q, want %q", got, "true")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(sampleProjectsJSON("public")))
	})
	src, _ := newTestRepoSource(t, handler)

	result, err := src.ListRepos(context.Background(), adapter.RepoListOpts{OwnedOnly: true})
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
	if repo.FullName != "group/repo1" {
		t.Errorf("FullName = %q, want %q", repo.FullName, "group/repo1")
	}
	if repo.Description != "A repo" {
		t.Errorf("Description = %q, want %q", repo.Description, "A repo")
	}
	if repo.URL != "https://gitlab.com/group/repo1.git" {
		t.Errorf("URL = %q, want %q", repo.URL, "https://gitlab.com/group/repo1.git")
	}
	if repo.SSHURL != "git@gitlab.com:group/repo1.git" {
		t.Errorf("SSHURL = %q, want %q", repo.SSHURL, "git@gitlab.com:group/repo1.git")
	}
	if repo.DefaultBranch != "main" {
		t.Errorf("DefaultBranch = %q, want %q", repo.DefaultBranch, "main")
	}
	if repo.Source != "gitlab" {
		t.Errorf("Source = %q, want %q", repo.Source, "gitlab")
	}
	if repo.Owner != "user" {
		t.Errorf("Owner = %q, want %q", repo.Owner, "user")
	}
	if repo.IsPrivate {
		t.Errorf("IsPrivate = true, want false for public visibility")
	}
	if result.HasMore {
		t.Errorf("HasMore = true, want false (no Link header)")
	}
}

func TestGitlabRepoSource_ListProjects_AllRepos(t *testing.T) {
	t.Parallel()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// OwnedOnly=false: membership=true must be present, owned must NOT be sent.
		if got := r.URL.Query().Get("membership"); got != "true" {
			t.Errorf("membership param = %q, want %q", got, "true")
		}
		if r.URL.Query().Has("owned") {
			t.Error("owned param must not be sent when OwnedOnly is false")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(sampleProjectsJSON("public")))
	})
	src, _ := newTestRepoSource(t, handler)

	result, err := src.ListRepos(context.Background(), adapter.RepoListOpts{OwnedOnly: false})
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(result.Repos) != 1 {
		t.Fatalf("len(Repos) = %d, want 1", len(result.Repos))
	}
}

func TestGitlabRepoSource_SearchProjects(t *testing.T) {
	t.Parallel()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		search := r.URL.Query().Get("search")
		if search != "my-repo" {
			t.Errorf("search param = %q, want %q", search, "my-repo")
		}
		// When search is set, membership/owned params should NOT be sent.
		if r.URL.Query().Has("membership") {
			t.Error("membership param should not be set during search")
		}
		if r.URL.Query().Has("owned") {
			t.Error("owned param should not be set during search")
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(sampleProjectsJSON("public")))
	})
	src, _ := newTestRepoSource(t, handler)

	result, err := src.ListRepos(context.Background(), adapter.RepoListOpts{Search: "my-repo"})
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

func TestGitlabRepoSource_Pagination_LinkHeader(t *testing.T) {
	t.Parallel()

	t.Run("next page present", func(t *testing.T) {
		t.Parallel()
		handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Link", "</api/v4/projects?page=2>; rel=\"next\", </api/v4/projects?page=5>; rel=\"last\"")
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("[]"))
		})
		src, server := newTestRepoSource(t, handler)

		// Verify the Link header we send is well-formed by checking server URL usage.
		// The handler above uses relative paths in the Link header which is fine
		// for parseLinkHeaderNext — it only checks for rel="next" presence.
		_ = server

		result, err := src.ListRepos(context.Background(), adapter.RepoListOpts{})
		if err != nil {
			t.Fatalf("ListRepos: %v", err)
		}
		if !result.HasMore {
			t.Errorf("HasMore = false, want true (Link header has rel=next)")
		}
	})

	t.Run("no next page", func(t *testing.T) {
		t.Parallel()
		handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("[]"))
		})
		src, _ := newTestRepoSource(t, handler)

		result, err := src.ListRepos(context.Background(), adapter.RepoListOpts{})
		if err != nil {
			t.Fatalf("ListRepos: %v", err)
		}
		if result.HasMore {
			t.Errorf("HasMore = true, want false (no Link header)")
		}
	})
}

func TestGitlabRepoSource_AuthError(t *testing.T) {
	t.Parallel()
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"401 Unauthorized"}`))
	})
	src, _ := newTestRepoSource(t, handler)

	_, err := src.ListRepos(context.Background(), adapter.RepoListOpts{})
	if err == nil {
		t.Fatal("ListRepos: expected error, got nil")
	}

	var permErr *adapter.PermissionError
	if !errors.As(err, &permErr) {
		t.Fatalf("error type = %T, want *adapter.PermissionError", err)
	}
	if permErr.Adapter != "gitlab" {
		t.Errorf("Adapter = %q, want %q", permErr.Adapter, "gitlab")
	}
	if permErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want %d", permErr.StatusCode, http.StatusUnauthorized)
	}
}

func TestGitlabRepoSource_PrivateVisibility(t *testing.T) {
	t.Parallel()
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(sampleProjectsJSON("private")))
	})
	src, _ := newTestRepoSource(t, handler)

	result, err := src.ListRepos(context.Background(), adapter.RepoListOpts{})
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(result.Repos) != 1 {
		t.Fatalf("len(Repos) = %d, want 1", len(result.Repos))
	}
	if !result.Repos[0].IsPrivate {
		t.Errorf("IsPrivate = false, want true for private visibility")
	}
}

func TestGitlabRepoSource_InternalVisibility(t *testing.T) {
	t.Parallel()
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(sampleProjectsJSON("internal")))
	})
	src, _ := newTestRepoSource(t, handler)

	result, err := src.ListRepos(context.Background(), adapter.RepoListOpts{})
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(result.Repos) != 1 {
		t.Fatalf("len(Repos) = %d, want 1", len(result.Repos))
	}
	if !result.Repos[0].IsPrivate {
		t.Errorf("IsPrivate = false, want true for internal visibility")
	}
}
