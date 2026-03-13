package sessionlog

import (
	"encoding/json"
	"fmt"
	"strings"
)

type EntryKind string

const (
	KindPlain      EntryKind = "plain"
	KindInput      EntryKind = "input"
	KindAssistant  EntryKind = "assistant_output"
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

func FormatForTranscript(entry Entry) (string, bool) {
	switch entry.Kind {
	case KindPlain:
		return entry.Text, strings.TrimSpace(entry.Text) != ""
	case KindInput:
		text := strings.TrimSpace(entry.Text)
		if text == "" {
			return "", false
		}
		prefix := "Input"
		switch entry.InputKind {
		case "prompt":
			prefix = "Prompt"
		case "message":
			prefix = "Feedback"
		case "answer":
			prefix = "Answer"
		}
		return formatBlock(prefix+": ", text), true
	case KindAssistant:
		if strings.TrimSpace(entry.Text) == "" {
			return "", false
		}
		return entry.Text, true
	case KindToolStart:
		label := firstNonEmpty(entry.Tool, "tool")
		if strings.TrimSpace(entry.Intent) != "" && strings.TrimSpace(entry.Text) != "" {
			return fmt.Sprintf("Tool: %s — %s\n%s", label, strings.TrimSpace(entry.Intent), formatBlock("  Args: ", strings.TrimSpace(entry.Text))), true
		}
		if strings.TrimSpace(entry.Intent) != "" {
			return fmt.Sprintf("Tool: %s — %s", label, strings.TrimSpace(entry.Intent)), true
		}
		if strings.TrimSpace(entry.Text) != "" {
			return fmt.Sprintf("Tool: %s\n%s", label, formatBlock("  Args: ", strings.TrimSpace(entry.Text))), true
		}
		return "Tool: " + label, true
	case KindToolOutput:
		text := strings.TrimSpace(entry.Text)
		if text == "" {
			return "", false
		}
		return formatBlock("Tool output ["+firstNonEmpty(entry.Tool, "tool")+"]: ", text), true
	case KindToolResult:
		text := strings.TrimSpace(entry.Text)
		status := "result"
		if entry.IsError {
			status = "error"
		}
		prefix := fmt.Sprintf("Tool %s [%s]: ", status, firstNonEmpty(entry.Tool, "tool"))
		if text == "" {
			return strings.TrimSpace(prefix), true
		}
		return formatBlock(prefix, text), true
	case KindQuestion:
		question := strings.TrimSpace(entry.Question)
		if question == "" {
			return "", false
		}
		if ctx := strings.TrimSpace(entry.Context); ctx != "" {
			question += " — " + ctx
		}
		return "Question: " + question, true
	case KindForeman:
		text := strings.TrimSpace(entry.Text)
		if text == "" {
			return "", false
		}
		return formatBlock("Foreman: ", text), true
	case KindLifecycle:
		switch entry.Stage {
		case "started":
			if msg := strings.TrimSpace(entry.Message); msg != "" {
				return msg, true
			}
			return "Session started", true
		case "completed":
			if summary := strings.TrimSpace(entry.Summary); summary != "" {
				return summary, true
			}
			return "Session complete", true
		case "failed":
			msg := strings.TrimSpace(firstNonEmpty(entry.Message, entry.Summary))
			if msg == "" {
				msg = "Session failed"
			}
			return "Failed: " + msg, true
		default:
			text := strings.TrimSpace(firstNonEmpty(entry.Message, entry.Summary, entry.Text))
			if text == "" {
				return "", false
			}
			return text, true
		}
	case EntryKind("error"):
		text := strings.TrimSpace(firstNonEmpty(entry.Message, entry.Text))
		if text == "" {
			return "", false
		}
		return "Error: " + text, true
	case EntryKind("complete"):
		text := strings.TrimSpace(firstNonEmpty(entry.Summary, entry.Text))
		if text == "" {
			return "Session complete", true
		}
		return text, true
	default:
		text := strings.TrimSpace(firstNonEmpty(entry.Text, entry.Message, entry.Summary))
		if text == "" {
			return "", false
		}
		return text, true
	}
}

func formatBlock(prefix, text string) string {
	if text == "" {
		return strings.TrimSpace(prefix)
	}
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return strings.TrimSpace(prefix)
	}
	indent := strings.Repeat(" ", len(prefix))
	for i := range lines {
		if i == 0 {
			lines[i] = prefix + lines[i]
			continue
		}
		lines[i] = indent + lines[i]
	}
	return strings.Join(lines, "\n")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
