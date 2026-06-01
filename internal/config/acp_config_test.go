package config

import "testing"

func TestLoadACPHarnessConfig(t *testing.T) {
	path := writeTestConfig(t, `
harness:
  default: acp
adapters:
  acp:
    agent: cursor
    command: agent
    args: ["acp"]
    env:
      FOO: bar
    registry_id: cursor
    model: model-1
    mode: agent
    thought_level: high
    foreman_bridge_path: /tmp/foreman-mcp/index.ts
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Harness.Default != HarnessACP {
		t.Fatalf("harness.default = %q, want acp", cfg.Harness.Default)
	}
	if cfg.Adapters.ACP.Command != "agent" || len(cfg.Adapters.ACP.Args) != 1 || cfg.Adapters.ACP.Args[0] != "acp" {
		t.Fatalf("ACP command/args = %q %#v", cfg.Adapters.ACP.Command, cfg.Adapters.ACP.Args)
	}
	if cfg.Adapters.ACP.Agent != "cursor" {
		t.Fatalf("ACP agent = %q, want cursor", cfg.Adapters.ACP.Agent)
	}
	if cfg.Adapters.ACP.RegistryID != "cursor" {
		t.Fatalf("ACP registry_id = %q, want cursor", cfg.Adapters.ACP.RegistryID)
	}
	if cfg.Adapters.ACP.Model != "model-1" {
		t.Fatalf("ACP model = %q, want model-1", cfg.Adapters.ACP.Model)
	}
	if cfg.Adapters.ACP.Mode != "agent" {
		t.Fatalf("ACP mode = %q, want agent", cfg.Adapters.ACP.Mode)
	}
	if cfg.Adapters.ACP.ThoughtLevel != "high" {
		t.Fatalf("ACP thought_level = %q, want high", cfg.Adapters.ACP.ThoughtLevel)
	}
	if cfg.Adapters.ACP.Env == nil || cfg.Adapters.ACP.Env["FOO"] != "bar" {
		t.Fatalf("ACP env = %#v, want map[FOO:bar]", cfg.Adapters.ACP.Env)
	}
	if cfg.Adapters.ACP.ForemanBridgePath != "/tmp/foreman-mcp/index.ts" {
		t.Fatalf("ACP foreman_bridge_path = %q, want configured path", cfg.Adapters.ACP.ForemanBridgePath)
	}
	if cfg.Adapters.ACP.ClientFS == nil || !*cfg.Adapters.ACP.ClientFS || cfg.Adapters.ACP.ClientTerminal == nil || !*cfg.Adapters.ACP.ClientTerminal {
		t.Fatalf("ACP client capabilities defaulted to off; got fs=%v terminal=%v", cfg.Adapters.ACP.ClientFS, cfg.Adapters.ACP.ClientTerminal)
	}
}

func TestACPConfigDefaults(t *testing.T) {
	// Test that AuthTerminal defaults to true when loading config.
	path := writeTestConfig(t, `
harness:
  default: acp
adapters:
  acp:
    command: agent
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Adapters.ACP.AuthTerminal == nil || !*cfg.Adapters.ACP.AuthTerminal {
		t.Fatalf("ACP auth_terminal defaulted to false; want true")
	}
}

func TestACPConfigExplicitBooleans(t *testing.T) {
	path := writeTestConfig(t, `
harness:
  default: acp
adapters:
  acp:
    command: agent
    client_fs: false
    client_terminal: false
    auth_terminal: false
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Adapters.ACP.ClientFS == nil || *cfg.Adapters.ACP.ClientFS {
		t.Fatalf("ACP client_fs = %v, want false", cfg.Adapters.ACP.ClientFS)
	}
	if cfg.Adapters.ACP.ClientTerminal == nil || *cfg.Adapters.ACP.ClientTerminal {
		t.Fatalf("ACP client_terminal = %v, want false", cfg.Adapters.ACP.ClientTerminal)
	}
	if cfg.Adapters.ACP.AuthTerminal == nil || *cfg.Adapters.ACP.AuthTerminal {
		t.Fatalf("ACP auth_terminal = %v, want false", cfg.Adapters.ACP.AuthTerminal)
	}
}
