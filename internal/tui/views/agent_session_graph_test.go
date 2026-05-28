package views

import (
	"sort"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/domain"
)

func leafIDs(sessions []domain.AgentSession) []string {
	ids := make([]string, 0, len(sessions))
	for _, s := range sessions {
		ids = append(ids, s.ID)
	}
	sort.Strings(ids)
	return ids
}

// TestLeafAgentSessions_RetryProjection covers a sub-plan where the previous
// implementation was interrupted/failed and a new session was started. The
// previous session has a child (the new one), so it must not appear as a leaf
// — only the new running session is a leaf.
func TestLeafAgentSessions_RetryProjection(t *testing.T) {
	t0 := time.Now().Add(-time.Hour)
	t1 := t0.Add(10 * time.Minute)

	old := domain.AgentSession{
		ID:             "old",
		SubPlanID:      "sp-1",
		RepositoryName: "repo-a",
		Status:         domain.AgentSessionInterrupted,
		CreatedAt:      t0,
		UpdatedAt:      t0,
	}
	newer := domain.AgentSession{
		ID:                   "new",
		SubPlanID:            "sp-1",
		RepositoryName:       "repo-a",
		Status:               domain.AgentSessionRunning,
		CreatedAt:            t1,
		UpdatedAt:            t1,
		ParentAgentSessionID: "old",
	}

	got := leafAgentSessions([]domain.AgentSession{old, newer})
	if want := []string{"new"}; !sliceEqual(leafIDs(got), want) {
		t.Errorf("leaves = %v, want %v", leafIDs(got), want)
	}
}

// TestLeafAgentSessions_ReviewLeaf covers an implementation that completed and
// a review session was started for it. The implementation has a child (the
// review), so the leaf is the review.
func TestLeafAgentSessions_ReviewLeaf(t *testing.T) {
	t0 := time.Now().Add(-time.Hour)
	t1 := t0.Add(10 * time.Minute)

	impl := domain.AgentSession{
		ID:             "impl",
		SubPlanID:      "sp-1",
		RepositoryName: "repo-a",
		Kind:           domain.AgentSessionKindImplementation,
		Status:         domain.AgentSessionCompleted,
		CreatedAt:      t0,
		UpdatedAt:      t0,
	}
	review := domain.AgentSession{
		ID:                   "review",
		SubPlanID:            "sp-1",
		RepositoryName:       "repo-a",
		Kind:                 domain.AgentSessionKindReview,
		Status:               domain.AgentSessionRunning,
		CreatedAt:            t1,
		UpdatedAt:            t1,
		ParentAgentSessionID: "impl",
	}

	got := leafAgentSessions([]domain.AgentSession{impl, review})
	if want := []string{"review"}; !sliceEqual(leafIDs(got), want) {
		t.Errorf("leaves = %v, want %v", leafIDs(got), want)
	}
}

// TestLeafAgentSessions_ReimplementationChain covers the impl -> review ->
// reimpl chain. Only the reimplementation is a leaf because the review has a
// child (reimpl) and the original impl has a child (review).
func TestLeafAgentSessions_ReimplementationChain(t *testing.T) {
	t0 := time.Now().Add(-3 * time.Hour)
	t1 := t0.Add(time.Hour)
	t2 := t1.Add(time.Hour)

	impl := domain.AgentSession{
		ID:             "impl",
		SubPlanID:      "sp-1",
		RepositoryName: "repo-a",
		Kind:           domain.AgentSessionKindImplementation,
		Status:         domain.AgentSessionCompleted,
		CreatedAt:      t0,
		UpdatedAt:      t0,
	}
	review := domain.AgentSession{
		ID:                   "review",
		SubPlanID:            "sp-1",
		RepositoryName:       "repo-a",
		Kind:                 domain.AgentSessionKindReview,
		Status:               domain.AgentSessionCompleted,
		CreatedAt:            t1,
		UpdatedAt:            t1,
		ParentAgentSessionID: "impl",
	}
	reimpl := domain.AgentSession{
		ID:                   "reimpl",
		SubPlanID:            "sp-1",
		RepositoryName:       "repo-a",
		Kind:                 domain.AgentSessionKindImplementation,
		Status:               domain.AgentSessionRunning,
		CreatedAt:            t2,
		UpdatedAt:            t2,
		ParentAgentSessionID: "review",
	}

	got := leafAgentSessions([]domain.AgentSession{impl, review, reimpl})
	if want := []string{"reimpl"}; !sliceEqual(leafIDs(got), want) {
		t.Errorf("leaves = %v, want %v", leafIDs(got), want)
	}
}

