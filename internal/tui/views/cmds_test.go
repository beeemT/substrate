package views_test

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/event"
	"github.com/beeemT/substrate/internal/orchestrator"
	"github.com/beeemT/substrate/internal/repository"
	"github.com/beeemT/substrate/internal/service"
	"github.com/beeemT/substrate/internal/sessionlog"
	"github.com/beeemT/substrate/internal/tui/views"
)

func TestAnswerQuestionCmd_PlanningFallbackPersistsResumesAndPublishesAnswered(t *testing.T) {
	t.Parallel()

	questionRepo := newCmdQuestionRepo()
	taskRepo := newCmdTaskRepo()
	questionSvc := service.NewQuestionService(repository.NoopTransacter{Res: repository.Resources{Questions: questionRepo}}, views.NewNoopPublisher())
	taskSvc := service.NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: taskRepo}}, views.NewNoopPublisher())
	bus := event.NewBus(event.BusConfig{})
	defer bus.Close()
	sub, err := bus.Subscribe("planning-answer-test", string(domain.EventAgentQuestionAnswered))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	registry := orchestrator.NewSessionRegistry()
	router := orchestrator.NewAnswerRouter(registry, questionSvc, taskSvc, bus)

	questionRepo.questions["q-plan"] = domain.Question{ID: "q-plan", AgentSessionID: "plan-session", Stage: domain.AgentSessionKindPlanning, Status: domain.QuestionPending}
	taskRepo.tasks["plan-session"] = domain.AgentSession{ID: "plan-session", WorkItemID: "wi-1", WorkspaceID: "ws-1", Kind: domain.AgentSessionKindPlanning, Status: domain.AgentSessionWaitingForAnswer}

	msg := views.AnswerQuestionCmd(router, "q-plan", "use full cutover", "human")()
	if done, ok := msg.(views.ActionDoneMsg); !ok || done.Message != "Answer submitted" {
		t.Fatalf("message = %T %#v, want ActionDoneMsg", msg, msg)
	}

	gotQuestion := questionRepo.questions["q-plan"]
	if gotQuestion.Status != domain.QuestionAnswered {
		t.Fatalf("question status = %s, want %s", gotQuestion.Status, domain.QuestionAnswered)
	}
	if gotQuestion.Answer != "use full cutover" || gotQuestion.AnsweredBy != "human" {
		t.Fatalf("answer = %q by %q, want answer persisted by human", gotQuestion.Answer, gotQuestion.AnsweredBy)
	}
	if gotTask := taskRepo.tasks["plan-session"]; gotTask.Status != domain.AgentSessionRunning {
		t.Fatalf("task status = %s, want %s", gotTask.Status, domain.AgentSessionRunning)
	}

	select {
	case evt := <-sub.C:
		if evt.EventType != string(domain.EventAgentQuestionAnswered) {
			t.Fatalf("event type = %q, want %q", evt.EventType, domain.EventAgentQuestionAnswered)
		}
		var payload map[string]string
		if err := json.Unmarshal([]byte(evt.Payload), &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if payload["id"] != "q-plan" || payload["agent_session_id"] != "plan-session" {
			t.Fatalf("payload = %#v, want id/agent_session_id", payload)
		}
	default:
		t.Fatal("expected planning answered event")
	}
}

