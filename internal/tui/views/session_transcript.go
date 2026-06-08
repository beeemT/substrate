package views

import (
	"encoding/json"
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
	blockKindThinking
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
	question    string
	ctx         string
	uncertain   bool
	fromForeman bool // true when the question was emitted by ask_foreman

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
	return groupEntriesWithMessageLabel(entries, "")
}

func groupEntriesWithMessageLabel(entries []sessionlog.Entry, messageLabel string) []transcriptBlock {
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
			if strings.TrimSpace(e.Text) == "" || isBridgeInitializationPayload(e.Text) {
				continue
			}
			label := "Input"
			switch e.InputKind {
			case "prompt":
				label = "Prompt"
			case "message":
				label = firstNonEmptyString(messageLabel, "Feedback")
			case "answer":
				label = "Answer"
			case "session_context":
				label = "Session context"
			}
			blocks = append(blocks, transcriptBlock{kind: blockKindPrompt, text: e.Text, label: label})

		case sessionlog.KindAssistant:
			if e.Text == "" {
				continue
			}
			if len(blocks) > 0 && blocks[len(blocks)-1].kind == blockKindAssistant {
				blocks[len(blocks)-1].text += e.Text
			} else {
				blocks = append(blocks, transcriptBlock{kind: blockKindAssistant, text: e.Text})
			}

		case sessionlog.KindThinking:
			if e.Text == "" {
				continue
			}
			if len(blocks) > 0 && blocks[len(blocks)-1].kind == blockKindThinking {
				blocks[len(blocks)-1].text += e.Text
			} else {
				blocks = append(blocks, transcriptBlock{kind: blockKindThinking, text: e.Text})
			}

		case sessionlog.KindQuestion:
			if strings.TrimSpace(e.Question) == "" {
				continue
			}
			// Detect whether this question came from ask_foreman by checking
			// if there is a pending ask_foreman tool block in the queue.
			// The question event is emitted synchronously from within the
			// tool execution, so a pending entry is always present.
			fromForeman := len(toolQueue["ask_foreman"]) > 0 || len(toolQueue["mcp__substrate__ask_foreman"]) > 0
			blocks = append(blocks, transcriptBlock{
				kind:        blockKindQuestion,
				question:    e.Question,
				ctx:         e.Context,
				uncertain:   e.Uncertain,
				fromForeman: fromForeman,
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

func isBridgeInitializationPayload(text string) bool {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "{") {
		return false
	}
	var payload struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return false
	}
	return payload.Type == "init"
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

func renderTranscriptBlock(st styles.Styles, block transcriptBlock, width int, verbose, collapseThinking bool, animationFrame int) string {
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
		return strings.Trim(renderMarkdownDocument(block.text, width), "\n")

	case blockKindThinking:
		return renderThinkingBlock(st, block, width, collapseThinking)

	case blockKindPrompt:
		label := block.label
		if label == "" {
			label = "Input"
		}
		innerW := components.CalloutInnerWidth(st, width)
		header := st.Accent.Render(label)
		body := ansi.Hardwrap(block.text, max(1, innerW), true)
		return components.RenderCallout(st, components.CalloutSpec{
			Body:    header + "\n" + body,
			Width:   width,
			Variant: components.CalloutPrompt,
		})

	case blockKindForeman:
		label := block.label
		if label == "" {
			label = "Foreman"
		}
		innerW := components.CalloutInnerWidth(st, width)
		header := st.SectionLabel.Render(label)
		body := ansi.Hardwrap(block.text, max(1, innerW), true)
		return components.RenderCallout(st, components.CalloutSpec{
			Body:    header + "\n" + body,
			Width:   width,
			Variant: components.CalloutCard,
		})

	case blockKindLifecycle:
		// Retry events render with amber warning style; other lifecycle stages use muted text.
		switch block.stage {
		case "retry_wait":
			text := firstNonEmptyTranscript(block.message, "Rate limited — retrying...")
			return st.Warning.Render(ansi.Truncate("⏸ "+text, width, "…"))
		case "retry_resumed":
			return st.Muted.Render(ansi.Truncate("↺ Resumed after rate limit", width, "…"))
		case "retry_exhausted":
			text := firstNonEmptyTranscript(block.message, "Rate limit retries exhausted")
			return st.Error.Render(ansi.Truncate("✗ "+text, width, "…"))
		case "compaction_start":
			text := firstNonEmptyTranscript(block.message, "Compacting context…")
			return st.Muted.Render(ansi.Truncate("⟳ "+text, width, "…"))
		case "compaction_end":
			return st.Muted.Render(ansi.Truncate("⟳ Context compacted", width, "…"))
		case "compaction_failed":
			text := "Compaction failed: " + firstNonEmptyTranscript(block.message, "unknown error")
			return st.Warning.Render(ansi.Truncate(text, width, "…"))
		}
		var text string
		switch block.stage {
		case "started":
			text = firstNonEmptyTranscript(block.message, "Session started")
			rule := renderInsetRule(st, width)
			label := " " + st.Subtitle.Render(ansi.Truncate("◈ "+text, max(1, width-2), "…"))
			return rule + "\n" + label
		case "completed":
			text = firstNonEmptyTranscript(block.summary, block.message, "Session complete")
			rule := renderInsetRule(st, width)
			label := " " + st.Success.Render(ansi.Truncate("◉ "+text, max(1, width-2), "…"))
			return rule + "\n" + label + "\n" + rule
		case "failed":
			text = "Failed: " + firstNonEmptyTranscript(block.message, block.summary, "session failed")
			rule := renderInsetRule(st, width)
			label := " " + st.Error.Render(ansi.Truncate("✗ "+text, max(1, width-2), "…"))
			return rule + "\n" + label
		default:
			text = firstNonEmptyTranscript(block.message, block.summary, block.text)
			return st.Muted.Render(ansi.Truncate(text, width, "…"))
		}

	case blockKindQuestion:
		label := "Question"
		if block.fromForeman {
			label = "Foreman Question"
		}
		question := block.question
		if block.uncertain {
			question = "(uncertain) " + question
		}
		body := label + ": " + question
		if block.ctx != "" {
			body += " — " + block.ctx
		}
		innerW := components.CalloutInnerWidth(st, width)
		body = ansi.Hardwrap(body, max(1, innerW), true)
		return components.RenderCallout(st, components.CalloutSpec{Body: body, Width: width, Variant: components.CalloutWarning})

	case blockKindTool:
		return renderToolBlock(st, block, width, verbose, animationFrame)
	}

	return ""
}

func renderToolBlock(st styles.Styles, block transcriptBlock, width int, verbose bool, animationFrame int) string {
	var variant components.CalloutVariant
	switch {
	case block.toolRunning:
		variant = components.CalloutRunning
	case block.toolError:
		variant = components.CalloutError
	default:
		variant = components.CalloutTool
	}

	innerW := components.CalloutInnerWidth(st, width)

	var icon string
	switch {
	case block.toolRunning:
		icon = st.Active.Render(components.SpinnerFrame(animationFrame))
	case block.toolError:
		icon = st.Error.Render("✗")
	default:
		icon = st.Success.Render("✓")
	}

	nameAndTitle := block.toolName
	if block.toolArgs != "" {
		if primary := toolPrimaryArg(block.toolName, block.toolArgs); primary != "" {
			nameAndTitle = block.toolName + " — " + st.Accent.Render(primary)
		}
	}
	iconWidth := ansi.StringWidth(icon)
	iconGap := " "
	if block.toolRunning {
		iconGap = ""
	}
	// Finished/error icons are 1 visual char plus a 1-cell gap. Spinner frames
	// include their own trailing gap.
	titleText := ansi.Truncate(nameAndTitle, max(1, innerW-iconWidth-ansi.StringWidth(iconGap)), "…")
	titleLine := icon + iconGap + titleText

	var bodyLines []string
	bodyLines = append(bodyLines, titleLine)

	// Smart tool detail summary — show the most semantically important args
	// prominently as a labelled line. Falls back to raw args for unknown tools.
	if block.toolArgs != "" {
		summary := toolArgsSummary(st, block.toolName, block.toolArgs, innerW)
		if summary != "" {
			bodyLines = append(bodyLines, summary)
		}
		// In verbose mode also show the raw JSON args below the summary.
		if verbose {
			bodyLines = append(bodyLines, st.SectionLabel.Render("Args:"))
			wrapped := ansi.Hardwrap(block.toolArgs, max(1, innerW), true)
			for line := range strings.SplitSeq(wrapped, "\n") {
				bodyLines = append(bodyLines, line)
			}
		}
	}

	shouldSeparateOutput := block.toolRunning || len(block.toolOutput) > 0 || (!block.toolRunning && block.toolResult != "")
	if shouldSeparateOutput {
		bodyLines = append(bodyLines, toolOutputSeparator(st, innerW))
	}

	// Output section
	if len(block.toolOutput) > 0 {
		limit := 4
		if verbose {
			limit = 12
		}
		allLines := make([]string, 0, len(block.toolOutput))
		for _, entry := range block.toolOutput {
			allLines = append(allLines, strings.Split(entry, "\n")...)
		}
		shown := allLines
		remaining := 0
		if len(allLines) > limit {
			shown = allLines[:limit]
			remaining = len(allLines) - limit
		}
		bodyLines = append(bodyLines, shown...)
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
			resultLines := strings.Split(strings.TrimRight(block.toolResult, "\n"), "\n")
			if len(resultLines) <= 1 {
				// Single-line result: compact "Result: <value>" format.
				// "Result: " is 8 visible chars.
				bodyLines = append(bodyLines, resultLabel+" "+ansi.Truncate(singleLine(block.toolResult), max(1, innerW-8), "…"))
			} else {
				// Multi-line result: label on its own line then content lines with
				// the same 4/12 limit used for tool output.
				bodyLines = append(bodyLines, resultLabel)
				limit := 4
				if verbose {
					limit = 12
				}
				shown := resultLines
				remaining := 0
				if len(resultLines) > limit {
					shown = resultLines[:limit]
					remaining = len(resultLines) - limit
				}
				bodyLines = append(bodyLines, shown...)
				if remaining > 0 {
					bodyLines = append(bodyLines, st.Muted.Render(fmt.Sprintf("… %d more lines", remaining)))
				}
			}
		}
	}

	// Truncate all body lines to innerW to guarantee content fits
	for i, line := range bodyLines {
		bodyLines[i] = ansi.Truncate(line, max(1, innerW), "…")
	}

	body := strings.Join(bodyLines, "\n")

	return components.RenderCallout(st, components.CalloutSpec{Body: body, Width: width, Variant: variant})
}

