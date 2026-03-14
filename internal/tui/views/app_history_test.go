package views

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/orchestrator"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
	"github.com/beeemT/substrate/internal/sessionlog"
)

type duplicateCreateWorkItemRepo struct {
	items     []domain.Session
	createErr error
	listErr   error
}

func (r *duplicateCreateWorkItemRepo) Get(_ context.Context, id string) (domain.Session, error) {
	for _, item := range r.items {
		if item.ID == id {
			return item, nil
		}
	}
	return domain.Session{}, repository.ErrNotFound
}

func (r *duplicateCreateWorkItemRepo) List(_ context.Context, filter repository.SessionFilter) ([]domain.Session, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	items := make([]domain.Session, 0, len(r.items))
	for _, item := range r.items {
		if filter.WorkspaceID != nil && item.WorkspaceID != *filter.WorkspaceID {
			continue
		}
		if filter.ExternalID != nil && item.ExternalID != *filter.ExternalID {
			continue
		}
		if filter.Source != nil && item.Source != *filter.Source {
			continue
		}
		items = append(items, item)
	}
	return items, nil
}

func (r *duplicateCreateWorkItemRepo) Create(context.Context, domain.Session) error {
	return r.createErr
}

func (r *duplicateCreateWorkItemRepo) Update(context.Context, domain.Session) error {
	return nil
}

func (r *duplicateCreateWorkItemRepo) Delete(_ context.Context, id string) error {
	filtered := r.items[:0]
	for _, item := range r.items {
		if item.ID != id {
			filtered = append(filtered, item)
		}
	}
	r.items = filtered
	return nil
}

type sessionSearchDeleteRepo struct {
	sessions map[string]domain.Task
	entry    domain.SessionHistoryEntry
}

func (r *sessionSearchDeleteRepo) Get(_ context.Context, id string) (domain.Task, error) {
	session, ok := r.sessions[id]
	if !ok {
		return domain.Task{}, repository.ErrNotFound
	}
	return session, nil
}

func (r *sessionSearchDeleteRepo) ListByWorkItemID(_ context.Context, workItemID string) ([]domain.Task, error) {
	result := make([]domain.Task, 0, len(r.sessions))
	for _, session := range r.sessions {
		if session.WorkItemID == workItemID {
			result = append(result, session)
		}
	}
	return result, nil
}

func (r *sessionSearchDeleteRepo) ListBySubPlanID(_ context.Context, subPlanID string) ([]domain.Task, error) {
	result := make([]domain.Task, 0, len(r.sessions))
	for _, session := range r.sessions {
		if session.SubPlanID == subPlanID {
			result = append(result, session)
		}
	}
	return result, nil
}

func (r *sessionSearchDeleteRepo) ListByWorkspaceID(_ context.Context, workspaceID string) ([]domain.Task, error) {
	result := make([]domain.Task, 0, len(r.sessions))
	for _, session := range r.sessions {
		if session.WorkspaceID == workspaceID {
			result = append(result, session)
		}
	}
	return result, nil
}

func (r *sessionSearchDeleteRepo) ListByOwnerInstanceID(_ context.Context, instanceID string) ([]domain.Task, error) {
	result := make([]domain.Task, 0, len(r.sessions))
	for _, session := range r.sessions {
		if session.OwnerInstanceID != nil && *session.OwnerInstanceID == instanceID {
			result = append(result, session)
		}
	}
	return result, nil
}

func (r *sessionSearchDeleteRepo) SearchHistory(_ context.Context, filter domain.SessionHistoryFilter) ([]domain.SessionHistoryEntry, error) {
	if _, ok := r.sessions[r.entry.SessionID]; !ok {
		return nil, nil
	}
	if filter.WorkspaceID != nil && r.entry.WorkspaceID != *filter.WorkspaceID {
		return nil, nil
	}
	return []domain.SessionHistoryEntry{r.entry}, nil
}

func (r *sessionSearchDeleteRepo) Create(_ context.Context, session domain.Task) error {
	r.sessions[session.ID] = session
	return nil
}

func (r *sessionSearchDeleteRepo) Update(_ context.Context, session domain.Task) error {
	r.sessions[session.ID] = session
	return nil
}

func (r *sessionSearchDeleteRepo) Delete(_ context.Context, id string) error {
	delete(r.sessions, id)
	return nil
}

func TestSessionSearchPollingRefreshStaysSilent(t *testing.T) {
	now := time.Now()
	app := NewApp(Services{Settings: &SettingsService{}})
	app.activeOverlay = overlaySessionSearch
	app.sessionSearch.Open(sessionHistoryScopeGlobal, false)
	app.sessionSearch.SetEntries([]domain.SessionHistoryEntry{{
		SessionID:          "sess-1",
		WorkspaceID:        "ws-1",
		WorkspaceName:      "workspace",
		WorkItemID:         "wi-1",
		WorkItemExternalID: "SUB-1",
		WorkItemTitle:      "Work item",
		UpdatedAt:          now,
		CreatedAt:          now,
	}})
	app.sessionSearch.SetLoading(false)

	model, cmd := app.Update(PollTickMsg{})
	if cmd == nil {
		t.Fatal("expected poll tick command batch")
	}
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}
	if updated.sessionSearch.loading {
		t.Fatal("expected background poll refresh to keep loading indicator hidden")
	}
	if view := updated.sessionSearch.View(); strings.Contains(view, "Searching…") {
		t.Fatalf("view = %q, want no searching indicator during background refresh", view)
	}

	model, cmd = updated.Update(SessionHistorySearchRequestedMsg{})
	if cmd == nil {
		t.Fatal("expected interactive search command")
	}
	updated, ok = model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}
	if !updated.sessionSearch.loading {
		t.Fatal("expected interactive search request to show loading indicator")
	}
}

func TestApp_OpenSessionSearchIncludesWorkItemWithoutAgentSessions(t *testing.T) {
	t.Parallel()

	now := time.Now()
	app := NewApp(Services{
		WorkspaceID:   "ws-local",
		WorkspaceName: "local",
		Settings:      &SettingsService{},
	})
	app.workItems = []domain.Session{{
		ID:          "wi-preplan",
		WorkspaceID: "ws-local",
		ExternalID:  "SUB-0",
		Title:       "New item awaiting planning",
		State:       domain.SessionIngested,
		CreatedAt:   now,
		UpdatedAt:   now,
	}}

	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model after resize = %T, want App", model)
	}
	model, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if cmd == nil {
		t.Fatal("expected session search command")
	}
	updated, ok = model.(App)
	if !ok {
		t.Fatalf("model after opening search = %T, want App", model)
	}
	sel := updated.sessionSearch.Selected()
	if sel == nil {
		t.Fatal("selected entry = nil, want pre-planning work item session")
	}
	if sel.WorkItemID != "wi-preplan" {
		t.Fatalf("selected work item = %q, want wi-preplan", sel.WorkItemID)
	}
	if sel.SessionID != "" {
		t.Fatalf("selected session id = %q, want empty for pre-planning session", sel.SessionID)
	}
	if sel.AgentSessionCount != 0 {
		t.Fatalf("agent session count = %d, want 0", sel.AgentSessionCount)
	}
	view := stripBrowseANSI(updated.sessionSearch.View())
	assertOverlayFits(t, view, 80, 20)
	for _, want := range []string{"SUB-0", "Ready to plan · local", "Search Sessions"} {
		if !strings.Contains(view, want) {
			t.Fatalf("session search view = %q, want %q", view, want)
		}
	}
}

func TestApp_OpenSessionSearchSeedsLocalAvailableSessions(t *testing.T) {
	t.Parallel()

	app := newSidebarDrilldownTestApp()
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model after resize = %T, want App", model)
	}

	model, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if cmd == nil {
		t.Fatal("expected session search command")
	}
	updated, ok = model.(App)
	if !ok {
		t.Fatalf("model after opening search = %T, want App", model)
	}
	if updated.activeOverlay != overlaySessionSearch {
		t.Fatalf("activeOverlay = %v, want %v", updated.activeOverlay, overlaySessionSearch)
	}
	sel := updated.sessionSearch.Selected()
	if sel == nil {
		t.Fatal("selected entry = nil, want seeded local session")
	}
	if sel.WorkItemID != "wi-1" || sel.SessionID != "sess-1" {
		t.Fatalf("selected entry = %#v, want local work item/session", sel)
	}
	view := stripBrowseANSI(updated.sessionSearch.View())
	assertOverlayFits(t, view, 80, 20)
	for _, want := range []string{"SUB-1", "Implementing · local", "Search Sessions"} {
		if !strings.Contains(view, want) {
			t.Fatalf("session search view = %q, want %q", view, want)
		}
	}
}

func TestApp_SessionHistoryLoadedKeepsSeededLocalSessionsWhenHistoryIsEmpty(t *testing.T) {
	t.Parallel()

	app := newSidebarDrilldownTestApp()
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model after resize = %T, want App", model)
	}
	model, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if cmd == nil {
		t.Fatal("expected session search command")
	}
	updated, ok = model.(App)
	if !ok {
		t.Fatalf("model after opening search = %T, want App", model)
	}

	filter := updated.sessionSearchFilter()
	model, _ = updated.Update(SessionHistoryLoadedMsg{Filter: filter, Entries: nil})
	updated, ok = model.(App)
	if !ok {
		t.Fatalf("model after empty history load = %T, want App", model)
	}
	if updated.sessionSearch.loading {
		t.Fatal("expected loading indicator to clear after history response")
	}
	sel := updated.sessionSearch.Selected()
	if sel == nil {
		t.Fatal("selected entry = nil, want seeded local session retained")
	}
	if sel.WorkItemID != "wi-1" || sel.SessionID != "sess-1" {
		t.Fatalf("selected entry after empty history load = %#v, want local work item/session", sel)
	}
	view := stripBrowseANSI(updated.sessionSearch.View())
	assertOverlayFits(t, view, 80, 20)
	if strings.Contains(view, "No sessions found") {
		t.Fatalf("session search view = %q, want local seeded session instead of empty state", view)
	}
}

