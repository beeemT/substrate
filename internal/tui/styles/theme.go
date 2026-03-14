package styles

import "github.com/charmbracelet/lipgloss"

// Theme holds all semantic color values for the TUI.
type Theme struct {
	HeaderBg, HeaderFg       string
	StatusBarBg, StatusBarFg string
	KeybindAccent            string

	// Status
	Pending, Active, Success, Error, Warning, Interrupted string

	// Shared text + chrome roles
	Title, Subtitle, Muted, Hint, Label, Accent, Link, Divider string
	Border, PaneBorder, PaneBorderFocused                      string
	OverlayBg, OverlayBorder, OverlayBorderFocused             string
	SelectedBg, SelectionActive, SelectionInactive             string

	// Settings subtheme
	SettingsText, SettingsTextStrong, SettingsBreadcrumb, SettingsSelectionInactiveText string
	ScrollbarTrack, ScrollbarThumb, ScrollbarThumbFocused                               string

	// Diff + plan
	DiffAdd, DiffDel, CodeBlockBg string
}

var DefaultTheme = Theme{
	HeaderBg:      "#1a1a2e",
	HeaderFg:      "#e0e0e0",
	StatusBarBg:   "#16213e",
	StatusBarFg:   "#a0a0a0",
	KeybindAccent: "#5b8def",

	Pending:     "#6b7280",
	Active:      "#5b8def",
	Success:     "#34d399",
	Error:       "#f87171",
	Warning:     "#fbbf24",
	Interrupted: "#f59e0b",

	Title:                "#f0f0f0",
	Subtitle:             "#b0b0b0",
	Muted:                "#6b7280",
	Hint:                 "#6b7280",
	Label:                "#94a3b8",
	Accent:               "#5b8def",
	Link:                 "#7dd3fc",
	Divider:              "#2d2d44",
	Border:               "#2d2d44",
	PaneBorder:           "#334155",
	PaneBorderFocused:    "#60a5fa",
	OverlayBg:            "#1a1a2e",
	OverlayBorder:        "#2d2d44",
	OverlayBorderFocused: "#60a5fa",
	SelectedBg:           "#1e293b",
	SelectionActive:      "#1e293b",
	SelectionInactive:    "#122033",

	SettingsText:                  "#cbd5e1",
	SettingsTextStrong:            "#f8fafc",
	SettingsBreadcrumb:            "#93c5fd",
	SettingsSelectionInactiveText: "#dbeafe",
	ScrollbarTrack:                "#64748b",
	ScrollbarThumb:                "#cbd5e1",
	ScrollbarThumbFocused:         "#60a5fa",

	DiffAdd:     "#34d399",
	DiffDel:     "#f87171",
	CodeBlockBg: "#0f0f1a",
}

// Styles pre-builds common lipgloss styles from theme and shared chrome metrics.
type Styles struct {
	Theme  Theme
	Chrome ChromeMetrics

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

	// Text + semantic chrome
	Title       lipgloss.Style
	Subtitle    lipgloss.Style
	Divider     lipgloss.Style
	Hint        lipgloss.Style
	Label       lipgloss.Style
	Accent      lipgloss.Style
	Link        lipgloss.Style
	Pane        lipgloss.Style
	PaneFocused lipgloss.Style

	OverlayFrame        lipgloss.Style
	OverlayFrameFocused lipgloss.Style
	OverlayPane         lipgloss.Style
	OverlayPaneFocused  lipgloss.Style
	SectionLabel        lipgloss.Style
	TabActive           lipgloss.Style
	TabInactive         lipgloss.Style
	Callout             lipgloss.Style
	CalloutWarning      lipgloss.Style

	SettingsText              lipgloss.Style
	SettingsTextStrong        lipgloss.Style
	SettingsBreadcrumb        lipgloss.Style
	SettingsSection           lipgloss.Style
	SettingsSelectionActive   lipgloss.Style
	SettingsSelectionInactive lipgloss.Style
	ScrollbarTrack            lipgloss.Style
	ScrollbarThumb            lipgloss.Style
	ScrollbarThumbFocused     lipgloss.Style

	// Diff
	DiffAdd lipgloss.Style
	DiffDel lipgloss.Style
}

