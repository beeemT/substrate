package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/beeemT/substrate/internal/adapter"
)

const foremanSocketEnv = "SUBSTRATE_FOREMAN_SOCKET"

type pendingQuestion struct {
	id      string
	source  adapter.AgentQuestionSource
	options []permissionOption
	answer  chan string
}

type questionBroker struct {
	sessionID string
	mode      adapter.SessionMode
	emit      func(adapter.AgentEvent)
	mu        sync.Mutex
	next      int
	pending   map[string]*pendingQuestion
}

func newQuestionBroker(sessionID string, mode adapter.SessionMode, emit func(adapter.AgentEvent)) *questionBroker {
	return &questionBroker{sessionID: sessionID, mode: mode, emit: emit, pending: make(map[string]*pendingQuestion)}
}

func (b *questionBroker) ask(ctx context.Context, source adapter.AgentQuestionSource, text string, structured *adapter.StructuredQuestionSet, meta map[string]any, options []permissionOption) (string, error) {
	b.mu.Lock()
	b.next++
	id := fmt.Sprintf("acp_q_%d", b.next)
	pq := &pendingQuestion{id: id, source: source, options: options, answer: make(chan string, 1)}
	b.pending[id] = pq
	b.mu.Unlock()
	question := &adapter.AgentQuestion{ID: id, SessionID: b.sessionID, Stage: b.mode, Source: source, FreeText: text, Structured: structured, PendingAnswerHandle: id, Metadata: meta}
	b.emit(adapter.AgentEvent{Type: "question", Timestamp: time.Now(), Payload: text, Question: question, Metadata: meta})
	select {
	case <-ctx.Done():
		b.remove(id)
		return "", fmt.Errorf("question cancelled: %w", ctx.Err())
	case answer := <-pq.answer:
		b.remove(id)
		return answer, nil
	}
}

func (b *questionBroker) answer(answer string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, pq := range b.pending {
		select {
		case pq.answer <- answer:
			return nil
		default:
		}
	}
	return adapter.ErrSendAnswerNotSupported
}

func (b *questionBroker) cancelAll() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, pq := range b.pending {
		delete(b.pending, id)
		select {
		case pq.answer <- "":
		default:
		}
	}
}

func (b *questionBroker) remove(id string) {
	b.mu.Lock()
	delete(b.pending, id)
	b.mu.Unlock()
}

func (s *Session) handleClientRequest(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "session/request_permission":
		return s.handlePermissionRequest(ctx, params)
	case "fs/read_text_file":
		return s.handleReadTextFile(params)
	case "fs/write_text_file":
		return nil, s.handleWriteTextFile(params)
	case "terminal/create":
		var p terminalCreateParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("decode terminal/create params: %w", err)
		}
		return s.terminals.create(ctx, p)
	case "terminal/output":
		var p terminalIDParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("decode terminal/output params: %w", err)
		}
		return s.terminals.output(p.TerminalID)
	case "terminal/wait_for_exit":
		var p terminalIDParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("decode terminal/wait_for_exit params: %w", err)
		}
		return s.terminals.wait(ctx, p.TerminalID)
	case "terminal/kill":
		var p terminalIDParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("decode terminal/kill params: %w", err)
		}
		return nil, s.terminals.kill(p.TerminalID)
	case "terminal/release":
		var p terminalIDParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("decode terminal/release params: %w", err)
		}
		return nil, s.terminals.release(p.TerminalID)
	default:
		return nil, fmt.Errorf("unsupported acp client method %q", method)
	}
}

