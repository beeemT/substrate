package views

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/sessionlog"
	"github.com/beeemT/substrate/internal/tui/styles"
)

func testStyles() styles.Styles {
	return styles.NewStyles(styles.DefaultTheme)
}

func TestGroupEntriesGroupsToolBlock(t *testing.T) {
	t.Parallel()
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindToolStart, Tool: "read", Intent: "Reading"},
		{Kind: sessionlog.KindToolOutput, Text: "line1"},
		{Kind: sessionlog.KindToolResult, Text: "ok", IsError: false},
	}
	blocks := groupEntries(entries)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	b := blocks[0]
	if b.kind != blockKindTool {
		t.Errorf("expected blockKindTool, got %v", b.kind)
	}
	if b.toolName != "read" {
		t.Errorf("expected toolName=read, got %q", b.toolName)
	}
	if b.toolIntent != "Reading" {
		t.Errorf("expected toolIntent=Reading, got %q", b.toolIntent)
	}
	if len(b.toolOutput) != 1 || b.toolOutput[0] != "line1" {
		t.Errorf("expected toolOutput=[line1], got %v", b.toolOutput)
	}
	if b.toolResult != "ok" {
		t.Errorf("expected toolResult=ok, got %q", b.toolResult)
	}
	if b.toolRunning {
		t.Error("expected toolRunning=false")
	}
	if b.toolError {
		t.Error("expected toolError=false")
	}
}

func TestGroupEntriesRunningToolHasNoResult(t *testing.T) {
	t.Parallel()
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindToolStart, Tool: "write"},
		{Kind: sessionlog.KindToolOutput, Text: "writing..."},
	}
	blocks := groupEntries(entries)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if !blocks[0].toolRunning {
		t.Error("expected toolRunning=true (no result received)")
	}
}

func TestGroupEntriesToolResultMatchesAcrossNonToolEntry(t *testing.T) {
	t.Parallel()
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindToolStart, Tool: "grep"},
		{Kind: sessionlog.KindAssistant, Text: "reply"},
		{Kind: sessionlog.KindToolResult, Tool: "grep", Text: "r"},
	}
	blocks := groupEntries(entries)
	// The queue matches the tool_result to its tool_start even though an
	// assistant entry appears in between. Result: tool block (completed) + assistant.
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d: %+v", len(blocks), blocks)
	}
	if blocks[0].kind != blockKindTool {
		t.Errorf("block[0]: expected tool, got %v", blocks[0].kind)
	}
	if blocks[0].toolRunning {
		t.Error("block[0]: expected toolRunning=false (result arrived)")
	}
	if blocks[0].toolResult != "r" {
		t.Errorf("block[0]: expected toolResult=\"r\", got %q", blocks[0].toolResult)
	}
	if blocks[1].kind != blockKindAssistant {
		t.Errorf("block[1]: expected assistant, got %v", blocks[1].kind)
	}
}

func TestGroupEntriesConcurrentToolCallsAreGrouped(t *testing.T) {
	t.Parallel()
	// Simulates the common pattern where the agent fans out multiple read/grep calls
	// in parallel: all tool_starts arrive first, then results come back.
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindToolStart, Tool: "read", Intent: "reading A"},
		{Kind: sessionlog.KindToolStart, Tool: "read", Intent: "reading B"},
		{Kind: sessionlog.KindToolStart, Tool: "read", Intent: "reading C"},
		{Kind: sessionlog.KindToolResult, Tool: "read", Text: "content A"},
		{Kind: sessionlog.KindToolResult, Tool: "read", Text: "content B"},
		{Kind: sessionlog.KindToolResult, Tool: "read", Text: "content C"},
	}
	blocks := groupEntries(entries)
	if len(blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d: %+v", len(blocks), blocks)
	}
	for i, want := range []string{"content A", "content B", "content C"} {
		if blocks[i].kind != blockKindTool {
			t.Errorf("block[%d]: expected tool, got %v", i, blocks[i].kind)
		}
		if blocks[i].toolRunning {
			t.Errorf("block[%d]: expected toolRunning=false", i)
		}
		if blocks[i].toolResult != want {
			t.Errorf("block[%d]: expected toolResult=%q, got %q", i, want, blocks[i].toolResult)
		}
	}
}

func TestGroupEntriesOrphanedToolResultRendersAsTool(t *testing.T) {
	t.Parallel()
	// A tool_result with no preceding tool_start (e.g. log truncation) must
	// render as a collapsed tool block, not as a wall of raw plain-text lines.
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindToolResult, Tool: "read", Text: "1#AA:line one\n2#BB:line two"},
	}
	blocks := groupEntries(entries)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d: %+v", len(blocks), blocks)
	}
	if blocks[0].kind != blockKindTool {
		t.Errorf("expected blockKindTool, got %v", blocks[0].kind)
	}
	if blocks[0].toolRunning {
		t.Error("expected toolRunning=false")
	}
}

