package adapter

import (
	"fmt"
	"strings"
)

// ErrorCategory classifies errors for UI-friendly message display.
type ErrorCategory int

const (
	CategoryUnknown ErrorCategory = iota
	CategoryNotFound
	CategoryPermissionDenied
	CategoryValidation
	CategoryNetwork
	CategoryRateLimit
	CategoryTimeout
	CategoryServerError
)

// ResourceType identifies what resource the error pertains to.
type ResourceType string

const (
	ResourceProject   ResourceType = "project"
	ResourceIssue     ResourceType = "issue"
	ResourceRepo      ResourceType = "repo"
	ResourceMilestone ResourceType = "milestone"
	ResourceMR        ResourceType = "merge_request"
	ResourceEpic      ResourceType = "epic"
	ResourceGeneric   ResourceType = ""
)

// CategorizedError wraps an error with structured metadata for UI translation.
// Callers can use errors.As to extract the category and provider for display.
type CategorizedError struct {
	Err      error
	Category ErrorCategory
	Provider string // "github", "gitlab", "linear", etc.
	Resource ResourceType
	Details  string // Optional additional context
}

func (e *CategorizedError) Error() string { return e.Err.Error() }
func (e *CategorizedError) Unwrap() error { return e.Err }

// PermissionError is returned by an adapter's HTTP layer when the server
// responds with 401 Unauthorized or 403 Forbidden. These responses are
// permanent — retrying will not help — and require the user to fix their
// credentials or token scopes. Callers that drive a retry loop MUST check
// for this type and skip retries immediately.
type PermissionError struct {
	Adapter    string
	StatusCode int
	Body       string
}

func (e *PermissionError) Error() string {
	return fmt.Sprintf("%s: permission denied (HTTP %d): %s", e.Adapter, e.StatusCode, e.Body)
}

// NewNotFoundError creates a CategorizedError for 404-type responses.
func NewNotFoundError(provider string, resource ResourceType, details string) *CategorizedError {
	return &CategorizedError{
		Err:      fmt.Errorf("%s %s not found", provider, resource),
		Category: CategoryNotFound,
		Provider: provider,
		Resource: resource,
		Details:  details,
	}
}

// NewPermissionError creates a CategorizedError for 401/403 responses.
// This is a convenience wrapper around PermissionError for callers that want
// categorized error behavior.
func NewPermissionError(provider string, statusCode int, body string) *CategorizedError {
	return &CategorizedError{
		Err:      fmt.Errorf("%s: permission denied (HTTP %d)", provider, statusCode),
		Category: CategoryPermissionDenied,
		Provider: provider,
		Resource: ResourceGeneric,
		Details:  body,
	}
}

// NewValidationError creates a CategorizedError for 422/bad request responses.
func NewValidationError(provider string, details string) *CategorizedError {
	return &CategorizedError{
		Err:      fmt.Errorf("%s: validation error", provider),
		Category: CategoryValidation,
		Provider: provider,
		Resource: ResourceGeneric,
		Details:  details,
	}
}

// NewRateLimitError creates a CategorizedError for 429 responses.
func NewRateLimitError(provider string, retryAfter string) *CategorizedError {
	return &CategorizedError{
		Err:      fmt.Errorf("%s: rate limit exceeded", provider),
		Category: CategoryRateLimit,
		Provider: provider,
		Resource: ResourceGeneric,
		Details:  retryAfter,
	}
}

// NewNetworkError creates a CategorizedError for network-level failures.
func NewNetworkError(provider string, originalErr error) *CategorizedError {
	return &CategorizedError{
		Err:      fmt.Errorf("%s: network error: %w", provider, originalErr),
		Category: CategoryNetwork,
		Provider: provider,
		Resource: ResourceGeneric,
	}
}

// NewTimeoutError creates a CategorizedError for timeout failures.
func NewTimeoutError(provider string, originalErr error) *CategorizedError {
	return &CategorizedError{
		Err:      fmt.Errorf("%s: request timed out", provider),
		Category: CategoryTimeout,
		Provider: provider,
		Resource: ResourceGeneric,
	}
}

// NewServerError creates a CategorizedError for 5xx responses.
func NewServerError(provider string, statusCode int, body string) *CategorizedError {
	return &CategorizedError{
		Err:      fmt.Errorf("%s: server error (HTTP %d)", provider, statusCode),
		Category: CategoryServerError,
		Provider: provider,
		Resource: ResourceGeneric,
		Details:  body,
	}
}

// DetectGitLabResource attempts to identify the resource type from GitLab API error body.
func DetectGitLabResource(body string) ResourceType {
	lower := strings.ToLower(body)
	switch {
	case strings.Contains(lower, "project"):
		return ResourceProject
	case strings.Contains(lower, "issue"):
		return ResourceIssue
	case strings.Contains(lower, "merge request"):
		return ResourceMR
	case strings.Contains(lower, "milestone"):
		return ResourceMilestone
	case strings.Contains(lower, "epic"):
		return ResourceEpic
	default:
		return ResourceGeneric
	}
}
