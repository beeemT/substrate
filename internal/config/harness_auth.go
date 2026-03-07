package config

import (
	"context"
	"fmt"
	"strings"

	"github.com/beeemT/substrate/internal/adapter"
)

func HarnessCredentialFields() map[string][]string {
	return map[string][]string{
		string(HarnessClaudeCode): {"binary_path", "model", "permission_mode", "max_turns", "max_budget_usd"},
		string(HarnessCodex):      {"binary_path", "model", "approval_mode", "full_auto", "quiet"},
		string(HarnessOhMyPi):     {"bun_path", "bridge_path", "thinking_level"},
	}
}

func RunHarnessAction(ctx context.Context, runner adapter.HarnessActionRunner, req adapter.HarnessActionRequest) (adapter.HarnessActionResult, error) {
	if runner == nil {
		return adapter.HarnessActionResult{}, fmt.Errorf("harness action runner is nil")
	}
	result, err := runner.RunAction(ctx, req)
	if err != nil {
		return adapter.HarnessActionResult{}, err
	}
	return result, nil
}

func HarnessFieldAllowed(harness, key string) bool {
	allowed, ok := HarnessCredentialFields()[strings.TrimSpace(harness)]
	if !ok {
		return false
	}
	for _, candidate := range allowed {
		if candidate == key {
			return true
		}
	}
	return false
}
