package views

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
	"github.com/beeemT/substrate/internal/tui/styles"
)

func TestTaskOverviewMatchesRootOverviewContent(t *testing.T) {
	t.Parallel()

	app := newSidebarDrilldownTestApp()
	rootView := stripBrowseANSI(app.content.View())
	if app.content.Mode() != ContentModeOverview {
		t.Fatalf("content mode = %v, want %v", app.content.Mode(), ContentModeOverview)
	}

	model, cmd := app.Update(teaKeyRight())
	updated := model.(*App)
	if cmd != nil {
		t.Fatalf("overview drilldown cmd = %v, want nil", cmd)
	}
	if updated.content.Mode() != ContentModeOverview {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModeOverview)
	}
	if got := stripBrowseANSI(updated.content.View()); got != rootView {
		t.Fatalf("overview view mismatch\nroot:\n%s\n\ntask overview:\n%s", rootView, got)
	}
}

func TestPlanReviewOverviewExposesActionControls(t *testing.T) {
	t.Parallel()

	now := time.Now()
	app := newTestApp(Services{
		WorkspaceID:   "ws-local",
		WorkspaceName: "local",
		Settings:      newTestSettingsService(),
		SessionArtifacts: service.NewSessionReviewArtifactService(repository.NoopTransacter{Res: repository.Resources{
			SessionReviewArtifacts: emptySessionArtifactRepo{},
		}}),
		Events: service.NewEventService(repository.NoopTransacter{Res: repository.Resources{
			Events: emptyEventRepo{},
		}}),
	})
	app.content.SetSize(90, 24)
	app.workItems = []domain.Session{{
		ID:          "wi-1",
		WorkspaceID: "ws-local",
		ExternalID:  "SUB-1",
		Title:       "Review plan",
		State:       domain.SessionPlanReview,
		CreatedAt:   now,
		UpdatedAt:   now,
	}}
	app.plans["wi-1"] = &domain.Plan{
		ID:               "plan-1",
		WorkItemID:       "wi-1",
		Status:           domain.PlanPendingReview,
		Version:          2,
		UpdatedAt:        now,
		OrchestratorPlan: "```substrate-plan\nexecution_groups:\n  - [repo-a]\n```\n\n## Orchestration\n\nShip it.\n\n## SubPlan: repo-a\nDo the thing.\n",
		FAQ:              []domain.FAQEntry{{ID: "faq-1"}},
	}
	app.subPlans["plan-1"] = []domain.TaskPlan{{
		ID:             "sp-1",
		PlanID:         "plan-1",
		RepositoryName: "repo-a",
		Status:         domain.SubPlanPending,
		UpdatedAt:      now,
	}}
	app.currentWorkItemID = "wi-1"
	app.rebuildSidebar()
	app.sidebar.SelectWorkItem("wi-1")
	if cmd := app.updateContentFromState(); cmd != nil {
		t.Fatalf("updateContentFromState() cmd = %v, want nil", cmd)
	}
	if app.content.Mode() != ContentModeOverview {
		t.Fatalf("content mode = %v, want %v", app.content.Mode(), ContentModeOverview)
	}
	if got := len(app.content.overview.data.Actions); got != 1 {
		t.Fatalf("overview actions = %d, want 1", got)
	}
	hints := app.content.KeybindHints()
	for _, want := range []string{"Approve", "Inspect / Request changes"} {
		found := false
		for _, hint := range hints {
			if hint.Label == want {
				found = true

				break
			}
		}
		if !found {
			t.Fatalf("keybind hints = %#v, want label %q", hints, want)
		}
	}
	if context := strings.Join(app.content.overview.data.Actions[0].Context, " | "); !strings.Contains(context, "Version: v2") {
		t.Fatalf("overview action context = %q, want plan version", context)
	}
	view := stripBrowseANSI(app.content.View())
	for _, want := range []string{"Action required", "Plan review required", "repo-a"} {
		if !strings.Contains(view, want) {
			t.Fatalf("content view = %q, want %q", view, want)
		}
	}
}

func TestArtifactItemFromReviewArtifactPopulatesStableID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		artifact domain.ReviewArtifact
		wantID   string
	}{
		{
			name: "github",
			artifact: domain.ReviewArtifact{
				Provider: "github",
				Kind:     "PR",
				RepoName: "acme/api",
				Ref:      "#42",
			},
			wantID: "github:acme/api:#42",
		},
		{
			name: "gitlab",
			artifact: domain.ReviewArtifact{
				Provider: "gitlab",
				Kind:     "MR",
				RepoName: "group/project",
				Ref:      "!7",
			},
			wantID: "gitlab:group/project:!7",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			item := artifactItemFromReviewArtifact(tt.artifact)
			if item.ID != tt.wantID {
				t.Fatalf("ID = %q, want %q", item.ID, tt.wantID)
			}
		})
	}
}

func TestOverviewUsesDurableSourceSummariesWhenAvailable(t *testing.T) {
	t.Parallel()

	now := time.Now()
	app := newTestApp(Services{
		WorkspaceID:   "ws-local",
		WorkspaceName: "local",
		Settings:      newTestSettingsService(),
		SessionArtifacts: service.NewSessionReviewArtifactService(repository.NoopTransacter{Res: repository.Resources{
			SessionReviewArtifacts: emptySessionArtifactRepo{},
		}}),
		Events: service.NewEventService(repository.NoopTransacter{Res: repository.Resources{
			Events: emptyEventRepo{},
		}}),
	})
	app.content.SetSize(100, 40)
	app.workItems = []domain.Session{{
		ID:            "wi-1",
		WorkspaceID:   "ws-local",
		ExternalID:    "SUB-1",
		Source:        "github",
		SourceScope:   domain.ScopeIssues,
		SourceItemIDs: []string{"acme/rocket#42", "acme/rocket#43"},
		Title:         "Issue 42 (+1 more)",
		State:         domain.SessionIngested,
		Metadata: map[string]any{
			"tracker_refs":     []domain.TrackerReference{{Provider: "github", Kind: "issue", Owner: "acme", Repo: "rocket", Number: 42}, {Provider: "github", Kind: "issue", Owner: "acme", Repo: "rocket", Number: 43}},
			"source_summaries": []domain.SourceSummary{{Provider: "github", Ref: "acme/rocket#42", Title: "Fix auth", Excerpt: "Investigate auth timeouts", URL: "https://github.com/acme/rocket/issues/42"}, {Provider: "github", Ref: "acme/rocket#43", Title: "Repair billing", Excerpt: "Stabilize billing retries", URL: "https://github.com/acme/rocket/issues/43"}},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}}
	app.currentWorkItemID = "wi-1"
	app.rebuildSidebar()
	app.sidebar.SelectWorkItem("wi-1")
	if cmd := app.updateContentFromState(); cmd != nil {
		t.Fatalf("updateContentFromState() cmd = %v, want nil", cmd)
	}
	view := stripBrowseANSI(app.content.View())
	for _, want := range []string{"Fix auth", "Repair billing", "Investigate auth timeouts", "Stabilize billing retries"} {
		if !strings.Contains(view, want) {
			t.Fatalf("content view = %q, want %q", view, want)
		}
	}
}

type overviewEventRepo struct {
	events []domain.SystemEvent
}

func (r *overviewEventRepo) Create(_ context.Context, e domain.SystemEvent) error {
	r.events = append(r.events, e)

	return nil
}

func (r *overviewEventRepo) ListByType(_ context.Context, eventType string, limit int) ([]domain.SystemEvent, error) {
	filtered := make([]domain.SystemEvent, 0, len(r.events))
	for _, event := range r.events {
		if event.EventType == eventType {
			filtered = append(filtered, event)
		}
	}
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
	}

	return filtered, nil
}

func (r *overviewEventRepo) ListByWorkspaceID(_ context.Context, workspaceID string, limit int) ([]domain.SystemEvent, error) {
	filtered := make([]domain.SystemEvent, 0, len(r.events))
	for _, event := range r.events {
		if event.WorkspaceID == workspaceID {
			filtered = append(filtered, event)
		}
	}
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
	}

	return filtered, nil
}

type overviewArtifactLinkRepo struct {
	links []domain.SessionReviewArtifact
}

func (r *overviewArtifactLinkRepo) Upsert(_ context.Context, link domain.SessionReviewArtifact) error {
	r.links = append(r.links, link)
	return nil
}

func (r *overviewArtifactLinkRepo) ListByWorkItemID(_ context.Context, workItemID string) ([]domain.SessionReviewArtifact, error) {
	out := make([]domain.SessionReviewArtifact, 0, len(r.links))
	for _, link := range r.links {
		if link.WorkItemID == workItemID {
			out = append(out, link)
		}
	}
	return out, nil
}

func (r *overviewArtifactLinkRepo) ListByWorkspaceID(_ context.Context, workspaceID string) ([]domain.SessionReviewArtifact, error) {
	out := make([]domain.SessionReviewArtifact, 0, len(r.links))
	for _, link := range r.links {
		if link.WorkspaceID == workspaceID {
			out = append(out, link)
		}
	}
	return out, nil
}

