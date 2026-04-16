package views

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

const overviewLinksMaxWidth = 110

type overviewLinksItem struct {
	label string // primary display: ref · title or repo · ref
	meta  string // secondary: provider or state/branch
	url   string
}

// OverviewLinksOverlay displays input tickets and MR/PR links for a session.
type OverviewLinksOverlay struct { //nolint:recvcheck
	active  bool
	tickets []overviewLinksItem
	mrs     []overviewLinksItem
	cursor  int // index into items() (tickets first, then MRs)
	vp      viewport.Model
	styles  styles.Styles
	width   int
	height  int
}

func NewOverviewLinksOverlay(st styles.Styles) OverviewLinksOverlay {
	return OverviewLinksOverlay{
		vp:     viewport.New(0, 0),
		styles: st,
	}
}

// Open populates the overlay from session sources and reviews, then makes it active.
func (m *OverviewLinksOverlay) Open(sources []OverviewSourceItem, reviews []OverviewReviewRow) {
	m.tickets = make([]overviewLinksItem, 0, len(sources))
	for _, s := range sources {
		var label string
		switch {
		case s.Ref != "" && s.Title != "":
			label = s.Ref + " · " + s.Title
		case s.Ref != "":
			label = s.Ref
		case s.Title != "":
			label = s.Title
		default:
			label = "—"
		}
		m.tickets = append(m.tickets, overviewLinksItem{
			label: label,
			meta:  firstNonEmptyString(s.Provider, ""),
			url:   s.URL,
		})
	}

	m.mrs = make([]overviewLinksItem, 0, len(reviews))
	for _, r := range reviews {
		var label string
		if r.Ref != "" {
			label = r.RepoName + " · " + r.Ref
		} else {
			label = r.RepoName
		}
		metaParts := filterEmptyStrings([]string{r.State, r.Branch})
		m.mrs = append(m.mrs, overviewLinksItem{
			label: label,
			meta:  strings.Join(metaParts, " · "),
			url:   r.URL,
		})
	}

	m.cursor = 0
	m.active = true
	m.syncViewport(true)
}

// Close deactivates the overlay and clears its state.
func (m *OverviewLinksOverlay) Close() {
	m.active = false
	m.tickets = nil
	m.mrs = nil
	m.cursor = 0
	m.vp.SetContent("")
	m.vp.GotoTop()
}

// Active reports whether the overlay is currently visible.
func (m OverviewLinksOverlay) Active() bool { return m.active }

// SetSize stores the terminal dimensions and re-syncs the viewport.
func (m *OverviewLinksOverlay) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.syncViewport(false)
}

// items returns all navigable items: tickets first, then MRs.
func (m OverviewLinksOverlay) items() []overviewLinksItem {
	result := make([]overviewLinksItem, 0, len(m.tickets)+len(m.mrs))
	result = append(result, m.tickets...)
	return append(result, m.mrs...)
}

// navigate moves the cursor by delta, clamped to valid range.
func (m *OverviewLinksOverlay) navigate(delta int) {
	all := m.items()
	if len(all) == 0 {
		return
	}
	m.cursor = max(0, min(m.cursor+delta, len(all)-1))
	m.syncViewport(false)
}

// selectedURL returns the URL of the currently focused item, or "" if none.
func (m OverviewLinksOverlay) selectedURL() string {
	all := m.items()
	if len(all) == 0 {
		return ""
	}
	return strings.TrimSpace(all[m.cursor].url)
}

// frameWidth computes the overlay frame width bounded by the terminal width and the max constant.
func (m OverviewLinksOverlay) frameWidth() int {
	if m.width <= 0 {
		return overviewLinksMaxWidth
	}
	return min(max(60, m.width-4), overviewLinksMaxWidth)
}

// innerWidth computes the usable content width inside the overlay frame.
func (m OverviewLinksOverlay) innerWidth() int {
	return styles.DefaultChromeMetrics.OverlayFrame.InnerWidth(m.frameWidth())
}

