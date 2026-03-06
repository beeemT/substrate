package views

import (
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/tui/styles"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
)

// ConfigFieldKind describes the type of a config field.
type ConfigFieldKind int

const (
	ConfigFieldString  ConfigFieldKind = iota
	ConfigFieldBool
	ConfigFieldEnum
	ConfigFieldPath
	ConfigFieldComplex // opens $EDITOR
)

// ConfigField represents a single editable config field.
type ConfigField struct {
	Key     string
	Value   string
	Kind    ConfigFieldKind
	Options []string // for ConfigFieldEnum
	Dirty   bool
}

// ConfigSection is a group of config fields.
type ConfigSection struct {
	Name   string
	Fields []ConfigField
}

// ConfigOverlay is the settings editor overlay.
type ConfigOverlay struct {
	sections      []ConfigSection
	sectionCursor int
	fieldCursor   int
	editing       bool
	editInput     textinput.Model
	dirty         bool
	configPath    string
	rawContent    string // full TOML for $EDITOR
	active        bool
	styles        styles.Styles
	width         int
	height        int
}

func NewConfigOverlay(cfg *config.Config, st styles.Styles) ConfigOverlay {
	ti := textinput.New()
	ti.CharLimit = 500

	sections := buildConfigSections(cfg)
	cfgPath, _ := config.ConfigPath()

	// read raw TOML; ignore error — rawContent stays empty if unreadable
	raw, _ := os.ReadFile(cfgPath)

	return ConfigOverlay{
		sections:   sections,
		editInput:  ti,
		configPath: cfgPath,
		rawContent: string(raw),
		styles:     st,
	}
}

func buildConfigSections(cfg *config.Config) []ConfigSection {
	if cfg == nil {
		return nil
	}
	return []ConfigSection{
		{
			Name: "commit",
			Fields: []ConfigField{
				{Key: "strategy", Value: string(cfg.Commit.Strategy), Kind: ConfigFieldEnum,
					Options: []string{"granular", "semi-regular", "single"}},
				{Key: "message_format", Value: string(cfg.Commit.MessageFormat), Kind: ConfigFieldEnum,
					Options: []string{"ai-generated", "conventional", "custom"}},
				{Key: "message_template", Value: cfg.Commit.MessageTemplate, Kind: ConfigFieldString},
			},
		},
		{
			Name: "plan",
			Fields: []ConfigField{
				{Key: "max_parse_retries", Value: intPtrStr(cfg.Plan.MaxParseRetries), Kind: ConfigFieldString},
			},
		},
		{
			Name: "review",
			Fields: []ConfigField{
				{Key: "pass_threshold", Value: string(cfg.Review.PassThreshold), Kind: ConfigFieldEnum,
					Options: []string{"nit_only", "minor_ok", "no_critiques"}},
				{Key: "max_cycles", Value: intPtrStr(cfg.Review.MaxCycles), Kind: ConfigFieldString},
			},
		},
		{
			Name: "foreman",
			Fields: []ConfigField{
				{Key: "enabled", Value: boolStr(cfg.Foreman.Enabled), Kind: ConfigFieldBool},
				{Key: "question_timeout", Value: cfg.Foreman.QuestionTimeout, Kind: ConfigFieldString},
			},
		},
		{
			Name: "adapters.ohmypi",
			Fields: []ConfigField{
				{Key: "bun_path", Value: cfg.Adapters.OhMyPi.BunPath, Kind: ConfigFieldPath},
				{Key: "bridge_path", Value: cfg.Adapters.OhMyPi.BridgePath, Kind: ConfigFieldPath},
				{Key: "thinking_level", Value: cfg.Adapters.OhMyPi.ThinkingLevel, Kind: ConfigFieldString},
			},
		},
		{
			Name: "adapters.linear",
			Fields: []ConfigField{
				{Key: "api_key", Value: cfg.Adapters.Linear.APIKey, Kind: ConfigFieldString},
				{Key: "team_id", Value: cfg.Adapters.Linear.TeamID, Kind: ConfigFieldString},
				{Key: "poll_interval", Value: cfg.Adapters.Linear.PollInterval, Kind: ConfigFieldString},
			},
		},
	}
}

