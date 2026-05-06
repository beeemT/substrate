package views

import (
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
)

// archFlowRepo is a minimal in-memory SessionRepository.
type archFlowRepo struct {
	items        map[string]domain.Session
	updateCalled int
}

func (r *archFlowRepo) Get(_ context.Context, id string) (domain.Session, error) {
	if item, ok := r.items[id]; ok {
		return item, nil
	}
	return domain.Session{}, repository.ErrNotFound
}

func (r *archFlowRepo) List(_ context.Context, _ repository.SessionFilter) ([]domain.Session, error) {
	result := make([]domain.Session, 0, len(r.items))
	for _, item := range r.items {
		result = append(result, item)
	}
	return result, nil
}
func (r *archFlowRepo) Create(_ context.Context, _ domain.Session) error { return nil }
func (r *archFlowRepo) Update(_ context.Context, item domain.Session) error {
	r.updateCalled++
	r.items[item.ID] = item
	return nil
}
func (r *archFlowRepo) Delete(_ context.Context, _ string) error { return nil }

// TestArchAppFlow verifies the archive and unarchive user flows:
//  1. Press 'a' on a completed session → confirm dialog opens
//  2. Confirm with Enter → ArchiveSessionMsg fires via onYes
//  3. Feed ArchiveSessionMsg back → handler calls archiveSessionCmd → service archives
//  4. State → archived, PreviousState → previous state
//
// Unarchive reverses: archived → completed (or merged/failed).



func TestArchAppFlow(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name      string
		workItem  domain.Session
		wantHint  string
		wantState domain.SessionState
		wantPrev  domain.SessionState // what PreviousState should hold after action
	}{
		{
			name:     "archive_completed",
			workItem: domain.Session{ID: "wi-1", WorkspaceID: "ws-local", State: domain.SessionCompleted, CreatedAt: now, UpdatedAt: now},
			wantHint: "Archive session",
			// After archiving: state=archived, previousState=completed (the state we transitioned from)
			wantState: domain.SessionArchived,
			wantPrev:  domain.SessionCompleted,
		},
		{
			name:     "unarchive_archived",
			workItem: domain.Session{ID: "wi-2", WorkspaceID: "ws-local", State: domain.SessionArchived, PreviousState: domain.SessionCompleted, CreatedAt: now, UpdatedAt: now},
			wantHint: "Unarchive session",
			// After unarchiving: state=completed (restored), previousState=archived (the state we transitioned from)
			wantState: domain.SessionCompleted,
			wantPrev:  domain.SessionArchived,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := &archFlowRepo{
				items: map[string]domain.Session{tc.workItem.ID: tc.workItem},
			}
			svc := service.NewSessionService(repository.NoopTransacter{Res: repository.Resources{Sessions: repo}}, NewNoopPublisher())

			app := newTestApp(Services{
				WorkspaceID:   "ws-local",
				WorkspaceName: "local",
				Session:       svc,
				Settings:      &SettingsService{},
			})
			app.workItems = []domain.Session{tc.workItem}
			app.content.SetSize(80, 20)
			app.currentWorkItemID = tc.workItem.ID

			// 1. Verify archive/unarchive hint appears with correct key binding.
			hints := app.currentHints()
			var hintKey string
			for _, h := range hints {
				if h.Label == tc.wantHint {
					hintKey = h.Key
					break
				}
			}
			if hintKey != "a" {
				t.Fatalf("hint key = %q, want \"a\"", hintKey)
			}

			// 2. Press 'a' to open confirm dialog (no cmd returned — dialog is inline).
			model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
			app = model.(*App)
			model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
			app = model.(*App)
			if cmd != nil {
				t.Fatalf("'a' key should not return cmd, got %v", cmd)
			}
			if !app.confirmActive {
				t.Fatal("confirm dialog should be active after pressing 'a'")
			}

			// 3. Press Enter: onYes lambda in showArchiveConfirm/showUnarchiveConfirm fires
			//    ArchiveSessionMsg/UnarchiveSessionMsg directly (not a cmd).
			model, _ = app.Update(tea.KeyMsg{Type: tea.KeyEnter})
			app = model.(*App)

			// 4. Feed the message back to the app to trigger the handler.
			//    The handler appends archiveSessionCmd/unarchiveSessionCmd to tea.Batch.
			var completionMsg tea.Msg
			switch tc.wantHint {
			case "Archive session":
				model, cmd := app.Update(ArchiveSessionMsg{WorkItemID: tc.workItem.ID})
				if cmd != nil {
					completionMsg = cmd()
				}
				_ = model.(*App)
			case "Unarchive session":
				model, cmd := app.Update(UnarchiveSessionMsg{WorkItemID: tc.workItem.ID})
				if cmd != nil {
					completionMsg = cmd()
				}
				_ = model.(*App)
			}

			// 5. Verify service call succeeded and state is correct.
			item, _ := repo.Get(context.Background(), tc.workItem.ID)
			if repo.updateCalled == 0 {
				t.Fatal("repo.Update was never called — service call failed")
			}
			if item.State != tc.wantState {
				t.Errorf("state = %q, want %q", item.State, tc.wantState)
			}
			if item.PreviousState != tc.wantPrev {
				t.Errorf("previousState = %q, want %q", item.PreviousState, tc.wantPrev)
			}

			// 6. Verify the correct completion message was returned.
			switch tc.wantHint {
			case "Archive session":
				if _, ok := completionMsg.(SessionArchivedMsg); !ok {
					t.Errorf("completion msg = %T, want SessionArchivedMsg", completionMsg)
				}
			case "Unarchive session":
				if _, ok := completionMsg.(SessionUnarchivedMsg); !ok {
					t.Errorf("completion msg = %T, want SessionUnarchivedMsg", completionMsg)
				}
			}
		})
	}
}
