package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"

	"github.com/beeemT/go-atomic/generic"
	goatomicsqlx "github.com/beeemT/go-atomic/generic/sqlx"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
	reposqlite "github.com/beeemT/substrate/internal/repository/sqlite"
	"github.com/beeemT/substrate/migrations"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func setupDB(t *testing.T) *sqlx.DB {
	t.Helper()
	db, err := sqlx.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.ExecContext(context.Background(), pragma); err != nil {
			t.Fatalf("pragma %s: %v", pragma, err)
		}
	}

	if err := repository.Migrate(context.Background(), db, migrations.FS); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	return db
}

func beginTx(t *testing.T, db *sqlx.DB) *sqlx.Tx {
	t.Helper()
	tx, err := db.Beginx()
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })

	return tx
}

func now() time.Time { return time.Now().UTC().Truncate(time.Millisecond) }

func makeWorkspace(t *testing.T, tx generic.SQLXRemote) domain.Workspace {
	t.Helper()
	ws := domain.Workspace{
		ID:        domain.NewID(),
		Name:      "test-workspace",
		RootPath:  "/tmp/test-ws",
		Status:    domain.WorkspaceReady,
		CreatedAt: now(),
		UpdatedAt: now(),
	}
	if err := reposqlite.NewWorkspaceRepo(tx).Create(context.Background(), ws); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	return ws
}

func makeWorkItem(t *testing.T, tx generic.SQLXRemote, wsID string) domain.Session {
	t.Helper()
	item := domain.Session{
		ID:          domain.NewID(),
		WorkspaceID: wsID,
		Source:      "github",
		Title:       "test-item",
		State:       domain.SessionIngested,
		CreatedAt:   now(),
		UpdatedAt:   now(),
	}
	if err := reposqlite.NewSessionRepo(tx).Create(context.Background(), item); err != nil {
		t.Fatalf("create work item: %v", err)
	}

	return item
}

func makePlan(t *testing.T, tx generic.SQLXRemote, wiID string) domain.Plan {
	t.Helper()
	plan := domain.Plan{
		ID:               domain.NewID(),
		WorkItemID:       wiID,
		OrchestratorPlan: "plan-content",
		Status:           domain.PlanDraft,
		Version:          1,
		CreatedAt:        now(),
		UpdatedAt:        now(),
	}
	if err := reposqlite.NewPlanRepo(tx).Create(context.Background(), plan); err != nil {
		t.Fatalf("create plan: %v", err)
	}

	return plan
}

func makeSubPlan(t *testing.T, tx generic.SQLXRemote, planID string) domain.TaskPlan {
	t.Helper()
	sp := domain.TaskPlan{
		ID:             domain.NewID(),
		PlanID:         planID,
		RepositoryName: "test-repo",
		Content:        "sub-plan-content",
		Order:          1,
		Status:         domain.SubPlanPending,
		CreatedAt:      now(),
		UpdatedAt:      now(),
	}
	if err := reposqlite.NewSubPlanRepo(tx).Create(context.Background(), sp); err != nil {
		t.Fatalf("create sub-plan: %v", err)
	}

	return sp
}

func makeInstance(t *testing.T, tx generic.SQLXRemote, wsID string) domain.SubstrateInstance {
	t.Helper()
	inst := domain.SubstrateInstance{
		ID:            domain.NewID(),
		WorkspaceID:   wsID,
		PID:           12345,
		Hostname:      "localhost",
		LastHeartbeat: now(),
		StartedAt:     now(),
	}
	if err := reposqlite.NewInstanceRepo(tx).Create(context.Background(), inst); err != nil {
		t.Fatalf("create instance: %v", err)
	}

	return inst
}

func makeSession(t *testing.T, tx generic.SQLXRemote, spID, wsID string) domain.Task {
	t.Helper()
	var workItemID string
	if err := tx.GetContext(context.Background(), &workItemID, `SELECT p.work_item_id FROM sub_plans sp JOIN plans p ON p.id = sp.plan_id WHERE sp.id = ?`, spID); err != nil {
		t.Fatalf("lookup work item for session helper: %v", err)
	}
	s := domain.Task{
		ID:             domain.NewID(),
		WorkItemID:     workItemID,
		SubPlanID:      spID,
		WorkspaceID:    wsID,
		Phase:          domain.TaskPhaseImplementation,
		RepositoryName: "test-repo",
		HarnessName:    "claude",
		WorktreePath:   "/tmp/worktree",
		Status:         domain.AgentSessionPending,
		CreatedAt:      now(),
		UpdatedAt:      now(),
	}
	if err := reposqlite.NewTaskRepo(tx).Create(context.Background(), s); err != nil {
		t.Fatalf("create session: %v", err)
	}

	return s
}

