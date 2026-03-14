package views

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type settingsFocus int

const (
	settingsFocusSections settingsFocus = iota
	settingsFocusFields
)

type settingsEditMode int

const (
	settingsEditModeText settingsEditMode = iota
	settingsEditModeSelect
)

type settingsEditOption struct {
	Label string
	Value string
}

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

type SettingsLoginCompletedMsg struct {
	Snapshot SettingsSnapshot
	Message  string
	Dirty    bool
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
	service            *SettingsService
	sections           []SettingsSection
	providerStatus     map[string]ProviderStatus
	rawContent         string
	active             bool
	width              int
	height             int
	sectionCursor      int
	fieldCursor        int
	navCursor          string
	focus              settingsFocus
	expandedSections   map[string]bool
	mainViewport       viewport.Model
	mainDocWidth       int
	mainDocRevision    int
	mainDocContentKey  string
	mainSectionAnchors map[int]int
	mainFieldAnchors   map[string]int
	editing            bool
	editMode           settingsEditMode
	editOptions        []settingsEditOption
	editOptionCursor   int
	revealSecrets      bool
	dirty              bool
	editInput          textinput.Model
	styles             styles.Styles
	errorText          string
	statusText         string
	warningText        string
}

func NewSettingsPage(svc *SettingsService, snapshot SettingsSnapshot, st styles.Styles) SettingsPage {
	ti := textinput.New()
	ti.CharLimit = 1000
	ti.Prompt = ""
	vp := viewport.New(0, 0)
	return SettingsPage{
		service:          svc,
		sections:         snapshot.Sections,
		providerStatus:   snapshot.Providers,
		rawContent:       snapshot.RawYAML,
		focus:            settingsFocusSections,
		expandedSections: defaultExpandedSections(snapshot.Sections),
		mainViewport:     vp,
		mainDocWidth:     -1,
		editInput:        ti,
		styles:           st,
		warningText:      snapshot.HarnessWarning,
	}
}

func (m *SettingsPage) Open() {
	m.active = true
	m.focusSections()
	m.closeFieldEditor()
	m.clampCursor()
	m.syncMainViewport()
}

