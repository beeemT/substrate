package views

import (
	"context"
	"fmt"
	"strings"

	"github.com/beeemT/substrate/internal/tui/styles"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type settingsAction int

const (
	settingsActionSave settingsAction = iota
	settingsActionApply
	settingsActionTestProvider
	settingsActionLoginProvider
)

type settingsFocus int

const (
	settingsFocusSections settingsFocus = iota
	settingsFocusFields
)

type SettingsSavedMsg struct {
	Raw     string
	Message string
}

type SettingsAppliedMsg struct {
	Reload  viewsServicesReload
	Message string
}

type SettingsProviderTestedMsg struct {
	Provider string
	Status   ProviderStatus
}

type SettingsSectionPatchedMsg struct {
	SectionID string
	Section   SettingsSection
	Message   string
}

type settingsNavNode struct {
	key          string
	label        string
	sectionIndex int
	depth        int
	hasChildren  bool
	expanded     bool
	synthetic    bool
}

type settingsSyntheticGroup struct {
	key   string
	label string
}

type SettingsPage struct {
	service          *SettingsService
	sections         []SettingsSection
	providerStatus   map[string]ProviderStatus
	rawContent       string
	active           bool
	width            int
	height           int
	sectionCursor    int
	fieldCursor      int
	navCursor        string
	focus            settingsFocus
	expandedSections map[string]bool
	mainViewport     viewport.Model
	editing          bool
	revealSecrets    bool
	dirty            bool
	editInput        textinput.Model
	styles           styles.Styles
	errorText        string
	statusText       string
}

func NewSettingsPage(svc *SettingsService, snapshot SettingsSnapshot, st styles.Styles) SettingsPage {
	ti := textinput.New()
	ti.CharLimit = 1000
	vp := viewport.New(0, 0)
	return SettingsPage{
		service:          svc,
		sections:         snapshot.Sections,
		providerStatus:   snapshot.Providers,
		rawContent:       snapshot.RawTOML,
		focus:            settingsFocusSections,
		expandedSections: defaultExpandedSections(snapshot.Sections),
		mainViewport:     vp,
		editInput:        ti,
		styles:           st,
	}
}

func (m *SettingsPage) Open() {
	m.active = true
	m.focusSections()
	m.editing = false
	m.editInput.Blur()
	m.clampCursor()
	m.syncMainViewport()
}

func (m *SettingsPage) Close() {
	m.active = false
	m.focusSections()
	m.editing = false
	m.editInput.Blur()
	m.errorText = ""
	m.syncMainViewport()
}

func (m SettingsPage) Active() bool { return m.active }

func (m *SettingsPage) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.syncMainViewport()
}

func (m *SettingsPage) SetSnapshot(snapshot SettingsSnapshot) {
	m.sections = snapshot.Sections
	m.providerStatus = snapshot.Providers
	m.rawContent = snapshot.RawTOML
	m.dirty = false
	m.errorText = ""
	m.statusText = ""
	m.editing = false
	m.editInput.Blur()
	m.expandedSections = defaultExpandedSections(snapshot.Sections)
	m.clampCursor()
	m.syncMainViewport()
}

func (m *SettingsPage) currentSection() *SettingsSection {
	if len(m.sections) == 0 || m.sectionCursor < 0 || m.sectionCursor >= len(m.sections) {
		return nil
	}
	return &m.sections[m.sectionCursor]
}

func (m *SettingsPage) currentField() *SettingsField {
	sec := m.currentSection()
	if sec == nil || len(sec.Fields) == 0 || m.fieldCursor < 0 || m.fieldCursor >= len(sec.Fields) {
		return nil
	}
	return &sec.Fields[m.fieldCursor]
}

func sectionParentIndexFor(sections []SettingsSection, sectionIndex int) int {
	if sectionIndex < 0 || sectionIndex >= len(sections) {
		return -1
	}
	childID := sections[sectionIndex].ID
	bestParent := -1
	bestLen := -1
	for i, section := range sections {
		if i == sectionIndex {
			continue
		}
		if !strings.HasPrefix(childID, section.ID+".") {
			continue
		}
		if len(section.ID) > bestLen {
			bestParent = i
			bestLen = len(section.ID)
		}
	}
	return bestParent
}

func syntheticGroupForSectionID(sectionID string) (settingsSyntheticGroup, bool) {
	switch {
	case strings.HasPrefix(sectionID, "provider."):
		return settingsSyntheticGroup{key: "group.providers", label: "Providers"}, true
	case sectionID == "repo.glab":
		return settingsSyntheticGroup{key: "group.repo-lifecycle", label: "Repo lifecycle"}, true
	default:
		return settingsSyntheticGroup{}, false
	}
}

func hasSectionChildren(sections []SettingsSection, sectionIndex int) bool {
	for i := range sections {
		if sectionParentIndexFor(sections, i) == sectionIndex {
			return true
		}
	}
	return false
}

