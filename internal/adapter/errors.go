package adapter

import "fmt"

// PermissionError is returned by an adapter's HTTP layer when the server
// responds with 401 Unauthorized or 403 Forbidden. These responses are
// permanent — retrying will not help — and require the user to fix their
// credentials or token scopes. Callers that drive a retry loop MUST check
// for this type and skip retries immediately.
type PermissionError struct {
	Adapter    string // e.g. "github", "gitlab", "linear"
	StatusCode int    // 401 or 403
	Body       string // raw API response body, for logging only
}

func (e *PermissionError) Error() string {
	return fmt.Sprintf("%s: permission denied (HTTP %d): %s", e.Adapter, e.StatusCode, e.Body)
}