func TestSkipQuestionCmd_PlanningFallbackPersistsAndResumes(t *testing.T) {
	t.Parallel()

	questionRepo := newCmdQuestionRepo()
	taskRepo := newCmdTaskRepo()
	questionSvc := service.NewQuestionService(repository.NoopTransacter{Res: repository.Resources{Questions: questionRepo}}, views.NewNoopPublisher())
	taskSvc := service.NewAgentSessionService(repository.NoopTransacter{Res: repository.Resources{AgentSessions: taskRepo}}, views.NewNoopPublisher())
	bus := event.NewBus(event.BusConfig{})
	defer bus.Close()

	registry := orchestrator.NewSessionRegistry()
	router := orchestrator.NewAnswerRouter(registry, questionSvc, taskSvc, bus)

	questionRepo.questions["q-skip"] = domain.Question{ID: "q-skip", AgentSessionID: "plan-session", Stage: domain.AgentSessionKindPlanning, Status: domain.QuestionPending}
	taskRepo.tasks["plan-session"] = domain.AgentSession{ID: "plan-session", WorkItemID: "wi-1", WorkspaceID: "ws-1", Kind: domain.AgentSessionKindPlanning, Status: domain.AgentSessionWaitingForAnswer}

	msg := views.SkipQuestionCmd(router, "q-skip")()
	if done, ok := msg.(views.ActionDoneMsg); !ok || done.Message != "Question skipped" {
		t.Fatalf("message = %T %#v, want ActionDoneMsg", msg, msg)
	}

	gotQuestion := questionRepo.questions["q-skip"]
	if gotQuestion.Status != domain.QuestionAnswered {
		t.Fatalf("question status = %s, want %s", gotQuestion.Status, domain.QuestionAnswered)
	}
	if gotQuestion.Answer != "" || gotQuestion.AnsweredBy != "human" {
		t.Fatalf("skip answer = %q by %q, want empty answer by human", gotQuestion.Answer, gotQuestion.AnsweredBy)
	}
	if gotTask := taskRepo.tasks["plan-session"]; gotTask.Status != domain.AgentSessionRunning {
		t.Fatalf("task status = %s, want %s", gotTask.Status, domain.AgentSessionRunning)
	}
}

type cmdQuestionRepo struct {
	questions map[string]domain.Question
	bySession map[string][]string
}

func newCmdQuestionRepo() *cmdQuestionRepo {
	return &cmdQuestionRepo{questions: make(map[string]domain.Question), bySession: make(map[string][]string)}
}

func (r *cmdQuestionRepo) Get(_ context.Context, id string) (domain.Question, error) {
	q, ok := r.questions[id]
	if !ok {
		return domain.Question{}, repository.ErrNotFound
	}
	return q, nil
}

func (r *cmdQuestionRepo) ListBySessionID(_ context.Context, sessionID string) ([]domain.Question, error) {
	var questions []domain.Question
	for _, id := range r.bySession[sessionID] {
		questions = append(questions, r.questions[id])
	}
	return questions, nil
}

func (r *cmdQuestionRepo) Create(_ context.Context, q domain.Question) error {
	r.questions[q.ID] = q
	r.bySession[q.AgentSessionID] = append(r.bySession[q.AgentSessionID], q.ID)
	return nil
}

func (r *cmdQuestionRepo) Update(_ context.Context, q domain.Question) error {
	if _, ok := r.questions[q.ID]; !ok {
		return repository.ErrNotFound
	}
	r.questions[q.ID] = q
	return nil
}

func (r *cmdQuestionRepo) UpdateProposedAnswer(_ context.Context, id, proposedAnswer string) error {
	q, ok := r.questions[id]
	if !ok {
		return repository.ErrNotFound
	}
	q.ProposedAnswer = proposedAnswer
	r.questions[id] = q
	return nil
}

type cmdTaskRepo struct {
	tasks       map[string]domain.AgentSession
	byWorkItem  map[string][]string
	bySubPlan   map[string][]string
	byWorkspace map[string][]string
}

func newCmdTaskRepo() *cmdTaskRepo {
	return &cmdTaskRepo{
		tasks:       make(map[string]domain.AgentSession),
		byWorkItem:  make(map[string][]string),
		bySubPlan:   make(map[string][]string),
		byWorkspace: make(map[string][]string),
	}
}

func (r *cmdTaskRepo) Get(_ context.Context, id string) (domain.AgentSession, error) {
	task, ok := r.tasks[id]
	if !ok {
		return domain.AgentSession{}, repository.ErrNotFound
	}
	return task, nil
}

func (r *cmdTaskRepo) ListByWorkItemID(_ context.Context, workItemID string) ([]domain.AgentSession, error) {
	return r.list(r.byWorkItem[workItemID]), nil
}

func (r *cmdTaskRepo) ListBySubPlanID(_ context.Context, subPlanID string) ([]domain.AgentSession, error) {
	return r.list(r.bySubPlan[subPlanID]), nil
}