func defaultExpandedSections(sections []SettingsSection) map[string]bool {
	expanded := make(map[string]bool, len(sections))
	for i := range sections {
		if hasSectionChildren(sections, i) {
			expanded[sections[i].ID] = true
		}
		if group, ok := syntheticGroupForSectionID(sections[i].ID); ok {
			expanded[group.key] = true
		}
	}
	return expanded
}

func (m SettingsPage) sectionParentIndex(sectionIndex int) int {
	return sectionParentIndexFor(m.sections, sectionIndex)
}

func (m SettingsPage) sectionChildrenIndexes(sectionIndex int) []int {
	children := make([]int, 0, 4)
	for i := range m.sections {
		if m.sectionParentIndex(i) == sectionIndex {
			children = append(children, i)
		}
	}
	return children
}

func (m SettingsPage) syntheticGroupChildren(groupKey string) []int {
	children := make([]int, 0, 4)
	for i := range m.sections {
		if m.sectionParentIndex(i) != -1 {
			continue
		}
		group, ok := syntheticGroupForSectionID(m.sections[i].ID)
		if ok && group.key == groupKey {
			children = append(children, i)
		}
	}
	return children
}

func (m SettingsPage) sectionDepth(sectionIndex int) int {
	depth := 0
	if group, ok := syntheticGroupForSectionID(m.sections[sectionIndex].ID); ok && len(m.syntheticGroupChildren(group.key)) > 0 {
		depth++
	}
	for parent := m.sectionParentIndex(sectionIndex); parent >= 0; parent = m.sectionParentIndex(parent) {
		depth++
	}
	return depth
}

func (m *SettingsPage) expandAncestors(sectionIndex int) {
	for parent := m.sectionParentIndex(sectionIndex); parent >= 0; parent = m.sectionParentIndex(parent) {
		m.expandedSections[m.sections[parent].ID] = true
	}
	if group, ok := syntheticGroupForSectionID(m.sections[sectionIndex].ID); ok {
		m.expandedSections[group.key] = true
	}
}

func (m *SettingsPage) clampCursor() {
	if m.sectionCursor >= len(m.sections) {
		m.sectionCursor = max(0, len(m.sections)-1)
	}
	sec := m.currentSection()
	if sec == nil {
		m.fieldCursor = 0
		return
	}
	if m.fieldCursor >= len(sec.Fields) {
		m.fieldCursor = max(0, len(sec.Fields)-1)
	}
}

func (m SettingsPage) visibleNavNodes() []settingsNavNode {
	nodes := make([]settingsNavNode, 0, len(m.sections)+4)
	seenSynthetic := make(map[string]bool)
	var walkActual func(sectionIndex int, depth int)
	walkActual = func(sectionIndex int, depth int) {
		children := m.sectionChildrenIndexes(sectionIndex)
		node := settingsNavNode{
			key:          m.sections[sectionIndex].ID,
			label:        m.sidebarLabel(sectionIndex),
			sectionIndex: sectionIndex,
			depth:        depth,
			hasChildren:  len(children) > 0,
			expanded:     m.expandedSections[m.sections[sectionIndex].ID],
		}
		nodes = append(nodes, node)
		if !node.hasChildren || !node.expanded {
			return
		}
		for _, childIndex := range children {
			walkActual(childIndex, depth+1)
		}
	}
	for i := range m.sections {
		if m.sectionParentIndex(i) != -1 {
			continue
		}
		if group, ok := syntheticGroupForSectionID(m.sections[i].ID); ok {
			if seenSynthetic[group.key] {
				continue
			}
			children := m.syntheticGroupChildren(group.key)
			if len(children) == 0 {
				continue
			}
			node := settingsNavNode{
				key:          group.key,
				label:        group.label,
				sectionIndex: children[0],
				depth:        0,
				hasChildren:  true,
				expanded:     m.expandedSections[group.key],
				synthetic:    true,
			}
			nodes = append(nodes, node)
			seenSynthetic[group.key] = true
			if node.expanded {
				for _, childIndex := range children {
					walkActual(childIndex, 1)
				}
			}
			continue
		}
		walkActual(i, 0)
	}
	return nodes
}

func (m SettingsPage) navNodeForSection(sectionIndex int) (settingsNavNode, int, bool) {
	nodes := m.visibleNavNodes()
	for i, node := range nodes {
		if !node.synthetic && node.sectionIndex == sectionIndex {
			return node, i, true
		}
	}
	return settingsNavNode{}, 0, false
}

func (m SettingsPage) navNodeForKey(key string) (settingsNavNode, int, bool) {
	nodes := m.visibleNavNodes()
	for i, node := range nodes {
		if node.key == key {
			return node, i, true
		}
	}
	return settingsNavNode{}, 0, false
}

func (m SettingsPage) currentSidebarNode() (settingsNavNode, int, bool) {
	key := m.navCursor
	if key == "" {
		if sec := m.currentSection(); sec != nil {
			key = sec.ID
		}
	}
	return m.navNodeForKey(key)
}

func settingsFieldAnchorKey(sectionIndex, fieldIndex int) string {
	return fmt.Sprintf("%d:%d", sectionIndex, fieldIndex)
}

