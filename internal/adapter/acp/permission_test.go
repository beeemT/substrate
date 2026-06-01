package acp

import (
	"context"
	"encoding/json"
	"testing"
)

func TestHandlePermissionRequestAutoAllowsSpecAllowAlways(t *testing.T) {
	s := &Session{}
	resp, err := s.handlePermissionRequest(context.Background(), json.RawMessage(`{
		"sessionId":"acp-sess-1",
		"toolCall":{"sessionUpdate":"tool_call","toolCallId":"tc1","title":"Write file","kind":"edit","status":"pending"},
		"options":[
			{"optionId":"allow-once","name":"Allow once","kind":"allow_once"},
			{"optionId":"reject-once","name":"Reject","kind":"reject_once"},
			{"optionId":"allow-always","name":"Allow always","kind":"allow_always"}
		]
	}`))
	if err != nil {
		t.Fatalf("handlePermissionRequest: %v", err)
	}
	if resp.Outcome.Outcome != "selected" || resp.Outcome.OptionID != "allow-always" {
		t.Fatalf("permission response = %#v, want selected allow-always", resp)
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	want := `{"outcome":{"outcome":"selected","optionId":"allow-always"}}`
	if string(data) != want {
		t.Fatalf("marshaled response = %s, want %s", data, want)
	}
}

func TestHandlePermissionRequestAutoAllowsSpecAllowOnce(t *testing.T) {
	s := &Session{}
	resp, err := s.handlePermissionRequest(context.Background(), json.RawMessage(`{
		"sessionId":"acp-sess-1",
		"options":[
			{"optionId":"reject-once","name":"Reject","kind":"reject_once"},
			{"optionId":"allow-once","name":"Allow once","kind":"allow_once"}
		]
	}`))
	if err != nil {
		t.Fatalf("handlePermissionRequest: %v", err)
	}
	if resp.Outcome.Outcome != "selected" || resp.Outcome.OptionID != "allow-once" {
		t.Fatalf("permission response = %#v, want selected allow-once", resp)
	}
}

func TestHandlePermissionRequestCancelsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &Session{}
	resp, err := s.handlePermissionRequest(ctx, json.RawMessage(`{
		"sessionId":"acp-sess-1",
		"options":[{"optionId":"allow-always","name":"Allow always","kind":"allow_always"}]
	}`))
	if err != nil {
		t.Fatalf("handlePermissionRequest: %v", err)
	}
	if resp.Outcome.Outcome != "cancelled" || resp.Outcome.OptionID != "" {
		t.Fatalf("permission response = %#v, want cancelled", resp)
	}
}

func TestHandlePermissionRequestCancelsWithoutAllowOption(t *testing.T) {
	s := &Session{}
	resp, err := s.handlePermissionRequest(context.Background(), json.RawMessage(`{
		"sessionId":"acp-sess-1",
		"options":[
			{"optionId":"reject-once","name":"Reject","kind":"reject_once"},
			{"optionId":"reject-always","name":"Reject always","kind":"reject_always"}
		]
	}`))
	if err != nil {
		t.Fatalf("handlePermissionRequest: %v", err)
	}
	if resp.Outcome.Outcome != "cancelled" || resp.Outcome.OptionID != "" {
		t.Fatalf("permission response = %#v, want cancelled", resp)
	}
}

func TestPermissionOptionDecodesSpecAndLegacyIDs(t *testing.T) {
	var spec permissionOption
	if err := json.Unmarshal([]byte(`{"optionId":"allow-always","name":"Allow always","kind":"allow_always"}`), &spec); err != nil {
		t.Fatalf("unmarshal spec option: %v", err)
	}
	if spec.ID != "allow-always" {
		t.Fatalf("spec ID = %q, want allow-always", spec.ID)
	}

	var legacy permissionOption
	if err := json.Unmarshal([]byte(`{"id":"allow","name":"Allow"}`), &legacy); err != nil {
		t.Fatalf("unmarshal legacy option: %v", err)
	}
	if legacy.ID != "allow" {
		t.Fatalf("legacy ID = %q, want allow", legacy.ID)
	}
}