func (r *cmdTaskRepo) ListByWorkspaceID(_ context.Context, workspaceID string) ([]domain.AgentSession, error) {
	return r.list(r.byWorkspace[workspaceID]), nil
}

func (r *cmdTaskRepo) ListByOwnerInstanceID(_ context.Context, _ string) ([]domain.AgentSession, error) {
	return nil, nil
}

func (r *cmdTaskRepo) SearchHistory(_ context.Context, _ domain.SessionHistoryFilter) ([]domain.SessionHistoryEntry, error) {
	return nil, nil
}

func (r *cmdTaskRepo) Create(_ context.Context, task domain.AgentSession) error {
	r.tasks[task.ID] = task
	r.byWorkItem[task.WorkItemID] = append(r.byWorkItem[task.WorkItemID], task.ID)
	if task.SubPlanID != "" {
		r.bySubPlan[task.SubPlanID] = append(r.bySubPlan[task.SubPlanID], task.ID)
	}
	r.byWorkspace[task.WorkspaceID] = append(r.byWorkspace[task.WorkspaceID], task.ID)
	return nil
}

func (r *cmdTaskRepo) Update(_ context.Context, task domain.AgentSession) error {
	if _, ok := r.tasks[task.ID]; !ok {
		return repository.ErrNotFound
	}
	r.tasks[task.ID] = task
	return nil
}

func (r *cmdTaskRepo) Delete(_ context.Context, id string) error {
	if _, ok := r.tasks[id]; !ok {
		return repository.ErrNotFound
	}
	delete(r.tasks, id)
	return nil
}

func (r *cmdTaskRepo) list(ids []string) []domain.AgentSession {
	tasks := make([]domain.AgentSession, 0, len(ids))
	for _, id := range ids {
		tasks = append(tasks, r.tasks[id])
	}
	return tasks
}

// TestTailSessionLogCmd_Basic verifies that reading a freshly-written file from
// offset 0 returns all lines and advances NextOffset to the file size.
func TestTailSessionLogCmd_Basic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "sess1.log")
	content := "alpha\nbeta\ngamma\n"
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := views.TailSessionLogCmd(logPath, "sess1", 0)()
	got, ok := msg.(views.SessionLogLinesMsg)
	if !ok {
		t.Fatalf("expected SessionLogLinesMsg, got %T", msg)
	}
	if got.SessionID != "sess1" {
		t.Errorf("SessionID: want %q, got %q", "sess1", got.SessionID)
	}
	want := []string{"alpha", "beta", "gamma"}
	if len(got.Entries) != len(want) {
		t.Fatalf("Entries: want %v, got %v", want, got.Entries)
	}
	for i, w := range want {
		if got.Entries[i].Text != w {
			t.Errorf("Entries[%d]: want %q, got %q", i, w, got.Entries[i].Text)
		}
	}
	if got.NextOffset != int64(len(content)) {
		t.Errorf("NextOffset: want %d, got %d", len(content), got.NextOffset)
	}
}

// TestTailSessionLogCmd_OffsetContinuation verifies that supplying a non-zero
// since offset causes only the bytes after that offset to be returned.
func TestTailSessionLogCmd_OffsetContinuation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f, err := os.CreateTemp(dir, "session-*.log")
	if err != nil {
		t.Fatal(err)
	}
	firstLine := "first\n"
	if _, err := f.WriteString(firstLine); err != nil {
		t.Fatal(err)
	}
	// Record the offset after the first line.
	offset := int64(len(firstLine))

	secondLine := "second\n"
	if _, err := f.WriteString(secondLine); err != nil {
		t.Fatal(err)
	}
	f.Close()

	msg := views.TailSessionLogCmd(f.Name(), "s", offset)()
	got, ok := msg.(views.SessionLogLinesMsg)
	if !ok {
		t.Fatalf("expected SessionLogLinesMsg, got %T", msg)
	}
	if len(got.Entries) != 1 || got.Entries[0].Text != "second" {
		t.Errorf("Entries: want [\"second\"], got %v", got.Entries)
	}
	wantOff := offset + int64(len(secondLine))
	if got.NextOffset != wantOff {
		t.Errorf("NextOffset: want %d, got %d", wantOff, got.NextOffset)
	}
}

