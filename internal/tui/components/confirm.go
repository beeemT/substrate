package components

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ConfirmDialog is a generic yes/no confirmation modal.
type ConfirmDialog struct {
	Title   string
	Message string
	OnYes   tea.Cmd
	Active  bool
}

// NewConfirmDialog creates an active confirmation dialog.
func NewConfirmDialog(title, message string, onYes tea.Cmd) ConfirmDialog {
	return ConfirmDialog{Title: title, Message: message, OnYes: onYes, Active: true}
}

// ConfirmMsg is emitted when the dialog is resolved.
type ConfirmMsg struct{ Confirmed bool }

// View renders the dialog; returns empty string when inactive.
func (d ConfirmDialog) View() string {
	if !d.Active {
		return ""
	}
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#2d2d44")).
		Padding(1, 2).
		Background(lipgloss.Color("#1a1a2e"))
	content := lipgloss.NewStyle().Foreground(lipgloss.Color("#f0f0f0")).Bold(true).Render(d.Title) + "\n\n" +
		lipgloss.NewStyle().Foreground(lipgloss.Color("#b0b0b0")).Render(d.Message) + "\n\n" +
		lipgloss.NewStyle().Foreground(lipgloss.Color("#5b8def")).Bold(true).Render("[y]") +
		lipgloss.NewStyle().Foreground(lipgloss.Color("#b0b0b0")).Render(" Confirm  ") +
		lipgloss.NewStyle().Foreground(lipgloss.Color("#5b8def")).Bold(true).Render("[n]") +
		lipgloss.NewStyle().Foreground(lipgloss.Color("#b0b0b0")).Render(" Cancel")
	return style.Render(content)
}
