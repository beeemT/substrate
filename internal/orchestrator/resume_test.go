package orchestrator

import (
	"strings"
	"testing"

	"github.com/beeemT/substrate/internal/domain"
)

func TestBuildResumeSystemPrompt(t *testing.T) {
	t.Parallel()

	plan := domain.TaskPlan{Content: "implement feature X"}
	result := buildResumeSystemPrompt(plan, "some output")

	for _, want := range []string{
		"## Sub-Plan",
		"implement feature X",
		"## Resume Context",
		"some output",
		"## Instructions",
		"continuing work on this sub-plan",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("expected prompt to contain %q, got:\n%s", want, result)
		}
	}
}

func TestBuildFollowUpSystemPrompt(t *testing.T) {
	t.Parallel()

	plan := domain.TaskPlan{Content: "fix bug Y"}
	result := buildFollowUpSystemPrompt(plan, "done", "also update tests")

	for _, want := range []string{
		"## Sub-Plan",
		"fix bug Y",
		"## Previous Session Summary",
		"done",
		"## Follow-Up Request",
		"also update tests",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("expected prompt to contain %q, got:\n%s", want, result)
		}
	}
}