func TestApp_SessionSearchDeleteRemovesSessionAndLogs(t *testing.T) {
	t.Parallel()

	now := time.Now()
	repo := &sessionSearchDeleteRepo{
		sessions: map[string]domain.Task{
			"sess-1": {ID: "sess-1", WorkItemID: "wi-1", WorkspaceID: "ws-1", Phase: domain.TaskPhaseImplementation, SubPlanID: "sp-1", RepositoryName: "repo-a", Status: domain.AgentSessionCompleted, UpdatedAt: now, CreatedAt: now},
		},
		entry: domain.SessionHistoryEntry{
			SessionID:          "sess-1",
			WorkspaceID:        "ws-1",
			WorkspaceName:      "workspace",
			WorkItemID:         "wi-1",
			WorkItemExternalID: "SUB-1",
			WorkItemTitle:      "Work item",
			UpdatedAt:          now,
			CreatedAt:          now,
		},
	}
	workItemRepo := &duplicateCreateWorkItemRepo{items: []domain.Session{{ID: "wi-1", WorkspaceID: "ws-1", ExternalID: "SUB-1", Title: "Work item", State: domain.SessionImplementing}}}
	planSvc := service.NewPlanService(&cmdPlanRepo{plans: map[string]domain.Plan{"plan-1": {ID: "plan-1", WorkItemID: "wi-1"}}}, &cmdSubPlanRepo{subPlans: map[string]domain.TaskPlan{"sp-1": {ID: "sp-1", PlanID: "plan-1", RepositoryName: "repo-a"}}})
	app := NewApp(Services{
		Task:          service.NewTaskService(repo),
		Session:       service.NewSessionService(workItemRepo),
		Plan:          planSvc,
		WorkspaceID:   "ws-1",
		WorkspaceName: "workspace",
		Settings:      &SettingsService{},
	})
	tempDir := t.TempDir()
	for _, path := range []string{
		filepath.Join(tempDir, "sess-1.log"),
		filepath.Join(tempDir, "sess-1.log.1.gz"),
		filepath.Join(tempDir, "review-1.log"),
	} {
		if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	app.sessionsDir = tempDir
	app.reviewSessionLogs["sess-1"] = filepath.Join(tempDir, "review-1.log")
	app.sessions = []domain.Task{repo.sessions["sess-1"]}
	app.activeOverlay = overlaySessionSearch
	app.sessionSearch.Open(sessionHistoryScopeWorkspace, true)
	app.sessionSearch.SetEntries([]domain.SessionHistoryEntry{repo.entry})
	app.sessionSearch.SetLoading(false)
	app.sessionSearch.SetEntries([]domain.SessionHistoryEntry{repo.entry})
	app.sessionSearch.SetLoading(false)

	model, _ := app.Update(tea.WindowSizeMsg{Width: 72, Height: 18})
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}

	model, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	if cmd != nil {
		t.Fatalf("unexpected command moving focus: %v", cmd)
	}
	updated, ok = model.(App)
	if !ok {
		t.Fatalf("model after down = %T, want App", model)
	}

	model, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if cmd == nil {
		t.Fatal("expected delete confirmation command")
	}
	updated, ok = model.(App)
	if !ok {
		t.Fatalf("model after delete key = %T, want App", model)
	}
	confirmMsg, ok := cmd().(ConfirmDeleteSessionMsg)
	if !ok {
		t.Fatalf("cmd() message = %T, want ConfirmDeleteSessionMsg", cmd())
	}
	if confirmMsg.SessionID != "wi-1" {
		t.Fatalf("confirm session id = %q, want wi-1", confirmMsg.SessionID)
	}

	model, cmd = updated.Update(confirmMsg)
	if cmd != nil {
		t.Fatalf("unexpected command when showing confirm: %v", cmd)
	}
	updated, ok = model.(App)
	if !ok {
		t.Fatalf("model after confirm msg = %T, want App", model)
	}
	if !updated.confirmActive {
		t.Fatal("expected confirm modal to be active")
	}
	confirmView := stripBrowseANSI(updated.confirm.View())
	assertOverlayFits(t, confirmView, 72, 18)
	for _, want := range []string{"Delete Session", "full session", "[y]", "[n]"} {
		if !strings.Contains(confirmView, want) {
			t.Fatalf("confirm view = %q, want %q", confirmView, want)
		}
	}

	model, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("expected delete command after confirming")
	}
	updated, ok = model.(App)
	if !ok {
		t.Fatalf("model after confirm key = %T, want App", model)
	}
	if updated.confirmActive {
		t.Fatal("expected confirm modal to close after confirmation")
	}

	updated = applyAppCmds(t, updated, cmd)
	if updated.activeOverlay != overlaySessionSearch {
		t.Fatalf("activeOverlay = %v, want session search", updated.activeOverlay)
	}
	if len(updated.sessions) != 0 {
		t.Fatalf("sessions len = %d, want 0", len(updated.sessions))
	}
	if sel := updated.sessionSearch.Selected(); sel != nil {
		t.Fatalf("selected entry = %#v, want nil after deleting the full session", sel)
	}
	if len(updated.workItems) != 0 {
		t.Fatalf("work items len = %d, want 0", len(updated.workItems))
	}
	if _, ok := repo.sessions["sess-1"]; ok {
		t.Fatal("expected session repo entry to be deleted")
	}
	if _, ok := updated.reviewSessionLogs["sess-1"]; ok {
		t.Fatal("expected review log mapping to be removed")
	}
	for _, path := range []string{
		filepath.Join(tempDir, "sess-1.log"),
		filepath.Join(tempDir, "sess-1.log.1.gz"),
		filepath.Join(tempDir, "review-1.log"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be removed, stat err = %v", path, err)
		}
	}
	searchView := stripBrowseANSI(updated.sessionSearch.View())
	if !strings.Contains(searchView, "No sessions found") {
		t.Fatalf("search view = %q, want empty state after deleting the full session", searchView)
	}
}

func TestDeleteSessionCmd_ReturnsSuccessWithCleanupWarning(t *testing.T) {
	t.Parallel()

	now := time.Now()
	taskRepo := &sessionSearchDeleteRepo{
		sessions: map[string]domain.Task{
			"sess-1": {ID: "sess-1", WorkItemID: "wi-1", WorkspaceID: "ws-1", Phase: domain.TaskPhaseImplementation, SubPlanID: "sp-1", RepositoryName: "repo-a", Status: domain.AgentSessionCompleted, UpdatedAt: now, CreatedAt: now},
		},
	}
	workItemRepo := &duplicateCreateWorkItemRepo{items: []domain.Session{{ID: "wi-1", WorkspaceID: "ws-1", ExternalID: "SUB-1", Title: "Work item", State: domain.SessionImplementing}}}
	planSvc := service.NewPlanService(&cmdPlanRepo{plans: map[string]domain.Plan{"plan-1": {ID: "plan-1", WorkItemID: "wi-1"}}}, &cmdSubPlanRepo{subPlans: map[string]domain.TaskPlan{"sp-1": {ID: "sp-1", PlanID: "plan-1", RepositoryName: "repo-a"}}})
	sessionsDir := filepath.Join(t.TempDir(), "[")

	msg := deleteSessionCmd(Services{
		Task:    service.NewTaskService(taskRepo),
		Session: service.NewSessionService(workItemRepo),
		Plan:    planSvc,
	}, sessionsDir, "wi-1", map[string]string{"sess-1": filepath.Join(sessionsDir, "review-1.log")})()
	deleted, ok := msg.(SessionDeletedMsg)
	if !ok {
		t.Fatalf("deleteSessionCmd() message = %T, want SessionDeletedMsg", msg)
	}
	if deleted.Message != "Session deleted" {
		t.Fatalf("deleted message = %q, want Session deleted", deleted.Message)
	}
	if deleted.Warning == "" {
		t.Fatal("expected cleanup warning when artifact removal fails after delete")
	}
	if !strings.Contains(deleted.Warning, "could not be removed") {
		t.Fatalf("deleted warning = %q, want cleanup failure context", deleted.Warning)
	}
	if _, ok := taskRepo.sessions["sess-1"]; ok {
		t.Fatal("expected task repo entry to be deleted despite cleanup warning")
	}
	if len(workItemRepo.items) != 0 {
		t.Fatalf("work item repo items = %d, want 0 after successful delete", len(workItemRepo.items))
	}

	model, cmd := NewApp(Services{Settings: &SettingsService{}}).Update(deleted)
	if cmd != nil {
		t.Fatalf("unexpected follow-up command for warning toast: %v", cmd)
	}
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model after delete warning = %T, want App", model)
	}
	toastView := stripToastANSI(updated.toasts.StackView())
	for _, want := range []string{"Session deleted", deleted.Warning} {
		if !strings.Contains(toastView, want) {
			t.Fatalf("toast view = %q, want %q", toastView, want)
		}
	}
}

