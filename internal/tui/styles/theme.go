package styles

import "github.com/charmbracelet/lipgloss"

// Theme holds all color values for the TUI.
type Theme struct {
	HeaderBg, HeaderFg       string
	StatusBarBg, StatusBarFg string
	KeybindAccent            string
	// Status
	Pending, Active, Success, Error, Warning, Interrupted string
	// Content
	Title, Subtitle, Muted string
	Border, SelectedBg      string
	// Diff + Plan
	DiffAdd, DiffDel, CodeBlockBg string
}

var DefaultTheme = Theme{
	HeaderBg:    "#1a1a2e",
	HeaderFg:    "#e0e0e0",
	StatusBarBg: "#16213e",
	StatusBarFg: "#a0a0a0",
	KeybindAccent: "#5b8def",
	Pending:     "#6b7280",
	Active:      "#5b8def",
	Success:     "#34d399",
	Error:       "#f87171",
	Warning:     "#fbbf24",
	Interrupted: "#f59e0b",
	Title:       "#f0f0f0",
	Subtitle:    "#b0b0b0",
	Muted:       "#6b7280",
	Border:      "#2d2d44",
	SelectedBg:  "#1e293b",
	DiffAdd:     "#34d399",
	DiffDel:     "#f87171",
	CodeBlockBg: "#0f0f1a",
}

// Styles pre-builds common lipgloss styles from theme.
type Styles struct {
	Theme           Theme
	Header          lipgloss.Style
	StatusBar       lipgloss.Style
	Sidebar         lipgloss.Style
	SidebarSelected lipgloss.Style
	Border          lipgloss.Style
	KeybindAccent   lipgloss.Style
	// Status badges
	Active      lipgloss.Style
	Success     lipgloss.Style
	Error       lipgloss.Style
	Warning     lipgloss.Style
	Muted       lipgloss.Style
	Interrupted lipgloss.Style
	// Text
	Title    lipgloss.Style
	Subtitle lipgloss.Style
	// Diff
	DiffAdd lipgloss.Style
	DiffDel lipgloss.Style
}

// NewStyles builds a Styles from the given Theme.
func NewStyles(t Theme) Styles {
	return Styles{
		Theme: t,
		Header: lipgloss.NewStyle().
			Background(lipgloss.Color(t.HeaderBg)).
			Foreground(lipgloss.Color(t.HeaderFg)).
			Bold(true).Padding(0, 1),
		StatusBar: lipgloss.NewStyle().
			Background(lipgloss.Color(t.StatusBarBg)).
			Foreground(lipgloss.Color(t.StatusBarFg)).Padding(0, 1),
		Sidebar: lipgloss.NewStyle().
			BorderRight(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color(t.Border)),
		SidebarSelected: lipgloss.NewStyle().
			Background(lipgloss.Color(t.SelectedBg)),
		Border: lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(t.Border)),
		KeybindAccent: lipgloss.NewStyle().Foreground(lipgloss.Color(t.KeybindAccent)).Bold(true),
		Active:        lipgloss.NewStyle().Foreground(lipgloss.Color(t.Active)),
		Success:       lipgloss.NewStyle().Foreground(lipgloss.Color(t.Success)),
		Error:         lipgloss.NewStyle().Foreground(lipgloss.Color(t.Error)),
		Warning:       lipgloss.NewStyle().Foreground(lipgloss.Color(t.Warning)),
		Muted:         lipgloss.NewStyle().Foreground(lipgloss.Color(t.Muted)),
		Interrupted:   lipgloss.NewStyle().Foreground(lipgloss.Color(t.Interrupted)),
		Title:         lipgloss.NewStyle().Foreground(lipgloss.Color(t.Title)).Bold(true),
		Subtitle:      lipgloss.NewStyle().Foreground(lipgloss.Color(t.Subtitle)),
		DiffAdd:       lipgloss.NewStyle().Foreground(lipgloss.Color(t.DiffAdd)),
		DiffDel:       lipgloss.NewStyle().Foreground(lipgloss.Color(t.DiffDel)),
	}
}
