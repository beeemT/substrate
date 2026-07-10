package logic

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/orchestrator"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
)

type archiveTestPublisher struct{}

func (archiveTestPublisher) Publish(context.Context, domain.SystemEvent) error { return nil }

type archiveTestSessionRepository struct {
	items                    map[string]domain.Session
	agentRepo                *archiveTestAgentSessionRepository
	archiveCalled            bool
	archiveBeforeTermination bool
}

func (r *archiveTestSessionRepository) Get(_ context.Context, id string) (domain.Session, error) {
	item, ok := r.items[id]
	if !ok {
		return domain.Session{}, repository.ErrNotFound
	}
	return item, nil
}

func (r *archiveTestSessionRepository) List(context.Context, repository.SessionFilter) ([]domain.Session, error) {
	items := make([]domain.Session, 0, len(r.items))
	for _, item := range r.items {
		items = append(items, item)
	}
	return items, nil
}

func (r *archiveTestSessionRepository) Create(_ context.Context, item domain.Session) error {
	r.items[item.ID] = item
	return nil
}

func (r *archiveTestSessionRepository) Update(_ context.Context, item domain.Session) error {
	if item.State == domain.SessionArchived {
		r.archiveCalled = true
		for _, agentSession := range r.agentRepo.sessions {
			switch agentSession.Status {
			case domain.AgentSessionPending, domain.AgentSessionRunning, domain.AgentSessionWaitingForAnswer:
				r.archiveBeforeTermination = true
			}
		}
	}
	r.items[item.ID] = item
	return nil
}

func (r *archiveTestSessionRepository) Delete(_ context.Context, id string) error {
	delete(r.items, id)
	return nil
}

type archiveTestAgentSessionRepository struct {
	sessions   map[string]domain.AgentSession
	updateErr  map[string]error
}

func (r *archiveTestAgentSessionRepository) Get(_ context.Context, id string) (domain.AgentSession, error) {
	session, ok := r.sessions[id]
	if !ok {
		return domain.AgentSession{}, repository.ErrNotFound
	}
	return session, nil
}

