package terminal

import (
	"os"
	"strings"
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

func TestCommand_TerminalAppUsesQuotedPOSIXPath(t *testing.T) {
	cmd := terminalAppAppleScriptCmd(`/tmp/work tree/with"quote`)
	if cmd.Args[0] != "osascript" || cmd.Args[1] != "-e" {
		t.Fatalf("args = %#v, want osascript -e", cmd.Args)
	}
	script := cmd.Args[2]
	if !strings.Contains(script, `quoted form of POSIX path`) {
		t.Fatalf("script missing quoted POSIX path: %s", script)
	}
	if strings.Contains(script, `do script "cd %s"`) {
		t.Fatalf("script uses unsafe cd formatting: %s", script)
	}
	if !strings.Contains(script, `/tmp/work tree/with\"quote`) {
		t.Fatalf("script does not escape path literal: %s", script)
	}
}

func TestCommand_ITerm2UsesQuotedPOSIXPath(t *testing.T) {
	cmd := iTerm2Cmd(`/tmp/work tree`)
	if cmd.Args[0] != "osascript" || cmd.Args[1] != "-e" {
		t.Fatalf("args = %#v, want osascript -e", cmd.Args)
	}
	script := cmd.Args[2]
	if !strings.Contains(script, `write text "cd " & quoted form of POSIX path "/tmp/work tree"`) {
		t.Fatalf("script missing safe iTerm cd command: %s", script)
	}
}

func TestCommand_KittyUsesCwdArg(t *testing.T) {
	dir := "/tmp/work tree"
	cmd := kittyLaunchCmd(dir)
	want := []string{"kitten", "@", "launch", "--type=tab", "--cwd", dir}
	assertArgs(t, cmd.Args, want)
}

func TestCommand_WezTermUsesCwdArg(t *testing.T) {
	dir := "/tmp/work tree"
	assertArgs(t, wezTermCliSpawnCmd(dir).Args, []string{"wezterm", "cli", "spawn", "--cwd", dir})
	assertArgs(t, wezTermStartNewTabCmd(dir).Args, []string{"wezterm", "start", "--new-tab", "--cwd", dir})
	assertArgs(t, wezTermStartWindowCmd(dir).Args, []string{"wezterm", "start", "--cwd", dir})
}

func TestCommand_WarpUsesDocumentedURIWithEscapedPath(t *testing.T) {
	dir := "/tmp/work tree"
	cmd := warpCmd(dir)
	assertArgs(t, cmd.Args, []string{"open", "warp://action/new_tab?path=%2Ftmp%2Fwork+tree"})
}

func TestCommand_TerminalFallbackUsesOpenAppWithPathArg(t *testing.T) {
	dir := "/tmp/work tree"
	cmd := terminalAppOpenFallbackCmd(dir)
	assertArgs(t, cmd.Args, []string{"open", "-a", "Terminal.app", dir})
}

func TestCommand_AlacrittyUsesWorkingDirectoryArg(t *testing.T) {
	dir := "/tmp/work tree"
	cmd := alacrittyCmd(dir)
	assertArgs(t, cmd.Args, []string{"alacritty", "--working-directory", dir})
}

func TestOpenRejectsMissingDirectory(t *testing.T) {
	missing := t.TempDir() + "/missing"
	if _, err := OpenWithTerminal(missing, TerminalTerminal); err == nil {
		t.Fatal("expected missing directory error")
	}
}

func TestOpenRejectsFilePath(t *testing.T) {
	file := t.TempDir() + "/file"
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenWithTerminal(file, TerminalTerminal); err == nil {
		t.Fatal("expected file path error")
	}
}

func assertArgs(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("args = %#v, want %#v", got, want)
		}
	}
}