func TestRenderTranscriptWidthBounded(t *testing.T) {
	t.Parallel()
	const width = 40
	st := testStyles()
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindAssistant, Text: "This is a longer assistant message that should wrap properly within bounds."},
		{Kind: sessionlog.KindInput, InputKind: "prompt", Text: "Begin the analysis now, please."},
		{Kind: sessionlog.KindToolStart, Tool: "read", Intent: "Reading file"},
		{Kind: sessionlog.KindToolOutput, Text: "output line one\noutput line two"},
		{Kind: sessionlog.KindToolResult, Text: "done"},
		{Kind: sessionlog.KindLifecycle, Stage: "completed", Summary: "All done"},
	}
	output := RenderTranscript(st, entries, width, false, true)
	for line := range strings.SplitSeq(output, "\n") {
		if w := ansi.StringWidth(line); w > width {
			t.Errorf("line width %d > %d: %q", w, width, line)
		}
	}
}

func TestGroupEntriesThinkingBlock(t *testing.T) {
	t.Parallel()
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindThinking, Text: "Let me reason through this step by step."},
	}
	blocks := groupEntries(entries)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d: %+v", len(blocks), blocks)
	}
	if blocks[0].kind != blockKindThinking {
		t.Errorf("expected blockKindThinking, got %v", blocks[0].kind)
	}
	if blocks[0].text != "Let me reason through this step by step." {
		t.Errorf("unexpected text: %q", blocks[0].text)
	}
}

func TestRenderTranscriptThinkingCollapsed(t *testing.T) {
	t.Parallel()
	st := testStyles()
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindThinking, Text: "I need to analyze the code carefully."},
	}
	output := RenderTranscript(st, entries, 80, false, true)
	plain := ansi.Strip(output)
	if !strings.Contains(plain, "Thinking") {
		t.Errorf("expected 'Thinking' label in output, got: %q", plain)
	}
	// In collapsed mode the thinking text should be present as a preview
	if !strings.Contains(plain, "I need to analyze") {
		t.Errorf("expected preview text in collapsed output, got: %q", plain)
	}
	// Must be a single line in collapsed mode
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if len(lines) != 1 {
		t.Errorf("collapsed thinking block: expected 1 line, got %d: %v", len(lines), lines)
	}
}

func TestRenderTranscriptThinkingExpanded(t *testing.T) {
	t.Parallel()
	st := testStyles()
	const thinking = "First I will read the file.\nThen I will edit it."
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindThinking, Text: thinking},
	}
	// collapseThinking=false → full content rendered (verbose flag is irrelevant for thinking)
	output := RenderTranscript(st, entries, 80, false, false)
	plain := ansi.Strip(output)
	if !strings.Contains(plain, "First I will read the file.") {
		t.Errorf("expanded thinking: expected full content in output, got: %q", plain)
	}
	if !strings.Contains(plain, "Then I will edit it.") {
		t.Errorf("expanded thinking: expected full content in output, got: %q", plain)
	}
	// Expanded mode must produce more than one line.
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if len(lines) < 2 {
		t.Errorf("expanded thinking block: expected multiple lines, got %d: %v", len(lines), lines)
	}
}

func TestRenderTranscriptThinkingExpandedIsGrey(t *testing.T) {
	t.Parallel()
	st := testStyles()
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindThinking, Text: "Some reasoning here."},
	}
	// In expanded mode, the full content should be present and styled differently
	// from collapsed (which only shows a single preview line).
	expanded := RenderTranscript(st, entries, 80, false, false)
	collapsed := RenderTranscript(st, entries, 80, false, true)
	// Expanded output should be longer due to full content rendering.
	if len(expanded) <= len(collapsed) {
		t.Errorf("expanded thinking should produce more output than collapsed: expanded=%d collapsed=%d", len(expanded), len(collapsed))
	}
	// The expanded output must contain the actual thinking text.
	if !strings.Contains(ansi.Strip(expanded), "Some reasoning here.") {
		t.Errorf("expanded thinking output missing content: %q", ansi.Strip(expanded))
	}
}

func TestRenderTranscriptThinkingCodeBlockLastLineIndented(t *testing.T) {
	t.Parallel()
	st := testStyles()
	// Thinking content ending with a code fence: glamour appends a trailing
	// ANSI-reset-only line after the closing fence. Without the fix this
	// becomes a 2-space orphan as the last rendered line while all other
	// content lines have 4+ spaces of indent.
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindThinking, Text: "Let me check:\n\n```\nfmt.Println(\"hi\")\n```"},
	}
	output := RenderTranscript(st, entries, 80, false, false)
	plain := ansi.Strip(output)
	lines := strings.Split(strings.TrimRight(plain, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected multiple lines, got %d: %v", len(lines), lines)
	}
	// Every content line after the label must have at least 2 spaces of indent.
	// The last line must not be the degenerate 2-space trailing blank produced
	// by the ANSI-reset glamour artifact.
	for i, line := range lines[1:] { // skip label line
		if strings.TrimSpace(line) == "" {
			// Internal blank lines (paragraph spacing) are allowed; they just
			// must not be the LAST non-empty line following actual content.
			continue
		}
		if !strings.HasPrefix(line, "  ") {
			t.Errorf("content line %d missing 2-space indent: %q", i+1, line)
		}
	}
	// Last non-empty line must contain actual code content, not be blank.
	lastContent := ""
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) != "" {
			lastContent = line
		}
	}
	if !strings.Contains(lastContent, "fmt.Println") {
		t.Errorf("last content line should be the code line, got: %q", lastContent)
	}
}

