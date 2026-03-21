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
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, stub.run)
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
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, stub.run)

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
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, stub.run)

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
	}, coreadapter.ReviewArtifactRepos{}, stub.run)

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
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, stub.run)

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
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, stub.run)

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
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, stub.run)

	// Pre-populate tracking
	branch := "sub-LIN-FOO-99-complete-me"
	a.mu.Lock()
	a.tracked[branch] = []branchEntry{
		{repo: "repo-a", worktreePath: "/wt/a"},
		{repo: "repo-b", worktreePath: "/wt/b"},
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
			if !strings.Contains(joined, "--draft=false") {
				t.Errorf("call[%d] missing --draft=false: %q", i, joined)
			}
			if !strings.Contains(joined, "--source-branch "+branch) {
				t.Errorf("call[%d] missing --source-branch: %q", i, joined)
			}
		}
	}
	if updateCalls != 2 {
		t.Fatalf("updateCalls = %d, want 2", updateCalls)
	}
}

func TestOnEvent_WorkItemCompleted_NoBranch_ReturnsNil(t *testing.T) {
	stub := &stubRunner{}
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, stub.run)

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
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, stub.run)

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

func TestOnEvent_WorkItemCompleted_GlabFailure_ReturnsNil(t *testing.T) {
	stub := &stubRunner{err: errors.New("glab: MR not found")}
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, stub.run)

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

// --- mrExists (JSON parsing from `glab mr view`) ---

func TestMRExists_MRPresent_ReturnsTrue(t *testing.T) {
	mrJSON := `{"iid":3,"state":"opened","web_url":"https://gitlab.com/org/repo/-/merge_requests/3"}`
	stub := &stubRunner{output: []byte(mrJSON)}
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, stub.run)

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
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, stub.run)

	if a.mrExists(context.Background(), "/wt", "no-such-branch") {
		t.Error("expected mrExists to return false on glab error")
	}
}

func TestMRExists_InvalidJSON_ReturnsFalse(t *testing.T) {
	stub := &stubRunner{output: []byte("not json")}
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, stub.run)

	if a.mrExists(context.Background(), "/wt", "branch") {
		t.Error("expected mrExists to return false on invalid JSON")
	}
}

func TestMRExists_ZeroIID_ReturnsFalse(t *testing.T) {
	// Some glab versions return empty JSON on not-found instead of non-zero exit
	stub := &stubRunner{output: []byte(`{"iid":0,"state":"","web_url":""}`)}
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, stub.run)

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
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, runner)

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
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, stub.run)
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
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, stub.run)
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
	a := newWithRunner(config.GlabConfig{}, coreadapter.ReviewArtifactRepos{}, stub.run)
	payload := mustJSON(worktreePayload{Branch: "sub-GL-9999-40-fix-bug", WorktreePath: "/tmp/wt", SubPlan: "Repo specific implementation plan", TrackerRefs: []domain.TrackerReference{{Provider: "gitlab", Kind: "issue", ID: "40", Repo: "other-group/other-project", URL: "https://gitlab.example.com/other-group/other-project/-/issues/40", Number: 40}}})
	if err := a.OnEvent(context.Background(), domain.SystemEvent{EventType: string(domain.EventWorktreeCreated), Payload: payload}); err != nil {
		t.Fatalf("OnEvent returned error: %v", err)
	}
	joined := strings.Join(stub.calls[1].args, " ")
	if !strings.Contains(joined, "Resolves [other-group/other-project#40](https://gitlab.example.com/other-group/other-project/-/issues/40)") {
		t.Fatalf("args %q missing cross-project gitlab resolves footer", joined)
	}
}