func (m SettingsPage) layoutSpacerWidth() int {
	if m.width > 2 {
		return 1
	}
	return 0
}

func (m SettingsPage) layoutMetrics() (leftWidth, mainWidth, bodyHeight, detailHeight int) {
	footerHeight := 2
	bodyHeight = max(1, m.height-footerHeight)

	availableWidth := max(2, m.width-m.layoutSpacerWidth())
	desiredSidebarWidth := min(34, max(18, m.width/3))
	minSidebarWidth := 12
	minMainWidth := 24
	if m.width < 48 {
		minMainWidth = min(max(1, availableWidth-1), max(12, availableWidth/2))
	}
	if availableWidth < minSidebarWidth+minMainWidth {
		minMainWidth = max(1, (availableWidth*2)/3)
		minSidebarWidth = max(1, availableWidth-minMainWidth)
	}

	leftWidth = min(desiredSidebarWidth, max(1, availableWidth-minMainWidth))
	if leftWidth < minSidebarWidth {
		leftWidth = minSidebarWidth
	}
	mainWidth = max(1, availableWidth-leftWidth)
	detailHeight = min(9, max(7, bodyHeight/3))
	return leftWidth, mainWidth, bodyHeight, detailHeight
}

func (m SettingsPage) mainViewportSize() (width, height, detailHeight int) {
	_, mainWidth, bodyHeight, desiredDetailHeight := m.layoutMetrics()
	innerWidth := max(1, mainWidth-2)
	innerHeight := max(1, bodyHeight-2)
	headerHeight := m.stickySectionHeaderHeight()
	if headerHeight >= innerHeight {
		headerHeight = max(1, innerHeight-1)
	}
	remainingHeight := max(1, innerHeight-headerHeight)
	detailHeight = desiredDetailHeight
	if detailHeight >= remainingHeight {
		detailHeight = max(0, remainingHeight-1)
	}
	width = max(1, innerWidth)
	if width > 2 {
		width -= 2
	}
	return width, max(1, remainingHeight-detailHeight), detailHeight
}

func (m SettingsPage) selectedDocumentAnchor(sectionAnchors map[int]int, fieldAnchors map[string]int) int {
	if (m.fieldsFocused() || m.editing) && m.currentField() != nil {
		if line, ok := fieldAnchors[settingsFieldAnchorKey(m.sectionCursor, m.fieldCursor)]; ok {
			return line
		}
	}
	if line, ok := sectionAnchors[m.sectionCursor]; ok {
		return line
	}
	return 0
}

func (m *SettingsPage) alignViewportToAnchor(anchor int) {
	if m.mainViewport.Height <= 0 {
		return
	}
	top := m.mainViewport.YOffset
	bottom := top + m.mainViewport.Height - 1
	margin := 1
	if anchor < top+margin {
		m.mainViewport.SetYOffset(max(0, anchor-margin))
		return
	}
	if anchor > bottom-margin {
		m.mainViewport.SetYOffset(max(0, anchor-m.mainViewport.Height+1+margin))
	}
}

func (m *SettingsPage) syncMainViewport() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	viewportWidth, viewportHeight, _ := m.mainViewportSize()
	m.mainViewport.Width = viewportWidth
	m.mainViewport.Height = viewportHeight
	content, sectionAnchors, fieldAnchors := m.buildMainDocument(viewportWidth)
	m.mainViewport.SetContent(content)
	m.alignViewportToAnchor(m.selectedDocumentAnchor(sectionAnchors, fieldAnchors))
}

func (m SettingsPage) fieldsFocused() bool {
	return m.focus == settingsFocusFields && m.currentField() != nil
}

func (m *SettingsPage) focusSections() {
	m.focus = settingsFocusSections
	if sec := m.currentSection(); sec != nil {
		m.navCursor = sec.ID
	}
}

func (m *SettingsPage) focusFields() {
	m.clampCursor()
	if m.currentField() == nil {
		m.focus = settingsFocusSections
		return
	}
	m.focus = settingsFocusFields
	if sec := m.currentSection(); sec != nil {
		m.navCursor = sec.ID
	}
}

func (m *SettingsPage) moveSection(delta int) {
	nodes := m.visibleNavNodes()
	if len(nodes) == 0 {
		return
	}
	_, currentPos, ok := m.currentSidebarNode()
	if !ok {
		currentPos = 0
	}
	nextPos := currentPos + delta
	if nextPos < 0 {
		nextPos = 0
	}
	if nextPos >= len(nodes) {
		nextPos = len(nodes) - 1
	}
	node := nodes[nextPos]
	m.navCursor = node.key
	m.sectionCursor = node.sectionIndex
	m.fieldCursor = 0
	m.clampCursor()
}

func (m *SettingsPage) expandCurrentSection() bool {
	node, _, ok := m.currentSidebarNode()
	if !ok || !node.hasChildren || node.expanded {
		return false
	}
	m.expandedSections[node.key] = true
	return true
}

