package terminal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// sanitizeAppleScriptPath escapes characters that would break AppleScript string literals.
// This prevents injection attacks where a malicious path could execute arbitrary AppleScript.
func sanitizeAppleScriptPath(path string) string {
	// Escape backslashes first, then double quotes.
	return strings.NewReplacer(
		"\\", "\\\\",
		"\"", "\\\"",
	).Replace(path)
}

// sanitizeArgPath ensures a path is not interpreted as a flag by prepending ./ if needed.
func sanitizeArgPath(path string) string {
	if path == "" {
		return "."
	}
	// If path starts with -, it would be interpreted as a flag.
	// Prepend ./ to force interpretation as a path.
	if path[0] == '-' {
		return "./" + path
	}
	return path
}

// ErrNoSupportedTerminal is returned when no supported terminal is detected.
var ErrNoSupportedTerminal = errors.New("no supported terminal detected")

// Open opens a new terminal tab/window in the specified directory.
// Returns the terminal type used, or an error if no supported terminal is found.
func Open(dir string) (TerminalType, error) {
	if err := validateDir(dir); err != nil {
		return TerminalUnknown, err
	}

	term := Detect()
	if term == TerminalUnknown {
		// Fall back to Terminal.app on macOS, otherwise return an error.
		if runtime.GOOS == "darwin" {
			return openTerminalAppFallback(dir)
		}
		return TerminalUnknown, fmt.Errorf("%w: tried warp, iterm2, terminal, kitty, wezterm, alacritty", ErrNoSupportedTerminal)
	}
	return OpenWithTerminal(dir, term)
}

// OpenWithTerminal opens in the specified terminal (ignoring detection).
// On macOS, falls back to Terminal.app if the requested terminal is unavailable.
func OpenWithTerminal(dir string, termType TerminalType) (TerminalType, error) {
	if err := validateDir(dir); err != nil {
		return TerminalUnknown, err
	}

	switch termType {
	case TerminalWezTerm:
		return openWezTerm(dir)
	case TerminalKitty:
		return openKitty(dir)
	case TerminalITerm2:
		return openITerm2(dir)
	case TerminalTerminal:
		return openTerminalApp(dir)
	case TerminalWarp:
		return openWarp(dir)
	case TerminalAlacritty:
		return openAlacritty(dir)
	default:
		return TerminalUnknown, fmt.Errorf("%w: unknown terminal type %q", ErrNoSupportedTerminal, termType)
	}
}

