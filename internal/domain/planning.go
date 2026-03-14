package domain

// RepoPointer contains information about a discovered git-work repository.
type RepoPointer struct {
	// Name is the directory name of the repository.
	Name string
	// Path is the absolute path to the repository root.
	Path string
	// MainDir is the absolute path to the main/ worktree.
	MainDir string
	// Language is the detected primary language (e.g., "go", "typescript", "python").
	Language string
	// Framework is the detected framework (e.g., "gin", "next", "fastapi"). Empty if not detected.
	Framework string
	// AgentsMdPath is the absolute path to AGENTS.md if present in the main worktree. Empty if absent.
	AgentsMdPath string
	// DocPaths are configured documentation paths from repo config. Nil if none.
	DocPaths []string
}

// WorkItemSnapshot is a projection of WorkItem for planning context.
type WorkItemSnapshot struct {
	ID          string
	ExternalID  string
	Title       string
	Description string
	Labels      []string
	Source      string
}

// PlanningContext contains all information needed to run a planning session.
type PlanningContext struct {
	// WorkItem is the work item being planned.
	WorkItem WorkItemSnapshot
	// WorkspaceAgentsMd is the content of the workspace-root AGENTS.md file. Empty if absent.
	WorkspaceAgentsMd string
	// Repos is the list of discovered git-work repositories.
	Repos []RepoPointer
	// SessionID is the ULID for this planning session.
	SessionID string
	// SessionDraftPath is the absolute path to plan-draft.md for this session.
	SessionDraftPath string
	// MaxParseRetries is the maximum number of correction attempts (from config).
	MaxParseRetries int
	// RevisionFeedback is the human's feedback when requesting plan changes.
	// Empty for initial planning sessions.
	RevisionFeedback string
	// CurrentPlanText is the previous plan content for revision context.
	// Empty for initial planning sessions.
	CurrentPlanText string
}

// RawPlanOutput represents the parsed output from a planning agent.
type RawPlanOutput struct {
	// ExecutionGroups is a list of repo groups that can run in parallel.
	// repos in group 0 run first (parallel), then group 1, etc.
	ExecutionGroups [][]string
	// Orchestration is the cross-repo coordination section content.
	Orchestration string
	// SubPlans is the list of parsed sub-plan sections.
	SubPlans []RawSubPlan
	// RawContent is the full markdown content of the plan.
	RawContent string
}

// RawSubPlan represents a parsed sub-plan section from the planning output.
type RawSubPlan struct {
	// RepoName is the repository name from the section heading.
	RepoName string
	// Content is the full markdown content of the sub-plan section.
	Content string
}

// ParseErrors represents errors encountered during plan parsing.
type ParseErrors struct {
	// MissingBlock is true if the substrate-plan YAML block was not found.
	MissingBlock bool
	// InvalidYAML is true if the YAML block could not be parsed.
	InvalidYAML bool
	// YAMLParseError contains the YAML parsing error message if InvalidYAML is true.
	YAMLParseError string
	// UnknownRepos contains repo names in YAML but not in workspace.
	UnknownRepos []string
	// MissingSubPlans contains repo names in YAML but no matching section found.
	MissingSubPlans []string
	// UndeclaredSubPlans contains section headings for repos not in YAML.
	UndeclaredSubPlans []string
	// EmptyExecutionGroups is true if execution_groups is empty or missing.
	EmptyExecutionGroups bool
	// MissingOrchestration is true if the Orchestration section is absent or empty.
	MissingOrchestration bool
	// IncompleteSubPlans contains repo-scoped quality issues for thin or malformed sub-plans.
	IncompleteSubPlans []string
}

