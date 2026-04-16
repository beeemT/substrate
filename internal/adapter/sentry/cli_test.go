package sentry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
		if len(args) == 1 && args[0] == "--version" {
			return []byte("0.27.0"), nil
		}
		seenName = name
		seenArgs = append([]string(nil), args...)
		seenEnv = append([]string(nil), env...)

		return []byte("[api] \u2699 < HTTP 200\n[api] \u2699 < Link: <https://ignored>; rel=\"next\"; results=\"true\"; cursor=\"0:100:0\"\n[api] \u2699 < Content-Type: application/json\n[api] \u2699 <\n[" + string(mustJSON(t, issuePayload(issue, 3))) + "]"), nil
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
	if len(seenArgs) != 3 || seenArgs[0] != "api" || seenArgs[2] != "--verbose" {
		t.Fatalf("runner args = %#v, want [api endpoint --verbose]", seenArgs)
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

func TestNewResolvesOrganizationFromCLI(t *testing.T) {
	clearSentryTestEnv(t)
	binDir := t.TempDir()
	writeCLIExecutable(t, binDir, "sentry", "#!/bin/sh\nif [ \"$1\" = \"auth\" ] && [ \"$2\" = \"status\" ]; then\n  exit 0\nfi\nexit 1\n")
	t.Setenv("PATH", binDir)

	orgPayload := `[{"slug":"acme","name":"Acme Corp"}]`
	runner := func(_ context.Context, _ string, args []string, _ []string) ([]byte, error) {
		if len(args) == 1 && args[0] == "--version" {
			return []byte("0.27.0"), nil
		}
		if len(args) >= 3 && args[0] == "org" && args[1] == "list" && args[2] == "--json" {
			return []byte(orgPayload), nil
		}

		return nil, fmt.Errorf("unexpected args: %v", args)
	}
	a, err := newWithDeps(context.Background(), config.SentryConfig{}, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected HTTP request: %s", req.URL.String())

		return nil, nil
	}), runner)
	if err != nil {
		t.Fatalf("newWithDeps: %v", err)
	}
	if a.organization != "acme" {
		t.Fatalf("organization = %q, want %q", a.organization, "acme")
	}
}

func TestNewRequiresOrganizationWhenCLIReturnsMultiple(t *testing.T) {
	clearSentryTestEnv(t)
	binDir := t.TempDir()
	writeCLIExecutable(t, binDir, "sentry", "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", binDir)

	orgPayload := `[{"slug":"acme"},{"slug":"globex"}]`
	runner := func(_ context.Context, _ string, args []string, _ []string) ([]byte, error) {
		if len(args) == 1 && args[0] == "--version" {
			return []byte("0.27.0"), nil
		}
		if len(args) >= 3 && args[0] == "org" && args[1] == "list" {
			return []byte(orgPayload), nil
		}

		return nil, fmt.Errorf("unexpected args: %v", args)
	}
	_, err := newWithDeps(context.Background(), config.SentryConfig{}, roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return nil, nil
	}), runner)
	if err == nil {
		t.Fatal("newWithDeps() error = nil, want multiple orgs error")
	}
	if !strings.Contains(err.Error(), "multiple sentry organizations") {
		t.Fatalf("newWithDeps() error = %q, want multiple orgs hint", err)
	}
}

func TestNewFallsBackToOrgRequiredWhenCLIFails(t *testing.T) {
	clearSentryTestEnv(t)
	binDir := t.TempDir()
	writeCLIExecutable(t, binDir, "sentry", "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", binDir)

	runner := func(_ context.Context, _ string, args []string, _ []string) ([]byte, error) {
		if len(args) == 1 && args[0] == "--version" {
			return []byte("0.27.0"), nil
		}

		return []byte("error"), errors.New("cli failed")
	}
	_, err := newWithDeps(context.Background(), config.SentryConfig{}, roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return nil, nil
	}), runner)
	if err == nil {
		t.Fatal("newWithDeps() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "resolve sentry organization") {
		t.Fatalf("newWithDeps() error = %q, want resolve org error", err)
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

func versionRunner(version string) commandRunner {
	return func(_ context.Context, _ string, args []string, _ []string) ([]byte, error) {
		if len(args) == 1 && args[0] == "--version" {
			return []byte(version), nil
		}

		return nil, fmt.Errorf("unexpected args: %v", args)
	}
}

func TestCheckSentryCLIVersionRejectsTooOld(t *testing.T) {
	t.Parallel()

	for _, version := range []string{"0.26.1", "0.1.0", "0.0.1"} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			err := checkSentryCLIVersion(context.Background(), "", versionRunner(version))
			if err == nil {
				t.Fatalf("checkSentryCLIVersion(%q) error = nil, want version error", version)
			}
			if !strings.Contains(err.Error(), "too old") {
				t.Fatalf("error = %q, want 'too old' hint", err.Error())
			}
			if !strings.Contains(err.Error(), "sentry cli upgrade") {
				t.Fatalf("error = %q, want upgrade command hint", err.Error())
			}
		})
	}
}

func TestCheckSentryCLIVersionAcceptsMinimumAndNewer(t *testing.T) {
	t.Parallel()

	for _, version := range []string{"0.27.0", "0.27.1", "0.28.0", "1.0.0", "sentry 0.27.0", "0.27.0-rc.1"} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			if err := checkSentryCLIVersion(context.Background(), "", versionRunner(version)); err != nil {
				t.Fatalf("checkSentryCLIVersion(%q) error = %v, want nil", version, err)
			}
		})
	}
}

func TestCheckSentryCLIVersionHandlesUnparseable(t *testing.T) {
	t.Parallel()

	// Unrecognisable version must not fail — we warn and proceed to avoid
	// breaking setups where the binary uses an unexpected format.
	if err := checkSentryCLIVersion(context.Background(), "", versionRunner("nightly")); err != nil {
		t.Fatalf("checkSentryCLIVersion(\"nightly\") error = %v, want nil", err)
	}
}

func TestCheckSentryCLIVersionHandlesRunnerFailure(t *testing.T) {
	t.Parallel()

	runner := func(_ context.Context, _ string, _ []string, _ []string) ([]byte, error) {
		return []byte("exec: not found"), errors.New("sentry: command not found")
	}
	err := checkSentryCLIVersion(context.Background(), "", runner)
	if err == nil {
		t.Fatal("checkSentryCLIVersion() error = nil, want runner error")
	}
	if !strings.Contains(err.Error(), "check sentry cli version") {
		t.Fatalf("error = %q, want version check prefix", err.Error())
	}
}

func TestNewWithOldCLIVersionFails(t *testing.T) {
	clearSentryTestEnv(t)
	binDir := t.TempDir()
	writeCLIExecutable(t, binDir, "sentry", "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", binDir)

	oldCLIRunner := func(_ context.Context, _ string, args []string, _ []string) ([]byte, error) {
		if len(args) == 1 && args[0] == "--version" {
			return []byte("0.26.1"), nil
		}
		return nil, fmt.Errorf("unexpected args: %v", args)
	}

	_, err := newWithDeps(context.Background(),
		config.SentryConfig{Organization: "acme"},
		roundTripFunc(func(_ *http.Request) (*http.Response, error) { return nil, nil }),
		oldCLIRunner)
	if err == nil {
		t.Fatal("newWithDeps() error = nil, want version error")
	}
	if !strings.Contains(err.Error(), "too old") {
		t.Fatalf("newWithDeps() error = %q, want 'too old' in message", err.Error())
	}
}