func (m *SettingsPage) collapseCurrentSection() bool {
	node, _, ok := m.currentSidebarNode()
	if !ok || !node.hasChildren || !node.expanded {
		return false
	}
	m.expandedSections[node.key] = false
	return true
}

func (m *SettingsPage) focusParentSection() bool {
	node, _, ok := m.currentSidebarNode()
	if !ok || node.synthetic {
		return false
	}
	if parentIndex := m.sectionParentIndex(node.sectionIndex); parentIndex >= 0 {
		m.sectionCursor = parentIndex
		m.fieldCursor = 0
		m.navCursor = m.sections[parentIndex].ID
		m.clampCursor()
		m.expandAncestors(m.sectionCursor)
		return true
	}
	if group, ok := syntheticGroupForSectionID(m.sections[node.sectionIndex].ID); ok {
		m.navCursor = group.key
		children := m.syntheticGroupChildren(group.key)
		if len(children) > 0 {
			m.sectionCursor = children[0]
			m.fieldCursor = 0
			m.clampCursor()
			m.expandAncestors(m.sectionCursor)
		}
		return true
	}
	return false
}

func (m *SettingsPage) focusFirstChildSection() bool {
	node, _, ok := m.currentSidebarNode()
	if !ok || !node.hasChildren {
		return false
	}
	var childIndex int
	if node.synthetic {
		children := m.syntheticGroupChildren(node.key)
		if len(children) == 0 {
			return false
		}
		childIndex = children[0]
	} else {
		children := m.sectionChildrenIndexes(node.sectionIndex)
		if len(children) == 0 {
			return false
		}
		childIndex = children[0]
	}
	m.sectionCursor = childIndex
	m.fieldCursor = 0
	m.navCursor = m.sections[childIndex].ID
	m.clampCursor()
	m.expandAncestors(m.sectionCursor)
	return true
}

func (m *SettingsPage) moveField(delta int) {
	sec := m.currentSection()
	if sec == nil || len(sec.Fields) == 0 {
		m.focusSections()
		return
	}
	next := m.fieldCursor + delta
	if next >= 0 && next < len(sec.Fields) {
		m.fieldCursor = next
		m.focus = settingsFocusFields
		m.navCursor = sec.ID
		return
	}

	sectionIndex := m.sectionCursor + delta
	for sectionIndex >= 0 && sectionIndex < len(m.sections) {
		nextSection := m.sections[sectionIndex]
		if len(nextSection.Fields) > 0 {
			m.sectionCursor = sectionIndex
			if delta > 0 {
				m.fieldCursor = 0
			} else {
				m.fieldCursor = len(nextSection.Fields) - 1
			}
			m.focus = settingsFocusFields
			m.navCursor = m.sections[m.sectionCursor].ID
			m.expandAncestors(m.sectionCursor)
			return
		}
		sectionIndex += delta
	}

	if delta < 0 {
		m.fieldCursor = 0
	} else {
		m.fieldCursor = len(sec.Fields) - 1
	}
	m.focus = settingsFocusFields
	m.navCursor = sec.ID
}

func (m SettingsPage) Update(msg tea.Msg, svcs Services) (SettingsPage, tea.Cmd) {
	defer m.syncMainViewport()
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.editing {
			switch msg.String() {
			case "enter":
				if f := m.currentField(); f != nil {
					f.Value = m.editInput.Value()
					f.Dirty = true
					m.dirty = true
					m.statusText = "Field updated"
				}
				m.editing = false
				m.editInput.Blur()
				return m, nil
			case "esc":
				m.editing = false
				m.editInput.Blur()
				return m, nil
			default:
				m.editInput, cmd = m.editInput.Update(msg)
				return m, cmd
			}
		}
		switch msg.String() {
		case "up", "k":
			if m.fieldsFocused() {
				m.moveField(-1)
			} else {
				m.moveSection(-1)
			}
		case "down", "j":
			if m.fieldsFocused() {
				m.moveField(1)
			} else {
				m.moveSection(1)
			}
		case "left", "h":
			if m.fieldsFocused() {
				m.focusSections()
				break
			}
			if m.collapseCurrentSection() {
				break
			}
			m.focusParentSection()
		case "right", "l":
			if m.focus == settingsFocusSections {
				if m.expandCurrentSection() {
					break
				}
				if m.focusFirstChildSection() {
					break
				}
				m.focusFields()
			}
		case "enter":
			if m.focus == settingsFocusSections {
				if node, _, ok := m.currentSidebarNode(); ok && node.synthetic {
					if m.expandCurrentSection() {
						return m, nil
					}
					if m.focusFirstChildSection() {
						return m, nil
					}
				}
				m.focusFields()
				return m, nil
			}
			if f := m.currentField(); f != nil {
				m.editInput.SetValue(f.Value)
				m.editInput.Focus()
				m.editing = true
			}
		case " ":
			if f := m.currentField(); m.fieldsFocused() && f != nil && f.Type == SettingsFieldBool {
				if parseBool(f.Value) {
					f.Value = "false"
				} else {
					f.Value = "true"
				}
				f.Dirty = true
				m.dirty = true
			}
		case "r":
			m.revealSecrets = !m.revealSecrets
		case "s":
			return m, m.saveCmd()
		case "a":
			return m, m.applyCmd(svcs)
		case "t":
			return m, m.testProviderCmd()
		case "g":
			return m, m.loginProviderCmd(svcs)
		case "esc":
			if m.fieldsFocused() {
				m.focusSections()
				return m, nil
			}
			return m, func() tea.Msg { return CloseOverlayMsg{} }
		}
	case SettingsSavedMsg:
		m.rawContent = msg.Raw
		m.statusText = msg.Message
		m.errorText = ""
		m.dirty = false
	case SettingsAppliedMsg:
		m.statusText = msg.Message
		m.errorText = ""
		m.SetSnapshot(msg.Reload.SettingsData)
	case SettingsProviderTestedMsg:
		m.providerStatus[msg.Provider] = msg.Status
		m.statusText = msg.Provider + " connection verified"
		m.errorText = ""
	case SettingsSectionPatchedMsg:
		for i := range m.sections {
			if m.sections[i].ID == msg.SectionID {
				m.sections[i] = msg.Section
				break
			}
		}
		m.dirty = true
		m.statusText = msg.Message
	case ErrMsg:
		m.errorText = msg.Err.Error()
	}
	return m, nil
}