func (m *SettingsPage) Close() {
	m.active = false
	m.focusSections()
	m.closeFieldEditor()
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
	m.rawContent = snapshot.RawYAML
	m.dirty = false
	m.errorText = ""
	m.statusText = ""
	m.warningText = snapshot.HarnessWarning
	m.closeFieldEditor()
	m.expandedSections = defaultExpandedSections(snapshot.Sections)
	m.invalidateMainDocument()
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

func (m *SettingsPage) closeFieldEditor() {
	m.editing = false
	m.editMode = settingsEditModeText
	m.editOptions = nil
	m.editOptionCursor = 0
	m.editInput.Blur()
}

func (m *SettingsPage) fieldEditOptions(field *SettingsField) []settingsEditOption {
	if field == nil {
		return nil
	}
	if field.Type == SettingsFieldBool {
		return []settingsEditOption{{Label: "Enabled", Value: "true"}, {Label: "Disabled", Value: "false"}}
	}
	if len(field.Options) == 0 {
		return nil
	}
	options := make([]settingsEditOption, 0, len(field.Options))
	for _, option := range field.Options {
		options = append(options, settingsEditOption{Label: displaySettingsOption(field, option), Value: option})
	}
	return options
}

func displaySettingsOption(field *SettingsField, value string) string {
	if field == nil {
		return value
	}
	switch field.Section {
	case "harness", "harness.phase":
		switch config.HarnessName(value) {
		case config.HarnessOhMyPi:
			return "Oh My Pi"
		case config.HarnessClaudeCode:
			return "Claude Code"
		case config.HarnessCodex:
			return "Codex"
		}
	}
	return value
}

func (m *SettingsPage) openFieldEditor() {
	field := m.currentField()
	if field == nil {
		return
	}
	m.errorText = ""
	if options := m.fieldEditOptions(field); len(options) > 0 {
		m.editMode = settingsEditModeSelect
		m.editOptions = options
		m.editOptionCursor = 0
		for i, option := range options {
			if option.Value == field.Value {
				m.editOptionCursor = i
				break
			}
		}
		m.editing = true
		return
	}
	m.editMode = settingsEditModeText
	m.editOptions = nil
	m.editOptionCursor = 0
	m.editInput.SetValue(field.Value)
	m.editInput.SetCursor(len([]rune(field.Value)))
	m.editInput.Focus()
	m.editing = true
}

func (m *SettingsPage) commitFieldEditor() {
	field := m.currentField()
	if field == nil {
		m.closeFieldEditor()
		return
	}
	if m.editMode == settingsEditModeSelect {
		if len(m.editOptions) == 0 || m.editOptionCursor < 0 || m.editOptionCursor >= len(m.editOptions) {
			m.closeFieldEditor()
			return
		}
		field.Value = m.editOptions[m.editOptionCursor].Value
	} else {
		field.Value = m.editInput.Value()
	}
	field.Dirty = true
	m.dirty = true
	m.statusText = "Field updated"
	m.invalidateMainDocument()
	m.closeFieldEditor()
}

func (m *SettingsPage) cycleEditOption(delta int) {
	if len(m.editOptions) == 0 {
		m.editOptionCursor = 0
		return
	}
	m.editOptionCursor = (m.editOptionCursor + delta + len(m.editOptions)) % len(m.editOptions)
}

func (m *SettingsPage) updateFieldEditor(msg tea.KeyMsg) tea.Cmd {
	if m.editMode == settingsEditModeSelect {
		switch msg.String() {
		case "up", "k", "shift+tab", "left", "h":
			m.cycleEditOption(-1)
			return nil
		case "down", "j", "tab", "right", "l":
			m.cycleEditOption(1)
			return nil
		case "enter":
			m.commitFieldEditor()
			return nil
		case "esc":
			m.closeFieldEditor()
			return nil
		default:
			return nil
		}
	}
	switch msg.String() {
	case "enter":
		m.commitFieldEditor()
		return nil
	case "esc":
		m.closeFieldEditor()
		return nil
	default:
		var cmd tea.Cmd
		m.editInput, cmd = m.editInput.Update(msg)
		return cmd
	}
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
	contentWidth := max(1, innerWidth-2)
	innerHeight := max(1, bodyHeight-2)
	headerHeight := m.stickySectionHeaderHeight()
	if headerHeight >= innerHeight {
		headerHeight = max(1, innerHeight-1)
	}
	remainingHeight := max(1, innerHeight-headerHeight)
	minViewportHeight := min(3, remainingHeight)
	detailHeight = min(desiredDetailHeight, max(0, remainingHeight-minViewportHeight))
	width = max(1, contentWidth)
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

func (m *SettingsPage) invalidateMainDocument() {
	m.mainDocRevision++
	m.mainDocWidth = -1
	m.mainDocContentKey = ""
	m.mainSectionAnchors = nil
	m.mainFieldAnchors = nil
}

func (m SettingsPage) mainDocumentContentKey(width int) string {
	return fmt.Sprintf("%d|%d|%d|%d|%d|%t|%t", width, m.mainDocRevision, m.sectionCursor, m.fieldCursor, m.focus, m.editing, m.revealSecrets)
}

func (m *SettingsPage) preparedMainViewport(width int, height int, alignSelection bool) viewport.Model {
	vp := m.mainViewport
	vp.Width = width
	vp.Height = height
	sectionAnchors := m.mainSectionAnchors
	fieldAnchors := m.mainFieldAnchors
	contentKey := m.mainDocumentContentKey(width)
	if m.mainDocWidth != width || m.mainDocContentKey != contentKey || sectionAnchors == nil || fieldAnchors == nil || vp.TotalLineCount() == 0 {
		content, builtSectionAnchors, builtFieldAnchors := m.buildMainDocument(width)
		vp.SetContent(content)
		sectionAnchors = builtSectionAnchors
		fieldAnchors = builtFieldAnchors
		m.mainDocWidth = width
		m.mainDocContentKey = contentKey
		m.mainSectionAnchors = sectionAnchors
		m.mainFieldAnchors = fieldAnchors
	}
	vp.SetYOffset(vp.YOffset)
	if alignSelection && vp.Height > 0 {
		anchor := m.selectedDocumentAnchor(sectionAnchors, fieldAnchors)
		top := vp.YOffset
		bottom := top + vp.Height - 1
		margin := 0
		if vp.Height > 2 {
			margin = 1
		}
		if anchor < top+margin {
			vp.SetYOffset(max(0, anchor-margin))
		} else if anchor > bottom-margin {
			vp.SetYOffset(max(0, anchor-vp.Height+1+margin))
		}
	}
	vp.SetYOffset(vp.YOffset)
	return vp
}

func (m *SettingsPage) syncMainViewport() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	viewportWidth, viewportHeight, _ := m.mainViewportSize()
	m.mainViewport = m.preparedMainViewport(viewportWidth, viewportHeight, true)
}

func (m *SettingsPage) returnWithSyncedMainViewport(cmd tea.Cmd) (SettingsPage, tea.Cmd) {
	m.syncMainViewport()
	return *m, cmd
}

func (m SettingsPage) wheelAtViewportEdge(direction int) bool {
	if direction == 0 || m.mainViewport.Height <= 0 {
		return false
	}
	maxOffset := max(0, m.mainViewport.TotalLineCount()-m.mainViewport.Height)
	switch {
	case direction < 0:
		return m.mainViewport.YOffset <= 0
	case direction > 0:
		return m.mainViewport.YOffset >= maxOffset
	default:
		return false
	}
}

func (m *SettingsPage) syncFieldSelectionToScroll(fieldAnchors map[string]int, top int, bottom int, direction int) bool {
	if !m.fieldsFocused() || bottom < top || direction == 0 {
		return false
	}
	type visibleField struct {
		section int
		field   int
	}
	visible := make([]visibleField, 0, 8)
	currentSection, currentField := m.sectionCursor, m.fieldCursor
	above := visibleField{section: -1, field: -1}
	below := visibleField{section: -1, field: -1}
	for sectionIndex, sec := range m.sections {
		for fieldIndex := range sec.Fields {
			anchor, ok := fieldAnchors[settingsFieldAnchorKey(sectionIndex, fieldIndex)]
			if !ok {
				continue
			}
			field := visibleField{section: sectionIndex, field: fieldIndex}
			switch {
			case anchor < top:
				above = field
			case anchor > bottom:
				if below.section == -1 {
					below = field
				}
			default:
				visible = append(visible, field)
			}
		}
	}
	choose := func(field visibleField) {
		m.sectionCursor = field.section
		m.fieldCursor = field.field
		m.navCursor = m.sections[field.section].ID
	}
	if len(visible) == 0 {
		if direction > 0 && below.section >= 0 {
			choose(below)
		}
		if direction < 0 && above.section >= 0 {
			choose(above)
		}
		return false
	}
	chosen := visible[0]
	if direction < 0 {
		chosen = visible[len(visible)-1]
	}
	if chosen.section == currentSection && chosen.field == currentField {
		if direction > 0 {
			if len(visible) > 1 {
				chosen = visible[1]
			} else if below.section >= 0 {
				choose(below)
				return false
			}
		} else {
			if len(visible) > 1 {
				chosen = visible[len(visible)-2]
			} else if above.section >= 0 {
				choose(above)
				return false
			}
		}
	}
	choose(chosen)
	return true
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
	if mouseMsg, ok := msg.(tea.MouseMsg); ok {
		if m.editing || m.width <= 0 || m.height <= 0 {
			return m, nil
		}
		if mouseMsg.Action != tea.MouseActionPress {
			return m, nil
		}
		direction := 0
		switch mouseMsg.Button {
		case tea.MouseButtonWheelDown:
			direction = 1
		case tea.MouseButtonWheelUp:
			direction = -1
		default:
			return m, nil
		}
		if !m.fieldsFocused() {
			previousSection := m.sectionCursor
			previousField := m.fieldCursor
			previousNav := m.navCursor
			m.moveSection(direction)
			if m.sectionCursor == previousSection && m.fieldCursor == previousField && m.navCursor == previousNav {
				return m, nil
			}
			m.syncMainViewport()
			return m, nil
		}
		viewportWidth, viewportHeight, _ := m.mainViewportSize()
		if viewportWidth <= 0 || viewportHeight <= 0 {
			return m, nil
		}
		if m.mainViewport.Width != viewportWidth || m.mainViewport.Height != viewportHeight || m.mainViewport.TotalLineCount() == 0 || m.mainSectionAnchors == nil || m.mainFieldAnchors == nil {
			m.mainViewport = m.preparedMainViewport(viewportWidth, viewportHeight, false)
		}
		if m.wheelAtViewportEdge(direction) {
			return m, nil
		}
		previousOffset := m.mainViewport.YOffset
		step := max(1, m.mainViewport.MouseWheelDelta)
		if direction > 0 {
			m.mainViewport.ScrollDown(step)
		} else {
			m.mainViewport.ScrollUp(step)
		}
		if m.mainViewport.YOffset == previousOffset {
			return m, nil
		}
		top := m.mainViewport.YOffset
		bottom := top + m.mainViewport.Height - 1
		if m.mainFieldAnchors != nil {
			m.syncFieldSelectionToScroll(m.mainFieldAnchors, top, bottom, direction)
		}
		m.mainViewport = m.preparedMainViewport(viewportWidth, viewportHeight, false)
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.editing {
			return m.returnWithSyncedMainViewport(m.updateFieldEditor(msg))
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
						return m.returnWithSyncedMainViewport(nil)
					}
					if m.focusFirstChildSection() {
						return m.returnWithSyncedMainViewport(nil)
					}
				}
				m.focusFields()
				return m.returnWithSyncedMainViewport(nil)
			}
			m.openFieldEditor()
		case "e":
			if m.fieldsFocused() {
				m.openFieldEditor()
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
				m.invalidateMainDocument()
			}
		case "r":
			m.revealSecrets = !m.revealSecrets
		case "s":
			return m.returnWithSyncedMainViewport(m.saveCmd())
		case "a":
			return m.returnWithSyncedMainViewport(m.applyCmd(svcs))
		case "t":
			return m.returnWithSyncedMainViewport(m.testProviderCmd())
		case "g":
			return m.returnWithSyncedMainViewport(m.loginProviderCmd(svcs))
		case "esc":
			if m.fieldsFocused() {
				m.focusSections()
				return m.returnWithSyncedMainViewport(nil)
			}
			return m.returnWithSyncedMainViewport(func() tea.Msg { return CloseOverlayMsg{} })
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
		m.invalidateMainDocument()
		m.statusText = msg.Provider + " connection verified"
		m.errorText = ""
	case SettingsLoginCompletedMsg:
		wasDirty := m.dirty
		expanded := make(map[string]bool, len(m.expandedSections))
		for key, value := range m.expandedSections {
			expanded[key] = value
		}
		snapshot := msg.Snapshot
		for name, status := range snapshot.Providers {
			if prior, ok := m.providerStatus[name]; ok {
				status.Connected = prior.Connected
				status.LastError = prior.LastError
				snapshot.Providers[name] = status
			}
		}
		m.SetSnapshot(snapshot)
		for key, value := range expanded {
			if _, ok := m.expandedSections[key]; ok {
				m.expandedSections[key] = value
			}
		}
		m.invalidateMainDocument()
		m.clampCursor()
		m.syncMainViewport()
		m.dirty = wasDirty || msg.Dirty
		m.statusText = msg.Message
		m.errorText = ""
	case ErrMsg:
		m.errorText = msg.Err.Error()
	}
	return m.returnWithSyncedMainViewport(nil)
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
	if !providerSupportsLogin(provider) {
		return nil
	}
	if provider == "sentry" {
		cfg, err := configFromSections(m.sections)
		if err != nil {
			return func() tea.Msg { return ErrMsg{Err: err} }
		}
		inputs := providerLoginInputs(cfg, provider)
		cmd := exec.Command("sentry", "auth", "login")
		cmd.Env = config.SentryCLIEnvironment(inputs["base_url"])
		return tea.ExecProcess(cmd, func(err error) tea.Msg {
			if err != nil {
				return ErrMsg{Err: fmt.Errorf("sentry auth login: %w", err)}
			}
			snapshotCfg, cfgErr := configFromSections(m.sections)
			if cfgErr != nil {
				return ErrMsg{Err: cfgErr}
			}
			snapshot := settingsSnapshotFromConfig(snapshotCfg)
			return SettingsLoginCompletedMsg{Snapshot: snapshot, Message: "sentry login complete", Dirty: false}
		})
	}
	harness := harnessForProvider(provider)
	return func() tea.Msg {
		result, err := m.service.LoginProvider(context.Background(), provider, harness, m.sections, svcs)
		if err != nil {
			return ErrMsg{Err: err}
		}
		return SettingsLoginCompletedMsg{Snapshot: result.Snapshot, Message: result.Message, Dirty: result.Dirty}
	}
}

