package views

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/sessionlog"
	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

type transcriptBlockKind int

const (
	blockKindPlain transcriptBlockKind = iota
	blockKindAssistant
	blockKindPrompt
	blockKindTool
	blockKindLifecycle
	blockKindQuestion
	blockKindForeman
)

type transcriptBlock struct {
	kind transcriptBlockKind

	// prompt / assistant / foreman / plain / lifecycle
	text    string
	label   string // for prompt blocks: "Prompt", "Feedback", "Answer", "Input"
	isError bool

	// lifecycle
	stage   string
	message string
	summary string

	// question
	question  string
	ctx       string
	uncertain bool

	// tool
	toolName    string
	toolIntent  string
	toolArgs    string
	toolOutput  []string
	toolResult  string
	toolRunning bool // true = no result received yet
	toolError   bool // true = result was an error
}

// groupEntries converts a flat entry slice into grouped transcript blocks.
// Adjacent tool_start / tool_output / tool_result entries are collapsed
// into a single ToolBlock. All other entries map 1:1 to a block.
func groupEntries(entries []sessionlog.Entry) []transcriptBlock {
	var blocks []transcriptBlock
	i := 0
	for i < len(entries) {
		e := entries[i]
		switch e.Kind {
		case sessionlog.KindToolStart:
			block := transcriptBlock{
				kind:        blockKindTool,
				toolName:    e.Tool,
				toolIntent:  e.Intent,
				toolArgs:    e.Text,
				toolRunning: true,
			}
			i++
			for i < len(entries) && entries[i].Kind == sessionlog.KindToolOutput {
				if entries[i].Text != "" {
					block.toolOutput = append(block.toolOutput, entries[i].Text)
				}
				i++
			}
			if i < len(entries) && entries[i].Kind == sessionlog.KindToolResult {
				block.toolResult = entries[i].Text
				block.toolError = entries[i].IsError
				block.toolRunning = false
				i++
			}
			blocks = append(blocks, block)

		case sessionlog.KindToolOutput:
			// Orphaned — no preceding KindToolStart in current group
			if e.Text != "" {
				blocks = append(blocks, transcriptBlock{kind: blockKindPlain, text: e.Text})
			}
			i++

		case sessionlog.KindToolResult:
			// Orphaned — no preceding KindToolStart in current group
			if e.Text != "" {
				blocks = append(blocks, transcriptBlock{kind: blockKindPlain, text: e.Text})
			}
			i++

		case sessionlog.KindInput:
			if strings.TrimSpace(e.Text) == "" {
				i++
				continue
			}
			label := "Input"
			switch e.InputKind {
			case "prompt":
				label = "Prompt"
			case "message":
				label = "Feedback"
			case "answer":
				label = "Answer"
			}
			blocks = append(blocks, transcriptBlock{kind: blockKindPrompt, text: e.Text, label: label})
			i++

		case sessionlog.KindAssistant:
			if strings.TrimSpace(e.Text) == "" {
				i++
				continue
			}
			blocks = append(blocks, transcriptBlock{kind: blockKindAssistant, text: e.Text})
			i++

		case sessionlog.KindQuestion:
			if strings.TrimSpace(e.Question) == "" {
				i++
				continue
			}
			blocks = append(blocks, transcriptBlock{
				kind:      blockKindQuestion,
				question:  e.Question,
				ctx:       e.Context,
				uncertain: e.Uncertain,
			})
			i++

		case sessionlog.KindForeman:
			if strings.TrimSpace(e.Text) == "" {
				i++
				continue
			}
			blocks = append(blocks, transcriptBlock{kind: blockKindForeman, text: e.Text, label: "Foreman"})
			i++

		case sessionlog.KindLifecycle:
			blocks = append(blocks, transcriptBlock{
				kind:    blockKindLifecycle,
				stage:   e.Stage,
				message: e.Message,
				summary: e.Summary,
				text:    e.Text,
			})
			i++

		case sessionlog.KindPlain:
			if strings.TrimSpace(e.Text) == "" {
				i++
				continue
			}
			blocks = append(blocks, transcriptBlock{kind: blockKindPlain, text: e.Text})
			i++

		default:
			text := firstNonEmptyTranscript(e.Text, e.Message, e.Summary)
			if text != "" {
				blocks = append(blocks, transcriptBlock{kind: blockKindPlain, text: text, isError: e.Kind == "error"})
			}
			i++
		}
	}
	return blocks
}

