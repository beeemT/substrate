package terminal

import (
	"testing"
)

func TestTerminalTypeConstants(t *testing.T) {
	// Verify all terminal types are distinct
	types := []TerminalType{
		TerminalUnknown,
		TerminalWarp,
		TerminalITerm2,
		TerminalTerminal,
		TerminalKitty,
		TerminalWezTerm,
		TerminalAlacritty,
	}
	seen := make(map[TerminalType]bool)
	for _, tt := range types {
		if seen[tt] {
			t.Errorf("duplicate terminal type: %v", tt)
		}
		seen[tt] = true
	}
}

func TestTerminalTypeStringValues(t *testing.T) {
	tests := []struct {
		term    TerminalType
		wantStr string
	}{
		{TerminalUnknown, ""},
		{TerminalWarp, "warp"},
		{TerminalITerm2, "iterm2"},
		{TerminalTerminal, "terminal"},
		{TerminalKitty, "kitty"},
		{TerminalWezTerm, "wezterm"},
		{TerminalAlacritty, "alacritty"},
	}

	for _, tt := range tests {
		t.Run(string(tt.wantStr), func(t *testing.T) {
			if got := string(tt.term); got != tt.wantStr {
				t.Errorf("TerminalType(%v) = %q, want %q", tt.term, got, tt.wantStr)
			}
		})
	}
}

func TestSanitizeAppleScriptPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{"normal path unchanged", "/Users/test/project", "/Users/test/project"},
		{"double quotes escaped", `path/with"quote`, `path/with\"quote`},
		{"backslash escaped", `path\with`, `path\\with`},
		{"both escaped", `path"\test`, `path\"\\test`},
		{"empty path", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeAppleScriptPath(tt.path)
			if got != tt.want {
				t.Errorf("sanitizeAppleScriptPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestSanitizeArgPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{"normal path unchanged", "/Users/test/project", "/Users/test/project"},
		{"empty path becomes dot", "", "."},
		{"flag-like path prepended", "-l", "./-l"},
		{"double dash prepended", "--help", "./--help"},
		{"relative path unchanged", "./my-project", "./my-project"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeArgPath(tt.path)
			if got != tt.want {
				t.Errorf("sanitizeArgPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}