func (m SettingsPage) editModalFooterText() string {
	if m.editMode == settingsEditModeSelect {
		return "[↑↓] choose  [enter] select  [esc] cancel"
	}
	return "[enter] save  [esc] cancel"
}

func (m SettingsPage) renderEditModal() string {
	field := m.currentField()
	if field == nil {
		return ""
	}
	frameWidth := min(96, max(24, m.width-4))
	if m.width > 0 {
		frameWidth = min(frameWidth, m.width)
	}
	contentWidth := m.styles.Chrome.OverlayFrame.InnerWidth(max(1, frameWidth))
	header := []string{m.styles.Title.Render(field.Label)}
	if field.Description != "" {
		header = append(header, m.styles.Muted.Render(truncate(field.Description, contentWidth)))
	}
	body := m.renderTextEditBody(contentWidth, field)
	if m.editMode == settingsEditModeSelect {
		body = m.renderSelectEditBody(contentWidth, field)
	}
	footer := m.styles.Hint.Render(truncate(m.editModalFooterText(), contentWidth))
	return components.RenderOverlayFrame(m.styles, frameWidth, components.OverlayFrameSpec{
		HeaderLines: header,
		Body:        body,
		Footer:      footer,
		Focused:     true,
	})
}

func (m SettingsPage) renderTextEditBody(width int, field *SettingsField) string {
	input := m.editInput
	input.Width = max(1, width-4)
	lines := []string{
		m.styles.Subtitle.Render("Value"),
		input.View(),
	}
	if field != nil && field.DefaultValue != "" {
		lines = append(lines, "", m.styles.Muted.Render(truncate("Default: "+field.DefaultValue, width)))
	}
	return strings.Join(lines, "\n")
}