func toolOutputSeparator(st styles.Styles, width int) string {
	return st.Divider.Render(strings.Repeat("─", max(1, width)))
}

func renderTranscriptWithMessageLabelFrame(st styles.Styles, entries []sessionlog.Entry, width int, verbose, collapseThinking bool, messageLabel string, animationFrame int) (string, bool) {
	if width <= 0 {
		return "", false
	}
	blocks := groupEntriesWithMessageLabel(entries, messageLabel)
	var parts []string
	hasRunningTool := false
	for i, block := range blocks {
		// Insert a blank spacer before the first tool call in a consecutive group.
		if block.kind == blockKindTool && i > 0 && blocks[i-1].kind != blockKindTool {
			parts = append(parts, "")
		}

		if block.kind == blockKindTool && block.toolRunning {
			hasRunningTool = true
		}
		rendered := renderTranscriptBlock(st, block, width, verbose, collapseThinking, animationFrame)
		if rendered != "" {
			parts = append(parts, rendered)
		}

		// Insert a blank spacer after the last tool call in a consecutive group.
		if block.kind == blockKindTool && i < len(blocks)-1 && blocks[i+1].kind != blockKindTool {
			parts = append(parts, "")
		}
	}
	return strings.Join(parts, "\n"), hasRunningTool
}