// TestTailSessionLogCmd_RotationDetected verifies that when the file is smaller
// than the stored offset (rotation), scanning restarts from byte 0.
func TestTailSessionLogCmd_RotationDetected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := dir + "/rotated.log"

	// Simulate pre-rotation: old file had 1000 bytes.
	staleOffset := int64(1000)

	// New file (post-rotation) is much smaller.
	newContent := "fresh line after rotation\n"
	if err := os.WriteFile(path, []byte(newContent), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := views.TailSessionLogCmd(path, "r", staleOffset)()
	got, ok := msg.(views.SessionLogLinesMsg)
	if !ok {
		t.Fatalf("expected SessionLogLinesMsg, got %T", msg)
	}
	if len(got.Entries) != 1 || got.Entries[0].Text != "fresh line after rotation" {
		t.Errorf("Entries: want rotation-fresh line, got %v", got.Entries)
	}
	if got.NextOffset != int64(len(newContent)) {
		t.Errorf("NextOffset after rotation: want %d, got %d", len(newContent), got.NextOffset)
	}
}

// TestTailSessionLogCmd_LargeLine verifies that lines larger than the old
// 64 KiB scanner default (now 1 MiB) are returned correctly.
func TestTailSessionLogCmd_LargeLine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "big.log")
	// 100 KiB line — would have failed with the default bufio.Scanner buffer.
	bigPayload := strings.Repeat("x", 100*1024)
	content := bigPayload + "\n"
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := views.TailSessionLogCmd(logPath, "big", 0)()
	got, ok := msg.(views.SessionLogLinesMsg)
	if !ok {
		t.Fatalf("expected SessionLogLinesMsg, got %T", msg)
	}
	if len(got.Entries) != 1 {
		t.Fatalf("Entries: want 1 entry, got %d", len(got.Entries))
	}
	if got.Entries[0].Text != bigPayload {
		t.Errorf("Entries[0]: length %d, want %d", len(got.Entries[0].Text), len(bigPayload))
	}
	if got.NextOffset != int64(len(content)) {
		t.Errorf("NextOffset: want %d, got %d", len(content), got.NextOffset)
	}
}

// TestTailSessionLogCmd_MissingFile verifies that a missing log file returns a
// no-op SessionLogLinesMsg (not an ErrMsg) so the tail loop stays alive.
func TestTailSessionLogCmd_MissingFile(t *testing.T) {
	t.Parallel()
	msg := views.TailSessionLogCmd("/nonexistent/path/session.log", "x", 0)()
	got, ok := msg.(views.SessionLogLinesMsg)
	if !ok {
		t.Fatalf("expected SessionLogLinesMsg on missing file, got %T", msg)
	}
	if len(got.Entries) != 0 {
		t.Errorf("Entries: want empty slice, got %v", got.Entries)
	}
}