func intPtrStr(p *int) string {
	if p == nil {
		return ""
	}
	return strconv.Itoa(*p)
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func (m *ConfigOverlay) Open() { m.active = true }

func (m *ConfigOverlay) Close() {
	m.active = false
	m.editing = false
	m.editInput.Blur()
}

func (m ConfigOverlay) Active() bool { return m.active }

func (m *ConfigOverlay) SetSize(w, h int) { m.width = w; m.height = h }

func (m *ConfigOverlay) currentSection() *ConfigSection {
	if m.sectionCursor >= len(m.sections) {
		return nil
	}
	return &m.sections[m.sectionCursor]
}

func (m *ConfigOverlay) currentField() *ConfigField {
	sec := m.currentSection()
	if sec == nil || m.fieldCursor >= len(sec.Fields) {
		return nil
	}
	return &sec.Fields[m.fieldCursor]
}

func (m ConfigOverlay) Update(msg tea.Msg) (ConfigOverlay, tea.Cmd) {
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
				}
				m.editing = false
				m.editInput.Blur()
			case "esc":
				m.editing = false
				m.editInput.Blur()
			default:
				m.editInput, cmd = m.editInput.Update(msg)
			}
			return m, cmd
		}
		switch msg.String() {
		case "esc":
			if m.dirty {
				return m, func() tea.Msg { return ConfirmCloseConfigMsg{} }
			}
			return m, func() tea.Msg { return CloseOverlayMsg{} }
		case "j", "down":
			sec := m.currentSection()
			if sec != nil && m.fieldCursor < len(sec.Fields)-1 {
				m.fieldCursor++
			} else if m.sectionCursor < len(m.sections)-1 {
				m.sectionCursor++
				m.fieldCursor = 0
			}
		case "k", "up":
			if m.fieldCursor > 0 {
				m.fieldCursor--
			} else if m.sectionCursor > 0 {
				m.sectionCursor--
				sec := m.currentSection()
				if sec != nil {
					m.fieldCursor = len(sec.Fields) - 1
				}
			}
		case "enter":
			if f := m.currentField(); f != nil && f.Kind != ConfigFieldComplex {
				m.editInput.SetValue(f.Value)
				m.editInput.Focus()
				m.editing = true
			}
		case "e":
			return m, m.openEditorCmd()
		case "s":
			if m.dirty {
				return m, m.saveCmd()
			}
		}
	case ConfigSaveMsg:
		m.rawContent = msg.NewContent
		m.dirty = false
	}
	return m, cmd
}

func (m ConfigOverlay) openEditorCmd() tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	f, err := os.CreateTemp("", "substrate-config-*.toml")
	if err != nil {
		return func() tea.Msg { return ErrMsg{Err: err} }
	}
	if _, err := f.WriteString(m.rawContent); err != nil {
		f.Close()
		os.Remove(f.Name())
		return func() tea.Msg { return ErrMsg{Err: err} }
	}
	f.Close()
	tmpFile := f.Name()
	return tea.ExecProcess(exec.Command(editor, tmpFile), func(err error) tea.Msg {
		if err != nil {
			os.Remove(tmpFile)
			return ErrMsg{Err: err}
		}
		data, readErr := os.ReadFile(tmpFile)
		os.Remove(tmpFile)
		if readErr != nil {
			return ErrMsg{Err: readErr}
		}
		return ConfigSaveMsg{NewContent: string(data)}
	})
}

func (m ConfigOverlay) saveCmd() tea.Cmd {
	configPath := m.configPath
	rawContent := m.rawContent
	return func() tea.Msg {
		if err := os.WriteFile(configPath, []byte(rawContent), 0o644); err != nil {
			return ErrMsg{Err: err}
		}
		return ActionDoneMsg{Message: "Configuration saved"}
	}
}

func (m ConfigOverlay) View() string {
	if !m.active {
		return ""
	}
	w := m.width - 4
	if w < 40 {
		w = 40
	}
	if w > 80 {
		w = 80
	}

	var lines []string
	lines = append(lines,
		lipgloss.NewStyle().Foreground(lipgloss.Color("#f0f0f0")).Bold(true).Render("Configuration"),
		"",
	)

	for si, sec := range m.sections {
		secStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#5b8def")).Bold(true)
		lines = append(lines, secStyle.Render("["+sec.Name+"]"))
		for fi, field := range sec.Fields {
			isActive := si == m.sectionCursor && fi == m.fieldCursor
			prefix := "  "
			if isActive {
				prefix = "▶ "
			}
			keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#b0b0b0"))
			valStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#f0f0f0"))
			if field.Dirty {
				valStyle = valStyle.Foreground(lipgloss.Color("#fbbf24"))
			}
			if isActive && m.editing {
				lines = append(lines, prefix+keyStyle.Render(field.Key+" = ")+m.editInput.View())
			} else {
				lines = append(lines, prefix+keyStyle.Render(field.Key+" = ")+valStyle.Render(field.Value))
			}
		}
		lines = append(lines, "")
	}

	dirtyNote := ""
	if m.dirty {
		dirtyNote = lipgloss.NewStyle().Foreground(lipgloss.Color("#fbbf24")).Render(" (unsaved changes)")
	}
	hints := lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280")).Render(
		"[j/k] Navigate  [Enter] Edit  [e] Open in $EDITOR  [s] Save  [Esc] Close") + dirtyNote
	lines = append(lines, hints)

	content := strings.Join(lines, "\n")
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#2d2d44")).
		Background(lipgloss.Color("#1a1a2e")).
		Padding(1, 2).
		Width(w)
	return boxStyle.Render(content)
}
