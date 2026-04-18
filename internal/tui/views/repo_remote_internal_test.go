package views

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRepoSlugFromURL verifies URL normalization to lowercase owner/repo slugs.
func TestRepoSlugFromURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"https with .git", "https://github.com/beeemT/substrate.git", "beeemt/substrate"},
		{"https without .git", "https://github.com/beeemT/substrate", "beeemt/substrate"},
		{"ssh with .git", "git@github.com:beeemT/substrate.git", "beeemt/substrate"},
		{"ssh without .git", "git@github.com:beeemT/substrate", "beeemt/substrate"},
		{"gitlab https", "https://gitlab.com/group/repo.git", "group/repo"},
		{"gitlab ssh", "git@gitlab.com:group/repo.git", "group/repo"},
		{"mixed case preserved lowercase", "https://github.com/Owner/Repo.git", "owner/repo"},
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := repoSlugFromURL(tc.input)
			if got != tc.want {
				t.Errorf("repoSlugFromURL(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestReadOriginRemoteURL verifies config parsing.
func TestReadOriginRemoteURL(t *testing.T) {
	t.Parallel()

	writeConfig := func(t *testing.T, content string) string {
		t.Helper()
		dir := t.TempDir()
		path := filepath.Join(dir, "config")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
		return path
	}

	t.Run("https url", func(t *testing.T) {
		t.Parallel()
		cfg := writeConfig(t, `[core]
	bare = true
[remote "origin"]
	url = https://github.com/owner/repo.git
	fetch = +refs/heads/*:refs/remotes/origin/*
`)
		got := readOriginRemoteURL(cfg)
		if got != "https://github.com/owner/repo.git" {
			t.Errorf("got %q, want https url", got)
		}
	})

	t.Run("ssh url", func(t *testing.T) {
		t.Parallel()
		cfg := writeConfig(t, `[core]
	bare = true
[remote "origin"]
	url = git@github.com:owner/repo.git
`)
		got := readOriginRemoteURL(cfg)
		if got != "git@github.com:owner/repo.git" {
			t.Errorf("got %q, want ssh url", got)
		}
	})

	t.Run("no origin section", func(t *testing.T) {
		t.Parallel()
		cfg := writeConfig(t, `[core]
	bare = true
`)
		got := readOriginRemoteURL(cfg)
		if got != "" {
			t.Errorf("got %q, want empty for missing section", got)
		}
	})

	t.Run("origin section without url key", func(t *testing.T) {
		t.Parallel()
		cfg := writeConfig(t, `[remote "origin"]
	fetch = +refs/heads/*:refs/remotes/origin/*
`)
		got := readOriginRemoteURL(cfg)
		if got != "" {
			t.Errorf("got %q, want empty when url key absent", got)
		}
	})

	t.Run("file not found", func(t *testing.T) {
		t.Parallel()
		got := readOriginRemoteURL("/nonexistent/path/config")
		if got != "" {
			t.Errorf("got %q, want empty for missing file", got)
		}
	})

	t.Run("multiple remotes only origin read", func(t *testing.T) {
		t.Parallel()
		cfg := writeConfig(t, `[remote "upstream"]
	url = https://github.com/other/repo.git
[remote "origin"]
	url = https://github.com/owner/repo.git
`)
		got := readOriginRemoteURL(cfg)
		if got != "https://github.com/owner/repo.git" {
			t.Errorf("got %q, want origin url", got)
		}
	})
}
