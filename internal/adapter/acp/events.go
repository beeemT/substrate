package acp

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
)

func (s *Session) handleNotification(method string, params json.RawMessage) {
	if method != "session/update" {
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
	for _, evt := range mapSessionUpdate(p.Update) {
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
		payload := u.Title
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
		return []adapter.AgentEvent{{Type: eventType, Timestamp: now, Payload: contentPayload(u.Content), Metadata: toolMetadata(u)}}
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
	meta := map[string]any{"tool_call_id": u.ToolCallID, "status": u.Status, "acp_update": u.SessionUpdate}
	if u.Kind != "" {
		meta["kind"] = u.Kind
		meta["tool"] = u.Kind
	}
	if u.Title != "" {
		meta["title"] = u.Title
	}
	if len(u.RawInput) > 0 {
		meta["raw_input"] = string(u.RawInput)
	}
	return meta
}

func contentPayload(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var items []struct {
		Type    string      `json:"type"`
		Content textContent `json:"content"`
		Text    string      `json:"text"`
	}
	if err := json.Unmarshal(raw, &items); err == nil {
		var b strings.Builder
		for _, item := range items {
			if item.Text != "" {
				b.WriteString(item.Text)
			} else if item.Content.Text != "" {
				b.WriteString(item.Content.Text)
			}
		}
		if b.Len() > 0 {
			return b.String()
		}
	}
	var single textContent
	if err := json.Unmarshal(raw, &single); err == nil && single.Text != "" {
		return single.Text
	}
	const max = 8192
	if len(raw) > max {
		return string(raw[:max]) + "…"
	}
	return string(raw)
}