func (r *archiveTestAgentSessionRepository) ListByWorkItemID(_ context.Context, workItemID string) ([]domain.AgentSession, error) {
	ids := make([]string, 0, len(r.sessions))
	for id, session := range r.sessions {
		if session.WorkItemID == workItemID {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	result := make([]domain.AgentSession, 0, len(ids))
	for _, id := range ids {
		result = append(result, r.sessions[id])
	}
	return result, nil
}

func (r *archiveTestAgentSessionRepository) ListBySubPlanID(context.Context, string) ([]domain.AgentSession, error) {
	return nil, nil
}

func (r *archiveTestAgentSessionRepository) ListByWorkspaceID(_ context.Context, workspaceID string) ([]domain.AgentSession, error) {
	result := make([]domain.AgentSession, 0)
	for _, session := range r.sessions {
		if session.WorkspaceID == workspaceID {
			result = append(result, session)
		}
	}
	return result, nil
}

func (r *archiveTestAgentSessionRepository) ListActiveChildrenByParentID(context.Context, string) ([]domain.AgentSession, error) {
	return nil, nil
}

func (r *archiveTestAgentSessionRepository) ListByOwnerInstanceID(context.Context, string) ([]domain.AgentSession, error) {
	return nil, nil
}

func (r *archiveTestAgentSessionRepository) SearchHistory(context.Context, domain.SessionHistoryFilter) ([]domain.SessionHistoryEntry, error) {
	return nil, nil
}

func (r *archiveTestAgentSessionRepository) Create(_ context.Context, session domain.AgentSession) error {
	r.sessions[session.ID] = session
	return nil
}

func (r *archiveTestAgentSessionRepository) Update(_ context.Context, session domain.AgentSession) error {
	if err := r.updateErr[session.ID]; err != nil {
		return err
	}
	r.sessions[session.ID] = session
	return nil
}

func (r *archiveTestAgentSessionRepository) Delete(_ context.Context, id string) error {
	delete(r.sessions, id)
	return nil
}

type archiveTestAgentSession struct {
	id          string
	resumeInfo  map[string]string
	abortCalled bool
	done        chan struct{}
	events      chan adapter.AgentEvent
}

func (s *archiveTestAgentSession) ID() string { return s.id }
func (s *archiveTestAgentSession) Wait(context.Context) error { return nil }
func (s *archiveTestAgentSession) Done() <-chan struct{} { return s.done }
func (s *archiveTestAgentSession) Events() <-chan adapter.AgentEvent { return s.events }
func (s *archiveTestAgentSession) SendMessage(context.Context, string) error { return nil }
func (s *archiveTestAgentSession) Steer(context.Context, string) error { return nil }
func (s *archiveTestAgentSession) SendAnswer(context.Context, string) error { return nil }
func (s *archiveTestAgentSession) Abort(context.Context) error {
	s.abortCalled = true
	return nil
}
func (s *archiveTestAgentSession) ResumeInfo() map[string]string { return s.resumeInfo }
func (s *archiveTestAgentSession) Compact(context.Context) error { return nil }

type archiveTestSessionRegistry struct {
	sessions map[string]adapter.AgentSession
	aborted  map[string]bool
}

func (r *archiveTestSessionRegistry) Register(id string, session adapter.AgentSession) {
	r.sessions[id] = session
}
func (r *archiveTestSessionRegistry) Deregister(id string) { delete(r.sessions, id) }
func (r *archiveTestSessionRegistry) SendMessage(context.Context, string, string) error { return nil }
func (r *archiveTestSessionRegistry) Steer(context.Context, string, string) error { return nil }
func (r *archiveTestSessionRegistry) SendAnswer(context.Context, string, string) error { return nil }
func (r *archiveTestSessionRegistry) IsRunning(id string) bool {
	_, ok := r.sessions[id]
	return ok
}
func (r *archiveTestSessionRegistry) Registered(id string) (adapter.AgentSession, bool) {
	session, ok := r.sessions[id]
	return session, ok
}
func (r *archiveTestSessionRegistry) AbortAndDeregister(ctx context.Context, id string) {
	r.aborted[id] = true
	if session, ok := r.sessions[id]; ok {
		_ = session.Abort(ctx)
	}
	delete(r.sessions, id)
}
func (r *archiveTestSessionRegistry) RegisterForeman(string, *orchestrator.Foreman) {}
func (r *archiveTestSessionRegistry) GetForeman(string) *orchestrator.Foreman { return nil }
func (r *archiveTestSessionRegistry) DeregisterForeman(string) {}
func (r *archiveTestSessionRegistry) Close(context.Context) {}

func newArchiveTestClient(root domain.Session, agentSessions []domain.AgentSession, updateErr map[string]error, registry orchestrator.SessionRegistry) (*InProcessClient, *archiveTestSessionRepository, *archiveTestAgentSessionRepository) {
	agentRepo := &archiveTestAgentSessionRepository{
		sessions:  make(map[string]domain.AgentSession, len(agentSessions)),
		updateErr: updateErr,
	}
	for _, session := range agentSessions {
		agentRepo.sessions[session.ID] = session
	}
	sessionRepo := &archiveTestSessionRepository{
		items:     map[string]domain.Session{root.ID: root},
		agentRepo: agentRepo,
	}
	resources := repository.Resources{Sessions: sessionRepo, AgentSessions: agentRepo}
	client := NewInProcessClient(Dependencies{
		Sessions:      service.NewSessionService(repository.NoopTransacter{Res: resources}, archiveTestPublisher{}),
		AgentSessions: service.NewAgentSessionService(repository.NoopTransacter{Res: resources}, archiveTestPublisher{}),
		SessionRegistry: registry,
	})
	return client, sessionRepo, agentRepo
}

func TestInProcessClientArchiveSessionTerminatesChildrenBeforeArchive(t *testing.T) {
	workItemID := "work-item"
	pending := domain.AgentSession{ID: "pending", WorkItemID: workItemID, Status: domain.AgentSessionPending}
	running := domain.AgentSession{ID: "running", WorkItemID: workItemID, Status: domain.AgentSessionRunning}
	waiting := domain.AgentSession{ID: "waiting", WorkItemID: workItemID, Status: domain.AgentSessionWaitingForAnswer}
	completed := domain.AgentSession{ID: "completed", WorkItemID: workItemID, Status: domain.AgentSessionCompleted}
	failed := domain.AgentSession{ID: "failed", WorkItemID: workItemID, Status: domain.AgentSessionFailed}
	interrupted := domain.AgentSession{ID: "interrupted", WorkItemID: workItemID, Status: domain.AgentSessionInterrupted}
	terminal := []domain.AgentSession{completed, failed, interrupted}
	registry := &archiveTestSessionRegistry{
		sessions: map[string]adapter.AgentSession{
			running.ID: &archiveTestAgentSession{id: running.ID, resumeInfo: map[string]string{"resume": "running"}, done: make(chan struct{}), events: make(chan adapter.AgentEvent)},
		waiting.ID: &archiveTestAgentSession{id: waiting.ID, resumeInfo: map[string]string{"resume": "waiting"}, done: make(chan struct{}), events: make(chan adapter.AgentEvent)},
	},
	aborted: make(map[string]bool),
	}
	client, sessionRepo, agentRepo := newArchiveTestClient(
		domain.Session{ID: workItemID, State: domain.SessionImplementing, PreviousState: domain.SessionReviewing},
		append([]domain.AgentSession{pending, running, waiting}, terminal...),
		nil,
		registry,
	)

	result, err := client.ArchiveSession(context.Background(), workItemID)
	if err != nil {
		t.Fatalf("ArchiveSession failed: %v", err)
	}
	if result.Message != "Session archived" {
		t.Fatalf("message = %q, want Session archived", result.Message)
	}
	if !sessionRepo.archiveCalled {
		t.Fatal("expected work item archive")
	}
	if sessionRepo.archiveBeforeTermination {
		t.Fatal("work item archived before child termination")
	}
	if got := sessionRepo.items[workItemID]; got.State != domain.SessionArchived || got.PreviousState != domain.SessionImplementing {
		t.Fatalf("archived work item = %#v, want archived with previous implementing state", got)
	}

	if got := agentRepo.sessions[pending.ID].Status; got != domain.AgentSessionFailed {
		t.Errorf("pending status = %q, want failed", got)
	}
	for _, id := range []string{running.ID, waiting.ID} {
		if got := agentRepo.sessions[id].Status; got != domain.AgentSessionInterrupted {
			t.Errorf("%s status = %q, want interrupted", id, got)
		}
		if !registry.aborted[id] {
			t.Errorf("%s was not aborted in the session registry", id)
		}
		if got := agentRepo.sessions[id].ResumeInfo; !reflect.DeepEqual(got, map[string]string{"resume": id}) {
			t.Errorf("%s resume info = %#v, want persisted harness resume info", id, got)
		}
	}
	for _, before := range terminal {
		if got := agentRepo.sessions[before.ID]; !reflect.DeepEqual(got, before) {
			t.Errorf("terminal session %s changed: got %#v, want %#v", before.ID, got, before)
		}
	}
}

func TestInProcessClientArchiveSessionDoesNotArchiveWhenTerminationFails(t *testing.T) {
	workItemID := "work-item"
	terminationErr := errors.New("interrupt failed")
	running := domain.AgentSession{ID: "running", WorkItemID: workItemID, Status: domain.AgentSessionRunning}
	client, sessionRepo, agentRepo := newArchiveTestClient(
		domain.Session{ID: workItemID, State: domain.SessionImplementing},
		[]domain.AgentSession{running},
		map[string]error{running.ID: terminationErr},
		&archiveTestSessionRegistry{sessions: make(map[string]adapter.AgentSession), aborted: make(map[string]bool)},
	)

	_, err := client.ArchiveSession(context.Background(), workItemID)
	if err == nil {
		t.Fatal("expected termination error")
	}
	if !errors.Is(err, terminationErr) {
		t.Fatalf("error = %v, want chain containing %v", err, terminationErr)
	}
	if sessionRepo.archiveCalled {
		t.Fatal("work item archived despite termination failure")
	}
	if got := sessionRepo.items[workItemID].State; got != domain.SessionImplementing {
		t.Fatalf("work item state = %q, want implementing", got)
	}
	if got := agentRepo.sessions[running.ID].Status; got != domain.AgentSessionRunning {
		t.Fatalf("running status = %q, want running after failed termination", got)
	}
}
