package views

import (
	"context"
	"testing"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/gitwork"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
)

type recoveryWorkspaceRepo struct {
	workspaces map[string]domain.Workspace
	creates    int
}

func (r *recoveryWorkspaceRepo) Get(_ context.Context, id string) (domain.Workspace, error) {
	if ws, ok := r.workspaces[id]; ok {
		return ws, nil
	}
	return domain.Workspace{}, repository.ErrNotFound
}

func (r *recoveryWorkspaceRepo) Create(_ context.Context, ws domain.Workspace) error {
	if r.workspaces == nil {
		r.workspaces = make(map[string]domain.Workspace)
	}
	r.creates++
	r.workspaces[ws.ID] = ws
	return nil
}

func (r *recoveryWorkspaceRepo) Update(_ context.Context, ws domain.Workspace) error {
	if r.workspaces == nil {
		r.workspaces = make(map[string]domain.Workspace)
	}
	r.workspaces[ws.ID] = ws
	return nil
}

func (r *recoveryWorkspaceRepo) Delete(_ context.Context, id string) error {
	delete(r.workspaces, id)
	return nil
}

func TestInitWorkspaceCmd_RecoversExistingWorkspaceFile(t *testing.T) {
	cwd := t.TempDir()
	wsFile := &gitwork.WorkspaceFile{
		ID:        domain.NewID(),
		Name:      "existing-workspace",
		CreatedAt: time.Now().UTC(),
	}
	if err := gitwork.WriteWorkspaceFile(cwd, wsFile); err != nil {
		t.Fatalf("WriteWorkspaceFile: %v", err)
	}

	repo := &recoveryWorkspaceRepo{workspaces: make(map[string]domain.Workspace)}
	workspaceSvc := service.NewWorkspaceService(repo)

	msg := initWorkspaceCmd(cwd, workspaceSvc)()
	done, ok := msg.(WorkspaceInitDoneMsg)
	if !ok {
		t.Fatalf("msg = %T, want WorkspaceInitDoneMsg", msg)
	}
	if done.WorkspaceID != wsFile.ID {
		t.Fatalf("workspace id = %q, want %q", done.WorkspaceID, wsFile.ID)
	}
	if done.WorkspaceName != wsFile.Name {
		t.Fatalf("workspace name = %q, want %q", done.WorkspaceName, wsFile.Name)
	}
	if done.WorkspaceDir != cwd {
		t.Fatalf("workspace dir = %q, want %q", done.WorkspaceDir, cwd)
	}
	if repo.creates != 1 {
		t.Fatalf("create count = %d, want 1", repo.creates)
	}
	stored, ok := repo.workspaces[wsFile.ID]
	if !ok {
		t.Fatalf("workspace %q was not registered", wsFile.ID)
	}
	if stored.RootPath != cwd {
		t.Fatalf("stored root path = %q, want %q", stored.RootPath, cwd)
	}
}
