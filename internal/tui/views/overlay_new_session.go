package views

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/styles"
)

// SourceKind identifies the work item source.
type SourceKind int

const (
	SourceLinear SourceKind = iota
	SourceManual
)

// selectableItem adapts adapter.ListItem for the bubbles list widget.
type selectableItem struct {
	item     adapter.ListItem
	selected bool
}

func (i selectableItem) Title() string       { return i.item.ID + "  " + i.item.Title }
func (i selectableItem) Description() string { return i.item.State }
func (i selectableItem) FilterValue() string { return i.item.Title + " " + i.item.ID }

// NewSessionOverlay is the overlay for creating a new work item session.
type NewSessionOverlay struct {
	source      SourceKind
	adapters    []adapter.WorkItemAdapter // adapters with CanBrowse=true or manual
	workspaceID string
	// Linear
	filterInput  textinput.Model
	issueList    list.Model
	allItems     []adapter.ListItem
	selectedIDs  map[string]bool
	currentScope domain.SelectionScope
	loading      bool
	// Manual
	manualTitle textinput.Model
	manualDesc  textarea.Model
	manualFocus int // 0=title 1=desc
	// Common
	styles styles.Styles
	width  int
	height int
	active bool
}

// NewNewSessionOverlay constructs a NewSessionOverlay with sane defaults.
func NewNewSessionOverlay(adapters []adapter.WorkItemAdapter, workspaceID string, st styles.Styles) NewSessionOverlay {
	fi := textinput.New()
	fi.Placeholder = "Filter…"
	fi.CharLimit = 200

	mt := textinput.New()
	mt.Placeholder = "Work item title…"
	mt.CharLimit = 200

	md := textarea.New()
	md.Placeholder = "Description (optional)…"
	md.SetWidth(60)
	md.SetHeight(3)
	md.CharLimit = 2000

	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = true
	il := list.New([]list.Item{}, delegate, 60, 10)
	il.Title = "Issues"
	il.SetShowStatusBar(false)
	il.SetFilteringEnabled(true)
	il.SetShowHelp(false)

	return NewSessionOverlay{
		source:       SourceManual,
		adapters:     adapters,
		workspaceID:  workspaceID,
		filterInput:  fi,
		issueList:    il,
		selectedIDs:  make(map[string]bool),
		currentScope: domain.ScopeIssues,
		manualTitle:  mt,
		manualDesc:   md,
		styles:       st,
	}
}

// Open activates the overlay and sets initial focus.
func (m *NewSessionOverlay) Open() {
	m.active = true
	for _, a := range m.adapters {
		if a.Capabilities().CanBrowse {
			m.source = SourceLinear
			break
		}
	}
	if m.source == SourceLinear {
		m.filterInput.Focus()
	} else {
		m.source = SourceManual
		m.manualTitle.Focus()
		m.manualFocus = 0
	}
}

// Close deactivates the overlay and resets all transient state.
func (m *NewSessionOverlay) Close() {
	m.active = false
	m.filterInput.SetValue("")
	m.filterInput.Blur()
	m.manualTitle.SetValue("")
	m.manualDesc.SetValue("")
	m.manualDesc.Blur()
	m.selectedIDs = make(map[string]bool)
}

// Active reports whether the overlay is currently shown.
func (m NewSessionOverlay) Active() bool { return m.active }

// loadItemsCmd fetches items from the first browse-capable adapter.
func (m NewSessionOverlay) loadItemsCmd() tea.Cmd {
	for _, a := range m.adapters {
		if a.Capabilities().CanBrowse {
			return func() tea.Msg {
				result, err := a.ListSelectable(context.Background(), adapter.ListOpts{
					WorkspaceID: m.workspaceID,
					Scope:       m.currentScope,
					Limit:       50,
				})
				if err != nil {
					return ErrMsg{Err: err}
				}
				return issueListLoadedMsg{items: result.Items}
			}
		}
	}
	return nil
}

// issueListLoadedMsg is an internal msg carrying fetched list items.
type issueListLoadedMsg struct{ items []adapter.ListItem }

