package views

import (
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

const (
	sourceItemsWindowWidth    = 160
	sourceItemsPaneMinWidth   = 36
	sourceItemsDetailMinWidth = 48
)

var sourceItemsSizingSpec = components.SplitOverlaySizingSpec{
	MaxOverlayWidth:   sourceItemsWindowWidth,
	LeftMinWidth:      sourceItemsPaneMinWidth,
	RightMinWidth:     sourceItemsDetailMinWidth,
	LeftWeight:        2,
	RightWeight:       3,
	MinBodyHeight:     8,
	DefaultBodyHeight: 18,
	HeightRatioNum:    3,
	HeightRatioDen:    5,
	InputWidthOffset:  0, // no search bar
}

type sourceItemsFocus int

const (
	sourceItemsFocusList sourceItemsFocus = iota
	sourceItemsFocusPreview
)

// sourceItemsListItem wraps a SourceSummary for the list.Model.
type sourceItemsListItem struct {
	item     domain.SourceSummary
	index    int
	disabled bool // true when item has no URL (cannot be opened/selected)
	selected bool // multi-select state
}

func (i sourceItemsListItem) Title() string {
	return renderSourceItemHeading(i.item, i.index)
}

func (i sourceItemsListItem) Description() string {
	var parts []string
	if provider := detailProviderLabel(i.item.Provider); provider != "" {
		parts = append(parts, provider)
	}
	if i.item.State != "" {
		parts = append(parts, i.item.State)
	}
	if i.disabled {
		parts = append(parts, "no URL")
	}
	if i.selected {
		parts = append(parts, "✓ selected")
	}
	return strings.Join(parts, " · ")
}

func (i sourceItemsListItem) FilterValue() string {
	return i.item.Ref + " " + i.item.Title
}

// SourceItemsOverlay renders a split-pane overlay listing source items for a session.
// Items without URLs are shown in a disabled state and cannot be selected or opened.
type SourceItemsOverlay struct { //nolint:recvcheck // Bubble Tea: Update returns value, View on value receiver
	active bool
	items  []domain.SourceSummary
	list   list.Model
	detail viewport.Model
	focus  sourceItemsFocus
	styles styles.Styles
	width  int
	height int
}

func NewSourceItemsOverlay(st styles.Styles) SourceItemsOverlay {
	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = true
	resultList := list.New([]list.Item{}, delegate, 60, 12)
	resultList.Title = "Source Items"
	resultList.SetShowStatusBar(false)
	resultList.SetFilteringEnabled(false)
	resultList.SetShowHelp(false)
	resultList = components.ApplyOverlayListStyles(resultList, st)

	return SourceItemsOverlay{
		list:   resultList,
		detail: viewport.New(0, 0),
		focus:  sourceItemsFocusList,
		styles: st,
	}
}

func (m *SourceItemsOverlay) Open(items []domain.SourceSummary) {
	m.active = true
	m.items = append([]domain.SourceSummary(nil), items...)
	m.focus = sourceItemsFocusList

	listItems := make([]list.Item, 0, len(items))
	for i, item := range items {
		listItems = append(listItems, sourceItemsListItem{
			item:     item,
			index:    i,
			disabled: strings.TrimSpace(item.URL) == "",
		})
	}
	m.list.SetItems(listItems)
	if len(listItems) > 0 {
		m.list.Select(0)
	}
	m.syncDetailViewport(true)
}

func (m *SourceItemsOverlay) Close() {
	m.active = false
	m.items = nil
	m.focus = sourceItemsFocusList
	m.list.ResetSelected()
	m.list.SetItems(nil)
	m.detail.SetContent("")
	m.detail.GotoTop()
}

func (m SourceItemsOverlay) Active() bool { return m.active }

func (m *SourceItemsOverlay) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.syncDetailViewport(false)
}

func (m SourceItemsOverlay) selectedItem() *sourceItemsListItem {
	item, ok := m.list.SelectedItem().(sourceItemsListItem)
	if !ok {
		return nil
	}
	return &item
}

