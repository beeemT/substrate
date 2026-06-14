package config

import (
	"fmt"
	"strings"
)

// AddAndSaveDaemonRegistryEntry adds or replaces a daemon registry entry and
// persists the updated config to path.
func AddAndSaveDaemonRegistryEntry(path string, cfg *Config, name string, entry DaemonRegistryEntry) error {
	if err := AddDaemonRegistryEntry(cfg, name, entry); err != nil {
		return err
	}
	if err := Save(path, cfg); err != nil {
		return fmt.Errorf("save daemon registry entry: %w", err)
	}
	return nil
}

// validateDaemonTokenRef enforces that only secret-store references are
// stored in config files. Plaintext tokens would be persisted to disk by
// the YAML round-trip, so anything that is not an empty string (which gets
// defaulted to a keychain ref) or a `keychain:` reference is rejected.
func validateDaemonTokenRef(ref string) error {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return nil
	}
	if strings.HasPrefix(trimmed, "keychain:") {
		if strings.TrimSpace(strings.TrimPrefix(trimmed, "keychain:")) == "" {
			return fmt.Errorf("daemon token_ref %q is missing the keychain key", ref)
		}
		return nil
	}
	return fmt.Errorf("daemon token_ref must be empty or a keychain: reference; plaintext tokens cannot be stored in config (got %q)", ref)
}

func AddDaemonRegistryEntry(cfg *Config, name string, entry DaemonRegistryEntry) error {
	if cfg == nil {
		return fmt.Errorf("config is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("daemon name is required")
	}
	if name == "local" && !entry.AutoManaged {
		return fmt.Errorf("local daemon entry is auto-managed")
	}
	if strings.TrimSpace(entry.Kind) == "" {
		entry.Kind = "remote"
	}
	if err := validateDaemonTokenRef(entry.TokenRef); err != nil {
		return fmt.Errorf("daemon %q: %w", name, err)
	}
	if entry.Kind != "local" {
		address := strings.TrimSpace(entry.Address)
		if address == "" {
			return fmt.Errorf("daemon address is required")
		}
		if err := validateDaemonAddress(address); err != nil {
			return fmt.Errorf("daemon %q: %w", name, err)
		}
	}
	if strings.TrimSpace(entry.TokenRef) == "" {
		entry.TokenRef = "keychain:daemon." + name + ".access_token"
	}
	if strings.TrimSpace(entry.Label) == "" {
		entry.Label = name
	}
	if cfg.TUI.Daemons == nil {
		cfg.TUI.Daemons = map[string]DaemonRegistryEntry{}
	}
	cfg.TUI.Daemons[name] = entry
	return nil
}

// SwitchActiveDaemon switches the selected visualization daemon.
// SwitchAndSaveActiveDaemon switches the selected visualization daemon and
// persists the updated config to path.
func SwitchAndSaveActiveDaemon(path string, cfg *Config, name string) error {
	if err := SwitchActiveDaemon(cfg, name); err != nil {
		return err
	}
	if err := Save(path, cfg); err != nil {
		return fmt.Errorf("save active daemon: %w", err)
	}
	return nil
}

func SwitchActiveDaemon(cfg *Config, name string) error {
	if cfg == nil {
		return fmt.Errorf("config is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("daemon name is required")
	}
	if _, ok := cfg.TUI.Daemons[name]; !ok {
		return fmt.Errorf("daemon %q is not configured", name)
	}
	cfg.TUI.ActiveDaemon = name
	return nil
}

// RemoveDaemonRegistryEntry removes a user-managed remote daemon entry.
// RemoveAndSaveDaemonRegistryEntry removes a user-managed remote daemon entry
// and persists the updated config to path.
func RemoveAndSaveDaemonRegistryEntry(path string, cfg *Config, name string) error {
	if err := RemoveDaemonRegistryEntry(cfg, name); err != nil {
		return err
	}
	if err := Save(path, cfg); err != nil {
		return fmt.Errorf("save daemon registry removal: %w", err)
	}
	return nil
}

func RemoveDaemonRegistryEntry(cfg *Config, name string) error {
	if cfg == nil {
		return fmt.Errorf("config is required")
	}
	name = strings.TrimSpace(name)
	entry, ok := cfg.TUI.Daemons[name]
	if !ok {
		return fmt.Errorf("daemon %q is not configured", name)
	}
	if name == "local" || entry.AutoManaged {
		return fmt.Errorf("daemon %q is auto-managed and cannot be removed", name)
	}
	delete(cfg.TUI.Daemons, name)
	if cfg.TUI.ActiveDaemon == name {
		cfg.TUI.ActiveDaemon = "local"
	}
	return nil
}