func toolStringArg(args map[string]any, key string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func toolIntArg(args map[string]any, key string) int {
	if v, ok := args[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return 0
}

func toolStringListArg(args map[string]any, key string) []string {
	values, ok := args[key].([]any)
	if !ok || len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if s, ok := value.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func firstToolOperation(args map[string]any) map[string]any {
	ops, ok := args["operations"].([]any)
	if !ok || len(ops) == 0 {
		return nil
	}
	op, _ := ops[0].(map[string]any)
	return op
}

func toolOperationCount(args map[string]any) int {
	ops, ok := args["operations"].([]any)
	if !ok {
		return 0
	}
	return len(ops)
}

func toolNestedStringArg(args map[string]any, key string) string {
	if value := toolStringArg(args, key); value != "" {
		return value
	}
	if op := firstToolOperation(args); op != nil {
		return toolStringArg(op, key)
	}
	return ""
}

func toolNestedIntArg(args map[string]any, key string) int {
	if value := toolIntArg(args, key); value > 0 {
		return value
	}
	if op := firstToolOperation(args); op != nil {
		return toolIntArg(op, key)
	}
	return 0
}

func toolPathArg(args map[string]any) string {
	if path := toolNestedStringArg(args, "path"); path != "" {
		return path
	}
	if paths, ok := args["paths"].([]any); ok {
		if len(paths) == 1 {
			if path, ok := paths[0].(string); ok {
				return path
			}
		}
		if len(paths) > 1 {
			return fmt.Sprintf("%d paths", len(paths))
		}
	}
	if count := toolOperationCount(args); count > 1 {
		return fmt.Sprintf("%d operations", count)
	}
	return ""
}

func toolPatternArg(args map[string]any) string {
	if value := toolStringArg(args, "pattern"); value != "" {
		return value
	}
	if value := toolStringArg(args, "query"); value != "" {
		return value
	}
	if value := toolStringArg(args, "glob"); value != "" {
		return value
	}
	return toolStringArg(args, "include")
}

func toolCommandArg(args map[string]any) string {
	if value := toolStringArg(args, "command"); value != "" {
		return value
	}
	if value := toolStringArg(args, "cmd"); value != "" {
		return value
	}
	return toolStringArg(args, "script")
}

func toolContentSummaryParts(st styles.Styles, content string) []string {
	if content == "" {
		return nil
	}
	dim := func(v string) string { return st.Muted.Render(v) }
	lines := strings.Split(content, "\n")
	n := len(lines)
	// A trailing \n produces a final empty element — don't count it.
	if n > 0 && lines[n-1] == "" {
		n--
	}
	parts := make([]string, 0, 2)
	if n > 0 {
		parts = append(parts, dim(fmt.Sprintf("%d lines", n)))
	}
	// First non-empty line gives context on what is being written.
	for _, l := range lines {
		if trimmed := strings.TrimSpace(l); trimmed != "" {
			parts = append(parts, dim(singleLine(trimmed)))
			break
		}
	}
	return parts
}

func todoListPrimaryArg(args map[string]any) string {
	switch toolStringArg(args, "command") {
	case "create":
		if n := todoTaskCount(args); n > 0 {
			return "create " + pluralize(n, "task")
		}
		return "create"
	case "complete":
		if ids := todoCompletedTaskIDs(args); len(ids) > 0 {
			return "complete " + todoIDList(ids)
		}
		return "complete"
	default:
		return ""
	}
}

func todoTaskCount(args map[string]any) int {
	tasks, ok := args["tasks"].([]any)
	if !ok {
		return 0
	}
	return len(tasks)
}

func todoCompletedTaskIDs(args map[string]any) []string {
	return toolStringListArg(args, "completed_task_ids")
}

func todoIDList(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	var b strings.Builder
	for i, id := range ids {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteByte('#')
		b.WriteString(id)
	}
	return b.String()
}

func pluralize(n int, singular string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", singular)
	}
	return fmt.Sprintf("%d %ss", n, singular)
}

// toolPrimaryArg returns the primary single-line label for a tool call,
// shown in the title after " — ". Returns "" for unknown tools or when no
// meaningful label can be derived from the args.
func toolPrimaryArg(toolName, argsJSON string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ""
	}
	switch toolName {
	case "read", "write", "edit", "ast_edit":
		return toolPathArg(args)
	case "grep":
		if p := toolStringArg(args, "pattern"); p != "" {
			return "/" + p + "/"
		}
	case "search", "glob":
		return singleLine(toolPatternArg(args))
	case "find":
		if pattern := toolStringArg(args, "pattern"); pattern != "" {
			return pattern
		}
		return toolPathArg(args)
	case "bash", "execute", "shell":
		if cmd := toolCommandArg(args); cmd != "" {
			return singleLine(cmd)
		}
	case "lsp":
		return toolStringArg(args, "action")
	case "ast_grep":
		if pats, ok := args["pat"]; ok {
			switch v := pats.(type) {
			case []any:
				if len(v) > 0 {
					if s, ok := v[0].(string); ok {
						return singleLine(s)
					}
				}
			case string:
				return singleLine(v)
			}
		}
	case "fetch":
		return singleLine(toolStringArg(args, "url"))
	case "web_search":
		return singleLine(toolStringArg(args, "query"))
	case "todo_list":
		return todoListPrimaryArg(args)
	case "task":
		if tasks, ok := args["tasks"]; ok {
			if taskSlice, ok := tasks.([]any); ok && len(taskSlice) > 0 {
				return fmt.Sprintf("%d task(s)", len(taskSlice))
			}
		}
	}
	return ""
}

// toolArgsSummary returns a concise, human-readable summary line for the most
// important arguments of a known tool. Returns "" for unknown tools or when no
// meaningful fields are found. The summary is styled with accent/label colours
// and truncated to fit innerW.
func toolArgsSummary(st styles.Styles, toolName, argsJSON string, innerW int) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		// Not valid JSON (e.g. legacy plain-text args): fall back to truncated raw display.
		return st.SectionLabel.Render("Args:") + " " + ansi.Truncate(singleLine(argsJSON), max(1, innerW-6), "…")
	}

	dim := func(v string) string { return st.Muted.Render(v) }

	var parts []string

	switch toolName {
	case "read":
		// path is in the title; show range, mode, and depth details.
		offset, limit := toolNestedIntArg(args, "offset"), toolNestedIntArg(args, "limit")
		if offset > 0 && limit > 0 {
			parts = append(parts, dim(fmt.Sprintf("L%d +%d lines", offset, limit)))
		} else if offset > 0 {
			parts = append(parts, dim(fmt.Sprintf("L%d+", offset)))
		} else if limit > 0 {
			parts = append(parts, dim(fmt.Sprintf("%d lines", limit)))
		}
		if mode := toolNestedStringArg(args, "mode"); mode != "" && mode != "Line" {
			parts = append(parts, dim(mode))
		}
		if depth := toolNestedIntArg(args, "depth"); depth > 0 {
			parts = append(parts, dim(fmt.Sprintf("depth %d", depth)))
		}

	case "grep", "search", "glob":
		// pattern is in the title; show where and how the search is scoped.
		if path := toolStringArg(args, "path"); path != "" {
			parts = append(parts, dim(path))
		}
		if glob := toolStringArg(args, "glob"); glob != "" {
			parts = append(parts, dim(glob))
		}
		if include := toolStringArg(args, "include"); include != "" {
			parts = append(parts, dim(include))
		}
		if depth := toolIntArg(args, "max_depth"); depth > 0 {
			parts = append(parts, dim(fmt.Sprintf("depth %d", depth)))
		}

	case "lsp":
		// action is in the title; show file and symbol.
		if file := toolStringArg(args, "file"); file != "" {
			parts = append(parts, dim(file))
		}
		if sym := toolStringArg(args, "symbol"); sym != "" {
			parts = append(parts, dim(sym))
		}

	case "ast_grep":
		// first pattern is in the title; show additional patterns and path.
		if pats, ok := args["pat"]; ok {
			if v, ok := pats.([]any); ok && len(v) > 1 {
				parts = append(parts, dim(fmt.Sprintf("+%d patterns", len(v)-1)))
			}
		}
		if path := toolStringArg(args, "path"); path != "" {
			parts = append(parts, dim(path))
		}

	case "ast_edit":
		// path is in the title; show op count.
		if ops, ok := args["ops"]; ok {
			if opSlice, ok := ops.([]any); ok && len(opSlice) > 0 {
				parts = append(parts, dim(fmt.Sprintf("%d op(s)", len(opSlice))))
			}
		}

	case "write", "edit":
		// path is in the title; show content line count and a first-line preview.
		parts = append(parts, toolContentSummaryParts(st, toolStringArg(args, "content"))...)
		if command := toolStringArg(args, "command"); command != "" && command != "create" {
			parts = append(parts, dim(command))
		}

	case "todo_list":
		switch toolStringArg(args, "command") {
		case "create":
			if desc := toolStringArg(args, "task_list_description"); desc != "" {
				parts = append(parts, dim(singleLine(desc)))
			}
		case "complete":
			if update := toolStringArg(args, "context_update"); update != "" {
				parts = append(parts, dim(singleLine(update)))
			}
			if files := toolStringListArg(args, "modified_files"); len(files) > 0 {
				parts = append(parts, dim(pluralize(len(files), "file")))
				parts = append(parts, dim(files[0]))
			}
		}

	case "find", "bash", "execute", "shell", "fetch", "web_search", "task", "ask_foreman", "mcp__substrate__ask_foreman", "mcp__substrate-foreman__ask_foreman":
		// no summary — primary arg or dedicated event rendering is sufficient.

	default:
		// Unknown tool: show a single-line truncated raw args summary.
		return st.SectionLabel.Render("Args:") + " " + ansi.Truncate(singleLine(argsJSON), max(1, innerW-6), "…")
	}

	if len(parts) == 0 {
		return ""
	}
	line := strings.Join(parts, "  ")
	return ansi.Truncate(line, max(1, innerW), "…")
}

