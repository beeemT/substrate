package acp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"unicode/utf8"
)

const defaultTerminalOutputLimit = 1024 * 1024

type terminalManager struct {
	root  string
	mu    sync.Mutex
	next  atomic.Int64
	terms map[string]*terminalProcess
}

type terminalProcess struct {
	id   string
	cmd  *exec.Cmd
	buf  terminalRing
	done chan struct{}
	mu   sync.Mutex
	exit *terminalExitStatus
	err  error
}

type terminalRing struct {
	buf       []byte
	limit     int
	truncated bool
}

func newTerminalManager(root string) *terminalManager {
	return &terminalManager{root: root, terms: make(map[string]*terminalProcess)}
}

func (m *terminalManager) create(ctx context.Context, params terminalCreateParams) (terminalCreateResponse, error) {
	if params.Command == "" {
		return terminalCreateResponse{}, errors.New("terminal command is required")
	}
	cwd := params.CWD
	if cwd == "" {
		cwd = m.root
	}
	if err := ensurePathInsideRoot(m.root, cwd); err != nil {
		return terminalCreateResponse{}, fmt.Errorf("terminal cwd rejected: %w", err)
	}
	limit := params.OutputByteLimit
	if limit <= 0 {
		limit = defaultTerminalOutputLimit
	}
	id := "term_" + strconv.FormatInt(m.next.Add(1), 10)
	cmd := exec.CommandContext(ctx, params.Command, params.Args...)
	cmd.Dir = cwd
	cmd.Env = append([]string{}, baseEnvironment(params.Env)...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return terminalCreateResponse{}, fmt.Errorf("create terminal stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return terminalCreateResponse{}, fmt.Errorf("create terminal stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return terminalCreateResponse{}, fmt.Errorf("start terminal command: %w", err)
	}
	term := &terminalProcess{id: id, cmd: cmd, done: make(chan struct{})}
	term.buf.limit = limit
	m.mu.Lock()
	m.terms[id] = term
	m.mu.Unlock()
	go term.copyOutput(stdout)
	go term.copyOutput(stderr)
	go term.wait()
	return terminalCreateResponse{TerminalID: id}, nil
}

func baseEnvironment(extra []envVar) []string {
	env := os.Environ()
	for _, item := range extra {
		if item.Name == "" {
			continue
		}
		env = append(env, item.Name+"="+item.Value)
	}
	return env
}

func (m *terminalManager) get(id string) (*terminalProcess, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	term := m.terms[id]
	if term == nil {
		return nil, fmt.Errorf("unknown terminal id %q", id)
	}
	return term, nil
}

func (m *terminalManager) output(id string) (terminalOutputResponse, error) {
	term, err := m.get(id)
	if err != nil {
		return terminalOutputResponse{}, err
	}
	term.mu.Lock()
	defer term.mu.Unlock()
	return terminalOutputResponse{Output: string(term.buf.buf), Truncated: term.buf.truncated, ExitStatus: term.exit}, nil
}

func (m *terminalManager) wait(ctx context.Context, id string) (terminalExitStatus, error) {
	term, err := m.get(id)
	if err != nil {
		return terminalExitStatus{}, err
	}
	select {
	case <-ctx.Done():
		return terminalExitStatus{}, fmt.Errorf("wait terminal: %w", ctx.Err())
	case <-term.done:
	}
	term.mu.Lock()
	defer term.mu.Unlock()
	if term.exit == nil {
		return terminalExitStatus{}, errors.New("terminal exited without status")
	}
	return *term.exit, nil
}

func (m *terminalManager) kill(id string) error {
	term, err := m.get(id)
	if err != nil {
		return err
	}
	return term.kill()
}

func (m *terminalManager) release(id string) error {
	term, err := m.get(id)
	if err != nil {
		return err
	}
	// Remove from map first to prevent leaks, then kill the process.
	m.mu.Lock()
	delete(m.terms, id)
	m.mu.Unlock()
	// Kill is best-effort; we already removed from the map.
	_ = term.kill()
	return nil
}

func (m *terminalManager) cleanup() {
	m.mu.Lock()
	terms := make([]*terminalProcess, 0, len(m.terms))
	for id, term := range m.terms {
		terms = append(terms, term)
		delete(m.terms, id)
	}
	m.mu.Unlock()
	for _, term := range terms {
		_ = term.kill()
	}
}

var errProcessDone = errors.New("process already done")

func (t *terminalProcess) kill() error {
	select {
	case <-t.done:
		return errProcessDone
	default:
	}
	if t.cmd.Process == nil {
		return nil
	}
	if err := t.cmd.Process.Kill(); err != nil {
		// Handle the TOCTOU race: process exited between the select above and Kill().
		if errors.Is(err, os.ErrProcessDone) {
			return errProcessDone
		}
		return fmt.Errorf("kill terminal process: %w", err)
	}
	return nil
}

func (t *terminalProcess) copyOutput(r io.Reader) {
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			t.mu.Lock()
			t.buf.write(buf[:n])
			t.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (t *terminalProcess) wait() {
	err := t.cmd.Wait()
	status := exitStatusFromError(err)
	t.mu.Lock()
	t.err = err
	t.exit = &status
	t.mu.Unlock()
	close(t.done)
}

func exitStatusFromError(err error) terminalExitStatus {
	if err == nil {
		code := 0
		return terminalExitStatus{ExitCode: &code}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code := exitErr.ExitCode()
		status := terminalExitStatus{ExitCode: &code}
		if runtime.GOOS != "windows" && code < 0 {
			msg := "signal"
			status.Signal = &msg
		}
		return status
	}
	msg := err.Error()
	return terminalExitStatus{Signal: &msg}
}

func (r *terminalRing) write(data []byte) {
	if r.limit <= 0 {
		return
	}
	r.buf = append(r.buf, data...)
	if len(r.buf) <= r.limit {
		return
	}
	r.truncated = true
	over := len(r.buf) - r.limit
	r.buf = r.buf[over:]
	for len(r.buf) > 0 && !utf8.Valid(r.buf) {
		_, size := utf8.DecodeRune(r.buf)
		if size <= 0 {
			break
		}
		r.buf = r.buf[size:]
	}
	if !utf8.Valid(r.buf) {
		r.buf = bytes.ToValidUTF8(r.buf, nil)
	}
}

func ensurePathInsideRoot(root, p string) error {
	if !filepath.IsAbs(p) {
		return fmt.Errorf("path %q is not absolute", p)
	}
	rootEval, err := filepath.EvalSymlinks(root)
	if err != nil {
		rootEval = filepath.Clean(root)
	}
	pathEval, err := filepath.EvalSymlinks(p)
	if err != nil {
		pathEval = filepath.Clean(p)
	}
	rel, err := filepath.Rel(rootEval, pathEval)
	if err != nil {
		return err
	}
	if rel == "." || (rel != "" && rel != ".." && !startsWithDotDot(rel)) {
		return nil
	}
	return fmt.Errorf("path %q escapes root %q", p, root)
}

func startsWithDotDot(rel string) bool {
	return rel == ".." || len(rel) > 3 && rel[:3] == ".."+string(filepath.Separator)
}
