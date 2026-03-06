package components

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type ToastLevel int

const (
	ToastInfo    ToastLevel = iota
	ToastSuccess
	ToastWarning
	ToastError
)

type Toast struct {
	Message string
	Level   ToastLevel
	Expires time.Time
}

type ToastModel struct {
	toasts []Toast
}

func (m *ToastModel) AddToast(msg string, level ToastLevel) {
	m.toasts = append(m.toasts, Toast{
		Message: msg,
		Level:   level,
		Expires: time.Now().Add(3 * time.Second),
	})
}

func (m *ToastModel) Prune() {
	now := time.Now()
	filtered := m.toasts[:0]
	for _, t := range m.toasts {
		if t.Expires.After(now) {
			filtered = append(filtered, t)
		}
	}
	m.toasts = filtered
}

func (m *ToastModel) HasToasts() bool { return len(m.toasts) > 0 }

func (m *ToastModel) View(fg, bg string) string {
	if len(m.toasts) == 0 {
		return ""
	}
	t := m.toasts[len(m.toasts)-1]
	color := colorForLevel(t.Level)
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#f0f0f0")).
		Background(lipgloss.Color("#1a1a2e")).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(color)).
		Padding(0, 1).
		Bold(true)
	prefix := prefixForLevel(t.Level)
	return style.Render(prefix + t.Message)
}

func colorForLevel(l ToastLevel) string {
	switch l {
	case ToastSuccess:
		return "#34d399"
	case ToastWarning:
		return "#fbbf24"
	case ToastError:
		return "#f87171"
	default:
		return "#5b8def"
	}
}

func prefixForLevel(l ToastLevel) string {
	switch l {
	case ToastSuccess:
		return "✓ "
	case ToastWarning:
		return "⚠ "
	case ToastError:
		return "✗ "
	default:
		return "ℹ "
	}
}

// ToastTickMsg is sent every second to prune expired toasts.
type ToastTickMsg time.Time

// ToastTickCmd returns a Cmd that fires after 1s.
func ToastTickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return ToastTickMsg(t)
	})
}