// RenderTranscript converts a sequence of session-log entries into bounded
// transcript text suitable for a viewport. width must be positive.
// verbose expands tool args and output; collapseThinking collapses thinking
// blocks to a single preview line (true by default in callers).
func RenderTranscript(st styles.Styles, entries []sessionlog.Entry, width int, verbose, collapseThinking bool) string {
	rendered, _ := renderTranscriptWithMessageLabelFrame(st, entries, width, verbose, collapseThinking, "", 0)
	return rendered
}

func RenderTranscriptWithMessageLabel(st styles.Styles, entries []sessionlog.Entry, width int, verbose, collapseThinking bool, messageLabel string) string {
	rendered, _ := renderTranscriptWithMessageLabelFrame(st, entries, width, verbose, collapseThinking, messageLabel, 0)
	return rendered
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

// renderThinkingBlock renders a thinking block.
// When collapseThinking is true it shows a single muted header line with a
// truncated preview. When false the full content is rendered in a muted grey
// to visually distinguish it from normal agent output.
func renderThinkingBlock(st styles.Styles, block transcriptBlock, width int, collapseThinking bool) string {
	const label = "~ Thinking"
	labelRendered := st.Muted.Render(label)
	// header: "~ Thinking  <truncated single line preview>"
	// label visual width + 2 spaces of separation
	labelW := ansi.StringWidth(labelRendered)
	headerBodyW := max(1, width-labelW-2)
	if collapseThinking {
		preview := ansi.Truncate(singleLine(block.text), headerBodyW, "…")
		return labelRendered + "  " + st.Muted.Render(preview)
	}
	// Expanded: label on first line then full content in muted grey to signal
	// that this is internal reasoning, not the agent's final output.
	var lines []string
	lines = append(lines, labelRendered)
	rendered := strings.Trim(renderMarkdownDocument(block.text, max(1, width-2)), "\n")
	// Split and drop trailing lines that contain no visible characters.
	// Glamour appends a bare ANSI-reset line after some block types (code
	// fences in particular), which would otherwise become a "  " (2-space)
	// orphan at the end of the block while all content lines have 4+ spaces.
	contentLines := strings.Split(rendered, "\n")
	for len(contentLines) > 0 && ansi.Strip(contentLines[len(contentLines)-1]) == "" {
		contentLines = contentLines[:len(contentLines)-1]
	}
	for _, line := range contentLines {
		lines = append(lines, "  "+st.Thinking.Render(line))
	}
	return strings.Join(lines, "\n")
}

// renderInsetRule renders a horizontal rule indented by 1 space on the left
// so it does not quite reach the viewport border, giving visual breathing room.
func renderInsetRule(st styles.Styles, width int) string {
	ruleW := max(1, width-2)
	return " " + st.Divider.Render(strings.Repeat("─", ruleW))
}

// hasThinkingBlocks reports whether any entry in the slice is a thinking block.
func hasThinkingBlocks(entries []sessionlog.Entry) bool {
	for _, e := range entries {
		if e.Kind == sessionlog.KindThinking {
			return true
		}
	}
	return false
}