// Update handles incoming messages for the overlay.
func (m NewSessionOverlay) Update(msg tea.Msg) (NewSessionOverlay, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case issueListLoadedMsg:
		m.loading = false
		m.allItems = msg.items
		items := make([]list.Item, len(msg.items))
		for i, it := range msg.items {
			items[i] = selectableItem{item: it}
		}
		m.issueList.SetItems(items)

	case tea.KeyMsg:
		if m.source == SourceLinear {
			switch msg.String() {
			case "esc":
				return m, func() tea.Msg { return CloseOverlayMsg{} }

			case "tab":
				m.source = SourceManual
				m.filterInput.Blur()
				m.manualTitle.Focus()
				m.manualFocus = 0

			case "enter":
				if len(m.selectedIDs) == 0 {
					if sel, ok := m.issueList.SelectedItem().(selectableItem); ok {
						m.selectedIDs[sel.item.ID] = true
					}
				}
				if len(m.selectedIDs) > 0 {
					ids := make([]string, 0, len(m.selectedIDs))
					for id := range m.selectedIDs {
						ids = append(ids, id)
					}
					for _, a := range m.adapters {
						if a.Capabilities().CanBrowse {
							sel := adapter.Selection{
								Scope:   m.currentScope,
								ItemIDs: ids,
							}
							return m, func() tea.Msg {
								return NewSessionLinearMsg{Adapter: a, Selection: sel}
							}
						}
					}
				}

			case " ":
				if sel, ok := m.issueList.SelectedItem().(selectableItem); ok {
					if m.selectedIDs[sel.item.ID] {
						delete(m.selectedIDs, sel.item.ID)
					} else {
						m.selectedIDs[sel.item.ID] = true
					}
				}

			case "/":
				m.filterInput.Focus()

			default:
				m.issueList, cmd = m.issueList.Update(msg)
				cmds = append(cmds, cmd)
				m.filterInput, cmd = m.filterInput.Update(msg)
				cmds = append(cmds, cmd)
			}
		} else { // SourceManual
			switch msg.String() {
			case "esc":
				return m, func() tea.Msg { return CloseOverlayMsg{} }

			case "tab":
				if m.manualFocus == 0 {
					m.manualTitle.Blur()
					m.manualFocus = 1
					m.manualDesc.Focus()
				} else {
					for _, a := range m.adapters {
						if a.Capabilities().CanBrowse {
							m.source = SourceLinear
							m.manualDesc.Blur()
							m.filterInput.Focus()
							if len(m.allItems) == 0 {
								m.loading = true
								cmds = append(cmds, m.loadItemsCmd())
							}
							break
						}
					}
				}

			case "shift+tab":
				if m.manualFocus == 1 {
					m.manualDesc.Blur()
					m.manualFocus = 0
					m.manualTitle.Focus()
				}

			case "enter":
				if m.manualFocus == 1 || m.manualTitle.Value() != "" {
					title := m.manualTitle.Value()
					if title == "" {
						break
					}
					desc := m.manualDesc.Value()
					for _, a := range m.adapters {
						if a.Name() == "manual" {
							return m, func() tea.Msg {
								return NewSessionManualMsg{Adapter: a, Title: title, Desc: desc}
							}
						}
					}
					// Fallback: use first available adapter.
					if len(m.adapters) > 0 {
						a := m.adapters[0]
						return m, func() tea.Msg {
							return NewSessionManualMsg{Adapter: a, Title: title, Desc: desc}
						}
					}
					// No adapter available — surface as error rather than silent no-op.
					return m, func() tea.Msg { return ErrMsg{Err: fmt.Errorf("no adapter configured")} }
				}

			default:
				if m.manualFocus == 0 {
					m.manualTitle, cmd = m.manualTitle.Update(msg)
				} else {
					m.manualDesc, cmd = m.manualDesc.Update(msg)
				}
				cmds = append(cmds, cmd)
			}
		}
	}

	return m, tea.Batch(cmds...)
}

// View renders the overlay, or empty string when inactive.
func (m NewSessionOverlay) View() string {
	if !m.active {
		return ""
	}

	w := 72
	if m.width > 0 && m.width < 80 {
		w = m.width - 4
	}

	var linearLabel, manualLabel string
	if m.source == SourceLinear {
		linearLabel = lipgloss.NewStyle().Foreground(lipgloss.Color("#5b8def")).Bold(true).Render("[► Linear ◄]")
		manualLabel = lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render("Manual")
	} else {
		linearLabel = lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render("Linear")
		manualLabel = lipgloss.NewStyle().Foreground(lipgloss.Color("#5b8def")).Bold(true).Render("[► Manual ◄]")
	}
	sourceRow := "Source: " + linearLabel + "  " + manualLabel

	var body string
	if m.source == SourceLinear {
		body = m.linearView(w)
	} else {
		body = m.manualView(w)
	}

	content := lipgloss.NewStyle().Foreground(lipgloss.Color("#f0f0f0")).Bold(true).Render("New Session") + "\n\n" +
		sourceRow + "\n\n" + body

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#2d2d44")).
		Background(lipgloss.Color("#1a1a2e")).
		Padding(1, 2).
		Width(w)

	return boxStyle.Render(content)
}

func (m NewSessionOverlay) linearView(w int) string {
	var lines []string
	filterRow := lipgloss.NewStyle().Foreground(lipgloss.Color("#b0b0b0")).Render("Filter: ") + m.filterInput.View()
	lines = append(lines, filterRow)
	lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("#2d2d44")).Render(strings.Repeat("─", w-4)))
	if m.loading {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render("Loading…"))
	} else {
		m.issueList.SetWidth(w - 4)
		lines = append(lines, m.issueList.View())
	}
	hints := lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render(
		"[Enter] Start  [Space] Select  [Tab] Switch source  [Esc] Cancel")
	lines = append(lines, hints)
	return strings.Join(lines, "\n")
}

func (m NewSessionOverlay) manualView(_ int) string {
	titleLabel := lipgloss.NewStyle().Foreground(lipgloss.Color("#b0b0b0")).Render("Title:       ")
	descLabel := lipgloss.NewStyle().Foreground(lipgloss.Color("#b0b0b0")).Render("Description: ")
	hints := lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render(
		"[Tab] Next field  [Enter] Start  [Esc] Cancel")
	return strings.Join([]string{
		titleLabel + m.manualTitle.View(),
		descLabel + m.manualDesc.View(),
		"",
		hints,
	}, "\n")
}

// SetSize updates the overlay dimensions and propagates to sub-widgets.
func (m *NewSessionOverlay) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.issueList.SetWidth(w - 8)
	m.issueList.SetHeight(h / 2)
}
