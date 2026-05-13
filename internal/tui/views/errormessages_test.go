package views

import (
	"errors"
	"testing"

	"github.com/beeemT/substrate/internal/adapter"
)

func TestLookupMessage_GitLabProjectNotFound(t *testing.T) {
	t.Parallel()

	err := adapter.NewNotFoundError("gitlab", adapter.ResourceProject, "")
	msg := lookupMessage(err.Category, err.Provider, err.Resource, err.Error())
	if msg != "GitLab project not found" {
		t.Errorf("got %q, want %q", msg, "GitLab project not found")
	}
}

func TestLookupMessage_GitLabIssueNotFound(t *testing.T) {
	t.Parallel()

	err := adapter.NewNotFoundError("gitlab", adapter.ResourceIssue, "")
	msg := lookupMessage(err.Category, err.Provider, err.Resource, err.Error())
	if msg != "GitLab issue not found" {
		t.Errorf("got %q, want %q", msg, "GitLab issue not found")
	}
}

func TestLookupMessage_GitHubRepoNotFound(t *testing.T) {
	t.Parallel()

	err := adapter.NewNotFoundError("github", adapter.ResourceRepo, "")
	msg := lookupMessage(err.Category, err.Provider, err.Resource, err.Error())
	if msg != "GitHub repository not found" {
		t.Errorf("got %q, want %q", msg, "GitHub repository not found")
	}
}

func TestLookupMessage_PermissionDenied(t *testing.T) {
	t.Parallel()

	err := adapter.NewPermissionError("gitlab", 401, "Unauthorized")
	msg := lookupMessage(err.Category, err.Provider, err.Resource, err.Error())
	if msg != "GitLab access denied. Check your token permissions." {
		t.Errorf("got %q, want %q", msg, "GitLab access denied. Check your token permissions.")
	}
}

func TestLookupMessage_RateLimit(t *testing.T) {
	t.Parallel()

	err := adapter.NewRateLimitError("github", "60")
	msg := lookupMessage(err.Category, err.Provider, err.Resource, err.Error())
	if msg != "GitHub rate limit exceeded. Try again in a moment." {
		t.Errorf("got %q, want %q", msg, "GitHub rate limit exceeded. Try again in a moment.")
	}
}

func TestLookupMessage_NetworkError(t *testing.T) {
	t.Parallel()

	err := adapter.NewNetworkError("gitlab", errors.New("connection refused"))
	msg := lookupMessage(err.Category, err.Provider, err.Resource, err.Error())
	if msg != "Network error. Check your connection." {
		t.Errorf("got %q, want %q", msg, "Network error. Check your connection.")
	}
}

func TestLookupMessage_TimeoutError(t *testing.T) {
	t.Parallel()

	err := adapter.NewTimeoutError("linear", errors.New("context deadline exceeded"))
	msg := lookupMessage(err.Category, err.Provider, err.Resource, err.Error())
	if msg != "Linear request timed out. Try again." {
		t.Errorf("got %q, want %q", msg, "Linear request timed out. Try again.")
	}
}

func TestLookupMessage_ServerError(t *testing.T) {
	t.Parallel()

	err := adapter.NewServerError("github", 500, "Internal Server Error")
	msg := lookupMessage(err.Category, err.Provider, err.Resource, err.Error())
	if msg != "GitHub server error. Try again in a moment." {
		t.Errorf("got %q, want %q", msg, "GitHub server error. Try again in a moment.")
	}
}

func TestLookupMessage_UnknownCategory(t *testing.T) {
	t.Parallel()

	// When there's no matching entry, it falls back to the original message
	msg := lookupMessage(999, "unknown", adapter.ResourceGeneric, "some error")
	if msg != "Error: some error" {
		t.Errorf("got %q, want %q", msg, "Error: some error")
	}
}

func TestFormatOperationErrorToast_CategorizedError(t *testing.T) {
	t.Parallel()

	err := adapter.NewNotFoundError("gitlab", adapter.ResourceProject, "")
	msg := formatOperationErrorToast(err)
	if msg != "GitLab project not found" {
		t.Errorf("got %q, want %q", msg, "GitLab project not found")
	}
}

func TestFormatOperationErrorToast_GitHubInvalidSearchError(t *testing.T) {
	t.Parallel()

	// This tests the legacy GitHub-specific error handling
	err := errors.New("github api status 422: {\"message\":\"Validation Failed\",\"errors\":[{\"resource\":\"Search\",\"field\":\"q\",\"code\":\"invalid\"}]}")
	msg := formatOperationErrorToast(err)
	expected := "Error: GitHub can't search one or more selected owners/repos.\nCheck the Owner/Repo filters or your repository access."
	if msg != expected {
		t.Errorf("got %q, want %q", msg, expected)
	}
}

func TestFormatOperationErrorToast_Fallback(t *testing.T) {
	t.Parallel()

	// Regular error without categorization falls back to "Error: <message>"
	err := errors.New("something went wrong")
	msg := formatOperationErrorToast(err)
	if msg != "Error: something went wrong" {
		t.Errorf("got %q, want %q", msg, "Error: something went wrong")
	}
}

func TestFormatOperationErrorToast_PermissionDenied(t *testing.T) {
	t.Parallel()

	err := adapter.NewPermissionError("github", 403, "Forbidden")
	msg := formatOperationErrorToast(err)
	expected := "GitHub access denied. Check your token permissions."
	if msg != expected {
		t.Errorf("got %q, want %q", msg, expected)
	}
}

func TestIsGitHubInvalidSearchError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "valid invalid search error",
			err:      errors.New("github api status 422: {\"message\":\"Validation Failed\",\"errors\":[{\"resource\":\"Search\",\"field\":\"q\",\"code\":\"invalid\"}]}"),
			expected: true,
		},
		{
			name:     "different 422 error",
			err:      errors.New("github api status 422: {\"message\":\"Validation Failed\"}"),
			expected: false,
		},
		{
			name:     "different status code",
			err:      errors.New("github api status 404: Not Found"),
			expected: false,
		},
		{
			name:     "not a github error",
			err:      errors.New("gitlab api status 404: Not Found"),
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsGitHubInvalidSearchError(tc.err)
			if got != tc.expected {
				t.Errorf("IsGitHubInvalidSearchError() = %v, want %v", got, tc.expected)
			}
		})
	}
}