func TestTailSessionLogCmd_NormalizesEventJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "json.log")
	content := strings.Join([]string{
		`{"type":"event","event":{"type":"input","input_kind":"prompt","text":"Begin planning"}}`,
		`{"type":"event","event":{"type":"assistant_output","text":"planning step"}}`,
		`{"type":"event","event":{"type":"tool_start","tool":"read","text":"{\"path\":\"AGENTS.md\"}","intent":"Reading guidance"}}`,
		`{"type":"event","event":{"type":"tool_output","tool":"read","text":"AGENTS contents"}}`,
		`{"type":"event","event":{"type":"tool_result","tool":"read","text":"done","is_error":false}}`,
		`{"type":"event","event":{"type":"question","question":"Need input","context":"missing token"}}`,
		"plain fallback line",
		"",
	}, "\n")
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := views.TailSessionLogCmd(logPath, "json", 0)()
	got, ok := msg.(views.SessionLogLinesMsg)
	if !ok {
		t.Fatalf("expected SessionLogLinesMsg, got %T", msg)
	}
	type wantEntry struct {
		kind      sessionlog.EntryKind
		inputKind string
		text      string
		tool      string
		intent    string
		question  string
		ctx       string
	}
	want := []wantEntry{
		{kind: sessionlog.KindInput, inputKind: "prompt", text: "Begin planning"},
		{kind: sessionlog.KindAssistant, text: "planning step"},
		{kind: sessionlog.KindToolStart, tool: "read", text: `{"path":"AGENTS.md"}`, intent: "Reading guidance"},
		{kind: sessionlog.KindToolOutput, tool: "read", text: "AGENTS contents"},
		{kind: sessionlog.KindToolResult, tool: "read", text: "done"},
		{kind: sessionlog.KindQuestion, question: "Need input", ctx: "missing token"},
		{kind: sessionlog.KindPlain, text: "plain fallback line"},
	}
	if len(got.Entries) != len(want) {
		t.Fatalf("Entries: want %d, got %d", len(want), len(got.Entries))
	}
	for i, w := range want {
		e := got.Entries[i]
		if e.Kind != w.kind {
			t.Errorf("Entries[%d].Kind: want %q, got %q", i, w.kind, e.Kind)
		}
		if e.Text != w.text {
			t.Errorf("Entries[%d].Text: want %q, got %q", i, w.text, e.Text)
		}
		if w.inputKind != "" && e.InputKind != w.inputKind {
			t.Errorf("Entries[%d].InputKind: want %q, got %q", i, w.inputKind, e.InputKind)
		}
		if w.tool != "" && e.Tool != w.tool {
			t.Errorf("Entries[%d].Tool: want %q, got %q", i, w.tool, e.Tool)
		}
		if w.intent != "" && e.Intent != w.intent {
			t.Errorf("Entries[%d].Intent: want %q, got %q", i, w.intent, e.Intent)
		}
		if w.question != "" && e.Question != w.question {
			t.Errorf("Entries[%d].Question: want %q, got %q", i, w.question, e.Question)
		}
		if w.ctx != "" && e.Context != w.ctx {
			t.Errorf("Entries[%d].Context: want %q, got %q", i, w.ctx, e.Context)
		}
	}
}

func TestTailSessionLogCmd_PreservesLegacyErrorAndCompleteEvents(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "legacy.log")
	content := strings.Join([]string{
		`{"type":"event","event":{"type":"error","message":"bridge crashed"}}`,
		`{"type":"event","event":{"type":"complete","summary":"Legacy completion summary"}}`,
		`{"type":"event","event":{"type":"complete"}}`,
		"",
	}, "\n")
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := views.TailSessionLogCmd(logPath, "legacy", 0)()
	got, ok := msg.(views.SessionLogLinesMsg)
	if !ok {
		t.Fatalf("expected SessionLogLinesMsg, got %T", msg)
	}
	if len(got.Entries) != 3 {
		t.Fatalf("Entries: want 3, got %d", len(got.Entries))
	}
	// "error" kind — legacy event
	if got.Entries[0].Kind != sessionlog.EntryKind("error") {
		t.Errorf("Entries[0].Kind: want %q, got %q", "error", got.Entries[0].Kind)
	}
	if got.Entries[0].Message != "bridge crashed" {
		t.Errorf("Entries[0].Message: want %q, got %q", "bridge crashed", got.Entries[0].Message)
	}
	// "complete" kind with summary — legacy event
	if got.Entries[1].Kind != sessionlog.EntryKind("complete") {
		t.Errorf("Entries[1].Kind: want %q, got %q", "complete", got.Entries[1].Kind)
	}
	if got.Entries[1].Summary != "Legacy completion summary" {
		t.Errorf("Entries[1].Summary: want %q, got %q", "Legacy completion summary", got.Entries[1].Summary)
	}
	// "complete" kind, empty summary — legacy event
	if got.Entries[2].Kind != sessionlog.EntryKind("complete") {
		t.Errorf("Entries[2].Kind: want %q, got %q", "complete", got.Entries[2].Kind)
	}
}

