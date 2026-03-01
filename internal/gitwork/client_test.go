package gitwork

import (
	"testing"
)

// Helper to wrap worktrees in the expected JSON structure
func wrapJSON(worktrees string) string {
	if worktrees == "" {
		return ""
	}
	return `{"data":{"worktrees":[` + worktrees + `]},"messages":[]}`
}

func TestParseListJSON(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantLen   int
		wantPaths []string
		wantErr   bool
	}{
		{
			name:      "empty output",
			input:     "",
			wantLen:   0,
			wantPaths: nil,
			wantErr:   false,
		},
		{
			name:      "empty worktrees array",
			input:     `{"data":{"worktrees":[]},"messages":[]}`,
			wantLen:   0,
			wantPaths: nil,
			wantErr:   false,
		},
		{
			name:      "single main worktree",
			input:     wrapJSON(`{"dir": "main", "branch": "main", "current": true}`),
			wantLen:   1,
			wantPaths: []string{"/repo/main"},
			wantErr:   false,
		},
		{
			name: "multiple worktrees",
			input: wrapJSON(`{"dir": "main", "branch": "main", "current": true},` +
				`{"dir": "feature-branch", "branch": "feature-branch", "current": false}`),
			wantLen:   2,
			wantPaths: []string{"/repo/main", "/repo/feature-branch"},
			wantErr:   false,
		},
		{
			name:    "invalid JSON",
			input:   "not json",
			wantErr: true,
		},
		{
			name:      "missing data field",
			input:     `{}`,
			wantLen:   0,
			wantPaths: nil,
			wantErr:   false, // missing fields get zero values
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			worktrees, err := parseListJSON([]byte(tt.input), "/repo")
			if (err != nil) != tt.wantErr {
				t.Errorf("parseListJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}
			if len(worktrees) != tt.wantLen {
				t.Errorf("parseListJSON() got %d worktrees, want %d", len(worktrees), tt.wantLen)
				return
			}
			for i, wt := range worktrees {
				if i < len(tt.wantPaths) && wt.Path != tt.wantPaths[i] {
					t.Errorf("worktree[%d].Path = %q, want %q", i, wt.Path, tt.wantPaths[i])
				}
			}
		})
	}
}

func TestParseListJSON_IsMainFlag(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name:  "current worktree (main branch)",
			input: wrapJSON(`{"dir": "main", "branch": "main", "current": true}`),
			want:  true,
		},
		{
			name:  "non-current feature branch",
			input: wrapJSON(`{"dir": "feature", "branch": "feature", "current": false}`),
			want:  false,
		},
		{
			name:  "main branch not current but still IsMain",
			input: wrapJSON(`{"dir": "main", "branch": "main", "current": false}`),
			want:  true, // branch named "main" is always IsMain
		},
		{
			name:  "master branch not current but still IsMain",
			input: wrapJSON(`{"dir": "master", "branch": "master", "current": false}`),
			want:  true, // branch named "master" is always IsMain
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			worktrees, err := parseListJSON([]byte(tt.input), "/repo")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(worktrees) != 1 {
				t.Fatalf("expected 1 worktree, got %d", len(worktrees))
			}
			if worktrees[0].IsMain != tt.want {
				t.Errorf("IsMain = %v, want %v", worktrees[0].IsMain, tt.want)
			}
		})
	}
}

func TestParseListJSON_BranchName(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantBranch string
	}{
		{
			name:       "simple branch name",
			input:      wrapJSON(`{"dir": "main", "branch": "main", "current": true}`),
			wantBranch: "main",
		},
		{
			name:       "feature branch",
			input:      wrapJSON(`{"dir": "feature-xyz", "branch": "feature-xyz", "current": false}`),
			wantBranch: "feature-xyz",
		},
		{
			name:       "branch with slash",
			input:      wrapJSON(`{"dir": "feature/auth", "branch": "feature/auth", "current": false}`),
			wantBranch: "feature/auth",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			worktrees, err := parseListJSON([]byte(tt.input), "/repo")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(worktrees) != 1 {
				t.Fatalf("expected 1 worktree, got %d", len(worktrees))
			}
			if worktrees[0].Branch != tt.wantBranch {
				t.Errorf("Branch = %q, want %q", worktrees[0].Branch, tt.wantBranch)
			}
		})
	}
}

func TestParseListJSON_PathConstruction(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		repoDir  string
		wantPath string
	}{
		{
			name:     "constructs path from dir",
			input:    wrapJSON(`{"dir": "main", "branch": "main", "current": true}`),
			repoDir:  "/home/user/projects/myrepo",
			wantPath: "/home/user/projects/myrepo/main",
		},
		{
			name:     "constructs path for feature branch",
			input:    wrapJSON(`{"dir": "feature-login", "branch": "feature-login", "current": false}`),
			repoDir:  "/home/user/projects/myrepo",
			wantPath: "/home/user/projects/myrepo/feature-login",
		},
		{
			name:     "absolute path not modified",
			input:    wrapJSON(`{"dir": "/absolute/path/to/worktree", "branch": "main", "current": true}`),
			repoDir:  "/home/user/projects/myrepo",
			wantPath: "/absolute/path/to/worktree",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			worktrees, err := parseListJSON([]byte(tt.input), tt.repoDir)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(worktrees) != 1 {
				t.Fatalf("expected 1 worktree, got %d", len(worktrees))
			}
			if worktrees[0].Path != tt.wantPath {
				t.Errorf("Path = %q, want %q", worktrees[0].Path, tt.wantPath)
			}
		})
	}
}
