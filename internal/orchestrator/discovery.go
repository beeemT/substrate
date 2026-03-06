package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/gitwork"
)

// Discoverer handles workspace scanning and repo discovery.
type Discoverer struct {
	gitClient *gitwork.Client
	cfg       *config.Config
}

// NewDiscoverer creates a new Discoverer.
func NewDiscoverer(gitClient *gitwork.Client, cfg *config.Config) *Discoverer {
	return &Discoverer{gitClient: gitClient, cfg: cfg}
}

// PreflightCheck scans the workspace for health issues.
// It identifies git-work repos, plain git clones, and other directories.
func (d *Discoverer) PreflightCheck(ctx context.Context, workspaceDir string) (domain.WorkspaceHealthCheck, error) {
	entries, err := os.ReadDir(workspaceDir)
	if err != nil {
		return domain.WorkspaceHealthCheck{}, fmt.Errorf("read workspace directory: %w", err)
	}

	var check domain.WorkspaceHealthCheck

	for _, entry := range entries {
		// Use os.Stat to follow symlinks
		info, err := os.Stat(filepath.Join(workspaceDir, entry.Name()))
		if err != nil {
			slog.Warn("failed to stat entry, skipping", "path", entry.Name(), "err", err)
			continue
		}
		if !info.IsDir() {
			continue
		}

		repoPath := filepath.Join(workspaceDir, entry.Name())

		// Check if it's a git-work repo
		if gitwork.IsGitWorkRepo(repoPath) {
			check.GitWorkRepos = append(check.GitWorkRepos, repoPath)
		} else if d.isPlainGitClone(repoPath) {
			check.PlainGitClones = append(check.PlainGitClones, repoPath)
		}
		// Other directories are ignored
	}

	return check, nil
}

// PullMainWorktrees pulls the main worktree in each git-work repo.
// Failures are recorded but don't stop the process.
func (d *Discoverer) PullMainWorktrees(ctx context.Context, repoPaths []string) []domain.PullFailure {
	var failures []domain.PullFailure

	for _, repoPath := range repoPaths {
		repoName := filepath.Base(repoPath)
		output, err := d.gitClient.PullMainWorktree(ctx, repoPath)
		if err != nil {
			slog.Warn("failed to pull main worktree", "repo", repoName, "err", err, "output", output)
			failures = append(failures, domain.PullFailure{
				RepoName: repoName,
				Error:    fmt.Sprintf("%v: %s", err, strings.TrimSpace(output)),
			})
		} else {
			slog.Debug("pulled main worktree", "repo", repoName, "output", strings.TrimSpace(output))
		}
	}

	return failures
}

// DiscoverRepos discovers git-work repos and builds RepoPointers with metadata.
func (d *Discoverer) DiscoverRepos(ctx context.Context, workspaceDir string, repoPaths []string) ([]domain.RepoPointer, error) {
	var pointers []domain.RepoPointer

	for _, repoPath := range repoPaths {
		pointer, err := d.buildRepoPointer(ctx, repoPath)
		if err != nil {
			slog.Warn("failed to build repo pointer, skipping", "repo", repoPath, "err", err)
			continue
		}
		pointers = append(pointers, pointer)
	}

	return pointers, nil
}

// buildRepoPointer builds a RepoPointer for a single repository.
func (d *Discoverer) buildRepoPointer(ctx context.Context, repoPath string) (domain.RepoPointer, error) {
	repoName := filepath.Base(repoPath)

	// Get main worktree path
	mainDir, err := d.gitClient.GetMainWorktree(ctx, repoPath)
	if err != nil {
		return domain.RepoPointer{}, fmt.Errorf("get main worktree: %w", err)
	}

	pointer := domain.RepoPointer{
		Name:    repoName,
		Path:    repoPath,
		MainDir: mainDir,
	}

	// Detect language and framework
	d.detectLanguage(mainDir, &pointer)

	// Check for AGENTS.md in main worktree
	agentsMdPath := filepath.Join(mainDir, "AGENTS.md")
	if _, err := os.Stat(agentsMdPath); err == nil {
		pointer.AgentsMdPath = agentsMdPath
	}

	// Populate doc paths from repo config if available
	if d.cfg != nil {
		if repoConfig, ok := d.cfg.Repos[repoName]; ok {
			if len(repoConfig.DocPaths) > 0 {
				pointer.DocPaths = repoConfig.DocPaths
			}
		}
	}

	return pointer, nil
}

