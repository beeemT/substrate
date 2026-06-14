package config

import (
	"path/filepath"
	"testing"
)

func TestDaemonRegistryAddSwitchRemoveRemote(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	syncConfigRoots(cfg)
	if err := AddDaemonRegistryEntry(cfg, "staging", DaemonRegistryEntry{Address: "https://localhost:8443"}); err != nil {
		t.Fatalf("AddDaemonRegistryEntry() error = %v", err)
	}
	entry := cfg.TUI.Daemons["staging"]
	if entry.TokenRef != "keychain:daemon.staging.access_token" {
		t.Fatalf("token_ref = %q", entry.TokenRef)
	}
	if err := SwitchActiveDaemon(cfg, "staging"); err != nil {
		t.Fatalf("SwitchActiveDaemon() error = %v", err)
	}
	if cfg.TUI.ActiveDaemon != "staging" {
		t.Fatalf("active daemon = %q", cfg.TUI.ActiveDaemon)
	}
	if err := RemoveDaemonRegistryEntry(cfg, "staging"); err != nil {
		t.Fatalf("RemoveDaemonRegistryEntry() error = %v", err)
	}
	if cfg.TUI.ActiveDaemon != "local" {
		t.Fatalf("active daemon after remove = %q", cfg.TUI.ActiveDaemon)
	}
}

func TestDaemonRegistrySaveHelpersPersistNestedConfig(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	syncConfigRoots(cfg)
	path := filepath.Join(t.TempDir(), "config.yaml")

	if err := AddAndSaveDaemonRegistryEntry(path, cfg, "staging", DaemonRegistryEntry{Address: "https://localhost:8443"}); err != nil {
		t.Fatalf("AddAndSaveDaemonRegistryEntry() error = %v", err)
	}
	if err := SwitchAndSaveActiveDaemon(path, cfg, "staging"); err != nil {
		t.Fatalf("SwitchAndSaveActiveDaemon() error = %v", err)
	}
	saved, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if saved.TUI.ActiveDaemon != "staging" {
		t.Fatalf("saved active daemon = %q", saved.TUI.ActiveDaemon)
	}
	if saved.TUI.Daemons["staging"].TokenRef != "keychain:daemon.staging.access_token" {
		t.Fatalf("saved token ref = %q", saved.TUI.Daemons["staging"].TokenRef)
	}
	if err := RemoveAndSaveDaemonRegistryEntry(path, cfg, "staging"); err != nil {
		t.Fatalf("RemoveAndSaveDaemonRegistryEntry() error = %v", err)
	}
	saved, err = Load(path)
	if err != nil {
		t.Fatalf("Load(after remove) error = %v", err)
	}
	if _, ok := saved.TUI.Daemons["staging"]; ok {
		t.Fatal("removed daemon was persisted")
	}
}

func TestDaemonRegistryCannotRemoveLocal(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	syncConfigRoots(cfg)
	if err := RemoveDaemonRegistryEntry(cfg, "local"); err == nil {
		t.Fatal("RemoveDaemonRegistryEntry(local) succeeded, want error")
	}
}

func TestNormalizeConfigRoots_PreservesLegacyTopLevelWhenOnlyOneNestedSubsectionSet(t *testing.T) {
	// Mixing the legacy top-level `harness:` with a single nested
	// `daemon.foreman:` must not wipe the legacy harness section: only the
	// foreman subsection should be promoted to the flat form.
	cfg := &Config{
		Harness: HarnessConfig{Default: HarnessCodex},
		Daemon: DaemonConfig{
			Foreman: ForemanConfig{QuestionTimeout: "30s"},
		},
	}
	normalizeConfigRoots(cfg)
	if cfg.Harness.Default != HarnessCodex {
		t.Fatalf("legacy harness.default wiped: got %q, want %q", cfg.Harness.Default, HarnessCodex)
	}
	if cfg.Foreman.QuestionTimeout != "30s" {
		t.Fatalf("nested foreman.question_timeout not promoted: got %q", cfg.Foreman.QuestionTimeout)
	}
}