func TestRenderTranscriptNarrowWidth(t *testing.T) {
	t.Parallel()
	st := testStyles()
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindAssistant, Text: "hi"},
	}
	output := RenderTranscript(st, entries, 10, false, true)
	if output == "" {
		t.Error("expected non-empty output for narrow width")
	}
}

func TestRenderTranscriptToolCardContainsPrimaryArg(t *testing.T) {
	t.Parallel()
	st := testStyles()
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindToolStart, Tool: "read", Intent: "1", Text: `{"path":"x"}`},
	}
	output := RenderTranscript(st, entries, 80, false, true)
	plain := ansi.Strip(output)
	if !strings.Contains(plain, "read") {
		t.Errorf("expected output to contain tool name %q, got: %q", "read", plain)
	}
	if !strings.Contains(plain, "x") {
		t.Errorf("expected output to contain path %q in title, got: %q", "x", plain)
	}
}

func TestRenderTranscriptPromptRendersLabel(t *testing.T) {
	t.Parallel()
	st := testStyles()
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindInput, InputKind: "prompt", Text: "Begin planning"},
	}
	output := RenderTranscript(st, entries, 80, false, true)
	plain := ansi.Strip(output)
	if !strings.Contains(plain, "Prompt") {
		t.Errorf("expected output to contain %q, got: %q", "Prompt", plain)
	}
	if !strings.Contains(plain, "Begin planning") {
		t.Errorf("expected output to contain %q, got: %q", "Begin planning", plain)
	}
}

func TestRenderTranscriptToolOutputTruncatedCollapsed(t *testing.T) {
	t.Parallel()
	st := testStyles()
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindToolStart, Tool: "search"},
	}
	for range 10 {
		entries = append(entries, sessionlog.Entry{Kind: sessionlog.KindToolOutput, Text: "result line"})
	}
	entries = append(entries, sessionlog.Entry{Kind: sessionlog.KindToolResult, Text: "done"})

	output := RenderTranscript(st, entries, 80, false, true)
	plain := ansi.Strip(output)
	if !strings.Contains(plain, "more lines") {
		t.Errorf("expected collapsed output to contain overflow indicator, got: %q", plain)
	}
}

func TestRenderTranscriptToolOutputExpandedVerbose(t *testing.T) {
	t.Parallel()
	st := testStyles()
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindToolStart, Tool: "search"},
	}
	for range 10 {
		entries = append(entries, sessionlog.Entry{Kind: sessionlog.KindToolOutput, Text: "result line"})
	}
	entries = append(entries, sessionlog.Entry{Kind: sessionlog.KindToolResult, Text: "done"})

	collapsed := RenderTranscript(st, entries, 80, false, true)
	verbose := RenderTranscript(st, entries, 80, true, true)

	// Verbose output should not have an overflow indicator for 10 lines (limit=12)
	verbosePlain := ansi.Strip(verbose)
	if strings.Contains(verbosePlain, "more lines") {
		t.Error("expected verbose output to show all 10 lines without overflow indicator")
	}

	// Verbose output should be longer (more content shown) than collapsed
	if len(verbose) <= len(collapsed) {
		t.Errorf("expected verbose output longer than collapsed: verbose=%d collapsed=%d", len(verbose), len(collapsed))
	}
}

func TestRenderTranscriptToolResultMultilineRendersLines(t *testing.T) {
	t.Parallel()
	st := testStyles()
	// read result with 3 lines: all should appear in the rendered output.
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindToolStart, Tool: "read", Intent: "Reading file"},
		{Kind: sessionlog.KindToolResult, Tool: "read", Text: "1#AA:first line\n2#BB:second line\n3#CC:third line"},
	}
	output := RenderTranscript(st, entries, 80, false, true)
	plain := ansi.Strip(output)
	if !strings.Contains(plain, "first line") {
		t.Errorf("multi-line result: expected line 1 present, got: %q", plain)
	}
	if !strings.Contains(plain, "second line") {
		t.Errorf("multi-line result: expected line 2 present, got: %q", plain)
	}
	if !strings.Contains(plain, "third line") {
		t.Errorf("multi-line result: expected line 3 present, got: %q", plain)
	}
	// Content must NOT be collapsed to one line: three distinct content-bearing
	// lines must be present (plus card chrome).
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	contentLines := 0
	for _, l := range lines {
		if strings.Contains(ansi.Strip(l), "#") {
			contentLines++
		}
	}
	if contentLines < 3 {
		t.Errorf("multi-line result: expected 3 content lines visible, got %d in:\n%s", contentLines, plain)
	}
}