// detectLanguage detects the primary language and framework from manifest files.
func (d *Discoverer) detectLanguage(mainDir string, pointer *domain.RepoPointer) {
	// Check for Go
	if _, err := os.Stat(filepath.Join(mainDir, "go.mod")); err == nil {
		pointer.Language = "go"
		d.detectGoFramework(mainDir, pointer)
		return
	}

	// Check for TypeScript/JavaScript
	if _, err := os.Stat(filepath.Join(mainDir, "package.json")); err == nil {
		pointer.Language = "typescript"
		d.detectJSFramework(mainDir, pointer)
		return
	}

	// Check for Rust
	if _, err := os.Stat(filepath.Join(mainDir, "Cargo.toml")); err == nil {
		pointer.Language = "rust"
		d.detectRustFramework(mainDir, pointer)
		return
	}

	// Check for Python
	if _, err := os.Stat(filepath.Join(mainDir, "pyproject.toml")); err == nil {
		pointer.Language = "python"
		d.detectPythonFramework(mainDir, pointer)
		return
	}
	if _, err := os.Stat(filepath.Join(mainDir, "setup.py")); err == nil {
		pointer.Language = "python"
		d.detectPythonFramework(mainDir, pointer)
		return
	}

	pointer.Language = "unknown"
}

// detectGoFramework detects Go web frameworks.
func (d *Discoverer) detectGoFramework(mainDir string, pointer *domain.RepoPointer) {
	// Read go.mod and check for framework imports
	// For simplicity, we'll check for common framework files/patterns
	goModPath := filepath.Join(mainDir, "go.mod")
	content, err := os.ReadFile(goModPath)
	if err != nil {
		return
	}

	contentStr := string(content)

	// Check for common Go frameworks
	frameworks := []struct {
		name    string
		imports []string
	}{
		{"gin", []string{"github.com/gin-gonic/gin"}},
		{"echo", []string{"github.com/labstack/echo"}},
		{"fiber", []string{"github.com/gofiber/fiber"}},
		{"chi", []string{"github.com/go-chi/chi"}},
		{"mux", []string{"github.com/gorilla/mux"}},
		{"stdlib", []string{"net/http"}},
	}

	for _, fw := range frameworks {
		for _, imp := range fw.imports {
			if strings.Contains(contentStr, imp) {
				pointer.Framework = fw.name
				return
			}
		}
	}
}

// detectJSFramework detects JavaScript/TypeScript frameworks.
func (d *Discoverer) detectJSFramework(mainDir string, pointer *domain.RepoPointer) {
	// Read package.json
	packagePath := filepath.Join(mainDir, "package.json")
	content, err := os.ReadFile(packagePath)
	if err != nil {
		return
	}

	contentStr := string(content)

	// Check for common frameworks
	frameworks := []struct {
		name string
		pkgs []string
	}{
		{"next", []string{"\"next\""}},
		{"react", []string{"\"react\""}},
		{"vue", []string{"\"vue\""}},
		{"svelte", []string{"\"svelte\""}},
		{"express", []string{"\"express\""}},
		{"fastify", []string{"\"fastify\""}},
		{"nestjs", []string{"@nestjs/core"}},
		{"remix", []string{"@remix-run/react"}},
		{"astro", []string{"\"astro\""}},
	}

	for _, fw := range frameworks {
		for _, pkg := range fw.pkgs {
			if strings.Contains(contentStr, pkg) {
				pointer.Framework = fw.name
				return
			}
		}
	}

	// Check if TypeScript is used
	if strings.Contains(contentStr, "typescript") || strings.Contains(contentStr, "\"tslib\"") {
		pointer.Language = "typescript"
	} else {
		pointer.Language = "javascript"
	}
}

