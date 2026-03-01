package gitwork

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitWorkspace(t *testing.T) {
	// Create temp dir
	dir, err := os.MkdirTemp("", "substrate-workspace-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	ws, err := InitWorkspace(dir, "test-workspace")
	if err != nil {
		t.Fatalf("InitWorkspace() error = %v", err)
	}

	// Verify workspace file was created
	workspacePath := filepath.Join(dir, WorkspaceFileName)
	if _, err := os.Stat(workspacePath); os.IsNotExist(err) {
		t.Error("workspace file was not created")
	}

	// Verify workspace ID is a valid ULID
	if err := ValidateWorkspaceID(ws.ID); err != nil {
		t.Errorf("workspace ID is not a valid ULID: %v", err)
	}

	// Verify name
	if ws.Name != "test-workspace" {
		t.Errorf("Name = %q, want %q", ws.Name, "test-workspace")
	}

	// Verify timestamp is recent
	if ws.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero")
	}
}

func TestInitWorkspace_AlreadyExists(t *testing.T) {
	// Create temp dir
	dir, err := os.MkdirTemp("", "substrate-workspace-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	// Create workspace file
	if _, err := InitWorkspace(dir, "first"); err != nil {
		t.Fatalf("first InitWorkspace() error = %v", err)
	}

	// Try to create again
	_, err = InitWorkspace(dir, "second")
	if !IsWorkspaceExists(err) {
		t.Errorf("second InitWorkspace() error = %v, want ErrWorkspaceExists", err)
	}
}

func TestReadWorkspaceFile(t *testing.T) {
	// Create temp dir
	dir, err := os.MkdirTemp("", "substrate-workspace-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	// Create workspace
	original, err := InitWorkspace(dir, "test-workspace")
	if err != nil {
		t.Fatalf("InitWorkspace() error = %v", err)
	}

	// Read it back
	read, err := ReadWorkspaceFile(dir)
	if err != nil {
		t.Fatalf("ReadWorkspaceFile() error = %v", err)
	}

	// Verify contents match
	if read.ID != original.ID {
		t.Errorf("ID = %q, want %q", read.ID, original.ID)
	}
	if read.Name != original.Name {
		t.Errorf("Name = %q, want %q", read.Name, original.Name)
	}
}

func TestReadWorkspaceFile_NotFound(t *testing.T) {
	// Create temp dir without workspace file
	dir, err := os.MkdirTemp("", "substrate-workspace-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	_, err = ReadWorkspaceFile(dir)
	if !IsNotInWorkspace(err) {
		t.Errorf("ReadWorkspaceFile() error = %v, want ErrNotInWorkspace", err)
	}
}

func TestFindWorkspace(t *testing.T) {
	// Create nested directory structure
	rootDir, err := os.MkdirTemp("", "substrate-workspace-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(rootDir)

	subDir := filepath.Join(rootDir, "subdir", "nested")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("create nested dir: %v", err)
	}

	// Create workspace at root
	ws, err := InitWorkspace(rootDir, "test-workspace")
	if err != nil {
		t.Fatalf("InitWorkspace() error = %v", err)
	}

	// Find from nested directory
	foundDir, foundWS, err := FindWorkspace(subDir)
	if err != nil {
		t.Fatalf("FindWorkspace() error = %v", err)
	}

	if foundDir != rootDir {
		t.Errorf("foundDir = %q, want %q", foundDir, rootDir)
	}
	if foundWS.ID != ws.ID {
		t.Errorf("foundWS.ID = %q, want %q", foundWS.ID, ws.ID)
	}
}

func TestFindWorkspace_NotFound(t *testing.T) {
	// Create temp dir without workspace file
	dir, err := os.MkdirTemp("", "substrate-workspace-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	_, _, err = FindWorkspace(dir)
	if !IsNotInWorkspace(err) {
		t.Errorf("FindWorkspace() error = %v, want ErrNotInWorkspace", err)
	}
}

func TestDiscoverRepos(t *testing.T) {
	// Create temp workspace dir
	workspaceDir, err := os.MkdirTemp("", "substrate-workspace-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(workspaceDir)

	// Create git-work repo (has .bare subdirectory)
	repo1 := filepath.Join(workspaceDir, "repo1")
	if err := os.MkdirAll(filepath.Join(repo1, ".bare"), 0o755); err != nil {
		t.Fatalf("create git-work repo1: %v", err)
	}

	// Create another git-work repo
	repo2 := filepath.Join(workspaceDir, "repo2")
	if err := os.MkdirAll(filepath.Join(repo2, ".bare"), 0o755); err != nil {
		t.Fatalf("create git-work repo2: %v", err)
	}

	// Create a regular directory (no .bare)
	regularDir := filepath.Join(workspaceDir, "regular-dir")
	if err := os.MkdirAll(regularDir, 0o755); err != nil {
		t.Fatalf("create regular dir: %v", err)
	}

	// Create a regular file (should be ignored)
	regularFile := filepath.Join(workspaceDir, "some-file.txt")
	if err := os.WriteFile(regularFile, []byte("test"), 0o644); err != nil {
		t.Fatalf("create regular file: %v", err)
	}

	repos, err := DiscoverRepos(workspaceDir)
	if err != nil {
		t.Fatalf("DiscoverRepos() error = %v", err)
	}

	// Should find exactly 2 repos
	if len(repos) != 2 {
		t.Errorf("DiscoverRepos() found %d repos, want 2", len(repos))
	}

	// Verify repos are found
	repoMap := make(map[string]bool)
	for _, r := range repos {
		repoMap[r] = true
	}
	if !repoMap[repo1] {
		t.Errorf("repo1 %q not found", repo1)
	}
	if !repoMap[repo2] {
		t.Errorf("repo2 %q not found", repo2)
	}
}

func TestDiscoverRepos_EmptyDir(t *testing.T) {
	// Create empty temp dir
	dir, err := os.MkdirTemp("", "substrate-workspace-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	repos, err := DiscoverRepos(dir)
	if err != nil {
		t.Fatalf("DiscoverRepos() error = %v", err)
	}

	if len(repos) != 0 {
		t.Errorf("DiscoverRepos() found %d repos in empty dir, want 0", len(repos))
	}
}

func TestIsGitWorkRepo(t *testing.T) {
	// Create temp dir
	dir, err := os.MkdirTemp("", "substrate-gitwork-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	// Test with .bare directory
	gitWorkDir := filepath.Join(dir, "gitwork")
	if err := os.MkdirAll(filepath.Join(gitWorkDir, ".bare"), 0o755); err != nil {
		t.Fatalf("create git-work dir: %v", err)
	}
	if !IsGitWorkRepo(gitWorkDir) {
		t.Error("IsGitWorkRepo() = false for git-work repo, want true")
	}

	// Test without .bare directory
	regularDir := filepath.Join(dir, "regular")
	if err := os.MkdirAll(regularDir, 0o755); err != nil {
		t.Fatalf("create regular dir: %v", err)
	}
	if IsGitWorkRepo(regularDir) {
		t.Error("IsGitWorkRepo() = true for regular dir, want false")
	}

	// Test with .bare file (not directory)
	fileDir := filepath.Join(dir, "file")
	if err := os.MkdirAll(fileDir, 0o755); err != nil {
		t.Fatalf("create file dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fileDir, ".bare"), []byte(""), 0o644); err != nil {
		t.Fatalf("create .bare file: %v", err)
	}
	if IsGitWorkRepo(fileDir) {
		t.Error("IsGitWorkRepo() = true when .bare is a file, want false")
	}
}

func TestValidateWorkspaceID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{
			name:    "valid ULID",
			id:      "01H9ZQK3ZQK3ZQK3ZQK3ZQK3ZQ",
			wantErr: false,
		},
		{
			name:    "invalid - too short",
			id:      "01H9ZQK3",
			wantErr: true,
		},
		{
			name:    "invalid - empty",
			id:      "",
			wantErr: true,
		},
		{
			name:    "invalid - too long",
			id:      "01H9ZQK3ZQK3ZQK3ZQK3ZQK3ZQK3",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateWorkspaceID(tt.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateWorkspaceID(%q) error = %v, wantErr %v", tt.id, err, tt.wantErr)
			}
		})
	}
}

func TestReadWorkspaceFile_MalformedYAML(t *testing.T) {
	// Create temp dir
	dir, err := os.MkdirTemp("", "substrate-workspace-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	// Write malformed YAML
	workspacePath := filepath.Join(dir, ".substrate-workspace")
	malformedYAML := `invalid: yaml: content: [unclosed`
	if err := os.WriteFile(workspacePath, []byte(malformedYAML), 0o644); err != nil {
		t.Fatalf("write malformed yaml: %v", err)
	}

	_, err = ReadWorkspaceFile(dir)
	if err == nil {
		t.Error("expected error for malformed YAML, got nil")
	}
	// Should return a yaml parse error
}