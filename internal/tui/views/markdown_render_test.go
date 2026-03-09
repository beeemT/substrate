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

	rendered := stripBrowseANSI(renderMarkdownDocument("## Summary\n\nThis is **important**.\n\n- first\n- second", 80))
	for _, want := range []string{"Summary", "This is important.", "first", "second"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered = %q, want %q", rendered, want)
		}
	}

	for _, raw := range []string{"## Summary", "**important**"} {
		if strings.Contains(rendered, raw) {
			t.Fatalf("rendered = %q, must not contain raw markdown token %q", rendered, raw)
		}
	}
}

func TestRenderMarkdownDocumentRendersLinksWithHrefText(t *testing.T) {
	t.Parallel()

	rendered := stripBrowseANSI(renderMarkdownDocument("See [the guide](https://example.com/guide) for details.", 80))
	for _, want := range []string{"See", "the guide", "https://example.com/guide", "for details."} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered = %q, want %q", rendered, want)
		}
	}

	if strings.Contains(rendered, "[the guide](https://example.com/guide)") {
		t.Fatalf("rendered = %q, must not contain raw markdown link syntax", rendered)
	}
}
