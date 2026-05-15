package views

import (
	"testing"
)

func TestExtractWorkItemID(t *testing.T) {
	// Real payload from the database
	payload := `{"session":{"ID":"01KR0WZPRFCRY356KBNH45ANKT","WorkItemID":"01KR0NAP6ZW1AAZJGSN6DE4AEE","WorkspaceID":"01KP3EBN5HTYJ7RN86VQQ5EZQP","Phase":"planning","SubPlanID":"","PlanID":"","RepositoryName":"","WorktreePath":"","HarnessName":"omp","Status":"running","PID":null,"StartedAt":"2026-05-07T11:40:59.79306+02:00","CompletedAt":null,"ShutdownAt":null,"ExitCode":null,"OwnerInstanceID":null,"CreatedAt":"2026-05-07T09:40:59.791Z","UpdatedAt":"2026-05-07T11:40:59.79306+02:00","ResumeInfo":null},"work_item_id":"01KR0NAP6ZW1AAZJGSN6DE4AEE","agent_session_id":"01KR0WZPRFCRY356KBNH45ANKT"}`

	extracted := extractWorkItemID(payload)
	if extracted != "01KR0NAP6ZW1AAZJGSN6DE4AEE" {
		t.Errorf("extractWorkItemID = %q, want %q", extracted, "01KR0NAP6ZW1AAZJGSN6DE4AEE")
	}
}

func TestExtractSessionID(t *testing.T) {
	// Real payload from the database
	payload := `{"session":{"ID":"01KR0WZPRFCRY356KBNH45ANKT","WorkItemID":"01KR0NAP6ZW1AAZJGSN6DE4AEE","WorkspaceID":"01KP3EBN5HTYJ7RN86VQQ5EZQP","Phase":"planning","SubPlanID":"","PlanID":"","RepositoryName":"","WorktreePath":"","HarnessName":"omp","Status":"running","PID":null,"StartedAt":"2026-05-07T11:40:59.79306+02:00","CompletedAt":null,"ShutdownAt":null,"ExitCode":null,"OwnerInstanceID":null,"CreatedAt":"2026-05-07T09:40:59.791Z","UpdatedAt":"2026-05-07T11:40:59.79306+02:00","ResumeInfo":null},"work_item_id":"01KR0NAP6ZW1AAZJGSN6DE4AEE","agent_session_id":"01KR0WZPRFCRY356KBNH45ANKT"}`

	extracted := extractSessionID(payload)
	if extracted != "01KR0WZPRFCRY356KBNH45ANKT" {
		t.Errorf("extractSessionID = %q, want %q", extracted, "01KR0WZPRFCRY356KBNH45ANKT")
	}
}