func validateDir(dir string) error {
	if dir == "" {
		return errors.New("terminal directory is required")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("stat terminal directory %q: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("terminal path %q is not a directory", dir)
	}
	return nil
}

func startDetached(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func startDetachedWithFallback(primary *exec.Cmd, fallback func() (TerminalType, error), term TerminalType, errPrefix string) (TerminalType, error) {
	if err := startDetached(primary); err == nil {
		return term, nil
	} else if runtime.GOOS != "darwin" {
		return term, fmt.Errorf("%s: %w", errPrefix, err)
	}
	return fallback()
}

// openWezTerm opens a new tab in WezTerm, targeting the current pane if available.
func openWezTerm(dir string) (TerminalType, error) {
	if _, ok := os.LookupEnv("WEZTERM_PANE"); ok {
		if err := startDetached(wezTermCliSpawnCmd(dir)); err == nil {
			return TerminalWezTerm, nil
		}
	}
	if err := startDetached(wezTermStartNewTabCmd(dir)); err == nil {
		return TerminalWezTerm, nil
	}
	if err := startDetached(wezTermStartWindowCmd(dir)); err == nil {
		return TerminalWezTerm, nil
	} else if runtime.GOOS != "darwin" {
		return TerminalWezTerm, fmt.Errorf("wezterm not found or failed to launch: %w", err)
	}
	return openTerminalAppFallback(dir)
}

// openKitty opens a new tab in Kitty using kitten remote control.
func openKitty(dir string) (TerminalType, error) {
	if !KittyRemoteControlTargetAvailable() {
		if runtime.GOOS == "darwin" {
			return openTerminalAppFallback(dir)
		}
		return TerminalKitty, errors.New("kitty remote control not available (no KITTY_WINDOW_ID or KITTY_LISTEN_ON)")
	}
	return startDetachedWithFallback(kittyLaunchCmd(dir), func() (TerminalType, error) {
		return openTerminalAppFallback(dir)
	}, TerminalKitty, "kitten @ launch failed")
}

// openITerm2 opens a new tab in iTerm2 using AppleScript.
func openITerm2(dir string) (TerminalType, error) {
	return startDetachedWithFallback(iTerm2Cmd(dir), func() (TerminalType, error) {
		return openTerminalAppFallback(dir)
	}, TerminalITerm2, "iTerm2 AppleScript failed")
}

// openTerminalApp opens a new tab in Terminal.app using AppleScript.
// Uses quoted form of POSIX path for safe AppleScript quoting.
func openTerminalApp(dir string) (TerminalType, error) {
	if err := startDetached(terminalAppAppleScriptCmd(dir)); err != nil {
		return TerminalTerminal, fmt.Errorf("Terminal.app AppleScript failed: %w", err)
	}
	return TerminalTerminal, nil
}

func openTerminalAppFallback(dir string) (TerminalType, error) {
	if err := startDetached(terminalAppOpenFallbackCmd(dir)); err != nil {
		return TerminalTerminal, fmt.Errorf("Terminal.app fallback failed: %w", err)
	}
	return TerminalTerminal, nil
}

// openWarp opens Warp (limited: new window only, no tab support).
func openWarp(dir string) (TerminalType, error) {
	if err := startDetached(warpCmd(dir)); err != nil {
		return TerminalWarp, fmt.Errorf("failed to open Warp.app: %w", err)
	}
	return TerminalWarp, nil
}

// openAlacritty opens a new window in Alacritty (no tab support).
func openAlacritty(dir string) (TerminalType, error) {
	return startDetachedWithFallback(alacrittyCmd(dir), func() (TerminalType, error) {
		return openTerminalAppFallback(dir)
	}, TerminalAlacritty, "alacritty failed")
}

func wezTermCliSpawnCmd(dir string) *exec.Cmd {
	return exec.Command("wezterm", "cli", "spawn", "--cwd", dir)
}

func wezTermStartNewTabCmd(dir string) *exec.Cmd {
	return exec.Command("wezterm", "start", "--new-tab", "--cwd", dir)
}

func wezTermStartWindowCmd(dir string) *exec.Cmd {
	return exec.Command("wezterm", "start", "--cwd", dir)
}

func kittyLaunchCmd(dir string) *exec.Cmd {
	return exec.Command("kitten", "@", "launch", "--type=tab", "--cwd", dir)
}

func iTerm2Cmd(dir string) *exec.Cmd {
	safeDir := sanitizeAppleScriptPath(sanitizeArgPath(dir))
	script := fmt.Sprintf(`tell application "iTerm2"
	activate
	tell current window
		create tab with default profile
		tell current session
			write text "cd " & quoted form of POSIX path "%s"
		end tell
	end tell
end tell`, safeDir)
	return exec.CommandContext(context.TODO(), "osascript", "-e", script)
}

func terminalAppAppleScriptCmd(dir string) *exec.Cmd {
	safeDir := sanitizeAppleScriptPath(sanitizeArgPath(dir))
	script := fmt.Sprintf(`tell application "Terminal"
	activate
	do script "cd " & quoted form of POSIX path "%s" in front window
end tell`, safeDir)
	return exec.CommandContext(context.TODO(), "osascript", "-e", script)
}

func terminalAppOpenFallbackCmd(dir string) *exec.Cmd {
	return exec.Command("open", "-a", "Terminal.app", sanitizeArgPath(dir))
}

func warpCmd(dir string) *exec.Cmd {
	return exec.Command("open", "-a", "Warp.app", sanitizeArgPath(dir))
}

func alacrittyCmd(dir string) *exec.Cmd {
	return exec.Command("alacritty", "--working-directory", sanitizeArgPath(dir))
}
