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

const (
	questionSocketEnv   = "SUBSTRATE_QUESTION_SOCKET"
	questionToolModeEnv = "SUBSTRATE_QUESTION_TOOL_MODE"
)

type pendingQuestion struct {
	id     string
	answer chan string
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

func (b *questionBroker) ask(ctx context.Context, source adapter.AgentQuestionSource, text string, structured *adapter.StructuredQuestionSet, meta map[string]any) (string, error) {
	b.mu.Lock()
	b.next++
	id := fmt.Sprintf("acp_q_%d", b.next)
	pq := &pendingQuestion{id: id, answer: make(chan string, 1)}
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
	select {
	case <-ctx.Done():
		return cancelledPermissionResponse(), nil
	default:
	}
	var p requestPermissionParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return permissionResponse{}, fmt.Errorf("decode permission params: %w", err)
	}
	optionID := autoAllowPermissionOptionID(p.Options)
	if optionID == "" {
		return cancelledPermissionResponse(), nil
	}
	return selectedPermissionResponse(optionID), nil
}

func cancelledPermissionResponse() permissionResponse {
	return permissionResponse{Outcome: permissionOutcome{Outcome: "cancelled"}}
}

func selectedPermissionResponse(optionID string) permissionResponse {
	return permissionResponse{Outcome: permissionOutcome{Outcome: "selected", OptionID: optionID}}
}

func autoAllowPermissionOptionID(options []permissionOption) string {
	if id := permissionOptionIDByKind(options, "allow_always"); id != "" {
		return id
	}
	if id := permissionOptionIDByKind(options, "allow_once"); id != "" {
		return id
	}
	for _, opt := range options {
		if isLegacyAllowPermissionOption(opt) {
			return opt.ID
		}
	}
	return ""
}

func permissionOptionIDByKind(options []permissionOption, kind string) string {
	for _, opt := range options {
		if opt.ID != "" && opt.Kind == kind {
			return opt.ID
		}
	}
	return ""
}

func isLegacyAllowPermissionOption(opt permissionOption) bool {
	if opt.ID == "" {
		return false
	}
	for _, value := range []string{opt.ID, opt.Name, opt.Description} {
		if isAllowPermissionLabel(value) {
			return true
		}
	}
	return false
}

func isAllowPermissionLabel(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "allow", "approve", "accept", "yes", "continue", "allow once", "allow always", "allow_once", "allow_always", "allow-once", "allow-always", "approved", "accepted":
		return true
	default:
		return false
	}
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
	// resolveWritePath validates the path is inside the sandbox and creates parent
	// directories as its last step. We resolve the path after validation to use the
	// resolved path for I/O (prevents TOCTOU).
	resolvedPath, err := resolveWritePath(s.root, p.Path)
	if err != nil {
		return err
	}
	if err := os.WriteFile(resolvedPath, []byte(p.Content), 0o600); err != nil {
		return fmt.Errorf("write text file: %w", err)
	}
	return nil
}

// resolveWritePath validates and resolves a write path to prevent TOCTOU.
// Returns the resolved absolute path suitable for I/O operations.
// Creates parent directories if they don't exist.
func resolveWritePath(root, p string) (string, error) {
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("path %q is not absolute", p)
	}
	rootEval, err := filepath.EvalSymlinks(root)
	if err != nil {
		rootEval = filepath.Clean(root)
	}
	pEval, err := filepath.EvalSymlinks(p)
	if err != nil {
		pEval = filepath.Clean(p)
		pEval, err = walkPathResolveSymlinks(pEval)
		if err != nil {
			pEval = filepath.Clean(p)
		}
	}
	if !strings.HasPrefix(pEval+string(filepath.Separator), rootEval+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside sandbox root %q", p, root)
	}
	// Create parent directories using the resolved path.
	parent := filepath.Dir(pEval)
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return "", fmt.Errorf("create parent dir: %w", err)
	}
	return pEval, nil
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

type questionSocket struct {
	path     string
	listener net.Listener
	broker   *questionBroker
	source   adapter.AgentQuestionSource
	done     chan struct{}
	once     sync.Once
}

func startQuestionSocket(broker *questionBroker, source adapter.AgentQuestionSource) (*questionSocket, error) {
	dir, err := os.MkdirTemp("", "substrate-acp-question-*")
	if err != nil {
		return nil, fmt.Errorf("create question socket temp dir: %w", err)
	}
	path := filepath.Join(dir, "question.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		if rmErr := os.RemoveAll(dir); rmErr != nil {
			slog.Warn("acp: failed to remove question socket temp dir", "error", rmErr)
		}
		return nil, fmt.Errorf("listen question socket: %w", err)
	}
	qs := &questionSocket{path: path, listener: ln, broker: broker, source: source, done: make(chan struct{})}
	go qs.serve()
	return qs, nil
}

func (s *questionSocket) env() envVar { return envVar{Name: questionSocketEnv, Value: s.path} }

func (s *questionSocket) close() {
	s.once.Do(func() {
		if err := s.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			slog.Warn("acp: failed to close question socket", "error", err)
		}
		<-s.done
		if err := os.RemoveAll(filepath.Dir(s.path)); err != nil {
			slog.Warn("acp: failed to remove question socket", "error", err)
		}
	})
}

func (s *questionSocket) serve() {
	defer close(s.done)
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			slog.Warn("acp: question socket accept failed", "error", err)
			continue
		}
		go s.handleConn(conn)
	}
}

func (s *questionSocket) handleConn(conn net.Conn) {
	defer conn.Close()
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		slog.Warn("acp: read question request failed", "error", err)
		return
	}
	var req struct {
		Type     string `json:"type"`
		Question string `json:"question"`
		Context  string `json:"context"`
	}
	if err := json.Unmarshal([]byte(line), &req); err != nil {
		slog.Warn("acp: malformed question request", "error", err)
		return
	}
	if req.Question == "" {
		return
	}
	// Use a timeout to prevent indefinite blocking if the question is never answered.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	answer, err := s.broker.ask(ctx, s.source, req.Question, nil, map[string]any{"context": req.Context})
	if err != nil {
		slog.Warn("acp: question request failed", "error", err)
		return
	}
	resp := map[string]string{"type": "answer", "answer": answer, "confidence": "high"}
	data, err := json.Marshal(resp)
	if err != nil {
		slog.Warn("acp: marshal question answer failed", "error", err)
		return
	}
	if _, err := conn.Write(append(data, '\n')); err != nil {
		slog.Warn("acp: write question answer failed", "error", err)
	}
}
