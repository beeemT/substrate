package orchestrator

import (
	"regexp"
	"strings"
	"unicode"
)

// Branch naming format: sub-<externalID>-<short-slug>
// - externalID: WorkItem.ExternalID (e.g., "LIN-FOO-123" or "MAN-001")
// - short-slug: derived from work item title
//   - lowercased
//   - spaces -> dashes
//   - stripped to [a-z0-9-]
//   - consecutive dashes collapsed
//   - leading/trailing dashes trimmed
//   - max 30 chars

const maxSlugLength = 30

// slugReplacer is used to replace non-alphanumeric characters with dashes
var (
	nonAlphaNum = regexp.MustCompile(`[^a-z0-9]+`)
	multiDash   = regexp.MustCompile(`-+`)
)

// GenerateBranchName creates a branch name from external ID and title.
// Format: sub-<externalID>-<short-slug>
//
// Example:
//   - externalID: "LIN-FOO-123"
//   - title: "Fix auth flow for OAuth2"
//   - result: "sub-LIN-FOO-123-fix-auth-flow-for-oauth2"
func GenerateBranchName(externalID, title string) string {
	slug := slugFromTitle(title)
	if slug == "" {
		slug = "work"
	}
	return "sub-" + externalID + "-" + slug
}

// slugFromTitle converts a title to a URL-safe slug.
func slugFromTitle(title string) string {
	// Lowercase
	s := strings.ToLower(title)

	// Replace spaces with dashes first
	s = strings.ReplaceAll(s, " ", "-")

	// Remove/replace non-alphanumeric characters (except dashes)
	s = nonAlphaNum.ReplaceAllString(s, "-")

	// Collapse consecutive dashes
	s = multiDash.ReplaceAllString(s, "-")

	// Trim leading and trailing dashes
	s = strings.Trim(s, "-")

	// Truncate to max length
	if len(s) > maxSlugLength {
		s = s[:maxSlugLength]
		// Trim trailing partial character or dash
		s = strings.TrimRightFunc(s, func(r rune) bool {
			return r == '-' || !unicode.IsLetter(r) && !unicode.IsDigit(r)
		})
		// Trim trailing dashes again after truncation
		s = strings.Trim(s, "-")
	}

	return s
}

// ValidateBranchName checks if a branch name is valid.
// A valid branch name:
// - Starts with "sub-"
// - Contains no slashes (avoids git ref namespace collisions)
// - Has non-empty content after "sub-" with no leading or trailing dash
func ValidateBranchName(branch string) bool {
	if !strings.HasPrefix(branch, "sub-") {
		return false
	}
	if strings.Contains(branch, "/") {
		return false
	}
	rest := strings.TrimPrefix(branch, "sub-")
	return rest != "" && !strings.HasPrefix(rest, "-") && !strings.HasSuffix(rest, "-")
}
