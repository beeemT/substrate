// Package app wires together adapters and services from configuration.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/beeemT/substrate/internal/adapter"
	githubadapter "github.com/beeemT/substrate/internal/adapter/github"
	gitlabadapter "github.com/beeemT/substrate/internal/adapter/gitlab"
	gladapter "github.com/beeemT/substrate/internal/adapter/glab"
	linearadapter "github.com/beeemT/substrate/internal/adapter/linear"
	manualadapter "github.com/beeemT/substrate/internal/adapter/manual"
	sentryadapter "github.com/beeemT/substrate/internal/adapter/sentry"
	"github.com/beeemT/substrate/internal/app/remotedetect"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/gitwork"
	"github.com/beeemT/substrate/internal/service"
)

// BuildWorkItemAdapters constructs all available WorkItemAdapters for the given
// configuration and workspace. The manual adapter is always included. The linear
// adapter is included when an API key is present in configuration.
// The second return value contains human-readable warnings for adapters that
// were detected but could not be initialised (e.g. missing organisation).
func BuildWorkItemAdapters(
	cfg *config.Config,
	workspaceID string,
	workItemSvc *service.SessionService,
) ([]adapter.WorkItemAdapter, []string) {
	store := manualadapter.NewWorkspaceStore(workItemSvc, workspaceID)
	adapters := []adapter.WorkItemAdapter{
		manualadapter.New(store, workspaceID),
	}
	var warnings []string

	if cfg.Adapters.Linear.APIKey != "" {
		adapters = append(adapters, linearadapter.New(cfg.Adapters.Linear))
	}
	if config.GitLabAuthConfigured(cfg.Adapters.GitLab) {
		gitlabAdapter, err := gitlabadapter.New(context.Background(), cfg.Adapters.GitLab)
		if err != nil {
			slog.Warn("skipping gitlab work item adapter", "err", err)
		} else {
			adapters = append(adapters, gitlabAdapter)
		}
	}
	if config.GitHubAuthConfigured(cfg.Adapters.GitHub) {
		githubAdapter, err := githubadapter.New(context.Background(), cfg.Adapters.GitHub)
		if err != nil {
			slog.Warn("skipping github work item adapter", "err", err)
		} else {
			adapters = append(adapters, githubAdapter)
		}
	}
	if config.SentryAuthConfigured(cfg.Adapters.Sentry) {
		sentryAdapter, err := sentryadapter.New(context.Background(), cfg.Adapters.Sentry)
		if err != nil {
			slog.Warn("skipping sentry work item adapter", "err", err)
			warnings = append(warnings, "Sentry: "+err.Error())
		} else {
			adapters = append(adapters, sentryAdapter)
		}
	}

	return adapters, warnings
}

// BuildRepoSources constructs the ordered list of repo sources for the Add Repo
// overlay. GitHub and GitLab sources are added when their auth is configured;
// init failures are logged and skipped so a single broken provider does not
// block the overlay from showing the others. Manual URL entry is handled
// separately via the overlay's Ctrl+N input and needs no source entry here.
func BuildRepoSources(ctx context.Context, cfg *config.Config) []adapter.RepoSource {
	var sources []adapter.RepoSource
	if config.GitHubAuthConfigured(cfg.Adapters.GitHub) {
		src, err := githubadapter.NewRepoSource(ctx, cfg.Adapters.GitHub)
		if err != nil {
			slog.Warn("skipping github repo source", "err", err)
		} else {
			sources = append(sources, src)
		}
	}
	if config.GitLabAuthConfigured(cfg.Adapters.GitLab) {
		src, err := gitlabadapter.NewRepoSource(ctx, cfg.Adapters.GitLab)
		if err != nil {
			slog.Warn("skipping gitlab repo source", "err", err)
		} else {
			sources = append(sources, src)
		}
	}
	return sources
}