// Error implements the error interface.
func (e ParseErrors) Error() string {
	if !e.HasErrors() {
		return ""
	}

	var parts []string
	if e.MissingBlock {
		parts = append(parts, "missing substrate-plan YAML block")
	}
	if e.InvalidYAML {
		parts = append(parts, "invalid YAML: "+e.YAMLParseError)
	}
	if e.EmptyExecutionGroups {
		parts = append(parts, "execution_groups is empty or missing")
	}
	if e.MissingOrchestration {
		parts = append(parts, "missing orchestration section")
	}
	if len(e.UnknownRepos) > 0 {
		parts = append(parts, "unknown repos: "+joinQuoted(e.UnknownRepos))
	}
	if len(e.MissingSubPlans) > 0 {
		parts = append(parts, "missing sub-plan sections: "+joinQuoted(e.MissingSubPlans))
	}
	if len(e.UndeclaredSubPlans) > 0 {
		parts = append(parts, "undeclared sub-plan sections: "+joinQuoted(e.UndeclaredSubPlans))
	}
	if len(e.IncompleteSubPlans) > 0 {
		parts = append(parts, "incomplete sub-plans: "+joinQuoted(e.IncompleteSubPlans))
	}

	result := "Plan parsing failed: "
	for i, p := range parts {
		if i > 0 {
			result += "; "
		}
		result += p
	}
	return result
}

// HasErrors returns true if any parse errors are present.
func (e ParseErrors) HasErrors() bool {
	return e.MissingBlock ||
		e.InvalidYAML ||
		e.EmptyExecutionGroups ||
		e.MissingOrchestration ||
		len(e.UnknownRepos) > 0 ||
		len(e.MissingSubPlans) > 0 ||
		len(e.UndeclaredSubPlans) > 0 ||
		len(e.IncompleteSubPlans) > 0
}

// PlanningWarning represents a non-fatal issue discovered during planning.
type PlanningWarning struct {
	// Type is the warning type (e.g., "plain_git_clone", "pull_failed").
	Type string
	// Message is a human-readable description.
	Message string
	// Path is the affected path (if applicable).
	Path string
}

// PlanningResult contains the result of a planning pipeline run.
type PlanningResult struct {
	// Plan is the generated plan (nil if planning failed).
	Plan *Plan
	// SubPlans is the list of generated sub-plans.
	SubPlans []TaskPlan
	// Warnings is the list of non-fatal warnings.
	Warnings []PlanningWarning
	// ParseErrors contains parsing errors if the plan could not be parsed.
	ParseErrors *ParseErrors
	// Retries is the number of correction attempts made.
	Retries int
}

// WorkspaceHealthCheck contains the result of a workspace health scan.
type WorkspaceHealthCheck struct {
	// GitWorkRepos is the list of git-work initialized repositories (paths).
	GitWorkRepos []string
	// PlainGitClones is the list of plain git clones (warnings).
	PlainGitClones []string
	// PullFailures is the list of repos where git pull failed.
	PullFailures []PullFailure
}

// PullFailure represents a failed git pull operation.
type PullFailure struct {
	// RepoName is the repository name.
	RepoName string
	// Error is the error message.
	Error string
}

// ToPlanningWarnings converts the health check issues to warnings.
func (c WorkspaceHealthCheck) ToPlanningWarnings() []PlanningWarning {
	var warnings []PlanningWarning

	for _, clone := range c.PlainGitClones {
		warnings = append(warnings, PlanningWarning{
			Type:    "plain_git_clone",
			Message: "Plain git clone found; not managed by git-work. Will be excluded from planning.",
			Path:    clone,
		})
	}

	for _, pf := range c.PullFailures {
		warnings = append(warnings, PlanningWarning{
			Type:    "pull_failed",
			Message: "Failed to pull main worktree: " + pf.Error,
			Path:    pf.RepoName,
		})
	}

	return warnings
}

// SessionDirInfo contains information about a planning session directory.
type SessionDirInfo struct {
	// Path is the absolute path to the session directory.
	Path string
	// DraftPath is the absolute path to plan-draft.md.
	DraftPath string
	// Exists is true if the directory already exists.
	Exists bool
}

// helper - quoted join for error messages
func joinQuoted(s []string) string {
	if len(s) == 0 {
		return ""
	}
	result := ""
	for i, v := range s {
		if i > 0 {
			result += ", "
		}
		result += "\"" + v + "\""
	}
	return result
}
