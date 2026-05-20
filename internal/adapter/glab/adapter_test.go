package glab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	coreadapter "github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
)

// stubRunner returns a commandRunner that records calls and returns the configured
// output/error. If outputs is set (multiple), they are returned in order and
// rotated. Otherwise, output (single) is returned on every call.
type stubRunner struct {
	calls    []stubCall
	output   []byte
	outputs  [][]byte
	outputMu sync.Mutex
	err      error
}

type stubCall struct {
	dir  string
	name string
	args []string
}

func (s *stubRunner) run(_ context.Context, dir, name string, args ...string) ([]byte, error) {
	s.calls = append(s.calls, stubCall{dir: dir, name: name, args: args})
	s.outputMu.Lock()
	defer s.outputMu.Unlock()
	if len(s.outputs) > 0 {
		out := s.outputs[0]
		s.outputs = s.outputs[1:]
		return out, s.err
	}
	return s.output, s.err
}

// emptyReviewArtifactRepos is used by tests that verify CLI output only.
// These tests intentionally omit WorkItemID and WorkspaceID from payloads
// to exercise the early-return guard in PersistGitlabMR/PersistReviewArtifact.
// This prevents nil panics when repos are not initialized.
//
// Tests that verify persistence behavior should use proper in-memory repos
// (e.g., inMemGitlabMRRepo, inMemArtifactLinkRepo) and include
// WorkItemID/WorkspaceID in the payload.
var emptyReviewArtifactRepos = coreadapter.ReviewArtifactRepos{}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}

	return string(b)
}

// --- titleFromBranch ---

func TestTitleFromBranch(t *testing.T) {
	cases := []struct {
		branch string
		want   string
	}{
		{"sub-LIN-FOO-123-fix-auth-flow", "Fix auth flow [LIN-FOO-123]"},
		{"sub-LIN-BAR-456-add-retry-logic", "Add retry logic [LIN-BAR-456]"},
		{"sub-MAN-42-add-new-feature", "Add new feature [MAN-42]"},
		{"sub-MAN-1-initial-commit", "Initial commit [MAN-1]"},
		// No remainder — just external ID
		{"sub-LIN-TEAM-7", "LIN-TEAM-7"},
		{"sub-MAN-3", "MAN-3"},
		// No sub- prefix — verbatim capitalize
		{"feature-branch", "Feature branch"},
		{"", ""},
		// Unrecognised sub- prefix
		{"sub-other-stuff", "Other stuff"},
	}
	for _, c := range cases {
		t.Run(c.branch, func(t *testing.T) {
			got := titleFromBranch(c.branch)
			if got != c.want {
				t.Errorf("titleFromBranch(%q) = %q, want %q", c.branch, got, c.want)
			}
		})
	}
}

// --- mrTitle ---

func TestMRTitle_PrefersWorkItemTitle(t *testing.T) {
	got := mrTitle("Fix login bug", "sub-LIN-FOO-1-irrelevant")
	if got != "Fix login bug" {
		t.Errorf("got %q, want %q", got, "Fix login bug")
	}
}

