package views

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

// ArtifactsModel is an accordion list view for PR/MR artifacts.
type ArtifactsModel struct { //nolint:recvcheck // Bubble Tea: Update returns value, View on value receiver
	styles      styles.Styles
	items       []ArtifactItem
	cursor      int
	expandedSet map[int]bool
	viewport    viewport.Model
	width       int
	height      int
}

// NewArtifactsModel creates a new artifacts accordion model.
func NewArtifactsModel(st styles.Styles) ArtifactsModel {
	return ArtifactsModel{
		styles:      st,
		cursor:      -1,
		expandedSet: make(map[int]bool),
	}
}

// SetSize updates the available dimensions and syncs the viewport.
func (m *ArtifactsModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.syncViewport()
}

// SetItems replaces the current item list and resets cursor state.
func (m *ArtifactsModel) SetItems(items []ArtifactItem) {
	m.items = items
	m.expandedSet = make(map[int]bool)
	if len(items) > 0 {
		m.cursor = 0
	} else {
		m.cursor = -1
	}
	m.syncViewport()
}

// syncViewport rebuilds the viewport content from the current state.
// Must be called from pointer-receiver methods (SetSize, SetItems) and
// at the end of Update after any state change. The viewport survives
// across frames only when set via pointer receiver or Update return value.
func (m *ArtifactsModel) syncViewport() {
	m.viewport.Width = m.width
	headerLines := m.headerLineCount()
	vpHeight := m.height - headerLines
	if vpHeight < 1 {
		vpHeight = 1
	}
	m.viewport.Height = vpHeight
	m.viewport.SetContent(m.buildBody())
}

func (m ArtifactsModel) headerLineCount() int {
	if m.width <= 0 {
		return 0
	}
	header := m.renderHeader()
	return strings.Count(header, "\n") + 1
}

func (m ArtifactsModel) renderHeader() string {
	return components.RenderHeaderBlock(m.styles, components.HeaderBlockSpec{
		Title:   "Artifacts",
		Meta:    "Pull requests and merge requests",
		Width:   m.width,
		Divider: true,
	})
}

func (m ArtifactsModel) buildBody() string {
	switch {
	case len(m.items) == 0:
		return m.styles.Muted.Render("No artifacts")
	case len(m.items) == 1:
		return m.renderExpandedCard(0)
	default:
		return m.renderAccordion()
	}
}

// Update handles key and mouse input.
func (m ArtifactsModel) Update(msg tea.Msg) (ArtifactsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if len(m.items) == 0 {
			return m, nil
		}
		changed := false
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
				changed = true
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
				changed = true
			}
		case "right", "l":
			if !m.expandedSet[m.cursor] {
				m.expandedSet[m.cursor] = true
				changed = true
			}
		case " ":
			m.expandedSet[m.cursor] = !m.expandedSet[m.cursor]
			changed = true
		case "o":
			if url := m.items[m.cursor].URL; url != "" {
				return m, func() tea.Msg { return OpenExternalURLMsg{URL: url} }
			}
		}
		if changed {
			m.syncViewport()
			m.ensureCursorVisible()
		}

	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress {
			switch msg.Button {
			case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
				var cmd tea.Cmd
				m.viewport, cmd = m.viewport.Update(msg)
				return m, cmd
			}
		}
	}

	return m, nil
}

// ensureCursorVisible scrolls the viewport so the focused item row is visible.
func (m *ArtifactsModel) ensureCursorVisible() {
	if m.cursor < 0 || len(m.items) == 0 {
		return
	}
	// Count lines before the cursor row to find its position in the document.
	linesBefore := 0
	for i := 0; i < m.cursor; i++ {
		linesBefore++ // collapsed row
		if m.expandedSet[i] {
			linesBefore += strings.Count(m.renderExpandedCard(i), "\n") + 1
		}
	}
	// The cursor row is at linesBefore. Ensure it's within the viewport.
	if linesBefore < m.viewport.YOffset {
		m.viewport.SetYOffset(linesBefore)
	} else if linesBefore >= m.viewport.YOffset+m.viewport.Height {
		m.viewport.SetYOffset(linesBefore - m.viewport.Height + 1)
	}
}

// View renders the accordion list inside a scrollable viewport.
func (m ArtifactsModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	header := m.renderHeader()
	return fitViewBox(header+"\n"+m.viewport.View(), m.width, m.height)
}

// KeybindHints returns the status bar hints for the artifacts view.
func (m ArtifactsModel) KeybindHints() []KeybindHint {
	if len(m.items) == 0 {
		return nil
	}
	hints := []KeybindHint{{Key: "↑↓", Label: "Navigate"}}
	if len(m.items) > 1 {
		hints = append(hints, KeybindHint{Key: "Space", Label: "Expand/collapse"})
	}
	if m.cursor >= 0 && m.cursor < len(m.items) && m.items[m.cursor].URL != "" {
		hints = append(hints, KeybindHint{Key: "o", Label: "Open in browser"})
	}
	return hints
}

func (m ArtifactsModel) renderAccordion() string {
	var rows []string
	for i, item := range m.items {
		rows = append(rows, m.renderCollapsedRow(i, item))
		if m.expandedSet[i] {
			rows = append(rows, m.renderExpandedCard(i))
		}
	}
	return strings.Join(rows, "\n")
}

func (m ArtifactsModel) renderCollapsedRow(idx int, item ArtifactItem) string {
	stateTag := item.State
	if item.Draft && stateTag != "merged" && stateTag != "closed" {
		stateTag = "draft"
	}
	line := fmt.Sprintf("  %s  %s  %s  [%s]", item.Ref, item.RepoName, item.Branch, stateTag)
	line = ansi.Truncate(line, m.width, "…")

	if idx == m.cursor {
		return m.styles.SidebarSelected.Width(m.width).Render(line)
	}
	return m.styles.SettingsText.Render(line)
}

func (m ArtifactsModel) renderExpandedCard(idx int) string {
	item := m.items[idx]
	innerWidth := components.CalloutInnerWidth(m.styles, m.width)

	rows := []string{
		renderKeyValueLine(m.styles, innerWidth, "Kind", item.Kind),
		renderKeyValueLine(m.styles, innerWidth, "Repo", item.RepoName),
		renderKeyValueLine(m.styles, innerWidth, "Ref", item.Ref),
		renderKeyValueLine(m.styles, innerWidth, "Branch", item.Branch),
		renderKeyValueLine(m.styles, innerWidth, "State", item.State),
	}
	if item.URL != "" {
		rows = append(rows, renderKeyValueLine(m.styles, innerWidth, "URL", item.URL))
	}
	if !item.CreatedAt.IsZero() {
		rows = append(rows, renderKeyValueLine(m.styles, innerWidth, "Created", formatAbsoluteTime(item.CreatedAt)))
	}
	if !item.UpdatedAt.IsZero() {
		rows = append(rows, renderKeyValueLine(m.styles, innerWidth, "Updated", formatAbsoluteTime(item.UpdatedAt)))
	}
	if item.MergedAt != nil {
		rows = append(rows, renderKeyValueLine(m.styles, innerWidth, "Merged", formatAbsoluteTime(*item.MergedAt)))
	}

	return components.RenderCallout(m.styles, components.CalloutSpec{
		Body:    strings.Join(rows, "\n"),
		Width:   m.width,
		Variant: components.CalloutCard,
	})
}
