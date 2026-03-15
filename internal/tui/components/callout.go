package components

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/beeemT/substrate/internal/tui/styles"
)

// CalloutVariant selects the semantic surface treatment.
type CalloutVariant int

const (
	CalloutDefault CalloutVariant = iota
	CalloutCard
	CalloutWarning
	CalloutRunning // active/accent border color for in-progress tool cards
	CalloutError   // error border color for failed tool cards
)

// CalloutSpec describes a bordered content box.
type CalloutSpec struct {
	Body    string
	Width   int
	Variant CalloutVariant
}

// CalloutInnerWidth returns the content width available inside the callout shell.
func CalloutInnerWidth(st styles.Styles, width int) int {
	return st.Chrome.Callout.InnerWidth(width)
}

// RenderCallout renders semantic bordered content.
func RenderCallout(st styles.Styles, spec CalloutSpec) string {
	calloutStyle := st.Callout
	switch spec.Variant {
	case CalloutCard:
		calloutStyle = st.Callout.Copy().BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(st.Theme.Divider))
	case CalloutWarning:
		calloutStyle = st.CalloutWarning
	case CalloutRunning:
		calloutStyle = st.CalloutRunning
	case CalloutError:
		calloutStyle = st.CalloutError
	}
	if spec.Width > 0 {
		horizontalBorder := st.Chrome.Callout.BorderLeft + st.Chrome.Callout.BorderRight
		calloutStyle = calloutStyle.Copy().Width(max(1, spec.Width-horizontalBorder))
	}
	return calloutStyle.Render(spec.Body)
}

// RenderCalloutWithBg renders semantic bordered content with an explicit
// background color applied to the content area and border region.
// Use only for inline viewport content — not for overlays (see AGENTS.md).
func RenderCalloutWithBg(st styles.Styles, spec CalloutSpec, bg lipgloss.Color) string {
	calloutStyle := st.Callout
	switch spec.Variant {
	case CalloutCard:
		calloutStyle = st.Callout.Copy().BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(st.Theme.Divider))
	case CalloutWarning:
		calloutStyle = st.CalloutWarning
	case CalloutRunning:
		calloutStyle = st.CalloutRunning
	case CalloutError:
		calloutStyle = st.CalloutError
	}
	if spec.Width > 0 {
		horizontalBorder := st.Chrome.Callout.BorderLeft + st.Chrome.Callout.BorderRight
		calloutStyle = calloutStyle.Copy().Width(max(1, spec.Width-horizontalBorder))
	}
	calloutStyle = calloutStyle.Copy().Background(bg).BorderBackground(bg)
	return calloutStyle.Render(spec.Body)
}
