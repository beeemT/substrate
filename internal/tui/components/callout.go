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
	}
	if spec.Width > 0 {
		calloutStyle = calloutStyle.Copy().Width(st.Chrome.Callout.InnerWidth(spec.Width))
	}
	return calloutStyle.Render(spec.Body)
}