func TestMRTitle_FallsBackToBranch(t *testing.T) {
	got := mrTitle("", "sub-MAN-5-fix-flaky-tests")
	want := "Fix flaky tests [MAN-5]"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMRTitle_ClampsLongWorkItemTitle(t *testing.T) {
	longTitle := strings.Repeat("x", 300)
	got := mrTitle(longTitle, "sub-SEN-acme-123-fmt-wraperror-post-api-v6-kpi")
	want := strings.Repeat("x", 254) + gitlabMRTitleEllipsisSuffix
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if len([]rune(got)) != gitlabMRTitleMaxRunes {
		t.Fatalf("title has %d runes, want %d", len([]rune(got)), gitlabMRTitleMaxRunes)
	}
}

func TestMRTitle_ClampsLongWorkItemTitleAtRuneBoundary(t *testing.T) {
	longTitle := strings.Repeat("å", 300)
	got := mrTitle(longTitle, "sub-SEN-acme-123-fmt-wraperror-post-api-v6-kpi")
	want := strings.Repeat("å", 254) + gitlabMRTitleEllipsisSuffix
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if len([]rune(got)) != gitlabMRTitleMaxRunes {
		t.Fatalf("title has %d runes, want %d", len([]rune(got)), gitlabMRTitleMaxRunes)
	}
}

func TestMRTitle_ClampsLongBranchFallback(t *testing.T) {
	got := mrTitle("", "sub-"+strings.Repeat("very-long-", 40))
	if len([]rune(got)) != gitlabMRTitleMaxRunes {
		t.Fatalf("title has %d runes, want %d", len([]rune(got)), gitlabMRTitleMaxRunes)
	}
	if !strings.HasSuffix(got, gitlabMRTitleEllipsisSuffix) {
		t.Fatalf("title = %q, want ellipsis suffix", got)
	}
}

// --- parseMRURL ---

func TestParseMRURL_FoundInOutput(t *testing.T) {
	output := []byte("Creating MR\nhttps://gitlab.com/org/repo/-/merge_requests/42\nDone.")
	got := parseMRURL(output)
	want := "https://gitlab.com/org/repo/-/merge_requests/42"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseMRURL_NotFound(t *testing.T) {
	got := parseMRURL([]byte("Creating MR\nError: already exists"))
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

// --- OnEvent unknown ---

func TestOnEvent_UnknownEvent_ReturnsNil(t *testing.T) {
	stub := &stubRunner{}
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, "", stub.run)
	err := a.OnEvent(context.Background(), domain.SystemEvent{
		EventType: "workspace.created",
		Payload:   "{}",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stub.calls) != 0 {
		t.Fatalf("expected no glab calls, got %d", len(stub.calls))
	}
}

// --- OnEvent WorktreeCreated ---

func TestOnEvent_WorktreeCreated_CreatesMR(t *testing.T) {
	stub := &stubRunner{
		output: []byte("https://gitlab.com/org/repo/-/merge_requests/1\n"),
	}
	a := newWithRunner(config.GlabConfig{}, emptyReviewArtifactRepos, "", stub.run)

	// WorkItemID intentionally omitted: this test verifies CLI output only,
	// not persistence. The early-return guard in PersistGitlabMR prevents
	// nil panics when WorkItemID is empty.
	payload := mustJSON(worktreePayload{
		WorkspaceID:   "ws-1",
		Repository:    "myrepo",
		Branch:        "sub-LIN-FOO-1-fix-bug",
		WorktreePath:  "/tmp/wt",
		WorkItemTitle: "Fix the bug",
		SubPlan:       "Implement repo-specific steps",
	})

	err := a.OnEvent(context.Background(), domain.SystemEvent{
		EventType: string(domain.EventWorktreeCreated),
		Payload:   payload,
	})
	if err != nil {
		t.Fatalf("OnEvent returned error: %v", err)
	}
	if len(stub.calls) != 3 {
		t.Fatalf("expected 3 glab calls (mr view + mr create + mr view), got %d", len(stub.calls))
	}
	// call[0] is mr view (idempotency check); call[1] is mr create
	call := stub.calls[1]
	if call.name != "glab" {
		t.Errorf("command = %q, want %q", call.name, "glab")
	}
	if call.dir != "/tmp/wt" {
		t.Errorf("dir = %q, want %q", call.dir, "/tmp/wt")
	}
	// Verify flags on mr create call
	joined := strings.Join(call.args, " ")
	for _, want := range []string{"mr create", "--draft", "--source-branch sub-LIN-FOO-1-fix-bug", "--title Fix the bug", "--description Implement repo-specific steps"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args %q missing %q", joined, want)
		}
	}
}

func TestOnEvent_WorktreeCreated_DraftTitleFromSlug(t *testing.T) {
	stub := &stubRunner{output: []byte("https://gitlab.com/org/repo/-/merge_requests/2\n")}
	a := newWithRunner(config.GlabConfig{}, emptyReviewArtifactRepos, "", stub.run)

	// WorkItemID and WorkspaceID intentionally omitted: this test verifies
	// CLI output only, not persistence. Early-return guard prevents nil panics.
	payload := mustJSON(worktreePayload{
		Branch:       "sub-MAN-7-refactor-auth",
		WorktreePath: "/tmp/wt2",
		// WorkItemTitle intentionally empty
	})
	_ = a.OnEvent(context.Background(), domain.SystemEvent{
		EventType: string(domain.EventWorktreeCreated),
		Payload:   payload,
	})

	joined := strings.Join(stub.calls[1].args, " ")
	if !strings.Contains(joined, "--title Refactor auth [MAN-7]") {
		t.Errorf("args %q missing derived title", joined)
	}
}

func TestOnEvent_WorktreeCreated_ReviewersAndLabels(t *testing.T) {
	stub := &stubRunner{output: []byte("https://gitlab.com/org/repo/-/merge_requests/3\n")}
	a := newWithRunner(config.GlabConfig{
		Reviewers: []string{"alice", "bob"},
		Labels:    []string{"backend"},
	}, emptyReviewArtifactRepos, "", stub.run)

	// WorkItemID and WorkspaceID intentionally omitted: this test verifies
	// CLI output only, not persistence. Early-return guard prevents nil panics.
	payload := mustJSON(worktreePayload{
		Branch:       "sub-LIN-X-1-thing",
		WorktreePath: "/tmp/wt3",
	})
	_ = a.OnEvent(context.Background(), domain.SystemEvent{
		EventType: string(domain.EventWorktreeCreated),
		Payload:   payload,
	})

	joined := strings.Join(stub.calls[1].args, " ")
	for _, want := range []string{"--reviewer alice", "--reviewer bob", "--label backend"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args %q missing %q", joined, want)
		}
	}
}

func TestOnEvent_WorktreeCreated_GlabFailure_ReturnsNil(t *testing.T) {
	stub := &stubRunner{err: errors.New("glab: authentication required")}
	a := newWithRunner(config.GlabConfig{}, emptyReviewArtifactRepos, "", stub.run)

	// WorkItemID and WorkspaceID intentionally omitted: this test verifies
	// CLI error handling only, not persistence. Early-return guard prevents nil panics.
	payload := mustJSON(worktreePayload{
		Branch:       "sub-LIN-FOO-2-fail",
		WorktreePath: "/tmp/wt-fail",
	})
	err := a.OnEvent(context.Background(), domain.SystemEvent{
		EventType: string(domain.EventWorktreeCreated),
		Payload:   payload,
	})
	// Must not propagate error
	if err != nil {
		t.Fatalf("OnEvent must return nil on glab failure, got: %v", err)
	}
	// Tracking must still be populated so un-draft can be retried
	a.mu.RLock()
	entries := a.tracked["sub-LIN-FOO-2-fail"]
	a.mu.RUnlock()
	if len(entries) == 0 {
		t.Error("expected tracking entry even after glab failure")
	}
}

func TestOnEvent_WorktreeCreated_MalformedPayload_ReturnsNil(t *testing.T) {
	stub := &stubRunner{}
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, "", stub.run)

	err := a.OnEvent(context.Background(), domain.SystemEvent{
		EventType: string(domain.EventWorktreeCreated),
		Payload:   "not json",
	})
	if err != nil {
		t.Fatalf("malformed payload must not propagate error, got: %v", err)
	}
	if len(stub.calls) != 0 {
		t.Error("expected no glab calls on malformed payload")
	}
}

// --- OnEvent SubPlanPRReady ---

