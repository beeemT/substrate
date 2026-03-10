package components

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/beeemT/substrate/internal/tui/styles"
)

type ToastLevel int

const (
	ToastInfo ToastLevel = iota
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
	styles styles.Styles
	toasts []Toast
}

func NewToastModel(st styles.Styles) ToastModel {
	return ToastModel{styles: st}
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

func (m *ToastModel) View() string {
	if len(m.toasts) == 0 {
		return ""
	}
	return renderToast(m.styles, m.toasts[len(m.toasts)-1])
}

func (m *ToastModel) StackView(pinned ...Toast) string {
	if len(pinned) == 0 {
		return m.View()
	}
	pinnedViews := make([]string, 0, len(pinned))
	for _, toast := range pinned {
		pinnedViews = append(pinnedViews, renderToast(m.styles, toast))
	}
	if len(m.toasts) == 0 {
		return lipgloss.JoinVertical(lipgloss.Left, pinnedViews...)
	}
	transientViews := make([]string, 0, len(m.toasts))
	for i := len(m.toasts) - 1; i >= 0; i-- {
		transientViews = append(transientViews, renderToast(m.styles, m.toasts[i]))
	}
	stack := make([]string, 0, len(pinnedViews)+1)
	stack = append(stack, pinnedViews...)
	stack = append(stack, lipgloss.JoinVertical(lipgloss.Right, transientViews...))
	return lipgloss.JoinVertical(lipgloss.Left, stack...)
}

func renderToast(st styles.Styles, t Toast) string {
	style := st.OverlayPane.Copy().
		Foreground(lipgloss.Color(st.Theme.Title)).
		Background(lipgloss.Color(st.Theme.OverlayBg)).
		BorderForeground(borderColorForLevel(st, t.Level)).
		Padding(0, 1).
		Bold(true)
	prefix := prefixForLevel(t.Level)
	return style.Render(prefix + t.Message)
}

func borderColorForLevel(st styles.Styles, l ToastLevel) lipgloss.TerminalColor {
	switch l {
	case ToastSuccess:
		return lipgloss.Color(st.Theme.Success)
	case ToastWarning:
		return lipgloss.Color(st.Theme.Warning)
	case ToastError:
		return lipgloss.Color(st.Theme.Error)
	default:
		return lipgloss.Color(st.Theme.Active)
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
