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
