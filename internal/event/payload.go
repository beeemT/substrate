package event

import (
	"encoding/json"

	"github.com/beeemT/substrate/internal/domain"
)

func WorkItemStatePayload(workItemID, workspaceID string, session domain.Session) string {
	m := map[string]any{
		"work_item_id": workItemID,
		"workspace_id": workspaceID,
		"session":      session,
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func WorkItemIngestedPayload(workspaceID string, session domain.Session) string {
	m := map[string]any{
		"workspace_id": workspaceID,
		"session":      session,
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func SessionPayload(session domain.Task) string {
	m := map[string]any{
		"session_id":   session.ID,
		"work_item_id": session.WorkItemID,
		"workspace_id": session.WorkspaceID,
		"phase":        string(session.Phase),
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func QuestionAnsweredPayload(sessionID, questionID string) string {
	m := map[string]any{
		"session_id":  sessionID,
		"question_id": questionID,
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func ExtractWorkItemID(payload string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		return ""
	}
	if id, ok := m["work_item_id"].(string); ok {
		return id
	}
	return ""
}

func ExtractSessionID(payload string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		return ""
	}
	if id, ok := m["session_id"].(string); ok {
		return id
	}
	return ""
}
