package components

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/beeemT/substrate/internal/tui/styles"
)

// ConfirmDialog is a generic yes/no confirmation modal.
type ConfirmDialog struct {
	Title   string
	Message string
	OnYes   tea.Cmd
	Active  bool
	Styles  styles.Styles
}

// NewConfirmDialog creates an active confirmation dialog.
func NewConfirmDialog(st styles.Styles, title, message string, onYes tea.Cmd) ConfirmDialog {
	return ConfirmDialog{Title: title, Message: message, OnYes: onYes, Active: true, Styles: st}
}

// ConfirmMsg is emitted when the dialog is resolved.
type ConfirmMsg struct{ Confirmed bool }

// View renders the dialog; returns empty string when inactive.
func (d ConfirmDialog) View() string {
	if !d.Active {
		return ""
	}
	style := d.Styles.OverlayFrame.Copy().Padding(1, 2)
	content := lipgloss.JoinVertical(lipgloss.Left,
		d.Styles.Title.Render(d.Title),
		"",
		d.Styles.Subtitle.Render(d.Message),
		"",
		d.Styles.KeybindAccent.Render("[y]")+d.Styles.Subtitle.Render(" Confirm  ")+
			d.Styles.KeybindAccent.Render("[n]")+d.Styles.Subtitle.Render(" Cancel"),
	)
	return style.Render(content)
}