// TestLeafAgentSessions_LegacyFallback covers rows that have NO graph edges
// at all (pre-migration or rows from a separate flow). When a (sub_plan_id,
// repo_name) group has no edges, the newest session by CreatedAt wins so
// historical interrupted rows don't pollute the leaf set.
func TestLeafAgentSessions_LegacyFallback(t *testing.T) {
	t0 := time.Now().Add(-2 * time.Hour)
	t1 := t0.Add(time.Hour)

	old := domain.AgentSession{
		ID:             "legacy-old",
		SubPlanID:      "sp-1",
		RepositoryName: "repo-a",
		Status:         domain.AgentSessionInterrupted,
		CreatedAt:      t0,
		UpdatedAt:      t0,
	}
	newer := domain.AgentSession{
		ID:             "legacy-new",
		SubPlanID:      "sp-1",
		RepositoryName: "repo-a",
		Status:         domain.AgentSessionRunning,
		CreatedAt:      t1,
		UpdatedAt:      t1,
	}

	got := leafAgentSessions([]domain.AgentSession{old, newer})
	if want := []string{"legacy-new"}; !sliceEqual(leafIDs(got), want) {
		t.Errorf("legacy fallback leaves = %v, want %v", leafIDs(got), want)
	}
}

// TestLeafAgentSessions_LegacyFallbackTieBreakByUpdatedThenID covers tie-break
// in the legacy-fallback path when CreatedAt is identical.
func TestLeafAgentSessions_LegacyFallbackTieBreakByUpdatedThenID(t *testing.T) {
	t0 := time.Now()
	a := domain.AgentSession{
		ID:             "aa",
		SubPlanID:      "sp-1",
		RepositoryName: "repo-a",
		CreatedAt:      t0,
		UpdatedAt:      t0,
	}
	b := domain.AgentSession{
		ID:             "bb",
		SubPlanID:      "sp-1",
		RepositoryName: "repo-a",
		CreatedAt:      t0,
		UpdatedAt:      t0.Add(time.Second),
	}
	c := domain.AgentSession{
		ID:             "cc",
		SubPlanID:      "sp-1",
		RepositoryName: "repo-a",
		CreatedAt:      t0,
		UpdatedAt:      t0.Add(time.Second),
	}

	got := leafAgentSessions([]domain.AgentSession{a, b, c})
	if want := []string{"cc"}; !sliceEqual(leafIDs(got), want) {
		t.Errorf("tie-break leaves = %v, want %v", leafIDs(got), want)
	}
}

// TestLeafAgentSessions_DifferentSubPlansIndependent verifies that the legacy
// fallback only collapses leaves within a single (sub_plan_id, repo_name)
// group — different sub-plans are not merged.
func TestLeafAgentSessions_DifferentSubPlansIndependent(t *testing.T) {
	t0 := time.Now()
	t1 := t0.Add(time.Minute)

	a := domain.AgentSession{
		ID:             "a",
		SubPlanID:      "sp-1",
		RepositoryName: "repo-a",
		CreatedAt:      t0,
		UpdatedAt:      t0,
	}
	b := domain.AgentSession{
		ID:             "b",
		SubPlanID:      "sp-2",
		RepositoryName: "repo-b",
		CreatedAt:      t1,
		UpdatedAt:      t1,
	}

	got := leafAgentSessions([]domain.AgentSession{a, b})
	if want := []string{"a", "b"}; !sliceEqual(leafIDs(got), want) {
		t.Errorf("multi-subplan leaves = %v, want %v", leafIDs(got), want)
	}
}

// TestLeafAgentSessions_GraphEdgeKeepsAllLeaves verifies that once a group
// has any graph edge (parent or child), all leaves are kept verbatim — the
// legacy fallback no longer applies. This prevents losing legitimate parallel
// leaves (e.g. a follow-up session that runs alongside).
func TestLeafAgentSessions_GraphEdgeKeepsAllLeaves(t *testing.T) {
	t0 := time.Now()

	root := domain.AgentSession{
		ID:             "root",
		SubPlanID:      "sp-1",
		RepositoryName: "repo-a",
		CreatedAt:      t0,
		UpdatedAt:      t0,
	}
	childA := domain.AgentSession{
		ID:                   "child-a",
		SubPlanID:            "sp-1",
		RepositoryName:       "repo-a",
		CreatedAt:            t0.Add(time.Minute),
		UpdatedAt:            t0.Add(time.Minute),
		ParentAgentSessionID: "root",
	}
	childB := domain.AgentSession{
		ID:                   "child-b",
		SubPlanID:            "sp-1",
		RepositoryName:       "repo-a",
		CreatedAt:            t0.Add(2 * time.Minute),
		UpdatedAt:            t0.Add(2 * time.Minute),
		ParentAgentSessionID: "root",
	}

	got := leafAgentSessions([]domain.AgentSession{root, childA, childB})
	if want := []string{"child-a", "child-b"}; !sliceEqual(leafIDs(got), want) {
		t.Errorf("graph-edge leaves = %v, want %v", leafIDs(got), want)
	}
}

// TestLeafAgentSessions_Empty handles the empty-slice edge case.
func TestLeafAgentSessions_Empty(t *testing.T) {
	if got := leafAgentSessions(nil); len(got) != 0 {
		t.Errorf("expected empty result, got %v", got)
	}
}

func sliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