func TestRenderTranscriptToolResultMultilineTruncatedCollapsed(t *testing.T) {
	t.Parallel()
	st := testStyles()
	// 7 lines: non-verbose cap is 4, so 3 must be hidden behind overflow indicator.
	var lines []string
	for i := range 7 {
		lines = append(lines, fmt.Sprintf("%d: content line", i+1))
	}
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindToolStart, Tool: "read", Intent: "Reading"},
		{Kind: sessionlog.KindToolResult, Tool: "read", Text: strings.Join(lines, "\n")},
	}
	output := RenderTranscript(st, entries, 80, false, true)
	plain := ansi.Strip(output)
	if !strings.Contains(plain, "more lines") {
		t.Errorf("multi-line result truncated: expected overflow indicator, got: %q", plain)
	}
	if strings.Contains(plain, "7: content line") {
		t.Errorf("multi-line result truncated: line 7 should not be visible in non-verbose mode, got: %q", plain)
	}
}

func TestRenderTranscriptToolResultMultilineVerboseShows12(t *testing.T) {
	t.Parallel()
	st := testStyles()
	// 13 lines: verbose cap is 12, so the last line is hidden.
	var lines []string
	for i := range 13 {
		lines = append(lines, fmt.Sprintf("%d: verbose line", i+1))
	}
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindToolStart, Tool: "read", Intent: "Reading"},
		{Kind: sessionlog.KindToolResult, Tool: "read", Text: strings.Join(lines, "\n")},
	}
	collapsed := RenderTranscript(st, entries, 80, false, true)
	verbose := RenderTranscript(st, entries, 80, true, true)
	verbosePlain := ansi.Strip(verbose)
	// Line 12 visible, line 13 hidden behind overflow indicator.
	if !strings.Contains(verbosePlain, "12: verbose line") {
		t.Errorf("verbose multi-line result: expected line 12 visible, got: %q", verbosePlain)
	}
	if strings.Contains(verbosePlain, "13: verbose line") {
		t.Errorf("verbose multi-line result: line 13 should not be visible at cap 12, got: %q", verbosePlain)
	}
	// Verbose output longer than collapsed.
	if len(verbose) <= len(collapsed) {
		t.Errorf("verbose multi-line result: expected verbose longer than collapsed: verbose=%d collapsed=%d", len(verbose), len(collapsed))
	}
}

func TestRenderTranscriptToolResultSingleLineStaysCompact(t *testing.T) {
	t.Parallel()
	st := testStyles()
	// A single-line result must render as "Result: <value>" on one line.
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindToolStart, Tool: "bash", Intent: "Running"},
		{Kind: sessionlog.KindToolResult, Tool: "bash", Text: "exit 0"},
	}
	output := RenderTranscript(st, entries, 80, false, true)
	plain := ansi.Strip(output)
	if !strings.Contains(plain, "Result:") {
		t.Errorf("single-line result: expected 'Result:' label, got: %q", plain)
	}
	if !strings.Contains(plain, "exit 0") {
		t.Errorf("single-line result: expected result text, got: %q", plain)
	}
	// 'Result:' and 'exit 0' must be on the same line.
	for _, l := range strings.Split(output, "\n") {
		if strings.Contains(ansi.Strip(l), "Result:") {
			if !strings.Contains(ansi.Strip(l), "exit 0") {
				t.Errorf("single-line result: 'Result:' and value must be on same line, got line: %q", l)
			}
		}
	}
}

func TestRenderTranscriptToolResultTrailingNewlineIgnored(t *testing.T) {
	t.Parallel()
	st := testStyles()
	// Result with a trailing newline must not produce a spurious blank line that
	// triggers the multi-line path with an empty last entry.
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindToolStart, Tool: "bash"},
		{Kind: sessionlog.KindToolResult, Tool: "bash", Text: "ok\n"},
	}
	output := RenderTranscript(st, entries, 80, false, true)
	plain := ansi.Strip(output)
	if !strings.Contains(plain, "ok") {
		t.Errorf("trailing-newline result: expected 'ok', got: %q", plain)
	}
	// Must NOT show an overflow indicator — only one real line of content.
	if strings.Contains(plain, "more lines") {
		t.Errorf("trailing-newline result: spurious overflow indicator, got: %q", plain)
	}
}

func TestRenderTranscriptToolResultMultilineWidthBounded(t *testing.T) {
	t.Parallel()
	const width = 40
	st := testStyles()
	// Long lines in a multi-line result must be truncated to fit the card width.
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindToolStart, Tool: "read"},
		{
			Kind: sessionlog.KindToolResult, Tool: "read",
			Text: "short\na very long line that exceeds the forty character card width limit comfortably\nanother line",
		},
	}
	output := RenderTranscript(st, entries, width, false, true)
	for _, line := range strings.Split(output, "\n") {
		if w := ansi.StringWidth(line); w > width {
			t.Errorf("multi-line result: line width %d > %d: %q", w, width, line)
		}
	}
}

