package domain

// SelectionScope identifies the granularity of a work item selection.
type SelectionScope string

const (
	ScopeIssues      SelectionScope = "issues"
	ScopeProjects    SelectionScope = "projects"
	ScopeInitiatives SelectionScope = "initiatives"
	ScopeManual      SelectionScope = "manual"
)