func (m SettingsPage) saveCmd() tea.Cmd {
	return func() tea.Msg {
		raw, _, err := m.service.Serialize(m.sections)
		if err != nil {
			return ErrMsg{Err: err}
		}
		if err := m.service.SaveRaw(raw); err != nil {
			return ErrMsg{Err: err}
		}
		return SettingsSavedMsg{Raw: raw, Message: "Settings saved"}
	}
}

func (m SettingsPage) applyCmd(svcs Services) tea.Cmd {
	return func() tea.Msg {
		raw, _, err := m.service.Serialize(m.sections)
		if err != nil {
			return ErrMsg{Err: err}
		}
		result, err := m.service.Apply(context.Background(), raw, svcs)
		if err != nil {
			return ErrMsg{Err: err}
		}
		return SettingsAppliedMsg{Reload: result.Services, Message: result.Message}
	}
}

func (m SettingsPage) testProviderCmd() tea.Cmd {
	provider := providerForSection(m.currentSection())
	if provider == "" {
		return nil
	}
	return func() tea.Msg {
		status, err := m.service.TestProvider(context.Background(), provider, m.sections)
		if err != nil {
			return ErrMsg{Err: err}
		}
		return SettingsProviderTestedMsg{Provider: provider, Status: status}
	}
}

func (m SettingsPage) loginProviderCmd(svcs Services) tea.Cmd {
	provider := providerForSection(m.currentSection())
	if provider == "" {
		return nil
	}
	harness := harnessForProvider(provider)
	return func() tea.Msg {
		section, err := m.service.LoginProvider(context.Background(), provider, harness, m.sections, svcs)
		if err != nil {
			return ErrMsg{Err: err}
		}
		return SettingsSectionPatchedMsg{SectionID: section.ID, Section: section, Message: fmt.Sprintf("%s login complete", strings.Title(provider))}
	}
}

func (m SettingsPage) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}

	leftWidth, mainWidth, bodyHeight, _ := m.layoutMetrics()
	bodyParts := []string{m.renderSidebarPane(leftWidth, bodyHeight)}
	if spacerWidth := m.layoutSpacerWidth(); spacerWidth > 0 {
		bodyParts = append(bodyParts, strings.Repeat(" ", spacerWidth))
	}
	bodyParts = append(bodyParts, m.renderMainPane(mainWidth, bodyHeight))
	body := lipgloss.JoinHorizontal(lipgloss.Top, bodyParts...)
	footer := lipgloss.NewStyle().
		Width(max(1, m.width-2)).
		BorderTop(true).
		BorderForeground(lipgloss.Color("#334155")).
		Padding(0, 1).
		Render(m.styles.Muted.Render(m.footerText()))
	return lipgloss.JoinVertical(lipgloss.Left, body, footer)
}

func (m SettingsPage) sidebarBorderColor() lipgloss.Color {
	if m.focus == settingsFocusSections && !m.editing {
		return lipgloss.Color("#60a5fa")
	}
	return lipgloss.Color("#334155")
}

func (m SettingsPage) mainBorderColor() lipgloss.Color {
	if m.fieldsFocused() || m.editing {
		return lipgloss.Color("#60a5fa")
	}
	return lipgloss.Color("#334155")
}

func (m SettingsPage) renderSidebarPane(width int, height int) string {
	innerWidth := max(1, width-2)
	innerHeight := max(1, height-2)
	content := m.renderSidebarContent(innerWidth, innerHeight)
	return lipgloss.NewStyle().
		Width(innerWidth).
		Height(innerHeight).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.sidebarBorderColor()).
		Render(content)
}

