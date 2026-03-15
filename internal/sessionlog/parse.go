package sessionlog

import (
	"encoding/json"
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