func TestOnEvent_SubPlanPRReady_UnDraftsMRs(t *testing.T) {
	// Two mr view outputs: first finds the MR, second refreshes state after undrafting.
	stub := &stubRunner{
		outputs: [][]byte{
			[]byte(`{"iid":5,"state":"opened","web_url":"https://gitlab.com/group/project/-/merge_requests/5"}`),
			[]byte(`{"iid":5,"state":"opened","web_url":"https://gitlab.com/group/project/-/merge_requests/5"}`),
		},
	}
	mrRepo := &inMemGitlabMRRepo{}
	artifactRepo := &inMemArtifactLinkRepo{}
	eventRepo := &glabArtifactEventRepo{}
	repos := coreadapter.ReviewArtifactRepos{
		Events:           service.NewEventService(repository.NoopTransacter{Res: repository.Resources{Events: eventRepo}}),
		GitlabMRs:        service.NewGitlabMRService(repository.NoopTransacter{Res: repository.Resources{GitlabMRs: mrRepo}}),
		SessionArtifacts: service.NewSessionReviewArtifactService(repository.NoopTransacter{Res: repository.Resources{SessionReviewArtifacts: artifactRepo}}),
	}
	a := newWithRunner(config.GlabConfig{}, repos, "", stub.run)

	branch := "sub-LIN-FOO-99-complete-me"
	payload := mustJSON(subPlanPRReadyPayload{
		WorkItemID:   "wi-99",
		WorkspaceID:  "ws-1",
		Branch:       branch,
		WorktreePath: "/tmp/wt",
		Repository:   "group/project",
		Review: domain.ReviewRef{
			BaseRepo: domain.RepoRef{Provider: "gitlab", Owner: "group", Repo: "project"},
		},
	})
	err := a.OnEvent(context.Background(), domain.SystemEvent{
		EventType: string(domain.EventSubPlanPRReady),
		Payload:   payload,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expect mr view (to find MR) + mr update --ready + mr view (refresh state after undrafting).
	if len(stub.calls) != 3 {
		t.Fatalf("expected 3 glab calls (mr view + mr update + mr view refresh), got %d", len(stub.calls))
	}
	// First call: mr view to find the MR
	viewCall := stub.calls[0]
	viewJoined := strings.Join(viewCall.args, " ")
	if !strings.Contains(viewJoined, "mr view") {
		t.Errorf("call[0] = %v, want mr view", viewCall)
	}
	if viewCall.dir != "/tmp/wt" {
		t.Errorf("view dir = %q, want /tmp/wt", viewCall.dir)
	}
	// Second call: mr update --ready
	updateCall := stub.calls[1]
	updateJoined := strings.Join(updateCall.args, " ")
	if !strings.Contains(updateJoined, "mr update") {
		t.Errorf("call[1] = %v, want mr update", updateCall)
	}
	if !strings.Contains(updateJoined, "--ready") {
		t.Errorf("call[1] missing --ready: %q", updateJoined)
	}
	if !strings.Contains(updateJoined, branch) {
		t.Errorf("call[1] missing branch %q: %q", branch, updateJoined)
	}
	// Third call: mr view to refresh state after undrafting
	refreshCall := stub.calls[2]
	refreshJoined := strings.Join(refreshCall.args, " ")
	if !strings.Contains(refreshJoined, "mr view") {
		t.Errorf("call[2] = %v, want mr view", refreshCall)
	}
}

func TestOnEvent_WorkItemCompleted_NoBranch_ReturnsNil(t *testing.T) {
	stub := &stubRunner{}
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, "", stub.run)

	payload := mustJSON(map[string]string{"external_id": "LIN-FOO-1"}) // no branch
	err := a.OnEvent(context.Background(), domain.SystemEvent{
		EventType: string(domain.EventWorkItemCompleted),
		Payload:   payload,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stub.calls) != 0 {
		t.Errorf("expected no glab calls, got %d", len(stub.calls))
	}
}

func TestOnEvent_WorkItemCompleted_NoTrackedRepos_ReturnsNil(t *testing.T) {
	stub := &stubRunner{}
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, "", stub.run)

	payload := mustJSON(completedPayload{Branch: "sub-LIN-X-1-unknown"})
	err := a.OnEvent(context.Background(), domain.SystemEvent{
		EventType: string(domain.EventWorkItemCompleted),
		Payload:   payload,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stub.calls) != 0 {
		t.Errorf("expected no glab calls for untracked branch, got %d", len(stub.calls))
	}
}

func TestOnEvent_WorkItemCompleted_GitHubProvider_IsIgnored(t *testing.T) {
	// The glab adapter must not invoke the glab CLI when the
	// EventWorkItemCompleted payload names a GitHub-hosted repo.
	stub := &stubRunner{}
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, "", stub.run)

	branch := "sub-gh-99-feature"
	a.mu.Lock()
	a.tracked[branch] = []branchEntry{
		{repo: "org/repo", worktreePath: "/wt/repo"},
	}
	a.mu.Unlock()

	payload := mustJSON(map[string]any{
		"branch":       branch,
		"external_id":  "gh:issue:org/repo#99",
		"work_item_id": "wi-999",
		"workspace_id": "ws-local",
		"review": map[string]any{
			"base_repo": map[string]any{
				"provider": "github",
				"owner":    "org",
				"repo":     "repo",
			},
		},
	})
	err := a.OnEvent(context.Background(), domain.SystemEvent{
		EventType: string(domain.EventWorkItemCompleted),
		Payload:   payload,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stub.calls) != 0 {
		t.Errorf("expected no glab CLI calls for GitHub-hosted work item, got %d", len(stub.calls))
	}
}

func TestOnEvent_WorkItemCompleted_GlabFailure_ReturnsNil(t *testing.T) {
	stub := &stubRunner{err: errors.New("glab: MR not found")}
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, "", stub.run)

	branch := "sub-LIN-FOO-5-fail"
	a.mu.Lock()
	a.tracked[branch] = []branchEntry{{repo: "r", worktreePath: "/wt/r"}}
	a.mu.Unlock()

	payload := mustJSON(completedPayload{Branch: branch})
	err := a.OnEvent(context.Background(), domain.SystemEvent{
		EventType: string(domain.EventWorkItemCompleted),
		Payload:   payload,
	})
	if err != nil {
		t.Fatalf("glab failure must not propagate, got: %v", err)
	}
}

func TestOnEvent_SubPlanPRReady_GitHubProvider_IsIgnored(t *testing.T) {
	// The glab adapter must not invoke the glab CLI when the
	// EventSubPlanPRReady payload names a GitHub-hosted repo.
	stub := &stubRunner{}
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, "", stub.run)

	branch := "sub-gh-99-feature"
	payload := mustJSON(subPlanPRReadyPayload{
		WorkItemID:   "wi-999",
		WorkspaceID:  "ws-local",
		Branch:       branch,
		WorktreePath: "/wt/repo",
		Repository:   "group/project",
		Review: domain.ReviewRef{
			BaseRepo: domain.RepoRef{Provider: "github", Owner: "org", Repo: "repo"},
		},
	})
	err := a.OnEvent(context.Background(), domain.SystemEvent{
		EventType: string(domain.EventSubPlanPRReady),
		Payload:   payload,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stub.calls) != 0 {
		t.Errorf("expected no glab CLI calls for GitHub-hosted sub-plan, got %d", len(stub.calls))
	}
}

func TestOnEvent_SubPlanPRReady_CreatesMRWhenNoneExists(t *testing.T) {
	// When no MR exists for the branch, createMRNonDraft should be called.
	var calls []stubCall
	createdMR := false
	mrJSON := `{"iid":10,"state":"opened","web_url":"https://gitlab.com/group/project/-/merge_requests/10"}`
	callCount := 0
	runner := func(_ context.Context, dir, name string, args ...string) ([]byte, error) {
		calls = append(calls, stubCall{dir: dir, name: name, args: args})
		callCount++
		joined := strings.Join(args, " ")
		// First call is mr view — return error (no MR exists).
		if callCount == 1 && strings.Contains(joined, "mr view") {
			return nil, errors.New("MR not found")
		}
		// Second call is mr create (non-draft).
		if strings.Contains(joined, "mr create") {
			createdMR = true
			return []byte("https://gitlab.com/group/project/-/merge_requests/10\n"), nil
		}
		// Third call is mr view for recording.
		if strings.Contains(joined, "mr view") {
			return []byte(mrJSON), nil
		}
		t.Errorf("unexpected call %d: %q %v", callCount, name, args)
		return nil, errors.New("unexpected")
	}

	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{
		Events:           service.NewEventService(repository.NoopTransacter{Res: repository.Resources{Events: &glabArtifactEventRepo{}}}),
		GitlabMRs:        service.NewGitlabMRService(repository.NoopTransacter{Res: repository.Resources{GitlabMRs: &inMemGitlabMRRepo{}}}),
		SessionArtifacts: service.NewSessionReviewArtifactService(repository.NoopTransacter{Res: repository.Resources{SessionReviewArtifacts: &inMemArtifactLinkRepo{}}}),
	}, "", runner)

	branch := "sub-LIN-FOO-7-create-missing"
	payload := mustJSON(subPlanPRReadyPayload{
		WorkItemID:     "wi-7",
		WorkspaceID:    "ws-1",
		Branch:         branch,
		WorktreePath:   "/tmp/wt",
		Repository:     "group/project",
		WorkItemTitle:  "Create missing MR",
		SubPlanContent: "Implementation details",
		Review: domain.ReviewRef{
			BaseRepo: domain.RepoRef{Provider: "gitlab", Owner: "group", Repo: "project"},
		},
	})
	err := a.OnEvent(context.Background(), domain.SystemEvent{
		EventType: string(domain.EventSubPlanPRReady),
		Payload:   payload,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !createdMR {
		t.Fatal("expected mr create to be called when no MR exists")
	}
	// Verify the expected call sequence: mr create (non-draft) + mr view (recording).
	var foundCreate bool
	for _, call := range calls {
		joined := strings.Join(call.args, " ")
		if strings.Contains(joined, "mr create") {
			foundCreate = true
			if !strings.Contains(joined, "--title Create missing MR") {
				t.Errorf("mr create missing expected title: %q", joined)
			}
			// Non-draft MR create should NOT have --draft flag
			if strings.Contains(joined, "--draft") {
				t.Errorf("mr create should NOT have --draft for subplan PR-ready: %q", joined)
			}
		}
	}
	if !foundCreate {
		t.Fatal("expected mr create to be called when no MR exists")
	}
}

func TestOnEvent_SubPlanPRReady_UsesTrackedArtifactBeforeCreate(t *testing.T) {
	callCount := 0
	runner := func(_ context.Context, _ string, _ string, args ...string) ([]byte, error) {
		callCount++
		joined := strings.Join(args, " ")
		switch {
		case callCount == 1 && strings.Contains(joined, "mr update"):
			return []byte("updated"), nil
		case callCount == 2 && strings.Contains(joined, "mr view"):
			return []byte(`{"iid":979,"state":"opened","web_url":"https://gitlab.justtrack.io/justtrack/backend/management/-/merge_requests/979"}`), nil
		case strings.Contains(joined, "mr create"):
			t.Fatalf("must not create MR when tracked artifact exists: %q", joined)
		}
		t.Fatalf("unexpected glab call %d: %q", callCount, joined)
		return nil, errors.New("unexpected")
	}

	mrRepo := &inMemGitlabMRRepo{data: map[string]domain.GitlabMergeRequest{
		"mr-979": {
			ID:           "mr-979",
			ProjectPath:  "justtrack/backend/management",
			IID:          979,
			State:        "opened",
			Draft:        true,
			SourceBranch: "sub-gl-issue-justtrack-general-tickets-14-security-deactivated-manager-c",
			WebURL:       "https://gitlab.justtrack.io/justtrack/backend/management/-/merge_requests/979",
		},
	}}
	artifactRepo := &inMemArtifactLinkRepo{links: []domain.SessionReviewArtifact{{
		ID:                 "link-1",
		WorkspaceID:        "ws-1",
		WorkItemID:         "wi-409",
		Provider:           "gitlab",
		ProviderArtifactID: "mr-979",
	}}}
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{
		Events:           service.NewEventService(repository.NoopTransacter{Res: repository.Resources{Events: &glabArtifactEventRepo{}}}),
		GitlabMRs:        service.NewGitlabMRService(repository.NoopTransacter{Res: repository.Resources{GitlabMRs: mrRepo}}),
		SessionArtifacts: service.NewSessionReviewArtifactService(repository.NoopTransacter{Res: repository.Resources{SessionReviewArtifacts: artifactRepo}}),
	}, "", runner)

	payload := mustJSON(subPlanPRReadyPayload{
		WorkItemID:     "wi-409",
		WorkspaceID:    "ws-1",
		Branch:         "sub-gl-issue-justtrack-general-tickets-14-security-deactivated-manager-c",
		WorktreePath:   "/tmp/wt",
		Repository:     "justtrack/backend/management",
		WorkItemTitle:  "Fix deactivation bug",
		SubPlanContent: "Implementation details",
		Review: domain.ReviewRef{
			BaseRepo: domain.RepoRef{Provider: "gitlab", Owner: "justtrack", Repo: "backend/management"},
		},
	})
	if err := a.OnEvent(context.Background(), domain.SystemEvent{EventType: string(domain.EventSubPlanPRReady), Payload: payload}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("expected 2 glab calls (update + refresh), got %d", callCount)
	}
}

// --- mrExists (JSON parsing from `glab mr view`) ---

func TestMRExists_MRPresent_ReturnsTrue(t *testing.T) {
	mrJSON := `{"iid":3,"state":"opened","web_url":"https://gitlab.com/org/repo/-/merge_requests/3"}`
	stub := &stubRunner{output: []byte(mrJSON)}
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, "", stub.run)

	if !a.mrExists(context.Background(), "/wt", "sub-LIN-FOO-1-test") {
		t.Error("expected mrExists to return true for valid JSON response")
	}
	// Verify it called mr view --source-branch --output json
	call := stub.calls[0]
	joined := strings.Join(call.args, " ")
	for _, want := range []string{"mr view", "--source-branch sub-LIN-FOO-1-test", "--output json"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args %q missing %q", joined, want)
		}
	}
}

