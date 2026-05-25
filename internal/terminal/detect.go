package terminal

import (
	"os"
)

// TerminalType identifies the active terminal emulator.
type TerminalType string

const (
	TerminalUnknown   TerminalType = ""
	TerminalWarp      TerminalType = "warp"
	TerminalITerm2    TerminalType = "iterm2"
	TerminalTerminal  TerminalType = "terminal"
	TerminalKitty     TerminalType = "kitty"
	TerminalWezTerm   TerminalType = "wezterm"
	TerminalAlacritty TerminalType = "alacritty"
)

// Detect returns the active terminal based on TERM_PROGRAM and other heuristics.
func Detect() TerminalType {
	term := os.Getenv("TERM_PROGRAM")
	switch term {
	case "WarpTerminal", "Warp":
		return TerminalWarp
	case "iTerm.app":
		return TerminalITerm2
	case "Apple_Terminal":
		return TerminalTerminal
	}
	// Check for kitty window ID. This identifies Kitty, but does not prove
	// remote-control authorization; launch code must still handle kitten errors.
	if _, ok := os.LookupEnv("KITTY_WINDOW_ID"); ok {
		return TerminalKitty
	}
	// Check for WezTerm. WEZTERM_PANE is the documented pane selector used by
	// `wezterm cli spawn`; keep WEZTERM_SOCK as an additional heuristic.
	if _, ok := os.LookupEnv("WEZTERM_PANE"); ok {
		return TerminalWezTerm
	}
	if _, ok := os.LookupEnv("WEZTERM_SOCK"); ok {
		return TerminalWezTerm
	}
	return TerminalUnknown
}

// KittyRemoteControlTargetAvailable reports whether `kitten @` has an addressing
// target. Authorization still depends on kitty configuration
// (allow_remote_control/remote_control_password) and must be handled as a
// command error.
func KittyRemoteControlTargetAvailable() bool {
	if _, ok := os.LookupEnv("KITTY_WINDOW_ID"); ok {
		return true
	}
	if _, ok := os.LookupEnv("KITTY_LISTEN_ON"); ok {
		return true
	}
	return false
}