func renderTranscriptBlock(st styles.Styles, block transcriptBlock, width int, verbose bool) string {
	switch block.kind {
	case blockKindPlain:
		if block.isError {
			prefix := st.Error.Render("Error:")
			// "Error:" is 6 visual chars + 1 separating space = 7 overhead
			text := ansi.Hardwrap(block.text, max(1, width-7), true)
			return prefix + " " + text
		}
		return ansi.Hardwrap(block.text, width, true)
	case blockKindAssistant:
		return ansi.Hardwrap(block.text, width, true)

	case blockKindPrompt, blockKindForeman:
		label := block.label
		if label == "" {
			if block.kind == blockKindForeman {
				label = "Foreman"
			} else {
				label = "Input"
			}
		}
		bodyWidth := max(1, width-ansi.StringWidth(label)-2)
		body := ansi.Hardwrap(block.text, bodyWidth, true)
		return st.SectionLabel.Render(label+":") + " " + body

	case blockKindLifecycle:
		var text string
		switch block.stage {
		case "started":
			text = firstNonEmptyTranscript(block.message, "Session started")
		case "completed":
			text = firstNonEmptyTranscript(block.summary, block.message, "Session complete")
		case "failed":
			text = "Failed: " + firstNonEmptyTranscript(block.message, block.summary, "session failed")
		default:
			text = firstNonEmptyTranscript(block.message, block.summary, block.text)
		}
		return st.Muted.Render(ansi.Truncate(text, width, "…"))

	case blockKindQuestion:
		question := block.question
		if block.uncertain {
			question = "(uncertain) " + question
		}
		body := "Question: " + question
		if block.ctx != "" {
			body += " — " + block.ctx
		}
		innerW := components.CalloutInnerWidth(st, width)
		body = ansi.Hardwrap(body, max(1, innerW), true)
		return components.RenderCallout(st, components.CalloutSpec{Body: body, Width: width, Variant: components.CalloutWarning})

	case blockKindTool:
		return renderToolBlock(st, block, width, verbose)
	}
	return ""
}

func renderToolBlock(st styles.Styles, block transcriptBlock, width int, verbose bool) string {
	var variant components.CalloutVariant
	switch {
	case block.toolRunning:
		variant = components.CalloutRunning
	case block.toolError:
		variant = components.CalloutError
	default:
		variant = components.CalloutDefault
	}

	innerW := components.CalloutInnerWidth(st, width)

	var icon string
	switch {
	case block.toolRunning:
		icon = st.Active.Render("●")
	case block.toolError:
		icon = st.Error.Render("✗")
	default:
		icon = st.Success.Render("✓")
	}

	nameAndIntent := block.toolName
	if block.toolIntent != "" {
		nameAndIntent = block.toolName + " — " + block.toolIntent
	}
	// icon is 1 visual char, " " is 1, so 2 visual chars of overhead
	titleText := ansi.Truncate(nameAndIntent, max(1, innerW-2), "…")
	titleLine := icon + " " + titleText

	var bodyLines []string
	bodyLines = append(bodyLines, titleLine)

	// Args section
	if block.toolArgs != "" {
		if verbose {
			bodyLines = append(bodyLines, st.SectionLabel.Render("Args:"))
			wrapped := ansi.Hardwrap(block.toolArgs, max(1, innerW), true)
			for _, line := range strings.Split(wrapped, "\n") {
				bodyLines = append(bodyLines, line)
			}
		} else {
			// "Args: " is 6 visible chars
			argsLine := st.SectionLabel.Render("Args:") + " " + ansi.Truncate(singleLine(block.toolArgs), max(1, innerW-6), "…")
			bodyLines = append(bodyLines, argsLine)
		}
	}

	// Output section
	if len(block.toolOutput) > 0 {
		limit := 4
		if verbose {
			limit = 12
		}
		var allLines []string
		for _, entry := range block.toolOutput {
			allLines = append(allLines, strings.Split(entry, "\n")...)
		}
		shown := allLines
		remaining := 0
		if len(allLines) > limit {
			shown = allLines[:limit]
			remaining = len(allLines) - limit
		}
		for _, line := range shown {
			bodyLines = append(bodyLines, line)
		}
		if remaining > 0 {
			bodyLines = append(bodyLines, st.Muted.Render(fmt.Sprintf("… %d more lines", remaining)))
		}
	}

	// Result section
	if !block.toolRunning && block.toolResult != "" {
		showResult := verbose || len(block.toolOutput) == 0
		if showResult {
			var resultLabel string
			if block.toolError {
				resultLabel = st.Error.Render("Result:")
			} else {
				resultLabel = st.SectionLabel.Render("Result:")
			}
			// "Result: " is 8 visible chars
			bodyLines = append(bodyLines, resultLabel+" "+ansi.Truncate(singleLine(block.toolResult), max(1, innerW-8), "…"))
		}
	}

	// Truncate all body lines to innerW to guarantee content fits
	for i, line := range bodyLines {
		bodyLines[i] = ansi.Truncate(line, max(1, innerW), "…")
	}

	body := strings.Join(bodyLines, "\n")
	return components.RenderCallout(st, components.CalloutSpec{Body: body, Width: width, Variant: variant})
}

// RenderTranscript converts a sequence of session-log entries into bounded
// transcript text suitable for a viewport. width must be positive.
func RenderTranscript(st styles.Styles, entries []sessionlog.Entry, width int, verbose bool) string {
	if width <= 0 {
		return ""
	}
	blocks := groupEntries(entries)
	var parts []string
	for _, block := range blocks {
		rendered := renderTranscriptBlock(st, block, width, verbose)
		if rendered != "" {
			parts = append(parts, rendered)
		}
	}
	return strings.Join(parts, "\n")
}

// firstNonEmptyTranscript returns the first non-blank string.
func firstNonEmptyTranscript(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// singleLine replaces newlines and tabs with spaces for compact display.
func singleLine(s string) string {
	r := strings.NewReplacer("\n", " ", "\t", " ")
	return r.Replace(s)
}