func TestRenderTranscriptEmptyEntriesReturnsEmpty(t *testing.T) {
	t.Parallel()
	st := testStyles()
	output := RenderTranscript(st, nil, 80, false, true)
	if output != "" {
		t.Errorf("expected empty string for nil entries, got %q", output)
	}
}

func TestRenderTranscriptLifecycleRendered(t *testing.T) {
	t.Parallel()
	st := testStyles()
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindLifecycle, Stage: "completed", Summary: "done"},
	}
	output := RenderTranscript(st, entries, 80, false, true)
	plain := ansi.Strip(output)
	if !strings.Contains(plain, "done") {
		t.Errorf("expected lifecycle output to contain %q, got: %q", "done", plain)
	}
}

func TestRenderTranscriptCompactionStartRendered(t *testing.T) {
	t.Parallel()
	st := testStyles()
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindLifecycle, Stage: "compaction_start", Message: "Compacting context…"},
	}
	output := RenderTranscript(st, entries, 80, false, true)
	plain := ansi.Strip(output)
	if !strings.Contains(plain, "Compacting") {
		t.Errorf("expected compaction_start output to contain 'Compacting', got: %q", plain)
	}
	for line := range strings.SplitSeq(output, "\n") {
		if w := ansi.StringWidth(line); w > 80 {
			t.Errorf("line width %d > 80: %q", w, line)
		}
	}
}

func TestRenderTranscriptCompactionEndRendered(t *testing.T) {
	t.Parallel()
	st := testStyles()
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindLifecycle, Stage: "compaction_end"},
	}
	output := RenderTranscript(st, entries, 80, false, true)
	plain := ansi.Strip(output)
	if !strings.Contains(plain, "compacted") {
		t.Errorf("expected compaction_end output to contain 'compacted', got: %q", plain)
	}
}

func TestRenderTranscriptCompactionFailedRendered(t *testing.T) {
	t.Parallel()
	st := testStyles()
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindLifecycle, Stage: "compaction_failed", Message: "context overflow recovery failed: rate limited"},
	}
	output := RenderTranscript(st, entries, 80, false, true)
	plain := ansi.Strip(output)
	if !strings.Contains(plain, "Compaction failed") {
		t.Errorf("expected compaction_failed output to contain 'Compaction failed', got: %q", plain)
	}
	if !strings.Contains(plain, "rate limited") {
		t.Errorf("expected compaction_failed output to contain error message, got: %q", plain)
	}
}

func TestGroupEntriesLegacyErrorSetsIsError(t *testing.T) {
	t.Parallel()
	entries := []sessionlog.Entry{
		{Kind: sessionlog.EntryKind("error"), Message: "bridge crashed"},
	}
	blocks := groupEntries(entries)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	b := blocks[0]
	if b.kind != blockKindPlain {
		t.Errorf("expected blockKindPlain, got %v", b.kind)
	}
	if !b.isError {
		t.Error("expected isError=true for EntryKind(\"error\")")
	}
	if b.text != "bridge crashed" {
		t.Errorf("expected text=%q, got %q", "bridge crashed", b.text)
	}
}

func TestRenderTranscriptLegacyErrorShowsPrefix(t *testing.T) {
	t.Parallel()
	st := testStyles()
	entries := []sessionlog.Entry{
		{Kind: sessionlog.EntryKind("error"), Message: "bridge crashed"},
	}
	output := RenderTranscript(st, entries, 80, false, true)
	plain := ansi.Strip(output)
	if !strings.Contains(plain, "Error:") {
		t.Errorf("expected output to contain %q, got: %q", "Error:", plain)
	}
	if !strings.Contains(plain, "bridge crashed") {
		t.Errorf("expected output to contain %q, got: %q", "bridge crashed", plain)
	}
}

func TestRenderTranscriptLegacyErrorWidthBounded(t *testing.T) {
	t.Parallel()
	const width = 40
	st := testStyles()
	// Message long enough to require wrapping with the 7-char Error: prefix overhead.
	entries := []sessionlog.Entry{
		{Kind: sessionlog.EntryKind("error"), Message: "a very long error message that definitely exceeds forty characters when prefixed"},
	}
	output := RenderTranscript(st, entries, width, false, true)
	for line := range strings.SplitSeq(output, "\n") {
		if w := ansi.StringWidth(line); w > width {
			t.Errorf("line width %d > %d: %q", w, width, line)
		}
	}
}

// ---- New tests for spacing, smart args, and thinking ----

