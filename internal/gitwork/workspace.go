package gitwork

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/oklog/ulid/v2"
	"gopkg.in/yaml.v3"
)

const (
	// WorkspaceFileName is the name of the workspace marker file.
	WorkspaceFileName = ".substrate-workspace"
)

var (
	// ErrNotInWorkspace is returned when no workspace file is found.
	ErrNotInWorkspace = errors.New("not in a substrate workspace")
	// ErrWorkspaceExists is returned when trying to initialize an existing workspace.
	ErrWorkspaceExists = errors.New("workspace already exists")
)

// IsNotInWorkspace returns true if err is ErrNotInWorkspace.
func IsNotInWorkspace(err error) bool {
	return errors.Is(err, ErrNotInWorkspace)
}

// IsWorkspaceExists returns true if err is ErrWorkspaceExists.
func IsWorkspaceExists(err error) bool {
	return errors.Is(err, ErrWorkspaceExists)
}

// DiscoverRepos scans the workspace directory for git-work repositories.
// A git-work repository is identified by the presence of a .bare/ subdirectory.
func DiscoverRepos(workspaceDir string) ([]string, error) {
	entries, err := os.ReadDir(workspaceDir)
	if err != nil {
		return nil, fmt.Errorf("read workspace directory: %w", err)
	}

	var repos []string
	for _, entry := range entries {
		// Use os.Stat to follow symlinks when checking if entry is a directory
		info, err := os.Stat(filepath.Join(workspaceDir, entry.Name()))
		if err != nil {
			continue // Skip entries that can't be stat'd
		}
		if !info.IsDir() {
			continue
		}

		repoPath := filepath.Join(workspaceDir, entry.Name())
		if IsGitWorkRepo(repoPath) {
			repos = append(repos, repoPath)
		}
	}

	return repos, nil
}

// InitWorkspace creates a .substrate-workspace file in the given directory.
// It returns an error if the file already exists.
func InitWorkspace(dir, name string) (*WorkspaceFile, error) {
	workspacePath := filepath.Join(dir, WorkspaceFileName)

	// Check if workspace file already exists
	if _, err := os.Stat(workspacePath); err == nil {
		return nil, ErrWorkspaceExists
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("check workspace file: %w", err)
	}

	ws := &WorkspaceFile{
		ID:        ulid.Make().String(),
		Name:      name,
		CreatedAt: time.Now().UTC(),
	}

	if err := WriteWorkspaceFile(dir, ws); err != nil {
		return nil, fmt.Errorf("write workspace file: %w", err)
	}

	return ws, nil
}

// WriteWorkspaceFile writes the workspace file to the given directory.
func WriteWorkspaceFile(dir string, ws *WorkspaceFile) error {
	workspacePath := filepath.Join(dir, WorkspaceFileName)

	data, err := yaml.Marshal(ws)
	if err != nil {
		return fmt.Errorf("marshal workspace file: %w", err)
	}

	if err := os.WriteFile(workspacePath, data, 0o644); err != nil {
		return fmt.Errorf("write workspace file: %w", err)
	}

	return nil
}

// ReadWorkspaceFile reads the workspace file from the given directory.
// Returns ErrNotInWorkspace if no workspace file exists.
func ReadWorkspaceFile(dir string) (*WorkspaceFile, error) {
	workspacePath := filepath.Join(dir, WorkspaceFileName)

	data, err := os.ReadFile(workspacePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotInWorkspace
		}
		return nil, fmt.Errorf("read workspace file: %w", err)
	}

	var ws WorkspaceFile
	if err := yaml.Unmarshal(data, &ws); err != nil {
		return nil, fmt.Errorf("parse workspace file: %w", err)
	}

	// Validate required fields
	if err := ValidateWorkspaceID(ws.ID); err != nil {
		return nil, err
	}
	if ws.CreatedAt.IsZero() {
		return nil, fmt.Errorf("workspace file missing created_at")
	}

	return &ws, nil
}

// FindWorkspace searches for a workspace file starting from the given directory
// and walking up the directory tree. Returns the workspace directory and file,
// or ErrNotInWorkspace if not found.
func FindWorkspace(startDir string) (string, *WorkspaceFile, error) {
	dir := startDir

	for {
		ws, err := ReadWorkspaceFile(dir)
		if err == nil {
			return dir, ws, nil
		}
		if !errors.Is(err, ErrNotInWorkspace) {
			return "", nil, err
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached root without finding a workspace
			return "", nil, ErrNotInWorkspace
		}
		dir = parent
	}
}

// ValidateWorkspaceID checks if the given string is a valid ULID.
func ValidateWorkspaceID(id string) error {
	_, err := ulid.Parse(id)
	if err != nil {
		return fmt.Errorf("invalid workspace ID: %w", err)
	}
	return nil
}