func TestLoadHistoryEntry_LocalWorkspaceUsesWorkItemContent(t *testing.T) {
	app := NewApp(Services{
		WorkspaceID:   "ws-local",
		WorkspaceName: "local",
		Settings:      &SettingsService{},
	})
	app.content.SetSize(80, 20)
	app.workItems = []domain.Session{{
		ID:          "wi-1",
		ExternalID:  "SUB-1",
		Source:      "github",
		Title:       "Local item",
		Description: "## Summary\n\nThis is **important**.",
		Labels:      []string{"bug", "backend"},
		Metadata: map[string]any{
			"tracker_refs": []domain.TrackerReference{{Provider: "github", Kind: "issue", Owner: "acme", Repo: "rocket", Number: 42}},
		},
		State: domain.SessionIngested,
	}}

	cmd := app.loadHistoryEntry(SidebarEntry{
		Kind:        SidebarEntrySessionHistory,
		WorkItemID:  "wi-1",
		SessionID:   "sess-local",
		WorkspaceID: "ws-local",
		ExternalID:  "SUB-1",
		Title:       "Local item",
	})

	if cmd != nil {
		t.Fatalf("loadHistoryEntry() cmd = %v, want nil for local workspace entry", cmd)
	}
	if app.currentWorkItemID != "wi-1" {
		t.Fatalf("currentWorkItemID = %q, want wi-1", app.currentWorkItemID)
	}
	if app.currentHistorySessionID != "" {
		t.Fatalf("currentHistorySessionID = %q, want empty", app.currentHistorySessionID)
	}
	if app.content.Mode() != ContentModeOverview {
		t.Fatalf("content mode = %v, want %v", app.content.Mode(), ContentModeOverview)
	}

	app.content.SetSize(100, 40)
	view := stripBrowseANSI(app.content.View())
	for _, want := range []string{"SUB-1 · Local item", "Summary", "Source", "Provider: GitHub", "Ref: acme/rocket#42", "This is important."} {
		if !strings.Contains(view, want) {
			t.Fatalf("content view = %q, want %q", view, want)
		}
	}
	for _, raw := range []string{"## Summary", "**important**"} {
		if strings.Contains(view, raw) {
			t.Fatalf("content view = %q, must not contain raw markdown token %q", view, raw)
		}
	}
}

func TestLoadHistoryEntry_RemoteWorkspaceUsesSessionInteraction(t *testing.T) {
	app := NewApp(Services{
		WorkspaceID:   "ws-local",
		WorkspaceName: "local",
		Settings:      &SettingsService{},
	})

	cmd := app.loadHistoryEntry(SidebarEntry{
		Kind:          SidebarEntrySessionHistory,
		SessionID:     "sess-remote",
		WorkspaceID:   "ws-remote",
		WorkspaceName: "remote",
		ExternalID:    "SUB-2",
		Title:         "Remote item",
	})
	if cmd == nil {
		t.Fatal("loadHistoryEntry() cmd = nil, want interaction load command")
	}
	if app.currentWorkItemID != "" {
		t.Fatalf("currentWorkItemID = %q, want empty", app.currentWorkItemID)
	}
	if app.currentHistorySessionID != "sess-remote" {
		t.Fatalf("currentHistorySessionID = %q, want sess-remote", app.currentHistorySessionID)
	}
	if app.content.Mode() != ContentModeSessionInteraction {
		t.Fatalf("content mode = %v, want %v", app.content.Mode(), ContentModeSessionInteraction)
	}

	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}
	if view := stripToastANSI(updated.View()); !strings.Contains(view, "Read only") {
		t.Fatalf("view = %q, want persistent read only toast", view)
	}

	msg := cmd()
	loaded, ok := msg.(SessionInteractionLoadedMsg)
	if !ok {
		t.Fatalf("cmd() message = %T, want SessionInteractionLoadedMsg", msg)
	}
	if loaded.SessionID != "sess-remote" {
		t.Fatalf("loaded session id = %q, want sess-remote", loaded.SessionID)
	}
}

func TestLoadHistoryEntry_RemoteWorkspaceWithoutAgentSessionShowsSummary(t *testing.T) {
	app := NewApp(Services{
		WorkspaceID:   "ws-local",
		WorkspaceName: "local",
		Settings:      &SettingsService{},
	})
	app.content.SetSize(80, 20)

	cmd := app.loadHistoryEntry(SidebarEntry{
		Kind:          SidebarEntrySessionHistory,
		WorkspaceID:   "ws-remote",
		WorkspaceName: "remote",
		WorkItemID:    "wi-remote",
		ExternalID:    "SUB-3",
		Title:         "Remote planning item",
		State:         domain.SessionPlanning,
	})
	if cmd != nil {
		t.Fatalf("loadHistoryEntry() cmd = %v, want nil when no agent session exists", cmd)
	}
	if app.currentHistorySessionID != "" {
		t.Fatalf("currentHistorySessionID = %q, want empty", app.currentHistorySessionID)
	}
	if app.content.Mode() != ContentModeSessionInteraction {
		t.Fatalf("content mode = %v, want %v", app.content.Mode(), ContentModeSessionInteraction)
	}
	if view := app.content.View(); !strings.Contains(view, "No agent-session log is available") {
		t.Fatalf("content view = %q, want summary fallback", view)
	}
}

func TestSessionDeletedMsg_ClearsOpenRemoteHistoryEntry(t *testing.T) {
	t.Parallel()

	app := NewApp(Services{Settings: &SettingsService{}})
	app.content.SetSize(80, 20)

	cmd := app.loadHistoryEntry(SidebarEntry{
		Kind:          SidebarEntrySessionHistory,
		WorkspaceID:   "ws-remote",
		WorkspaceName: "remote",
		WorkItemID:    "wi-remote",
		ExternalID:    "SUB-3",
		Title:         "Remote planning item",
		State:         domain.SessionPlanning,
	})
	if cmd != nil {
		t.Fatalf("loadHistoryEntry() cmd = %v, want nil when no agent session exists", cmd)
	}
	if view := app.content.View(); !strings.Contains(view, "No agent-session log is available") {
		t.Fatalf("content view before delete = %q, want remote summary", view)
	}

	model, cmd := app.Update(SessionDeletedMsg{SessionID: "wi-remote", Message: "Session deleted"})
	if cmd != nil {
		t.Fatalf("unexpected command clearing remote history entry: %v", cmd)
	}
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model after remote delete = %T, want App", model)
	}
	if updated.currentWorkItemID != "" {
		t.Fatalf("currentWorkItemID = %q, want empty", updated.currentWorkItemID)
	}
	if updated.currentHistorySessionID != "" {
		t.Fatalf("currentHistorySessionID = %q, want empty", updated.currentHistorySessionID)
	}
	if updated.currentHistoryEntry != (SidebarEntry{}) {
		t.Fatalf("currentHistoryEntry = %#v, want empty", updated.currentHistoryEntry)
	}
	if updated.content.Mode() != ContentModeEmpty {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModeEmpty)
	}
	if view := updated.content.View(); strings.Contains(view, "Remote planning item") || strings.Contains(view, "No agent-session log is available") {
		t.Fatalf("content view after delete = %q, want remote history content cleared", view)
	}
}

func TestRebuildSidebarLeavesSessionsUnselectedUntilNavigation(t *testing.T) {
	now := time.Now()
	older := now.Add(-2 * time.Hour)
	newer := now.Add(-15 * time.Minute)

	app := NewApp(Services{
		WorkspaceID:   "ws-local",
		WorkspaceName: "local",
		Settings:      &SettingsService{},
	})
	app.workItems = []domain.Session{
		{ID: "wi-old", ExternalID: "SUB-1", Title: "Old", State: domain.SessionIngested, CreatedAt: older, UpdatedAt: older},
		{ID: "wi-new", ExternalID: "SUB-2", Title: "New", State: domain.SessionIngested, CreatedAt: older, UpdatedAt: newer},
	}

	app.rebuildSidebar()

	if sel := app.sidebar.Selected(); sel != nil {
		t.Fatalf("selected sidebar entry = %v, want nil before navigation", sel)
	}
	if app.currentWorkItemID != "" {
		t.Fatalf("currentWorkItemID = %q, want empty before navigation", app.currentWorkItemID)
	}

	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}
	if cmd != nil {
		t.Fatalf("cmd = %v, want nil for ready-to-plan selection", cmd)
	}
	sel := updated.sidebar.Selected()
	if sel == nil || sel.WorkItemID != "wi-new" {
		t.Fatalf("selected work item after first MoveDown = %v, want wi-new", sel)
	}
	if updated.currentWorkItemID != "wi-new" {
		t.Fatalf("currentWorkItemID after first MoveDown = %q, want wi-new", updated.currentWorkItemID)
	}
	if updated.content.Mode() != ContentModeOverview {
		t.Fatalf("content mode after first MoveDown = %v, want %v", updated.content.Mode(), ContentModeOverview)
	}

	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)
	sel = updated.sidebar.Selected()
	if sel == nil || sel.WorkItemID != "wi-old" {
		t.Fatalf("selected work item after second MoveDown = %v, want wi-old", sel)
	}
}

func TestWorkItemCreatedMsgUpdatesSidebarImmediately(t *testing.T) {
	now := time.Now()
	older := now.Add(-time.Hour)
	app := NewApp(Services{
		WorkspaceID:   "ws-local",
		WorkspaceName: "local",
		Settings:      &SettingsService{},
	})
	app.workItems = []domain.Session{{
		ID:          "wi-old",
		WorkspaceID: "ws-local",
		ExternalID:  "SUB-1",
		Title:       "Old item",
		State:       domain.SessionIngested,
		CreatedAt:   older,
		UpdatedAt:   older,
	}}
	app.rebuildSidebar()

	model, _ := app.Update(SessionCreatedMsg{
		Session: domain.Session{
			ID:          "wi-new",
			WorkspaceID: "ws-local",
			ExternalID:  "SUB-2",
			Title:       "New item",
			State:       domain.SessionIngested,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		Message: "Work item created: SUB-2",
	})
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}
	if updated.currentWorkItemID != "wi-new" {
		t.Fatalf("currentWorkItemID = %q, want wi-new", updated.currentWorkItemID)
	}
	if updated.content.Mode() != ContentModeOverview {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModeOverview)
	}
	sel := updated.sidebar.Selected()
	if sel == nil || sel.WorkItemID != "wi-new" {
		t.Fatalf("selected sidebar entry = %v, want wi-new", sel)
	}
	if len(updated.workItems) != 2 {
		t.Fatalf("work item count = %d, want 2", len(updated.workItems))
	}
}