// BuildReviewCommentFetcher constructs a dispatcher capable of fetching unresolved
// review comments from every configured provider. The dispatcher is safe to use
// even when a provider is not configured — it returns a descriptive error for
// unknown providers. The second return value contains human-readable warnings
// for providers that were detected but could not be initialised; callers MUST
// surface these via Services.StartupWarnings (matches BuildWorkItemAdapters).
func BuildReviewCommentFetcher(ctx context.Context, cfg *config.Config, workspaceDir string) (*adapter.ReviewCommentDispatcher, []string) {
	fetchers := make(map[string]adapter.ReviewCommentFetcher)
	var warnings []string
	if config.GitHubAuthConfigured(cfg.Adapters.GitHub) {
		ghAdapter, err := githubadapter.New(ctx, cfg.Adapters.GitHub)
		if err != nil {
			slog.Warn("skipping github review comment fetcher", "err", err)
			warnings = append(warnings, "GitHub review comments: "+err.Error())
		} else {
			fetchers[ghAdapter.Provider()] = ghAdapter
		}
	}
	// GitLab adapter does not need network auth up front — it shells out to glab,
	// which manages its own auth. Construct via NewWithEventRepo so workspaceDir
	// (when present) is wired up; both constructors fall through to the same
	// runner with an empty workspaceDir as default.
	glAdapter := gladapter.NewWithEventRepo(cfg.Adapters.Glab, adapter.ReviewArtifactRepos{}, workspaceDir)
	fetchers[glAdapter.Provider()] = glAdapter
	return adapter.NewReviewCommentDispatcher(fetchers), warnings
}

// BuildRepoLifecycleAdapters constructs repo lifecycle adapters for the current workspace.
func BuildRepoLifecycleAdapters(
	ctx context.Context,
	cfg *config.Config,
	workspaceDir string,
	repos adapter.ReviewArtifactRepos,
) []adapter.RepoLifecycleAdapter {
	if workspaceDir == "" {
		return nil
	}

	platforms, err := detectWorkspaceLifecyclePlatforms(ctx, cfg, workspaceDir)
	if err != nil {
		slog.Warn("failed to detect repo lifecycle platforms; no repo lifecycle adapters registered", "workspace_dir", workspaceDir, "err", err)
		return nil
	}

	adapters := make([]adapter.RepoLifecycleAdapter, 0, len(platforms))
	for _, platform := range platforms {
		switch platform {
		case remotedetect.PlatformGitLab:
			adapters = append(adapters, routedRepoLifecycleAdapter{provider: platform, adapter: gladapter.NewWithEventRepo(cfg.Adapters.Glab, repos, workspaceDir)})
		case remotedetect.PlatformGitHub:
			if !config.GitHubAuthConfigured(cfg.Adapters.GitHub) {
				slog.Warn("skipping github lifecycle adapter: no github auth configured")
				continue
			}
			githubAdapter, err := githubadapter.NewRepoLifecycle(ctx, cfg.Adapters.GitHub, repos)
			if err != nil {
				slog.Warn("skipping github lifecycle adapter", "err", err)
				continue
			}
			adapters = append(adapters, routedRepoLifecycleAdapter{provider: platform, adapter: githubAdapter})
		default:
			slog.Warn("skipping repo lifecycle adapters: remote platform is unknown", "workspace_dir", workspaceDir)
		}
	}
	return adapters
}

type routedRepoLifecycleAdapter struct {
	provider remotedetect.Platform
	adapter  adapter.RepoLifecycleAdapter
}

func (a routedRepoLifecycleAdapter) Name() string { return a.adapter.Name() }

func (a routedRepoLifecycleAdapter) OnEvent(ctx context.Context, evt domain.SystemEvent) error {
	provider, ok := repoLifecycleEventPlatform(evt)
	if !ok {
		slog.Debug("repo lifecycle adapter: dropping event, platform detection failed",
			"adapter_provider", a.provider,
			"event_type", evt.EventType,
			"workspace_id", evt.WorkspaceID,
		)
		return nil
	}
	if provider != a.provider {
		slog.Debug("repo lifecycle adapter: dropping event, platform mismatch",
			"adapter_provider", a.provider,
			"detected_provider", provider,
			"event_type", evt.EventType,
			"workspace_id", evt.WorkspaceID,
		)
		return nil
	}
	return a.adapter.OnEvent(ctx, evt)
}

func (a routedRepoLifecycleAdapter) StartPRRefresh(ctx context.Context, workspaceID string) {
	type refresher interface {
		StartPRRefresh(ctx context.Context, workspaceID string)
	}
	if r, ok := a.adapter.(refresher); ok {
		r.StartPRRefresh(ctx, workspaceID)
	}
}