func (m SettingsPage) renderSelectEditBody(width int, field *SettingsField) string {
	lines := make([]string, 0, len(m.editOptions)+2)
	for i, option := range m.editOptions {
		line := truncate(option.Label, max(1, width-2))
		if i == m.editOptionCursor {
			lines = append(lines, m.styles.Accent.Render("› "+line))
			continue
		}
		lines = append(lines, m.styles.Subtitle.Render("  "+line))
	}
	if field != nil && field.DefaultValue != "" {
		lines = append(lines, "", m.styles.Muted.Render(truncate("Default: "+field.DefaultValue, width)))
	}
	return strings.Join(lines, "\n")
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
	footerText := truncate(m.footerText(), max(1, m.width-4))
	footer := lipgloss.NewStyle().
		Width(max(1, m.width-2)).
		BorderTop(true).
		BorderForeground(lipgloss.Color(m.styles.Theme.PaneBorder)).
		Padding(0, 1).
		Render(m.styles.Hint.Render(footerText))
	base := lipgloss.JoinVertical(lipgloss.Left, body, footer)
	if m.editing {
		if modal := m.renderEditModal(); modal != "" {
			return renderCenteredOverlay(base, modal, m.width, m.height)
		}
	}
	return base
}

func (m SettingsPage) sidebarBorderColor() lipgloss.Color {
	if m.focus == settingsFocusSections && !m.editing {
		return lipgloss.Color(m.styles.Theme.PaneBorderFocused)
	}
	return lipgloss.Color(m.styles.Theme.PaneBorder)
}