// TestTailSessionLogCmd_LoadsArchivedContentOnFirstCall verifies that when
// since==0, TailSessionLogCmd reads gzipped archives in addition to the active
// log so that sessions whose log has been rotated show their full history.
func TestTailSessionLogCmd_LoadsArchivedContentOnFirstCall(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const sessionID = "tail-archive"

	// Write two gzipped archive segments (sorted by name so older comes first).
	writeGZ := func(name, line string) {
		t.Helper()
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		gw := gzip.NewWriter(f)
		if _, err := gw.Write([]byte(line + "\n")); err != nil {
			t.Fatal(err)
		}
		if err := gw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	writeGZ(sessionID+".log.1000000000.gz", "archived line 1")
	writeGZ(sessionID+".log.2000000000.gz", "archived line 2")

	// Active log file with recent content.
	activePath := filepath.Join(dir, sessionID+".log")
	if err := os.WriteFile(activePath, []byte("live line\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := views.TailSessionLogCmd(activePath, sessionID, 0)()
	got, ok := msg.(views.SessionLogLinesMsg)
	if !ok {
		t.Fatalf("expected SessionLogLinesMsg, got %T", msg)
	}
	if got.SessionID != sessionID {
		t.Errorf("SessionID: want %q, got %q", sessionID, got.SessionID)
	}
	// All three lines (two archived + one live) must be present.
	if len(got.Entries) != 3 {
		t.Fatalf("Entries: want 3, got %d (%v)", len(got.Entries), got.Entries)
	}
	wantTexts := []string{"archived line 1", "archived line 2", "live line"}
	for i, want := range wantTexts {
		if got.Entries[i].Text != want {
			t.Errorf("Entries[%d].Text: want %q, got %q", i, want, got.Entries[i].Text)
		}
	}
	// NextOffset must equal the current active file size so the first
	// continuation poll reads only bytes written after this initial load.
	wantOffset := int64(len("live line\n"))
	if got.NextOffset != wantOffset {
		t.Errorf("NextOffset: want %d, got %d", wantOffset, got.NextOffset)
	}
}

func TestLoadSessionInteractionCmd_ReadsCompressedHistory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sessionID := "sess-history"
	compressedPath := filepath.Join(dir, sessionID+".log.20260308.gz")
	compressedFile, err := os.Create(compressedPath)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(compressedFile)
	compressedContent := `{"type":"event","event":{"type":"assistant_output","text":"first chunk"}}` + "\n" + `{"type":"event","event":{"type":"lifecycle","stage":"completed","summary":"done"}}` + "\n"
	if _, err := gz.Write([]byte(compressedContent)); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressedFile.Close(); err != nil {
		t.Fatal(err)
	}

	activePath := filepath.Join(dir, sessionID+".log")
	if err := os.WriteFile(activePath, []byte("live tail line\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := views.LoadSessionInteractionCmd(dir, sessionID)()
	got, ok := msg.(views.SessionInteractionLoadedMsg)
	if !ok {
		t.Fatalf("expected SessionInteractionLoadedMsg, got %T", msg)
	}
	if len(got.Entries) != 3 {
		t.Fatalf("Entries: want 3, got %d (%v)", 3, got.Entries)
	}
	if got.Entries[0].Kind != sessionlog.KindAssistant || got.Entries[0].Text != "first chunk" {
		t.Errorf("Entries[0]: want {KindAssistant, \"first chunk\"}, got %+v", got.Entries[0])
	}
	if got.Entries[1].Kind != sessionlog.KindLifecycle || got.Entries[1].Stage != "completed" || got.Entries[1].Summary != "done" {
		t.Errorf("Entries[1]: want {KindLifecycle, stage=completed, summary=done}, got %+v", got.Entries[1])
	}
	if got.Entries[2].Kind != sessionlog.KindPlain || got.Entries[2].Text != "live tail line" {
		t.Errorf("Entries[2]: want {KindPlain, \"live tail line\"}, got %+v", got.Entries[2])
	}
	if got.SessionID != sessionID {
		t.Fatalf("SessionID: want %q, got %q", sessionID, got.SessionID)
	}
}

func TestTailSessionLogCmd_LoadsFinalCompressedACPLog(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const sessionID = "acp-final"
	compressedPath := filepath.Join(dir, sessionID+".log.gz")
	compressedFile, err := os.Create(compressedPath)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(compressedFile)
	line := `2026-06-01T12:09:45.518652+02:00 in {"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"persisted ACP output"}}}}`
	if _, err := gz.Write([]byte(line + "\n")); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressedFile.Close(); err != nil {
		t.Fatal(err)
	}

	msg := views.TailSessionLogCmd(filepath.Join(dir, sessionID+".log"), sessionID, 0)()
	got, ok := msg.(views.SessionLogLinesMsg)
	if !ok {
		t.Fatalf("expected SessionLogLinesMsg, got %T", msg)
	}
	if len(got.Entries) != 1 {
		t.Fatalf("Entries: want 1, got %d (%v)", len(got.Entries), got.Entries)
	}
	if got.Entries[0].Kind != sessionlog.KindAssistant || got.Entries[0].Text != "persisted ACP output" {
		t.Fatalf("Entries[0] = %+v, want persisted ACP assistant output", got.Entries[0])
	}
}

func TestLoadSessionInteractionCmd_LoadsFinalCompressedACPLog(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const sessionID = "acp-history"
	compressedPath := filepath.Join(dir, sessionID+".log.gz")
	compressedFile, err := os.Create(compressedPath)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(compressedFile)
	line := `2026-06-01T12:09:45.518652+02:00 in {"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"historical ACP output"}}}}`
	if _, err := gz.Write([]byte(line + "\n")); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressedFile.Close(); err != nil {
		t.Fatal(err)
	}

	msg := views.LoadSessionInteractionCmd(dir, sessionID)()
	got, ok := msg.(views.SessionInteractionLoadedMsg)
	if !ok {
		t.Fatalf("expected SessionInteractionLoadedMsg, got %T", msg)
	}
	if len(got.Entries) != 1 {
		t.Fatalf("Entries: want 1, got %d (%v)", len(got.Entries), got.Entries)
	}
	if got.Entries[0].Kind != sessionlog.KindAssistant || got.Entries[0].Text != "historical ACP output" {
		t.Fatalf("Entries[0] = %+v, want historical ACP assistant output", got.Entries[0])
	}
}

// TestTailSessionLogCmd_ArchivesOnly_NoActiveLog verifies that when only gzipped
// rotations exist (no active .log file), TailSessionLogCmd returns a non-zero
// NextOffset so that subsequent polls enter the continuation path instead of
// re-triggering a full archive reload every cycle (which would cause unbounded
// entry growth and O(n²) rendering cost).
func TestTailSessionLogCmd_ArchivesOnly_NoActiveLog(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const sessionID = "tail-archives-only"

	writeGZ := func(name, line string) {
		t.Helper()
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		gw := gzip.NewWriter(f)
		if _, err := gw.Write([]byte(line + "\n")); err != nil {
			t.Fatal(err)
		}
		if err := gw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	writeGZ(sessionID+".log.1000000000.gz", "archived line 1")
	writeGZ(sessionID+".log.2000000000.gz", "archived line 2")

	// No active .log file exists — only the rotated archives above.
	activePath := filepath.Join(dir, sessionID+".log")
	msg := views.TailSessionLogCmd(activePath, sessionID, 0)()
	got, ok := msg.(views.SessionLogLinesMsg)
	if !ok {
		t.Fatalf("expected SessionLogLinesMsg, got %T", msg)
	}
	if got.SessionID != sessionID {
		t.Errorf("SessionID: want %q, got %q", sessionID, got.SessionID)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("Entries: want 2, got %d (%v)", len(got.Entries), got.Entries)
	}
	if got.Entries[0].Text != "archived line 1" {
		t.Errorf("Entries[0].Text: want %q, got %q", "archived line 1", got.Entries[0].Text)
	}
	if got.Entries[1].Text != "archived line 2" {
		t.Errorf("Entries[1].Text: want %q, got %q", "archived line 2", got.Entries[1].Text)
	}
	// Critical: NextOffset must be non-zero so that a subsequent
	// TailSessionLogCmd(logPath, sessionID, got.NextOffset) enters the
	// continuation path and does NOT re-read all archives.
	if got.NextOffset == 0 {
		t.Fatalf("NextOffset must be non-zero when active .log is missing (got 0); " +
			"this causes an infinite full-reload loop with unbounded entry growth")
	}

	// Second call with the returned offset should enter the continuation path,
	// NOT re-read archives. Since the active .log still doesn't exist, it should
	// return zero entries with the same non-zero offset.
	msg2 := views.TailSessionLogCmd(activePath, sessionID, got.NextOffset)()
	got2, ok := msg2.(views.SessionLogLinesMsg)
	if !ok {
		t.Fatalf("expected SessionLogLinesMsg on second call, got %T", msg2)
	}
	if len(got2.Entries) != 0 {
		t.Errorf("second call should return zero entries when .log is missing, got %d", len(got2.Entries))
	}
	if got2.NextOffset == 0 {
		t.Fatalf("second call NextOffset must be non-zero to prevent re-entering initial load path")
	}
}

// pagingRepoSource emits up to TotalPages pages, each with PageSize repos,
// signalling HasMore until the last page. It records every call.
type pagingRepoSource struct {
	name       string
	totalPages int
	pageSize   int
	calls      []adapter.RepoListOpts
}

func (p *pagingRepoSource) Name() string { return p.name }

func (p *pagingRepoSource) ListRepos(_ context.Context, opts adapter.RepoListOpts) (*adapter.RepoListResult, error) {
	p.calls = append(p.calls, opts)
	repos := make([]adapter.RepoItem, p.pageSize)
	for i := range repos {
		repos[i] = adapter.RepoItem{Name: "r", FullName: "o/r", Source: p.name}
	}
	return &adapter.RepoListResult{Repos: repos, HasMore: opts.Page < p.totalPages}, nil
}

// TestLoadReposCmd_AggregatesPagesUntilNoMore asserts that LoadReposCmd walks
// pagination, calling the source once per page until HasMore is false, and that
// the aggregated result contains every page's repos.
func TestLoadReposCmd_AggregatesPagesUntilNoMore(t *testing.T) {
	t.Parallel()
	src := &pagingRepoSource{name: "gitlab", totalPages: 3, pageSize: 100}
	cmd := views.LoadReposCmd([]adapter.RepoSource{src}, 0, "", 100, 5, 1, true)
	msg, ok := cmd().(views.RepoListLoadedMsg)
	if !ok {
		t.Fatalf("expected RepoListLoadedMsg, got %T", cmd())
	}
	if len(msg.Errs) != 0 {
		t.Fatalf("unexpected errs: %v", msg.Errs)
	}
	if len(src.calls) != 3 {
		t.Fatalf("ListRepos calls = %d, want 3", len(src.calls))
	}
	for i, c := range src.calls {
		if c.Page != i+1 {
			t.Errorf("call %d Page = %d, want %d", i, c.Page, i+1)
		}
		if c.Limit != 100 {
			t.Errorf("call %d Limit = %d, want 100", i, c.Limit)
		}
	}
	if len(msg.Repos) != 300 {
		t.Fatalf("aggregated repos = %d, want 300", len(msg.Repos))
	}
	if msg.HasMore {
		t.Errorf("HasMore = true, want false (source exhausted)")
	}
}

// TestLoadReposCmd_StopsAtMaxPages asserts the loader caps at maxPages even when
// the source still signals HasMore, and reports HasMore=true so the UI can hint
// that more results exist upstream.
func TestLoadReposCmd_StopsAtMaxPages(t *testing.T) {
	t.Parallel()
	src := &pagingRepoSource{name: "github", totalPages: 100, pageSize: 100}
	cmd := views.LoadReposCmd([]adapter.RepoSource{src}, 0, "", 100, 5, 1, false)
	msg, ok := cmd().(views.RepoListLoadedMsg)
	if !ok {
		t.Fatalf("expected RepoListLoadedMsg, got %T", cmd())
	}
	if len(src.calls) != 5 {
		t.Fatalf("ListRepos calls = %d, want 5 (maxPages cap)", len(src.calls))
	}
	if len(msg.Repos) != 500 {
		t.Fatalf("aggregated repos = %d, want 500", len(msg.Repos))
	}
	if !msg.HasMore {
		t.Error("HasMore = false, want true (source still has more after cap)")
	}
}