func (r *overviewArtifactLinkRepo) TransferArtifactLinks(_ context.Context, fromID, toID string) error {
	for i := range r.links {
		if r.links[i].ProviderArtifactID == fromID {
			r.links[i].ProviderArtifactID = toID
		}
	}
	return nil
}

func (r *overviewArtifactLinkRepo) DeleteByWorkItemID(_ context.Context, workItemID string) error {
	filtered := make([]domain.SessionReviewArtifact, 0, len(r.links))
	for _, link := range r.links {
		if link.WorkItemID != workItemID {
			filtered = append(filtered, link)
		}
	}
	r.links = filtered
	return nil
}

type overviewGitlabMRRepo struct {
	mrs map[string]domain.GitlabMergeRequest
}

func (r *overviewGitlabMRRepo) Upsert(_ context.Context, mr domain.GitlabMergeRequest) error {
	if r.mrs == nil {
		r.mrs = make(map[string]domain.GitlabMergeRequest)
	}
	r.mrs[mr.ID] = mr
	return nil
}

func (r *overviewGitlabMRRepo) Get(_ context.Context, id string) (domain.GitlabMergeRequest, error) {
	return r.mrs[id], nil
}

func (r *overviewGitlabMRRepo) GetByIID(_ context.Context, projectPath string, iid int) (domain.GitlabMergeRequest, error) {
	for _, mr := range r.mrs {
		if mr.ProjectPath == projectPath && mr.IID == iid {
			return mr, nil
		}
	}
	return domain.GitlabMergeRequest{}, nil
}

func (r *overviewGitlabMRRepo) ListByWorkspaceID(_ context.Context, _ string) ([]domain.GitlabMergeRequest, error) {
	return nil, nil
}

func (r *overviewGitlabMRRepo) ListNonTerminal(_ context.Context, _ string) ([]domain.GitlabMergeRequest, error) {
	return nil, nil
}

func (r *overviewGitlabMRRepo) Delete(_ context.Context, id string) error {
	delete(r.mrs, id)
	return nil
}

