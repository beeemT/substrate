package acp

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/beeemT/substrate/internal/adapter"
)

func (s *Session) handleNotification(method string, params json.RawMessage) {
	if method != "session/update" && !strings.HasSuffix(method, "/session/update") {
		s.emit(adapter.AgentEvent{Type: "tool_output", Timestamp: time.Now(), Payload: method, Metadata: map[string]any{"acp_notification": method}})
		return
	}
	var p sessionUpdateParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.emit(adapter.AgentEvent{Type: "error", Timestamp: time.Now(), Payload: fmt.Sprintf("decode session/update: %v", err)})
		return
	}
	var env updateEnvelope
	if err := json.Unmarshal(p.Update, &env); err == nil && env.SessionUpdate == "available_commands_update" {
		var commands availableCommandsUpdate
		if err := json.Unmarshal(p.Update, &commands); err == nil {
			s.compactMu.Lock()
			s.compact = detectCompactStrategy(s.init, s.acpCfg, commands.AvailableCommands)
			s.compactMu.Unlock()
		}
	}
	// Persist canonical transcript records (not raw protocol frames) for the
	// update types the transcript renders. Control-plane updates are emitted as
	// events for orchestration but are never written to the session log.
	logWorthy := false
	switch env.SessionUpdate {
	case "agent_message_chunk", "user_message_chunk", "tool_call", "tool_call_update":
		logWorthy = true
	}
	for _, evt := range mapSessionUpdate(p.Update) {
		if logWorthy {
			s.writeLogEvent(evt)
		}
		s.emit(evt)
	}
}

func mapSessionUpdate(raw json.RawMessage) []adapter.AgentEvent {
	now := time.Now()
	var env updateEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return []adapter.AgentEvent{{Type: "error", Timestamp: now, Payload: fmt.Sprintf("decode ACP update: %v", err)}}
	}
	switch env.SessionUpdate {
	case "agent_message_chunk":
		var u messageChunkUpdate
		if err := json.Unmarshal(raw, &u); err != nil {
			return []adapter.AgentEvent{{Type: "error", Timestamp: now, Payload: err.Error()}}
		}
		return []adapter.AgentEvent{{Type: "text_delta", Timestamp: now, Payload: u.Content.Text}}
	case "user_message_chunk":
		var u messageChunkUpdate
		if err := json.Unmarshal(raw, &u); err != nil {
			return []adapter.AgentEvent{{Type: "error", Timestamp: now, Payload: err.Error()}}
		}
		return []adapter.AgentEvent{{Type: "input", Timestamp: now, Payload: u.Content.Text, Metadata: map[string]any{"input_kind": "history"}}}
	case "plan":
		var u planUpdate
		if err := json.Unmarshal(raw, &u); err != nil {
			return []adapter.AgentEvent{{Type: "error", Timestamp: now, Payload: err.Error()}}
		}
		parts := make([]string, 0, len(u.Entries))
		for _, entry := range u.Entries {
			line := entry.Content
			if entry.Status != "" {
				line += " [" + entry.Status + "]"
			}
			parts = append(parts, line)
		}
		return []adapter.AgentEvent{{Type: "tool_output", Timestamp: now, Payload: strings.Join(parts, "\n"), Metadata: map[string]any{"acp_update": "plan"}}}
	case "tool_call":
		var u toolCallUpdate
		if err := json.Unmarshal(raw, &u); err != nil {
			return []adapter.AgentEvent{{Type: "error", Timestamp: now, Payload: err.Error()}}
		}
		payload := rawJSONPayload(u.RawInput)
		if payload == "" {
			payload = u.Title
		}
		if payload == "" {
			payload = u.ToolCallID
		}
		return []adapter.AgentEvent{{Type: "tool_start", Timestamp: now, Payload: payload, Metadata: toolMetadata(u)}}
	case "tool_call_update":
		var u toolCallUpdate
		if err := json.Unmarshal(raw, &u); err != nil {
			return []adapter.AgentEvent{{Type: "error", Timestamp: now, Payload: err.Error()}}
		}
		eventType := "tool_output"
		if u.Status == "completed" || u.Status == "failed" || u.Status == "cancelled" {
			eventType = "tool_result"
		}
		payload := contentPayload(u.Content)
		if payload == "" {
			payload = contentPayload(u.RawOutput)
		}
		return []adapter.AgentEvent{{Type: eventType, Timestamp: now, Payload: payload, Metadata: toolMetadata(u)}}
	case "tool_call_chunk":
		return nil
	case "available_commands_update":
		var u availableCommandsUpdate
		if err := json.Unmarshal(raw, &u); err != nil {
			return []adapter.AgentEvent{{Type: "error", Timestamp: now, Payload: err.Error()}}
		}
		return []adapter.AgentEvent{{Type: "tool_output", Timestamp: now, Payload: "", Metadata: map[string]any{"acp_update": "available_commands_update", "available_commands": u.AvailableCommands}}}
	case "current_mode_update", "config_option_update", "session_info_update":
		return []adapter.AgentEvent{{Type: "tool_output", Timestamp: now, Payload: "", Metadata: map[string]any{"acp_update": env.SessionUpdate, "raw": string(raw)}}}
	default:
		return []adapter.AgentEvent{{Type: "tool_output", Timestamp: now, Payload: string(raw), Metadata: map[string]any{"acp_update": env.SessionUpdate}}}
	}
}

