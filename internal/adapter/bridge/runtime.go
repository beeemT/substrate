package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// BridgeRuntime describes a located bridge binary or script.
type BridgeRuntime struct {
	Path     string
	NeedsBun bool
}

// LaunchDir returns the working directory for the bridge process.
func (r BridgeRuntime) LaunchDir(workDir string) string {
	if r.NeedsBun {
		return filepath.Dir(r.Path)
	}
	return workDir
}

// ResolveBridgeRuntime locates the bridge binary relative to the substrate executable.
func ResolveBridgeRuntime(configured, bridgeName, notFoundLabel string) (BridgeRuntime, error) {
	executablePath, err := os.Executable()
	if err != nil {
		return BridgeRuntime{}, fmt.Errorf("locate substrate executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(executablePath); err == nil {
		executablePath = resolved
	}
	return ResolveBridgeRuntimeFrom(configured, executablePath, bridgeName, notFoundLabel)
}

// ResolveBridgeRuntimeFrom resolves a bridge binary from the given configuration and executable path.
// bridgeName is the filename used in default candidate paths (e.g. "omp-bridge" or "claude-agent-bridge").
// notFoundLabel is used in error messages (e.g. "ohmypi bridge" or "claude-agent bridge").
func ResolveBridgeRuntimeFrom(configured, executablePath, bridgeName, notFoundLabel string) (BridgeRuntime, error) {
	checked := make([]string, 0, 6)
	for _, candidate := range BridgeCandidates(strings.TrimSpace(configured), executablePath, bridgeName) {
		if candidate == "" {
			continue
		}
		checked = append(checked, candidate)
		info, err := os.Stat(candidate)
		if err != nil {
			continue
		}
		if info.IsDir() {
			continue
		}
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			return BridgeRuntime{}, fmt.Errorf("resolve bridge path %q: %w", candidate, err)
		}
		return BridgeRuntime{
			Path:     absolute,
			NeedsBun: IsBridgeScript(absolute),
		}, nil
	}
	return BridgeRuntime{}, fmt.Errorf("resolve %s: no bridge binary or script found; checked %s", notFoundLabel, strings.Join(DedupePaths(checked), ", "))
}

// ResolveBunExecutable resolves the bun binary path.
func ResolveBunExecutable(configured string) (string, error) {
	bunPath := strings.TrimSpace(configured)
	if bunPath == "" {
		bunPath = "bun"
	}
	resolved, err := exec.LookPath(bunPath)
	if err != nil {
		return "", fmt.Errorf("resolve bun %q: %w", bunPath, err)
	}
	return resolved, nil
}

// BridgeCandidates returns candidate bridge paths to search, in priority order.
// bridgeName is the filename stem used in default candidate paths.
func BridgeCandidates(configured, executablePath, bridgeName string) []string {
	execDir := filepath.Dir(executablePath)
	shareDir := filepath.Clean(filepath.Join(execDir, "..", "share", "substrate"))

	if configured != "" {
		if filepath.IsAbs(configured) {
			return []string{configured}
		}
		candidates := []string{
			filepath.Join(execDir, configured),
			filepath.Join(shareDir, configured),
		}
		if absolute, err := filepath.Abs(configured); err == nil {
			candidates = append(candidates, absolute)
		} else {
			candidates = append(candidates, configured)
		}
		return DedupePaths(candidates)
	}

	return DedupePaths([]string{
		filepath.Join(execDir, "bridge", bridgeName),
		filepath.Join(shareDir, "bridge", bridgeName),
		filepath.Join(execDir, "bridge", bridgeName+".ts"),
		filepath.Join(shareDir, "bridge", bridgeName+".ts"),
	})
}

// DedupePaths removes duplicate and empty paths after normalizing with filepath.Clean.
func DedupePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		path = filepath.Clean(path)
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		result = append(result, path)
	}
	return result
}

// IsBridgeScript returns true if the path has a TypeScript/JavaScript extension.
func IsBridgeScript(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js", ".mjs", ".cjs", ".ts", ".mts", ".cts":
		return true
	default:
		return false
	}
}

// EnsureBridgeDependencies checks that the bridge's npm dependencies are installed.
// depSubpath is the node_modules subpath to check (e.g. "@oh-my-pi/pi-coding-agent").
// bridgeLabel is used in error messages (e.g. "ohmypi source bridge").
func EnsureBridgeDependencies(rt BridgeRuntime, depSubpath, bridgeLabel string) error {
	if !rt.NeedsBun {
		return nil
	}
	runtimeDir := filepath.Dir(rt.Path)
	packagePath := filepath.Join(runtimeDir, "package.json")
	if _, err := os.Stat(packagePath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("check bridge package metadata: %w", err)
	}
	dependencyPath := filepath.Join(runtimeDir, "node_modules", depSubpath)
	if _, err := os.Stat(dependencyPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s dependencies missing under %s; run `bun install --cwd %s`", bridgeLabel, runtimeDir, runtimeDir)
		}
		return fmt.Errorf("check %s dependencies: %w", bridgeLabel, err)
	}
	return nil
}

// ResolveGitDir returns the git directory path that needs write access inside the
// sandbox. For git-work repos (identified by a .bare/ directory in the parent
// of the worktree), this returns the .bare/ path. For plain git repos,
// .git/ is already inside the worktree and covered by the workDir sandbox allow,
// so an empty string is returned.
func ResolveGitDir(workDir string) string {
	repoRoot := filepath.Dir(workDir)
	bareDir := filepath.Join(repoRoot, ".bare")
	if info, err := os.Stat(bareDir); err == nil && info.IsDir() {
		return bareDir
	}
	return ""
}