func TestRenderTranscriptToolGroupSpacing(t *testing.T) {
	t.Parallel()
	st := testStyles()
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindAssistant, Text: "I will read a file."},
		{Kind: sessionlog.KindToolStart, Tool: "read", Intent: "Reading file", Text: `{"path":"a.go"}`},
		{Kind: sessionlog.KindToolResult, Tool: "read", Text: "file contents"},
		{Kind: sessionlog.KindAssistant, Text: "Done reading."},
	}
	output := RenderTranscript(st, entries, 80, false, true)
	// The rendered output should have blank lines separating the tool block from
	// the surrounding assistant blocks. We verify by checking that two non-empty
	// lines are not directly adjacent at the tool boundary.
	lines := strings.Split(output, "\n")
	// Find index of the first blank line: there should be at least one blank
	// line before the tool block and one after.
	blankCount := 0
	for _, l := range lines {
		if strings.TrimSpace(ansi.Strip(l)) == "" {
			blankCount++
		}
	}
	if blankCount < 2 {
		t.Errorf("expected at least 2 blank separator lines around tool group, got %d in output:\n%s",
			blankCount, ansi.Strip(output))
	}
}

func TestRenderTranscriptConsecutiveToolsOnlyOneGroupSpacer(t *testing.T) {
	t.Parallel()
	st := testStyles()
	// Two consecutive tool calls should get ONE leading blank and ONE trailing blank
	// (not a blank between them).
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindAssistant, Text: "Starting."},
		{Kind: sessionlog.KindToolStart, Tool: "read", Intent: "read A", Text: `{"path":"a"}`},
		{Kind: sessionlog.KindToolResult, Tool: "read", Text: "a contents"},
		{Kind: sessionlog.KindToolStart, Tool: "read", Intent: "read B", Text: `{"path":"b"}`},
		{Kind: sessionlog.KindToolResult, Tool: "read", Text: "b contents"},
		{Kind: sessionlog.KindAssistant, Text: "Done."},
	}
	output := RenderTranscript(st, entries, 80, false, true)
	lines := strings.Split(output, "\n")
	blankCount := 0
	for _, l := range lines {
		if strings.TrimSpace(ansi.Strip(l)) == "" {
			blankCount++
		}
	}
	// Should be exactly 2: one before the group, one after.
	if blankCount != 2 {
		t.Errorf("expected exactly 2 blank spacer lines for one consecutive tool group, got %d in:\n%s",
			blankCount, ansi.Strip(output))
	}
}

func TestToolArgsSummaryRead(t *testing.T) {
	t.Parallel()
	st := testStyles()
	args := `{"path":"internal/tui/views/session_transcript.go","offset":100,"limit":50,"_i":"reading file"}`
	summary := toolArgsSummary(st, "read", args, 80)
	plain := ansi.Strip(summary)
	if !strings.Contains(plain, "L100") {
		t.Errorf("read summary missing offset hint, got: %q", plain)
	}
	if !strings.Contains(plain, "50 lines") {
		t.Errorf("read summary missing limit hint, got: %q", plain)
	}
}

func TestToolArgsSummaryGrep(t *testing.T) {
	t.Parallel()
	st := testStyles()
	args := `{"pattern":"RenderTranscript","path":"internal/tui","glob":"*.go","_i":"searching"}`
	summary := toolArgsSummary(st, "grep", args, 80)
	plain := ansi.Strip(summary)
	if !strings.Contains(plain, "internal/tui") {
		t.Errorf("grep summary missing path, got: %q", plain)
	}
	if !strings.Contains(plain, "*.go") {
		t.Errorf("grep summary missing glob, got: %q", plain)
	}
}

func TestToolArgsSummaryBash(t *testing.T) {
	t.Parallel()
	st := testStyles()
	args := `{"command":"go test ./...","_i":"running tests"}`
	summary := toolArgsSummary(st, "bash", args, 80)
	// command is shown in the title; args summary for bash must be empty
	if summary != "" {
		t.Errorf("bash args summary should be empty (command in title), got: %q", ansi.Strip(summary))
	}
}

func TestToolArgsSummaryUnknownTool(t *testing.T) {
	t.Parallel()
	st := testStyles()
	args := `{"foo":"bar"}`
	summary := toolArgsSummary(st, "unknown_tool_xyz", args, 80)
	// Unknown tool falls back to the raw Args: line.
	plain := ansi.Strip(summary)
	if !strings.Contains(plain, "Args:") {
		t.Errorf("unknown tool should show Args: prefix, got: %q", plain)
	}
}

func TestToolPrimaryArgRead(t *testing.T) {
	t.Parallel()
	args := `{"path":"internal/tui/views/session_transcript.go","offset":100,"limit":50}`
	if got := toolPrimaryArg("read", args); got != "internal/tui/views/session_transcript.go" {
		t.Errorf("read primary arg = %q, want path", got)
	}
}

func TestToolPrimaryArgBash(t *testing.T) {
	t.Parallel()
	args := `{"command":"go test ./...","_i":"tests"}`
	if got := toolPrimaryArg("bash", args); got != "go test ./..." {
		t.Errorf("bash primary arg = %q, want command", got)
	}
}

