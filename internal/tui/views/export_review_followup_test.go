package views

// Test-only wrappers exposing unexported overlay state for views_test.

// ReviewFollowupStageLoading returns the loading stage value for test comparisons.
func ReviewFollowupStageLoading() reviewFollowupStage { return reviewFollowupStageLoading }

// ReviewFollowupStagePicker returns the picker stage value for test comparisons.
func ReviewFollowupStagePicker() reviewFollowupStage { return reviewFollowupStagePicker }

// ReviewFollowupStageSelector returns the selector stage value for test comparisons.
func ReviewFollowupStageSelector() reviewFollowupStage { return reviewFollowupStageSelector }

// ReviewFollowupStageConfirm returns the confirm stage value for test comparisons.
func ReviewFollowupStageConfirm() reviewFollowupStage { return reviewFollowupStageConfirm }

// ApplyPickerAllForTest promotes every PR from picker selection to the selector stage.
// Tests use this to skip the picker UI when they only care about dispatch logic.
func (m *ReviewFollowupModel) ApplyPickerAllForTest() {
	if m.stage == reviewFollowupStagePicker {
		m.applyPickerSelection()
		return
	}
	// Single-PR path already landed in selector; no-op.
}
