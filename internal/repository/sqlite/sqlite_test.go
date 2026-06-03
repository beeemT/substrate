package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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

func makeSession(t *testing.T, tx generic.SQLXRemote, spID, wsID string) domain.AgentSession {
	t.Helper()
	var workItemID string
	if err := tx.GetContext(context.Background(), &workItemID, `SELECT p.work_item_id FROM sub_plans sp JOIN plans p ON p.id = sp.plan_id WHERE sp.id = ?`, spID); err != nil {
		t.Fatalf("lookup work item for session helper: %v", err)
	}
	s := domain.AgentSession{
		ID:             domain.NewID(),
		WorkItemID:     workItemID,
		SubPlanID:      spID,
		WorkspaceID:    wsID,
		Kind:           domain.AgentSessionKindImplementation,
		RepositoryName: "test-repo",
		HarnessName:    "claude",
		WorktreePath:   "/tmp/worktree",
		Status:         domain.AgentSessionPending,
		CreatedAt:      now(),
		UpdatedAt:      now(),
	}
	if err := reposqlite.NewAgentSessionRepo(tx).Create(context.Background(), s); err != nil {
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

func TestPlanAppendFAQStoresCanonicalTimestamp(t *testing.T) {
	db := setupDB(t)
	tx := beginTx(t, db)
	ctx := context.Background()

	ws := makeWorkspace(t, tx)
	wi := makeWorkItem(t, tx, ws.ID)
	plan := makePlan(t, tx, wi.ID)

	entry := domain.FAQEntry{
		ID:             domain.NewID(),
		PlanID:         plan.ID,
		AgentSessionID: domain.NewID(),
		RepoName:       "test-repo",
		Question:       "Question?",
		Answer:         "Answer.",
		AnsweredBy:     "foreman",
		CreatedAt:      now(),
	}
	if err := reposqlite.NewPlanRepo(tx).AppendFAQ(ctx, entry); err != nil {
		t.Fatalf("append faq: %v", err)
	}

	var stored string
	if err := tx.GetContext(ctx, &stored, `SELECT updated_at FROM plans WHERE id = ?`, plan.ID); err != nil {
		t.Fatalf("get updated_at: %v", err)
	}
	if !strings.Contains(stored, "T") || !strings.HasSuffix(stored, "Z") {
		t.Fatalf("updated_at = %q, want RFC3339-style UTC timestamp", stored)
	}

	if _, err := reposqlite.NewPlanRepo(tx).Get(ctx, plan.ID); err != nil {
		t.Fatalf("get plan: %v", err)
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

func TestSubPlanGetParsesSQLiteCurrentTimestampFormat(t *testing.T) {
	db := setupDB(t)
	tx := beginTx(t, db)
	ctx := context.Background()

	ws := makeWorkspace(t, tx)
	wi := makeWorkItem(t, tx, ws.ID)
	plan := makePlan(t, tx, wi.ID)
	sp := makeSubPlan(t, tx, plan.ID)

	if _, err := tx.ExecContext(ctx, `UPDATE sub_plans SET updated_at = CURRENT_TIMESTAMP WHERE id = ?`, sp.ID); err != nil {
		t.Fatalf("set sqlite timestamp: %v", err)
	}

	got, err := reposqlite.NewSubPlanRepo(tx).Get(ctx, sp.ID)
	if err != nil {
		t.Fatalf("get sub-plan: %v", err)
	}
	if got.UpdatedAt.IsZero() {
		t.Fatal("updated_at parsed as zero time")
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

func TestNewSessionFilterCRUD(t *testing.T) {
	db := setupDB(t)
	tx := beginTx(t, db)
	ctx := context.Background()

	ws := makeWorkspace(t, tx)
	repo := reposqlite.NewSessionFilterRepo(tx)

	filter := domain.NewSessionFilter{
		ID:          domain.NewID(),
		WorkspaceID: ws.ID,
		Name:        "critical-github-issues",
		Provider:    "github",
		Criteria: domain.NewSessionFilterCriteria{
			Scope:      domain.ScopeIssues,
			View:       "all",
			State:      "open",
			Search:     "panic",
			Labels:     []string{"substrate:auto-plan"},
			Owner:      "me",
			Repository: "acme/rocket",
			TeamID:     "",
		},
		CreatedAt: now(),
		UpdatedAt: now(),
	}

	if err := repo.Create(ctx, filter); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := repo.Get(ctx, filter.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != filter.Name {
		t.Fatalf("name = %q, want %q", got.Name, filter.Name)
	}
	if got.Criteria.Repository != filter.Criteria.Repository {
		t.Fatalf("criteria.repository = %q, want %q", got.Criteria.Repository, filter.Criteria.Repository)
	}
	if len(got.Criteria.Labels) != 1 || got.Criteria.Labels[0] != "substrate:auto-plan" {
		t.Fatalf("criteria.labels = %#v", got.Criteria.Labels)
	}

	byWorkspace, err := repo.ListByWorkspaceID(ctx, ws.ID)
	if err != nil {
		t.Fatalf("list workspace: %v", err)
	}
	if len(byWorkspace) != 1 {
		t.Fatalf("workspace list len = %d, want 1", len(byWorkspace))
	}

	byProvider, err := repo.ListByWorkspaceProvider(ctx, ws.ID, "github")
	if err != nil {
		t.Fatalf("list workspace/provider: %v", err)
	}
	if len(byProvider) != 1 {
		t.Fatalf("workspace/provider list len = %d, want 1", len(byProvider))
	}

	byName, err := repo.GetByWorkspaceProviderName(ctx, ws.ID, "github", filter.Name)
	if err != nil {
		t.Fatalf("get by name: %v", err)
	}
	if byName.ID != filter.ID {
		t.Fatalf("get by name id = %q, want %q", byName.ID, filter.ID)
	}

	filter.Criteria.Search = "deadlock"
	filter.UpdatedAt = now()
	if err := repo.Update(ctx, filter); err != nil {
		t.Fatalf("update: %v", err)
	}

	updated, err := repo.Get(ctx, filter.ID)
	if err != nil {
		t.Fatalf("get updated: %v", err)
	}
	if updated.Criteria.Search != "deadlock" {
		t.Fatalf("criteria.search = %q, want deadlock", updated.Criteria.Search)
	}

	if err := repo.Delete(ctx, filter.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := repo.Get(ctx, filter.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("get after delete error = %v, want %v", err, sql.ErrNoRows)
	}
}

func TestNewSessionFilterLockAcquireRenewRelease(t *testing.T) {
	db := setupDB(t)
	tx := beginTx(t, db)
	ctx := context.Background()

	ws := makeWorkspace(t, tx)
	filterRepo := reposqlite.NewSessionFilterRepo(tx)
	lockRepo := reposqlite.NewSessionFilterLockRepo(tx)

	filter := domain.NewSessionFilter{
		ID:          domain.NewID(),
		WorkspaceID: ws.ID,
		Name:        "sentry-prod-errors",
		Provider:    "sentry",
		Criteria: domain.NewSessionFilterCriteria{
			Scope:      domain.ScopeIssues,
			View:       "all",
			Repository: "prod",
		},
		CreatedAt: now(),
		UpdatedAt: now(),
	}
	if err := filterRepo.Create(ctx, filter); err != nil {
		t.Fatalf("create filter: %v", err)
	}

	base := now()
	first := domain.NewSessionFilterLock{
		FilterID:       filter.ID,
		InstanceID:     "instance-1",
		LeaseExpiresAt: base.Add(2 * time.Minute),
		AcquiredAt:     base,
		UpdatedAt:      base,
	}
	current, acquired, err := lockRepo.Acquire(ctx, first)
	if err != nil {
		t.Fatalf("acquire first: %v", err)
	}
	if !acquired {
		t.Fatal("first acquire = false, want true")
	}
	if current.InstanceID != "instance-1" {
		t.Fatalf("current.instance_id = %q, want instance-1", current.InstanceID)
	}

	second := domain.NewSessionFilterLock{
		FilterID:       filter.ID,
		InstanceID:     "instance-2",
		LeaseExpiresAt: base.Add(3 * time.Minute),
		AcquiredAt:     base.Add(30 * time.Second),
		UpdatedAt:      base.Add(30 * time.Second),
	}
	current, acquired, err = lockRepo.Acquire(ctx, second)
	if err != nil {
		t.Fatalf("acquire second: %v", err)
	}
	if acquired {
		t.Fatal("second acquire = true, want false while active lease is held")
	}
	if current.InstanceID != "instance-1" {
		t.Fatalf("current.instance_id after conflict = %q, want instance-1", current.InstanceID)
	}

	renew := domain.NewSessionFilterLock{
		FilterID:       filter.ID,
		InstanceID:     "instance-1",
		LeaseExpiresAt: base.Add(4 * time.Minute),
		UpdatedAt:      base.Add(45 * time.Second),
	}
	current, renewed, err := lockRepo.Renew(ctx, renew)
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if !renewed {
		t.Fatal("renew = false, want true")
	}
	if !current.LeaseExpiresAt.Equal(renew.LeaseExpiresAt) {
		t.Fatalf("lease_expires_at = %s, want %s", current.LeaseExpiresAt, renew.LeaseExpiresAt)
	}

	if err := lockRepo.Release(ctx, filter.ID, "instance-2"); err != nil {
		t.Fatalf("release wrong owner should no-op: %v", err)
	}
	stillHeld, err := lockRepo.Get(ctx, filter.ID)
	if err != nil {
		t.Fatalf("get after wrong-owner release: %v", err)
	}
	if stillHeld.InstanceID != "instance-1" {
		t.Fatalf("instance after wrong-owner release = %q, want instance-1", stillHeld.InstanceID)
	}

	if err := lockRepo.Release(ctx, filter.ID, "instance-1"); err != nil {
		t.Fatalf("release owner: %v", err)
	}
	if _, err := lockRepo.Get(ctx, filter.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("get after release error = %v, want %v", err, sql.ErrNoRows)
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
	repo := reposqlite.NewAgentSessionRepo(tx)

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

func TestSessionParentAgentSessionIDRoundTrip(t *testing.T) {
	db := setupDB(t)
	tx := beginTx(t, db)
	ctx := context.Background()

	ws := makeWorkspace(t, tx)
	wi := makeWorkItem(t, tx, ws.ID)
	plan := makePlan(t, tx, wi.ID)
	sp := makeSubPlan(t, tx, plan.ID)
	repo := reposqlite.NewAgentSessionRepo(tx)

	parent := makeSession(t, tx, sp.ID, ws.ID)

	// Create a child session with ParentAgentSessionID set on insert.
	child := domain.AgentSession{
		ID:                   domain.NewID(),
		WorkItemID:           wi.ID,
		SubPlanID:            sp.ID,
		WorkspaceID:          ws.ID,
		Kind:                 domain.AgentSessionKindReview,
		RepositoryName:       "test-repo",
		HarnessName:          "claude",
		WorktreePath:         "/tmp/worktree",
		Status:               domain.AgentSessionPending,
		CreatedAt:            now(),
		UpdatedAt:            now(),
		ParentAgentSessionID: parent.ID,
	}
	if err := repo.Create(ctx, child); err != nil {
		t.Fatalf("create child: %v", err)
	}

	// Round-trip via Get.
	got, err := repo.Get(ctx, child.ID)
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if got.ParentAgentSessionID != parent.ID {
		t.Errorf("ParentAgentSessionID = %q, want %q", got.ParentAgentSessionID, parent.ID)
	}

	// Round-trip via List.
	list, err := repo.ListBySubPlanID(ctx, sp.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var listChild *domain.AgentSession
	for i := range list {
		if list[i].ID == child.ID {
			listChild = &list[i]
			break
		}
	}
	if listChild == nil {
		t.Fatalf("child %s not in list", child.ID)
	}
	if listChild.ParentAgentSessionID != parent.ID {
		t.Errorf("list ParentAgentSessionID = %q, want %q", listChild.ParentAgentSessionID, parent.ID)
	}

	// The parent session should still have an empty ParentAgentSessionID.
	gotParent, err := repo.Get(ctx, parent.ID)
	if err != nil {
		t.Fatalf("get parent: %v", err)
	}
	if gotParent.ParentAgentSessionID != "" {
		t.Errorf("parent ParentAgentSessionID = %q, want empty", gotParent.ParentAgentSessionID)
	}

	// Update should preserve ParentAgentSessionID and allow changing it.
	other := makeSession(t, tx, sp.ID, ws.ID)
	got.ParentAgentSessionID = other.ID
	got.UpdatedAt = now()
	if err := repo.Update(ctx, got); err != nil {
		t.Fatalf("update child: %v", err)
	}
	got2, err := repo.Get(ctx, child.ID)
	if err != nil {
		t.Fatalf("get child after update: %v", err)
	}
	if got2.ParentAgentSessionID != other.ID {
		t.Errorf("updated ParentAgentSessionID = %q, want %q", got2.ParentAgentSessionID, other.ID)
	}

	// Update can clear the parent back to empty.
	got2.ParentAgentSessionID = ""
	got2.UpdatedAt = now()
	if err := repo.Update(ctx, got2); err != nil {
		t.Fatalf("update child clear parent: %v", err)
	}
	got3, err := repo.Get(ctx, child.ID)
	if err != nil {
		t.Fatalf("get child after clear: %v", err)
	}
	if got3.ParentAgentSessionID != "" {
		t.Errorf("cleared ParentAgentSessionID = %q, want empty", got3.ParentAgentSessionID)
	}
}

func TestSessionListActiveChildrenByParentID(t *testing.T) {
	db := setupDB(t)
	tx := beginTx(t, db)
	ctx := context.Background()

	ws := makeWorkspace(t, tx)
	wi := makeWorkItem(t, tx, ws.ID)
	plan := makePlan(t, tx, wi.ID)
	sp := makeSubPlan(t, tx, plan.ID)
	repo := reposqlite.NewAgentSessionRepo(tx)

	parent := makeSession(t, tx, sp.ID, ws.ID)
	active := domain.AgentSession{
		ID:                   domain.NewID(),
		WorkItemID:           wi.ID,
		SubPlanID:            sp.ID,
		WorkspaceID:          ws.ID,
		Kind:                 domain.AgentSessionKindImplementation,
		RepositoryName:       "test-repo",
		HarnessName:          "claude",
		WorktreePath:         "/tmp/worktree",
		Status:               domain.AgentSessionRunning,
		CreatedAt:            now(),
		UpdatedAt:            now(),
		ParentAgentSessionID: parent.ID,
	}
	completed := active
	completed.ID = domain.NewID()
	completed.Status = domain.AgentSessionCompleted
	if err := repo.Create(ctx, active); err != nil {
		t.Fatalf("create active child: %v", err)
	}
	if err := repo.Create(ctx, completed); err != nil {
		t.Fatalf("create completed child: %v", err)
	}

	children, err := repo.ListActiveChildrenByParentID(ctx, parent.ID)
	if err != nil {
		t.Fatalf("ListActiveChildrenByParentID: %v", err)
	}
	if len(children) != 1 {
		t.Fatalf("active children = %d, want 1", len(children))
	}
	if children[0].ID != active.ID {
		t.Fatalf("active child ID = %q, want %q", children[0].ID, active.ID)
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

	sessionRepo := reposqlite.NewAgentSessionRepo(tx)
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
	sessionRepo := reposqlite.NewAgentSessionRepo(tx)

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
	planningSession := domain.AgentSession{
		ID:          domain.NewID(),
		WorkItemID:  planningOnlyItem.ID,
		WorkspaceID: remoteWS.ID,
		Kind:        domain.AgentSessionKindPlanning,
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

	_, err = reposqlite.NewAgentSessionRepo(tx).Get(ctx, "nope")
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
	transacter := generic.NewTransacter[generic.SQLXRemote, repository.Resources](
		executer, reposqlite.ResourcesFactory,
	)

	var wsID string
	err := transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
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
	transacter := generic.NewTransacter[generic.SQLXRemote, repository.Resources](
		executer, reposqlite.ResourcesFactory,
	)

	deliberateErr := errors.New("deliberate")
	var wsID string
	err := transacter.Transact(ctx, func(ctx context.Context, res repository.Resources) error {
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

	sessions, err := reposqlite.NewAgentSessionRepo(tx).ListBySubPlanID(ctx, "nonexistent-sp")
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

// ---------------------------------------------------------------------------
// Migration 019: agent_sessions phase→kind rename + foreman CHECK
// ---------------------------------------------------------------------------

func TestMigration019_AgentSessionKind(t *testing.T) {
	// Start with migration 018 applied. Existing databases at that point still have a phase column.
	db, err := sqlx.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ctx := context.Background()
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			t.Fatalf("pragma %s: %v", pragma, err)
		}
	}

	// Parse and apply migrations 001–018 in version order.
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	var migrationFiles []struct {
		version  int
		filename string
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".sql" {
			continue
		}
		parts := strings.SplitN(e.Name(), "_", 2)
		if len(parts) < 2 {
			continue
		}
		ver, err := strconv.Atoi(parts[0])
		if err != nil || ver < 1 || ver > 18 {
			continue
		}
		migrationFiles = append(migrationFiles, struct {
			version  int
			filename string
		}{ver, e.Name()})
	}
	sort.Slice(migrationFiles, func(i, j int) bool {
		return migrationFiles[i].version < migrationFiles[j].version
	})
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		);
	`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	for _, m := range migrationFiles {
		data, err := fs.ReadFile(migrations.FS, m.filename)
		if err != nil {
			t.Fatalf("read migration %s: %v", m.filename, err)
		}
		if _, err := db.ExecContext(ctx, string(data)); err != nil {
			t.Fatalf("apply migration %s: %v", m.filename, err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES (?)`, m.version); err != nil {
			t.Fatalf("record migration %d: %v", m.version, err)
		}
	}

	// Shared timestamp for deterministic inserts.
	nowStr := now().Format(time.RFC3339Nano)

	// Create the minimal row structure needed by agent_sessions FKs.
	wsID := domain.NewID()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO workspaces (id, name, root_path, status) VALUES (?, 'test', '/tmp', 'ready')
	`, wsID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	wiID := domain.NewID()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO work_items (id, workspace_id, source, title, state, created_at, updated_at) VALUES (?, ?, 'github', 'test', 'ingested', ?, ?)
	`, wiID, wsID, nowStr, nowStr); err != nil {
		t.Fatalf("create work_item: %v", err)
	}

	// Insert an agent_sessions row with phase='implementation' directly into the 018 schema.
	sessionID := domain.NewID()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO agent_sessions (
			id, work_item_id, workspace_id, sub_plan_id, plan_id,
			repository_name, harness_name, worktree_path, status, phase,
			created_at, updated_at
		) VALUES (?, ?, ?, NULL, NULL, 'test-repo', 'claude', '/tmp/wt', 'completed', 'implementation', ?, ?)
	`, sessionID, wiID, wsID, nowStr, nowStr); err != nil {
		t.Fatalf("insert agent_session with phase='implementation': %v", err)
	}

	// Read and apply migration 019 directly to avoid re-running 001–018.
	migration019, err := fs.ReadFile(migrations.FS, "019_agent_session_kind.sql")
	if err != nil {
		t.Fatalf("read migration 019: %v", err)
	}
	if _, err := db.ExecContext(ctx, string(migration019)); err != nil {
		t.Fatalf("apply migration 019: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES (19)`); err != nil {
		t.Fatalf("record migration 019: %v", err)
	}

	// Verify the pre-existing row survived migration and still has kind='implementation'.
	var gotKind string
	if err := db.GetContext(ctx, &gotKind, `SELECT kind FROM agent_sessions WHERE id = ?`, sessionID); err != nil {
		t.Fatalf("get kind after migration: %v", err)
	}
	if gotKind != "implementation" {
		t.Errorf("kind = %q, want %q", gotKind, "implementation")
	}

	// Verify the CHECK constraint now accepts 'foreman'.
	foremanID := domain.NewID()
	_, err = db.ExecContext(ctx, `
		INSERT INTO agent_sessions (
			id, work_item_id, workspace_id, sub_plan_id, plan_id,
			repository_name, harness_name, worktree_path, status, kind,
			created_at, updated_at
		) VALUES (?, ?, ?, NULL, NULL, 'test-repo', 'claude', '/tmp/wt', 'running', 'foreman', ?, ?)
	`, foremanID, wiID, wsID, nowStr, nowStr)
	if err != nil {
		t.Errorf("insert with kind='foreman' should succeed after 019: %v", err)
	}

	var foremanKind string
	if err := db.GetContext(ctx, &foremanKind, `SELECT kind FROM agent_sessions WHERE id = ?`, foremanID); err != nil {
		t.Fatalf("get foreman kind after insert: %v", err)
	}
	if foremanKind != "foreman" {
		t.Errorf("foreman kind = %q, want %q", foremanKind, "foreman")
	}

	// Verify the CHECK constraint rejects invalid kind values.
	_, err = db.ExecContext(ctx, `
		INSERT INTO agent_sessions (
			id, work_item_id, workspace_id, sub_plan_id, plan_id,
			repository_name, harness_name, worktree_path, status, kind,
			created_at, updated_at
		) VALUES (?, ?, ?, NULL, NULL, 'test-repo', 'claude', '/tmp/wt', 'completed', 'invalid-kind', ?, ?)
	`, domain.NewID(), wiID, wsID, nowStr, nowStr)
	if err == nil {
		t.Error("insert with kind='invalid-kind' should be rejected by CHECK constraint")
	}
}

// ---------------------------------------------------------------------------
// Migration 020: parent_agent_session_id column + backfill
// ---------------------------------------------------------------------------

func TestMigration020_AgentSessionParent(t *testing.T) {
	// Apply migrations 001-019, then seed rows that mimic the pre-graph world
	// (no parent_agent_session_id), then apply 020 and verify the schema and
	// backfill match expectations.
	db, err := sqlx.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ctx := context.Background()
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			t.Fatalf("pragma %s: %v", pragma, err)
		}
	}

	// Apply migrations 001-019 in order.
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	type migFile struct {
		version  int
		filename string
	}
	var migrationFiles []migFile
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".sql" {
			continue
		}
		parts := strings.SplitN(e.Name(), "_", 2)
		if len(parts) < 2 {
			continue
		}
		ver, err := strconv.Atoi(parts[0])
		if err != nil || ver < 1 || ver > 19 {
			continue
		}
		migrationFiles = append(migrationFiles, migFile{ver, e.Name()})
	}
	sort.Slice(migrationFiles, func(i, j int) bool {
		return migrationFiles[i].version < migrationFiles[j].version
	})
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		);
	`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	for _, m := range migrationFiles {
		data, err := fs.ReadFile(migrations.FS, m.filename)
		if err != nil {
			t.Fatalf("read migration %s: %v", m.filename, err)
		}
		if _, err := db.ExecContext(ctx, string(data)); err != nil {
			t.Fatalf("apply migration %s: %v", m.filename, err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES (?)`, m.version); err != nil {
			t.Fatalf("record migration %d: %v", m.version, err)
		}
	}

	// Seed parent rows.
	wsID := domain.NewID()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO workspaces (id, name, root_path, status) VALUES (?, 'test', '/tmp', 'ready')
	`, wsID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	wiID := domain.NewID()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO work_items (id, workspace_id, source, title, state, created_at, updated_at)
		VALUES (?, ?, 'github', 'test', 'ingested', strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
	`, wiID, wsID); err != nil {
		t.Fatalf("create work_item: %v", err)
	}

	planID := domain.NewID()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO plans (id, work_item_id, version, status, orchestrator_plan, created_at, updated_at)
		VALUES (?, ?, 1, 'approved', '{}', strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
	`, planID, wiID); err != nil {
		t.Fatalf("create plan: %v", err)
	}

	subPlanA := domain.NewID()
	subPlanB := domain.NewID()
	subPlanRepoNames := map[string]string{
		subPlanA: "repo-a",
		subPlanB: "repo-b",
	}
	for _, sp := range []string{subPlanA, subPlanB} {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO sub_plans (id, plan_id, repo_name, content, status, created_at, updated_at)
			VALUES (?, ?, ?, '{}', 'pending', strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		`, sp, planID, subPlanRepoNames[sp]); err != nil {
			t.Fatalf("create sub_plan: %v", err)
		}
	}

	// Three sessions in sub_plan A, with strictly ordered created_at, all
	// implementation/review (so they should be chained by the backfill).
	type seedSession struct {
		id        string
		subPlan   string
		kind      string
		createdAt string
	}
	tBase := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	a1 := seedSession{id: domain.NewID(), subPlan: subPlanA, kind: "implementation", createdAt: tBase.Format(time.RFC3339Nano)}
	a2 := seedSession{id: domain.NewID(), subPlan: subPlanA, kind: "review", createdAt: tBase.Add(time.Minute).Format(time.RFC3339Nano)}
	a3 := seedSession{id: domain.NewID(), subPlan: subPlanA, kind: "implementation", createdAt: tBase.Add(2 * time.Minute).Format(time.RFC3339Nano)}
	// One session in sub_plan B — it must NOT be linked to anything in A.
	b1 := seedSession{id: domain.NewID(), subPlan: subPlanB, kind: "implementation", createdAt: tBase.Add(30 * time.Second).Format(time.RFC3339Nano)}
	// A foreman session that is NOT implementation/review and must be skipped
	// by the backfill (excluded from the chain).
	foreman := seedSession{id: domain.NewID(), subPlan: subPlanA, kind: "foreman", createdAt: tBase.Add(15 * time.Second).Format(time.RFC3339Nano)}

	for _, s := range []seedSession{a1, a2, a3, b1, foreman} {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO agent_sessions (
				id, work_item_id, workspace_id, sub_plan_id, plan_id,
				repository_name, harness_name, worktree_path, status, kind,
				created_at, updated_at
			) VALUES (?, ?, ?, ?, NULL, 'repo', 'claude', '/tmp/wt', 'completed', ?, ?, ?)
		`, s.id, wiID, wsID, s.subPlan, s.kind, s.createdAt, s.createdAt); err != nil {
			t.Fatalf("insert seed session %s: %v", s.id, err)
		}
	}

	// Apply migration 020.
	migration020, err := fs.ReadFile(migrations.FS, "020_agent_session_parent.sql")
	if err != nil {
		t.Fatalf("read migration 020: %v", err)
	}
	if _, err := db.ExecContext(ctx, string(migration020)); err != nil {
		t.Fatalf("apply migration 020: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES (20)`); err != nil {
		t.Fatalf("record migration 020: %v", err)
	}

	// 1. The column must exist.
	type pragmaCol struct {
		Cid       int     `db:"cid"`
		Name      string  `db:"name"`
		Type      string  `db:"type"`
		NotNull   int     `db:"notnull"`
		DfltValue *string `db:"dflt_value"`
		Pk        int     `db:"pk"`
	}
	var cols []pragmaCol
	if err := db.SelectContext(ctx, &cols, `SELECT cid, name, type, "notnull", dflt_value, pk FROM pragma_table_info('agent_sessions')`); err != nil {
		t.Fatalf("pragma_table_info: %v", err)
	}
	hasParentCol := false
	for _, c := range cols {
		if c.Name == "parent_agent_session_id" {
			hasParentCol = true
			if c.Type != "TEXT" {
				t.Errorf("parent_agent_session_id type = %q, want TEXT", c.Type)
			}
		}
	}
	if !hasParentCol {
		t.Fatal("parent_agent_session_id column missing after migration 020")
	}

	// 2. The index must exist.
	var indexCount int
	if err := db.GetContext(ctx, &indexCount, `
		SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'index' AND name = 'idx_sessions_parent_agent_session'
	`); err != nil {
		t.Fatalf("count index: %v", err)
	}
	if indexCount != 1 {
		t.Errorf("idx_sessions_parent_agent_session count = %d, want 1", indexCount)
	}

	// 3. Backfill links sub_plan A's implementation/review sessions in created order.
	getParent := func(id string) *string {
		var v *string
		if err := db.GetContext(ctx, &v, `SELECT parent_agent_session_id FROM agent_sessions WHERE id = ?`, id); err != nil {
			t.Fatalf("get parent of %s: %v", id, err)
		}
		return v
	}
	if p := getParent(a1.id); p != nil {
		t.Errorf("a1.parent = %v, want nil (first in chain)", *p)
	}
	if p := getParent(a2.id); p == nil || *p != a1.id {
		t.Errorf("a2.parent = %v, want %s", p, a1.id)
	}
	if p := getParent(a3.id); p == nil || *p != a2.id {
		t.Errorf("a3.parent = %v, want %s", p, a2.id)
	}

	// 4. Sub-plan B's session must be untouched (it's the only one in its
	//    subplan and must not link to anything in A).
	if p := getParent(b1.id); p != nil {
		t.Errorf("b1.parent = %v, want nil (different subplan, alone)", *p)
	}

	// 5. The foreman session is excluded from the implementation/review chain
	//    by the WHERE filter and must remain unparented even though it shares
	//    a sub_plan with the impl/review chain.
	if p := getParent(foreman.id); p != nil {
		t.Errorf("foreman.parent = %v, want nil (excluded by kind filter)", *p)
	}
}

func TestMigration022_RepairsReviewRetryParent(t *testing.T) {
	db := setupDB(t)
	ctx := context.Background()

	wsID := domain.NewID()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO workspaces (id, name, root_path, status) VALUES (?, 'test', '/tmp', 'ready')
	`, wsID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	wiID := domain.NewID()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO work_items (id, workspace_id, source, title, state, created_at, updated_at)
		VALUES (?, ?, 'github', 'test', 'implementing', strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
	`, wiID, wsID); err != nil {
		t.Fatalf("create work_item: %v", err)
	}

	planID := domain.NewID()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO plans (id, work_item_id, version, status, orchestrator_plan, created_at, updated_at)
		VALUES (?, ?, 1, 'approved', '{}', strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
	`, planID, wiID); err != nil {
		t.Fatalf("create plan: %v", err)
	}

	subPlanID := domain.NewID()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO sub_plans (id, plan_id, repo_name, content, status, created_at, updated_at)
		VALUES (?, ?, 'repo-a', '{}', 'in_progress', strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
	`, subPlanID, planID); err != nil {
		t.Fatalf("create sub_plan: %v", err)
	}

	tBase := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	implID := domain.NewID()
	oldReviewID := domain.NewID()
	staleLeafID := domain.NewID()
	newReviewID := domain.NewID()
	runningChildID := domain.NewID()
	nullReviewID := domain.NewID()
	seed := []struct {
		id        string
		kind      string
		status    string
		parentID  *string
		createdAt time.Time
	}{
		{id: implID, kind: "implementation", status: "completed", createdAt: tBase},
		{id: oldReviewID, kind: "review", status: "failed", parentID: &implID, createdAt: tBase.Add(time.Minute)},
		{id: staleLeafID, kind: "implementation", status: "interrupted", parentID: &oldReviewID, createdAt: tBase.Add(2 * time.Minute)},
		// This is the bad edge produced by the shipped retry path: the new review
		// reviews implID but should supersede staleLeafID in the graph.
		{id: newReviewID, kind: "review", status: "completed", parentID: &implID, createdAt: tBase.Add(3 * time.Minute)},
		{id: runningChildID, kind: "implementation", status: "completed", parentID: &newReviewID, createdAt: tBase.Add(4 * time.Minute)},
		{id: nullReviewID, kind: "review", status: "running", createdAt: tBase.Add(5 * time.Minute)},
	}
	for _, s := range seed {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO agent_sessions (
				id, work_item_id, workspace_id, sub_plan_id, plan_id,
				repository_name, harness_name, worktree_path, status, kind,
				created_at, updated_at, parent_agent_session_id
			) VALUES (?, ?, ?, ?, NULL, 'repo-a', 'claude', '/tmp/wt', ?, ?, ?, ?, ?)
		`, s.id, wiID, wsID, subPlanID, s.status, s.kind, s.createdAt.Format(time.RFC3339Nano), s.createdAt.Format(time.RFC3339Nano), s.parentID); err != nil {
			t.Fatalf("insert seed session %s: %v", s.id, err)
		}
	}

	migration022, err := fs.ReadFile(migrations.FS, "022_repair_review_retry_parent.sql")
	if err != nil {
		t.Fatalf("read migration 022: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := db.ExecContext(ctx, string(migration022)); err != nil {
			t.Fatalf("apply migration 022 run %d: %v", i+1, err)
		}
	}

	var newReviewParent string
	if err := db.GetContext(ctx, &newReviewParent, `SELECT parent_agent_session_id FROM agent_sessions WHERE id = ?`, newReviewID); err != nil {
		t.Fatalf("get new review parent: %v", err)
	}
	if newReviewParent != staleLeafID {
		t.Fatalf("new review parent = %q, want stale leaf %q", newReviewParent, staleLeafID)
	}

	var childParent string
	if err := db.GetContext(ctx, &childParent, `SELECT parent_agent_session_id FROM agent_sessions WHERE id = ?`, runningChildID); err != nil {
		t.Fatalf("get running child parent: %v", err)
	}
	if childParent != newReviewID {
		t.Fatalf("running child parent = %q, want %q", childParent, newReviewID)
	}

	var nullReviewParent string
	if err := db.GetContext(ctx, &nullReviewParent, `SELECT parent_agent_session_id FROM agent_sessions WHERE id = ?`, nullReviewID); err != nil {
		t.Fatalf("get null review parent: %v", err)
	}
	if nullReviewParent != runningChildID {
		t.Fatalf("null review parent = %q, want latest completed impl %q", nullReviewParent, runningChildID)
	}
}
