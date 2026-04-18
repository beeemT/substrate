package views

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

const sourceTypeIssue = "issue"

// SourceDetailsModel renders source-system details for the selected session.
type sourceDetailsNotice struct {
	Title   string
	Body    string
	Hint    string
	Variant components.CalloutVariant
}

type SourceDetailsModel struct { //nolint:recvcheck // Bubble Tea: Update returns value, View on value receiver
	viewport    viewport.Model
	session     *domain.Session
	notice      *sourceDetailsNotice
	styles      styles.Styles
	items       []domain.SourceSummary
	cursor      int
	expandedSet map[int]bool
	width       int
	height      int
}

func NewSourceDetailsModel(st styles.Styles) SourceDetailsModel {
	return SourceDetailsModel{
		styles:      st,
		cursor:      -1,
		expandedSet: make(map[int]bool),
	}
}

func (m *SourceDetailsModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.syncViewport(false)
}

func (m *SourceDetailsModel) SetSession(session *domain.Session) {
	reset := m.session == nil || session == nil || m.session.ID != session.ID
	m.session = session
	m.items = sessionSourceItems(session)
	if reset {
		m.expandedSet = make(map[int]bool)
		if len(m.items) > 0 {
			m.cursor = 0
		} else {
			m.cursor = -1
		}
	} else {
		// Preserve cursor, but clamp.
		if m.cursor >= len(m.items) {
			m.cursor = max(0, len(m.items)-1)
		}
		if len(m.items) == 0 {
			m.cursor = -1
		}
	}
	m.syncViewport(reset)
}

func (m *SourceDetailsModel) SetNotice(notice *sourceDetailsNotice) {
	m.notice = notice
	m.syncViewport(false)
}

func (m SourceDetailsModel) KeybindHints() []KeybindHint {
	if m.session == nil {
		return nil
	}
	hints := []KeybindHint{{Key: "↑↓", Label: "Navigate"}}
	if m.notice != nil {
		hints = append(hints, KeybindHint{Key: "Enter", Label: "Open overview"})
	}
	if len(m.items) > 1 {
		hints = append(hints, KeybindHint{Key: "Space", Label: "Expand/collapse"})
	}
	if m.focusedItemHasURL() {
		hints = append(hints, KeybindHint{Key: "o", Label: "Open in browser"})
	}
	return hints
}

