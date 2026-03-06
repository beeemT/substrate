package orchestrator

// Backtick constants for templates
const (
	bt = "`"
	tb = "```"
)

// RevisionData contains data for the revision template.
type RevisionData struct {
	Feedback            string
	CurrentPlan         string
	NewSessionDraftPath string
}

var revisionPromptTemplate = "The human has requested changes to this plan:\n{{.Feedback}}\n\nThe current plan is below. Apply the requested changes and write your revised plan to\n" + bt + "{{.NewSessionDraftPath}}" + bt + ". Use the same substrate-plan format.\n\n--- CURRENT PLAN ---\n{{.CurrentPlan}}\n---\n"
