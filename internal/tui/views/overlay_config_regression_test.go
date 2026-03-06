package views

import (
	"os"
	"testing"

	"github.com/beeemT/substrate/internal/tui/styles"
)

// TestConfigOverlay_ConfigSaveMsg_WritesFile is a regression test for the bug
// where ConfigSaveMsg updated in-memory state (rawContent/dirty) but never
// called saveCmd, leaving the config file unchanged on disk.
func TestConfigOverlay_ConfigSaveMsg_WritesFile(t *testing.T) {
	t.Parallel()

	// Arrange: temp file that acts as the config path.
	f, err := os.CreateTemp(t.TempDir(), "config-*.toml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("old = true\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	m := ConfigOverlay{
		configPath: f.Name(),
		rawContent: "old = true\n",
		styles:     styles.NewStyles(styles.DefaultTheme),
	}

	newContent := "new = true\ncustom = 42\n"

	// Act: dispatch ConfigSaveMsg — this must trigger saveCmd.
	updated, cmd := m.Update(ConfigSaveMsg{NewContent: newContent})

	// Assert model state.
	if updated.dirty {
		t.Error("dirty should be cleared after ConfigSaveMsg")
	}
	if updated.rawContent != newContent {
		t.Errorf("rawContent: want %q, got %q", newContent, updated.rawContent)
	}

	// Assert a write command was returned.
	if cmd == nil {
		t.Fatal("cmd is nil — ConfigSaveMsg handler did not return saveCmd()")
	}

	// Execute the command; it should write the file and return ActionDoneMsg.
	result := cmd()
	if _, ok := result.(ActionDoneMsg); !ok {
		t.Fatalf("expected ActionDoneMsg from saveCmd, got %T: %v", result, result)
	}

	// Verify the file on disk was actually updated.
	written, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != newContent {
		t.Errorf("disk content: want %q, got %q", newContent, string(written))
	}
}
