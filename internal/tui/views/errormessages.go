package views

import (
	"encoding/json"
	"strings"

	"github.com/beeemT/substrate/internal/adapter"
)

// ErrorCategoryKey is used as a map key for the message catalog.
type ErrorCategoryKey struct {
	Category adapter.ErrorCategory
	Provider string
	Resource adapter.ResourceType
}

// errorMessages maps error category + provider + resource combinations to user-friendly messages.
// Messages can contain placeholders: {Provider}, {Resource}, {Details}
var errorMessages = map[ErrorCategoryKey]string{
	// Not Found errors
	{adapter.CategoryNotFound, "gitlab", adapter.ResourceProject}:   "GitLab project not found",
	{adapter.CategoryNotFound, "gitlab", adapter.ResourceIssue}:     "GitLab issue not found",
	{adapter.CategoryNotFound, "gitlab", adapter.ResourceMR}:        "GitLab merge request not found",
	{adapter.CategoryNotFound, "gitlab", adapter.ResourceEpic}:      "GitLab epic not found",
	{adapter.CategoryNotFound, "gitlab", adapter.ResourceMilestone}: "GitLab milestone not found",
	{adapter.CategoryNotFound, "gitlab", adapter.ResourceGeneric}:   "GitLab resource not found",

	{adapter.CategoryNotFound, "github", adapter.ResourceRepo}:    "GitHub repository not found",
	{adapter.CategoryNotFound, "github", adapter.ResourceIssue}:   "GitHub issue not found",
	{adapter.CategoryNotFound, "github", adapter.ResourceGeneric}: "GitHub resource not found",

	{adapter.CategoryNotFound, "linear", adapter.ResourceIssue}:   "Linear issue not found",
	{adapter.CategoryNotFound, "linear", adapter.ResourceGeneric}: "Linear resource not found",

	// Permission errors
	{adapter.CategoryPermissionDenied, "*", "*"}: "{Provider} access denied. Check your token permissions.",

	// Validation errors
	{adapter.CategoryValidation, "github", "*"}: "GitHub validation error. Check your filters and try again.",

	// Rate limit errors
	{adapter.CategoryRateLimit, "*", "*"}: "{Provider} rate limit exceeded. Try again in a moment.",

	// Network errors
	{adapter.CategoryNetwork, "*", "*"}: "Network error. Check your connection.",

	// Timeout errors
	{adapter.CategoryTimeout, "*", "*"}: "{Provider} request timed out. Try again.",

	// Server errors (5xx)
	{adapter.CategoryServerError, "*", "*"}: "{Provider} server error. Try again in a moment.",
}

// lookupMessage finds the best matching message for an error.
func lookupMessage(category adapter.ErrorCategory, provider string, resource adapter.ResourceType, originalMsg string) string {
	// 1. Try exact match
	key := ErrorCategoryKey{Category: category, Provider: provider, Resource: resource}
	if msg, ok := errorMessages[key]; ok {
		return formatMessage(msg, provider, resource, originalMsg)
	}

	// 2. Try provider wildcard (any resource)
	key = ErrorCategoryKey{Category: category, Provider: provider, Resource: "*"}
	if msg, ok := errorMessages[key]; ok {
		return formatMessage(msg, provider, resource, originalMsg)
	}

	// 3. Try global wildcard (any provider, any resource)
	key = ErrorCategoryKey{Category: category, Provider: "*", Resource: "*"}
	if msg, ok := errorMessages[key]; ok {
		return formatMessage(msg, provider, resource, originalMsg)
	}

	// 4. Fallback: generic error with original message
	return "Error: " + originalMsg
}

// formatMessage replaces placeholders in the message template.
func formatMessage(template, provider string, resource adapter.ResourceType, originalMsg string) string {
	result := template
	// Capitalize provider name: special case for multi-word providers like "github" -> "GitHub"
	var capitalizedProvider string
	switch provider {
	case "github":
		capitalizedProvider = "GitHub"
	case "gitlab":
		capitalizedProvider = "GitLab"
	default:
		capitalizedProvider = strings.ToUpper(provider[:1]) + provider[1:]
	}
	result = strings.ReplaceAll(result, "{Provider}", capitalizedProvider)
	result = strings.ReplaceAll(result, "{Resource}", string(resource))
	result = strings.ReplaceAll(result, "{Details}", originalMsg)
	return result
}

// IsGitHubInvalidSearchError checks for GitHub's specific invalid search error.
// This is a special case that requires parsing the error body for "invalid" code.
func IsGitHubInvalidSearchError(err error) bool {
	errText := err.Error()
	if !strings.Contains(errText, "github api status 422:") {
		return false
	}
	payloadStart := strings.Index(errText, "{")
	if payloadStart < 0 {
		return false
	}

	type githubValidationError struct {
		Message  string `json:"message"`
		Resource string `json:"resource"`
		Field    string `json:"field"`
		Code     string `json:"code"`
	}
	type githubAPIErrorPayload struct {
		Message string                  `json:"message"`
		Errors  []githubValidationError `json:"errors"`
	}

	var payload githubAPIErrorPayload
	if err := json.Unmarshal([]byte(errText[payloadStart:]), &payload); err != nil {
		return false
	}
	for _, validationErr := range payload.Errors {
		if strings.EqualFold(validationErr.Resource, "Search") && strings.EqualFold(validationErr.Field, "q") && strings.EqualFold(validationErr.Code, "invalid") {
			return true
		}
	}

	return false
}
