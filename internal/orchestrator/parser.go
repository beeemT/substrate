package orchestrator

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/beeemT/substrate/internal/domain"
	"gopkg.in/yaml.v3"
)

// PlanParser parses planning agent output into structured data.
type PlanParser struct{}

// NewPlanParser creates a new PlanParser.
func NewPlanParser() *PlanParser {
	return &PlanParser{}
}

// Parse reads plan content and extracts structured data.
func (p *PlanParser) Parse(content string) (domain.RawPlanOutput, domain.ParseErrors) {
	var output domain.RawPlanOutput
	var errors domain.ParseErrors

	output.RawContent = content

	// Find the substrate-plan YAML block
	yamlBlock, found := extractYAMLBlock(content, "substrate-plan")
	if !found {
		errors.MissingBlock = true
		return output, errors
	}

	// Parse the YAML
	var planYAML struct {
		ExecutionGroups [][]string `yaml:"execution_groups"`
	}
	if err := yaml.Unmarshal([]byte(yamlBlock), &planYAML); err != nil {
		errors.InvalidYAML = true
		errors.YAMLParseError = err.Error()
		return output, errors
	}

	// Check for empty execution groups
	if len(planYAML.ExecutionGroups) == 0 {
		errors.EmptyExecutionGroups = true
		return output, errors
	}

	output.ExecutionGroups = planYAML.ExecutionGroups

	// Extract orchestration section
	output.Orchestration = extractSection(content, "Orchestration")

	// Extract sub-plan sections
	output.SubPlans = extractSubPlans(content)

	return output, errors
}

// Validate checks that the parsed plan is consistent with discovered repos.
func (p *PlanParser) Validate(output domain.RawPlanOutput, discoveredRepos []domain.RepoPointer) domain.ParseErrors {
	var errors domain.ParseErrors

	// Build a map of discovered repo names (lowercase for case-insensitive matching)
	discoveredNames := make(map[string]string) // lowercase -> original case
	for _, repo := range discoveredRepos {
		discoveredNames[strings.ToLower(repo.Name)] = repo.Name
	}

	// Flatten execution groups to get declared repos
	declaredRepos := flattenExecutionGroups(output.ExecutionGroups)

	// Check for unknown repos
	for _, declared := range declaredRepos {
		if _, found := discoveredNames[strings.ToLower(declared)]; !found {
			errors.UnknownRepos = append(errors.UnknownRepos, declared)
		}
	}

	// Build a map of sub-plan repo names (lowercase)
	subPlanNames := make(map[string]string) // lowercase -> original case
	for _, sp := range output.SubPlans {
		subPlanNames[strings.ToLower(sp.RepoName)] = sp.RepoName
	}

	// Check for missing sub-plans
	for _, declared := range declaredRepos {
		if _, found := subPlanNames[strings.ToLower(declared)]; !found {
			errors.MissingSubPlans = append(errors.MissingSubPlans, declared)
		}
	}

	// Check for undeclared sub-plans
	for _, sp := range output.SubPlans {
		if _, found := discoveredNames[strings.ToLower(sp.RepoName)]; !found {
			// This repo is not in discovered repos - might be a typo in the plan
			errors.UndeclaredSubPlans = append(errors.UndeclaredSubPlans, sp.RepoName)
		} else if !containsRepo(declaredRepos, sp.RepoName) {
			// Repo exists but not declared in execution_groups
			errors.UndeclaredSubPlans = append(errors.UndeclaredSubPlans, sp.RepoName)
		}
	}

	// Deduplicate error lists
	errors.UnknownRepos = dedupe(errors.UnknownRepos)
	errors.MissingSubPlans = dedupe(errors.MissingSubPlans)
	errors.UndeclaredSubPlans = dedupe(errors.UndeclaredSubPlans)

	return errors
}

// ParseAndValidate combines parsing and validation.
func (p *PlanParser) ParseAndValidate(content string, discoveredRepos []domain.RepoPointer) (domain.RawPlanOutput, domain.ParseErrors) {
	output, parseErrors := p.Parse(content)
	if parseErrors.HasErrors() {
		return output, parseErrors
	}

	validationErrors := p.Validate(output, discoveredRepos)
	return output, validationErrors
}

