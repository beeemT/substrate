package domain

import (
	"testing"
	"time"
)

func graphSession(id string, kind AgentSessionKind, status AgentSessionStatus, parent string) AgentSession {
	return AgentSession{
		ID:                   id,
		WorkItemID:           "wi-1",
		WorkspaceID:          "ws-1",
		Kind:                 kind,
		SubPlanID:            "sp-1",
		RepositoryName:       "repo-1",
		Status:               status,
		ParentAgentSessionID: parent,
		CreatedAt:            time.Unix(1, 0),
		UpdatedAt:            time.Unix(1, 0),
	}
}

func TestAgentSessionGraphHelpers_ExcludeSupersededFailedParent(t *testing.T) {
	parent := graphSession("failed-parent", AgentSessionKindImplementation, AgentSessionFailed, "")
	child := graphSession("running-child", AgentSessionKindImplementation, AgentSessionRunning, parent.ID)

	if _, ok := FindLeafAgentSessionByID([]AgentSession{parent, child}, parent.ID); ok {
		t.Fatal("failed parent with a child must not be a leaf")
	}
	leaves := LeafAgentSessions([]AgentSession{parent, child})
	if len(leaves) != 1 || leaves[0].ID != child.ID {
		t.Fatalf("leaves = %+v, want only %q", leaves, child.ID)
	}
}

func TestAgentSessionGraphHelpers_FailedChildLeafRetryable(t *testing.T) {
	parent := graphSession("interrupted-parent", AgentSessionKindImplementation, AgentSessionInterrupted, "")
	child := graphSession("failed-child", AgentSessionKindImplementation, AgentSessionFailed, parent.ID)

	retryable := RetryableAgentSessionLeaves([]AgentSession{parent, child})
	if len(retryable) != 1 || retryable[0].ID != child.ID {
		t.Fatalf("retryable = %+v, want only %q", retryable, child.ID)
	}
	if len(ResumableAgentSessionLeaves([]AgentSession{parent, child})) != 0 {
		t.Fatal("superseded interrupted parent must not remain resumable")
	}
}

func TestAgentSessionGraphHelpers_ReviewRetryChainUsesReplacementReviewLeaf(t *testing.T) {
	impl := graphSession("impl", AgentSessionKindImplementation, AgentSessionCompleted, "")
	review := graphSession("review-failed", AgentSessionKindReview, AgentSessionFailed, impl.ID)
	replacement := graphSession("review-replacement", AgentSessionKindReview, AgentSessionRunning, review.ID)

	leaves := LeafAgentSessions([]AgentSession{impl, review, replacement})
	if len(leaves) != 1 || leaves[0].ID != replacement.ID {
		t.Fatalf("leaves = %+v, want only replacement review", leaves)
	}
}

func TestAgentSessionGraphHelpers_ExcludeManualSessions(t *testing.T) {
	manual := graphSession("manual", AgentSessionKindManual, AgentSessionFailed, "")
	auto := graphSession("auto", AgentSessionKindImplementation, AgentSessionFailed, "")

	leaves := RetryableAgentSessionLeaves([]AgentSession{manual, auto})
	if len(leaves) != 1 || leaves[0].ID != auto.ID {
		t.Fatalf("retryable = %+v, want only automated session", leaves)
	}
	if IsLeafAgentSessionID([]AgentSession{manual}, manual.ID) {
		t.Fatal("manual session must not be treated as a graph leaf")
	}
}