func (s *Session) handlePermissionRequest(ctx context.Context, raw json.RawMessage) (permissionResponse, error) {
	var p requestPermissionParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return permissionResponse{}, fmt.Errorf("decode permission params: %w", err)
	}
	questions := make([]adapter.StructuredQuestion, 0, 1)
	options := make([]adapter.QuestionOption, 0, len(p.Options))
	ids := make([]string, 0, len(p.Options))
	for _, opt := range p.Options {
		label := opt.Name
		if label == "" {
			label = opt.ID
		}
		options = append(options, adapter.QuestionOption{Label: label, Description: opt.Description})
		ids = append(ids, opt.ID)
	}
	questionText := p.Title
	if questionText == "" {
		questionText = "Agent requests permission to continue."
	}
	questions = append(questions, adapter.StructuredQuestion{ID: p.ToolCallID, Question: questionText, Options: options})
	structured := &adapter.StructuredQuestionSet{Questions: questions, SupportsCustomAnswer: false, NativeResponseFormat: "acp_permission_option_id"}
	meta := map[string]any{"tool_call_id": p.ToolCallID, "option_ids": ids}
	answer, err := s.questions.ask(ctx, adapter.AgentQuestionSourceFutureHarnessQuestion, questionText, structured, meta, p.Options)
	if err != nil || answer == "" {
		return permissionResponse{Outcome: "cancelled"}, nil
	}
	optionID := selectPermissionOption(answer, p.Options)
	if optionID == "" {
		return permissionResponse{Outcome: "cancelled"}, nil
	}
	return permissionResponse{Outcome: "selected", OptionID: optionID}, nil
}

func selectPermissionOption(answer string, options []permissionOption) string {
	answer = strings.TrimSpace(answer)
	for _, opt := range options {
		if answer == opt.ID || strings.EqualFold(answer, opt.Name) || strings.EqualFold(answer, opt.Description) {
			return opt.ID
		}
	}
	// No option matched. Since SupportsCustomAnswer is false, return empty
	// string so the caller returns {Outcome: "cancelled"}.
	return ""
}

func (s *Session) handleReadTextFile(raw json.RawMessage) (fsReadTextFileResponse, error) {
	var p fsReadTextFileParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return fsReadTextFileResponse{}, fmt.Errorf("decode fs/read_text_file params: %w", err)
	}
	if err := ensurePathInsideRoot(s.root, p.Path); err != nil {
		return fsReadTextFileResponse{}, err
	}
	data, err := os.ReadFile(p.Path)
	if err != nil {
		return fsReadTextFileResponse{}, fmt.Errorf("read text file: %w", err)
	}
	content := string(data)
	if p.Line > 0 || p.Limit > 0 {
		content = sliceLines(content, p.Line, p.Limit)
	}
	return fsReadTextFileResponse{Content: content}, nil
}

func (s *Session) handleWriteTextFile(raw json.RawMessage) error {
	var p fsWriteTextFileParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("decode fs/write_text_file params: %w", err)
	}
	// ensureWritePathInsideRoot validates the path is inside the sandbox and creates
	// parent directories as its last step, so no separate MkdirAll is needed.
	if err := ensureWritePathInsideRoot(s.root, p.Path); err != nil {
		return err
	}
	if err := os.WriteFile(p.Path, []byte(p.Content), 0o600); err != nil {
		return fmt.Errorf("write text file: %w", err)
	}
	return nil
}

func ensureWritePathInsideRoot(root, p string) error {
	if !filepath.IsAbs(p) {
		return fmt.Errorf("path %q is not absolute", p)
	}
	// Resolve the root's symlinks once. For the target path, we clean it and
	// walk each component to resolve symlinks along the way. This handles the case
	// where the file and its parent directories don't exist yet.
	rootEval, err := filepath.EvalSymlinks(root)
	if err != nil {
		rootEval = filepath.Clean(root)
	}
	// Clean and walk the target path to resolve any symlinks or .. components.
	// WalkComponent resolves symlinks for each existing directory component.
	pEval, err := filepath.EvalSymlinks(p)
	if err != nil {
		// File doesn't exist; walk the path components to resolve symlinks.
		pEval = filepath.Clean(p)
		pEval, err = walkPathResolveSymlinks(pEval)
		if err != nil {
			pEval = filepath.Clean(p)
		}
	}
	// Validate the resolved path is inside root.
	if !strings.HasPrefix(pEval+string(filepath.Separator), rootEval+string(filepath.Separator)) {
		return fmt.Errorf("path %q is outside sandbox root %q", p, root)
	}
	// Now safe to create parent directories.
	parent := filepath.Dir(p)
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return fmt.Errorf("create parent dir: %w", err)
	}
	return nil
}

