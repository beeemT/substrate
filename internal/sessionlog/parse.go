package sessionlog

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

type EntryKind string

const (
	KindPlain      EntryKind = "plain"
	KindInput      EntryKind = "input"
	KindAssistant  EntryKind = "assistant_output"
	KindThinking   EntryKind = "thinking_output"
	KindToolStart  EntryKind = "tool_start"
	KindToolOutput EntryKind = "tool_output"
	KindToolResult EntryKind = "tool_result"
	KindQuestion   EntryKind = "question"
	KindForeman    EntryKind = "foreman_proposed"
	KindLifecycle  EntryKind = "lifecycle"
)

type Entry struct {
	Kind      EntryKind
	Text      string
	InputKind string
	Tool      string
	Question  string
	Context   string
	Uncertain bool
	Stage     string
	Summary   string
	Message   string
	IsError   bool
	Intent    string
}

func ParseLine(line string) (Entry, bool) {
	raw := strings.TrimSpace(line)
	if raw == "" {
		return Entry{}, false
	}
	payload, transport := logPayload(raw)
	if !strings.HasPrefix(payload, "{") {
		return Entry{Kind: KindPlain, Text: raw}, true
	}
	if entry, ok, handled := parseACPProtocolPayload(payload); handled {
		return entry, ok
	}
	var record sessionLogRecord
	if err := json.Unmarshal([]byte(payload), &record); err != nil {
		return Entry{Kind: KindPlain, Text: raw}, true
	}
	// ACP transport logs are protocol frames. Non-session/update frames are
	// control-plane noise and should not render as transcript text.
	if transport {
		return Entry{}, false
	}
	// Drop internal metadata lines that are not user-visible events.
	// session_meta carries harness bookkeeping (omp session file/ID) and
	// should never surface in the transcript.
	if record.Type != "event" {
		return Entry{}, false
	}
	entry := record.Event.entry()
	if entry.Kind == "" {
		return Entry{Kind: KindPlain, Text: raw}, true
	}

	return entry, true
}

type sessionLogRecord struct {
	Type  string          `json:"type"`
	Event sessionLogEvent `json:"event"`
}

type sessionLogEvent struct {
	Type      string `json:"type"`
	TypeUpper string `json:"Type"`

	InputKind string `json:"input_kind"`
	Text      string `json:"text"`
	Tool      string `json:"tool"`
	Question  string `json:"question"`
	Context   string `json:"context"`
	Uncertain bool   `json:"uncertain"`
	Stage     string `json:"stage"`
	Summary   string `json:"summary"`
	Message   string `json:"message"`
	IsError   bool   `json:"is_error"`
	Intent    string `json:"intent"`

	Payload  string         `json:"Payload"`
	Metadata map[string]any `json:"Metadata"`
}

func (e sessionLogEvent) entry() Entry {
	kind := firstNonEmpty(e.Type, e.TypeUpper)
	text := firstNonEmpty(e.Text, e.Payload)
	entry := Entry{
		Kind:      EntryKind(kind),
		Text:      text,
		InputKind: e.InputKind,
		Tool:      e.Tool,
		Question:  e.Question,
		Context:   e.Context,
		Uncertain: e.Uncertain,
		Stage:     e.Stage,
		Summary:   e.Summary,
		Message:   e.Message,
		IsError:   e.IsError,
		Intent:    e.Intent,
	}
	if e.Metadata != nil {
		if entry.InputKind == "" {
			entry.InputKind, _ = e.Metadata["input_kind"].(string)
		}
		if entry.Tool == "" {
			entry.Tool, _ = e.Metadata["tool"].(string)
		}
		if entry.Intent == "" {
			entry.Intent, _ = e.Metadata["intent"].(string)
		}
		if !entry.IsError {
			entry.IsError, _ = e.Metadata["is_error"].(bool)
		}
		if entry.Stage == "" {
			entry.Stage, _ = e.Metadata["stage"].(string)
		}
		if entry.Context == "" {
			entry.Context, _ = e.Metadata["context"].(string)
		}
		if v, _ := e.Metadata["thinking"].(bool); v && entry.Kind == "text_delta" {
			entry.Kind = KindThinking
		}
	}
	switch entry.Kind {
	case "text_delta", "assistant_output":
		entry.Kind = KindAssistant
	case "thinking_output":
		entry.Kind = KindThinking
	}
	return entry
}

func logPayload(raw string) (payload string, transport bool) {
	payload = raw
	first, rest, ok := strings.Cut(raw, " ")
	if !ok {
		return payload, false
	}
	if _, err := time.Parse(time.RFC3339Nano, first); err != nil {
		return payload, false
	}
	rest = strings.TrimLeft(rest, " ")
	if strings.HasPrefix(rest, "{") {
		return rest, false
	}
	direction, rest, ok := strings.Cut(rest, " ")
	if !ok {
		return payload, false
	}
	if direction != "in" && direction != "out" && direction != "err" {
		return payload, false
	}
	rest = strings.TrimLeft(rest, " ")
	if !strings.HasPrefix(rest, "{") {
		return payload, false
	}
	return rest, true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

type acpRPCMessage struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  struct {
		Update json.RawMessage `json:"update"`
	} `json:"params"`
}

type acpUpdateEnvelope struct {
	SessionUpdate string `json:"sessionUpdate"`
}

type acpTextContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type acpMessageChunkUpdate struct {
	SessionUpdate string         `json:"sessionUpdate"`
	Content       acpTextContent `json:"content"`
}

type acpToolCallUpdate struct {
	SessionUpdate string          `json:"sessionUpdate"`
	ToolCallID    string          `json:"toolCallId"`
	Title         string          `json:"title,omitempty"`
	Kind          string          `json:"kind,omitempty"`
	Status        string          `json:"status,omitempty"`
	Content       json.RawMessage `json:"content,omitempty"`
	RawInput      json.RawMessage `json:"rawInput,omitempty"`
	RawOutput     json.RawMessage `json:"rawOutput,omitempty"`
}

func parseACPProtocolPayload(payload string) (Entry, bool, bool) {
	var msg acpRPCMessage
	if err := json.Unmarshal([]byte(payload), &msg); err != nil || msg.JSONRPC == "" {
		return Entry{}, false, false
	}
	if msg.Method != "session/update" && !strings.HasSuffix(msg.Method, "/session/update") {
		return Entry{}, false, true
	}
	if len(msg.Params.Update) == 0 {
		return Entry{}, false, true
	}
	return parseACPUpdate(msg.Params.Update)
}

func parseACPUpdate(raw json.RawMessage) (Entry, bool, bool) {
	var env acpUpdateEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return Entry{Kind: KindPlain, Text: "decode ACP update: " + err.Error()}, true, true
	}
	switch env.SessionUpdate {
	case "agent_message_chunk":
		var u acpMessageChunkUpdate
		if err := json.Unmarshal(raw, &u); err != nil || u.Content.Text == "" {
			return Entry{}, false, true
		}
		return Entry{Kind: KindAssistant, Text: u.Content.Text}, true, true
	case "user_message_chunk":
		var u acpMessageChunkUpdate
		if err := json.Unmarshal(raw, &u); err != nil || u.Content.Text == "" {
			return Entry{}, false, true
		}
		return Entry{Kind: KindInput, InputKind: "history", Text: u.Content.Text}, true, true
	case "tool_call":
		var u acpToolCallUpdate
		if err := json.Unmarshal(raw, &u); err != nil {
			return Entry{}, false, true
		}
		return Entry{Kind: KindToolStart, Tool: u.Kind, Intent: u.Title, Text: rawJSONText(u.RawInput)}, true, true
	case "tool_call_update":
		var u acpToolCallUpdate
		if err := json.Unmarshal(raw, &u); err != nil {
			return Entry{}, false, true
		}
		kind := KindToolOutput
		if u.Status == "completed" || u.Status == "failed" || u.Status == "cancelled" {
			kind = KindToolResult
		}
		text := acpContentPayload(u.Content)
		if text == "" {
			text = acpContentPayload(u.RawOutput)
		}
		return Entry{Kind: kind, Tool: u.Kind, Intent: u.Title, Text: text, IsError: u.Status == "failed"}, true, true
	case "tool_call_chunk", "available_commands_update", "current_mode_update", "config_option_update", "session_info_update":
		return Entry{}, false, true
	default:
		return Entry{}, false, true
	}
}

func rawJSONText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	return string(raw)
}

func acpContentPayload(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var items []acpContentItem
	if err := json.Unmarshal(raw, &items); err == nil {
		return joinACPContentItems(items)
	}
	var wrapped struct {
		Items []acpContentItem `json:"items"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil && len(wrapped.Items) > 0 {
		return joinACPContentItems(wrapped.Items)
	}
	var single acpTextContent
	if err := json.Unmarshal(raw, &single); err == nil && single.Text != "" {
		return single.Text
	}
	return ""
}

type acpContentItem struct {
	Text      string         `json:"text"`
	TextUpper string         `json:"Text"`
	Content   acpTextContent `json:"content"`
}

func joinACPContentItems(items []acpContentItem) string {
	var b strings.Builder
	for _, item := range items {
		b.WriteString(firstNonEmpty(item.Text, item.TextUpper, item.Content.Text))
	}
	return b.String()
}

// ScanEntries reads all log entries from r.
// It uses a 10 MB scanner buffer to handle large log lines.
func ScanEntries(r io.Reader) ([]Entry, error) {
	var entries []Entry
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		if entry, ok := ParseLine(scanner.Text()); ok {
			entries = append(entries, entry)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

// ReadFile reads all log entries from a file path.
// Gzip-compressed files (suffix .gz) are decompressed transparently.
func ReadFile(path string) ([]Entry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open session log %s: %w", path, err)
	}
	defer file.Close()

	if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(file)
		if err != nil {
			return nil, fmt.Errorf("open compressed session log %s: %w", path, err)
		}
		defer gz.Close()
		return ScanEntries(gz)
	}

	return ScanEntries(file)
}

// FlattenAssistantOutput concatenates all assistant and plain-text entries
// into a single string, suitable for feeding to a review pipeline.
func FlattenAssistantOutput(entries []Entry) string {
	var b strings.Builder
	for _, e := range entries {
		switch e.Kind {
		case KindAssistant:
			b.WriteString(e.Text)
		case KindPlain:
			b.WriteString(strings.TrimSpace(e.Text))
			b.WriteByte('\n')
		}
	}
	return b.String()
}
