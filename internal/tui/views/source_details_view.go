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

// SourceDetailsModel renders source-system details for the selected session.
type SourceDetailsModel struct {
	viewport viewport.Model
	session  *domain.Session
	styles   styles.Styles
	width    int
	height   int
}

func NewSourceDetailsModel(st styles.Styles) SourceDetailsModel {
	return SourceDetailsModel{styles: st}
}

func (m *SourceDetailsModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.syncViewport(false)
}

func (m *SourceDetailsModel) SetSession(session *domain.Session) {
	m.session = session
	m.syncViewport(true)
}

func (m SourceDetailsModel) KeybindHints() []KeybindHint {
	return []KeybindHint{{Key: "↑↓", Label: "Scroll"}}
}

func (m SourceDetailsModel) Update(msg tea.Msg) (SourceDetailsModel, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "down", "j", "k", "pgup", "pgdown", "home", "end":
			m.viewport, cmd = m.viewport.Update(msg)
		}
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress {
			switch msg.Button {
			case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
				m.viewport, cmd = m.viewport.Update(msg)
			}
		}
	case tea.WindowSizeMsg:
		m.syncViewport(false)
	}
	return m, cmd
}

func (m SourceDetailsModel) View() string {
	if m.session == nil || m.width <= 0 || m.height <= 0 {
		return ""
	}
	header := m.header()
	headerLines := len(strings.Split(header, "\n"))
	if headerLines >= m.height {
		return fitViewBox(header, m.width, m.height)
	}
	body := m.viewport.View()
	if strings.TrimSpace(body) == "" {
		body = m.styles.Muted.Render("No source details available.")
	}
	return fitViewBox(header+"\n"+body, m.width, m.height)
}

func (m SourceDetailsModel) header() string {
	if m.session == nil {
		return ""
	}
	return components.RenderHeaderBlock(m.styles, components.HeaderBlockSpec{
		Title:   m.session.ExternalID + " · " + m.session.Title,
		Meta:    "Source details",
		Width:   m.width,
		Divider: true,
	})
}

func (m *SourceDetailsModel) syncViewport(reset bool) {
	if m.session == nil || m.width <= 0 || m.height <= 0 {
		return
	}
	headerHeight := len(strings.Split(m.header(), "\n"))
	m.viewport.Width = m.width
	m.viewport.Height = max(0, m.height-headerHeight)
	m.viewport.SetContent(renderSourceDetailsDocument(m.styles, m.session, m.width))
	if reset {
		m.viewport.GotoTop()
	}
}

func renderSourceDetailsDocument(st styles.Styles, session *domain.Session, width int) string {
	if session == nil || width <= 0 {
		return ""
	}
	sections := []string{
		st.SectionLabel.Render("Summary"),
		components.RenderCallout(st, components.CalloutSpec{
			Body:  renderSourceSummaryBody(st, session, components.CalloutInnerWidth(st, width)),
			Width: width,
		}),
	}
	if references := renderSourceReferencesBody(st, session, components.CalloutInnerWidth(st, width)); references != "" {
		sections = append(sections,
			st.SectionLabel.Render("Selected items"),
			components.RenderCallout(st, components.CalloutSpec{Body: references, Width: width, Variant: components.CalloutCard}),
		)
	}
	return strings.Join(sections, "\n\n")
}

func renderSourceSummaryBody(st styles.Styles, session *domain.Session, width int) string {
	labelStyle := st.SectionLabel
	valueStyle := st.SettingsText
	rows := make([]string, 0, 6)
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
	} else if len(session.Labels) > 0 {
		rows = append(rows, ansi.Hardwrap(st.Muted.Render("Labels are omitted here because this session aggregates multiple source items."), width, true))
	}
	if len(rows) == 0 {
		return st.Muted.Render("No source summary available.")
	}
	return strings.Join(rows, "\n")
}

func renderSourceReferencesBody(st styles.Styles, session *domain.Session, width int) string {
	summaries := sessionSourceSummaries(session.Metadata)
	rows := make([]string, 0, max(len(summaries), max(len(sessionTrackerRefs(session.Metadata)), len(session.SourceItemIDs))))
	if len(summaries) > 0 {
		for _, summary := range summaries {
			block := []string{ansi.Hardwrap(st.SettingsText.Render("• "+firstNonEmptyString(summary.Ref, "unknown")), width, true)}
			if strings.TrimSpace(summary.Title) != "" {
				block = append(block, ansi.Hardwrap(st.Muted.Render("  "+summary.Title), width, true))
			}
			if strings.TrimSpace(summary.Excerpt) != "" {
				block = append(block, ansi.Hardwrap(st.Muted.Render("  "+summarizeText(summary.Excerpt, 120)), width, true))
			}
			rows = append(rows, strings.Join(block, "\n"))
		}
	} else {
		refs := sessionTrackerRefs(session.Metadata)
		for _, ref := range refs {
			rows = append(rows, ansi.Hardwrap(st.SettingsText.Render("• "+formatTrackerRef(ref)), width, true))
		}
		if len(rows) == 0 {
			for _, id := range session.SourceItemIDs {
				if strings.TrimSpace(id) == "" {
					continue
				}
				rows = append(rows, ansi.Hardwrap(st.SettingsText.Render("• "+id), width, true))
			}
		}
	}
	if len(rows) == 0 {
		return ""
	}
	return strings.Join(rows, "\n")
}

func sessionHasSourceDetails(session *domain.Session) bool {
	if session == nil || session.Source == "" || session.Source == "manual" {
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
	if session.Source != "" && session.Source != "manual" {
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
		return "issue"
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
	switch typed := raw.(type) {
	case []string:
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
	switch typed := raw.(type) {
	case []domain.TrackerReference:
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
	if ref.Kind == "issue" {
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
	case "issue":
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