// selectedURLs returns the URLs of all multi-selected items.
// If no items are explicitly selected, returns the current item's URL (if any).
func (m SourceItemsOverlay) selectedURLs() []string {
	var urls []string
	for _, li := range m.list.Items() {
		si, ok := li.(sourceItemsListItem)
		if ok && si.selected && strings.TrimSpace(si.item.URL) != "" {
			urls = append(urls, si.item.URL)
		}
	}
	if len(urls) > 0 {
		return urls
	}
	// Fallback: open the cursor item if no explicit multi-selection.
	if item := m.selectedItem(); item != nil && !item.disabled {
		if url := strings.TrimSpace(item.item.URL); url != "" {
			return []string{url}
		}
	}
	return nil
}

func (m SourceItemsOverlay) hasMultiSelection() bool {
	for _, li := range m.list.Items() {
		if si, ok := li.(sourceItemsListItem); ok && si.selected {
			return true
		}
	}
	return false
}

func (m *SourceItemsOverlay) toggleSelection() {
	item := m.selectedItem()
	if item == nil || item.disabled {
		return
	}
	// Toggle the selected state in the list items slice.
	items := m.list.Items()
	idx := m.list.Index()
	if idx < 0 || idx >= len(items) {
		return
	}
	toggled := *item
	toggled.selected = !toggled.selected
	items[idx] = toggled
	m.list.SetItems(items)
	m.list.Select(idx)
}

func (m *SourceItemsOverlay) focusList() {
	m.focus = sourceItemsFocusList
}

func (m *SourceItemsOverlay) focusPreview() bool {
	if len(m.items) == 0 {
		return false
	}
	m.focus = sourceItemsFocusPreview
	return true
}

func (m *SourceItemsOverlay) cycleFocus() {
	switch m.focus {
	case sourceItemsFocusList:
		m.focusPreview()
	default:
		m.focusList()
	}
}

func (m *SourceItemsOverlay) moveFocusLeft() bool {
	if m.focus == sourceItemsFocusPreview {
		m.focusList()
		return true
	}
	return false
}

func (m *SourceItemsOverlay) moveFocusRight() bool {
	if m.focus == sourceItemsFocusList {
		return m.focusPreview()
	}
	return false
}

func (m SourceItemsOverlay) chromeLines() int {
	return 8 // outer border + title + divider + blank + footer + spacing
}

func (m SourceItemsOverlay) layout() components.SplitOverlayLayout {
	return components.ComputeSplitOverlayLayout(m.width, m.height, m.chromeLines(), sourceItemsSizingSpec)
}

func (m *SourceItemsOverlay) syncDetailViewport(forceTop bool) {
	m.syncDetailViewportWithLayout(m.layout(), forceTop)
}

func (m *SourceItemsOverlay) syncDetailViewportWithLayout(layout components.SplitOverlayLayout, forceTop bool) {
	m.detail.Width = layout.ViewportWidth
	m.detail.Height = layout.ViewportHeight
	content := ansi.Hardwrap(m.detailContent(), layout.ViewportWidth, true)
	m.detail.SetContent(content)
	if forceTop {
		m.detail.GotoTop()
	}
}

func (m SourceItemsOverlay) detailContent() string {
	item := m.selectedItem()
	if item == nil {
		return "No source items available."
	}

	innerWidth := m.layout().ViewportWidth
	if innerWidth <= 0 {
		innerWidth = 60
	}

	var sections []string

	heading := renderSourceItemHeading(item.item, item.index)
	sections = append(sections, heading, "")

	if metadata := renderSourceItemMetadata(m.styles, item.item, innerWidth); metadata != "" {
		sections = append(sections, metadata)
	}

	if markdown := sourceItemMarkdown(item.item); markdown != "" {
		sections = append(sections, "", "Description:", renderMarkdownDocument(markdown, innerWidth))
	}

	if item.disabled {
		sections = append(sections, "", m.styles.Muted.Render("No URL available — cannot open in browser."))
	} else {
		hint := "Press Enter or o to open in browser."
		if m.hasMultiSelection() {
			hint = "Press Enter or o to open all selected items."
		}
		sections = append(sections, "", hint)
	}

	return strings.Join(sections, "\n")
}

