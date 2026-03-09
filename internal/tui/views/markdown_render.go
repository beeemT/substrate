package views

import (
	"regexp"
	"strings"

	mermaidcmd "github.com/AlexanderGrooff/mermaid-ascii/cmd"
	mermaiddiagram "github.com/AlexanderGrooff/mermaid-ascii/pkg/diagram"
	"github.com/charmbracelet/glamour"
	glamouransi "github.com/charmbracelet/glamour/ansi"
	glamourstyles "github.com/charmbracelet/glamour/styles"
)

var mermaidFencePattern = regexp.MustCompile("(?s)```mermaid\\s*\\r?\\n(.*?)\\r?\\n```")

var detailMarkdownStyleConfig = newDetailMarkdownStyleConfig()

func newDetailMarkdownStyleConfig() glamouransi.StyleConfig {
	cfg := glamourstyles.DarkStyleConfig
	cfg.H1.Prefix = ""
	cfg.H1.Suffix = ""
	cfg.H2.Prefix = ""
	cfg.H3.Prefix = ""
	cfg.H4.Prefix = ""
	cfg.H5.Prefix = ""
	cfg.H6.Prefix = ""
	return cfg
}

func renderMarkdownDocument(content string, width int) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}
	if width < 20 {
		width = 20
	}

	matches := mermaidFencePattern.FindAllStringSubmatchIndex(trimmed, -1)
	if len(matches) == 0 {
		return renderMarkdownSegment(trimmed, width)
	}

	parts := make([]string, 0, len(matches)*2+1)
	cursor := 0
	for _, match := range matches {
		if match[0] > cursor {
			segment := strings.TrimSpace(trimmed[cursor:match[0]])
			if segment != "" {
				parts = append(parts, renderMarkdownSegment(segment, width))
			}
		}
		source := strings.TrimSpace(trimmed[match[2]:match[3]])
		if source != "" {
			parts = append(parts, renderMermaidBlock(source))
		}
		cursor = match[1]
	}
	if cursor < len(trimmed) {
		segment := strings.TrimSpace(trimmed[cursor:])
		if segment != "" {
			parts = append(parts, renderMarkdownSegment(segment, width))
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func renderMarkdownSegment(content string, width int) string {
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStyles(detailMarkdownStyleConfig),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return content
	}
	out, err := renderer.Render(content)
	if err != nil {
		return content
	}
	return strings.TrimRight(out, "\n")
}

func renderMermaidBlock(source string) string {
	cfg := mermaiddiagram.DefaultConfig()
	cfg.PaddingBetweenX = 2
	cfg.PaddingBetweenY = 1
	cfg.SequenceParticipantSpacing = 3
	cfg.SequenceMessageSpacing = 0

	diagram, err := mermaidcmd.RenderDiagram(source, cfg)
	if err != nil {
		return "Mermaid diagram\n" + source
	}
	return "Mermaid diagram\n" + strings.TrimRight(diagram, "\n")
}