func (m SettingsPage) mainBorderColor() lipgloss.Color {
	if m.fieldsFocused() || m.editing {
		return lipgloss.Color(m.styles.Theme.PaneBorderFocused)
	}
	return lipgloss.Color(m.styles.Theme.PaneBorder)
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
				style = m.styles.SettingsSelectionActive.Copy().Width(lineWidth)
			} else {
				style = m.styles.SettingsSelectionInactive.Copy().Width(lineWidth)
			}
		} else {
			style = m.styles.Label.Copy().Width(lineWidth)
		}
		lines = append(lines, style.Render(line))
	}
	bodyWidth := max(1, width-2)
	bodyHeight := max(1, height-2)
	content := fitViewBox(strings.Join(lines, "\n"), bodyWidth, bodyHeight)
	return lipgloss.NewStyle().Padding(1).Render(content)
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
	contentAreaWidth := max(1, innerWidth-2)
	contentWidth, contentHeight, detailHeight := m.mainViewportSize()
	if contentWidth > max(1, contentAreaWidth-2) {
		contentWidth = max(1, contentAreaWidth-2)
	}
	vp := m.mainViewport
	if vp.Width != contentWidth || vp.Height != contentHeight || vp.TotalLineCount() == 0 {
		vp = m.preparedMainViewport(contentWidth, contentHeight, false)
	}
	header := m.renderStickySectionHeader(contentAreaWidth)
	bodyParts := []string{
		header,
		m.renderViewportWithScrollbar(vp, contentAreaWidth, contentHeight),
	}
	if detail := m.renderStickyFieldDetails(contentAreaWidth, detailHeight); detail != "" {
		bodyParts = append(bodyParts, detail)
	}
	body := fitViewBox(lipgloss.JoinVertical(lipgloss.Left, bodyParts...), contentAreaWidth, innerHeight)
	body = lipgloss.NewStyle().Padding(0, 1).Width(innerWidth).Height(innerHeight).Render(body)
	return lipgloss.NewStyle().
		Width(innerWidth).
		Height(innerHeight).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.mainBorderColor()).
		Render(body)
}