func TestPersistCreatedWorkItemMsgDuplicateReturnsPrompt(t *testing.T) {
	existing := domain.Session{
		ID:          "wi-existing",
		WorkspaceID: "ws-local",
		ExternalID:  "SUB-1",
		Title:       "Existing item",
		State:       domain.SessionIngested,
	}
	repo := &duplicateCreateWorkItemRepo{
		items:     []domain.Session{existing},
		createErr: service.ErrAlreadyExists{Entity: "work item", ID: existing.ExternalID},
	}

	requested := domain.Session{
		ID:         "wi-new",
		ExternalID: existing.ExternalID,
		Title:      "Duplicate item",
	}
	msg := persistCreatedWorkItemMsg(Services{
		WorkspaceID: "ws-local",
		Session:     service.NewSessionService(repo),
	}, requested)

	dup, ok := msg.(SessionDuplicatePromptMsg)
	if !ok {
		t.Fatalf("msg = %T, want SessionDuplicatePromptMsg", msg)
	}
	if dup.ExistingSession.ID != existing.ID {
		t.Fatalf("existing session id = %q, want %q", dup.ExistingSession.ID, existing.ID)
	}
	if dup.RequestedSession.ID != requested.ID {
		t.Fatalf("requested session id = %q, want %q", dup.RequestedSession.ID, requested.ID)
	}
}

func TestPersistCreatedWorkItemMsgAggregateDuplicateReturnsPrompt(t *testing.T) {
	existing := domain.Session{
		ID:            "wi-existing",
		WorkspaceID:   "ws-local",
		ExternalID:    "SUB-42",
		Title:         "Existing issue",
		Source:        "github",
		SourceScope:   domain.ScopeIssues,
		SourceItemIDs: []string{"acme/rocket#42"},
		State:         domain.SessionIngested,
	}
	repo := &duplicateCreateWorkItemRepo{
		items:     []domain.Session{existing},
		createErr: service.ErrAlreadyExists{Entity: "work item", ID: existing.ExternalID},
	}

	requested := domain.Session{
		ID:            "wi-aggregate",
		ExternalID:    "SUB-7",
		Title:         "Issue 7 (+1 more)",
		Source:        "github",
		SourceScope:   domain.ScopeIssues,
		SourceItemIDs: []string{"acme/rocket#7", "acme/rocket#42"},
	}
	msg := persistCreatedWorkItemMsg(Services{
		WorkspaceID: "ws-local",
		Session:     service.NewSessionService(repo),
	}, requested)

	dup, ok := msg.(SessionDuplicatePromptMsg)
	if !ok {
		t.Fatalf("msg = %T, want SessionDuplicatePromptMsg", msg)
	}
	if dup.ExistingSession.ID != existing.ID {
		t.Fatalf("existing session id = %q, want %q", dup.ExistingSession.ID, existing.ID)
	}
	if dup.RequestedSession.ID != requested.ID {
		t.Fatalf("requested session id = %q, want %q", dup.RequestedSession.ID, requested.ID)
	}
}

func newDuplicatePromptTestApp() (App, domain.Session, domain.Session, domain.Session) {
	now := time.Now()
	older := now.Add(-time.Hour)
	existing := domain.Session{
		ID:          "wi-existing",
		WorkspaceID: "ws-local",
		ExternalID:  "SUB-1",
		Source:      "github",
		Title:       "Existing item",
		Description: "## Summary\n\nThis is **important**.",
		Labels:      []string{"bug", "backend"},
		Metadata: map[string]any{
			"tracker_refs": []domain.TrackerReference{{Provider: "github", Kind: "issue", Owner: "acme", Repo: "rocket", Number: 42}},
		},
		State:     domain.SessionIngested,
		CreatedAt: older,
		UpdatedAt: older,
	}
	other := domain.Session{
		ID:          "wi-other",
		WorkspaceID: "ws-local",
		ExternalID:  "SUB-2",
		Title:       "Other item",
		State:       domain.SessionIngested,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	requested := domain.Session{
		ID:         "wi-requested",
		ExternalID: "SUB-99",
		Title:      "Requested item",
	}
	app := NewApp(Services{
		WorkspaceID:   "ws-local",
		WorkspaceName: "local",
		Settings:      &SettingsService{},
		Planning:      new(orchestrator.PlanningService),
	})
	app.content.SetSize(80, 20)
	app.workItems = []domain.Session{existing, other}
	app.currentWorkItemID = other.ID
	app.rebuildSidebar()
	app.sidebar.SelectWorkItem(other.ID)
	_ = app.updateContentFromState()
	return app, existing, other, requested
}

func TestWorkItemDuplicatePromptShowsDecisionDialog(t *testing.T) {
	app, existing, other, requested := newDuplicatePromptTestApp()

	model, cmd := app.Update(SessionDuplicatePromptMsg{
		RequestedSession: requested,
		ExistingSession:  existing,
	})
	if cmd != nil {
		t.Fatal("expected duplicate prompt to show dialog without command")
	}
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}
	if !updated.duplicateSessionActive {
		t.Fatal("expected duplicate-session dialog to be active")
	}
	if updated.currentWorkItemID != other.ID {
		t.Fatalf("currentWorkItemID = %q, want %q before resolution", updated.currentWorkItemID, other.ID)
	}
	view := stripBrowseANSI(updated.duplicateSessionDialogView())
	for _, want := range []string{"Session already exists", "Existing session: SUB-1 · Existing item", "Requested selection: SUB-99 · Requested item", "Go to existing session", "Start planning with existing session"} {
		if !strings.Contains(view, want) {
			t.Fatalf("dialog view = %q, want %q", view, want)
		}
	}
}

func TestWorkItemDuplicateOpenExistingChoiceFocusesExistingWorkItemOverview(t *testing.T) {
	app, existing, _, requested := newDuplicatePromptTestApp()

	model, _ := app.Update(SessionDuplicatePromptMsg{
		RequestedSession: requested,
		ExistingSession:  existing,
	})
	updated := model.(App)
	model, cmd := updated.Update(SessionDuplicateActionMsg{Action: SessionDuplicateOpenExisting})
	if cmd != nil {
		t.Fatal("expected opening existing work item to avoid auto-starting planning")
	}
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}
	if updated.duplicateSessionActive {
		t.Fatal("expected duplicate-session dialog to close after opening existing work item")
	}
	if updated.currentWorkItemID != existing.ID {
		t.Fatalf("currentWorkItemID = %q, want %q", updated.currentWorkItemID, existing.ID)
	}
	if updated.content.Mode() != ContentModeOverview {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModeOverview)
	}
	sel := updated.sidebar.Selected()
	if sel == nil || sel.WorkItemID != existing.ID {
		t.Fatalf("selected sidebar entry = %v, want %q", sel, existing.ID)
	}
	toastView := stripBrowseANSI(updated.toasts.View())
	if !strings.Contains(toastView, "ℹ Opened existing item SUB-1") {
		t.Fatalf("toast view = %q", toastView)
	}
	updated.content.SetSize(100, 40)
	view := stripBrowseANSI(updated.content.View())
	for _, want := range []string{"SUB-1 · Existing item", "Summary", "Source", "Provider: GitHub", "Ref: acme/rocket#42", "This is important."} {
		if !strings.Contains(view, want) {
			t.Fatalf("content view = %q, want %q", view, want)
		}
	}
}

func TestWorkItemDuplicateCreateSessionChoiceStartsPlanningWithExistingWorkItem(t *testing.T) {
	app, existing, _, requested := newDuplicatePromptTestApp()

	model, _ := app.Update(SessionDuplicatePromptMsg{
		RequestedSession: requested,
		ExistingSession:  existing,
	})
	updated := model.(App)

	model, cmd := updated.Update(SessionDuplicateActionMsg{Action: SessionDuplicateCreateSession})
	if cmd == nil {
		t.Fatal("expected planning command for duplicate-session start")
	}
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}
	if updated.duplicateSessionActive {
		t.Fatal("expected duplicate-session dialog to close after starting planning")
	}
	if updated.currentWorkItemID != existing.ID {
		t.Fatalf("currentWorkItemID = %q, want %q", updated.currentWorkItemID, existing.ID)
	}
	if got := updated.workItemByID(existing.ID); got == nil {
		t.Fatalf("expected session %q to remain present", existing.ID)
	} else if got.State != domain.SessionPlanning {
		t.Fatalf("session state = %v, want %v", got.State, domain.SessionPlanning)
	}
	if updated.content.Mode() != ContentModeOverview {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModeOverview)
	}
	view := stripBrowseANSI(updated.content.View())
	for _, want := range []string{"Planning...", "Summary", "Source"} {
		if !strings.Contains(view, want) {
			t.Fatalf("content view = %q, want %q", view, want)
		}
	}
	if strings.Contains(view, "Waiting for live planning transcript...") {
		t.Fatalf("content view = %q, want overview planning snapshot instead of transcript placeholder", view)
	}
	toastView := stripBrowseANSI(updated.toasts.View())
	if !strings.Contains(toastView, "ℹ Starting planning with existing item SUB-1") {
		t.Fatalf("toast view = %q", toastView)
	}
}