func TestNormalizeConfigRoots_PromotesOnlyExplicitlyConfiguredNestedSubsections(t *testing.T) {
	cfg := &Config{
		Commit:  CommitConfig{Strategy: CommitStrategyGranular},
		Harness: HarnessConfig{Default: HarnessCodex},
		Daemon: DaemonConfig{
			Commit: CommitConfig{Strategy: CommitStrategySingle},
		},
	}
	normalizeConfigRoots(cfg)
	if cfg.Commit.Strategy != CommitStrategySingle {
		t.Fatalf("commit.strategy not promoted from nested: got %q", cfg.Commit.Strategy)
	}
	if cfg.Harness.Default != HarnessCodex {
		t.Fatalf("harness.default wiped by partial nested merge: got %q", cfg.Harness.Default)
	}
}

func TestAddDaemonRegistryEntry_RejectsPlaintextRemoteAddress(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	syncConfigRoots(cfg)
	for _, addr := range []string{
		"http://substrate.example.com:443",
		"http://10.0.0.1:9000",
		"ftp://substrate.example.com:443",
	} {
		t.Run(addr, func(t *testing.T) {
			err := AddDaemonRegistryEntry(cfg, "staging-"+addr, DaemonRegistryEntry{Address: addr})
			if err == nil {
				t.Fatalf("AddDaemonRegistryEntry(%q) succeeded, want error", addr)
			}
		})
	}
}

func TestAddDaemonRegistryEntry_AllowsLocalAndUnixAndLoopback(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	syncConfigRoots(cfg)
	for _, addr := range []string{
		"unix:///tmp/substrate.sock",
		"http://127.0.0.1:8080",
		"http://localhost:8080",
		"http://[::1]:8080",
		"https://127.0.0.1:8443",
		"https://localhost:8443",
		"https://[::1]:8443",
	} {
		t.Run(addr, func(t *testing.T) {
			if err := AddDaemonRegistryEntry(cfg, "ok-"+addr, DaemonRegistryEntry{Address: addr}); err != nil {
				t.Fatalf("AddDaemonRegistryEntry(%q) error = %v", addr, err)
			}
		})
	}
}

func TestAddDaemonRegistryEntry_RejectsRemoteHTTPSUntilTLSWired(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	syncConfigRoots(cfg)
	for _, addr := range []string{
		"https://substrate.example.com:443",
		"https://10.0.0.1:8443",
	} {
		t.Run(addr, func(t *testing.T) {
			err := AddDaemonRegistryEntry(cfg, "remote-"+addr, DaemonRegistryEntry{Address: addr})
			if err == nil {
				t.Fatalf("AddDaemonRegistryEntry(%q) succeeded, want error", addr)
			}
		})
	}
}

func TestLoadRejectsInsecureRemoteDaemonAddress(t *testing.T) {
	path := writeTestConfig(t, `
tui:
  active_daemon: staging
  daemons:
    staging:
      kind: remote
      address: http://substrate.example.com:443
      token_ref: keychain:daemon.staging.access_token
`)
	if _, err := Load(path); err == nil {
		t.Fatal("Load() should error on plaintext remote daemon address")
	}
}

func TestAddDaemonRegistryEntry_RejectsPlaintextTokenRef(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	syncConfigRoots(cfg)
	for _, ref := range []string{
		"plain-secret-token",
		"Bearer abc123",
		"file:/etc/passwd",
		"env:DAEMON_TOKEN",
		"keychain:",
		"keychain:   ",
	} {
		t.Run(ref, func(t *testing.T) {
			err := AddDaemonRegistryEntry(cfg, "staging-"+ref, DaemonRegistryEntry{Address: "https://localhost:8443", TokenRef: ref})
			if err == nil {
				t.Fatalf("AddDaemonRegistryEntry(TokenRef=%q) succeeded, want error", ref)
			}
		})
	}
}

func TestLoadRejectsPlaintextDaemonTokenRef(t *testing.T) {
	path := writeTestConfig(t, `
tui:
  daemons:
    staging:
      kind: remote
      address: https://localhost:8443
      token_ref: "plaintext-token-value"
`)
	if _, err := Load(path); err == nil {
		t.Fatal("Load() should error on plaintext daemon token_ref")
	}
}

func TestAddDaemonRegistryEntry_DefaultsTokenRefToKeychain(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	syncConfigRoots(cfg)
	if err := AddDaemonRegistryEntry(cfg, "staging", DaemonRegistryEntry{Address: "https://localhost:8443"}); err != nil {
		t.Fatalf("AddDaemonRegistryEntry() error = %v", err)
	}
	if got := cfg.TUI.Daemons["staging"].TokenRef; got != "keychain:daemon.staging.access_token" {
		t.Fatalf("defaulted token_ref = %q", got)
	}
}