func TestToolPrimaryArgWrite(t *testing.T) {
	t.Parallel()
	args := `{"path":"src/main.go","content":"package main\n"}`
	if got := toolPrimaryArg("write", args); got != "src/main.go" {
		t.Errorf("write primary arg = %q, want path", got)
	}
}

func TestToolArgsSummaryWrite(t *testing.T) {
	t.Parallel()
	st := testStyles()

	// Multi-line content: line count and first non-empty line must appear.
	args := `{"path":"src/main.go","content":"package main\n\nfunc main() {}\n"}`
	summary := toolArgsSummary(st, "write", args, 80)
	plain := ansi.Strip(summary)
	if !strings.Contains(plain, "3 lines") {
		t.Errorf("write summary missing line count, got: %q", plain)
	}
	if !strings.Contains(plain, "package main") {
		t.Errorf("write summary missing first-line preview, got: %q", plain)
	}

	// Empty content: summary must be empty.
	emptyArgs := `{"path":"src/main.go","content":""}`
	if got := toolArgsSummary(st, "write", emptyArgs, 80); got != "" {
		t.Errorf("write empty content: expected empty summary, got: %q", ansi.Strip(got))
	}
}

func TestRenderTranscriptWriteToolCardWidthBounded(t *testing.T) {
	t.Parallel()
	const width = 60
	st := testStyles()
	entries := []sessionlog.Entry{
		{
			Kind:   sessionlog.KindToolStart,
			Tool:   "write",
			Intent: "Creating file",
			Text:   `{"path":"internal/tui/views/session_transcript.go","content":"package views\n\nimport \"fmt\"\n\nfunc Foo() { fmt.Println(\"hello\") }\n"}`,
		},
		{Kind: sessionlog.KindToolResult, Text: "ok"},
	}
	output := RenderTranscript(st, entries, width, false, true)
	for line := range strings.SplitSeq(output, "\n") {
		if w := ansi.StringWidth(line); w > width {
			t.Errorf("line width %d > %d: %q", w, width, line)
		}
	}
}

func TestRenderTranscriptSmartArgsShownInToolCard(t *testing.T) {
	t.Parallel()
	st := testStyles()
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindToolStart, Tool: "read", Intent: "Reading transcript", Text: `{"path":"internal/tui/views/session_transcript.go","offset":50,"_i":"reading"}`},
	}
	output := RenderTranscript(st, entries, 80, false, true)
	plain := ansi.Strip(output)
	if !strings.Contains(plain, "internal/tui/views/session_transcript.go") {
		t.Errorf("expected smart path summary in tool card, got: %q", plain)
	}
	if !strings.Contains(plain, "L50+") {
		t.Errorf("expected offset hint in tool card, got: %q", plain)
	}
}

func TestRenderTranscriptToolCardWidthBounded(t *testing.T) {
	t.Parallel()
	const width = 60
	st := testStyles()
	entries := []sessionlog.Entry{
		{
			Kind: sessionlog.KindToolStart, Tool: "bash", Intent: "Running tests",
			Text: `{"command":"go test -v ./internal/tui/views/... -run TestRender","_i":"tests"}`,
		},
		{Kind: sessionlog.KindToolResult, Text: "PASS"},
	}
	output := RenderTranscript(st, entries, width, false, true)
	for line := range strings.SplitSeq(output, "\n") {
		if w := ansi.StringWidth(line); w > width {
			t.Errorf("line width %d > %d: %q", w, width, line)
		}
	}
}

// ---- Tests for callout prompt, markdown assistant, hasThinkingBlocks ----

func TestRenderTranscriptPromptRendersAsCallout(t *testing.T) {
	t.Parallel()
	st := testStyles()
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindInput, InputKind: "prompt", Text: "Implement the feature"},
	}
	output := RenderTranscript(st, entries, 80, false, true)
	plain := ansi.Strip(output)
	// Label and body must both be present.
	if !strings.Contains(plain, "Prompt") {
		t.Errorf("prompt callout: expected label 'Prompt', got: %q", plain)
	}
	if !strings.Contains(plain, "Implement the feature") {
		t.Errorf("prompt callout: expected body text, got: %q", plain)
	}
	// Callout renders as multiple lines (border + content).
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if len(lines) < 2 {
		t.Errorf("prompt callout: expected multiple lines for card, got %d: %v", len(lines), lines)
	}
	// All lines must be within width bounds.
	for _, line := range lines {
		if w := ansi.StringWidth(line); w > 80 {
			t.Errorf("prompt callout: line width %d > 80: %q", w, line)
		}
	}
}

