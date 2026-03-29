package views

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/atotto/clipboard"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/tui/styles"
	"github.com/beeemT/substrate/internal/tuilog"
)

// LogsOverlay displays captured slog entries in a scrollable viewport.
type LogsOverlay struct {
	st     styles.Styles
	store  *tuilog.Store
	vp     viewport.Model
	width  int
	height int
	ready  bool
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
	maxH := max(chromeRows+3, l.height-frameRows)
	vpH := max(1, maxH-chromeRows)

	innerW := l.overlayInnerWidth()
	l.vp = viewport.New(innerW, vpH)
	l.vp.SetContent(content)
	l.vp.GotoBottom()
	l.ready = true
}

// Update handles key/mouse events while the overlay is active.
func (l *LogsOverlay) Update(msg tea.Msg) (LogsOverlay, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "c":
			// clipboardContent produces raw unwrapped plain text with no gutter.
			if clipErr := clipboard.WriteAll(l.clipboardContent()); clipErr != nil {
				slog.Warn("failed to copy log to clipboard", "error", clipErr)
			}
			return *l, func() tea.Msg { return ActionDoneMsg{Message: "Log copied to clipboard"} }
		case keyEsc:
			return *l, func() tea.Msg { return CloseOverlayMsg{} }
		}
	case tea.MouseMsg:
		if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
			if msg.Action == tea.MouseActionPress {
				var cmd tea.Cmd
				l.vp, cmd = l.vp.Update(msg)
				return *l, cmd
			}
		}
		return *l, nil
	}

	var cmd tea.Cmd
	l.vp, cmd = l.vp.Update(msg)
	return *l, cmd
}

// View renders the logs overlay.
func (l *LogsOverlay) View() string {
	if !l.ready {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(l.st.Title.Render("Logs") + "\n\n")
	sb.WriteString(l.vp.View())
	sb.WriteString("\n" + l.st.Muted.Render("↑↓ scroll  c copy  Esc close"))

	return l.st.OverlayFrame.Padding(1, 3).Render(sb.String())
}

func (l *LogsOverlay) overlayInnerWidth() int {
	w := min(l.width-4, max(60, l.width*3/4))
	return w
}

func (l *LogsOverlay) renderContent() string {
	if l.store == nil {
		return "(no log store)"
	}
	entries := l.store.Snapshot()
	if len(entries) == 0 {
		return l.st.Muted.Render("  (no log entries)")
	}

	innerW := l.overlayInnerWidth()
	// Account for padding inside OverlayFrame (3 left + 3 right = 6).
	textW := max(20, innerW-6)

	// Gutter sizing matches renderPlanReviewContent: right-aligned line
	// number, fixed separator, remaining width for wrapped content.
	numberWidth := max(2, len(strconv.Itoa(len(entries))))
	const separator = " │ "
	separatorWidth := ansi.StringWidth(separator)
	contentWidth := max(1, textW-numberWidth-separatorWidth)

	var sb strings.Builder
	for index, e := range entries {
		ts := l.st.Muted.Render(e.Time.Format("15:04:05"))
		level := l.levelStyle(e.Level).Render(fmt.Sprintf("%-5s", e.Level.String()))
		msg := e.Message
		if e.Attrs != "" {
			msg += " " + l.st.Muted.Render(e.Attrs)
		}
		line := ts + " " + level + " " + msg

		// ANSI-aware word wrap so escape codes are preserved across breaks.
		wrapped := ansi.Hardwrap(line, contentWidth, true)
		segments := strings.Split(wrapped, "\n")

		for segIdx, segment := range segments {
			gutter := strings.Repeat(" ", numberWidth)
			if segIdx == 0 {
				gutter = fmt.Sprintf("%*d", numberWidth, index+1)
			}
			// Pad content to a fixed width so continuation lines align with
			// the first segment under the same gutter column.
			paddedContent := lipgloss.NewStyle().Width(contentWidth).Render(segment)
			sb.WriteString(l.st.Muted.Render(gutter+separator) + paddedContent + "\n")
		}
	}

	return strings.TrimRight(sb.String(), "\n")
}

// clipboardContent returns the log entries as raw plain text for clipboard
// use: one entry per line, no line numbers, no separator, no ANSI codes,
// and no wrapping — Entry.String() is purpose-built for this.
func (l *LogsOverlay) clipboardContent() string {
	if l.store == nil {
		return ""
	}
	entries := l.store.Snapshot()
	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(e.String())
		sb.WriteByte('\n')
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (l *LogsOverlay) levelStyle(level slog.Level) lipgloss.Style {
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