func (m SettingsPage) renderSidebarContent(width int, height int) string {
	nodes := m.visibleNavNodes()
	selectedKey := ""
	if m.focus == settingsFocusSections && !m.editing {
		selectedKey = m.navCursor
	} else if sec := m.currentSection(); sec != nil {
		selectedKey = sec.ID
	}
	lineWidth := max(1, width-2)
	lines := []string{m.styles.Title.Render("Settings"), ""}
	for _, node := range nodes {
		marker := "•"
		if node.hasChildren {
			if node.expanded {
				marker = "▾"
			} else {
				marker = "▸"
			}
		}
		prefix := strings.Repeat("  ", node.depth)
		line := truncate(prefix+marker+" "+node.label, lineWidth)
		style := lipgloss.NewStyle().Width(lineWidth)
		if node.key == selectedKey {
			if m.focus == settingsFocusSections && !m.editing {
				style = style.Background(lipgloss.Color("#1e293b")).Foreground(lipgloss.Color("#f8fafc")).Bold(true)
			} else {
				style = style.Background(lipgloss.Color("#122033")).Foreground(lipgloss.Color("#dbeafe")).Bold(true)
			}
		} else {
			style = style.Foreground(lipgloss.Color("#94a3b8"))
		}
		lines = append(lines, style.Render(line))
	}
	return lipgloss.NewStyle().Padding(1).Width(max(1, width-2)).Height(max(1, height-2)).Render(strings.Join(lines, "\n"))
}

func (m SettingsPage) sidebarLabel(sectionIndex int) string {
	section := m.sections[sectionIndex]
	if idx := strings.LastIndex(section.Title, "· "); idx >= 0 {
		return strings.TrimSpace(section.Title[idx+len("· "):])
	}
	return section.Title
}

func (m SettingsPage) renderMainPane(width int, height int) string {
	innerWidth := max(1, width-2)
	innerHeight := max(1, height-2)
	contentWidth, contentHeight, detailHeight := m.mainViewportSize()
	if contentWidth > innerWidth-2 {
		contentWidth = max(1, innerWidth-2)
	}
	vp := m.configuredMainViewport(contentWidth, contentHeight)
	header := m.renderStickySectionHeader(innerWidth)
	bodyParts := []string{
		header,
		m.renderViewportWithScrollbar(vp, innerWidth, contentHeight),
	}
	if detail := m.renderStickyFieldDetails(innerWidth, detailHeight); detail != "" {
		bodyParts = append(bodyParts, detail)
	}
	body := lipgloss.JoinVertical(lipgloss.Left, bodyParts...)
	return lipgloss.NewStyle().
		Width(innerWidth).
		Height(innerHeight).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.mainBorderColor()).
		Render(body)
}

func (m SettingsPage) configuredMainViewport(width int, height int) viewport.Model {
	vp := m.mainViewport
	vp.Width = width
	vp.Height = height
	content, sectionAnchors, fieldAnchors := m.buildMainDocument(width)
	vp.SetContent(content)
	anchor := m.selectedDocumentAnchor(sectionAnchors, fieldAnchors)
	top := vp.YOffset
	bottom := top + vp.Height - 1
	margin := 1
	if anchor < top+margin {
		vp.SetYOffset(max(0, anchor-margin))
	} else if anchor > bottom-margin {
		vp.SetYOffset(max(0, anchor-vp.Height+1+margin))
	}
	return vp
}

func (m SettingsPage) renderViewportWithScrollbar(vp viewport.Model, width int, height int) string {
	if width <= 2 {
		return lipgloss.NewStyle().Width(max(1, width)).Height(height).Render(vp.View())
	}
	contentWidth := max(1, width-2)
	content := lipgloss.NewStyle().Width(contentWidth).Height(height).Render(vp.View())
	scrollbar := m.renderMainScrollbar(vp, height)
	return lipgloss.JoinHorizontal(lipgloss.Top, content, " ", scrollbar)
}

func (m SettingsPage) renderMainScrollbar(vp viewport.Model, height int) string {
	if height <= 0 {
		return ""
	}
	lines := make([]string, height)
	trackStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#64748b"))
	thumbColor := lipgloss.Color("#cbd5e1")
	if m.fieldsFocused() || m.editing {
		thumbColor = lipgloss.Color("#60a5fa")
	}
	thumbStyle := lipgloss.NewStyle().Foreground(thumbColor)
	total := vp.TotalLineCount()
	thumbHeight := height
	thumbTop := 0
	if total > height {
		thumbHeight = max(1, (height*height)/max(1, total))
		if thumbHeight > height {
			thumbHeight = height
		}
		thumbRange := max(0, height-thumbHeight)
		scrollRange := max(1, total-height)
		if thumbRange > 0 {
			thumbTop = (vp.YOffset * thumbRange) / scrollRange
		}
	}
	for i := range lines {
		lines[i] = trackStyle.Render("▏")
		if i >= thumbTop && i < thumbTop+thumbHeight {
			lines[i] = thumbStyle.Render("▐")
		}
	}
	return strings.Join(lines, "\n")
}