func TestRenderTranscriptPromptWidthBounded(t *testing.T) {
	t.Parallel()
	const width = 40
	st := testStyles()
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindInput, InputKind: "message", Text: "Please also update all the tests and make sure nothing is broken."},
	}
	output := RenderTranscript(st, entries, width, false, true)
	for line := range strings.SplitSeq(output, "\n") {
		if w := ansi.StringWidth(line); w > width {
			t.Errorf("line width %d > %d: %q", w, width, line)
		}
	}
}

func TestRenderTranscriptAssistantMarkdown(t *testing.T) {
	t.Parallel()
	st := testStyles()
	// Markdown bold markers must be consumed by the renderer, not rendered literally.
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindAssistant, Text: "I will **read** the file first."},
	}
	output := RenderTranscript(st, entries, 80, false, true)
	plain := ansi.Strip(output)
	if strings.Contains(plain, "**") {
		t.Errorf("assistant markdown: raw ** markers should not appear in output, got: %q", plain)
	}
	if !strings.Contains(plain, "read") {
		t.Errorf("assistant markdown: expected content word 'read', got: %q", plain)
	}
}

func TestHasThinkingBlocks(t *testing.T) {
	t.Parallel()
	if hasThinkingBlocks(nil) {
		t.Error("nil entries: expected false")
	}
	noThinking := []sessionlog.Entry{
		{Kind: sessionlog.KindAssistant, Text: "hi"},
		{Kind: sessionlog.KindToolStart, Tool: "read"},
	}
	if hasThinkingBlocks(noThinking) {
		t.Error("entries with no thinking: expected false")
	}
	withThinking := []sessionlog.Entry{
		{Kind: sessionlog.KindAssistant, Text: "hi"},
		{Kind: sessionlog.KindThinking, Text: "reasoning..."},
	}
	if !hasThinkingBlocks(withThinking) {
		t.Error("entries with thinking: expected true")
	}
}

func TestToolPrimaryArgAskForeman(t *testing.T) {
	t.Parallel()
	args := `{"question":"Should I use a channel or mutex here?","context":"The struct is accessed by multiple goroutines"}`
	if got := toolPrimaryArg("ask_foreman", args); got != "Should I use a channel or mutex here?" {
		t.Errorf("ask_foreman primary arg = %q, want question", got)
	}
}

func TestToolPrimaryArgAskForemanMCP(t *testing.T) {
	t.Parallel()
	args := `{"question":"Which file owns the DB connection?"}`
	if got := toolPrimaryArg("mcp__substrate__ask_foreman", args); got != "Which file owns the DB connection?" {
		t.Errorf("mcp__substrate__ask_foreman primary arg = %q, want question", got)
	}
}

func TestToolArgsSummaryAskForemanWithContext(t *testing.T) {
	t.Parallel()
	st := testStyles()
	args := `{"question":"Should I use a channel or mutex here?","context":"The struct is accessed by multiple goroutines"}`
	summary := toolArgsSummary(st, "ask_foreman", args, 80)
	plain := ansi.Strip(summary)
	if !strings.Contains(plain, "The struct is accessed by multiple goroutines") {
		t.Errorf("ask_foreman summary missing context, got: %q", plain)
	}
}

func TestToolArgsSummaryAskForemanNoContext(t *testing.T) {
	t.Parallel()
	st := testStyles()
	args := `{"question":"Should I use a channel or mutex here?"}`
	summary := toolArgsSummary(st, "ask_foreman", args, 80)
	if summary != "" {
		t.Errorf("ask_foreman no-context summary should be empty, got: %q", ansi.Strip(summary))
	}
}

func TestRenderTranscriptAskForemanToolCardWidthBounded(t *testing.T) {
	t.Parallel()
	const width = 60
	st := testStyles()
	entries := []sessionlog.Entry{
		{
			Kind:   sessionlog.KindToolStart,
			Tool:   "ask_foreman",
			Intent: "Asking foreman",
			Text:   `{"question":"Should I refactor this function?","context":"It is 200 lines and has 5 levels of nesting"}`,
		},
		{Kind: sessionlog.KindToolResult, Text: "Use a mutex with a clear critical section."},
	}
	output := RenderTranscript(st, entries, width, false, true)
	for line := range strings.SplitSeq(output, "\n") {
		if w := ansi.StringWidth(line); w > width {
			t.Errorf("line width %d > %d: %q", w, width, line)
		}
	}
}

func TestRenderTranscriptAskForemanMCPToolCard(t *testing.T) {
	t.Parallel()
	st := testStyles()
	entries := []sessionlog.Entry{
		{
			Kind:   sessionlog.KindToolStart,
			Tool:   "mcp__substrate__ask_foreman",
			Intent: "Asking foreman",
			Text:   `{"question":"Which adapter handles Claude?"}`,
		},
		{Kind: sessionlog.KindToolResult, Text: "The claudeagent adapter."},
	}
	output := RenderTranscript(st, entries, 80, false, true)
	plain := ansi.Strip(output)
	if !strings.Contains(plain, "Which adapter handles Claude?") {
		t.Errorf("expected question in tool card, got: %q", plain)
	}
}