func (m SettingsPage) configuredMainViewport(width int, height int) viewport.Model {
	return m.preparedMainViewport(width, height, true)
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
	trackStyle := m.styles.ScrollbarTrack
	thumbStyle := m.styles.ScrollbarThumb
	if m.fieldsFocused() || m.editing {
		thumbStyle = m.styles.ScrollbarThumbFocused
	}
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
			thumbTop = (vp.YOffset*thumbRange + scrollRange/2) / scrollRange
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
		m.styles.SettingsTextStrong.Copy().Width(width).Render(truncate(title, width)),
	}
	if breadcrumb != "" {
		lines = append(lines, m.styles.SettingsBreadcrumb.Copy().Width(width).Render(truncate(breadcrumb, width)))
	}
	lines = append(lines, m.styles.Divider.Copy().Width(width).Render(strings.Repeat("─", max(1, width))))
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
				appendRendered(m.styles.Divider.Copy().Width(width).Render(strings.Repeat("═", max(1, width))))
				appendRendered(m.styles.SettingsSection.Copy().Width(width).Render(group.label))
				appendRendered(m.styles.Divider.Copy().Width(width).Render(strings.Repeat("─", max(1, width))))
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
			appendRendered(m.styles.Divider.Copy().Width(width).Render(strings.Repeat("─", max(1, width))))
		}

		sectionAnchors[i] = len(lines)
		title := truncate(strings.Repeat(" ", indent)+m.sectionDisplayTitle(i), width)
		headerStyle := m.styles.SettingsText.Copy().Width(width).Bold(true)
		if depth == 0 {
			headerStyle = m.styles.SettingsTextStrong.Copy().Width(width)
		}
		if i == m.sectionCursor {
			if m.focus == settingsFocusSections && !m.editing {
				headerStyle = m.styles.SettingsSelectionActive.Copy().Width(width)
			} else {
				headerStyle = m.styles.SettingsSelectionInactive.Copy().Width(width)
			}
		}
		appendRendered(headerStyle.Render(title))

		metaPrefix := strings.Repeat(" ", indent+2)
		metaStyle := m.styles.Muted.Copy().Width(width)
		if sec.Description != "" {
			appendRendered(metaStyle.Render(truncate(metaPrefix+sec.Description, width)))
		}
		appendRendered(metaStyle.Render(truncate(metaPrefix+"Section status: "+sec.Status, width)))
		if sec.Error != "" {
			errorStyle := m.styles.Error.Copy().Width(width)
			for _, line := range strings.Split(sec.Error, "\n") {
				appendRendered(errorStyle.Render(truncate(metaPrefix+line, width)))
			}
		}
		if provider := providerForSection(&sec); provider != "" {
			if st, ok := m.providerStatus[provider]; ok {
				appendRendered(m.renderProviderStatusLine(metaPrefix, st, width))
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
					rowStyle = m.styles.SettingsSelectionActive.Copy().Width(width)
				} else {
					rowStyle = m.styles.SettingsSelectionInactive.Copy().Width(width)
				}
			} else {
				rowStyle = m.styles.Label.Copy().Width(width)
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
				m.styles.SettingsText.Copy().Width(labelWidth).Render(truncate(label, labelWidth)),
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
			lines = append(lines, m.styles.Error.Render("Field error: "+field.Error))
		}
	}
	innerWidth := m.styles.Chrome.Callout.InnerWidth(max(1, width))
	innerHeight := m.styles.Chrome.Callout.InnerHeight(max(1, height))
	content := fitViewBox(strings.Join(lines, "\n"), max(1, innerWidth), max(1, innerHeight))
	return m.styles.Callout.Copy().
		Width(max(1, innerWidth)).
		Height(max(1, innerHeight)).
		Render(content)
}