func TestWorkItemDuplicateCancelChoiceKeepsCurrentSelection(t *testing.T) {
	app, existing, other, requested := newDuplicatePromptTestApp()

	model, _ := app.Update(SessionDuplicatePromptMsg{
		RequestedSession: requested,
		ExistingSession:  existing,
	})
	updated := model.(App)
	model, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}
	if cmd == nil {
		t.Fatal("expected escape to resolve the duplicate-session dialog")
	}
	updated = applyAppCmds(t, updated, cmd)
	if updated.duplicateSessionActive {
		t.Fatal("expected duplicate-session dialog to close after cancel")
	}
	if updated.currentWorkItemID != other.ID {
		t.Fatalf("currentWorkItemID = %q, want %q after cancel", updated.currentWorkItemID, other.ID)
	}
	toastView := stripBrowseANSI(updated.toasts.View())
	if strings.Contains(toastView, "Opened existing item") || strings.Contains(toastView, "Starting planning with existing item") {
		t.Fatalf("toast view = %q, want no duplicate-action toast after cancel", toastView)
	}
}

func TestWorkItemCreatedMsgAutoStartsPlanningWhenConfigured(t *testing.T) {
	now := time.Now()
	app := NewApp(Services{
		WorkspaceID:   "ws-local",
		WorkspaceName: "local",
		Settings:      &SettingsService{},
		Planning:      &orchestrator.PlanningService{},
	})

	model, cmd := app.Update(SessionCreatedMsg{
		Session: domain.Session{
			ID:          "wi-new",
			WorkspaceID: "ws-local",
			ExternalID:  "SUB-2",
			Title:       "New item",
			State:       domain.SessionIngested,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		Message: "Session created: SUB-2",
	})
	if cmd == nil {
		t.Fatal("expected planning command after work item creation")
	}
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}
	if updated.content.Mode() != ContentModeOverview {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModeOverview)
	}
	if len(updated.workItems) != 1 || updated.workItems[0].State != domain.SessionPlanning {
		t.Fatalf("work items = %#v, want one planning work item", updated.workItems)
	}
}

func newSidebarDrilldownTestApp() App {
	now := time.Now()
	app := NewApp(Services{
		WorkspaceID:   "ws-local",
		WorkspaceName: "local",
		Settings:      &SettingsService{},
	})
	workItem := domain.Session{
		ID:            "wi-1",
		WorkspaceID:   "ws-local",
		ExternalID:    "SUB-1",
		Source:        "github",
		SourceScope:   domain.ScopeIssues,
		SourceItemIDs: []string{"acme/rocket#42", "acme/rocket#43"},
		Title:         "Work item",
		Description:   "## Work item plan\n\nCombine auth and billing fixes into one coordinated rollout.",
		Labels:        []string{"bug", "backend"},
		Metadata: map[string]any{
			"tracker_refs": []domain.TrackerReference{
				{Provider: "github", Kind: "issue", Owner: "acme", Repo: "rocket", Number: 42},
				{Provider: "github", Kind: "issue", Owner: "acme", Repo: "rocket", Number: 43},
			},
			"source_summaries": []domain.SourceSummary{{
				Provider:    "github",
				Kind:        "issue",
				Ref:         "acme/rocket#42",
				Title:       "Fix auth",
				Description: "Investigate auth timeouts in the login flow.",
				Excerpt:     "Investigate auth timeouts in the login flow.",
				State:       "open",
				Labels:      []string{"bug", "backend"},
				Container:   "acme/rocket",
				URL:         "https://github.com/acme/rocket/issues/42",
			}, {
				Provider:    "github",
				Kind:        "issue",
				Ref:         "acme/rocket#43",
				Title:       "Repair billing",
				Description: "Stabilize billing retries and duplicate charge handling.",
				Excerpt:     "Stabilize billing retries and duplicate charge handling.",
				State:       "open",
				Labels:      []string{"payments"},
				Container:   "acme/rocket",
				URL:         "https://github.com/acme/rocket/issues/43",
			}},
		},
		State:     domain.SessionImplementing,
		CreatedAt: now,
		UpdatedAt: now,
	}
	app.workItems = []domain.Session{workItem}
	plan := &domain.Plan{ID: "plan-1", WorkItemID: workItem.ID}
	subPlan := domain.TaskPlan{
		ID:             "sp-1",
		PlanID:         plan.ID,
		RepositoryName: "repo-a",
		Status:         domain.SubPlanInProgress,
		UpdatedAt:      now,
	}
	session := domain.Task{
		ID:             "sess-1",
		WorkItemID:     workItem.ID,
		WorkspaceID:    "ws-local",
		Phase:          domain.TaskPhaseImplementation,
		SubPlanID:      subPlan.ID,
		RepositoryName: subPlan.RepositoryName,
		HarnessName:    "omp",
		Status:         domain.AgentSessionRunning,
		UpdatedAt:      now,
	}
	app.plans[workItem.ID] = plan
	app.subPlans[plan.ID] = []domain.TaskPlan{subPlan}
	app.sessions = []domain.Task{session}
	app.content.SetSize(80, 60)
	app.rebuildSidebar()
	app.currentWorkItemID = workItem.ID
	app.sidebar.SelectWorkItem(workItem.ID)
	_ = app.updateContentFromState()
	return app
}

func newPlanningDrilldownTestApp() App {
	now := time.Now()
	app := NewApp(Services{
		WorkspaceID:   "ws-local",
		WorkspaceName: "local",
		Settings:      &SettingsService{},
	})
	workItem := domain.Session{
		ID:            "wi-plan",
		WorkspaceID:   "ws-local",
		ExternalID:    "SUB-PLAN",
		Source:        "github",
		SourceScope:   domain.ScopeIssues,
		SourceItemIDs: []string{"acme/rocket#99"},
		Title:         "Plan this work",
		Metadata: map[string]any{
			"source_summaries": []domain.SourceSummary{{
				Provider:    "github",
				Kind:        "issue",
				Ref:         "acme/rocket#99",
				Title:       "Planning issue",
				Description: "## Goal\n\nPlan the migration carefully before coding.",
				Excerpt:     "Plan the migration carefully before coding.",
				State:       "open",
				Labels:      []string{"planning"},
				Container:   "acme/rocket",
				URL:         "https://github.com/acme/rocket/issues/99",
			}},
		},
		State:     domain.SessionPlanning,
		CreatedAt: now,
		UpdatedAt: now,
	}
	planningSession := domain.Task{
		ID:          "plan-sess-1",
		WorkItemID:  workItem.ID,
		WorkspaceID: workItem.WorkspaceID,
		Phase:       domain.TaskPhasePlanning,
		HarnessName: "omp",
		Status:      domain.AgentSessionRunning,
		UpdatedAt:   now,
		CreatedAt:   now,
	}
	app.workItems = []domain.Session{workItem}
	app.sessions = []domain.Task{planningSession}
	app.content.SetSize(80, 60)
	app.rebuildSidebar()
	app.currentWorkItemID = workItem.ID
	app.sidebar.SelectWorkItem(workItem.ID)
	_ = app.updateContentFromState()
	return app
}

func TestPlanningSidebarRightDrillsIntoTasksOverview(t *testing.T) {
	app := newPlanningDrilldownTestApp()
	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRight})
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}
	if updated.sidebarMode != sidebarPaneTasks {
		t.Fatalf("sidebarMode = %v, want sidebarPaneTasks", updated.sidebarMode)
	}
	sel := updated.sidebar.Selected()
	if sel == nil || sel.Kind != SidebarEntryTaskOverview || sel.SessionID != "" {
		t.Fatalf("selected entry = %#v, want overview row", sel)
	}
	if updated.selectedTaskSessionID() != "" {
		t.Fatalf("selected task session = %q, want empty while overview is selected", updated.selectedTaskSessionID())
	}
	if updated.content.Mode() != ContentModeOverview {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModeOverview)
	}
	if got := len(updated.sidebar.entries); got != 3 {
		t.Fatalf("task sidebar entries = %d, want overview + source details + planning session", got)
	}
	if cmd != nil {
		t.Fatal("expected planning drilldown overview to avoid starting a session tail")
	}
}

func TestPlanningSidebarSourceDetailsSelectionShowsSourceContent(t *testing.T) {
	app := newPlanningDrilldownTestApp()
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRight})
	updated := model.(App)
	model, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)

	sel := updated.sidebar.Selected()
	if sel == nil || sel.Kind != SidebarEntryTaskSourceDetails {
		t.Fatalf("selected entry = %#v, want source-details row", sel)
	}
	if updated.content.Mode() != ContentModeSourceDetails {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModeSourceDetails)
	}
	if cmd != nil {
		t.Fatal("expected planning source-details selection to avoid starting a session tail")
	}
	view := stripBrowseANSI(updated.content.View())
	for _, want := range []string{"Source details", "Provider: GitHub", "Selected: 1 issue", "acme/rocket#99 · Planning issue", "Labels: planning", "Plan the migration carefully before coding."} {
		if !strings.Contains(view, want) {
			t.Fatalf("content view = %q, want %q", view, want)
		}
	}
	if updated.content.sessionLog.sessionID != "" {
		t.Fatalf("session log session id = %q, want empty while showing planning source details", updated.content.sessionLog.sessionID)
	}
}

