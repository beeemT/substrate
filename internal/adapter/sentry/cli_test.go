package sentry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
)

func writeCLIExecutable(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}

	return path
}

func clearSentryTestEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"SENTRY_AUTH_TOKEN", "SENTRY_URL", "SENTRY_ORG", "SENTRY_PROJECT"} {
		t.Setenv(key, "")
	}
}

func TestListSelectableUsesSentryCLITransport(t *testing.T) {
	clearSentryTestEnv(t)
	binDir := t.TempDir()
	writeCLIExecutable(t, binDir, "sentry", "#!/bin/sh\nif [ \"$1\" = \"auth\" ] && [ \"$2\" = \"status\" ]; then\n  exit 0\nfi\nexit 1\n")
	t.Setenv("PATH", binDir)

	issue := testIssue("101", "SEN-101", "Crash in checkout", "web")
	var seenName string
	var seenArgs []string
	var seenEnv []string
	runner := func(_ context.Context, name string, args []string, env []string) ([]byte, error) {
		seenName = name
		seenArgs = append([]string(nil), args...)
		seenEnv = append([]string(nil), env...)

		return []byte("HTTP/2 200\nLink: <https://ignored>; rel=\"next\"; results=\"true\"; cursor=\"0:100:0\"\nContent-Type: application/json\n\n[" + string(mustJSON(t, issuePayload(issue, 3))) + "]"), nil
	}

	a, err := newWithDeps(context.Background(), config.SentryConfig{BaseURL: "https://sentry.example.com/self-hosted/api/0", Organization: "acme", Projects: []string{"web"}}, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected HTTP request: %s", req.URL.String())

		return nil, nil
	}), runner)
	if err != nil {
		t.Fatalf("newWithDeps: %v", err)
	}

	res, err := a.ListSelectable(context.Background(), adapter.ListOpts{Scope: domain.ScopeIssues, Repo: "web", Limit: 10})
	if err != nil {
		t.Fatalf("ListSelectable: %v", err)
	}
	if seenName != "sentry" {
		t.Fatalf("runner name = %q, want sentry", seenName)
	}
	if len(seenArgs) != 3 || seenArgs[0] != "api" || seenArgs[2] != "--include" {
		t.Fatalf("runner args = %#v, want [api endpoint --include]", seenArgs)
	}
	endpoint, err := url.Parse("https://stub" + seenArgs[1])
	if err != nil {
		t.Fatalf("parse endpoint: %v", err)
	}
	if endpoint.Path != "/organizations/acme/issues/" {
		t.Fatalf("endpoint path = %q, want org issues path", endpoint.Path)
	}
	if endpoint.Query().Get("limit") != "10" {
		t.Fatalf("limit = %q, want 10", endpoint.Query().Get("limit"))
	}
	if got := endpoint.Query().Get("query"); got != "project:web" {
		t.Fatalf("query = %q, want %q", got, "project:web")
	}
	if !slices.Contains(seenEnv, "SENTRY_URL=https://sentry.example.com/self-hosted") {
		t.Fatalf("runner env = %#v, want SENTRY_URL for self-hosted root", seenEnv)
	}
	if len(res.Items) != 1 || res.Items[0].Identifier != "SEN-101" {
		t.Fatalf("items = %#v, want single CLI-backed issue", res.Items)
	}
	if !res.HasMore || res.NextCursor != "0:100:0" {
		t.Fatalf("pagination = %#v, want next cursor", res)
	}
}

func TestNewRequiresOrganizationEvenWithSentryCLIAuth(t *testing.T) {
	clearSentryTestEnv(t)
	binDir := t.TempDir()
	writeCLIExecutable(t, binDir, "sentry", "#!/bin/sh\nif [ \"$1\" = \"auth\" ] && [ \"$2\" = \"status\" ]; then\n  exit 0\nfi\nexit 1\n")
	t.Setenv("PATH", binDir)

	_, err := New(context.Background(), config.SentryConfig{})
	if err == nil {
		t.Fatal("New() error = nil, want missing organization error")
	}
	if !strings.Contains(err.Error(), "sentry organization is required") {
		t.Fatalf("New() error = %q, want organization requirement", err)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	return payload
}