func (m SettingsPage) footerText() string {
	hint := "[↑↓] navigate tree  [→] expand/open  [←] collapse/up  [enter] focus settings  [esc] close  [s] save  [a] apply  [t] test  [r] reveal"
	if providerSupportsLogin(providerForSection(m.currentSection())) {
		hint = "[↑↓] navigate tree  [→] expand/open  [←] collapse/up  [enter] focus settings  [esc] close  [s] save  [a] apply  [t] test  [g] login  [r] reveal"
	}
	if m.editing {
		hint = "[enter] save edit  [esc] cancel edit"
	} else if m.fieldsFocused() {
		hint = "[↑↓] settings  [enter/e] edit  [space] toggle bool  [left/esc] groups  [s] save  [a] apply  [t] test  [r] reveal"
		if providerSupportsLogin(providerForSection(m.currentSection())) {
			hint = "[↑↓] settings  [enter/e] edit  [space] toggle bool  [left/esc] groups  [s] save  [a] apply  [t] test  [g] login  [r] reveal"
		}
	}
	extras := make([]string, 0, 2)
	if m.warningText != "" {
		extras = append(extras, "warning: "+m.warningText)
	}
	if m.errorText != "" {
		extras = append(extras, "error: "+m.errorText)
	} else if m.statusText != "" {
		extras = append(extras, m.statusText)
	}
	if len(extras) == 0 {
		return hint
	}
	return strings.Join(extras, "  │  ") + "  │  " + hint
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
	case "provider.sentry":
		return "sentry"
	case "provider.github":
		return "github"
	default:
		return ""
	}
}

func providerSupportsLogin(provider string) bool {
	return harnessForProvider(provider) != ""
}

func harnessForProvider(provider string) string {
	switch provider {
	case "github":
		return "gh-cli"
	case "sentry":
		return "sentry"
	default:
		return ""
	}
}

func (m SettingsPage) renderProviderStatusLine(prefix string, status ProviderStatus, width int) string {
	parts := []string{
		m.styles.Muted.Render(prefix + "Provider auth: " + status.AuthSource),
		m.styles.Muted.Render(" · "),
	}
	if status.Configured {
		parts = append(parts, m.styles.Muted.Render("configured"))
	} else {
		parts = append(parts, m.styles.Muted.Render("unconfigured"))
	}
	if status.Connected {
		parts = append(parts, m.styles.Muted.Render(" · "), m.styles.Success.Render("connected"))
	}
	if status.LastError != "" {
		parts = append(parts, m.styles.Muted.Render(" · error: "+status.LastError))
	}
	return ansi.Truncate(strings.Join(parts, ""), width, "…")
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