func (m SettingsPage) sectionDisplayTitle(sectionIndex int) string {
	if m.sectionDepth(sectionIndex) > 0 {
		return m.sidebarLabel(sectionIndex)
	}
	return m.sections[sectionIndex].Title
}

func (m SettingsPage) sectionBreadcrumb(sectionIndex int) string {
	parts := make([]string, 0, 4)
	if group, ok := syntheticGroupForSectionID(m.sections[sectionIndex].ID); ok && len(m.syntheticGroupChildren(group.key)) > 0 {
		parts = append(parts, group.label)
	}
	parents := make([]string, 0, 4)
	for parent := m.sectionParentIndex(sectionIndex); parent >= 0; parent = m.sectionParentIndex(parent) {
		parents = append([]string{m.sidebarLabel(parent)}, parents...)
	}
	parts = append(parts, parents...)
	return strings.Join(parts, " / ")
}

func (m SettingsPage) stickySectionHeaderHeight() int {
	if m.currentSection() != nil && m.sectionBreadcrumb(m.sectionCursor) != "" {
		return 3
	}
	return 2
}

func (m SettingsPage) renderStickySectionHeader(width int) string {
	title := "Settings"
	breadcrumb := ""
	if m.currentSection() != nil {
		title = m.sectionDisplayTitle(m.sectionCursor)
		breadcrumb = m.sectionBreadcrumb(m.sectionCursor)
	}
	lines := []string{
		lipgloss.NewStyle().Width(width).Bold(true).Foreground(lipgloss.Color("#f8fafc")).Render(truncate(title, width)),
	}
	if breadcrumb != "" {
		lines = append(lines, lipgloss.NewStyle().Width(width).Foreground(lipgloss.Color("#93c5fd")).Render(truncate(breadcrumb, width)))
	}
	lines = append(lines, lipgloss.NewStyle().Width(width).Foreground(lipgloss.Color("#334155")).Render(strings.Repeat("─", max(1, width))))
	return strings.Join(lines, "\n")
}

func (m SettingsPage) buildMainDocument(width int) (string, map[int]int, map[string]int) {
	sectionAnchors := make(map[int]int, len(m.sections))
	fieldAnchors := make(map[string]int)
	lines := make([]string, 0, len(m.sections)*10)
	appendRendered := func(rendered string) {
		lines = append(lines, strings.Split(rendered, "\n")...)
	}
	lastSyntheticGroup := ""

	for i, sec := range m.sections {
		depth := m.sectionDepth(i)
		indent := depth * 2

		if group, ok := syntheticGroupForSectionID(sec.ID); ok {
			if group.key != lastSyntheticGroup {
				if len(lines) > 0 {
					lines = append(lines, "", "")
				}
				appendRendered(lipgloss.NewStyle().Width(width).Foreground(lipgloss.Color("#334155")).Render(strings.Repeat("═", max(1, width))))
				appendRendered(lipgloss.NewStyle().Width(width).Bold(true).Foreground(lipgloss.Color("#93c5fd")).Render(group.label))
				appendRendered(lipgloss.NewStyle().Width(width).Foreground(lipgloss.Color("#334155")).Render(strings.Repeat("─", max(1, width))))
				lines = append(lines, "")
				lastSyntheticGroup = group.key
			}
		} else {
			lastSyntheticGroup = ""
		}

		if len(lines) > 0 && lines[len(lines)-1] != "" {
			lines = append(lines, "")
		}
		if depth == 0 {
			appendRendered(lipgloss.NewStyle().Width(width).Foreground(lipgloss.Color("#334155")).Render(strings.Repeat("─", max(1, width))))
		}

		sectionAnchors[i] = len(lines)
		title := truncate(strings.Repeat(" ", indent)+m.sectionDisplayTitle(i), width)
		headerStyle := lipgloss.NewStyle().Width(width).Bold(true).Foreground(lipgloss.Color("#e2e8f0"))
		if depth == 0 {
			headerStyle = headerStyle.Foreground(lipgloss.Color("#f8fafc"))
		}
		if i == m.sectionCursor {
			if m.focus == settingsFocusSections && !m.editing {
				headerStyle = headerStyle.Background(lipgloss.Color("#1e293b")).Foreground(lipgloss.Color("#f8fafc"))
			} else {
				headerStyle = headerStyle.Background(lipgloss.Color("#122033")).Foreground(lipgloss.Color("#dbeafe"))
			}
		}
		appendRendered(headerStyle.Render(title))

		metaPrefix := strings.Repeat(" ", indent+2)
		metaStyle := lipgloss.NewStyle().Width(width).Foreground(lipgloss.Color("#64748b"))
		if sec.Description != "" {
			appendRendered(metaStyle.Render(truncate(metaPrefix+sec.Description, width)))
		}
		appendRendered(metaStyle.Render(truncate(metaPrefix+"Section status: "+sec.Status, width)))
		if provider := providerForSection(&sec); provider != "" {
			if st, ok := m.providerStatus[provider]; ok {
				appendRendered(metaStyle.Render(truncate(metaPrefix+"Provider auth: "+providerStatusLine(st), width)))
				if st.Description != "" {
					appendRendered(metaStyle.Render(truncate(metaPrefix+st.Description, width)))
				}
			}
		}
		lines = append(lines, "")

		contentWidth := max(1, width-(indent+2))
		for j, field := range sec.Fields {
			fieldAnchors[settingsFieldAnchorKey(i, j)] = len(lines)
			label := field.Label
			if field.Required {
				label += " *"
			}
			value := m.displayFieldValue(field)
			rowStyle := lipgloss.NewStyle().Width(width)
			if i == m.sectionCursor && j == m.fieldCursor {
				if m.fieldsFocused() || m.editing {
					rowStyle = rowStyle.Background(lipgloss.Color("#16314f")).Foreground(lipgloss.Color("#f8fafc")).Bold(true)
				} else {
					rowStyle = rowStyle.Background(lipgloss.Color("#122033")).Foreground(lipgloss.Color("#dbeafe"))
				}
			} else {
				rowStyle = rowStyle.Foreground(lipgloss.Color("#94a3b8"))
			}
			if contentWidth < 18 {
				rowLine := ansi.Truncate(strings.Repeat(" ", indent+2)+label+": "+value, width, "…")
				appendRendered(rowStyle.Render(rowLine))
				continue
			}
			labelWidth := min(26, max(8, contentWidth/3))
			if labelWidth >= contentWidth {
				labelWidth = max(1, contentWidth-1)
			}
			valueWidth := max(1, contentWidth-labelWidth)
			row := lipgloss.JoinHorizontal(lipgloss.Top,
				lipgloss.NewStyle().Width(labelWidth).Foreground(lipgloss.Color("#cbd5e1")).Render(truncate(label, labelWidth)),
				lipgloss.NewStyle().Width(valueWidth).Render(ansi.Truncate(value, valueWidth, "…")),
			)
			rowLine := ansi.Truncate(strings.Repeat(" ", indent+2)+row, width, "…")
			appendRendered(rowStyle.Render(rowLine))
		}
	}

	return strings.Join(lines, "\n"), sectionAnchors, fieldAnchors
}