// extractYAMLBlock extracts a fenced code block with the given info string.
func extractYAMLBlock(content, infoString string) (string, bool) {
	// Match ```infoString ... ```
	// Using a regex that handles both ```substrate-plan and ``` substrate-plan
	pattern := fmt.Sprintf("```\\s*%s\\s*\n([\\s\\S]*?)```", regexp.QuoteMeta(infoString))
	re := regexp.MustCompile(pattern)
	match := re.FindStringSubmatch(content)
	if len(match) < 2 {
		return "", false
	}
	return strings.TrimSpace(match[1]), true
}

// extractSection extracts a markdown section by heading name.
func extractSection(content, heading string) string {
	// Find the heading line
	headingPattern := fmt.Sprintf(`(?m)^##\s+%s\s*$`, regexp.QuoteMeta(heading))
	headingRe := regexp.MustCompile(headingPattern)
	headingMatch := headingRe.FindStringIndex(content)
	if headingMatch == nil {
		return ""
	}

	// Find the start of content (after the heading line)
	headingLine := headingRe.FindString(content)
	contentStart := headingMatch[0] + len(headingLine)
	if contentStart >= len(content) {
		return ""
	}

	// Find the next ## heading after contentStart
	nextHeadingRe := regexp.MustCompile(`(?m)^##\s`)
	nextMatch := nextHeadingRe.FindStringIndex(content[contentStart:])
	if nextMatch == nil {
		// No next heading, return rest of content
		return strings.TrimSpace(content[contentStart:])
	}

	return strings.TrimSpace(content[contentStart : contentStart+nextMatch[0]])
}

// extractSubPlans extracts all SubPlan sections from the content.
func extractSubPlans(content string) []domain.RawSubPlan {
	var subPlans []domain.RawSubPlan

	// Find all SubPlan headings: ## SubPlan: <name>
	headingRe := regexp.MustCompile(`(?m)^##\s+SubPlan[:\s]+(\S[^\n]*)\s*$`)

	matches := headingRe.FindAllStringSubmatchIndex(content, -1)
	for i, match := range matches {
		if len(match) < 4 {
			continue
		}

		// match[0] is start of full match, match[1] is end of full match
		// match[2] is start of capture group 1, match[3] is end of capture group 1
		repoName := string(content[match[2]:match[3]])
		repoName = strings.TrimSpace(repoName)
		repoName = strings.TrimRight(repoName, ":-")
		repoName = strings.TrimSpace(repoName)

		// Content starts after the heading line
		contentStart := match[1]
		if contentStart >= len(content) {
			continue
		}

		// Find the next ## heading
		var contentEnd int
		if i+1 < len(matches) {
			contentEnd = matches[i+1][0]
		} else {
			contentEnd = len(content)
		}

		sectionContent := strings.TrimSpace(content[contentStart:contentEnd])

		subPlans = append(subPlans, domain.RawSubPlan{
			RepoName: repoName,
			Content:  sectionContent,
		})
	}

	return subPlans
}

// flattenExecutionGroups flattens execution groups into a deduplicated list.
func flattenExecutionGroups(groups [][]string) []string {
	seen := make(map[string]bool)
	var result []string

	for _, group := range groups {
		for _, repo := range group {
			repo = strings.TrimSpace(repo)
			if repo != "" && !seen[strings.ToLower(repo)] {
				seen[strings.ToLower(repo)] = true
				result = append(result, repo)
			}
		}
	}

	return result
}

// containsRepo checks if a repo name is in the list (case-insensitive).
func containsRepo(repos []string, name string) bool {
	for _, repo := range repos {
		if strings.EqualFold(repo, name) {
			return true
		}
	}
	return false
}

// dedupe removes duplicates from a string slice while preserving order.
func dedupe(s []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, v := range s {
		if !seen[strings.ToLower(v)] {
			seen[strings.ToLower(v)] = true
			result = append(result, v)
		}
	}
	return result
}
