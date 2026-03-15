package views

import (
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
	output := RenderTranscript(st, entries, width, false)
	for _, line := range strings.Split(output, "\n") {
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
	output := RenderTranscript(st, entries, 80, false)
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

func TestRenderTranscriptThinkingVerbose(t *testing.T) {
	t.Parallel()
	st := testStyles()
	const thinking = "First I will read the file.\nThen I will edit it."
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindThinking, Text: thinking},
	}
	output := RenderTranscript(st, entries, 80, true)
	plain := ansi.Strip(output)
	if !strings.Contains(plain, "First I will read the file.") {
		t.Errorf("verbose thinking: expected full content in output, got: %q", plain)
	}
	if !strings.Contains(plain, "Then I will edit it.") {
		t.Errorf("verbose thinking: expected full content in output, got: %q", plain)
	}
}

func TestRenderTranscriptNarrowWidth(t *testing.T) {
	t.Parallel()
	st := testStyles()
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindAssistant, Text: "hi"},
	}
	output := RenderTranscript(st, entries, 10, false)
	if output == "" {
		t.Error("expected non-empty output for narrow width")
	}
}

func TestRenderTranscriptToolCardContainsNameAndIntent(t *testing.T) {
	t.Parallel()
	st := testStyles()
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindToolStart, Tool: "read", Intent: "Reading guidance", Text: `{"path":"x"}`},
	}
	output := RenderTranscript(st, entries, 80, false)
	plain := ansi.Strip(output)
	if !strings.Contains(plain, "read") {
		t.Errorf("expected output to contain tool name %q, got: %q", "read", plain)
	}
	if !strings.Contains(plain, "Reading guidance") {
		t.Errorf("expected output to contain intent %q, got: %q", "Reading guidance", plain)
	}
}

func TestRenderTranscriptPromptRendersLabel(t *testing.T) {
	t.Parallel()
	st := testStyles()
	entries := []sessionlog.Entry{
		{Kind: sessionlog.KindInput, InputKind: "prompt", Text: "Begin planning"},
	}
	output := RenderTranscript(st, entries, 80, false)
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
	for i := 0; i < 10; i++ {
		entries = append(entries, sessionlog.Entry{Kind: sessionlog.KindToolOutput, Text: "result line"})
	}
	entries = append(entries, sessionlog.Entry{Kind: sessionlog.KindToolResult, Text: "done"})

	output := RenderTranscript(st, entries, 80, false)
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
	for i := 0; i < 10; i++ {
		entries = append(entries, sessionlog.Entry{Kind: sessionlog.KindToolOutput, Text: "result line"})
	}
	entries = append(entries, sessionlog.Entry{Kind: sessionlog.KindToolResult, Text: "done"})

	collapsed := RenderTranscript(st, entries, 80, false)
	verbose := RenderTranscript(st, entries, 80, true)

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

func TestRenderTranscriptEmptyEntriesReturnsEmpty(t *testing.T) {
	t.Parallel()
	st := testStyles()
	output := RenderTranscript(st, nil, 80, false)
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
	output := RenderTranscript(st, entries, 80, false)
	plain := ansi.Strip(output)
	if !strings.Contains(plain, "done") {
		t.Errorf("expected lifecycle output to contain %q, got: %q", "done", plain)
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
	output := RenderTranscript(st, entries, 80, false)
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
	output := RenderTranscript(st, entries, width, false)
	for _, line := range strings.Split(output, "\n") {
		if w := ansi.StringWidth(line); w > width {
			t.Errorf("line width %d > %d: %q", w, width, line)
		}
	}
}
