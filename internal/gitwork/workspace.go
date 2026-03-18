package gitwork

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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

// WorkspaceScan classifies direct child directories in a workspace.
type WorkspaceScan struct {
	GitWorkRepos  []string
	PlainGitRepos []string
}

// IsNotInWorkspace returns true if err is ErrNotInWorkspace.
func IsNotInWorkspace(err error) bool {
	return errors.Is(err, ErrNotInWorkspace)
}

// IsWorkspaceExists returns true if err is ErrWorkspaceExists.
func IsWorkspaceExists(err error) bool {
	return errors.Is(err, ErrWorkspaceExists)
}

// ScanWorkspace scans the workspace directory for managed and plain git repositories.
func ScanWorkspace(workspaceDir string) (WorkspaceScan, error) {
	dirs, err := workspaceChildDirs(workspaceDir)
	if err != nil {
		return WorkspaceScan{}, err
	}

	scan := WorkspaceScan{}
	for _, dir := range dirs {
		switch {
		case IsGitWorkRepo(dir):
			scan.GitWorkRepos = append(scan.GitWorkRepos, dir)
		case IsPlainGitRepo(dir):
			scan.PlainGitRepos = append(scan.PlainGitRepos, dir)
		}
	}

	return scan, nil
}

// DiscoverRepos scans the workspace directory for git-work repositories.
// A git-work repository is identified by the presence of a .bare/ subdirectory.
func DiscoverRepos(workspaceDir string) ([]string, error) {
	scan, err := ScanWorkspace(workspaceDir)
	if err != nil {
		return nil, err
	}
	return scan.GitWorkRepos, nil
}

// DiscoverPlainRepos scans the workspace directory for plain git repositories.
// A plain git repository has a .git entry but no .bare/ subdirectory.
func DiscoverPlainRepos(workspaceDir string) ([]string, error) {
	scan, err := ScanWorkspace(workspaceDir)
	if err != nil {
		return nil, err
	}
	return scan.PlainGitRepos, nil
}

// IsPlainGitRepo checks if the given directory is a plain git repository.
// Plain git repositories have a .git entry and do not yet use the git-work .bare layout.
func IsPlainGitRepo(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return false
	}
	return !IsGitWorkRepo(dir)
}

type repoInitializer interface {
	Init(ctx context.Context, repoDir string) error
}

func InitWorkspace(dir, name string) (*WorkspaceFile, error) {
	return initWorkspace(context.Background(), NewClient(""), dir, name)
}

func initWorkspace(ctx context.Context, initializer repoInitializer, dir, name string) (*WorkspaceFile, error) {
	workspacePath := filepath.Join(dir, WorkspaceFileName)
	if _, err := os.Stat(workspacePath); err == nil {
		return nil, ErrWorkspaceExists
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("stat workspace file: %w", err)
	}

	scan, err := ScanWorkspace(dir)
	if err != nil {
		return nil, fmt.Errorf("scan workspace: %w", err)
	}
	for _, repoPath := range scan.PlainGitRepos {
		if initializer == nil {
			return nil, errors.New("repo initializer is nil")
		}
		if err := initializer.Init(ctx, repoPath); err != nil {
			return nil, fmt.Errorf("initialize git-work repo %s: %w", filepath.Base(repoPath), err)
		}
	}

	ws := &WorkspaceFile{
		ID:        ulid.Make().String(),
		Name:      name,
		CreatedAt: time.Now().UTC(),
	}

	data, err := yaml.Marshal(ws)
	if err != nil {
		return nil, fmt.Errorf("marshal workspace file: %w", err)
	}

	// Use O_EXCL for atomic creation - fails if file already exists.
	f, err := os.OpenFile(workspacePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil, ErrWorkspaceExists
		}
		return nil, fmt.Errorf("create workspace file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		f.Close() // Close before remove.
		os.Remove(workspacePath)
		return nil, fmt.Errorf("write workspace file: %w", err)
	}

	return ws, nil
}

func workspaceChildDirs(workspaceDir string) ([]string, error) {
	entries, err := os.ReadDir(workspaceDir)
	if err != nil {
		return nil, fmt.Errorf("read workspace directory: %w", err)
	}

	dirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		path := filepath.Join(workspaceDir, entry.Name())
		info, err := os.Stat(path)
		if err != nil {
			slog.Warn("failed to stat entry, skipping", "path", entry.Name(), "err", err)
			continue
		}
		if !info.IsDir() {
			continue
		}
		dirs = append(dirs, path)
	}

	return dirs, nil
}

// WriteWorkspaceFile writes the workspace file to the given directory.
func WriteWorkspaceFile(dir string, ws *WorkspaceFile) error {
	workspacePath := filepath.Join(dir, WorkspaceFileName)

	data, err := yaml.Marshal(ws)
	if err != nil {
		return fmt.Errorf("marshal workspace file: %w", err)
	}

	if err := os.WriteFile(workspacePath, data, 0o600); err != nil {
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
		return nil, errors.New("workspace file missing created_at")
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

// ResolveWorkspacePath attempts to read the workspace file from the given path
// and returns the absolute path. This is used for path reconciliation when
// a workspace directory may have been moved.
// Returns the absolute path, workspace file, and error if the workspace cannot be found.
func ResolveWorkspacePath(storedPath string) (string, *WorkspaceFile, error) {
	absPath, err := filepath.Abs(storedPath)
	if err != nil {
		return "", nil, fmt.Errorf("resolve absolute path: %w", err)
	}

	ws, err := ReadWorkspaceFile(absPath)
	if err != nil {
		return "", nil, err
	}

	return absPath, ws, nil
}
