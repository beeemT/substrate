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

var revisionPromptTemplate = "Your role is still to plan only. The human has requested changes to this plan:\n" +
	"{{.Feedback}}\n\n" +
	"The current full plan is below. Apply the requested changes, keep the orchestrator section distinct from repo sub-plans, " +
	"and write your revised plan to\n" +
	bt + "{{.NewSessionDraftPath}}" + bt + ". Preserve the substrate-plan format " +
	"and keep every repo sub-plan implementation-ready with ### Goal, ### Scope, ### Changes, ### Validation, and ### Risks sections.\n\n" +
	"--- CURRENT PLAN ---\n" +
	"{{.CurrentPlan}}\n" +
	"---\n"

// RepoResultSummary holds the implementation outcome for a single repository.
type RepoResultSummary struct {
	RepoName string
	Status   string
	LogTail  string
}

// FollowUpData provides context for the follow-up planning template.
type FollowUpData struct {
	Feedback            string
	CurrentPlan         string
	RepoResults         []RepoResultSummary
	NewSessionDraftPath string
}

var followUpPromptTemplate = "Your role is still to plan only. " +
	"This work item was previously implemented and the human has requested changes based on the results:\n" + `

{{.Feedback}}

## Previous Implementation Results

{{range .RepoResults}}### {{.RepoName}} ({{.Status}})
` + tb + `
{{.LogTail}}
` + tb + `

{{end}}## Current Plan

{{.CurrentPlan}}

---

Produce an updated plan. You MAY:
- Add new sub-plans for repositories that need changes
- Modify existing sub-plans where the implementation was insufficient
- Remove sub-plans that are no longer needed

You MUST NOT reproduce sub-plans for repositories where no changes are needed.
Write the revised plan to ` + bt + `{{.NewSessionDraftPath}}` + bt + `. Preserve the substrate-plan format and keep every repo sub-plan implementation-ready with ### Goal, ### Scope, ### Changes, ### Validation, and ### Risks sections.
`
