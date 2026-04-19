package glab

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	coreadapter "github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
)

// stubRunner returns a commandRunner that records calls and returns the configured
// output/error.
type stubRunner struct {
	calls  []stubCall
	output []byte
	err    error
}

type stubCall struct {
	dir  string
	name string
	args []string
}

func (s *stubRunner) run(_ context.Context, dir, name string, args ...string) ([]byte, error) {
	s.calls = append(s.calls, stubCall{dir: dir, name: name, args: args})

	return s.output, s.err
}

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
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, "", stub.run)

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
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, "", stub.run)

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
	}, coreadapter.ReviewArtifactRepos{}, "", stub.run)

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
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, "", stub.run)

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

// --- OnEvent WorkItemCompleted ---

func TestOnEvent_WorkItemCompleted_UnDraftsMRs(t *testing.T) {
	stub := &stubRunner{}
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, "", stub.run)

	// Pre-populate tracking
	branch := "sub-LIN-FOO-99-complete-me"
	a.mu.Lock()
	a.tracked[branch] = []branchEntry{
		{repo: "repo-a", worktreePath: "/wt/a", ref: "!1", url: "https://gitlab.com/org/repo-a/-/merge_requests/1"},
		{repo: "repo-b", worktreePath: "/wt/b", ref: "!2", url: "https://gitlab.com/org/repo-b/-/merge_requests/2"},
	}
	a.mu.Unlock()

	payload := mustJSON(completedPayload{Branch: branch, ExternalID: "LIN-FOO-99"})
	err := a.OnEvent(context.Background(), domain.SystemEvent{
		EventType: string(domain.EventWorkItemCompleted),
		Payload:   payload,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stub.calls) != 4 {
		t.Fatalf("expected 4 glab calls (update + view per repo), got %d", len(stub.calls))
	}
	updateCalls := 0
	for i, call := range stub.calls {
		joined := strings.Join(call.args, " ")
		if strings.Contains(joined, "mr update") {
			updateCalls++
			if !strings.Contains(joined, "--ready") {
				t.Errorf("call[%d] missing --ready: %q", i, joined)
			}
			if !strings.Contains(joined, "mr update "+branch) {
				t.Errorf("call[%d] missing branch as positional arg: %q", i, joined)
			}
		}
	}
	if updateCalls != 2 {
		t.Fatalf("updateCalls = %d, want 2", updateCalls)
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

func TestOnEvent_WorkItemCompleted_CreatesMRWhenNoneExists(t *testing.T) {
	// Simulate adapter restart: tracked entry has empty ref/url
	// (MR creation failed during worktree setup). mrExists returns false,
	// so createMR should be called.
	var calls []stubCall
	createdMR := false
	mrJSON := `{"iid":10,"state":"opened","web_url":"https://gitlab.com/org/repo/-/merge_requests/10"}`
	callCount := 0
	runner := func(_ context.Context, dir, name string, args ...string) ([]byte, error) {
		calls = append(calls, stubCall{dir: dir, name: name, args: args})
		callCount++
		joined := strings.Join(args, " ")
		// First call is mr view (mrExists check) — return error.
		if callCount == 1 && strings.Contains(joined, "mr view") {
			return nil, errors.New("MR not found")
		}
		// Second call is mr create.
		if strings.Contains(joined, "mr create") {
			createdMR = true
			return []byte("https://gitlab.com/org/repo/-/merge_requests/10\n"), nil
		}
		// Third call is mr update (markMRReady after create).
		if strings.Contains(joined, "mr update") {
			return nil, nil
		}
		// Fourth call is mr view for recording.
		if strings.Contains(joined, "mr view") {
			return []byte(mrJSON), nil
		}
		t.Errorf("unexpected call %d: %q %v", callCount, name, args)
		return nil, errors.New("unexpected")
	}

	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, "", runner)

	branch := "sub-LIN-FOO-7-create-missing"
	a.mu.Lock()
	a.tracked[branch] = []branchEntry{{repo: "repo-c", worktreePath: "/wt/c"}}
	a.mu.Unlock()

	payload := mustJSON(completedPayload{
		Branch:        branch,
		ExternalID:    "LIN-FOO-7",
		WorkItemTitle: "Create missing MR",
		SubPlan:       "Implementation details",
	})
	err := a.OnEvent(context.Background(), domain.SystemEvent{
		EventType: string(domain.EventWorkItemCompleted),
		Payload:   payload,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !createdMR {
		t.Fatal("expected mr create to be called when no MR exists")
	}
	// Verify the expected call sequence: mr create + mr update (mark ready).
	var foundCreate, foundUpdate bool
	for _, call := range calls {
		joined := strings.Join(call.args, " ")
		if strings.Contains(joined, "mr create") {
			foundCreate = true
			if !strings.Contains(joined, "--title Create missing MR") {
				t.Errorf("mr create missing expected title: %q", joined)
			}
		}
		if strings.Contains(joined, "mr update") && strings.Contains(joined, "--ready") {
			foundUpdate = true
		}
	}
	if !foundCreate {
		t.Fatal("expected mr create to be called when no MR exists")
	}
	if !foundUpdate {
		t.Fatal("expected mr update --ready after completion-time create")
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
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, "", runner)

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
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, "", stub.run)
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
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, "", stub.run)
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
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, "", stub.run)
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

// --- syncMRDescriptionsOnApproval tests ---

func TestSyncMRDescriptionsOnApproval_UpdatesOpenMRs(t *testing.T) {
	t.Parallel()

	mrRepo := &inMemGitlabMRRepo{data: map[string]domain.GitlabMergeRequest{
		"mr-1": {ID: "mr-1", ProjectPath: "group/project", IID: 10, State: "opened"},
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
		"work_item_id":  "wi-1",
		"comment_body":  "Updated MR description",
		"external_id":   "gl:issue:1234#5",
		"external_ids":  []string{"gl:issue:1234#5"},
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