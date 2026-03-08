package views

import (
	"strings"
	"testing"
)

func TestRenderMarkdownDocumentRendersMermaidBlock(t *testing.T) {
	t.Parallel()

	rendered := stripBrowseANSI(renderMarkdownDocument("# Heading\n\n```mermaid\ngraph LR\nA-->B\n```", 80))
	for _, want := range []string{"Heading", "Mermaid diagram", "A", "B"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered = %q, want %q", rendered, want)
		}
	}
}

func TestRenderMarkdownDocumentRendersStandardMarkdown(t *testing.T) {
	t.Parallel()

	rendered := stripBrowseANSI(renderMarkdownDocument("## Summary\n\n- first\n- second", 80))
	for _, want := range []string{"Summary", "first", "second"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered = %q, want %q", rendered, want)
		}
	}
}
