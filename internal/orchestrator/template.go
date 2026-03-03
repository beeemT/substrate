package orchestrator

import (
	"bytes"
	"text/template"

	"github.com/beeemT/substrate/internal/domain"
)

// Backtick constants for templates
const (
	bt = "`"
	tb = "```"
)

// PlanningTemplate renders the planning prompt for the agent.
type PlanningTemplate struct {
	tmpl *template.Template
}

// NewPlanningTemplate creates a new PlanningTemplate.
func NewPlanningTemplate() (*PlanningTemplate, error) {
	tmpl, err := template.New("planning").Parse(planningPromptTemplate)
	if err != nil {
		return nil, err
	}
	return &PlanningTemplate{tmpl: tmpl}, nil
}

// Render renders the planning prompt with the given context.
func (t *PlanningTemplate) Render(ctx domain.PlanningContext) (string, error) {
	var buf bytes.Buffer
	if err := t.tmpl.Execute(&buf, ctx); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// planningPromptTemplate is the template for planning agent prompts.
// Uses string concatenation to include backtick characters.
var planningPromptTemplate = "{{if .WorkspaceAgentsMd}}## Workspace Guidance\n{{.WorkspaceAgentsMd}}\n{{end}}## Work Item\nTitle: {{.WorkItem.Title}}\nID: {{.WorkItem.ExternalID}}\nDescription:\n{{.WorkItem.Description}}\n\n## Repos\n{{range .Repos}}- {{.Name}} ({{.Language}}{{if .Framework}}/{{.Framework}}{{end}}) - {{.MainDir}}{{if .AgentsMdPath}}\n  guidance: {{.AgentsMdPath}}{{end}}{{if .DocPaths}}\n  docs: {{range .DocPaths}}{{.}} {{end}}{{end}}\n{{end}}\n\n## Instructions\nIf " + bt + "{{.SessionDraftPath}}" + bt + " already exists, read it first to orient yourself before exploring.\nExplore the workspace before finalising your plan. After each significant decision or\nexploration finding, update " + bt + "{{.SessionDraftPath}}" + bt + ". Substrate reads this file as your\nplan output - your final message is not used. The last complete version in the file\nwhen the session ends is what gets executed.\n\nBegin the file with a fenced code block tagged " + bt + "substrate-plan" + bt + " containing YAML:\n\n" + tb + "substrate-plan\nexecution_groups:\n  - [<repo-name>, ...]   # group 1: no dependencies, run first (parallel within group)\n  - [<repo-name>, ...]   # group 2: run after group 1 completes (parallel within group)\n  # add further groups as needed; list only repos that require changes\n" + tb + "\n\nThen write:\n\n## Orchestration\n<cross-repo coordination, shared contracts, data flow, rationale for execution order>\n\n## SubPlan: <repo-name>\n<files to change, approach, tests, edge cases>\n\nOne " + bt + "## SubPlan" + bt + " section per repo listed in " + bt + "execution_groups" + bt + ". Omit repos requiring no changes.\n\n## Validation\nBefore marking complete: run all relevant formatters, compilation checks, and unit tests.\nAll must pass. Refer to AGENTS.md in this repo for tooling specifics.\n"

// CorrectionTemplate renders correction messages for the planning agent.
type CorrectionTemplate struct {
	tmpl *template.Template
}

// NewCorrectionTemplate creates a new CorrectionTemplate.
func NewCorrectionTemplate() (*CorrectionTemplate, error) {
	tmpl, err := template.New("correction").Parse(correctionPromptTemplate)
	if err != nil {
		return nil, err
	}
	return &CorrectionTemplate{tmpl: tmpl}, nil
}

// Render renders the correction prompt with the given data.
func (t *CorrectionTemplate) Render(data CorrectionData) (string, error) {
	var buf bytes.Buffer
	if err := t.tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// CorrectionData contains data for the correction template.
type CorrectionData struct {
	Errors           string
	DiscoveredRepos  []string
	SessionDraftPath string
}

var correctionPromptTemplate = "Your plan had structural errors that prevent execution:\n{{.Errors}}\n\nValid repos in this workspace:\n{{range .DiscoveredRepos}}  - {{.}}\n{{end}}\n\nRe-read {{.SessionDraftPath}} to see your current plan, then address the errors above.\nRewrite {{.SessionDraftPath}} with your complete revised plan. The substrate-plan YAML\nblock must appear first, before any prose.\n"

// RevisionTemplate renders the revision prompt for plan changes.
type RevisionTemplate struct {
	tmpl *template.Template
}

// NewRevisionTemplate creates a new RevisionTemplate.
func NewRevisionTemplate() (*RevisionTemplate, error) {
	tmpl, err := template.New("revision").Parse(revisionPromptTemplate)
	if err != nil {
		return nil, err
	}
	return &RevisionTemplate{tmpl: tmpl}, nil
}

// Render renders the revision prompt with the given data.
func (t *RevisionTemplate) Render(data RevisionData) (string, error) {
	var buf bytes.Buffer
	if err := t.tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// RevisionData contains data for the revision template.
type RevisionData struct {
	Feedback            string
	CurrentPlan         string
	NewSessionDraftPath string
}

var revisionPromptTemplate = "The human has requested changes to this plan:\n{{.Feedback}}\n\nThe current plan is below. Apply the requested changes and write your revised plan to\n" + bt + "{{.NewSessionDraftPath}}" + bt + ". Use the same substrate-plan format.\n\n--- CURRENT PLAN ---\n{{.CurrentPlan}}\n---\n"