func (m SourceItemsOverlay) hintText() string {
	parts := []string{"[↑↓] Select", "[←→] Focus", "[Space] Toggle select"}
	if m.hasMultiSelection() {
		parts = append(parts, "[Enter/o] Open selected")
	} else {
		parts = append(parts, "[Enter/o] Open")
	}
	parts = append(parts, "[Esc] Close")
	return strings.Join(parts, "  ")
}

func (m SourceItemsOverlay) View() string {
	if !m.active {
		return ""
	}

	layout := m.layout()
	renderWidth := max(1, layout.ContentWidth-4)
	m.list.SetWidth(layout.LeftInnerWidth)
	m.list.SetHeight(layout.ListHeight)
	m.syncDetailViewportWithLayout(layout, false)

	title := m.styles.Title.Render("Source Items")
	header := []string{
		title,
		components.RenderOverlayDivider(m.styles, renderWidth),
	}

	leftContent := m.list.View()

	body := components.RenderSplitOverlayBody(m.styles, layout, components.SplitOverlaySpec{
		LeftPane: components.OverlayPaneSpec{
			Body:    leftContent,
			Focused: m.focus == sourceItemsFocusList,
		},
		RightPane: components.OverlayPaneSpec{
			Title:   "Preview",
			Body:    m.detail.View(),
			Focused: m.focus == sourceItemsFocusPreview,
		},
	})

	hints := m.styles.Hint.Render(truncate(m.hintText(), renderWidth))

	return components.RenderOverlayFrame(m.styles, layout.FrameWidth, components.OverlayFrameSpec{
		HeaderLines: header,
		Body:        body,
		Footer:      hints,
		Focused:     true,
	})
}

func (m SourceItemsOverlay) openSelectedCmd() tea.Cmd {
	urls := m.selectedURLs()
	if len(urls) == 0 {
		return nil
	}
	return func() tea.Msg {
		return openSourceItemURLsMsg{URLs: urls}
	}
}

func (m SourceItemsOverlay) Update(msg tea.Msg) (SourceItemsOverlay, tea.Cmd) {
	if !m.active {
		return m, nil
	}

	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress {
			switch msg.Button {
			case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
				switch m.focus {
				case sourceItemsFocusList:
					before := m.list.Index()
					m.list, cmd = m.list.Update(msg)
					if m.list.Index() != before {
						m.syncDetailViewport(true)
					}
					return m, cmd
				case sourceItemsFocusPreview:
					m.detail, cmd = m.detail.Update(msg)
					return m, cmd
				}
			}
		}
	case tea.KeyMsg:
		switch msg.String() {
		case keyEsc:
			return m, func() tea.Msg { return CloseOverlayMsg{} }
		case keyTab:
			m.cycleFocus()
			return m, nil
		case "shift+tab":
			if m.moveFocusLeft() {
				return m, nil
			}
		case panelLeft:
			if m.moveFocusLeft() {
				return m, nil
			}
		case panelRight:
			if m.moveFocusRight() {
				return m, nil
			}
		case "up":
			if m.focus == sourceItemsFocusPreview {
				m.focusList()
				return m, nil
			}
		case keyDown:
			// down in preview: stay in preview (viewport scrolls below)
		case " ":
			m.toggleSelection()
			m.syncDetailViewport(false)
			return m, nil
		case keyEnter, "o":
			if openCmd := m.openSelectedCmd(); openCmd != nil {
				return m, openCmd
			}
			return m, nil
		}

		switch m.focus {
		case sourceItemsFocusList:
			before := m.list.Index()
			m.list, cmd = m.list.Update(msg)
			if m.list.Index() != before {
				m.syncDetailViewport(true)
			}
			return m, cmd
		case sourceItemsFocusPreview:
			m.detail, cmd = m.detail.Update(msg)
			return m, cmd
		}
	}

	return m, nil
}
