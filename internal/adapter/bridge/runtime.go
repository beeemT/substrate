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

// EscapeSandboxPath escapes a path for use in a sandbox-exec profile.
// It replaces backslashes and double quotes to prevent profile injection.
func EscapeSandboxPath(path string) string {
	path = strings.ReplaceAll(path, "\\", "\\\\")
	path = strings.ReplaceAll(path, "\"", "\\\"")
	return path
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

// BuildSandboxCmd constructs a sandboxed command for the bridge subprocess.
// homeDirName is the config directory name (e.g. ".claude" or ".omp").
// Returns the command, the created temp directory path, and any error.
func BuildSandboxCmd(ctx context.Context, rt BridgeRuntime, workDir, bunPath, bunCacheDir, homeDirName string) (*exec.Cmd, string, error) {
	if runtime.GOOS == "darwin" {
		return buildDarwinSandboxCmd(ctx, rt, workDir, bunPath, bunCacheDir, homeDirName)
	}
	return buildLinuxSandboxCmd(ctx, rt, workDir, bunPath, bunCacheDir, homeDirName)
}

func buildDarwinSandboxCmd(ctx context.Context, rt BridgeRuntime, workDir, bunPath, bunCacheDir, homeDirName string) (*exec.Cmd, string, error) {
	sessionTmpDir, err := os.MkdirTemp("", "substrate-session-*")
	if err != nil {
		return nil, "", fmt.Errorf("create session temp dir: %w", err)
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		os.RemoveAll(sessionTmpDir)
		return nil, "", fmt.Errorf("get user home dir: %w", err)
	}
	configDir := filepath.Join(homeDir, homeDirName)
	w := EscapeSandboxPath(workDir)
	tmp := EscapeSandboxPath(sessionTmpDir)
	cl := EscapeSandboxPath(configDir)
	const allow = `(version 1)(allow default)(deny file-write* (subpath "/"))`
	if rt.NeedsBun {
		bc := EscapeSandboxPath(bunCacheDir)
		profile := allow +
			fmt.Sprintf(`(allow file-write* (subpath "%s"))(allow file-write* (subpath "%s"))(allow file-write* (subpath "%s"))(allow file-write* (subpath "%s"))(allow file-write* (literal "/dev/null"))`,
				w, tmp, cl, bc)
		return exec.CommandContext(ctx, "sandbox-exec", "-p", profile, bunPath, "run", rt.Path), sessionTmpDir, nil
	}
	profile := allow +
		fmt.Sprintf(`(allow file-write* (subpath "%s"))(allow file-write* (subpath "%s"))(allow file-write* (subpath "%s"))(allow file-write* (literal "/dev/null"))`,
			w, tmp, cl)
	return exec.CommandContext(ctx, "sandbox-exec", "-p", profile, rt.Path), sessionTmpDir, nil
}

func buildLinuxSandboxCmd(ctx context.Context, rt BridgeRuntime, workDir, bunPath, bunCacheDir, homeDirName string) (*exec.Cmd, string, error) {
	sessionTmpDir, err := os.MkdirTemp("", "substrate-session-*")
	if err != nil {
		return nil, "", fmt.Errorf("create session temp dir: %w", err)
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		os.RemoveAll(sessionTmpDir)
		return nil, "", fmt.Errorf("get user home dir: %w", err)
	}
	configDir := filepath.Join(homeDir, homeDirName)
	bwrapPath, lookErr := exec.LookPath("bwrap")
	if lookErr != nil {
		slog.Warn("bubblewrap (bwrap) not found; running bridge without sandbox", "error", lookErr)
		if rt.NeedsBun {
			return exec.CommandContext(ctx, bunPath, "run", rt.Path), sessionTmpDir, nil
		}
		return exec.CommandContext(ctx, rt.Path), sessionTmpDir, nil
	}
	bwrapArgs := []string{
		"--ro-bind", "/", "/",
		"--bind", workDir, workDir,
		"--bind", sessionTmpDir, sessionTmpDir,
		"--bind", configDir, configDir,
		"--dev", "/dev",
		"--proc", "/proc",
		"--unshare-pid",
		"--die-with-parent",
	}
	if rt.NeedsBun {
		bwrapArgs = append(bwrapArgs, "--bind", bunCacheDir, bunCacheDir)
		bwrapArgs = append(bwrapArgs, bunPath, "run", rt.Path)
	} else {
		bwrapArgs = append(bwrapArgs, rt.Path)
	}
	return exec.CommandContext(ctx, bwrapPath, bwrapArgs...), sessionTmpDir, nil
}