// BuildSandboxCmd constructs a sandboxed command for the bridge subprocess.
// gitDir is the git directory path that needs write access (e.g. the .bare/
// directory for git-work repos). When empty, no additional git write access
// is needed (appropriate for plain git repos where .git/ is inside workDir).
// Returns the command, the created temp directory path, and any error.
func BuildSandboxCmd(ctx context.Context, rt BridgeRuntime, workDir, gitDir, bunPath string) (*exec.Cmd, string, error) {
	if runtime.GOOS == "darwin" {
		return buildDarwinSandboxCmd(ctx, rt, bunPath)
	}
	return buildLinuxSandboxCmd(ctx, rt, workDir, gitDir, bunPath)
}

// buildDarwinSandboxCmd builds a sandbox-exec command using a deny-list strategy.
// A deny-list (allow default + deny specific write paths) lets developer tools
// (bun, git, gpg, etc.) use their own cache/temp dirs without explicit per-tool
// allowances, while still preventing writes to sensitive system directories.
func buildDarwinSandboxCmd(ctx context.Context, rt BridgeRuntime, bunPath string) (*exec.Cmd, string, error) {
	sessionTmpDir, err := os.MkdirTemp("", "substrate-session-*")
	if err != nil {
		return nil, "", fmt.Errorf("create session temp dir: %w", err)
	}

	// Deny-list: allow everything by default, then block writes to OS-owned
	// directories. This avoids maintaining an exhaustive allow-list of every
	// tool cache location while still protecting system integrity.
	const profile = `(version 1)` +
		`(allow default)` +
		`(deny file-write* (subpath "/System"))` +
		`(deny file-write* (subpath "/usr"))` +
		`(deny file-write* (subpath "/bin"))` +
		`(deny file-write* (subpath "/sbin"))` +
		`(deny file-write* (subpath "/private/etc"))` +
		`(deny file-write* (subpath "/Library"))` +
		`(deny file-write* (subpath "/Applications"))`

	if rt.NeedsBun {
		return exec.CommandContext(ctx, "sandbox-exec", "-p", profile, bunPath, "run", rt.Path), sessionTmpDir, nil
	}
	return exec.CommandContext(ctx, "sandbox-exec", "-p", profile, rt.Path), sessionTmpDir, nil
}

func buildLinuxSandboxCmd(ctx context.Context, rt BridgeRuntime, workDir, gitDir, bunPath string) (*exec.Cmd, string, error) {
	sessionTmpDir, err := os.MkdirTemp("", "substrate-session-*")
	if err != nil {
		return nil, "", fmt.Errorf("create session temp dir: %w", err)
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		os.RemoveAll(sessionTmpDir)
		return nil, "", fmt.Errorf("get user home dir: %w", err)
	}

	bwrapPath, lookErr := exec.LookPath("bwrap")
	if lookErr != nil {
		slog.Warn("bubblewrap (bwrap) not found; running bridge without sandbox", "error", lookErr)
		if rt.NeedsBun {
			return exec.CommandContext(ctx, bunPath, "run", rt.Path), sessionTmpDir, nil
		}
		return exec.CommandContext(ctx, rt.Path), sessionTmpDir, nil
	}

	// isUnder reports whether path is the same as parent or nested within it.
	// The trailing-slash trick avoids false positives like /home/user2 under /home/user.
	isUnder := func(path, parent string) bool {
		return strings.HasPrefix(filepath.Clean(path)+"/", filepath.Clean(parent)+"/")
	}

	// Deny-list strategy: bind the root read-only, then grant write access to
	// the user home directory. This covers ~/.bun, ~/.cache, ~/.config, project
	// dirs under home, gnupg, ssh config, and any tool-specific caches without
	// needing to enumerate each one explicitly.
	bwrapArgs := []string{
		"--ro-bind", "/", "/",
		"--bind", homeDir, homeDir,
		"--dev", "/dev",
		"--proc", "/proc",
		"--unshare-pid",
		"--die-with-parent",
	}

	// /tmp: standard Linux temp location; may not exist in minimal containers.
	if _, err := os.Stat("/tmp"); err == nil {
		bwrapArgs = append(bwrapArgs, "--bind", "/tmp", "/tmp")
	}

	// TMPDIR: if it's a non-standard location outside the home dir, bind it.
	if tmpdir := os.Getenv("TMPDIR"); tmpdir != "" && tmpdir != "/tmp" && !isUnder(tmpdir, homeDir) {
		bwrapArgs = append(bwrapArgs, "--bind", tmpdir, tmpdir)
	}

	// workDir: workspace may be on an external mount (e.g. NFS, separate volume).
	if !isUnder(workDir, homeDir) {
		bwrapArgs = append(bwrapArgs, "--bind", workDir, workDir)
	}

	// gitDir: bare repo may live outside home (e.g. /srv/repos).
	if gitDir != "" && !isUnder(gitDir, homeDir) {
		bwrapArgs = append(bwrapArgs, "--bind", gitDir, gitDir)
	}

	// Network is allowed by default in bwrap (no --unshare-net flag).
	if rt.NeedsBun {
		bwrapArgs = append(bwrapArgs, bunPath, "run", rt.Path)
	} else {
		bwrapArgs = append(bwrapArgs, rt.Path)
	}
	return exec.CommandContext(ctx, bwrapPath, bwrapArgs...), sessionTmpDir, nil
}
