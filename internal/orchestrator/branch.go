package orchestrator

import (
	"regexp"
	"strings"
	"unicode"
)

// Branch naming format: sub-<sanitized-externalID>-<short-slug>
// - externalID: WorkItem.ExternalID (e.g., "LIN-FOO-123" or "gh:issue:owner/repo#42")
// - short-slug: derived from work item title
//   - lowercased
//   - spaces -> dashes
//   - stripped to [a-z0-9-]
//   - consecutive dashes collapsed
//   - leading/trailing dashes trimmed
//   - max 30 chars

const maxSlugLength = 30

// nonAlphaNum and nonBranchSafe are regexps used in slug/ID sanitization.
var (
	nonAlphaNum   = regexp.MustCompile(`[^a-z0-9]+`)
	nonBranchSafe = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)
	multiDash     = regexp.MustCompile(`-+`)
)

// GenerateBranchName creates a branch name from external ID and title.
// Format: sub-<sanitized-externalID>-<short-slug>
//
// Example:
//
//   - externalID: "LIN-FOO-123"
//
//   - title: "Fix auth flow for OAuth2"
//
//   - result: "sub-LIN-FOO-123-fix-auth-flow-for-oauth2"
//
//   - externalID: "gh:issue:rtk-ai/rtk#591"
//
//   - title: "Add support for Oh My Pi"
//
//   - result: "sub-gh-issue-rtk-ai-rtk-591-add-support-for-oh-my-pi"
func GenerateBranchName(externalID, title string) string {
	safeID := sanitizeExternalID(externalID)
	slug := slugFromTitle(title)
	if slug == "" {
		slug = "work"
	}

	return "sub-" + safeID + "-" + slug
}

// sanitizeExternalID replaces characters that are invalid in git branch names
// (colons, slashes, hashes, dots, etc.) with dashes, then collapses and trims.
// Uppercase is preserved so IDs like "LIN-FOO-123" remain readable.
func sanitizeExternalID(id string) string {
	s := nonBranchSafe.ReplaceAllString(id, "-")
	s = multiDash.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
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

// ValidateBranchName checks if a branch name is valid per git check-ref-format rules.
// A valid branch name:
// - Starts with "sub-"
// - Contains no sequences invalid in git ref names (/, :, #, .., @{)
// - Does not end with . or .lock
// - Has non-empty content after "sub-" with no leading or trailing dash
func ValidateBranchName(branch string) bool {
	if !strings.HasPrefix(branch, "sub-") {
		return false
	}
	// Reject sequences that are invalid in git ref names.
	for _, seq := range []string{"/", ":", "#", "..", "@{"} {
		if strings.Contains(branch, seq) {
			return false
		}
	}
	// A branch name must not end with a dot or the .lock suffix (git rule).
	if strings.HasSuffix(branch, ".") || strings.HasSuffix(branch, ".lock") {
		return false
	}
	rest := strings.TrimPrefix(branch, "sub-")

	return rest != "" && !strings.HasPrefix(rest, "-") && !strings.HasSuffix(rest, "-")
}
