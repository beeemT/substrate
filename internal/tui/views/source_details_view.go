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

// SourceDetailsModel renders source-system details for the selected work item.
type SourceDetailsModel struct {
	viewport viewport.Model
	workItem *domain.WorkItem
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

func (m *SourceDetailsModel) SetWorkItem(wi *domain.WorkItem) {
	m.workItem = wi
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
	case tea.WindowSizeMsg:
		m.syncViewport(false)
	}
	return m, cmd
}

func (m SourceDetailsModel) View() string {
	if m.workItem == nil || m.width <= 0 || m.height <= 0 {
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
	if m.workItem == nil {
		return ""
	}
	return components.RenderHeaderBlock(m.styles, components.HeaderBlockSpec{
		Title:   m.workItem.ExternalID + " · " + m.workItem.Title,
		Meta:    "Source details",
		Width:   m.width,
		Divider: true,
	})
}

func (m *SourceDetailsModel) syncViewport(reset bool) {
	if m.workItem == nil || m.width <= 0 || m.height <= 0 {
		return
	}
	headerHeight := len(strings.Split(m.header(), "\n"))
	m.viewport.Width = m.width
	m.viewport.Height = max(0, m.height-headerHeight)
	m.viewport.SetContent(renderSourceDetailsDocument(m.styles, m.workItem, m.width))
	if reset {
		m.viewport.GotoTop()
	}
}

func renderSourceDetailsDocument(st styles.Styles, wi *domain.WorkItem, width int) string {
	if wi == nil || width <= 0 {
		return ""
	}
	sections := []string{
		st.SectionLabel.Render("Summary"),
		components.RenderCallout(st, components.CalloutSpec{
			Body:  renderSourceSummaryBody(st, wi, components.CalloutInnerWidth(st, width)),
			Width: width,
		}),
	}
	if references := renderSourceReferencesBody(st, wi, components.CalloutInnerWidth(st, width)); references != "" {
		sections = append(sections,
			st.SectionLabel.Render("Selected items"),
			components.RenderCallout(st, components.CalloutSpec{Body: references, Width: width, Variant: components.CalloutCard}),
		)
	}
	return strings.Join(sections, "\n\n")
}

func renderSourceSummaryBody(st styles.Styles, wi *domain.WorkItem, width int) string {
	labelStyle := st.SectionLabel
	valueStyle := st.SettingsText
	rows := make([]string, 0, 6)
	add := func(label, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		rows = append(rows, ansi.Hardwrap(labelStyle.Render(label+": ")+valueStyle.Render(value), width, true))
	}

	add("Provider", detailProviderLabel(wi.Source))
	add("Selected", workItemSourceSelectionSummary(wi))
	if containers := workItemContainers(wi); len(containers) > 0 {
		label := "Container"
		if len(containers) > 1 {
			label = "Containers"
		}
		add(label, strings.Join(containers, ", "))
	}
	if workItemSourceCount(wi) <= 1 {
		add("State", workItemExternalState(wi))
		if len(wi.Labels) > 0 {
			add("Labels", strings.Join(wi.Labels, ", "))
		}
	} else if len(wi.Labels) > 0 {
		rows = append(rows, ansi.Hardwrap(st.Muted.Render("Labels are omitted here because this work item aggregates multiple source items."), width, true))
	}
	if len(rows) == 0 {
		return st.Muted.Render("No source summary available.")
	}
	return strings.Join(rows, "\n")
}

func renderSourceReferencesBody(st styles.Styles, wi *domain.WorkItem, width int) string {
	refs := workItemTrackerRefs(wi.Metadata)
	rows := make([]string, 0, max(len(refs), len(wi.SourceItemIDs)))
	for _, ref := range refs {
		rows = append(rows, ansi.Hardwrap(st.SettingsText.Render("• "+formatTrackerRef(ref)), width, true))
	}
	if len(rows) == 0 {
		for _, id := range wi.SourceItemIDs {
			if strings.TrimSpace(id) == "" {
				continue
			}
			rows = append(rows, ansi.Hardwrap(st.SettingsText.Render("• "+id), width, true))
		}
	}
	if len(rows) == 0 {
		return ""
	}
	return strings.Join(rows, "\n")
}

func workItemHasSourceDetails(wi *domain.WorkItem) bool {
	if wi == nil || wi.Source == "" || wi.Source == "manual" {
		return false
	}
	if workItemSourceCount(wi) > 0 {
		return true
	}
	if len(wi.Labels) > 0 {
		return true
	}
	if len(workItemContainers(wi)) > 0 {
		return true
	}
	return workItemExternalState(wi) != ""
}

func workItemSourceSidebarSubtitle(wi *domain.WorkItem) string {
	if wi == nil {
		return ""
	}
	parts := make([]string, 0, 2)
	if provider := detailProviderLabel(wi.Source); provider != "" {
		parts = append(parts, provider)
	}
	if selected := workItemSourceSelectionSummary(wi); selected != "" {
		parts = append(parts, selected)
	}
	return strings.Join(parts, " · ")
}

func workItemSourceSelectionSummary(wi *domain.WorkItem) string {
	if wi == nil {
		return ""
	}
	count := workItemSourceCount(wi)
	noun := workItemSourceNoun(wi.SourceScope, count)
	if noun == "" {
		return ""
	}
	if count <= 0 {
		return noun
	}
	return fmt.Sprintf("%d %s", count, noun)
}

func workItemSourceCount(wi *domain.WorkItem) int {
	if wi == nil {
		return 0
	}
	if len(wi.SourceItemIDs) > 0 {
		return len(wi.SourceItemIDs)
	}
	if refs := workItemTrackerRefs(wi.Metadata); len(refs) > 0 {
		return len(refs)
	}
	if wi.Source != "" && wi.Source != "manual" {
		return 1
	}
	return 0
}

func workItemSourceNoun(scope domain.SelectionScope, count int) string {
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

func workItemContainers(wi *domain.WorkItem) []string {
	if wi == nil {
		return nil
	}
	if team := workItemMetadataString(wi.Metadata, "linear_team_key"); team != "" {
		return []string{team}
	}
	if name := workItemMetadataString(wi.Metadata, "linear_project_name"); name != "" {
		return []string{name}
	}
	if names := workItemMetadataStrings(wi.Metadata, "linear_project_names"); len(names) > 0 {
		return append([]string(nil), names...)
	}

	refs := workItemTrackerRefs(wi.Metadata)
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

func workItemExternalState(wi *domain.WorkItem) string {
	if wi == nil {
		return ""
	}
	for _, key := range []string{"state", "linear_state_name", "linear_project_state", "linear_initiative_status"} {
		if value := workItemMetadataString(wi.Metadata, key); value != "" {
			return value
		}
	}
	return ""
}

func workItemMetadataString(metadata map[string]any, key string) string {
	if len(metadata) == 0 {
		return ""
	}
	value, _ := metadata[key].(string)
	return strings.TrimSpace(value)
}

func workItemMetadataStrings(metadata map[string]any, key string) []string {
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

func workItemTrackerRefs(metadata map[string]any) []domain.TrackerReference {
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
