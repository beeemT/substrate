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
)

type duplicateCreateWorkItemRepo struct {
	items     []domain.WorkItem
	createErr error
	listErr   error
}

func (r duplicateCreateWorkItemRepo) Get(context.Context, string) (domain.WorkItem, error) {
	return domain.WorkItem{}, repository.ErrNotFound
}

func (r duplicateCreateWorkItemRepo) List(_ context.Context, filter repository.WorkItemFilter) ([]domain.WorkItem, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	items := make([]domain.WorkItem, 0, len(r.items))
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

func (r duplicateCreateWorkItemRepo) Create(context.Context, domain.WorkItem) error {
	return r.createErr
}

func (r duplicateCreateWorkItemRepo) Update(context.Context, domain.WorkItem) error {
	return nil
}

func (r duplicateCreateWorkItemRepo) Delete(context.Context, string) error {
	return nil
}

type sessionSearchDeleteRepo struct {
	sessions map[string]domain.AgentSession
	entry    domain.SessionHistoryEntry
}

func (r *sessionSearchDeleteRepo) Get(_ context.Context, id string) (domain.AgentSession, error) {
	session, ok := r.sessions[id]
	if !ok {
		return domain.AgentSession{}, repository.ErrNotFound
	}
	return session, nil
}

func (r *sessionSearchDeleteRepo) ListBySubPlanID(_ context.Context, subPlanID string) ([]domain.AgentSession, error) {
	result := make([]domain.AgentSession, 0, len(r.sessions))
	for _, session := range r.sessions {
		if session.SubPlanID == subPlanID {
			result = append(result, session)
		}
	}
	return result, nil
}

func (r *sessionSearchDeleteRepo) ListByWorkspaceID(_ context.Context, workspaceID string) ([]domain.AgentSession, error) {
	result := make([]domain.AgentSession, 0, len(r.sessions))
	for _, session := range r.sessions {
		if session.WorkspaceID == workspaceID {
			result = append(result, session)
		}
	}
	return result, nil
}

func (r *sessionSearchDeleteRepo) ListByOwnerInstanceID(_ context.Context, instanceID string) ([]domain.AgentSession, error) {
	result := make([]domain.AgentSession, 0, len(r.sessions))
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

func (r *sessionSearchDeleteRepo) Create(_ context.Context, session domain.AgentSession) error {
	r.sessions[session.ID] = session
	return nil
}

func (r *sessionSearchDeleteRepo) Update(_ context.Context, session domain.AgentSession) error {
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
	if strings.Contains(view, "No work item sessions found") {
		t.Fatalf("session search view = %q, want local seeded session instead of empty state", view)
	}
}

func TestApp_SessionSearchDeleteRemovesSessionAndLogs(t *testing.T) {
	t.Parallel()

	now := time.Now()
	repo := &sessionSearchDeleteRepo{
		sessions: map[string]domain.AgentSession{
			"sess-1": {ID: "sess-1", WorkspaceID: "ws-1", SubPlanID: "sp-1", RepositoryName: "repo-a", Status: domain.AgentSessionCompleted, UpdatedAt: now, CreatedAt: now},
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
	workItemSvc := service.NewWorkItemService(duplicateCreateWorkItemRepo{items: []domain.WorkItem{{ID: "wi-1", WorkspaceID: "ws-1", ExternalID: "SUB-1", Title: "Work item", State: domain.WorkItemImplementing}}})
	planSvc := service.NewPlanService(&cmdPlanRepo{plans: map[string]domain.Plan{}}, &cmdSubPlanRepo{subPlans: map[string]domain.SubPlan{}})
	app := NewApp(Services{
		Session:       service.NewSessionService(repo),
		WorkItem:      workItemSvc,
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
	app.sessions = []domain.AgentSession{repo.sessions["sess-1"]}
	app.activeOverlay = overlaySessionSearch
	app.sessionSearch.Open(sessionHistoryScopeWorkspace, true)
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
	if confirmMsg.SessionID != "sess-1" {
		t.Fatalf("confirm session id = %q, want sess-1", confirmMsg.SessionID)
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
	for _, want := range []string{"Delete Session", "review data", "[y]", "[n]"} {
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
	if updated.sessionSearch.Selected() != nil {
		t.Fatalf("selected entry = %#v, want nil after deletion", updated.sessionSearch.Selected())
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
	if !strings.Contains(searchView, "No items.") {
		t.Fatalf("search view = %q, want list empty state after deletion", searchView)
	}
}

func TestLoadHistoryEntry_LocalWorkspaceUsesWorkItemContent(t *testing.T) {
	app := NewApp(Services{
		WorkspaceID:   "ws-local",
		WorkspaceName: "local",
		Settings:      &SettingsService{},
	})
	app.content.SetSize(80, 20)
	app.workItems = []domain.WorkItem{{
		ID:          "wi-1",
		ExternalID:  "SUB-1",
		Source:      "github",
		Title:       "Local item",
		Description: "## Summary\n\nThis is **important**.",
		Labels:      []string{"bug", "backend"},
		Metadata: map[string]any{
			"tracker_refs": []domain.TrackerReference{{Provider: "github", Kind: "issue", Owner: "acme", Repo: "rocket", Number: 42}},
		},
		State: domain.WorkItemIngested,
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
	if app.content.Mode() != ContentModeReadyToPlan {
		t.Fatalf("content mode = %v, want %v", app.content.Mode(), ContentModeReadyToPlan)
	}

	view := stripBrowseANSI(app.content.View())
	for _, want := range []string{"SUB-1 · Local item", "Details", "Summary", "This is important.", "Press [Enter]"} {
		if !strings.Contains(view, want) {
			t.Fatalf("content view = %q, want %q", view, want)
		}
	}
	for _, hidden := range []string{"GitHub", "acme/rocket", "Labels: bug, backend"} {
		if strings.Contains(view, hidden) {
			t.Fatalf("content view = %q, want overview to omit source detail %q", view, hidden)
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
		State:         domain.WorkItemPlanning,
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

func TestRebuildSidebarLeavesSessionsUnselectedUntilNavigation(t *testing.T) {
	now := time.Now()
	older := now.Add(-2 * time.Hour)
	newer := now.Add(-15 * time.Minute)

	app := NewApp(Services{
		WorkspaceID:   "ws-local",
		WorkspaceName: "local",
		Settings:      &SettingsService{},
	})
	app.workItems = []domain.WorkItem{
		{ID: "wi-old", ExternalID: "SUB-1", Title: "Old", State: domain.WorkItemIngested, CreatedAt: older, UpdatedAt: older},
		{ID: "wi-new", ExternalID: "SUB-2", Title: "New", State: domain.WorkItemIngested, CreatedAt: older, UpdatedAt: newer},
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
	if updated.content.Mode() != ContentModeReadyToPlan {
		t.Fatalf("content mode after first MoveDown = %v, want %v", updated.content.Mode(), ContentModeReadyToPlan)
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
	app.workItems = []domain.WorkItem{{
		ID:          "wi-old",
		WorkspaceID: "ws-local",
		ExternalID:  "SUB-1",
		Title:       "Old item",
		State:       domain.WorkItemIngested,
		CreatedAt:   older,
		UpdatedAt:   older,
	}}
	app.rebuildSidebar()

	model, _ := app.Update(WorkItemCreatedMsg{
		WorkItem: domain.WorkItem{
			ID:          "wi-new",
			WorkspaceID: "ws-local",
			ExternalID:  "SUB-2",
			Title:       "New item",
			State:       domain.WorkItemIngested,
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
	if updated.content.Mode() != ContentModeReadyToPlan {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModeReadyToPlan)
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
	existing := domain.WorkItem{
		ID:          "wi-existing",
		WorkspaceID: "ws-local",
		ExternalID:  "SUB-1",
		Title:       "Existing item",
		State:       domain.WorkItemIngested,
	}
	repo := duplicateCreateWorkItemRepo{
		items:     []domain.WorkItem{existing},
		createErr: service.ErrAlreadyExists{Entity: "work item", ID: existing.ExternalID},
	}

	requested := domain.WorkItem{
		ID:         "wi-new",
		ExternalID: existing.ExternalID,
		Title:      "Duplicate item",
	}
	msg := persistCreatedWorkItemMsg(Services{
		WorkspaceID: "ws-local",
		WorkItem:    service.NewWorkItemService(repo),
	}, requested)

	dup, ok := msg.(WorkItemDuplicatePromptMsg)
	if !ok {
		t.Fatalf("msg = %T, want WorkItemDuplicatePromptMsg", msg)
	}
	if dup.ExistingWorkItem.ID != existing.ID {
		t.Fatalf("existing work item id = %q, want %q", dup.ExistingWorkItem.ID, existing.ID)
	}
	if dup.RequestedWorkItem.ID != requested.ID {
		t.Fatalf("requested work item id = %q, want %q", dup.RequestedWorkItem.ID, requested.ID)
	}
}

func TestPersistCreatedWorkItemMsgAggregateDuplicateReturnsPrompt(t *testing.T) {
	existing := domain.WorkItem{
		ID:            "wi-existing",
		WorkspaceID:   "ws-local",
		ExternalID:    "SUB-42",
		Title:         "Existing issue",
		Source:        "github",
		SourceScope:   domain.ScopeIssues,
		SourceItemIDs: []string{"acme/rocket#42"},
		State:         domain.WorkItemIngested,
	}
	repo := duplicateCreateWorkItemRepo{
		items:     []domain.WorkItem{existing},
		createErr: service.ErrAlreadyExists{Entity: "work item", ID: existing.ExternalID},
	}

	requested := domain.WorkItem{
		ID:            "wi-aggregate",
		ExternalID:    "SUB-7",
		Title:         "Issue 7 (+1 more)",
		Source:        "github",
		SourceScope:   domain.ScopeIssues,
		SourceItemIDs: []string{"acme/rocket#7", "acme/rocket#42"},
	}
	msg := persistCreatedWorkItemMsg(Services{
		WorkspaceID: "ws-local",
		WorkItem:    service.NewWorkItemService(repo),
	}, requested)

	dup, ok := msg.(WorkItemDuplicatePromptMsg)
	if !ok {
		t.Fatalf("msg = %T, want WorkItemDuplicatePromptMsg", msg)
	}
	if dup.ExistingWorkItem.ID != existing.ID {
		t.Fatalf("existing work item id = %q, want %q", dup.ExistingWorkItem.ID, existing.ID)
	}
	if dup.RequestedWorkItem.ID != requested.ID {
		t.Fatalf("requested work item id = %q, want %q", dup.RequestedWorkItem.ID, requested.ID)
	}
}

func newDuplicatePromptTestApp() (App, domain.WorkItem, domain.WorkItem, domain.WorkItem) {
	now := time.Now()
	older := now.Add(-time.Hour)
	existing := domain.WorkItem{
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
		State:     domain.WorkItemIngested,
		CreatedAt: older,
		UpdatedAt: older,
	}
	other := domain.WorkItem{
		ID:          "wi-other",
		WorkspaceID: "ws-local",
		ExternalID:  "SUB-2",
		Title:       "Other item",
		State:       domain.WorkItemIngested,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	requested := domain.WorkItem{
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
	app.workItems = []domain.WorkItem{existing, other}
	app.currentWorkItemID = other.ID
	app.rebuildSidebar()
	app.sidebar.SelectWorkItem(other.ID)
	_ = app.updateContentFromState()
	return app, existing, other, requested
}

func TestWorkItemDuplicatePromptShowsDecisionDialog(t *testing.T) {
	app, existing, other, requested := newDuplicatePromptTestApp()

	model, cmd := app.Update(WorkItemDuplicatePromptMsg{
		RequestedWorkItem: requested,
		ExistingWorkItem:  existing,
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
	for _, want := range []string{"Work item already exists", "Existing work item: SUB-1 · Existing item", "Requested selection: SUB-99 · Requested item", "Go to existing work item", "Start planning with existing work item"} {
		if !strings.Contains(view, want) {
			t.Fatalf("dialog view = %q, want %q", view, want)
		}
	}
}

func TestWorkItemDuplicateOpenExistingChoiceFocusesExistingWorkItemOverview(t *testing.T) {
	app, existing, _, requested := newDuplicatePromptTestApp()

	model, _ := app.Update(WorkItemDuplicatePromptMsg{
		RequestedWorkItem: requested,
		ExistingWorkItem:  existing,
	})
	updated := model.(App)
	model, cmd := updated.Update(WorkItemDuplicateActionMsg{Action: WorkItemDuplicateOpenExisting})
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
	if updated.content.Mode() != ContentModeReadyToPlan {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModeReadyToPlan)
	}
	sel := updated.sidebar.Selected()
	if sel == nil || sel.WorkItemID != existing.ID {
		t.Fatalf("selected sidebar entry = %v, want %q", sel, existing.ID)
	}
	toastView := stripBrowseANSI(updated.toasts.View())
	if !strings.Contains(toastView, "ℹ Opened existing item SUB-1") {
		t.Fatalf("toast view = %q", toastView)
	}
	view := stripBrowseANSI(updated.content.View())
	for _, want := range []string{"SUB-1 · Existing item", "Details", "Summary", "This is important.", "Press [Enter]"} {
		if !strings.Contains(view, want) {
			t.Fatalf("content view = %q, want %q", view, want)
		}
	}
}

func TestWorkItemDuplicateCreateSessionChoiceStartsPlanningWithExistingWorkItem(t *testing.T) {
	app, existing, _, requested := newDuplicatePromptTestApp()

	model, _ := app.Update(WorkItemDuplicatePromptMsg{
		RequestedWorkItem: requested,
		ExistingWorkItem:  existing,
	})
	updated := model.(App)
	model, cmd := updated.Update(WorkItemDuplicateActionMsg{Action: WorkItemDuplicateCreateSession})
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
		t.Fatalf("expected work item %q to remain present", existing.ID)
	} else if got.State != domain.WorkItemPlanning {
		t.Fatalf("work item state = %v, want %v", got.State, domain.WorkItemPlanning)
	}
	if updated.content.Mode() != ContentModePlanning {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModePlanning)
	}
	toastView := stripBrowseANSI(updated.toasts.View())
	if !strings.Contains(toastView, "ℹ Starting planning with existing item SUB-1") {
		t.Fatalf("toast view = %q", toastView)
	}
}

func TestWorkItemDuplicateCancelChoiceKeepsCurrentSelection(t *testing.T) {
	app, existing, other, requested := newDuplicatePromptTestApp()

	model, _ := app.Update(WorkItemDuplicatePromptMsg{
		RequestedWorkItem: requested,
		ExistingWorkItem:  existing,
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

	model, cmd := app.Update(WorkItemCreatedMsg{
		WorkItem: domain.WorkItem{
			ID:          "wi-new",
			WorkspaceID: "ws-local",
			ExternalID:  "SUB-2",
			Title:       "New item",
			State:       domain.WorkItemIngested,
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
	if updated.content.Mode() != ContentModePlanning {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModePlanning)
	}
	if len(updated.workItems) != 1 || updated.workItems[0].State != domain.WorkItemPlanning {
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
	workItem := domain.WorkItem{
		ID:            "wi-1",
		WorkspaceID:   "ws-local",
		ExternalID:    "SUB-1",
		Source:        "github",
		SourceScope:   domain.ScopeIssues,
		SourceItemIDs: []string{"acme/rocket#42", "acme/rocket#43"},
		Title:         "Work item",
		Labels:        []string{"bug", "backend"},
		Metadata: map[string]any{
			"tracker_refs": []domain.TrackerReference{
				{Provider: "github", Kind: "issue", Owner: "acme", Repo: "rocket", Number: 42},
				{Provider: "github", Kind: "issue", Owner: "acme", Repo: "rocket", Number: 43},
			},
		},
		State:     domain.WorkItemImplementing,
		CreatedAt: now,
		UpdatedAt: now,
	}
	plan := &domain.Plan{ID: "plan-1", WorkItemID: workItem.ID}
	subPlan := domain.SubPlan{
		ID:             "sp-1",
		PlanID:         plan.ID,
		RepositoryName: "repo-a",
		Status:         domain.SubPlanInProgress,
		UpdatedAt:      now,
	}
	session := domain.AgentSession{
		ID:             "sess-1",
		WorkspaceID:    "ws-local",
		SubPlanID:      subPlan.ID,
		RepositoryName: subPlan.RepositoryName,
		HarnessName:    "omp",
		Status:         domain.AgentSessionRunning,
		UpdatedAt:      now,
	}
	app.workItems = []domain.WorkItem{workItem}
	app.plans[workItem.ID] = plan
	app.subPlans[plan.ID] = []domain.SubPlan{subPlan}
	app.sessions = []domain.AgentSession{session}
	app.content.SetSize(80, 20)
	app.rebuildSidebar()
	app.currentWorkItemID = workItem.ID
	app.sidebar.SelectWorkItem(workItem.ID)
	_ = app.updateContentFromState()
	return app
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
	if updated.content.Mode() != ContentModeImplementing {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModeImplementing)
	}
	if cmd == nil {
		t.Fatal("expected overview drilldown to preserve implementing tail command")
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
	for _, want := range []string{"Source details", "Provider: GitHub", "Selected: 2 issues", "acme/rocket#42", "acme/rocket#43"} {
		if !strings.Contains(view, want) {
			t.Fatalf("content view = %q, want %q", view, want)
		}
	}
	if !strings.Contains(view, "Labels are omitted here because") || !strings.Contains(view, "multiple source") {
		t.Fatalf("content view = %q, want multi-source labels note", view)
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

func TestSidebarSourceDetailsYieldsToEscalatedQuestion(t *testing.T) {
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
		t.Fatal("expected escalated question refresh to avoid starting a tail command")
	}
	if updated.selectedTaskSessionID() != taskSidebarSourceDetailsID {
		t.Fatalf("selected task session = %q, want %q", updated.selectedTaskSessionID(), taskSidebarSourceDetailsID)
	}
	if updated.content.Mode() != ContentModeQuestion {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModeQuestion)
	}
	if view := stripBrowseANSI(updated.content.View()); !strings.Contains(view, "Need approval before continuing") {
		t.Fatalf("content view = %q, want escalated question text", view)
	}
}

func TestSidebarSourceDetailsYieldsToCompletedState(t *testing.T) {
	app := newSidebarDrilldownTestApp()
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRight})
	updated := model.(App)
	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = model.(App)

	updated.workItems[0].State = domain.WorkItemCompleted
	updated.workItems[0].UpdatedAt = time.Now().Add(time.Minute)

	cmd := updated.updateContentFromState()
	if cmd != nil {
		t.Fatal("expected completed-state refresh to avoid starting a tail command")
	}
	if updated.selectedTaskSessionID() != taskSidebarSourceDetailsID {
		t.Fatalf("selected task session = %q, want %q", updated.selectedTaskSessionID(), taskSidebarSourceDetailsID)
	}
	if updated.content.Mode() != ContentModeCompleted {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModeCompleted)
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
	if updated.content.Mode() != ContentModeImplementing {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModeImplementing)
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
	if updated.content.Mode() != ContentModeImplementing {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModeImplementing)
	}
}