// detectRustFramework detects Rust frameworks.
func (d *Discoverer) detectRustFramework(mainDir string, pointer *domain.RepoPointer) {
	cargoPath := filepath.Join(mainDir, "Cargo.toml")
	content, err := os.ReadFile(cargoPath)
	if err != nil {
		return
	}

	contentStr := string(content)

	frameworks := []struct {
		name  string
		crate string
	}{
		{"actix", "actix-web"},
		{"axum", "axum"},
		{"rocket", "rocket"},
		{"warp", "warp"},
		{"tide", "tide"},
	}

	for _, fw := range frameworks {
		if strings.Contains(contentStr, fw.crate) {
			pointer.Framework = fw.name
			return
		}
	}
}

// detectPythonFramework detects Python frameworks.
func (d *Discoverer) detectPythonFramework(mainDir string, pointer *domain.RepoPointer) {
	// Check pyproject.toml first
	pyprojectPath := filepath.Join(mainDir, "pyproject.toml")
	content, err := os.ReadFile(pyprojectPath)
	if err == nil {
		contentStr := string(content)

		frameworks := []struct {
			name string
			pkg  string
		}{
			{"fastapi", "fastapi"},
			{"flask", "flask"},
			{"django", "django"},
			{"starlette", "starlette"},
			{"tornado", "tornado"},
			{"aiohttp", "aiohttp"},
		}

		for _, fw := range frameworks {
			if strings.Contains(contentStr, fw.pkg) {
				pointer.Framework = fw.name
				return
			}
		}
	}

	// Check requirements.txt
	reqPath := filepath.Join(mainDir, "requirements.txt")
	reqContent, err := os.ReadFile(reqPath)
	if err == nil {
		reqStr := string(reqContent)

		frameworks := []struct {
			name string
			pkg  string
		}{
			{"fastapi", "fastapi"},
			{"flask", "flask"},
			{"django", "django"},
			{"starlette", "starlette"},
			{"tornado", "tornado"},
			{"aiohttp", "aiohttp"},
		}

		for _, fw := range frameworks {
			if strings.Contains(reqStr, fw.pkg) {
				pointer.Framework = fw.name
				return
			}
		}
	}
}

// isPlainGitClone checks if a directory is a plain git clone (has .git, no .bare).
func (d *Discoverer) isPlainGitClone(dir string) bool {
	gitPath := filepath.Join(dir, ".git")
	barePath := filepath.Join(dir, ".bare")

	_, gitErr := os.Stat(gitPath)
	bareInfo, bareErr := os.Stat(barePath)

	// Has .git (file or dir) and no .bare dir
	hasGit := gitErr == nil // .git can be a file (worktree) or dir
	hasBare := bareErr == nil && bareInfo.IsDir()

	return hasGit && !hasBare
}

// ReadWorkspaceAgentsMd reads the workspace-root AGENTS.md file if it exists.
func ReadWorkspaceAgentsMd(workspaceDir string) (string, error) {
	agentsMdPath := filepath.Join(workspaceDir, "AGENTS.md")
	content, err := os.ReadFile(agentsMdPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil // File doesn't exist, return empty string
		}
		return "", fmt.Errorf("read AGENTS.md: %w", err)
	}
	return string(content), nil
}

// EnsureSessionDir creates the session directory and returns its info.
func EnsureSessionDir(workspaceDir, sessionID string) (domain.SessionDirInfo, error) {
	sessionDir := filepath.Join(workspaceDir, ".substrate", "sessions", sessionID)
	draftPath := filepath.Join(sessionDir, "plan-draft.md")

	info := domain.SessionDirInfo{
		Path:      sessionDir,
		DraftPath: draftPath,
		Exists:    false,
	}

	// Check if directory exists
	if _, err := os.Stat(sessionDir); err == nil {
		info.Exists = true
		return info, nil
	}

	// Create directory
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return domain.SessionDirInfo{}, fmt.Errorf("create session directory: %w", err)
	}

	return info, nil
}