// ---------------------------------------------------------------------------
// Workspace CRUD
// ---------------------------------------------------------------------------

func TestWorkspaceCRUD(t *testing.T) {
	db := setupDB(t)
	tx := beginTx(t, db)
	repo := reposqlite.NewWorkspaceRepo(tx)
	ctx := context.Background()

	ws := makeWorkspace(t, tx)

	got, err := repo.Get(ctx, ws.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != ws.Name {
		t.Errorf("name = %q, want %q", got.Name, ws.Name)
	}

	ws.Name = "updated"
	ws.UpdatedAt = now()
	if err := repo.Update(ctx, ws); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = repo.Get(ctx, ws.ID)
	if got.Name != "updated" {
		t.Errorf("name after update = %q, want %q", got.Name, "updated")
	}

	if err := repo.Delete(ctx, ws.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = repo.Get(ctx, ws.ID)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected sql.ErrNoRows after delete, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// WorkItem CRUD + List
// ---------------------------------------------------------------------------

func TestWorkItemCRUD(t *testing.T) {
	db := setupDB(t)
	tx := beginTx(t, db)
	ctx := context.Background()

	ws := makeWorkspace(t, tx)
	repo := reposqlite.NewSessionRepo(tx)

	item := domain.Session{
		ID:          domain.NewID(),
		WorkspaceID: ws.ID,
		Source:      "github",
		Title:       "my-item",
		Description: "desc",
		State:       domain.SessionIngested,
		Labels:      []string{"bug", "p0"},
		Metadata:    map[string]any{"key": "val"},
		CreatedAt:   now(),
		UpdatedAt:   now(),
	}
	if err := repo.Create(ctx, item); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := repo.Get(ctx, item.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Title != item.Title {
		t.Errorf("title = %q, want %q", got.Title, item.Title)
	}
	if len(got.Labels) != 2 || got.Labels[0] != "bug" {
		t.Errorf("labels = %v, want [bug p0]", got.Labels)
	}

	item.State = domain.SessionPlanning
	item.UpdatedAt = now()
	if err := repo.Update(ctx, item); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = repo.Get(ctx, item.ID)
	if got.State != domain.SessionPlanning {
		t.Errorf("state = %q, want %q", got.State, domain.SessionPlanning)
	}

	if err := repo.Delete(ctx, item.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestWorkItemListFilter(t *testing.T) {
	db := setupDB(t)
	tx := beginTx(t, db)
	ctx := context.Background()

	ws := makeWorkspace(t, tx)
	repo := reposqlite.NewSessionRepo(tx)

	for range 3 {
		makeWorkItem(t, tx, ws.ID)
	}

	wsID := ws.ID
	state := domain.SessionIngested
	items, err := repo.List(ctx, repository.SessionFilter{
		WorkspaceID: &wsID,
		State:       &state,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 3 {
		t.Errorf("len = %d, want 3", len(items))
	}
}

func TestWorkItemListEmpty(t *testing.T) {
	db := setupDB(t)
	tx := beginTx(t, db)
	ctx := context.Background()

	repo := reposqlite.NewSessionRepo(tx)
	items, err := repo.List(ctx, repository.SessionFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("len = %d, want 0", len(items))
	}
}

// ---------------------------------------------------------------------------
// Plan CRUD
// ---------------------------------------------------------------------------

func TestPlanCRUD(t *testing.T) {
	db := setupDB(t)
	tx := beginTx(t, db)
	ctx := context.Background()

	ws := makeWorkspace(t, tx)
	wi := makeWorkItem(t, tx, ws.ID)
	repo := reposqlite.NewPlanRepo(tx)

	plan := makePlan(t, tx, wi.ID)

	got, err := repo.Get(ctx, plan.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.OrchestratorPlan != plan.OrchestratorPlan {
		t.Errorf("plan content mismatch")
	}

	gotByWI, err := repo.GetByWorkItemID(ctx, wi.ID)
	if err != nil {
		t.Fatalf("get by work item: %v", err)
	}
	if gotByWI.ID != plan.ID {
		t.Errorf("id mismatch on get by work item")
	}

	plan.Status = domain.PlanApproved
	plan.UpdatedAt = now()
	if err := repo.Update(ctx, plan); err != nil {
		t.Fatalf("update: %v", err)
	}

	if err := repo.Delete(ctx, plan.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

// ---------------------------------------------------------------------------
// SubPlan CRUD
// ---------------------------------------------------------------------------

func TestSubPlanCRUD(t *testing.T) {
	db := setupDB(t)
	tx := beginTx(t, db)
	ctx := context.Background()

	ws := makeWorkspace(t, tx)
	wi := makeWorkItem(t, tx, ws.ID)
	plan := makePlan(t, tx, wi.ID)
	repo := reposqlite.NewSubPlanRepo(tx)

	sp := makeSubPlan(t, tx, plan.ID)

	got, err := repo.Get(ctx, sp.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.RepositoryName != sp.RepositoryName {
		t.Errorf("repo name mismatch")
	}

	list, err := repo.ListByPlanID(ctx, plan.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("len = %d, want 1", len(list))
	}

	sp.Status = domain.SubPlanInProgress
	sp.UpdatedAt = now()
	if err := repo.Update(ctx, sp); err != nil {
		t.Fatalf("update: %v", err)
	}

	if err := repo.Delete(ctx, sp.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Instance CRUD
// ---------------------------------------------------------------------------

func TestInstanceCRUD(t *testing.T) {
	db := setupDB(t)
	tx := beginTx(t, db)
	ctx := context.Background()

	ws := makeWorkspace(t, tx)
	repo := reposqlite.NewInstanceRepo(tx)

	inst := makeInstance(t, tx, ws.ID)

	got, err := repo.Get(ctx, inst.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.PID != inst.PID {
		t.Errorf("pid = %d, want %d", got.PID, inst.PID)
	}

	list, err := repo.ListByWorkspaceID(ctx, ws.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("len = %d, want 1", len(list))
	}

	inst.Hostname = "other-host"
	if err := repo.Update(ctx, inst); err != nil {
		t.Fatalf("update: %v", err)
	}

	if err := repo.Delete(ctx, inst.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Session CRUD
// ---------------------------------------------------------------------------

func TestSessionCRUD(t *testing.T) {
	db := setupDB(t)
	tx := beginTx(t, db)
	ctx := context.Background()

	ws := makeWorkspace(t, tx)
	wi := makeWorkItem(t, tx, ws.ID)
	plan := makePlan(t, tx, wi.ID)
	sp := makeSubPlan(t, tx, plan.ID)
	repo := reposqlite.NewTaskRepo(tx)

	sess := makeSession(t, tx, sp.ID, ws.ID)

	got, err := repo.Get(ctx, sess.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.HarnessName != sess.HarnessName {
		t.Errorf("harness = %q, want %q", got.HarnessName, sess.HarnessName)
	}

	listByWorkItem, err := repo.ListByWorkItemID(ctx, wi.ID)
	if err != nil {
		t.Fatalf("list by work item: %v", err)
	}
	if len(listByWorkItem) != 1 {
		t.Errorf("work item len = %d, want 1", len(listByWorkItem))
	}

	list, err := repo.ListBySubPlanID(ctx, sp.ID)
	if err != nil {
		t.Fatalf("list by sub-plan: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("len = %d, want 1", len(list))
	}

	listByWs, err := repo.ListByWorkspaceID(ctx, ws.ID)
	if err != nil {
		t.Fatalf("list by workspace: %v", err)
	}
	if len(listByWs) != 1 {
		t.Errorf("len = %d, want 1", len(listByWs))
	}

	sess.Status = domain.AgentSessionRunning
	startedAt := now()
	sess.StartedAt = &startedAt
	sess.UpdatedAt = now()
	if err := repo.Update(ctx, sess); err != nil {
		t.Fatalf("update: %v", err)
	}

	if err := repo.Delete(ctx, sess.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestSessionDeleteCascadesDependents(t *testing.T) {
	db := setupDB(t)
	tx := beginTx(t, db)
	ctx := context.Background()

	ws := makeWorkspace(t, tx)
	wi := makeWorkItem(t, tx, ws.ID)
	plan := makePlan(t, tx, wi.ID)
	sp := makeSubPlan(t, tx, plan.ID)
	sess := makeSession(t, tx, sp.ID, ws.ID)

	sessionRepo := reposqlite.NewTaskRepo(tx)
	reviewRepo := reposqlite.NewReviewRepo(tx)
	questionRepo := reposqlite.NewQuestionRepo(tx)

	reviewCycle := domain.ReviewCycle{
		ID:              domain.NewID(),
		AgentSessionID:  sess.ID,
		CycleNumber:     1,
		ReviewerHarness: "claude-reviewer",
		Status:          domain.ReviewCycleReviewing,
		CreatedAt:       now(),
		UpdatedAt:       now(),
	}
	if err := reviewRepo.CreateCycle(ctx, reviewCycle); err != nil {
		t.Fatalf("create review cycle: %v", err)
	}

	critique := domain.Critique{
		ID:            domain.NewID(),
		ReviewCycleID: reviewCycle.ID,
		Severity:      domain.CritiqueMajor,
		Description:   "needs fix",
		Status:        domain.CritiqueOpen,
		CreatedAt:     now(),
	}
	if err := reviewRepo.CreateCritique(ctx, critique); err != nil {
		t.Fatalf("create critique: %v", err)
	}

	question := domain.Question{
		ID:             domain.NewID(),
		AgentSessionID: sess.ID,
		Content:        "what next?",
		Status:         domain.QuestionPending,
		CreatedAt:      now(),
	}
	if err := questionRepo.Create(ctx, question); err != nil {
		t.Fatalf("create question: %v", err)
	}

	if err := sessionRepo.Delete(ctx, sess.ID); err != nil {
		t.Fatalf("delete session: %v", err)
	}

	if _, err := sessionRepo.Get(ctx, sess.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("session get error = %v, want %v", err, sql.ErrNoRows)
	}
	if _, err := reviewRepo.GetCycle(ctx, reviewCycle.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("review cycle get error = %v, want %v", err, sql.ErrNoRows)
	}
	if _, err := reviewRepo.GetCritique(ctx, critique.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("critique get error = %v, want %v", err, sql.ErrNoRows)
	}
	if _, err := questionRepo.Get(ctx, question.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("question get error = %v, want %v", err, sql.ErrNoRows)
	}

	cycles, err := reviewRepo.ListCyclesBySessionID(ctx, sess.ID)
	if err != nil {
		t.Fatalf("list review cycles: %v", err)
	}
	if len(cycles) != 0 {
		t.Fatalf("review cycles len = %d, want 0", len(cycles))
	}

	critiques, err := reviewRepo.ListCritiquesByReviewCycleID(ctx, reviewCycle.ID)
	if err != nil {
		t.Fatalf("list critiques: %v", err)
	}
	if len(critiques) != 0 {
		t.Fatalf("critiques len = %d, want 0", len(critiques))
	}

	questions, err := questionRepo.ListBySessionID(ctx, sess.ID)
	if err != nil {
		t.Fatalf("list questions: %v", err)
	}
	if len(questions) != 0 {
		t.Fatalf("questions len = %d, want 0", len(questions))
	}
}

func TestSessionSearchHistory(t *testing.T) {
	db := setupDB(t)
	tx := beginTx(t, db)
	ctx := context.Background()

	workspaceRepo := reposqlite.NewWorkspaceRepo(tx)
	workItemRepo := reposqlite.NewSessionRepo(tx)
	sessionRepo := reposqlite.NewTaskRepo(tx)

	localWS := makeWorkspace(t, tx)
	localWS.Name = "local-workspace"
	localWS.RootPath = "/tmp/local-workspace"
	localWS.UpdatedAt = now()
	if err := workspaceRepo.Update(ctx, localWS); err != nil {
		t.Fatalf("update local workspace: %v", err)
	}
	remoteWS := domain.Workspace{
		ID:        domain.NewID(),
		Name:      "remote-workspace",
		RootPath:  "/tmp/remote-workspace",
		Status:    domain.WorkspaceReady,
		CreatedAt: now(),
		UpdatedAt: now(),
	}
	if err := workspaceRepo.Create(ctx, remoteWS); err != nil {
		t.Fatalf("create remote workspace: %v", err)
	}

	localItem := makeWorkItem(t, tx, localWS.ID)
	localItem.ExternalID = "LOC-1"
	localItem.Title = "Local planner"
	localItem.State = domain.SessionPlanning
	localItem.UpdatedAt = now()
	if err := workItemRepo.Update(ctx, localItem); err != nil {
		t.Fatalf("update local work item: %v", err)
	}
	localPlan := makePlan(t, tx, localItem.ID)
	localSubPlan := makeSubPlan(t, tx, localPlan.ID)
	localSession := makeSession(t, tx, localSubPlan.ID, localWS.ID)
	localSession.RepositoryName = "local-repo"
	localSession.Status = domain.AgentSessionCompleted
	localUpdatedAt := now().Add(-2 * time.Minute)
	completedAt := localUpdatedAt
	localSession.CompletedAt = &completedAt
	localSession.UpdatedAt = localUpdatedAt
	if err := sessionRepo.Update(ctx, localSession); err != nil {
		t.Fatalf("update local session: %v", err)
	}

	remoteItem := makeWorkItem(t, tx, remoteWS.ID)
	remoteItem.ExternalID = "REM-1"
	remoteItem.Title = "Remote search target"
	remoteItem.State = domain.SessionReviewing
	remoteItem.UpdatedAt = now()
	if err := workItemRepo.Update(ctx, remoteItem); err != nil {
		t.Fatalf("update remote work item: %v", err)
	}
	remotePlan := makePlan(t, tx, remoteItem.ID)
	remoteSubPlan := makeSubPlan(t, tx, remotePlan.ID)
	remoteSession := makeSession(t, tx, remoteSubPlan.ID, remoteWS.ID)
	remoteSession.RepositoryName = "remote-repo"
	remoteSession.Status = domain.AgentSessionRunning
	remoteSession.UpdatedAt = now().Add(-1 * time.Minute)
	if err := sessionRepo.Update(ctx, remoteSession); err != nil {
		t.Fatalf("update remote session: %v", err)
	}
	remoteLatestSubPlan := domain.TaskPlan{
		ID:             domain.NewID(),
		PlanID:         remotePlan.ID,
		RepositoryName: "latest-repo",
		Content:        "latest remote sub-plan",
		Order:          2,
		Status:         domain.SubPlanPending,
		CreatedAt:      now(),
		UpdatedAt:      now(),
	}
	if err := reposqlite.NewSubPlanRepo(tx).Create(ctx, remoteLatestSubPlan); err != nil {
		t.Fatalf("create latest remote sub-plan: %v", err)
	}
	remoteLatestSession := makeSession(t, tx, remoteLatestSubPlan.ID, remoteWS.ID)
	remoteLatestSession.RepositoryName = remoteLatestSubPlan.RepositoryName
	remoteLatestSession.Status = domain.AgentSessionCompleted
	latestCompletedAt := now().Add(-30 * time.Second)
	remoteLatestSession.CompletedAt = &latestCompletedAt
	remoteLatestSession.UpdatedAt = latestCompletedAt
	if err := sessionRepo.Update(ctx, remoteLatestSession); err != nil {
		t.Fatalf("update latest remote session: %v", err)
	}

	planningOnlyItem := makeWorkItem(t, tx, remoteWS.ID)
	planningOnlyItem.ExternalID = "REM-2"
	planningOnlyItem.Title = "Planning only"
	planningOnlyItem.State = domain.SessionPlanning
	planningUpdatedAt := now().Add(1 * time.Minute)
	planningOnlyItem.UpdatedAt = planningUpdatedAt
	if err := workItemRepo.Update(ctx, planningOnlyItem); err != nil {
		t.Fatalf("update planning-only work item: %v", err)
	}
	planningSession := domain.Task{
		ID:          domain.NewID(),
		WorkItemID:  planningOnlyItem.ID,
		WorkspaceID: remoteWS.ID,
		Phase:       domain.TaskPhasePlanning,
		HarnessName: "omp",
		Status:      domain.AgentSessionRunning,
		CreatedAt:   planningUpdatedAt,
		UpdatedAt:   planningUpdatedAt,
	}
	if err := sessionRepo.Create(ctx, planningSession); err != nil {
		t.Fatalf("create planning-only session: %v", err)
	}
	ingestedItem := makeWorkItem(t, tx, remoteWS.ID)
	ingestedItem.ExternalID = "REM-3"
	ingestedItem.Title = "Untouched backlog item"
	if err := workItemRepo.Update(ctx, ingestedItem); err != nil {
		t.Fatalf("update ingested work item: %v", err)
	}

	localEntries, err := sessionRepo.SearchHistory(ctx, domain.SessionHistoryFilter{
		WorkspaceID: &localWS.ID,
		Search:      "local",
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("search local history: %v", err)
	}
	if len(localEntries) != 1 {
		t.Fatalf("local entries len = %d, want 1", len(localEntries))
	}
	if localEntries[0].SessionID != localSession.ID {
		t.Fatalf("local session id = %q, want %q", localEntries[0].SessionID, localSession.ID)
	}
	if localEntries[0].WorkspaceName != localWS.Name {
		t.Fatalf("local workspace name = %q, want %q", localEntries[0].WorkspaceName, localWS.Name)
	}
	if localEntries[0].WorkItemTitle != localItem.Title {
		t.Fatalf("local work item title = %q, want %q", localEntries[0].WorkItemTitle, localItem.Title)
	}
	if localEntries[0].RepositoryName != localSession.RepositoryName {
		t.Fatalf("local repository = %q, want %q", localEntries[0].RepositoryName, localSession.RepositoryName)
	}
	if localEntries[0].CompletedAt == nil {
		t.Fatal("local completedAt = nil, want populated timestamp")
	}

	remoteEntries, err := sessionRepo.SearchHistory(ctx, domain.SessionHistoryFilter{Search: "remote search", Limit: 10})
	if err != nil {
		t.Fatalf("search remote history: %v", err)
	}
	if len(remoteEntries) != 1 {
		t.Fatalf("remote entries len = %d, want 1", len(remoteEntries))
	}
	if remoteEntries[0].SessionID != remoteLatestSession.ID {
		t.Fatalf("remote session id = %q, want %q", remoteEntries[0].SessionID, remoteLatestSession.ID)
	}
	if remoteEntries[0].WorkspaceID != remoteWS.ID {
		t.Fatalf("remote workspace id = %q, want %q", remoteEntries[0].WorkspaceID, remoteWS.ID)
	}
	if remoteEntries[0].WorkItemExternalID != remoteItem.ExternalID {
		t.Fatalf("remote external id = %q, want %q", remoteEntries[0].WorkItemExternalID, remoteItem.ExternalID)
	}
	if remoteEntries[0].Status != remoteLatestSession.Status {
		t.Fatalf("remote status = %q, want %q", remoteEntries[0].Status, remoteLatestSession.Status)
	}

	olderSessionEntries, err := sessionRepo.SearchHistory(ctx, domain.SessionHistoryFilter{Search: "remote-repo", Limit: 10})
	if err != nil {
		t.Fatalf("search older child session metadata: %v", err)
	}
	if len(olderSessionEntries) != 1 || olderSessionEntries[0].WorkItemID != remoteItem.ID {
		t.Fatalf("older session search = %#v, want one entry for remote item", olderSessionEntries)
	}

	recentEntries, err := sessionRepo.SearchHistory(ctx, domain.SessionHistoryFilter{Limit: 10})
	if err != nil {
		t.Fatalf("search recent history: %v", err)
	}
	if len(recentEntries) != 3 {
		t.Fatalf("recent entries len = %d, want 3", len(recentEntries))
	}
	if recentEntries[0].WorkItemID != planningOnlyItem.ID {
		t.Fatalf("recent first work item = %q, want %q", recentEntries[0].WorkItemID, planningOnlyItem.ID)
	}
	if recentEntries[0].SessionID != planningSession.ID {
		t.Fatalf("planning-only session id = %q, want %q", recentEntries[0].SessionID, planningSession.ID)
	}
	if recentEntries[0].AgentSessionCount != 1 {
		t.Fatalf("planning-only agent session count = %d, want 1", recentEntries[0].AgentSessionCount)
	}
	if recentEntries[1].SessionID != remoteLatestSession.ID || recentEntries[2].SessionID != localSession.ID {
		t.Fatalf("recent order = [%q %q %q], want [%q %q %q]", recentEntries[0].SessionID, recentEntries[1].SessionID, recentEntries[2].SessionID, planningSession.ID, remoteLatestSession.ID, localSession.ID)
	}
}

// ---------------------------------------------------------------------------
// ReviewCycle + Critique CRUD
// ---------------------------------------------------------------------------

func TestReviewCRUD(t *testing.T) {
	db := setupDB(t)
	tx := beginTx(t, db)
	ctx := context.Background()

	ws := makeWorkspace(t, tx)
	wi := makeWorkItem(t, tx, ws.ID)
	plan := makePlan(t, tx, wi.ID)
	sp := makeSubPlan(t, tx, plan.ID)
	sess := makeSession(t, tx, sp.ID, ws.ID)
	repo := reposqlite.NewReviewRepo(tx)

	rc := domain.ReviewCycle{
		ID:              domain.NewID(),
		AgentSessionID:  sess.ID,
		CycleNumber:     1,
		ReviewerHarness: "claude-reviewer",
		Status:          domain.ReviewCycleReviewing,
		CreatedAt:       now(),
		UpdatedAt:       now(),
	}
	if err := repo.CreateCycle(ctx, rc); err != nil {
		t.Fatalf("create cycle: %v", err)
	}

	got, err := repo.GetCycle(ctx, rc.ID)
	if err != nil {
		t.Fatalf("get cycle: %v", err)
	}
	if got.ReviewerHarness != rc.ReviewerHarness {
		t.Errorf("harness mismatch")
	}

	cycles, err := repo.ListCyclesBySessionID(ctx, sess.ID)
	if err != nil {
		t.Fatalf("list cycles: %v", err)
	}
	if len(cycles) != 1 {
		t.Errorf("len = %d, want 1", len(cycles))
	}

	rc.Status = domain.ReviewCyclePassed
	rc.UpdatedAt = now()
	if err := repo.UpdateCycle(ctx, rc); err != nil {
		t.Fatalf("update cycle: %v", err)
	}

	lineNo := 42
	crit := domain.Critique{
		ID:            domain.NewID(),
		ReviewCycleID: rc.ID,
		FilePath:      "main.go",
		LineNumber:    &lineNo,
		Severity:      domain.CritiqueMajor,
		Description:   "needs fix",
		Status:        domain.CritiqueOpen,
		CreatedAt:     now(),
	}
	if err := repo.CreateCritique(ctx, crit); err != nil {
		t.Fatalf("create critique: %v", err)
	}

	gotCrit, err := repo.GetCritique(ctx, crit.ID)
	if err != nil {
		t.Fatalf("get critique: %v", err)
	}
	if gotCrit.Description != crit.Description {
		t.Errorf("description mismatch")
	}

	crits, err := repo.ListCritiquesByReviewCycleID(ctx, rc.ID)
	if err != nil {
		t.Fatalf("list critiques: %v", err)
	}
	if len(crits) != 1 {
		t.Errorf("len = %d, want 1", len(crits))
	}

	crit.Status = domain.CritiqueResolved
	if err := repo.UpdateCritique(ctx, crit); err != nil {
		t.Fatalf("update critique: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Question CRUD
// ---------------------------------------------------------------------------

func TestQuestionCRUD(t *testing.T) {
	db := setupDB(t)
	tx := beginTx(t, db)
	ctx := context.Background()

	ws := makeWorkspace(t, tx)
	wi := makeWorkItem(t, tx, ws.ID)
	plan := makePlan(t, tx, wi.ID)
	sp := makeSubPlan(t, tx, plan.ID)
	sess := makeSession(t, tx, sp.ID, ws.ID)
	repo := reposqlite.NewQuestionRepo(tx)

	q := domain.Question{
		ID:             domain.NewID(),
		AgentSessionID: sess.ID,
		Content:        "what should I do?",
		Context:        "some context",
		Status:         domain.QuestionPending,
		CreatedAt:      now(),
	}
	if err := repo.Create(ctx, q); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := repo.Get(ctx, q.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Content != q.Content {
		t.Errorf("content mismatch")
	}

	list, err := repo.ListBySessionID(ctx, sess.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("len = %d, want 1", len(list))
	}

	answeredAt := now()
	q.Answer = "do this"
	q.AnsweredBy = "foreman"
	q.Status = domain.QuestionAnswered
	q.AnsweredAt = &answeredAt
	if err := repo.Update(ctx, q); err != nil {
		t.Fatalf("update: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Event CRUD
// ---------------------------------------------------------------------------

func TestEventCRUD(t *testing.T) {
	db := setupDB(t)
	tx := beginTx(t, db)
	ctx := context.Background()

	ws := makeWorkspace(t, tx)
	repo := reposqlite.NewEventRepo(tx)

	ev := domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   "workspace_created",
		WorkspaceID: ws.ID,
		Payload:     `{"name":"test"}`,
		CreatedAt:   now(),
	}
	if err := repo.Create(ctx, ev); err != nil {
		t.Fatalf("create: %v", err)
	}

	byType, err := repo.ListByType(ctx, "workspace_created", 10)
	if err != nil {
		t.Fatalf("list by type: %v", err)
	}
	if len(byType) != 1 {
		t.Errorf("len = %d, want 1", len(byType))
	}

	byWs, err := repo.ListByWorkspaceID(ctx, ws.ID, 10)
	if err != nil {
		t.Fatalf("list by workspace: %v", err)
	}
	if len(byWs) != 1 {
		t.Errorf("len = %d, want 1", len(byWs))
	}
}

// ---------------------------------------------------------------------------
// FK constraint enforcement
// ---------------------------------------------------------------------------

func TestFKConstraintWorkItem(t *testing.T) {
	db := setupDB(t)
	tx := beginTx(t, db)
	ctx := context.Background()

	repo := reposqlite.NewSessionRepo(tx)
	item := domain.Session{
		ID:          domain.NewID(),
		WorkspaceID: "nonexistent",
		Source:      "github",
		Title:       "orphan",
		State:       domain.SessionIngested,
		CreatedAt:   now(),
		UpdatedAt:   now(),
	}
	err := repo.Create(ctx, item)
	if err == nil {
		t.Fatal("expected FK error for nonexistent workspace_id")
	}
}

// ---------------------------------------------------------------------------
// GetNotFound returns sql.ErrNoRows
// ---------------------------------------------------------------------------

func TestGetNotFound(t *testing.T) {
	db := setupDB(t)
	tx := beginTx(t, db)
	ctx := context.Background()

	_, err := reposqlite.NewWorkspaceRepo(tx).Get(ctx, "nope")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("workspace get not found: got %v, want sql.ErrNoRows", err)
	}

	_, err = reposqlite.NewSessionRepo(tx).Get(ctx, "nope")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("work item get not found: got %v, want sql.ErrNoRows", err)
	}

	_, err = reposqlite.NewPlanRepo(tx).Get(ctx, "nope")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("plan get not found: got %v, want sql.ErrNoRows", err)
	}

	_, err = reposqlite.NewSubPlanRepo(tx).Get(ctx, "nope")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("sub-plan get not found: got %v, want sql.ErrNoRows", err)
	}

	_, err = reposqlite.NewTaskRepo(tx).Get(ctx, "nope")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("session get not found: got %v, want sql.ErrNoRows", err)
	}

	_, err = reposqlite.NewInstanceRepo(tx).Get(ctx, "nope")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("instance get not found: got %v, want sql.ErrNoRows", err)
	}
}

// ---------------------------------------------------------------------------
// Transact: commit + rollback
// ---------------------------------------------------------------------------

func TestTransactCommit(t *testing.T) {
	db := setupDB(t)
	ctx := context.Background()

	executer := goatomicsqlx.NewExecuter(db)
	transacter := generic.NewTransacter[generic.SQLXRemote, reposqlite.Resources](
		executer, reposqlite.ResourcesFactory,
	)

	var wsID string
	err := transacter.Transact(ctx, func(ctx context.Context, res reposqlite.Resources) error {
		ws := domain.Workspace{
			ID:        domain.NewID(),
			Name:      "transact-ws",
			RootPath:  "/tmp/transact",
			Status:    domain.WorkspaceReady,
			CreatedAt: now(),
			UpdatedAt: now(),
		}
		wsID = ws.ID

		return res.Workspaces.Create(ctx, ws)
	})
	if err != nil {
		t.Fatalf("transact commit: %v", err)
	}

	// Verify committed: read outside transaction
	tx := beginTx(t, db)
	got, err := reposqlite.NewWorkspaceRepo(tx).Get(ctx, wsID)
	if err != nil {
		t.Fatalf("read committed: %v", err)
	}
	if got.Name != "transact-ws" {
		t.Errorf("name = %q, want %q", got.Name, "transact-ws")
	}
}

func TestTransactRollback(t *testing.T) {
	db := setupDB(t)
	ctx := context.Background()

	executer := goatomicsqlx.NewExecuter(db)
	transacter := generic.NewTransacter[generic.SQLXRemote, reposqlite.Resources](
		executer, reposqlite.ResourcesFactory,
	)

	deliberateErr := errors.New("deliberate")
	var wsID string
	err := transacter.Transact(ctx, func(ctx context.Context, res reposqlite.Resources) error {
		ws := domain.Workspace{
			ID:        domain.NewID(),
			Name:      "rollback-ws",
			RootPath:  "/tmp/rollback",
			Status:    domain.WorkspaceReady,
			CreatedAt: now(),
			UpdatedAt: now(),
		}
		wsID = ws.ID
		if err := res.Workspaces.Create(ctx, ws); err != nil {
			return err
		}

		return deliberateErr
	})
	if err == nil {
		t.Fatal("expected error from transact")
	}

	// Verify rolled back
	tx := beginTx(t, db)
	_, err = reposqlite.NewWorkspaceRepo(tx).Get(ctx, wsID)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected sql.ErrNoRows after rollback, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Empty list returns empty slice, not nil
// ---------------------------------------------------------------------------

func TestEmptyLists(t *testing.T) {
	db := setupDB(t)
	tx := beginTx(t, db)
	ctx := context.Background()

	ws := makeWorkspace(t, tx)
	wi := makeWorkItem(t, tx, ws.ID)
	plan := makePlan(t, tx, wi.ID)
	sp := makeSubPlan(t, tx, plan.ID)
	sess := makeSession(t, tx, sp.ID, ws.ID)

	subPlans, err := reposqlite.NewSubPlanRepo(tx).ListByPlanID(ctx, "nonexistent-plan")
	if err != nil {
		t.Fatalf("sub-plans: %v", err)
	}
	if subPlans == nil {
		t.Error("sub-plans should be non-nil empty slice")
	}

	sessions, err := reposqlite.NewTaskRepo(tx).ListBySubPlanID(ctx, "nonexistent-sp")
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	if sessions == nil {
		t.Error("sessions should be non-nil empty slice")
	}

	cycles, err := reposqlite.NewReviewRepo(tx).ListCyclesBySessionID(ctx, sess.ID)
	if err != nil {
		t.Fatalf("review cycles: %v", err)
	}
	if cycles == nil {
		t.Error("review cycles should be non-nil empty slice")
	}

	questions, err := reposqlite.NewQuestionRepo(tx).ListBySessionID(ctx, sess.ID)
	if err != nil {
		t.Fatalf("questions: %v", err)
	}
	if questions == nil {
		t.Error("questions should be non-nil empty slice")
	}

	events, err := reposqlite.NewEventRepo(tx).ListByType(ctx, "nonexistent", 10)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if events == nil {
		t.Error("events should be non-nil empty slice")
	}

	instances, err := reposqlite.NewInstanceRepo(tx).ListByWorkspaceID(ctx, "nonexistent-ws")
	if err != nil {
		t.Fatalf("instances: %v", err)
	}
	if instances == nil {
		t.Error("instances should be non-nil empty slice")
	}
}
