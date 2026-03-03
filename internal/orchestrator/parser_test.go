package orchestrator

import (
	"strings"
	"testing"

	"github.com/beeemT/substrate/internal/domain"
)

func TestPlanParser_Parse(t *testing.T) {
	parser := NewPlanParser()

	t.Run("parses valid plan with execution groups", func(t *testing.T) {
		input := buildTestPlan()
		output, errors := parser.Parse(input)

		if errors.HasErrors() {
			t.Fatalf("unexpected parse errors: %s", errors.Error())
		}

		if len(output.ExecutionGroups) != 2 {
			t.Errorf("expected 2 execution groups, got %d", len(output.ExecutionGroups))
		}

		if len(output.ExecutionGroups[0]) != 2 {
			t.Errorf("expected 2 repos in group 0, got %d", len(output.ExecutionGroups[0]))
		}

		if len(output.ExecutionGroups[1]) != 1 {
			t.Errorf("expected 1 repo in group 1, got %d", len(output.ExecutionGroups[1]))
		}
	})

	t.Run("extracts orchestration section", func(t *testing.T) {
		input := buildTestPlan()
		output, errors := parser.Parse(input)

		if errors.HasErrors() {
			t.Fatalf("unexpected parse errors: %s", errors.Error())
		}

		if !strings.Contains(output.Orchestration, "backend-api") {
			t.Errorf("expected orchestration to contain 'backend-api', got %q", output.Orchestration)
		}
	})

	t.Run("extracts sub-plan sections", func(t *testing.T) {
		input := buildTestPlan()
		output, errors := parser.Parse(input)

		if errors.HasErrors() {
			t.Fatalf("unexpected parse errors: %s", errors.Error())
		}

		if len(output.SubPlans) != 3 {
			t.Errorf("expected 3 sub-plans, got %d", len(output.SubPlans))
		}

		repoNames := make(map[string]bool)
		for _, sp := range output.SubPlans {
			repoNames[sp.RepoName] = true
		}

		for _, expected := range []string{"backend-api", "frontend-app", "shared-lib"} {
			if !repoNames[expected] {
				t.Errorf("expected sub-plan for %s", expected)
			}
		}
	})

	t.Run("fails when substrate-plan block missing", func(t *testing.T) {
		input := "Some content without YAML block"
		_, errors := parser.Parse(input)

		if !errors.MissingBlock {
			t.Error("expected MissingBlock error")
		}
	})

	t.Run("fails when YAML is invalid", func(t *testing.T) {
		input := "```substrate-plan\ninvalid: [yaml: content\n```"
		_, errors := parser.Parse(input)

		if !errors.InvalidYAML {
			t.Error("expected InvalidYAML error")
		}
	})

	t.Run("fails when execution_groups empty", func(t *testing.T) {
		input := "```substrate-plan\nexecution_groups: []\n```"
		_, errors := parser.Parse(input)

		if !errors.EmptyExecutionGroups {
			t.Error("expected EmptyExecutionGroups error")
		}
	})
}