func TestMRExists_GlabError_ReturnsFalse(t *testing.T) {
	stub := &stubRunner{err: errors.New("FAILED: 404 Not Found")}
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, "", stub.run)

	if a.mrExists(context.Background(), "/wt", "no-such-branch") {
		t.Error("expected mrExists to return false on glab error")
	}
}

func TestMRExists_InvalidJSON_ReturnsFalse(t *testing.T) {
	stub := &stubRunner{output: []byte("not json")}
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, "", stub.run)

	if a.mrExists(context.Background(), "/wt", "branch") {
		t.Error("expected mrExists to return false on invalid JSON")
	}
}

func TestMRExists_ZeroIID_ReturnsFalse(t *testing.T) {
	// Some glab versions return empty JSON on not-found instead of non-zero exit
	stub := &stubRunner{output: []byte(`{"iid":0,"state":"","web_url":""}`)}
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, "", stub.run)

	if a.mrExists(context.Background(), "/wt", "branch") {
		t.Error("expected mrExists to return false when iid == 0")
	}
}

// --- Idempotency: WorktreeCreated when MR already exists ---

func TestOnEvent_WorktreeCreated_SkipsCreateWhenMRExists(t *testing.T) {
	// First call (mr view) returns valid MR JSON; second call (mr create) must NOT happen.
	callCount := 0
	runner := func(_ context.Context, _ /* dir */, name string, args ...string) ([]byte, error) {
		callCount++
		// Only the mr view call should fire.
		if callCount == 1 {
			return []byte(`{"iid":7,"state":"opened","web_url":"https://gitlab.com/org/repo/-/merge_requests/7"}`), nil
		}
		t.Errorf("unexpected call %d: %q %v", callCount, name, args)

		return nil, errors.New("unexpected")
	}
	a := newWithRunner(config.GlabConfig{}, emptyReviewArtifactRepos, "", runner)

	// WorkItemID and WorkspaceID intentionally omitted: this test verifies
	// CLI behavior only (idempotency check), not persistence.
	payload := mustJSON(worktreePayload{
		Branch:       "sub-LIN-FOO-7-existing-mr",
		WorktreePath: "/wt/exist",
	})
	err := a.OnEvent(context.Background(), domain.SystemEvent{
		EventType: string(domain.EventWorktreeCreated),
		Payload:   payload,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected 1 glab call (mr view only), got %d", callCount)
	}
}

func TestOnEvent_WorktreeCreated_AddsGitLabResolvesFooter(t *testing.T) {
	stub := &stubRunner{output: []byte("https://gitlab.com/org/repo/-/merge_requests/4\n")}
	a := newWithRunner(config.GlabConfig{}, emptyReviewArtifactRepos, "", stub.run)
	// WorkItemID and WorkspaceID intentionally omitted: this test verifies
	// CLI description formatting only, not persistence.
	payload := mustJSON(worktreePayload{Branch: "sub-GL-1234-40-fix-bug", WorktreePath: "/tmp/wt", SubPlan: "Repo specific implementation plan", TrackerRefs: []domain.TrackerReference{{Provider: "gitlab", Kind: "issue", ID: "40", Number: 40}}})
	if err := a.OnEvent(context.Background(), domain.SystemEvent{EventType: string(domain.EventWorktreeCreated), Payload: payload}); err != nil {
		t.Fatalf("OnEvent returned error: %v", err)
	}
	joined := strings.Join(stub.calls[1].args, " ")
	if !strings.Contains(joined, "--description Repo specific implementation plan\n\nResolves #40") {
		t.Fatalf("args %q missing gitlab resolves footer", joined)
	}
}

func TestOnEvent_WorktreeCreated_AddsLinearResolvesFooter(t *testing.T) {
	stub := &stubRunner{output: []byte("https://gitlab.com/org/repo/-/merge_requests/5\n")}
	a := newWithRunner(config.GlabConfig{}, emptyReviewArtifactRepos, "", stub.run)
	// WorkItemID and WorkspaceID intentionally omitted: this test verifies
	// CLI description formatting only, not persistence.
	payload := mustJSON(worktreePayload{Branch: "sub-LIN-FOO-123-fix-bug", WorktreePath: "/tmp/wt", SubPlan: "Repo specific implementation plan", TrackerRefs: []domain.TrackerReference{{Provider: "linear", Kind: "issue", ID: "FOO-123", URL: "https://linear.app/acme/issue/FOO-123"}}})
	if err := a.OnEvent(context.Background(), domain.SystemEvent{EventType: string(domain.EventWorktreeCreated), Payload: payload}); err != nil {
		t.Fatalf("OnEvent returned error: %v", err)
	}
	joined := strings.Join(stub.calls[1].args, " ")
	if !strings.Contains(joined, "Resolves [FOO-123](https://linear.app/acme/issue/FOO-123)") {
		t.Fatalf("args %q missing linear resolves footer", joined)
	}
}

func TestOnEvent_WorktreeCreated_AddsCrossProjectGitLabResolvesFooter(t *testing.T) {
	stub := &stubRunner{output: []byte("https://gitlab.com/org/repo/-/merge_requests/6\n")}
	a := newWithRunner(config.GlabConfig{}, emptyReviewArtifactRepos, "", stub.run)
	// WorkItemID and WorkspaceID intentionally omitted: this test verifies
	// CLI description formatting only, not persistence.
	payload := mustJSON(worktreePayload{Branch: "sub-GL-9999-40-fix-bug", WorktreePath: "/tmp/wt", SubPlan: "Repo specific implementation plan", TrackerRefs: []domain.TrackerReference{{Provider: "gitlab", Kind: "issue", ID: "40", Repo: "other-group/other-project", URL: "https://gitlab.example.com/other-group/other-project/-/issues/40", Number: 40}}})
	if err := a.OnEvent(context.Background(), domain.SystemEvent{EventType: string(domain.EventWorktreeCreated), Payload: payload}); err != nil {
		t.Fatalf("OnEvent returned error: %v", err)
	}
	joined := strings.Join(stub.calls[1].args, " ")
	if !strings.Contains(joined, "Resolves [other-group/other-project#40](https://gitlab.example.com/other-group/other-project/-/issues/40)") {
		t.Fatalf("args %q missing cross-project gitlab resolves footer", joined)
	}
}

// --- In-memory repos for syncMRDescriptionsOnApproval tests ---

type inMemGitlabMRRepo struct {
	data map[string]domain.GitlabMergeRequest
}

func (r *inMemGitlabMRRepo) Upsert(_ context.Context, mr domain.GitlabMergeRequest) error {
	if r.data == nil {
		r.data = make(map[string]domain.GitlabMergeRequest)
	}
	r.data[mr.ID] = mr
	return nil
}

func (r *inMemGitlabMRRepo) Get(_ context.Context, id string) (domain.GitlabMergeRequest, error) {
	mr, ok := r.data[id]
	if !ok {
		return domain.GitlabMergeRequest{}, errors.New("not found")
	}
	return mr, nil
}

func (r *inMemGitlabMRRepo) GetByIID(_ context.Context, projectPath string, iid int) (domain.GitlabMergeRequest, error) {
	for _, mr := range r.data {
		if mr.ProjectPath == projectPath && mr.IID == iid {
			return mr, nil
		}
	}
	return domain.GitlabMergeRequest{}, errors.New("not found")
}

func (r *inMemGitlabMRRepo) ListByWorkspaceID(_ context.Context, _ string) ([]domain.GitlabMergeRequest, error) {
	out := make([]domain.GitlabMergeRequest, 0, len(r.data))
	for _, mr := range r.data {
		out = append(out, mr)
	}
	return out, nil
}

func (r *inMemGitlabMRRepo) ListNonTerminal(_ context.Context, _ string) ([]domain.GitlabMergeRequest, error) {
	var out []domain.GitlabMergeRequest
	for _, mr := range r.data {
		if mr.State != "merged" && mr.State != "closed" {
			out = append(out, mr)
		}
	}
	return out, nil
}

func (r *inMemGitlabMRRepo) Delete(_ context.Context, id string) error {
	delete(r.data, id)
	return nil
}

type inMemArtifactLinkRepo struct {
	links []domain.SessionReviewArtifact
}

func (r *inMemArtifactLinkRepo) Upsert(_ context.Context, link domain.SessionReviewArtifact) error {
	r.links = append(r.links, link)
	return nil
}

func (r *inMemArtifactLinkRepo) ListByWorkItemID(_ context.Context, workItemID string) ([]domain.SessionReviewArtifact, error) {
	var out []domain.SessionReviewArtifact
	for _, l := range r.links {
		if l.WorkItemID == workItemID {
			out = append(out, l)
		}
	}
	return out, nil
}

func (r *inMemArtifactLinkRepo) ListByWorkspaceID(_ context.Context, workspaceID string) ([]domain.SessionReviewArtifact, error) {
	var out []domain.SessionReviewArtifact
	for _, l := range r.links {
		if l.WorkspaceID == workspaceID {
			out = append(out, l)
		}
	}
	return out, nil
}

func (r *inMemArtifactLinkRepo) TransferArtifactLinks(_ context.Context, fromID, toID string) error {
	for i := range r.links {
		if r.links[i].ProviderArtifactID == fromID {
			r.links[i].ProviderArtifactID = toID
		}
	}
	return nil
}

type missingWorkItemRepo struct{}

func (missingWorkItemRepo) Get(_ context.Context, _ string) (domain.Session, error) {
	return domain.Session{}, repository.ErrNotFound
}

func (missingWorkItemRepo) List(_ context.Context, _ repository.SessionFilter) ([]domain.Session, error) {
	return nil, nil
}

func (missingWorkItemRepo) Create(_ context.Context, _ domain.Session) error { return nil }
func (missingWorkItemRepo) Update(_ context.Context, _ domain.Session) error { return nil }
func (missingWorkItemRepo) Delete(_ context.Context, _ string) error         { return nil }

func TestCheckAllMergedSkipsStaleReviewArtifactLink(t *testing.T) {
	artifactRepo := &inMemArtifactLinkRepo{links: []domain.SessionReviewArtifact{{
		ID:                 "link-1",
		WorkspaceID:        "ws-1",
		WorkItemID:         "wi-deleted",
		Provider:           "gitlab",
		ProviderArtifactID: "mr-1",
	}}}
	a := &GlabAdapter{
		workspaceID: "ws-1",
		repos: coreadapter.ReviewArtifactRepos{
			SessionArtifacts: service.NewSessionReviewArtifactService(repository.NoopTransacter{Res: repository.Resources{SessionReviewArtifacts: artifactRepo}}),
			Sessions:         service.NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: missingWorkItemRepo{}}}, nil),
			Bus:              event.NewBus(event.BusConfig{}),
		},
	}

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(previous)

	a.checkAllMerged(context.Background(), "mr-1")

	logged := logs.String()
	if strings.Contains(logged, "WARN") || strings.Contains(logged, "get work item for merge check failed") {
		t.Fatalf("unexpected warning log: %s", logged)
	}
	if !strings.Contains(logged, "skip merge check for stale review artifact") {
		t.Fatalf("log = %q, want stale-link debug entry", logged)
	}
}

