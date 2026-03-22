package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeTestExecutable(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}

	return path
}

func clearSentryEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"SENTRY_AUTH_TOKEN", "SENTRY_URL", "SENTRY_ORG", "SENTRY_PROJECT"} {
		t.Setenv(key, "")
	}
}

func installFakeSentryCLI(t *testing.T) {
	t.Helper()
	binDir := t.TempDir()
	writeTestExecutable(t, binDir, "sentry", "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", binDir)
}

func TestNormalizeSentryBaseURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		raw  string
		want string
	}{
		{raw: "", want: DefaultSentryBaseURL},
		{raw: "https://sentry.io", want: DefaultSentryBaseURL},
		{raw: "https://sentry.example.com/self-hosted", want: "https://sentry.example.com/self-hosted/api/0"},
		{raw: "https://sentry.example.com/self-hosted/api/0", want: "https://sentry.example.com/self-hosted/api/0"},
	}

	for _, tc := range cases {
		if got := NormalizeSentryBaseURL(tc.raw); got != tc.want {
			t.Fatalf("NormalizeSentryBaseURL(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestResolveSentryContextUsesEnvironmentFallbacks(t *testing.T) {
	clearSentryEnv(t)
	t.Setenv("SENTRY_URL", "https://sentry.example.com/self-hosted")
	t.Setenv("SENTRY_PROJECT", "acme/web")

	resolved := ResolveSentryContext(SentryConfig{})
	if resolved.BaseURL != "https://sentry.example.com/self-hosted/api/0" {
		t.Fatalf("BaseURL = %q, want self-hosted API URL", resolved.BaseURL)
	}
	if resolved.Organization != "acme" {
		t.Fatalf("Organization = %q, want %q", resolved.Organization, "acme")
	}
	if len(resolved.Projects) != 1 || resolved.Projects[0] != "web" {
		t.Fatalf("Projects = %#v, want [web]", resolved.Projects)
	}
}

func TestSentryAuthSourceUsesEnvironmentToken(t *testing.T) {
	clearSentryEnv(t)
	t.Setenv("SENTRY_AUTH_TOKEN", "env-token")
	if got := SentryAuthSource(SentryConfig{}); got != "env token" {
		t.Fatalf("SentryAuthSource() = %q, want %q", got, "env token")
	}
	if !SentryAuthConfigured(SentryConfig{}) {
		t.Fatal("SentryAuthConfigured() = false, want true with env token")
	}
}

func TestSentryAuthSourceUsesCLIFallbackWhenPresent(t *testing.T) {
	clearSentryEnv(t)
	installFakeSentryCLI(t)
	if got := SentryAuthSource(SentryConfig{}); got != "sentry cli" {
		t.Fatalf("SentryAuthSource() = %q, want %q", got, "sentry cli")
	}
}

func TestResolveSentryAuthUsesCLIWhenPresent(t *testing.T) {
	clearSentryEnv(t)
	installFakeSentryCLI(t)
	resolved, err := ResolveSentryAuth(context.Background(), SentryConfig{})
	if err != nil {
		t.Fatalf("ResolveSentryAuth() error = %v", err)
	}
	if resolved.Source != "sentry cli" {
		t.Fatalf("ResolveSentryAuth() source = %q, want %q", resolved.Source, "sentry cli")
	}
	if !resolved.UseCLI {
		t.Fatal("ResolveSentryAuth().UseCLI = false, want true when CLI is available")
	}
	if resolved.Token != "" {
		t.Fatalf("ResolveSentryAuth() token = %q, want empty when using CLI", resolved.Token)
	}
}

func TestResolveSentryAuthPrefersConfigTokenOverCLI(t *testing.T) {
	clearSentryEnv(t)
	installFakeSentryCLI(t)
	resolved, err := ResolveSentryAuth(context.Background(), SentryConfig{Token: "token"})
	if err != nil {
		t.Fatalf("ResolveSentryAuth() error = %v", err)
	}
	if resolved.Source != "config token" {
		t.Fatalf("ResolveSentryAuth() source = %q, want %q", resolved.Source, "config token")
	}
	if resolved.Token != "token" {
		t.Fatalf("ResolveSentryAuth() token = %q, want %q", resolved.Token, "token")
	}
	if resolved.UseCLI {
		t.Fatal("ResolveSentryAuth().UseCLI = true, want false when a token is configured")
	}
}

func TestResolveSentryAuthReportsKeychainWhenTokenRefSet(t *testing.T) {
	clearSentryEnv(t)

	resolved, err := ResolveSentryAuth(context.Background(), SentryConfig{TokenRef: "keychain:something"})
	if err != nil {
		t.Fatalf("ResolveSentryAuth() error = %v", err)
	}
	if resolved.Source != "keychain" {
		t.Fatalf("ResolveSentryAuth() source = %q, want %q", resolved.Source, "keychain")
	}
	if resolved.UseCLI {
		t.Fatal("ResolveSentryAuth().UseCLI = true, want false when TokenRef is configured")
	}
}

func TestResolveSentryContextHonorsExplicitDefaultBaseURL(t *testing.T) {
	clearSentryEnv(t)
	t.Setenv("SENTRY_URL", "https://sentry.example.com/self-hosted")

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("adapters:\n  sentry:\n    base_url: https://sentry.io/api/0\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	resolved := ResolveSentryContext(cfg.Adapters.Sentry)
	if resolved.BaseURL != DefaultSentryBaseURL {
		t.Fatalf("BaseURL = %q, want %q", resolved.BaseURL, DefaultSentryBaseURL)
	}
	if !cfg.Adapters.Sentry.BaseURLExplicit {
		t.Fatal("BaseURLExplicit = false, want true for explicit YAML base_url")
	}
}
