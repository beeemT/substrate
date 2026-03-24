package views

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/tui/styles"
	"github.com/beeemT/substrate/internal/tuilog"
)

// LogsOverlay displays captured slog entries in a scrollable viewport.
type LogsOverlay struct {
	st    styles.Styles
	store *tuilog.Store
	vp    viewport.Model
	width int
	height int
	ready bool
}

// NewLogsOverlay creates a LogsOverlay backed by the given store.
func NewLogsOverlay(store *tuilog.Store, st styles.Styles) LogsOverlay {
	return LogsOverlay{st: st, store: store}
}

// SetSize updates the overlay dimensions on terminal resize.
func (l *LogsOverlay) SetSize(w, h int) {
	l.width = w
	l.height = h
}

// Open refreshes the viewport content from the log store.
func (l *LogsOverlay) Open() {
	content := l.renderContent()

	// Title + blank line + footer hint = 4 reserved lines.
	// Overlay frame border/padding adds 4 more (top/bottom padding 1 each + border 1 each).
	const chromeRows = 4
	const frameRows = 4
	maxH := l.height - frameRows
	if maxH < chromeRows+3 {
		maxH = chromeRows + 3
	}
	vpH := maxH - chromeRows
	if vpH < 1 {
		vpH = 1
	}

	innerW := l.overlayInnerWidth()
	l.vp = viewport.New(innerW, vpH)
	l.vp.SetContent(content)
	l.vp.GotoBottom()
	l.ready = true
}

// Update handles key/mouse events while the overlay is active.
func (l LogsOverlay) Update(msg tea.Msg) (LogsOverlay, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			return l, func() tea.Msg { return CloseOverlayMsg{} }
		}
	case tea.MouseMsg:
		if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
			if msg.Action == tea.MouseActionPress {
				var cmd tea.Cmd
				l.vp, cmd = l.vp.Update(msg)
				return l, cmd
			}
		}
		return l, nil
	}

	var cmd tea.Cmd
	l.vp, cmd = l.vp.Update(msg)
	return l, cmd
}

// View renders the logs overlay.
func (l LogsOverlay) View() string {
	if !l.ready {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(l.st.Title.Render("Logs") + "\n\n")
	sb.WriteString(l.vp.View())
	sb.WriteString("\n" + l.st.Muted.Render("↑↓ scroll  Esc close"))

	return l.st.OverlayFrame.Padding(1, 3).Render(sb.String())
}

func (l LogsOverlay) overlayInnerWidth() int {
	w := l.width * 3 / 4
	if w < 60 {
		w = 60
	}
	if w > l.width-4 {
		w = l.width - 4
	}
	return w
}

func (l LogsOverlay) renderContent() string {
	if l.store == nil {
		return "(no log store)"
	}
	entries := l.store.Snapshot()
	if len(entries) == 0 {
		return l.st.Muted.Render("  (no log entries)")
	}

	innerW := l.overlayInnerWidth()
	// Account for padding inside OverlayFrame (3 left + 3 right = 6).
	textW := innerW - 6
	if textW < 20 {
		textW = 20
	}

	var sb strings.Builder
	for _, e := range entries {
		ts := l.st.Muted.Render(e.Time.Format("15:04:05"))
		level := l.levelStyle(e.Level).Render(fmt.Sprintf("%-5s", e.Level.String()))
		msg := e.Message
		if e.Attrs != "" {
			msg += " " + l.st.Muted.Render(e.Attrs)
		}

		line := ts + " " + level + " " + msg
		// Truncate if wider than available space.
		if ansi.StringWidth(line) > textW {
			line = ansi.Truncate(line, textW, "…")
		}
		sb.WriteString(line)
		sb.WriteByte('\n')
	}

	return strings.TrimRight(sb.String(), "\n")
}

func (l LogsOverlay) levelStyle(level slog.Level) lipgloss.Style {
	switch {
	case level >= slog.LevelError:
		return l.st.Error
	case level >= slog.LevelWarn:
		return l.st.Warning
	case level >= slog.LevelInfo:
		return lipgloss.NewStyle().Foreground(lipgloss.Color(l.st.Theme.Active))
	default:
		return l.st.Muted
	}
}
