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

var revisionPromptTemplate = "The human has requested changes to this plan:\n{{.Feedback}}\n\nThe current full plan is below. Apply the requested changes, keep the orchestrator section distinct from repo sub-plans, and write your revised plan to\n" + bt + "{{.NewSessionDraftPath}}" + bt + ". Preserve the substrate-plan format and keep every repo sub-plan implementation-ready with ### Goal, ### Scope, ### Changes, ### Validation, and ### Risks sections.\n\n--- CURRENT PLAN ---\n{{.CurrentPlan}}\n---\n"
