package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const rpcScannerMaxToken = 10 * 1024 * 1024

type (
	serverRequestHandler func(context.Context, string, json.RawMessage) (any, error)
	notificationHandler  func(string, json.RawMessage)
)

type rpcClient struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	writeMu   sync.Mutex
	pendingMu sync.Mutex
	pending   map[string]chan rpcMessage
	nextID    atomic.Int64
	done      chan struct{}
	closed    atomic.Bool
	closeOnce sync.Once
	closeErr  error

	handleRequest      serverRequestHandler
	handleNotification notificationHandler
	logLine            func(direction string, data []byte)
}

func newRPCClient(stdin io.WriteCloser, stdout io.ReadCloser, stderr io.ReadCloser, logLine func(string, []byte)) *rpcClient {
	return &rpcClient{
		stdin:   stdin,
		stdout:  stdout,
		stderr:  stderr,
		pending: make(map[string]chan rpcMessage),
		done:    make(chan struct{}),
		logLine: logLine,
	}
}

func (c *rpcClient) setHandlers(req serverRequestHandler, notif notificationHandler) {
	c.handleRequest = req
	c.handleNotification = notif
}

func (c *rpcClient) start() {
	go c.readStdout()
	go c.readStderr()
}

func (c *rpcClient) wait() <-chan struct{} { return c.done }

func (c *rpcClient) closeWithError(err error) {
	if err == nil {
		err = io.EOF
	}
	c.closeOnce.Do(func() {
		c.closeErr = err
		c.closed.Store(true) // prevent new writes after close
		c.pendingMu.Lock()
		for id, ch := range c.pending {
			delete(c.pending, id)
			ch <- rpcMessage{Error: &rpcError{Code: -32000, Message: err.Error()}}
			close(ch)
		}
		c.pendingMu.Unlock()
		close(c.done)
	})
}

func (c *rpcClient) Err() error {
	select {
	case <-c.done:
		return c.closeErr
	default:
		return nil
	}
}

func (c *rpcClient) Call(ctx context.Context, method string, params any, result any) error {
	id := c.nextID.Add(1)
	idRaw := json.RawMessage(strconv.FormatInt(id, 10))
	paramsRaw, err := marshalRaw(params)
	if err != nil {
		return fmt.Errorf("marshal %s params: %w", method, err)
	}
	msg := rpcMessage{JSONRPC: "2.0", ID: &idRaw, Method: method, Params: paramsRaw}
	respCh := make(chan rpcMessage, 1)
	idKey := string(idRaw)
	c.pendingMu.Lock()
	c.pending[idKey] = respCh
	c.pendingMu.Unlock()
	if err := c.writeMessage(msg); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, idKey)
		c.pendingMu.Unlock()
		return err
	}
	select {
	case <-ctx.Done():
		c.pendingMu.Lock()
		delete(c.pending, idKey)
		c.pendingMu.Unlock()
		return fmt.Errorf("%s cancelled: %w", method, ctx.Err())
	case resp := <-respCh:
		if resp.Error != nil {
			return fmt.Errorf("%s: %w", method, resp.Error)
		}
		if result != nil && len(resp.Result) > 0 && string(resp.Result) != "null" {
			if err := json.Unmarshal(resp.Result, result); err != nil {
				return fmt.Errorf("decode %s response: %w", method, err)
			}
		}
		return nil
	}
}

func (c *rpcClient) Notify(ctx context.Context, method string, params any) error {
	paramsRaw, err := marshalRaw(params)
	if err != nil {
		return fmt.Errorf("marshal %s params: %w", method, err)
	}
	msg := rpcMessage{JSONRPC: "2.0", Method: method, Params: paramsRaw}
	// Don't spawn a goroutine: writeMessage acquires writeMu and checks c.closed,
	// so cancellation races with write completion. If ctx is cancelled, the caller
	// can safely ignore the notification loss.
	return c.writeMessage(msg)
}

func (c *rpcClient) writeMessage(msg rpcMessage) error {
	if c.closed.Load() {
		return errors.New("acp rpc client closed")
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal json-rpc message: %w", err)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.logLine != nil {
		c.logLine("out", data)
	}
	if _, err := c.stdin.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write json-rpc message: %w", err)
	}
	return nil
}

func (c *rpcClient) readStdout() {
	scanner := bufio.NewScanner(c.stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), rpcScannerMaxToken)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		if c.logLine != nil {
			c.logLine("in", line)
		}
		var msg rpcMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			slog.Warn("acp: ignoring malformed json-rpc line", "error", err)
			continue
		}
		c.handleMessage(msg)
	}
	if err := scanner.Err(); err != nil {
		c.closeWithError(fmt.Errorf("read acp stdout: %w", err))
		return
	}
	c.closeWithError(io.EOF)
}

func (c *rpcClient) readStderr() {
	if c.stderr == nil {
		return
	}
	scanner := bufio.NewScanner(c.stderr)
	scanner.Buffer(make([]byte, 0, 64*1024), rpcScannerMaxToken)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		if c.logLine != nil {
			c.logLine("err", line)
		}
		slog.Debug("acp stderr", "line", string(line))
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, os.ErrClosed) || c.closed.Load() {
			slog.Debug("acp stderr reader stopped after close", "error", err)
			return
		}
		slog.Warn("acp stderr reader failed", "error", err)
	}
}

func (c *rpcClient) handleMessage(msg rpcMessage) {
	if msg.ID != nil && msg.Method == "" {
		idKey := string(*msg.ID)
		c.pendingMu.Lock()
		ch := c.pending[idKey]
		delete(c.pending, idKey)
		c.pendingMu.Unlock()
		if ch != nil {
			ch <- msg
			close(ch)
		} else {
			slog.Warn("acp: response for unknown request", "id", idKey)
		}
		return
	}
	if msg.Method == "" {
		slog.Warn("acp: ignoring json-rpc message without method or response id")
		return
	}
	if msg.ID != nil {
		go c.respondToServerRequest(msg, context.Background())
		return
	}
	if c.handleNotification != nil {
		c.handleNotification(msg.Method, msg.Params)
	}
}

func (c *rpcClient) respondToServerRequest(msg rpcMessage, ctx context.Context) {
	var result any
	var err error
	if c.handleRequest == nil {
		err = fmt.Errorf("unsupported client method %q", msg.Method)
	} else {
		result, err = c.handleRequest(ctx, msg.Method, msg.Params)
	}
	resp := rpcMessage{JSONRPC: "2.0", ID: msg.ID}
	if err != nil {
		slog.Warn("acp client method failed", "method", msg.Method, "error", err)
		resp.Error = &rpcError{Code: -32603, Message: err.Error()}
	} else {
		raw, marshalErr := marshalRaw(result)
		if marshalErr != nil {
			resp.Error = &rpcError{Code: -32603, Message: marshalErr.Error()}
		} else {
			resp.Result = raw
		}
	}
	if err := c.writeMessage(resp); err != nil {
		slog.Warn("acp: failed to write server request response", "method", msg.Method, "error", err)
	}
}

func marshalRaw(v any) (json.RawMessage, error) {
	if v == nil {
		return json.RawMessage("null"), nil
	}
	if raw, ok := v.(json.RawMessage); ok {
		if len(raw) == 0 {
			return json.RawMessage("null"), nil
		}
		return raw, nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

func timestampedLogLine(direction string, data []byte) []byte {
	prefix := time.Now().Format(time.RFC3339Nano) + " " + direction + " "
	out := make([]byte, 0, len(prefix)+len(data)+1)
	out = append(out, prefix...)
	out = append(out, data...)
	out = append(out, '\n')
	return out
}
