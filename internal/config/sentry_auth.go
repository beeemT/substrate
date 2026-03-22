package config

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

const DefaultSentryBaseURL = "https://sentry.io/api/0"

const defaultSentryRootURL = "https://sentry.io"

type ResolvedSentryContext struct {
	BaseURL      string
	Organization string
	Projects     []string
}

type ResolvedSentryAuth struct {
	Token        string
	BaseURL      string
	Organization string
	Projects     []string
	Source       string
	UseCLI       bool
}

func HasSentryCLI() bool {
	_, err := exec.LookPath("sentry")
	return err == nil
}

func SentryAuthConfigured(cfg SentryConfig) bool {
	source := SentryAuthSource(cfg)
	return source != "unset"
}

func SentryAuthSource(cfg SentryConfig) string {
	if strings.TrimSpace(cfg.Token) != "" {
		return "config token"
	}
	if strings.TrimSpace(os.Getenv("SENTRY_AUTH_TOKEN")) != "" {
		return "env token"
	}
	if HasSentryCLI() {
		return "sentry cli"
	}
	if strings.TrimSpace(cfg.TokenRef) != "" {
		return "keychain"
	}
	return "unset"
}

func ResolveSentryContext(cfg SentryConfig) ResolvedSentryContext {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	envBaseURL := strings.TrimSpace(os.Getenv("SENTRY_URL"))
	if !cfg.BaseURLExplicit && envBaseURL != "" && (baseURL == "" || baseURL == DefaultSentryBaseURL) {
		baseURL = envBaseURL
	}
	baseURL = NormalizeSentryBaseURL(baseURL)

	organization := strings.TrimSpace(cfg.Organization)
	projects := normalizeSentryProjects(cfg.Projects)

	projectOrg, project := parseSentryProjectEnv(os.Getenv("SENTRY_PROJECT"))
	if organization == "" {
		if projectOrg != "" {
			organization = projectOrg
		} else {
			organization = strings.TrimSpace(os.Getenv("SENTRY_ORG"))
		}
	}
	if len(projects) == 0 && project != "" {
		projects = []string{project}
	}

	return ResolvedSentryContext{
		BaseURL:      baseURL,
		Organization: organization,
		Projects:     projects,
	}
}

func ResolveSentryAuth(ctx context.Context, cfg SentryConfig) (ResolvedSentryAuth, error) {
	resolved := ResolveSentryContext(cfg)
	result := ResolvedSentryAuth{
		BaseURL:      resolved.BaseURL,
		Organization: resolved.Organization,
		Projects:     append([]string(nil), resolved.Projects...),
		Source:       "unset",
	}

	if token := strings.TrimSpace(cfg.Token); token != "" {
		result.Token = token
		result.Source = "config token"
		return result, nil
	}
	if token := strings.TrimSpace(os.Getenv("SENTRY_AUTH_TOKEN")); token != "" {
		result.Token = token
		result.Source = "env token"
		return result, nil
	}

	if HasSentryCLI() {
		result.Source = "sentry cli"
		result.UseCLI = true
		return result, nil
	}

	if strings.TrimSpace(cfg.TokenRef) != "" {
		result.Source = "keychain"
	}
	return result, nil
}

func NormalizeSentryBaseURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return DefaultSentryBaseURL
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimRight(trimmed, "/")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	path := strings.TrimRight(parsed.Path, "/")
	switch {
	case path == "":
		parsed.Path = "/api/0"
	case strings.HasSuffix(path, "/api/0"):
		parsed.Path = path
	case path == "/api":
		parsed.Path = "/api/0"
	default:
		parsed.Path = path + "/api/0"
	}
	return strings.TrimRight(parsed.String(), "/")
}

func SentryRootURL(raw string) string {
	parsed, err := url.Parse(NormalizeSentryBaseURL(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimRight(strings.TrimSpace(raw), "/")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	path := strings.TrimRight(parsed.Path, "/")
	if before, ok := strings.CutSuffix(path, "/api/0"); ok {
		path = before
	}
	if path == "" {
		parsed.Path = ""
	} else {
		parsed.Path = path
	}
	return strings.TrimRight(parsed.String(), "/")
}

func parseSentryProjectEnv(raw string) (string, string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", ""
	}
	org, project, ok := strings.Cut(trimmed, "/")
	if ok {
		return strings.TrimSpace(org), strings.TrimSpace(project)
	}
	return "", trimmed
}

func normalizeSentryProjects(projects []string) []string {
	if len(projects) == 0 {
		return nil
	}
	out := make([]string, 0, len(projects))
	seen := make(map[string]struct{}, len(projects))
	for _, project := range projects {
		project = strings.TrimSpace(project)
		if project == "" {
			continue
		}
		if _, ok := seen[project]; ok {
			continue
		}
		seen[project] = struct{}{}
		out = append(out, project)
	}
	return out
}

func SentryCLIEnvironment(baseURL string) []string {
	env := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "SENTRY_URL=") {
			continue
		}
		env = append(env, entry)
	}
	if root := strings.TrimSpace(SentryRootURL(baseURL)); root != "" && root != defaultSentryRootURL {
		env = append(env, "SENTRY_URL="+root)
	}
	return env
}
