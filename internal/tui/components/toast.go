package components

import (
	"strings"
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

	rendered      string
	renderedWidth int
}

type ToastModel struct {
	styles styles.Styles
	toasts []Toast
}

func NewToastModel(st styles.Styles) ToastModel {
	return ToastModel{styles: st}
}

func (m *ToastModel) AddToast(msg string, level ToastLevel) {
	toast := Toast{
		Message: msg,
		Level:   level,
		Expires: time.Now().Add(3 * time.Second),
	}
	toast.rendered = renderToast(m.styles, toast)
	toast.renderedWidth = lipgloss.Width(toast.rendered)
	m.toasts = append(m.toasts, toast)
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
	rendered, _ := m.toastRenderData(m.toasts[len(m.toasts)-1])

	return rendered
}

func (m *ToastModel) StackView(pinned ...Toast) string {
	if len(pinned) == 0 {
		return m.View()
	}
	items := make([]Toast, 0, len(pinned)+len(m.toasts))
	items = append(items, pinned...)
	for i := len(m.toasts) - 1; i >= 0; i-- {
		items = append(items, m.toasts[i])
	}
	views := make([]string, 0, len(items))
	widths := make([]int, 0, len(items))
	maxWidth := 0
	for _, toast := range items {
		rendered, width := m.toastRenderData(toast)
		views = append(views, rendered)
		widths = append(widths, width)
		maxWidth = max(maxWidth, width)
	}
	if maxWidth <= 0 {
		return lipgloss.JoinVertical(lipgloss.Left, views...)
	}
	normalized := make([]string, 0, len(items))
	for i, toast := range items {
		if widths[i] < maxWidth {
			normalized = append(normalized, renderToastAtWidth(m.styles, toast, maxWidth))

			continue
		}
		normalized = append(normalized, views[i])
	}

	return lipgloss.JoinVertical(lipgloss.Left, normalized...)
}

func (m *ToastModel) toastRenderData(toast Toast) (string, int) {
	if toast.rendered != "" && toast.renderedWidth > 0 {
		return toast.rendered, toast.renderedWidth
	}
	rendered := renderToastAtWidth(m.styles, toast, 0)

	return rendered, lipgloss.Width(rendered)
}

func renderToast(st styles.Styles, t Toast) string {
	return renderToastAtWidth(st, t, 0)
}

func renderToastAtWidth(st styles.Styles, t Toast, width int) string {
	contentText := prefixForLevel(t.Level) + t.Message
	contentWidth := lipgloss.Width(contentText)
	if width <= 0 {
		width = contentWidth + 4
	}
	width = max(width, 4)
	innerWidth := width - 2
	innerWidth = max(innerWidth, 2)
	textWidth := innerWidth - 2
	textWidth = max(textWidth, 0)
	padding := strings.Repeat(" ", max(0, textWidth-contentWidth))

	borderStyle := lipgloss.NewStyle().Foreground(borderColorForLevel(st, t.Level))
	contentStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(st.Theme.Title)).Bold(true)
	top := borderStyle.Render("╭" + strings.Repeat("─", innerWidth) + "╮")
	middle := borderStyle.Render("│") + contentStyle.Render(" "+contentText+padding+" ") + borderStyle.Render("│")
	bottom := borderStyle.Render("╰" + strings.Repeat("─", innerWidth) + "╯")

	return strings.Join([]string{top, middle, bottom}, "\n")
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