func (a routedRepoLifecycleAdapter) StartMRRefresh(ctx context.Context, workspaceID string) {
	type refresher interface {
		StartMRRefresh(ctx context.Context, workspaceID string)
	}
	if r, ok := a.adapter.(refresher); ok {
		r.StartMRRefresh(ctx, workspaceID)
	}
}

func repoLifecycleEventPlatform(evt domain.SystemEvent) (remotedetect.Platform, bool) {
	var payload struct {
		Review      domain.ReviewRef `json:"review"`
		ExternalID  string           `json:"external_id"`
		ExternalIDs []string         `json:"external_ids"`
	}
	if err := json.Unmarshal([]byte(evt.Payload), &payload); err != nil {
		return remotedetect.PlatformUnknown, false
	}
	if provider, ok := repoLifecycleEventPlatformFromReview(payload.Review); ok {
		return provider, true
	}
	return repoLifecycleEventPlatformFromExternalIDs(payload.ExternalID, payload.ExternalIDs)
}

func repoLifecycleEventPlatformFromReview(review domain.ReviewRef) (remotedetect.Platform, bool) {
	base, baseOK := repoLifecycleEventPlatformFromProvider(review.BaseRepo.Provider)
	head, headOK := repoLifecycleEventPlatformFromProvider(review.HeadRepo.Provider)
	switch {
	case baseOK && headOK && base != head:
		return remotedetect.PlatformUnknown, false
	case baseOK:
		return base, true
	case headOK:
		return head, true
	default:
		return remotedetect.PlatformUnknown, false
	}
}

func repoLifecycleEventPlatformFromProvider(provider string) (remotedetect.Platform, bool) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case remotedetect.PlatformGitHub.String():
		return remotedetect.PlatformGitHub, true
	case remotedetect.PlatformGitLab.String():
		return remotedetect.PlatformGitLab, true
	default:
		return remotedetect.PlatformUnknown, false
	}
}

func repoLifecycleEventPlatformFromExternalIDs(externalID string, externalIDs []string) (remotedetect.Platform, bool) {
	provider, ok := repoLifecycleEventPlatformFromExternalID(externalID)
	if !ok {
		provider = remotedetect.PlatformUnknown
	}
	for _, candidate := range externalIDs {
		next, nextOK := repoLifecycleEventPlatformFromExternalID(candidate)
		if !nextOK {
			continue
		}
		if provider == remotedetect.PlatformUnknown {
			provider = next
			ok = true
			continue
		}
		if provider != next {
			return remotedetect.PlatformUnknown, false
		}
	}
	if !ok || provider == remotedetect.PlatformUnknown {
		return remotedetect.PlatformUnknown, false
	}
	return provider, true
}

func repoLifecycleEventPlatformFromExternalID(externalID string) (remotedetect.Platform, bool) {
	trimmed := strings.TrimSpace(externalID)
	switch {
	case strings.HasPrefix(trimmed, "gh:"):
		return remotedetect.PlatformGitHub, true
	case strings.HasPrefix(trimmed, "gl:"):
		return remotedetect.PlatformGitLab, true
	default:
		return remotedetect.PlatformUnknown, false
	}
}

func detectWorkspaceLifecyclePlatforms(ctx context.Context, cfg *config.Config, workspaceDir string) ([]remotedetect.Platform, error) {
	repoPaths, err := gitwork.DiscoverRepos(workspaceDir)
	if err != nil {
		return nil, fmt.Errorf("discover workspace repos: %w", err)
	}
	if len(repoPaths) == 0 {
		return nil, fmt.Errorf("no git-work repos found in workspace %s", workspaceDir)
	}

	platforms := make([]remotedetect.Platform, 0, 2)
	seen := make(map[remotedetect.Platform]struct{}, 2)
	var firstErr error
	for _, repoPath := range repoPaths {
		platform, err := remotedetect.DetectPlatform(ctx, repoPath, cfg)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("detect platform for %s: %w", repoPath, err)
			}
			continue
		}
		if platform == remotedetect.PlatformUnknown {
			continue
		}
		if _, ok := seen[platform]; ok {
			continue
		}
		seen[platform] = struct{}{}
		platforms = append(platforms, platform)
	}

	if len(platforms) > 0 {
		return platforms, nil
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return nil, fmt.Errorf("no supported repo lifecycle platform detected in workspace %s", workspaceDir)
}
