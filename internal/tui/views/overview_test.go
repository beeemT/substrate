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
	updated := model.(App)
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
	app := NewApp(Services{WorkspaceID: "ws-local", WorkspaceName: "local", Settings: &SettingsService{}})
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
	for _, want := range []string{"Approve", "Changes", "Reject", "Inspect"} {
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

func TestOverviewUsesDurableSourceSummariesWhenAvailable(t *testing.T) {
	t.Parallel()

	now := time.Now()
	app := NewApp(Services{WorkspaceID: "ws-local", WorkspaceName: "local", Settings: &SettingsService{}})
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
	app := NewApp(Services{WorkspaceID: "ws-local", WorkspaceName: "local", Settings: &SettingsService{}, Events: repo})
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
	for _, want := range []string{"External lifecycle", "Review artifacts", "#7"} {
		if !strings.Contains(view, want) {
			t.Fatalf("content view = %q, want %q", view, want)
		}
	}
}

func TestReviewingOverviewExposesReviewDecisionAction(t *testing.T) {
	t.Parallel()

	now := time.Now()
	app := NewApp(Services{WorkspaceID: "ws-local", WorkspaceName: "local", Settings: &SettingsService{}})
	app.content.SetSize(90, 24)
	app.workItems = []domain.Session{{ID: "wi-1", WorkspaceID: "ws-local", ExternalID: "SUB-1", Title: "Review plan", State: domain.SessionReviewing, CreatedAt: now, UpdatedAt: now}}
	app.plans["wi-1"] = &domain.Plan{ID: "plan-1", WorkItemID: "wi-1", Status: domain.PlanApproved, Version: 1, UpdatedAt: now}
	app.subPlans["plan-1"] = []domain.TaskPlan{{ID: "sp-1", PlanID: "plan-1", RepositoryName: "repo-a", Status: domain.SubPlanCompleted, UpdatedAt: now}}
	app.sessions = []domain.Task{{ID: "sess-1", WorkItemID: "wi-1", WorkspaceID: "ws-local", Phase: domain.TaskPhaseImplementation, SubPlanID: "sp-1", RepositoryName: "repo-a", Status: domain.AgentSessionCompleted, UpdatedAt: now, CreatedAt: now}}
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
		if hint.Label == "Review artifacts" {
			foundHint = true

			break
		}
	}
	if !foundHint {
		t.Fatalf("keybind hints = %#v, want review-artifacts hint", m.KeybindHints())
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
		if hint.Label == "Review artifacts" {
			foundHint = true

			break
		}
	}
	if !foundHint {
		t.Fatalf("keybind hints = %#v, want review-artifacts hint", m.KeybindHints())
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	view := stripBrowseANSI(updated.View())
	for _, want := range []string{"Completed", "#7", "acme/rocket"} {
		if !strings.Contains(view, want) {
			t.Fatalf("overlay view = %q, want %q", view, want)
		}
	}
	opened, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected completion overlay enter to emit open-url command")
	}
	msg := cmd()
	openMsg, ok := msg.(OpenExternalURLMsg)
	if !ok {
		t.Fatalf("cmd() message = %T, want OpenExternalURLMsg", msg)
	}
	if openMsg.URL != "https://github.com/acme/rocket/pull/7" {
		t.Fatalf("open url = %q, want review artifact url", openMsg.URL)
	}
	if opened.completed.selectedLink != 0 {
		t.Fatalf("selected link = %d, want 0", opened.completed.selectedLink)
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

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
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

	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	updated := model.(App)
	if cmd != nil {
		t.Fatalf("opening plan overlay returned cmd %v, want nil", cmd)
	}
	if updated.content.overview.overlay != overviewOverlayPlan {
		t.Fatalf("overlay = %v, want %v", updated.content.overview.overlay, overviewOverlayPlan)
	}
	if updated.mainFocus != mainFocusContent {
		t.Fatalf("mainFocus = %v, want %v", updated.mainFocus, mainFocusContent)
	}

	model, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = model.(App)
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

	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	updated := model.(App)
	if cmd != nil {
		t.Fatalf("opening plan overlay returned cmd %v, want nil", cmd)
	}
	if updated.content.overview.overlay != overviewOverlayPlan {
		t.Fatalf("overlay = %v, want %v", updated.content.overview.overlay, overviewOverlayPlan)
	}
	if updated.mainFocus != mainFocusContent {
		t.Fatalf("mainFocus = %v, want %v when overlay opens from sidebar", updated.mainFocus, mainFocusContent)
	}

	model, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = model.(App)
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
	if !strings.Contains(view, "Planning session planning") {
		t.Fatalf("content view = %q, want sidebar-style planning session title", view)
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
	if !strings.Contains(view, "Task: Session implemen") {
		t.Fatalf("content view = %q, want sidebar-style task title in overview", view)
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
	updated := model.(App)
	if updated.content.overview.overlay != overviewOverlayPlan {
		t.Fatalf("overlay = %v, want %v", updated.content.overview.overlay, overviewOverlayPlan)
	}
	if updated.mainFocus != mainFocusContent {
		t.Fatalf("mainFocus = %v, want %v when overlay opens from sidebar", updated.mainFocus, mainFocusContent)
	}

	model, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyLeft})
	updated = model.(App)
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

func TestAppPlanReviewUsesCForRequestChangesInsteadOfSettings(t *testing.T) {
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

	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	updated := model.(App)
	if updated.content.overview.overlay != overviewOverlayPlan {
		t.Fatalf("overlay = %v, want %v", updated.content.overview.overlay, overviewOverlayPlan)
	}

	model, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	updated = model.(App)
	if cmd != nil {
		t.Fatalf("cmd = %v, want nil when requesting plan changes", cmd)
	}
	if updated.activeOverlay == overlaySettings {
		t.Fatal("expected c to stay within the plan review flow instead of opening settings")
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
	updated := model.(App)
	model, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	updated = model.(App)

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
	endByte := strings.LastIndex(borderLine, "╮")
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