func TestPlanningSidebarRefreshPreservesSessionOutput(t *testing.T) {
	app := newPlanningDrilldownTestApp()
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRight})
	updated := model.(App)
	model, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)
	if cmd != nil {
		t.Fatal("expected planning source-details selection to avoid starting a session tail")
	}
	model, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)
	if cmd == nil {
		t.Fatal("expected selecting the planning session row to start tailing the session log")
	}
	if updated.content.Mode() != ContentModePlanning {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModePlanning)
	}

	model, cmd = updated.Update(SessionLogLinesMsg{SessionID: "plan-sess-1", Entries: []sessionlog.Entry{{Kind: sessionlog.KindInput, InputKind: "prompt", Text: "Begin planning"}, {Kind: sessionlog.KindToolStart, Tool: "read", Intent: "Reading guidance"}}, NextOffset: 42})
	updated = model.(App)
	if cmd == nil {
		t.Fatal("expected live planning update to continue tailing the session log")
	}

	before := stripBrowseANSI(updated.content.View())
	for _, want := range []string{"Prompt: Begin planning", "read — Reading guidance"} {
		if !strings.Contains(before, want) {
			t.Fatalf("content view before refresh = %q, want %q", before, want)
		}
	}
	if strings.Contains(before, "No session output captured.") {
		t.Fatalf("content view before refresh = %q, want live planning output", before)
	}

	cmd = updated.updateContentFromState()
	if cmd != nil {
		t.Fatalf("updateContentFromState() cmd = %v, want nil while already tailing the selected planning session", cmd)
	}
	after := stripBrowseANSI(updated.content.View())
	for _, want := range []string{"Prompt: Begin planning", "read — Reading guidance"} {
		if !strings.Contains(after, want) {
			t.Fatalf("content view after refresh = %q, want %q", after, want)
		}
	}
	if strings.Contains(after, "No session output captured.") {
		t.Fatalf("content view after refresh = %q, want preserved live planning output", after)
	}
	if updated.content.sessionLog.offset != 42 {
		t.Fatalf("session log offset = %d, want 42", updated.content.sessionLog.offset)
	}
}

func TestPlanningSidebarReopenSessionResumesTailOffset(t *testing.T) {
	app := newPlanningDrilldownTestApp()
	app.sessionsDir = t.TempDir()
	content := "Prompt: Begin planning\nTool: read — Reading guidance\n"
	logPath := filepath.Join(app.sessionsDir, "plan-sess-1.log")
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	offset := int64(len(content))

	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRight})
	updated := model.(App)
	model, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)
	if cmd != nil {
		t.Fatal("expected source-details selection to avoid starting a tail command")
	}
	model, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)
	if cmd == nil {
		t.Fatal("expected selecting the planning session row to start tailing the session log")
	}
	model, _ = updated.Update(SessionLogLinesMsg{SessionID: "plan-sess-1", Entries: []sessionlog.Entry{{Kind: sessionlog.KindInput, InputKind: "prompt", Text: "Begin planning"}, {Kind: sessionlog.KindToolStart, Tool: "read", Intent: "Reading guidance"}}, NextOffset: offset})
	updated = model.(App)
	model, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyUp})
	updated = model.(App)
	if cmd != nil {
		t.Fatalf("moving back to source details returned cmd %v, want nil", cmd)
	}
	if updated.content.Mode() != ContentModeSourceDetails {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModeSourceDetails)
	}
	model, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)
	if cmd == nil {
		t.Fatal("expected reopening the planning session row to resume the tail command")
	}
	msg := cmd()
	linesMsg, ok := msg.(SessionLogLinesMsg)
	if !ok {
		t.Fatalf("cmd() message = %T, want SessionLogLinesMsg", msg)
	}
	if len(linesMsg.Entries) != 0 {
		t.Fatalf("resumed entries = %v, want no duplicate replay at saved offset", linesMsg.Entries)
	}
	if linesMsg.NextOffset != offset {
		t.Fatalf("next offset = %d, want %d", linesMsg.NextOffset, offset)
	}
	if updated.content.sessionLog.offset != offset {
		t.Fatalf("session log offset = %d, want %d", updated.content.sessionLog.offset, offset)
	}
}

func TestPlanningTaskViewShowsPlanReviewNoticeWithoutAutoNavigating(t *testing.T) {
	app := newPlanningDrilldownTestApp()
	app.plans["wi-plan"] = &domain.Plan{ID: "plan-1", WorkItemID: "wi-plan", Status: domain.PlanApproved}
	app.subPlans["plan-1"] = []domain.TaskPlan{{ID: "sp-1", PlanID: "plan-1", RepositoryName: "repo-a"}}

	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRight})
	updated := model.(App)
	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)
	model, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)
	if cmd == nil {
		t.Fatal("expected selecting the planning session row to start tailing the session log")
	}

	updated.workItems[0].State = domain.SessionPlanReview
	updated.workItems[0].UpdatedAt = time.Now().Add(time.Minute)
	updated.sessions[0].Status = domain.AgentSessionCompleted
	updated.sessions[0].UpdatedAt = time.Now().Add(time.Minute)

	cmd = updated.updateContentFromState()
	if cmd != nil {
		t.Fatalf("updateContentFromState() cmd = %v, want nil while preserving selected planning task view", cmd)
	}
	if updated.content.Mode() != ContentModePlanning {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModePlanning)
	}
	if updated.content.sessionLog.sessionID != "plan-sess-1" {
		t.Fatalf("session log session id = %q, want plan-sess-1", updated.content.sessionLog.sessionID)
	}
	notice := updated.content.sessionLog.notice
	if notice == nil {
		t.Fatal("expected planning task notice")
	}
	if notice.Title != "Plan review required" {
		t.Fatalf("notice title = %q, want plan review notice", notice.Title)
	}
	if !strings.Contains(notice.Body, "approved, revised, or rejected") {
		t.Fatalf("notice body = %q, want plan review guidance", notice.Body)
	}
	view := stripBrowseANSI(updated.content.View())
	for _, want := range []string{"Plan review required", "Press [Enter] to open the overview.", "No session output captured."} {
		if !strings.Contains(view, want) {
			t.Fatalf("content view = %q, want %q", view, want)
		}
	}
	foundEnter := false
	for _, hint := range updated.currentHints() {
		if hint.Key == "Enter" && hint.Label == "Open overview" {
			foundEnter = true
			break
		}
	}
	if !foundEnter {
		t.Fatalf("hints = %#v, want Enter/Open overview", updated.currentHints())
	}
}

func TestPlanningTaskViewEnterOpensOverviewForPlanReviewNotice(t *testing.T) {
	app := newPlanningDrilldownTestApp()
	app.plans["wi-plan"] = &domain.Plan{ID: "plan-1", WorkItemID: "wi-plan", Status: domain.PlanApproved}
	app.subPlans["plan-1"] = []domain.TaskPlan{{ID: "sp-1", PlanID: "plan-1", RepositoryName: "repo-a"}}

	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRight})
	updated := model.(App)
	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)
	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)
	updated.workItems[0].State = domain.SessionPlanReview
	updated.workItems[0].UpdatedAt = time.Now().Add(time.Minute)
	updated.sessions[0].Status = domain.AgentSessionCompleted
	updated.sessions[0].UpdatedAt = time.Now().Add(time.Minute)
	if cmd := updated.updateContentFromState(); cmd != nil {
		t.Fatalf("updateContentFromState() cmd = %v, want nil", cmd)
	}

	model, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = model.(App)
	if cmd != nil {
		t.Fatalf("expected Enter quick-jump to avoid starting a tail command, got %v", cmd)
	}
	if updated.mainFocus != mainFocusContent {
		t.Fatalf("mainFocus = %v, want %v", updated.mainFocus, mainFocusContent)
	}
	if updated.selectedTaskSessionID() != "" {
		t.Fatalf("selected task session = %q, want overview selection", updated.selectedTaskSessionID())
	}
	if updated.content.Mode() != ContentModeOverview {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModeOverview)
	}
	if view := stripBrowseANSI(updated.content.View()); !strings.Contains(view, "Plan review required") {
		t.Fatalf("content view = %q, want plan review action", view)
	}
}

func TestInterruptedPlanningSessionShowsRecoveryContent(t *testing.T) {
	t.Parallel()

	app := newPlanningDrilldownTestApp()
	app.sessions[0].Status = domain.AgentSessionInterrupted

	cmd := app.updateContentFromState()
	if cmd != nil {
		t.Fatalf("updateContentFromState() cmd = %v, want nil for interrupted planning session", cmd)
	}
	if app.content.Mode() != ContentModeOverview {
		t.Fatalf("content mode = %v, want %v", app.content.Mode(), ContentModeOverview)
	}
	view := stripBrowseANSI(app.content.View())
	for _, want := range []string{"Action required", "Interrupted task needs recovery", "previous substrate owner stopped heartbeating"} {
		if !strings.Contains(view, want) {
			t.Fatalf("content view = %q, want %q", view, want)
		}
	}
	hints := app.content.KeybindHints()
	if len(hints) < 4 || hints[1].Label != "Resume" || hints[2].Label != "Abandon" {
		t.Fatalf("keybind hints = %#v, want resume/abandon actions", hints)
	}
}

func TestReviewingContentUsesImplementationSessionReviewData(t *testing.T) {
	t.Parallel()

	app := newSidebarDrilldownTestApp()
	now := time.Now().Add(2 * time.Minute)
	app.workItems[0].State = domain.SessionReviewing
	app.workItems[0].UpdatedAt = now
	app.sessions[0].Status = domain.AgentSessionCompleted
	app.sessions[0].UpdatedAt = now
	app.sessions[0].CreatedAt = now
	app.sessions = append(app.sessions, domain.Task{
		ID:             "review-sess-1",
		WorkItemID:     "wi-1",
		WorkspaceID:    "ws-local",
		Phase:          domain.TaskPhaseReview,
		SubPlanID:      "sp-1",
		RepositoryName: "repo-a",
		HarnessName:    "omp-review",
		Status:         domain.AgentSessionCompleted,
		CreatedAt:      now.Add(time.Minute),
		UpdatedAt:      now.Add(time.Minute),
	})
	app.reviews["sess-1"] = ReviewsLoadedMsg{
		SessionID: "sess-1",
		Cycles: []domain.ReviewCycle{{
			ID:             "cycle-1",
			AgentSessionID: "sess-1",
			CycleNumber:    1,
			Status:         domain.ReviewCycleCritiquesFound,
		}},
		Critiques: map[string][]domain.Critique{
			"cycle-1": {{
				ID:            "crit-1",
				ReviewCycleID: "cycle-1",
				Severity:      domain.CritiqueMajor,
				Description:   "Missing nil check before rendering review details",
			}},
		},
	}

	cmd := app.updateContentFromState()
	if cmd != nil {
		t.Fatalf("updateContentFromState() cmd = %v, want nil without review log tail", cmd)
	}
	if app.content.Mode() != ContentModeOverview {
		t.Fatalf("content mode = %v, want %v", app.content.Mode(), ContentModeOverview)
	}
	view := stripBrowseANSI(app.content.View())
	for _, want := range []string{"Under review", "Summary", "Source"} {
		if !strings.Contains(view, want) {
			t.Fatalf("content view = %q, want %q", view, want)
		}
	}
	if got := len(app.content.overview.data.Tasks); got != 1 {
		t.Fatalf("overview task count = %d, want 1", got)
	}
	if note := app.content.overview.data.Tasks[0].Note; note != "1 critique(s)" {
		t.Fatalf("overview task note = %q, want %q", note, "1 critique(s)")
	}
}