func TestPlanParser_Validate(t *testing.T) {
	parser := NewPlanParser()

	discoveredRepos := []domain.RepoPointer{
		{Name: "backend-api"},
		{Name: "frontend-app"},
		{Name: "shared-lib"},
	}

	t.Run("validates repos in execution groups", func(t *testing.T) {
		output := domain.RawPlanOutput{
			ExecutionGroups: [][]string{
				{"backend-api", "shared-lib"},
				{"frontend-app"},
			},
			SubPlans: []domain.RawSubPlan{
				{RepoName: "backend-api", Content: "content"},
				{RepoName: "frontend-app", Content: "content"},
				{RepoName: "shared-lib", Content: "content"},
			},
		}

		errors := parser.Validate(output, discoveredRepos)
		if errors.HasErrors() {
			t.Errorf("unexpected validation errors: %s", errors.Error())
		}
	})

	t.Run("detects unknown repos", func(t *testing.T) {
		output := domain.RawPlanOutput{
			ExecutionGroups: [][]string{
				{"backend-api", "unknown-repo"},
			},
			SubPlans: []domain.RawSubPlan{
				{RepoName: "backend-api", Content: "content"},
				{RepoName: "unknown-repo", Content: "content"},
			},
		}

		errors := parser.Validate(output, discoveredRepos)
		if len(errors.UnknownRepos) == 0 {
			t.Error("expected unknown repo error")
		}
	})

	t.Run("detects missing sub-plans", func(t *testing.T) {
		output := domain.RawPlanOutput{
			ExecutionGroups: [][]string{
				{"backend-api", "frontend-app"},
			},
			SubPlans: []domain.RawSubPlan{
				{RepoName: "backend-api", Content: "content"},
				// Missing frontend-app sub-plan
			},
		}

		errors := parser.Validate(output, discoveredRepos)
		if len(errors.MissingSubPlans) == 0 {
			t.Error("expected missing sub-plan error")
		}
	})

	t.Run("detects undeclared sub-plans", func(t *testing.T) {
		output := domain.RawPlanOutput{
			ExecutionGroups: [][]string{
				{"backend-api"},
			},
			SubPlans: []domain.RawSubPlan{
				{RepoName: "backend-api", Content: "content"},
				{RepoName: "frontend-app", Content: "content"}, // Not in execution_groups
			},
		}

		errors := parser.Validate(output, discoveredRepos)
		if len(errors.UndeclaredSubPlans) == 0 {
			t.Error("expected undeclared sub-plan error")
		}
	})

	t.Run("case insensitive repo matching", func(t *testing.T) {
		output := domain.RawPlanOutput{
			ExecutionGroups: [][]string{
				{"Backend-API", "FRONTEND-APP"},
			},
			SubPlans: []domain.RawSubPlan{
				{RepoName: "backend-api", Content: "content"},
				{RepoName: "frontend-app", Content: "content"},
			},
		}

		errors := parser.Validate(output, discoveredRepos)
		if errors.HasErrors() {
			t.Errorf("expected case-insensitive matching, got errors: %s", errors.Error())
		}
	})
}

func TestParseErrors(t *testing.T) {
	t.Run("Error method formats all errors", func(t *testing.T) {
		err := domain.ParseErrors{
			UnknownRepos:    []string{"unknown-1", "unknown-2"},
			MissingSubPlans: []string{"missing-1"},
		}

		errStr := err.Error()
		if !strings.Contains(errStr, "unknown repos") {
			t.Errorf("expected error to contain 'unknown repos', got %q", errStr)
		}
		if !strings.Contains(errStr, "missing sub-plan sections") {
			t.Errorf("expected error to contain 'missing sub-plan sections', got %q", errStr)
		}
	})

	t.Run("HasErrors returns true when errors present", func(t *testing.T) {
		err := domain.ParseErrors{
			UnknownRepos: []string{"unknown"},
		}
		if !err.HasErrors() {
			t.Error("expected HasErrors to be true")
		}
	})

	t.Run("HasErrors returns false when no errors", func(t *testing.T) {
		err := domain.ParseErrors{}
		if err.HasErrors() {
			t.Error("expected HasErrors to be false")
		}
	})
}

func TestFlattenExecutionGroups(t *testing.T) {
	t.Run("flattens and deduplicates", func(t *testing.T) {
		groups := [][]string{
			{"a", "b"},
			{"b", "c"},
			{"a", "d"},
		}

		result := flattenExecutionGroups(groups)

		if len(result) != 4 {
			t.Errorf("expected 4 unique repos, got %d", len(result))
		}

		// Check all repos are present
		seen := make(map[string]bool)
		for _, r := range result {
			seen[r] = true
		}
		for _, expected := range []string{"a", "b", "c", "d"} {
			if !seen[expected] {
				t.Errorf("expected repo %s in result", expected)
			}
		}
	})

	t.Run("handles empty groups", func(t *testing.T) {
		result := flattenExecutionGroups(nil)
		if len(result) != 0 {
			t.Errorf("expected empty result, got %d", len(result))
		}
	})
}

// buildTestPlan creates a valid test plan string.
func buildTestPlan() string {
	return `# Planning Output

` + "```substrate-plan" + `
execution_groups:
  - [backend-api, shared-lib]
  - [frontend-app]
` + "```" + `

## Orchestration

The backend-api and frontend-app share a REST API contract.
The shared-lib provides utility functions.

## SubPlan: backend-api
Update the user model to support email validation.

## SubPlan: frontend-app
Update the React components to use the new API hooks.

## SubPlan: shared-lib
Update the date formatting utilities.
`
}
