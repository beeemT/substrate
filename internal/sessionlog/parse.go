package sessionlog

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
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
	payload := raw
	if idx := strings.IndexByte(raw, ' '); idx >= 0 && idx+1 < len(raw) && raw[idx+1] == '{' {
		payload = raw[idx+1:]
	}
	if !strings.HasPrefix(payload, "{") {
		return Entry{Kind: KindPlain, Text: raw}, true
	}
	var record struct {
		Type  string `json:"type"`
		Event struct {
			Type      string `json:"type"`
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
		} `json:"event"`
	}
	if err := json.Unmarshal([]byte(payload), &record); err != nil || record.Type != "event" {
		return Entry{Kind: KindPlain, Text: raw}, true
	}
	entry := Entry{
		Kind:      EntryKind(record.Event.Type),
		Text:      record.Event.Text,
		InputKind: record.Event.InputKind,
		Tool:      record.Event.Tool,
		Question:  record.Event.Question,
		Context:   record.Event.Context,
		Uncertain: record.Event.Uncertain,
		Stage:     record.Event.Stage,
		Summary:   record.Event.Summary,
		Message:   record.Event.Message,
		IsError:   record.Event.IsError,
		Intent:    record.Event.Intent,
	}
	if entry.Kind == "" {
		return Entry{Kind: KindPlain, Text: raw}, true
	}

	return entry, true
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