func TestHistoricalPlanningSessionRemainsSelectable(t *testing.T) {
	app := newSidebarDrilldownTestApp()
	planningSession := domain.Task{
		ID:          "plan-hist-1",
		WorkItemID:  "wi-1",
		WorkspaceID: "ws-local",
		Phase:       domain.TaskPhasePlanning,
		HarnessName: "omp",
		Status:      domain.AgentSessionCompleted,
		UpdatedAt:   time.Now().Add(time.Minute),
		CreatedAt:   time.Now().Add(time.Minute),
	}
	app.sessions = append(app.sessions, planningSession)
	app.rebuildSidebar()
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRight})
	updated := model.(App)
	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)
	model, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)
	sel := updated.sidebar.Selected()
	if sel == nil || sel.Kind != SidebarEntryTaskSession || sel.SessionID != "plan-hist-1" {
		t.Fatalf("selected entry = %#v, want historical planning session row", sel)
	}
	if updated.content.Mode() != ContentModePlanning {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModePlanning)
	}
	if updated.content.sessionLog.sessionID != "plan-hist-1" {
		t.Fatalf("session log session id = %q, want plan-hist-1", updated.content.sessionLog.sessionID)
	}
	if cmd == nil {
		t.Fatal("expected selecting historical planning session to tail its log")
	}
}

func TestNewSessionOpensFromWorkItemWithExistingSession(t *testing.T) {
	app := newSidebarDrilldownTestApp()

	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if cmd == nil {
		t.Fatal("expected new-session open command")
	}
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}
	if updated.activeOverlay != overlayNewSession {
		t.Fatalf("activeOverlay = %v, want %v", updated.activeOverlay, overlayNewSession)
	}
	if !updated.newSession.Active() {
		t.Fatal("expected new-session overlay to be active")
	}
	if updated.currentWorkItemID != "wi-1" {
		t.Fatalf("currentWorkItemID = %q, want wi-1", updated.currentWorkItemID)
	}
	msg := cmd()
	if _, ok := msg.(issueListLoadedMsg); !ok {
		t.Fatalf("msg = %T, want issueListLoadedMsg", msg)
	}
}

func TestSidebarSessionsHintsUseTasksLabel(t *testing.T) {
	app := newSidebarDrilldownTestApp()
	hints := app.currentHints()

	for _, hint := range hints {
		if hint.Key != "→" {
			continue
		}
		if hint.Label != "Tasks" {
			t.Fatalf("right-arrow hint label = %q, want %q", hint.Label, "Tasks")
		}
		return
	}

	t.Fatal("missing right-arrow hint in sessions sidebar state")
}

func TestSidebarRightDrillsIntoTasksOverview(t *testing.T) {
	app := newSidebarDrilldownTestApp()
	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRight})
	updated, ok := model.(App)
	if !ok {
		t.Fatalf("model = %T, want App", model)
	}
	if updated.sidebarMode != sidebarPaneTasks {
		t.Fatalf("sidebarMode = %v, want sidebarPaneTasks", updated.sidebarMode)
	}
	if updated.mainFocus != mainFocusSidebar {
		t.Fatalf("mainFocus = %v, want mainFocusSidebar", updated.mainFocus)
	}
	foundTaskHint := false
	foundBackHint := false
	for _, hint := range updated.currentHints() {
		switch hint.Key {
		case "↑/↓":
			foundTaskHint = true
			if hint.Label != "Tasks" {
				t.Fatalf("task-pane up/down hint label = %q, want %q", hint.Label, "Tasks")
			}
		case "←/Esc":
			foundBackHint = true
			if hint.Label != "Sessions" {
				t.Fatalf("task-pane back hint label = %q, want %q", hint.Label, "Sessions")
			}
		}
	}
	if !foundTaskHint {
		t.Fatal("missing up/down hint in tasks sidebar state")
	}
	if !foundBackHint {
		t.Fatal("missing back hint in tasks sidebar state")
	}
	if updated.sidebar.title != "SUB-1 · Tasks" {
		t.Fatalf("sidebar title = %q, want %q", updated.sidebar.title, "SUB-1 · Tasks")
	}
	sel := updated.sidebar.Selected()
	if sel == nil || sel.Kind != SidebarEntryTaskOverview || sel.SessionID != "" {
		t.Fatalf("selected entry = %#v, want overview row", sel)
	}
	if got := len(updated.sidebar.entries); got != 3 {
		t.Fatalf("task sidebar entries = %d, want overview + source details + session", got)
	}
	if updated.sidebar.entries[1].Kind != SidebarEntryTaskSourceDetails {
		t.Fatalf("task sidebar second row = %#v, want source-details row", updated.sidebar.entries[1])
	}
	if updated.content.Mode() != ContentModeOverview {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModeOverview)
	}
	if cmd != nil {
		t.Fatal("expected overview drilldown to avoid starting a session tail")
	}
}

func TestSidebarSourceDetailsSelectionShowsSourceContent(t *testing.T) {
	app := newSidebarDrilldownTestApp()
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRight})
	updated := model.(App)
	model, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)
	sel := updated.sidebar.Selected()
	if sel == nil || sel.Kind != SidebarEntryTaskSourceDetails {
		t.Fatalf("selected entry = %#v, want source-details row", sel)
	}
	if updated.content.Mode() != ContentModeSourceDetails {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModeSourceDetails)
	}
	if cmd != nil {
		t.Fatal("expected source-details selection to avoid starting a session tail")
	}
	view := stripBrowseANSI(updated.content.View())
	for _, want := range []string{"Source details", "Work item", "Combine auth and billing fixes into one coordinated rollout.", "Provider: GitHub", "Selected: 2 issues", "acme/rocket#42 · Fix auth", "Investigate auth timeouts in the login flow.", "Labels: bug, backend", "acme/rocket#43 · Repair billing", "Stabilize billing retries and duplicate charge handling."} {
		if !strings.Contains(view, want) {
			t.Fatalf("content view = %q, want %q", view, want)
		}
	}
	if updated.selectedTaskSessionID() != taskSidebarSourceDetailsID {
		t.Fatalf("selected task session = %q, want %q", updated.selectedTaskSessionID(), taskSidebarSourceDetailsID)
	}
	hints := updated.content.KeybindHints()
	if len(hints) == 0 || hints[0].Label != "Scroll" {
		t.Fatalf("keybind hints = %#v, want scroll hint", hints)
	}
	if updated.content.sessionLog.sessionID != "" {
		t.Fatalf("session log session id = %q, want empty while showing source details", updated.content.sessionLog.sessionID)
	}
}

func TestSidebarSourceDetailsShowsQuestionAlertWithoutAutoNavigating(t *testing.T) {
	app := newSidebarDrilldownTestApp()
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRight})
	updated := model.(App)
	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)

	updated.sessions[0].Status = domain.AgentSessionWaitingForAnswer
	updated.questions["sess-1"] = []domain.Question{{
		ID:             "q-1",
		AgentSessionID: "sess-1",
		Content:        "Need approval before continuing",
		ProposedAnswer: "Approve the follow-up.",
		Status:         domain.QuestionEscalated,
		CreatedAt:      time.Now(),
	}}

	cmd := updated.updateContentFromState()
	if cmd != nil {
		t.Fatal("expected question refresh to avoid starting a tail command")
	}
	if updated.selectedTaskSessionID() != taskSidebarSourceDetailsID {
		t.Fatalf("selected task session = %q, want %q", updated.selectedTaskSessionID(), taskSidebarSourceDetailsID)
	}
	if updated.content.Mode() != ContentModeSourceDetails {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModeSourceDetails)
	}
	view := stripBrowseANSI(updated.content.View())
	for _, want := range []string{"Source details", "Question waiting for answer", "repo-a is paused until someone answers", "Press [Enter] to open the overview."} {
		if !strings.Contains(view, want) {
			t.Fatalf("content view = %q, want %q", view, want)
		}
	}
	notice := updated.content.sourceDetails.notice
	if notice == nil {
		t.Fatal("expected source-details notice")
	}
	if !strings.Contains(notice.Body, "Need approval before continuing") {
		t.Fatalf("notice body = %q, want escalated question text", notice.Body)
	}
	hints := updated.currentHints()
	foundEnter := false
	for _, hint := range hints {
		if hint.Key == "Enter" && hint.Label == "Open overview" {
			foundEnter = true
			break
		}
	}
	if !foundEnter {
		t.Fatalf("hints = %#v, want Enter/Open overview", hints)
	}
}

