package orchestrator

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/beeemT/substrate/internal/adapter"
)

type registryMockSession struct {
	id       string
	sentMsgs []string
	mu       sync.Mutex
}

func (m *registryMockSession) ID() string { return m.id }
func (m *registryMockSession) Wait(_ context.Context) error {
	return nil
}
func (m *registryMockSession) Events() <-chan adapter.AgentEvent { return nil }
func (m *registryMockSession) SendMessage(_ context.Context, msg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sentMsgs = append(m.sentMsgs, msg)
	return nil
}
func (m *registryMockSession) Abort(_ context.Context) error { return nil }
func (m *registryMockSession) Steer(_ context.Context, msg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sentMsgs = append(m.sentMsgs, msg)
	return nil
}

func (m *registryMockSession) messages() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.sentMsgs))
	copy(out, m.sentMsgs)
	return out
}

func TestSessionRegistry_RegisterAndSend(t *testing.T) {
	reg := NewSessionRegistry()
	mock := &registryMockSession{id: "sess-1"}

	reg.Register("sess-1", mock)

	err := reg.SendMessage(context.Background(), "sess-1", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := mock.messages()
	if len(msgs) != 1 || msgs[0] != "hello" {
		t.Fatalf("expected [\"hello\"], got %v", msgs)
	}
}

func TestSessionRegistry_SendToUnregistered(t *testing.T) {
	reg := NewSessionRegistry()

	err := reg.SendMessage(context.Background(), "nonexistent", "hello")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err != ErrSessionNotRunning {
		t.Fatalf("expected ErrSessionNotRunning, got %v", err)
	}
}

func TestSessionRegistry_Deregister(t *testing.T) {
	reg := NewSessionRegistry()
	mock := &registryMockSession{id: "sess-1"}

	reg.Register("sess-1", mock)
	reg.Deregister("sess-1")

	err := reg.SendMessage(context.Background(), "sess-1", "hello")
	if err != ErrSessionNotRunning {
		t.Fatalf("expected ErrSessionNotRunning after deregister, got %v", err)
	}

	// Deregister non-existent should not panic.
	reg.Deregister("nonexistent")
}

func TestSessionRegistry_IsRunning(t *testing.T) {
	reg := NewSessionRegistry()
	mock := &registryMockSession{id: "sess-1"}

	if reg.IsRunning("sess-1") {
		t.Fatal("expected IsRunning=false before register")
	}

	reg.Register("sess-1", mock)
	if !reg.IsRunning("sess-1") {
		t.Fatal("expected IsRunning=true after register")
	}

	reg.Deregister("sess-1")
	if reg.IsRunning("sess-1") {
		t.Fatal("expected IsRunning=false after deregister")
	}
}

func TestSessionRegistry_DoubleRegister(t *testing.T) {
	reg := NewSessionRegistry()
	mock1 := &registryMockSession{id: "sess-1"}
	mock2 := &registryMockSession{id: "sess-1"}

	reg.Register("sess-1", mock1)
	reg.Register("sess-1", mock2)

	err := reg.SendMessage(context.Background(), "sess-1", "to-second")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if msgs := mock1.messages(); len(msgs) != 0 {
		t.Fatalf("first mock should not receive messages after re-register, got %v", msgs)
	}
	if msgs := mock2.messages(); len(msgs) != 1 || msgs[0] != "to-second" {
		t.Fatalf("second mock should receive message, got %v", msgs)
	}
}

func TestSessionRegistry_ConcurrentAccess(t *testing.T) {
	reg := NewSessionRegistry()
	const n = 50

	var wg sync.WaitGroup
	wg.Add(n)

	for i := range n {
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("sess-%d", i%10)
			mock := &registryMockSession{id: id}

			reg.Register(id, mock)
			_ = reg.SendMessage(context.Background(), id, "ping")
			reg.IsRunning(id)
			reg.Deregister(id)
			_ = reg.SendMessage(context.Background(), id, "after-deregister")
		}(i)
	}

	wg.Wait()
}