func toolMetadata(u toolCallUpdate) map[string]any {
	meta := map[string]any{"tool_call_id": u.ToolCallID, "status": u.Status, "acp_update": u.SessionUpdate, "is_error": u.Status == "failed"}
	if u.Kind != "" {
		meta["kind"] = u.Kind
		meta["tool"] = u.Kind
	} else if u.Title != "" {
		meta["tool"] = u.Title
	}
	if u.Title != "" {
		meta["title"] = u.Title
		meta["intent"] = u.Title
	}
	if rawInput := rawJSONPayload(u.RawInput); rawInput != "" {
		meta["raw_input"] = rawInput
	}
	return meta
}

func rawJSONPayload(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	return string(raw)
}

func contentPayload(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var items []contentItem
	if err := json.Unmarshal(raw, &items); err == nil {
		if text := joinContentItems(items); text != "" {
			return text
		}
	}
	var wrapped struct {
		Items []contentItem `json:"items"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil {
		if text := joinContentItems(wrapped.Items); text != "" {
			return text
		}
	}
	var single textContent
	if err := json.Unmarshal(raw, &single); err == nil && single.Text != "" {
		return single.Text
	}
	const max = 8192
	if len(raw) > max {
		// Truncate at a valid UTF-8 boundary to avoid corrupting multi-byte characters.
		truncated := raw[:max]
		// DecodeLastRune returns (RuneError, 1) for incomplete sequences;
		// loop backward until we land on a valid rune start.
		for len(truncated) > 0 {
			r, size := utf8.DecodeLastRune(truncated)
			// RuneError with size==1 means an invalid/incomplete byte sequence; a valid
			// U+FFFD character is encoded as 3 bytes (size==3) and must not be trimmed.
			if !(r == utf8.RuneError && size == 1) {
				return string(truncated) + "…"
			}
			truncated = truncated[:len(truncated)-size]
		}
		return "…"
	}
	return string(raw)
}

type contentItem struct {
	Type      string      `json:"type"`
	Content   textContent `json:"content"`
	Text      string      `json:"text"`
	TextUpper string      `json:"Text"`
}

func joinContentItems(items []contentItem) string {
	var b strings.Builder
	for _, item := range items {
		switch {
		case item.Text != "":
			b.WriteString(item.Text)
		case item.TextUpper != "":
			b.WriteString(item.TextUpper)
		case item.Content.Text != "":
			b.WriteString(item.Content.Text)
		}
	}
	return b.String()
}
