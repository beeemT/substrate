package views

import (
	"context"
	"fmt"
	"strings"

	"github.com/beeemT/substrate/internal/tui/styles"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type settingsAction int

const (
	settingsActionSave settingsAction = iota
	settingsActionApply
	settingsActionTestProvider
	settingsActionLoginProvider
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

type SettingsPage struct {
	service        *SettingsService
	sections       []SettingsSection
	providerStatus map[string]ProviderStatus
	rawContent     string
	active         bool
	width          int
	height         int
	sectionCursor  int
	fieldCursor    int
	editing        bool
	revealSecrets  bool
	dirty          bool
	editInput      textinput.Model
	styles         styles.Styles
	errorText      string
	statusText     string
}

func NewSettingsPage(svc *SettingsService, snapshot SettingsSnapshot, st styles.Styles) SettingsPage {
	ti := textinput.New()
	ti.CharLimit = 1000
	return SettingsPage{
		service:        svc,
		sections:       snapshot.Sections,
		providerStatus: snapshot.Providers,
		rawContent:     snapshot.RawTOML,
		editInput:      ti,
		styles:         st,
	}
}

func (m *SettingsPage) Open() { m.active = true }
func (m *SettingsPage) Close() {
	m.active = false
	m.editing = false
	m.editInput.Blur()
	m.errorText = ""
}
func (m SettingsPage) Active() bool      { return m.active }
func (m *SettingsPage) SetSize(w, h int) { m.width = w; m.height = h }

func (m *SettingsPage) SetSnapshot(snapshot SettingsSnapshot) {
	m.sections = snapshot.Sections
	m.providerStatus = snapshot.Providers
	m.rawContent = snapshot.RawTOML
	m.dirty = false
	m.errorText = ""
	m.statusText = ""
	m.editing = false
	m.editInput.Blur()
	m.clampCursor()
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

func (m SettingsPage) Update(msg tea.Msg, svcs Services) (SettingsPage, tea.Cmd) {
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
			if m.fieldCursor > 0 {
				m.fieldCursor--
			} else if m.sectionCursor > 0 {
				m.sectionCursor--
				sec := m.currentSection()
				if sec != nil {
					m.fieldCursor = max(0, len(sec.Fields)-1)
				}
			}
		case "down", "j":
			sec := m.currentSection()
			if sec != nil && m.fieldCursor < len(sec.Fields)-1 {
				m.fieldCursor++
			} else if m.sectionCursor < len(m.sections)-1 {
				m.sectionCursor++
				m.fieldCursor = 0
			}
		case "left", "h":
			if m.sectionCursor > 0 {
				m.sectionCursor--
				m.fieldCursor = 0
			}
		case "right", "l":
			if m.sectionCursor < len(m.sections)-1 {
				m.sectionCursor++
				m.fieldCursor = 0
			}
		case "enter":
			if f := m.currentField(); f != nil {
				m.editInput.SetValue(f.Value)
				m.editInput.Focus()
				m.editing = true
			}
		case " ":
			if f := m.currentField(); f != nil && f.Type == SettingsFieldBool {
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
	leftWidth := max(24, min(32, m.width/4))
	mainWidth := m.width - leftWidth - 3
	sections := make([]string, 0, len(m.sections))
	for i, sec := range m.sections {
		line := sec.Title
		if sec.Status != "" {
			line += " · " + sec.Status
		}
		style := lipgloss.NewStyle().Width(leftWidth - 2).PaddingLeft(1)
		if i == m.sectionCursor {
			style = style.Background(lipgloss.Color("#1e293b")).Foreground(lipgloss.Color("#f8fafc"))
		} else {
			style = style.Foreground(lipgloss.Color("#94a3b8"))
		}
		sections = append(sections, style.Render(truncate(line, leftWidth-3)))
	}
	left := lipgloss.NewStyle().BorderRight(true).BorderForeground(lipgloss.Color("#334155")).Width(leftWidth).Render(strings.Join(sections, "\n"))
	main := m.renderMain(mainWidth)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, main)
}

func (m SettingsPage) renderMain(width int) string {
	sec := m.currentSection()
	if sec == nil {
		return ""
	}
	var lines []string
	header := m.styles.Title.Render(sec.Title)
	lines = append(lines, header)
	if sec.Description != "" {
		lines = append(lines, m.styles.Subtitle.Render(sec.Description))
	}
	provider := providerForSection(sec)
	if provider != "" {
		if st, ok := m.providerStatus[provider]; ok {
			lines = append(lines, m.styles.Muted.Render("Status: "+providerStatusLine(st)))
		}
	}
	lines = append(lines, "")
	for i, field := range sec.Fields {
		selected := i == m.fieldCursor
		value := field.Value
		if field.Sensitive && !m.revealSecrets && value != "" {
			value = strings.Repeat("•", min(8, len([]rune(value))))
		}
		if value == "" {
			value = m.styles.Muted.Render("<empty>")
		}
		label := field.Label
		if field.Required {
			label += " *"
		}
		row := lipgloss.JoinHorizontal(lipgloss.Top,
			lipgloss.NewStyle().Width(24).Foreground(lipgloss.Color("#cbd5e1")).Render(label),
			lipgloss.NewStyle().Width(max(20, width-28)).Render(value),
		)
		if selected {
			row = lipgloss.NewStyle().Background(lipgloss.Color("#1e293b")).Render(row)
		}
		lines = append(lines, row)
		if field.Description != "" {
			lines = append(lines, lipgloss.NewStyle().PaddingLeft(2).Foreground(lipgloss.Color("#64748b")).Render(field.Description))
		}
		if field.Status != "" {
			lines = append(lines, lipgloss.NewStyle().PaddingLeft(2).Foreground(lipgloss.Color("#60a5fa")).Render("auth: "+field.Status))
		}
		if field.Error != "" {
			lines = append(lines, lipgloss.NewStyle().PaddingLeft(2).Foreground(lipgloss.Color("#f87171")).Render(field.Error))
		}
		lines = append(lines, "")
	}
	if m.editing {
		lines = append(lines, m.styles.Muted.Render("Editing:"), m.editInput.View(), "")
	}
	if m.errorText != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("#f87171")).Render("Error: "+m.errorText))
	}
	if m.statusText != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("#34d399")).Render(m.statusText))
	}
	footer := m.styles.Muted.Render("[enter] edit  [space] toggle bool  [r] reveal  [s] save  [a] apply  [t] test  [g] login  [esc] close")
	lines = append(lines, "", footer)
	return lipgloss.NewStyle().Padding(1, 2).Width(width).Height(m.height).Render(strings.Join(lines, "\n"))
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