// NewStyles builds a Styles from the given Theme.
func NewStyles(t Theme) Styles {
	chrome := DefaultChromeMetrics
	return Styles{
		Theme:  t,
		Chrome: chrome,
		Header: lipgloss.NewStyle().
			Background(lipgloss.Color(t.HeaderBg)).
			Foreground(lipgloss.Color(t.HeaderFg)).
			Bold(true).Padding(0, 1),
		StatusBar: lipgloss.NewStyle().
			Foreground(lipgloss.Color(t.StatusBarFg)).
			Padding(0, 1),
		Sidebar: lipgloss.NewStyle().
			BorderRight(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color(t.Divider)),
		SidebarSelected: lipgloss.NewStyle().
			Background(lipgloss.Color(t.SelectionActive)),
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
		Divider:       lipgloss.NewStyle().Foreground(lipgloss.Color(t.Divider)),
		Hint:          lipgloss.NewStyle().Foreground(lipgloss.Color(t.Hint)),
		Label:         lipgloss.NewStyle().Foreground(lipgloss.Color(t.Label)),
		Accent:        lipgloss.NewStyle().Foreground(lipgloss.Color(t.Accent)).Bold(true),
		Link:          lipgloss.NewStyle().Foreground(lipgloss.Color(t.Link)).Underline(true),
		Pane: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(t.PaneBorder)),
		PaneFocused: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(t.PaneBorderFocused)),
		OverlayFrame: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(t.OverlayBorder)).
			Background(lipgloss.Color(t.OverlayBg)).
			Padding(chrome.OverlayFrame.PaddingTop, chrome.OverlayFrame.PaddingRight, chrome.OverlayFrame.PaddingBottom, chrome.OverlayFrame.PaddingLeft),
		OverlayFrameFocused: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(t.OverlayBorderFocused)).
			Background(lipgloss.Color(t.OverlayBg)).
			Padding(chrome.OverlayFrame.PaddingTop, chrome.OverlayFrame.PaddingRight, chrome.OverlayFrame.PaddingBottom, chrome.OverlayFrame.PaddingLeft),
		OverlayPane: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(t.OverlayBorder)).
			BorderBackground(lipgloss.Color(t.OverlayBg)).
			Background(lipgloss.Color(t.OverlayBg)).
			Padding(chrome.OverlayPane.PaddingTop, chrome.OverlayPane.PaddingRight, chrome.OverlayPane.PaddingBottom, chrome.OverlayPane.PaddingLeft),
		OverlayPaneFocused: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(t.OverlayBorderFocused)).
			BorderBackground(lipgloss.Color(t.OverlayBg)).
			Background(lipgloss.Color(t.OverlayBg)).
			Padding(chrome.OverlayPane.PaddingTop, chrome.OverlayPane.PaddingRight, chrome.OverlayPane.PaddingBottom, chrome.OverlayPane.PaddingLeft),
		SectionLabel: lipgloss.NewStyle().Foreground(lipgloss.Color(t.Label)).Bold(true),
		TabActive:    lipgloss.NewStyle().Foreground(lipgloss.Color(t.Title)).Underline(true),
		TabInactive:  lipgloss.NewStyle().Foreground(lipgloss.Color(t.Muted)),
		Callout: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(t.PaneBorder)).
			Padding(chrome.Callout.PaddingTop, chrome.Callout.PaddingRight, chrome.Callout.PaddingBottom, chrome.Callout.PaddingLeft),
		CalloutWarning: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(t.Warning)).
			Padding(chrome.Callout.PaddingTop, chrome.Callout.PaddingRight, chrome.Callout.PaddingBottom, chrome.Callout.PaddingLeft),
		SettingsText:              lipgloss.NewStyle().Foreground(lipgloss.Color(t.SettingsText)),
		SettingsTextStrong:        lipgloss.NewStyle().Foreground(lipgloss.Color(t.SettingsTextStrong)).Bold(true),
		SettingsBreadcrumb:        lipgloss.NewStyle().Foreground(lipgloss.Color(t.SettingsBreadcrumb)),
		SettingsSection:           lipgloss.NewStyle().Foreground(lipgloss.Color(t.SettingsBreadcrumb)).Bold(true),
		SettingsSelectionActive:   lipgloss.NewStyle().Background(lipgloss.Color(t.SelectionActive)).Foreground(lipgloss.Color(t.SettingsTextStrong)).Bold(true),
		SettingsSelectionInactive: lipgloss.NewStyle().Background(lipgloss.Color(t.SelectionInactive)).Foreground(lipgloss.Color(t.SettingsSelectionInactiveText)).Bold(true),
		ScrollbarTrack:            lipgloss.NewStyle().Foreground(lipgloss.Color(t.ScrollbarTrack)),
		ScrollbarThumb:            lipgloss.NewStyle().Foreground(lipgloss.Color(t.ScrollbarThumb)),
		ScrollbarThumbFocused:     lipgloss.NewStyle().Foreground(lipgloss.Color(t.ScrollbarThumbFocused)),
		DiffAdd:                   lipgloss.NewStyle().Foreground(lipgloss.Color(t.DiffAdd)),
		DiffDel:                   lipgloss.NewStyle().Foreground(lipgloss.Color(t.DiffDel)),
	}
}