// syncViewport rebuilds the document and updates the viewport dimensions and content.
// If forceTop is true the viewport scrolls to the top; otherwise it follows the cursor.
func (m *OverviewLinksOverlay) syncViewport(forceTop bool) {
	m.vp.Width = max(1, m.innerWidth())
	// 6 = frame border (2) + title (1) + divider (1) + blank (1) + hint (1)
	m.vp.Height = max(4, m.height-6)

	if m.vp.Width <= 0 {
		return
	}

	doc, cursorLine := m.buildDocument()
	m.vp.SetContent(doc)

	if forceTop {
		m.vp.GotoTop()
	} else if cursorLine >= 0 {
		m.vp.SetYOffset(max(0, cursorLine-m.vp.Height/2))
	}
}

// buildDocument renders the full scrollable content and returns it together with
// the 0-based line index of the cursor item (-1 when there are no items).
func (m OverviewLinksOverlay) buildDocument() (string, int) {
	all := m.items()
	if len(all) == 0 {
		return m.styles.Muted.Render("No links available."), -1
	}

	iw := m.innerWidth()
	var lines []string
	cursorLine := -1

	// Global item index so cursor comparisons are simple.
	globalIdx := 0

	if len(m.tickets) > 0 {
		lines = append(lines, m.styles.SectionLabel.Render("Tickets"))
		for _, t := range m.tickets {
			if globalIdx == m.cursor {
				cursorLine = len(lines)
			}
			lines = append(lines, m.renderItem(t, globalIdx == m.cursor, iw))
			globalIdx++
		}
	}

	if len(m.tickets) > 0 && len(m.mrs) > 0 {
		lines = append(lines, "")
	}

	if len(m.mrs) > 0 {
		lines = append(lines, m.styles.SectionLabel.Render("MRs / PRs"))
		for _, r := range m.mrs {
			if globalIdx == m.cursor {
				cursorLine = len(lines)
			}
			lines = append(lines, m.renderItem(r, globalIdx == m.cursor, iw))
			globalIdx++
		}
	}

	return strings.Join(lines, "\n"), cursorLine
}

// renderItem formats a single list row, truncated to width.
func (m OverviewLinksOverlay) renderItem(item overviewLinksItem, selected bool, width int) string {
	var prefix, label string
	if selected {
		prefix = m.styles.Active.Render("▶ ")
		label = m.styles.Active.Render(item.label)
	} else {
		prefix = "  "
		label = m.styles.SettingsText.Render(item.label)
	}

	line := prefix + label
	if item.meta != "" {
		line += m.styles.Muted.Render("  " + item.meta)
	}

	return ansi.Truncate(line, width, "")
}

// hintText returns the context-sensitive key hint shown at the bottom of the overlay.
func (m OverviewLinksOverlay) hintText() string {
	if m.selectedURL() != "" {
		return "[↑↓] Select  [Enter/o] Open  [Esc] Close"
	}
	return "[↑↓] Select  [Esc] Close"
}

// View renders the overlay frame. Returns "" when not active.
func (m OverviewLinksOverlay) View() string {
	if !m.active {
		return ""
	}

	fw := m.frameWidth()
	iw := m.innerWidth()

	header := []string{
		m.styles.Title.Render("Links"),
		components.RenderOverlayDivider(m.styles, iw),
	}

	hints := m.styles.Hint.Render(truncate(m.hintText(), iw))

	return components.RenderOverlayFrame(m.styles, fw, components.OverlayFrameSpec{
		HeaderLines: header,
		Body:        m.vp.View(),
		Footer:      hints,
		Focused:     true,
	})
}

// Update handles keyboard and mouse input for the overlay.
func (m OverviewLinksOverlay) Update(msg tea.Msg) (OverviewLinksOverlay, tea.Cmd) {
	if !m.active {
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			m.navigate(-1)
		case "down", "j":
			m.navigate(1)
		case keyEnter, "o":
			if url := m.selectedURL(); url != "" {
				return m, func() tea.Msg { return OpenExternalURLMsg{URL: url} }
			}
		case keyEsc:
			return m, func() tea.Msg { return CloseOverlayMsg{} }
		}
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress {
			switch msg.Button {
			case tea.MouseButtonWheelUp:
				m.navigate(-1)
			case tea.MouseButtonWheelDown:
				m.navigate(1)
			}
		}
	}

	return m, nil
}
