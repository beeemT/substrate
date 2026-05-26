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
	term := Detect()
	if term == TerminalUnknown {
		// Fall back to Terminal.app on macOS, otherwise return an error.
		if runtime.GOOS == "darwin" {
			return openTerminalApp(dir)
		}
		return TerminalUnknown, fmt.Errorf("%w: tried warp, iterm2, terminal, kitty, wezterm, alacritty", ErrNoSupportedTerminal)
	}
	return OpenWithTerminal(dir, term)
}

// OpenWithTerminal opens in the specified terminal (ignoring detection).
// On macOS, falls back to Terminal.app if the requested terminal is unavailable.
func OpenWithTerminal(dir string, termType TerminalType) (TerminalType, error) {
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

// openWezTerm opens a new tab in WezTerm, targeting the current pane if available.
func openWezTerm(dir string) (TerminalType, error) {
	// Try WezTerm CLI first (works from inside WezTerm via WEZTERM_PANE).
	cmd := exec.Command("wezterm", "cli", "spawn", "--cwd", dir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err == nil {
		return TerminalWezTerm, nil
	}
	// Fall back: ask an existing GUI instance for a new tab.
	cmd = exec.Command("wezterm", "start", "--new-tab", "--cwd", dir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err == nil {
		return TerminalWezTerm, nil
	}
	// As last resort, try starting a new window.
	cmd = exec.Command("wezterm", "start", "--cwd", dir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err == nil {
		return TerminalWezTerm, nil
	}
	// WezTerm not installed or failed; fall back on macOS.
	if runtime.GOOS == "darwin" {
		return openTerminalApp(dir)
	}
	return TerminalWezTerm, fmt.Errorf("wezterm not found or failed to launch")
}

// openKitty opens a new tab in Kitty using kitten remote control.
func openKitty(dir string) (TerminalType, error) {
	if !KittyRemoteControlTargetAvailable() {
		// Fall back on macOS.
		if runtime.GOOS == "darwin" {
			return openTerminalApp(dir)
		}
		return TerminalKitty, fmt.Errorf("kitty remote control not available (no KITTY_WINDOW_ID or KITTY_LISTEN_ON)")
	}
	cmd := exec.Command("kitten", "@", "launch", "--type=tab", "--cwd", dir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	kittenErr := cmd.Run()
	if kittenErr == nil {
		return TerminalKitty, nil
	}
	// kitten @ failed (likely remote control not authorized).
	// Fall back on macOS.
	if runtime.GOOS == "darwin" {
		return openTerminalApp(dir)
	}
	return TerminalKitty, fmt.Errorf("kitten @ launch failed: %w", kittenErr)
}

// openITerm2 opens a new tab in iTerm2 using AppleScript.
func openITerm2(dir string) (TerminalType, error) {
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
	cmd := exec.CommandContext(context.TODO(), "osascript", "-e", script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	itermErr := cmd.Run()
	if itermErr == nil {
		return TerminalITerm2, nil
	}
	// Fall back to Terminal.app on macOS.
	if runtime.GOOS == "darwin" {
		return openTerminalApp(dir)
	}
	return TerminalITerm2, fmt.Errorf("iTerm2 AppleScript failed: %w", itermErr)
}

// openTerminalApp opens a new tab in Terminal.app using AppleScript.
// Uses quoted form of POSIX path for safe AppleScript quoting.
func openTerminalApp(dir string) (TerminalType, error) {
	safeDir := sanitizeAppleScriptPath(sanitizeArgPath(dir))
	script := fmt.Sprintf(`tell application "Terminal"
	activate
	do script "cd " & quoted form of POSIX path "%s" in front window
end tell`, safeDir)
	cmd := exec.CommandContext(context.TODO(), "osascript", "-e", script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return TerminalTerminal, fmt.Errorf("Terminal.app AppleScript failed: %w", err)
	}
	return TerminalTerminal, nil
}

// openWarp opens Warp (limited: new window only, no tab support).
func openWarp(dir string) (TerminalType, error) {
	cmd := exec.Command("open", "-a", "Warp.app", sanitizeArgPath(dir))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return TerminalWarp, fmt.Errorf("failed to open Warp.app: %w", err)
	}
	return TerminalWarp, nil
}

// openAlacritty opens a new window in Alacritty (no tab support).
func openAlacritty(dir string) (TerminalType, error) {
	cmd := exec.Command("alacritty", "--working-directory", sanitizeArgPath(dir))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// Fall back on macOS.
		if runtime.GOOS == "darwin" {
			return openTerminalApp(dir)
		}
		return TerminalAlacritty, fmt.Errorf("alacritty failed: %w", err)
	}
	return TerminalAlacritty, nil
}