// walkPathResolveSymlinks walks the path and resolves symlinks for each component.
// Returns the fully resolved path, or an error if any component can't be resolved.
func walkPathResolveSymlinks(p string) (string, error) {
	parts := strings.Split(filepath.Clean(p), string(filepath.Separator))
	var result string
	for i, part := range parts {
		if part == "" {
			if i == 0 {
				result = string(filepath.Separator)
			}
			continue
		}
		candidate := filepath.Join(result, part)
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			// Component doesn't exist yet; use the unresolved path for this and
			// all remaining components.
			return filepath.Join(result, filepath.Join(parts[i:]...)), nil
		}
		result = resolved
	}
	return result, nil
}

func sliceLines(content string, line, limit int) string {
	if line <= 0 {
		line = 1
	}
	if limit < 0 {
		limit = 0
	}
	parts := strings.SplitAfter(content, "\n")
	start := line - 1
	if start >= len(parts) {
		return ""
	}
	end := len(parts)
	if limit > 0 && start+limit < end {
		end = start + limit
	}
	return strings.Join(parts[start:end], "")
}

type foremanSocket struct {
	path     string
	listener net.Listener
	broker   *questionBroker
	done     chan struct{}
	once     sync.Once
}

func startForemanSocket(broker *questionBroker) (*foremanSocket, error) {
	dir, err := os.MkdirTemp("", "substrate-acp-foreman-*")
	if err != nil {
		return nil, fmt.Errorf("create foreman socket temp dir: %w", err)
	}
	path := filepath.Join(dir, "foreman.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		if rmErr := os.RemoveAll(dir); rmErr != nil {
			slog.Warn("acp: failed to remove foreman socket temp dir", "error", rmErr)
		}
		return nil, fmt.Errorf("listen foreman socket: %w", err)
	}
	fs := &foremanSocket{path: path, listener: ln, broker: broker, done: make(chan struct{})}
	go fs.serve()
	return fs, nil
}

func (s *foremanSocket) env() envVar { return envVar{Name: foremanSocketEnv, Value: s.path} }

func (s *foremanSocket) close() {
	s.once.Do(func() {
		if err := s.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			slog.Warn("acp: failed to close foreman socket", "error", err)
		}
		<-s.done
		if err := os.RemoveAll(filepath.Dir(s.path)); err != nil {
			slog.Warn("acp: failed to remove foreman socket", "error", err)
		}
	})
}

func (s *foremanSocket) serve() {
	defer close(s.done)
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			slog.Warn("acp: foreman socket accept failed", "error", err)
			continue
		}
		go s.handleConn(conn)
	}
}

func (s *foremanSocket) handleConn(conn net.Conn) {
	defer conn.Close()
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		slog.Warn("acp: read foreman question failed", "error", err)
		return
	}
	var req struct {
		Type     string `json:"type"`
		Question string `json:"question"`
		Context  string `json:"context"`
	}
	if err := json.Unmarshal([]byte(line), &req); err != nil {
		slog.Warn("acp: malformed foreman question", "error", err)
		return
	}
	if req.Question == "" {
		return
	}
	ctx := context.Background()
	answer, err := s.broker.ask(ctx, adapter.AgentQuestionSourceAskForeman, req.Question, nil, map[string]any{"context": req.Context}, nil)
	if err != nil {
		slog.Warn("acp: foreman question failed", "error", err)
		return
	}
	resp := map[string]string{"type": "answer", "answer": answer, "confidence": "high"}
	data, err := json.Marshal(resp)
	if err != nil {
		slog.Warn("acp: marshal foreman answer failed", "error", err)
		return
	}
	_, _ = conn.Write(append(data, '\n'))
}
