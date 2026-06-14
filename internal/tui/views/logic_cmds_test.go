package views

import (
	"context"
	"testing"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/logic"
)

type fakeLogicClient struct {
	sessions []domain.Session
	approved bool
	answered bool
	ranPlan  string
}

func (f *fakeLogicClient) GetInitialSnapshot(context.Context, string) (logic.InitialSnapshot, error) {
	return logic.InitialSnapshot{Sessions: f.sessions}, nil
}
func (f *fakeLogicClient) ListSessions(context.Context, string) ([]domain.Session, error) {
	return f.sessions, nil
}
func (f *fakeLogicClient) GetSession(context.Context, string) (domain.Session, error) {
	return domain.Session{}, nil
}
func (f *fakeLogicClient) SearchSessionHistory(context.Context, domain.SessionHistoryFilter) ([]domain.SessionHistoryEntry, error) {
	return nil, nil
}
func (f *fakeLogicClient) ArchiveSession(context.Context, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "Session archived"}, nil
}
func (f *fakeLogicClient) UnarchiveSession(context.Context, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "Session unarchived"}, nil
}
func (f *fakeLogicClient) DeleteSession(context.Context, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "Session deleted"}, nil
}
func (f *fakeLogicClient) OverrideAccept(context.Context, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "Work item accepted"}, nil
}
func (f *fakeLogicClient) FailReview(context.Context, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "Work item failed"}, nil
}
func (f *fakeLogicClient) ApprovePlan(context.Context, string, string) (logic.ActionResult, error) {
	f.approved = true
	return logic.ActionResult{Message: "approved"}, nil
}
func (f *fakeLogicClient) RequestPlanChanges(context.Context, string, string, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "Plan revised"}, nil
}
func (f *fakeLogicClient) SaveReviewedPlan(context.Context, string, string) (domain.Plan, []domain.TaskPlan, error) {
	return domain.Plan{}, nil, nil
}
func (f *fakeLogicClient) RunImplementation(context.Context, string) (logic.Operation, error) {
	f.ranPlan = "plan-1"
	return logic.Operation{}, nil
}
func (f *fakeLogicClient) StartPlanning(context.Context, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "Planning complete"}, nil
}
func (f *fakeLogicClient) CancelPipeline(context.Context, string, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "Pipeline cancelled"}, nil
}

func (f *fakeLogicClient) RestartPlanning(context.Context, string, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "Planning restart dispatched"}, nil
}
func (f *fakeLogicClient) FollowUpPlan(context.Context, string, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "Follow-up plan ready for review"}, nil
}
func (f *fakeLogicClient) FinalizeWorkItem(context.Context, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "Finalize dispatched"}, nil
}
func (f *fakeLogicClient) RetryFailedWorkItem(context.Context, string, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "Retry dispatched"}, nil
}
func (f *fakeLogicClient) ResumeAllSessionsForWorkItem(context.Context, string, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "Resume dispatched"}, nil
}
func (f *fakeLogicClient) RetryAgentSession(context.Context, string, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "Retry dispatched"}, nil
}
func (f *fakeLogicClient) FollowUpAgentSession(context.Context, string, string, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "Follow-up dispatched"}, nil
}
func (f *fakeLogicClient) SteerSession(context.Context, string, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "Steering prompt sent"}, nil
}
func (f *fakeLogicClient) AnswerQuestion(context.Context, string, string, string) (logic.ActionResult, error) {
	f.answered = true
	return logic.ActionResult{Message: "Answer submitted"}, nil
}
func (f *fakeLogicClient) SkipQuestion(context.Context, string) (logic.ActionResult, error) {
	return logic.ActionResult{Message: "Question skipped"}, nil
}

func TestProductLogicCommands(t *testing.T) {
	client := &fakeLogicClient{sessions: []domain.Session{{ID: "work-1", WorkspaceID: "ws-1"}}}
	if msg := LoadSessionsWithClientCmd(client, "ws-1")(); msg.(InitialSnapshotLoadedMsg).Snapshot.Sessions[0].ID != "work-1" {
		t.Fatalf("LoadSessionsWithClientCmd msg = %#v", msg)
	}
	if msg := ApprovePlanWithClientCmd(client, "plan-1", "work-1")(); msg.(PlanApprovedMsg).PlanID != "plan-1" || !client.approved {
		t.Fatalf("ApprovePlanWithClientCmd msg = %#v approved=%v", msg, client.approved)
	}
	if msg := AnswerQuestionWithClientCmd(client, "q-1", "answer", "human")(); msg.(ActionDoneMsg).Message != "Answer submitted" || !client.answered {
		t.Fatalf("AnswerQuestionWithClientCmd msg = %#v answered=%v", msg, client.answered)
	}
}
