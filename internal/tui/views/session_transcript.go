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
// tool_start / tool_output / tool_result entries are matched by tool name using a
// per-tool FIFO queue, so concurrent tool calls (multiple tool_starts before their
// results arrive) are each collapsed into their own tool block instead of leaving
// orphaned result entries that render as raw plain-text. Non-tool entries between
// a tool_start and its tool_result are processed normally and do not break pairing.
func groupEntries(entries []sessionlog.Entry) []transcriptBlock {
	var blocks []transcriptBlock
	// toolQueue maps tool name → ordered list of block indices awaiting their result.
	toolQueue := make(map[string][]int)

	for _, e := range entries {
		switch e.Kind {
		case sessionlog.KindToolStart:
			idx := len(blocks)
			blocks = append(blocks, transcriptBlock{
				kind:        blockKindTool,
				toolName:    e.Tool,
				toolIntent:  e.Intent,
				toolArgs:    e.Text,
				toolRunning: true,
			})
			toolQueue[e.Tool] = append(toolQueue[e.Tool], idx)

		case sessionlog.KindToolOutput:
			if e.Text == "" {
				continue
			}
			if idx := toolOutputTarget(blocks, toolQueue, e.Tool); idx >= 0 {
				blocks[idx].toolOutput = append(blocks[idx].toolOutput, e.Text)
			} else {
				// Orphaned output with no pending tool block.
				blocks = append(blocks, transcriptBlock{kind: blockKindPlain, text: e.Text})
			}

		case sessionlog.KindToolResult:
			if idx := dequeueToolResult(blocks, toolQueue, e.Tool); idx >= 0 {
				blocks[idx].toolResult = e.Text
				blocks[idx].toolError = e.IsError
				blocks[idx].toolRunning = false
			} else if e.Text != "" {
				// Truly orphaned — no matching tool_start. Render as a finished tool
				// block so that file-content output (LINE#ID:content format) does not
				// spill as a wall of raw plain-text lines.
				blocks = append(blocks, transcriptBlock{
					kind:       blockKindTool,
					toolName:   e.Tool,
					toolError:  e.IsError,
					toolResult: e.Text,
				})
			}

		case sessionlog.KindInput:
			if strings.TrimSpace(e.Text) == "" {
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

		case sessionlog.KindAssistant:
			if strings.TrimSpace(e.Text) == "" {
				continue
			}
			blocks = append(blocks, transcriptBlock{kind: blockKindAssistant, text: e.Text})

		case sessionlog.KindQuestion:
			if strings.TrimSpace(e.Question) == "" {
				continue
			}
			blocks = append(blocks, transcriptBlock{
				kind:      blockKindQuestion,
				question:  e.Question,
				ctx:       e.Context,
				uncertain: e.Uncertain,
			})

		case sessionlog.KindForeman:
			if strings.TrimSpace(e.Text) == "" {
				continue
			}
			blocks = append(blocks, transcriptBlock{kind: blockKindForeman, text: e.Text, label: "Foreman"})

		case sessionlog.KindLifecycle:
			blocks = append(blocks, transcriptBlock{
				kind:    blockKindLifecycle,
				stage:   e.Stage,
				message: e.Message,
				summary: e.Summary,
				text:    e.Text,
			})

		case sessionlog.KindPlain:
			if strings.TrimSpace(e.Text) == "" {
				continue
			}
			blocks = append(blocks, transcriptBlock{kind: blockKindPlain, text: e.Text})

		default:
			text := firstNonEmptyTranscript(e.Text, e.Message, e.Summary)
			if text != "" {
				blocks = append(blocks, transcriptBlock{kind: blockKindPlain, text: text, isError: e.Kind == "error"})
			}
		}
	}
	return blocks
}

// toolOutputTarget returns the index of the running tool block that should
// receive a tool_output entry. It does NOT dequeue because multiple output
// events stream into the same block. When toolName is non-empty the oldest
// pending block for that name is used (FIFO). When toolName is empty a
// legacy LIFO fallback scans for the most-recently-added running block.
func toolOutputTarget(blocks []transcriptBlock, toolQueue map[string][]int, toolName string) int {
	if toolName != "" {
		if q := toolQueue[toolName]; len(q) > 0 {
			return q[0]
		}
		return -1
	}
	// Legacy fallback: entries emitted without a tool name.
	for i := len(blocks) - 1; i >= 0; i-- {
		if blocks[i].kind == blockKindTool && blocks[i].toolRunning {
			return i
		}
	}
	return -1
}

// dequeueToolResult finds and removes the oldest pending tool block for the
// given tool name (FIFO). When toolName is empty a legacy LIFO fallback
// scans for the most-recently-added running block and removes it from
// whichever queue owns it.
func dequeueToolResult(blocks []transcriptBlock, toolQueue map[string][]int, toolName string) int {
	if toolName != "" {
		q := toolQueue[toolName]
		if len(q) == 0 {
			return -1
		}
		idx := q[0]
		if len(q) == 1 {
			delete(toolQueue, toolName)
		} else {
			toolQueue[toolName] = q[1:]
		}
		return idx
	}
	// Legacy fallback: entries emitted without a tool name.
	for i := len(blocks) - 1; i >= 0; i-- {
		if blocks[i].kind != blockKindTool || !blocks[i].toolRunning {
			continue
		}
		// Remove this block from whichever queue owns it.
		for name, q := range toolQueue {
			for j, qIdx := range q {
				if qIdx != i {
					continue
				}
				if len(q) == 1 {
					delete(toolQueue, name)
				} else {
					newQ := make([]int, len(q)-1)
					copy(newQ, q[:j])
					copy(newQ[j:], q[j+1:])
					toolQueue[name] = newQ
				}
				return i
			}
		}
		return i // Running block not in queue; complete it anyway.
	}
	return -1
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