func TestRefreshSingleMRUsesPersistedWorktreePath(t *testing.T) {
	t.Parallel()

	workspaceDir := t.TempDir()
	worktreeDir := t.TempDir()
	stub := &stubRunner{output: []byte(`{"iid":3977,"state":"opened","web_url":"https://gitlab.com/justtrack/frontend/paket/-/merge_requests/3977","draft":false}`)}
	mrRepo := &inMemGitlabMRRepo{data: map[string]domain.GitlabMergeRequest{
		"mr-1": {
			ID:           "mr-1",
			ProjectPath:  "justtrack/frontend/paket",
			IID:          3977,
			State:        "draft",
			SourceBranch: "sub-LIN-APP-1-fix",
			WorktreePath: worktreeDir,
		},
	}}
	repos := coreadapter.ReviewArtifactRepos{
		GitlabMRs: service.NewGitlabMRService(repository.NoopTransacter{Res: repository.Resources{GitlabMRs: mrRepo}}),
	}
	a := newWithRunner(config.GlabConfig{}, repos, workspaceDir, stub.run)

	a.refreshSingleMR(context.Background(), mrRepo.data["mr-1"])

	if len(stub.calls) != 1 {
		t.Fatalf("glab calls = %d, want 1", len(stub.calls))
	}
	if stub.calls[0].dir != worktreeDir {
		t.Fatalf("refresh dir = %q, want persisted worktree path %q", stub.calls[0].dir, worktreeDir)
	}
	updated := mrRepo.data["mr-1"]
	if updated.State != "ready" || updated.WebURL != "https://gitlab.com/justtrack/frontend/paket/-/merge_requests/3977" {
		t.Fatalf("updated MR = %+v", updated)
	}
	if updated.WorktreePath != worktreeDir {
		t.Fatalf("updated worktree path = %q, want %q", updated.WorktreePath, worktreeDir)
	}
}