func (m SourceDetailsModel) Update(msg tea.Msg) (SourceDetailsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		changed := false
		switch msg.String() {
		case "up", "k":
			if len(m.items) > 1 && m.cursor > 0 {
				m.cursor--
				changed = true
			} else if len(m.items) <= 1 {
				// No items or single-item: scroll viewport.
				var cmd tea.Cmd
				m.viewport, cmd = m.viewport.Update(msg)
				return m, cmd
			}
		case "down", "j":
			if len(m.items) > 1 && m.cursor < len(m.items)-1 {
				m.cursor++
				changed = true
			} else if len(m.items) <= 1 {
				var cmd tea.Cmd
				m.viewport, cmd = m.viewport.Update(msg)
				return m, cmd
			}
		case "right", "l":
			if len(m.items) > 1 && m.cursor >= 0 && !m.expandedSet[m.cursor] {
				m.expandedSet[m.cursor] = true
				changed = true
			}
		case " ":
			if len(m.items) > 1 && m.cursor >= 0 {
				m.expandedSet[m.cursor] = !m.expandedSet[m.cursor]
				changed = true
			}
		case "o":
			if cmd := m.openFocusedItemCmd(); cmd != nil {
				return m, cmd
			}
		case "pgup", "pgdown", "home", "end":
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
		if changed {
			m.syncViewport(false)
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
	case tea.WindowSizeMsg:
		m.syncViewport(false)
	}
	return m, nil
}

func (m SourceDetailsModel) View() string {
	if m.session == nil || m.width <= 0 || m.height <= 0 {
		return ""
	}
	header := m.header()
	return fitViewBox(header+"\n"+m.viewport.View(), m.width, m.height)
}

func (m SourceDetailsModel) header() string {
	if m.session == nil {
		return ""
	}
	header := components.RenderHeaderBlock(m.styles, components.HeaderBlockSpec{
		Title:   m.session.Title,
		Meta:    "Source details",
		Width:   m.width,
		Divider: true,
	})
	if notice := m.noticeView(); notice != "" {
		return header + "\n" + notice
	}
	return header
}

func (m SourceDetailsModel) noticeView() string {
	return renderTaskViewNotice(m.styles, m.width, m.notice)
}

func (m *SourceDetailsModel) syncViewport(reset bool) {
	if m.session == nil || m.width <= 0 || m.height <= 0 {
		return
	}
	headerHeight := strings.Count(m.header(), "\n") + 1
	m.viewport.Width = m.width
	m.viewport.Height = max(1, m.height-headerHeight)
	m.viewport.SetContent(m.buildBody())
	if reset {
		m.viewport.GotoTop()
	}
}

func (m SourceDetailsModel) buildBody() string {
	if m.session == nil || m.width <= 0 {
		return ""
	}
	st := m.styles
	sections := []string{
		st.SectionLabel.Render("Summary"),
		components.RenderCallout(st, components.CalloutSpec{
			Body:  renderSourceSummaryBody(st, m.session, components.CalloutInnerWidth(st, m.width)),
			Width: m.width,
		}),
	}
	if workItem := renderAggregateWorkItemBody(m.session, m.width); workItem != "" {
		sections = append(sections,
			st.SectionLabel.Render("Work item"),
			workItem,
		)
	}
	if itemsContent := m.renderSourceItems(); itemsContent != "" {
		sections = append(sections,
			st.SectionLabel.Render("Selected items"),
			itemsContent,
		)
	}
	return strings.Join(sections, "\n\n")
}

// renderSourceItems builds the selected-items section content.
// Single item: rendered inline (no accordion chrome).
// Multiple items: accordion with cursor and expand/collapse.
// No items: falls back to tracker refs or source IDs.
func (m SourceDetailsModel) renderSourceItems() string {
	switch {
	case len(m.items) == 0:
		return m.renderSourceItemsFallback()
	case len(m.items) == 1:
		return renderSourceItemBlock(m.styles, m.items[0], 0, m.width)
	default:
		return m.renderAccordion()
	}
}

func (m SourceDetailsModel) renderAccordion() string {
	var rows []string
	for i, item := range m.items {
		rows = append(rows, m.renderCollapsedRow(i, item))
		if m.expandedSet[i] {
			rows = append(rows, renderSourceItemBlock(m.styles, item, i, m.width))
		}
	}
	return strings.Join(rows, "\n")
}

func (m SourceDetailsModel) renderCollapsedRow(idx int, item domain.SourceSummary) string {
	heading := renderSourceItemHeading(item, idx)
	state := strings.TrimSpace(item.State)
	var line string
	if state != "" {
		line = fmt.Sprintf("  %s  [%s]", heading, state)
	} else {
		line = "  " + heading
	}
	line = ansi.Truncate(line, m.width, "…")
	if idx == m.cursor {
		return m.styles.SidebarSelected.Width(m.width).Render(line)
	}
	return m.styles.SettingsText.Render(line)
}

// renderSourceItemsFallback handles the case where no durable source summaries
// exist — renders tracker refs or raw source IDs as a simple list.
func (m SourceDetailsModel) renderSourceItemsFallback() string {
	st := m.styles
	refs := sessionTrackerRefs(m.session.Metadata)
	rows := make([]string, 0, max(len(refs), len(m.session.SourceItemIDs)))
	for _, ref := range refs {
		rows = append(rows, ansi.Hardwrap(st.SettingsText.Render("• "+formatTrackerRef(ref)), m.width, true))
	}
	if len(rows) == 0 {
		for _, id := range m.session.SourceItemIDs {
			if strings.TrimSpace(id) == "" {
				continue
			}
			rows = append(rows, ansi.Hardwrap(st.SettingsText.Render("• "+id), m.width, true))
		}
	}
	if len(rows) == 0 {
		return ""
	}
	return strings.Join(rows, "\n")
}

// ensureCursorVisible scrolls the viewport so the focused item row is visible.
func (m *SourceDetailsModel) ensureCursorVisible() {
	if m.cursor < 0 || len(m.items) <= 1 {
		return
	}
	// Count lines before the cursor to find its offset in the body.
	// The body starts after Summary, optional Work item, and "Selected items" label.
	// Rather than computing section sizes, scan the body text for cursor row position.
	body := m.buildBody()
	bodyLines := strings.Split(body, "\n")

	// Find the cursor's collapsed row in the rendered body.
	cursorLine := m.renderCollapsedRow(m.cursor, m.items[m.cursor])
	plainCursor := ansi.Strip(cursorLine)
	lineIdx := -1
	for i, bl := range bodyLines {
		if ansi.Strip(bl) == plainCursor {
			lineIdx = i
			break
		}
	}
	if lineIdx < 0 {
		return
	}
	if lineIdx < m.viewport.YOffset {
		m.viewport.SetYOffset(lineIdx)
	} else if lineIdx >= m.viewport.YOffset+m.viewport.Height {
		m.viewport.SetYOffset(lineIdx - m.viewport.Height + 1)
	}
}

// focusedItemHasURL reports whether the focused source item has a URL.
func (m SourceDetailsModel) focusedItemHasURL() bool {
	if len(m.items) == 0 {
		return false
	}
	// Single item: always check it.
	if len(m.items) == 1 {
		return strings.TrimSpace(m.items[0].URL) != ""
	}
	if m.cursor >= 0 && m.cursor < len(m.items) {
		return strings.TrimSpace(m.items[m.cursor].URL) != ""
	}
	return false
}

// openFocusedItemCmd returns a command to open the focused item's URL.
func (m SourceDetailsModel) openFocusedItemCmd() tea.Cmd {
	if len(m.items) == 0 {
		return nil
	}
	// Single item: open directly if it has a URL.
	if len(m.items) == 1 {
		if url := strings.TrimSpace(m.items[0].URL); url != "" {
			return func() tea.Msg { return OpenExternalURLMsg{URL: url} }
		}
		return nil
	}
	// Multiple items: open the focused item's URL directly.
	if m.cursor >= 0 && m.cursor < len(m.items) {
		if url := strings.TrimSpace(m.items[m.cursor].URL); url != "" {
			return func() tea.Msg { return OpenExternalURLMsg{URL: url} }
		}
	}
	return nil
}

func renderTaskViewNotice(st styles.Styles, width int, notice *sourceDetailsNotice) string {
	if notice == nil || width <= 0 {
		return ""
	}
	innerWidth := components.CalloutInnerWidth(st, width)
	lines := []string{ansi.Hardwrap(st.Title.Render(notice.Title), innerWidth, true)}
	if body := strings.TrimSpace(notice.Body); body != "" {
		lines = append(lines, "", ansi.Hardwrap(st.SettingsText.Render(body), innerWidth, true))
	}
	if hint := strings.TrimSpace(notice.Hint); hint != "" {
		lines = append(lines, "", ansi.Hardwrap(st.Muted.Render(hint), innerWidth, true))
	}
	return components.RenderCallout(st, components.CalloutSpec{
		Body:    strings.Join(filterEmptyStringsPreserveBlanks(lines), "\n"),
		Width:   width,
		Variant: notice.Variant,
	})
}

func renderSourceSummaryBody(st styles.Styles, session *domain.Session, width int) string {
	labelStyle := st.SectionLabel
	valueStyle := st.SettingsText
	rows := make([]string, 0, 5)
	add := func(label, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		rows = append(rows, ansi.Hardwrap(labelStyle.Render(label+": ")+valueStyle.Render(value), width, true))
	}
	add("Provider", detailProviderLabel(session.Source))
	add("Selected", sessionSourceSelectionSummary(session))
	if containers := sessionContainers(session); len(containers) > 0 {
		label := "Container"
		if len(containers) > 1 {
			label = "Containers"
		}
		add(label, strings.Join(containers, ", "))
	}
	if sessionSourceCount(session) <= 1 {
		add("State", sessionExternalState(session))
		if len(session.Labels) > 0 {
			add("Labels", strings.Join(session.Labels, ", "))
		}
	}
	if len(rows) == 0 {
		return st.Muted.Render("No source summary available.")
	}
	return strings.Join(rows, "\n")
}

func renderAggregateWorkItemBody(session *domain.Session, width int) string {
	if session == nil || sessionSourceCount(session) <= 1 {
		return ""
	}
	return renderMarkdownDocument(strings.TrimSpace(session.Description), width)
}

func renderSourceItemBlock(st styles.Styles, item domain.SourceSummary, index, width int) string {
	sections := []string{st.SectionLabel.Render(renderSourceItemHeading(item, index))}
	if metadata := renderSourceItemMetadata(st, item, components.CalloutInnerWidth(st, width)); metadata != "" {
		sections = append(sections, components.RenderCallout(st, components.CalloutSpec{Body: metadata, Width: width, Variant: components.CalloutCard}))
	}
	if markdown := sourceItemMarkdown(item); markdown != "" {
		sections = append(sections, st.SectionLabel.Render("Description"), renderMarkdownDocument(markdown, width))
	} else {
		sections = append(sections, st.Muted.Render("No description captured."))
	}

	return strings.Join(sections, "\n\n")
}

func renderSourceItemHeading(item domain.SourceSummary, index int) string {
	ref := strings.TrimSpace(item.Ref)
	title := strings.TrimSpace(item.Title)
	switch {
	case ref != "" && title != "":
		return ref + " · " + title
	case title != "":
		return title
	case ref != "":
		return ref
	default:
		return fmt.Sprintf("Source item %d", index+1)
	}
}

func renderSourceItemMetadata(st styles.Styles, item domain.SourceSummary, width int) string {
	labelStyle := st.SectionLabel
	valueStyle := st.SettingsText
	linkStyle := st.Link
	rows := make([]string, 0, 8+len(item.Metadata))
	add := func(label, value string, link bool) {
		if strings.TrimSpace(value) == "" {
			return
		}
		style := valueStyle
		if link {
			style = linkStyle
		}
		rows = append(rows, ansi.Hardwrap(labelStyle.Render(label+": ")+style.Render(value), width, true))
	}

	add("Provider", detailProviderLabel(item.Provider), false)
	if strings.TrimSpace(item.Kind) != "" {
		add("Type", trackerRefKindLabel(item.Kind), false)
	}
	add("Ref", item.Ref, false)
	add("State", item.State, false)
	add("Container", item.Container, false)
	if len(item.Labels) > 0 {
		add("Labels", strings.Join(item.Labels, ", "), false)
	}
	if item.CreatedAt != nil && !item.CreatedAt.IsZero() {
		add("Created", item.CreatedAt.Local().Format("2006-01-02 15:04"), false)
	}
	if item.UpdatedAt != nil && !item.UpdatedAt.IsZero() {
		add("Updated", item.UpdatedAt.Local().Format("2006-01-02 15:04"), false)
	}
	add("URL", item.URL, true)
	for _, field := range item.Metadata {
		value := strings.TrimSpace(field.Value)
		add(field.Label, value, strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://"))
	}
	if len(rows) == 0 {
		return st.Muted.Render("No metadata available.")
	}

	return strings.Join(rows, "\n")
}

func sourceItemMarkdown(item domain.SourceSummary) string {
	if strings.TrimSpace(item.Description) != "" {
		return item.Description
	}

	return strings.TrimSpace(item.Excerpt)
}

func sessionSourceItems(session *domain.Session) []domain.SourceSummary {
	if session == nil {
		return nil
	}
	summaries := sessionSourceSummaries(session.Metadata)
	if len(summaries) > 0 {
		items := make([]domain.SourceSummary, len(summaries))
		for i, summary := range summaries {
			items[i] = hydrateSourceSummary(session, summary, i, len(summaries))
		}

		return items
	}
	if sessionSourceCount(session) != 1 {
		return nil
	}

	return []domain.SourceSummary{hydrateSourceSummary(session, domain.SourceSummary{
		Provider:    session.Source,
		Kind:        sourceScopeKind(session.SourceScope),
		Ref:         sessionSourceRef(session, 0),
		Title:       strings.TrimSpace(session.Title),
		Description: strings.TrimSpace(session.Description),
		Excerpt:     summarizeText(session.Description, 240),
	}, 0, 1)}
}

func hydrateSourceSummary(session *domain.Session, summary domain.SourceSummary, index, total int) domain.SourceSummary {
	if session == nil {
		return summary
	}
	hydrated := summary
	if strings.TrimSpace(hydrated.Provider) == "" {
		hydrated.Provider = session.Source
	}
	if strings.TrimSpace(hydrated.Kind) == "" {
		hydrated.Kind = sourceScopeKind(session.SourceScope)
	}
	if strings.TrimSpace(hydrated.Ref) == "" {
		hydrated.Ref = sessionSourceRef(session, index)
	}
	if total != 1 {
		return hydrated
	}
	if strings.TrimSpace(hydrated.Title) == "" {
		hydrated.Title = strings.TrimSpace(session.Title)
	}
	if strings.TrimSpace(hydrated.Description) == "" {
		hydrated.Description = strings.TrimSpace(session.Description)
	}
	if strings.TrimSpace(hydrated.State) == "" {
		hydrated.State = sessionExternalState(session)
	}
	if len(hydrated.Labels) == 0 && len(session.Labels) > 0 {
		hydrated.Labels = append([]string(nil), session.Labels...)
	}
	if strings.TrimSpace(hydrated.Container) == "" {
		hydrated.Container = strings.Join(sessionContainers(session), ", ")
	}
	if strings.TrimSpace(hydrated.URL) == "" {
		hydrated.URL = sessionURL(session.Metadata)
	}

	return hydrated
}

func sessionSourceRef(session *domain.Session, index int) string {
	if session == nil {
		return ""
	}
	if refs := sessionTrackerRefs(session.Metadata); index >= 0 && index < len(refs) {
		return formatTrackerRef(refs[index])
	}
	if index >= 0 && index < len(session.SourceItemIDs) {
		return strings.TrimSpace(session.SourceItemIDs[index])
	}
	if index == 0 {
		return strings.TrimSpace(session.ExternalID)
	}

	return ""
}

func sourceScopeKind(scope domain.SelectionScope) string {
	switch scope {
	case domain.ScopeIssues:
		return sourceTypeIssue
	case domain.ScopeProjects:
		return "project"
	case domain.ScopeInitiatives:
		return "initiative"
	default:
		return ""
	}
}

func sessionHasSourceDetails(session *domain.Session) bool {
	if session == nil || session.Source == "" || session.Source == providerManual {
		return false
	}
	if sessionSourceCount(session) > 0 {
		return true
	}
	if len(session.Labels) > 0 {
		return true
	}
	if len(sessionContainers(session)) > 0 {
		return true
	}

	return sessionExternalState(session) != ""
}

func sessionSourceSidebarSubtitle(session *domain.Session) string {
	if session == nil {
		return ""
	}
	parts := make([]string, 0, 2)
	if provider := detailProviderLabel(session.Source); provider != "" {
		parts = append(parts, provider)
	}
	if selected := sessionSourceSelectionSummary(session); selected != "" {
		parts = append(parts, selected)
	}

	return strings.Join(parts, " · ")
}

func sessionSourceSelectionSummary(session *domain.Session) string {
	if session == nil {
		return ""
	}
	count := sessionSourceCount(session)
	noun := sessionSourceNoun(session.SourceScope, count)
	if noun == "" {
		return ""
	}
	if count <= 0 {
		return noun
	}

	return fmt.Sprintf("%d %s", count, noun)
}

func sessionSourceCount(session *domain.Session) int {
	if session == nil {
		return 0
	}
	if len(session.SourceItemIDs) > 0 {
		return len(session.SourceItemIDs)
	}
	if refs := sessionTrackerRefs(session.Metadata); len(refs) > 0 {
		return len(refs)
	}
	if session.Source != "" && session.Source != providerManual {
		return 1
	}

	return 0
}

func sessionSourceNoun(scope domain.SelectionScope, count int) string {
	plural := count != 1
	switch scope {
	case domain.ScopeIssues:
		if plural {
			return "issues"
		}

		return sourceTypeIssue
	case domain.ScopeProjects:
		if plural {
			return "projects"
		}

		return "project"
	case domain.ScopeInitiatives:
		if plural {
			return "initiatives"
		}

		return "initiative"
	case domain.ScopeManual:
		if plural {
			return "manual items"
		}

		return "manual item"
	default:
		if plural {
			return "source items"
		}

		return "source item"
	}
}

func sessionContainers(session *domain.Session) []string {
	if session == nil {
		return nil
	}
	if team := sessionMetadataString(session.Metadata, "linear_team_key"); team != "" {
		return []string{team}
	}
	if name := sessionMetadataString(session.Metadata, "linear_project_name"); name != "" {
		return []string{name}
	}
	if names := sessionMetadataStrings(session.Metadata, "linear_project_names"); len(names) > 0 {
		return append([]string(nil), names...)
	}

	refs := sessionTrackerRefs(session.Metadata)
	containers := make([]string, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		container := trackerRefContainer(ref)
		if container == "" {
			continue
		}
		if _, ok := seen[container]; ok {
			continue
		}
		seen[container] = struct{}{}
		containers = append(containers, container)
	}

	return containers
}

func sessionExternalState(session *domain.Session) string {
	if session == nil {
		return ""
	}
	for _, key := range []string{"state", "linear_state_name", "linear_project_state", "linear_initiative_status"} {
		if value := sessionMetadataString(session.Metadata, key); value != "" {
			return value
		}
	}

	return ""
}

func sessionMetadataString(metadata map[string]any, key string) string {
	if len(metadata) == 0 {
		return ""
	}
	value, _ := metadata[key].(string)

	return strings.TrimSpace(value)
}

func sessionMetadataStrings(metadata map[string]any, key string) []string {
	if len(metadata) == 0 {
		return nil
	}
	raw, ok := metadata[key]
	if !ok || raw == nil {
		return nil
	}
	if typed, ok := raw.([]string); ok {
		return append([]string(nil), typed...)
	}

	payload, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var values []string
	if err := json.Unmarshal(payload, &values); err != nil {
		return nil
	}

	return values
}

func sessionTrackerRefs(metadata map[string]any) []domain.TrackerReference {
	if len(metadata) == 0 {
		return nil
	}
	raw, ok := metadata["tracker_refs"]
	if !ok || raw == nil {
		return nil
	}
	if typed, ok := raw.([]domain.TrackerReference); ok {
		return append([]domain.TrackerReference(nil), typed...)
	}

	payload, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var refs []domain.TrackerReference
	if err := json.Unmarshal(payload, &refs); err != nil {
		return nil
	}

	return refs
}

func formatTrackerRef(ref domain.TrackerReference) string {
	container := trackerRefContainer(ref)
	if ref.Kind == sourceTypeIssue {
		if container != "" && ref.Number > 0 {
			return container + "#" + strconv.FormatInt(ref.Number, 10)
		}
		if ref.Number > 0 {
			return "#" + strconv.FormatInt(ref.Number, 10)
		}
		if strings.TrimSpace(ref.ID) != "" {
			return ref.ID
		}
	}
	kind := trackerRefKindLabel(ref.Kind)
	if strings.TrimSpace(ref.ID) != "" {
		return kind + " " + ref.ID
	}
	if ref.Number > 0 {
		return kind + " #" + strconv.FormatInt(ref.Number, 10)
	}
	if container != "" {
		return kind + " " + container
	}
	if ref.URL != "" {
		return ref.URL
	}

	return kind
}

func trackerRefKindLabel(kind string) string {
	switch strings.TrimSpace(kind) {
	case sourceTypeIssue:
		return "Issue"
	case "project":
		return "Project"
	case "initiative":
		return "Initiative"
	default:
		if kind == "" {
			return "Item"
		}

		return strings.ToUpper(kind[:1]) + kind[1:]
	}
}

func trackerRefContainer(ref domain.TrackerReference) string {
	if ref.Owner != "" && ref.Repo != "" {
		return ref.Owner + "/" + ref.Repo
	}
	if ref.Repository.Owner != "" && ref.Repository.Repo != "" {
		return ref.Repository.Owner + "/" + ref.Repository.Repo
	}
	if ref.Repo != "" {
		return ref.Repo
	}
	if ref.Repository.Repo != "" {
		if ref.Repository.Owner != "" {
			return ref.Repository.Owner + "/" + ref.Repository.Repo
		}

		return ref.Repository.Repo
	}

	return ""
}
