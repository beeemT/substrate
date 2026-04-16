package views

import (
	"fmt"
	"strings"

	"github.com/beeemT/substrate/internal/tui/styles"
)

// HelpOverlay is a read-only keybind reference panel.
// It has no internal state; dismissal is handled by app.go overlay routing (Esc).
type HelpOverlay struct {
	st styles.Styles
}

// NewHelpOverlay creates a HelpOverlay pre-built with the given styles.
func NewHelpOverlay(st styles.Styles) HelpOverlay {
	return HelpOverlay{st: st}
}

// View renders the help overlay. The caller passes the result to renderOverlay.
func (h HelpOverlay) View() string {
	type entry struct{ key, label string }
	type section struct {
		name    string
		entries []entry
	}

	global := []entry{
		{"n", "New session"},
		{"r", "Repos"},
		{"s", "Settings"},
		{"L", "Logs"},
		{"j / ↓", "Navigate down"},
		{"k / ↑", "Navigate up"},
		{"Esc", "Back / close overlay / cancel"},
		{"q", "Quit"},
		{"Ctrl+C", "Force quit"},
	}

	panels := []section{
		{"Plan Review", []entry{
			{"a", "Approve plan"},
			{"c", "Request changes"},
			{"e", "Edit in $EDITOR"},
			{"↑↓", "Scroll"},
		}},
		{"Implementing", []entry{
			{"Tab", "Cycle repos"},
			{"p", "Pause / unpause"},
			{"↑↓", "Scroll"},
		}},
		{"Question (Foreman)", []entry{
			{"A", "Approve Foreman answer"},
			{"Enter", "Send message to Foreman"},
			{"Esc", "Skip question"},
		}},
		{"Reviewing", []entry{
			{"j/k", "Navigate critiques"},
			{"Tab", "Switch repo"},
			{"r", "Re-implement"},
			{"o", "Override accept"},
		}},
		{"Interrupted", []entry{
			{"r", "Resume session"},
			{"a", "Abandon session"},
		}},
		{"Repo Manager", []entry{
			{"a", "Add repo"},
			{"d", "Delete selected repo"},
			{"i", "Init plain git repo (git-work)"},
			{"Tab", "Switch focus"},
			{"Esc", "Close"},
		}},
	}

	var sb strings.Builder
	sb.WriteString(h.st.Title.Render("Keybindings") + "\n\n")

	sb.WriteString(h.st.Subtitle.Render("Global") + "\n")
	for _, e := range global {
		fmt.Fprintf(&sb, "  %-18s %s\n", h.st.KeybindAccent.Render(e.key), e.label)
	}

	for _, sec := range panels {
		sb.WriteString("\n" + h.st.Subtitle.Render(sec.name) + "\n")
		for _, e := range sec.entries {
			fmt.Fprintf(&sb, "  %-18s %s\n", h.st.KeybindAccent.Render(e.key), e.label)
		}
	}

	sb.WriteString("\n" + h.st.Muted.Render("Esc  close"))

	return h.st.OverlayFrame.Padding(1, 3).Render(sb.String())
}