// --- syncMRDescriptionsOnApproval tests ---

func TestSyncMRDescriptionsOnApproval_UpdatesOpenMRs(t *testing.T) {
	t.Parallel()

	mrRepo := &inMemGitlabMRRepo{data: map[string]domain.GitlabMergeRequest{
		"mr-1": {ID: "mr-1", ProjectPath: "group/project", IID: 10, State: "ready"},
		"mr-2": {ID: "mr-2", ProjectPath: "group/project", IID: 11, State: "merged"},
	}}

	artifactRepo := &inMemArtifactLinkRepo{links: []domain.SessionReviewArtifact{
		{ID: "link-1", WorkspaceID: "ws-1", WorkItemID: "wi-1", Provider: "gitlab", ProviderArtifactID: "mr-1"},
		{ID: "link-2", WorkspaceID: "ws-1", WorkItemID: "wi-1", Provider: "gitlab", ProviderArtifactID: "mr-2"},
	}}

	stub := &stubRunner{}
	repos := coreadapter.ReviewArtifactRepos{
		GitlabMRs:        service.NewGitlabMRService(repository.NoopTransacter{Res: repository.Resources{GitlabMRs: mrRepo}}),
		SessionArtifacts: service.NewSessionReviewArtifactService(repository.NoopTransacter{Res: repository.Resources{SessionReviewArtifacts: artifactRepo}}),
	}
	a := newWithRunner(config.GlabConfig{}, repos, "/workspace", stub.run)

	payload := mustJSON(map[string]any{
		"work_item_id": "wi-1",
		"comment_body": "Updated MR description",
		"external_id":  "gl:issue:1234#5",
		"external_ids": []string{"gl:issue:1234#5"},
	})

	err := a.OnEvent(context.Background(), domain.SystemEvent{
		EventType:   string(domain.EventPlanApproved),
		WorkspaceID: "ws-1",
		Payload:     payload,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Expect exactly one glab api PUT call for the opened MR (iid=10).
	var putCalls []stubCall
	for _, c := range stub.calls {
		joined := strings.Join(c.args, " ")
		if strings.Contains(joined, "api -X PUT") {
			putCalls = append(putCalls, c)
		}
	}
	if len(putCalls) != 1 {
		t.Fatalf("expected 1 PUT call, got %d: %+v", len(putCalls), stub.calls)
	}

	joined := strings.Join(putCalls[0].args, " ")
	if !strings.Contains(joined, "/projects/group%2Fproject/merge_requests/10") {
		t.Errorf("PUT call missing expected path: %q", joined)
	}
	if !strings.Contains(joined, "description=Updated MR description") {
		t.Errorf("PUT call missing expected description field: %q", joined)
	}
}

func TestSyncMRDescriptionsOnApproval_SkipsEmptyCommentBody(t *testing.T) {
	t.Parallel()

	mrRepo := &inMemGitlabMRRepo{data: map[string]domain.GitlabMergeRequest{
		"mr-1": {ID: "mr-1", ProjectPath: "group/project", IID: 10, State: "opened"},
	}}

	artifactRepo := &inMemArtifactLinkRepo{links: []domain.SessionReviewArtifact{
		{ID: "link-1", WorkspaceID: "ws-1", WorkItemID: "wi-1", Provider: "gitlab", ProviderArtifactID: "mr-1"},
	}}

	stub := &stubRunner{}
	repos := coreadapter.ReviewArtifactRepos{
		GitlabMRs:        service.NewGitlabMRService(repository.NoopTransacter{Res: repository.Resources{GitlabMRs: mrRepo}}),
		SessionArtifacts: service.NewSessionReviewArtifactService(repository.NoopTransacter{Res: repository.Resources{SessionReviewArtifacts: artifactRepo}}),
	}
	a := newWithRunner(config.GlabConfig{}, repos, "/workspace", stub.run)

	payload := mustJSON(map[string]any{
		"work_item_id": "wi-1",
		"comment_body": "",
	})

	err := a.OnEvent(context.Background(), domain.SystemEvent{
		EventType:   string(domain.EventPlanApproved),
		WorkspaceID: "ws-1",
		Payload:     payload,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, c := range stub.calls {
		joined := strings.Join(c.args, " ")
		if strings.Contains(joined, "api -X PUT") && strings.Contains(joined, "merge_requests") {
			t.Fatalf("expected no PUT calls for merge_requests, got: %q", joined)
		}
	}
}

func TestSyncMRDescriptionsOnApproval_NoRepos(t *testing.T) {
	t.Parallel()

	stub := &stubRunner{}
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, "/workspace", stub.run)

	payload := mustJSON(map[string]any{
		"work_item_id": "wi-1",
		"comment_body": "Updated MR description",
	})

	err := a.OnEvent(context.Background(), domain.SystemEvent{
		EventType:   string(domain.EventPlanApproved),
		WorkspaceID: "ws-1",
		Payload:     payload,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stub.calls) != 0 {
		t.Fatalf("expected no runner calls, got %d", len(stub.calls))
	}
}

func TestResolveProjectPath(t *testing.T) {
	t.Parallel()

	t.Run("valid path with slash", func(t *testing.T) {
		t.Parallel()
		mr := domain.GitlabMergeRequest{
			ProjectPath: "justtrack/frontend/paket",
			WebURL:      "https://gitlab.justtrack.io/justtrack/frontend/paket/-/merge_requests/3977",
		}
		if got := resolveProjectPath(mr); got != "justtrack/frontend/paket" {
			t.Fatalf("got %q, want %q", got, "justtrack/frontend/paket")
		}
	})

	t.Run("local folder name corrected from WebURL", func(t *testing.T) {
		t.Parallel()
		mr := domain.GitlabMergeRequest{
			ProjectPath: "backend.postback-service",
			WebURL:      "https://gitlab.justtrack.io/justtrack/backend/postback-service/-/merge_requests/421",
		}
		if got := resolveProjectPath(mr); got != "justtrack/backend/postback-service" {
			t.Fatalf("got %q, want %q", got, "justtrack/backend/postback-service")
		}
	})

	t.Run("empty WebURL falls back to stored path", func(t *testing.T) {
		t.Parallel()
		mr := domain.GitlabMergeRequest{
			ProjectPath: "some-folder",
			WebURL:      "",
		}
		if got := resolveProjectPath(mr); got != "some-folder" {
			t.Fatalf("got %q, want %q", got, "some-folder")
		}
	})

	t.Run("WebURL without merge request path falls back", func(t *testing.T) {
		t.Parallel()
		mr := domain.GitlabMergeRequest{
			ProjectPath: "some-folder",
			WebURL:      "https://gitlab.justtrack.io/invalid-url",
		}
		if got := resolveProjectPath(mr); got != "some-folder" {
			t.Fatalf("got %q, want %q", got, "some-folder")
		}
	})
}

func TestRefreshSingleMR_CorrectsBadProjectPath(t *testing.T) {
	t.Parallel()

	workspaceDir := t.TempDir()
	stub := &stubRunner{
		output: []byte(`{"iid":421,"state":"opened","web_url":"https://gitlab.justtrack.io/justtrack/backend/postback-service/-/merge_requests/421","draft":false}`),
	}
	mrRepo := &inMemGitlabMRRepo{data: map[string]domain.GitlabMergeRequest{
		"mr-1": {
			ID:           "mr-1",
			ProjectPath:  "backend.postback-service",
			IID:          421,
			State:        "draft",
			SourceBranch: "sub-LIN-APP-1-fix",
			WebURL:       "https://gitlab.justtrack.io/justtrack/backend/postback-service/-/merge_requests/421",
		},
	}}
	repos := coreadapter.ReviewArtifactRepos{
		GitlabMRs: service.NewGitlabMRService(repository.NoopTransacter{Res: repository.Resources{GitlabMRs: mrRepo}}),
	}
	a := newWithRunner(config.GlabConfig{}, repos, workspaceDir, stub.run)

	a.refreshSingleMR(context.Background(), mrRepo.data["mr-1"])

	// The stub should have been called with the corrected path.
	if len(stub.calls) < 1 {
		t.Fatalf("expected at least 1 glab call, got %d", len(stub.calls))
	}

	// Verify the API endpoint uses the corrected project path.
	foundEndpoint := false
	for _, call := range stub.calls {
		for _, arg := range call.args {
			if strings.Contains(arg, "/projects/justtrack%2Fbackend%2Fpostback-service/merge_requests/421") {
				foundEndpoint = true
				break
			}
		}
	}
	if !foundEndpoint {
		t.Fatalf("expected API call with corrected path, calls: %+v", stub.calls)
	}

	// Verify the MR was updated with the correct path.
	updated := mrRepo.data["mr-1"]
	if updated.ProjectPath != "justtrack/backend/postback-service" {
		t.Fatalf("project path = %q, want %q", updated.ProjectPath, "justtrack/backend/postback-service")
	}
	if updated.State != "ready" {
		t.Fatalf("state = %q, want %q", updated.State, "ready")
	}
}

func TestRefreshSingleMR_UsesOriginRemoteForBadStoredProjectPath(t *testing.T) {
	t.Parallel()

	workspaceDir := t.TempDir()
	worktreeDir := t.TempDir()
	var calls []stubCall
	runner := func(_ context.Context, dir, name string, args ...string) ([]byte, error) {
		calls = append(calls, stubCall{dir: dir, name: name, args: args})
		joined := strings.Join(args, " ")
		switch {
		case name == "git" && joined == "remote get-url origin":
			return []byte("git@gitlab.justtrack.io:justtrack/backend/postback-service.git\n"), nil
		case name == "glab" && strings.HasPrefix(joined, "mr view "):
			return nil, errors.New("no merge request for source branch")
		case name == "glab" && strings.Contains(joined, "/projects/justtrack%2Fbackend%2Fpostback-service/merge_requests/421"):
			return []byte(`{"iid":421,"state":"merged","web_url":"https://gitlab.justtrack.io/justtrack/backend/postback-service/-/merge_requests/421","draft":false}`), nil
		case name == "glab" && strings.Contains(joined, "/projects/backend.postback-service/merge_requests/421"):
			t.Fatalf("queried local folder name as GitLab project path: %q", joined)
		}
		return nil, errors.New("unexpected command: " + name + " " + joined)
	}
	mrRepo := &inMemGitlabMRRepo{data: map[string]domain.GitlabMergeRequest{
		"mr-1": {
			ID:           "mr-1",
			ProjectPath:  "backend.postback-service",
			IID:          421,
			State:        "ready",
			SourceBranch: "sub-GL-421-fix",
			WorktreePath: worktreeDir,
		},
	}}
	repos := coreadapter.ReviewArtifactRepos{
		GitlabMRs: service.NewGitlabMRService(repository.NoopTransacter{Res: repository.Resources{GitlabMRs: mrRepo}}),
	}
	a := newWithRunner(config.GlabConfig{BaseURL: "https://gitlab.justtrack.io"}, repos, workspaceDir, runner)

	a.refreshSingleMR(context.Background(), mrRepo.data["mr-1"])

	updated := mrRepo.data["mr-1"]
	if updated.ProjectPath != "justtrack/backend/postback-service" {
		t.Fatalf("project path = %q, want justtrack/backend/postback-service", updated.ProjectPath)
	}
	if updated.State != "merged" {
		t.Fatalf("state = %q, want merged", updated.State)
	}
	if len(calls) != 3 {
		t.Fatalf("calls = %+v, want git remote, local mr view, remote mr api", calls)
	}
}
