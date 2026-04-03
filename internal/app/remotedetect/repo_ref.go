package remotedetect

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/beeemT/substrate/internal/domain"
)

const defaultBranch = "main"

type ReviewContext struct {
	Platform   Platform
	RemoteName string
	RemoteURL  string
	Review     domain.ReviewRef
}

func ResolveReviewContext(ctx context.Context, dir string) (ReviewContext, error) {
	if strings.TrimSpace(dir) == "" {
		return ReviewContext{}, errors.New("workspace directory is empty")
	}

	remotes, err := resolveRemotes(ctx, dir)
	if err != nil {
		return ReviewContext{}, err
	}
	if len(remotes) == 0 {
		return ReviewContext{}, errors.New("no git remotes found in " + dir)
	}

	headBranch, err := gitOutput(ctx, dir, "branch", "--show-current")
	if err != nil {
		return ReviewContext{}, fmt.Errorf("resolve current branch: %w", err)
	}

	baseRemote, headRemote := chooseReviewRemotes(remotes)

	// Prefer DetectPlatform which includes glab CLI fallback.
	platform, _ := DetectPlatform(ctx, dir, nil)
	if platform == PlatformUnknown {
		platform = platformForHost(baseRemote.Host)
		if platform == PlatformUnknown {
			platform = platformForHost(headRemote.Host)
		}
	}

	baseBranch := detectDefaultBranch(ctx, dir, baseRemote.Name)

	return ReviewContext{
		Platform:   platform,
		RemoteName: headRemote.Name,
		RemoteURL:  headRemote.URL,
		Review: domain.ReviewRef{
			BaseRepo:   baseRemote.RepoRef,
			HeadRepo:   headRemote.RepoRef,
			BaseBranch: baseBranch,
			HeadBranch: strings.TrimSpace(headBranch),
		},
	}, nil
}

// ResolveReviewContextWithBranch resolves remotes and platform from dir,
// using the provided headBranch instead of running git branch --show-current.
// This is necessary for bare repos where --show-current always fails.
func ResolveReviewContextWithBranch(ctx context.Context, dir string, headBranch string) (ReviewContext, error) {
	if strings.TrimSpace(dir) == "" {
		return ReviewContext{}, errors.New("workspace directory is empty")
	}

	remotes, err := resolveRemotes(ctx, dir)
	if err != nil {
		return ReviewContext{}, err
	}
	if len(remotes) == 0 {
		return ReviewContext{}, errors.New("no git remotes found in " + dir)
	}

	baseRemote, headRemote := chooseReviewRemotes(remotes)

	// Prefer DetectPlatform which includes glab CLI fallback.
	platform, _ := DetectPlatform(ctx, dir, nil)
	if platform == PlatformUnknown {
		platform = platformForHost(baseRemote.Host)
		if platform == PlatformUnknown {
			platform = platformForHost(headRemote.Host)
		}
	}

	baseBranch := detectDefaultBranch(ctx, dir, baseRemote.Name)

	return ReviewContext{
		Platform:   platform,
		RemoteName: headRemote.Name,
		RemoteURL:  headRemote.URL,
		Review: domain.ReviewRef{
			BaseRepo:   baseRemote.RepoRef,
			HeadRepo:   headRemote.RepoRef,
			BaseBranch: baseBranch,
			HeadBranch: strings.TrimSpace(headBranch),
		},
	}, nil
}


type resolvedRemote struct {
	Name string
	URL  string
	Host string
	domain.RepoRef
}

func resolveRemotes(ctx context.Context, dir string) ([]resolvedRemote, error) {
	remotesOutput, err := gitOutput(ctx, dir, "remote")
	if err != nil {
		return nil, fmt.Errorf("list git remotes: %w", err)
	}
	remoteNames := strings.Fields(remotesOutput)
	if len(remoteNames) == 0 {
		return nil, nil
	}

	resolved := make([]resolvedRemote, 0, len(remoteNames))
	for _, name := range remoteNames {
		remoteURL, err := gitOutput(ctx, dir, "remote", "get-url", name)
		if err != nil {
			return nil, fmt.Errorf("get git remote url for %s: %w", name, err)
		}
		repoRef, host := parseRepoRefFromRemoteURL(remoteURL)
		resolved = append(resolved, resolvedRemote{Name: name, URL: remoteURL, Host: host, RepoRef: repoRef})
	}

	return resolved, nil
}

func chooseReviewRemotes(remotes []resolvedRemote) (base resolvedRemote, head resolvedRemote) {
	head = remotes[0]
	for _, remote := range remotes {
		if remote.Name == "origin" {
			head = remote

			break
		}
	}
	base = head
	for _, remote := range remotes {
		if remote.Name == "upstream" {
			base = remote

			break
		}
	}

	return base, head
}

func detectDefaultBranch(ctx context.Context, dir, remote string) string {
	if strings.TrimSpace(remote) == "" {
		return defaultBranch
	}
	ref, err := gitOutput(ctx, dir, "symbolic-ref", fmt.Sprintf("refs/remotes/%s/HEAD", remote))
	if err != nil {
		return defaultBranch
	}
	parts := strings.Split(strings.TrimSpace(ref), "/")
	if len(parts) == 0 {
		return defaultBranch
	}
	branch := parts[len(parts)-1]
	if strings.TrimSpace(branch) == "" {
		return defaultBranch
	}

	return branch
}

func parseRepoRefFromRemoteURL(remoteURL string) (domain.RepoRef, string) {
	host := remoteHost(remoteURL)
	owner, repo := parseRemoteOwnerRepo(remoteURL)
	provider := platformForHost(host).String()
	if provider == PlatformUnknown.String() {
		provider = ""
	}

	return domain.RepoRef{Provider: provider, Host: host, Owner: owner, Repo: repo, URL: strings.TrimSpace(remoteURL)}, host
}

func parseRemoteOwnerRepo(remoteURL string) (string, string) {
	trimmed := strings.TrimSpace(remoteURL)
	trimmed = strings.TrimSuffix(trimmed, ".git")
	if strings.Contains(trimmed, "://") {
		parsed, err := url.Parse(trimmed)
		if err != nil {
			return "", ""
		}
		path := strings.Trim(parsed.Path, "/")

		return splitOwnerRepo(path)
	}
	if strings.HasPrefix(trimmed, "git@") || strings.HasPrefix(trimmed, "ssh://") {
		trimmed = strings.TrimPrefix(trimmed, "ssh://")
		if at := strings.Index(trimmed, "@"); at >= 0 {
			trimmed = trimmed[at+1:]
		}
		if colon := strings.Index(trimmed, ":"); colon >= 0 {
			trimmed = trimmed[colon+1:]
		} else if slash := strings.Index(trimmed, "/"); slash >= 0 {
			trimmed = trimmed[slash+1:]
		}

		return splitOwnerRepo(trimmed)
	}

	return "", ""
}

func splitOwnerRepo(path string) (string, string) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 {
		return "", ""
	}
	owner := strings.Join(parts[:len(parts)-1], "/")
	repo := parts[len(parts)-1]

	return owner, repo
}

func platformForHost(host string) Platform {
	switch normalizeHost(host) {
	case "github.com":
		return PlatformGitHub
	case "gitlab.com":
		return PlatformGitLab
	default:
		knownHosts, _ := loadGlabKnownHosts()
		for _, knownHost := range knownHosts {
			if strings.EqualFold(normalizeHost(host), knownHost) {
				return PlatformGitLab
			}
		}

		return PlatformUnknown
	}
}
