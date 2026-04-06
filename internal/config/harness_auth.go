package config

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/beeemT/substrate/internal/adapter"
)

func HarnessCredentialFields() map[string][]string {
	return map[string][]string{
		string(HarnessClaudeCode): {"bun_path", "bridge_path", "model", "thinking", "effort"},
		string(HarnessCodex):      {"binary_path", "model", "reasoning_effort"},
		string(HarnessOhMyPi):     {"bun_path", "bridge_path", "model", "thinking_level"},
		string(HarnessOpenCode): {"binary_path", "hostname", "port", "model", "agent", "variant"},
	}
}

func RunHarnessAction(ctx context.Context, runner adapter.HarnessActionRunner, req adapter.HarnessActionRequest) (adapter.HarnessActionResult, error) {
	if runner == nil {
		return adapter.HarnessActionResult{}, errors.New("harness action runner is nil")
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
	return slices.Contains(allowed, key)
}
