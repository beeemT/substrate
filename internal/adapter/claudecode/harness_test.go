package claudecode

import (
	"testing"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/config"
)

func TestHarnessNameAndCapabilities(t *testing.T) {
	h := NewHarness(config.ClaudeCodeConfig{})
	if got := h.Name(); got != "claude-code" {
		t.Fatalf("Name() = %q, want claude-code", got)
	}
	caps := h.Capabilities()
	if !caps.SupportsStreaming {
		t.Fatal("SupportsStreaming = false, want true")
	}
	if caps.SupportsMessaging {
		t.Fatal("SupportsMessaging = true, want false")
	}
}

func TestBuildArgsAgentAndForeman(t *testing.T) {
	h := NewHarness(config.ClaudeCodeConfig{Model: "sonnet", PermissionMode: "auto", MaxTurns: 4, MaxBudgetUSD: 1.5})
	args := h.buildArgs(adapter.SessionOpts{Mode: adapter.SessionModeAgent, WorktreePath: "/tmp", SystemPrompt: "sys", UserPrompt: "user"})
	joined := join(args)
	for _, want := range []string{"-p", "--output-format", "stream-json", "--model", "sonnet", "--permission-mode", "auto", "--max-turns", "4", "--max-budget-usd", "1.50"} {
		if !contains(args, want) {
			t.Fatalf("args missing %q: %s", want, joined)
		}
	}
	foreman := h.buildArgs(adapter.SessionOpts{Mode: adapter.SessionModeForeman, WorktreePath: "/tmp", UserPrompt: "review"})
	if !contains(foreman, "Read,Grep,Glob") {
		t.Fatalf("foreman args missing tool restriction: %v", foreman)
	}
}

func TestMapClaudeEvent(t *testing.T) {
	evt, ok := mapClaudeEvent(`{"type":"assistant","content":[{"text":"hello"}]}`)
	if !ok || evt.Type != "text_delta" || evt.Payload != "hello" {
		t.Fatalf("unexpected assistant event: %+v ok=%v", evt, ok)
	}
	evt, ok = mapClaudeEvent(`{"type":"result","result":"done"}`)
	if !ok || evt.Type != "done" {
		t.Fatalf("unexpected result event: %+v ok=%v", evt, ok)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func join(values []string) string {
	result := ""
	for i, value := range values {
		if i > 0 {
			result += " "
		}
		result += value
	}
	return result
}
