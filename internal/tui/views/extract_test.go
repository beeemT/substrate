package views

import (
	"testing"
)

func TestExtractWorkItemID(t *testing.T) {
	payload := `{"work_item_id":"01KR0NAP6ZW1AAZJGSN6DE4AEE"}`
	extracted := extractWorkItemID(payload)
	if extracted != "01KR0NAP6ZW1AAZJGSN6DE4AEE" {
		t.Errorf("extractWorkItemID = %q, want %q", extracted, "01KR0NAP6ZW1AAZJGSN6DE4AEE")
	}
}