func TestSidebarSourceDetailsEnterOpensOverviewForAlert(t *testing.T) {
	app := newSidebarDrilldownTestApp()
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRight})
	updated := model.(App)
	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)

	updated.sessions[0].Status = domain.AgentSessionWaitingForAnswer
	updated.questions["sess-1"] = []domain.Question{{
		ID:             "q-1",
		AgentSessionID: "sess-1",
		Content:        "Need approval before continuing",
		Status:         domain.QuestionEscalated,
		CreatedAt:      time.Now(),
	}}
	if cmd := updated.updateContentFromState(); cmd != nil {
		t.Fatalf("updateContentFromState() cmd = %v, want nil", cmd)
	}

	model, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = model.(App)
	if cmd != nil {
		t.Fatalf("expected Enter quick-jump to avoid starting a tail command, got %v", cmd)
	}
	if updated.mainFocus != mainFocusContent {
		t.Fatalf("mainFocus = %v, want %v", updated.mainFocus, mainFocusContent)
	}
	if updated.selectedTaskSessionID() != "" {
		t.Fatalf("selected task session = %q, want overview selection", updated.selectedTaskSessionID())
	}
	sel := updated.sidebar.Selected()
	if sel == nil || sel.Kind != SidebarEntryTaskOverview || sel.SessionID != "" {
		t.Fatalf("selected entry = %#v, want overview row", sel)
	}
	if updated.content.Mode() != ContentModeOverview {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModeOverview)
	}
	if view := stripBrowseANSI(updated.content.View()); !strings.Contains(view, "Question waiting for answer") {
		t.Fatalf("content view = %q, want overview question action", view)
	}
}

func TestSidebarSourceDetailsShowsCompletedAlertWithoutAutoNavigating(t *testing.T) {
	app := newSidebarDrilldownTestApp()
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRight})
	updated := model.(App)
	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)

	updated.workItems[0].State = domain.SessionCompleted
	updated.workItems[0].UpdatedAt = time.Now().Add(time.Minute)

	cmd := updated.updateContentFromState()
	if cmd != nil {
		t.Fatal("expected completed-state refresh to avoid starting a tail command")
	}
	if updated.selectedTaskSessionID() != taskSidebarSourceDetailsID {
		t.Fatalf("selected task session = %q, want %q", updated.selectedTaskSessionID(), taskSidebarSourceDetailsID)
	}
	if updated.content.Mode() != ContentModeSourceDetails {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModeSourceDetails)
	}
	view := stripBrowseANSI(updated.content.View())
	for _, want := range []string{"Source details", "Work item completed", "This work item completed while you were focused on a task view.", "Press [Enter] to open the overview and inspect the final status"} {
		if !strings.Contains(view, want) {
			t.Fatalf("content view = %q, want %q", view, want)
		}
	}
	notice := updated.content.sourceDetails.notice
	if notice == nil {
		t.Fatal("expected source-details notice")
	}
	if notice.Hint != "Press [Enter] to open the overview and inspect the final status or review artifacts." {
		t.Fatalf("notice hint = %q, want completed quick-jump hint", notice.Hint)
	}
	hints := updated.currentHints()
	foundEnter := false
	for _, hint := range hints {
		if hint.Key == "Enter" && hint.Label == "Open overview" {
			foundEnter = true
			break
		}
	}
	if !foundEnter {
		t.Fatalf("hints = %#v, want Enter/Open overview", hints)
	}
}

func TestSidebarTaskSelectionShowsTaskContent(t *testing.T) {
	app := newSidebarDrilldownTestApp()
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRight})
	updated := model.(App)
	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)
	model, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)
	sel := updated.sidebar.Selected()
	if sel == nil || sel.Kind != SidebarEntryTaskSession || sel.SessionID != "sess-1" {
		t.Fatalf("selected entry = %#v, want task row for sess-1", sel)
	}
	if updated.selectedTaskSessionID() != "sess-1" {
		t.Fatalf("selected task session = %q, want sess-1", updated.selectedTaskSessionID())
	}
	if updated.content.Mode() != ContentModeSessionInteraction {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModeSessionInteraction)
	}
	if updated.content.sessionLog.sessionID != "sess-1" {
		t.Fatalf("session log session id = %q, want sess-1", updated.content.sessionLog.sessionID)
	}
	if cmd == nil {
		t.Fatal("expected selecting a task to tail its log")
	}
}

func TestSidebarTaskContentUsesSidebarSessionTitle(t *testing.T) {
	app := newSidebarDrilldownTestApp()
	app.sessions[0].ID = "implementation-session-123456789"
	app.rebuildSidebar()

	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRight})
	updated := model.(App)
	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)
	model, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)
	if cmd == nil {
		t.Fatal("expected selecting a task to tail its log")
	}
	if updated.content.sessionLog.sessionID != "implementation-session-123456789" {
		t.Fatalf("session log session id = %q, want implementation-session-123456789", updated.content.sessionLog.sessionID)
	}
	view := stripBrowseANSI(updated.content.View())
	if !strings.Contains(view, "SUB-1 · Session implemen") {
		t.Fatalf("content view = %q, want sidebar-style task title", view)
	}
	if strings.Contains(view, "implementation-session-123456789") {
		t.Fatalf("content view = %q, want full session id omitted from rendered title/meta", view)
	}
}

func TestSidebarTaskViewShowsInterruptedNoticeWithoutAutoNavigating(t *testing.T) {
	app := newSidebarDrilldownTestApp()
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRight})
	updated := model.(App)
	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)
	model, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)
	if cmd == nil {
		t.Fatal("expected selecting a task to tail its log")
	}

	updated.sessions[0].Status = domain.AgentSessionInterrupted
	updated.sessions[0].UpdatedAt = time.Now().Add(time.Minute)

	cmd = updated.updateContentFromState()
	if cmd != nil {
		t.Fatalf("updateContentFromState() cmd = %v, want nil while preserving selected repo task view", cmd)
	}
	if updated.content.Mode() != ContentModeSessionInteraction {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModeSessionInteraction)
	}
	if updated.content.sessionLog.sessionID != "sess-1" {
		t.Fatalf("session log session id = %q, want sess-1", updated.content.sessionLog.sessionID)
	}
	notice := updated.content.sessionLog.notice
	if notice == nil {
		t.Fatal("expected interrupted task notice")
	}
	if notice.Title != "Interrupted task needs recovery" {
		t.Fatalf("notice title = %q, want interrupted task notice", notice.Title)
	}
	if !strings.Contains(notice.Body, "resumed or abandoned") {
		t.Fatalf("notice body = %q, want interrupted recovery guidance", notice.Body)
	}
	view := stripBrowseANSI(updated.content.View())
	for _, want := range []string{"Interrupted task needs recovery", "Press [Enter] to open the overview.", "No session output captured."} {
		if !strings.Contains(view, want) {
			t.Fatalf("content view = %q, want %q", view, want)
		}
	}
	foundEnter := false
	for _, hint := range updated.currentHints() {
		if hint.Key == "Enter" && hint.Label == "Open overview" {
			foundEnter = true
			break
		}
	}
	if !foundEnter {
		t.Fatalf("hints = %#v, want Enter/Open overview", updated.currentHints())
	}
}

func TestSidebarLeftBacksOutFromTaskContentToSessions(t *testing.T) {
	app := newSidebarDrilldownTestApp()
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRight})
	updated := model.(App)
	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)
	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)
	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRight})
	updated = model.(App)
	if updated.mainFocus != mainFocusContent {
		t.Fatalf("mainFocus = %v, want mainFocusContent", updated.mainFocus)
	}
	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyLeft})
	updated = model.(App)
	if updated.mainFocus != mainFocusSidebar || updated.sidebarMode != sidebarPaneTasks {
		t.Fatalf("focus/sidebarMode = %v/%v, want sidebar focus in task pane", updated.mainFocus, updated.sidebarMode)
	}
	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyLeft})
	updated = model.(App)
	if updated.sidebarMode != sidebarPaneSessions {
		t.Fatalf("sidebarMode = %v, want sidebarPaneSessions", updated.sidebarMode)
	}
	sel := updated.sidebar.Selected()
	if sel == nil || sel.Kind != SidebarEntryWorkItem || sel.WorkItemID != "wi-1" {
		t.Fatalf("selected entry = %#v, want parent work item row", sel)
	}
	if updated.content.Mode() != ContentModeOverview {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModeOverview)
	}
}

func TestSidebarEscBacksOutFromTaskContentToSessions(t *testing.T) {
	app := newSidebarDrilldownTestApp()
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRight})
	updated := model.(App)
	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)
	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)
	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRight})
	updated = model.(App)

	foundBackHint := false
	for _, hint := range updated.currentHints() {
		if hint.Key != "←/Esc" {
			continue
		}
		foundBackHint = true
		if hint.Label != "Back" {
			t.Fatalf("content back hint label = %q, want %q", hint.Label, "Back")
		}
	}
	if !foundBackHint {
		t.Fatal("missing escape back hint in content focus state")
	}

	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = model.(App)
	if updated.mainFocus != mainFocusSidebar || updated.sidebarMode != sidebarPaneTasks {
		t.Fatalf("focus/sidebarMode = %v/%v, want sidebar focus in task pane", updated.mainFocus, updated.sidebarMode)
	}
	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = model.(App)
	if updated.sidebarMode != sidebarPaneSessions {
		t.Fatalf("sidebarMode = %v, want sidebarPaneSessions", updated.sidebarMode)
	}
	sel := updated.sidebar.Selected()
	if sel == nil || sel.Kind != SidebarEntryWorkItem || sel.WorkItemID != "wi-1" {
		t.Fatalf("selected entry = %#v, want parent work item row", sel)
	}
	if updated.content.Mode() != ContentModeOverview {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModeOverview)
	}
}
