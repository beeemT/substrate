package terminal

import (
	"os"
	"testing"
)

func TestDetect(t *testing.T) {
	tests := []struct {
		name         string
		env          map[string]string
		want         TerminalType
		unsetTERMPRG bool
	}{
		{
			name:         "warp terminal",
			env:          map[string]string{"TERM_PROGRAM": "WarpTerminal"},
			want:         TerminalWarp,
			unsetTERMPRG: false,
		},
		{
			name:         "warp",
			env:          map[string]string{"TERM_PROGRAM": "Warp"},
			want:         TerminalWarp,
			unsetTERMPRG: false,
		},
		{
			name:         "iterm2",
			env:          map[string]string{"TERM_PROGRAM": "iTerm.app"},
			want:         TerminalITerm2,
			unsetTERMPRG: false,
		},
		{
			name:         "terminal app",
			env:          map[string]string{"TERM_PROGRAM": "Apple_Terminal"},
			want:         TerminalTerminal,
			unsetTERMPRG: false,
		},
		{
			name:         "kitty via KITTY_WINDOW_ID",
			env:          map[string]string{"KITTY_WINDOW_ID": "1"},
			want:         TerminalKitty,
			unsetTERMPRG: true,
		},
		{
			name:         "wezterm via WEZTERM_PANE",
			env:          map[string]string{"WEZTERM_PANE": "1"},
			want:         TerminalWezTerm,
			unsetTERMPRG: true,
		},
		{
			name:         "wezterm via WEZTERM_SOCK",
			env:          map[string]string{"WEZTERM_SOCK": "/tmp/wezterm.sock"},
			want:         TerminalWezTerm,
			unsetTERMPRG: true,
		},
		{
			name:         "unknown terminal",
			env:          map[string]string{},
			want:         TerminalUnknown,
			unsetTERMPRG: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original env
			origTERMPRG := os.Getenv("TERM_PROGRAM")
			origKITTY := os.Getenv("KITTY_WINDOW_ID")
			origWEZPANE := os.Getenv("WEZTERM_PANE")
			origWEZSOCK := os.Getenv("WEZTERM_SOCK")
			defer func() {
				os.Setenv("TERM_PROGRAM", origTERMPRG)
				os.Setenv("KITTY_WINDOW_ID", origKITTY)
				os.Setenv("WEZTERM_PANE", origWEZPANE)
				os.Setenv("WEZTERM_SOCK", origWEZSOCK)
			}()

			// Unset all terminal env vars
			os.Unsetenv("TERM_PROGRAM")
			os.Unsetenv("KITTY_WINDOW_ID")
			os.Unsetenv("WEZTERM_PANE")
			os.Unsetenv("WEZTERM_SOCK")

			// Set the test env vars
			if !tt.unsetTERMPRG {
				os.Setenv("TERM_PROGRAM", tt.env["TERM_PROGRAM"])
			}
			if v, ok := tt.env["KITTY_WINDOW_ID"]; ok {
				os.Setenv("KITTY_WINDOW_ID", v)
			}
			if v, ok := tt.env["WEZTERM_PANE"]; ok {
				os.Setenv("WEZTERM_PANE", v)
			}
			if v, ok := tt.env["WEZTERM_SOCK"]; ok {
				os.Setenv("WEZTERM_SOCK", v)
			}

			got := Detect()
			if got != tt.want {
				t.Errorf("Detect() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestKittyRemoteControlTargetAvailable(t *testing.T) {
	tests := []struct {
		name   string
		env    map[string]string
		unset  bool
		expect bool
	}{
		{
			name:   "KITTY_WINDOW_ID set",
			env:    map[string]string{"KITTY_WINDOW_ID": "1"},
			unset:  false,
			expect: true,
		},
		{
			name:   "KITTY_LISTEN_ON set",
			env:    map[string]string{"KITTY_LISTEN_ON": "unix:/tmp/mykitty"},
			unset:  false,
			expect: true,
		},
		{
			name:   "neither set",
			env:    map[string]string{},
			unset:  true,
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origKITTY := os.Getenv("KITTY_WINDOW_ID")
			origLISTEN := os.Getenv("KITTY_LISTEN_ON")
			defer func() {
				os.Setenv("KITTY_WINDOW_ID", origKITTY)
				os.Setenv("KITTY_LISTEN_ON", origLISTEN)
			}()

			os.Unsetenv("KITTY_WINDOW_ID")
			os.Unsetenv("KITTY_LISTEN_ON")

			if !tt.unset {
				if v, ok := tt.env["KITTY_WINDOW_ID"]; ok {
					os.Setenv("KITTY_WINDOW_ID", v)
				}
				if v, ok := tt.env["KITTY_LISTEN_ON"]; ok {
					os.Setenv("KITTY_LISTEN_ON", v)
				}
			}

			got := KittyRemoteControlTargetAvailable()
			if got != tt.expect {
				t.Errorf("KittyRemoteControlTargetAvailable() = %v, want %v", got, tt.expect)
			}
		})
	}
}