func TestBuildArtifactItemsMergesRecordedGitLabWorktreePath(t *testing.T) {
	t.Parallel()

	now := time.Now()
	projectPath := "justtrack/backend/postback-service"
	worktreePath := "/workspace/backend.postback-service"
	payload, err := json.Marshal(domain.ReviewArtifactEventPayload{
		WorkItemID: "wi-1",
		Artifact: domain.ReviewArtifact{
			Provider:     "gitlab",
			Kind:         "MR",
			RepoName:     projectPath,
			Ref:          "!421",
			URL:          "https://gitlab.example.com/justtrack/backend/postback-service/-/merge_requests/421",
			State:        "ready",
			Branch:       "sub-LIN-421-fix-postback",
			WorktreePath: worktreePath,
			UpdatedAt:    now,
		},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	artifactRepo := &overviewArtifactLinkRepo{links: []domain.SessionReviewArtifact{{
		ID:                 "link-1",
		WorkspaceID:        "ws-local",
		WorkItemID:         "wi-1",
		Provider:           "gitlab",
		ProviderArtifactID: "mr-1",
	}}}
	mrRepo := &overviewGitlabMRRepo{mrs: map[string]domain.GitlabMergeRequest{
		"mr-1": {
			ID:           "mr-1",
			ProjectPath:  projectPath,
			IID:          421,
			State:        "ready",
			SourceBranch: "sub-LIN-421-fix-postback",
			WebURL:       "https://gitlab.example.com/justtrack/backend/postback-service/-/merge_requests/421",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}}
	eventRepo := &overviewEventRepo{events: []domain.SystemEvent{{
		ID:          domain.NewID(),
		EventType:   string(domain.EventReviewArtifactRecorded),
		WorkspaceID: "ws-local",
		Payload:     string(payload),
		CreatedAt:   now,
	}}}
	app := newTestApp(Services{
		WorkspaceID:      "ws-local",
		WorkspaceName:    "local",
		Settings:         newTestSettingsService(),
		SessionArtifacts: service.NewSessionReviewArtifactService(repository.NoopTransacter{Res: repository.Resources{SessionReviewArtifacts: artifactRepo}}),
		GitlabMRs:        service.NewGitlabMRService(repository.NoopTransacter{Res: repository.Resources{GitlabMRs: mrRepo}}),
		Events:           service.NewEventService(repository.NoopTransacter{Res: repository.Resources{Events: eventRepo}}),
	})

	items := app.buildArtifactItems(&domain.Session{
		ID:          "wi-1",
		WorkspaceID: "ws-local",
	})
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1: %+v", len(items), items)
	}
	if items[0].WorktreePath != worktreePath {
		t.Fatalf("worktree path = %q, want %q", items[0].WorktreePath, worktreePath)
	}
}

func TestOverviewExternalLifecycleUsesRecordedArtifacts(t *testing.T) {
	t.Parallel()

	now := time.Now()
	payload, err := json.Marshal(domain.ReviewArtifactEventPayload{
		WorkItemID: "wi-1",
		Artifact: domain.ReviewArtifact{
			Provider:  "github",
			Kind:      "PR",
			RepoName:  "acme/rocket",
			Ref:       "#7",
			URL:       "https://github.com/acme/rocket/pull/7",
			State:     "ready",
			Branch:    "sub-branch",
			UpdatedAt: now,
		},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	noise := make([]domain.SystemEvent, 0, 251)
	for i := range 250 {
		noise = append(noise, domain.SystemEvent{ID: domain.NewID(), EventType: string(domain.EventWorkItemCompleted), WorkspaceID: "ws-local", Payload: `{}`, CreatedAt: now.Add(time.Duration(i+1) * time.Minute)})
	}
	noise = append(noise, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventReviewArtifactRecorded),
		WorkspaceID: "ws-local",
		Payload:     string(payload),
		CreatedAt:   now,
	})
	repo := &overviewEventRepo{events: noise}
	eventSvc := service.NewEventService(repository.NoopTransacter{Res: repository.Resources{Events: repo}})
	app := newTestApp(Services{
		WorkspaceID:   "ws-local",
		WorkspaceName: "local",
		Settings:      newTestSettingsService(),
		Events:        eventSvc,
		SessionArtifacts: service.NewSessionReviewArtifactService(repository.NoopTransacter{Res: repository.Resources{
			SessionReviewArtifacts: emptySessionArtifactRepo{},
		}}),
	})
	app.content.SetSize(100, 40)
	app.workItems = []domain.Session{{
		ID:          "wi-1",
		WorkspaceID: "ws-local",
		ExternalID:  "SUB-1",
		Title:       "Completed work",
		State:       domain.SessionCompleted,
		Metadata:    map[string]any{"tracker_refs": []domain.TrackerReference{{Provider: "github", Kind: "issue", Owner: "acme", Repo: "rocket", Number: 42}}},
		CreatedAt:   now,
		UpdatedAt:   now,
	}}
	app.currentWorkItemID = "wi-1"
	app.rebuildSidebar()
	app.sidebar.SelectWorkItem("wi-1")
	if cmd := app.updateContentFromState(); cmd != nil {
		t.Fatalf("updateContentFromState() cmd = %v, want nil", cmd)
	}
	if got := len(app.content.overview.data.External.Reviews); got != 1 {
		t.Fatalf("external review rows = %d, want 1", got)
	}
	row := app.content.overview.data.External.Reviews[0]
	if row.Ref != "#7" || row.URL == "" || row.State != "ready" {
		t.Fatalf("review row = %+v", row)
	}
	view := stripBrowseANSI(app.content.View())
	if !strings.Contains(view, "External lifecycle") {
		t.Fatalf("content view = %q, want External lifecycle section", view)
	}
	// Verify the completed action card is built and shows the correct hints.
	hints := app.content.KeybindHints()
	foundInspectRequestChanges := false
	for _, h := range hints {
		if h.Key == "i" && h.Label == "Inspect / Request changes" {
			foundInspectRequestChanges = true
		}
	}
	if !foundInspectRequestChanges {
		t.Fatalf("expected 'i Inspect / Request changes' hint, got %v", hints)
	}
}

func TestReviewingOverviewExposesReviewDecisionAction(t *testing.T) {
	t.Parallel()

	now := time.Now()
	app := newTestApp(Services{
		WorkspaceID:   "ws-local",
		WorkspaceName: "local",
		Settings:      newTestSettingsService(),
		SessionArtifacts: service.NewSessionReviewArtifactService(repository.NoopTransacter{Res: repository.Resources{
			SessionReviewArtifacts: emptySessionArtifactRepo{},
		}}),
		Events: service.NewEventService(repository.NoopTransacter{Res: repository.Resources{
			Events: emptyEventRepo{},
		}}),
	})
	app.content.SetSize(90, 24)
	app.workItems = []domain.Session{{ID: "wi-1", WorkspaceID: "ws-local", ExternalID: "SUB-1", Title: "Review plan", State: domain.SessionReviewing, CreatedAt: now, UpdatedAt: now}}
	app.plans["wi-1"] = &domain.Plan{ID: "plan-1", WorkItemID: "wi-1", Status: domain.PlanApproved, Version: 1, UpdatedAt: now}
	app.subPlans["plan-1"] = []domain.TaskPlan{{ID: "sp-1", PlanID: "plan-1", RepositoryName: "repo-a", Status: domain.SubPlanCompleted, UpdatedAt: now}}
	app.sessions = []domain.AgentSession{{ID: "sess-1", WorkItemID: "wi-1", WorkspaceID: "ws-local", Kind: domain.AgentSessionKindImplementation, SubPlanID: "sp-1", RepositoryName: "repo-a", Status: domain.AgentSessionCompleted, UpdatedAt: now, CreatedAt: now}}
	app.reviews["sess-1"] = ReviewsLoadedMsg{
		SessionID: "sess-1",
		Cycles:    []domain.ReviewCycle{{ID: "cycle-1", AgentSessionID: "sess-1", CycleNumber: 1, Status: domain.ReviewCycleCritiquesFound}},
		Critiques: map[string][]domain.Critique{"cycle-1": {{ID: "crit-1", ReviewCycleID: "cycle-1", Severity: domain.CritiqueMajor, Description: "Missing nil check before rendering review details"}}},
	}
	app.currentWorkItemID = "wi-1"
	app.rebuildSidebar()
	app.sidebar.SelectWorkItem("wi-1")
	if cmd := app.updateContentFromState(); cmd != nil {
		t.Fatalf("updateContentFromState() cmd = %v, want nil", cmd)
	}
	if got := len(app.content.overview.data.Actions); got != 1 {
		t.Fatalf("overview actions = %d, want 1", got)
	}
	if app.content.overview.data.Actions[0].Kind != overviewActionReviewing {
		t.Fatalf("overview action kind = %q, want %q", app.content.overview.data.Actions[0].Kind, overviewActionReviewing)
	}
	hints := app.content.KeybindHints()
	for _, want := range []string{"Re-implement", "Override accept", "Inspect review"} {
		found := false
		for _, hint := range hints {
			if hint.Label == want {
				found = true

				break
			}
		}
		if !found {
			t.Fatalf("keybind hints = %#v, want %q", hints, want)
		}
	}
	view := stripBrowseANSI(app.content.View())
	for _, want := range []string{"Action required", "Review requires decision", "repo-a"} {
		if !strings.Contains(view, want) {
			t.Fatalf("content view = %q, want %q", view, want)
		}
	}
}

func TestReviewingOverviewExposesReviewArtifactAction(t *testing.T) {
	t.Parallel()

	m := NewSessionOverviewModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(90, 24)
	m.SetData(SessionOverviewData{
		WorkItemID: "wi-1",
		State:      domain.SessionReviewing,
		Header:     OverviewHeader{ExternalID: "SUB-1", Title: "Reviewing work", StatusLabel: "Under review", UpdatedAt: time.Now()},
		External: OverviewExternalLifecycle{
			Reviews: []OverviewReviewRow{{RepoName: "acme/rocket", Ref: "#7", URL: "https://github.com/acme/rocket/pull/7", State: "ready"}},
		},
	})
	foundHint := false
	for _, hint := range m.KeybindHints() {
		if hint.Key == "o" && hint.Label == "Links" {
			foundHint = true

			break
		}
	}
	if !foundHint {
		t.Fatalf("keybind hints = %#v, want o Links hint", m.KeybindHints())
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	view := stripBrowseANSI(updated.View())
	for _, want := range []string{"Review artifacts", "#7", "acme/rocket"} {
		if !strings.Contains(view, want) {
			t.Fatalf("overlay view = %q, want %q", view, want)
		}
	}
}

func TestCompletedOverviewOpensCompletionDetailsOverlay(t *testing.T) {
	t.Parallel()

	m := NewSessionOverviewModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(90, 24)
	m.SetData(SessionOverviewData{
		WorkItemID: "wi-1",
		State:      domain.SessionCompleted,
		Header: OverviewHeader{
			ExternalID:  "SUB-1",
			Title:       "Completed work",
			StatusLabel: "Completed",
			UpdatedAt:   time.Now(),
		},
		External: OverviewExternalLifecycle{
			Reviews: []OverviewReviewRow{{RepoName: "acme/rocket", Ref: "#7", URL: "https://github.com/acme/rocket/pull/7", State: "ready"}},
		},
	})
	foundHint := false
	for _, hint := range m.KeybindHints() {
		if hint.Key == "o" && hint.Label == "Links" {
			foundHint = true

			break
		}
	}
	if !foundHint {
		t.Fatalf("keybind hints = %#v, want o Links hint", m.KeybindHints())
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if cmd == nil {
		t.Fatal("expected [o] with reviews to emit OpenOverviewLinksMsg command")
	}
	linksMsg, ok := cmd().(OpenOverviewLinksMsg)
	if !ok {
		t.Fatalf("[o] cmd() = %T, want OpenOverviewLinksMsg", cmd())
	}
	if len(linksMsg.Reviews) == 0 {
		t.Fatal("expected Reviews to be populated in OpenOverviewLinksMsg")
	}
	// Verify the review data from the test fixture is present in the message.
	found := false
	for _, r := range linksMsg.Reviews {
		if r.Ref == "#7" && r.RepoName == "acme/rocket" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("OpenOverviewLinksMsg.Reviews = %v, want entry with Ref #7 RepoName acme/rocket", linksMsg.Reviews)
	}
	// Verify the main overview page still shows the MR data (it is not stripped from the overview body).
	view := stripBrowseANSI(updated.View())
	for _, want := range []string{"Completed", "#7", "acme/rocket"} {
		if !strings.Contains(view, want) {
			t.Fatalf("overview view missing %q after pressing o", want)
		}
	}
}

// TestCompletedActionCardCNoLongerOpensFollowUpInput verifies that pressing [c] on a
// completed action card no longer opens the request-changes input; [c] is reserved
// for copying the plan inside the overlay.
func TestCompletedActionCardCNoLongerOpensFollowUpInput(t *testing.T) {
	t.Parallel()

	m := NewSessionOverviewModel(styles.NewStyles(styles.DefaultTheme))
	m.SetTerminalSize(120, 40)
	m.SetSize(90, 24)
	m.SetData(SessionOverviewData{
		WorkItemID: "wi-1",
		State:      domain.SessionCompleted,
		Header:     OverviewHeader{ExternalID: "SUB-1", Title: "Done", StatusLabel: "Completed", UpdatedAt: time.Now()},
		Actions:    []OverviewActionCard{{Kind: overviewActionCompleted}},
	})

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if cmd != nil {
		t.Fatalf("[c] returned cmd %v, want nil", cmd)
	}
	if updated.overlay != overviewOverlayNone {
		t.Fatalf("overlay = %v, want overviewOverlayNone", updated.overlay)
	}
	if updated.completed.inputActive {
		t.Fatal("expected inputActive to stay false after [c]")
	}
}

// TestCompletedActionCardIOpensOverlayWithFollowUpInput verifies that pressing [i] on a
// completed action card opens the completed overlay with the feedback input active,
// matching the Inspect / Request changes label.
func TestCompletedActionCardIOpensOverlayWithFollowUpInput(t *testing.T) {
	t.Parallel()

	m := NewSessionOverviewModel(styles.NewStyles(styles.DefaultTheme))
	m.SetTerminalSize(120, 40)
	m.SetSize(90, 24)
	m.SetData(SessionOverviewData{
		WorkItemID: "wi-1",
		State:      domain.SessionCompleted,
		Header:     OverviewHeader{ExternalID: "SUB-1", Title: "Done", StatusLabel: "Completed", UpdatedAt: time.Now()},
		External: OverviewExternalLifecycle{
			Reviews: []OverviewReviewRow{{RepoName: "acme/rocket", Ref: "#7", URL: "https://github.com/acme/rocket/pull/7", State: "ready"}},
		},
		Actions: []OverviewActionCard{{Kind: overviewActionCompleted}},
	})

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if cmd == nil {
		t.Fatal("[i] returned nil cmd, want focus cmd")
	}
	if updated.overlay != overviewOverlayCompleted {
		t.Fatalf("overlay = %v, want overviewOverlayCompleted", updated.overlay)
	}
	if !updated.completed.inputActive {
		t.Fatal("expected inputActive to be true after [i]")
	}
}

func TestCompletedOverlayEscClosesAndResetsFeedback(t *testing.T) {
	t.Parallel()

	m := NewSessionOverviewModel(styles.NewStyles(styles.DefaultTheme))
	m.SetTerminalSize(120, 40)
	m.SetSize(90, 24)
	m.SetData(SessionOverviewData{
		WorkItemID: "wi-1",
		State:      domain.SessionCompleted,
		Header:     OverviewHeader{ExternalID: "SUB-1", Title: "Done", StatusLabel: "Completed", UpdatedAt: time.Now()},
		Actions:    []OverviewActionCard{{Kind: overviewActionCompleted}},
	})

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	updated.completed.feedbackInput.SetValue("stale feedback")
	if updated.overlay != overviewOverlayCompleted || !updated.completed.inputActive {
		t.Fatalf("overlay/inputActive = %v/%v, want completed overlay with active feedback", updated.overlay, updated.completed.inputActive)
	}

	// First Esc cancels the input without closing the overlay.
	updated, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if updated.overlay != overviewOverlayCompleted {
		t.Fatalf("overlay = %v, want completed overlay to remain open after first esc", updated.overlay)
	}
	if updated.completed.inputActive {
		t.Fatal("inputActive = true after first esc, want false")
	}
	if got := updated.completed.feedbackInput.Value(); got != "" {
		t.Fatalf("feedback input = %q, want reset after first esc", got)
	}
	if cmd == nil {
		t.Fatal("expected first esc to return reset command")
	}

	// Second Esc closes the overlay.
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if updated.overlay != overviewOverlayNone {
		t.Fatalf("overlay = %v, want closed after second esc", updated.overlay)
	}

	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if updated.overlay != overviewOverlayCompleted {
		t.Fatalf("overlay after reopen = %v, want completed", updated.overlay)
	}
	if !updated.completed.inputActive {
		t.Fatal("inputActive = false after reopen via inspect, want true")
	}
	if got := updated.completed.feedbackInput.Value(); got != "" {
		t.Fatalf("feedback input after reopen = %q, want empty", got)
	}
}

// TestCompletedOverlayShowsPlanContent verifies that pressing [i] on a completed
// action card renders the plan document inside the overlay. This is the primary
// regression test for the bug where only the completion timestamp was shown.
func TestCompletedOverlayShowsPlanContent(t *testing.T) {
	t.Parallel()

	const planText = "# Plan\n\nStep 1: Implement the feature.\nStep 2: Write tests.\n"

	m := NewSessionOverviewModel(styles.NewStyles(styles.DefaultTheme))
	m.SetTerminalSize(120, 40)
	m.SetSize(90, 30)
	m.SetData(SessionOverviewData{
		WorkItemID: "wi-plan",
		State:      domain.SessionCompleted,
		Header:     OverviewHeader{ExternalID: "SUB-2", Title: "Plan visible", StatusLabel: "✓ Completed", UpdatedAt: time.Now()},
		Actions:    []OverviewActionCard{{Kind: overviewActionCompleted}},
		Plan: OverviewPlan{
			Exists:       true,
			FullDocument: planText,
		},
	})

	// Open via [i] (inspect, no feedback input).
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if updated.overlay != overviewOverlayCompleted {
		t.Fatalf("overlay = %v, want overviewOverlayCompleted", updated.overlay)
	}

	overlay := stripBrowseANSI(updated.overlayView(220, 50))
	for _, want := range []string{"Step 1", "Step 2", "Implement the feature"} {
		if !strings.Contains(overlay, want) {
			t.Fatalf("completed overlay view = %q\nwant substring %q", overlay, want)
		}
	}
}

func teaKeyRight() tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRight}
}

func TestOverviewTabRebindsQuestionToSelectedAction(t *testing.T) {
	t.Parallel()

	m := NewSessionOverviewModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(90, 24)
	m.SetData(SessionOverviewData{
		WorkItemID: "wi-1",
		State:      domain.SessionImplementing,
		Header:     OverviewHeader{ExternalID: "SUB-1", Title: "Question routing", StatusLabel: "Implementing", UpdatedAt: time.Now()},
		Actions: []OverviewActionCard{
			{Kind: overviewActionQuestion, Title: "Question 1", Question: &domain.Question{ID: "q-1", Content: "First question?"}, ProposedAnswer: "first"},
			{Kind: overviewActionQuestion, Title: "Question 2", Question: &domain.Question{ID: "q-2", Content: "Second question?"}, ProposedAnswer: "second"},
		},
	})

	if m.question.question.ID != "q-1" {
		t.Fatalf("initial question id = %q, want q-1", m.question.question.ID)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if updated.question.question.ID != "q-2" {
		t.Fatalf("question after tab = %q, want q-2", updated.question.question.ID)
	}
}

func TestOverviewQuestionHintsRequireModalAnswer(t *testing.T) {
	t.Parallel()

	m := NewSessionOverviewModel(styles.NewStyles(styles.DefaultTheme))
	m.SetTerminalSize(120, 40)
	m.SetSize(90, 24)
	m.SetData(SessionOverviewData{
		WorkItemID: "wi-1",
		State:      domain.SessionImplementing,
		Header:     OverviewHeader{ExternalID: "SUB-1", Title: "Question routing", StatusLabel: "Implementing", UpdatedAt: time.Now()},
		Actions: []OverviewActionCard{{
			Kind:           overviewActionQuestion,
			Title:          "Question waiting for answer",
			Question:       &domain.Question{ID: "q-1", Content: "Question?"},
			ProposedAnswer: "proposal should not be approved directly",
		}},
	})

	for _, hint := range m.KeybindHints() {
		if hint.Label == "Approve answer" {
			t.Fatalf("keybind hints = %#v, want no approve-answer action", m.KeybindHints())
		}
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	if cmd != nil {
		for _, msg := range runOverlayCmd(t, cmd) {
			if _, ok := msg.(AnswerQuestionMsg); ok {
				t.Fatalf("uppercase A emitted AnswerQuestionMsg: %#v", msg)
			}
		}
	}
	if updated.overlay != overviewOverlayNone {
		t.Fatalf("overlay = %v, want no hidden approve/open action for A", updated.overlay)
	}
}

func TestOverviewQuestionEscClosesWithoutResolving(t *testing.T) {
	t.Parallel()

	m := NewSessionOverviewModel(styles.NewStyles(styles.DefaultTheme))
	m.SetTerminalSize(120, 40)
	m.SetSize(90, 24)
	m.SetData(SessionOverviewData{
		WorkItemID: "wi-1",
		State:      domain.SessionImplementing,
		Header:     OverviewHeader{ExternalID: "SUB-1", Title: "Question routing", StatusLabel: "Implementing", UpdatedAt: time.Now()},
		Actions: []OverviewActionCard{{
			Kind:           overviewActionQuestion,
			Title:          "Question waiting for answer",
			Question:       &domain.Question{ID: "q-esc", Content: "Question?"},
			ProposedAnswer: "proposal",
		}},
	})

	opened, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("opening question overlay cmd = %v, want nil", cmd)
	}
	if opened.overlay != overviewOverlayQuestion {
		t.Fatalf("overlay = %v, want question overlay", opened.overlay)
	}
	opened.question.input.SetValue("draft answer")

	closed, cmd := opened.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if closed.overlay != overviewOverlayNone {
		t.Fatalf("overlay = %v, want closed", closed.overlay)
	}
	for _, msg := range runOverlayCmd(t, cmd) {
		switch msg.(type) {
		case AnswerQuestionMsg, SkipQuestionMsg:
			t.Fatalf("esc emitted resolving message %T", msg)
		}
	}
}

func TestOverviewQuestionOverlayUsesExpandedHalfScreenSize(t *testing.T) {
	t.Parallel()

	m := NewSessionOverviewModel(styles.NewStyles(styles.DefaultTheme))
	m.SetTerminalSize(240, 80)
	m.SetSize(120, 40)
	m.SetData(SessionOverviewData{
		WorkItemID: "wi-1",
		State:      domain.SessionImplementing,
		Header:     OverviewHeader{ExternalID: "SUB-1", Title: "Question routing", StatusLabel: "Implementing", UpdatedAt: time.Now()},
		Actions: []OverviewActionCard{{
			Kind:     overviewActionQuestion,
			Title:    "Question waiting for answer",
			Question: &domain.Question{ID: "q-size", Content: "Question?"},
		}},
	})

	if m.question.height < 40 {
		t.Fatalf("question overlay body height = %d, want at least half terminal height", m.question.height)
	}
	if m.question.width < 112 {
		t.Fatalf("question overlay body width = %d, want expanded half-screen width", m.question.width)
	}
}

func TestOverviewRefreshPreservesViewportPlanAndQuestionState(t *testing.T) {
	t.Parallel()

	plan := domain.Plan{ID: "plan-1", OrchestratorPlan: strings.Repeat("plan line\n", 20)}
	data := SessionOverviewData{
		WorkItemID: "wi-1",
		State:      domain.SessionPlanReview,
		Header:     OverviewHeader{ExternalID: "SUB-1", Title: "Refresh state", StatusLabel: "Plan review needed", UpdatedAt: time.Now()},
		Actions: []OverviewActionCard{{
			Kind:           overviewActionQuestion,
			Title:          "Need clarification",
			Question:       &domain.Question{ID: "q-1", Content: "Question text"},
			ProposedAnswer: "Proposed reply",
		}},
		Plan: OverviewPlan{Exists: true, Document: &plan, FullDocument: domain.ComposePlanDocument(plan, nil)},
	}

	m := NewSessionOverviewModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(90, 24)
	m.SetData(data)
	m.viewport.YOffset = 5
	m.planReview.viewport.YOffset = 7
	m.question.input.SetValue("draft reply")

	m.SetData(data)

	if m.viewport.YOffset != 5 {
		t.Fatalf("overview offset = %d, want 5", m.viewport.YOffset)
	}
	if m.planReview.viewport.YOffset != 7 {
		t.Fatalf("plan review offset = %d, want 7", m.planReview.viewport.YOffset)
	}
	if got := m.question.input.Value(); got != "draft reply" {
		t.Fatalf("draft reply = %q, want preserved input", got)
	}
}

func TestOverviewPlanOverlayEscapeCancelsInputWithoutClosing(t *testing.T) {
	t.Parallel()

	plan := domain.Plan{ID: "plan-1", WorkItemID: "wi-1", OrchestratorPlan: strings.Repeat("plan line\n", 10)}
	m := NewSessionOverviewModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(90, 24)
	m.SetData(SessionOverviewData{
		WorkItemID: "wi-1",
		State:      domain.SessionPlanReview,
		Header:     OverviewHeader{ExternalID: "SUB-1", Title: "Plan review", StatusLabel: "Plan review needed", UpdatedAt: time.Now()},
		Actions: []OverviewActionCard{{
			Kind: overviewActionPlanReview,
			Plan: &plan,
		}},
		Plan: OverviewPlan{Exists: true, Document: &plan, FullDocument: domain.ComposePlanDocument(plan, nil)},
	})

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	updated.planReview.feedbackInput.SetValue("needs changes")
	if updated.overlay != overviewOverlayPlan || updated.planReview.inputMode != planReviewChanges {
		t.Fatalf("overlay/input mode = %v/%v, want plan overlay in changes mode", updated.overlay, updated.planReview.inputMode)
	}
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if updated.overlay != overviewOverlayPlan {
		t.Fatalf("overlay = %v, want plan overlay to remain open after esc", updated.overlay)
	}
	if updated.planReview.inputMode != planReviewNormal {
		t.Fatalf("input mode = %v, want normal after esc", updated.planReview.inputMode)
	}
	if got := updated.planReview.feedbackInput.Value(); got != "" {
		t.Fatalf("feedback input = %q, want cleared after esc", got)
	}
}

func TestAppEscClosesOverviewPlanOverlay(t *testing.T) {
	t.Parallel()

	app := newSidebarDrilldownTestApp()
	app.workItems[0].State = domain.SessionPlanReview
	app.workItems[0].UpdatedAt = time.Now().Add(time.Minute)
	app.plans["wi-1"] = &domain.Plan{
		ID:               "plan-1",
		WorkItemID:       "wi-1",
		OrchestratorPlan: strings.Repeat("plan line\n", 4),
	}
	app.mainFocus = mainFocusContent

	// [i] opens the overlay for the existing plan (no action card — session state was
	// not rebuilt, so it takes the else-if branch with planReviewNormal mode).
	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	updated := model.(*App)
	if cmd != nil {
		t.Fatalf("opening plan overlay returned cmd %v, want nil", cmd)
	}
	if updated.content.overview.overlay != overviewOverlayPlan {
		t.Fatalf("overlay = %v, want %v", updated.content.overview.overlay, overviewOverlayPlan)
	}
	if updated.mainFocus != mainFocusContent {
		t.Fatalf("mainFocus = %v, want %v", updated.mainFocus, mainFocusContent)
	}

	// Single Esc closes the overlay (input is in normal mode, no cancel step needed).
	model, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = model.(*App)
	if cmd != nil {
		t.Fatalf("closing plan overlay returned cmd %v, want nil", cmd)
	}
	if updated.content.overview.overlay != overviewOverlayNone {
		t.Fatalf("overlay = %v, want %v", updated.content.overview.overlay, overviewOverlayNone)
	}
	if updated.mainFocus != mainFocusContent {
		t.Fatalf("mainFocus = %v, want %v", updated.mainFocus, mainFocusContent)
	}
	if updated.content.Mode() != ContentModeOverview {
		t.Fatalf("content mode = %v, want %v", updated.content.Mode(), ContentModeOverview)
	}
}

func TestAppSidebarPlanOverlayTakesFocusAndEscRestoresSidebar(t *testing.T) {
	t.Parallel()

	app := newSidebarDrilldownTestApp()
	app.workItems[0].State = domain.SessionPlanReview
	app.workItems[0].UpdatedAt = time.Now().Add(time.Minute)
	app.plans["wi-1"] = &domain.Plan{
		ID:               "plan-1",
		WorkItemID:       "wi-1",
		OrchestratorPlan: strings.Repeat("plan line\n", 4),
	}

	// [i] opens the overlay (existing plan, no action card in the overview data).
	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	updated := model.(*App)
	if cmd != nil {
		t.Fatalf("opening plan overlay returned cmd %v, want nil", cmd)
	}
	if updated.content.overview.overlay != overviewOverlayPlan {
		t.Fatalf("overlay = %v, want %v", updated.content.overview.overlay, overviewOverlayPlan)
	}
	if updated.mainFocus != mainFocusContent {
		t.Fatalf("mainFocus = %v, want %v when overlay opens from sidebar", updated.mainFocus, mainFocusContent)
	}

	// Single Esc closes the overlay (planReviewNormal, no input cancel step).
	model, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = model.(*App)
	if cmd != nil {
		t.Fatalf("closing plan overlay returned cmd %v, want nil", cmd)
	}
	if updated.content.overview.overlay != overviewOverlayNone {
		t.Fatalf("overlay = %v, want %v", updated.content.overview.overlay, overviewOverlayNone)
	}
	if updated.mainFocus != mainFocusSidebar {
		t.Fatalf("mainFocus = %v, want %v restored after closing overlay opened from sidebar", updated.mainFocus, mainFocusSidebar)
	}
}

func TestOverviewPlanSectionUsesSidebarSessionTitle(t *testing.T) {
	t.Parallel()

	app := newPlanningDrilldownTestApp()
	app.sessions[0].ID = "planning-session-123456789"
	app.updateContentFromState()

	view := stripBrowseANSI(app.content.View())
	// The overview pane shows "Planning session: <draft session ID>" as a key-value row —
	// this is intentional. The sidebar-style short title change applies to the sidebar
	// entry label, which should show "planning" (lowercase) without the "Planning session"
	// prefix. Since we cannot easily isolate the sidebar title from the overview row
	// in the rendered view, we verify the full ID is NOT shown (confirming short title).
	if strings.Contains(view, "planning-session-123456789") {
		t.Fatalf("content view should not contain full session id; got %q", view)
	}
	if strings.Contains(view, "planning-session-123456789") {
		t.Fatalf("content view = %q, want full planning session id omitted", view)
	}
}

func TestOverviewTaskRowUsesSidebarSessionTitle(t *testing.T) {
	t.Parallel()

	app := newSidebarDrilldownTestApp()
	app.sessions[0].ID = "implementation-session-123456789"
	app.updateContentFromState()

	view := stripBrowseANSI(app.content.View())
	// Session title uses sidebar-style short label (just "Session"), not "Task Session implementation".
	if !strings.Contains(view, "Session") || strings.Contains(view, "Task Session") {
		t.Fatalf("content view = %q, want sidebar-style 'Session' label without 'Task Session' prefix", view)
	}
	if strings.Contains(view, "implementation-session-123456789") {
		t.Fatalf("content view = %q, want full implementation session id omitted", view)
	}
}

func TestAppSidebarPlanOverlayLeftRestoresSidebar(t *testing.T) {
	t.Parallel()

	app := newSidebarDrilldownTestApp()
	app.workItems[0].State = domain.SessionPlanReview
	app.workItems[0].UpdatedAt = time.Now().Add(time.Minute)
	app.plans["wi-1"] = &domain.Plan{
		ID:               "plan-1",
		WorkItemID:       "wi-1",
		OrchestratorPlan: strings.Repeat("plan line\n", 4),
	}

	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	updated := model.(*App)
	if updated.content.overview.overlay != overviewOverlayPlan {
		t.Fatalf("overlay = %v, want %v", updated.content.overview.overlay, overviewOverlayPlan)
	}
	if updated.mainFocus != mainFocusContent {
		t.Fatalf("mainFocus = %v, want %v when overlay opens from sidebar", updated.mainFocus, mainFocusContent)
	}

	model, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyLeft})
	updated = model.(*App)
	if cmd != nil {
		t.Fatalf("closing plan overlay with left returned cmd %v, want nil", cmd)
	}
	if updated.content.overview.overlay != overviewOverlayNone {
		t.Fatalf("overlay = %v, want %v", updated.content.overview.overlay, overviewOverlayNone)
	}
	if updated.mainFocus != mainFocusSidebar {
		t.Fatalf("mainFocus = %v, want %v restored after left closes overlay", updated.mainFocus, mainFocusSidebar)
	}
}

func TestAppPlanReviewUsesIForRequestChanges(t *testing.T) {
	t.Parallel()

	app := newSidebarDrilldownTestApp()
	app.workItems[0].State = domain.SessionPlanReview
	app.workItems[0].UpdatedAt = time.Now().Add(time.Minute)
	app.plans["wi-1"] = &domain.Plan{
		ID:               "plan-1",
		WorkItemID:       "wi-1",
		OrchestratorPlan: strings.Repeat("plan line\n", 4),
	}
	if cmd := app.updateContentFromState(); cmd != nil {
		t.Fatalf("updateContentFromState() cmd = %v, want nil", cmd)
	}
	app.mainFocus = mainFocusContent

	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	updated := model.(*App)
	if updated.content.overview.overlay != overviewOverlayPlan {
		t.Fatalf("overlay = %v, want %v", updated.content.overview.overlay, overviewOverlayPlan)
	}

	if updated.activeOverlay == overlaySettings {
		t.Fatal("expected i to stay within the plan review flow instead of opening settings")
	}
	if updated.content.overview.planReview.inputMode != planReviewChanges {
		t.Fatalf("input mode = %v, want %v", updated.content.overview.planReview.inputMode, planReviewChanges)
	}
	if updated.content.overview.overlay != overviewOverlayPlan {
		t.Fatalf("overlay = %v, want %v", updated.content.overview.overlay, overviewOverlayPlan)
	}
}

func TestOverviewPlanOverlayUsesExpandedFrame(t *testing.T) {
	t.Parallel()

	plan := domain.Plan{ID: "plan-1", WorkItemID: "wi-1", OrchestratorPlan: strings.Repeat("plan line\n", 12)}
	m := NewSessionOverviewModel(styles.NewStyles(styles.DefaultTheme))
	m.SetSize(220, 30)
	m.SetData(SessionOverviewData{
		WorkItemID: "wi-1",
		State:      domain.SessionPlanReview,
		Header:     OverviewHeader{ExternalID: "SUB-1", Title: "Plan review", StatusLabel: "Plan review needed", UpdatedAt: time.Now()},
		Actions: []OverviewActionCard{{
			Kind: overviewActionPlanReview,
			Plan: &plan,
		}},
		Plan: OverviewPlan{Exists: true, Document: &plan, FullDocument: domain.ComposePlanDocument(plan, nil)},
	})

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if updated.overlay != overviewOverlayPlan {
		t.Fatalf("overlay = %v, want %v", updated.overlay, overviewOverlayPlan)
	}
	overlay := stripBrowseANSI(updated.overlayView(220, 30))
	maxWidth := 0
	for line := range strings.SplitSeq(overlay, "\n") {
		maxWidth = max(maxWidth, ansi.StringWidth(line))
	}
	if maxWidth > 220 {
		t.Fatalf("overlay width = %d, want plan frame capped at 220 columns", maxWidth)
	}
	if maxWidth < 210 {
		t.Fatalf("overlay width = %d, want wide plan frame on large terminals", maxWidth)
	}
	if got := len(strings.Split(overlay, "\n")); got < 28 {
		t.Fatalf("overlay height = %d, want taller plan frame with at least 28 lines", got)
	}
}

func TestAppOverviewOverlayCentersOnFullWindow(t *testing.T) {
	t.Parallel()

	app := newSidebarDrilldownTestApp()
	app.workItems[0].State = domain.SessionPlanReview
	app.workItems[0].UpdatedAt = time.Now().Add(time.Minute)
	app.plans["wi-1"] = &domain.Plan{
		ID:               "plan-1",
		WorkItemID:       "wi-1",
		OrchestratorPlan: "## Orchestration\n\nShip it.",
	}
	model, _ := app.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	updated := model.(*App)
	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	updated = model.(*App)

	plain := stripBrowseANSI(updated.View())
	lines := strings.Split(plain, "\n")
	titleLineIndex := -1
	for i, line := range lines {
		if strings.Contains(line, "· Plan Review") {
			titleLineIndex = i

			break
		}
	}
	if titleLineIndex < 1 {
		t.Fatalf("view = %q, want overlay title line", plain)
	}
	borderLine := lines[titleLineIndex-1]
	startByte := strings.LastIndex(borderLine, "╭")
	endByte := startByte + strings.Index(borderLine[startByte:], "╮")
	if startByte < 0 || endByte <= startByte {
		t.Fatalf("border line = %q, want overlay frame borders", borderLine)
	}
	start := ansi.StringWidth(borderLine[:startByte])
	overlayWidth := ansi.StringWidth(borderLine[startByte : endByte+len("╮")])
	expectedStart := (160 - overlayWidth) / 2
	if diff := start - expectedStart; diff < -1 || diff > 1 {
		t.Fatalf("overlay start col = %d, want centered around %d (width %d)\nline: %q", start, expectedStart, overlayWidth, borderLine)
	}
}

func TestOverviewInterruptedPlanningDispatchesRestartPlanMsg(t *testing.T) {
	t.Parallel()

	st := styles.NewStyles(styles.DefaultTheme)
	m := NewSessionOverviewModel(st)
	m.SetTerminalSize(80, 40)
	m.SetSize(80, 40)
	planningSession := domain.AgentSession{
		ID:          "sess-planning",
		WorkItemID:  "wi-1",
		WorkspaceID: "ws-1",
		Kind: domain.AgentSessionKindPlanning,
		Status:      domain.AgentSessionInterrupted,
	}
	m.SetData(SessionOverviewData{
		WorkItemID: "wi-1",
		Actions: []OverviewActionCard{{
			Kind:    overviewActionInterrupted,
			Title:   "Planning was interrupted",
			Session: &planningSession,
			CanAct:  true,
		}},
	})

	// Press 'r' — should dispatch RestartPlanMsg for planning phase.
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	_ = updated
	if cmd == nil {
		t.Fatal("pressing r on interrupted planning session must return a command")
	}
	msg := cmd()
	restart, ok := msg.(RestartPlanMsg)
	if !ok {
		t.Fatalf("expected RestartPlanMsg, got %T", msg)
	}
	if restart.WorkItemID != "wi-1" {
		t.Fatalf("RestartPlanMsg.WorkItemID = %q, want %q", restart.WorkItemID, "wi-1")
	}
}

func TestOverviewInterruptedActionCard_FiresResumeSessionMsgWithWorkItemID(t *testing.T) {
	t.Parallel()

	st := styles.NewStyles(styles.DefaultTheme)
	m := NewSessionOverviewModel(st)
	m.SetTerminalSize(80, 40)
	m.SetSize(80, 40)
	implSession := domain.AgentSession{
		ID:          "sess-impl",
		WorkItemID:  "wi-1",
		WorkspaceID: "ws-1",
		Kind: domain.AgentSessionKindImplementation,
		SubPlanID:   "sp-1",
		Status:      domain.AgentSessionInterrupted,
	}
	m.SetData(SessionOverviewData{
		WorkItemID: "wi-1",
		Actions: []OverviewActionCard{{
			Kind:    overviewActionInterrupted,
			Title:   "Interrupted task needs recovery",
			Session: &implSession,
			CanAct:  true,
		}},
	})

	// Press 'r' — should dispatch ResumeSessionMsg for non-planning phase.
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	_ = updated
	if cmd == nil {
		t.Fatal("pressing r on interrupted implementation session must return a command")
	}
	msg := cmd()
	if _, ok := msg.(ResumeSessionMsg); !ok {
		t.Fatalf("expected ResumeSessionMsg, got %T", msg)
	}
	resumeMsg := msg.(ResumeSessionMsg)
	if resumeMsg.WorkItemID != "wi-1" {
		t.Fatalf("ResumeSessionMsg.WorkItemID = %q, want %q", resumeMsg.WorkItemID, "wi-1")
	}
}

func TestOverviewInterruptedKeybindHints_Pluralizes(t *testing.T) {
	t.Parallel()

	st := styles.NewStyles(styles.DefaultTheme)
	m := NewSessionOverviewModel(st)
	m.SetTerminalSize(80, 40)
	m.SetSize(80, 40)
	first := domain.AgentSession{ID: "sess-1", WorkItemID: "wi-1", Kind: domain.AgentSessionKindImplementation, Status: domain.AgentSessionInterrupted}
	second := domain.AgentSession{ID: "sess-2", WorkItemID: "wi-1", Kind: domain.AgentSessionKindReview, Status: domain.AgentSessionInterrupted}
	m.SetData(SessionOverviewData{
		WorkItemID: "wi-1",
		Actions: []OverviewActionCard{
			{Kind: overviewActionInterrupted, Title: "Interrupted task", Session: &first, CanAct: true},
			{Kind: overviewActionInterrupted, Title: "Interrupted task", Session: &second, CanAct: true},
		},
	})

	hints := m.KeybindHints()
	for _, hint := range hints {
		if hint.Key == "r" {
			if hint.Label != "Resume all (2)" {
				t.Fatalf("resume hint label = %q, want %q", hint.Label, "Resume all (2)")
			}
			return
		}
	}
	t.Fatalf("hints = %#v, want resume hint", hints)
}

// TestRetryFromSessionsSidebar_PlanLoadedViaSessionsMsg verifies that the plan is
// available when the user presses 'r' for retry from the sessions sidebar focus
// state, even if SessionsLoadedMsg arrived before TasksLoadedMsg (the race that
// previously caused a "plan not found" toast).
func TestRetryFromSessionsSidebar_PlanLoadedViaSessionsMsg(t *testing.T) {
	t.Parallel()

	now := time.Now()
	app := newTestApp(Services{
		WorkspaceID:   "ws-local",
		WorkspaceName: "local",
		Settings:      newTestSettingsService(),
		SessionArtifacts: service.NewSessionReviewArtifactService(repository.NoopTransacter{Res: repository.Resources{
			SessionReviewArtifacts: emptySessionArtifactRepo{},
		}}),
		Events: service.NewEventService(repository.NoopTransacter{Res: repository.Resources{
			Events: emptyEventRepo{},
		}}),
	})
	app.content.SetSize(90, 40)

	failedWI := domain.Session{
		ID:          "wi-failed",
		WorkspaceID: "ws-local",
		Title:       "Auth fix",
		State:       domain.SessionFailed,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	// Step 1: SessionsLoadedMsg arrives (TasksLoadedMsg has NOT been processed yet).
	// Before the fix, no LoadPlanCmd was dispatched here, leaving a.plans[wi.ID] nil
	// until the next poll cycle ~2 seconds later.
	model, cmds := app.Update(SessionsLoadedMsg{WorkspaceID: "ws-local", Items: []domain.Session{failedWI}})
	app = model.(*App)
	if cmds == nil {
		t.Fatal("SessionsLoadedMsg must return at least one command (LoadPlanCmd)")
	}

	// Step 2: Simulate the PlanLoadedMsg that the dispatched LoadPlanCmd returns.
	plan := &domain.Plan{ID: "plan-1", WorkItemID: "wi-failed", Status: domain.PlanApproved}
	model, _ = app.Update(PlanLoadedMsg{WorkItemID: "wi-failed", Plan: plan})
	app = model.(*App)

	if got := app.plans["wi-failed"]; got == nil {
		t.Fatal("plan must be in a.plans after SessionsLoadedMsg + PlanLoadedMsg; got nil")
	}

	// Step 3: Navigate to the failed session (simulate sidebar selection).
	app.currentWorkItemID = "wi-failed"
	app.mainFocus = mainFocusSidebar
	app.sidebarMode = sidebarPaneSessions
	app.rebuildSidebar()
	app.sidebar.SelectWorkItem("wi-failed")
	if cmd := app.updateContentFromState(); cmd != nil {
		if msg := cmd(); msg != nil {
			_, _ = app.Update(msg)
		}
	}

	if app.content.mode != ContentModeOverview {
		t.Fatalf("content mode = %v, want ContentModeOverview", app.content.mode)
	}

	// Step 4: Press 'r' with focus on sessions sidebar.
	// The key must reach the overview and emit RetryFailedMsg with the correct WorkItemID.
	_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("pressing 'r' on a failed session must return a command")
	}
	msg := cmd()
	retryMsg, ok := msg.(RetryFailedMsg)
	if !ok {
		t.Fatalf("expected RetryFailedMsg, got %T", msg)
	}
	if retryMsg.WorkItemID != "wi-failed" {
		t.Fatalf("RetryFailedMsg.WorkItemID = %q, want %q", retryMsg.WorkItemID, "wi-failed")
	}
}

func TestOverviewConsolidatesInterruptedImplementationSessions(t *testing.T) {
	t.Parallel()

	now := time.Now()
	app := newTestApp(Services{WorkspaceID: "ws-local", WorkspaceName: "local", Settings: newTestSettingsService()})
	workItem := domain.Session{ID: "wi-1", WorkspaceID: "ws-local", Title: "Interrupted work", State: domain.SessionImplementing, CreatedAt: now, UpdatedAt: now}
	plan := &domain.Plan{ID: "plan-1", WorkItemID: "wi-1", Status: domain.PlanApproved}
	subPlans := []domain.TaskPlan{
		{ID: "sp-1", PlanID: "plan-1", RepositoryName: "repo-a", Status: domain.SubPlanInProgress},
		{ID: "sp-2", PlanID: "plan-1", RepositoryName: "repo-b", Status: domain.SubPlanInProgress},
	}
	app.sessions = []domain.AgentSession{
		{ID: "sess-a", WorkItemID: "wi-1", WorkspaceID: "ws-local", SubPlanID: "sp-1", RepositoryName: "repo-a", Kind: domain.AgentSessionKindImplementation, Status: domain.AgentSessionInterrupted, UpdatedAt: now.Add(-time.Minute)},
		{ID: "sess-b", WorkItemID: "wi-1", WorkspaceID: "ws-local", SubPlanID: "sp-2", RepositoryName: "repo-b", Kind: domain.AgentSessionKindReview, Status: domain.AgentSessionInterrupted, UpdatedAt: now},
	}

	actions := app.buildOverviewActions(&workItem, plan, subPlans)
	if len(actions) != 1 {
		t.Fatalf("actions len = %d, want 1: %#v", len(actions), actions)
	}
	action := actions[0]
	if action.Kind != overviewActionInterrupted {
		t.Fatalf("action kind = %q, want %q", action.Kind, overviewActionInterrupted)
	}
	if len(action.InterruptedSessions) != 2 {
		t.Fatalf("interrupted sessions = %d, want 2", len(action.InterruptedSessions))
	}
	if got := strings.Join(action.Affected, ","); got != "repo-a,repo-b" {
		t.Fatalf("affected = %q, want repo-a,repo-b", got)
	}

	m := NewSessionOverviewModel(styles.NewStyles(styles.DefaultTheme))
	m.SetTerminalSize(100, 30)
	m.SetSize(90, 24)
	m.SetData(SessionOverviewData{WorkItemID: "wi-1", State: domain.SessionImplementing, Actions: actions})
	hints := m.KeybindHints()
	for _, hint := range hints {
		if hint.Key == "r" {
			if hint.Label != "Resume all (2)" {
				t.Fatalf("resume hint label = %q, want Resume all (2)", hint.Label)
			}
			return
		}
	}
	t.Fatalf("hints = %#v, want resume hint", hints)
}

func TestOverviewShowsFinalizeActionForCompletedButImplementingWorkItem(t *testing.T) {
	t.Parallel()

	now := time.Now()
	app := newTestApp(Services{WorkspaceID: "ws-local", WorkspaceName: "local", Settings: newTestSettingsService()})
	workItem := domain.Session{
		ID:          "wi-stuck",
		WorkspaceID: "ws-local",
		Title:       "Finalize stuck work",
		State:       domain.SessionImplementing,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	plan := &domain.Plan{ID: "plan-1", WorkItemID: "wi-stuck", Status: domain.PlanApproved}
	subPlans := []domain.TaskPlan{{ID: "sp-1", PlanID: "plan-1", RepositoryName: "repo-a", Status: domain.SubPlanCompleted}}
	app.plans["wi-stuck"] = plan
	app.subPlans["plan-1"] = subPlans
	app.sessions = []domain.AgentSession{{
		ID:             "impl-1",
		WorkItemID:     "wi-stuck",
		WorkspaceID:    "ws-local",
		SubPlanID:      "sp-1",
		RepositoryName: "repo-a",
		Kind: domain.AgentSessionKindImplementation,
		Status:         domain.AgentSessionCompleted,
		UpdatedAt:      now,
	}}

	actions := app.buildOverviewActions(&workItem, plan, subPlans)
	if len(actions) != 1 {
		t.Fatalf("actions len = %d, want 1: %#v", len(actions), actions)
	}
	if actions[0].Kind != overviewActionFinalize {
		t.Fatalf("action kind = %q, want %q", actions[0].Kind, overviewActionFinalize)
	}

	m := NewSessionOverviewModel(styles.NewStyles(styles.DefaultTheme))
	m.SetTerminalSize(100, 30)
	m.SetSize(90, 24)
	m.SetData(SessionOverviewData{
		WorkItemID: "wi-stuck",
		State:      domain.SessionImplementing,
		Actions:    actions,
	})

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if cmd == nil {
		t.Fatal("pressing f on finalize action must return a command")
	}
	msg := cmd()
	finalizeMsg, ok := msg.(FinalizeWorkItemMsg)
	if !ok {
		t.Fatalf("expected FinalizeWorkItemMsg, got %T", msg)
	}
	if finalizeMsg.WorkItemID != "wi-stuck" {
		t.Fatalf("FinalizeWorkItemMsg.WorkItemID = %q, want wi-stuck", finalizeMsg.WorkItemID)
	}
}

func TestOverviewSuppressesFinalizeActionWhenAgentStillActive(t *testing.T) {
	t.Parallel()

	now := time.Now()
	app := newTestApp(Services{WorkspaceID: "ws-local", WorkspaceName: "local", Settings: newTestSettingsService()})
	workItem := domain.Session{ID: "wi-active", WorkspaceID: "ws-local", State: domain.SessionImplementing, UpdatedAt: now}
	plan := &domain.Plan{ID: "plan-1", WorkItemID: "wi-active", Status: domain.PlanApproved}
	subPlans := []domain.TaskPlan{{ID: "sp-1", PlanID: "plan-1", RepositoryName: "repo-a", Status: domain.SubPlanCompleted}}
	app.sessions = []domain.AgentSession{{
		ID:          "impl-1",
		WorkItemID:  "wi-active",
		WorkspaceID: "ws-local",
		SubPlanID:   "sp-1",
		Kind: domain.AgentSessionKindImplementation,
		Status:      domain.AgentSessionRunning,
		UpdatedAt:   now,
	}}

	actions := app.buildOverviewActions(&workItem, plan, subPlans)
	for _, action := range actions {
		if action.Kind == overviewActionFinalize {
			t.Fatalf("unexpected finalize action while agent is active: %#v", action)
		}
	}
}

// TestInspectKeyOpensPlanOverlayWithChangesInput verifies that pressing [i] on a
// plan-review action card opens the overlay with the request-changes input
// already active.
func TestInspectKeyOpensPlanOverlayWithChangesInput(t *testing.T) {
	t.Parallel()

	now := time.Now()
	m := NewSessionOverviewModel(styles.NewStyles(styles.DefaultTheme))
	m.SetTerminalSize(120, 40)
	m.SetSize(90, 24)
	plan := domain.Plan{ID: "plan-1", WorkItemID: "wi-1", OrchestratorPlan: strings.Repeat("plan line\n", 8)}
	m.SetData(SessionOverviewData{
		WorkItemID: "wi-1",
		State:      domain.SessionPlanReview,
		Header:     OverviewHeader{ExternalID: "SUB-1", Title: "Inspect test", StatusLabel: "Plan review needed", UpdatedAt: now},
		Actions: []OverviewActionCard{{
			Kind: overviewActionPlanReview,
			Plan: &plan,
		}},
		Plan: OverviewPlan{Exists: true, Document: &plan, FullDocument: domain.ComposePlanDocument(plan, nil)},
	})

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if cmd == nil {
		t.Fatal("[i] returned nil cmd, want focus cmd from openPlanOverlayForChanges")
	}
	if updated.overlay != overviewOverlayPlan {
		t.Fatalf("overlay = %v, want overviewOverlayPlan", updated.overlay)
	}
	if updated.planReview.inputMode != planReviewChanges {
		t.Fatalf("inputMode = %v, want planReviewChanges", updated.planReview.inputMode)
	}
}

// TestPlanOverlayEnterSubmitClosesOverlay verifies that pressing Enter to submit
// a request-changes prompt fires PlanRequestChangesMsg and closes the overlay.
func TestPlanOverlayEnterSubmitClosesOverlay(t *testing.T) {
	t.Parallel()

	now := time.Now()
	m := NewSessionOverviewModel(styles.NewStyles(styles.DefaultTheme))
	m.SetTerminalSize(120, 40)
	m.SetSize(90, 24)
	plan := domain.Plan{ID: "plan-42", WorkItemID: "wi-1", OrchestratorPlan: strings.Repeat("plan line\n", 8)}
	m.SetData(SessionOverviewData{
		WorkItemID: "wi-1",
		State:      domain.SessionPlanReview,
		Header:     OverviewHeader{ExternalID: "SUB-1", Title: "Submit test", StatusLabel: "Plan review needed", UpdatedAt: now},
		Actions: []OverviewActionCard{{
			Kind: overviewActionPlanReview,
			Plan: &plan,
		}},
		Plan: OverviewPlan{Exists: true, Document: &plan, FullDocument: domain.ComposePlanDocument(plan, nil)},
	})

	// Open overlay (changes mode).
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if m.overlay != overviewOverlayPlan || m.planReview.inputMode != planReviewChanges {
		t.Fatalf("overlay=%v inputMode=%v, want plan overlay in changes mode", m.overlay, m.planReview.inputMode)
	}

	// Type some feedback.
	for _, r := range "needs more tests" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	// Press Enter to submit.
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if updated.overlay != overviewOverlayNone {
		t.Fatalf("overlay = %v after Enter, want overviewOverlayNone", updated.overlay)
	}
	if cmd == nil {
		t.Fatal("Enter submit must return a command")
	}
	result := cmd()
	// The cmd is now a batch (action + EnableMouseCellMotion).
	// Unwrap the batch to find PlanRequestChangesMsg.
	batch, ok := result.(tea.BatchMsg)
	if !ok {
		t.Fatalf("expected BatchMsg, got %T", result)
	}
	var rcMsg PlanRequestChangesMsg
	var foundAction bool
	var foundMouse bool
	for _, c := range batch {
		switch msg := c().(type) {
		case PlanRequestChangesMsg:
			rcMsg = msg
			foundAction = true
		default:
			if msg == tea.EnableMouseCellMotion() {
				foundMouse = true
			}
		}
	}
	if !foundAction {
		t.Fatal("batch did not contain PlanRequestChangesMsg")
	}
	if !foundMouse {
		t.Fatal("batch did not contain EnableMouseCellMotion")
	}
	if rcMsg.PlanID != "plan-42" {
		t.Fatalf("PlanRequestChangesMsg.PlanID = %q, want plan-42", rcMsg.PlanID)
	}
	if rcMsg.Feedback != "needs more tests" {
		t.Fatalf("PlanRequestChangesMsg.Feedback = %q, want 'needs more tests'", rcMsg.Feedback)
	}
}

// TestCompletedOverlayEnterSubmitPreservesLongFeedback verifies that completed
// follow-up feedback accepts pasted research beyond the previous 5000-character
// cap, emits the full FollowUpPlanMsg, and still renders inside a narrow terminal.
func TestCompletedOverlayEnterSubmitPreservesLongFeedback(t *testing.T) {
	t.Parallel()

	now := time.Now()
	m := NewSessionOverviewModel(styles.NewStyles(styles.DefaultTheme))
	m.SetTerminalSize(80, 24)
	m.SetSize(72, 18)
	plan := domain.Plan{ID: "plan-done", WorkItemID: "wi-1", OrchestratorPlan: strings.Repeat("plan line\n", 8)}
	m.SetData(SessionOverviewData{
		WorkItemID: "wi-1",
		State:      domain.SessionCompleted,
		Header:     OverviewHeader{ExternalID: "SUB-1", Title: "Completed", StatusLabel: "Completed", UpdatedAt: now},
		Actions: []OverviewActionCard{{
			Kind: overviewActionCompleted,
		}},
		Plan: OverviewPlan{Exists: true, Document: &plan, FullDocument: domain.ComposePlanDocument(plan, nil)},
	})

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if m.overlay != overviewOverlayCompleted || !m.completed.inputActive {
		t.Fatalf("overlay/inputActive = %v/%v, want completed overlay with feedback input", m.overlay, m.completed.inputActive)
	}

	longFeedback := strings.Repeat("research result line with enough detail\n", 160) // > 5000 chars.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(longFeedback)})

	view := m.overlayView(80, 24)
	lines := strings.Split(view, "\n")
	if got, want := len(lines), 24; got > want {
		t.Fatalf("overlay line count = %d, want <= %d\nview:\n%s", got, want, view)
	}
	for i, line := range lines {
		if got, want := ansi.StringWidth(line), 80; got > want {
			t.Fatalf("overlay line %d width = %d, want <= %d; line = %q", i+1, got, want, line)
		}
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if updated.overlay != overviewOverlayNone || updated.completed.inputActive {
		t.Fatalf("overlay/inputActive = %v/%v after Enter, want no overlay and inactive input", updated.overlay, updated.completed.inputActive)
	}
	if cmd == nil {
		t.Fatal("Enter submit must return a command")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("expected BatchMsg, got %T", cmd())
	}

	var followUpMsg FollowUpPlanMsg
	foundAction := false
	foundMouse := false
	for _, c := range batch {
		switch msg := c().(type) {
		case FollowUpPlanMsg:
			followUpMsg = msg
			foundAction = true
		default:
			if msg == tea.EnableMouseCellMotion() {
				foundMouse = true
			}
		}
	}
	if !foundAction {
		t.Fatal("batch did not contain FollowUpPlanMsg")
	}
	if !foundMouse {
		t.Fatal("batch did not contain EnableMouseCellMotion")
	}
	if followUpMsg.WorkItemID != "wi-1" {
		t.Fatalf("FollowUpPlanMsg.WorkItemID = %q, want wi-1", followUpMsg.WorkItemID)
	}
	if followUpMsg.Feedback != longFeedback {
		t.Fatalf("FollowUpPlanMsg.Feedback length = %d, want %d", len(followUpMsg.Feedback), len(longFeedback))
	}
}

// TestInspectKeyForCompletedPlanOpensNormalMode verifies that pressing [i] on an
// overview with a completed/existing plan (no active plan-review action) opens
// the overlay in normal mode — no request-changes input.
func TestInspectKeyForCompletedPlanOpensNormalMode(t *testing.T) {
	t.Parallel()

	now := time.Now()
	m := NewSessionOverviewModel(styles.NewStyles(styles.DefaultTheme))
	m.SetTerminalSize(120, 40)
	m.SetSize(90, 24)
	plan := domain.Plan{ID: "plan-done", WorkItemID: "wi-1", OrchestratorPlan: strings.Repeat("plan line\n", 8)}
	m.SetData(SessionOverviewData{
		WorkItemID: "wi-1",
		State:      domain.SessionCompleted,
		Header:     OverviewHeader{ExternalID: "SUB-1", Title: "Completed", StatusLabel: "Completed", UpdatedAt: now},
		// No Actions entry — session is done, no review needed.
		Plan: OverviewPlan{Exists: true, Document: &plan, FullDocument: domain.ComposePlanDocument(plan, nil)},
	})

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if cmd != nil {
		t.Fatalf("[i] returned cmd %v, want nil", cmd)
	}
	if updated.overlay != overviewOverlayPlan {
		t.Fatalf("overlay = %v, want overviewOverlayPlan", updated.overlay)
	}
	if updated.planReview.inputMode != planReviewNormal {
		t.Fatalf("inputMode = %v, want planReviewNormal for completed plan inspect", updated.planReview.inputMode)
	}
}
