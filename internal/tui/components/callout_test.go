package components

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/tui/styles"
)

func TestRenderCalloutRespectsRequestedWidth(t *testing.T) {
	t.Parallel()

	st := styles.NewStyles(styles.DefaultTheme)
	width := 44
	rendered := RenderCallout(st, CalloutSpec{
		Body:    ansi.Hardwrap("Press [Enter] to start planning.", CalloutInnerWidth(st, width), true),
		Width:   width,
		Variant: CalloutCard,
	})

	for i, line := range strings.Split(rendered, "\n") {
		if got := ansi.StringWidth(line); got != width {
			t.Fatalf("line %d width = %d, want %d\nline: %q", i+1, got, width, line)
		}
	}
}