func (m SettingsPage) displayFieldValue(field SettingsField) string {
	value := field.Value
	if field.Sensitive && !m.revealSecrets && value != "" {
		value = strings.Repeat("•", min(8, len([]rune(value))))
	}
	if value == "" {
		return m.styles.Muted.Render("<empty>")
	}
	return value
}

func (m SettingsPage) renderStickyFieldDetails(width int, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	field := m.currentField()
	lines := []string{}
	if field == nil {
		lines = append(lines, m.styles.Subtitle.Render("Setting details"), m.styles.Muted.Render("Select a setting to inspect its explanation and default."))
	} else {
		lines = append(lines, m.styles.Subtitle.Render(field.Label))
		if field.Description != "" {
			lines = append(lines, field.Description)
		}
		if field.DefaultValue != "" {
			lines = append(lines, m.styles.Muted.Render("Default: "+field.DefaultValue))
		}
		lines = append(lines, m.styles.Muted.Render("Current: "+m.displayFieldValue(*field)))
		if len(field.Options) > 0 {
			lines = append(lines, m.styles.Muted.Render("Options: "+strings.Join(field.Options, ", ")))
		}
		if field.Status != "" {
			lines = append(lines, m.styles.Muted.Render("Status: "+field.Status))
		}
		if field.Error != "" {
			lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("#f87171")).Render("Field error: "+field.Error))
		}
	}
	return lipgloss.NewStyle().
		Width(max(1, width-4)).
		Height(max(1, height-2)).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#334155")).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
}

func (m SettingsPage) footerText() string {
	if m.editing {
		return "[enter] save edit  [esc] cancel edit"
	}
	if m.fieldsFocused() {
		return "[↑↓] settings  [enter] edit  [space] toggle bool  [left/esc] groups  [s] save  [a] apply  [t] test  [g] login  [r] reveal"
	}
	return "[↑↓] navigate tree  [→] expand/open  [←] collapse/up  [enter] focus settings  [esc] close  [s] save  [a] apply  [t] test  [g] login  [r] reveal"
}

func providerForSection(section *SettingsSection) string {
	if section == nil {
		return ""
	}
	switch section.ID {
	case "provider.linear":
		return "linear"
	case "provider.gitlab":
		return "gitlab"
	case "provider.github":
		return "github"
	default:
		return ""
	}
}

func harnessForProvider(provider string) string {
	switch provider {
	case "github":
		return "gh-cli"
	default:
		return ""
	}
}

func providerStatusLine(status ProviderStatus) string {
	parts := []string{status.AuthSource}
	if status.Configured {
		parts = append(parts, "configured")
	} else {
		parts = append(parts, "unconfigured")
	}
	if status.Connected {
		parts = append(parts, "connected")
	}
	if status.LastError != "" {
		parts = append(parts, "error: "+status.LastError)
	}
	return strings.Join(parts, " · ")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
